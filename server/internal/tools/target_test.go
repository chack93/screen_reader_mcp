package tools

import (
	"strings"
	"testing"

	"screenreader/internal/config"
	"screenreader/internal/geom"
)

func TestClassifyTarget(t *testing.T) {
	r := &geom.Rect{X: 7, Y: 8, W: 9, H: 10}
	appCfg := func() *config.Config {
		c := config.Defaults()
		c.App, c.Title = "Windows App", "Prod"
		return c
	}

	t.Run("explicit rect wins", func(t *testing.T) {
		got, err := classifyTarget(appCfg(), "", "", r)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Kind != kindRect || got.Rect != *r {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("per-call title overrides mode", func(t *testing.T) {
		cfg := appCfg()
		cfg.Mode = "rect"
		cfg.Rect = config.Rect{X: 1, Y: 1, W: 5, H: 5}
		got, err := classifyTarget(cfg, "", "Dev", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		// Title override without an app keeps the configured app (as before).
		if got.Kind != kindApp || got.App != "Windows App" || got.Title != "Dev" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("pinned rect mode", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Mode = "rect"
		cfg.Rect = config.Rect{X: 1, Y: 2, W: 300, H: 200}
		got, err := classifyTarget(cfg, "", "", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Kind != kindRect || got.Rect.W != 300 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("rect mode without a size falls through to the app", func(t *testing.T) {
		cfg := appCfg()
		cfg.Mode = "rect"
		got, err := classifyTarget(cfg, "", "", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Kind != kindApp || got.App != "Windows App" || got.Title != "Prod" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("screen mode", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Mode, cfg.Screen = "screen", 1
		got, err := classifyTarget(cfg, "", "", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got.Kind != kindScreen || got.Screen != 1 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("title override without any app", func(t *testing.T) {
		_, err := classifyTarget(config.Defaults(), "", "Dev", nil)
		if err == nil || !strings.Contains(err.Error(), "no app target configured") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("nothing configured", func(t *testing.T) {
		_, err := classifyTarget(config.Defaults(), "", "", nil)
		if err == nil || !strings.Contains(err.Error(), "no target configured") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestClassifyTargetCamera(t *testing.T) {
	cfg := config.Defaults()
	cfg.Mode, cfg.Camera.ID, cfg.Camera.Name = "camera", "AA11", "Cam X"
	cfg.App, cfg.Title = "Stale App", "Old" // stale app must NOT retarget camera mode

	t.Run("camera mode wins over stored app", func(t *testing.T) {
		got, err := classifyTarget(cfg, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kindCamera || got.CameraID != "AA11" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("per-call app override still wins", func(t *testing.T) {
		got, err := classifyTarget(cfg, "Windows App", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kindApp || got.App != "Windows App" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("explicit rect still wins", func(t *testing.T) {
		got, err := classifyTarget(cfg, "", "", &geom.Rect{X: 1, Y: 2, W: 3, H: 4})
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kindRect {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("camera target has no screen bounds", func(t *testing.T) {
		spec, _ := classifyTarget(cfg, "", "", nil)
		if _, err := spec.rect(); err == nil || !strings.Contains(err.Error(), "no screen bounds") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCameraLabel(t *testing.T) {
	cfg := config.Defaults()
	if got := cameraLabel(cfg); got != "system default" {
		t.Fatalf("empty name: %q", got)
	}
	cfg.Camera.Name = "Cam X"
	if got := cameraLabel(cfg); got != "Cam X" {
		t.Fatalf("named: %q", got)
	}
}

func TestCaptureMode(t *testing.T) {
	if got := captureMode(config.Defaults()); got != "region" {
		t.Fatalf("default capture mode = %q, want region", got)
	}
	cfg := config.Defaults()
	cfg.Capture.Window = true
	if got := captureMode(cfg); got != "window" {
		t.Fatalf("capture mode = %q, want window", got)
	}
}
