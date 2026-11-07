//go:build windows

package secret

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// confirmPresence asks the person to confirm it is them: with Windows
// Hello (face, fingerprint or the Hello PIN) when it is set up for the
// account, otherwise with the account's password in Windows' credential
// dialog. reason is the dialog's message. It returns ErrCancelled when
// they cancel or fail.
func confirmPresence(ctx context.Context, reason string) error {
	err := helloVerify(ctx, reason)
	if errors.Is(err, errHelloUnavailable) {
		return passwordVerify(ctx, reason)
	}
	return err
}

// errHelloUnavailable reports that Windows Hello cannot confirm the person
// here: it is not set up, not allowed, or not reachable from this program.
var errHelloUnavailable = errors.New("secret: Windows Hello is not available")

// Windows Hello is reached through the WinRT class UserConsentVerifier,
// called through its COM interfaces with syscall, as Pinky has no cgo.

var (
	modcombase                 = windows.NewLazySystemDLL("combase.dll")
	procRoInitialize           = modcombase.NewProc("RoInitialize")
	procRoUninitialize         = modcombase.NewProc("RoUninitialize")
	procRoGetActivationFactory = modcombase.NewProc("RoGetActivationFactory")
	procWindowsCreateString    = modcombase.NewProc("WindowsCreateString")
	procWindowsDeleteString    = modcombase.NewProc("WindowsDeleteString")
)

var (
	// IUserConsentVerifierStatics (windows.security.credentials.ui.idl).
	iidUserConsentVerifierStatics = windows.GUID{Data1: 0xaf4f3f91, Data2: 0x564c, Data3: 0x4ddc, Data4: [8]byte{0xb8, 0xb5, 0x97, 0x34, 0x47, 0x62, 0x7c, 0x65}}
	// IUserConsentVerifierInterop (UserConsentVerifierInterop.h), for
	// desktop programs, whose dialog needs an owner window: Windows 11.
	iidUserConsentVerifierInterop = windows.GUID{Data1: 0x39e050c3, Data2: 0x4e74, Data3: 0x441a, Data4: [8]byte{0x8d, 0xc0, 0xb8, 0x11, 0x04, 0xdf, 0x94, 0x9c}}
	// IAsyncOperation<UserConsentVerificationResult>, from its WinRT
	// signature (TestAsyncOperationIID derives it).
	iidAsyncOpVerificationResult = windows.GUID{Data1: 0xfd596ffd, Data2: 0x2318, Data3: 0x558f, Data4: [8]byte{0x9d, 0xbe, 0xd2, 0x1d, 0xf4, 0x37, 0x64, 0xa5}}
	// IAsyncInfo.
	iidAsyncInfo = windows.GUID{Data1: 0x00000036, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

const userConsentVerifierClass = "Windows.Security.Credentials.UI.UserConsentVerifier"

// Vtable slots, after IInspectable's six.
const (
	slotRelease                           = 2
	slotQueryInterface                    = 0
	slotCheckAvailabilityAsync            = 6 // IUserConsentVerifierStatics
	slotRequestVerificationAsync          = 7 // IUserConsentVerifierStatics
	slotRequestVerificationForWindowAsync = 6 // IUserConsentVerifierInterop
	slotGetResults                        = 8 // IAsyncOperation<T>
	slotAsyncStatus                       = 7 // IAsyncInfo.get_Status
	slotAsyncErrorCode                    = 8 // IAsyncInfo.get_ErrorCode
	slotAsyncCancel                       = 9 // IAsyncInfo.Cancel
)

// AsyncStatus, UserConsentVerifierAvailability and
// UserConsentVerificationResult values.
const (
	asyncStarted   = 0
	asyncCompleted = 1
	asyncCanceled  = 2
	asyncError     = 3

	consentAvailable = 0

	consentVerified         = 0
	consentDeviceBusy       = 4
	consentRetriesExhausted = 5
	consentCanceled         = 6
)

const (
	roInitMultithreaded = 1          // RO_INIT_MULTITHREADED
	rpcEChangedMode     = 0x80010106 // RPC_E_CHANGED_MODE
)

// comObject is a COM interface pointer.
type comObject struct{ p unsafe.Pointer }

// call calls the method in a vtable slot. Pointers the arguments carry
// stay alive and in place for the call, as for syscall.SyscallN.
//
//go:uintptrescapes
func (o comObject) call(slot int, args ...uintptr) uint32 {
	vtbl := *(*unsafe.Pointer)(o.p)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(o.p)}, args...)...)
	return uint32(r)
}

