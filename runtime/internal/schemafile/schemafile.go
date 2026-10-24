package schemafile

import (
	"encoding/json"
	"fmt"
)

func MustDecode[T any](name string, data []byte) T {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		panic(fmt.Sprintf("%s: %v", name, err))
	}
	return value
}
