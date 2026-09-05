package app

import (
	"net"
	"testing"

	"github.com/suprunchuk/pubg-lobby-fix/internal/tcp"
)

func conn(family tcp.Family, lport, rport uint16) tcp.Connection {
	c := tcp.Connection{
		Family:     family,
		LocalAddr:  net.IPv4(127, 0, 0, 1),
		LocalPort:  lport,
		RemoteAddr: net.IPv4(1, 2, 3, 4),
		RemotePort: rport,
	}
	if family == tcp.IPv6 {
		c.LocalAddr = net.IPv6loopback
		c.RemoteAddr = net.ParseIP("2001:db8::1")
	}
	return c
}

func TestIntersect(t *testing.T) {
	targets := []tcp.Connection{conn(tcp.IPv4, 1000, 443), conn(tcp.IPv4, 1001, 443)}
	now := []tcp.Connection{conn(tcp.IPv4, 1000, 443), conn(tcp.IPv4, 2000, 80)} // 1001 died, 2000 is new

	got := intersect(targets, now)
	if len(got) != 1 || got[0].LocalPort != 1000 {
		t.Fatalf("intersect = %+v, want only the surviving :1000 connection", got)
	}

	if got := intersect(targets, nil); len(got) != 0 {
		t.Fatalf("intersect against an empty snapshot must be empty, got %+v", got)
	}
}

func TestIntersectIsFamilyAware(t *testing.T) {
	// Same ports but different family must not match.
	v4 := conn(tcp.IPv4, 1000, 443)
	v6 := conn(tcp.IPv6, 1000, 443)
	if got := intersect([]tcp.Connection{v4}, []tcp.Connection{v6}); len(got) != 0 {
		t.Fatalf("IPv4 and IPv6 tuples must not intersect, got %+v", got)
	}
}

func TestV4OnlyAndAllIPv6(t *testing.T) {
	mixed := []tcp.Connection{conn(tcp.IPv4, 1, 2), conn(tcp.IPv6, 3, 4)}
	if got := v4Only(mixed); len(got) != 1 || got[0].Family != tcp.IPv4 {
		t.Fatalf("v4Only = %+v", got)
	}
	if allIPv6(mixed) {
		t.Fatal("mixed set must not be all-IPv6")
	}
	if !allIPv6([]tcp.Connection{conn(tcp.IPv6, 3, 4)}) {
		t.Fatal("single IPv6 connection must be all-IPv6")
	}
	if allIPv6(nil) {
		t.Fatal("empty set must not be all-IPv6")
	}
}
