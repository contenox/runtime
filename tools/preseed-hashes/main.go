package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/contenox/runtime/runtime/agentdecl"
)

const (
	modulePath   = "github.com/contenox/runtime"
	packageDir   = "runtime/agentdecl"
	tableFile    = "preseed_blessed.go"
	tableVarName = "blessedPreseedHashes"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "preseed-hashes: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findModuleRoot()
	if err != nil {
		return err
	}
	pkgDir := filepath.Join(root, filepath.FromSlash(packageDir))
	tablePath := filepath.Join(pkgDir, tableFile)

	table, err := readTable(tablePath)
	if err != nil {
		return err
	}

	sources, err := agentdecl.PreseedSources()
	if err != nil {
		return fmt.Errorf("list preseed sources: %w", err)
	}
	if len(sources) == 0 {
		return fmt.Errorf("no preseed sources found")
	}

	next := make(map[string][]string, len(sources))
	added := 0
	for _, source := range sources {
		sourcePath := filepath.Join(pkgDir, filepath.FromSlash(source.Source))
		shipped, err := hashFile(sourcePath)
		if err != nil {
			return err
		}
		gitPath := filepath.ToSlash(filepath.Join(packageDir, filepath.FromSlash(source.Source)))
		history, err := gitHashes(root, gitPath)
		if err != nil {
			return err
		}

		seen := make(map[string]bool)
		var hashes []string
		add := func(hash string) {
			if hash == "" || seen[hash] {
				return
			}
			seen[hash] = true
			hashes = append(hashes, hash)
		}
		for _, hash := range table[source.Rel] {
			add(hash)
		}
		before := len(hashes)
		for _, hash := range history {
			add(hash)
		}
		add(shipped)
		added += len(hashes) - before
		next[source.Rel] = hashes
	}

	if err := writeTable(tablePath, next); err != nil {
		return err
	}
	fmt.Printf("preseed-hashes: %d declarations, %d new hashes -> %s\n", len(next), added, tablePath)
	return nil
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		mod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(mod); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) == "module "+modulePath {
					return dir, nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod for %s above %s", modulePath, dir)
		}
		dir = parent
	}
}

func readTable(path string) (map[string][]string, error) {
	table := map[string][]string{}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if os.IsNotExist(err) {
		return table, nil
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if name.Name != tableVarName || i >= len(spec.Values) {
				continue
			}
			literal, ok := spec.Values[i].(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, element := range literal.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.BasicLit)
				if !ok || key.Kind != token.STRING {
					continue
				}
				rel, err := strconv.Unquote(key.Value)
				if err != nil {
					continue
				}
				values, ok := kv.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, value := range values.Elts {
					hash, ok := value.(*ast.BasicLit)
					if !ok || hash.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(hash.Value)
					if err == nil {
						table[rel] = append(table[rel], unquoted)
					}
				}
				found = true
			}
		}
		return true
	})
	if !found {
		return nil, fmt.Errorf("%s: %s table not found", path, tableVarName)
	}
	return table, nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return hashBytes(data), nil
}

func gitHashes(root, gitPath string) ([]string, error) {
	log := exec.Command("git", "-C", root, "log", "--all", "--format=%H", "--", gitPath)
	out, err := log.Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", gitPath, err)
	}
	commits := strings.Fields(string(out))
	hashes := make([]string, 0, len(commits))
	for _, commit := range commits {
		show := exec.Command("git", "-C", root, "show", commit+":"+gitPath)
		blob, err := show.Output()
		if err != nil {
			return nil, fmt.Errorf("git show %s:%s: %w", commit, gitPath, err)
		}
		hashes = append(hashes, hashBytes(blob))
	}
	return hashes, nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeTable(path string, table map[string][]string) error {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("package agentdecl\n\n")
	buf.WriteString("var " + tableVarName + " = map[string][]string{\n")
	for _, key := range keys {
		fmt.Fprintf(&buf, "\t%q: {\n", key)
		for _, hash := range table[key] {
			fmt.Fprintf(&buf, "\t\t%q,\n", hash)
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format generated table: %w", err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
