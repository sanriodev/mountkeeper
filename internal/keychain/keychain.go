// Package keychain stores SMB passwords in the macOS login Keychain through
// the Security framework. Passwords only ever travel through process memory:
// they are never written to disk, logged, or passed as command-line arguments.
package keychain

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFStringRef mk_str(const char *s) {
	return CFStringCreateWithCString(kCFAllocatorDefault, s, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef mk_query(CFStringRef service, CFStringRef account) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, service);
	CFDictionarySetValue(q, kSecAttrAccount, account);
	return q;
}

static OSStatus mk_set(const char *service, const char *account, const char *label,
		const char *comment, const void *pw, long pwlen) {
	CFStringRef svc = mk_str(service), acc = mk_str(account);
	CFStringRef lbl = mk_str(label), cmt = mk_str(comment);
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)pw, (CFIndex)pwlen);
	if (!svc || !acc || !lbl || !cmt || !data) {
		if (svc) CFRelease(svc);
		if (acc) CFRelease(acc);
		if (lbl) CFRelease(lbl);
		if (cmt) CFRelease(cmt);
		if (data) CFRelease(data);
		return errSecParam;
	}

	CFMutableDictionaryRef q = mk_query(svc, acc);
	CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(attrs, kSecValueData, data);
	CFDictionarySetValue(attrs, kSecAttrLabel, lbl);
	CFDictionarySetValue(attrs, kSecAttrComment, cmt);

	OSStatus st = SecItemUpdate(q, attrs);
	if (st == errSecItemNotFound) {
		CFDictionarySetValue(q, kSecValueData, data);
		CFDictionarySetValue(q, kSecAttrLabel, lbl);
		CFDictionarySetValue(q, kSecAttrComment, cmt);
		st = SecItemAdd(q, NULL);
	}

	CFRelease(attrs);
	CFRelease(q);
	CFRelease(data);
	CFRelease(cmt);
	CFRelease(lbl);
	CFRelease(acc);
	CFRelease(svc);
	return st;
}

// On success *out is a malloc'ed copy of the password that the caller must
// wipe and free.
static OSStatus mk_get(const char *service, const char *account, void **out, long *outlen) {
	CFStringRef svc = mk_str(service), acc = mk_str(account);
	if (!svc || !acc) {
		if (svc) CFRelease(svc);
		if (acc) CFRelease(acc);
		return errSecParam;
	}
	CFMutableDictionaryRef q = mk_query(svc, acc);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);

	CFTypeRef result = NULL;
	OSStatus st = SecItemCopyMatching(q, &result);
	if (st == errSecSuccess && result != NULL) {
		CFDataRef data = (CFDataRef)result;
		CFIndex n = CFDataGetLength(data);
		*out = malloc(n > 0 ? (size_t)n : 1);
		if (*out == NULL) {
			st = errSecAllocate;
		} else {
			memcpy(*out, CFDataGetBytePtr(data), (size_t)n);
			*outlen = (long)n;
		}
	}
	if (result) CFRelease(result);
	CFRelease(q);
	CFRelease(acc);
	CFRelease(svc);
	return st;
}

// Looks the item up without requesting its secret, so no access prompt appears.
static OSStatus mk_exists(const char *service, const char *account) {
	CFStringRef svc = mk_str(service), acc = mk_str(account);
	if (!svc || !acc) {
		if (svc) CFRelease(svc);
		if (acc) CFRelease(acc);
		return errSecParam;
	}
	CFMutableDictionaryRef q = mk_query(svc, acc);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	OSStatus st = SecItemCopyMatching(q, NULL);
	CFRelease(q);
	CFRelease(acc);
	CFRelease(svc);
	return st;
}

static OSStatus mk_delete(const char *service, const char *account) {
	CFStringRef svc = mk_str(service), acc = mk_str(account);
	if (!svc || !acc) {
		if (svc) CFRelease(svc);
		if (acc) CFRelease(acc);
		return errSecParam;
	}
	CFMutableDictionaryRef q = mk_query(svc, acc);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	CFRelease(acc);
	CFRelease(svc);
	return st;
}

