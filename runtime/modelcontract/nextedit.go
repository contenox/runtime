package modelcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type EditHistory struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type ContextFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type NextEditInput struct {
	Path    string        `json:"path"`
	Before  string        `json:"before"`
	Current string        `json:"current"`
	Cursor  int           `json:"cursor"`
	Start   int           `json:"start"`
	End     int           `json:"end"`
	History []EditHistory `json:"history"`
	Context []ContextFile `json:"context"`
}

func (i NextEditInput) Validate() error {
	if !validPath(i.Path) || !utf8.ValidString(i.Before) || !utf8.ValidString(i.Current) {
		return errors.New("next edit requires a path and valid UTF-8 document snapshots")
	}
	if i.Start < 0 || i.End < i.Start || i.End > len(i.Current) || i.Cursor < i.Start || i.Cursor > i.End {
		return errors.New("next edit cursor and editable range must lie within the document")
	}
	for _, offset := range []int{i.Start, i.End, i.Cursor} {
		if !byteBoundary(i.Current, offset) {
			return errors.New("next edit positions must be UTF-8 boundaries")
		}
	}
	for _, change := range i.History {
		if !validPath(change.Path) || !utf8.ValidString(change.Before) || !utf8.ValidString(change.After) {
			return errors.New("next edit history contains invalid source or path")
		}
	}
	for _, file := range i.Context {
		if !validPath(file.Path) || !utf8.ValidString(file.Text) {
			return errors.New("next edit context contains invalid source or path")
		}
	}
	return nil
}

func validPath(path string) bool {
	return path != "" && utf8.ValidString(path) && !strings.ContainsAny(path, "\r\n\x00")
}

func byteBoundary(text string, offset int) bool {
	return offset >= 0 && offset <= len(text) && (offset == len(text) || utf8.RuneStart(text[offset]))
}

type TextEdit struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func ValidateEdits(input NextEditInput, edits []TextEdit) error {
	if err := input.Validate(); err != nil {
		return err
	}
	previousEnd := input.Start
	for index, edit := range edits {
		if edit.Start < previousEnd || edit.End < edit.Start || edit.End > input.End || !byteBoundary(input.Current, edit.Start) || !byteBoundary(input.Current, edit.End) {
			return fmt.Errorf("next edit replacement %d is outside its region or overlaps another replacement", index)
		}
		if input.Current[edit.Start:edit.End] != edit.OldText || !utf8.ValidString(edit.NewText) {
			return fmt.Errorf("next edit replacement %d does not match its source snapshot", index)
		}
		if index > 0 && edits[index-1].Start == edit.Start {
			return errors.New("next edit replacements must have distinct positions")
		}
		previousEnd = edit.End
	}
	return nil
}

type NextEditProfile struct {
	ID            string
	Version       int
	ContextTokens int
	MaxTokens     int
	Stops         []string
	TextStops     []string
}

func ResolveNextEdit(format string) (NextEditProfile, error) {
	entry, err := declaredFormats.nextEditFormat(format)
	if err != nil {
		return NextEditProfile{}, err
	}
	return NextEditProfile{ID: entry.ID, Version: entry.Version, ContextTokens: entry.ContextTokens, MaxTokens: entry.MaxTokens, Stops: entry.Stops, TextStops: entry.TextStops}, nil
}

