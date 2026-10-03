// Package capture screenshots a screen region via robotgo and encodes PNG.
package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"github.com/go-vgo/robotgo"
	"golang.org/x/image/draw"

	"screenreader/internal/geom"
)

type Result struct {
	PNG    []byte
	Bounds geom.Rect // screen-absolute captured region
	ImgW   int       // PNG pixel width
	ImgH   int       // PNG pixel height
}

// CaptureRegion captures the screen region r, optionally downscaling so the width
// is at most maxWidth (aspect preserved). Returns the PNG bytes and geometry.
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
