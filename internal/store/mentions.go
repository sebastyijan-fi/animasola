package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) ListMentions(ctx context.Context, userID, username string, limit int) ([]FeedMessage, error) {
	needle := "@" + username
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.room_id, m.author_id, m.content, m.parent_id,
			m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			r.name,
			c.id, c.name,
			u.username
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN users u ON u.id = m.author_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		WHERE lower(m.content) LIKE '%' || lower(?) || '%'
		ORDER BY m.created_at DESC
		LIMIT ?
	`, userID, needle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FeedMessage
	for rows.Next() {
		var fm FeedMessage
		var createdAt string
		var parent sql.NullString
		var isDeleted int
		if err := rows.Scan(
			&fm.ID, &fm.RoomID, &fm.AuthorID, &fm.Content, &parent,
			&fm.Upvotes, &fm.Replies, &isDeleted, &createdAt,
			&fm.RoomName,
			&fm.CommunityID, &fm.CommunityName,
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
