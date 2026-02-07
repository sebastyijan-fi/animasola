package store

import (
	"context"
	"database/sql"
	"math"
	"sort"
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

func (s *Store) ListRoomTopLevelNewPage(ctx context.Context, roomID string, limit int, beforeID *string) ([]FeedMessage, *string, error) {
	items, err := s.ListRoomTopLevelNew(ctx, roomID, limit, beforeID)
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(items) == limit {
		id := items[len(items)-1].ID
		next = &id
	}
	return items, next, nil
}

type RoomTopCursor struct {
	Upvotes int
	ID      string
}

func (s *Store) ListRoomTopLevelTop(ctx context.Context, roomID string, topRange TopRange, limit int, before *RoomTopCursor) ([]FeedMessage, error) {
	var since *time.Time
	now := time.Now().UTC()
	switch topRange {
	case TopToday:
		t := now.Add(-24 * time.Hour)
		since = &t
	case TopWeek:
		t := now.Add(-7 * 24 * time.Hour)
		since = &t
	case TopMonth:
		t := now.Add(-30 * 24 * time.Hour)
		since = &t
	case TopAll:
		since = nil
	default:
		since = nil
	}

	q := `
		SELECT
			m.id, m.room_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username
		FROM messages m
		JOIN users u ON u.id = m.author_id
		WHERE m.room_id = ?
		  AND m.parent_id IS NULL
	`
	var args []any
	args = append(args, roomID)
	if since != nil {
		q += ` AND m.created_at >= ?`
		args = append(args, since.Format(time.RFC3339Nano))
	}
	if before != nil {
		// Cursor is (upvotes, id).
		q += ` AND (m.upvote_count < ? OR (m.upvote_count = ? AND m.id < ?))`
		args = append(args, before.Upvotes, before.Upvotes, before.ID)
	}
	q += ` ORDER BY m.upvote_count DESC, m.id DESC LIMIT ?`
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

func (s *Store) ListRoomTopLevelTopPage(ctx context.Context, roomID string, topRange TopRange, limit int, before *RoomTopCursor) ([]FeedMessage, *RoomTopCursor, error) {
	items, err := s.ListRoomTopLevelTop(ctx, roomID, topRange, limit, before)
	if err != nil {
		return nil, nil, err
	}
	var next *RoomTopCursor
	if len(items) == limit {
		last := items[len(items)-1]
		next = &RoomTopCursor{Upvotes: last.Upvotes, ID: last.ID}
	}
	return items, next, nil
}

type RoomHotCursor struct {
	Score float64
	ID    string
}

func (s *Store) ListRoomTopLevelHotPage(ctx context.Context, roomID string, now time.Time, limit int, before *RoomHotCursor) ([]FeedMessage, *RoomHotCursor, error) {
	// Hot score is computed in Go for portability. We fetch a candidate set and sort.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.room_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username
		FROM messages m
		JOIN users u ON u.id = m.author_id
		WHERE m.room_id = ?
		  AND m.parent_id IS NULL
		ORDER BY m.id DESC
		LIMIT 500
	`, roomID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var items []FeedMessage
	for rows.Next() {
		var fm FeedMessage
		var parent sql.NullString
		var createdAt string
		var isDeleted int
		if err := rows.Scan(
			&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.Content, &parent, &fm.Upvotes, &fm.Replies, &isDeleted, &createdAt,
			&fm.AuthorUsername,
		); err != nil {
			return nil, nil, err
		}
		if parent.Valid {
			fm.ParentID = &parent.String
		}
		fm.IsDeleted = isDeleted != 0
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, nil, err
		}
		fm.CreatedAt = t
		items = append(items, fm)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	type scored struct {
		m     FeedMessage
		score float64
	}
	scoredItems := make([]scored, 0, len(items))
	for _, it := range items {
		age := now.Sub(it.CreatedAt).Hours()
		den := math.Pow(age+2, 1.5)
		score := 0.0
		if den > 0 {
			score = float64(it.Upvotes) / den
		}
		scoredItems = append(scoredItems, scored{m: it, score: score})
	}

	sort.SliceStable(scoredItems, func(i, j int) bool {
		if scoredItems[i].score == scoredItems[j].score {
			return scoredItems[i].m.ID > scoredItems[j].m.ID
		}
		return scoredItems[i].score > scoredItems[j].score
	})

	start := 0
	if before != nil {
		for i, s := range scoredItems {
			if s.score < before.Score || (s.score == before.Score && s.m.ID < before.ID) {
				start = i
				break
			}
			// If we never find a smaller score, we end up returning none.
			start = len(scoredItems)
		}
	}
	if start >= len(scoredItems) {
		return nil, nil, nil
	}
	end := start + limit
	if end > len(scoredItems) {
		end = len(scoredItems)
	}
	out := make([]FeedMessage, 0, end-start)
	for _, s := range scoredItems[start:end] {
		out = append(out, s.m)
	}

	var next *RoomHotCursor
	if len(out) == limit {
		last := scoredItems[start+limit-1]
		next = &RoomHotCursor{Score: last.score, ID: last.m.ID}
	}
	return out, next, nil
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
