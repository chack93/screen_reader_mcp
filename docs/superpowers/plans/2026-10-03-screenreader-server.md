# ScreenReader MCP Server (Go) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `screenreader`, a single-binary Go stdio MCP server that captures/OCRs a Remote Desktop window on the Mac and drives it with synthetic mouse/keyboard input, so an opencode agent can read files and do research on a remote machine.

**Architecture:** Two layers. *Observation* resolves the target window's screen-absolute bounds, captures that region, and returns a PNG (for the vision model) and/or Apple Vision OCR text. *Action* maps model-chosen coordinates to screen space and synthesizes mouse/keyboard events. Pure logic (coordinate math, config, key parsing) is isolated into cgo-free packages that are unit-testable without a display; platform work (window, capture, OCR, input) is thin wrappers around `osascript`, `robotgo`, and `tmc/apple`.

**Tech Stack:** Go (module floor `go 1.26.0` — tmc/apple needs ≥1.25, x/image@latest needs 1.26; Go's toolchain auto-download covers older local installs), `CGO_ENABLED=1`, Xcode Command Line Tools. Deps: `github.com/modelcontextprotocol/go-sdk` (Tier 1), `github.com/go-vgo/robotgo` (Cgo backend: mouse/keyboard/region-capture/clipboard), `github.com/tmc/apple` (Vision OCR), `golang.org/x/image` (downscale). Window enumeration/activation via `osascript` (AppleScript).

**Spec:** `docs/superpowers/specs/2026-10-03-screenreader-mcp-design.md`

## Global Constraints

- **Platform:** macOS only. Requires **Xcode Command Line Tools** (`xcode-select --install`) for cgo (robotgo + tmc/apple).
- **Build:** `CGO_ENABLED=1 go build -o screenreader .` from `server/`. Produces one executable linking only macOS system frameworks.
- **Module path:** `screenreader` (go.mod lives at `server/go.mod`). Internal imports are `screenreader/internal/<pkg>`.
- **Coordinate space:** screen-absolute, **top-left origin of the main display, Y down** (Quartz/window-server space). All input tools default to this.
- **Config file:** `~/.config/screenreader/config.json` (single source of truth). Read fresh on every action.
- **Coordinate convention:** tool `x,y` are screen-absolute by default; `capture`/`ocr` return region `bounds` + image pixel size (or screen-absolute OCR boxes) so the model can map image→screen.
- **Errors:** tool failures return `mcp.CallToolResult{IsError: true, Content:[TextContent]}` with an actionable message (never a bare transport error).
- **Commit cadence:** every task ends with a commit. Pure packages get automated tests; platform packages get a manual smoke run on the dev Mac.

---

## File Structure

```
server/
  go.mod                     # module screenreader
  main.go                    # stdio MCP server entry; registers all tools
  internal/
    geom/geom.go             # Rect + image<->screen + Vision bbox math (PURE, no cgo)
    geom/geom_test.go
    config/config.go         # config load/save/defaults (PURE, no cgo)
    config/config_test.go
    keycombo/keycombo.go     # "cmd+shift+p" parsing (PURE, no cgo)
    keycombo/keycombo_test.go
    window/window.go         # osascript: resolve bounds, list, focus (cgo-free; exec osascript)
    capture/capture.go       # robotgo region capture + downscale + PNG encode
    ocr/ocr.go               # tmc/apple Vision OCR + bbox conversion
    input/input.go           # robotgo mouse/keyboard/clipboard + dry_run
    tools/tools.go           # all MCP tool definitions + handlers
```

---

### Task 1: Scaffold module + verify build environment

**Files:**
- Create: `server/go.mod`
- Create: `server/probe/main.go` (temporary build probe; deleted in Task 12)

**Interfaces:**
- Produces: a module `screenreader` that compiles with cgo and links robotgo + tmc/apple + go-sdk. Confirms Xcode CLT is present.

- [ ] **Step 1: Verify toolchain**

Run:
```bash
cd server 2>/dev/null || mkdir server && cd server
go version                 # expect go1.22 or newer
xcode-select -p            # expect a path (e.g. /Applications/Xcode.app/.../CommandLineTools)
```
Expected: Go ≥1.22 and an Xcode CLT path. If CLT is missing, run `xcode-select --install` and wait, then re-check.

- [ ] **Step 2: Init module and add deps**

```bash
cd server
go mod init screenreader
go get github.com/modelcontextprotocol/go-sdk@latest
go get github.com/go-vgo/robotgo@latest
go get github.com/tmc/apple@latest
go get golang.org/x/image@latest
```

- [ ] **Step 3: Write a build probe**

Create `server/probe/main.go`:
```go
package main

import (
	"fmt"

	"github.com/go-vgo/robotgo"
	"github.com/tmc/apple/vision"
)

func main() {
	sx, sy := robotgo.GetScreenSize()
	fmt.Printf("screen: %dx%d\n", sx, sy)
	// Touch the Vision symbol to force the framework link.
	_ = vision.NewVNRecognizeTextRequest
	fmt.Println("probe ok")
}
```

- [ ] **Step 4: Build and run the probe**

```bash
cd server
CGO_ENABLED=1 go build -o /tmp/sr-probe ./probe
/tmp/sr-probe
```
Expected: prints `screen: <W>x<H>` and `probe ok`. If the build fails on missing frameworks/headers, Xcode CLT is not fully installed — fix and re-run. (Exact `GetScreenSize`/`NewVNRecognizeTextRequest` names verified in research; if the compiler rejects a name, run `go doc github.com/go-vgo/robotgo GetScreenSize` and `go doc github.com/tmc/apple/vision NewVNRecognizeTextRequest` and adjust.)

- [ ] **Step 5: Commit**

```bash
cd server && git add go.mod go.sum probe && git commit -m "chore: scaffold screenreader module; verify cgo + robotgo + Vision link"
```

---

### Task 2: `geom` — coordinate math (pure, TDD)

**Files:**
- Create: `server/internal/geom/geom.go`
- Test: `server/internal/geom/geom_test.go`

**Interfaces:**
- Produces:
  - `type Rect struct{ X, Y, W, H int }`
  - `func MapImageToScreen(imgX, imgY, imgW, imgH int, r Rect) (sx, sy int)`
  - `func VisionBBoxToScreen(bx, by, bw, bh float64, imgW, imgH int, r Rect) Rect`
  - `func Center(r Rect) (cx, cy int)`
  - `func ScaleDown(imgW, imgH, maxWidth int) (w, h int)`

- [ ] **Step 1: Write the failing tests**

Create `server/internal/geom/geom_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/geom/`
Expected: FAIL (functions/types undefined).

- [ ] **Step 3: Implement**

Create `server/internal/geom/geom.go`:
```go
// Package geom holds pure coordinate math (no cgo, no display).
package geom

// Rect is a screen-absolute rectangle: top-left (X,Y) and size (W,H).
// JSON tags use lowercase so tool `rect` args match the on-disk config shape.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// MapImageToScreen maps a point in image pixel space (top-left origin) to
// screen-absolute coordinates, given the captured region r and the image size.
func MapImageToScreen(imgX, imgY, imgW, imgH int, r Rect) (sx, sy int) {
	if imgW <= 0 || imgH <= 0 {
		return r.X + imgX, r.Y + imgY
	}
	sx = r.X + imgX*r.W/imgW
	sy = r.Y + imgY*r.H/imgH
	return sx, sy
}

// Center returns the integer center of a rect.
func Center(r Rect) (cx, cy int) {
	return r.X + r.W/2, r.Y + r.H/2
}

// ScaleDown returns (w,h) fitting maxWidth while preserving aspect ratio.
// maxWidth<=0 or imgW<=maxWidth returns the original size.
func ScaleDown(imgW, imgH, maxWidth int) (w, h int) {
	if maxWidth <= 0 || imgW <= maxWidth {
		return imgW, imgH
	}
	w = maxWidth
	h = imgH * maxWidth / imgW
	if h < 1 {
		h = 1
	}
	return w, h
}

// VisionBBoxToScreen converts a Vision bounding box (normalized [0,1], bottom-left
// origin) to a screen-absolute Rect, given the image pixel size and the captured region.
func VisionBBoxToScreen(bx, by, bw, bh float64, imgW, imgH int, r Rect) Rect {
	// Vision (bottom-left) -> image pixels (top-left).
	ix := bx * float64(imgW)
	iy := (1 - by - bh) * float64(imgH)
	iw := bw * float64(imgW)
	ih := bh * float64(imgH)
	// Image pixels -> screen-absolute (proportional, DPI-safe).
	sx := r.X + int(ix*float64(r.W)/float64(imgW))
	sy := r.Y + int(iy*float64(r.H)/float64(imgH))
	sw := int(iw * float64(r.W) / float64(imgW))
	sh := int(ih * float64(r.H) / float64(imgH))
	return Rect{X: sx, Y: sy, W: sw, H: sh}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./internal/geom/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd server && git add internal/geom && git commit -m "feat(geom): screen-absolute image/bbox coordinate math"
```

---

### Task 3: `config` — load/save/defaults (pure, TDD)

**Files:**
- Create: `server/internal/config/config.go`
- Test: `server/internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Rect struct{ X, Y, W, H int }`
  - `type Config struct{ Version int; App, Title, Mode string; Rect Rect; OCR OCRCfg; Capture CaptureCfg; Input InputCfg }`
  - `type OCRCfg struct{ Languages []string; Level string }`
  - `type CaptureCfg struct{ MaxWidth int }`
  - `type InputCfg struct{ DelayMs int }`
  - `func DefaultPath() string`
  - `func Defaults() *Config`
  - `func Load(path string) (*Config, error)`
  - `func Save(path string, c *Config) error`

- [ ] **Step 1: Write the failing tests**

Create `server/internal/config/config_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/config/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

Create `server/internal/config/config.go`:
```go
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

type OCRCfg struct {
	Languages []string `json:"languages"`
	Level     string   `json:"level"` // "fast" | "accurate"
}

type CaptureCfg struct {
	MaxWidth int `json:"max_width"`
}

type InputCfg struct {
	DelayMs int `json:"delay_ms"`
}

type Config struct {
	Version int         `json:"version"`
	App     string      `json:"app"`
	Title   string      `json:"title"`
	Mode    string      `json:"mode"` // "app" | "rect"
	Rect    Rect        `json:"rect"`
	OCR     OCRCfg      `json:"ocr"`
	Capture CaptureCfg  `json:"capture"`
	Input   InputCfg    `json:"input"`
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
		Mode:    "app",
		Rect:    Rect{},
		OCR:     OCRCfg{Languages: []string{"en"}, Level: "accurate"},
		Capture: CaptureCfg{MaxWidth: 1600},
		Input:   InputCfg{DelayMs: 120},
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd server && git add internal/config && git commit -m "feat(config): load/save/defaults for shared config.json"
```

---

### Task 4: `keycombo` — shortcut parsing (pure, TDD)

**Files:**
- Create: `server/internal/keycombo/keycombo.go`
- Test: `server/internal/keycombo/keycombo_test.go`

**Interfaces:**
- Produces: `func ParseCombo(combo string) (key string, mods []string, err error)`
  - `key` is the last token (lowercased). `mods` are the leading tokens normalized to `cmd|ctrl|shift|opt`.
  - Accepts aliases: `command/cmd`, `control/ctrl`, `option/opt/alt`, `shift`.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/keycombo/keycombo_test.go`:
```go
package keycombo

import (
	"reflect"
	"testing"
)

func TestParseCombo(t *testing.T) {
	cases := []struct {
		in      string
		key     string
		mods    []string
		wantErr bool
	}{
		{"cmd+v", "v", []string{"cmd"}, false},
		{"cmd+shift+p", "p", []string{"cmd", "shift"}, false},
		{"return", "return", nil, false},
		{"control+a", "a", []string{"ctrl"}, false},
		{"option+tab", "tab", []string{"opt"}, false},
		{"alt+f4", "f4", []string{"opt"}, false},
		{"", "", nil, true},
		{"bogus+x", "x", nil, true},
	}
	for _, c := range cases {
		key, mods, err := ParseCombo(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q: wanted error, got key=%q mods=%v", c.in, key, mods)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, err)
		}
		if key != c.key || !reflect.DeepEqual(mods, c.mods) {
			t.Fatalf("%q: got key=%q mods=%v want key=%q mods=%v", c.in, key, mods, c.key, c.mods)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/keycombo/`
Expected: FAIL (undefined).

- [ ] **Step 3: Implement**

Create `server/internal/keycombo/keycombo.go`:
```go
// Package keycombo parses key shortcut strings (pure Go, no cgo).
package keycombo

import (
	"errors"
	"strings"
)

var aliases = map[string]string{
	"cmd": "cmd", "command": "cmd",
	"ctrl": "ctrl", "control": "ctrl",
	"shift": "shift",
	"opt": "opt", "option": "opt", "alt": "opt",
}

// ParseCombo splits a "+"-separated shortcut into its key (last token) and
// normalized modifiers. Unknown leading tokens are an error.
func ParseCombo(combo string) (key string, mods []string, err error) {
	combo = strings.TrimSpace(combo)
	if combo == "" {
		return "", nil, errors.New("empty key combo")
	}
	parts := strings.Split(combo, "+")
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(parts[i]))
	}
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "", nil, errors.New("missing key in combo")
	}
	for _, m := range parts[:len(parts)-1] {
		n, ok := aliases[m]
		if !ok {
			return "", nil, errors.New("unknown modifier: " + m)
		}
		mods = append(mods, n)
	}
	return parts[len(parts)-1], mods, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./internal/keycombo/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd server && git add internal/keycombo && git commit -m "feat(keycombo): parse cmd/ctrl/shift/opt shortcuts"
```

---

### Task 5: `window` — osascript bounds/list/focus

**Files:**
- Create: `server/internal/window/window.go`

**Interfaces:**
- Consumes: `screenreader/internal/geom` (`geom.Rect`).
- Produces:
  - `type Win struct{ App, Title string; Rect geom.Rect }`
  - `func Resolve(app, title string) (geom.Rect, error)` — screen-absolute rect of the app's window whose title matches `title` (regex; empty = first window).
  - `func List() ([]Win, error)` — visible windows across all apps.
  - `func Focus(app, title string) (geom.Rect, error)` — activate the app, raise the matching window, return its rect.

- [ ] **Step 1: Implement**

Create `server/internal/window/window.go`:
```go
// Package window resolves window geometry via AppleScript (osascript).
// It is cgo-free (uses exec) and returns screen-absolute, top-left/Y-down rects.
package window

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"screenreader/internal/geom"
)

type Win struct {
	App   string
	Title string
	Rect  geom.Rect
}

func runAppleScript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("osascript: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// windowsOfApp returns "title<tab>x<tab>y<tab>w<tab>h" lines for the app's windows.
func windowsOfApp(app string) (string, error) {
	script := fmt.Sprintf(`
tell application "System Events"
  set proc to first process whose name is "%s"
  set lines to {}
  repeat with w in (every window of proc)
    set p to position of w
    set s to size of w
    set end of lines to ((title of w) & tab & (item 1 of p) & tab & (item 2 of p) & tab & (item 1 of s) & tab & (item 2 of s))
  end repeat
  set AppleScript's text item delimiters to linefeed
  return lines as text
end tell`, app)
	return runAppleScript(script)
}

// Resolve returns the screen-absolute rect of the app's window matching title (regex).
// Empty title returns the first window.
func Resolve(app, title string) (geom.Rect, error) {
	raw, err := windowsOfApp(app)
	if err != nil {
		return geom.Rect{}, fmt.Errorf("resolve %q: %w", app, err)
	}
	lines := strings.Split(raw, "\n")
	re := regexp.MustCompile(title)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		if title != "" && !re.MatchString(f[0]) {
			continue
		}
		x, _ := strconv.Atoi(f[1])
		y, _ := strconv.Atoi(f[2])
		w, _ := strconv.Atoi(f[3])
		h, _ := strconv.Atoi(f[4])
		return geom.Rect{X: x, Y: y, W: w, H: h}, nil
	}
	return geom.Rect{}, fmt.Errorf("no window for app %q (title %q)", app, title)
}

// List returns visible windows across all apps.
func List() ([]Win, error) {
	script := `
tell application "System Events"
  set lines to {}
  repeat with p in (every process whose visible is true)
    try
      repeat with w in (every window of p)
        set pn to name of p
        set pos to position of w
        set sz to size of w
        set end of lines to (pn & tab & (title of w) & tab & (item 1 of pos) & tab & (item 2 of pos) & tab & (item 1 of sz) & tab & (item 2 of sz))
      end repeat
    end try
  end repeat
  set AppleScript's text item delimiters to linefeed
  return lines as text
`
	raw, err := runAppleScript(script)
	if err != nil {
		return nil, err
	}
	var wins []Win
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			continue
		}
		x, _ := strconv.Atoi(f[2])
		y, _ := strconv.Atoi(f[3])
		w, _ := strconv.Atoi(f[4])
		h, _ := strconv.Atoi(f[5])
		wins = append(wins, Win{App: f[0], Title: f[1], Rect: geom.Rect{X: x, Y: y, W: w, H: h}})
	}
	return wins, nil
}

// Focus activates the app and raises the matching window; returns its rect.
func Focus(app, title string) (geom.Rect, error) {
	if _, err := runAppleScript(fmt.Sprintf(`tell application "%s" to activate`, app)); err != nil {
		return geom.Rect{}, err
	}
	return Resolve(app, title)
}
```

> If `osascript` returns an AppleScript error for a non-running app, `Resolve`/`Focus` surface it via the `osascript:` error — tools map that to an actionable message.

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/window/`
Expected: compiles (no cgo needed for this package).

- [ ] **Step 3: Manual smoke (dev Mac, needs Accessibility permission)**

Run:
```bash
cd server
mkdir -p probe
cat > probe/winsmoke.go <<'EOF'
package main

import (
	"fmt"

	"screenreader/internal/window"
)

func main() {
	w, err := window.List()
	if err != nil {
		fmt.Println("list err:", err)
		return
	}
	fmt.Printf("windows: %d\n", len(w))
	for i, x := range w {
		if i > 4 {
			break
		}
		fmt.Printf("  %-20s %-20s %+v\n", x.App, x.Title, x.Rect)
	}
}
EOF
CGO_ENABLED=1 go run ./probe/winsmoke.go
```
Expected: prints a count and a few `App Title {x y w h}` lines. If you get an `osascript:` permission error, grant **Automation** (System Events) + **Accessibility** in System Settings, then re-run.

- [ ] **Step 4: Clean up + commit**

```bash
cd server && rm -f probe/winsmoke.go
git add internal/window && git commit -m "feat(window): osascript window resolve/list/focus (screen-absolute)"
```

---

### Task 6: `capture` — robotgo region capture + downscale

**Files:**
- Create: `server/internal/capture/capture.go`

**Interfaces:**
- Consumes: `geom.Rect`, `geom.ScaleDown`; `github.com/go-vgo/robotgo`, `golang.org/x/image/draw`.
- Produces:
  - `type Result struct{ PNG []byte; Bounds geom.Rect; ImgW, ImgH int }`
  - `func CaptureRegion(r geom.Rect, maxWidth int) (*Result, error)`

- [ ] **Step 1: Implement**

Create `server/internal/capture/capture.go`:
```go
// Package capture screenshots a screen region via robotgo and encodes PNG.
package capture

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"github.com/go-vgo/robotgo"
	"golang.org/x/image/draw"

	"screenreader/internal/geom"
)

type Result struct {
	PNG     []byte
	Bounds  geom.Rect // screen-absolute captured region
	ImgW    int       // PNG pixel width
	ImgH    int       // PNG pixel height
}

// CaptureRegion captures the screen region r, optionally downscaling so the width
// is at most maxWidth (aspect preserved). Returns the PNG bytes and geometry.
func CaptureRegion(r geom.Rect, maxWidth int) (*Result, error) {
	img, err := robotgo.Capture(r.X, r.Y, r.W, r.H) // *image.RGBA
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	w, h := geom.ScaleDown(srcW, srcH, maxWidth)
	if w != srcW || h != srcH {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return &Result{PNG: buf.Bytes(), Bounds: r, ImgW: w, ImgH: h}, nil
}
```

> Verify `robotgo.Capture(x, y, w, h) (*image.RGBA, error)` with `go doc github.com/go-vgo/robotgo Capture`. If the version returns a different type (e.g. `*image.Image`), adapt the `img` variable accordingly — `png.Encode` accepts any `image.Image`.

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/capture/`
Expected: compiles.

- [ ] **Step 3: Manual smoke (dev Mac, needs Screen Recording permission)**

```bash
cd server
mkdir -p probe
cat > probe/cap.go <<'EOF'
package main

import (
	"fmt"
	"os"

	"screenreader/internal/capture"
	"screenreader/internal/geom"
)

func main() {
	r, err := capture.CaptureRegion(geom.Rect{X: 0, Y: 0, W: 400, H: 300}, 0)
	if err != nil {
		fmt.Println("err:", err)
		os.Exit(1)
	}
	os.WriteFile("/tmp/sr-shot.png", r.PNG, 0o644)
	fmt.Printf("saved /tmp/sr-shot.png %dx%d bounds=%+v\n", r.ImgW, r.ImgH, r.Bounds)
}
EOF
CGO_ENABLED=1 go run ./probe/cap.go && open /tmp/sr-shot.png
```
Expected: `/tmp/sr-shot.png` is written and opens showing the top-left 400×300 of the screen. If it's black/empty, grant **Screen Recording** to the terminal/opencode, then re-run.

- [ ] **Step 4: Clean up + commit**

```bash
cd server && rm -f probe/cap.go
git add internal/capture && git commit -m "feat(capture): robotgo region capture + downscale + PNG"
```

---

### Task 7: `ocr` — tmc/apple Vision OCR

**Files:**
- Create: `server/internal/ocr/ocr.go`

**Interfaces:**
- Consumes: `geom.Rect`, `geom.VisionBBoxToScreen`; `github.com/tmc/apple/{foundation,vision}`.
- Produces:
  - `type Line struct{ Text string; Confidence float64; Box geom.Rect }` (Box screen-absolute)
  - `func OCR(pngPath string, languages []string, level string, imgW, imgH int, rect geom.Rect) ([]Line, error)`

- [ ] **Step 1: Implement**

Create `server/internal/ocr/ocr.go`:
```go
// Package ocr runs Apple Vision text recognition via tmc/apple.
package ocr

import (
	"fmt"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/vision"

	"screenreader/internal/geom"
)

type Line struct {
	Text       string
	Confidence float64
	Box        geom.Rect // screen-absolute
}

// OCR runs Vision on the PNG at pngPath and returns lines with screen-absolute
// boxes. imgW/imgH are the PNG's pixel dims; rect is the captured region.
func OCR(pngPath string, languages []string, level string, imgW, imgH int, rect geom.Rect) ([]Line, error) {
	url := foundation.NewURLFileURLWithPath(pngPath)
	handler := vision.NewImageRequestHandlerWithURLOptions(url, nil)
	req := vision.NewVNRecognizeTextRequest()
	if len(languages) > 0 {
		req.SetRecognitionLanguages(languages)
	}
	if level == "fast" {
		req.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelFast)
	} else {
		req.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelAccurate)
	}
	ok, err := handler.PerformRequestsError([]vision.VNRequest{req.VNRequest})
	if err != nil {
		return nil, fmt.Errorf("vision: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("vision: request failed")
	}
	var lines []Line
	for _, obs := range req.Results() {
		o := vision.VNRecognizedTextObservationFromID(obs.ID)
		cands := o.TopCandidates(1)
		if len(cands) == 0 {
			continue
		}
		c := cands[0]
		bb := o.BoundingBox() // normalized, bottom-left origin
		box := geom.VisionBBoxToScreen(bb.X, bb.Y, bb.Width, bb.Height, imgW, imgH, rect)
		lines = append(lines, Line{Text: c.String(), Confidence: float64(c.Confidence()), Box: box})
	}
	return lines, nil
}
```

> Verify the exact names with `go doc`:
> - `go doc github.com/tmc/apple/vision VNRecognizedTextObservation` → confirm `BoundingBox()` return fields (`X,Y,Width,Height`) and `TopCandidates(n)`.
> - `go doc github.com/tmc/apple/vision VNRequestTextRecognitionLevelFast` / `...Accurate`.
> - `go doc github.com/tmc/apple/foundation NewURLFileURLWithPath`.
> Adjust identifiers if they differ (e.g. `BoundingBox` may return a named `CGRect` type — read its `.X/.Y/.Width/.Height`).

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/ocr/`
Expected: compiles.

- [ ] **Step 3: Manual smoke (dev Mac, needs Screen Recording)**

First produce a known image, then OCR it:
```bash
cd server
cat > probe/ocr.go <<'EOF'
package main

import (
	"fmt"
	"os"

	"screenreader/internal/geom"
	"screenreader/internal/ocr"
)

func main() {
	lines, err := ocr.OCR("/tmp/sr-shot.png", []string{"en"}, "accurate", 400, 300, geom.Rect{X: 0, Y: 0, W: 400, H: 300})
	if err != nil {
		fmt.Println("err:", err)
		os.Exit(1)
	}
	fmt.Printf("lines: %d\n", len(lines))
	for _, l := range lines {
		if l.Text != "" {
			fmt.Printf("  [%.2f] %-30s %+v\n", l.Confidence, l.Text, l.Box)
		}
	}
}
EOF
CGO_ENABLED=1 go run ./probe/ocr.go
```
(Uses `/tmp/sr-shot.png` from Task 6 — re-run that first if missing.)
Expected: prints a line count and recognized text with confidence + a box. If the on-screen region has no text, 0 lines is fine — the point is it runs without error.

- [ ] **Step 4: Clean up + commit**

```bash
cd server && rm -f probe/ocr.go
git add internal/ocr && git commit -m "feat(ocr): Apple Vision OCR via tmc/apple with screen-absolute boxes"
```

---

### Task 8: `input` — robotgo mouse/keyboard/clipboard

**Files:**
- Create: `server/internal/input/input.go`

**Interfaces:**
- Consumes: `keycombo.ParseCombo`; `github.com/go-vgo/robotgo`; `time`.
- Produces:
  - `type ClickResult struct{ Executed bool; X, Y int }`
  - `func Click(x, y int, button string, clicks int, dryRun bool) ClickResult`
  - `func Move(x, y int)`
  - `func Scroll(dx, dy int)`
  - `func Drag(x1, y1, x2, y2 int, button string, dryRun bool) ClickResult`
  - `func Type(text string)`
  - `func Paste(text string)`
  - `func Key(combo string) error`
  - `func Delay(ms int)`

- [ ] **Step 1: Implement**

Create `server/internal/input/input.go`:
```go
// Package input synthesizes mouse/keyboard events via robotgo.
package input

import (
	"time"

	"github.com/go-vgo/robotgo"

	"screenreader/internal/keycombo"
)

type ClickResult struct {
	Executed bool
	X, Y     int
}

// Click moves to (x,y) and clicks `clicks` times with `button`.
// dryRun returns the target without moving or clicking.
func Click(x, y int, button string, clicks int, dryRun bool) ClickResult {
	if dryRun {
		return ClickResult{Executed: false, X: x, Y: y}
	}
	robotgo.Move(x, y)
	robotgo.MilliSleep(30)
	if clicks >= 2 {
		robotgo.Click(button, true)
	} else {
		robotgo.Click(button)
	}
	return ClickResult{Executed: true, X: x, Y: y}
}

func Move(x, y int) { robotgo.Move(x, y) }

// Scroll scrolls by (dx, dy). Verify direction/sign with `go doc github.com/go-vgo/robotgo Scroll`.
func Scroll(dx, dy int) { robotgo.Scroll(dx, dy) }

func Drag(x1, y1, x2, y2 int, button string, dryRun bool) ClickResult {
	if dryRun {
		return ClickResult{Executed: false, X: x2, Y: y2}
	}
	robotgo.Move(x1, y1)
	robotgo.MilliSleep(30)
	robotgo.MouseDown(button)
	robotgo.Move(x2, y2)
	robotgo.MilliSleep(30)
	robotgo.MouseUp(button)
	return ClickResult{Executed: true, X: x2, Y: y2}
}

func Type(text string) { robotgo.TypeStr(text) }

// Paste writes text to the clipboard and sends Cmd+V (reliable through RDP/VNC).
func Paste(text string) {
	robotgo.SetClipboard(text)
	robotgo.KeyTap("v", "command")
}

// Key presses a shortcut combo (e.g. "cmd+shift+p", "return").
func Key(combo string) error {
	key, mods, err := keycombo.ParseCombo(combo)
	if err != nil {
		return err
	}
	args := make([]string, 0, len(mods)+1)
	args = append(args, key)
	args = append(args, mods...)
	robotgo.KeyTap(args...)
	return nil
}

func Delay(ms int) {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
```

> Verify with `go doc github.com/go-vgo/robotgo`: `Click`, `Scroll`, `MouseDown`, `MouseUp`, `TypeStr`, `SetClipboard`, `KeyTap`, `MilliSleep`. Adjust `KeyTap` arg order (key first, then modifiers) if the version differs.

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/input/`
Expected: compiles.

- [ ] **Step 3: Manual smoke (dev Mac, needs Accessibility permission)**

```bash
cd server
mkdir -p probe
cat > probe/in.go <<'EOF'
package main

import (
	"fmt"

	"screenreader/internal/input"
)

func main() {
	fmt.Println("dry click:", input.Click(100, 100, "left", 1, true))
	input.Type("hello from screenreader")
	input.Delay(300)
	input.Key("return")
	fmt.Println("done")
}
EOF
CGO_ENABLED=1 go run ./probe/in.go
```
Expected: the dry click prints `Executed:false`; then "hello from screenreader<enter>" is typed into whatever app is focused (focus a text field first). If nothing is typed, grant **Accessibility** to the terminal/opencode and re-run.

- [ ] **Step 4: Clean up + commit**

```bash
cd server && rm -f probe/in.go
git add internal/input && git commit -m "feat(input): robotgo mouse/keyboard/clipboard + dry_run"
```

---

### Task 9: `tools` — reading tools (capture, ocr, click_text)

**Files:**
- Create: `server/internal/tools/tools.go`

**Interfaces:**
- Consumes: `config`, `window`, `capture`, `ocr`, `geom`; `github.com/modelcontextprotocol/go-sdk/mcp`.
- Produces: `func RegisterReading(server *mcp.Server)` and a shared helper `resolveRect(cfg *config.Config, app, title string, rect *geom.Rect) (geom.Rect, error)`.

- [ ] **Step 1: Implement**

Create `server/internal/tools/tools.go`:
```go
// Package tools defines the MCP tool surface.
package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/capture"
	"screenreader/internal/config"
	"screenreader/internal/geom"
	"screenreader/internal/input"
	"screenreader/internal/ocr"
	"screenreader/internal/window"
)

// resolveRect picks the target region: explicit rect > app/title (override or cfg) > cfg pinned rect.
func resolveRect(cfg *config.Config, app, title string, rect *geom.Rect) (geom.Rect, error) {
	if rect != nil {
		return *rect, nil
	}
	appName := app
	if appName == "" {
		appName = cfg.App
	}
	titleRe := title
	if titleRe == "" {
		titleRe = cfg.Title
	}
	if appName != "" {
		return window.Resolve(appName, titleRe)
	}
	if cfg.Mode == "rect" && cfg.Rect.W > 0 && cfg.Rect.H > 0 {
		return cfg.Rect, nil
	}
	return geom.Rect{}, fmt.Errorf("no target configured (set app or a pinned rect)")
}

func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func loadCfg() *config.Config {
	c, _ := config.Load(config.DefaultPath())
	return c
}

// tempPNG writes bytes to a temp file and returns its path (caller removes it).
func tempPNG(data []byte) (string, error) {
	f, err := os.CreateTemp("", "sr-ocr-*.png")
	if err != nil {
		return "", err
	}
	p := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return "", err
	}
	f.Close()
	return p, nil
}

// RegisterReading wires the capture, ocr, and click_text tools.
func RegisterReading(server *mcp.Server) {
	type regionArgs struct {
		App      string     `json:"app,omitempty" jsonschema:"App (process) name override"`
		Title    string     `json:"title,omitempty" jsonschema:"Window title regex override"`
		Rect     *geom.Rect `json:"rect,omitempty" jsonschema:"Pinned screen-absolute region {x,y,w,h}"`
		MaxWidth int        `json:"max_width,omitempty" jsonschema:"Downscale capture to this width in px"`
	}

	mcp.AddTool(server, &mcp.Tool{Name: "capture", Description: "Capture the target window (or region) as a PNG image, with region bounds and image size."},
		func(ctx context.Context, req *mcp.CallToolRequest, a regionArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, a.App, a.Title, a.Rect)
			if err != nil {
				return errResult(err), nil, nil
			}
			mw := a.MaxWidth
			if mw == 0 {
				mw = cfg.Capture.MaxWidth
			}
			res, err := capture.CaptureRegion(r, mw)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: res.PNG, MIMEType: "image/png"},
				&mcp.TextContent{Text: fmt.Sprintf("bounds={x:%d,y:%d,w:%d,h:%d} image=%dx%d", r.X, r.Y, r.W, r.H, res.ImgW, res.ImgH)},
			}}, nil, nil
		})

	type ocrArgs struct {
		App          string     `json:"app,omitempty"`
		Title        string     `json:"title,omitempty"`
		Rect         *geom.Rect `json:"rect,omitempty"`
		Languages    []string   `json:"languages,omitempty"`
		Level        string     `json:"level,omitempty" jsonschema:"fast|accurate"`
		IncludeBoxes bool       `json:"include_boxes"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "ocr", Description: "OCR the target window (or region) with Apple Vision; returns text and screen-absolute boxes."},
		func(ctx context.Context, req *mcp.CallToolRequest, a ocrArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, a.App, a.Title, a.Rect)
			if err != nil {
				return errResult(err), nil, nil
			}
			// OCR runs on full resolution (no downscale).
			res, err := capture.CaptureRegion(r, 0)
			if err != nil {
				return errResult(err), nil, nil
			}
			p, err := tempPNG(res.PNG)
			if err != nil {
				return errResult(err), nil, nil
			}
			defer os.Remove(p)
			langs := a.Languages
			if len(langs) == 0 {
				langs = cfg.OCR.Languages
			}
			lvl := a.Level
			if lvl == "" {
				lvl = cfg.OCR.Level
			}
			lines, err := ocr.OCR(p, langs, lvl, res.ImgW, res.ImgH, r)
			if err != nil {
				return errResult(err), nil, nil
			}
			var out string
			for i, l := range lines {
				if a.IncludeBoxes {
					out += fmt.Sprintf("%d: [%0.2f] %s (x:%d y:%d w:%d h:%d)\n", i+1, l.Confidence, l.Text, l.Box.X, l.Box.Y, l.Box.W, l.Box.H)
				} else {
					out += l.Text + "\n"
				}
			}
			if out == "" {
				out = "(no text recognized)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})

	type clickTextArgs struct {
		App        string     `json:"app,omitempty"`
		Title      string     `json:"title,omitempty"`
		Rect       *geom.Rect `json:"rect,omitempty"`
		Text       string     `json:"text" jsonschema:"Text to find and click"`
		Match      string     `json:"match,omitempty" jsonschema:"exact|contains|regex (default contains)"`
		Occurrence int        `json:"occurrence,omitempty" jsonschema:"Which match (1-based, default 1)"`
		DryRun     bool       `json:"dry_run"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "click_text", Description: "OCR-locate text in the target and click its center (screen-absolute)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a clickTextArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, a.App, a.Title, a.Rect)
			if err != nil {
				return errResult(err), nil, nil
			}
			res, err := capture.CaptureRegion(r, 0)
			if err != nil {
				return errResult(err), nil, nil
			}
			p, err := tempPNG(res.PNG)
			if err != nil {
				return errResult(err), nil, nil
			}
			defer os.Remove(p)
			langs := cfg.OCR.Languages
			lines, err := ocr.OCR(p, langs, cfg.OCR.Level, res.ImgW, res.ImgH, r)
			if err != nil {
				return errResult(err), nil, nil
			}
			occ := a.Occurrence
			if occ < 1 {
				occ = 1
			}
			found := 0
			for _, l := range lines {
				match := false
				switch a.Match {
				case "exact":
					match = l.Text == a.Text
				case "regex":
					match = func() bool { re, e := regexpCompile(a.Text); return e == nil && re.MatchString(l.Text) }()
				default: // contains
					match = containsFold(l.Text, a.Text)
				}
				if !match {
					continue
				}
				found++
				if found != occ {
					continue
				}
				cx, cy := geom.Center(l.Box)
				inputClick(cx, cy, a.DryRun)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("clicked %q at (%d,%d) box=%+v dry_run=%v", l.Text, cx, cy, l.Box, a.DryRun),
				}}}, nil, nil
			}
			return errResult(fmt.Errorf("text %q not found (match=%q)", a.Text, orDefault(a.Match, "contains"))), nil, nil
		})
}

// Shared small helpers (used by Task 9 and later tasks).
func containsFold(s, sub string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(sub)) }
func orDefault(s, d string) string { if s == "" { return d }; return s }
func regexpCompile(s string) (*regexp.Regexp, error) { return regexp.Compile(s) }
func inputClick(x, y int, dryRun bool) { input.Click(x, y, "left", 1, dryRun) }
```

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/tools/`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
cd server && git add internal/tools && git commit -m "feat(tools): capture, ocr, click_text reading tools"
```

---

### Task 10: `tools` — input tools (click/move/scroll/drag/type/paste/key/wait)

**Files:**
- Modify: `server/internal/tools/tools.go`

**Interfaces:**
- Consumes: `geom`, `input`, `config`; `mcp`.
- Produces: `func RegisterInput(server *mcp.Server)`.

- [ ] **Step 1: Add the input tool registration**

Append to `server/internal/tools/tools.go` (add `"screenreader/internal/geom"` already imported; ensure `input` imported):
```go
func RegisterInput(server *mcp.Server) {
	delay := func() int { return loadCfg().Input.DelayMs }

	type clickArgs struct {
		X      int     `json:"x"`
		Y      int     `json:"y"`
		Button string  `json:"button,omitempty" jsonschema:"left|right|middle"`
		Clicks int     `json:"clicks,omitempty" jsonschema:"1 or 2"`
		DryRun bool    `json:"dry_run"`
		Coords string  `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect `json:"rect,omitempty" jsonschema:"Region for image-relative coords"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "click", Description: "Click at screen-absolute (x,y)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a clickArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = geom.MapImageToScreen(a.X, a.Y, a.Rect.W, a.Rect.H, *a.Rect)
			}
			button := orDefault(a.Button, "left")
			clicks := a.Clicks
			if clicks < 1 {
				clicks = 1
			}
			r := input.Click(x, y, button, clicks, a.DryRun)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("click %s @(%d,%d) executed=%v", button, x, y, r.Executed),
			}}}, nil, nil
		})

	type xyArgs struct {
		X      int         `json:"x"`
		Y      int         `json:"y"`
		Coords string      `json:"coords,omitempty" jsonschema:"screen|image"`
		Rect   *geom.Rect  `json:"rect,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "move", Description: "Move the cursor to (x,y)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a xyArgs) (*mcp.CallToolResult, any, error) {
			x, y := a.X, a.Y
			if a.Coords == "image" && a.Rect != nil {
				x, y = geom.MapImageToScreen(a.X, a.Y, a.Rect.W, a.Rect.H, *a.Rect)
			}
			input.Move(x, y)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("moved to (%d,%d)", x, y)}}}, nil, nil
		})

	type scrollArgs struct {
		DX int `json:"dx"`
		DY int `json:"dy"`
		X  int `json:"x,omitempty"`
		Y  int `json:"y,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "scroll", Description: "Scroll by (dx,dy) at (x,y) if given."},
		func(ctx context.Context, req *mcp.CallToolRequest, a scrollArgs) (*mcp.CallToolResult, any, error) {
			if a.X != 0 || a.Y != 0 {
				input.Move(a.X, a.Y)
			}
			input.Scroll(a.DX, a.DY)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("scrolled %d,%d", a.DX, a.DY)}}}, nil, nil
		})

	type dragArgs struct {
		X1     int    `json:"x1"`
		Y1     int    `json:"y1"`
		X2     int    `json:"x2"`
		Y2     int    `json:"y2"`
		Button string `json:"button,omitempty"`
		DryRun bool   `json:"dry_run"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "drag", Description: "Drag from (x1,y1) to (x2,y2)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a dragArgs) (*mcp.CallToolResult, any, error) {
			button := orDefault(a.Button, "left")
			r := input.Drag(a.X1, a.Y1, a.X2, a.Y2, button, a.DryRun)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("drag to (%d,%d) executed=%v", r.X, r.Y, r.Executed)}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "type", Description: "Type text via keystrokes."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{ Text string `json:"text"` }) (*mcp.CallToolResult, any, error) {
			input.Type(a.Text)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "typed " + itoa(len(a.Text)) + " chars"}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "paste", Description: "Paste text (clipboard + Cmd+V); reliable for long text/commands."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{ Text string `json:"text"` }) (*mcp.CallToolResult, any, error) {
			input.Paste(a.Text)
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pasted"}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "key", Description: "Press a key/shortcut combo, e.g. \"cmd+v\", \"return\"."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{ Combo string `json:"combo"` }) (*mcp.CallToolResult, any, error) {
			if err := input.Key(a.Combo); err != nil {
				return errResult(err), nil, nil
			}
			input.Delay(delay())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pressed " + a.Combo}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "wait", Description: "Sleep for ms milliseconds."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{ Ms int `json:"ms"` }) (*mcp.CallToolResult, any, error) {
			input.Delay(a.Ms)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "waited " + itoa(a.Ms) + "ms"}}}, nil, nil
		})
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
```

> The generic `AddTool` handler takes a typed args struct; using an anonymous `struct{...}` is fine. If the SDK requires a named type for schema inference, rename each to a named type (e.g. `type typeArgs struct{ Text string \`json:"text"\` }`).

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/tools/`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
cd server && git add internal/tools && git commit -m "feat(tools): click/move/scroll/drag/type/paste/key/wait input tools"
```

