//go:build darwin

package keychain

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The quiet read calls Security.framework's SecItemCopyMatching through
// purego, since the macOS binaries are built without cgo. It forbids UI
// three ways, because on the login keychain, where browser and Claude
// Code items live, no single one is enough: kSecUseAuthenticationUI set to
// kSecUseAuthenticationUIFail alone still showed the Allow/Deny prompt on
// macOS 27. With an LAContext whose interactionNotAllowed is set, as
// CodexBar does, and SecKeychainSetUserInteractionAllowed(false), the
// legacy keychain's own switch, the prompt becomes
// errSecInteractionNotAllowed.

const (
	errSecItemNotFound          = -25300
	errSecInteractionNotAllowed = -25308
	errSecAuthFailed            = -25293
	kCFStringEncodingUTF8       = 0x08000100
)

type security struct {
	cfStringCreate      func(alloc uintptr, s string, encoding uint32) uintptr
	cfDictionaryCreate  func(alloc uintptr, keys, values unsafe.Pointer, n int, keyCallbacks, valueCallbacks uintptr) uintptr
	cfRelease           func(ref uintptr)
	cfDataGetLength     func(data uintptr) int
	cfDataGetBytePtr    func(data uintptr) uintptr
	secItemCopyMatching func(query uintptr, result *uintptr) int32
	memcpy              func(dst unsafe.Pointer, src uintptr, n uintptr) uintptr
	cfArrayCreate       func(alloc uintptr, values unsafe.Pointer, n int, callbacks uintptr) uintptr
	keychainOpen        func(path string, keychain *uintptr) int32
	getInteraction      func(allowed *bool) int32
	setInteraction      func(allowed bool) int32

	arrayCallbacks uintptr

	keyCallbacks, valueCallbacks uintptr
	cfTrue                       uintptr
}

var (
	loadOnce sync.Once
	loaded   *security
	loadErr  error
)

func load() (*security, error) {
	loadOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("keychain: load CoreFoundation: %w", err)
			return
		}
		sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("keychain: load Security: %w", err)
			return
		}
		libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = fmt.Errorf("keychain: load libSystem: %w", err)
			return
		}
		s := &security{}
		purego.RegisterLibFunc(&s.cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&s.cfDictionaryCreate, cf, "CFDictionaryCreate")
		purego.RegisterLibFunc(&s.cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&s.cfDataGetLength, cf, "CFDataGetLength")
		purego.RegisterLibFunc(&s.cfDataGetBytePtr, cf, "CFDataGetBytePtr")
		purego.RegisterLibFunc(&s.secItemCopyMatching, sec, "SecItemCopyMatching")
		purego.RegisterLibFunc(&s.memcpy, libc, "memcpy")
		purego.RegisterLibFunc(&s.cfArrayCreate, cf, "CFArrayCreate")
		purego.RegisterLibFunc(&s.keychainOpen, sec, "SecKeychainOpen")
		purego.RegisterLibFunc(&s.getInteraction, sec, "SecKeychainGetUserInteractionAllowed")
		purego.RegisterLibFunc(&s.setInteraction, sec, "SecKeychainSetUserInteractionAllowed")
		if _, err := purego.Dlopen("/System/Library/Frameworks/LocalAuthentication.framework/LocalAuthentication", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			loadErr = fmt.Errorf("keychain: load LocalAuthentication: %w", err)
			return
		}
		if s.arrayCallbacks, err = purego.Dlsym(cf, "kCFTypeArrayCallBacks"); err != nil {
			loadErr = err
			return
		}
		if s.keyCallbacks, err = purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks"); err != nil {
			loadErr = err
			return
		}
		if s.valueCallbacks, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks"); err != nil {
			loadErr = err
			return
		}
		// kCFBooleanTrue is a global holding the CFBoolean; read its value.
		sym, err := purego.Dlsym(cf, "kCFBooleanTrue")
		if err != nil {
			loadErr = err
			return
		}
		s.memcpy(unsafe.Pointer(&s.cfTrue), sym, unsafe.Sizeof(s.cfTrue))
		loaded = s
	})
	return loaded, loadErr
}

