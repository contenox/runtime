package modelcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

type AutocompleteInput struct {
	Format string `json:"format"`
	Prefix string `json:"prefix"`
	Suffix string `json:"suffix"`
}

type FIMFormat struct {
	ID      string
	Version int
	Kind    string
	Prefix  string
	Suffix  string
	Middle  string
	Stops   []string
}

func ResolveFIM(name string) (FIMFormat, error) {
	entry, err := declaredFormats.fimFormat(name)
	if err != nil {
		return FIMFormat{}, err
	}
	return FIMFormat{ID: entry.ID, Version: entry.Version, Kind: entry.Kind, Prefix: entry.Prefix, Suffix: entry.Suffix, Middle: entry.Middle, Stops: entry.Stops}, nil
}

func FIMFingerprint(name string) (string, error) {
	format, err := ResolveFIM(name)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(format)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (f FIMFormat) Render(prefix, suffix string) string {
	return f.Prefix + prefix + f.Suffix + suffix + f.Middle
}

func TokenizeFIM(input AutocompleteInput, tokenize func(string, bool) ([]int, error)) ([]int, []int, error) {
	if tokenize == nil {
		return nil, nil, errors.New("autocomplete requires a tokenizer")
	}
	format, err := ResolveFIM(input.Format)
	if err != nil {
		return nil, nil, err
	}
	if format.Kind != "tokens" {
		return nil, nil, fmt.Errorf("FIM contract %q requires its provider endpoint", input.Format)
	}
	control := func(text string) (int, error) {
		ids, err := tokenize(text, true)
		if err != nil {
			return 0, err
		}
		if len(ids) != 1 {
			return 0, fmt.Errorf("autocomplete control %q is not one token in this model's vocabulary", text)
		}
		return ids[0], nil
	}
	var tokens []int
	for _, part := range []struct {
		text    string
		control bool
	}{{format.Prefix, true}, {input.Prefix, false}, {format.Suffix, true}, {input.Suffix, false}, {format.Middle, true}} {
		if part.control {
			id, err := control(part.text)
			if err != nil {
				return nil, nil, err
			}
			tokens = append(tokens, id)
		} else {
			ids, err := tokenize(part.text, false)
			if err != nil {
				return nil, nil, err
			}
			tokens = append(tokens, ids...)
		}
	}
	stops := make([]int, 0, len(format.Stops))
	for _, text := range format.Stops {
		id, err := control(text)
		if err != nil {
			return nil, nil, err
		}
		stops = append(stops, id)
	}
	return tokens, stops, nil
}