func (o comObject) release() {
	if o.p != nil {
		o.call(slotRelease)
	}
}

func (o comObject) queryInterface(iid *windows.GUID) (comObject, error) {
	out := new(unsafe.Pointer)
	if hr := o.call(slotQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(out))); failed(hr) {
		return comObject{}, hresult(hr)
	}
	return comObject{*out}, nil
}

func failed(hr uint32) bool { return int32(hr) < 0 }

func hresult(hr uint32) error { return syscall.Errno(hr) }

// hstring is a WinRT string, deleted with free.
type hstring uintptr

func newHString(s string) (hstring, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	h := new(hstring)
	if r, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(h))); failed(uint32(r)) {
		return 0, hresult(uint32(r))
	}
	return *h, nil
}

func (h hstring) free() {
	if h != 0 {
		procWindowsDeleteString.Call(uintptr(h))
	}
}

// winrt runs fn on a thread of its own in WinRT's multithreaded apartment.
func winrt(fn func() error) error {
	if err := modcombase.Load(); err != nil {
		return fmt.Errorf("%w: %v", errHelloUnavailable, err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, _, _ := procRoInitialize.Call(roInitMultithreaded)
	switch hr := uint32(r); {
	case !failed(hr): // S_OK, or S_FALSE when it already was
		defer procRoUninitialize.Call()
	case hr == rpcEChangedMode: // already a single-threaded apartment: use it
	default:
		return fmt.Errorf("%w: RoInitialize: %v", errHelloUnavailable, hresult(hr))
	}
	return fn()
}

func activationFactory(class hstring, iid *windows.GUID) (comObject, error) {
	out := new(unsafe.Pointer)
	if r, _, _ := procRoGetActivationFactory.Call(uintptr(class), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(out))); failed(uint32(r)) {
		return comObject{}, hresult(uint32(r))
	}
	return comObject{*out}, nil
}

// await waits for an IAsyncOperation whose result is a 32-bit enum and
// returns the result, releasing the operation. When ctx ends first it
// cancels the operation, which takes its dialog away.
func await(ctx context.Context, op comObject) (int32, error) {
	defer op.release()
	info, err := op.queryInterface(&iidAsyncInfo)
	if err != nil {
		return 0, err
	}
	defer info.release()
	status := new(int32)
	cancelled := false
	for {
		if hr := info.call(slotAsyncStatus, uintptr(unsafe.Pointer(status))); failed(hr) {
			return 0, hresult(hr)
		}
		switch *status {
		case asyncStarted:
			select {
			case <-ctx.Done():
				if !cancelled {
					info.call(slotAsyncCancel)
					cancelled = true
				}
				time.Sleep(20 * time.Millisecond)
			case <-time.After(50 * time.Millisecond):
			}
		case asyncCompleted:
			result := new(int32)
			if hr := op.call(slotGetResults, uintptr(unsafe.Pointer(result))); failed(hr) {
				return 0, hresult(hr)
			}
			return *result, nil
		case asyncCanceled:
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return 0, ErrCancelled
		default: // asyncError
			code := new(int32)
			info.call(slotAsyncErrorCode, uintptr(unsafe.Pointer(code)))
			return 0, hresult(uint32(*code))
		}
	}
}

// helloAvailable reports whether Windows Hello can confirm the person:
// nil, or errHelloUnavailable with the reason.
func helloAvailable(ctx context.Context) error {
	return winrt(func() error {
		class, err := newHString(userConsentVerifierClass)
		if err != nil {
			return err
		}
		defer class.free()
		statics, err := activationFactory(class, &iidUserConsentVerifierStatics)
		if err != nil {
			return fmt.Errorf("%w: %v", errHelloUnavailable, err)
		}
		defer statics.release()
		return checkAvailability(ctx, statics)
	})
}

func checkAvailability(ctx context.Context, statics comObject) error {
	op := new(unsafe.Pointer)
	if hr := statics.call(slotCheckAvailabilityAsync, uintptr(unsafe.Pointer(op))); failed(hr) {
		return fmt.Errorf("%w: %v", errHelloUnavailable, hresult(hr))
	}
	avail, err := await(ctx, comObject{*op})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", errHelloUnavailable, err)
	}
	if avail != consentAvailable {
		return fmt.Errorf("%w (UserConsentVerifierAvailability %d)", errHelloUnavailable, avail)
	}
	return nil
}

