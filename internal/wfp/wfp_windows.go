// Package wfp provides a short-notice full traffic block for one executable
// via the Windows Filtering Platform. It is used as a fallback when
// SetTcpEntry could not delete every connection (e.g. IPv6 sockets, for which
// there is no public delete API).
//
// Filters are installed inside a dynamic WFP session: the Base Filtering
// Engine removes them automatically when the engine handle is closed or the
// process exits, so a crash can never leave the game permanently blocked.
package wfp

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modFwpuclnt                   = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0           = modFwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0          = modFwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmFilterAdd0            = modFwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmGetAppIdFromFileName0 = modFwpuclnt.NewProc("FwpmGetAppIdFromFileName0")
	procFwpmFreeMemory0           = modFwpuclnt.NewProc("FwpmFreeMemory0")
)

const (
	fwpActionBlock       = 0x1001 // FWP_ACTION_BLOCK = 1 | FWP_ACTION_FLAG_TERMINATING
	fwpMatchEqual        = 0      // FWP_MATCH_EQUAL
	fwpDataTypeByteBlob  = 11     // FWP_BYTE_BLOB_TYPE
	fwpmSessionDynamic   = 0x1    // FWPM_SESSION_FLAG_DYNAMIC
	rpcCAuthnWinNT       = 10     // RPC_C_AUTHN_WINNT
)

var (
	layerALEAuthConnectV4    = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	layerALEAuthConnectV6    = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	layerALEAuthRecvAcceptV4 = windows.GUID{Data1: 0xe1cd9fe7, Data2: 0xf4b5, Data3: 0x4273, Data4: [8]byte{0x96, 0xc0, 0x59, 0x2e, 0x48, 0x7b, 0x86, 0x50}}
	layerALEAuthRecvAcceptV6 = windows.GUID{Data1: 0xa3b42c97, Data2: 0x9f04, Data3: 0x4672, Data4: [8]byte{0xb8, 0x7e, 0xce, 0xe9, 0xc4, 0x83, 0x25, 0x7f}}
	fieldALEAppID            = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
)

type fwpByteBlob struct {
	Size uint32
	_    uint32
	Data *byte
}

type fwpmDisplayData0 struct {
	Name        *uint16
	Description *uint16
}

// fwpValue0 / fwpConditionValue0: a FWP_DATA_TYPE tag followed by the union.
// Only the byteBlob pointer variant is used here; the union cell is 8 bytes.
type fwpValue0 struct {
	Type  uint32
	_     uint32
	Value unsafe.Pointer
}

type fwpmAction0 struct {
	Type uint32
	// union { GUID filterType; GUID calloutKey; } at offset 4
	FilterType windows.GUID
}

type fwpmFilterCondition0 struct {
	FieldKey       windows.GUID
	MatchType      uint32
	_              uint32 // pad ConditionValue to 8-byte alignment
	ConditionValue fwpValue0
}

type fwpmSession0 struct {
	SessionKey           windows.GUID
	DisplayData          fwpmDisplayData0
	Flags                uint32
	TxnWaitTimeoutInMSec uint32
	ProcessId            uint32
	_                    uint32
	Sid                  unsafe.Pointer
	Username             *uint16
	KernelMode           uint32
}

type fwpmFilter0 struct {
	FilterKey           windows.GUID
	DisplayData         fwpmDisplayData0
	Flags               uint32
	_                   uint32
	ProviderKey         unsafe.Pointer
	ProviderData        fwpByteBlob
	LayerKey            windows.GUID
	SubLayerKey         windows.GUID
	Weight              fwpValue0
	NumFilterConditions uint32
	_                   uint32
	FilterCondition     *fwpmFilterCondition0
	Action              fwpmAction0
	_                   [4]byte // pad union to 8-byte alignment
	Context             [16]byte // union { UINT64 rawContext; GUID providerContextKey; }
	Reserved            unsafe.Pointer
	FilterID            uint64
	EffectiveWeight     fwpValue0
}

// errLayoutMismatch guards against a compiler or x/sys change silently
// breaking the C struct layouts above.
func errLayoutMismatch(name string, got, want uintptr) error {
	return fmt.Errorf("wfp: struct %s has size %d, expected %d — C struct layout mismatch", name, got, want)
}

