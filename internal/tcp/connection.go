package tcp

import (
	"fmt"
	"net"
	"strconv"
)

// State is a TCP connection state from MIB_TCP_STATE_*.
type State uint32

const (
	StateClosed State = iota + 1
	StateListen
	StateSynSent
	StateSynReceived
	StateEstablished
	StateFinWait1
	StateFinWait2
	StateCloseWait
	StateClosing
	StateLastAck
	StateTimeWait
	StateDeleteTCB
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateListen:
		return "LISTEN"
	case StateSynSent:
		return "SYN_SENT"
	case StateSynReceived:
		return "SYN_RECEIVED"
	case StateEstablished:
		return "ESTABLISHED"
	case StateFinWait1:
		return "FIN_WAIT1"
	case StateFinWait2:
		return "FIN_WAIT2"
	case StateCloseWait:
		return "CLOSE_WAIT"
	case StateClosing:
		return "CLOSING"
	case StateLastAck:
		return "LAST_ACK"
	case StateTimeWait:
		return "TIME_WAIT"
	case StateDeleteTCB:
		return "DELETE_TCB"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", uint32(s))
	}
}

// Family is the IP family of a connection.
type Family uint8

const (
	IPv4 Family = iota
	IPv6
)

func (f Family) String() string {
	if f == IPv6 {
		return "IPv6"
	}
	return "IPv4"
}

// Connection is a TCP connection owned by a process.
type Connection struct {
	ProcessName string
	PID         uint32
	Family      Family
	LocalAddr   net.IP
	LocalPort   uint16
	RemoteAddr  net.IP
	RemotePort  uint16
	State       State

	// Raw fields in Windows MIB layout (needed for SetTcpEntry, IPv4 only).
	rawLocalAddr  uint32
	rawLocalPort  uint32
	rawRemoteAddr uint32
	rawRemotePort uint32
}

func (c Connection) String() string {
	return fmt.Sprintf("%s -> %s [%s] pid=%d (%s) %s",
		endpoint(c.LocalAddr, c.LocalPort),
		endpoint(c.RemoteAddr, c.RemotePort),
		c.State,
		c.PID, c.ProcessName,
		c.Family,
	)
}

func endpoint(ip net.IP, port uint16) string {
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))
}

// IsRemoteZero reports whether the remote endpoint is unset (listener / unbound).
func (c Connection) IsRemoteZero() bool {
	return c.RemoteAddr == nil || c.RemoteAddr.IsUnspecified()
}

// CanDelete reports whether the connection is a candidate for a DELETE_TCB
// close: it has a remote endpoint and is in a live state. Ghost CLOSED rows,
// listeners and TIME_WAIT entries are not worth attempting to delete.
func (c Connection) CanDelete() bool {
	if c.IsRemoteZero() {
		return false
	}
	switch c.State {
	case StateClosed, StateListen, StateTimeWait:
		return false
	default:
		return true
	}
}

// Key identifies the connection 5-tuple, used to detect survivors between
// table snapshots.
func (c Connection) Key() string {
	return fmt.Sprintf("%d|%s|%d|%s|%d",
		c.Family,
		c.LocalAddr, c.LocalPort,
		c.RemoteAddr, c.RemotePort,
	)
}