// str makes a CFString the caller releases.
func (s *security) str(v string) uintptr { return s.cfStringCreate(0, v, kCFStringEncodingUTF8) }

// testKeychainPath confines quiet reads to one keychain file: the live
// test's own, so it never reads the operator's keychains.
var testKeychainPath string

// interactionMu serialises quiet reads: the legacy keychain's interaction
// switch is process-wide, and each read restores what it found.
var interactionMu sync.Mutex

func quiet(item Item) (string, error) {
	s, err := load()
	if err != nil {
		return "", err
	}
	interactionMu.Lock()
	defer interactionMu.Unlock()
	var allowed bool
	if s.getInteraction(&allowed) == 0 {
		defer s.setInteraction(allowed)
	}
	s.setInteraction(false)

	// An LAContext that may not interact, as kSecUseAuthenticationContext.
	ctx := objc.ID(objc.GetClass("LAContext")).Send(objc.RegisterName("new"))
	if ctx == 0 {
		return "", fmt.Errorf("keychain: could not create an LAContext")
	}
	defer ctx.Send(objc.RegisterName("release"))
	ctx.Send(objc.RegisterName("setInteractionNotAllowed:"), true)

	// The Security constants are CFStrings with these values; equal
	// strings are equal keys, so they are built rather than looked up.
	pairs := [][2]uintptr{
		{s.str("class"), s.str("genp")},         // kSecClass: kSecClassGenericPassword
		{s.str("svce"), s.str(item.Service)},    // kSecAttrService
		{s.str("m_Limit"), s.str("m_LimitOne")}, // kSecMatchLimit: kSecMatchLimitOne
		{s.str("u_AuthUI"), s.str("u_AuthUIF")}, // kSecUseAuthenticationUI: ...UIFail
	}
	if item.Account != "" {
		pairs = append(pairs, [2]uintptr{s.str("acct"), s.str(item.Account)}) // kSecAttrAccount
	}
	keys := make([]uintptr, 0, len(pairs)+1)
	values := make([]uintptr, 0, len(pairs)+1)
	for _, p := range pairs {
		keys, values = append(keys, p[0]), append(values, p[1])
	}
	returnData := s.str("r_Data")     // kSecReturnData
	authContext := s.str("u_AuthCtx") // kSecUseAuthenticationContext
	keys, values = append(keys, returnData, authContext), append(values, s.cfTrue, uintptr(ctx))
	var searchList, keychain uintptr
	if testKeychainPath != "" {
		if status := s.keychainOpen(testKeychainPath, &keychain); status != 0 {
			return "", fmt.Errorf("keychain: open %s: OSStatus %d", testKeychainPath, status)
		}
		searchList = s.cfArrayCreate(0, unsafe.Pointer(&keychain), 1, s.arrayCallbacks)
		matchList := s.str("m_SearchList") // kSecMatchSearchList
		keys, values = append(keys, matchList), append(values, searchList)
		defer s.cfRelease(matchList)
	}
	defer func() {
		for _, p := range pairs {
			s.cfRelease(p[0])
			s.cfRelease(p[1])
		}
		s.cfRelease(returnData)
		s.cfRelease(authContext)
		if searchList != 0 {
			s.cfRelease(searchList)
			s.cfRelease(keychain)
		}
	}()
	query := s.cfDictionaryCreate(0, unsafe.Pointer(&keys[0]), unsafe.Pointer(&values[0]), len(keys), s.keyCallbacks, s.valueCallbacks)
	if query == 0 {
		return "", fmt.Errorf("keychain: could not build the query for %s", item)
	}
	defer s.cfRelease(query)

	var data uintptr
	switch status := s.secItemCopyMatching(query, &data); status {
	case 0:
	case errSecItemNotFound:
		return "", ErrNotFound
	case errSecInteractionNotAllowed, errSecAuthFailed:
		return "", fmt.Errorf("%w: %s", ErrInteractionRequired, item)
	default:
		return "", fmt.Errorf("keychain: read %s: OSStatus %d", item, status)
	}
	defer s.cfRelease(data)
	n := s.cfDataGetLength(data)
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	s.memcpy(unsafe.Pointer(&buf[0]), s.cfDataGetBytePtr(data), uintptr(n))
	return string(buf), nil
}