func checkLayouts() error {
	switch {
	case unsafe.Sizeof(fwpByteBlob{}) != 16:
		return errLayoutMismatch("FWP_BYTE_BLOB", unsafe.Sizeof(fwpByteBlob{}), 16)
	case unsafe.Sizeof(fwpmDisplayData0{}) != 16:
		return errLayoutMismatch("FWPM_DISPLAY_DATA0", unsafe.Sizeof(fwpmDisplayData0{}), 16)
	case unsafe.Sizeof(fwpValue0{}) != 16:
		return errLayoutMismatch("FWP_VALUE0", unsafe.Sizeof(fwpValue0{}), 16)
	case unsafe.Sizeof(fwpmAction0{}) != 20:
		return errLayoutMismatch("FWPM_ACTION0", unsafe.Sizeof(fwpmAction0{}), 20)
	case unsafe.Sizeof(fwpmFilterCondition0{}) != 40:
		return errLayoutMismatch("FWPM_FILTER_CONDITION0", unsafe.Sizeof(fwpmFilterCondition0{}), 40)
	case unsafe.Sizeof(fwpmSession0{}) != 72:
		return errLayoutMismatch("FWPM_SESSION0", unsafe.Sizeof(fwpmSession0{}), 72)
	case unsafe.Sizeof(fwpmFilter0{}) != 200:
		return errLayoutMismatch("FWPM_FILTER0", unsafe.Sizeof(fwpmFilter0{}), 200)
	}
	return nil
}

// BlockApp blocks all network traffic (IPv4 and IPv6, inbound and outbound)
// of the executable at exePath for the duration d. Requires elevation.
//
// The block is enforced by four WFP filters scoped to the application ID,
// installed in a dynamic session: if this process dies, the engine removes
// the filters by itself.
func BlockApp(ctx context.Context, exePath string, d time.Duration) error {
	if err := checkLayouts(); err != nil {
		return err
	}

	path16, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return fmt.Errorf("wfp: bad executable path: %w", err)
	}
	if err := modFwpuclnt.Load(); err != nil {
		return fmt.Errorf("wfp: load fwpuclnt: %w", err)
	}

	appID, err := appIDFromPath(path16)
	if err != nil {
		return err
	}
	defer freeBlob(appID)

	engine, err := openDynamicEngine()
	if err != nil {
		return err
	}
	defer func() { _, _, _ = procFwpmEngineClose0.Call(uintptr(engine)) }()

	if err := addBlockFilters(engine, appID); err != nil {
		return err
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func appIDFromPath(path16 *uint16) (*fwpByteBlob, error) {
	var blob *fwpByteBlob
	r, _, _ := procFwpmGetAppIdFromFileName0.Call(
		uintptr(unsafe.Pointer(path16)),
		uintptr(unsafe.Pointer(&blob)),
	)
	if r != 0 {
		return nil, fmt.Errorf("wfp: FwpmGetAppIdFromFileName0: %w", windows.Errno(r))
	}
	if blob == nil {
		return nil, errors.New("wfp: FwpmGetAppIdFromFileName0 returned no app ID")
	}
	return blob, nil
}

func freeBlob(blob *fwpByteBlob) {
	p := unsafe.Pointer(blob)
	procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&p)))
}

func openDynamicEngine() (windows.Handle, error) {
	session := fwpmSession0{Flags: fwpmSessionDynamic}
	var engine windows.Handle
	r, _, _ := procFwpmEngineOpen0.Call(
		0, // local engine
		rpcCAuthnWinNT,
		0, // no auth identity
		uintptr(unsafe.Pointer(&session)),
		uintptr(unsafe.Pointer(&engine)),
	)
	if r != 0 {
		return 0, fmt.Errorf("wfp: FwpmEngineOpen0: %w", windows.Errno(r))
	}
	return engine, nil
}

func addBlockFilters(engine windows.Handle, appID *fwpByteBlob) error {
	name, err := windows.UTF16PtrFromString("pubg-lobby-fix temporary block")
	if err != nil {
		return fmt.Errorf("wfp: filter display name: %w", err)
	}

	cond := fwpmFilterCondition0{
		FieldKey:  fieldALEAppID,
		MatchType: fwpMatchEqual,
	}
	cond.ConditionValue.Type = fwpDataTypeByteBlob
	cond.ConditionValue.Value = unsafe.Pointer(appID)

	layers := []struct {
		guid windows.GUID
		name string
	}{
		{layerALEAuthConnectV4, "ALE_AUTH_CONNECT_V4"},
		{layerALEAuthConnectV6, "ALE_AUTH_CONNECT_V6"},
		{layerALEAuthRecvAcceptV4, "ALE_AUTH_RECV_ACCEPT_V4"},
		{layerALEAuthRecvAcceptV6, "ALE_AUTH_RECV_ACCEPT_V6"},
	}

	for _, l := range layers {
		filter := fwpmFilter0{
			DisplayData:         fwpmDisplayData0{Name: name},
			LayerKey:            l.guid,
			SubLayerKey:         windows.GUID{}, // universal sublayer
			NumFilterConditions: 1,
			FilterCondition:     &cond,
		}
		filter.Action.Type = fwpActionBlock

		r, _, _ := procFwpmFilterAdd0.Call(
			uintptr(engine),
			uintptr(unsafe.Pointer(&filter)),
			0, // security descriptor
			0, // filter id out
		)
		if r != 0 {
			return fmt.Errorf("wfp: FwpmFilterAdd0 (%s): %w", l.name, windows.Errno(r))
		}
	}
	return nil
}
