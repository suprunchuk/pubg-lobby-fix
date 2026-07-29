package tcp

import (
	"fmt"
	"net"
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

// Connection is a TCP connection owned by a process.
type Connection struct {
	ProcessName string
	PID         uint32
	LocalAddr   net.IP
	LocalPort   uint16
	RemoteAddr  net.IP
	RemotePort  uint16
	State       State

	// Raw fields in Windows MIB layout (needed for SetTcpEntry).
	rawLocalAddr  uint32
	rawLocalPort  uint32
	rawRemoteAddr uint32
	rawRemotePort uint32
}

func (c Connection) String() string {
	return fmt.Sprintf("%s:%d -> %s:%d [%s] pid=%d (%s)",
		c.LocalAddr, c.LocalPort,
		c.RemoteAddr, c.RemotePort,
		c.State,
		c.PID, c.ProcessName,
	)
}

// IsRemoteZero reports whether the remote endpoint is unset (listener / unbound).
func (c Connection) IsRemoteZero() bool {
	return c.RemoteAddr == nil || c.RemoteAddr.IsUnspecified()
}
