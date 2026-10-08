package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if c.Version != 1 || c.Mode != "app" || c.OCR.Level != "accurate" ||
		c.Capture.MaxWidth != 1600 || c.Input.DelayMs != 120 ||
		!reflect.DeepEqual(c.OCR.Languages, []string{"en"}) {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoad_missingFile_returnsDefaults(t *testing.T) {
	tempDir(t)
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Mode != "app" || c.Capture.MaxWidth != 1600 {
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
	if c.OCR.Level != "accurate" || c.Capture.MaxWidth != 1600 {
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
	if c.Mode != "app" || c.Capture.MaxWidth != 1600 {
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
