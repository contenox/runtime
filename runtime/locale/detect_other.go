//go:build !windows

package locale

func platformLocale() string {
	return ""
}
