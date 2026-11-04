package agentdecl

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

type tomlBlock struct {
	path   []string
	start  int
	header int
	end    int
}

type tomlLine struct {
	text  string
	start int
	end   int
}

func splitTomlLines(raw string) []tomlLine {
	var lines []tomlLine
	pos := 0
	for pos < len(raw) {
		next := strings.IndexByte(raw[pos:], '\n')
		var end int
		if next < 0 {
			end = len(raw)
		} else {
			end = pos + next + 1
		}
		lines = append(lines, tomlLine{text: strings.TrimRight(raw[pos:end], "\r\n"), start: pos, end: end})
		pos = end
	}
	return lines
}

type tomlScanState struct {
	depth     int
	multiline string
}

func (s *tomlScanState) feed(line string) {
	i := 0
	for i < len(line) {
		if s.multiline != "" {
			idx := strings.Index(line[i:], s.multiline)
			if idx < 0 {
				return
			}
			i += idx + len(s.multiline)
			s.multiline = ""
			continue
		}
		c := line[i]
		switch {
		case c == '#':
			return
		case strings.HasPrefix(line[i:], `"""`):
			s.multiline = `"""`
			i += 3
		case strings.HasPrefix(line[i:], `'''`):
			s.multiline = `'''`
			i += 3
		case c == '"':
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case c == '\'':
			i++
			for i < len(line) && line[i] != '\'' {
				i++
			}
			i++
		case c == '[' || c == '{':
			s.depth++
			i++
		case c == ']' || c == '}':
			if s.depth > 0 {
				s.depth--
			}
			i++
		default:
			i++
		}
	}
}

var tomlHeaderLine = regexp.MustCompile(`^\s*(\[\[?)\s*(.+?)\s*(\]\]?)\s*(#.*)?$`)

func parseTomlHeaderPath(text string) ([]string, bool) {
	m := tomlHeaderLine.FindStringSubmatch(text)
	if m == nil || len(m[1]) != len(m[3]) {
		return nil, false
	}
	return parseTomlDottedKey(m[2])
}

func parseTomlDottedKey(s string) ([]string, bool) {
	var parts []string
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return nil, false
		}
		var part strings.Builder
		switch s[i] {
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				part.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, false
			}
			i++
		case '\'':
			i++
			for i < len(s) && s[i] != '\'' {
				part.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, false
			}
			i++
		default:
			for i < len(s) && s[i] != '.' && s[i] != ' ' && s[i] != '\t' {
				c := s[i]
				if !(c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
					return nil, false
				}
				part.WriteByte(c)
				i++
			}
		}
		if part.Len() == 0 {
			return nil, false
		}
		parts = append(parts, part.String())
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) {
			if s[i] != '.' {
				return nil, false
			}
			i++
		}
	}
	return parts, len(parts) > 0
}

func tomlBlocks(raw string) []tomlBlock {
	lines := splitTomlLines(raw)
	var blocks []tomlBlock
	var state tomlScanState
	for i, line := range lines {
		wasClear := state.depth == 0 && state.multiline == ""
		isHeader := false
		var path []string
		if wasClear && strings.HasPrefix(strings.TrimLeft(line.text, " \t"), "[") {
			if p, ok := parseTomlHeaderPath(line.text); ok {
				isHeader, path = true, p
			}
		}
		if isHeader {
			start := line.start
			for j := i - 1; j >= 0; j-- {
				if !strings.HasPrefix(strings.TrimLeft(lines[j].text, " \t"), "#") {
					break
				}
				start = lines[j].start
			}
			if n := len(blocks); n > 0 {
				blocks[n-1].end = start
			}
			blocks = append(blocks, tomlBlock{path: path, start: start, header: line.start})
		} else {
			state.feed(line.text)
		}
	}
	if n := len(blocks); n > 0 {
		blocks[n-1].end = len(raw)
	}
	return blocks
}

func pathHasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func groupBlocks(raw string, prefix []string) []tomlBlock {
	var out []tomlBlock
	for _, b := range tomlBlocks(raw) {
		if pathHasPrefix(b.path, prefix) {
			out = append(out, b)
		}
	}
	return out
}

func tomlKeyText(key string) string {
	bare := key != ""
	for _, c := range key {
		if !(c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			bare = false
			break
		}
	}
	if bare {
		return key
	}
	return tomlQuote(key)
}

func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func validTomlText(s string) error {
	if s == "" || !utf8.ValidString(s) {
		return fmt.Errorf("must be non-empty text")
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("must not contain control characters or line breaks")
		}
	}
	return nil
}

func removeSpans(raw string, spans []tomlBlock) string {
	var b strings.Builder
	pos := 0
	for _, sp := range spans {
		b.WriteString(raw[pos:sp.start])
		pos = sp.end
	}
	b.WriteString(raw[pos:])
	return b.String()
}

func ensureBlankLineBefore(prefix string) string {
	if prefix == "" || strings.HasSuffix(prefix, "\n\n") || strings.HasSuffix(prefix, "\r\n\r\n") {
		return prefix
	}
	if strings.HasSuffix(prefix, "\n") {
		return prefix + "\n"
	}
	return prefix + "\n\n"
}

func replaceGroupText(raw string, prefix []string, text string) string {
	text = strings.TrimRight(text, "\r\n") + "\n"
	blocks := groupBlocks(raw, prefix)
	if len(blocks) == 0 {
		return ensureBlankLineBefore(raw) + text
	}
	first := blocks[0]
	rest := removeSpans(raw, blocks[1:])
	tail := rest[first.end:]
	if tail != "" {
		text += "\n"
	}
	return rest[:first.header] + text + strings.TrimLeft(tail, "\r\n")
}

