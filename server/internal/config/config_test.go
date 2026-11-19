package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	return d
}

func TestDefaultPath_usesHome(t *testing.T) {
	tempDir(t)
	h, _ := os.UserHomeDir()
	want := filepath.Join(h, ".config", "screenreader", "config.json")
	if got := DefaultPath(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Version != 1 || c.Mode != "app" || c.Input.DelayMs != 120 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.Capture.MaxWidth != 0 {
		t.Fatalf("capture.max_width default = %d, want 0 (native resolution)", c.Capture.MaxWidth)
	}
	// capture.window unset means "auto": an app target is captured as that window
	// alone, so nothing quietly grabs the region behind it instead.
	if c.Capture.Window != nil {
		t.Fatalf("capture.window default = %t, want nil (auto)", *c.Capture.Window)
	}
}

func TestWindowPreference(t *testing.T) {
	yes, no := true, false
	if got := Defaults().WindowPreference(); got != nil {
		t.Fatalf("unset capture.window must report auto (nil), got %t", *got)
	}
	on := Defaults()
	on.Capture.Window = &yes
	if got := on.WindowPreference(); got == nil || !*got {
		t.Fatal("capture.window=true must report true")
	}
	off := Defaults()
	off.Capture.Window = &no
	if got := off.WindowPreference(); got == nil || *got {
		t.Fatal("capture.window=false must report false, not auto")
	}
}

func TestLoad_captureWindow(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"capture":{"max_width":1200,"window":false}}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.Capture.MaxWidth != 1200 || c.Capture.Window == nil || *c.Capture.Window {
		t.Fatalf("capture config not loaded: %+v", c.Capture)
	}
}

func TestLoad_captureWindowAbsentIsAuto(t *testing.T) {
	// A config written before window capture existed (or by the app, which never
	// writes this key) must stay on auto instead of decaying into "false".
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"capture":{"max_width":1200}}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.Capture.Window != nil {
		t.Fatalf("capture.window = %t, want nil (auto)", *c.Capture.Window)
	}
}

func TestSaveLoad_captureWindowRoundTrip(t *testing.T) {
	// set_target rewrites the whole file, so the capture block must survive it —
	// including "false", a deliberate opt-out that must not decay back to auto.
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	yes, no := true, false
	for _, want := range []*bool{&yes, &no} {
		c := Defaults()
		c.Capture.Window = want
		if err := Save(p, c); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := Load(p)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.Capture.Window == nil || *got.Capture.Window != *want {
			t.Fatalf("capture.window=%t lost in round-trip: %+v", *want, got.Capture)
		}
	}

	c := Defaults() // nil is omitted from the file, so auto stays auto
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Capture.Window != nil {
		t.Fatalf("auto must round-trip as auto, got %t", *got.Capture.Window)
	}
}

func TestLoad_missingFile_returnsDefaults(t *testing.T) {
	tempDir(t)
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Mode != "app" || c.Input.DelayMs != 120 {
		t.Fatalf("not defaults: %+v", c)
	}
}

func TestLoad_partialFile_fillsDefaults(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"mode":"rect","rect":{"x":1,"y":2,"w":3,"h":4}}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.Mode != "rect" || c.Rect != (Rect{1, 2, 3, 4}) {
		t.Fatalf("rect not loaded: %+v", c)
	}
	if c.Input.DelayMs != 120 || c.Capture.MaxWidth != 0 {
		t.Fatalf("defaults not filled: %+v", c)
	}
}

func TestSaveLoad_roundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.App = "Microsoft Remote Desktop"
	c.Mode = "app"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.App != c.App || got.Mode != c.Mode {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoad_screenMode(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"mode":"screen","screen":1}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.Mode != "screen" || c.Screen != 1 {
		t.Fatalf("screen target not loaded: %+v", c)
	}
}

func TestSaveLoad_screenRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.Mode = "screen"
	c.Screen = 2
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Mode != "screen" || got.Screen != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// absent "screen" key defaults to 0 (main display)
	if Defaults().Screen != 0 {
		t.Fatalf("default screen should be 0: %+v", Defaults())
	}
}

func TestDefaults_http(t *testing.T) {
	c := Defaults()
	if c.HTTP.Addr != "127.0.0.1:18427" || c.HTTP.Token != "" {
		t.Fatalf("unexpected http defaults: %+v", c.HTTP)
	}
}

func TestDefaults_access(t *testing.T) {
	if c := Defaults(); c.Access != "full" {
		t.Fatalf("default access = %q, want full", c.Access)
	}
}

func TestLoad_access(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"access":"read_only"}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.Access != "read_only" {
		t.Fatalf("access = %q, want read_only", c.Access)
	}
}

func TestSaveLoad_accessRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.Access = "read_only"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Access != "read_only" {
		t.Fatalf("round-trip access = %q, want read_only", got.Access)
	}
}

func TestDefaults_offline(t *testing.T) {
	if c := Defaults(); c.Offline {
		t.Fatalf("default offline = true, want false")
	}
}

func TestLoad_offline(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"offline":true}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !c.Offline {
		t.Fatalf("offline = false, want true")
	}
}

// TestSaveLoad_offlineRoundTrip: "offline" belongs to the menu-bar app, but
// it must survive a server-side Save (set_target rewrites the whole file).
func TestSaveLoad_offlineRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.Offline = true
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Offline {
		t.Fatalf("round-trip offline = false, want true")
	}
}

// TestSaveLoad_hotkeyRoundTrip: "hotkey" belongs to the menu-bar app (the
// server never reads it), but it must survive a server-side Save — set_target
// rewrites the whole file, and any key missing from the struct is dropped.
func TestSaveLoad_hotkeyRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.Hotkey = "ctrl+opt+f9"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Hotkey != "ctrl+opt+f9" {
		t.Fatalf("hotkey = %q, want ctrl+opt+f9", got.Hotkey)
	}
	// A config without the key must come back as the app's default, not as "":
	// the app reads "" as "hotkey disabled", so an empty default would silently
	// switch the kill key off the first time set_target rewrote the file.
	os.WriteFile(p, []byte(`{"mode":"app"}`), 0o644)
	got, err = Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Hotkey != "f9" {
		t.Fatalf("hotkey default = %q, want f9", got.Hotkey)
	}
	if err := Save(p, got); err != nil {
		t.Fatalf("save: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), `"hotkey": "f9"`) {
		t.Fatalf("saved config lost the hotkey: %s", b)
	}
}

func TestLoad_httpOverride(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"http":{"addr":"127.0.0.1:19999","token":"abc"}}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.HTTP.Addr != "127.0.0.1:19999" || c.HTTP.Token != "abc" {
		t.Fatalf("http not loaded: %+v", c.HTTP)
	}
	if c.Mode != "app" || c.Input.DelayMs != 120 {
		t.Fatalf("defaults not filled: %+v", c)
	}
}

func TestSaveLoad_httpRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.HTTP.Token = "t0k"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.HTTP != c.HTTP {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got.HTTP, c.HTTP)
	}
}

func TestCameraKeyRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"mode":"camera","camera":{"id":"AA11","name":"Cam X"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != "camera" || c.Camera.ID != "AA11" || c.Camera.Name != "Cam X" {
		t.Fatalf("parsed %+v", c)
	}
	if err := Save(p, c); err != nil { // a set_target-style full rewrite must keep the app's key
		t.Fatal(err)
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Camera.ID != "AA11" || c2.Camera.Name != "Cam X" {
		t.Fatalf("camera key lost on Save: %+v", c2.Camera)
	}
	if Defaults().Camera.ID != "" || Defaults().Mode != "app" {
		t.Fatal("defaults changed")
	}
}
