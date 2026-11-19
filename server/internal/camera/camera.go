package camera

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=13.0 -Wno-deprecated-declarations
#cgo LDFLAGS: -Wl,-framework,AVFoundation -framework Foundation -framework CoreMedia -framework CoreVideo

#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#include <stdlib.h>
#include <string.h>

typedef struct { char *id; char *name; } SRCam;

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

static long sr_signal(dispatch_semaphore_t s, int secs) {
  return dispatch_semaphore_wait(s, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(secs * NSEC_PER_SEC)));
}

// Device-type list for discovery: .external/.continuityCamera exist on 14+;
// ExternalUnknown is their deprecated synonym on 13 (still the only spelling
// that links there, hence -Wno-deprecated-declarations above).
static NSArray<AVCaptureDeviceType> *sr_cam_types(void) {
  if (__builtin_available(macOS 14.0, *)) {
    return @[AVCaptureDeviceTypeBuiltInWideAngleCamera, AVCaptureDeviceTypeExternal, AVCaptureDeviceTypeContinuityCamera];
  }
  return @[AVCaptureDeviceTypeBuiltInWideAngleCamera, AVCaptureDeviceTypeExternalUnknown];
}

// SRCamHolder: video-output delegate keeping the newest frame as a private
// BGRA copy. Delegates run on OUR serial queue (AVFoundation hands them to the
// queue passed to setSampleBufferDelegate:queue:), never the main queue.
@interface SRCamHolder : NSObject <AVCaptureVideoDataOutputSampleBufferDelegate> {
  NSLock *_lock;
  dispatch_semaphore_t _frame; // signaled on every arrived frame
  unsigned char *_buf;
  int _w, _h, _bpr;
}
- (instancetype)init;
- (int)waitFrame:(int)secs;                    // 0 = a frame arrived within secs
- (int)copyOut:(unsigned char **)out w:(int *)w h:(int *)h bpr:(int *)bpr; // 0 = ok
@end

@implementation SRCamHolder
- (instancetype)init {
  if ((self = [super init])) {
    _lock = [NSLock new];
    _frame = dispatch_semaphore_create(0);
  }
  return self;
}
- (void)dealloc {
  // _buf is a raw malloc that swap-in frees; without this the LAST frame of
  // every session leaks on teardown (~8 MB at 1080p, ~33 MB at 4K per
  // idle->capture cycle). Safe here: the only path that drops the last
  // reference is sr_cam_stop, which stopRunning's FIRST — and stopRunning
  // returns only after in-flight delegate callbacks have finished, so no
  // captureOutput can be mid-copy when the holder dies.
  if (_buf) free(_buf);
}
- (int)waitFrame:(int)secs { return sr_signal(_frame, secs) == 0 ? 0 : -1; }
- (void)captureOutput:(AVCaptureOutput *)output
  didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
         fromConnection:(AVCaptureConnection *)connection {
  CVPixelBufferRef pb = CMSampleBufferGetImageBuffer(sampleBuffer);
  if (pb == NULL) return;
  CVPixelBufferLockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
  size_t w = CVPixelBufferGetWidth(pb), h = CVPixelBufferGetHeight(pb);
  size_t bpr = CVPixelBufferGetBytesPerRowOfPlane(pb, 0);
  unsigned char *src = (unsigned char *)CVPixelBufferGetBaseAddressOfPlane(pb, 0);
  unsigned char *copy = (src && w && h && bpr) ? (unsigned char *)malloc(bpr * h) : NULL;
  if (copy) {
    for (size_t row = 0; row < h; row++) memcpy(copy + row * bpr, src + row * bpr, bpr);
    [_lock lock];
    free(_buf);
    _buf = copy; _w = (int)w; _h = (int)h; _bpr = (int)bpr;
    [_lock unlock];
  }
  CVPixelBufferUnlockBaseAddress(pb, kCVPixelBufferLock_ReadOnly);
  dispatch_semaphore_signal(_frame);
}
- (int)copyOut:(unsigned char **)out w:(int *)w h:(int *)h bpr:(int *)bpr {
  *out = NULL;
  [_lock lock];
  if (_buf != NULL) {
    *out = (unsigned char *)malloc((size_t)_bpr * _h);
    if (*out) {
      memcpy(*out, _buf, (size_t)_bpr * _h);
      *w = _w; *h = _h; *bpr = _bpr;
    }
  }
  [_lock unlock];
  return *out ? 0 : -1;
}
@end

