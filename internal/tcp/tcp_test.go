package tcp

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNTonHS(t *testing.T) {
	if got := ntohs(0xBB01); got != 443 {
		t.Fatalf("ntohs(0xBB01) = %d, want 443", got)
	}
	if got := ntohs(443); got != 0xBB01 {
		t.Fatalf("ntohs(443) = %#x, want 0x1bb", got)
	}
}

func TestParseV4Table(t *testing.T) {
	rows := []mibTCPRowOwnerPID{
		{
			State:      uint32(StateEstablished),
			LocalAddr:  0x0100007f, // 127.0.0.1 (little-endian DWORD)
			RemoteAddr: 0x0100A8C0, // 192.168.0.1
			OwningPID:  4242,
		},
	}
	// ports arrive in network byte order in the low 16 bits: 443 = 0x0000BB01
	rows[0].LocalPort = 0x0000BB01
	rows[0].RemotePort = 0x00005000 // 80

	buf := make([]byte, 4+len(rows)*int(unsafe.Sizeof(mibTCPRowOwnerPID{})))
	binary.LittleEndian.PutUint32(buf, uint32(len(rows)))
	copy(buf[4:], unsafe.Slice((*byte)(unsafe.Pointer(&rows[0])), int(unsafe.Sizeof(mibTCPRowOwnerPID{}))*len(rows)))

	got := parseV4Table(buf)
	if len(got) != 1 {
		t.Fatalf("parsed %d rows, want 1", len(got))
	}
	c := connectionFromV4Row(got[0], "TslGame.exe")
	if c.Family != IPv4 {
		t.Errorf("family = %v, want IPv4", c.Family)
	}
	if c.LocalPort != 443 || c.RemotePort != 80 {
		t.Errorf("ports = %d/%d, want 443/80", c.LocalPort, c.RemotePort)
	}
	if c.LocalAddr.String() != "127.0.0.1" || c.RemoteAddr.String() != "192.168.0.1" {
		t.Errorf("addrs = %s/%s", c.LocalAddr, c.RemoteAddr)
	}
	if c.PID != 4242 {
		t.Errorf("pid = %d, want 4242", c.PID)
	}
	if c.State != StateEstablished {
		t.Errorf("state = %v, want ESTABLISHED", c.State)
	}
}

func TestParseV6Table(t *testing.T) {
	rows := []mibTCP6RowOwnerPID{
		{
			LocalAddr:  [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 127, 0, 0, 1},
			LocalPort:  0x0000BB01, // 443, network byte order in low 16 bits
			RemoteAddr: [16]byte{0x20, 0x01, 0x0d, 0xb8, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			RemotePort: 0x00005000, // 80
			State:      uint32(StateEstablished),
			OwningPID:  7,
		},
	}
	buf := make([]byte, 4+len(rows)*int(unsafe.Sizeof(mibTCP6RowOwnerPID{})))
	binary.LittleEndian.PutUint32(buf, uint32(len(rows)))
	copy(buf[4:], unsafe.Slice((*byte)(unsafe.Pointer(&rows[0])), int(unsafe.Sizeof(mibTCP6RowOwnerPID{}))*len(rows)))

	got := parseV6Table(buf)
	if len(got) != 1 {
		t.Fatalf("parsed %d rows, want 1", len(got))
	}
	c := connectionFromV6Row(got[0], "TslGame.exe")
	if c.Family != IPv6 {
		t.Errorf("family = %v, want IPv6", c.Family)
	}
	if c.LocalAddr.String() != "127.0.0.1" {
		t.Errorf("local addr = %s, want 127.0.0.1 (v4-mapped)", c.LocalAddr)
	}
	if c.RemoteAddr.String() != "2001:db8:102:304:506:708:90a:b0c" {
		t.Errorf("remote addr = %s", c.RemoteAddr)
	}
	if c.LocalPort != 443 || c.RemotePort != 80 {
		t.Errorf("ports = %d/%d, want 443/80", c.LocalPort, c.RemotePort)
	}
	if c.PID != 7 {
		t.Errorf("pid = %d, want 7", c.PID)
	}
}

func TestParseTableRejectsTruncatedBuffers(t *testing.T) {
	if got := parseV4Table([]byte{1}); len(got) != 0 {
		t.Errorf("v4: expected 0 rows for a truncated buffer, got %d", len(got))
	}
	if got := parseV6Table(nil); len(got) != 0 {
		t.Errorf("v6: expected 0 rows for an empty buffer, got %d", len(got))
	}
	// count says 2 rows, buffer has room for one
	buf := make([]byte, 4+int(unsafe.Sizeof(mibTCPRowOwnerPID{})))
	binary.LittleEndian.PutUint32(buf, 2)
	if got := parseV4Table(buf); len(got) != 0 {
		t.Errorf("v4: expected 0 rows when the buffer is too small for the count, got %d", len(got))
	}
}

func TestCanDelete(t *testing.T) {
	c := Connection{
		Family:     IPv4,
		LocalAddr:  net.IPv4(127, 0, 0, 1),
		LocalPort:  1234,
		RemoteAddr: net.IPv4(1, 2, 3, 4),
		RemotePort: 80,
	}
	if c.IsRemoteZero() || !c.CanDelete() {
		t.Fatal("established connection with a remote endpoint should be deletable")
	}

	listener := c
	listener.RemoteAddr = net.IPv4zero
	listener.RemotePort = 0
	if !listener.IsRemoteZero() || listener.CanDelete() {
		t.Fatal("listener should not be deletable")
	}

	tw := c
	tw.State = StateTimeWait
	if tw.CanDelete() {
		t.Fatal("TIME_WAIT should not be deletable")
	}

	ghost := c
	ghost.State = StateClosed
	if ghost.CanDelete() {
		t.Fatal("CLOSED ghosts should not be deletable")
	}
}

func TestClassifyErrno(t *testing.T) {
	c := Connection{PID: 1}
	tests := []struct {
		errno windows.Errno
		want  error
	}{
		{windows.ERROR_ACCESS_DENIED, ErrAccessDenied},
		{windows.ERROR_INVALID_PARAMETER, ErrVanished},
		{windows.ERROR_NOT_FOUND, ErrVanished},
	}
	for _, tt := range tests {
		err := classifyErrno(tt.errno, c)
		if !errors.Is(err, tt.want) {
			t.Errorf("classifyErrno(%d) = %v, want wrapping %v", tt.errno, err, tt.want)
		}
	}
	// arbitrary error must not be reclassified
	err := classifyErrno(windows.Errno(9999), c)
	if errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrVanished) {
		t.Errorf("unexpected errno got reclassified: %v", err)
	}
}

func TestKeyDistinguishesTuples(t *testing.T) {
	a := Connection{
		Family:     IPv4,
		LocalAddr:  net.IPv4(127, 0, 0, 1),
		LocalPort:  1234,
		RemoteAddr: net.IPv4(1, 2, 3, 4),
		RemotePort: 80,
	}
	b := a
	if a.Key() != b.Key() {
		t.Fatal("identical tuples must have identical keys")
	}
	b.Family = IPv6
	if a.Key() == b.Key() {
		t.Fatal("family must be part of the key")
	}
	b = a
	b.RemotePort = 81
	if a.Key() == b.Key() {
		t.Fatal("remote port must be part of the key")
	}
}
