// Package config loads/saves the shared screenreader config (pure Go, no cgo).
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// CaptureCfg bounds capture output. MaxWidth 0 (the default) means native
// resolution: the model reads on-screen text from the image itself, so the
// image's pixels are its reading budget. Set a width only to cap payload size.
type CaptureCfg struct {
	MaxWidth int `json:"max_width"` // 0 or negative = native resolution
}

type InputCfg struct {
	DelayMs int `json:"delay_ms"`
}

type HTTPCfg struct {
	Addr  string `json:"addr"`  // loopback only, e.g. "127.0.0.1:18427"
	Token string `json:"token"` // bearer token for /mcp; empty = unauthenticated
}

type Config struct {
	Version int    `json:"version"`
	Access  string `json:"access"` // "full" | "read_only" — which MCP tools the agent gets
	// Hotkey is the menu-bar app's global "stop the server" key spec (e.g. "f9",
	// "ctrl+opt+f9", "off"). The server never reads it; the field exists so
	// Save (set_target rewrites the whole file) does not drop the app's setting.
	Hotkey  string     `json:"hotkey"`
	App     string     `json:"app"`
	Title   string     `json:"title"`
	Mode    string     `json:"mode"`   // "app" | "rect" | "screen"
	Screen  int        `json:"screen"` // display index for mode "screen" (0 = main)
	Rect    Rect       `json:"rect"`
	Capture CaptureCfg `json:"capture"`
	Input   InputCfg   `json:"input"`
	HTTP    HTTPCfg    `json:"http"`
}

// DefaultPath returns $HOME/.config/screenreader/config.json.
func DefaultPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "screenreader", "config.json")
}

// Defaults returns a Config with sane values.
func Defaults() *Config {
	return &Config{
		Version: 1,
		Access:  "full",
		Hotkey:  "f9", // mirrors the app's default; empty would mean "disabled" there
		Mode:    "app",
		Rect:    Rect{},
		// Capture.MaxWidth stays 0: native-resolution captures, because the
		// image is the model's only reading channel.
		Input: InputCfg{DelayMs: 120},
		HTTP:  HTTPCfg{Addr: "127.0.0.1:18427"},
	}
}

// Load reads the config at path. A missing file returns Defaults (no error).
// Present keys override defaults; absent keys keep defaults.
func Load(path string) (*Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return c, err
	}
	return c, nil
}

// Save writes c to path, creating parent directories as needed.
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
