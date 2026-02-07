package store

import (
	"context"
	"database/sql"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	_ "modernc.org/sqlite"
)

func TestMigrate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate #1: %v", err)
	}
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate #2: %v", err)
	}

	// Spot check a couple of tables exist.
	ok, err := st.tableExists("users")
	if err != nil {
		t.Fatalf("tableExists(users): %v", err)
	}
	if !ok {
		t.Fatalf("expected users table to exist")
	}
	ok, err = st.tableExists("messages")
	if err != nil {
		t.Fatalf("tableExists(messages): %v", err)
	}
	if !ok {
		t.Fatalf("expected messages table to exist")
	}
}

func TestMigrate_IncompatibleSchemaFailsFast(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "old.db")

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatalf("create old users table: %v", err)
	}
	_ = db.Close()

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	err = st.Migrate(filepath.Join("..", "..", "migrations"))
	if err == nil {
		t.Fatalf("expected migrate error for incompatible schema")
	}
}

func TestCreateUser_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rt.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u, err := st.CreateUser(ctx, "seba", "SHA256:fp", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := st.GetUserByFingerprint(ctx, "SHA256:fp")
	if err != nil {
		t.Fatalf("GetUserByFingerprint: %v", err)
	}
	if got == nil {
		t.Fatalf("expected user")
	}
	if got.ID != u.ID || got.Username != "seba" {
		t.Fatalf("unexpected user: %#v", got)
	}
}

func TestTriggers_MemberCount(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trg1.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}

	c, _, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if c.MemberCount != 1 {
		t.Fatalf("expected member_count=1 after creator membership, got %d", c.MemberCount)
	}

	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}
	c2, err := st.GetCommunityByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityByID: %v", err)
	}
	if c2.MemberCount != 2 {
		t.Fatalf("expected member_count=2 after join, got %d", c2.MemberCount)
	}

	if err := st.LeaveCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("LeaveCommunity: %v", err)
	}
	c3, err := st.GetCommunityByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityByID: %v", err)
	}
	if c3.MemberCount != 1 {
		t.Fatalf("expected member_count=1 after leave, got %d", c3.MemberCount)
	}
}

func TestListCommunityMembers_ReturnsSortedUsernames(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "members.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	c, _, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	members, err := st.ListCommunityMembers(ctx, c.ID, 10)
	if err != nil {
		t.Fatalf("ListCommunityMembers: %v", err)
	}
	if strings.Join(members, ",") != "kai,seba" {
		t.Fatalf("unexpected members: %#v", members)
	}
}

func TestTriggers_UpvotesAndRepliesAndFTS(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trg2.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	c, r, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	top, err := st.CreateMessage(ctx, r.ID, u1.ID, "tokio io_uring deep dive", nil)
	if err != nil {
		t.Fatalf("CreateMessage top: %v", err)
	}

	// FTS should have indexed the content.
	row := st.db.QueryRowContext(ctx, `SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'tokio'`)
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("fts count scan: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected fts match count=1, got %d", n)
	}

	// Reply nesting should bump reply_count on ancestors.
	r1, err := st.CreateMessage(ctx, r.ID, u2.ID, "nice", &top.ID)
	if err != nil {
		t.Fatalf("CreateMessage reply1: %v", err)
	}
	_, err = st.CreateMessage(ctx, r.ID, u1.ID, "thanks", &r1.ID)
	if err != nil {
		t.Fatalf("CreateMessage reply2: %v", err)
	}
	mtop, err := st.GetMessageByID(ctx, top.ID)
	if err != nil {
		t.Fatalf("GetMessageByID top: %v", err)
	}
	if mtop.Replies != 2 {
		t.Fatalf("expected top.reply_count=2, got %d", mtop.Replies)
	}

	// Upvote toggle should bump upvote_count via triggers.
	state, err := st.ToggleUpvote(ctx, u2.ID, top.ID)
	if err != nil {
		t.Fatalf("ToggleUpvote: %v", err)
	}
	if !state.Upvoted || state.Count != 1 {
		t.Fatalf("expected upvoted=true count=1, got %+v", *state)
	}
	state, err = st.ToggleUpvote(ctx, u2.ID, top.ID)
	if err != nil {
		t.Fatalf("ToggleUpvote (2): %v", err)
	}
	if state.Upvoted || state.Count != 0 {
		t.Fatalf("expected upvoted=false count=0, got %+v", *state)
	}
}

