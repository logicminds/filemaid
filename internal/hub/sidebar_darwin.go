//go:build darwin

package hub

/*
#cgo LDFLAGS: -framework CoreServices
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

#include <CoreServices/CoreServices.h>
#include <stdlib.h>

// addSidebarFavorite adds path to the Finder sidebar favorites if it is not
// already present. Returns 0 on success, 1 if already present, and negative
// values on error.
static int addSidebarFavorite(const char *path) {
	CFStringRef urlString = CFStringCreateWithCString(NULL, path, kCFStringEncodingUTF8);
	if (!urlString) return -1;
	CFURLRef url = CFURLCreateWithFileSystemPath(NULL, urlString, kCFURLPOSIXPathStyle, true);
	CFRelease(urlString);
	if (!url) return -1;

	LSSharedFileListRef list = LSSharedFileListCreate(NULL, kLSSharedFileListFavoriteItems, NULL);
	if (!list) {
		CFRelease(url);
		return -2;
	}

	UInt32 seed;
	CFArrayRef items = LSSharedFileListCopySnapshot(list, &seed);
	int found = 0;
	if (items) {
		CFIndex count = CFArrayGetCount(items);
		for (CFIndex i = 0; i < count; i++) {
			LSSharedFileListItemRef item = (LSSharedFileListItemRef)CFArrayGetValueAtIndex(items, i);
			CFURLRef itemURL = NULL;
			if (LSSharedFileListItemResolve(item, 0, &itemURL, NULL) == noErr && itemURL) {
				if (CFEqual(itemURL, url)) {
					found = 1;
				}
				CFRelease(itemURL);
			}
		}
		CFRelease(items);
	}

	int result;
	if (found) {
		result = 1;
	} else {
		LSSharedFileListItemRef item = LSSharedFileListInsertItemURL(list, kLSSharedFileListItemLast, NULL, NULL, url, NULL, NULL);
		if (item) {
			CFRelease(item);
			result = 0;
		} else {
			result = -3;
		}
	}
	CFRelease(list);
	CFRelease(url);
	return result;
}
#pragma clang diagnostic pop
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// osSidebarAdder pins a folder to the Finder sidebar using the deprecated but
// still functional LSSharedFileList API.
type osSidebarAdder struct{}

func (osSidebarAdder) Add(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	switch res := C.addSidebarFavorite(cpath); res {
	case 0, 1:
		return nil
	default:
		return fmt.Errorf("LSSharedFileListInsertItemURL failed (code %d)", int(res))
	}
}
