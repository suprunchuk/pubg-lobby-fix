// Package tray shows the tool as a notification-area icon instead of a
// console window: left click (or the menu) triggers the same action as the
// hotkey, a balloon reports the outcome, and the menu has an Exit item.
//
// The icon lives on a hidden window with its own message loop, which must
// run on one OS thread (Run locks one). Shell_NotifyIconW may be called from
// any goroutine, so Notify is safe after Run has published the icon.
package tray

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed icon.ico
var icoFile []byte

// ErrExitRequested is returned by Run when the user picked Exit in the menu.
var ErrExitRequested = errors.New("exit requested from the tray menu")

// Options configures the tray icon.
type Options struct {
	// Tooltip is shown when hovering the icon. Empty means "pubg-lobby-fix".
	Tooltip string
	// OnTrigger fires on a left click and on the "Close connections now" menu
	// item. It is called on the tray's message-loop thread and must be quick.
	OnTrigger func()
}

const (
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifInfo    = 0x10

	niifInfo  = 0x01
	niifError = 0x03

	// callbackMsg is the private message the icon posts to our window;
	// lParam then holds the mouse message.
	callbackMsg = 0x8000 + 1 // WM_APP+1

	menuClose = 1
	menuExit  = 2

	mfSeparator = 0x0800

	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmQuit          = 0x0012
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	tpmRightButton  = 0x0002
	tpmBottomAlign  = 0x0020
	tpmReturnCmd    = 0x0100
	idiApplication  = 32512
	smCxSmallIcon   = 49
	swHide          = 0
	iconVersion300  = 0x00030000
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procShellNotifyIcon = modShell32.NewProc("Shell_NotifyIconW")

	procLoadIcon            = modUser32.NewProc("LoadIconW")
	procCreateIconFromResEx = modUser32.NewProc("CreateIconFromResourceEx")
	procRegisterClassEx     = modUser32.NewProc("RegisterClassExW")
	procCreateWindowEx      = modUser32.NewProc("CreateWindowExW")
	procDestroyWindow       = modUser32.NewProc("DestroyWindow")
	procDefWindowProc       = modUser32.NewProc("DefWindowProcW")
	procGetMessage          = modUser32.NewProc("GetMessageW")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")
	procDispatchMessage     = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procPostThreadMessage   = modUser32.NewProc("PostThreadMessageW")
	procPostMessage         = modUser32.NewProc("PostMessageW")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procShowWindow          = modUser32.NewProc("ShowWindow")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procAppendMenu          = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenu      = modUser32.NewProc("TrackPopupMenu")
	procDestroyMenu         = modUser32.NewProc("DestroyMenu")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procGetSystemMetrics    = modUser32.NewProc("GetSystemMetrics")

	procGetModuleHandle    = modKernel32.NewProc("GetModuleHandleW")
	procGetConsoleWindow   = modKernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcList = modKernel32.NewProc("GetConsoleProcessList")
	procGetCurrentThreadId = modKernel32.NewProc("GetCurrentThreadId")
)

