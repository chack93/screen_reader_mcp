package window

import (
	"strings"
	"testing"

	"screenreader/internal/geom"
)

func cands() []Candidate {
	return []Candidate{
		{ID: 10, App: "Windows App", Title: "Session - Dev", Rect: geom.Rect{X: 0, Y: 30, W: 800, H: 600}, OnScreen: true},
		{ID: 11, App: "Windows App", Title: "Session - Prod", Rect: geom.Rect{X: 100, Y: 130, W: 900, H: 700}, OnScreen: true},
		{ID: 12, App: "Ghostty", Title: "", Rect: geom.Rect{X: 5, Y: 5, W: 400, H: 300}, OnScreen: true},
		{ID: 13, App: "Minimized App", Title: "Doc", Rect: geom.Rect{X: -1, Y: -1, W: 10, H: 10}, OnScreen: false},
	}
}

func TestMatch_emptyAppNeedsTarget(t *testing.T) {
	_, err := Match(cands(), "", "")
	if err == nil || !strings.Contains(err.Error(), "app target") {
		t.Fatalf("want an app-target hint, got %v", err)
	}
}

func TestMatch_firstWindowOfApp(t *testing.T) {
	// Empty title takes the app's first window, like Resolve.
	got, err := Match(cands(), "Windows App", "")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got.ID != 10 {
		t.Fatalf("got %+v, want id 10", got)
	}
}

func TestMatch_titleRegex(t *testing.T) {
	got, err := Match(cands(), "Windows App", "Prod")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got.ID != 11 || got.Rect != (geom.Rect{X: 100, Y: 130, W: 900, H: 700}) {
		t.Fatalf("got %+v, want id 11 with its frame", got)
	}
}

func TestMatch_appCaseInsensitive(t *testing.T) {
	// ScreenCaptureKit's owner name need not match the AppleScript process name
	// in case, and a target copied from list_windows must still resolve.
	if _, err := Match(cands(), "windows app", "Dev"); err != nil {
		t.Fatalf("case-insensitive app match failed: %v", err)
	}
}

func TestMatch_onscreenBeatsMinimized(t *testing.T) {
	list := append(cands(), Candidate{ID: 20, App: "Safari", Title: "Tab", OnScreen: false},
		Candidate{ID: 21, App: "Safari", Title: "Tab", OnScreen: true})
	got, err := Match(list, "Safari", "Tab")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got.ID != 21 {
		t.Fatalf("got id %d, want the on-screen window 21", got.ID)
	}
}

func TestMatch_minimizedStillMatches(t *testing.T) {
	// Minimized is worse (it may not capture) but it is still the window the
	// target names, so report it rather than "no window".
	got, err := Match(cands(), "Minimized App", "Doc")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got.ID != 13 {
		t.Fatalf("got %+v, want id 13", got)
	}
}

func TestMatch_literalFallbackForStoredTitle(t *testing.T) {
	// The live case that made the app's own selection unusable: the picker stored
	// the full title, whose "(EXT)" is a capture group as a regex.
	stored := "Enroll.Prod.W365.Cloud.D - König, Christian (EXT)"
	list := []Candidate{
		{ID: 7, App: "Windows App", Title: "Login", OnScreen: true},
		{ID: 8, App: "Windows App", Title: stored, OnScreen: true},
	}
	got, err := Match(list, "Windows App", stored)
	if err != nil {
		t.Fatalf("stored title must still select its window: %v", err)
	}
	if got.ID != 8 {
		t.Fatalf("got %+v, want id 8", got)
	}
}

func TestMatch_regexWinsOverLiteral(t *testing.T) {
	list := []Candidate{
		{ID: 1, App: "Windows App", Title: "Session (1) - literal twin", OnScreen: true},
		{ID: 2, App: "Windows App", Title: "Session 1 - regex hit", OnScreen: true},
	}
	got, err := Match(list, "Windows App", `Session (1)`)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got.ID != 2 {
		t.Fatalf("got id %d, want the regex match 2", got.ID)
	}
}

func TestMatch_badTitleRegex(t *testing.T) {
	_, err := Match(cands(), "Windows App", "Session (")
	if err == nil || !strings.Contains(err.Error(), "invalid title regex") {
		t.Fatalf("want a regex error, got %v", err)
	}
}

func TestMatch_noSuchTitleSuggests(t *testing.T) {
	_, err := Match(cands(), "Windows App", "Finance")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `no window for app "Windows App"`) || !strings.Contains(msg, "similar apps") {
		t.Fatalf("unexpected error text: %s", msg)
	}
	// The suggestion must not repeat the app for every window it owns.
	if strings.Count(msg, "Windows App") > 2 {
		t.Fatalf("suggestion repeats app names: %s", msg)
	}
}

func TestMatch_unknownAppListsScreen(t *testing.T) {
	_, err := Match(cands(), "Zoom", "")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "on screen:") || !strings.Contains(msg, "Ghostty") {
		t.Fatalf("want on-screen apps listed, got %s", msg)
	}
}
