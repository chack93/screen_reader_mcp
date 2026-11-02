package window

import (
	"strings"
	"testing"
)

// The app's settings picker stores the real window title, and real titles are
// full of regex punctuation: this stored title compiles as a pattern that
// requires "Christian EXT", so only the literal pass can name the window back.
func TestSelectTitle_literalFallbackForStoredTitle(t *testing.T) {
	stored := "Enroll.Prod.W365.Cloud.D - König, Christian (EXT)"
	titles := []string{"Login", stored}
	f, err := newTitleFilter(stored)
	if err != nil {
		t.Fatalf("stored title must compile: %v", err)
	}
	if f.matchRegex(stored) {
		t.Fatal("expected the regex form NOT to match, so the fallback is what is being tested")
	}
	if got := f.selectTitle(titles); got != 1 {
		t.Fatalf("selectTitle = %d, want 1 (the stored title itself)", got)
	}
}

func TestSelectTitle_regexWinsOverLiteral(t *testing.T) {
	// "Session (1)" matches "Session 1" as a regex and "Session (1)" as text. The
	// regex gets the whole list first, so it must not be preempted by the literal
	// twin that happens to sit earlier in the app's window list.
	f, err := newTitleFilter(`Session (1)`)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	titles := []string{"Session (1) - literal twin", "Session 1 - regex hit"}
	if got := f.selectTitle(titles); got != 1 {
		t.Fatalf("selectTitle = %d, want 1 (regex pass runs over the whole list first)", got)
	}
}

func TestSelectTitle_emptyTitleTakesFirst(t *testing.T) {
	f, err := newTitleFilter("")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if got := f.selectTitle([]string{"Any window", "Another"}); got != 0 {
		t.Fatalf("selectTitle = %d, want 0", got)
	}
	if got := f.selectTitle(nil); got != -1 {
		t.Fatalf("selectTitle with no windows = %d, want -1", got)
	}
}

func TestSelectTitle_noMatchAtAll(t *testing.T) {
	f, err := newTitleFilter("Finance")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if got := f.selectTitle([]string{"Inbox • All Accounts", "Draft"}); got != -1 {
		t.Fatalf("selectTitle = %d, want -1", got)
	}
}

func TestNewTitleFilter_invalidRegexStillErrors(t *testing.T) {
	// A pattern that does not compile is a caller mistake, not a literal title, so
	// it must stay an error instead of silently degrading to a text match.
	if _, err := newTitleFilter("Session ("); err == nil || !strings.Contains(err.Error(), "invalid title regex") {
		t.Fatalf("want an invalid-regex error, got %v", err)
	}
}
