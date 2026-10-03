package window

import "testing"

func TestSplitWindowLineValid(t *testing.T) {
	first, title, x, y, w, h, ok := splitWindowLine("1\tMy Window\t10\t20\t300\t200")
	if !ok {
		t.Fatal("expected ok=true for a valid 6-field line")
	}
	if first != "1" || title != "My Window" || x != 10 || y != 20 || w != 300 || h != 200 {
		t.Fatalf("got first=%q title=%q x=%d y=%d w=%d h=%d", first, title, x, y, w, h)
	}
}

func TestSplitWindowLineTabInTitle(t *testing.T) {
	first, title, x, y, w, h, ok := splitWindowLine("3\tMy\tTabbed\tWindow\t100\t-5\t800\t600")
	if !ok {
		t.Fatal("expected ok=true for a tab-in-title line")
	}
	if first != "3" || title != "My\tTabbed\tWindow" || x != 100 || y != -5 || w != 800 || h != 600 {
		t.Fatalf("got first=%q title=%q x=%d y=%d w=%d h=%d", first, title, x, y, w, h)
	}
}

func TestSplitWindowLineTooFewFields(t *testing.T) {
	if _, _, _, _, _, _, ok := splitWindowLine("1\tMy Window\t10\t20\t300"); ok {
		t.Fatal("expected ok=false for a 5-field line")
	}
	if _, _, _, _, _, _, ok := splitWindowLine("just some words without tabs"); ok {
		t.Fatal("expected ok=false for a 1-field line")
	}
}

func TestSplitWindowLineNonNumericGeometry(t *testing.T) {
	if _, _, _, _, _, _, ok := splitWindowLine("1\tMy Window\t10\t20\tabc\t200"); ok {
		t.Fatal("expected ok=false when the width is non-numeric")
	}
	if _, _, _, _, _, _, ok := splitWindowLine("1\tMy Window\t10\t20\t300\t2px"); ok {
		t.Fatal("expected ok=false when the height is non-numeric")
	}
}

func TestSplitWindowLineFirstFieldPreserved(t *testing.T) {
	// windowsOfApp emits the 1-based index first.
	first, _, _, _, _, _, ok := splitWindowLine("2\tSecond Window\t10\t20\t300\t200")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if first != "2" {
		t.Fatalf("got first=%q, want %q", first, "2")
	}
	// List emits the app name first.
	first, _, _, _, _, _, ok = splitWindowLine("Finder\tUntitled folder\t10\t20\t300\t200")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if first != "Finder" {
		t.Fatalf("got first=%q, want %q", first, "Finder")
	}
}
