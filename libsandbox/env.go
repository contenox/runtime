package libsandbox

import (
	"sort"
	"strings"
)

func envKey(name string) string {
	if envNamesFold {
		return strings.ToUpper(name)
	}
	return name
}

type envEntry struct {
	name  string
	value string
}

type envSet map[string]envEntry

func (s envSet) set(name, value string) {
	s[envKey(name)] = envEntry{name: name, value: value}
}

func (s envSet) list() []string {
	entries := make([]envEntry, 0, len(s))
	for _, e := range s {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.name + "=" + e.value
	}
	return out
}

func envMapValue(set map[string]string, name string) (string, bool) {
	if v, ok := set[name]; ok {
		return v, true
	}
	if !envNamesFold {
		return "", false
	}
	key := envKey(name)
	for k, v := range set {
		if envKey(k) == key {
			return v, true
		}
	}
	return "", false
}

func scrubEnv(parentEnv []string, allow []string, set map[string]string, home string) []string {
	allowed := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		allowed[envKey(name)] = struct{}{}
	}

	out := make(envSet, len(allow)+len(set)+2)
	for _, kv := range parentEnv {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		if _, ok := allowed[envKey(name)]; ok {
			out.set(name, kv[eq+1:])
		}
	}
	for k, v := range set {
		out.set(k, v)
	}
	if _, ok := envMapValue(set, "PATH"); !ok {
		out.set("PATH", canonicalPATH())
	}
	out.set("HOME", home)
	return out.list()
}