// SRCamSession pairs the live session with its frame holder for one handle.
// name is the device's localizedName, kept so the frame-timeout error can say
// WHICH camera stalled when several are registered.
@interface SRCamSession : NSObject
@property(strong, nonatomic) AVCaptureSession *session;
@property(strong, nonatomic) SRCamHolder *holder;
@property(strong, nonatomic) NSString *name;
@end
@implementation SRCamSession
@end

int sr_cam_auth(void) {
  switch ([AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo]) {
    case AVAuthorizationStatusAuthorized: return 0;
    case AVAuthorizationStatusDenied: return 1;
    case AVAuthorizationStatusRestricted: return 2;
    default: return 3; // not determined (first use will prompt)
  }
}

static NSString *sr_cam_names(void) {
  NSMutableArray *parts = [NSMutableArray array];
  for (AVCaptureDevice *d in [AVCaptureDeviceDiscoverySession discoverySessionWithDeviceTypes:sr_cam_types()
                                                                     mediaType:AVMediaTypeVideo position:AVCaptureDevicePositionUnspecified].devices) {
    [parts addObject:d.localizedName ?: @"(unnamed)"];
  }
  return parts.count ? [parts componentsJoinedByString:@", "] : @"none";
}

int sr_cam_list(SRCam **out, char **err) {
  *out = NULL;
  NSArray<AVCaptureDevice *> *devs = [AVCaptureDeviceDiscoverySession
      discoverySessionWithDeviceTypes:sr_cam_types() mediaType:AVMediaTypeVideo
                             position:AVCaptureDevicePositionUnspecified].devices;
  SRCam *recs = (SRCam *)calloc(devs.count ? devs.count : 1, sizeof(SRCam));
  if (recs == NULL) { sr_set_err(err, @"out of memory"); return -1; }
  int n = 0;
  for (AVCaptureDevice *d in devs) {
    recs[n].id = sr_utf8(d.uniqueID);
    recs[n].name = sr_utf8(d.localizedName);
    n++;
  }
  *out = recs; // hand the calloc'd array back — Go only ever sees *out
  return n;
}

void sr_cam_free_list(SRCam *recs, int n) {
  if (recs == NULL) return;
  for (int i = 0; i < n; i++) { free(recs[i].id); free(recs[i].name); }
  free(recs);
}

// sr_cam_find resolves a device by uniqueID. -[AVCaptureDevice initWithUniqueID:]
// is iOS-only (it exists in NO macOS SDK header), and discoverySession +
// defaultDeviceWithDeviceType: are the documented macOS lookup paths, so an
// id lookup must scan the discovery session. The compare is case-SENSITIVE —
// like deviceWithUniqueID:, cameraLine (Go), and the app's picker (Swift).
static AVCaptureDevice *sr_cam_find(NSString *uniqueID) {
  for (AVCaptureDevice *d in [AVCaptureDeviceDiscoverySession discoverySessionWithDeviceTypes:sr_cam_types()
                                                                     mediaType:AVMediaTypeVideo position:AVCaptureDevicePositionUnspecified].devices) {
    if ([d.uniqueID isEqualToString:uniqueID]) return d;
  }
  return nil;
}

