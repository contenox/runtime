package modelcontract

import (
	_ "embed"
	"fmt"

	"github.com/contenox/runtime/runtime/internal/schemafile"
)

const CompletionFormatSchemaFile = "completion_formats.json"

//go:embed completion_formats.json
var completionFormatSchema []byte

type formatSchema struct {
	FIM      []fimFormatEntry      `json:"fim"`
	NextEdit []nextEditFormatEntry `json:"nextEdit"`
}

type fimFormatEntry struct {
	ID        string   `json:"id"`
	Operation string   `json:"operation"`
	Version   int      `json:"version"`
	Kind      string   `json:"kind"`
	Prefix    string   `json:"prefix"`
	Suffix    string   `json:"suffix"`
	Middle    string   `json:"middle"`
	Stops     []string `json:"stops"`
}

type nextEditFormatEntry struct {
	ID            string   `json:"id"`
	Operation     string   `json:"operation"`
	Version       int      `json:"version"`
	ContextTokens int      `json:"contextTokens"`
	MaxTokens     int      `json:"maxTokens"`
	Stops         []string `json:"stops"`
	TextStops     []string `json:"textStops"`
	Controls      []string `json:"controls"`
}

var declaredFormats = mustLoadFormatSchema(completionFormatSchema)

func mustLoadFormatSchema(data []byte) formatSchema {
	schema := schemafile.MustDecode[formatSchema](CompletionFormatSchemaFile, data)
	seen := make(map[string]string, len(schema.FIM)+len(schema.NextEdit))
	for _, entry := range schema.FIM {
		if err := checkFormatEntry(entry.ID, entry.Operation, "fim", entry.Version, seen); err != nil {
			panic(fmt.Sprintf("modelcontract: %s: %v", CompletionFormatSchemaFile, err))
		}
	}
	for _, entry := range schema.NextEdit {
		if err := checkFormatEntry(entry.ID, entry.Operation, "next-edit", entry.Version, seen); err != nil {
			panic(fmt.Sprintf("modelcontract: %s: %v", CompletionFormatSchemaFile, err))
		}
	}
	return schema
}

func checkFormatEntry(id, operation, list string, version int, seen map[string]string) error {
	if id == "" {
		return fmt.Errorf("a %s entry has no id", list)
	}
	if operation != list {
		return fmt.Errorf("format %q declares operation %q outside the %s list", id, operation, list)
	}
	if version <= 0 {
		return fmt.Errorf("format %q declares no version", id)
	}
	if previous, exists := seen[id]; exists {
		return fmt.Errorf("format %q is declared in both %s and %s", id, previous, list)
	}
	seen[id] = list
	return nil
}

func FIMFormats() []string {
	formats := make([]string, 0, len(declaredFormats.FIM))
	for _, entry := range declaredFormats.FIM {
		formats = append(formats, entry.ID)
	}
	return formats
}

func NextEditFormats() []string {
	formats := make([]string, 0, len(declaredFormats.NextEdit))
	for _, entry := range declaredFormats.NextEdit {
		formats = append(formats, entry.ID)
	}
	return formats
}

func TokenizerCompanionControls(operation, format string) (int, []string, error) {
	switch operation {
	case "fim":
		entry, err := declaredFormats.fimFormat(format)
		if err != nil {
			return 0, nil, err
		}
		if entry.Kind != "tokens" {
			return 0, nil, fmt.Errorf("FIM format %q requires its provider endpoint and has no tokenizer companion", format)
		}
		return entry.Version, entry.Stops, nil
	case "next-edit":
		entry, err := declaredFormats.nextEditFormat(format)
		if err != nil {
			return 0, nil, err
		}
		return entry.Version, entry.Controls, nil
	default:
		return 0, nil, fmt.Errorf("unsupported tokenizer companion operation %q", operation)
	}
}

func (s formatSchema) fimFormat(id string) (fimFormatEntry, error) {
	for _, entry := range s.FIM {
		if entry.ID == id {
			return entry, nil
		}
	}
	return fimFormatEntry{}, fmt.Errorf("unsupported native autocomplete format %q; select a model with a declared FIM contract", id)
}

func (s formatSchema) nextEditFormat(id string) (nextEditFormatEntry, error) {
	for _, entry := range s.NextEdit {
		if entry.ID == id {
			return entry, nil
		}
	}
	return nextEditFormatEntry{}, fmt.Errorf("unsupported next-edit contract %q; select a model with a declared edit profile", id)
}
