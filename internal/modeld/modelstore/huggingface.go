package modelstore

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const huggingFaceOrigin = "https://huggingface.co"

type downloadTokenKey struct{}
type downloadOriginKey struct{}

// WithDownloadOrigin binds the desktop token to the explicitly selected repository origin.
func WithDownloadOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, downloadOriginKey{}, origin)
}

// WithDownloadToken supplies a read token for one Hugging Face download.
func WithDownloadToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, downloadTokenKey{}, token)
}

type AccessDenied struct {
	Status int
	Page   string
}

func (e *AccessDenied) Error() string {
	return fmt.Sprintf("Hugging Face access denied (HTTP %d): check repository access and accept any model license at %s; set HF_TOKEN to a token with read access", e.Status, e.Page)
}

func modelDownload(ctx context.Context, source string) (*http.Response, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("HF_ENDPOINT")), "/")
	if endpoint == "" {
		endpoint = huggingFaceOrigin
	}
	origin, explicit := ctx.Value(downloadOriginKey{}).(string)
	if explicit {
		endpoint = origin
	}
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("HF_ENDPOINT must be an HTTP or HTTPS base URL without credentials, query, or fragment")
	}
	target, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("invalid model download URL")
	}
	isHF := target.Scheme == "https" && target.Host == "huggingface.co"
	repoPath := target.Path
	if isHF && !explicit {
		target.Scheme = base.Scheme
		target.Host = base.Host
		target.Path = strings.TrimRight(base.Path, "/") + target.Path
		target.RawPath = ""
	}
	trusted := target.Scheme == base.Scheme && target.Host == base.Host && target.User == nil
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid model download request")
	}
	if trusted {
		token := strings.TrimSpace(os.Getenv("HF_TOKEN"))
		if supplied, ok := ctx.Value(downloadTokenKey{}).(string); ok {
			token = ""
			if base.Scheme == "https" && (explicit || (isHF && base.Host == "huggingface.co")) {
				token = supplied
			}
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := *http.DefaultClient
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if priorRedirect != nil {
			if err := priorRedirect(next, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return fmt.Errorf("too many model download redirects")
		}
		if !trusted || next.URL.Scheme != base.Scheme || next.URL.Host != base.Host {
			next.Header.Del("Authorization")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model download request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if (isHF || trusted) && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			repoPath = strings.TrimPrefix(strings.TrimPrefix(repoPath, "/"), "api/models/")
			parts := strings.Split(repoPath, "/")
			page := huggingFaceOrigin
			if explicit {
				page = base.Scheme + "://" + base.Host
			}
			if len(parts) >= 2 {
				page += "/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
			}
			return nil, &AccessDenied{Status: resp.StatusCode, Page: page}
		}
		return nil, fmt.Errorf("model download HTTP status %d", resp.StatusCode)
	}
	return resp, nil
}
