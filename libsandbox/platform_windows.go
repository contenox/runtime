//go:build windows

package libsandbox

import (
	"os"
	"path/filepath"
	"strings"
)

const envNamesFold = true

const pathsFold = true

func platformEnvAllow() []string {
	return []string{
		"SystemRoot",
		"SystemDrive",
		"windir",
		"ComSpec",
		"PATHEXT",
		"OS",
		"USERPROFILE",
		"USERNAME",
		"USERDOMAIN",
		"COMPUTERNAME",
		"HOMEDRIVE",
		"HOMEPATH",
		"APPDATA",
		"LOCALAPPDATA",
		"ProgramData",
		"ProgramFiles",
		"ProgramFiles(x86)",
		"ProgramW6432",
		"CommonProgramFiles",
		"CommonProgramFiles(x86)",
		"CommonProgramW6432",
		"PUBLIC",
		"ALLUSERSPROFILE",
		"NUMBER_OF_PROCESSORS",
		"PROCESSOR_ARCHITECTURE",
		"PROCESSOR_IDENTIFIER",
		"PROCESSOR_LEVEL",
		"PROCESSOR_REVISION",
		"PSModulePath",
		"TEMP",
		"TMP",
	}
}

func platformExecDirs() []string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	dirs := []string{
		filepath.Join(root, "System32"),
		root,
		filepath.Join(root, "System32", "Wbem"),
		filepath.Join(root, "System32", "WindowsPowerShell", "v1.0"),
		filepath.Join(root, "System32", "OpenSSH"),
	}
	seen := map[string]struct{}{}
	for _, name := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(v))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		dirs = append(dirs, filepath.Clean(v))
	}
	return dirs
}