// helloVerify asks the person through Windows Hello, owned by Pinky's
// window when it is in front.
func helloVerify(ctx context.Context, reason string) error {
	return winrt(func() error {
		class, err := newHString(userConsentVerifierClass)
		if err != nil {
			return err
		}
		defer class.free()
		statics, err := activationFactory(class, &iidUserConsentVerifierStatics)
		if err != nil {
			return fmt.Errorf("%w: %v", errHelloUnavailable, err)
		}
		defer statics.release()
		if err := checkAvailability(ctx, statics); err != nil {
			return err
		}
		msg, err := newHString(reason)
		if err != nil {
			return err
		}
		defer msg.free()

		op := new(unsafe.Pointer)
		if hwnd := ownWindow(); hwnd != 0 {
			if interop, err := activationFactory(class, &iidUserConsentVerifierInterop); err == nil {
				if hr := interop.call(slotRequestVerificationForWindowAsync, uintptr(hwnd), uintptr(msg), uintptr(unsafe.Pointer(&iidAsyncOpVerificationResult)), uintptr(unsafe.Pointer(op))); failed(hr) {
					*op = nil
				}
				interop.release()
			}
		}
		if *op == nil {
			if hr := statics.call(slotRequestVerificationAsync, uintptr(msg), uintptr(unsafe.Pointer(op))); failed(hr) {
				// Desktop programs without a window to own the dialog
				// may be refused: ask for the password instead.
				return fmt.Errorf("%w: %v", errHelloUnavailable, hresult(hr))
			}
		}
		result, err := await(ctx, comObject{*op})
		if err != nil {
			return err
		}
		switch result {
		case consentVerified:
			return nil
		case consentCanceled, consentRetriesExhausted:
			return ErrCancelled
		case consentDeviceBusy:
			return errors.New("secret: Windows Hello is busy; try again")
		}
		// Not set up or not allowed after all.
		return fmt.Errorf("%w (UserConsentVerificationResult %d)", errHelloUnavailable, result)
	})
}

// ownWindow returns the window in front when it is one of Pinky's, to own
// the system's dialog, or 0.
func ownWindow() windows.HWND {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid != windows.GetCurrentProcessId() {
		return 0
	}
	return hwnd
}

// The password fallback: Windows' credential dialog for the signed-in
// account, whose answer LogonUser checks.

var (
	modcredui                              = windows.NewLazySystemDLL("credui.dll")
	procCredUIPromptForWindowsCredentialsW = modcredui.NewProc("CredUIPromptForWindowsCredentialsW")
	procCredUnPackAuthenticationBufferW    = modcredui.NewProc("CredUnPackAuthenticationBufferW")
	procCredUIParseUserNameW               = modcredui.NewProc("CredUIParseUserNameW")
	procLogonUserW                         = modadvapi32.NewProc("LogonUserW")
)

// credUIInfoW is CREDUI_INFOW.
type credUIInfoW struct {
	Size        uint32
	Parent      windows.HWND
	MessageText *uint16
	CaptionText *uint16
	Banner      uintptr
}

const (
	creduiwinEnumerateCurrentUser = 0x200 // CREDUIWIN_ENUMERATE_CURRENT_USER
	logon32LogonNetwork           = 3     // LOGON32_LOGON_NETWORK
	logon32ProviderDefault        = 0     // LOGON32_PROVIDER_DEFAULT
	passwordTries                 = 3
)

