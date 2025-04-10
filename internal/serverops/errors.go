package serverops

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/js402/CATE/internal/serverops/messagerepo"
	"github.com/js402/CATE/libs/libauth"
	"github.com/js402/CATE/libs/libdb"
)

type ErrBadPathValue string

func (v ErrBadPathValue) Error() string {
	return fmt.Sprintf("serverops: path value error %s", string(v))
}

type Operation uint16

const (
	CreateOperation Operation = iota
	GetOperation
	UpdateOperation
	DeleteOperation
	ListOperation
	AuthorizeOperation
	ServerOperation
)

// Map known error types to HTTP status codes
func mapErrorToStatus(op Operation, err error) int {
	if op == AuthorizeOperation {
		return http.StatusForbidden
	}
	if op == ServerOperation {
		return http.StatusInternalServerError
	}
	if errors.Is(err, libdb.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, libdb.ErrUniqueViolation) ||
		errors.Is(err, libdb.ErrForeignKeyViolation) ||
		errors.Is(err, libdb.ErrNotNullViolation) ||
		errors.Is(err, libdb.ErrCheckViolation) ||
		errors.Is(err, libdb.ErrConstraintViolation) {
		// Conflict due to constraint violations.
		return http.StatusConflict
	}
	if errors.Is(err, libdb.ErrDeadlockDetected) ||
		errors.Is(err, libdb.ErrSerializationFailure) ||
		errors.Is(err, libdb.ErrLockNotAvailable) ||
		errors.Is(err, libdb.ErrQueryCanceled) ||
		errors.Is(err, libdb.ErrDataTruncation) ||
		errors.Is(err, libdb.ErrNumericOutOfRange) ||
		errors.Is(err, libdb.ErrInvalidInputSyntax) ||
		errors.Is(err, libdb.ErrUndefinedColumn) ||
		errors.Is(err, libdb.ErrUndefinedTable) {
		// These might also indicate conflict or unprocessable input.
		return http.StatusConflict
	}

	if errors.Is(err, libauth.ErrNotAuthorized) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, libauth.ErrTokenExpired) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, libauth.ErrIssuedAtMissing) ||
		errors.Is(err, libauth.ErrIssuedAtInFuture) ||
		errors.Is(err, libauth.ErrIdentityMissing) ||
		errors.Is(err, libauth.ErrInvalidTokenClaims) ||
		errors.Is(err, libauth.ErrUnexpectedSigningMethod) ||
		errors.Is(err, libauth.ErrTokenParsingFailed) ||
		errors.Is(err, libauth.ErrTokenSigningFailed) {
		return http.StatusBadRequest
	}

	if errors.Is(err, ErrEncodeInvalidJSON) ||
		errors.Is(err, ErrDecodeInvalidJSON) {
		return http.StatusBadRequest
	}

	if errors.Is(err, messagerepo.ErrMessageNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, messagerepo.ErrSerializeMessage) ||
		errors.Is(err, messagerepo.ErrDeserializeResponse) {
		return http.StatusBadRequest
	}
	if errors.Is(err, messagerepo.ErrIndexCreationFailed) ||
		errors.Is(err, messagerepo.ErrIndexCheckFailed) {
		return http.StatusInternalServerError
	}
	if errors.Is(err, messagerepo.ErrSearchFailed) {
		return http.StatusBadRequest
	}
	if errors.Is(err, messagerepo.ErrUpdateFailed) ||
		errors.Is(err, messagerepo.ErrDeleteFailed) {
		return http.StatusUnprocessableEntity
	}

	// Default mappings based on operation type
	switch op {
	case CreateOperation, UpdateOperation:
		return http.StatusUnprocessableEntity
	case GetOperation, ListOperation:
		return http.StatusNotFound
	case AuthorizeOperation:
		return http.StatusForbidden
	case DeleteOperation:
		return http.StatusNoContent
	default:
		return http.StatusInternalServerError
	}
}

// Error sends a JSON-encoded error response with an appropriate status code
func Error(w http.ResponseWriter, r *http.Request, err error, op Operation) error {
	status := mapErrorToStatus(op, err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := map[string]string{"error": err.Error()}
	if encodeErr := json.NewEncoder(w).Encode(response); encodeErr != nil {
		return fmt.Errorf("encode json: %w", encodeErr)
	}

	return nil
}
