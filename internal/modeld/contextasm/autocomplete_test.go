package contextasm_test

import (
	"errors"
	"testing"

	"github.com/contenox/runtime/internal/modeld/contextasm"
	"github.com/stretchr/testify/require"
)

func TestUnit_FIMTokenizationSeparatesControlFromLiteralSource(t *testing.T) {
	format, err := contextasm.ResolveFIM("qwen2.5-coder")
	require.NoError(t, err)
	controls := map[string]int{}
	for index, text := range format.Stops {
		controls[text] = 1000 + index
	}
	var literal []string
	encode := func(text string, special bool) ([]int, error) {
		if special {
			id, ok := controls[text]
			if !ok {
				return nil, errors.New("missing control")
			}
			return []int{id}, nil
		}
		literal = append(literal, text)
		return []int{len(text)}, nil
	}
	input := contextasm.AutocompleteInput{Format: "qwen2.5-coder", Prefix: "\tπ := \"<|fim_prefix|>\"\n", Suffix: "\r\n}\t"}
	tokens, stops, err := contextasm.TokenizeFIM(input, encode)
	require.NoError(t, err)
	require.Equal(t, []string{input.Prefix, input.Suffix}, literal)
	require.Equal(t, []int{controls[format.Prefix], len(input.Prefix), controls[format.Suffix], len(input.Suffix), controls[format.Middle]}, tokens)
	require.Contains(t, stops, controls[format.Middle])
	require.Contains(t, stops, controls["<|endoftext|>"])
	_, _, err = contextasm.TokenizeFIM(input, func(string, bool) ([]int, error) { return []int{1, 2}, nil })
	require.ErrorContains(t, err, "not one token")
	_, err = contextasm.ResolveFIM("guessed-from-model-name")
	require.Error(t, err)
}
