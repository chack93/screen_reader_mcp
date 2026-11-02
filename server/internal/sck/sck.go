// Package sck captures ONE window through ScreenCaptureKit. The window is
// rendered by the window server, so windows stacked on top of it are excluded
// and the target does not have to be frontmost — the only way to get an
// unobstructed window image on macOS 15+, where CGWindowListCreateImage has
// been obsoleted (not merely deprecated: it no longer compiles against the
// current SDK).
//
// SCScreenshotManager only exists on macOS 14+, so every entry point checks for
// it at runtime: on macOS 13 (the app's floor) Available() reports false and the
// calls return a clear error instead of trapping.
//
// TCC: this rides the same Screen Recording grant as region capture, but the two
// are not interchangeable in practice — a process that captures fine through the
// legacy display API can still be answered with -3801 ("The user declined
// TCCs") here. tools.doctor reports that case explicitly.
package sck

/*
// ScreenCaptureKit.framework itself exists from macOS 12.3, so a plain (not
// weak) framework reference still launches on macOS 13. The macOS 14-only class
// is resolved by name instead — a #cgo LDFLAGS weak link (-Wl,-weak_framework)
// is not in cgo's flag allowlist, so runtime lookup is how the 14-only symbol
// stays out of the binary.
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -Wl,-framework,ScreenCaptureKit -framework Foundation -framework CoreGraphics -framework CoreVideo

#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreVideo/CoreVideo.h>
#include <stdlib.h>
#include <string.h>

typedef struct { unsigned int id; char *app; char *title; int x, y, w, h; int onscreen; } SRWin;

// SCK hands its completion handlers to a background queue (verified: the handler
// runs with isMainThread == 0 in a process with no run loop), so a plain timed
// semaphore wait is enough here. It is bounded anyway: a wedged capture must
// never hang the MCP server.
#define SR_WAIT(s) dispatch_semaphore_wait((s), dispatch_time(DISPATCH_TIME_NOW, (int64_t)(15 * NSEC_PER_SEC)))

#define SR_NEEDS_14 @"window capture needs macOS 14 or newer (region capture works everywhere)"

static void sr_set_err(char **err, NSString *msg) {
  if (err == NULL) return;
  const char *c = msg ? msg.UTF8String : "unknown error";
  *err = strdup(c ? c : "unknown error");
}

static char *sr_utf8(NSString *s) {
  if (s == nil) return strdup("");
  const char *c = s.UTF8String;
  return strdup(c ? c : "");
}

// SCScreenshotManager only exists on macOS 14+. Declaring just the method that
// is called, and resolving the class by name, keeps the link-time reference to
// it out of the binary so macOS 13 can still load it.
@protocol SRScreenshotManager <NSObject>
+ (void)captureImageWithFilter:(SCContentFilter *)contentFilter
                 configuration:(SCStreamConfiguration *)config
             completionHandler:(void (^)(CGImageRef _Nullable, NSError *_Nullable))completionHandler;
@end

static Class sr_screenshot_manager(void) {
  Class cls = NSClassFromString(@"SCScreenshotManager");
  if (cls == nil) return nil;
  if (![cls respondsToSelector:@selector(captureImageWithFilter:configuration:completionHandler:)]) return nil;
  return cls;
}

// sr_have_sck is true when everything window capture needs is present at runtime.
static BOOL sr_have_sck(void) {
  if (NSClassFromString(@"SCShareableContent") == nil) return NO;
  if (NSClassFromString(@"SCContentFilter") == nil) return NO;
  if (sr_screenshot_manager() == nil) return NO;
  return YES;
}

// sr_display_scale returns the pixel-per-point factor of the display covering
// most of r (Retina: 2.0). Pure CoreGraphics, so it also answers when
// ScreenCaptureKit is unavailable.
static double sr_display_scale(CGRect r) {
  CGDirectDisplayID ids[8];
  uint32_t n = 0;
  if (CGGetActiveDisplayList(8, ids, &n) != kCGErrorSuccess || n == 0) return 1.0;
  CGDirectDisplayID best = CGMainDisplayID();
  double bestArea = -1;
  for (uint32_t i = 0; i < n; i++) {
    CGRect inter = CGRectIntersection(CGDisplayBounds(ids[i]), r);
    double area = CGRectIsNull(inter) ? 0 : inter.size.width * inter.size.height;
    if (area > bestArea) { bestArea = area; best = ids[i]; }
  }
  CGDisplayModeRef m = CGDisplayCopyDisplayMode(best);
  if (m == NULL) return 1.0;
  size_t pw = CGDisplayModeGetPixelWidth(m), mw = CGDisplayModeGetWidth(m);
  double scale = (pw > 0 && mw > 0) ? (double)pw / (double)mw : 1.0;
  CGDisplayModeRelease(m);
  return scale > 0 ? scale : 1.0;
}

int sr_sck_available(void) {
  if (!sr_have_sck()) return 0;
  if (__builtin_available(macOS 14.0, *)) return 1;
  return 0;
}

void sr_os_version(int *major, int *minor, int *patch) {
  NSOperatingSystemVersion v = [[NSProcessInfo processInfo] operatingSystemVersion];
  *major = (int)v.majorVersion;
  *minor = (int)v.minorVersion;
  *patch = (int)v.patchVersion;
}

double sr_display_scale_for(int x, int y, int w, int h) {
  return sr_display_scale(CGRectMake(x, y, w, h));
}

// sr_sck_windows lists layer-0 application windows. Minimized ones are kept
// when they carry a title (the rule colist applies too); untitled offscreen
// records are helper/phantom windows.
int sr_sck_windows(SRWin **out, char **err) {
  *out = NULL;
  if (!sr_have_sck()) { sr_set_err(err, SR_NEEDS_14); return -1; }
  if (__builtin_available(macOS 14.0, *)) {
    __block SCShareableContent *content = nil;
    __block NSError *cerr = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:NO
        completionHandler:^(SCShareableContent *c, NSError *e) {
          content = c;
          cerr = e;
          dispatch_semaphore_signal(sem);
        }];
    if (SR_WAIT(sem) != 0) { sr_set_err(err, @"SCShareableContent timed out"); return -1; }
    if (content == nil) {
      sr_set_err(err, cerr
          ? [NSString stringWithFormat:@"SCShareableContent failed (%ld): %@", (long)cerr.code, cerr.localizedDescription]
          : @"SCShareableContent returned nothing");
      return -1;
    }
    NSArray<SCWindow *> *wins = content.windows;
    SRWin *recs = (SRWin *)calloc(wins.count ? wins.count : 1, sizeof(SRWin));
    if (recs == NULL) { sr_set_err(err, @"out of memory"); return -1; }
    int count = 0;
    for (SCWindow *w in wins) {
      if (w.windowLayer != 0) continue; // menus/dock/wallpaper live on other layers
      if (w.frame.size.width <= 0 || w.frame.size.height <= 0) continue;
      if (!w.isOnScreen && w.title.length == 0) continue;
      recs[count].id = w.windowID;
      recs[count].app = sr_utf8(w.owningApplication.applicationName);
      recs[count].title = sr_utf8(w.title);
      recs[count].x = (int)w.frame.origin.x;
      recs[count].y = (int)w.frame.origin.y;
      recs[count].w = (int)w.frame.size.width;
      recs[count].h = (int)w.frame.size.height;
      recs[count].onscreen = w.isOnScreen ? 1 : 0;
      count++;
    }
    return count;
  }
  sr_set_err(err, SR_NEEDS_14);
  return -1;
}

void sr_sck_free_windows(SRWin *recs, int n) {
  if (recs == NULL) return;
  for (int i = 0; i < n; i++) { free(recs[i].app); free(recs[i].title); }
  free(recs);
}

// sr_sck_filter builds the single-window content filter and reports the
// content's pixel-per-point scale (0 = unknown, caller falls back).
void *sr_sck_filter(unsigned int window_id, double *scale, char **err) {
  *scale = 0;
  if (!sr_have_sck()) { sr_set_err(err, SR_NEEDS_14); return NULL; }
  if (__builtin_available(macOS 14.0, *)) {
    __block SCShareableContent *content = nil;
    __block NSError *cerr = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [SCShareableContent getShareableContentExcludingDesktopWindows:NO onScreenWindowsOnly:NO
        completionHandler:^(SCShareableContent *c, NSError *e) {
          content = c;
          cerr = e;
          dispatch_semaphore_signal(sem);
        }];
    if (SR_WAIT(sem) != 0) { sr_set_err(err, @"SCShareableContent timed out"); return NULL; }
    if (content == nil) {
      sr_set_err(err, cerr
          ? [NSString stringWithFormat:@"SCShareableContent failed (%ld): %@", (long)cerr.code, cerr.localizedDescription]
          : @"SCShareableContent returned nothing");
      return NULL;
    }
    SCWindow *match = nil;
    for (SCWindow *w in content.windows) {
      if (w.windowID == window_id) { match = w; break; }
    }
    if (match == nil) {
      sr_set_err(err, [NSString stringWithFormat:@"window %u is not shareable (closed, minimized, or on another Space)", window_id]);
      return NULL;
    }
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:match];
    SCShareableContentInfo *info = [SCShareableContent infoForFilter:filter];
    if (info) *scale = info.pointPixelScale;
    return (__bridge_retained void *)filter;
  }
  sr_set_err(err, SR_NEEDS_14);
  return NULL;
}

void sr_sck_filter_release(void *handle) {
  if (handle == NULL) return;
  (void)CFBridgingRelease(handle); // pairs the __bridge_retained +1
}

// sr_sck_render renders the filtered window into a top-down RGBA8 buffer at
// width x height pixels (SCK scales). Both dimensions must be > 0: on macOS a
// zero SCStreamConfiguration size silently falls back to 1920x1080.
int sr_sck_render(void *handle, int width, int height,
                  unsigned char **out, int *out_w, int *out_h, char **err) {
  *out = NULL; *out_w = 0; *out_h = 0;
  if (handle == NULL) { sr_set_err(err, @"no window filter"); return -1; }
  if (width <= 0 || height <= 0) { sr_set_err(err, @"bad output size"); return -1; }
  if (!sr_have_sck()) { sr_set_err(err, SR_NEEDS_14); return -1; }
  if (__builtin_available(macOS 14.0, *)) {
    Class manager = sr_screenshot_manager();
    if (manager == nil) { sr_set_err(err, SR_NEEDS_14); return -1; }
    SCContentFilter *filter = (__bridge SCContentFilter *)handle;

    SCStreamConfiguration *cfg = [[SCStreamConfiguration alloc] init];
    cfg.width = (size_t)width;
    cfg.height = (size_t)height;
    cfg.showsCursor = NO;
    cfg.ignoreShadowsSingleWindow = YES; // image maps 1:1 onto the window frame
    cfg.pixelFormat = kCVPixelFormatType_32BGRA;
    cfg.queueDepth = 1;

    __block CGImageRef image = NULL;
    __block NSError *cerr = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [(Class<SRScreenshotManager>)manager captureImageWithFilter:filter configuration:cfg
        completionHandler:^(CGImageRef img, NSError *e) {
          if (img != NULL) image = CGImageRetain(img);
          cerr = e;
          dispatch_semaphore_signal(sem);
        }];
    if (SR_WAIT(sem) != 0) { sr_set_err(err, @"SCScreenshotManager timed out"); return -1; }
    if (image == NULL) {
      sr_set_err(err, cerr
          ? [NSString stringWithFormat:@"capture failed (%ld): %@", (long)cerr.code, cerr.localizedDescription]
          : @"capture returned no image");
      return -1;
    }

    size_t w = CGImageGetWidth(image), h = CGImageGetHeight(image);
    unsigned char *buf = (unsigned char *)calloc(w * h * 4, 1);
    CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    CGContextRef ctx = CGBitmapContextCreate(buf, w, h, 8, w * 4, cs,
        kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    if (ctx == NULL) {
      free(buf);
      CGColorSpaceRelease(cs);
      CGImageRelease(image);
      sr_set_err(err, @"could not build a bitmap context");
      return -1;
    }
    // CGContext is bottom-up; flip so row 0 is the top row (image.RGBA order).
    CGContextTranslateCTM(ctx, 0, (CGFloat)h);
    CGContextScaleCTM(ctx, 1.0, -1.0);
    CGContextSetInterpolationQuality(ctx, kCGInterpolationNone);
    CGContextDrawImage(ctx, CGRectMake(0, 0, (CGFloat)w, (CGFloat)h), image);
    CGContextRelease(ctx);
    CGColorSpaceRelease(cs);
    CGImageRelease(image);

    *out = buf;
    *out_w = (int)w;
    *out_h = (int)h;
    return 0;
  }
  sr_set_err(err, SR_NEEDS_14);
  return -1;
}

void sr_sck_free(unsigned char *p) { free(p); }
*/
import "C"

