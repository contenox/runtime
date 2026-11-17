package contextasm

import "github.com/contenox/runtime/runtime/modelcontract"

// AutocompleteInput preserves the source on both sides of the insertion point.
type AutocompleteInput = modelcontract.AutocompleteInput

// FIMFormat identifies the versioned native completion contract.
type FIMFormat = modelcontract.FIMFormat

// ResolveFIM returns the explicit model contract independently of model names.
func ResolveFIM(name string) (FIMFormat, error) { return modelcontract.ResolveFIM(name) }

// TokenizeFIM preserves literal source while resolving model control tokens.
func TokenizeFIM(input AutocompleteInput, tokenize func(string, bool) ([]int, error)) ([]int, []int, error) {
	return modelcontract.TokenizeFIM(input, tokenize)
}
