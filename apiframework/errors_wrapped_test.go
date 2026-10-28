package apiframework_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/apiframework"
)

func TestUnit_WrappedAPIErrorPreservesWireMetadata(t *testing.T) {
	err := fmt.Errorf("service context: %w", apiframework.MissingParameter("name", "Name is required"))

	if got := apiframework.GetErrorParam(err); got != "name" {
		t.Fatalf("param = %q, want name", got)
	}
	message, code, errType, param, ok := apiframework.IsAPIError(err)
	if !ok || message != "Name is required" || param != "name" || code == "" || errType == "" {
		t.Fatalf("IsAPIError lost metadata: %q %q %q %q %v", message, code, errType, param, ok)
	}

	w := httptest.NewRecorder()
	if err := apiframework.Error(w, httptest.NewRequest(http.MethodPost, "/", nil), err, apiframework.CreateOperation); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body struct {
		Error struct {
			Message string  `json:"message"`
			Param   *string `json:"param"`
			Code    string  `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Message != "Name is required" || body.Error.Param == nil || *body.Error.Param != "name" || body.Error.Code != code {
		t.Fatalf("envelope lost metadata: %+v", body.Error)
	}
}