import (
	"fmt"
	"image"
	"math"
	"sync"
	"time"
	"unsafe"

	"screenreader/internal/geom"
	"screenreader/internal/window"
)

// Available reports whether window capture can run here (macOS 14+ with
// ScreenCaptureKit present).
func Available() bool { return C.sr_sck_available() != 0 }

// OSVersion is the system version as "major.minor.patch" (doctor output).
func OSVersion() string {
	var maj, min, pat C.int
	C.sr_os_version(&maj, &min, &pat)
	return fmt.Sprintf("%d.%d.%d", int(maj), int(min), int(pat))
}

// DisplayScale is the pixel-per-point factor of the display covering most of r
// (2.0 on Retina). Works even without ScreenCaptureKit.
func DisplayScale(r geom.Rect) float64 {
	return float64(C.sr_display_scale_for(C.int(r.X), C.int(r.Y), C.int(r.W), C.int(r.H)))
}

func takeErr(p *C.char) error {
	if p == nil {
		return fmt.Errorf("unknown ScreenCaptureKit error")
	}
	defer C.free(unsafe.Pointer(p))
	return fmt.Errorf("%s", C.GoString(p))
}

// Windows lists layer-0 application windows (minimized ones included when they
// carry a title), in ScreenCaptureKit's order. Rects are screen-absolute
// points: SCWindow.frame lives in the same global display space as
// kCGWindowBounds, so nothing needs converting.
func Windows() ([]window.Candidate, error) {
	var recs *C.SRWin
	var cerr *C.char
	n := C.sr_sck_windows(&recs, &cerr)
	if n < 0 {
		return nil, takeErr(cerr)
	}
	out := make([]window.Candidate, 0, int(n))
	for _, r := range unsafe.Slice(recs, int(n)) {
		out = append(out, window.Candidate{
			ID:       uint32(r.id),
			App:      C.GoString(r.app),
			Title:    C.GoString(r.title),
			Rect:     geom.Rect{X: int(r.x), Y: int(r.y), W: int(r.w), H: int(r.h)},
			OnScreen: r.onscreen != 0,
		})
	}
	C.sr_sck_free_windows(recs, C.int(n)) // the array is allocated even for n == 0
	return out, nil
}

