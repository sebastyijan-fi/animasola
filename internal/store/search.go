package store

import (
	"context"
	"database/sql"
	"time"
)

const (
	highlightStart = "<hl>"
	highlightEnd   = "</hl>"
)

func (s *Store) SearchMessages(ctx context.Context, userID string, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name,
			highlight(messages_fts, 0, ?, ?) AS highlighted
		FROM messages_fts
		JOIN messages m ON m.rowid = messages_fts.rowid
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts)
		LIMIT ?
	`, highlightStart, highlightEnd, userID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		var sr SearchResult
		var parent sql.NullString
		var createdAt string
		var isDeleted int
		if err := rows.Scan(
			&sr.ID, &sr.RoomID, &sr.CommunityID, &sr.AuthorID, &sr.Content, &parent, &sr.Upvotes, &sr.Replies, &isDeleted, &createdAt,
			&sr.AuthorUsername, &sr.CommunityName, &sr.RoomName,
			&sr.HighlightedContent,
		); err != nil {
			return nil, err
		}
		if parent.Valid {
			sr.ParentID = &parent.String
		}
		sr.IsDeleted = isDeleted != 0
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		sr.CreatedAt = t
		out = append(out, sr)
	}
	return out, rows.Err()
}

func (s *Store) ResolveThreadRootID(ctx context.Context, messageID string) (string, error) {
	// Find the top-level ancestor (parent_id IS NULL). If messageID is already top-level, it returns itself.
	row := s.db.QueryRowContext(ctx, `
		WITH RECURSIVE anc(id, parent_id) AS (
			SELECT id, parent_id FROM messages WHERE id = ?
			UNION ALL
			SELECT m.id, m.parent_id
			FROM messages m
			JOIN anc a ON m.id = a.parent_id
			WHERE a.parent_id IS NOT NULL
		)
		SELECT id
		FROM anc
		WHERE parent_id IS NULL
		LIMIT 1
	`, messageID)
	var rootID string
	if err := row.Scan(&rootID); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return rootID, nil
}
