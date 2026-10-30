package hitlservice

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type TrustedBinaries struct {
	Dirs   []string          `json:"dirs,omitempty"`
	Hashes map[string]string `json:"hashes,omitempty"`
}

const (
	maxTrustedDirs        = 256
	maxTrustedHashes      = 8192
	maxTrustedEntryBytes  = 4096
	sha256HexLen          = 64
	maxHashCacheEntries   = 1024
	trustedBinaryHashAlgo = "sha256"
)

const (
	trustRefusalUnresolved  = "command %q does not resolve to an executable on this host — allow refused"
	trustRefusalRelative    = "command %q is a relative path the policy cannot resolve — allow refused; name the command or give it an absolute path"
	trustRefusalOutsideDirs = "binary at %s is not under any trusted_binaries.dirs entry — allow refused; declare its directory after verifying what it is"
	trustRefusalUndeclared  = "binary at %s has no declared hash — allow refused; declare it with `contenox-cli hitl trust %s`"
	trustRefusalUnreadable  = "binary at %s could not be read for hashing (%v) — allow refused"
	trustRefusalMismatch    = "binary at %s does not match the declared hash — re-declare after verifying the upgrade, or investigate"
)

var errRelativeCommand = errors.New("relative command path")

func (tb *TrustedBinaries) enforced() bool {
	return tb != nil && (len(tb.Dirs) > 0 || len(tb.Hashes) > 0)
}

