// Package perms probes the macOS permissions screenreader needs.
package perms

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CGDirectDisplay.h>
*/
import "C"

import "os/exec"

// Accessibility reports whether the process is trusted to post synthetic
// keyboard/mouse events.
func Accessibility() bool {
	return C.AXIsProcessTrusted() != 0
}

// ScreenRecording reports whether screen capture is available (preflight; does
// not trigger a prompt).
func ScreenRecording() bool {
	return bool(C.CGPreflightScreenCaptureAccess())
}

// Automation probes whether Apple events to System Events are allowed (needed by
// list_windows/focus_window/bounds). The first call may trigger the macOS prompt.
func Automation() bool {
	_, err := exec.Command("osascript", "-e", `tell application "System Events" to get name`).Output()
	return err == nil
}