// sr_cam_start resolves the device (empty id = system default), starts the
// session, and returns the handle. The default comes from
// defaultDeviceWithDeviceType: (macOS 10.15+, the current API and the only
// one that can express the device-type set sr_cam_types gates on); the
// legacy media-type-only +[AVCaptureDevice defaultDeviceWithMediaType:] is
// superseded — do not use it.
// (The device-id parameter must NOT be named `id`: that shadows the ObjC `id`
// type for the whole scope and breaks the (id) cast in the videoSettings
// dictionary literal below.)
void *sr_cam_start(const char *cid, char **err) {
  AVCaptureDevice *dev = nil;
  NSString *want = cid ? [NSString stringWithUTF8String:cid] : @"";
  if (want.length == 0) {
    dev = [AVCaptureDevice defaultDeviceWithDeviceType:AVCaptureDeviceTypeBuiltInWideAngleCamera
                                             mediaType:AVMediaTypeVideo
                                              position:AVCaptureDevicePositionUnspecified];
    if (dev == nil) { sr_set_err(err, @"no cameras found"); return NULL; }
  } else {
    dev = sr_cam_find(want);
    if (dev == nil) {
      sr_set_err(err, [NSString stringWithFormat:@"camera \"%@\" is not connected (available: %@)", want, sr_cam_names()]);
      return NULL;
    }
  }
  NSError *cerr = nil;
  AVCaptureDeviceInput *input = [AVCaptureDeviceInput deviceInputWithDevice:dev error:&cerr];
  if (input == nil) {
    sr_set_err(err, [NSString stringWithFormat:@"camera %@: %@ — possibly in use by another app; quit it or pick another camera",
                     dev.localizedName, cerr.localizedDescription ?: @"could not open"]);
    return NULL;
  }
  SRCamHolder *holder = [SRCamHolder new];
  AVCaptureSession *session = [[AVCaptureSession alloc] init];
  if (![session canAddInput:input]) {
    sr_set_err(err, [NSString stringWithFormat:@"camera %@: cannot add input", dev.localizedName]);
    return NULL;
  }
  [session addInput:input];
  AVCaptureVideoDataOutput *output = [AVCaptureVideoDataOutput new];
  output.alwaysDiscardsLateVideoFrames = YES;
  output.videoSettings = @{ (id)kCVPixelBufferPixelFormatTypeKey : @(kCVPixelFormatType_32BGRA) };
  [output setSampleBufferDelegate:holder queue:dispatch_queue_create("sr.cam.frame", NULL)];
  if (![session canAddOutput:output]) {
    sr_set_err(err, [NSString stringWithFormat:@"camera %@: cannot add output", dev.localizedName]);
    return NULL;
  }
  [session addOutput:output];
  [session startRunning]; // synchronous-ish; every later wait is bounded anyway

  SRCamSession *h = [SRCamSession new];
  h.session = session;
  h.holder = holder;
  h.name = dev.localizedName ?: @"(unnamed)";
  return (__bridge_retained void *)h;
}

// sr_cam_frame waits (bounded) for the next frame and copies its BGRA bytes.
int sr_cam_frame(void *handle, int wait_secs, unsigned char **out,
                 int *w, int *h, int *bpr, char **err) {
  *out = NULL;
  if (handle == NULL) { sr_set_err(err, @"no camera session"); return -1; }
  SRCamSession *s = (__bridge SRCamSession *)handle;
  if ([s.holder waitFrame:wait_secs] != 0) {
    sr_set_err(err, [NSString stringWithFormat:@"timed out waiting for a frame from \"%@\" (is it in use by another app?)", s.name ?: @"(unnamed)"]);
    return -1;
  }
  return [s.holder copyOut:out w:w h:h bpr:bpr];
}

void sr_cam_stop(void *handle) {
  if (handle == NULL) return;
  SRCamSession *s = (__bridge_transfer SRCamSession *)handle; // pairs __bridge_retained
  [s.session stopRunning];
}
*/
import "C"

import (
	"fmt"
	"image"
	"sync"
	"time"
	"unsafe"
)

// Device names a camera the system can see.
type Device struct {
	ID   string
	Name string
}

// takeCamErr copies a strdup'd C error into a Go string and frees it
// (mirrors sck.takeErr — without the free every camera error leaks).
func takeCamErr(p *C.char) string {
	if p == nil {
		return "unknown error"
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

var (
	mu       sync.Mutex
	active   unsafe.Pointer // C.SRCamSession handle; guarded by mu
	activeID string         // device id of the cached session ("" = default)
	lastUsed time.Time
	idle     *time.Timer // tears the session down idleTTL after the last frame
)

// AuthStatus reports the camera TCC status without starting anything (no
// camera light): "authorized", "denied", "restricted", or "not-determined".
func AuthStatus() string {
	switch C.sr_cam_auth() {
	case 0:
		return "authorized"
	case 1:
		return "denied"
	case 2:
		return "restricted"
	default:
		return "not-determined"
	}
}

// Close releases a cached session immediately — "stop" must turn the camera
// light off (spec §4.1) — and is idempotent, so the tools offline guard can
// call it on every refused call. Takes mu (the same lock Capture holds), so
// it can never pull a session out from under a live frame grab, and cancels
// the idle timer since there is nothing left for it to tear down.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if idle != nil {
		idle.Stop()
	}
	if active != nil {
		C.sr_cam_stop(active)
		active = nil
		activeID = ""
	}
}