// filterTTL bounds how long a content filter is reused. Building one costs a
// whole-system SCShareableContent round trip, so captures of the same window —
// the normal case, one target many captures — reuse it.
const filterTTL = 30 * time.Second

// bigSide is the size above which a failed render is retried smaller: SCK's
// output ceiling varies by macOS version and a Retina capture of a large window
// can exceed it.
const bigSide = 2048

type filterEntry struct {
	handle  unsafe.Pointer
	scale   float64 // pixels per point for this window's content
	created time.Time
}

// mu serializes ScreenCaptureKit use. It also protects the cache, and a filter
// handle must never be released while another capture is rendering with it.
var mu sync.Mutex

var cache = map[uint32]*filterEntry{}

// filterFor returns w's cached content filter, rebuilding it when absent or
// stale. Callers must hold mu.
func filterFor(w window.Candidate, fresh bool) (*filterEntry, error) {
	if !fresh {
		if e := cache[w.ID]; e != nil && time.Since(e.created) < filterTTL {
			return e, nil
		}
	}
	prune()
	var scale C.double
	var cerr *C.char
	h := C.sr_sck_filter(C.uint(w.ID), &scale, &cerr)
	if h == nil {
		return nil, takeErr(cerr)
	}
	s := float64(scale)
	if s <= 0 {
		s = DisplayScale(w.Rect) // SCK did not say; derive it from the display mode
	}
	e := &filterEntry{handle: unsafe.Pointer(h), scale: s, created: time.Now()}
	if old := cache[w.ID]; old != nil {
		C.sr_sck_filter_release(old.handle)
	}
	cache[w.ID] = e
	return e, nil
}

