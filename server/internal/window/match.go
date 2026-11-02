package window

import (
	"fmt"
	"strings"

	"screenreader/internal/geom"
)

// Candidate is one window as ScreenCaptureKit sees it: an app+title target plus
// enough identity to capture that single window. Typed here (cgo-free, so it is
// testable in the fast loop) the way CGWin is for the CoreGraphics path.
type Candidate struct {
	ID       uint32
	App      string
	Title    string
	Rect     geom.Rect
	OnScreen bool
}

// Match picks the candidate an app/title target refers to, with Resolve's
// semantics: title is a regex with a literal fallback (see titleFilter), and an
// empty title takes the app's first window. The app name is compared
// case-insensitively because ScreenCaptureKit's owner name can differ in case
// from the AppleScript process name, and an on-screen window wins over a
// minimized one of the same app+title (minimized windows may not capture).
func Match(cands []Candidate, app, title string) (Candidate, error) {
	if app == "" {
		return Candidate{}, fmt.Errorf("window capture needs an app target (set_target app=...)")
	}
	f, err := newTitleFilter(title)
	if err != nil {
		return Candidate{}, err
	}
	var owned []Candidate
	for _, c := range cands {
		if strings.EqualFold(c.App, app) {
			owned = append(owned, c)
		}
	}
	// Same order as selectTitle: the regex sees the whole list before the literal
	// pass is allowed to select anything.
	if c, ok := pick(owned, func(c Candidate) bool { return f.matchRegex(c.Title) }); ok {
		return c, nil
	}
	if c, ok := pick(owned, func(c Candidate) bool { return f.matchLiteral(c.Title) }); ok {
		return c, nil
	}
	return Candidate{}, fmt.Errorf("no window for app %q (title %q): %s", app, title, nearMiss(cands, app))
}

// pick returns the first candidate the predicate selects, preferring an on-screen
// one: a minimized window is still the window the target names, but a visible
// twin is the better answer because a minimized one may not be shareable.
func pick(list []Candidate, match func(Candidate) bool) (Candidate, bool) {
	var minimized *Candidate
	for i := range list {
		c := list[i]
		if !match(c) {
			continue
		}
		if c.OnScreen {
			return c, true
		}
		if minimized == nil {
			saved := c
			minimized = &saved
		}
	}
	if minimized != nil {
		return *minimized, true
	}
	return Candidate{}, false
}

// nearMiss explains a failed match: ScreenCaptureKit sees no window for that
// app+title, so list what it does see near that name (or, if nothing is even
// close, a few on-screen apps) to make the target fixable in one step.
func nearMiss(cands []Candidate, app string) string {
	low := strings.ToLower(app)
	var similar, any []string
	add := func(list []string, name string) []string {
		if len(list) >= 3 || contains(list, name) {
			return list
		}
		return append(list, name)
	}
	for _, c := range cands {
		if !c.OnScreen {
			continue
		}
		any = add(any, c.App)
		cl := strings.ToLower(c.App)
		if strings.Contains(cl, low) || strings.Contains(low, cl) {
			similar = add(similar, c.App)
		}
	}
	label, list := "similar apps:", similar
	if len(similar) == 0 {
		label, list = "on screen:", any
	}
	if len(list) == 0 {
		return "no on-screen windows to suggest"
	}
	return label + " " + strings.Join(list, ", ")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
