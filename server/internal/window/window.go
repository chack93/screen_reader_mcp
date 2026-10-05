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

// appleQuote escapes a value for embedding in an AppleScript double-quoted string
// (AppleScript escapes an embedded double quote by doubling it).
func appleQuote(s string) string {
	return strings.ReplaceAll(s, `"`, `""`)
}

// splitWindowLine parses "<first>\t<title...>\t<x>\t<y>\t<w>\t<h>". The title may
// itself contain tabs; it is everything between the first field and the final four
// geometry fields, rejoined. ok is false if there are <6 fields or any geometry
// field is non-numeric.
func splitWindowLine(line string) (first, title string, x, y, w, h int, ok bool) {
	f := strings.Split(line, "\t")
	if len(f) < 6 {
		return "", "", 0, 0, 0, 0, false
	}
	geo := f[len(f)-4:]
	var e1, e2, e3, e4 error
	x, e1 = strconv.Atoi(geo[0])
	y, e2 = strconv.Atoi(geo[1])
	w, e3 = strconv.Atoi(geo[2])
	h, e4 = strconv.Atoi(geo[3])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return "", "", 0, 0, 0, 0, false
	}
	return f[0], strings.Join(f[1:len(f)-4], "\t"), x, y, w, h, true
}

// windowsOfApp returns "index<tab>title<tab>x<tab>y<tab>w<tab>h" lines for the
// app's windows; index is the 1-based position in the app's window list.
func windowsOfApp(app string) (string, error) {
	script := fmt.Sprintf(`
tell application "System Events"
  set proc to first process whose name is "%s"
  -- NOTE: local must not be named "lines" — AppleScript now treats "lines" as a
  -- built-in text property, and "set end of lines to X" fails with -10006.
  set out to {}
  set idx to 0
  repeat with w in (every window of proc)
    set idx to idx + 1
    set p to position of w
    set s to size of w
    set end of out to (idx & tab & (title of w) & tab & (item 1 of p) & tab & (item 2 of p) & tab & (item 1 of s) & tab & (item 2 of s))
  end repeat
  set AppleScript's text item delimiters to linefeed
  return out as text
end tell`, appleQuote(app))
	return runAppleScript(script)
}

// Resolve returns the screen-absolute rect of the app's window matching title (regex).
// Empty title returns the first window.
func Resolve(app, title string) (geom.Rect, error) {
	raw, err := windowsOfApp(app)
	if err != nil {
		return geom.Rect{}, fmt.Errorf("resolve %q: %w", app, err)
	}
	re, err := regexp.Compile(title)
	if err != nil {
		return geom.Rect{}, fmt.Errorf("invalid title regex %q: %w", title, err)
	}
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_, winTitle, x, y, w, h, okk := splitWindowLine(line)
		if !okk {
			continue
		}
		if title != "" && !re.MatchString(winTitle) {
			continue
		}
		return geom.Rect{X: x, Y: y, W: w, H: h}, nil
	}
	return geom.Rect{}, fmt.Errorf("no window for app %q (title %q)", app, title)
}

// resolveIndex returns the 1-based index (in the app's window list) of the window
// whose title matches title (regex; empty = first window).
func resolveIndex(app, title string) (int, error) {
	raw, err := windowsOfApp(app)
	if err != nil {
		return 0, err
	}
	re, err := regexp.Compile(title)
	if err != nil {
		return 0, fmt.Errorf("invalid title regex %q: %w", title, err)
	}
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_, winTitle, _, _, _, _, okk := splitWindowLine(line)
		if !okk {
			continue
		}
		if title != "" && !re.MatchString(winTitle) {
			continue
		}
		return i + 1, nil
	}
	return 0, fmt.Errorf("no window for app %q (title %q)", app, title)
}

// List returns visible windows across all apps.
func List() ([]Win, error) {
	script := `
tell application "System Events"
  -- NOTE: local must not be named "lines" — AppleScript now treats "lines" as a
  -- built-in text property, and "set end of lines to X" fails with -10006.
  set out to {}
  repeat with p in (every process whose visible is true)
    try
      repeat with w in (every window of p)
        set pn to name of p
        set pos to position of w
        set sz to size of w
        set end of out to (pn & tab & (title of w) & tab & (item 1 of pos) & tab & (item 2 of pos) & tab & (item 1 of sz) & tab & (item 2 of sz))
      end repeat
    end try
  end repeat
  set AppleScript's text item delimiters to linefeed
  return out as text
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
		appName, winTitle, x, y, w, h, okk := splitWindowLine(line)
		if !okk {
			continue
		}
		wins = append(wins, Win{App: appName, Title: winTitle, Rect: geom.Rect{X: x, Y: y, W: w, H: h}})
	}
	return wins, nil
}

// Focus activates the app and raises the matching window; returns its rect.
func Focus(app, title string) (geom.Rect, error) {
	if _, err := runAppleScript(fmt.Sprintf(`tell application "%s" to activate`, appleQuote(app))); err != nil {
		return geom.Rect{}, err
	}
	if idx, err := resolveIndex(app, title); err == nil && idx > 1 {
		runAppleScript(fmt.Sprintf(
			`tell application "System Events" to set index of window %d of (first process whose name is "%s") to 1`,
			idx, appleQuote(app)))
	}
	return Resolve(app, title)
}
