package window

import (
	"testing"

	"screenreader/internal/geom"
)

func TestParseProcLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		name string
		pid  int
		bg   bool
	}{
		{"Finder|2152|false", true, "Finder", 2152, false},
		{"talagentd|314|true", true, "talagentd", 314, true},
		// "|" inside the name must not corrupt the fields (right-to-left split).
		{"odd|name|42|false", true, "odd|name", 42, false},
		{"no pid|flag", false, "", 0, false},
		{"Finder|abc|false", false, "", 0, false},
		{"Finder|2152|maybe", false, "", 0, false},
		{"", false, "", 0, false},
		{"|2152|false", false, "", 0, false},
	} {
		got, ok := ParseProcLine(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseProcLine(%q) ok=%v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Name != tc.name || got.PID != tc.pid || got.Background != tc.bg {
			t.Errorf("ParseProcLine(%q) = %+v, want name=%q pid=%d bg=%v", tc.in, got, tc.name, tc.pid, tc.bg)
		}
	}
}

func TestMergeWindows(t *testing.T) {
	procs := []ProcInfo{
		{Name: "Music", PID: 10, Background: false},
		{Name: "agentd", PID: 20, Background: true},
	}
	raws := []CGWin{
		{PID: 10, Title: "Music", Rect: geom.Rect{X: -1260, Y: 470, W: 1260, H: 791}},
		{PID: 20, Title: "agent panel", Rect: geom.Rect{X: 0, Y: 0, W: 10, H: 10}}, // background → dropped
		{PID: 99, Title: "ghost", Rect: geom.Rect{X: 0, Y: 0, W: 10, H: 10}},       // not in index → dropped
		{PID: 10, Title: "", Rect: geom.Rect{X: 1, Y: 2, W: 3, H: 4}},              // untitled kept
	}
	got := MergeWindows(raws, procs)
	if len(got) != 2 {
		t.Fatalf("MergeWindows len=%d want 2 (%+v)", len(got), got)
	}
	if got[0].App != "Music" || got[0].Title != "Music" || got[0].Rect.X != -1260 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].App != "Music" || got[1].Title != "" {
		t.Errorf("got[1] = %+v", got[1])
	}
}
