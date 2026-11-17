package modelstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type downloadRoundTrip func(*http.Request) (*http.Response, error)

func (f downloadRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadTokenIsRequestScopedAndStrippedOnRedirect(t *testing.T) {
	t.Setenv("HF_ENDPOINT", "")
	t.Setenv("HF_TOKEN", "cli-token")
	original := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = original })
	http.DefaultClient = &http.Client{Transport: downloadRoundTrip(func(r *http.Request) (*http.Response, error) {
		res := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}
		switch r.URL.Path {
		case "/owner/model/resolve/main/file":
			if r.Header.Get("Authorization") != "Bearer desktop-token" {
				t.Fatalf("incorrect download credential")
			}
			res.StatusCode = 302
			res.Header.Set("Location", "https://cdn.example.com/blob")
		case "/blob":
			if r.Header.Get("Authorization") != "" {
				t.Fatal("credential reached a different origin")
			}
		case "/owner/gated/resolve/main/file":
			if r.Header.Get("Authorization") != "Bearer cli-token" {
				t.Fatal("desktop credential outlived the download")
			}
			res.StatusCode = 403
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return res, nil
	})}
	ctx := WithDownloadToken(context.Background(), "desktop-token")
	resp, err := modelDownload(ctx, "https://huggingface.co/owner/model/resolve/main/file")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, err = modelDownload(context.Background(), "https://huggingface.co/owner/gated/resolve/main/file")
	var denied *AccessDenied
	if !errors.As(err, &denied) || denied.Page != "https://huggingface.co/owner/gated" {
		t.Fatalf("unexpected access failure: %v", err)
	}
}
