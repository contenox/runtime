package nextedit

import (
	"fmt"

	"github.com/contenox/runtime/runtime/modelcontract"
)

func ValidateLimit(format string, maxTokens int) error {
	profile, err := modelcontract.ResolveNextEdit(format)
	if err != nil {
		return err
	}
	if maxTokens > profile.MaxTokens {
		return fmt.Errorf("next edit output limit exceeds the %s profile maximum of %d tokens", format, profile.MaxTokens)
	}
	return nil
}

func Prepare(format string, input modelcontract.NextEditInput) (modelcontract.PreparedEdit, error) {
	prepared, err := modelcontract.PrepareNextEdit(format, input)
	if err != nil {
		return modelcontract.PreparedEdit{}, err
	}
	return prepared, nil
}
