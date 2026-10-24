package tools

import (
	"context"
	"net/http"

	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/getkin/kin-openapi/openapi3"
)

type ToolProtocol interface {
	FetchSchema(ctx context.Context, endpointURL string, httpClient *http.Client) (*openapi3.T, error)
	FetchTools(ctx context.Context, endpointURL string, injectParams map[string]ParamArg, httpClient *http.Client) ([]taskengine.Tool, error)
	ExecuteTool(
		ctx context.Context,
		endpointURL string,
		httpClient *http.Client,
		injectParams map[string]ParamArg,
		toolCall taskengine.ToolCall,
	) (interface{}, taskengine.DataType, error)
}
