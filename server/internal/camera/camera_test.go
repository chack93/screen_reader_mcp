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

func TestBgraToRGBA_ExactBoundary(t *testing.T) {
	// needed = (height-1)*rowBytes + width*4 = 3*12 + 8 = 44.
	if _, err := bgraToRGBA(make([]byte, 44), 2, 4, 12); err != nil {
		t.Fatalf("len exactly == needed must pass: %v", err)
	}
	if _, err := bgraToRGBA(make([]byte, 43), 2, 4, 12); err == nil {
		t.Fatal("one byte short must error")
	}
}

func TestBgraToRGBA_PaddedTwoRows(t *testing.T) {
	// 2x2 at rowBytes 10: last pixel of each row must come from the row start,
	// never from the 2-byte padding tail.
	src := []byte{
		1, 2, 3, 255, 5, 6, 7, 255, 0, 0,
		11, 12, 13, 255, 15, 16, 17, 255, 0, 0,
	}
	img, err := bgraToRGBA(src, 2, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{3, 2, 1, 255, 7, 6, 5, 255, 13, 12, 11, 255, 17, 16, 15, 255}
	for i, w := range want {
		if img.Pix[i] != w {
			t.Fatalf("pix[%d] = %d, want %d (%v)", i, img.Pix[i], w, img.Pix)
		}
	}
}

func TestBgraToRGBA_BadGeometry(t *testing.T) {
	if _, err := bgraToRGBA(nil, 0, 4, 16); err == nil {
		t.Fatal("zero width must error")
	}
	if _, err := bgraToRGBA(nil, 4, 0, 16); err == nil {
		t.Fatal("zero height must error")
	}
	// rowBytes smaller than one row of pixels is invalid even if len is big enough.
	if _, err := bgraToRGBA(make([]byte, 64), 4, 4, 8); err == nil {
		t.Fatal("rowBytes < width*4 must error")
	}
}
