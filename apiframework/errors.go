package apiframework

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type APIError struct {
	err       error
	message   string
	param     string
	errorType string
	errorCode string
}

func (e *APIError) Error() string {
	return e.message
}

func (e *APIError) Unwrap() error {
	return e.err
}

func (e *APIError) Param() string {
	return e.param
}

func (e *APIError) Code() string {
	return e.errorCode
}

func IsAPIError(err error) (message, errorCode, errorType, param string, ok bool) {
	var e *APIError
	if errors.As(err, &e) {
		return e.message, e.errorCode, e.errorType, e.param, true
	}
	return "", "", "", "", false
}

func GetErrorParam(err error) string {
	var paramErr interface{ Param() string }
	if errors.As(err, &paramErr) {
		return paramErr.Param()
	}
	return ""
}

type apiErrorPayload struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"`
	Code    string  `json:"code"`
}

type apiErrorResponse struct {
	Error apiErrorPayload `json:"error"`
}

func Error(w http.ResponseWriter, r *http.Request, err error, op Operation) error {
	status := mapErrorToStatus(op, err)

	message, errorCode, errorType, param, ok := IsAPIError(err)
	if !ok {
		message = err.Error()
		errorType, errorCode = getErrorTypeAndCode(status)
	} else if errorCode == "" || errorType == "" {
		et, ec := getErrorTypeAndCode(status)
		if errorType == "" {
			errorType = et
		}
		if errorCode == "" {
			errorCode = ec
		}
	}

	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return nil
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	var paramField *string
	if param != "" {
		paramField = &param
	}

	response := apiErrorResponse{
		Error: apiErrorPayload{
			Message: message,
			Type:    errorType,
			Param:   paramField,
			Code:    errorCode,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		return fmt.Errorf("encode error response: %w", err)
	}
	return nil
}
