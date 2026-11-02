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
