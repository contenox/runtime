package bedrock

import "strings"

const (
	thinkingOpenTag  = "<thinking>"
	thinkingCloseTag = "</thinking>"
)

const leadingSpace = " \t\r\n"

func splitLeadingThinking(text string) (thinking, answer string, ok bool) {
	trimmed := strings.TrimLeft(text, leadingSpace)
	if !strings.HasPrefix(trimmed, thinkingOpenTag) {
		return "", text, false
	}
	rest := trimmed[len(thinkingOpenTag):]
	end := strings.Index(rest, thinkingCloseTag)
	if end < 0 {
		return strings.TrimSpace(rest), "", true
	}
	return strings.TrimSpace(rest[:end]), strings.TrimLeft(rest[end+len(thinkingCloseTag):], leadingSpace), true
}

type thinkingTagState int

const (
	thinkingTagUndecided thinkingTagState = iota
	thinkingTagInside
	thinkingTagAnswer
)

type thinkingTagFilter struct {
	state      thinkingTagState
	buf        string
	trimAnswer bool
}

func (f *thinkingTagFilter) feed(delta string) (thinking, answer string) {
	switch f.state {
	case thinkingTagUndecided:
		f.buf += delta
		trimmed := strings.TrimLeft(f.buf, leadingSpace)
		switch {
		case trimmed == "":
			return "", ""
		case strings.HasPrefix(trimmed, thinkingOpenTag):
			f.state = thinkingTagInside
			f.buf = trimmed[len(thinkingOpenTag):]
			return f.drainThinking()
		case strings.HasPrefix(thinkingOpenTag, trimmed):
			return "", ""
		default:
			f.state = thinkingTagAnswer
			out := f.buf
			f.buf = ""
			return "", out
		}
	case thinkingTagInside:
		f.buf += delta
		return f.drainThinking()
	default:
		return "", f.answerText(delta)
	}
}

func (f *thinkingTagFilter) drainThinking() (thinking, answer string) {
	if end := strings.Index(f.buf, thinkingCloseTag); end >= 0 {
		thinking = f.buf[:end]
		rest := f.buf[end+len(thinkingCloseTag):]
		f.state = thinkingTagAnswer
		f.trimAnswer = true
		f.buf = ""
		return thinking, f.answerText(rest)
	}
	hold := partialSuffix(f.buf, thinkingCloseTag)
	thinking = f.buf[:len(f.buf)-hold]
	f.buf = f.buf[len(f.buf)-hold:]
	return thinking, ""
}

func (f *thinkingTagFilter) answerText(delta string) string {
	if !f.trimAnswer {
		return delta
	}
	out := strings.TrimLeft(delta, leadingSpace)
	if out == "" {
		return ""
	}
	f.trimAnswer = false
	return out
}

func (f *thinkingTagFilter) flush() (thinking, answer string) {
	defer func() { f.buf = "" }()
	switch f.state {
	case thinkingTagUndecided:
		f.state = thinkingTagAnswer
		return "", f.buf
	case thinkingTagInside:
		return f.buf, ""
	default:
		return "", ""
	}
}

func partialSuffix(s, tag string) int {
	max := len(tag) - 1
	if max > len(s) {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
