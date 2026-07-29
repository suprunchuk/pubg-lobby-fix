package process

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FindByName returns a map of PID → process name for processes matching any of names
// (case-insensitive, without .exe).
func FindByName(names ...string) (map[uint32]string, error) {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".exe")
		if n != "" {
			want[n] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("no process names provided")
	}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, fmt.Errorf("Process32First: %w", err)
	}

	found := make(map[uint32]string)
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		base := strings.TrimSuffix(strings.ToLower(name), ".exe")
		if _, ok := want[base]; ok {
			found[entry.ProcessID] = name
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return found, nil
}
