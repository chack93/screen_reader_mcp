package window

import (
	"fmt"
	"strconv"
	"strings"

	"screenreader/internal/geom"
)

// ProcInfo is one process row from the batched System Events index query.
// Name is the AppleScript process name — the same identifier Resolve/Focus use,
// so picker output round-trips through target resolution regardless of what
// CoreGraphics calls the owner.
type ProcInfo struct {
	Name       string
	PID        int
	Background bool
}

// CGWin is one on-screen window record from CGWindowListCopyWindowInfo
// (produced by the cgo colist package; typed here to keep this package
// cgo-free and testable).
type CGWin struct {
	PID   int
	Title string
	Rect  geom.Rect
}

// ParseProcLine parses one "name|pid|background" line from the batched process
// index script. Splits right-to-left (PID second-to-last, flag last) so a "|"
// inside a process name cannot corrupt the fields — same lesson as
// splitWindowLine and tabs in window titles.
func ParseProcLine(line string) (ProcInfo, bool) {
	i := strings.LastIndex(line, "|")
	if i <= 0 {
		return ProcInfo{}, false
	}
	name, rest := line[:i], line[i+1:]
	flag, ok := parseBool(rest)
	if !ok {
		return ProcInfo{}, false
	}
	j := strings.LastIndex(name, "|")
	if j < 1 { // j == 0 would mean an empty process name ("|pid|flag")
		return ProcInfo{}, false
	}
	pid, err := strconv.Atoi(name[j+1:])
	if err != nil {
		return ProcInfo{}, false
	}
	return ProcInfo{Name: name[:j], PID: pid, Background: flag}, true
}

func parseBool(s string) (bool, bool) {
	switch s {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// procIndexScript batches three whole-list property fetches into fast Apple
// events (no per-window properties, which is what makes window.List() slow:
// System Events round-trips every window of every process, ~0.5-2s per app).
// Numbers are cast "as text" (macOS 27 returns a list for "number & text").
const procIndexScript = `
tell application "System Events"
  set N to name of every process
  set P to unix id of every process
  set B to background only of every process
  set o to {}
  repeat with i from 1 to count N
    set end of o to ((item i of N) & "|" & ((item i of P) as text) & "|" & ((item i of B) as text))
  end repeat
  set AppleScript's text item delimiters to linefeed
  return o as text
end tell`

// ProcessIndex returns a name+background flag for every process, resolved by
// PID, in one batched AppleScript round trip (~1s).
func ProcessIndex() ([]ProcInfo, error) {
	raw, err := runAppleScript(procIndexScript)
	if err != nil {
		return nil, fmt.Errorf("process index: %w", err)
	}
	var out []ProcInfo
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if p, ok := ParseProcLine(line); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// MergeWindows joins CGWindowList records with the process index: keeps
// windows owned by non-background processes, labels them with the AppleScript
// process name, and drops the rest. Output keeps the CGWindowList z-order
// (frontmost first).
func MergeWindows(raws []CGWin, procs []ProcInfo) []Win {
	byPID := make(map[int]ProcInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	var out []Win
	for _, r := range raws {
		p, ok := byPID[r.PID]
		if !ok || p.Background {
			continue
		}
		out = append(out, Win{App: p.Name, Title: r.Title, Rect: r.Rect})
	}
	return out
}