static char *mk_errstr(OSStatus st) {
	CFStringRef msg = SecCopyErrorMessageString(st, NULL);
	if (!msg) return NULL;
	CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(msg), kCFStringEncodingUTF8) + 1;
	char *buf = malloc((size_t)max);
	if (buf && !CFStringGetCString(msg, buf, max, kCFStringEncodingUTF8)) {
		free(buf);
		buf = NULL;
	}
	CFRelease(msg);
	return buf;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Service is the Keychain service name under which all items are stored.
const Service = "mountkeeper"

// ErrNotFound is returned when no password is stored for an account.
var ErrNotFound = errors.New("no password stored in the Keychain")

// ErrDenied is returned when the user declines the Keychain access prompt.
var ErrDenied = errors.New("access to the Keychain item was denied")

const (
	errSecItemNotFound   = -25300
	errSecAuthFailed     = -25293
	errSecUserCanceled   = -128
	errSecInteractionNot = -25308
)

// Set stores or replaces the password for account. description is shown in
// Keychain Access as the item comment (e.g. the share URL, never a secret).
func Set(account, description, password string) error {
	if account == "" {
		return errors.New("keychain: empty account")
	}
	cSvc, cAcc := C.CString(Service), C.CString(account)
	cLbl, cCmt := C.CString("Mountkeeper: "+account), C.CString(description)
	pw := C.CBytes([]byte(password))
	defer func() {
		C.memset(pw, 0, C.size_t(len(password)))
		C.free(pw)
		C.free(unsafe.Pointer(cCmt))
		C.free(unsafe.Pointer(cLbl))
		C.free(unsafe.Pointer(cAcc))
		C.free(unsafe.Pointer(cSvc))
	}()
	return statusErr(C.mk_set(cSvc, cAcc, cLbl, cCmt, pw, C.long(len(password))))
}

// Get returns the stored password for account, or ErrNotFound.
func Get(account string) (string, error) {
	if account == "" {
		return "", ErrNotFound
	}
	cSvc, cAcc := C.CString(Service), C.CString(account)
	defer C.free(unsafe.Pointer(cSvc))
	defer C.free(unsafe.Pointer(cAcc))

	var out unsafe.Pointer
	var n C.long
	if err := statusErr(C.mk_get(cSvc, cAcc, &out, &n)); err != nil {
		return "", err
	}
	if out == nil {
		return "", ErrNotFound
	}
	pw := C.GoStringN((*C.char)(out), C.int(n))
	C.memset(out, 0, C.size_t(n))
	C.free(out)
	return pw, nil
}

// Has reports whether a password is stored. It does not read the secret and
// therefore never triggers a Keychain access prompt.
func Has(account string) bool {
	if account == "" {
		return false
	}
	cSvc, cAcc := C.CString(Service), C.CString(account)
	defer C.free(unsafe.Pointer(cSvc))
	defer C.free(unsafe.Pointer(cAcc))
	return C.mk_exists(cSvc, cAcc) == 0
}

// Delete removes the stored password; a missing item is not an error.
func Delete(account string) error {
	if account == "" {
		return nil
	}
	cSvc, cAcc := C.CString(Service), C.CString(account)
	defer C.free(unsafe.Pointer(cSvc))
	defer C.free(unsafe.Pointer(cAcc))
	err := statusErr(C.mk_delete(cSvc, cAcc))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func statusErr(st C.OSStatus) error {
	switch int(st) {
	case 0:
		return nil
	case errSecItemNotFound:
		return ErrNotFound
	case errSecAuthFailed, errSecUserCanceled, errSecInteractionNot:
		return ErrDenied
	}
	if msg := C.mk_errstr(st); msg != nil {
		defer C.free(unsafe.Pointer(msg))
		return fmt.Errorf("keychain: %s (%d)", C.GoString(msg), int(st))
	}
	return fmt.Errorf("keychain: error %d", int(st))
}
