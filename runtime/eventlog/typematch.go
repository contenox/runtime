package eventlog

import "strings"

func MatchesType(pattern, typeName string) bool {
	pattern = strings.TrimSpace(pattern)
	typeName = strings.TrimSpace(typeName)
	if prefix, ok := strings.CutSuffix(pattern, ".*"); ok {
		return prefix != "" && strings.HasPrefix(typeName, prefix+".")
	}
	return pattern == typeName
}

func IsPattern(p string) bool {
	return strings.HasSuffix(strings.TrimSpace(p), ".*")
}
