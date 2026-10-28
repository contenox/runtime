package contenoxcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnit_ParseExpectedSum(t *testing.T) {
	sums := "AABB  contenox-runtime-linux-amd64\nccdd *contenox-runtime-windows-amd64.exe\nbad line\n"
	got, err := parseExpectedSum(strings.NewReader(sums), "v1", "contenox-runtime-linux-amd64")
	if err != nil || got != "aabb" {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = parseExpectedSum(strings.NewReader(sums), "v1", "contenox-runtime-windows-amd64.exe")
	if err != nil || got != "ccdd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := parseExpectedSum(strings.NewReader(sums), "v1", "contenox-runtime-darwin-arm64"); err == nil {
		t.Fatal("expected error for missing entry")
	}
}

func TestUnit_DownloadVerified(t *testing.T) {
	const asset = "contenox-runtime-linux-amd64"
	payload := []byte("binary-bytes")
	h := sha256.Sum256(payload)
	good := hex.EncodeToString(h[:])

	run := func(t *testing.T, sums string, status int) (string, error) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/download/v1/" + asset:
				_, _ = w.Write(payload)
			case "/download/v1/SHA256SUMS":
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(sums))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()
		old := releasesURL
		releasesURL = srv.URL
		defer func() { releasesURL = old }()
		var buf bytes.Buffer
		err := downloadVerified(context.Background(), "v1", asset, &buf)
		return buf.String(), err
	}

	if _, err := run(t, fmt.Sprintf("%s  %s\n", good, asset), 200); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	if _, err := run(t, fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), asset), 200); err == nil || !strings.Contains(err.Error(), "CHECKSUM MISMATCH") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if _, err := run(t, fmt.Sprintf("%s  other\n", good), 200); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("expected missing entry, got %v", err)
	}
	if _, err := run(t, "", 404); err == nil || !strings.Contains(err.Error(), "Refusing") {
		t.Fatalf("expected refusal on missing SHA256SUMS, got %v", err)
	}
}
