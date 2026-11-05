package locale

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const localeNameMaxLength = 85

var procGetUserDefaultLocaleName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")

func platformLocale() string {
	if err := procGetUserDefaultLocaleName.Find(); err != nil {
		return ""
	}
	buf := make([]uint16, localeNameMaxLength)
	n, _, _ := procGetUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}
