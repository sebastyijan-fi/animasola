package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func TestSQLiteDataBoundaries(t *testing.T) {
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "animasola.db")

	// 1. Boot Database
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite: %v", err)
	}
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate SQLite schema: %v", err)
	}

	// Create mock user
	username := "testuser"
	explicitID := "test_explicit_id"
	user, err := store.GetOrCreateUser(ctx, username, explicitID)
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// 3. Test Hostile Network Payload Truncation (Room Creation)
	// A malicious Peer sends a 1000 character Room Description
	hostileDescription := strings.Repeat("A", 1000)
	hostileName := strings.Repeat("B", 100)

	room, err := store.CreateRoom(ctx, hostileName, hostileDescription, user.ID, false, "")
	if err != nil {
		t.Fatalf("Failed to create hostile room: %v", err)
	}

	// Assert SQLite properly truncated it at the repository edge
	if len(room.Name) > 64 {
		t.Fatalf("Room Name failed truncation gate. Saved length: %d", len(room.Name))
	}
	if len(room.Description) > 256 {
		t.Fatalf("Room Description failed truncation gate. Saved length: %d", len(room.Description))
	}

	// 4. Test Concurrency Writes
	// Emulate 50 rapid chat messages hitting the WAL queue
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		go func(idx int) {
			e := store.EnsureRemoteUserExists(ctx, "remote_peer_id", "RemoteAlias")
			errs <- e
		}(i)
	}

	for i := 0; i < 50; i++ {
		err := <-errs
		if err != nil && !strings.Contains(err.Error(), "UNIQUE constraint failed") {
			// UNIQUE constraints are expected if 50 threads try to insert "remote_peer" at the identical microsecond.
			// What we are looking for are "database is locked" Panics.
			if strings.Contains(err.Error(), "locked") {
				t.Fatalf("SQLite WAL queue failed under threading stress: %v", err)
			}
		}
	}
}

func TestPublicRoomMetadataVersioning(t *testing.T) {
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "animasola.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite: %v", err)
	}
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate SQLite schema: %v", err)
	}

	owner, err := store.GetOrCreateUser(ctx, "owner", "owner_peer")
	if err != nil {
		t.Fatalf("failed to create owner: %v", err)
	}

	room, err := store.CreateRoom(ctx, "alpha", "first", owner.ID, false, "")
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	if room.Version != 1 {
		t.Fatalf("expected initial version 1, got %d", room.Version)
	}

	updated, err := store.UpdatePublicRoomMetadata(ctx, room.ID, owner.ID, "beta", "second", "sig-v2")
	if err != nil {
		t.Fatalf("failed to update room metadata: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected bumped version 2, got %d", updated.Version)
	}

	stale := &sqlite.Room{
		ID:          room.ID,
		Name:        "stale",
		Description: "stale-desc",
		CreatorID:   owner.ID,
		Signature:   "sig-v1",
		CreatedAt:   room.CreatedAt,
		UpdatedAt:   room.CreatedAt,
		LastSeenAt:  updated.LastSeenAt,
		Version:     1,
	}
	if err := store.UpsertPublicRoomIndex(ctx, stale); err != nil {
		t.Fatalf("failed to upsert stale room index entry: %v", err)
	}

	rooms, err := store.SearchAllRooms(ctx)
	if err != nil {
		t.Fatalf("failed to search rooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected exactly one indexed public room, got %d", len(rooms))
	}
	if rooms[0].Name != "beta" {
		t.Fatalf("expected newer metadata to win, got name %q", rooms[0].Name)
	}
	if rooms[0].Version != 2 {
		t.Fatalf("expected newer version 2 to survive, got %d", rooms[0].Version)
	}
}

func TestMigrateLegacyRoomsSchemaBeforeLastSeenAt(t *testing.T) {
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "legacy.db")

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	defer db.Close()

	legacySchema := `
	CREATE TABLE users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL
	);
	CREATE TABLE rooms (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		description TEXT,
		created_at TEXT NOT NULL
	);
	CREATE TABLE memberships (
		user_id TEXT NOT NULL,
		room_id TEXT NOT NULL,
		role TEXT NOT NULL,
		joined_at TEXT NOT NULL,
		PRIMARY KEY (user_id, room_id)
	);
	CREATE TABLE messages (
		id TEXT PRIMARY KEY,
		room_id TEXT NOT NULL,
		author_id TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL
	);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO rooms (id, name, description, created_at) VALUES ('room1', 'legacy', 'desc', '2026-03-07T00:00:00Z')`); err != nil {
		t.Fatalf("seed legacy room: %v", err)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}

	room, err := store.GetRoom(context.Background(), "room1")
	if err != nil {
		t.Fatalf("load migrated room: %v", err)
	}
	if room.Name != "legacy" {
		t.Fatalf("expected legacy room to survive migration, got %q", room.Name)
	}
}
