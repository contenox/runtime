package modelcontract_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
)

type declaredFormatSchema struct {
	FIM []struct {
		ID        string   `json:"id"`
		Operation string   `json:"operation"`
		Version   int      `json:"version"`
		Kind      string   `json:"kind"`
		Prefix    string   `json:"prefix"`
		Suffix    string   `json:"suffix"`
		Middle    string   `json:"middle"`
		Stops     []string `json:"stops"`
	} `json:"fim"`
	NextEdit []struct {
		ID            string   `json:"id"`
		Operation     string   `json:"operation"`
		Version       int      `json:"version"`
		ContextTokens int      `json:"contextTokens"`
		MaxTokens     int      `json:"maxTokens"`
		Stops         []string `json:"stops"`
		TextStops     []string `json:"textStops"`
		Controls      []string `json:"controls"`
	} `json:"nextEdit"`
}

func readDeclaredSchema(t *testing.T) declaredFormatSchema {
	t.Helper()
	data, err := os.ReadFile(modelcontract.CompletionFormatSchemaFile)
	if err != nil {
		t.Fatalf("read the format schema: %v", err)
	}
	var schema declaredFormatSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode the format schema: %v", err)
	}
	return schema
}

func TestUnit_ResolversAreLoadersOverTheCompletionFormatSchema(t *testing.T) {
	schema := readDeclaredSchema(t)
	if len(schema.FIM) == 0 || len(schema.NextEdit) == 0 {
		t.Fatal("the format schema declares no formats")
	}
	for _, entry := range schema.FIM {
		resolved, err := modelcontract.ResolveFIM(entry.ID)
		if err != nil {
			t.Fatalf("ResolveFIM(%q): %v", entry.ID, err)
		}
		declared := modelcontract.FIMFormat{ID: entry.ID, Version: entry.Version, Kind: entry.Kind,
			Prefix: entry.Prefix, Suffix: entry.Suffix, Middle: entry.Middle, Stops: entry.Stops}
		if !reflect.DeepEqual(resolved, declared) {
			t.Fatalf("ResolveFIM(%q) = %#v, schema declares %#v", entry.ID, resolved, declared)
		}
		if _, err := modelcontract.FIMFingerprint(entry.ID); err != nil {
			t.Fatalf("FIMFingerprint(%q): %v", entry.ID, err)
		}
	}
	for _, entry := range schema.NextEdit {
		resolved, err := modelcontract.ResolveNextEdit(entry.ID)
		if err != nil {
			t.Fatalf("ResolveNextEdit(%q): %v", entry.ID, err)
		}
		declared := modelcontract.NextEditProfile{ID: entry.ID, Version: entry.Version, ContextTokens: entry.ContextTokens,
			MaxTokens: entry.MaxTokens, Stops: entry.Stops, TextStops: entry.TextStops}
		if !reflect.DeepEqual(resolved, declared) {
			t.Fatalf("ResolveNextEdit(%q) = %#v, schema declares %#v", entry.ID, resolved, declared)
		}
		if _, err := modelcontract.NextEditFingerprint(entry.ID); err != nil {
			t.Fatalf("NextEditFingerprint(%q): %v", entry.ID, err)
		}
	}
	if _, err := modelcontract.ResolveFIM("declared-nowhere"); err == nil {
		t.Fatal("a name the schema does not declare resolved to a FIM format")
	}
	if _, err := modelcontract.ResolveNextEdit("declared-nowhere"); err == nil {
		t.Fatal("a format the schema does not declare resolved to a next-edit profile")
	}
}

func TestUnit_NextEditControlTokensAreTheDeclaredPromptControls(t *testing.T) {
	schema := readDeclaredSchema(t)
	for _, entry := range schema.NextEdit {
		version, controls, err := modelcontract.TokenizerCompanionControls("next-edit", entry.ID)
		if err != nil {
			t.Fatalf("TokenizerCompanionControls(next-edit, %q): %v", entry.ID, err)
		}
		if version != entry.Version {
			t.Fatalf("next-edit format %q declares version %d, the companion contract reports %d", entry.ID, entry.Version, version)
		}
		prepared, err := modelcontract.PrepareNextEdit(entry.ID, modelcontract.NextEditInput{Path: "source"})
		if err != nil {
			t.Fatalf("PrepareNextEdit(%q): %v", entry.ID, err)
		}
		var expected []string
		for _, part := range prepared.Parts {
			if part.Control {
				expected = appendUniqueText(expected, part.Text)
			}
		}
		for _, stop := range prepared.Stops {
			expected = appendUniqueText(expected, stop)
		}
		if !reflect.DeepEqual(controls, expected) {
			t.Fatalf("next-edit format %q companion controls %#v do not match the prompt controls %#v", entry.ID, controls, expected)
		}
	}
}

func appendUniqueText(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
