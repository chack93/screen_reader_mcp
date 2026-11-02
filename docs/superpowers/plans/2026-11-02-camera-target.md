# Camera Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a fourth target type — a camera (webcam / external camera) selectable in the app's settings — so the agent's `capture` tool returns still camera frames as PNG while `mode: "camera"`, with pointer input blocked during that mode.

**Architecture:** A new `internal/camera` Go package wraps AVFoundation through cgo (same shape as `internal/sck`: one mutex, bounded semaphore waits, loud named errors, cached session with 30 s idle teardown). The Swift menu-bar app owns selection: a 4th Target segment + device picker writing `mode: "camera"` and `camera: {id, name}` to config.json, which the Go server re-reads per action.

**Tech Stack:** Go 1.22+ with cgo/Objective-C (AVFoundation, CoreMedia, CoreVideo), `image/png` + `golang.org/x/image/draw`; Swift/AppKit + AVFoundation (SwiftPM); JSON config at `~/.config/screenreader/config.json`.

**Spec:** `docs/superpowers/specs/2026-11-02-camera-target-design.md` (read it first; it argues every choice below).

## Global Constraints

- macOS floor is **13.0** (`app/Package.swift` `.macOS(.v13)`): anything macOS 14+ must be behind `__builtin_available(macOS 14.0, *)` (ObjC) / `if #available(macOS 14.0, *)` (Swift).
- All Go build/test gates run from `server/` with `CGO_ENABLED=1` except the pure packages `internal/config` (cgo-free). Keep code `gofmt`-clean. Gate on `CGO_ENABLED=1 go vet ./...` + `CGO_ENABLED=1 go test ./...` before any final commit.
- cgo `LDFLAGS` must stay inside cgo's flag allowlist — use `-Wl,-framework,X` (as `internal/sck/sck.go` does), never `-Wl,-weak_framework`.
- No new MCP tools and no changes to the tool count (still 15) or the read-only allowlist; `set_target` must keep rejecting `mode: "camera"`.
- Camera errors are loud and name the way out; never silently fall back to another camera or to a screen grab.
- Unit tests never start a real capture session (they run under the developer's TCC context; only manual live verification does).
- Coordinates/config semantics from AGENTS.md stay intact: screen-absolute top-left origin, config re-read on every action, per-call args override config.
- Commit message style: `feat: ...`, `fix: ...`, `docs: ...` (see `git log`).

**Prerequisite (do before Task 1):** the working tree currently carries uncommitted window-capture work (`server/internal/sck/`, `internal/tools/tools.go`, `internal/capture/capture.go`, `internal/config/config.go`, tests, `AGENTS.md`, README/TODO/docs — see `git status`). This plan must land on top of it committed: ask the user to commit it themselves, or with their explicit approval run `CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...` in `server/`, `swift build && swift test` in `app/`, then commit that work as one commit. Do not proceed on a dirty tree.

---

### Task 1: Config — the `camera` key (pure Go)

**Files:**
- Modify: `server/internal/config/config.go` (struct + doc comments)
- Test: `server/internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing (starts from HEAD).
- Produces: `config.CameraCfg{ID string \`json:"id"\`; Name string \`json:"name"\`}` and field `Config.Camera CameraCfg \`json:"camera"\`` — used by Tasks 5, 7, 9, 10.

- [ ] **Step 1: Write the failing test** — append to `server/internal/config/config_test.go`:

```go
func TestCameraKeyRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"mode":"camera","camera":{"id":"AA11","name":"Cam X"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != "camera" || c.Camera.ID != "AA11" || c.Camera.Name != "Cam X" {
		t.Fatalf("parsed %+v", c)
	}
	if err := Save(p, c); err != nil { // a set_target-style full rewrite must keep the app's key
		t.Fatal(err)
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Camera.ID != "AA11" || c2.Camera.Name != "Cam X" {
		t.Fatalf("camera key lost on Save: %+v", c2.Camera)
	}
	if Defaults().Camera.ID != "" || Defaults().Mode != "app" {
		t.Fatal("defaults changed")
	}
}
```

- [ ] **Step 2: Run it to verify failure** — `cd server && go test ./internal/config/ -run TestCameraKeyRoundTrip` must fail to compile (`Camera` unknown).
- [ ] **Step 3: Implement** — in `config.go`, after `type InputCfg`, add:

```go
// CameraCfg names the camera target's device. App-owned like Hotkey: only the
// menu-bar app writes it; the field exists so set_target's Save preserves it.
// ID is AVCaptureDevice.uniqueID (stable across replugs/reboots); empty =
// system-default video device. Name is display-only.
type CameraCfg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
```

and in `Config`, right after the `Mode` field, add:

```go
	Camera CameraCfg  `json:"camera"` // target device when Mode == "camera"
```

Also extend the `Mode` comment to `// "app" | "rect" | "screen" | "camera"`.
- [ ] **Step 4: Run tests** — `cd server && go test ./internal/config/ ./internal/geom/` → PASS.
- [ ] **Step 5: Commit**

```bash
git add server/internal/config/
git commit -m "feat(config): camera device key (app-owned, carried by the server)"
```

---

### Task 2: `internal/camera` — pure helpers (expiry + pixel conversion)

**Files:**
- Create: `server/internal/camera/cache.go`
- Create: `server/internal/camera/convert.go`
- Test: `server/internal/camera/camera_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `expired(lastUsed, now time.Time, ttl time.Duration) bool`, `bgraToRGBA(bgra []byte, width, height, rowBytes int) (*image.RGBA, error)` — both used by Task 3. Constants `idleTTL = 30 * time.Second`, `settle = 500 * time.Millisecond`.

- [ ] **Step 1: Write the failing tests** — `server/internal/camera/camera_test.go` (package `camera`; the package compiles under cgo but these tests touch no ObjC code):

```go
package camera

import (
	"testing"
	"time"
)

func TestExpired(t *testing.T) {
	t0 := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if expired(t0, t0.Add(30*time.Second), idleTTL) {
		t.Fatal("exactly at ttl must still be warm")
	}
	if !expired(t0, t0.Add(30*time.Second+time.Millisecond), idleTTL) {
		t.Fatal("past ttl must be expired")
	}
}

func TestBgraToRGBA(t *testing.T) {
	// 2x1 row, padded to rowBytes 10 (cameras pad rows; the copy must skip padding).
	src := []byte{
		10, 20, 30, 255, // B G R A  → R30 G20 B10
		40, 50, 60, 255, //          → R60 G50 B40
		0xAA, 0xAA, // padding
	}
	img, err := bgraToRGBA(src, 2, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{30, 20, 10, 255, 60, 50, 40, 255}
	for i, w := range want {
		if img.Pix[i] != w {
			t.Fatalf("pix[%d] = %d, want %d (%v)", i, img.Pix[i], w, img.Pix)
		}
	}
	if img.Rect.Dx() != 2 || img.Rect.Dy() != 1 {
		t.Fatalf("rect %+v", img.Rect)
	}
}

func TestBgraToRGBA_SizeMismatch(t *testing.T) {
	if _, err := bgraToRGBA(make([]byte, 3), 2, 1, 8); err == nil {
		t.Fatal("short buffer must error")
	}
	if _, err := bgraToRGBA(make([]byte, 16), 2, 1, 8); err != nil {
		t.Fatalf("exact-size buffer: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `cd server && CGO_ENABLED=1 go test ./internal/camera/` → build fails (package/helpers don't exist).
- [ ] **Step 3: Implement** — `server/internal/camera/cache.go`:

```go
// Package camera grabs a single still frame from a camera device (built-in
// webcam or any connected external/continuity camera) through AVFoundation.
// One cached capture session lives behind one mutex and is torn down 30 s
// after the last frame, so the green camera light is on only while frames are
// being read — mirrors internal/sck's cached-filter discipline.
package camera

import "time"

const (
	idleTTL = 30 * time.Second // cached session lifetime after the last frame
	settle  = 500 * time.Millisecond // post-first-frame wait for auto-exposure
)

// expired reports whether the cached session is older than ttl. ONE predicate
// for the in-Capture check and the idle-teardown timer, so they cannot drift.
// At exactly ttl it is still warm.
func expired(lastUsed, now time.Time, ttl time.Duration) bool {
	return now.Sub(lastUsed) > ttl
}
```

and `server/internal/camera/convert.go`:

```go
package camera

import (
	"fmt"
	"image"
)

// bgraToRGBA converts a camera frame (little-endian BGRA rows padded to
// rowBytes) into image.RGBA. Pure so it unit-tests without cgo or a device.
func bgraToRGBA(bgra []byte, width, height, rowBytes int) (*image.RGBA, error) {
	if width <= 0 || height <= 0 || rowBytes < width*4 {
		return nil, fmt.Errorf("camera frame: bad geometry %dx%d rowBytes=%d", width, height, rowBytes)
	}
	if len(bgra) < (height-1)*rowBytes+width*4 {
		return nil, fmt.Errorf("camera frame: buffer %d bytes < %d needed", len(bgra), (height-1)*rowBytes+width*4)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		src := bgra[y*rowBytes:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
			b, g, r, a := src[x*4], src[x*4+1], src[x*4+2], src[x*4+3]
			d := dst[x*4:]
			d[0], d[1], d[2], d[3] = r, g, b, a
		}
	}
	return img, nil
}
```

- [ ] **Step 4: Run tests** — `cd server && CGO_ENABLED=1 go test ./internal/camera/ -v` → PASS.
- [ ] **Step 5: Commit**

```bash
git add server/internal/camera/
git commit -m "feat(camera): idle-expiry predicate and BGRA->RGBA conversion (pure)"
```

---

### Task 3: `internal/camera` — AVFoundation backend (cgo)

**Files:**
- Create: `server/internal/camera/camera.go`

**Interfaces:**
- Consumes: Task 2's `expired`, `bgraToRGBA`, `idleTTL`, `settle`.
- Produces (used by Tasks 4–7):

```go
type Device struct{ ID, Name string }
func List() ([]Device, error)                    // never starts a session
func Capture(id string) (*image.RGBA, error)     // id "" = system-default device
func AuthStatus() string                         // "authorized"|"denied"|"restricted"|"not-determined"
```

- [ ] **Step 1: Implement** — `server/internal/camera/camera.go`. Patterns copied from `internal/sck/sck.go` (same preamble style, `sr_set_err`/`sr_utf8` helpers, bounded waits; handlers arrive on our own dispatch queue, so a semaphore wait is enough and is always bounded). Full file:

```go
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

static void sr_signal(dispatch_semaphore_t s, int secs) {
  dispatch_semaphore_wait(s, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(secs * NSEC_PER_SEC)));
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
@interface SRCamSession : NSObject
@property(strong, nonatomic) AVCaptureSession *session;
@property(strong, nonatomic) SRCamHolder *holder;
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
  return n;
}

void sr_cam_free_list(SRCam *recs, int n) {
  if (recs == NULL) return;
  for (int i = 0; i < n; i++) { free(recs[i].id); free(recs[i].name); }
  free(recs);
}

// sr_cam_start resolves the device (empty id = system default), starts the
// session, and returns the handle. +[AVCaptureDevice defaultDeviceWithMediaType:]
// is unavailable on macOS, so the default comes from defaultDeviceWithDeviceType:.
void *sr_cam_start(const char *id, char **err) {
  AVCaptureDevice *dev = nil;
  NSString *want = id ? [NSString stringWithUTF8String:id] : @"";
  if (want.length == 0) {
    dev = [AVCaptureDevice defaultDeviceWithDeviceType:AVCaptureDeviceTypeBuiltInWideAngleCamera
                                             mediaType:AVMediaTypeVideo
                                              position:AVCaptureDevicePositionUnspecified];
    if (dev == nil) { sr_set_err(err, @"no cameras found"); return NULL; }
  } else {
    dev = [[AVCaptureDevice alloc] initWithUniqueID:want];
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
  return (__bridge_retained void *)h;
}

// sr_cam_frame waits (bounded) for the next frame and copies its BGRA bytes.
int sr_cam_frame(void *handle, int wait_secs, unsigned char **out,
                 int *w, int *h, int *bpr, char **err) {
  *out = NULL;
  if (handle == NULL) { sr_set_err(err, @"no camera session"); return -1; }
  SRCamSession *s = (__bridge SRCamSession *)handle;
  if ([s.holder waitFrame:wait_secs] != 0) {
    sr_set_err(err, @"timed out waiting for a camera frame (is the camera in use by another app?)");
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

// List enumerates cameras without starting a session (used only to name
// alternatives in error messages; the app's picker enumerates in Swift).
func List() ([]Device, error) {
	var cErr *C.char
	var recs *C.SRCam
	n := C.sr_cam_list(&recs, &cErr)
	if n < 0 {
		return nil, fmt.Errorf("camera list: %s", C.GoString(cErr))
	}
	out := make([]Device, 0, int(n))
	for i := 0; i < int(n); i++ {
		out = append(out, Device{ID: C.GoString(recs[i].id), Name: C.GoString(recs[i].name)})
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
		if active != nil {
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
	if st := AuthStatus(); st == "denied" || st == "restricted" {
		return nil, fmt.Errorf("camera access denied (status: %s) — grant: System Settings → Privacy & Security → Camera (restart Screen Reader MCP after granting)", st)
	}
	warm := active != nil && activeID == id && !expired(lastUsed, time.Now(), idleTTL)
	if !warm {
		if active != nil {
			C.sr_cam_stop(active)
			active = nil
		}
		cid := C.CString(id)
		defer C.free(unsafe.Pointer(cid))
		var startErr *C.char
		h := C.sr_cam_start(cid, &startErr)
		if h == nil {
			return nil, fmt.Errorf("camera: %s", C.GoString(startErr))
		}
		active, activeID = h, id
		// Fresh start: wait for the first frame (10 s cap) and let
		// auto-exposure settle; the frame grabbed below is newer, so it
		// carries the adjusted exposure.
		var dw, dh, dbpr C.int
		var dbuf *C.uchar
		var firstErr *C.char
		if C.sr_cam_frame(h, 10, &dbuf, &dw, &dh, &dbpr, &firstErr) != 0 {
			err := C.GoString(firstErr)
			C.sr_cam_stop(h)
			active = nil
			return nil, fmt.Errorf("camera: %s", err)
		}
		if dbuf != nil {
			C.free(unsafe.Pointer(dbuf))
		}
		time.Sleep(settle)
	}
	armIdle()
	var buf *C.uchar
	var cw, ch, cbpr C.int
	var ferr *C.char
	// 2 s per-frame wait, warm or cold (cold: this is the post-settle frame).
	if C.sr_cam_frame(active, 2, &buf, &cw, &ch, &cbpr, &ferr) != 0 {
		return nil, fmt.Errorf("camera: %s", C.GoString(ferr))
	}
	defer C.free(unsafe.Pointer(buf))
	lastUsed = time.Now()
	return bgraToRGBA(C.GoBytes(unsafe.Pointer(buf), C.int(int(cbpr)*int(ch))), int(cw), int(ch), int(cbpr))
}
```
- [ ] **Step 2: Build + vet** — `cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go build -o /tmp/sr-cam-check .` must pass (this is the task's compile gate; AVFoundation only links cleanly).
- [ ] **Step 3: Run existing tests** — `CGO_ENABLED=1 go test ./internal/camera/ ./internal/config/` → PASS (no unit test opens a camera; `List`/`AuthStatus` are session-free).
- [ ] **Step 4: Live smoke (developer machine, needs Camera grant for the terminal or app)** — create `/tmp/sr-cam-smoke/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"screenreader/internal/camera"
)

func main() {
	fmt.Println("auth:", camera.AuthStatus())
	devs, err := camera.List()
	fmt.Println("devices:", devs, err)
	img, err := camera.Capture("")
	if err != nil {
		fmt.Println("capture err:", err)
		os.Exit(1)
	}
	fmt.Println("frame:", img.Bounds())
}
```

Run `cd server && CGO_ENABLED=1 go run /tmp/sr-cam-smoke/main.go` twice: the second run must be visibly faster (warm cache) — then wait 30 s and confirm the green light goes off. (If TCC denies, the printed error must name the grant path; that is still a pass for the error contract.) Delete the smoke file afterwards.
- [ ] **Step 5: Commit**

```bash
git add server/internal/camera/camera.go
git commit -m "feat(camera): AVFoundation backend — cached session, 30s idle teardown, bounded waits"
```

---

### Task 4: `internal/capture` — `CaptureCamera` wrapper

**Files:**
- Modify: `server/internal/capture/capture.go`
- Test: `server/internal/capture/capture_test.go` (create if absent)

**Interfaces:**
- Consumes: `camera.Capture` (Task 3), `geom.ScaleDown`.
- Produces: `func CaptureCamera(id string, maxWidth int) (*Result, error)` — `Result.Bounds` is the **zero Rect** (camera frames have no screen region). Test seam: package var `cameraFrame = camera.Capture`.

- [ ] **Step 1: Write the failing test** — `server/internal/capture/capture_test.go`:

```go
package capture

import (
	"bytes"
	"image"
	"testing"
)

func TestCaptureCamera(t *testing.T) {
	old := cameraFrame
	defer func() { cameraFrame = old }()
	cameraFrame = func(id string) (*image.RGBA, error) {
		if id != "dev1" {
			t.Fatalf("id passthrough: %q", id)
		}
		return image.NewRGBA(image.Rect(0, 0, 400, 300)), nil
	}
	res, err := CaptureCamera("dev1", 200)
	if err != nil {
		t.Fatal(err)
	}
	if res.ImgW != 200 || res.ImgH != 150 {
		t.Fatalf("downscale: %dx%d, want 200x150", res.ImgW, res.ImgH)
	}
	if res.Bounds != (Result{}.Bounds) {
		t.Fatalf("camera result must have no screen bounds: %+v", res.Bounds)
	}
	if bytes.Count(res.PNG, []byte("IEND")) != 1 {
		t.Fatal("PNG must be exactly one complete image")
	}
	img, err := pngDecode(res.PNG)
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	if img.Bounds().Dx() != 200 || img.Bounds().Dy() != 150 {
		t.Fatalf("decoded %+v", img.Bounds())
	}
}

func pngDecode(b []byte) (image.Image, error) {
	return png.Decode(bytes.NewReader(b))
}
```

(Imports for the test file: `bytes`, `image`, `image/png`, `testing`.)
- [ ] **Step 2: Run to verify failure** — `cd server && CGO_ENABLED=1 go test ./internal/capture/` → undefined `CaptureCamera`/`cameraFrame`.
- [ ] **Step 3: Implement** — append to `capture.go` (and add `"screenreader/internal/camera"` to imports):

```go
// cameraFrame is the seam unit tests swap out; production grabs one frame
// through internal/camera (cached session, bounded waits).
var cameraFrame = camera.Capture

// CaptureCamera grabs one settled camera frame (id "" = system default) and
// encodes it like the other capture paths. Bounds is the zero Rect: a camera
// image has no screen region to map coordinates onto. Errors pass through
// untouched — internal/camera's are already actionable.
func CaptureCamera(id string, maxWidth int) (*Result, error) {
	img, err := cameraFrame(id)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := geom.ScaleDown(b.Dx(), b.Dy(), maxWidth)
	if w != b.Dx() || h != b.Dy() {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return &Result{PNG: buf.Bytes(), ImgW: w, ImgH: h}, nil
}
```

- [ ] **Step 4: Run tests** — `cd server && CGO_ENABLED=1 go test ./internal/capture/` → PASS.
- [ ] **Step 5: Commit**

```bash
git add server/internal/capture/
git commit -m "feat(capture): CaptureCamera — camera frame through the shared scale+PNG path"
```

---

### Task 5: `tools` — `kindCamera` target + capture wiring

**Files:**
- Modify: `server/internal/tools/tools.go` (`targetKind`, `targetSpec`, `classifyTarget`, `rect()`, capture handler, tool description)
- Test: `server/internal/tools/target_test.go`, `server/main_test.go`

**Interfaces:**
- Consumes: `config.CameraCfg` (Task 1), `capture.CaptureCamera` (Task 4).
- Produces: `kindCamera targetKind = "camera"`, `targetSpec.CameraID string`, `cameraLabel(cfg *config.Config) string` (Tasks 6–7 reuse both).

- [ ] **Step 1: Write the failing tests** — append to `target_test.go`:

```go
func TestClassifyTargetCamera(t *testing.T) {
	cfg := config.Defaults()
	cfg.Mode, cfg.Camera.ID, cfg.Camera.Name = "camera", "AA11", "Cam X"
	cfg.App, cfg.Title = "Stale App", "Old" // stale app must NOT retarget camera mode

	t.Run("camera mode wins over stored app", func(t *testing.T) {
		got, err := classifyTarget(cfg, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kindCamera || got.CameraID != "AA11" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("per-call app override still wins", func(t *testing.T) {
		got, err := classifyTarget(cfg, "Windows App", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kindApp || got.App != "Windows App" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("explicit rect still wins", func(t *testing.T) {
		got, err := classifyTarget(cfg, "", "", &geom.Rect{X: 1, Y: 2, W: 3, H: 4})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kindRect {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("camera target has no screen bounds", func(t *testing.T) {
		spec, _ := classifyTarget(cfg, "", "", nil)
		if _, err := spec.rect(); err == nil || !strings.Contains(err.Error(), "no screen bounds") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCameraLabel(t *testing.T) {
	cfg := config.Defaults()
	if got := cameraLabel(cfg); got != "system default" {
		t.Fatalf("empty name: %q", got)
	}
	cfg.Camera.Name = "Cam X"
	if got := cameraLabel(cfg); got != "Cam X" {
		t.Fatalf("named: %q", got)
	}
}
```

Append to `server/main_test.go` (follows `TestCapture_windowOnNonAppTarget`'s in-process pattern; the camera is never started):

```go
// TestCapture_cameraTargetWindowFalse: camera targets reject window:true
// through the generic non-app-target path, and never reach the camera.
func TestCapture_cameraTargetWindowTrue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Mode, cfg.Camera.ID = "camera", "does-not-exist-id"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "capture", Arguments: map[string]any{"window": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("camera target accepted window:true: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "needs an app target") || !strings.Contains(text, "camera") {
		t.Fatalf("text %q must name the camera kind", text)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `cd server && CGO_ENABLED=1 go test ./internal/tools/ -run 'Camera'` → undefined `kindCamera`/`cameraLabel`; root `CGO_ENABLED=1 go test . -run TestCapture_cameraTargetWindowTrue` fails too.
- [ ] **Step 3: Implement in `tools.go`:**

a. Constants: add to the `targetKind` const block:

```go
	kindCamera targetKind = "camera"
```

and extend the `targetKind` doc comment: "…`camera` is not a screen region either — it is the one target with no bounds at all."

b. `targetSpec`: add field `CameraID string // kindCamera`.

c. `classifyTarget`: insert immediately after the per-call app/title block, before the `cfg.Mode == "rect"` check:

```go
	// Camera mode is the app's choice and outranks a stale stored app: a user
	// on the camera segment is never silently retargeted. Per-call overrides
	// above still win (same semantics as rect/screen).
	if cfg.Mode == "camera" {
		return targetSpec{Kind: kindCamera, CameraID: cfg.Camera.ID}, nil
	}
```

d. `rect()`: add to the switch:

```go
	case kindCamera:
		return geom.Rect{}, fmt.Errorf("camera target has no screen bounds")
```

e. capture handler: insert the camera branch AFTER the `wantWindow` block and BEFORE `r, err := spec.rect()`:

```go
			if spec.Kind == kindCamera {
				res, err := capture.CaptureCamera(spec.CameraID, mw)
				if err != nil {
					return errResult(err), nil, nil
				}
				// No bounds token: a camera image maps to no screen region.
				return &mcp.CallToolResult{Content: []mcp.Content{
					&mcp.ImageContent{Data: res.PNG, MIMEType: "image/png"},
					&mcp.TextContent{Text: fmt.Sprintf("image=%dx%d source=camera device=%q", res.ImgW, res.ImgH, cameraLabel(cfg))},
				}}, nil, nil
			}
```

f. Add beside `captureMode`:

```go
// cameraLabel names the configured camera for reports ("system default" when
// no device was picked).
func cameraLabel(cfg *config.Config) string {
	if cfg.Camera.Name != "" {
		return cfg.Camera.Name
	}
	return "system default"
}
```

g. capture tool description: append to the existing `Description` string: `" With the target set to camera (Screen Reader settings) capture returns one settled frame from that camera; image coordinates then map to no screen region."`

- [ ] **Step 4: Run tests** — `cd server && CGO_ENABLED=1 go test ./internal/tools/ . ` → PASS (root `.` runs main_test incl. the new camera case).
- [ ] **Step 5: Commit**

```bash
git add server/internal/tools/tools.go server/internal/tools/target_test.go server/main_test.go
git commit -m "feat(tools): camera target kind — capture returns camera frames (source=camera)"
```

---

### Task 6: `tools` — pointer-input guard in camera mode

**Files:**
- Modify: `server/internal/tools/tools.go` (`click`, `move`, `scroll`, `drag` handlers + new helper)
- Test: `server/main_test.go`

**Interfaces:**
- Consumes: `loadCfg`, `errResult` (existing).
- Produces: `func cameraGuard(tool string) error` (nil unless the target is a camera).

- [ ] **Step 1: Write the failing test** — append to `main_test.go`:

```go
// TestClick_cameraModeBlocked: the target has no screen coordinates while it
// is a camera, so pointer tools fail loudly; keyboard tools keep working
// (they drive the focused app, not the target).
func TestClick_cameraModeBlocked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Mode = "camera"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"click", "move", "scroll", "drag"} {
		args := map[string]any{"x": 5, "y": 5, "dry_run": true}
		if name == "scroll" {
			args = map[string]any{"dx": 1, "dy": 1}
		}
		if name == "drag" {
			args = map[string]any{"x1": 1, "y1": 1, "x2": 2, "y2": 2, "dry_run": true}
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%s ran in camera mode: %v", name, res.Content)
		}
		if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "target is a camera") {
			t.Fatalf("%s: %q", name, text)
		}
	}
	// type is NOT blocked (it needs Accessibility to run; assert only that it
	// is not refused by the camera guard) — dry path not available, so skip
	// actual execution and rely on the registration tests instead.
}
```

- [ ] **Step 2: Run to verify failure** — `cd server && CGO_ENABLED=1 go test . -run TestClick_cameraModeBlocked` → handlers succeed (no guard yet; with `dry_run` no events are posted, but the guard assertion still fails). NOTE: `move` moves the REAL cursor when unguarded — acceptable once (it is the failing-run step on a dev machine); after the guard lands it never executes.
- [ ] **Step 3: Implement** — add near `cameraLabel`:

```go
// cameraGuard blocks pointer tools while the target is a camera: there are no
// screen coordinates to aim at, and an agent dragging the cursor while the
// user believes the session is camera-only is exactly the "silently wrong"
// this repo refuses. Keyboard tools stay allowed (focused app, not target).
func cameraGuard(tool string) error {
	if loadCfg().Mode == "camera" {
		return fmt.Errorf("%s is disabled: the target is a camera — set a window/rect/screen target in Screen Reader settings", tool)
	}
	return nil
}
```

At the FIRST line inside each of the four handlers (`click`, `move`, `scroll`, `drag`):

```go
			if e := cameraGuard("click"); e != nil { // (respectively "move"/"scroll"/"drag")
				return errResult(e), nil, nil
			}
```

- [ ] **Step 4: Run tests** — `cd server && CGO_ENABLED=1 go test . ./internal/tools/` → PASS.
- [ ] **Step 5: Commit**

```bash
git add server/internal/tools/tools.go server/main_test.go
git commit -m "feat(tools): pointer input blocked while the target is a camera"
```

---

### Task 7: `tools` — `window_info` + `doctor` camera reporting

**Files:**
- Modify: `server/internal/tools/tools.go` (`window_info`, `doctor`, new helpers)
- Test: `server/main_test.go`

**Interfaces:**
- Consumes: `cameraLabel` (Task 5), `camera.AuthStatus`, `camera.List` (Task 3).
- Produces: `cameraLine(cfg) string`; `window_info` camera text `mode=camera camera="<name>" capture=camera`; doctor `camera:` + `target: camera "…"` lines.

- [ ] **Step 1: Write the failing test** — append to `main_test.go`:

```go
// TestWindowInfo_cameraMode: camera mode has no bounds, so window_info must
// report the device instead of erroring out of resolveRect.
func TestWindowInfo_cameraMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cs := connectClient(t)
	cfg := config.Defaults()
	cfg.Mode, cfg.Camera.Name = "camera", "Cam X"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "window_info", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("window_info errored in camera mode: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, `mode=camera`) || !strings.Contains(text, `camera="Cam X"`) {
		t.Fatalf("text %q must report the camera", text)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `cd server && CGO_ENABLED=1 go test . -run TestWindowInfo_cameraMode` → fails: window_info still calls `resolveRect` and errors.
- [ ] **Step 3: Implement in `tools.go`:**

a. Add `"screenreader/internal/camera"` to imports.

b. `window_info` handler: insert before `r, err := resolveRect(cfg, "", "", nil)`:

```go
			if cfg.Mode == "camera" {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("mode=camera camera=%q capture=camera", cameraLabel(cfg)),
				}}}, nil, nil
			}
```

c. Doctor: after `out += windowCaptureLine(cfg)` add:

```go
			if cfg.Mode == "camera" {
				out += cameraLine(cfg)
			}
```

and replace the final target block (`if r, err := resolveRect(...)` …) with:

```go
			if cfg.Mode == "camera" {
				out += fmt.Sprintf("target: camera %q\n", cameraLabel(cfg))
			} else if r, err := resolveRect(cfg, "", "", nil); err != nil {
				out += "target: ERROR " + err.Error() + " (try list_windows / set_target)\n"
			} else {
				out += fmt.Sprintf("target: ok %+v\n", r)
			}
```

d. Add beside `windowCaptureLine` (never calls `Capture` — a check-up must not light the LED):

```go
// cameraLine is doctor's camera probe: TCC status + configured-device
// presence, WITHOUT starting a session (no camera light during a check-up).
func cameraLine(cfg *config.Config) string {
	if st := camera.AuthStatus(); st != "authorized" {
		return "  camera: MISSING — access not granted (status: " + st +
			")\n    grant: System Settings → Privacy & Security → Camera (restart Screen Reader MCP after granting)\n"
	}
	if cfg.Camera.ID != "" {
		devs, err := camera.List()
		if err != nil {
			return "  camera: ERROR " + err.Error() + "\n"
		}
		for _, d := range devs {
			if d.ID == cfg.Camera.ID {
				return fmt.Sprintf("  camera: ok (%q, auth=authorized)\n", cameraLabel(cfg))
			}
		}
		var names []string
		for _, d := range devs {
			names = append(names, d.Name)
		}
		return fmt.Sprintf("  camera: MISSING — configured camera %q is not connected (available: %s)\n",
			cfg.Camera.Name, strings.Join(names, ", "))
	}
	return fmt.Sprintf("  camera: ok (%q, auth=authorized, system default)\n", cameraLabel(cfg))
}
```

(`strings` is already imported in tools.go? No — check; add `"strings"` to imports if absent.)
- [ ] **Step 4: Run tests** — `cd server && CGO_ENABLED=1 go test . ./internal/tools/` → PASS. Full gate now: `CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...` → PASS.
- [ ] **Step 5: Commit**

```bash
git add server/internal/tools/tools.go server/main_test.go
git commit -m "feat(tools): window_info/doctor report the camera target (no session, no LED)"
```

---

### Task 8: App — camera TCC permission + Info.plist keys

**Files:**
- Modify: `app/Sources/ScreenReaderKit/Permissions.swift`
- Modify: `app/Info.plist`

**Interfaces:**
- Consumes: nothing new.
- Produces: `TCCPermission.camera` (rawValue `"Camera"`), used by Tasks 10 and 11.

- [ ] **Step 1: Implement Permissions.swift** — add `import AVFoundation` to the imports; in the enum:

```swift
    case camera = "Camera"
```

in `isGranted`:

```swift
        case .camera:          return AVCaptureDevice.authorizationStatus(for: .video) == .authorized
```

in `request()`:

```swift
        case .camera:
            AVCaptureDevice.requestAccess(for: .video) { _ in } // async prompt; the 2 s refresh re-reads status
```

- [ ] **Step 2: Implement Info.plist** — inside the top-level `<dict>` of `app/Info.plist`, after `NSAppleEventsUsageDescription`:

```xml
  <key>NSCameraUsageDescription</key>
  <string>Screen Reader MCP captures a still frame from the camera you choose as the reading target.</string>
  <key>NSCameraUseContinuityCameraDeviceType</key>
  <true/>
```

(`NSCameraUseContinuityCameraDeviceType` makes iPhone Continuity cameras enumerate as `.continuityCamera` on macOS 14+; without it they appear mis-typed as built-in. Both keys must exist BEFORE any camera call — a missing usage string is a hard refusal, not a prompt.)
- [ ] **Step 3: Build + test** — `cd app && swift build && swift test` → PASS (enum is CaseIterable; consumers in AppDelegate/SettingsWindow still compile — they iterate `allCases` generically; camera rows appear after Task 10, verify nothing crashes in between because menu/settings treat every case uniformly).
- [ ] **Step 4: Commit**

```bash
git add app/Sources/ScreenReaderKit/Permissions.swift app/Info.plist
git commit -m "feat(app): Camera TCC permission + usage description (usage string is mandatory)"
```

---

### Task 9: App — ConfigStore camera round-trip

**Files:**
- Modify: `app/Sources/ScreenReaderKit/ConfigStore.swift`
- Test: `app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift`

**Interfaces:**
- Consumes: existing `ConfigStore` conventions (missing file → `[:]`, malformed throws).
- Produces: `ConfigStore.Target` gains `cameraID`/`cameraName` (both default `""` in the initializer, so existing call sites keep compiling); `applyTarget` writes/removes the `camera` key.

- [ ] **Step 1: Write the failing tests** — append to `ConfigStoreTests.swift`:

```swift
    func testCameraRoundTrip() {
        let t = ConfigStore.Target(app: "", title: "", mode: "camera", x: 0, y: 0, w: 0, h: 0,
                                   cameraID: "AA11", cameraName: "Cam X")
        let back = ConfigStore.target(from: ConfigStore.applyTarget(t, to: [:]))
        XCTAssertEqual(back.mode, "camera")
        XCTAssertEqual(back.cameraID, "AA11")
        XCTAssertEqual(back.cameraName, "Cam X")
    }

    func testApplyTarget_removesEmptyCameraAndKeepsForeignKeys() {
        let json: [String: Any] = ["camera": ["id": "AA11", "name": "Cam X"], "hotkey": "f9"]
        let out = ConfigStore.applyTarget(
            ConfigStore.Target(app: "X", title: "", mode: "app", x: 0, y: 0, w: 0, h: 0), to: json)
        XCTAssertNil(out["camera"], "leaving camera mode must drop the stale device key")
        XCTAssertEqual(out["hotkey"] as? String, "f9")
    }
```

- [ ] **Step 2: Run to verify failure** — `cd app && swift test --filter ConfigStoreTests` → compile failure (no `cameraID`).
- [ ] **Step 3: Implement** — in `ConfigStore.swift`: add to `Target`:

```swift
        public var cameraID: String   // AVCaptureDevice.uniqueID; "" = system default
        public var cameraName: String // display-only
```

extend the initializer signature with (after `screen`):

```swift
                    cameraID: String = "", cameraName: String = "") {
```

and set `self.cameraID = cameraID; self.cameraName = cameraName`. In `applyTarget`, after the `rect` line:

```swift
        if !t.cameraID.isEmpty || !t.cameraName.isEmpty {
            out["camera"] = ["id": t.cameraID, "name": t.cameraName]
        } else {
            out.removeValue(forKey: "camera") // camera keys belong to camera mode only
        }
```

In `target(from:)` extend the returned struct with:

```swift
                      cameraID: (json["camera"] as? [String: Any])?["id"] as? String ?? "",
                      cameraName: (json["camera"] as? [String: Any])?["name"] as? String ?? "")
```

and update the file's doc comment to: `/// Mutates only app/title/mode/screen/rect/camera/http/access/hotkey; preserves every other key.`
- [ ] **Step 4: Run tests** — `cd app && swift test` → PASS.
- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/ConfigStore.swift app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift
git commit -m "feat(app): ConfigStore carries the camera device key (removed on leaving camera mode)"
```

---

### Task 10: App — Camera segment, device picker, menu lines

**Files:**
- Modify: `app/Sources/ScreenReader/SettingsWindow.swift`
- Modify: `app/Sources/ScreenReader/AppDelegate.swift`

**Interfaces:**
- Consumes: `TCCPermission.camera` (Task 8), `Target.cameraID/cameraName` (Task 9).
- Produces: settings UI with `App window | Rectangle | Screen | Camera`; menu camera row hidden unless camera mode.

- [ ] **Step 1: SettingsWindow — state + rows.** Add properties beside the existing ones:

```swift
    private var cameraRow: NSStackView!
    private var cameraPicker: NSPopUpButton!
    private var lastCamerasFingerprint = ""
    private var permRows: [TCCPermission: NSStackView] = [:]
```

Add a file-level (private) type for picker items:

```swift
private struct CamEntry { let id: String; let name: String }
```

In `build()`: change the segmented labels to `["App window", "Rectangle", "Screen", "Camera"]`; in the permissions loop `let row = stack(.horizontal)` … store `permRows[p] = row`; after building `screenRow` add:

```swift
        cameraRow = stack(.horizontal)
        cameraPicker = NSPopUpButton(frame: .zero, pullsDown: false)
        cameraPicker.target = self
        cameraPicker.action = #selector(cameraPickerChanged)
        cameraPicker.widthAnchor.constraint(equalToConstant: 320).isActive = true
        cameraRow.addArrangedSubview(cameraPicker)
        cameraRow.addArrangedSubview(NSButton(title: "Refresh", target: self,
                                              action: #selector(refreshCameraPicker)))
```

and `target.addArrangedSubview(cameraRow)` after the `screenRow` line.
- [ ] **Step 2: SettingsWindow — mode plumbing.** `updateModeRows()` gains `cameraRow.isHidden = segmented.selectedSegment != 3`. In `refresh()` replace the mode-key line with:

```swift
        let key = t.mode == "rect" ? "rect" : (t.mode == "screen" ? "screen" : (t.mode == "camera" ? "camera" : "app"))
```

and the segment mapping with:

```swift
            segmented.selectedSegment = key == "rect" ? 1 : (key == "screen" ? 2 : (key == "camera" ? 3 : 0))
```

In `modeChanged()` add `case 3: key = "camera"` and extend the adoption fix with the camera leg (same T17 pattern as the app case):

```swift
        if key == "camera",
           let c = cameraPicker.selectedItem?.representedObject as? CamEntry {
            t.cameraID = c.id
            t.cameraName = c.name
        }
```

(If `selectedItem` has no `CamEntry` — "(System default camera)" or a placeholder — leave `t.cameraID`/`t.cameraName` as loaded.) Add after `syncScreenPicker()` in `refresh()`: `syncCameraPicker()`; and camera-row visibility just before it:

```swift
        let camMode = t.mode == "camera"
        for p in TCCPermission.allCases {
            permRows[p]?.isHidden = (p == .camera && !camMode)
        }
```

and change the grant-button line to count only visible rows:

```swift
        grantButton.isHidden = !TCCPermission.allCases.contains { !$0.isGranted && ($0 != .camera || camMode) }
```

Note: `camMode` needs `t`, which `refresh()` already loads; move the line below `let t = ConfigStore.target(from: json)` and read `permStatus` updates through `p.isGranted` as before.
- [ ] **Step 3: SettingsWindow — picker methods.** After `refreshScreenPicker`/`screenPickerChanged` add:

```swift
    /// Device types: .external/.continuityCamera exist on macOS 14+ only;
    /// ExternalUnknown is the 13-floor spelling (deprecated there in 14, hence
    /// the availability split — mirrors internal/camera's sr_cam_types()).
    private func cameraTypes() -> [AVCaptureDevice.DeviceType] {
        if #available(macOS 14.0, *) {
            return [.builtInWideAngleCamera, .external, .continuityCamera]
        }
        return [.builtInWideAngleCamera, .externalUnknown]
    }

    /// Enumeration in-app via AVFoundation (like syncScreenPicker uses
    /// NSScreen.screens) so the picker works while the server is stopped.
    private func syncCameraPicker() {
        let devs = AVCaptureDevice.DiscoverySession(deviceTypes: cameraTypes(),
                                                    mediaType: .video, position: .unspecified).devices
        let fp = devs.map(\.uniqueID).joined(separator: ",")
        if fp != lastCamerasFingerprint {
            lastCamerasFingerprint = fp
            cameraPicker.removeAllItems()
            cameraPicker.addItem(withTitle: "(System default camera)")
            for d in devs {
                cameraPicker.addItem(withTitle: d.localizedName)
                cameraPicker.lastItem?.representedObject = CamEntry(id: d.uniqueID, name: d.localizedName)
            }
        }
        let t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        if t.cameraID.isEmpty {
            if cameraPicker.indexOfSelectedItem != 0 { cameraPicker.selectItem(at: 0) }
            return
        }
        for item in cameraPicker.itemArray {
            if (item.representedObject as? CamEntry)?.id == t.cameraID {
                if cameraPicker.indexOfSelectedItem != cameraPicker.indexOf(item) { cameraPicker.select(item) }
                return
            }
        }
        // Stored device is gone: say so, keep the config untouched (loud,
        // never silently retargeted) — mirrors the Go error contract. The
        // title guard stops the 2 s tick from appending the placeholder again.
        let title = "(not connected: \(t.cameraName.isEmpty ? t.cameraID : t.cameraName))"
        if cameraPicker.lastItem?.title != title {
            cameraPicker.addItem(withTitle: title)
        }
        cameraPicker.selectItem(at: cameraPicker.numberOfItems - 1)
    }

    @objc private func refreshCameraPicker() {
        lastCamerasFingerprint = ""
        syncCameraPicker()
    }

    @objc private func cameraPickerChanged() {
        guard let item = cameraPicker.selectedItem else { return }
        var t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        t.mode = "camera"
        if let c = item.representedObject as? CamEntry {
            t.cameraID = c.id; t.cameraName = c.name
        } else {
            t.cameraID = ""; t.cameraName = "" // "(System default camera)" or placeholder
        }
        saveTarget(t)
    }
```

and `grantAll()` becomes:

```swift
    @objc private func grantAll() {
        let camMode = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:]).mode == "camera"
        for p in TCCPermission.allCases where !p.isGranted && (p != .camera || camMode) { p.request() }
    }
```

Also add `import AVFoundation` at the top of the file (next to `import AppKit`).
- [ ] **Step 4: AppDelegate — menu lines.** In `updateMenu()`: add the camera branch to the target-line chain, after the `screen` branch and before the `rect` branch:

```swift
        } else if t.mode == "camera" {
            targetLine.title = "Target: camera " + (t.cameraName.isEmpty ? "(system default)" : t.cameraName)
```

and inside the permission-lines loop:

```swift
        for (p, i) in zip(TCCPermission.allCases, permLines) {
            i.isHidden = (p == .camera && t.mode != "camera") // fixed structure, hidden rows (autoStopItem precedent)
            i.title = "\(p.rawValue): " + (permCache[p].map { $0 ? "✓" : "✗" } ?? "…")
        }
```
- [ ] **Step 5: Build + test + assemble** — `cd app && swift build && swift test` → PASS; `./build.sh` from the repo root → `app/build/Screen Reader MCP.app` assembles and signs with the ScreenReader Dev identity.
- [ ] **Step 6: Manual UI smoke (rebuild + relaunch the app)** — 4th segment shows **Camera** and persists the mode to config.json (`"mode": "camera"` + `"camera": {...}`) on selection; camera permissions row appears ONLY in camera mode; menu target line reads `Target: camera …`; unplugging the picked camera after selecting it shows `(not connected: …)` on the next 2 s tick.
- [ ] **Step 7: Commit**

```bash
git add app/Sources/ScreenReader/SettingsWindow.swift app/Sources/ScreenReader/AppDelegate.swift
git commit -m "feat(app): Camera target segment with device picker + mode-scoped permission row"
```

---

### Task 11: Docs — conventions and reference

**Files:**
- Modify: `AGENTS.md`
- Modify: `docs/reference.md` (sections: "Permissions", "Configure the target", "Tools")

**Interfaces:**
- Consumes: everything above (docs describe shipped behavior only).
- Produces: nothing code-visible.

- [ ] **Step 1: AGENTS.md** — append this bullet after the `internal/sck` gotchas bullet:

```markdown
- Camera target (mode `camera`, app-owned `camera: {id,name}` config key — the Go field exists only so `set_target`'s Save preserves it): `internal/camera` wraps AVFoundation (cached session behind one mutex, 30 s idle teardown via `time.AfterFunc`, bounded waits — same discipline as sck). `capture` returns `image=WxH source=camera device="…"` with **no** bounds; pointer tools (click/move/scroll/drag) error while mode=camera, keyboard tools stay allowed. `AVCaptureDevice.defaultDeviceWithMediaType:` is unavailable on macOS — the system default comes from `defaultDeviceWithDeviceType:…Unspecified`; `.external`/`.continuityCamera` device types need macOS 14 (`ExternalUnknown` on 13). Camera TCC (`NSCameraUsageDescription` in the app's Info.plist — hard requirement, no key = no prompt) rides the app's cert like the other grants; the F9 stop hotkey kills the server, which tears the session down and turns the light off.
```

- [ ] **Step 2: docs/reference.md** — in "Permissions" add a Camera subsection:

```markdown
Camera (only when the target is a camera): grant via the settings window's
Permissions row (visible in camera mode) or System Settings → Privacy &
Security → Camera. The green camera indicator is on while a session runs —
the cached session tears down 30 s after the last frame, and stopping the
server releases the camera immediately.
```

In "Configure the target" add:

```markdown
### Camera target

Pick **Camera** in the settings Target segment and choose a device
("(System default camera)" = whatever macOS marks default). The agent reads
it through the same `capture` tool — one settled still frame per call
(`source=camera` in the text line, no `bounds`). While camera mode is set,
pointer tools (click/move/scroll/drag) are rejected; typing still reaches
the focused app. Device selection is the user's: `set_target` cannot switch
to or away from camera.
```

In "Tools", amend the `capture` row with `source=camera` and the camera-mode pointer-guard sentence.
- [ ] **Step 3: Full gate** — `cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...` → PASS; `cd app && swift test` → PASS.
- [ ] **Step 4: Commit**

```bash
git add AGENTS.md docs/reference.md
git commit -m "docs: camera target conventions and reference sections"
```

---

### Task 12: Live verification (spec §7 manual checklist)

No code unless a check fails. Run the app built by Task 10 (`./build.sh`, relaunch). Grant Camera to **Screen Reader MCP** when first prompted (or via the settings row), quit and relaunch after granting.

- [ ] **1.** Settings → Target → Camera, pick the built-in camera → `capture` (from any connected MCP client or the menu's "Test capture…") returns a real frame; text line contains `source=camera device="…"` and NO `bounds=`.
- [ ] **2.** Second `capture` within 30 s is visibly faster than the first (warm session); green light stayed on between the two.
- [ ] **3.** After ~30 s of no captures the green light goes off without any further calls.
- [ ] **4.** Open the camera in Photo Booth while camera mode is set → `capture` errors and names "in use by another app"; the server stays alive; quit Photo Booth → capture works again.
- [ ] **5.** Unplug an external camera selected as target → `capture` error names it and lists available devices; settings picker shows `(not connected: …)`; picking another device restores capture.
- [ ] **6.** `doctor` prints the `camera:` line and `target: camera "…"` **without** the light turning on.
- [ ] **7.** Access mode Read-only + camera mode → `capture` still works; `click` is not registered at all.
- [ ] **8.** F9 (stop hotkey) while a camera session is warm → light goes off within a second (server process died).
- [ ] **9.** Switch back to App window target → `capture` returns the window grab; `camera` key gone from config.json; click/move/scroll/drag work again.

If any check fails, stop and fix before proceeding (report with the failing output; do not paper over). When all pass:

```bash
git status --short   # must be clean; everything committed per task
```

## Self-Review Notes (author, 2026-11-02)

- Spec §4 (config) → Task 1; §5.1 (internal/camera incl. timer teardown & error contract) → Tasks 2–3; §5.2 (CaptureCamera) → Task 4; §5.3 (tools surface incl. guard, window_info/doctor, unchanged tools) → Tasks 5–7; §6.1–6.4 (app) → Tasks 8–10; §7 (testing incl. live list) → per-task tests + Task 12; §8 (verification checklist) → Task 3 smoke, Tasks 8/10 gates, Task 12 items 1–8; docs → Task 11.
- Header note about `defaultDeviceWithMediaType:` being macOS-unavailable and the `.external` 14+/13 split verified against the on-disk SDK header (`AVCaptureDevice.h`), not from memory.
- Signatures used across tasks match their Producing task's Interfaces block.
