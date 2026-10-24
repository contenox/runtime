package bedrock

import (
	"context"

	"github.com/contenox/runtime/runtime/modelrepo"
)

func (p *bedrockProvider) CanNextEdit() bool { return false }

func (p *bedrockProvider) GetNextEditConnection(context.Context, string) (modelrepo.LLMNextEditClient, error) {
	return nil, modelrepo.ErrNextEditUnsupported
}
