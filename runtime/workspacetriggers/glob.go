package workspacetriggers

import (
	"fmt"
	"regexp"
	"strings"
)

func compileGlob(glob string) (*regexp.Regexp, error) {
	glob = strings.TrimSpace(strings.ReplaceAll(glob, "\\", "/"))
	if glob == "" {
		return regexp.MustCompile(`^.*$`), nil
	}
	if strings.HasPrefix(glob, "/") {
		return nil, fmt.Errorf("%w: path glob %q must be relative to the project", ErrInvalidTrigger, glob)
	}
	if !strings.Contains(glob, "/") {
		glob = "**/" + glob
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			end := strings.IndexByte(glob[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("%w: path glob %q has an unclosed {", ErrInvalidTrigger, glob)
			}
			parts := strings.Split(glob[i+1:i+end], ",")
			for j, p := range parts {
				parts[j] = regexp.QuoteMeta(p)
			}
			b.WriteString("(?:" + strings.Join(parts, "|") + ")")
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
