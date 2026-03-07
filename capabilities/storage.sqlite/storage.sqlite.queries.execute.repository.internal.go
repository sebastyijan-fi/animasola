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
const publicRoomFreshnessTTL = 24 * time.Hour
const maxPrivateRoomsPerCreator = 100
const maxMessageContentRunes = 2000
const maxStoredMessagesPerPublicRoom = 2000
const maxStoredMessagesPerPrivateRoom = 5000
const publicRoomMetadataUpdateCooldown = 10 * time.Second

// StartDataPruning runs a background goroutine that deletes messages older than the retention period.
func (s *Store) StartDataPruning(ctx context.Context, retention time.Duration) {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		s.pruneOldMessages(retention) // Run immediately on startup
		_ = s.PruneStalePublicRooms(ctx, retention)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pruneOldMessages(retention)
				_ = s.PruneStalePublicRooms(ctx, retention)
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
		// We deliberately omit VACUUM here. SQLite will automatically reuse these empty pages
		// for future inserts. Running VACUUM aggressively locks the database file exclusively,
		// which immediately causes "database is locked" crashes in the main Bubbletea TUI loop.
	}
}

// PruneAndCompact forces an immediate deletion of expired messages followed by a physical disk VACUUM.
// Warning: This exclusively locks the database and should only be used by Headless/Automated routines!
func (s *Store) PruneAndCompact(ctx context.Context, retention time.Duration) error {
	s.pruneOldMessages(retention)
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}

func newID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}

