package libtracker

import (
	"encoding/json"
	"strings"
)

const redactedPlaceholder = "[REDACTED]"

const maxRedactDepth = 64

var defaultRedactedFields = []string{
	"password",
	"passwd",
	"passphrase",
	"secret",
	"credential",
	"authorization",
	"private_key",
	"privatekey",
	"api_key",
	"apikey",
	"access_key",
	"accesskey",
	"signing_key",
	"session_id",
	"sessionid",
	"jwt",
	"api_token",
	"auth_token",
	"access_token",
	"refresh_token",
	"id_token",
	"bearer",
	"token",
}

var tokenAccountingFields = []string{
	"tokens",
	"token_count",
	"tokenizer",
	"tokenized",
}

func DefaultRedactedFields() []string {
	out := make([]string, len(defaultRedactedFields))
	copy(out, defaultRedactedFields)
	return out
}

type fieldRedactor struct {
	fields []string
}

func newFieldRedactor(fields []string) *fieldRedactor {
	norm := make([]string, 0, len(fields))
	for _, f := range fields {
		if n := normalizeFieldName(f); n != "" {
			norm = append(norm, n)
		}
	}
	return &fieldRedactor{fields: norm}
}

func normalizeFieldName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func (r *fieldRedactor) sensitive(name string) bool {
	if r == nil {
		return false
	}
	n := normalizeFieldName(name)
	if n == "" {
		return false
	}
	for _, f := range r.fields {
		if !strings.Contains(n, f) {
			continue
		}
		if f == "token" && isTokenAccountingField(n) {
			continue
		}
		return true
	}
	return false
}

func isTokenAccountingField(normalized string) bool {
	for _, exempt := range tokenAccountingFields {
		if strings.Contains(normalized, exempt) {
			return true
		}
	}
	return false
}

func (r *fieldRedactor) redactValue(v any) (any, bool) {
	if r == nil || v == nil {
		return v, false
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v, false
	}
	return r.redactMarshaled(raw, v)
}

func (r *fieldRedactor) redactMarshaled(raw []byte, v any) (any, bool) {
	if r == nil {
		return v, false
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return v, false
	}
	out, changed := r.redactTree(tree, 0)
	if !changed {
		return v, false
	}
	return out, true
}

func (r *fieldRedactor) redactTree(v any, depth int) (any, bool) {
	if depth > maxRedactDepth {
		return redactedPlaceholder, true
	}
	switch node := v.(type) {
	case map[string]any:
		var changed bool
		out := make(map[string]any, len(node))
		for k, val := range node {
			if r.sensitive(k) {
				out[k] = redactedPlaceholder
				changed = true
				continue
			}
			nv, c := r.redactTree(val, depth+1)
			out[k] = nv
			changed = changed || c
		}
		if !changed {
			return v, false
		}
		return out, true
	case []any:
		var changed bool
		out := make([]any, len(node))
		for i, val := range node {
			nv, c := r.redactTree(val, depth+1)
			out[i] = nv
			changed = changed || c
		}
		if !changed {
			return v, false
		}
		return out, true
	default:
		return v, false
	}
}
