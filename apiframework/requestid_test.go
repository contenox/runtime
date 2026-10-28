package apiframework_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/libtracker"
)

func TestUnit_RequestIDMiddleware_BoundsAndSanitises(t *testing.T) {
	cases := []struct {
		name string
		in   string
		keep bool
	}{
		{"plain", "req-123_ABC.x", true},
		{"max length", strings.Repeat("a", apiframework.MaxRequestIDLength), true},
		{"too long", strings.Repeat("a", apiframework.MaxRequestIDLength+1), false},
		{"empty", "", false},
		{"space", "a b", false},
		{"control char", "a\x01b", false},
		{"non ascii", "café", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			h := apiframework.RequestIDMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen, _ = r.Context().Value(libtracker.ContextKeyRequestID).(string)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.in != "" {
				req.Header.Set("X-Request-ID", tc.in)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			echoed := rec.Header().Get("X-Request-ID")
			if echoed == "" || echoed != seen {
				t.Fatalf("echoed %q, context %q", echoed, seen)
			}
			if tc.keep && echoed != tc.in {
				t.Fatalf("valid id replaced: %q", echoed)
			}
			if !tc.keep && echoed == tc.in {
				t.Fatalf("invalid id kept: %q", echoed)
			}
		})
	}
}