---

### Task 11: `tools` — target tools + wait_for_text + doctor

**Files:**
- Modify: `server/internal/tools/tools.go`

**Interfaces:**
- Consumes: `config`, `window`, `capture`, `ocr`, `geom`, `input`; `mcp`.
- Produces: `func RegisterTarget(server *mcp.Server)`.

- [ ] **Step 1: Add target + wait_for_text + doctor**

Append to `server/internal/tools/tools.go`:
```go
func RegisterTarget(server *mcp.Server) {
	type setTargetArgs struct {
		App   string     `json:"app,omitempty"`
		Title string     `json:"title,omitempty"`
		Rect  *geom.Rect `json:"rect,omitempty"`
		Mode  string     `json:"mode,omitempty" jsonschema:"app|rect"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "set_target", Description: "Set + persist the default target (app/title or pinned rect)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a setTargetArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			if a.App != "" {
				cfg.App = a.App
			}
			if a.Title != "" {
				cfg.Title = a.Title
			}
			if a.Rect != nil {
				cfg.Rect = config.Rect{X: a.Rect.X, Y: a.Rect.Y, W: a.Rect.W, H: a.Rect.H}
			}
			if a.Mode != "" {
				cfg.Mode = a.Mode
			} else if a.Rect != nil {
				cfg.Mode = "rect"
			}
			if err := config.Save(config.DefaultPath(), cfg); err != nil {
				return errResult(err), nil, nil
			}
			r, err := resolveRect(cfg, "", "", nil)
			msg := fmt.Sprintf("target saved (mode=%s app=%q title=%q rect=%+v)", cfg.Mode, cfg.App, cfg.Title, cfg.Rect)
			if err == nil {
				msg += fmt.Sprintf(" resolved=%+v", r)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "focus_window", Description: "Activate the target app and raise its window; return bounds."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct {
			App   string `json:"app,omitempty"`
			Title string `json:"title,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			app := orDefault(a.App, cfg.App)
			title := orDefault(a.Title, cfg.Title)
			if app == "" {
				return errResult(fmt.Errorf("no app target configured")), nil, nil
			}
			r, err := window.Focus(app, title)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("focused %s %+v", app, r)}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "window_info", Description: "Report the resolved target (app, title, mode, bounds)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			r, err := resolveRect(cfg, "", "", nil)
			if err != nil {
				return errResult(err), nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("mode=%s app=%q title=%q bounds=%+v", cfg.Mode, cfg.App, cfg.Title, r),
			}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_windows", Description: "List visible windows (app, title, bounds) to choose a target."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			wins, err := window.List()
			if err != nil {
				return errResult(err), nil, nil
			}
			var out string
			for _, w := range wins {
				out += fmt.Sprintf("%s | %s | %+v\n", w.App, w.Title, w.Rect)
			}
			if out == "" {
				out = "(no visible windows)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})

	type waitTextArgs struct {
		Text       string `json:"text"`
		TimeoutMs  int    `json:"timeout_ms"`
		IntervalMs int    `json:"interval_ms,omitempty"`
		Match      string `json:"match,omitempty" jsonschema:"exact|contains|regex"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "wait_for_text", Description: "Poll OCR until the text appears or timeout."},
		func(ctx context.Context, req *mcp.CallToolRequest, a waitTextArgs) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			timeout := a.TimeoutMs
			if timeout <= 0 {
				timeout = 10000
			}
			interval := a.IntervalMs
			if interval <= 0 {
				interval = 500
			}
			deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
			for {
				if found, box, l := ocrFind(cfg, a.Text, a.Match); found {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
						Text: fmt.Sprintf("found %q at %+v (line %q)", a.Text, box, l),
					}}}, nil, nil
				}
				if time.Now().After(deadline) {
					return errResult(fmt.Errorf("timed out after %dms waiting for %q", timeout, a.Text)), nil, nil
				}
				input.Delay(interval)
			}
		})

	mcp.AddTool(server, &mcp.Tool{Name: "doctor", Description: "Self-check: target resolution and screen size."},
		func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, any, error) {
			cfg := loadCfg()
			sx, sy := screenSize()
			var out string
			out += fmt.Sprintf("screen: %dx%d\n", sx, sy)
			out += fmt.Sprintf("config: mode=%s app=%q title=%q rect=%+v\n", cfg.Mode, cfg.App, cfg.Title, cfg.Rect)
			if r, err := resolveRect(cfg, "", "", nil); err != nil {
				out += "target: ERROR " + err.Error() + " (try list_windows / set_target)\n"
			} else {
				out += fmt.Sprintf("target: ok %+v\n", r)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
}

// ocrFind runs OCR on the current target and searches for text; returns found, box, line.
func ocrFind(cfg *config.Config, text, match string) (bool, geom.Rect, string) {
	r, err := resolveRect(cfg, "", "", nil)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	res, err := capture.CaptureRegion(r, 0)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	p, err := tempPNG(res.PNG)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	defer os.Remove(p)
	lines, err := ocr.OCR(p, cfg.OCR.Languages, cfg.OCR.Level, res.ImgW, res.ImgH, r)
	if err != nil {
		return false, geom.Rect{}, ""
	}
	for _, l := range lines {
		ok := false
		switch match {
		case "exact":
			ok = l.Text == text
		case "regex":
			if re, e := regexpCompile(text); e == nil {
				ok = re.MatchString(l.Text)
			}
		default:
			ok = containsFold(l.Text, text)
		}
		if ok {
			return true, l.Box, l.Text
		}
	}
	return false, geom.Rect{}, ""
}

func screenSize() (int, int) {
	return robotgo.GetScreenSize()
}
```

> This task adds two imports to the top of `tools.go`: `"time"` and `"github.com/go-vgo/robotgo"`.

- [ ] **Step 2: Build**

Run: `cd server && CGO_ENABLED=1 go build ./internal/tools/`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
cd server && git add internal/tools && git commit -m "feat(tools): set_target/focus/window_info/list_windows/wait_for_text/doctor"
```

---

### Task 12: `main.go` — wire the stdio MCP server

**Files:**
- Create: `server/main.go`
- Delete: `server/probe/` (build probes are no longer needed)

**Interfaces:**
- Consumes: `tools.RegisterReading/RegisterInput/RegisterTarget`; `mcp`.
- Produces: the `screenreader` binary that serves all tools over stdio.

- [ ] **Step 1: Implement main**

Create `server/main.go`:
```go
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/tools"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "screenreader", Version: "0.1.0"}, nil)
	tools.RegisterReading(server)
	tools.RegisterInput(server)
	tools.RegisterTarget(server)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
```

> Verify the server constructor + stdio run with `go doc github.com/modelcontextprotocol/go-sdk/mcp NewServer` and `go doc github.com/modelcontextprotocol/go-sdk/mcp StdioTransport`. If the version uses `mcp.NewServer(name, version, opts)` or `server.Serve(...)`, adjust accordingly.

- [ ] **Step 2: Remove probes and build the binary**

```bash
cd server && rm -rf probe
CGO_ENABLED=1 go build -o screenreader .
ls -la screenreader
```
Expected: a `screenreader` executable is produced.

- [ ] **Step 3: Smoke — list our server's tools over stdio**

Write a tiny client that launches the built binary via `mcp.CommandTransport` and lists its tools:
```bash
cd server
mkdir -p probe
cat > probe/client.go <<'EOF'
package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	cmd := exec.Command("./screenreader")
	cs, err := c.Connect(context.Background(), &mcp.CommandTransport{Command: cmd})
	if err != nil {
		panic(err)
	}
	tl, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	for _, t := range tl.Tools {
		fmt.Println(t.Name)
	}
}
EOF
CGO_ENABLED=1 go run ./probe/client.go
```
Expected: prints the tool names (`capture ocr click_text click move scroll drag type paste key wait set_target focus_window window_info list_windows wait_for_text doctor`).

> If `mcp.CommandTransport` / `mcp.NewClient` / `cs.ListTools` names differ in the installed go-sdk version, check `go doc github.com/modelcontextprotocol/go-sdk/mcp` and adjust the client (the goal is just: spawn `./screenreader`, list tools).

- [ ] **Step 4: Clean up + commit**

```bash
cd server && rm -rf probe
git add main.go screenreader && git rm -r --cached probe 2>/dev/null; git add -A
git commit -m "feat: wire stdio MCP server exposing all screenreader tools"
```

> Add `server/screenreader` (the built binary) to `.gitignore` if you don't want to commit the binary; committing the source is what matters.

---

### Task 13: README + opencode MCP config

**Files:**
- Create: `README.md` (repo root)
- Create: `.gitignore` (repo root)

**Interfaces:**
- Produces: setup/permission/usage/safety docs + the opencode `mcp` config snippet.

- [ ] **Step 1: Write .gitignore**

Create `.gitignore`:
```
server/screenreader
*.png
.DS_Store
```

- [ ] **Step 2: Write README**

Create `README.md` with these sections (fill with the exact commands from this plan):
- **What it is:** one-paragraph purpose (see spec §1).
- **Prerequisites:** macOS, Go ≥1.22, Xcode Command Line Tools (`xcode-select --install`).
- **Build:** `cd server && CGO_ENABLED=1 go build -o screenreader .`
- **Permissions:** grant the terminal/opencode **Screen Recording** and **Accessibility** (and **Automation** for System Events) in System Settings → Privacy & Security. Explain each and why.
- **Configure the target:** either (a) edit `~/.config/screenreader/config.json`, (b) call the `set_target` tool, or (c) [Plan B] the config app. Show a sample config.
- **opencode config:** the MCP server entry (stdio) — see below.
- **Tools:** a table summarizing each tool (name, purpose, key args).
- **Coordinate model:** screen-absolute default; how to map image→screen using `capture` metadata; `ocr` boxes are screen-absolute.
- **Safety:** this drives your real cursor; use `dry_run`, pin a rect, and the risk note from spec §4.8.
- **Troubleshooting:** black capture (Screen Recording), no typing (Accessibility), `osascript:` errors (Automation), cgo build failures (Xcode CLT).

opencode MCP config snippet (for `~/.config/opencode/opencode.json` or the project `.opencode/config`):
```json
{
  "mcp": {
    "screenreader": {
      "type": "local",
      "command": ["path/to/screen_reader/server/screenreader"]
    }
  }
}
```
> Match opencode's current MCP config schema (verify against opencode docs; the key is a local command that opencode spawns over stdio).

- [ ] **Step 3: Verify config path + commit**

```bash
cd /Users/chack/syncthing/workspace/screen_reader
git add README.md .gitignore && git commit -m "docs: README with setup, permissions, usage, safety + opencode config"
```

---

## Self-Review

**Spec coverage:**
- §4.1 stack/deps/build/Xcode-CLT → Tasks 1, 12. ✅
- §4.2 window targeting (app/title/rect, re-resolve, osascript enumeration) → Tasks 5, 9 (`resolveRect`), 11. ✅
- §4.3 coordinate model (screen-absolute default, capture metadata, screen-absolute OCR boxes, `coords:"image"` escape) → Tasks 2, 6, 7, 9, 10. ✅
- §4.4 all 17 tools → Tasks 9 (capture/ocr/click_text), 10 (click/move/scroll/drag/type/paste/key/wait), 11 (set_target/focus/window_info/list_windows/wait_for_text/doctor). ✅
- §4.5 OCR (temp PNG, Vision, bbox conversion, full-res) → Tasks 2, 7, 9. ✅
- §4.6 input (type/paste/key, delay) → Task 8, 10. ✅
- §4.7 config (schema, defaults, per-action read) → Task 3, 9/11 (`loadCfg`). ✅
- §4.8 errors/safety (IsError results, dry_run) → Tasks 8, 9, 10, 13. ✅
- §4.9 permissions/onboarding (doctor) → Task 11 (doctor), 13 (README). ✅
- §6 testing (unit for pure; manual smoke for platform) → Tasks 2/3/4 (unit), 5/6/7/8 (smoke). ✅
- §7 layout → File Structure. ✅
- §8 risks (cgo, tmc/apple, typing, cursor) → Tasks 1 (cgo), 7 (tmc/apple verify), 13 (safety). ✅

**Placeholder scan:** No "TBD/TODO/implement later." Each code step has full code; uncertain exact-API names (robotgo `Capture`/`Click`/`Scroll`/`KeyTap`, tmc/apple `BoundingBox`/level constants, go-sdk `NewServer`/`AddTool`/`StdioTransport`) each carry an explicit `go doc` verification step — not placeholders.

**Type consistency:** `geom.Rect{X,Y,W,H}` used everywhere; `config.Rect` mirrors it (`set_target` copies field-by-field). `capture.Result{PNG,Bounds,ImgW,ImgH}` is produced in Task 6 and consumed consistently in Tasks 9/11. `ocr.Line{Text,Confidence,Box}` is produced in Task 7 and consumed in Tasks 9/11. `input.Click/Drag/Move/Scroll/Type/Paste/Key/Delay` signatures (Task 8) match their call sites in Tasks 9/10/11. `window.Resolve/List/Focus` (Task 5) match call sites in Tasks 9/11. Helper names `containsFold`/`orDefault`/`regexpCompile`/`inputClick`/`itoa` are all defined before use (Task 9 defines the shared helpers; Task 10/11 use them).

**Note on anonymous struct args:** the go-sdk generic `AddTool` infers the input schema from the handler's args type. If a given SDK version rejects anonymous struct types for schema inference, the Build step in each tools task will fail clearly — rename the anonymous structs to named types (the note in Task 10 covers this). This is the one place where the exact SDK behavior is version-sensitive, and it's called out at the point of use.
