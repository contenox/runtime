package agentdecl

import (
	"encoding/json"
	"fmt"
	"strings"
)

type WriteAction string

const (
	ActionCreated   WriteAction = "created"
	ActionUpdated   WriteAction = "updated"
	ActionUnchanged WriteAction = "unchanged"
	ActionRefused   WriteAction = "refused"
	ActionIgnored   WriteAction = "ignored"
)

var ReservedNames = map[string]bool{
	"agent-planner": true,
}

func marshalWithSchema(v any, schemaURL string) ([]byte, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(body), "{\n") {
		return body, nil
	}
	head := fmt.Sprintf("{\n  \"$schema\": %q,\n", schemaURL)
	return append([]byte(head), body[2:]...), nil
}
