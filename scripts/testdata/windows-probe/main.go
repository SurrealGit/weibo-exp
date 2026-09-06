// A local-only executable for native Task Scheduler acceptance.
package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

func main() {
	dir := flag.String("data-dir", "", "probe output directory")
	flag.Parse()
	if *dir == "" || flag.NArg() != 1 || flag.Arg(0) != "scheduled-run" {
		os.Exit(2)
	}
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		panic(err)
	}
	var token syscall.Token
	if err := syscall.OpenProcessToken(process, syscall.TOKEN_QUERY, &token); err != nil {
		panic(err)
	}
	defer token.Close()
	var elevation, size uint32
	getInfo := syscall.NewLazyDLL("advapi32.dll").NewProc("GetTokenInformation")
	ok, _, err := getInfo.Call(uintptr(token), 20, uintptr(unsafe.Pointer(&elevation)), 4, uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		panic(err)
	}
	f, err := os.OpenFile(filepath.Join(*dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(struct {
		Time     time.Time
		Elevated bool
	}{time.Now(), elevation != 0}); err != nil {
		panic(err)
	}
}
