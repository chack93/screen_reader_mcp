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