func (s *Store) GetOrCreateUser(ctx context.Context, username string, explicitID string) (*User, error) {
	var u User
	var createdAtStr string
	err := s.db.QueryRowContext(ctx, `SELECT id, username, created_at FROM users WHERE username = ?`, username).Scan(&u.ID, &u.Username, &createdAtStr)
	if err == nil {
		// Verify and upgrade the legacy UUID to the deterministic Ed25519 PeerID
		if u.ID != explicitID {
			tx, err := s.db.BeginTx(ctx, nil)
			if err == nil {
				tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`)
				tx.ExecContext(ctx, `UPDATE users SET id = ? WHERE id = ?`, explicitID, u.ID)
				tx.ExecContext(ctx, `UPDATE messages SET author_id = ? WHERE author_id = ?`, explicitID, u.ID)
				tx.ExecContext(ctx, `UPDATE memberships SET user_id = ? WHERE user_id = ?`, explicitID, u.ID)
				tx.Commit()
			}
			u.ID = explicitID
		}
		t, _ := time.Parse(SortableTimeFormat, createdAtStr)
		u.CreatedAt = t
		return &u, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	u = User{
		ID:        explicitID,
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

	var id string
	if isPrivate {
		var privateRoomCount int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM rooms WHERE creator_id = ? AND is_private = 1`, creatorID).Scan(&privateRoomCount); err != nil {
			return nil, err
		}
		if privateRoomCount >= maxPrivateRoomsPerCreator {
			return nil, fmt.Errorf("private room limit reached (%d max per profile)", maxPrivateRoomsPerCreator)
		}
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
		CreatorID:   creatorID,
		IsPrivate:   isPrivate,
		RoomKey:     roomKey,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		LastSeenAt:  time.Now().UTC(),
		Version:     1,
	}
	createdAtStr := r.CreatedAt.Format(SortableTimeFormat)
	updatedAtStr := r.UpdatedAt.Format(SortableTimeFormat)
	lastSeenAtStr := r.LastSeenAt.Format(SortableTimeFormat)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO rooms (id, name, description, creator_id, signature, is_private, room_key, created_at, updated_at, last_seen_at, version, announce_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.Name, r.Description, r.CreatorID, r.Signature, r.IsPrivate, r.RoomKey, createdAtStr, updatedAtStr, lastSeenAtStr, r.Version, 1)
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

	if !isPrivate {
		if err := s.UpsertPublicRoomIndex(ctx, r); err != nil {
			return nil, err
		}
	}

	return r, nil
}

func (s *Store) ListOwnedPublicRooms(ctx context.Context, userID string) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.description, r.creator_id, r.signature, r.created_at, r.updated_at, r.last_seen_at, r.version, COALESCE(r.announce_count, 0)
		FROM rooms r
		JOIN memberships m ON m.room_id = r.id
		WHERE m.user_id = ? AND m.role = 'owner' AND r.creator_id = ? AND r.is_private = 0
		ORDER BY COALESCE(r.last_seen_at, r.created_at) DESC, r.name ASC
	`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var r Room
		var createdAtStr string
		var updatedAtStr sql.NullString
		var creatorID sql.NullString
		var signature sql.NullString
		var lastSeenAtStr sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &creatorID, &signature, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.AnnounceCount); err != nil {
			return nil, err
		}
		if creatorID.Valid {
			r.CreatorID = creatorID.String
		}
		if signature.Valid {
			r.Signature = signature.String
		}
		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		if updatedAtStr.Valid {
			r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
		}
		if lastSeenAtStr.Valid {
			r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
		}
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

// EnsurePublicRoomExists silently inserts a public room into the database if it doesn't already exist.
// This is used by the P2P discovery listener when it hears about a room created by another peer.
func (s *Store) EnsurePublicRoomExists(ctx context.Context, id, name, desc, creatorID, signature string, createdAt, updatedAt time.Time, version int) error {
	// Truncate strings to prevent malicious room payloads from bloating the DB
	nameRunes := []rune(name)
	if len(nameRunes) > 64 {
		name = string(nameRunes[:64])
	}
	descRunes := []rune(desc)
	if len(descRunes) > 256 {
		desc = string(descRunes[:256])
	}

	lastSeenAtStr := time.Now().UTC().Format(SortableTimeFormat)
	entry := &Room{
		ID:            id,
		Name:          name,
		Description:   desc,
		CreatorID:     creatorID,
		Signature:     signature,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		LastSeenAt:    time.Now().UTC(),
		Version:       version,
		AnnounceCount: 1,
	}
	if err := s.UpsertPublicRoomIndex(ctx, entry); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE rooms
		SET name = CASE WHEN name LIKE 'Remote Room%' THEN ? ELSE name END,
		    description = CASE WHEN description = 'External Room' OR description = '' THEN ? ELSE description END,
		    creator_id = COALESCE(creator_id, ?),
		    signature = COALESCE(signature, ?),
		    updated_at = CASE WHEN COALESCE(version, 1) <= ? THEN ? ELSE updated_at END,
		    version = CASE WHEN COALESCE(version, 1) <= ? THEN ? ELSE version END,
		    last_seen_at = ?
		WHERE id = ? AND is_private = 0
	`, name, desc, creatorID, signature, version, updatedAt.Format(SortableTimeFormat), version, version, lastSeenAtStr, id)
	return err
}

