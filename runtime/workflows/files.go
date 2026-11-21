// Package workflows runs project-owned JavaScript against a narrow document API.
package workflows

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/google/uuid"
)

const maxDocument = 512 << 10
const maxPDF = 4 << 20

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var ErrConflict = errors.New("workflow conflict")

type Definition struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func documentPath(name string) (string, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") || strings.HasPrefix(strings.ToLower(name), "workflows/") {
		return "", fmt.Errorf("invalid document path %q", name)
	}
	for _, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") {
			return "", fmt.Errorf("hidden document path %q", name)
		}
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".html", ".txt", ".csv", ".pdf":
		return filepath.Localize(name)
	}
	return "", fmt.Errorf("unsupported document type %q", name)
}

func readBytes(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readRegular(f, name, limit)
}

func readDocument(parent *os.Root, leaf string, limit int64) ([]byte, error) {
	st, err := parent.Lstat(leaf)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular document", leaf)
	}
	f, err := parent.Open(leaf)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(st, actual) {
		return nil, fmt.Errorf("%w: document %s changed", ErrConflict, leaf)
	}
	return readRegular(f, leaf, limit)
}

func readRegular(f *os.File, name string, limit int64) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file under %d bytes", name, limit)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s exceeds size limit", name)
	}
	return b, err
}

// replace uses a sibling file to keep each individual file complete across a crash.
func replace(root *os.Root, name string, data []byte) error {
	tmp, err := stageFile(root, name, data)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	return root.Rename(tmp, name)
}

func stageFile(root *os.Root, name string, data []byte) (string, error) {
	mode := fs.FileMode(0644)
	if st, err := root.Lstat(name); err == nil {
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("cannot replace %s", name)
		}
		mode = st.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(name), ".contenox-"+uuid.NewString()+".tmp")
	f, err := root.OpenFile(tmp, os.O_EXCL|os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		root.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func Definitions(dir string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), "workflows")
	names := []string{}
	if errors.Is(err, fs.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		n, ok := strings.CutSuffix(entry.Name(), ".js")
		if ok && validName.MatchString(n) && entry.Type().IsRegular() {
			names = append(names, n)
		}
	}
	return names, nil
}
func Load(dir, name string) (Definition, error) {
	if !validName.MatchString(name) {
		return Definition{}, errors.New("invalid workflow name")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Definition{}, err
	}
	defer root.Close()
	b, err := readBytes(root, filepath.Join("workflows", name+".js"), 64<<10)
	if err != nil {
		return Definition{}, err
	}
	return Definition{Name: name, Source: string(b), Revision: digest(b)}, nil
}
func compile(name, source string) (*goja.Program, error) {
	if len(source) > 64<<10 || !utf8.ValidString(source) {
		return nil, errors.New("source exceeds 64 KiB or contains invalid text")
	}
	return goja.Compile(name+".js", source+"\n;if (typeof run !== 'function') throw Error('Define function run(event)');\n", true)
}
func (s *Service) Save(dir string, d Definition) (Definition, error) {
	if !validName.MatchString(d.Name) {
		return Definition{}, errors.New("use letters, digits, underscores or hyphens for the workflow name")
	}
	if _, err := compile(d.Name, d.Source); err != nil {
		return Definition{}, err
	}
	s.edits.Lock()
	defer s.edits.Unlock()
	current, err := Load(dir, d.Name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Definition{}, err
	}
	if current.Revision != d.Revision {
		return Definition{}, fmt.Errorf("%w: source changed, reopen the editor", ErrConflict)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Definition{}, err
	}
	defer root.Close()
	if err = root.MkdirAll("workflows", 0755); err != nil {
		return Definition{}, err
	}
	if err = replace(root, filepath.Join("workflows", d.Name+".js"), []byte(d.Source)); err != nil {
		return Definition{}, err
	}
	d.Revision = digest([]byte(d.Source))
	return d, nil
}
