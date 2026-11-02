// Package capture screenshots a screen region (robotgo) or one window on its
// own (ScreenCaptureKit) and encodes PNG.
package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"github.com/go-vgo/robotgo"
	"golang.org/x/image/draw"

	"screenreader/internal/camera"
	"screenreader/internal/geom"
	"screenreader/internal/sck"
	"screenreader/internal/window"
)

type Result struct {
	PNG    []byte
	Bounds geom.Rect // screen-absolute captured region
	ImgW   int       // PNG pixel width
	ImgH   int       // PNG pixel height
}

// CaptureRegion captures the screen region r, optionally downscaling so the width
// is at most maxWidth (aspect preserved). Returns the PNG bytes and geometry.
// Anything drawn on top of the region comes back in the image: this is a grab of
// the display, not of a window.
func CaptureRegion(r geom.Rect, maxWidth int) (*Result, error) {
	img, err := robotgo.Capture(r.X, r.Y, r.W, r.H) // *image.RGBA
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	w, h := geom.ScaleDown(srcW, srcH, maxWidth)
	if w != srcW || h != srcH {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return &Result{PNG: buf.Bytes(), Bounds: r, ImgW: w, ImgH: h}, nil
}

// CaptureWindow renders the single window c (from an app/title target resolved
// against ScreenCaptureKit) with no other window in frame: the window server
// draws just that window, so overlapping windows are excluded and it does not
// have to be frontmost. maxWidth behaves as in CaptureRegion — SCK scales, so
// there is no second downscale here.
//
// Bounds is the window's own frame, which is what image coordinates map onto
// (SCK is asked to leave the shadow out, so the image covers the frame exactly).
func CaptureWindow(c window.Candidate, maxWidth int) (*Result, error) {
	img, err := sck.Capture(c, maxWidth)
	if err != nil {
		return nil, fmt.Errorf("window capture: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	b := img.Bounds()
	return &Result{PNG: buf.Bytes(), Bounds: c.Rect, ImgW: b.Dx(), ImgH: b.Dy()}, nil
}

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
