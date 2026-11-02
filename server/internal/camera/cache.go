// Package camera grabs a single still frame from a camera device (built-in
// webcam or any connected external/continuity camera) through AVFoundation.
// One cached capture session lives behind one mutex and is torn down 30 s
// after the last frame, so the green camera light is on only while frames are
// being read — mirrors internal/sck's cached-filter discipline.
package camera

import "time"

const (
	idleTTL = 30 * time.Second       // cached session lifetime after the last frame
	settle  = 500 * time.Millisecond // post-first-frame wait for auto-exposure
)

// expired reports whether the cached session is older than ttl. ONE predicate
// for the in-Capture check and the idle-teardown timer, so they cannot drift.
// At exactly ttl it is still warm.
func expired(lastUsed, now time.Time, ttl time.Duration) bool {
	return now.Sub(lastUsed) > ttl
}
