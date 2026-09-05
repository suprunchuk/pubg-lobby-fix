package process

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Process is a matched process with its executable path.
type Process struct {
	PID    uint32
	Name   string
	ExePath string
}

// FindByName returns processes matching any of names (case-insensitive,
// without .exe). ExePath is best effort: it is empty if the image path could
// not be queried.
func FindByName(names ...string) ([]Process, error) {
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".exe")
		if n != "" {
			want[n] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil, errors.New("no process names provided")
	}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, fmt.Errorf("Process32First: %w", err)
	}

	var found []Process
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		base := strings.TrimSuffix(strings.ToLower(name), ".exe")
		if _, ok := want[base]; ok {
			found = append(found, Process{
				PID:     entry.ProcessID,
				Name:    name,
				ExePath: imagePaths(entry.ProcessID),
			})
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return found, nil
}

// imagePaths returns the full image path of pid, empty on failure.
func imagePaths(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()

	size := uint32(windows.MAX_PATH)
	for range 4 {
		buf := make([]uint16, size)
		err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size])
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return ""
		}
		size *= 2
	}
	return ""
}
