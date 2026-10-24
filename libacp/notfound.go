package libacp

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var e *Error
	if !errors.As(err, &e) || e == nil {
		return false
	}
	if e.Code == ErrResourceNotFound {
		return true
	}
	switch e.Code {
	case ErrParseError, ErrInvalidRequest, ErrMethodNotFound, ErrInvalidParams, ErrAuthRequired, ErrRequestTimeout:
		return false
	}
	return strings.Contains(strings.ToLower(e.Message), "not found")
}

func AsNotExist(err error) error {
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	}
	return err
}
