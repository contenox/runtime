package contenoxcli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/contenox/runtime/runtime/homedir"
	"os"
	"path/filepath"
	"strings"
)

const serveTokenFileName = "serve-token.txt"

func serveTokenFilePath() (string, error) {
	dir, err := globalContenoxDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, serveTokenFileName), nil
}

func serveTokenPathHint() string {
	if p, err := serveTokenFilePath(); err == nil {
		return p
	}
	return filepath.Join("~", homedir.DirName, serveTokenFileName)
}

func readServeTokenFile() string {
	path, err := serveTokenFilePath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeServeTokenFile(token string) error {
	path, err := serveTokenFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func generateServeToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
