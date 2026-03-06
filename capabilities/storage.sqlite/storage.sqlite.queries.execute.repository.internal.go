package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SortableTimeFormat zero-pads fractional seconds to 9 digits to guarantee stable string lengths,
// preventing SQLite's lexicographical sorting from erroneously evaluating "Z" as greater than ".".
const SortableTimeFormat = "2006-01-02T15:04:05.000000000Z"

// StartDataPruning runs a background goroutine that deletes messages older than the retention period.
func (s *Store) StartDataPruning(ctx context.Context, retention time.Duration) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		s.pruneOldMessages(retention) // Run immediately on startup

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pruneOldMessages(retention)
			}
		}
	}()
}

func (s *Store) pruneOldMessages(retention time.Duration) {
	cutoff := time.Now().UTC().Add(-retention).Format(SortableTimeFormat)

	// Ensure we only keep recent messages for Public Rooms. Private Rooms (is_private=1) are kept forever.
	res, err := s.db.Exec(`
		DELETE FROM messages 
		WHERE id IN (
			SELECT m.id FROM messages m
			JOIN rooms r ON r.id = m.room_id
			WHERE m.created_at < ? AND r.is_private = 0
		)
	`, cutoff)
	if err != nil {
		fmt.Printf("Warning: failed to prune old messages: %s\n", err)
		return
	}

	if rows, err := res.RowsAffected(); err == nil && rows > 0 {
		fmt.Printf("[Store] Pruned %d old messages from Public Rooms (Retention: %v)\n", rows, retention)

		// If we deleted a significant amount of data, actively shrink the database file
		if rows > 1000 {
			fmt.Printf("[Store] Highwater mark reached. Executing VACUUM to compact database SSD footprint...\n")
			if _, vacErr := s.db.Exec(`VACUUM`); vacErr != nil {
				fmt.Printf("Warning: VACUUM failed: %s\n", vacErr)
			} else {
				fmt.Printf("[Store] SQLite VACUUM Complete: Empty freelist bytes fully reclaimed by OS.\n")
			}
		}
	}
}

func newID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}

