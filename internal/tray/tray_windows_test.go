package tray

import (
	"encoding/binary"
	"testing"
)

// icoFixture builds a valid .ico directory with one 4-byte image per width.
func icoFixture(t *testing.T, widths ...int) []byte {
	t.Helper()
	buf := make([]byte, 6+16*len(widths)+4*len(widths))
	binary.LittleEndian.PutUint16(buf[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(widths)))
	dataBase := 6 + 16*len(widths)
	for i, w := range widths {
		dir := 6 + 16*i
		bw := byte(w)
		if w == 256 {
			bw = 0
		}
		buf[dir] = bw
		buf[dir+1] = bw
		binary.LittleEndian.PutUint32(buf[dir+8:], 4)  // bytes in image
		binary.LittleEndian.PutUint32(buf[dir+12:], uint32(dataBase+4*i))
	}
	return buf
}

func TestPickIconEntryChoosesClosestSize(t *testing.T) {
	raw := icoFixture(t, 16, 32, 48)
	for _, tc := range []struct{ want, expect int }{
		{16, 16},
		{20, 16},
		{24, 32},
		{40, 48},
		{200, 48},
	} {
		e, err := pickIconEntry(raw, tc.want)
		if err != nil {
			t.Fatalf("want %d: %v", tc.want, err)
		}
		if e.width != tc.expect {
			t.Fatalf("want %d: picked width %d, expected %d", tc.want, e.width, tc.expect)
		}
		if len(e.data) != 4 {
			t.Fatalf("want %d: image data length %d, expected 4", tc.want, len(e.data))
		}
	}
}

func TestPickIconEntryRejectsBadFiles(t *testing.T) {
	if _, err := pickIconEntry(nil, 16); err == nil {
		t.Fatal("empty file must be rejected")
	}
	if _, err := pickIconEntry([]byte{0, 0, 1}, 16); err == nil {
		t.Fatal("truncated directory must be rejected")
	}
	// A count that overclaims is tolerated when the existing entries are fine.
	buf := icoFixture(t, 16)
	binary.LittleEndian.PutUint16(buf[4:], 2)
	if e, err := pickIconEntry(buf, 16); err != nil || e.width != 16 {
		t.Fatalf("overclaimed count: got (%+v, %v), want the single valid entry", e, err)
	}

	// Every entry pointing outside the buffer leaves nothing usable.
	binary.LittleEndian.PutUint32(buf[6+12:], 1<<20)
	if _, err := pickIconEntry(buf, 16); err == nil {
		t.Fatal("entries with out-of-range data must be rejected")
	}
}

func TestCopyFixed(t *testing.T) {
	dst := make([]uint16, 4)
	copyFixed(dst, "ab")
	if dst[0] != 'a' || dst[1] != 'b' || dst[2] != 0 || dst[3] != 0 {
		t.Fatalf("copyFixed must write the string and a NUL, got %v", dst)
	}

	dst = make([]uint16, 4)
	copyFixed(dst, "abcdef")
	if dst[0] != 'a' || dst[1] != 'b' || dst[2] != 'c' || dst[3] != 0 {
		t.Fatalf("copyFixed must truncate and keep room for the NUL, got %v", dst)
	}

	dst = make([]uint16, 2)
	copyFixed(dst, "")
	if dst[0] != 0 {
		t.Fatalf(`copyFixed("") must write a NUL, got %v`, dst)
	}
}
