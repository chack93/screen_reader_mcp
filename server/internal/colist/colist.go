// Package colist enumerates on-screen windows via CoreGraphics
// (CGWindowListCopyWindowInfo): one API call in a few milliseconds, versus
// the per-window Apple-event round trips that make AppleScript enumeration
// take tens of seconds on machines with many apps.
//
// kCGWindowName (the title) is only populated when the process has Screen
// Recording permission; without it the windows still list, just untitled.
package colist

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

typedef struct { int pid; char *title; int x, y, w, h; } ColistWin;

static int colist_scan(ColistWin **out) {
  CFArrayRef arr = CGWindowListCopyWindowInfo(
      (CGWindowListOption)(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements),
      kCGNullWindowID);
  if (arr == NULL) return -1;
  CFIndex n = CFArrayGetCount(arr);
  ColistWin *recs = (ColistWin *)calloc((size_t)(n > 0 ? n : 1), sizeof(ColistWin));
  if (recs == NULL) { CFRelease(arr); return -1; }
  int count = 0;
  for (CFIndex i = 0; i < n; i++) {
    CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(arr, i);
    if (d == NULL || CFGetTypeID(d) != CFDictionaryGetTypeID()) continue;
    // Normal application windows only; menu bars, docks, overlays have other layers.
    CFTypeRef lv = CFDictionaryGetValue(d, kCGWindowLayer);
    double layer = -1;
    if (lv != NULL && CFGetTypeID(lv) == CFNumberGetTypeID())
      CFNumberGetValue((CFNumberRef)lv, kCFNumberDoubleType, &layer);
    if ((int)layer != 0) continue;
    double pid = 0;
    CFTypeRef pv = CFDictionaryGetValue(d, kCGWindowOwnerPID);
    if (pv != NULL && CFGetTypeID(pv) == CFNumberGetTypeID())
      CFNumberGetValue((CFNumberRef)pv, kCFNumberDoubleType, &pid);
    CFTypeRef bv = CFDictionaryGetValue(d, kCGWindowBounds);
    if (bv == NULL || CFGetTypeID(bv) != CFDictionaryGetTypeID()) continue;
    CFDictionaryRef b = (CFDictionaryRef)bv;
    double x = 0, y = 0, w = 0, h = 0;
    CFTypeRef v;
    if ((v = CFDictionaryGetValue(b, CFSTR("X"))) != NULL) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &x);
    if ((v = CFDictionaryGetValue(b, CFSTR("Y"))) != NULL) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &y);
    if ((v = CFDictionaryGetValue(b, CFSTR("W"))) != NULL) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &w);
    if ((v = CFDictionaryGetValue(b, CFSTR("H"))) != NULL) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &h);
    if (w <= 0 || h <= 0) continue;
    char *title = NULL;
    CFTypeRef tv = CFDictionaryGetValue(d, kCGWindowName);
    if (tv != NULL && CFGetTypeID(tv) == CFStringGetTypeID()) {
      char buf[8192];
      if (CFStringGetCString((CFStringRef)tv, buf, (CFIndex)sizeof(buf), kCFStringEncodingUTF8))
        title = strdup(buf);
    }
    recs[count].pid = (int)pid;
    recs[count].title = title;
    recs[count].x = (int)x;
    recs[count].y = (int)y;
    recs[count].w = (int)w;
    recs[count].h = (int)h;
    count++;
  }
  CFRelease(arr);
  *out = recs;
  return count;
}

static void colist_free(ColistWin *recs, int n) {
  for (int i = 0; i < n; i++) free(recs[i].title);
  free(recs);
}
*/
import "C"

import (
	"errors"
	"unsafe"

	"screenreader/internal/geom"
	"screenreader/internal/window"
)

// List returns layer-0 on-screen windows in front-to-back z-order. Rects are
// screen-absolute with top-left origin, Y down — the same convention the rest
// of the server uses (kCGWindowBounds shares the global display space).
func List() ([]window.CGWin, error) {
	var recs *C.ColistWin
	n := C.colist_scan(&recs)
	if n < 0 {
		return nil, errors.New("colist: CGWindowListCopyWindowInfo failed")
	}
	out := make([]window.CGWin, 0, int(n))
	if n > 0 {
		for _, r := range unsafe.Slice(recs, int(n)) {
			out = append(out, window.CGWin{
				PID:   int(r.pid),
				Title: C.GoString(r.title),
				Rect:  geom.Rect{X: int(r.x), Y: int(r.y), W: int(r.w), H: int(r.h)},
			})
		}
	}
	C.colist_free(recs, n) // cgo forbids defer with C args — free explicitly
	return out, nil
}