func (s *Store) GetOrCreateUser(ctx context.Context, username string) (*User, error) {
	var u User
	var createdAtStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, username, created_at FROM users WHERE username = ?`, username).Scan(&u.ID, &u.Username, &createdAtStr)
	if err == nil {
		t, _ := time.Parse(SortableTimeFormat, createdAtStr)
		u.CreatedAt = t
		return &u, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	u = User{
		ID:        newID(),
		Username:  username,
		CreatedAt: time.Now().UTC(),
	}
	createdAtStr = u.CreatedAt.Format(SortableTimeFormat)

	_, err = s.db.ExecContext(ctx, `INSERT INTO users (id, username, created_at) VALUES (?, ?, ?)`, u.ID, u.Username, createdAtStr)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) CreateRoom(ctx context.Context, name, description string, creatorID string, isPrivate bool, roomKey string) (*Room, error) {
	nameRunes := []rune(name)
	if len(nameRunes) > 64 {
		name = string(nameRunes[:64])
	}
	descRunes := []rune(description)
	if len(descRunes) > 256 {
		description = string(descRunes[:256])
	}

	// Simple duplicate name check before creating a new ID
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM rooms WHERE name = ?", name).Scan(&exists); err == nil {
		return nil, fmt.Errorf("room '%s' already exists", name)
	}

	var id string
	if isPrivate {
		id = newID()
	} else {
		hasher := sha256.New()
		hasher.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
		id = "public-" + hex.EncodeToString(hasher.Sum(nil))[:24] // 31 chars total
	}

	r := &Room{
		ID:          id,
		Name:        name,
		Description: description,
		IsPrivate:   isPrivate,
		RoomKey:     roomKey,
		CreatedAt:   time.Now().UTC(),
	}
	createdAtStr := r.CreatedAt.Format(SortableTimeFormat)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO rooms (id, name, description, is_private, room_key, created_at) VALUES (?, ?, ?, ?, ?, ?)`, r.ID, r.Name, r.Description, r.IsPrivate, r.RoomKey, createdAtStr)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO memberships (user_id, room_id, role, joined_at) VALUES (?, ?, ?, ?)`, creatorID, r.ID, "owner", createdAtStr)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return r, nil
}

// EnsurePublicRoomExists silently inserts a public room into the database if it doesn't already exist.
// This is used by the P2P discovery listener when it hears about a room created by another peer.
func (s *Store) EnsurePublicRoomExists(ctx context.Context, id, name, desc, creatorID string, createdAt time.Time) error {
	// Truncate strings to prevent malicious room payloads from bloating the DB
	nameRunes := []rune(name)
	if len(nameRunes) > 64 {
		name = string(nameRunes[:64])
	}
	descRunes := []rune(desc)
	if len(descRunes) > 256 {
		desc = string(descRunes[:256])
	}

	createdAtStr := createdAt.Format(SortableTimeFormat)
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO rooms (id, name, description, is_private, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, name, desc, false, createdAtStr)

	// Note: We don't automatically join the room (no membership) so it doesn't appear in the "pinned" list.
	return err
}

func (s *Store) JoinExternalRoom(ctx context.Context, roomID, name, userID string, isPrivate bool, roomKey string) (*Room, error) {
	// If the room already exists locally, just make sure the user is joined
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM rooms WHERE id = ?", roomID).Scan(&exists); err == nil {
		if err := s.JoinRoom(ctx, userID, roomID); err != nil {
			return nil, err
		}
		// fetch the fully initialized room to return
		var r Room
		var createdAtStr string
		var isPrivateLocal sql.NullBool
		var roomKeyLocal sql.NullString
		err := s.db.QueryRowContext(ctx, "SELECT id, name, description, is_private, room_key, created_at FROM rooms WHERE id = ?", roomID).Scan(
			&r.ID, &r.Name, &r.Description, &isPrivateLocal, &roomKeyLocal, &createdAtStr)
		if err != nil {
			return nil, err
		}
		if isPrivateLocal.Valid {
			r.IsPrivate = isPrivateLocal.Bool
		}
		if roomKeyLocal.Valid {
			r.RoomKey = roomKeyLocal.String
		}
		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		return &r, nil
	}

	// Wait, is it a new room ID to this local node? Then create it in DB
	r := &Room{
		ID:          roomID,
		Name:        name,
		Description: "External Room",
		IsPrivate:   isPrivate,
		RoomKey:     roomKey,
		CreatedAt:   time.Now().UTC(),
	}
	createdAtStr := r.CreatedAt.Format(SortableTimeFormat)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO rooms (id, name, description, is_private, room_key, created_at) VALUES (?, ?, ?, ?, ?, ?)`, r.ID, r.Name, r.Description, r.IsPrivate, r.RoomKey, createdAtStr)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO memberships (user_id, room_id, role, joined_at) VALUES (?, ?, ?, ?)`, userID, r.ID, "member", createdAtStr)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return r, nil
}

func (s *Store) UpdateRoomNameIfDefault(ctx context.Context, roomID, newName string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE rooms 
		SET name = ? 
		WHERE id = ? AND name = 'Remote Room'
	`, newName, roomID)
	return err
}

func (s *Store) ListRooms(ctx context.Context, userID string) ([]Room, error) {
	// We use a subquery to find the newest message in the room
	// and compare it against the user's last_read_at for that room.
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.description, r.is_private, r.room_key, r.created_at,
		       (SELECT COUNT(m2.id) > 0 
		        FROM messages m2 
		        WHERE m2.room_id = r.id 
		          AND m2.created_at > COALESCE(m.last_read_at, r.created_at)
		          AND m2.author_id != m.user_id) as has_unread
		FROM rooms r
		JOIN memberships m ON m.room_id = r.id
		WHERE m.user_id = ?
		ORDER BY has_unread DESC, r.name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var r Room
		var createdAtStr string
		var isPrivateLocal sql.NullBool
		var roomKeyLocal sql.NullString

		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &isPrivateLocal, &roomKeyLocal, &createdAtStr, &r.HasUnread); err != nil {
			return nil, err
		}

		if isPrivateLocal.Valid {
			r.IsPrivate = isPrivateLocal.Bool
		}
		if roomKeyLocal.Valid {
			r.RoomKey = roomKeyLocal.String
		}

		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

func (s *Store) SearchAllRooms(ctx context.Context) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, is_private, created_at
		FROM rooms
		WHERE is_private = false
		ORDER BY name ASC, created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rooms []Room
	for rows.Next() {
		var r Room
		var createdAtStr string
		var isPrivateLocal sql.NullBool

		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &isPrivateLocal, &createdAtStr); err != nil {
			return nil, err
		}
		if isPrivateLocal.Valid {
			r.IsPrivate = isPrivateLocal.Bool
		}
		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

