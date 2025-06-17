package hookrecipes

import (
	"fmt"
	"strings"
)

func ParsePrefixedArgs(input string) (string, map[string]string, error) {
	if !strings.HasPrefix(input, "args:") {
		return input, nil, nil
	}

	// Split into args part and remaining string
	parts := strings.SplitN(input, "|", 2)
	argsPart := strings.TrimPrefix(parts[0], "args:")
	var remPart string
	if len(parts) > 1 {
		remPart = strings.TrimSpace(parts[1])
	}

	// Parse key-value pairs
	pairs := strings.Split(argsPart, ",")
	args := make(map[string]string)

	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return "", nil, fmt.Errorf("invalid argument: %q", pair)
		}

		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(kv[1])
		args[key] = value
	}

	return remPart, args, nil
}
