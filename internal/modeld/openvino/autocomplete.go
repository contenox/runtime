package openvino

import (
	"context"

	"github.com/contenox/runtime/internal/modeld/contextasm"
	"github.com/contenox/runtime/internal/modeld/openvino/ovsession"
)

func (s *genaiSession) tokenizeCodePart(ctx context.Context, text string, control bool, kind, format string) ([]int, error) {
	mode := ovsession.TokenizeLiteralSource
	if control {
		mode = ovsession.TokenizeControl
	}
	return s.backend.TokenizeMode(ctx, text, false, mode)
}

func (s *genaiSession) tokenizeFIM(ctx context.Context, text string, special bool, name string) ([]int, error) {
	format, err := contextasm.ResolveFIM(name)
	if err != nil {
		return nil, err
	}
	if err := s.backend.ValidateLiteralSourceTokenizer("fim", format.ID); err != nil {
		return nil, err
	}
	return s.tokenizeCodePart(ctx, text, special, "fim", format.ID)
}
