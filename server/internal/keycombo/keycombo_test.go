package keycombo

import (
	"reflect"
	"testing"
)

func TestParseCombo(t *testing.T) {
	cases := []struct {
		in      string
		key     string
		mods    []string
		wantErr bool
	}{
		{"cmd+v", "v", []string{"cmd"}, false},
		{"cmd+shift+p", "p", []string{"cmd", "shift"}, false},
		{"return", "return", nil, false},
		{"control+a", "a", []string{"ctrl"}, false},
		{"option+tab", "tab", []string{"opt"}, false},
		{"alt+f4", "f4", []string{"opt"}, false},
		{"", "", nil, true},
		{"bogus+x", "x", nil, true},
	}
	for _, c := range cases {
		key, mods, err := ParseCombo(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q: wanted error, got key=%q mods=%v", c.in, key, mods)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, err)
		}
		if key != c.key || !reflect.DeepEqual(mods, c.mods) {
			t.Fatalf("%q: got key=%q mods=%v want key=%q mods=%v", c.in, key, mods, c.key, c.mods)
		}
	}
}
