package app

import (
	"context"
	"errors"
	"net"
	"strings"
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

func TestCloseBalloon(t *testing.T) {
	title, body, warn := closeBalloon(closeStats{Closed: 3}, nil)
	if title != "Sockets closed" || warn || !strings.Contains(body, "closed 3") {
		t.Fatalf("success: got (%q, %q, %v)", title, body, warn)
	}

	_, body, warn = closeBalloon(closeStats{Closed: 1, Gone: 2, Failed: 4}, nil)
	if !warn {
		t.Fatal("failures must set warn")
	}
	for _, want := range []string{"closed 1", "2 already gone", "4 failed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %q must contain %q", body, want)
		}
	}

	_, body, warn = closeBalloon(closeStats{Blocked: true}, nil)
	if warn || !strings.Contains(body, "blocked") {
		t.Fatalf("block fallback: got (%q, %v)", body, warn)
	}

	title, _, _ = closeBalloon(closeStats{}, nil)
	if title != "Nothing to close" {
		t.Fatalf("empty run: title %q", title)
	}

	title, body, warn = closeBalloon(closeStats{}, errors.New("boom"))
	if title != "Close failed" || !warn || body != "boom" {
		t.Fatalf("error: got (%q, %q, %v)", title, body, warn)
	}

	// A cancelled run must stay silent.
	if title, _, _ = closeBalloon(closeStats{Closed: 1}, context.Canceled); title != "" {
		t.Fatalf("cancelled run must produce no balloon, got %q", title)
	}
}
