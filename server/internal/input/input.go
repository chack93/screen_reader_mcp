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
	robotgo.Paste(text)
}

// Key presses a shortcut combo (e.g. "cmd+shift+p", "return").
func Key(combo string) error {
	key, mods, err := keycombo.ParseCombo(combo)
	if err != nil {
		return err
	}
	args := make([]interface{}, 0, len(mods))
	for _, m := range mods {
		args = append(args, m)
	}
	robotgo.KeyTap(key, args...)
	return nil
}

func Delay(ms int) {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
