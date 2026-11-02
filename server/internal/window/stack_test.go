package window

import (
	"testing"

	"screenreader/internal/geom"
)

// A stack in CGWindowList order: front to back. The target (id 3) sits under
// Chrome and Terminal and above Finder.
func testStack() []StackWin {
	return []StackWin{
		{ID: 1, Owner: "Google Chrome", Rect: geom.Rect{X: 0, Y: 30, W: 1279, H: 1570}},
		{ID: 2, Owner: "Terminal", Rect: geom.Rect{X: 1000, Y: 300, W: 400, H: 300}},
		{ID: 3, Owner: "Windows App", Rect: geom.Rect{X: 1, Y: 240, W: 2360, H: 1360}},
		{ID: 4, Owner: "Finder", Rect: geom.Rect{X: 1400, Y: 300, W: 900, H: 400}},
	}
}

func TestCoveringReportsOnlyWindowsAbove(t *testing.T) {
	covers, found := Covering(testStack(), 3)
	if !found {
		t.Fatal("target window must be found in the stack")
	}
	var got []string
	for _, w := range covers {
		got = append(got, w.Owner)
	}
	// Finder overlaps the same area but is BELOW the target, so it covers nothing.
	if len(got) != 2 || got[0] != "Google Chrome" || got[1] != "Terminal" {
		t.Fatalf("covers = %v, want [Google Chrome Terminal] front to back", got)
	}
}

func TestCoveringFrontmostWindow(t *testing.T) {
	covers, found := Covering(testStack(), 1)
	if !found {
		t.Fatal("target window must be found in the stack")
	}
	if len(covers) != 0 {
		t.Fatalf("frontmost window reported as covered by %v", covers)
	}
}

func TestCoveringIgnoresWindowsWithoutOverlap(t *testing.T) {
	list := []StackWin{
		{ID: 5, Owner: "Off to the side", Rect: geom.Rect{X: 5000, Y: 5000, W: 200, H: 200}},
		{ID: 3, Owner: "Windows App", Rect: geom.Rect{X: 1, Y: 240, W: 2360, H: 1360}},
	}
	if covers, _ := Covering(list, 3); len(covers) != 0 {
		t.Fatalf("non-overlapping window reported as covering: %v", covers)
	}
}

// A 1px sliver over the edge is not worth a warning: reporting the menu bar or a
// 1 px gap on nearly every capture trains the reader to ignore "occluded=".
func TestCoveringIgnoresEdgeSlivers(t *testing.T) {
	list := []StackWin{
		{ID: 6, Owner: "Sliver", Rect: geom.Rect{X: 2360, Y: 300, W: 1, H: 1000}},
		{ID: 7, Owner: "Real overlap", Rect: geom.Rect{X: 100, Y: 300, W: 200, H: 200}},
		{ID: 3, Owner: "Windows App", Rect: geom.Rect{X: 1, Y: 240, W: 2360, H: 1360}},
	}
	covers, _ := Covering(list, 3)
	if len(covers) != 1 || covers[0].Owner != "Real overlap" {
		t.Fatalf("covers = %v, want only the real overlap", covers)
	}
}

func TestCoveringUnknownWindowClaimsNothing(t *testing.T) {
	// A minimized window or one on another Space is not in the on-screen list;
	// saying "not covered" would be a lie, so the caller must be told nothing.
	if _, found := Covering(testStack(), 999); found {
		t.Fatal("unknown window must report found=false")
	}
}
