// Package keycombo parses key shortcut strings (pure Go, no cgo).
package keycombo

import (
	"errors"
	"strings"
)

var aliases = map[string]string{
	"cmd": "cmd", "command": "cmd",
	"ctrl": "ctrl", "control": "ctrl",
	"shift": "shift",
	"opt":   "opt", "option": "opt", "alt": "opt",
}

// ParseCombo splits a "+"-separated shortcut into its key (last token) and
// normalized modifiers. Unknown leading tokens are an error.
func ParseCombo(combo string) (key string, mods []string, err error) {
	combo = strings.TrimSpace(combo)
	if combo == "" {
		return "", nil, errors.New("empty key combo")
	}
	parts := strings.Split(combo, "+")
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(parts[i]))
	}
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "", nil, errors.New("missing key in combo")
	}
	for _, m := range parts[:len(parts)-1] {
		n, ok := aliases[m]
		if !ok {
			return "", nil, errors.New("unknown modifier: " + m)
		}
		mods = append(mods, n)
	}
	return parts[len(parts)-1], mods, nil
}
