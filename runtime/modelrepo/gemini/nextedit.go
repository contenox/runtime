package gemini

import (
	"context"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func (p *GeminiProvider) CanNextEdit() bool { return false }

func (p *GeminiProvider) GetNextEditConnection(context.Context, string) (modelrepo.LLMNextEditClient, error) {
	return nil, modelrepo.ErrNextEditUnsupported
}
