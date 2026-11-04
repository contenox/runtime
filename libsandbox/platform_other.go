//go:build !windows

package libsandbox

const envNamesFold = false

const pathsFold = false

func platformEnvAllow() []string {
	return nil
}

func platformExecDirs() []string {
	return []string{
		"/usr/local/sbin",
		"/usr/local/bin",
		"/usr/sbin",
		"/usr/bin",
		"/sbin",
		"/bin",
	}
}
