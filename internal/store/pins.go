package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) PinMessage(ctx context.Context, roomID, messageID string) error {
	// Ensure the message belongs to the room.
	row := s.db.QueryRowContext(ctx, `SELECT room_id FROM messages WHERE id = ?`, messageID)
	var gotRoomID string
	if err := row.Scan(&gotRoomID); err != nil {
		return err
	}
	if gotRoomID != roomID {
		return ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE rooms
		SET pinned_message_id = ?
		WHERE id = ?
	`, messageID, roomID)
	return err
}

func (s *Store) UnpinRoom(ctx context.Context, roomID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE rooms
		SET pinned_message_id = NULL
		WHERE id = ?
	`, roomID)
	return err
}

func (s *Store) GetPinnedMessage(ctx context.Context, roomID string) (*FeedMessage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			m.id, m.room_id, m.author_id, m.content, m.parent_id,
			m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			r.name,
			c.id, c.name,
			u.username
		FROM rooms ro
		JOIN messages m ON m.id = ro.pinned_message_id
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN users u ON u.id = m.author_id
		WHERE ro.id = ?
	`, roomID)
	var fm FeedMessage
	var createdAt string
	var parent sql.NullString
	var isDeleted int
	if err := row.Scan(
		&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.Content, &parent,
		&fm.Upvotes, &fm.Replies, &isDeleted, &createdAt,
		&fm.RoomName,
		&fm.CommunityID, &fm.CommunityName,
		&fm.AuthorUsername,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if parent.Valid {
		fm.ParentID = &parent.String
	}
	fm.IsDeleted = isDeleted != 0
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	fm.CreatedAt = t
	return &fm, nil
}
