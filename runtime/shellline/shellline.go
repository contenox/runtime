package shellline

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync/atomic"

	"mvdan.cc/sh/v3/syntax"
)

type Kind string

const (
	KindPOSIX      Kind = "sh"
	KindPowerShell Kind = "powershell"
	KindCmd        Kind = "cmd"
	KindUnknown    Kind = "unknown"
)

type kindContextKey struct{}

func WithKind(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, kindContextKey{}, NormalizeKind(kind))
}

func KindFromContext(ctx context.Context) Kind {
	kind, _ := ctx.Value(kindContextKey{}).(Kind)
	return kind
}

func NormalizeKind(s string) Kind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "sh", "bash", "dash", "ash", "posix":
		return KindPOSIX
	case "powershell", "pwsh":
		return KindPowerShell
	case "cmd", "cmd.exe":
		return KindCmd
	default:
		return KindUnknown
	}
}

func Structural(trusted Kind, goos string) bool {
	switch NormalizeKind(string(trusted)) {
	case KindPOSIX:
		return true
	case "":
	default:
		return false
	}
	return goos != "windows"
}

type Operator string

const (
	OpStart Operator = ""
	OpAnd   Operator = "&&"
	OpOr    Operator = "||"
	OpSeq   Operator = ";"
	OpPipe  Operator = "|"
)

type Step struct {
	Operator Operator
	Name     string
	Base     string
	Display  string
	Words    []string
	Literal  bool
	ArgCount int
	Assigns  []string
}

type Line struct {
	Readable     bool
	Parsed       bool
	BashOnly     bool
	Steps        []Step
	File         *syntax.File
	Redirects    []Redirect
	CommandSubst bool
	ProcessSubst bool
	ArithmExp    bool
	Background   bool
	Negated      bool
	Coprocess    bool
	Control      bool
	Pipelines    int
}

const MaxLineBytes = 64 * 1024

type Redirect struct {
	Op      string
	Target  string
	Heredoc bool
}

var parses atomic.Int64

func Parses() int64 { return parses.Load() }

func Parse(src string, kind Kind, goos string) Line {
	var line Line
	if !Structural(kind, goos) {
		return line
	}
	line.Readable = true
	if len(src) > MaxLineBytes || strings.TrimSpace(src) == "" {
		return line
	}
	file, bashOnly, ok := ParseFile(src)
	if !ok {
		return line
	}
	line.Parsed = true
	line.BashOnly = bashOnly
	line.File = file
	collect(&line, file)
	return line
}

func ParseFile(src string) (file *syntax.File, bashOnly bool, ok bool) {
	parses.Add(1)
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(src), "")
	if err == nil && file != nil {
		return file, false, true
	}
	parses.Add(1)
	file, err = syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil || file == nil {
		return nil, false, false
	}
	return file, true, true
}

func collect(line *Line, file *syntax.File) {
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.CmdSubst:
			line.CommandSubst = true
		case *syntax.ProcSubst:
			line.ProcessSubst = true
		case *syntax.ArithmExp:
			line.ArithmExp = true
		}
		return true
	})
	for i, stmt := range file.Stmts {
		op := OpStart
		if i > 0 {
			op = OpSeq
		}
		walkStmt(line, stmt, op)
	}
}

func walkStmt(line *Line, stmt *syntax.Stmt, op Operator) {
	if stmt == nil {
		return
	}
	if stmt.Background {
		line.Background = true
	}
	if stmt.Negated {
		line.Negated = true
	}
	if stmt.Coprocess {
		line.Coprocess = true
	}
	for _, redir := range stmt.Redirs {
		line.Redirects = append(line.Redirects, RedirectView(redir))
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		step := StepFromCall(cmd)
		step.Operator = op
		line.Steps = append(line.Steps, step)
	case *syntax.BinaryCmd:
		switch cmd.Op {
		case syntax.AndStmt:
			walkStmt(line, cmd.X, op)
			walkStmt(line, cmd.Y, OpAnd)
		case syntax.OrStmt:
			walkStmt(line, cmd.X, op)
			walkStmt(line, cmd.Y, OpOr)
		case syntax.Pipe, syntax.PipeAll:
			line.Pipelines++
			walkStmt(line, cmd.X, op)
			walkStmt(line, cmd.Y, OpPipe)
		default:
			line.Control = true
		}
	default:
		line.Control = true
	}
}

func StepFromCall(call *syntax.CallExpr) Step {
	var step Step
	for _, as := range call.Assigns {
		name := ""
		if as.Name != nil {
			name = as.Name.Value
		}
		step.Assigns = append(step.Assigns, name)
	}
	if len(call.Args) == 0 {
		return step
	}
	step.ArgCount = len(call.Args)
	if name, ok := WordName(call.Args[0]); ok && name != "" {
		step.Name = name
		step.Base = path.Base(name)
	}
	words := make([]string, 0, len(call.Args))
	literal := true
	for _, word := range call.Args {
		resolved, ok := LiteralWord(word)
		if !ok {
			literal = false
			break
		}
		words = append(words, resolved)
	}
	if literal {
		step.Words = words
		step.Literal = true
	}
	step.Display = displayOf(call)
	return step
}

func RedirectView(redir *syntax.Redirect) Redirect {
	out := Redirect{Op: redir.Op.String(), Heredoc: redir.Hdoc != nil}
	if redir.Word != nil {
		if target, ok := WordName(redir.Word); ok {
			out.Target = target
		} else {
			out.Target = UnknowableTarget
		}
	}
	return out
}

const UnknowableTarget = "(not statically knowable)"

