package modelcontract_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/stretchr/testify/require"
)

func TestUnit_TextDecoderPreservesBytesAcrossTokenBoundaries(t *testing.T) {
	input := "\tπ := \"🌳\"\r\n  "
	var decoder modelcontract.TextDecoder
	var output string
	for _, b := range []byte(input) {
		text, err := decoder.Push(string([]byte{b}), false)
		require.NoError(t, err)
		output += text
	}
	text, err := decoder.Push("", true)
	require.NoError(t, err)
	require.Equal(t, input, output+text)
	_, err = decoder.Push("\xff", false)
	require.ErrorContains(t, err, "invalid UTF-8")
	var unfinished modelcontract.TextDecoder
	_, err = unfinished.Push("\xe2", true)
	require.ErrorContains(t, err, "ended within")
}

func TestUnit_FIMContractsDistinguishEndpointOperations(t *testing.T) {
	for _, name := range []string{"qwen2.5-coder", "starcoder", "mistral-fim", "deepseek-fim"} {
		fingerprint, err := modelcontract.FIMFingerprint(name)
		require.NoError(t, err)
		require.Len(t, fingerprint, 64)
	}
	first, err := modelcontract.FIMFingerprint("mistral-fim")
	require.NoError(t, err)
	second, err := modelcontract.FIMFingerprint("deepseek-fim")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	_, _, err = modelcontract.TokenizeFIM(modelcontract.AutocompleteInput{Format: "mistral-fim"}, func(string, bool) ([]int, error) {
		t.Fatal("endpoint contract must not be tokenized locally")
		return nil, nil
	})
	require.ErrorContains(t, err, "provider endpoint")
}

func TestUnit_TextDecoderStopsAcrossTokenPieces(t *testing.T) {
	for _, chunks := range [][]string{{"π", " </", "s", ">ignored"}, {"π </s>ignored"}} {
		decoder := modelcontract.TextDecoder{Stops: []string{"</s>"}}
		var output string
		for _, piece := range chunks {
			text, err := decoder.Push(piece, false)
			require.NoError(t, err)
			output += text
		}
		require.True(t, decoder.Stopped)
		require.Equal(t, "π ", output)
		text, err := decoder.Push("more", true)
		require.NoError(t, err)
		require.Empty(t, text)
	}
	decoder := modelcontract.TextDecoder{Stops: []string{"</s>"}}
	text, err := decoder.Push("a </", false)
	require.NoError(t, err)
	require.Equal(t, "a ", text)
	text, err = decoder.Push("", true)
	require.NoError(t, err)
	require.Equal(t, "</", text)
	require.False(t, decoder.Stopped)
}
