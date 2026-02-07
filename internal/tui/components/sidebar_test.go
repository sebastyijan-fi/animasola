package components

import (
	"strings"
	"testing"
)

func TestSidebar_TruncatesAndUnreadDot(t *testing.T) {
	m := SidebarModel{
		Items: []SidebarItem{
			{ID: "1", Name: "this-is-a-very-long-community-name", Unread: 3},
			{ID: "2", Name: "rust", Unread: 0},
		},
		Selected: 0,
		Focused:  true,
	}
	out := m.View(10)
	if !strings.Contains(out, "COMMUNITIES") {
		t.Fatalf("expected header")
	}
	if !strings.Contains(out, "●") {
		t.Fatalf("expected unread dot")
	}
	if !strings.Contains(out, "(3)") {
		t.Fatalf("expected unread count")
	}
	if !strings.Contains(out, "…") {
		t.Fatalf("expected truncation ellipsis")
	}
}
