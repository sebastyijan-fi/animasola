package store

import (
	"context"
	"database/sql"
	"time"
)

type CommunityUnread struct {
	CommunityID string
	Count       int
}

// UpsertReadPosition records the last read message ID for a user in a room.
// It will never move the cursor backwards: if lastReadMessageID is older than
// what's already stored, the stored value is kept.
func (s *Store) UpsertReadPosition(ctx context.Context, userID, roomID, lastReadMessageID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO read_positions (user_id, room_id, last_read_message_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, room_id) DO UPDATE SET
			last_read_message_id = CASE
				WHEN excluded.last_read_message_id > read_positions.last_read_message_id THEN excluded.last_read_message_id
				ELSE read_positions.last_read_message_id
			END,
			updated_at = excluded.updated_at
	`, userID, roomID, lastReadMessageID, now)
	return err
}

func (s *Store) GetReadPosition(ctx context.Context, userID, roomID string) (*string, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT last_read_message_id
		FROM read_positions
		WHERE user_id = ? AND room_id = ?
	`, userID, roomID)
	var v sql.NullString
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	return &v.String, nil
}

// UnreadCountsByCommunity returns unread counts across communities the user has joined.
// Unread is defined as messages with ULID > last_read_message_id for a room.
func (s *Store) UnreadCountsByCommunity(ctx context.Context, userID string) ([]CommunityUnread, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.community_id, COUNT(*) AS unread
		FROM rooms r
		JOIN memberships ms
		  ON ms.community_id = r.community_id AND ms.user_id = ?
		JOIN messages m
		  ON m.room_id = r.id
		LEFT JOIN read_positions rp
		  ON rp.user_id = ? AND rp.room_id = r.id
		WHERE rp.last_read_message_id IS NULL OR m.id > rp.last_read_message_id
		GROUP BY r.community_id
	`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CommunityUnread
	for rows.Next() {
		var cu CommunityUnread
		if err := rows.Scan(&cu.CommunityID, &cu.Count); err != nil {
			return nil, err
		}
		out = append(out, cu)
	}
	return out, rows.Err()
}
