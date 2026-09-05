package wfp

import "testing"

// TestStructLayouts pins the C struct layouts the syscall layer relies on.
// A failure here means a struct definition drifted from the Win32 ABI and
// FwpmFilterAdd0 would corrupt memory or reject the filter.
func TestStructLayouts(t *testing.T) {
	if err := checkLayouts(); err != nil {
		t.Fatal(err)
	}
}
