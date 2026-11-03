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
// from the AppleScript process name, and exact equality loses to nothing; only
// when no candidate matches exactly does a second set qualify, whose names are
// equal after normApp (the app's picker stores the process name, e.g.
// "sublime_text", while ScreenCaptureKit names the app by localizedName, e.g.
// "Sublime Text"). An on-screen window wins over a minimized one of the same
// app+title (minimized windows may not capture).
func Match(cands []Candidate, app, title string) (Candidate, error) {
	if app == "" {
		return Candidate{}, fmt.Errorf("window capture needs an app target (set_target app=...)")
	}
	f, err := newTitleFilter(title)
	if err != nil {
		return Candidate{}, err
	}
	norm := normApp(app)
	var owned, bridged []Candidate
	for _, c := range cands {
		switch {
		case strings.EqualFold(c.App, app):
			owned = append(owned, c)
		case normApp(c.App) == norm:
			bridged = append(bridged, c)
		}
	}
	// Same order as selectTitle: the regex sees the whole list before the literal
	// pass is allowed to select anything. The exact set is "the whole list" the
	// regex-first rule was written for, so within each title pass it is consulted
	// before the bridged set: a stored name that an app really owns verbatim is
	// never displaced by a separator twin.
	for _, match := range []func(Candidate) bool{
		func(c Candidate) bool { return f.matchRegex(c.Title) },
		func(c Candidate) bool { return f.matchLiteral(c.Title) },
	} {
		for _, list := range [][]Candidate{owned, bridged} {
			if c, ok := pick(list, match); ok {
				return c, nil
			}
		}
	}
	return Candidate{}, fmt.Errorf("no window for app %q (title %q): %s", app, title, nearMiss(cands, app))
}

// normApp folds separator differences between the CGWindowList process name and
// ScreenCaptureKit's localizedName: lowercased, with runs of '_', '-' and ' '
// collapsed to a single space, so "sublime_text" ≡ "Sublime Text". It fuses
// separators only — word order and extra words still distinguish, which keeps
// Match conservative ("Text Sublime" and "Sublime Text 2" are not "sublime_text").
func normApp(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	sep := false
	for _, r := range strings.ToLower(name) {
		if r == '_' || r == '-' || r == ' ' {
			if !sep {
				b.WriteByte(' ')
				sep = true
			}
			continue
		}
		sep = false
		b.WriteRune(r)
	}
	return b.String()
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
// close, a few on-screen apps) to make the target fixable in one step. The
// comparison uses normApp like Match does, so the stored "sublime_text" is
// suggested back as its display name, "Sublime Text".
func nearMiss(cands []Candidate, app string) string {
	low := normApp(app)
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
		cl := normApp(c.App)
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