// prune drops expired filters. A long-lived server sees many window ids over a
// day (every window it ever captured), and each entry holds a live
// SCContentFilter, so expired ones must not pile up. Callers must hold mu.
func prune() {
	for id, e := range cache {
		if time.Since(e.created) >= filterTTL {
			C.sr_sck_filter_release(e.handle)
			delete(cache, id)
		}
	}
}

// dropLocked releases a cached filter so a stale handle is never reused after a
// failed capture. Callers must hold mu.
func dropLocked(id uint32) {
	if e := cache[id]; e != nil {
		C.sr_sck_filter_release(e.handle)
		delete(cache, id)
	}
}

// render draws the filtered window into an image.RGBA at width x height pixels.
func render(e *filterEntry, width, height int) (*image.RGBA, error) {
	var buf *C.uchar
	var ow, oh C.int
	var cerr *C.char
	if C.sr_sck_render(e.handle, C.int(width), C.int(height), &buf, &ow, &oh, &cerr) != 0 || buf == nil {
		return nil, takeErr(cerr)
	}
	defer C.sr_sck_free((*C.uchar)(unsafe.Pointer(buf)))
	w, h := int(ow), int(oh)
	return &image.RGBA{
		Pix:    C.GoBytes(unsafe.Pointer(buf), C.int(w*h*4)),
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}, nil
}

