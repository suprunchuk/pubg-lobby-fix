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

// ListByPIDs returns IPv4 TCP connections owned by any of the given PIDs.
func ListByPIDs(pids map[uint32]string) ([]Connection, error) {
	if len(pids) == 0 {
		return nil, nil
	}

	rows, err := extendedTCPTable()
	if err != nil {
		return nil, err
	}

	out := make([]Connection, 0, len(rows))
	for _, row := range rows {
		name, ok := pids[row.OwningPID]
		if !ok {
			continue
		}
		out = append(out, connectionFromRow(row, name))
	}
	return out, nil
}

func extendedTCPTable() ([]mibTCPRowOwnerPID, error) {
	if err := modIphlpapi.Load(); err != nil {
		return nil, fmt.Errorf("load iphlpapi: %w", err)
	}

	var size uint32
	r0, _, _ := procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		1, // sort
		afInet,
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
		afInet,
		tcpTableOwnerPIDAll,
		0,
	)
	if r0 != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.Errno(r0))
	}

	numEntries := binary.LittleEndian.Uint32(buf[:4])
	rowSize := unsafe.Sizeof(mibTCPRowOwnerPID{})
	needed := 4 + uintptr(numEntries)*rowSize
	if uintptr(len(buf)) < needed {
		return nil, fmt.Errorf("tcp table buffer too small: have %d need %d", len(buf), needed)
	}

	rows := make([]mibTCPRowOwnerPID, 0, numEntries)
	offset := uintptr(4)
	for i := uint32(0); i < numEntries; i++ {
		row := *(*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[offset]))
		rows = append(rows, row)
		offset += rowSize
	}
	return rows, nil
}

func connectionFromRow(row mibTCPRowOwnerPID, processName string) Connection {
	localPort := ntohs(uint16(row.LocalPort & 0xffff))
	remotePort := ntohs(uint16(row.RemotePort & 0xffff))

	return Connection{
		ProcessName:   processName,
		PID:           row.OwningPID,
		LocalAddr:     ipv4FromDWORD(row.LocalAddr),
		LocalPort:     localPort,
		RemoteAddr:    ipv4FromDWORD(row.RemoteAddr),
		RemotePort:    remotePort,
		State:         State(row.State),
		rawLocalAddr:  row.LocalAddr,
		rawLocalPort:  row.LocalPort,
		rawRemoteAddr: row.RemoteAddr,
		rawRemotePort: row.RemotePort,
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