func TestGetUserProfile_ComputesStatsAndTopPosts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "profile.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	c, r, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	post1, err := st.CreateMessage(ctx, r.ID, u1.ID, "post1", nil)
	if err != nil {
		t.Fatalf("CreateMessage post1: %v", err)
	}
	post2, err := st.CreateMessage(ctx, r.ID, u1.ID, "post2", nil)
	if err != nil {
		t.Fatalf("CreateMessage post2: %v", err)
	}
	_, err = st.CreateMessage(ctx, r.ID, u1.ID, "reply", &post1.ID)
	if err != nil {
		t.Fatalf("CreateMessage reply: %v", err)
	}
	if _, err := st.ToggleUpvote(ctx, u2.ID, post2.ID); err != nil {
		t.Fatalf("ToggleUpvote: %v", err)
	}

	p, err := st.GetUserProfile(ctx, "seba")
	if err != nil {
		t.Fatalf("GetUserProfile: %v", err)
	}
	if p == nil {
		t.Fatalf("expected profile")
	}
	if p.UserID != u1.ID {
		t.Fatalf("expected profile user id %q, got %q", u1.ID, p.UserID)
	}
	if p.Posts != 2 || p.Replies != 1 {
		t.Fatalf("expected posts=2 replies=1, got posts=%d replies=%d", p.Posts, p.Replies)
	}
	if p.UpvotesRec != 1 {
		t.Fatalf("expected upvotes received=1, got %d", p.UpvotesRec)
	}
	found := false
	for _, n := range p.ActiveIn {
		if n == "rust" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ActiveIn to contain rust, got %#v", p.ActiveIn)
	}
	if len(p.TopPosts) != 2 {
		t.Fatalf("expected 2 top posts, got %d", len(p.TopPosts))
	}
	if p.TopPosts[0].ID != post2.ID {
		t.Fatalf("expected top post to be post2")
	}
}

func TestPinUnpinAndGetPinnedMessage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pins.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	c, r, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	_ = c

	msg, err := st.CreateMessage(ctx, r.ID, u1.ID, "hello", nil)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if err := st.PinMessage(ctx, r.ID, msg.ID); err != nil {
		t.Fatalf("PinMessage: %v", err)
	}
	pinned, err := st.GetPinnedMessage(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetPinnedMessage: %v", err)
	}
	if pinned == nil || pinned.ID != msg.ID {
		t.Fatalf("expected pinned message %q, got %#v", msg.ID, pinned)
	}

	if err := st.UnpinRoom(ctx, r.ID); err != nil {
		t.Fatalf("UnpinRoom: %v", err)
	}
	pinned2, err := st.GetPinnedMessage(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetPinnedMessage #2: %v", err)
	}
	if pinned2 != nil {
		t.Fatalf("expected nil pinned after unpin, got %#v", pinned2)
	}
}

func TestDeleteRoomAndCommunityCascade(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "del.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	c, r, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}
	msg, err := st.CreateMessage(ctx, r.ID, u2.ID, "hi", nil)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Delete room should cascade message.
	if err := st.DeleteRoom(ctx, r.ID); err != nil {
		t.Fatalf("DeleteRoom: %v", err)
	}
	got, err := st.GetMessageByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	if got != nil {
		t.Fatalf("expected message deleted by cascade")
	}

	// Delete community should remove community row.
	if err := st.DeleteCommunity(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCommunity: %v", err)
	}
	c2, err := st.GetCommunityByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCommunityByID: %v", err)
	}
	if c2 != nil {
		t.Fatalf("expected community deleted")
	}
}

