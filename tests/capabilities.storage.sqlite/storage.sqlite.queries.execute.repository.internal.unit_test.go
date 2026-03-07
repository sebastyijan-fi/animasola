package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlite "github.com/sebastyijan/animasola/capabilities/storage.sqlite"
)

func openTestStore(t *testing.T) (*sqlite.Store, context.Context) {
	t.Helper()

	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "animasola.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate sqlite schema: %v", err)
	}

	return store, ctx
}

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

func TestResetOnIncompatibleSchemaCreatesCleanDatabase(t *testing.T) {
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "broken.db")

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open incompatible sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE rooms (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL
		);
		INSERT INTO rooms (id, name) VALUES ('room1', 'legacy-room');
	`); err != nil {
		t.Fatalf("seed incompatible schema: %v", err)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen broken store: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate incompatible schema: %v", err)
	}

	if _, err := store.GetRoom(context.Background(), "room1"); err == nil {
		t.Fatalf("expected incompatible legacy room data to be absent from the live database")
	}

	liveDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open migrated sqlite: %v", err)
	}
	defer liveDB.Close()

	var roomCount int
	if err := liveDB.QueryRow(`SELECT COUNT(*) FROM rooms`).Scan(&roomCount); err != nil {
		t.Fatalf("count rooms in live database: %v", err)
	}
	if roomCount != 0 {
		t.Fatalf("expected clean live database after reset, found %d room(s)", roomCount)
	}

	backups, err := filepath.Glob(dbPath + ".reset-*.bak")
	if err != nil {
		t.Fatalf("glob reset backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected exactly one archived incompatible database, found %d", len(backups))
	}
}

func TestCreateMessageTruncatesOversizedContent(t *testing.T) {
	store, ctx := openTestStore(t)

	user, err := store.GetOrCreateUser(ctx, "writer", "writer_peer")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	room, err := store.CreateRoom(ctx, "truncate-room", "", user.ID, false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	msg, err := store.CreateMessage(ctx, room.ID, user.ID, strings.Repeat("x", sqlite.MaxMessageContentRunes()+250))
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if got := len([]rune(msg.Content)); got != sqlite.MaxMessageContentRunes() {
		t.Fatalf("expected truncated content length %d, got %d", sqlite.MaxMessageContentRunes(), got)
	}
}

func TestPrivateRoomMessageCountIsCapped(t *testing.T) {
	store, ctx := openTestStore(t)

	user, err := store.GetOrCreateUser(ctx, "owner", "owner_peer")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	room, err := store.CreateRoom(ctx, "private-cap-room", "", user.ID, true, "roomkey")
	if err != nil {
		t.Fatalf("create private room: %v", err)
	}

	for i := 0; i < 5050; i++ {
		if _, err := store.CreateMessage(ctx, room.ID, user.ID, "m"); err != nil {
			t.Fatalf("create message %d: %v", i+1, err)
		}
	}

	msgs, err := store.ListMessages(ctx, room.ID, 6000)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	count := len(msgs)
	if count != 5000 {
		t.Fatalf("expected private room message cap of 5000, got %d", count)
	}
}

func TestPublicRoomMetadataUpdateCooldownAllowsOneImmediateEditThenBlocksBurst(t *testing.T) {
	store, ctx := openTestStore(t)

	user, err := store.GetOrCreateUser(ctx, "owner", "owner_peer")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	room, err := store.CreateRoom(ctx, "cooldown-room", "", user.ID, false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	first, err := store.UpdatePublicRoomMetadata(ctx, room.ID, user.ID, "cooldown-room-1", "desc1", "sig1")
	if err != nil {
		t.Fatalf("expected first immediate update to succeed, got %v", err)
	}
	if first.Version != 2 {
		t.Fatalf("expected version 2 after first update, got %d", first.Version)
	}

	_, err = store.UpdatePublicRoomMetadata(ctx, room.ID, user.ID, "cooldown-room-2", "desc2", "sig2")
	if err == nil {
		t.Fatalf("expected second rapid update to be blocked by cooldown")
	}
	if !strings.Contains(err.Error(), sqlite.PublicRoomMetadataUpdateCooldown().String()) {
		t.Fatalf("expected cooldown error, got %v", err)
	}
}

func TestDuplicateDisplayNamesDoNotConflictAndMessageSnapshotsAreStable(t *testing.T) {
	store, ctx := openTestStore(t)

	if err := store.EnsureRemoteUserExists(ctx, "peer-a", "shared"); err != nil {
		t.Fatalf("ensure peer-a: %v", err)
	}
	if err := store.EnsureRemoteUserExists(ctx, "peer-b", "shared"); err != nil {
		t.Fatalf("ensure peer-b: %v", err)
	}

	owner, err := store.GetOrCreateUser(ctx, "owner", "owner_peer")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	room, err := store.CreateRoom(ctx, "snapshot-room", "", owner.ID, false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if err := store.SyncMessage(ctx, &sqlite.Message{ID: "m1", RoomID: room.ID, AuthorID: "peer-a", AuthorUsername: "shared", Content: "one", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("sync first message: %v", err)
	}
	if err := store.SyncMessage(ctx, &sqlite.Message{ID: "m2", RoomID: room.ID, AuthorID: "peer-b", AuthorUsername: "shared", Content: "two", CreatedAt: time.Now().UTC().Add(time.Second)}); err != nil {
		t.Fatalf("sync second message: %v", err)
	}
	if err := store.EnsureRemoteUserExists(ctx, "peer-a", "renamed"); err != nil {
		t.Fatalf("rename peer-a: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	msgs, err := store.ListMessages(ctx, room.ID, 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].AuthorUsername != "shared" || msgs[1].AuthorUsername != "shared" {
		t.Fatalf("expected message snapshots to remain 'shared', got %q and %q", msgs[0].AuthorUsername, msgs[1].AuthorUsername)
	}
}

func TestSearchAllRoomsRespectsLocalPolicies(t *testing.T) {
	store, ctx := openTestStore(t)

	ownerA, err := store.GetOrCreateUser(ctx, "owner-a", "owner-a")
	if err != nil {
		t.Fatalf("create ownerA: %v", err)
	}
	ownerB, err := store.GetOrCreateUser(ctx, "owner-b", "owner-b")
	if err != nil {
		t.Fatalf("create ownerB: %v", err)
	}
	ownerC, err := store.GetOrCreateUser(ctx, "owner-c", "owner-c")
	if err != nil {
		t.Fatalf("create ownerC: %v", err)
	}

	trustedRoom, err := store.CreateRoom(ctx, "trusted-room", "", ownerA.ID, false, "")
	if err != nil {
		t.Fatalf("create trusted room: %v", err)
	}
	hiddenRoom, err := store.CreateRoom(ctx, "hidden-room", "", ownerB.ID, false, "")
	if err != nil {
		t.Fatalf("create hidden room: %v", err)
	}
	blockedRoom, err := store.CreateRoom(ctx, "blocked-room", "", ownerC.ID, false, "")
	if err != nil {
		t.Fatalf("create blocked room: %v", err)
	}

	if err := store.SetRoomTrusted(ctx, trustedRoom.ID, true); err != nil {
		t.Fatalf("trust room: %v", err)
	}
	if err := store.SetRoomHidden(ctx, hiddenRoom.ID, true); err != nil {
		t.Fatalf("hide room: %v", err)
	}
	if err := store.SetPeerBlocked(ctx, ownerC.ID, true); err != nil {
		t.Fatalf("block creator: %v", err)
	}

	rooms, err := store.SearchAllRooms(ctx)
	if err != nil {
		t.Fatalf("search rooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected only one visible public room, got %d", len(rooms))
	}
	if rooms[0].ID != trustedRoom.ID {
		t.Fatalf("expected trusted room to remain visible first, got %q", rooms[0].ID)
	}
	if !rooms[0].IsTrusted {
		t.Fatalf("expected trusted flag to be populated on visible room")
	}
	if rooms[0].ID == blockedRoom.ID {
		t.Fatalf("blocked creator room should not be visible in search")
	}
}

func TestListJoinedPublicRoomsRespectsLocalPolicies(t *testing.T) {
	store, ctx := openTestStore(t)

	viewer, err := store.GetOrCreateUser(ctx, "viewer", "viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	ownerA, err := store.GetOrCreateUser(ctx, "owner-a", "owner-a")
	if err != nil {
		t.Fatalf("create ownerA: %v", err)
	}
	ownerB, err := store.GetOrCreateUser(ctx, "owner-b", "owner-b")
	if err != nil {
		t.Fatalf("create ownerB: %v", err)
	}
	ownerC, err := store.GetOrCreateUser(ctx, "owner-c", "owner-c")
	if err != nil {
		t.Fatalf("create ownerC: %v", err)
	}

	trustedRoom, err := store.CreateRoom(ctx, "trusted-room", "", ownerA.ID, false, "")
	if err != nil {
		t.Fatalf("create trusted room: %v", err)
	}
	hiddenRoom, err := store.CreateRoom(ctx, "hidden-room", "", ownerB.ID, false, "")
	if err != nil {
		t.Fatalf("create hidden room: %v", err)
	}
	blockedRoom, err := store.CreateRoom(ctx, "blocked-room", "", ownerC.ID, false, "")
	if err != nil {
		t.Fatalf("create blocked room: %v", err)
	}

	for _, roomID := range []string{trustedRoom.ID, hiddenRoom.ID, blockedRoom.ID} {
		if err := store.JoinRoom(ctx, viewer.ID, roomID); err != nil {
			t.Fatalf("join room %s: %v", roomID, err)
		}
	}

	if err := store.SetRoomTrusted(ctx, trustedRoom.ID, true); err != nil {
		t.Fatalf("trust room: %v", err)
	}
	if err := store.SetRoomHidden(ctx, hiddenRoom.ID, true); err != nil {
		t.Fatalf("hide room: %v", err)
	}
	if err := store.SetPeerBlocked(ctx, ownerC.ID, true); err != nil {
		t.Fatalf("block creator: %v", err)
	}

	rooms, err := store.ListJoinedPublicRooms(ctx, viewer.ID)
	if err != nil {
		t.Fatalf("list joined public rooms: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected only one joined public room eligible for rebroadcast, got %d", len(rooms))
	}
	if rooms[0].ID != trustedRoom.ID {
		t.Fatalf("expected trusted room to remain rebroadcastable, got %q", rooms[0].ID)
	}
	if !rooms[0].IsTrusted {
		t.Fatalf("expected trusted room flag to be populated")
	}
}

func TestListMessagesRespectsMutedAndBlockedPeers(t *testing.T) {
	store, ctx := openTestStore(t)

	owner, err := store.GetOrCreateUser(ctx, "owner", "owner")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	visibleAuthor, err := store.GetOrCreateUser(ctx, "visible", "visible")
	if err != nil {
		t.Fatalf("create visible author: %v", err)
	}
	mutedAuthor, err := store.GetOrCreateUser(ctx, "muted", "muted")
	if err != nil {
		t.Fatalf("create muted author: %v", err)
	}
	blockedAuthor, err := store.GetOrCreateUser(ctx, "blocked", "blocked")
	if err != nil {
		t.Fatalf("create blocked author: %v", err)
	}

	room, err := store.CreateRoom(ctx, "room", "", owner.ID, false, "")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	if _, err := store.CreateMessage(ctx, room.ID, visibleAuthor.ID, "visible"); err != nil {
		t.Fatalf("create visible message: %v", err)
	}
	if _, err := store.CreateMessage(ctx, room.ID, mutedAuthor.ID, "muted"); err != nil {
		t.Fatalf("create muted message: %v", err)
	}
	if _, err := store.CreateMessage(ctx, room.ID, blockedAuthor.ID, "blocked"); err != nil {
		t.Fatalf("create blocked message: %v", err)
	}

	if err := store.SetPeerMuted(ctx, mutedAuthor.ID, true); err != nil {
		t.Fatalf("mute author: %v", err)
	}
	if err := store.SetPeerBlocked(ctx, blockedAuthor.ID, true); err != nil {
		t.Fatalf("block author: %v", err)
	}

	msgs, err := store.ListMessages(ctx, room.ID, 100)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected only one visible message after mute/block filters, got %d", len(msgs))
	}
	if msgs[0].AuthorID != visibleAuthor.ID {
		t.Fatalf("expected only visible author message to remain, got %q", msgs[0].AuthorID)
	}
	if msgs[0].Content != "visible" {
		t.Fatalf("expected visible message content to remain, got %q", msgs[0].Content)
	}
}
