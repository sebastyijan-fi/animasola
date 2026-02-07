package store

import (
	"context"
	"database/sql"
	"time"
)

type UserProfile struct {
	UserID     string
	Username   string
	CreatedAt  time.Time
	Posts      int
	Replies    int
	UpvotesRec int

	ActiveIn  []string
	TopPosts  []FeedMessage
	TopErrors error
}

func (s *Store) GetUserProfile(ctx context.Context, username string) (*UserProfile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, created_at
		FROM users
		WHERE lower(username) = lower(?)
	`, username)
	var uid, uname, createdAt string
	if err := row.Scan(&uid, &uname, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	ct, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	p := &UserProfile{
		UserID:    uid,
		Username:  uname,
		CreatedAt: ct,
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			SUM(CASE WHEN parent_id IS NULL THEN 1 ELSE 0 END) AS posts,
			SUM(CASE WHEN parent_id IS NOT NULL THEN 1 ELSE 0 END) AS replies
		FROM messages
		WHERE author_id = ?
	`, uid).Scan(&p.Posts, &p.Replies); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(upvote_count), 0)
		FROM messages
		WHERE author_id = ?
	`, uid).Scan(&p.UpvotesRec); err != nil {
		return nil, err
	}

	// Active in: communities posted to in last 30 days.
	since := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT c.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		WHERE m.author_id = ? AND m.created_at >= ?
		ORDER BY c.name ASC
		LIMIT 10
	`, uid, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		p.ActiveIn = append(p.ActiveIn, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Top posts: 5 most upvoted top-level messages.
	topRows, err := s.db.QueryContext(ctx, `
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
		WHERE m.author_id = ? AND m.parent_id IS NULL
		ORDER BY m.upvote_count DESC, m.created_at DESC
		LIMIT 5
	`, uid)
	if err != nil {
		return nil, err
	}
	defer topRows.Close()
	for topRows.Next() {
		var fm FeedMessage
		var createdAt string
		var parent sql.NullString
		var isDeleted int
		if err := topRows.Scan(
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
		p.TopPosts = append(p.TopPosts, fm)
	}
	if err := topRows.Err(); err != nil {
		return nil, err
	}

	return p, nil
}
