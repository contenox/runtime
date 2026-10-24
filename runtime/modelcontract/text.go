package modelcontract

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type TextDecoder struct {
	Stops   []string
	Stopped bool
	pending string
}

func (d *TextDecoder) Push(piece string, final bool) (string, error) {
	if d.Stopped {
		return "", nil
	}
	d.pending += piece
	stopAt := len(d.pending)
	for _, stop := range d.Stops {
		if stop == "" || !utf8.ValidString(stop) {
			return "", errors.New("model text stops must be nonempty UTF-8")
		}
		if index := strings.Index(d.pending, stop); index >= 0 && index < stopAt {
			stopAt = index
			d.Stopped = true
		}
	}
	if d.Stopped {
		d.pending = d.pending[:stopAt]
		final = true
	}
	end := 0
	for end < len(d.pending) {
		rest := d.pending[end:]
		if !utf8.FullRuneInString(rest) {
			break
		}
		r, size := utf8.DecodeRuneInString(rest)
		if r == utf8.RuneError && size == 1 {
			return "", errors.New("model output contains invalid UTF-8")
		}
		end += size
	}
	if final && end != len(d.pending) {
		return "", errors.New("model output ended within a UTF-8 character")
	}
	if !final {
		for _, stop := range d.Stops {
			for length := 1; length < len(stop) && length <= len(d.pending); length++ {
				if strings.HasSuffix(d.pending, stop[:length]) {
					end = min(end, len(d.pending)-length)
				}
			}
		}
	}
	text := d.pending[:end]
	d.pending = d.pending[end:]
	return text, nil
}
