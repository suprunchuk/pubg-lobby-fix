package tcp

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSetTcpEntry = modIphlpapi.NewProc("SetTcpEntry")

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
		return fmt.Errorf("SetTcpEntry %s: %w", c, windows.Errno(r0))
	}
	return nil
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
