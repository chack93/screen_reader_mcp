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

// Area is the rect's size in square units (0 for an empty rect).
func (r Rect) Area() int { return r.W * r.H }

// Intersect returns the overlap of a and b, or an empty rect when they do not
// overlap. Both must be in the same coordinate space (screen-absolute points).
func Intersect(a, b Rect) Rect {
	x1, y1 := max(a.X, b.X), max(a.Y, b.Y)
	x2, y2 := min(a.X+a.W, b.X+b.W), min(a.Y+a.H, b.Y+b.H)
	if x2 <= x1 || y2 <= y1 {
		return Rect{}
	}
	return Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
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