func NextEditFingerprint(format string) (string, error) {
	profile, err := ResolveNextEdit(format)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type TextPart struct {
	Text    string
	Control bool
}

type PreparedEdit struct {
	Parts     []TextPart
	Prefill   string
	Stops     []string
	TextStops []string
	input     NextEditInput
	format    string
}

func (p PreparedEdit) Render() string {
	var text strings.Builder
	for _, part := range p.Parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

func (p PreparedEdit) EndpointStops() []string {
	return append(append([]string{}, p.Stops...), p.TextStops...)
}

func PrepareNextEdit(format string, input NextEditInput) (PreparedEdit, error) {
	profile, err := ResolveNextEdit(format)
	if err != nil {
		return PreparedEdit{}, err
	}
	if err := input.Validate(); err != nil {
		return PreparedEdit{}, err
	}
	prepared := PreparedEdit{Stops: profile.Stops, TextStops: profile.TextStops, input: input, format: format}
	add := func(text string) { prepared.Parts = append(prepared.Parts, TextPart{Text: text}) }
	control := func(text string) { prepared.Parts = append(prepared.Parts, TextPart{Text: text, Control: true}) }
	region := input.Current[input.Start:input.End]
	cursor := input.Cursor - input.Start
	if format == "zeta-2.1" {
		for _, marker := range []string{"<|marker_", "<|user_cursor|>", "<filename>"} {
			if strings.Contains(input.Current, marker) || strings.Contains(input.Before, marker) {
				return PreparedEdit{}, errors.New("source contains reserved Zeta edit markers; select another edit profile")
			}
		}
		control("<[fim-suffix]>")
		add("\n")
		control("<[fim-prefix]>")
		for _, file := range input.Context {
			add("<filename>" + file.Path + "\n" + file.Text + "\n\n")
		}
		add("<filename>edit_history\n")
		for _, change := range append(append([]EditHistory{}, input.History...), EditHistory{Path: input.Path, Before: input.Before, After: input.Current}) {
			add(editHistoryDiff(change))
		}
		start, end := sourceContext(input)
		add("\n<filename>" + input.Path + "\n" + input.Current[start:input.Start] + "<|marker_1|>" + region[:cursor] + "<|user_cursor|>" + region[cursor:] + "<|marker_2|>" + input.Current[input.End:end] + "<|marker_3|>\n")
		control("<[fim-middle]>")
		add("\n")
		return prepared, nil
	}
	for _, file := range input.Context {
		control("<|file_sep|>")
		add(file.Path + "\n" + file.Text + "\n")
	}
	if format == "sweep-v2" {
		start, end := sourceContext(input)
		control("<|file_sep|>")
		add(input.Path + "\n" + input.Current[start:end] + "\n")
	}
	changes := append([]EditHistory{}, input.History...)
	if format == "sweep-v2" && input.Before != input.Current {
		changes = append(changes, EditHistory{Path: input.Path, Before: input.Before, After: input.Current})
	}
	for _, change := range changes {
		control("<|file_sep|>")
		if format == "sweep-v2" {
			start, beforeEnd, afterEnd := editHistoryRange(change)
			add(fmt.Sprintf("%s:%d:%d\noriginal:\n%s\nupdated:\n%s\n", change.Path, lineNumber(change.Before, start), lastLineNumber(change.Before, beforeEnd), change.Before[start:beforeEnd], change.After[start:afterEnd]))
		} else {
			add(change.Path + ".diff\noriginal:\n" + change.Before + "\nupdated:\n" + change.After + "\n")
		}
	}
	location := input.Path
	current := region
	original := originalRegion(input)
	if format == "sweep-v2" {
		location = fmt.Sprintf("%s:%d:%d", input.Path, lineNumber(input.Current, input.Start), lastLineNumber(input.Current, input.End))
		original = region
		current = region[:cursor] + "<|cursor|>" + region[cursor:]
		prepared.Prefill = region[:strings.LastIndex(region[:cursor], "\n")+1]
		changedStart, beforeEnd, afterEnd := changedRange(input.Before, input.Current)
		if afterEnd > beforeEnd && changedStart < input.Cursor {
			if end := strings.IndexByte(prepared.Prefill, '\n'); end >= 0 {
				end++
				for end < len(prepared.Prefill) && prepared.Prefill[end] == '\n' {
					end++
				}
				prepared.Prefill = prepared.Prefill[:end]
			}
		}
	}
	control("<|file_sep|>")
	add("original/" + location + "\n" + original + "\n")
	control("<|file_sep|>")
	add("current/" + location + "\n" + current + "\n")
	control("<|file_sep|>")
	add("updated/" + location + "\n" + prepared.Prefill)
	return prepared, nil
}

func originalRegion(input NextEditInput) string {
	start, beforeEnd, afterEnd := changedRange(input.Before, input.Current)
	mapOffset := func(offset int, end bool) int {
		if offset <= start {
			return offset
		}
		if offset >= afterEnd {
			return offset - afterEnd + beforeEnd
		}
		if end {
			return beforeEnd
		}
		return start
	}
	return input.Before[mapOffset(input.Start, false):mapOffset(input.End, true)]
}

func changedRange(before, after string) (int, int, int) {
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	for !byteBoundary(before, start) || !byteBoundary(after, start) {
		start--
	}
	beforeEnd, afterEnd := len(before), len(after)
	for beforeEnd > start && afterEnd > start && before[beforeEnd-1] == after[afterEnd-1] {
		beforeEnd--
		afterEnd--
	}
	for !byteBoundary(before, beforeEnd) || !byteBoundary(after, afterEnd) {
		beforeEnd++
		afterEnd++
	}
	return start, beforeEnd, afterEnd
}

func (p PreparedEdit) Parse(output, finishReason string) ([]TextEdit, error) {
	if _, err := ResolveNextEdit(p.format); err != nil {
		return nil, err
	}
	if err := p.input.Validate(); err != nil {
		return nil, err
	}
	if finishReason != "stop" || !utf8.ValidString(output) {
		return nil, errors.New("next edit requires a complete, valid UTF-8 replacement")
	}
	if p.format == "zeta-2.1" {
		const opening, closing = "<|marker_1|>", "<|marker_2|>"
		output = strings.TrimSuffix(output, "\n")
		output = strings.TrimSuffix(output, "\r")
		if !strings.HasPrefix(output, opening) || !strings.HasSuffix(output, closing) {
			return nil, errors.New("next edit returned an invalid region envelope")
		}
		output = strings.TrimSuffix(strings.TrimPrefix(output, opening), closing)
		if strings.Count(output, "<|user_cursor|>") > 1 {
			return nil, errors.New("next edit returned multiple cursor markers")
		}
		output = strings.Replace(output, "<|user_cursor|>", "", 1)
		if strings.Contains(output, "<|marker_") {
			return nil, errors.New("next edit returned an unexpected region marker")
		}
	}
	replacement := p.Prefill + output
	before := p.input.Current[p.input.Start:p.input.End]
	if before == replacement {
		return []TextEdit{}, nil
	}
	start, beforeEnd, afterEnd := changedRange(before, replacement)
	edits := []TextEdit{{Start: p.input.Start + start, End: p.input.Start + beforeEnd, OldText: before[start:beforeEnd], NewText: replacement[start:afterEnd]}}
	if err := ValidateEdits(p.input, edits); err != nil {
		return nil, err
	}
	return edits, nil
}

func TokenizeParts(parts []TextPart, stops []string, tokenize func(string, bool) ([]int, error)) ([]int, []int, error) {
	if tokenize == nil {
		return nil, nil, errors.New("native code generation requires a tokenizer")
	}
	var tokens, stopIDs []int
	for _, part := range parts {
		ids, err := tokenize(part.Text, part.Control)
		if err != nil {
			return nil, nil, err
		}
		if part.Control && len(ids) != 1 {
			return nil, nil, fmt.Errorf("model control %q is not one token", part.Text)
		}
		tokens = append(tokens, ids...)
	}
	for _, stop := range stops {
		ids, err := tokenize(stop, true)
		if err != nil {
			return nil, nil, err
		}
		if len(ids) != 1 {
			return nil, nil, fmt.Errorf("model stop %q is not one token", stop)
		}
		stopIDs = append(stopIDs, ids[0])
	}
	return tokens, stopIDs, nil
}
