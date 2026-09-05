package tcp

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSetTcpEntry = modIphlpapi.NewProc("SetTcpEntry")

var (
	// ErrUnsupportedFamily is returned for connections SetTcpEntry cannot
	// address: it only works on the IPv4 TCP table.
	ErrUnsupportedFamily = errors.New("SetTcpEntry supports IPv4 connections only")
	// ErrAccessDenied means the caller is not elevated.
	ErrAccessDenied = errors.New("access denied — run as administrator")
	// ErrVanished means the control block was already gone by the time of the
	// call. The game churns connections constantly, so a row can disappear
	// between listing and closing; the final verification pass catches real
	// failures, so this is treated as benign.
	ErrVanished = errors.New("connection already gone")
)

type mibTCPRow struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
}

// CloseResult is the outcome of closing one connection.
type CloseResult struct {
	Connection Connection
	Err        error
}

// Close forcibly deletes the TCP control block via SetTcpEntry(DELETE_TCB).
// Requires elevated privileges on modern Windows.
func Close(c Connection) error {
	if c.Family != IPv4 {
		return ErrUnsupportedFamily
	}
	if err := modIphlpapi.Load(); err != nil {
		return fmt.Errorf("load iphlpapi: %w", err)
	}

	row := mibTCPRow{
		State:      uint32(StateDeleteTCB),
		LocalAddr:  c.rawLocalAddr,
		LocalPort:  c.rawLocalPort,
		RemoteAddr: c.rawRemoteAddr,
		RemotePort: c.rawRemotePort,
	}

	r0, _, _ := procSetTcpEntry.Call(uintptr(unsafe.Pointer(&row)))
	if r0 != 0 {
		return classifyErrno(windows.Errno(r0), c)
	}
	return nil
}

func classifyErrno(errno windows.Errno, c Connection) error {
	switch errno {
	case windows.ERROR_ACCESS_DENIED:
		return fmt.Errorf("SetTcpEntry %s: %w", c, ErrAccessDenied)
	case windows.ERROR_INVALID_PARAMETER, windows.ERROR_NOT_FOUND:
		return fmt.Errorf("SetTcpEntry %s: %w", c, ErrVanished)
	default:
		return fmt.Errorf("SetTcpEntry %s: %w", c, errno)
	}
}

// CloseAll closes each connection, optionally pausing between calls.
func CloseAll(conns []Connection, pause time.Duration) []CloseResult {
	results := make([]CloseResult, 0, len(conns))
	for i, c := range conns {
		err := Close(c)
		results = append(results, CloseResult{Connection: c, Err: err})
		if pause > 0 && i < len(conns)-1 {
			time.Sleep(pause)
		}
	}
	return results
}