func (s *Store) JoinRoom(ctx context.Context, userID, roomID string) error {
	now := time.Now().UTC().Format(SortableTimeFormat)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memberships (user_id, room_id, role, joined_at, last_read_at)
		VALUES (?, ?, 'member', ?, ?)
		ON CONFLICT DO NOTHING
	`, userID, roomID, now, now)
	return err
}

func (s *Store) LeaveRoom(ctx context.Context, userID, roomID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM memberships WHERE user_id = ? AND room_id = ?`, userID, roomID)
	return err
}

func (s *Store) DeleteRoom(ctx context.Context, roomID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete all memberships for this room
	if _, err := tx.ExecContext(ctx, `DELETE FROM memberships WHERE room_id = ?`, roomID); err != nil {
		return err
	}
	// Delete all messages in the room
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE room_id = ?`, roomID); err != nil {
		return err
	}
	// Delete the room itself
	if _, err := tx.ExecContext(ctx, `DELETE FROM rooms WHERE id = ?`, roomID); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) IsRoomOwner(ctx context.Context, userID, roomID string) (bool, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM memberships WHERE user_id = ? AND room_id = ?`, userID, roomID).Scan(&role)
	if err != nil {
		return false, err
	}
	return role == "owner", nil
}

func (s *Store) MarkRoomAsRead(ctx context.Context, userID, roomID string) error {
	now := time.Now().UTC().Format(SortableTimeFormat)
	_, err := s.db.ExecContext(ctx, `
		UPDATE memberships 
		SET last_read_at = ?
		WHERE user_id = ? AND room_id = ?
	`, now, userID, roomID)
	return err
}

func (s *Store) CreateMessage(ctx context.Context, roomID, authorID, content string) (*Message, error) {
	m := &Message{
		ID:        newID(),
		RoomID:    roomID,
		AuthorID:  authorID,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	createdAtStr := m.CreatedAt.Format(SortableTimeFormat)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, author_id, content, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, m.ID, m.RoomID, m.AuthorID, m.Content, createdAtStr)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// EnsureRemoteUserExists creates a stub user record for incoming P2P messages if they aren't in the local DB yet.
// If it already exists, it updates the username if the new username is not a default "Guest-" alias.
func (s *Store) EnsureRemoteUserExists(ctx context.Context, userID string, username string) error {
	// Truncate to prevent malicious P2P profile names from breaking the UI
	userRunes := []rune(username)
	if len(userRunes) > 32 {
		username = string(userRunes[:32])
	}

	now := time.Now().UTC().Format(SortableTimeFormat)

	// If the username we are trying to insert is a Guest- ID, we don't want to overwrite a real username.
	// But if we received a real username, we DO want to overwrite a previous Guest- ID.
	query := `
		INSERT INTO users (id, username, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = CASE 
				WHEN excluded.username NOT LIKE 'Guest-%' THEN excluded.username
				ELSE users.username
			END
	`
	_, err := s.db.ExecContext(ctx, query, userID, username, now)
	return err
}

// SyncMessage inserts a pre-constructed message received from the P2P p2p into the async queue.
// It ignores conflicts (e.g., if we already received or sent this message).
func (s *Store) SyncMessage(ctx context.Context, m *Message) error {
	select {
	case s.syncQueue <- syncJob{ctx: ctx, m: m}:
		return nil
	default:
		return fmt.Errorf("sqlite sync queue saturated, dropping message %s", m.ID)
	}
}

func (s *Store) ListMessages(ctx context.Context, roomID string, limit int) ([]FeedMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.room_id, m.author_id, m.content, m.created_at, r.name, u.username
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN users u ON u.id = m.author_id
		WHERE m.room_id = ?
		ORDER BY m.created_at DESC
		LIMIT ?
	`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []FeedMessage
	for rows.Next() {
		var fm FeedMessage
		var createdAtStr string
		if err := rows.Scan(&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.Content, &createdAtStr, &fm.RoomName, &fm.AuthorUsername); err != nil {
			return nil, err
		}
		fm.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		messages = append(messages, fm)
	}

	// Reverse to chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, rows.Err()
}
