package hotkey

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	wmHotkey = 0x0312
	wmQuit   = 0x0012

	errHotkeyAlreadyRegistered syscall.Errno = 1409
)

var (
	modUser32              = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey     = modUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = modUser32.NewProc("UnregisterHotKey")
	procGetMessage         = modUser32.NewProc("GetMessageW")
	procTranslateMessage   = modUser32.NewProc("TranslateMessage")
	procDispatchMessage    = modUser32.NewProc("DispatchMessageW")
	procPostThreadMessage  = modUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId")
)

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// Binding is a registered global hotkey.
type Binding struct {
	ID      int
	Mods    uint32
	VK      uint32
	Display string
}

// Parse converts strings like "ctrl+shift+l", "f9", "alt+f10" into a Binding.
func Parse(s string) (Binding, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return Binding{}, errors.New("empty hotkey")
	}

	parts := strings.Split(s, "+")
	var mods uint32
	var key string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case "ctrl", "control", "ctl":
			mods |= modControl
		case "alt", "menu":
			mods |= modAlt
		case "shift":
			mods |= modShift
		case "win", "windows", "super", "meta":
			mods |= modWin
		default:
			if key != "" {
				return Binding{}, fmt.Errorf("multiple keys in hotkey %q", s)
			}
			key = p
		}
	}
	if key == "" {
		return Binding{}, fmt.Errorf("no key in hotkey %q", s)
	}

	vk, err := virtualKey(key)
	if err != nil {
		return Binding{}, err
	}

	return Binding{
		ID:      1,
		Mods:    mods | modNoRepeat,
		VK:      vk,
		Display: s,
	}, nil
}

func virtualKey(key string) (uint32, error) {
	if len(key) == 1 {
		c := key[0]
		if c >= 'a' && c <= 'z' {
			return uint32(c - 'a' + 'A'), nil
		}
		if c >= '0' && c <= '9' {
			return uint32(c), nil
		}
	}
	if strings.HasPrefix(key, "f") && len(key) >= 2 {
		var n int
		if _, err := fmt.Sscanf(key, "f%d", &n); err == nil && n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), nil // VK_F1 = 0x70
		}
	}
	switch key {
	case "space":
		return 0x20, nil
	case "tab":
		return 0x09, nil
	case "esc", "escape":
		return 0x1B, nil
	case "enter", "return":
		return 0x0D, nil
	}
	return 0, fmt.Errorf("unsupported key %q", key)
}

// Listen registers the hotkey and sends on ch whenever it is pressed.
// Blocks until ctx is cancelled. Must run on a dedicated OS thread
// (runtime.LockOSThread) that owns the message loop.
func Listen(ctx context.Context, b Binding, ch chan<- struct{}) error {
	if err := modUser32.Load(); err != nil {
		return fmt.Errorf("load user32: %w", err)
	}

	r0, _, e := procRegisterHotKey.Call(0, uintptr(b.ID), uintptr(b.Mods), uintptr(b.VK))
	if r0 == 0 {
		if errors.Is(e, errHotkeyAlreadyRegistered) {
			return fmt.Errorf("hotkey %q already registered by another app", b.Display)
		}
		return fmt.Errorf("RegisterHotKey %q: %w", b.Display, e)
	}
	defer func() {
		_, _, _ = procUnregisterHotKey.Call(0, uintptr(b.ID))
	}()

	tid, _, _ := procGetCurrentThreadId.Call()

	var wg sync.WaitGroup
	wg.Go(func() {
		<-ctx.Done()
		_, _, _ = procPostThreadMessage.Call(tid, wmQuit, 0, 0)
	})

	var m msg
	for {
		r, _, err := procGetMessage.Call(
			uintptr(unsafe.Pointer(&m)),
			0, 0, 0,
		)
		// GetMessage returns 0 on WM_QUIT, -1 on error
		if int32(r) == 0 {
			wg.Wait()
			return ctx.Err()
		}
		if int32(r) == -1 {
			wg.Wait()
			return fmt.Errorf("GetMessage: %w", err)
		}
		if m.Message == wmHotkey && int(m.WParam) == b.ID {
			select {
			case ch <- struct{}{}:
			default:
				// drop if still processing previous press
			}
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}
