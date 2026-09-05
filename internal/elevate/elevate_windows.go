// Package elevate checks administrator rights and relaunches the process
// elevated through UAC. SetTcpEntry is a no-op without elevation, so the
// close logic depends on running elevated.
package elevate

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modShell32       = windows.NewLazySystemDLL("shell32.dll")
	procShellExecute = modShell32.NewProc("ShellExecuteW")
)

// IsElevated reports whether the current process token has administrator
// rights.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RelaunchAsAdmin starts an elevated copy of the current executable with the
// same arguments plus -no-elevate (so the copy never loops back here) and
// returns once the UAC prompt has been accepted. The caller should exit.
func RelaunchAsAdmin(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	params := quoteArgs(args) + " -no-elevate"
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return fmt.Errorf("verb: %w", err)
	}
	exe16, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return fmt.Errorf("executable path: %w", err)
	}
	params16, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return fmt.Errorf("parameters: %w", err)
	}

	const swShownormal = 1
	r, _, _ := procShellExecute.Call(
		0, // hwnd
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(exe16)),
		uintptr(unsafe.Pointer(params16)),
		0, // working directory
		swShownormal,
	)
	if r <= 32 {
		return fmt.Errorf("ShellExecuteW rejected the elevation request (code %d) — the UAC prompt was probably declined", r)
	}
	return nil
}

func quoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		quoted = append(quoted, a)
	}
	return strings.Join(quoted, " ")
}
