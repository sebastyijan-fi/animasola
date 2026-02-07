package tui

import "testing"

func TestParseCommand(t *testing.T) {
	if ParseCommand("") != nil {
		t.Fatalf("expected nil")
	}
	if ParseCommand("hello") != nil {
		t.Fatalf("expected nil")
	}
	c := ParseCommand("/join rust")
	if c == nil || c.Name != "join" || len(c.Args) != 1 || c.Args[0] != "rust" {
		t.Fatalf("unexpected parse: %#v", c)
	}
	c = ParseCommand("  /create-community   ")
	if c == nil || c.Name != "create-community" {
		t.Fatalf("unexpected parse: %#v", c)
	}
}