func TestListMentions_ScopedToMembership(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mentions.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	c1, r1, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity c1: %v", err)
	}
	_, r2, err := st.CreateCommunity(ctx, u1.ID, "linux", "Penguins")
	if err != nil {
		t.Fatalf("CreateCommunity c2: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c1.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	_, err = st.CreateMessage(ctx, r1.ID, u1.ID, "hi @kai", nil)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	_, err = st.CreateMessage(ctx, r2.ID, u1.ID, "hi @kai from linux", nil)
	if err != nil {
		t.Fatalf("CreateMessage2: %v", err)
	}

	ms, err := st.ListMentions(ctx, u2.ID, "kai", 50)
	if err != nil {
		t.Fatalf("ListMentions: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("expected 1 mention scoped to rust membership, got %d", len(ms))
	}
	if ms[0].CommunityName != "rust" {
		t.Fatalf("expected rust mention, got %#v", ms[0])
	}
}

func TestCreateCommunity_CreatesGeneralRoom(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "comm.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, room, err := st.CreateCommunity(ctx, u.ID, "linux", "Penguins unite")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if room == nil || room.Name != "general" {
		t.Fatalf("expected default room name=general, got %#v", room)
	}

	got, err := st.GetRoomByName(ctx, room.CommunityID, "general")
	if err != nil {
		t.Fatalf("GetRoomByName: %v", err)
	}
	if got == nil {
		t.Fatalf("expected general room to exist")
	}
}

func TestSoftDeleteMessage_PreservesRow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "del.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, room, err := st.CreateCommunity(ctx, u.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	m, err := st.CreateMessage(ctx, room.ID, u.ID, "hello", nil)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if err := st.SoftDeleteMessage(ctx, m.ID, u.ID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	got, err := st.GetMessageByID(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMessageByID: %v", err)
	}
	if got == nil || !got.IsDeleted || got.Content != "" {
		t.Fatalf("unexpected deleted message: %#v", got)
	}
}

func TestListThread_IncludesDescendantsSortedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "thread.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, room, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}

	root, err := st.CreateMessage(ctx, room.ID, u1.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateMessage root: %v", err)
	}
	r1, err := st.CreateMessage(ctx, room.ID, u2.ID, "r1", &root.ID)
	if err != nil {
		t.Fatalf("CreateMessage r1: %v", err)
	}
	_, err = st.CreateMessage(ctx, room.ID, u1.ID, "r2", &r1.ID)
	if err != nil {
		t.Fatalf("CreateMessage r2: %v", err)
	}

	items, err := st.ListThread(ctx, root.ID)
	if err != nil {
		t.Fatalf("ListThread: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 thread items, got %d", len(items))
	}
	if items[0].ID != root.ID || items[1].ID != r1.ID {
		t.Fatalf("unexpected order: %q %q %q", items[0].ID, items[1].ID, items[2].ID)
	}
}