func LiteralAssignments(file *syntax.File) map[string]string {
	out := map[string]string{}
	syntax.Walk(file, func(node syntax.Node) bool {
		as, ok := node.(*syntax.Assign)
		if !ok || as.Append || as.Naked || as.Name == nil || as.Index != nil || as.Value == nil {
			return true
		}
		if v, ok := WordName(as.Value); ok && v != "" {
			out[as.Name.Value] = v
		}
		return true
	})
	return out
}

func ResolveAssignedWords(call *syntax.CallExpr, assigns map[string]string) ([]string, bool) {
	if len(assigns) == 0 || len(call.Args) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(call.Args))
	resolved := false
	for _, word := range call.Args {
		if v, ok := WordName(word); ok {
			out = append(out, v)
			continue
		}
		if name, ok := SoleParamName(word); ok {
			if v, ok := assigns[name]; ok {
				out = append(out, v)
				resolved = true
				continue
			}
		}
		out = append(out, "")
	}
	if !resolved || out[0] == "" {
		return nil, false
	}
	return out, true
}

func SoleParamName(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) != 1 {
		return "", false
	}
	pe, ok := word.Parts[0].(*syntax.ParamExp)
	if !ok || pe.Param == nil {
		return "", false
	}
	if pe.Excl || pe.Length || pe.Width || pe.IsSet ||
		pe.Index != nil || pe.Slice != nil || pe.Repl != nil || pe.Exp != nil ||
		len(pe.Modifiers) > 0 {
		return "", false
	}
	return pe.Param.Value, true
}

func LenientWords(call *syntax.CallExpr) []string {
	out := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		v, ok := WordName(word)
		if !ok {
			v = ""
		}
		out = append(out, v)
	}
	return out
}

func WordsFromValues(words []string) Step {
	if len(words) == 0 {
		return Step{}
	}
	step := Step{
		Name:     words[0],
		Base:     path.Base(words[0]),
		ArgCount: len(words),
		Display:  FlattenDisplay(words),
	}
	for _, word := range words {
		if word == "" {
			return step
		}
	}
	step.Words = append([]string(nil), words...)
	step.Literal = true
	return step
}

const maxDisplayRunes = 60

func FlattenDisplay(parts []string) string {
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.Join(parts, " "))
	if r := []rune(s); len(r) > maxDisplayRunes {
		s = string(r[:maxDisplayRunes-1]) + "…"
	}
	return s
}

func displayOf(call *syntax.CallExpr) string {
	parts := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		if v, ok := WordName(word); ok {
			parts = append(parts, v)
			continue
		}
		parts = append(parts, "…")
	}
	return FlattenDisplay(parts)
}

func DisplayWords(words []string) string {
	parts := make([]string, 0, len(words))
	for _, word := range words {
		if word == "" {
			word = "…"
		}
		parts = append(parts, word)
	}
	return FlattenDisplay(parts)
}

const wordUnsafeMeta = "*?[]{}~\\$"

func LiteralWord(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) == 0 {
		return "", false
	}
	var sb strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, wordUnsafeMeta) {
				return "", false
			}
			sb.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", false
			}
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok || strings.Contains(lit.Value, "\\") {
					return "", false
				}
				sb.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func WordName(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) == 0 {
		return "", false
	}
	var sb strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			sb.WriteString(UnescapeUnquoted(p.Value))
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				sb.WriteString(UnescapeDoubleQuoted(lit.Value))
			}
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func UnescapeUnquoted(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			sb.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			break
		}
		i++
		if s[i] == '\n' {
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func UnescapeDoubleQuoted(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			sb.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$', '`', '"', '\\':
			i++
			sb.WriteByte(s[i])
		case '\n':
			i++
		default:
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

func DecodeEscapes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			sb.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; c {
		case '\\':
			sb.WriteByte('\\')
		case 'a':
			sb.WriteByte(0x07)
		case 'b':
			sb.WriteByte(0x08)
		case 'e':
			sb.WriteByte(0x1b)
		case 'f':
			sb.WriteByte(0x0c)
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case 'v':
			sb.WriteByte(0x0b)
		case 'x':
			if n, width, ok := ParseEscapedNumber(s[i+1:], 16, 2); ok {
				sb.WriteByte(n)
				i += width
				continue
			}
			sb.WriteString(`\x`)
		case '0', '1', '2', '3', '4', '5', '6', '7':
			start := i
			if c == '0' {
				start = i + 1
			}
			if n, width, ok := ParseEscapedNumber(s[start:], 8, 3); ok {
				sb.WriteByte(n)
				i = start + width - 1
				continue
			}
			sb.WriteByte(c)
		default:
			sb.WriteByte('\\')
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func ParseEscapedNumber(s string, base, maxDigits int) (value byte, width int, ok bool) {
	n := 0
	for width < len(s) && width < maxDigits {
		d := DigitValue(s[width])
		if d < 0 || d >= base {
			break
		}
		next := n*base + d
		if next > 0xff {
			break
		}
		n = next
		width++
	}
	if width == 0 {
		return 0, 0, false
	}
	return byte(n), width, true
}

func DigitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

func ModeRequested(args map[string]any) bool {
	switch v := args["shell"].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y", "on":
			return true
		}
	}
	return false
}

func ArgTokens(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return strings.Fields(t)
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprintf("%v", e))
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}

func LineFromArgs(args map[string]any, kind Kind, goos string) (Line, bool) {
	var line Line
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return line, false
	}
	extra := ArgTokens(args["args"])
	src := command
	if ModeRequested(args) {
		if len(extra) > 0 {
			src += " " + strings.Join(extra, " ")
		}
	} else if len(extra) > 0 {
		return line, false
	}
	if len(src) > MaxLineBytes {
		return line, false
	}
	return Parse(src, kind, goos), true
}
