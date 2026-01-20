package switcher

import "testing"

func TestIsManualSwitchType(t *testing.T) {
	cases := map[string]bool{
		"select":     true,
		"avoid":      true,
		"auto":       false,
		"avoid_auto": false,
		"":           false,
	}
	for typ, expected := range cases {
		if got := isManualSwitchType(typ); got != expected {
			t.Fatalf("type=%q got=%v expected=%v", typ, got, expected)
		}
	}
}
