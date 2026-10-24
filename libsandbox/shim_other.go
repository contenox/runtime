//go:build !linux

package libsandbox

func ShimMain() (handled bool, err error) {
	return false, nil
}
