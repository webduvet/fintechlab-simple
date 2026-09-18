package money

import "testing"

func TestParseFormat(t *testing.T) {
	v, err := Parse("25.00")
	if err != nil || v != 2500 {
		t.Fatalf("got %d %v", v, err)
	}
	if Format(2500) != "25.00" {
		t.Fatalf("format %s", Format(2500))
	}
	if Format(-50) != "-0.50" {
		t.Fatalf("neg %s", Format(-50))
	}
}
