package store

import (
	"context"
	"database/sql"
	"time"

	"animasola/internal/id"
)

func (s *Store) CreateMessage(ctx context.Context, roomID, authorID, content string, parentID *string) (*Message, error) {
	mid, err := id.New()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	var parent any = nil
	if parentID != nil {
		parent = *parentID
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO messages (id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, 0, ?)
	`, mid, roomID, authorID, content, parent, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}

	return &Message{
		ID:        mid,
		RoomID:    roomID,
		AuthorID:  authorID,
		Content:   content,
		ParentID:  parentID,
		Upvotes:   0,
		Replies:   0,
		IsDeleted: false,
		CreatedAt: now,
	}, nil
}

func (s *Store) GetMessageByID(ctx context.Context, id string) (*Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, room_id, author_id, content, parent_id, upvote_count, reply_count, is_deleted, created_at
		FROM messages
		WHERE id = ?
	`, id)
	var m Message
	var parent sql.NullString
	var createdAt string
	var isDeleted int
	if err := row.Scan(&m.ID, &m.RoomID, &m.AuthorID, &m.Content, &parent, &m.Upvotes, &m.Replies, &isDeleted, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if parent.Valid {
		m.ParentID = &parent.String
	}
	m.IsDeleted = isDeleted != 0
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = t
	return &m, nil
}

func (s *Store) SoftDeleteMessage(ctx context.Context, messageID, requesterUserID string) error {
	row := s.db.QueryRowContext(ctx, `SELECT author_id FROM messages WHERE id = ?`, messageID)
	var authorID string
	if err := row.Scan(&authorID); err != nil {
		if err == sql.ErrNoRows {
			return sql.ErrNoRows
		}
		return err
	}
	if authorID != requesterUserID {
		return ErrPermission
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE messages
		SET is_deleted = 1, content = ''
		WHERE id = ?
	`, messageID)
	return err
}

func (s *Store) ListRoomTopLevelNew(ctx context.Context, roomID string, limit int, beforeID *string) ([]FeedMessage, error) {
	// ULIDs sort lexicographically by time, so id is a usable cursor.
	var args []any
	args = append(args, roomID)

	q := `
		SELECT
			m.id, m.room_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username
		FROM messages m
		JOIN users u ON u.id = m.author_id
		WHERE m.room_id = ?
		  AND m.parent_id IS NULL
	`
	if beforeID != nil {
		q += ` AND m.id < ?`
		args = append(args, *beforeID)
	}
	q += ` ORDER BY m.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FeedMessage
	for rows.Next() {
		var fm FeedMessage
		var parent sql.NullString
		var createdAt string
		var isDeleted int
		if err := rows.Scan(
			&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.Content, &parent, &fm.Upvotes, &fm.Replies, &isDeleted, &createdAt,
			&fm.AuthorUsername,
		); err != nil {
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
		out = append(out, fm)
	}
	return out, rows.Err()
}

func (s *Store) ListThread(ctx context.Context, rootMessageID string) ([]FeedMessage, error) {
	// Includes root + all descendants, sorted chronologically (ULID order asc).
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE thread(id) AS (
			SELECT ?
			UNION ALL
			SELECT m.id
			FROM messages m
			JOIN thread t ON m.parent_id = t.id
		)
		SELECT
			m.id, m.room_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username
		FROM messages m
		JOIN users u ON u.id = m.author_id
		WHERE m.id IN (SELECT id FROM thread)
		ORDER BY m.id ASC
	`, rootMessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FeedMessage
	for rows.Next() {
		var fm FeedMessage
		var parent sql.NullString
		var createdAt string
		var isDeleted int
		if err := rows.Scan(
			&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.Content, &parent, &fm.Upvotes, &fm.Replies, &isDeleted, &createdAt,
			&fm.AuthorUsername,
		); err != nil {
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
		out = append(out, fm)
	}
	return out, rows.Err()
}
