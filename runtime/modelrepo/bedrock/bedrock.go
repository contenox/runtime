package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

func classifyBedrockError(err error) error {
	if err == nil {
		return nil
	}
	var throttled *types.ThrottlingException
	if errors.As(err, &throttled) {
		return fmt.Errorf("%w: %w", modelrepo.ErrRateLimited,
			modelrepo.ClassifyProviderError(err, 429, "ThrottlingException", throttled.ErrorMessage()))
	}
	var notReady *types.ModelNotReadyException
	if errors.As(err, &notReady) {
		return fmt.Errorf("%w: %w", modelrepo.ErrRateLimited,
			modelrepo.ClassifyProviderError(err, 429, "ModelNotReadyException", notReady.ErrorMessage()))
	}
	var validation *types.ValidationException
	if errors.As(err, &validation) {
		return modelrepo.ClassifyProviderError(err, 400, "ValidationException", validation.ErrorMessage())
	}
	var denied *types.AccessDeniedException
	if errors.As(err, &denied) {
		return modelrepo.ClassifyProviderError(err, 403, "AccessDeniedException", denied.ErrorMessage())
	}
	var missing *types.ResourceNotFoundException
	if errors.As(err, &missing) {
		return modelrepo.ClassifyProviderError(err, 404, "ResourceNotFoundException", missing.ErrorMessage())
	}
	var modelErr *types.ModelErrorException
	if errors.As(err, &modelErr) {
		return modelrepo.ClassifyProviderError(err, 424, "ModelErrorException", modelErr.ErrorMessage())
	}
	var streamErr *types.ModelStreamErrorException
	if errors.As(err, &streamErr) {
		return modelrepo.ClassifyProviderError(err, 424, "ModelStreamErrorException", streamErr.ErrorMessage())
	}
	var timeout *types.ModelTimeoutException
	if errors.As(err, &timeout) {
		return modelrepo.ClassifyProviderError(err, 408, "ModelTimeoutException", timeout.ErrorMessage())
	}
	var internal *types.InternalServerException
	if errors.As(err, &internal) {
		return modelrepo.ClassifyProviderError(err, 500, "InternalServerException", internal.ErrorMessage())
	}
	var unavailable *types.ServiceUnavailableException
	if errors.As(err, &unavailable) {
		return modelrepo.ClassifyProviderError(err, 503, "ServiceUnavailableException", unavailable.ErrorMessage())
	}
	return err
}

func bedrockBaseModelID(modelID string) string {
	for _, prefix := range []string{"us.", "eu.", "apac.", "jp.", "global."} {
		if strings.HasPrefix(modelID, prefix) {
			return strings.TrimPrefix(modelID, prefix)
		}
	}
	return modelID
}

type bedrockClient struct {
	api     *bedrockruntime.Client
	model   converseModel
	tracker libtracker.ActivityTracker
}

type staticCreds struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
}

func loadAWSConfig(ctx context.Context, region, credBlob string, httpClient *http.Client) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if httpClient != nil {
		opts = append(opts, awsconfig.WithHTTPClient(httpClient))
	}
	if strings.TrimSpace(credBlob) != "" {
		var c staticCreds
		if err := json.Unmarshal([]byte(credBlob), &c); err != nil {
			return aws.Config{}, fmt.Errorf("bedrock: parse stored credentials JSON: %w", err)
		}
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

func regionFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if !strings.Contains(raw, "/") && !strings.Contains(raw, ".") {
			return raw
		}
		return ""
	}
	host := u.Host
	parts := strings.Split(host, ".")
	for i, p := range parts {
		if (p == "bedrock-runtime" || p == "bedrock") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func chatConfigFromArgs(args []modelrepo.ChatArgument) *modelrepo.ChatConfig {
	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		if a == nil {
			continue
		}
		a.Apply(cfg)
	}
	return cfg
}

func documentToJSONString(doc document.Interface) string {
	if doc == nil {
		return "{}"
	}
	b, err := doc.MarshalSmithyDocument()
	if err != nil || len(b) == 0 {
		return "{}"
	}
	return string(b)
}

func jsonStringToDocument(args string) document.Interface {
	var v any
	if strings.TrimSpace(args) == "" {
		v = map[string]any{}
	} else if err := json.Unmarshal([]byte(args), &v); err != nil {
		v = map[string]any{}
	}
	return document.NewLazyDocument(v)
}

func documentString(doc document.Interface, key string) string {
	if doc == nil {
		return ""
	}
	var decoded map[string]any
	if err := doc.UnmarshalSmithyDocument(&decoded); err != nil {
		return ""
	}
	s, _ := decoded[key].(string)
	return s
}
