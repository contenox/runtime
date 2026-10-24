package libsandbox

import (
	"sort"
	"strings"
	"unicode"
)

type EnvPolicy struct {
	Allow []string
	Deny  []string
}

func DefaultEnvAllow() []string {
	return []string{
		"PATH",
		"TERM",
		"COLORTERM",
		"TZ",
		"LANG",
		"LANGUAGE",
		"LC_*",
		"TMPDIR",
		"USER",
		"LOGNAME",
		"SHELL",
	}
}

func ControlPlaneEnvDeny() []string {
	return []string{"CONTENOX_*"}
}

func SecretEnvDeny() []string {
	return []string{
		"*_TOKEN",
		"*_KEY",
		"*_SECRET",
		"*_PASSWORD",
		"*_PASSWD",
		"*_CREDENTIALS",
	}
}

func DefaultEnvDeny() []string {
	return append(ControlPlaneEnvDeny(), SecretEnvDeny()...)
}

func DefaultEnvPolicy() EnvPolicy {
	return EnvPolicy{Allow: DefaultEnvAllow(), Deny: DefaultEnvDeny()}
}

func (p EnvPolicy) Allowing(names ...string) EnvPolicy {
	return EnvPolicy{Allow: concat(p.Allow, names), Deny: clone(p.Deny)}
}

func (p EnvPolicy) Denying(names ...string) EnvPolicy {
	return EnvPolicy{Allow: clone(p.Allow), Deny: concat(p.Deny, names)}
}

func (p EnvPolicy) Resolve(parentEnv []string) []string {
	kept := make(map[string]struct{}, len(p.Allow))
	for _, kv := range parentEnv {
		if eq := strings.IndexByte(kv, '='); eq >= 0 && p.passes(kv[:eq]) {
			kept[kv[:eq]] = struct{}{}
		}
	}
	names := make([]string, 0, len(kept))
	for name := range kept {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p EnvPolicy) Apply(parentEnv []string) []string {
	kept := make(map[string]string, len(parentEnv))
	for _, kv := range parentEnv {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if name := kv[:eq]; p.passes(name) {
			kept[name] = kv
		}
	}
	names := make([]string, 0, len(kept))
	for name := range kept {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = kept[name]
	}
	return out
}

func (p EnvPolicy) passes(name string) bool {
	return !matchesAny(p.Deny, name) && matchesAny(p.Allow, name)
}

func ParseEnvList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r)
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func matchesAny(patterns []string, name string) bool {
	for _, pat := range patterns {
		if envNameMatches(pat, name) {
			return true
		}
	}
	return false
}

func envNameMatches(pattern, name string) bool {
	switch {
	case pattern == "":
		return false
	case pattern == "*":
		return true
	case pattern == name:
		return true
	case strings.HasPrefix(pattern, "*"):
		return strings.HasSuffix(name, pattern[1:])
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	default:
		return false
	}
}

func clone(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func concat(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}
