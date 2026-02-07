package id

import (
	"testing"

	"github.com/oklog/ulid/v2"
)

func TestNew_ULIDParses(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if len(s) != 26 {
		t.Fatalf("expected 26-char ULID string, got %d (%q)", len(s), s)
	}
	if _, err := ulid.Parse(s); err != nil {
		t.Fatalf("Parse(%q) error: %v", s, err)
	}
}

func TestNew_MonotonicIncreasing(t *testing.T) {
	prev := ""
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		s, err := New()
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		if _, ok := seen[s]; ok {
			t.Fatalf("duplicate ULID generated: %q", s)
		}
		seen[s] = struct{}{}
		if prev != "" && !(s > prev) {
			t.Fatalf("expected monotonic increase: prev=%q curr=%q", prev, s)
		}
		prev = s
	}
}
