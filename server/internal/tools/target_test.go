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

func TestWantsWindow(t *testing.T) {
	yes, no := true, false
	auto := config.Defaults()
	on := config.Defaults()
	on.Capture.Window = &yes
	off := config.Defaults()
	off.Capture.Window = &no

	// Auto: an app target renders that window; a rect or a screen has none.
	if got, err := wantsWindow(auto, kindApp, nil); err != nil || !got {
		t.Fatalf("auto + app: got %t (err %v), want window capture", got, err)
	}
	for _, kind := range []targetKind{kindRect, kindScreen} {
		if got, err := wantsWindow(auto, kind, nil); err != nil || got {
			t.Fatalf("auto + %s: got %t (err %v), want the region grab", kind, got, err)
		}
	}

	// Asking for a window on a target that has none must fail, not quietly return
	// the everything-in-the-rect image; asking not to is always allowed.
	for _, kind := range []targetKind{kindRect, kindScreen} {
		if _, err := wantsWindow(auto, kind, &yes); err == nil || !strings.Contains(err.Error(), "needs an app target") {
			t.Fatalf("window:true + %s: want an error, got %v", kind, err)
		}
		if got, err := wantsWindow(auto, kind, &no); err != nil || got {
			t.Fatalf("window:false + %s: got %t (err %v), want the region grab", kind, got, err)
		}
	}

	// A config-level true must not break a rect/screen target: switching the app's
	// target kind cannot be expected to come with editing the capture block.
	if got, err := wantsWindow(on, kindRect, nil); err != nil || got {
		t.Fatalf("capture.window=true + rect: got %t (err %v), want the region grab", got, err)
	}

	// false is the escape hatch back to the region grab from either place, and the
	// per-call argument always beats the config.
	if got, err := wantsWindow(off, kindApp, nil); err != nil || got {
		t.Fatalf("capture.window=false + app: got %t (err %v), want the region grab", got, err)
	}
	if got, err := wantsWindow(auto, kindApp, &no); err != nil || got {
		t.Fatalf("window:false + app: got %t (err %v), want the region grab", got, err)
	}
	if got, err := wantsWindow(on, kindApp, &no); err != nil || got {
		t.Fatalf("window:false vs capture.window=true: got %t (err %v), want the per-call answer", got, err)
	}
	if got, err := wantsWindow(off, kindApp, &yes); err != nil || !got {
		t.Fatalf("window:true vs capture.window=false: got %t (err %v), want window capture", got, err)
	}
}

func TestCaptureMode(t *testing.T) {
	auto := config.Defaults()
	if got := captureMode(auto.WindowPreference(), kindApp); got != "window(auto)" {
		t.Fatalf("auto + app = %q, want window(auto)", got)
	}
	if got := captureMode(auto.WindowPreference(), kindRect); got != "region(auto)" {
		t.Fatalf("auto + rect = %q, want region(auto)", got)
	}
	yes, no := true, false
	if got := captureMode(&yes, kindApp); got != "window" {
		t.Fatalf("true + app = %q, want window", got)
	}
	if got := captureMode(&no, kindApp); got != "region" {
		t.Fatalf("false + app = %q, want region", got)
	}
	if got := captureMode(&yes, kindScreen); got != "region" {
		t.Fatalf("true + screen = %q, want region (no window to isolate)", got)
	}
}
