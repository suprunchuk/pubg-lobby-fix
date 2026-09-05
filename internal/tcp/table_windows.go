package tcp

import (
	"encoding/binary"
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	afInet                = 2
	afInet6               = 23
	tcpTableOwnerPIDAll   = 5
	errInsufficientBuffer = 122 // ERROR_INSUFFICIENT_BUFFER
)

var (
	modIphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

// MIB_TCP6ROW_OWNER_PID: field order differs from the IPv4 row — the state
// comes after the endpoints.
type mibTCP6RowOwnerPID struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    uint32
	State         uint32
	OwningPID     uint32
}

// ListByPIDs returns IPv4 and IPv6 TCP connections owned by any of the given
// PIDs. Rows from a family whose table query failed are still returned; the
// per-family errors come back in errs (empty if every query succeeded).
func ListByPIDs(pids map[uint32]string) (conns []Connection, errs []error) {
	if len(pids) == 0 {
		return nil, nil
	}

	if buf, err := extendedTCPTable(afInet); err != nil {
		errs = append(errs, fmt.Errorf("ipv4 tcp table: %w", err))
	} else {
		for _, row := range parseV4Table(buf) {
			if name, ok := pids[row.OwningPID]; ok {
				conns = append(conns, connectionFromV4Row(row, name))
			}
		}
	}

	if buf, err := extendedTCPTable(afInet6); err != nil {
		errs = append(errs, fmt.Errorf("ipv6 tcp table: %w", err))
	} else {
		for _, row := range parseV6Table(buf) {
			if name, ok := pids[row.OwningPID]; ok {
				conns = append(conns, connectionFromV6Row(row, name))
			}
		}
	}

	return conns, errs
}

func extendedTCPTable(family uint32) ([]byte, error) {
	if err := modIphlpapi.Load(); err != nil {
		return nil, fmt.Errorf("load iphlpapi: %w", err)
	}

	var size uint32
	r0, _, _ := procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		1, // sort
		uintptr(family),
		tcpTableOwnerPIDAll,
		0,
	)
	if windows.Errno(r0) != errInsufficientBuffer && r0 != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable size: %w", windows.Errno(r0))
	}
	if size == 0 {
		return nil, nil
	}

	buf := make([]byte, size)
	r0, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		1,
		uintptr(family),
		tcpTableOwnerPIDAll,
		0,
	)
	if r0 != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.Errno(r0))
	}
	return buf, nil
}

func parseV4Table(buf []byte) []mibTCPRowOwnerPID {
	const rowSize = uint32(unsafe.Sizeof(mibTCPRowOwnerPID{}))
	numEntries, ok := tableEntries(buf, rowSize)
	if !ok {
		return nil
	}
	rows := make([]mibTCPRowOwnerPID, 0, numEntries)
	offset := uint32(4)
	for range numEntries {
		rows = append(rows, *(*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[offset])))
		offset += rowSize
	}
	return rows
}

func parseV6Table(buf []byte) []mibTCP6RowOwnerPID {
	const rowSize = uint32(unsafe.Sizeof(mibTCP6RowOwnerPID{}))
	numEntries, ok := tableEntries(buf, rowSize)
	if !ok {
		return nil
	}
	rows := make([]mibTCP6RowOwnerPID, 0, numEntries)
	offset := uint32(4)
	for range numEntries {
		rows = append(rows, *(*mibTCP6RowOwnerPID)(unsafe.Pointer(&buf[offset])))
		offset += rowSize
	}
	return rows
}

// tableEntries validates a GetExtendedTcpTable buffer: a DWORD entry count
// followed by fixed-size rows.
func tableEntries(buf []byte, rowSize uint32) (numEntries uint32, ok bool) {
	if len(buf) < 4 {
		return 0, false
	}
	numEntries = binary.LittleEndian.Uint32(buf[:4])
	if uint64(len(buf)) < 4+uint64(numEntries)*uint64(rowSize) {
		return 0, false
	}
	return numEntries, true
}

func connectionFromV4Row(row mibTCPRowOwnerPID, processName string) Connection {
	return Connection{
		ProcessName: processName,
		PID:         row.OwningPID,
		Family:      IPv4,
		LocalAddr:   ipv4FromDWORD(row.LocalAddr),
		LocalPort:   ntohs(uint16(row.LocalPort & 0xffff)),
		RemoteAddr:  ipv4FromDWORD(row.RemoteAddr),
		RemotePort:  ntohs(uint16(row.RemotePort & 0xffff)),
		State:       State(row.State),

		rawLocalAddr:  row.LocalAddr,
		rawLocalPort:  row.LocalPort,
		rawRemoteAddr: row.RemoteAddr,
		rawRemotePort: row.RemotePort,
	}
}

func connectionFromV6Row(row mibTCP6RowOwnerPID, processName string) Connection {
	return Connection{
		ProcessName: processName,
		PID:         row.OwningPID,
		Family:      IPv6,
		LocalAddr:   net.IP(append([]byte(nil), row.LocalAddr[:]...)),
		LocalPort:   ntohs(uint16(row.LocalPort & 0xffff)),
		RemoteAddr:  net.IP(append([]byte(nil), row.RemoteAddr[:]...)),
		RemotePort:  ntohs(uint16(row.RemotePort & 0xffff)),
		State:       State(row.State),
	}
}

func ipv4FromDWORD(v uint32) net.IP {
	b := make(net.IP, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func ntohs(v uint16) uint16 {
	return (v>>8)&0xff | (v&0xff)<<8
}