func (tb *TrustedBinaries) verifyCommand(name string) string {
	real, err := resolveBinary(name)
	if err != nil {
		if errors.Is(err, errRelativeCommand) {
			return fmt.Sprintf(trustRefusalRelative, name)
		}
		return fmt.Sprintf(trustRefusalUnresolved, name)
	}
	if len(tb.Dirs) > 0 && !underAnyTrustedDir(real, tb.Dirs) {
		return fmt.Sprintf(trustRefusalOutsideDirs, real)
	}
	if len(tb.Hashes) == 0 {
		return ""
	}
	want, ok := lookupDeclaredHash(tb.Hashes, real)
	if !ok {
		return fmt.Sprintf(trustRefusalUndeclared, real, real)
	}
	got, err := binarySHA256(real)
	if err != nil {
		return fmt.Sprintf(trustRefusalUnreadable, real, err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Sprintf(trustRefusalMismatch, real)
	}
	return ""
}

func resolveBinary(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("empty command")
	}
	var candidate string
	switch {
	case filepath.IsAbs(name):
		candidate = name
	case commandHasPathSeparator(name):
		return "", errRelativeCommand
	default:
		p, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		candidate = p
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

func commandHasPathSeparator(name string) bool {
	if strings.ContainsRune(name, '/') {
		return true
	}
	return runtime.GOOS == "windows" && strings.ContainsRune(name, '\\')
}

func pathKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

func underAnyTrustedDir(real string, dirs []string) bool {
	target := pathKey(real)
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		prefix := pathKey(dir)
		if prefix == "" {
			continue
		}
		if !strings.HasSuffix(prefix, string(filepath.Separator)) {
			prefix += string(filepath.Separator)
		}
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

func lookupDeclaredHash(hashes map[string]string, real string) (string, bool) {
	if h, ok := hashes[real]; ok {
		return strings.TrimSpace(h), true
	}
	key := pathKey(real)
	for declared, h := range hashes {
		if pathKey(declared) == key {
			return strings.TrimSpace(h), true
		}
	}
	return "", false
}

type hashCacheEntry struct {
	size    int64
	modTime time.Time
	sum     string
}

var (
	hashCacheMu sync.Mutex
	hashCache   = map[string]hashCacheEntry{}
)

func binarySHA256(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	key := pathKey(path)
	hashCacheMu.Lock()
	entry, ok := hashCache[key]
	hashCacheMu.Unlock()
	if ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.sum, nil
	}
	sum, err := hashFileSHA256(path)
	if err != nil {
		return "", err
	}
	hashCacheMu.Lock()
	if len(hashCache) >= maxHashCacheEntries {
		hashCache = map[string]hashCacheEntry{}
	}
	hashCache[key] = hashCacheEntry{size: info.Size(), modTime: info.ModTime(), sum: sum}
	hashCacheMu.Unlock()
	return sum, nil
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func ResolveTrustedBinary(name string) (realPath, sha256Hex string, err error) {
	real, err := resolveBinary(name)
	if err != nil {
		return "", "", err
	}
	sum, err := hashFileSHA256(real)
	if err != nil {
		return "", "", err
	}
	return real, sum, nil
}

func validateTrustedBinaries(tb *TrustedBinaries) error {
	if tb == nil {
		return nil
	}
	if len(tb.Dirs) > maxTrustedDirs {
		return fmt.Errorf("trusted_binaries: too many dirs (%d, max %d)", len(tb.Dirs), maxTrustedDirs)
	}
	for i, dir := range tb.Dirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			return fmt.Errorf("trusted_binaries: dirs entry %d is empty", i)
		}
		if len(trimmed) > maxTrustedEntryBytes {
			return fmt.Errorf("trusted_binaries: dirs entry %d exceeds max length (%d bytes, max %d)", i, len(trimmed), maxTrustedEntryBytes)
		}
		if !filepath.IsAbs(trimmed) {
			return fmt.Errorf("trusted_binaries: dirs entry %d %q must be an absolute path — a relative directory has no fixed meaning at evaluation time", i, trimmed)
		}
	}
	if len(tb.Hashes) > maxTrustedHashes {
		return fmt.Errorf("trusted_binaries: too many hashes (%d, max %d)", len(tb.Hashes), maxTrustedHashes)
	}
	for _, path := range sortedKeys(tb.Hashes) {
		if strings.TrimSpace(path) == "" {
			return errors.New("trusted_binaries: hashes has an empty path key")
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("trusted_binaries: hashes key %q must be an absolute path — declare the real path the command resolves to", path)
		}
		if err := validateSHA256Hex(path, tb.Hashes[path]); err != nil {
			return err
		}
	}
	return nil
}

func validateSHA256Hex(path, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return fmt.Errorf("trusted_binaries: hashes[%q] is empty — declare a %s digest or remove the entry", path, trustedBinaryHashAlgo)
	}
	if len(v) != sha256HexLen {
		return fmt.Errorf("trusted_binaries: hashes[%q] is not a %s digest (%d hex characters, expected %d)", path, trustedBinaryHashAlgo, len(v), sha256HexLen)
	}
	if _, err := hex.DecodeString(v); err != nil {
		return fmt.Errorf("trusted_binaries: hashes[%q] is not hexadecimal: %w", path, err)
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type TrustedBinaryStatus struct {
	Path   string `json:"path"`
	State  string `json:"state"`
	Actual string `json:"actual,omitempty"`
	Detail string `json:"detail,omitempty"`
}

const (
	TrustedBinaryOK          = "ok"
	TrustedBinaryMissing     = "missing"
	TrustedBinaryMismatch    = "mismatch"
	TrustedBinaryUnreadable  = "unreadable"
	TrustedBinaryOutsideDirs = "outside_dirs"
)

func CheckTrustedBinaries(tb *TrustedBinaries) []TrustedBinaryStatus {
	if tb == nil || len(tb.Hashes) == 0 {
		return nil
	}
	var out []TrustedBinaryStatus
	for _, path := range sortedKeys(tb.Hashes) {
		want := strings.TrimSpace(tb.Hashes[path])
		if _, err := os.Stat(path); err != nil {
			out = append(out, TrustedBinaryStatus{Path: path, State: TrustedBinaryMissing, Detail: err.Error()})
			continue
		}
		if len(tb.Dirs) > 0 && !underAnyTrustedDir(path, tb.Dirs) {
			out = append(out, TrustedBinaryStatus{Path: path, State: TrustedBinaryOutsideDirs})
			continue
		}
		got, err := hashFileSHA256(path)
		if err != nil {
			out = append(out, TrustedBinaryStatus{Path: path, State: TrustedBinaryUnreadable, Detail: err.Error()})
			continue
		}
		if !strings.EqualFold(got, want) {
			out = append(out, TrustedBinaryStatus{Path: path, State: TrustedBinaryMismatch, Actual: got})
		}
	}
	return out
}

func (s TrustedBinaryStatus) String() string {
	switch s.State {
	case TrustedBinaryMissing:
		return fmt.Sprintf("%s: declared but not present on this host — remove the entry or restore the binary", s.Path)
	case TrustedBinaryMismatch:
		return fmt.Sprintf("%s: does not match the declared hash (on disk %s) — re-declare after verifying the upgrade, or investigate", s.Path, s.Actual)
	case TrustedBinaryUnreadable:
		return fmt.Sprintf("%s: declared but unreadable (%s) — calls naming it will be refused", s.Path, s.Detail)
	case TrustedBinaryOutsideDirs:
		return fmt.Sprintf("%s: declared outside every trusted_binaries.dirs entry — its hash can never be reached", s.Path)
	default:
		return fmt.Sprintf("%s: %s", s.Path, s.State)
	}
}
