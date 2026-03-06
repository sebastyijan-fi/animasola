package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDataPruningRetention(t *testing.T) {
	username := "test_agent_gamma_" + time.Now().Format("150405")
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "animasola.db")

	// Boot
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite: %v", err)
	}
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate SQLite schema: %v", err)
	}

	explicitID := "test_explicit_id"
	user, _ := store.GetOrCreateUser(ctx, username, explicitID)

	// Create a Public Room and a Private Room
	pubRoom, _ := store.CreateRoom(ctx, "Public Decay", "Should Delete", user.ID, false, "")
	privRoom, _ := store.CreateRoom(ctx, "Private Vault", "Should Keep", user.ID, true, "secret")

	// Create messages for both
	pubMsg, _ := store.CreateMessage(ctx, pubRoom.ID, user.ID, "Ephemeral")
	privMsg, _ := store.CreateMessage(ctx, privRoom.ID, user.ID, "Permanent")

	// Mathematically simulate the passage of 8 Days by hacking the DB directly (since we are in package sqlite)
	eightDaysAgo := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(SortableTimeFormat)

	_, err = store.db.ExecContext(ctx, "UPDATE messages SET created_at = ? WHERE id = ?", eightDaysAgo, pubMsg.ID)
	if err != nil {
		t.Fatalf("Failed to forge timestamp: %v", err)
	}
	_, err = store.db.ExecContext(ctx, "UPDATE messages SET created_at = ? WHERE id = ?", eightDaysAgo, privMsg.ID)
	if err != nil {
		t.Fatalf("Failed to forge timestamp: %v", err)
	}

	// Trigger the pruning logic (retention is 7 days)
	store.pruneOldMessages(7 * 24 * time.Hour)

	// Verify Public Message was deleted
	msgs, _ := store.ListMessages(ctx, pubRoom.ID, 100)
	if len(msgs) != 0 {
		t.Fatalf("Public message was NOT pruned! Count: %d", len(msgs))
	}

	// Verify Private Message was kept
	privMsgs, _ := store.ListMessages(ctx, privRoom.ID, 100)
	if len(privMsgs) != 1 {
		t.Fatalf("Private message was wrongfully pruned! Count: %d", len(privMsgs))
	}
}