// Capture renders w alone as an RGBA image: windows on top of it are excluded
// and w does not have to be frontmost. The image is at most maxWidth px wide
// (maxWidth <= 0 keeps native resolution, Retina pixels included), so callers
// map image points back to screen points through the reported size, exactly as
// they do for a region capture.
func Capture(w window.Candidate, maxWidth int) (*image.RGBA, error) {
	mu.Lock()
	defer mu.Unlock()

	e, err := filterFor(w, false)
	if err != nil {
		return nil, err
	}
	// Native size comes from the caller's rect, not from the cache: the window may
	// have been resized since the filter was built, and rendering new content at
	// the old pixel size would distort it (and skew image→screen mapping).
	nativeW := int(math.Round(float64(w.Rect.W) * e.scale))
	nativeH := int(math.Round(float64(w.Rect.H) * e.scale))
	tw, th := geom.ScaleDown(nativeW, nativeH, maxWidth)
	img, err := render(e, tw, th)
	if err == nil {
		return img, nil
	}

	// A stale filter (window closed and reopened, window server reset) is the
	// usual cause: rebuild it from a fresh snapshot and try again.
	first := err
	dropLocked(w.ID)
	e2, err2 := filterFor(w, true)
	if err2 != nil {
		return nil, fmt.Errorf("%v (rebuilt filter: %v)", first, err2)
	}
	if img, err = render(e2, tw, th); err == nil {
		return img, nil
	}
	if tw <= bigSide && th <= bigSide {
		return nil, err
	}
	// Still failing on a big request: SCK may reject the size. Halving once is a
	// softer image the agent can still read and map, which beats a failed read.
	if img2, err3 := render(e2, tw/2, th/2); err3 == nil {
		return img2, nil
	}
	return nil, err
}