func (s *Store) JoinExternalRoom(ctx context.Context, roomID, name, userID, creatorID string, isPrivate bool, roomKey string) (*Room, error) {
	// If the room already exists locally, just make sure the user is joined
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM rooms WHERE id = ?", roomID).Scan(&exists); err == nil {
		if err := s.JoinRoom(ctx, userID, roomID); err != nil {
			return nil, err
		}
		// fetch the fully initialized room to return
		var r Room
		var createdAtStr string
		var updatedAtStr sql.NullString
		var creatorID sql.NullString
		var signature sql.NullString
		var isPrivateLocal sql.NullBool
		var roomKeyLocal sql.NullString
		var lastSeenAtStr sql.NullString
		err := s.db.QueryRowContext(ctx, "SELECT id, name, description, creator_id, signature, is_private, room_key, created_at, updated_at, last_seen_at, version, announce_count FROM rooms WHERE id = ?", roomID).Scan(
			&r.ID, &r.Name, &r.Description, &creatorID, &signature, &isPrivateLocal, &roomKeyLocal, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.AnnounceCount)
		if err != nil {
			return nil, err
		}
		if creatorID.Valid {
			r.CreatorID = creatorID.String
		}
		if signature.Valid {
			r.Signature = signature.String
		}
		if isPrivateLocal.Valid {
			r.IsPrivate = isPrivateLocal.Bool
		}
		if roomKeyLocal.Valid {
			r.RoomKey = roomKeyLocal.String
		}
		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		if updatedAtStr.Valid {
			r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
		}
		if lastSeenAtStr.Valid {
			r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
		}
		return &r, nil
	}

	// Wait, is it a new room ID to this local node? Then create it in DB
	r := &Room{
		ID:          roomID,
		Name:        name,
		Description: "External Room",
		CreatorID:   creatorID,
		IsPrivate:   isPrivate,
		RoomKey:     roomKey,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		LastSeenAt:  time.Now().UTC(),
		Version:     1,
	}
	if strings.TrimSpace(r.CreatorID) == "" {
		r.CreatorID = userID
	}
	createdAtStr := r.CreatedAt.Format(SortableTimeFormat)
	updatedAtStr := r.UpdatedAt.Format(SortableTimeFormat)
	lastSeenAtStr := r.LastSeenAt.Format(SortableTimeFormat)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO rooms (id, name, description, creator_id, signature, is_private, room_key, created_at, updated_at, last_seen_at, version, announce_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.Name, r.Description, r.CreatorID, r.Signature, r.IsPrivate, r.RoomKey, createdAtStr, updatedAtStr, lastSeenAtStr, r.Version, 1)
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

	if !isPrivate {
		if err := s.UpsertPublicRoomIndex(ctx, r); err != nil {
			return nil, err
		}
	}

	return r, nil
}

func (s *Store) UpdateRoomNameIfDefault(ctx context.Context, roomID, newName string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE rooms 
		SET name = ? 
		WHERE id = ? AND name LIKE 'Remote Room%'
	`, newName, roomID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE public_room_index
		SET name = ?
		WHERE room_id = ?
	`, newName, roomID)
	return err
}

