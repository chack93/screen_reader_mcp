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
end tell
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
