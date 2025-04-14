package serverops

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var ErrEncodeInvalidJSON = errors.New("serverops: encoding failing, invalid json")
var ErrDecodeInvalidJSON = errors.New("serverops: decoding failing, invalid json")

func Encode[T any](w http.ResponseWriter, _ *http.Request, status int, v T) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrEncodeInvalidJSON, err)
	}

	return nil
}

func Decode[T any](r *http.Request) (T, error) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		return v, fmt.Errorf("%w: %w", ErrDecodeInvalidJSON, err)
	}
	return v, nil
}
