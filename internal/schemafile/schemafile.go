// Package schemafile decodes the machine-readable schemas the runtime embeds.
package schemafile

import (
	"encoding/json"
	"fmt"
)

// MustDecode decodes one embedded schema, reporting the file and the reason an
// unusable schema cannot be loaded.
func MustDecode[T any](name string, data []byte) T {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		panic(fmt.Sprintf("%s: %v", name, err))
	}
	return value
}