// NOTIFYICONDATAW, 976 bytes on x64.
type notifyIconData struct {
	CbSize           uint32
	_                uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	_                uint32 // pad HIcon to 8-byte alignment
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32 // union { uTimeout; uVersion; }
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

// WNDCLASSEXW, 80 bytes on x64.
type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// checkLayouts guards the C struct sizes against compiler/x/sys drift.
func checkLayouts() error {
	switch {
	case unsafe.Sizeof(notifyIconData{}) != 976:
		return fmt.Errorf("tray: NOTIFYICONDATAW has size %d, expected 976", unsafe.Sizeof(notifyIconData{}))
	case unsafe.Sizeof(wndClassEx{}) != 80:
		return fmt.Errorf("tray: WNDCLASSEXW has size %d, expected 80", unsafe.Sizeof(wndClassEx{}))
	}
	return nil
}

// state is the single tray icon of the process.
type state struct {
	opts  Options
	hwnd  windows.Handle
	uid   uint32
	added bool
}

var current atomic.Pointer[state]

// Run adds the tray icon, hides the console window if the process owns it,
// and pumps the message loop until Exit is picked in the menu or ctx is
// cancelled. It returns ErrExitRequested on a menu exit, ctx.Err() when the
// app is shutting down, or an error when the icon could not be created.
func Run(ctx context.Context, opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := checkLayouts(); err != nil {
		return err
	}
	if opts.Tooltip == "" {
		opts.Tooltip = "pubg-lobby-fix"
	}

	icon, err := loadIcon()
	if err != nil {
		return err
	}
	hmod, _, _ := procGetModuleHandle.Call(0)

	className, err := windows.UTF16PtrFromString("pubg-lobby-fix tray window")
	if err != nil {
		return err
	}
	windowName, err := windows.UTF16PtrFromString("pubg-lobby-fix")
	if err != nil {
		return err
	}
	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   wndProcCallback,
		HInstance:     windows.Handle(hmod),
		HIcon:         icon,
		HCursor:       0, // never shown, no cursor needed
		LpszClassName: className,
		HIconSm:       icon,
	}
	atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return fmt.Errorf("RegisterClassExW: %w", err)
	}

	hwnd, _, err := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0, // WS_OVERLAPPED: created, never shown
		0, 0, 0, 0,
		0, // parent
		0, // menu
		hmod,
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %w", err)
	}

	s := &state{opts: opts, hwnd: windows.Handle(hwnd), uid: 1}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = s.hwnd
	nid.UID = s.uid
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = callbackMsg
	nid.HIcon = icon
	copyFixed(nid.SzTip[:], opts.Tooltip)
	r, _, err := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD): %w", err)
	}
	s.added = true
	current.Store(s)

	// Hide the console only when nothing else is attached to it: started via
	// double-click or the UAC relaunch. From a shell the console is shared
	// and hiding it would close the user's terminal.
	if console := ownedConsoleWindow(); console != 0 {
		procShowWindow.Call(console, swHide)
	}

	tid, _, _ := procGetCurrentThreadId.Call()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			procPostThreadMessage.Call(tid, wmQuit, 0, 0)
		case <-done:
		}
	}()

	var m msg
	var loopErr error
	for {
		r, _, err := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == 0 { // WM_QUIT
			break
		}
		if int32(r) == -1 {
			loopErr = fmt.Errorf("GetMessage: %w", err)
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	close(done)

	var rm notifyIconData
	rm.CbSize = uint32(unsafe.Sizeof(rm))
	rm.HWnd = s.hwnd
	rm.UID = s.uid
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&rm)))
	procDestroyWindow.Call(hwnd)
	current.Store(nil)

	switch {
	case loopErr != nil:
		return loopErr
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return ErrExitRequested
	}
}

