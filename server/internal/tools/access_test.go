package tools

import (
	"testing"

	"screenreader/internal/config"
)

var allToolNames = []string{
	"capture", "click", "doctor", "drag", "focus_window", "key",
	"list_screens", "list_windows", "move", "paste", "scroll",
	"set_target", "type", "wait", "window_info",
}

// readOnlyToolNames is the explicit allowlist an agent sees when config
// access is "read_only".
var readOnlyToolNames = []string{
	"capture", "doctor", "list_screens", "list_windows", "wait", "window_info",
}

func TestAllow_full(t *testing.T) {
	for _, name := range allToolNames {
		if !Allow(name, "") || !Allow(name, AccessFull) {
			t.Errorf("Allow(%q, \"\"/full) = false, want true", name)
		}
	}
}

func TestAllow_readOnly(t *testing.T) {
	allowed := map[string]bool{}
	for _, n := range readOnlyToolNames {
		allowed[n] = true
	}
	for _, name := range allToolNames {
		if got := Allow(name, AccessReadOnly); got != allowed[name] {
			t.Errorf("Allow(%q, read_only) = %v, want %v", name, got, allowed[name])
		}
	}
}

func TestAllow_unknownAccessFailsClosed(t *testing.T) {
	if Allow("click", "bogus") {
		t.Error("input tool allowed under unknown access mode")
	}
	if !Allow("capture", "bogus") {
		t.Error("read-only tool denied under unknown access mode")
	}
}

func TestAccessGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// No config file → full: everything passes.
	if err := accessGuard("click"); err != nil {
		t.Fatalf("click blocked without config: %v", err)
	}

	cfg := config.Defaults()
	cfg.Access = AccessReadOnly
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := accessGuard("click"); err == nil {
		t.Fatal("click not blocked under read_only")
	}
	if err := accessGuard("capture"); err != nil {
		t.Fatalf("capture blocked under read_only: %v", err)
	}
}