func TestHomeFeed_NewAndTopAndHot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "home.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}

	c, room, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	entropy := ulid.Monotonic(rand.New(rand.NewSource(1)), 0)

	// Insert three top-level messages at different times.
	idOld := ulid.MustNew(ulid.Timestamp(now.Add(-10*time.Hour)), entropy).String()
	idMid := ulid.MustNew(ulid.Timestamp(now.Add(-2*time.Hour)), entropy).String()
	idNew := ulid.MustNew(ulid.Timestamp(now.Add(-30*time.Minute)), entropy).String()

	createdOld := now.Add(-10 * time.Hour).Format(time.RFC3339Nano)
	createdMid := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	createdNew := now.Add(-30 * time.Minute).Format(time.RFC3339Nano)

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at)
		VALUES (?, ?, ?, ?, NULL, 0, 0, 0, ?)
	`, idOld, room.ID, u1.ID, "old", createdOld); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at)
		VALUES (?, ?, ?, ?, NULL, 0, 0, 0, ?)
	`, idMid, room.ID, u1.ID, "mid", createdMid); err != nil {
		t.Fatalf("insert mid: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at)
		VALUES (?, ?, ?, ?, NULL, 0, 0, 0, ?)
	`, idNew, room.ID, u1.ID, "new", createdNew); err != nil {
		t.Fatalf("insert new: %v", err)
	}

	// Add upvotes so that "mid" is top, "new" is second, "old" is low.
	if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, u2.ID, idMid, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("upvote mid: %v", err)
	}
	// extra upvote from u1 on a different message isn't allowed by app logic, but DB allows.
	// Use it here strictly to validate ordering by denormalized count/triggers.
	if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, u1.ID, idMid, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("upvote mid 2: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, u2.ID, idNew, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("upvote new: %v", err)
	}

	// New: latest first.
	newItems, err := st.ListHomeFeed(ctx, u2.ID, SortNew, TopAll, 10, 0)
	if err != nil {
		t.Fatalf("ListHomeFeed new: %v", err)
	}
	if len(newItems) < 3 {
		t.Fatalf("expected 3 items, got %d", len(newItems))
	}
	if newItems[0].ID != idNew || newItems[1].ID != idMid || newItems[2].ID != idOld {
		t.Fatalf("unexpected new order: %q %q %q", newItems[0].ID, newItems[1].ID, newItems[2].ID)
	}

	// Top: by upvotes desc.
	topItems, err := st.ListHomeFeed(ctx, u2.ID, SortTop, TopAll, 10, 0)
	if err != nil {
		t.Fatalf("ListHomeFeed top: %v", err)
	}
	if topItems[0].ID != idMid || topItems[1].ID != idNew {
		t.Fatalf("unexpected top order: %q %q %q", topItems[0].ID, topItems[1].ID, topItems[2].ID)
	}

	// Hot: "new" should beat "mid" if mid is much older unless upvotes dominate; with our numbers it should still be competitive.
	// With our timestamps/upvotes: new should slightly beat mid, and old should be last.
	hotItems, err := st.listHomeHot(ctx, u2.ID, now, 10, 0)
	if err != nil {
		t.Fatalf("listHomeHot: %v", err)
	}
	if len(hotItems) < 3 {
		t.Fatalf("expected 3 hot items, got %d", len(hotItems))
	}
	if hotItems[0].ID != idNew || hotItems[1].ID != idMid || hotItems[2].ID != idOld {
		t.Fatalf("unexpected hot order: %q %q %q", hotItems[0].ID, hotItems[1].ID, hotItems[2].ID)
	}
}

func TestReadPositions_UnreadCountsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "readpos.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	c, room, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	m1, err := st.CreateMessage(ctx, room.ID, u1.ID, "m1", nil)
	if err != nil {
		t.Fatalf("CreateMessage m1: %v", err)
	}
	m2, err := st.CreateMessage(ctx, room.ID, u1.ID, "m2", nil)
	if err != nil {
		t.Fatalf("CreateMessage m2: %v", err)
	}

	// No read position: unread should be 2 in this community.
	unreads, err := st.UnreadCountsByCommunity(ctx, u2.ID)
	if err != nil {
		t.Fatalf("UnreadCountsByCommunity: %v", err)
	}
	if len(unreads) != 1 || unreads[0].CommunityID != c.ID || unreads[0].Count != 2 {
		t.Fatalf("unexpected unreads: %#v", unreads)
	}

	// Mark m1 as read: unread should be 1 (m2).
	if err := st.UpsertReadPosition(ctx, u2.ID, room.ID, m1.ID); err != nil {
		t.Fatalf("UpsertReadPosition: %v", err)
	}
	unreads, err = st.UnreadCountsByCommunity(ctx, u2.ID)
	if err != nil {
		t.Fatalf("UnreadCountsByCommunity: %v", err)
	}
	if len(unreads) != 1 || unreads[0].Count != 1 {
		t.Fatalf("expected unread=1 after marking m1, got %#v", unreads)
	}

	// Mark m2 as read: unread should be 0 (no row returned).
	if err := st.UpsertReadPosition(ctx, u2.ID, room.ID, m2.ID); err != nil {
		t.Fatalf("UpsertReadPosition: %v", err)
	}
	unreads, err = st.UnreadCountsByCommunity(ctx, u2.ID)
	if err != nil {
		t.Fatalf("UnreadCountsByCommunity: %v", err)
	}
	if len(unreads) != 0 {
		t.Fatalf("expected no unread rows, got %#v", unreads)
	}

	// Persist across reopening.
	_ = st.Close()
	st2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open #2: %v", err)
	}
	defer st2.Close()
	if err := st2.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate #2: %v", err)
	}
	unreads, err = st2.UnreadCountsByCommunity(ctx, u2.ID)
	if err != nil {
		t.Fatalf("UnreadCountsByCommunity #2: %v", err)
	}
	if len(unreads) != 0 {
		t.Fatalf("expected no unread rows after reopen, got %#v", unreads)
	}
}

func TestRoomFeed_NewAndTopCursorPagination(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "roomfeed.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	u3, err := st.CreateUser(ctx, "mari", "fp3", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u3: %v", err)
	}
	u4, err := st.CreateUser(ctx, "dev42", "fp4", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u4: %v", err)
	}
	_, room, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	entropy := ulid.Monotonic(rand.New(rand.NewSource(2)), 0)

	idA := ulid.MustNew(ulid.Timestamp(now.Add(-10*time.Hour)), entropy).String()
	idB := ulid.MustNew(ulid.Timestamp(now.Add(-2*time.Hour)), entropy).String()
	idC := ulid.MustNew(ulid.Timestamp(now.Add(-30*time.Minute)), entropy).String()
	idD := ulid.MustNew(ulid.Timestamp(now.Add(-5*time.Minute)), entropy).String()

	for _, it := range []struct {
		id      string
		content string
		created time.Time
	}{
		{id: idA, content: "a", created: now.Add(-10 * time.Hour)},
		{id: idB, content: "b", created: now.Add(-2 * time.Hour)},
		{id: idC, content: "c", created: now.Add(-30 * time.Minute)},
		{id: idD, content: "d", created: now.Add(-5 * time.Minute)},
	} {
		if _, err := st.db.ExecContext(ctx, `
			INSERT INTO messages (id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at)
			VALUES (?, ?, ?, ?, NULL, 0, 0, 0, ?)
		`, it.id, room.ID, u1.ID, it.content, it.created.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", it.id, err)
		}
	}

	// Upvotes: B=3, C=2, D=1, A=0.
	for _, uid := range []string{u1.ID, u2.ID, u3.ID} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, uid, idB, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("upvote B: %v", err)
		}
	}
	for _, uid := range []string{u1.ID, u2.ID} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, uid, idC, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("upvote C: %v", err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, u4.ID, idD, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("upvote D: %v", err)
	}

	// New: newest IDs first, cursor by id.
	page1, nextNew, err := st.ListRoomTopLevelNewPage(ctx, room.ID, 2, nil)
	if err != nil {
		t.Fatalf("ListRoomTopLevelNewPage: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != idD || page1[1].ID != idC {
		t.Fatalf("unexpected new page1: %#v", page1)
	}
	page2, nextNew2, err := st.ListRoomTopLevelNewPage(ctx, room.ID, 2, nextNew)
	if err != nil {
		t.Fatalf("ListRoomTopLevelNewPage #2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != idB || page2[1].ID != idA {
		t.Fatalf("unexpected new page2: %#v", page2)
	}
	if nextNew2 == nil {
		t.Fatalf("expected next cursor on page2")
	}
	page3, nextNew3, err := st.ListRoomTopLevelNewPage(ctx, room.ID, 2, nextNew2)
	if err != nil {
		t.Fatalf("ListRoomTopLevelNewPage #3: %v", err)
	}
	if len(page3) != 0 || nextNew3 != nil {
		t.Fatalf("expected empty page3, got %#v next=%v", page3, nextNew3)
	}

	// Top: by upvotes desc, cursor by (upvotes, id).
	top1, nextTop, err := st.ListRoomTopLevelTopPage(ctx, room.ID, TopAll, 2, nil)
	if err != nil {
		t.Fatalf("ListRoomTopLevelTopPage: %v", err)
	}
	if len(top1) != 2 || top1[0].ID != idB || top1[1].ID != idC {
		t.Fatalf("unexpected top page1: %#v", top1)
	}
	top2, nextTop2, err := st.ListRoomTopLevelTopPage(ctx, room.ID, TopAll, 2, nextTop)
	if err != nil {
		t.Fatalf("ListRoomTopLevelTopPage #2: %v", err)
	}
	if len(top2) != 2 || top2[0].ID != idD || top2[1].ID != idA {
		t.Fatalf("unexpected top page2: %#v", top2)
	}
	if nextTop2 == nil {
		t.Fatalf("expected next cursor on top page2")
	}
	top3, nextTop3, err := st.ListRoomTopLevelTopPage(ctx, room.ID, TopAll, 2, nextTop2)
	if err != nil {
		t.Fatalf("ListRoomTopLevelTopPage #3: %v", err)
	}
	if len(top3) != 0 || nextTop3 != nil {
		t.Fatalf("expected empty top page3, got %#v next=%v", top3, nextTop3)
	}
}

func TestHomeFeed_NewAndTopCursorPagination(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "homefeed.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	u3, err := st.CreateUser(ctx, "mari", "fp3", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u3: %v", err)
	}
	u4, err := st.CreateUser(ctx, "dev42", "fp4", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u4: %v", err)
	}

	c, room, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c.ID); err != nil {
		t.Fatalf("JoinCommunity: %v", err)
	}

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	entropy := ulid.Monotonic(rand.New(rand.NewSource(3)), 0)

	idA := ulid.MustNew(ulid.Timestamp(now.Add(-10*time.Hour)), entropy).String()
	idB := ulid.MustNew(ulid.Timestamp(now.Add(-2*time.Hour)), entropy).String()
	idC := ulid.MustNew(ulid.Timestamp(now.Add(-30*time.Minute)), entropy).String()
	idD := ulid.MustNew(ulid.Timestamp(now.Add(-5*time.Minute)), entropy).String()

	for _, it := range []struct {
		id      string
		content string
		created time.Time
	}{
		{id: idA, content: "a", created: now.Add(-10 * time.Hour)},
		{id: idB, content: "b", created: now.Add(-2 * time.Hour)},
		{id: idC, content: "c", created: now.Add(-30 * time.Minute)},
		{id: idD, content: "d", created: now.Add(-5 * time.Minute)},
	} {
		if _, err := st.db.ExecContext(ctx, `
			INSERT INTO messages (id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at)
			VALUES (?, ?, ?, ?, NULL, 0, 0, 0, ?)
		`, it.id, room.ID, u1.ID, it.content, it.created.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", it.id, err)
		}
	}

	// Upvotes: B=3, C=2, D=1, A=0.
	for _, uid := range []string{u1.ID, u2.ID, u3.ID} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, uid, idB, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("upvote B: %v", err)
		}
	}
	for _, uid := range []string{u1.ID, u2.ID} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, uid, idC, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("upvote C: %v", err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO upvotes (user_id, message_id, created_at) VALUES (?, ?, ?)`, u4.ID, idD, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("upvote D: %v", err)
	}

	// New pagination.
	n1, nextNew, err := st.ListHomeNewPage(ctx, u2.ID, 2, nil)
	if err != nil {
		t.Fatalf("ListHomeNewPage: %v", err)
	}
	if len(n1) != 2 || n1[0].ID != idD || n1[1].ID != idC {
		t.Fatalf("unexpected home new page1: %#v", n1)
	}
	n2, nextNew2, err := st.ListHomeNewPage(ctx, u2.ID, 2, nextNew)
	if err != nil {
		t.Fatalf("ListHomeNewPage #2: %v", err)
	}
	if len(n2) != 2 || n2[0].ID != idB || n2[1].ID != idA {
		t.Fatalf("unexpected home new page2: %#v", n2)
	}
	if nextNew2 == nil {
		t.Fatalf("expected next cursor on home new page2")
	}
	n3, nextNew3, err := st.ListHomeNewPage(ctx, u2.ID, 2, nextNew2)
	if err != nil {
		t.Fatalf("ListHomeNewPage #3: %v", err)
	}
	if len(n3) != 0 || nextNew3 != nil {
		t.Fatalf("expected empty home new page3, got %#v next=%v", n3, nextNew3)
	}

	// Top pagination.
	t1, nextTop, err := st.ListHomeTopPage(ctx, u2.ID, TopAll, 2, nil)
	if err != nil {
		t.Fatalf("ListHomeTopPage: %v", err)
	}
	if len(t1) != 2 || t1[0].ID != idB || t1[1].ID != idC {
		t.Fatalf("unexpected home top page1: %#v", t1)
	}
	t2, nextTop2, err := st.ListHomeTopPage(ctx, u2.ID, TopAll, 2, nextTop)
	if err != nil {
		t.Fatalf("ListHomeTopPage #2: %v", err)
	}
	if len(t2) != 2 || t2[0].ID != idD || t2[1].ID != idA {
		t.Fatalf("unexpected home top page2: %#v", t2)
	}
	if nextTop2 == nil {
		t.Fatalf("expected next cursor on home top page2")
	}
	t3, nextTop3, err := st.ListHomeTopPage(ctx, u2.ID, TopAll, 2, nextTop2)
	if err != nil {
		t.Fatalf("ListHomeTopPage #3: %v", err)
	}
	if len(t3) != 0 || nextTop3 != nil {
		t.Fatalf("expected empty home top page3, got %#v next=%v", t3, nextTop3)
	}
}