// passwordVerify asks for the signed-in account's password and checks it.
// The dialog cannot be taken away early: when ctx ends while it shows,
// the answer is dropped once the person closes it.
func passwordVerify(ctx context.Context, reason string) error {
	if err := modcredui.Load(); err != nil {
		return fmt.Errorf("secret: no way to confirm the person here: %v: %w", err, ErrUnsupported)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	message, err := windows.UTF16PtrFromString(reason)
	if err != nil {
		return err
	}
	caption, _ := windows.UTF16PtrFromString("Pinky")
	info := &credUIInfoW{Parent: ownWindow(), MessageText: message, CaptionText: caption}
	info.Size = uint32(unsafe.Sizeof(*info))

	authError := uintptr(0)
	for try := 0; try < passwordTries; try++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkg := new(uint32)
		out := new(unsafe.Pointer)
		outSize := new(uint32)
		r, _, _ := procCredUIPromptForWindowsCredentialsW.Call(
			uintptr(unsafe.Pointer(info)), authError, uintptr(unsafe.Pointer(pkg)),
			0, 0, uintptr(unsafe.Pointer(out)), uintptr(unsafe.Pointer(outSize)),
			0, creduiwinEnumerateCurrentUser)
		switch syscall.Errno(r) {
		case 0:
		case windows.ERROR_CANCELLED:
			return ErrCancelled
		default:
			return fmt.Errorf("secret: the credential dialog failed: %w", syscall.Errno(r))
		}
		ok, err := checkPassword(*out, *outSize)
		clear(unsafe.Slice((*byte)(*out), *outSize))
		windows.CoTaskMemFree(*out)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if ok {
			return nil
		}
		authError = uintptr(windows.ERROR_LOGON_FAILURE)
	}
	return ErrCancelled
}

// checkPassword unpacks the dialog's answer and asks Windows whether the
// password signs the account in. The copies are wiped.
func checkPassword(buf unsafe.Pointer, size uint32) (bool, error) {
	const n = 1024
	name := make([]uint16, n)
	domain := make([]uint16, n)
	password := make([]uint16, n)
	defer clear(password)
	nameLen, domainLen, passwordLen := new(uint32), new(uint32), new(uint32)
	*nameLen, *domainLen, *passwordLen = n, n, n
	if r, _, err := procCredUnPackAuthenticationBufferW.Call(0, uintptr(buf), uintptr(size),
		uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(nameLen)),
		uintptr(unsafe.Pointer(&domain[0])), uintptr(unsafe.Pointer(domainLen)),
		uintptr(unsafe.Pointer(&password[0])), uintptr(unsafe.Pointer(passwordLen))); r == 0 {
		return false, fmt.Errorf("secret: read the credential dialog's answer: %w", err)
	}
	// The name comes as DOMAIN\user or user@domain; LogonUser takes the
	// parts apart.
	user := make([]uint16, n)
	userDomain := make([]uint16, n)
	if r, _, _ := procCredUIParseUserNameW.Call(uintptr(unsafe.Pointer(&name[0])),
		uintptr(unsafe.Pointer(&user[0])), n, uintptr(unsafe.Pointer(&userDomain[0])), n); r != 0 {
		copy(user, name)
		clear(userDomain)
	}
	if userDomain[0] == 0 && domain[0] != 0 {
		copy(userDomain, domain)
	}
	var domainArg uintptr
	if userDomain[0] != 0 {
		domainArg = uintptr(unsafe.Pointer(&userDomain[0]))
	}
	token := new(windows.Handle)
	r, _, err := procLogonUserW.Call(uintptr(unsafe.Pointer(&user[0])), domainArg,
		uintptr(unsafe.Pointer(&password[0])), logon32LogonNetwork, logon32ProviderDefault,
		uintptr(unsafe.Pointer(token)))
	if r == 0 {
		if errors.Is(err, windows.ERROR_LOGON_FAILURE) {
			return false, nil
		}
		return false, fmt.Errorf("secret: check the password: %w", err)
	}
	windows.CloseHandle(*token)
	return true, nil
}
