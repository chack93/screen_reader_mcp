package capture

import (
	"bytes"
	"image"
	"image/png"
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
