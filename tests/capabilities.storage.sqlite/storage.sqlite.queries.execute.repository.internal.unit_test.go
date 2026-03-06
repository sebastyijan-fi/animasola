package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func TestSQLiteDataBoundaries(t *testing.T) {
	username := "test_agent_beta_" + time.Now().Format("150405")
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".config", "animasola", username)
	os.MkdirAll(configDir, 0700)
	dbPath := filepath.Join(configDir, "animasola.db")

	defer os.RemoveAll(configDir)

	// 1. Boot Database
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite: %v", err)
	}
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate SQLite schema: %v", err)
	}

	// 2. Test User Insertion (Truncation)
	user, err := store.GetOrCreateUser(ctx, username)
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
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
