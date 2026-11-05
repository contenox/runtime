package locale

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const (
	Key      = "locale"
	Fallback = "en-US"
)

var envKeys = []string{"LC_ALL", "LC_MESSAGES", "LANG"}

var (
	detectOnce sync.Once
	detected   string
)

func Detect() string {
	detectOnce.Do(func() {
		detected = detect(os.Getenv, platformLocale)
	})
	return detected
}

func detect(getenv func(string) string, platform func() string) string {
	if platform != nil {
		if v, ok := Normalize(platform()); ok {
			return v
		}
	}
	for _, key := range envKeys {
		if v, ok := Normalize(getenv(key)); ok {
			return v
		}
	}
	return Fallback
}

func Override(ctx context.Context, store runtimetypes.Store) string {
	if store == nil {
		return ""
	}
	v, ok := Normalize(clikv.Read(ctx, store, Key))
	if !ok {
		return ""
	}
	return v
}

func Resolve(ctx context.Context, store runtimetypes.Store) string {
	if v := Override(ctx, store); v != "" {
		return v
	}
	return Detect()
}

func Normalize(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, "_", "-")
	if s == "" {
		return "", false
	}
	parts := strings.Split(s, "-")
	lang := strings.ToLower(parts[0])
	if len(lang) < 2 || len(lang) > 3 || !alpha(lang) {
		return "", false
	}
	out := []string{lang}
	for _, part := range parts[1:] {
		switch {
		case len(part) == 4 && alpha(part):
			out = append(out, strings.ToUpper(part[:1])+strings.ToLower(part[1:]))
		case len(part) == 2 && alpha(part):
			out = append(out, strings.ToUpper(part))
		case len(part) == 3 && digits(part):
			out = append(out, part)
		case len(part) >= 5 && len(part) <= 8 && alnum(part):
			out = append(out, strings.ToLower(part))
		default:
			return "", false
		}
	}
	return strings.Join(out, "-"), true
}

func alpha(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func alnum(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return s != ""
}
