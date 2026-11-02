package window

import (
	"screenreader/internal/geom"
)

// StackWin is one on-screen window in CGWindowList order, which the API
// documents as front to back (kCGWindowListOptionOnScreenOnly). ID is the
// CGWindowID, the same number ScreenCaptureKit reports as SCWindow.windowID, so
// a captured window can be found in this list.
type StackWin struct {
	ID    int
	Owner string
	Title string
	Layer int
	Rect  geom.Rect
}

// minCoverFraction ignores windows that only nick the target's edge: a 1 px sliver
// or the menu bar grazing a full-height window would report "covered" on almost
// every capture, which trains the reader to ignore the warning.
const minCoverFraction = 0.005

// Covering returns, front to back, the windows stacked ABOVE the window with
// window id that overlap its frame, and whether that window was found in the
// stack at all. It exists because input is real cursor events at screen points:
// a click aimed at an occluded pixel lands on the covering window, not on the
// window that was captured, so a reader must know before it clicks.
func Covering(stack []StackWin, id int) ([]StackWin, bool) {
	at := -1
	for i, w := range stack {
		if w.ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, false // not on this list (minimized, other Space): no claim to make
	}
	if at == 0 {
		return nil, true // frontmost: nothing covers it
	}
	target := stack[at].Rect
	area := target.Area()
	if area <= 0 {
		return nil, true
	}
	var out []StackWin
	for _, w := range stack[:at] {
		if float64(geom.Intersect(w.Rect, target).Area())/float64(area) > minCoverFraction {
			out = append(out, w)
		}
	}
	return out, true
}
