package mount

/*
#cgo LDFLAGS: -framework NetFS -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <NetFS/NetFS.h>

// mk_mount mounts url directly on mountpath. user/pass may be NULL. The
// password is handed to NetFS in memory only. Returns 0, an errno value, or a
// (negative) NetFS/OSStatus code.
static int mk_mount(const char *url, const char *mountpath, const char *user,
		const char *pass, int guest, char *out, int outlen) {
	int rc = -1;
	CFStringRef urlStr = NULL, cfUser = NULL, cfPass = NULL;
	CFURLRef cfURL = NULL, cfPath = NULL;
	CFMutableDictionaryRef openOpts = NULL, mountOpts = NULL;
	CFArrayRef mountpoints = NULL;

	urlStr = CFStringCreateWithCString(kCFAllocatorDefault, url, kCFStringEncodingUTF8);
	if (!urlStr) goto done;
	cfURL = CFURLCreateWithString(kCFAllocatorDefault, urlStr, NULL);
	if (!cfURL) { rc = 22; goto done; } // EINVAL
	cfPath = CFURLCreateFromFileSystemRepresentation(kCFAllocatorDefault,
		(const UInt8 *)mountpath, (CFIndex)strlen(mountpath), true);
	if (!cfPath) { rc = 22; goto done; }

	if (user) cfUser = CFStringCreateWithCString(kCFAllocatorDefault, user, kCFStringEncodingUTF8);
	if (pass) cfPass = CFStringCreateWithCString(kCFAllocatorDefault, pass, kCFStringEncodingUTF8);

	openOpts = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	// Never pop up the system authentication dialog: this is a terminal tool.
	CFDictionarySetValue(openOpts, kNAUIOptionKey, kNAUIOptionNoUI);
	if (guest) CFDictionarySetValue(openOpts, kNetFSUseGuestKey, kCFBooleanTrue);

	mountOpts = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	// Mount on the directory itself instead of creating a child below it.
	CFDictionarySetValue(mountOpts, kNetFSMountAtMountDirKey, kCFBooleanTrue);
	CFDictionarySetValue(mountOpts, kNetFSAllowSubMountsKey, kCFBooleanTrue);
	CFDictionarySetValue(mountOpts, kNetFSSoftMountKey, kCFBooleanTrue);

	rc = NetFSMountURLSync(cfURL, cfPath, cfUser, cfPass, openOpts, mountOpts, &mountpoints);

	if (out && outlen > 0) {
		out[0] = 0;
		if (rc == 0 && mountpoints && CFArrayGetCount(mountpoints) > 0) {
			CFStringRef mp = (CFStringRef)CFArrayGetValueAtIndex(mountpoints, 0);
			if (mp && CFGetTypeID(mp) == CFStringGetTypeID())
				CFStringGetCString(mp, out, outlen, kCFStringEncodingUTF8);
		}
	}

done:
	if (mountpoints) CFRelease(mountpoints);
	if (mountOpts) CFRelease(mountOpts);
	if (openOpts) CFRelease(openOpts);
	if (cfPass) CFRelease(cfPass);
	if (cfUser) CFRelease(cfUser);
	if (cfPath) CFRelease(cfPath);
	if (cfURL) CFRelease(cfURL);
	if (urlStr) CFRelease(urlStr);
	return rc;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Errors that callers react to.
var (
	// ErrAuth means the server rejected the user name or password.
	ErrAuth = errors.New("authentication failed (wrong user name or password)")
	// ErrShareNotFound means the server is reachable but has no such share.
	ErrShareNotFound = errors.New("the share does not exist on the server")
)

// NetFS-specific status codes (NetFS.h) and the macOS EAUTH errno.
const (
	eAuth                  = 80
	enetfsPwdNeedsChange   = -5045
	enetfsPwdPolicy        = -5046
	enetfsAccountRestrict  = -5999
	enetfsNoSharesAvail    = -5998
	enetfsNoAuthMechSupp   = -5997
	enetfsNoProtoVersSupp  = -5996
	userCanceledErr        = -128
	errAuthorizationDenied = -60005
)

// netfsMount mounts url on dir through the NetFS framework and returns the
// resulting mount point. An empty password means "none supplied".
func netfsMount(url, dir, password string, guest bool) (string, error) {
	cURL, cDir := C.CString(url), C.CString(dir)
	defer C.free(unsafe.Pointer(cURL))
	defer C.free(unsafe.Pointer(cDir))

	var cPass *C.char
	if password != "" {
		cPass = C.CString(password)
		defer func() {
			C.memset(unsafe.Pointer(cPass), 0, C.size_t(len(password)))
			C.free(unsafe.Pointer(cPass))
		}()
	}

	g := C.int(0)
	if guest {
		g = 1
	}
	out := make([]C.char, 1024)
	// The user name travels inside the URL (together with the domain).
	rc := int(C.mk_mount(cURL, cDir, nil, cPass, g, &out[0], C.int(len(out))))
	if rc != 0 {
		return "", netfsError(rc)
	}
	if mp := C.GoString(&out[0]); mp != "" {
		return mp, nil
	}
	return dir, nil
}

func netfsError(rc int) error {
	switch rc {
	case eAuth, errAuthorizationDenied:
		return ErrAuth
	case int(syscall.ENOENT):
		return ErrShareNotFound
	case enetfsNoSharesAvail:
		return errors.New("the account may log in but has no access to this share")
	case enetfsAccountRestrict:
		return errors.New("the account is disabled, locked or restricted")
	case enetfsPwdNeedsChange:
		return errors.New("the password has expired and must be changed first")
	case enetfsPwdPolicy:
		return errors.New("the password violates the server's password policy")
	case enetfsNoAuthMechSupp:
		return errors.New("the server supports none of the available authentication methods")
	case enetfsNoProtoVersSupp:
		return errors.New("the server supports no compatible SMB protocol version")
	case userCanceledErr:
		return errors.New("the connection was cancelled")
	}
	if rc > 0 {
		return fmt.Errorf("%s (errno %d)", syscall.Errno(rc).Error(), rc)
	}
	return fmt.Errorf("NetFS error %d", rc)
}
