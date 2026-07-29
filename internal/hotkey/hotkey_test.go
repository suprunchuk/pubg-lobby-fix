package hotkey

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		wantVK  uint32
		wantMod uint32
		wantErr bool
	}{
		{in: "ctrl+shift+l", wantVK: 'L', wantMod: modControl | modShift | modNoRepeat},
		{in: "F9", wantVK: 0x78, wantMod: modNoRepeat},
		{in: "alt+f10", wantVK: 0x79, wantMod: modAlt | modNoRepeat},
		{in: "win+space", wantVK: 0x20, wantMod: modWin | modNoRepeat},
		{in: "", wantErr: true},
		{in: "ctrl+", wantErr: true},
		{in: "ctrl+shift+l+m", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.VK != tt.wantVK {
				t.Fatalf("VK: got %#x want %#x", got.VK, tt.wantVK)
			}
			if got.Mods != tt.wantMod {
				t.Fatalf("Mods: got %#x want %#x", got.Mods, tt.wantMod)
			}
		})
	}
}
