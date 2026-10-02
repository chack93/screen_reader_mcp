// Package geom holds pure coordinate math (no cgo, no display).
package geom

// Rect is a screen-absolute rectangle: top-left (X,Y) and size (W,H).
// JSON tags use lowercase so tool `rect` args match the on-disk config shape.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// MapImageToScreen maps a point in image pixel space (top-left origin) to
// screen-absolute coordinates, given the captured region r and the image size.
func MapImageToScreen(imgX, imgY, imgW, imgH int, r Rect) (sx, sy int) {
	if imgW <= 0 || imgH <= 0 {
		return r.X + imgX, r.Y + imgY
	}
	sx = r.X + imgX*r.W/imgW
	sy = r.Y + imgY*r.H/imgH
	return sx, sy
}

// Center returns the integer center of a rect.
func Center(r Rect) (cx, cy int) {
	return r.X + r.W/2, r.Y + r.H/2
}

// ScaleDown returns (w,h) fitting maxWidth while preserving aspect ratio.
// maxWidth<=0 or imgW<=maxWidth returns the original size.
func ScaleDown(imgW, imgH, maxWidth int) (w, h int) {
	if maxWidth <= 0 || imgW <= maxWidth {
		return imgW, imgH
	}
	w = maxWidth
	h = imgH * maxWidth / imgW
	if h < 1 {
		h = 1
	}
	return w, h
}

// VisionBBoxToScreen converts a Vision bounding box (normalized [0,1], bottom-left
// origin) to a screen-absolute Rect, given the image pixel size and the captured region.
func VisionBBoxToScreen(bx, by, bw, bh float64, imgW, imgH int, r Rect) Rect {
	// Vision (bottom-left) -> image pixels (top-left).
	ix := bx * float64(imgW)
	iy := (1 - by - bh) * float64(imgH)
	iw := bw * float64(imgW)
	ih := bh * float64(imgH)
	// Image pixels -> screen-absolute (proportional, DPI-safe).
	sx := r.X + int(ix*float64(r.W)/float64(imgW))
	sy := r.Y + int(iy*float64(r.H)/float64(imgH))
	sw := int(iw * float64(r.W) / float64(imgW))
	sh := int(ih * float64(r.H) / float64(imgH))
	return Rect{X: sx, Y: sy, W: sw, H: sh}
}
