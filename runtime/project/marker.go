package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	ContenoxDirName = ".contenox"
	MarkerFileName  = "workspace.id"
	MaxNameLen      = 120
)

type Marker struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

func contenoxDirOf(projectRoot string) string {
	return filepath.Join(projectRoot, ContenoxDirName)
}

func markerPath(contenoxDir string) string {
	return filepath.Join(contenoxDir, MarkerFileName)
}

func parseMarker(data []byte) Marker {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return Marker{}
	}
	var m Marker
	if err := json.Unmarshal([]byte(trimmed), &m); err == nil && strings.TrimSpace(m.ID) != "" {
		return Marker{ID: strings.TrimSpace(m.ID), Name: strings.TrimSpace(m.Name)}
	}
	return Marker{ID: trimmed}
}

func ReadFromContenoxDir(contenoxDir string) (Marker, bool) {
	data, err := os.ReadFile(markerPath(contenoxDir))
	if err != nil {
		return Marker{}, false
	}
	return parseMarker(data), true
}

func ReadFromProjectRoot(projectRoot string) (Marker, bool) {
	return ReadFromContenoxDir(contenoxDirOf(projectRoot))
}

func HasMarker(projectRoot string) bool {
	_, ok := ReadFromProjectRoot(projectRoot)
	return ok
}

func DisplayName(projectRoot string) string {
	if name := MarkerName(projectRoot); name != "" {
		return name
	}
	return filepath.Base(strings.TrimRight(projectRoot, string(filepath.Separator)))
}

func MarkerName(projectRoot string) string {
	if m, ok := ReadFromProjectRoot(projectRoot); ok {
		return m.Name
	}
	return ""
}

func Register(projectRoot, name string) (Marker, error) {
	if strings.TrimSpace(name) == "" {
		if m, ok := ReadFromProjectRoot(projectRoot); ok && m.Name != "" {
			return m, nil
		}
		name = filepath.Base(strings.TrimRight(projectRoot, string(filepath.Separator)))
	}
	return EnsureInProjectRoot(projectRoot, name)
}

func NormalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	if n := utf8.RuneCountInString(name); n > MaxNameLen {
		return "", fmt.Errorf("project name is too long (%d characters, max %d)", n, MaxNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("project name must not contain control characters")
		}
	}
	return name, nil
}

func EnsureInContenoxDir(contenoxDir, name string) (Marker, error) {
	name = strings.TrimSpace(name)
	if m, ok := ReadFromContenoxDir(contenoxDir); ok {
		if name != "" && m.Name != name {
			m.Name = name
			if err := writeMarker(contenoxDir, m); err != nil {
				return Marker{}, err
			}
		}
		return m, nil
	}
	if err := os.MkdirAll(contenoxDir, 0o750); err != nil {
		return Marker{}, err
	}
	m := Marker{ID: uuid.NewString(), Name: name}
	if err := writeMarker(contenoxDir, m); err != nil {
		return Marker{}, err
	}
	return m, nil
}

func EnsureInProjectRoot(projectRoot, name string) (Marker, error) {
	return EnsureInContenoxDir(contenoxDirOf(projectRoot), name)
}

func writeMarker(contenoxDir string, m Marker) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(contenoxDir), append(data, '\n'), 0o644)
}
