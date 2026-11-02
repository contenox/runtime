package project

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const LayerConfigFileName = "agents.toml"

func LayerConfigPath(root string) string {
	return filepath.Join(root, ContenoxDirName, LayerConfigFileName)
}

func LayerRootOf(path string) (string, bool) {
	dir := filepath.Clean(path)
	for {
		if info, err := os.Stat(LayerConfigPath(dir)); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func LayerKey(root string) string {
	clean := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:8])
}

func LayerKeyOf(path string) string {
	if root, ok := LayerRootOf(path); ok {
		return LayerKey(root)
	}
	return ""
}
