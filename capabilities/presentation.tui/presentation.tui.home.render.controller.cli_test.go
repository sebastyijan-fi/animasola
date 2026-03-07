package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func TestHomeSearchEnterCreatesLocalRoomBeforeOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "home.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	user, err := store.GetOrCreateUser(ctx, "searchtester", "peer-searchtester")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	model := NewHomeModel(store, user, nil, nil, nil, nil)
	model.mode = "search"
	model.rooms = []sqlite.Room{{
		ID:        "public-test-room",
		Name:      "vibecoders",
		IsPrivate: false,
		CreatorID: "peer-owner",
	}}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	home := updated.(*HomeModel)
	if cmd == nil {
		t.Fatalf("expected open-room command")
	}

	msg := cmd()
	open, ok := msg.(OpenRoomMsg)
	if !ok {
		t.Fatalf("expected OpenRoomMsg, got %T", msg)
	}
	if open.RoomID != "public-test-room" {
		t.Fatalf("expected public-test-room, got %q", open.RoomID)
	}

	room, err := store.GetRoom(ctx, "public-test-room")
	if err != nil {
		t.Fatalf("expected local room to exist before open: %v", err)
	}
	if room.Name != "vibecoders" {
		t.Fatalf("expected vibecoders, got %q", room.Name)
	}

	joined, err := store.ListRooms(ctx, home.user.ID)
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	if len(joined) != 1 || joined[0].ID != "public-test-room" {
		t.Fatalf("expected joined room to be persisted, got %#v", joined)
	}
}
