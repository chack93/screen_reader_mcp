package geom

import "testing"

func TestMapImageToScreen_identity(t *testing.T) {
	// 1:1 capture: image == region.
	sx, sy := MapImageToScreen(400, 300, 800, 600, Rect{X: 100, Y: 50, W: 800, H: 600})
	if sx != 500 || sy != 350 {
		t.Fatalf("got (%d,%d) want (500,350)", sx, sy)
	}
}

func TestMapImageToScreen_retina2x(t *testing.T) {
	// Region is 800x600 points; image is 1600x1200 px (2x). Image center -> region center.
	sx, sy := MapImageToScreen(800, 600, 1600, 1200, Rect{X: 0, Y: 0, W: 800, H: 600})
	if sx != 400 || sy != 300 {
		t.Fatalf("got (%d,%d) want (400,300)", sx, sy)
	}
}

func TestScaleDown(t *testing.T) {
	if w, h := ScaleDown(1600, 1200, 800); w != 800 || h != 600 {
		t.Fatalf("downscale got (%d,%d) want (800,600)", w, h)
	}
	if w, h := ScaleDown(800, 600, 1600); w != 800 || h != 600 {
		t.Fatalf("no-op got (%d,%d) want (800,600)", w, h)
	}
	if w, h := ScaleDown(800, 600, 0); w != 800 || h != 600 {
		t.Fatalf("zero-max got (%d,%d) want (800,600)", w, h)
	}
	if w, h := ScaleDown(800, 600, -1); w != 800 || h != 600 {
		t.Fatalf("negative-max got (%d,%d) want (800,600)", w, h)
	}
}

func TestRectArea(t *testing.T) {
	if got := (Rect{X: 5, Y: 5, W: 10, H: 4}).Area(); got != 40 {
		t.Fatalf("Area = %d, want 40", got)
	}
	if got := (Rect{}).Area(); got != 0 {
		t.Fatalf("empty Area = %d, want 0", got)
	}
}

func TestIntersect(t *testing.T) {
	a := Rect{X: 0, Y: 30, W: 1279, H: 1570}
	b := Rect{X: 1000, Y: 300, W: 400, H: 300}
	if got := Intersect(a, b); got != (Rect{X: 1000, Y: 300, W: 279, H: 300}) {
		t.Fatalf("overlap = %+v, want {1000,300,279,300}", got)
	}
	// Touching edges do not overlap (zero-area intersection would skew ratios).
	if got := Intersect(Rect{X: 0, Y: 0, W: 10, H: 10}, Rect{X: 10, Y: 0, W: 10, H: 10}); got.Area() != 0 {
		t.Fatalf("touching rects overlap: %+v", got)
	}
	// Containment returns the smaller rect unchanged.
	if got := Intersect(Rect{X: 0, Y: 0, W: 100, H: 100}, Rect{X: 10, Y: 10, W: 20, H: 20}); got != (Rect{X: 10, Y: 10, W: 20, H: 20}) {
		t.Fatalf("containment = %+v", got)
	}
	if got := Intersect(Rect{X: 0, Y: 0, W: 10, H: 10}, Rect{X: 50, Y: 50, W: 10, H: 10}); got.Area() != 0 {
		t.Fatalf("disjoint rects overlap: %+v", got)
	}
	// Negative-origin rects (secondary displays left of the main one) work too.
	if got := Intersect(Rect{X: -1800, Y: 431, W: 1800, H: 1169}, Rect{X: -1000, Y: 500, W: 400, H: 300}); got.Area() != 400*300 {
		t.Fatalf("secondary-display overlap = %+v", got)
	}
}