// Notify shows a balloon (rendered as a toast on modern Windows) from the
// tray icon. It is a no-op when the icon is not active, e.g. with -no-tray,
// -once, -list, or a failed NIM_ADD. Safe to call from any goroutine.
func Notify(title, text string, warn bool) {
	s := current.Load()
	if s == nil || !s.added {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = s.hwnd
	nid.UID = s.uid
	nid.UFlags = nifInfo
	copyFixed(nid.SzInfoTitle[:], title)
	copyFixed(nid.SzInfo[:], text)
	if warn {
		nid.DwInfoFlags = niifError
	} else {
		nid.DwInfoFlags = niifInfo
	}
	procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case callbackMsg:
		if s := current.Load(); s != nil {
			switch lParam {
			case wmLButtonUp:
				if s.opts.OnTrigger != nil {
					s.opts.OnTrigger()
				}
			case wmRButtonUp:
				s.showMenu(hwnd)
			}
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

var wndProcCallback = syscall.NewCallback(wndProc)

// showMenu pops up the context menu at the cursor and dispatches the choice.
// TrackPopupMenu must be preceded by SetForegroundWindow, or the menu will
// not close when the user clicks elsewhere.
func (s *state) showMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem := func(id uintptr, text string) {
		p, err := windows.UTF16PtrFromString(text)
		if err != nil {
			return
		}
		procAppendMenu.Call(menu, 0 /* MF_STRING */, id, uintptr(unsafe.Pointer(p)))
	}
	appendItem(menuClose, "Close connections now")
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	appendItem(menuExit, "Exit")

	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmReturnCmd|tpmRightButton|tpmBottomAlign,
		uintptr(pt.X), uintptr(pt.Y),
		0, hwnd, 0,
	)
	procPostMessage.Call(hwnd, wmNull, 0, 0)

	switch cmd {
	case menuClose:
		if s.opts.OnTrigger != nil {
			s.opts.OnTrigger()
		}
	case menuExit:
		procPostQuitMessage.Call(0)
	}
}

// ownedConsoleWindow returns the console window handle when the process is
// its only attachment, or 0 when there is no console (GUI start, Windows
// Terminal) or the console is shared with a parent shell.
func ownedConsoleWindow() uintptr {
	h, _, _ := procGetConsoleWindow.Call()
	if h == 0 {
		return 0
	}
	var procs [4]uint32
	n, _, _ := procGetConsoleProcList.Call(
		uintptr(unsafe.Pointer(&procs[0])),
		uintptr(len(procs)),
	)
	if n == 1 {
		return h
	}
	return 0
}

// loadIcon builds the HICON from the embedded .ico, picking the image closest
// to the tray's small-icon size; it falls back to the stock application icon.
func loadIcon() (windows.Handle, error) {
	size := smallIconSize()
	entry, err := pickIconEntry(icoFile, size)
	if err == nil {
		h, _, _ := procCreateIconFromResEx.Call(
			uintptr(unsafe.Pointer(&entry.data[0])),
			uintptr(len(entry.data)),
			1, // fIcon
			iconVersion300,
			uintptr(size), uintptr(size),
			0, // LR_DEFAULTCOLOR
		)
		if h != 0 {
			return windows.Handle(h), nil
		}
	}
	h, _, err := procLoadIcon.Call(0, idiApplication)
	if h == 0 {
		return 0, fmt.Errorf("LoadIconW(IDI_APPLICATION): %w", err)
	}
	return windows.Handle(h), nil
}

func smallIconSize() int {
	n, _, _ := procGetSystemMetrics.Call(smCxSmallIcon)
	if n == 0 {
		return 16
	}
	return int(int32(n))
}

// icoEntry is one image inside an .ico file.
type icoEntry struct {
	width int
	data  []byte
}

// pickIconEntry parses the ICO directory and returns the image whose width is
// closest to want. Width byte 0 stands for 256.
func pickIconEntry(ico []byte, want int) (icoEntry, error) {
	if len(ico) < 6 {
		return icoEntry{}, errors.New("icon file truncated")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:]))
	var best icoEntry
	bestDiff := 1 << 30
	found := false
	for i := 0; i < count; i++ {
		dir := 6 + 16*i
		if dir+16 > len(ico) {
			break
		}
		w := int(ico[dir])
		if w == 0 {
			w = 256
		}
		size := int(binary.LittleEndian.Uint32(ico[dir+8:]))
		off := int(binary.LittleEndian.Uint32(ico[dir+12:]))
		if off+size > len(ico) {
			continue
		}
		diff := w - want
		if diff < 0 {
			diff = -diff
		}
		// On a tie take the larger image: downscaling beats upscaling.
		if !found || diff < bestDiff || (diff == bestDiff && w > best.width) {
			found = true
			bestDiff = diff
			best = icoEntry{width: w, data: ico[off : off+size]}
		}
	}
	if !found {
		return icoEntry{}, errors.New("icon file has no usable images")
	}
	return best, nil
}

// copyFixed copies s into a NUL-terminated UTF-16 field, truncating to fit.
func copyFixed(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	n := len(dst) - 1
	if len(u) < n {
		n = len(u)
	}
	copy(dst, u[:n])
	dst[n] = 0
}
