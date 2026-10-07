// Package colist enumerates application windows via CoreGraphics
// (CGWindowListCopyWindowInfo): one API call in a few milliseconds, versus
// the per-window Apple-event round trips that make AppleScript enumeration
// take tens of seconds on machines with many apps. Layer-0 windows are kept
// even when minimized (matching the old AppleScript walk), but offscreen
// records without a title are dropped as helper/phantom windows.
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

typedef struct { int pid; char *title; int x, y, w, h; int onscreen; } ColistWin;

static int colist_scan(ColistWin **out) {
  CFArrayRef arr = CGWindowListCopyWindowInfo(kCGWindowListOptionAll, kCGNullWindowID);
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
    // OptionAll so minimized/hidden windows still appear (the old AppleScript
    // path listed them — the Remote Desktop window may well be minimized).
    // Offscreen untitled layer-0 records are helper/phantom windows; keep
    // offscreen ones only when they carry a real title.
    CFTypeRef sv = CFDictionaryGetValue(d, CFSTR("kCGWindowIsOnscreen"));
    int onscreen = 0;
    if (sv != NULL && CFGetTypeID(sv) == CFBooleanGetTypeID())
      onscreen = CFBooleanGetValue((CFBooleanRef)sv) ? 1 : 0;
    double pid = 0;
    CFTypeRef pv = CFDictionaryGetValue(d, kCGWindowOwnerPID);
    if (pv != NULL && CFGetTypeID(pv) == CFNumberGetTypeID())
      CFNumberGetValue((CFNumberRef)pv, kCFNumberDoubleType, &pid);
    CFTypeRef bv = CFDictionaryGetValue(d, kCGWindowBounds);
    if (bv == NULL || CFGetTypeID(bv) != CFDictionaryGetTypeID()) continue;
    CFDictionaryRef b = (CFDictionaryRef)bv;
    double x = 0, y = 0, w = 0, h = 0;
    CFTypeRef v;
    // Documented keys are X/Y/W/H, but recent macOS returns X/Y/Width/Height —
    // read both spellings so the code is version-agnostic.
    if ((v = CFDictionaryGetValue(b, CFSTR("X"))) != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &x);
    if ((v = CFDictionaryGetValue(b, CFSTR("Y"))) != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &y);
    if ((v = CFDictionaryGetValue(b, CFSTR("W"))) != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &w);
    else if ((v = CFDictionaryGetValue(b, CFSTR("Width"))) != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &w);
    if ((v = CFDictionaryGetValue(b, CFSTR("H"))) != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &h);
    else if ((v = CFDictionaryGetValue(b, CFSTR("Height"))) != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &h);
    if (w <= 0 || h <= 0) continue;
    char *title = NULL;
    CFTypeRef tv = CFDictionaryGetValue(d, kCGWindowName);
    if (tv != NULL && CFGetTypeID(tv) == CFStringGetTypeID()) {
      char buf[8192];
      if (CFStringGetCString((CFStringRef)tv, buf, (CFIndex)sizeof(buf), kCFStringEncodingUTF8))
        title = strdup(buf);
    }
    if (!onscreen && (title == NULL || title[0] == 0)) {
      free(title); // offscreen helper/phantom window — not a selectable target
      continue;
    }
    if (!onscreen && w * h < 10000) { // minimized junk like 1x1 telemetry windows
      free(title);
      continue;
    }
    recs[count].pid = (int)pid;
    recs[count].title = title;
    recs[count].x = (int)x;
    recs[count].y = (int)y;
    recs[count].w = (int)w;
    recs[count].h = (int)h;
    recs[count].onscreen = onscreen;
    count++;
  }
  // Onscreen windows first (stable insertion sort, n is small) so minimized
  // entries sink below the real ones in the picker without disturbing order.
  for (int i = 1; i < count; i++) {
    ColistWin key = recs[i];
    int j = i - 1;
    while (j >= 0 && !recs[j].onscreen && key.onscreen) {
      recs[j + 1] = recs[j];
      j--;
    }
    recs[j + 1] = key;
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

// List returns layer-0 application windows (minimized ones included when they
// have a title) in CGWindowList order. Rects are screen-absolute with
// top-left origin, Y down — the same convention the rest of the server uses
// (kCGWindowBounds shares the global display space).
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
	C.colist_free(recs, n) // freed after Go has copied every field (no defer needed)
	return out, nil
}
