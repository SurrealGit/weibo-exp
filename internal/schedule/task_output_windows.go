package schedule

import (
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// schtasks emits console-code-page bytes in some desktop sessions, even with
// an XML UTF-16 declaration. SSH may instead supply UTF-8. BOMs are handled by
// decodeTaskXML before this function is called.
func decodeTaskText(data []byte) []byte {
	if utf8.Valid(data) {
		return data
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	cp, _, _ := kernel.NewProc("GetConsoleOutputCP").Call()
	if cp == 0 {
		cp = 1
	} // CP_OEMCP for a process without an attached console.
	convert := kernel.NewProc("MultiByteToWideChar")
	n, _, _ := convert.Call(cp, 8, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 0, 0)
	if n == 0 {
		return data
	} // Leave invalid XML to the parser; never guess bytes.
	units := make([]uint16, n)
	n, _, _ = convert.Call(cp, 8, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&units[0])), n)
	if n == 0 {
		return data
	}
	return []byte(string(utf16.Decode(units[:n])))
}
