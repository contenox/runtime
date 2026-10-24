//go:build !linux

package libsandbox

import "fmt"

func Preflight() error {
	return fmt.Errorf("%w: OS-level agent confinement is implemented only on Linux; "+
		"the agent sandbox cannot be built on this host", ErrIsolation)
}
