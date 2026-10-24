//go:build linux

package fleetservice

import (
	"fmt"
	"os"
	"testing"

	"github.com/contenox/runtime/libsandbox"
)

func TestMain(m *testing.M) {
	if handled, err := libsandbox.ShimMain(); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "fleetservice sandbox shim:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