func deleteGroupText(raw string, prefix []string) (string, bool) {
	blocks := groupBlocks(raw, prefix)
	if len(blocks) == 0 {
		return raw, false
	}
	out := removeSpans(raw, blocks)
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out, true
}

func flatTableBlock(raw string, table []string) (tomlBlock, bool) {
	for _, b := range tomlBlocks(raw) {
		if reflect.DeepEqual(b.path, table) {
			return b, true
		}
	}
	return tomlBlock{}, false
}

func flatKeyLine(raw string, block tomlBlock, key string) (tomlLine, bool) {
	for _, line := range splitTomlLines(raw[block.start:block.end]) {
		trimmed := strings.TrimLeft(line.text, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		eq := strings.Index(line.text, "=")
		if eq < 0 {
			continue
		}
		parts, ok := parseTomlDottedKey(strings.TrimSpace(line.text[:eq]))
		if ok && len(parts) == 1 && parts[0] == key {
			return tomlLine{text: line.text, start: block.start + line.start, end: block.start + line.end}, true
		}
	}
	return tomlLine{}, false
}

func setFlatKeyText(raw string, table []string, key, value string) (string, error) {
	line := tomlKeyText(key) + " = " + tomlQuote(value) + "\n"
	block, ok := flatTableBlock(raw, table)
	if !ok {
		header := "[" + strings.Join(table, ".") + "]\n"
		return ensureBlankLineBefore(raw) + header + line, nil
	}
	if existing, found := flatKeyLine(raw, block, key); found {
		var state tomlScanState
		state.feed(existing.text)
		if state.depth != 0 || state.multiline != "" {
			return "", fmt.Errorf("%s spans several lines and is not edited here", key)
		}
		return raw[:existing.start] + line + raw[existing.end:], nil
	}
	insertAt := block.end
	for insertAt > block.start {
		prevEnd := insertAt
		prevStart := strings.LastIndex(raw[:prevEnd-1], "\n") + 1
		if prevStart < block.start {
			prevStart = block.start
		}
		if strings.TrimSpace(raw[prevStart:prevEnd]) != "" {
			break
		}
		insertAt = prevStart
	}
	prefix := raw[:insertAt]
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + line + raw[insertAt:], nil
}

func deleteFlatKeyText(raw string, table []string, key string) (string, bool) {
	block, ok := flatTableBlock(raw, table)
	if !ok {
		return raw, false
	}
	existing, found := flatKeyLine(raw, block, key)
	if !found {
		return raw, false
	}
	start := existing.start
	for {
		prevStart := strings.LastIndex(raw[:start-1], "\n") + 1
		if start <= block.start || prevStart < block.start {
			break
		}
		if !strings.HasPrefix(strings.TrimLeft(raw[prevStart:start], " \t"), "#") {
			break
		}
		start = prevStart
	}
	return raw[:start] + raw[existing.end:], true
}

func renderTomlGroup(prefix []string, value any) (string, error) {
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return "", err
	}
	var nested any = normalized
	for i := len(prefix) - 1; i >= 0; i-- {
		nested = map[string]any{prefix[i]: nested}
	}
	out, err := toml.Marshal(nested)
	if err != nil {
		return "", fmt.Errorf("cannot write this value as TOML: %w", err)
	}
	var kept []string
	parents := map[string]bool{}
	for i := 1; i < len(prefix); i++ {
		parents["["+strings.Join(prefix[:i], ".")+"]"] = true
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if parents[strings.TrimSpace(line)] {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), nil
}

type jsonNumber interface {
	Int64() (int64, error)
	Float64() (float64, error)
	String() string
}

func normalizeJSONValue(v any) (any, error) {
	switch t := v.(type) {
	case jsonNumber:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, err := t.Float64()
		if err != nil {
			return nil, fmt.Errorf("%s is not a number TOML can hold", t.String())
		}
		return f, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			n, err := normalizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			n, err := normalizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("null has no TOML form; delete the entry instead")
	default:
		return v, nil
	}
}

func parseTomlMap(raw string) (map[string]any, error) {
	var out map[string]any
	if err := toml.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func getAtPath(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, p := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = next[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setAtPath(m map[string]any, path []string, value any) {
	cur := m
	for _, p := range path[:len(path)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}

func deleteAtPath(m map[string]any, path []string) {
	cur := m
	for _, p := range path[:len(path)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, path[len(path)-1])
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyAny(v)
	}
	return out
}

func deepCopyAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = deepCopyAny(item)
		}
		return out
	default:
		return v
	}
}

func verifyOnlyPathChanged(before, after string, path []string, want any, deleted bool) error {
	beforeMap, err := parseTomlMap(before)
	if err != nil {
		return fmt.Errorf("the file as it stands does not parse: %w", err)
	}
	afterMap, err := parseTomlMap(after)
	if err != nil {
		return fmt.Errorf("the edited file does not parse: %w", err)
	}
	expected := deepCopyMap(beforeMap)
	if deleted {
		deleteAtPath(expected, path)
	} else {
		setAtPath(expected, path, want)
	}
	pruneEmptyTables(expected)
	pruneEmptyTables(afterMap)
	if !reflect.DeepEqual(expected, afterMap) {
		return fmt.Errorf("the edit would have changed more than %s; nothing was written", strings.Join(path, "."))
	}
	return nil
}

func pruneEmptyTables(m map[string]any) {
	for k, v := range m {
		child, ok := v.(map[string]any)
		if !ok {
			continue
		}
		pruneEmptyTables(child)
		if len(child) == 0 {
			delete(m, k)
		}
	}
}
