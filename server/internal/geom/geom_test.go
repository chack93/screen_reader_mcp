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
}

func TestCenter(t *testing.T) {
	if cx, cy := Center(Rect{X: 10, Y: 20, W: 100, H: 200}); cx != 60 || cy != 120 {
		t.Fatalf("got (%d,%d) want (60,120)", cx, cy)
	}
}

func TestVisionBBoxToScreen_identity(t *testing.T) {
	// Identity: image 1000x1000 == region 1000x1000 at origin.
	// Vision bbox normalized, bottom-left origin.
	box := VisionBBoxToScreen(0.25, 0.5, 0.5, 0.25, 1000, 1000, Rect{X: 0, Y: 0, W: 1000, H: 1000})
	want := Rect{X: 250, Y: 250, W: 500, H: 250}
	if box != want {
		t.Fatalf("got %v want %v", box, want)
	}
}

func TestVisionBBoxToScreen_offsetAndScale(t *testing.T) {
	// Region 100,50,400,300 ; image 800x600 (2x). Full-image bbox (0,0,1,1) -> whole region.
	box := VisionBBoxToScreen(0, 0, 1, 1, 800, 600, Rect{X: 100, Y: 50, W: 400, H: 300})
	want := Rect{X: 100, Y: 50, W: 400, H: 300}
	if box != want {
		t.Fatalf("got %v want %v", box, want)
	}
}