func TestSearchMessages_ScopedToMembershipAndHighlights(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}

	c1, r1, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity c1: %v", err)
	}
	_, r2, err := st.CreateCommunity(ctx, u1.ID, "linux", "Penguins unite")
	if err != nil {
		t.Fatalf("CreateCommunity c2: %v", err)
	}
	if err := st.JoinCommunity(ctx, u2.ID, c1.ID); err != nil {
		t.Fatalf("JoinCommunity c1: %v", err)
	}
	// u2 is not a member of c2.

	_, err = st.CreateMessage(ctx, r1.ID, u1.ID, "tokio io_uring deep dive", nil)
	if err != nil {
		t.Fatalf("CreateMessage r1: %v", err)
	}
	_, err = st.CreateMessage(ctx, r2.ID, u1.ID, "tokio io_uring is also cool here", nil)
	if err != nil {
		t.Fatalf("CreateMessage r2: %v", err)
	}

	results, err := st.SearchMessages(ctx, u2.ID, "tokio", 50)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 scoped result, got %d: %#v", len(results), results)
	}
	if results[0].CommunityID != c1.ID {
		t.Fatalf("expected result in community %s, got %s", c1.ID, results[0].CommunityID)
	}
	if !strings.Contains(results[0].HighlightedContent, "<hl>") || !strings.Contains(results[0].HighlightedContent, "</hl>") {
		t.Fatalf("expected highlight markers in %q", results[0].HighlightedContent)
	}
}

func TestResolveThreadRootID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "root.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	u1, err := st.CreateUser(ctx, "seba", "fp1", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "kai", "fp2", "ssh-ed25519 AAAA...")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	_, room, err := st.CreateCommunity(ctx, u1.ID, "rust", "All things Rust")
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}

	root, err := st.CreateMessage(ctx, room.ID, u1.ID, "root", nil)
	if err != nil {
		t.Fatalf("CreateMessage root: %v", err)
	}
	reply, err := st.CreateMessage(ctx, room.ID, u2.ID, "reply", &root.ID)
	if err != nil {
		t.Fatalf("CreateMessage reply: %v", err)
	}

	got, err := st.ResolveThreadRootID(ctx, reply.ID)
	if err != nil {
		t.Fatalf("ResolveThreadRootID: %v", err)
	}
	if got != root.ID {
		t.Fatalf("expected %q, got %q", root.ID, got)
	}
}