// List enumerates cameras without starting a session (used only to name
// alternatives in error messages; the app's picker enumerates in Swift).
func List() ([]Device, error) {
	var cErr *C.char
	var recs *C.SRCam
	n := C.sr_cam_list(&recs, &cErr)
	if n < 0 {
		return nil, fmt.Errorf("camera list: %s", takeCamErr(cErr))
	}
	out := make([]Device, 0, int(n))
	for _, r := range unsafe.Slice(recs, int(n)) { // cgo struct arrays need unsafe.Slice (see sck.Windows)
		out = append(out, Device{ID: C.GoString(r.id), Name: C.GoString(r.name)})
	}
	C.sr_cam_free_list(recs, C.int(n))
	return out, nil
}

// armIdle (re)schedules the teardown: the light must go off ~idleTTL after the
// LAST frame, not merely before the next call (spec §5.1).
func armIdle() {
	if idle != nil {
		idle.Stop()
	}
	idle = time.AfterFunc(idleTTL, func() {
		mu.Lock()
		defer mu.Unlock()
		if active != nil && expired(lastUsed, time.Now(), idleTTL) {
			C.sr_cam_stop(active)
			active = nil
			activeID = ""
		}
	})
}

// Capture returns one settled frame from the device id ("" = system default).
// One mutex serializes AVFoundation and caches a live session per device id
// for idleTTL, mirroring sck's cached filters. Never falls back to another
// camera; every wait is bounded, so a wedged camera cannot hang the server.
func Capture(id string) (*image.RGBA, error) {
	mu.Lock()
	defer mu.Unlock()
	// armIdle rewrites the shared `idle` var, so it must run holding mu; being
	// deferred after the unlock defer, LIFO runs it while the lock is still
	// held. Deferred (not inline) so EVERY exit that leaves a cached session
	// armed the teardown — above all a cold start that caches the session and
	// then fails the first-frame wait, which used to return with the LED on
	// and no timer. Exits that end with active == nil arm a no-op: the
	// callback guards on active != nil and expired(). Running at return also
	// means the timer is never armed before the fresh lastUsed stamp below.
	defer armIdle()
	if st := AuthStatus(); st == "denied" || st == "restricted" {
		return nil, fmt.Errorf("camera access denied (status: %s) — grant: System Settings → Privacy & Security → Camera (restart Screen Reader MCP after granting)", st)
	}
	warm := active != nil && activeID == id && !expired(lastUsed, time.Now(), idleTTL)
	if !warm {
		if active != nil {
			C.sr_cam_stop(active)
			active = nil
			activeID = "" // full reset, like the cold-start failure path below
		}
		cid := C.CString(id)
		defer C.free(unsafe.Pointer(cid))
		var startErr *C.char
		h := C.sr_cam_start(cid, &startErr)
		if h == nil {
			return nil, fmt.Errorf("camera: %s", takeCamErr(startErr))
		}
		active, activeID = h, id
		// Fresh start: wait for the first frame (10 s cap) and let
		// auto-exposure settle; the frame grabbed below is newer, so it
		// carries the adjusted exposure.
		var dw, dh, dbpr C.int
		var dbuf *C.uchar
		var firstErr *C.char
		if C.sr_cam_frame(h, 10, &dbuf, &dw, &dh, &dbpr, &firstErr) != 0 {
			err := takeCamErr(firstErr)
			C.sr_cam_stop(h)
			active = nil
			activeID = ""
			return nil, fmt.Errorf("camera: %s", err)
		}
		if dbuf != nil {
			C.free(unsafe.Pointer(dbuf))
		}
		time.Sleep(settle)
	}
	var buf *C.uchar
	var cw, ch, cbpr C.int
	var ferr *C.char
	// 2 s per-frame wait, warm or cold (cold: this is the post-settle frame).
	if C.sr_cam_frame(active, 2, &buf, &cw, &ch, &cbpr, &ferr) != 0 {
		return nil, fmt.Errorf("camera: %s", takeCamErr(ferr))
	}
	defer C.free(unsafe.Pointer(buf))
	lastUsed = time.Now() // deferred armIdle() fires at return: idleTTL after THIS stamp
	return bgraToRGBA(C.GoBytes(unsafe.Pointer(buf), C.int(int(cbpr)*int(ch))), int(cw), int(ch), int(cbpr))
}