func (s *Store) UpdateRoomSecret(ctx context.Context, roomID, roomKey string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE rooms
		SET room_key = ?
		WHERE id = ?
	`, roomKey, roomID)
	return err
}

func (s *Store) UpdatePublicRoomMetadata(ctx context.Context, roomID, ownerID, name, description, signature string) (*Room, error) {
	nameRunes := []rune(name)
	if len(nameRunes) > 64 {
		name = string(nameRunes[:64])
	}
	descRunes := []rune(description)
	if len(descRunes) > 256 {
		description = string(descRunes[:256])
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var role string
	var r Room
	var createdAtStr string
	var updatedAtStr sql.NullString
	var lastSeenAtStr sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT m.role, r.id, r.name, r.description, r.creator_id, r.signature, r.created_at, r.updated_at, r.last_seen_at, r.version, r.announce_count
		FROM rooms r
		JOIN memberships m ON m.room_id = r.id
		WHERE r.id = ? AND m.user_id = ? AND r.is_private = 0
	`, roomID, ownerID).Scan(&role, &r.ID, &r.Name, &r.Description, &r.CreatorID, &r.Signature, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.AnnounceCount)
	if err != nil {
		return nil, err
	}
	if role != "owner" {
		return nil, fmt.Errorf("user %s is not the owner of room %s", ownerID, roomID)
	}

	r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
	if updatedAtStr.Valid {
		r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
	}
	if lastSeenAtStr.Valid {
		r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
	}
	if !r.UpdatedAt.IsZero() && r.Version > 1 {
		now := time.Now().UTC()
		if now.Sub(r.UpdatedAt) < publicRoomMetadataUpdateCooldown {
			return nil, fmt.Errorf("public room metadata can only be updated every %s", publicRoomMetadataUpdateCooldown)
		}
	}

	r.Name = name
	r.Description = description
	r.Signature = signature
	r.Version++
	r.UpdatedAt = time.Now().UTC()
	if r.LastSeenAt.IsZero() {
		r.LastSeenAt = r.UpdatedAt
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE rooms
		SET name = ?, description = ?, signature = ?, updated_at = ?, version = ?
		WHERE id = ?
	`, r.Name, r.Description, r.Signature, r.UpdatedAt.Format(SortableTimeFormat), r.Version, roomID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.UpsertPublicRoomIndex(ctx, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListRooms(ctx context.Context, userID string) ([]Room, error) {
	// We use a subquery to find the newest message in the room
	// and compare it against the user's last_read_at for that room.
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.description, r.creator_id, r.signature, r.is_private, r.room_key, r.created_at, r.updated_at, r.last_seen_at, r.version,
		       (SELECT COUNT(m2.id) > 0 
		        FROM messages m2 
		        WHERE m2.room_id = r.id 
		          AND m2.created_at > COALESCE(m.last_read_at, r.created_at)
		          AND m2.author_id != m.user_id) as has_unread,
		       COALESCE(lrp.is_hidden, 0) as is_hidden,
		       COALESCE(lrp.is_trusted, 0) as is_trusted,
		       COALESCE(lpp.is_muted, 0) as creator_muted,
		       COALESCE(lpp.is_blocked, 0) as creator_blocked
		FROM rooms r
		JOIN memberships m ON m.room_id = r.id
		LEFT JOIN local_room_policies lrp ON lrp.room_id = r.id
		LEFT JOIN local_peer_policies lpp ON lpp.peer_id = r.creator_id
		WHERE m.user_id = ?
		ORDER BY has_unread DESC, COALESCE(lrp.is_trusted, 0) DESC, r.name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var r Room
		var createdAtStr string
		var updatedAtStr sql.NullString
		var creatorID sql.NullString
		var signature sql.NullString
		var lastSeenAtStr sql.NullString
		var isPrivateLocal sql.NullBool
		var roomKeyLocal sql.NullString
		var isHidden bool
		var isTrusted bool
		var creatorMuted bool
		var creatorBlocked bool

		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &creatorID, &signature, &isPrivateLocal, &roomKeyLocal, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.HasUnread, &isHidden, &isTrusted, &creatorMuted, &creatorBlocked); err != nil {
			return nil, err
		}
		if creatorID.Valid {
			r.CreatorID = creatorID.String
		}
		if signature.Valid {
			r.Signature = signature.String
		}

		if isPrivateLocal.Valid {
			r.IsPrivate = isPrivateLocal.Bool
		}
		if roomKeyLocal.Valid {
			r.RoomKey = roomKeyLocal.String
		}

		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		if updatedAtStr.Valid {
			r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
		}
		if lastSeenAtStr.Valid {
			r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
		}
		r.IsHidden = isHidden
		r.IsTrusted = isTrusted
		r.CreatorMuted = creatorMuted
		r.CreatorBlocked = creatorBlocked
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

func (s *Store) SearchAllRooms(ctx context.Context) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pri.room_id, pri.name, pri.description, pri.creator_id, pri.signature, pri.created_at, pri.updated_at, pri.last_seen_at, pri.version, pri.announce_count,
		       COALESCE(lrp.is_hidden, 0) as is_hidden,
		       COALESCE(lrp.is_trusted, 0) as is_trusted,
		       COALESCE(lpp.is_muted, 0) as creator_muted,
		       COALESCE(lpp.is_blocked, 0) as creator_blocked
		FROM public_room_index pri
		LEFT JOIN local_room_policies lrp ON lrp.room_id = pri.room_id
		LEFT JOIN local_peer_policies lpp ON lpp.peer_id = pri.creator_id
		WHERE COALESCE(pri.last_seen_at, pri.created_at) >= ?
		  AND COALESCE(lrp.is_hidden, 0) = 0
		  AND COALESCE(lpp.is_blocked, 0) = 0
		ORDER BY COALESCE(lrp.is_trusted, 0) DESC, COALESCE(pri.last_seen_at, pri.created_at) DESC, pri.name ASC
	`, time.Now().UTC().Add(-publicRoomFreshnessTTL).Format(SortableTimeFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rooms []Room
	for rows.Next() {
		var r Room
		var createdAtStr string
		var updatedAtStr sql.NullString
		var creatorID sql.NullString
		var signature sql.NullString
		var lastSeenAtStr sql.NullString
		var isHidden bool
		var isTrusted bool
		var creatorMuted bool
		var creatorBlocked bool
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &creatorID, &signature, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.AnnounceCount, &isHidden, &isTrusted, &creatorMuted, &creatorBlocked); err != nil {
			return nil, err
		}
		if creatorID.Valid {
			r.CreatorID = creatorID.String
		}
		if signature.Valid {
			r.Signature = signature.String
		}
		r.IsPrivate = false
		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		if updatedAtStr.Valid {
			r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
		}
		if lastSeenAtStr.Valid {
			r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
		}
		r.IsHidden = isHidden
		r.IsTrusted = isTrusted
		r.CreatorMuted = creatorMuted
		r.CreatorBlocked = creatorBlocked
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

func (s *Store) GetRoom(ctx context.Context, roomID string) (*Room, error) {
	var r Room
	var createdAtStr string
	var updatedAtStr sql.NullString
	var lastSeenAtStr sql.NullString
	var creatorID sql.NullString
	var signature sql.NullString
	var isPrivate sql.NullBool
	var roomKey sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.name, r.description, r.creator_id, r.signature, r.is_private, r.room_key, r.created_at, r.updated_at, r.last_seen_at, r.version, r.announce_count,
		       COALESCE(lrp.is_hidden, 0) as is_hidden,
		       COALESCE(lrp.is_trusted, 0) as is_trusted,
		       COALESCE(lpp.is_muted, 0) as creator_muted,
		       COALESCE(lpp.is_blocked, 0) as creator_blocked
		FROM rooms r
		LEFT JOIN local_room_policies lrp ON lrp.room_id = r.id
		LEFT JOIN local_peer_policies lpp ON lpp.peer_id = r.creator_id
		WHERE r.id = ?
	`, roomID).Scan(
		&r.ID, &r.Name, &r.Description, &creatorID, &signature, &isPrivate, &roomKey, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.AnnounceCount, &r.IsHidden, &r.IsTrusted, &r.CreatorMuted, &r.CreatorBlocked,
	)
	if err != nil {
		return nil, err
	}
	if creatorID.Valid {
		r.CreatorID = creatorID.String
	}
	if signature.Valid {
		r.Signature = signature.String
	}
	if isPrivate.Valid {
		r.IsPrivate = isPrivate.Bool
	}
	if roomKey.Valid {
		r.RoomKey = roomKey.String
	}
	r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
	if updatedAtStr.Valid {
		r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
	}
	if lastSeenAtStr.Valid {
		r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
	}
	return &r, nil
}

func (s *Store) ListJoinedPublicRooms(ctx context.Context, userID string) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.description, r.creator_id, r.signature, r.created_at, r.updated_at, r.last_seen_at, r.version, COALESCE(r.announce_count, 0),
		       COALESCE(lrp.is_hidden, 0) as is_hidden,
		       COALESCE(lrp.is_trusted, 0) as is_trusted,
		       COALESCE(lpp.is_muted, 0) as creator_muted,
		       COALESCE(lpp.is_blocked, 0) as creator_blocked
		FROM rooms r
		JOIN memberships m ON m.room_id = r.id
		LEFT JOIN local_room_policies lrp ON lrp.room_id = r.id
		LEFT JOIN local_peer_policies lpp ON lpp.peer_id = r.creator_id
		WHERE m.user_id = ? AND r.is_private = 0
		  AND COALESCE(lrp.is_hidden, 0) = 0
		  AND COALESCE(lpp.is_blocked, 0) = 0
		ORDER BY COALESCE(lrp.is_trusted, 0) DESC, COALESCE(r.last_seen_at, r.created_at) DESC, r.name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []Room
	for rows.Next() {
		var r Room
		var createdAtStr string
		var updatedAtStr sql.NullString
		var creatorID sql.NullString
		var signature sql.NullString
		var lastSeenAtStr sql.NullString
		var isHidden bool
		var isTrusted bool
		var creatorMuted bool
		var creatorBlocked bool
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &creatorID, &signature, &createdAtStr, &updatedAtStr, &lastSeenAtStr, &r.Version, &r.AnnounceCount, &isHidden, &isTrusted, &creatorMuted, &creatorBlocked); err != nil {
			return nil, err
		}
		if creatorID.Valid {
			r.CreatorID = creatorID.String
		}
		if signature.Valid {
			r.Signature = signature.String
		}
		r.CreatedAt, _ = time.Parse(SortableTimeFormat, createdAtStr)
		if updatedAtStr.Valid {
			r.UpdatedAt, _ = time.Parse(SortableTimeFormat, updatedAtStr.String)
		}
		if lastSeenAtStr.Valid {
			r.LastSeenAt, _ = time.Parse(SortableTimeFormat, lastSeenAtStr.String)
		}
		r.IsHidden = isHidden
		r.IsTrusted = isTrusted
		r.CreatorMuted = creatorMuted
		r.CreatorBlocked = creatorBlocked
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

func (s *Store) PruneStalePublicRooms(ctx context.Context, retention time.Duration) error {
	cutoff := time.Now().UTC().Add(-retention).Format(SortableTimeFormat)
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM public_room_index
		WHERE COALESCE(last_seen_at, created_at) < ?
		  AND room_id NOT IN (
			SELECT room_id FROM memberships
			INNER JOIN rooms ON rooms.id = memberships.room_id
			WHERE rooms.is_private = 0
		  )
	`, cutoff)
	return err
}

func (s *Store) UpsertPublicRoomIndex(ctx context.Context, room *Room) error {
	if room == nil || room.IsPrivate {
		return nil
	}
	createdAtStr := room.CreatedAt.Format(SortableTimeFormat)
	updatedAt := room.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = room.CreatedAt
	}
	updatedAtStr := updatedAt.Format(SortableTimeFormat)
	lastSeenAt := room.LastSeenAt
	if lastSeenAt.IsZero() {
		lastSeenAt = time.Now().UTC()
	}
	lastSeenAtStr := lastSeenAt.Format(SortableTimeFormat)
	version := room.Version
	if version <= 0 {
		version = 1
	}
	announceCount := room.AnnounceCount
	if announceCount <= 0 {
		announceCount = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO public_room_index (room_id, name, description, creator_id, signature, created_at, updated_at, last_seen_at, version, announce_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(room_id) DO UPDATE SET
			name = CASE WHEN excluded.version >= public_room_index.version THEN excluded.name ELSE public_room_index.name END,
			description = CASE WHEN excluded.version >= public_room_index.version THEN excluded.description ELSE public_room_index.description END,
			creator_id = COALESCE(excluded.creator_id, public_room_index.creator_id),
			signature = CASE WHEN excluded.version >= public_room_index.version THEN COALESCE(excluded.signature, public_room_index.signature) ELSE public_room_index.signature END,
			created_at = CASE WHEN excluded.version >= public_room_index.version THEN excluded.created_at ELSE public_room_index.created_at END,
			updated_at = CASE WHEN excluded.version >= public_room_index.version THEN excluded.updated_at ELSE public_room_index.updated_at END,
			last_seen_at = excluded.last_seen_at,
			version = MAX(public_room_index.version, excluded.version),
			announce_count = public_room_index.announce_count + 1
	`, room.ID, room.Name, room.Description, room.CreatorID, room.Signature, createdAtStr, updatedAtStr, lastSeenAtStr, version, announceCount)
	return err
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM public_room_index WHERE room_id = ?`, roomID); err != nil {
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
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("message cannot be empty")
	}
	contentRunes := []rune(content)
	if len(contentRunes) > maxMessageContentRunes {
		content = string(contentRunes[:maxMessageContentRunes])
	}

	m := &Message{
		ID:        newID(),
		RoomID:    roomID,
		AuthorID:  authorID,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, authorID).Scan(&m.AuthorUsername); err != nil {
		return nil, err
	}
	createdAtStr := m.CreatedAt.Format(SortableTimeFormat)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, author_id, author_username, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, m.ID, m.RoomID, m.AuthorID, m.AuthorUsername, m.Content, createdAtStr)
	if err != nil {
		return nil, err
	}
	if err := s.enforceRoomMessageLimit(ctx, roomID); err != nil {
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

func MaxMessageContentRunes() int {
	return maxMessageContentRunes
}

func PublicRoomMetadataUpdateCooldown() time.Duration {
	return publicRoomMetadataUpdateCooldown
}

func (s *Store) enforceRoomMessageLimit(ctx context.Context, roomID string) error {
	var isPrivate bool
	if err := s.db.QueryRowContext(ctx, `SELECT is_private FROM rooms WHERE id = ?`, roomID).Scan(&isPrivate); err != nil {
		return err
	}

	limit := maxStoredMessagesPerPublicRoom
	if isPrivate {
		limit = maxStoredMessagesPerPrivateRoom
	}

	_, err := s.db.ExecContext(ctx, `
		DELETE FROM messages
		WHERE id IN (
			SELECT id
			FROM messages
			WHERE room_id = ?
			ORDER BY created_at DESC
			LIMIT -1 OFFSET ?
		)
	`, roomID, limit)
	return err
}

func (s *Store) ListMessages(ctx context.Context, roomID string, limit int) ([]FeedMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.room_id, m.author_id, m.author_username, m.content, m.created_at, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		LEFT JOIN local_peer_policies lpp ON lpp.peer_id = m.author_id
		WHERE m.room_id = ?
		  AND COALESCE(lpp.is_blocked, 0) = 0
		  AND COALESCE(lpp.is_muted, 0) = 0
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
		if err := rows.Scan(&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.AuthorUsername, &fm.Content, &createdAtStr, &fm.RoomName); err != nil {
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

func (s *Store) SetRoomHidden(ctx context.Context, roomID string, hidden bool) error {
	return s.upsertRoomPolicy(ctx, roomID, "is_hidden", hidden)
}

func (s *Store) SetRoomTrusted(ctx context.Context, roomID string, trusted bool) error {
	return s.upsertRoomPolicy(ctx, roomID, "is_trusted", trusted)
}

func (s *Store) SetPeerMuted(ctx context.Context, peerID string, muted bool) error {
	return s.upsertPeerPolicy(ctx, peerID, "is_muted", muted)
}

func (s *Store) SetPeerBlocked(ctx context.Context, peerID string, blocked bool) error {
	return s.upsertPeerPolicy(ctx, peerID, "is_blocked", blocked)
}

func (s *Store) upsertRoomPolicy(ctx context.Context, roomID, column string, value bool) error {
	if roomID == "" {
		return fmt.Errorf("room id required")
	}
	if column != "is_hidden" && column != "is_trusted" {
		return fmt.Errorf("unsupported room policy column")
	}

	now := time.Now().UTC().Format(SortableTimeFormat)
	query := fmt.Sprintf(`
		INSERT INTO local_room_policies (room_id, %s, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(room_id) DO UPDATE SET
			%s = excluded.%s,
			updated_at = excluded.updated_at
	`, column, column, column)
	_, err := s.db.ExecContext(ctx, query, roomID, value, now)
	return err
}

func (s *Store) upsertPeerPolicy(ctx context.Context, peerID, column string, value bool) error {
	if peerID == "" {
		return fmt.Errorf("peer id required")
	}
	if column != "is_muted" && column != "is_blocked" {
		return fmt.Errorf("unsupported peer policy column")
	}

	now := time.Now().UTC().Format(SortableTimeFormat)
	query := fmt.Sprintf(`
		INSERT INTO local_peer_policies (peer_id, %s, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			%s = excluded.%s,
			updated_at = excluded.updated_at
	`, column, column, column)
	_, err := s.db.ExecContext(ctx, query, peerID, value, now)
	return err
}
