package store

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"
)

func (s *Store) ListHomeFeed(ctx context.Context, userID string, sortMode SortMode, topRange TopRange, limit, offset int) ([]FeedMessage, error) {
	switch sortMode {
	case SortNew:
		return s.listHomeNew(ctx, userID, limit, offset)
	case SortTop:
		return s.listHomeTop(ctx, userID, topRange, limit, offset)
	case SortHot:
		return s.listHomeHot(ctx, userID, time.Now().UTC(), limit, offset)
	default:
		return s.listHomeNew(ctx, userID, limit, offset)
	}
}

func (s *Store) ListHomeNewPage(ctx context.Context, userID string, limit int, beforeID *string) ([]FeedMessage, *string, error) {
	var args []any
	args = append(args, userID)

	q := `
		SELECT
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE m.parent_id IS NULL
	`
	if beforeID != nil {
		q += ` AND m.id < ?`
		args = append(args, *beforeID)
	}
	q += ` ORDER BY m.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items, err := scanFeedRows(rows)
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

type HomeTopCursor struct {
	Upvotes int
	ID      string
}

func (s *Store) ListHomeTopPage(ctx context.Context, userID string, topRange TopRange, limit int, before *HomeTopCursor) ([]FeedMessage, *HomeTopCursor, error) {
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
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE m.parent_id IS NULL
	`
	var args []any
	args = append(args, userID)
	if since != nil {
		q += ` AND m.created_at >= ?`
		args = append(args, since.Format(time.RFC3339Nano))
	}
	if before != nil {
		q += ` AND (m.upvote_count < ? OR (m.upvote_count = ? AND m.id < ?))`
		args = append(args, before.Upvotes, before.Upvotes, before.ID)
	}
	q += ` ORDER BY m.upvote_count DESC, m.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items, err := scanFeedRows(rows)
	if err != nil {
		return nil, nil, err
	}
	var next *HomeTopCursor
	if len(items) == limit {
		last := items[len(items)-1]
		next = &HomeTopCursor{Upvotes: last.Upvotes, ID: last.ID}
	}
	return items, next, nil
}

type HomeHotCursor struct {
	Score float64
	ID    string
}

func (s *Store) ListHomeHotPage(ctx context.Context, userID string, now time.Time, limit int, before *HomeHotCursor) ([]FeedMessage, *HomeHotCursor, error) {
	// Hot score is computed in Go for portability. We fetch a candidate set and sort.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE m.parent_id IS NULL
		ORDER BY m.id DESC
		LIMIT 500
	`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items, err := scanFeedRows(rows)
	if err != nil {
		return nil, nil, err
	}

	type scored struct {
		m     FeedMessage
		score float64
	}
	scoredItems := make([]scored, 0, len(items))
	for _, it := range items {
		age := now.Sub(it.CreatedAt).Hours()
		den := pow(age+2, 1.5)
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

	var next *HomeHotCursor
	if len(out) == limit {
		last := scoredItems[start+limit-1]
		next = &HomeHotCursor{Score: last.score, ID: last.m.ID}
	}
	return out, next, nil
}

func (s *Store) listHomeNew(ctx context.Context, userID string, limit, offset int) ([]FeedMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE m.parent_id IS NULL
		ORDER BY m.id DESC
		LIMIT ? OFFSET ?
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedRows(rows)
}

func (s *Store) listHomeTop(ctx context.Context, userID string, topRange TopRange, limit, offset int) ([]FeedMessage, error) {
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
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE m.parent_id IS NULL
	`
	var args []any
	args = append(args, userID)
	if since != nil {
		q += ` AND m.created_at >= ?`
		args = append(args, since.Format(time.RFC3339Nano))
	}
	q += ` ORDER BY m.upvote_count DESC, m.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedRows(rows)
}

func (s *Store) listHomeHot(ctx context.Context, userID string, now time.Time, limit, offset int) ([]FeedMessage, error) {
	// Hot score is computed in Go for portability. We fetch a candidate set and sort.
	// This is good enough for v1 and keeps SQL simple across SQLite builds.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.room_id, r.community_id, m.author_id, m.content, m.parent_id, m.upvote_count, m.reply_count, m.is_deleted, m.created_at,
			u.username, c.name, r.name
		FROM messages m
		JOIN rooms r ON r.id = m.room_id
		JOIN communities c ON c.id = r.community_id
		JOIN memberships ms ON ms.community_id = c.id AND ms.user_id = ?
		JOIN users u ON u.id = m.author_id
		WHERE m.parent_id IS NULL
		ORDER BY m.id DESC
		LIMIT 500
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items, err := scanFeedRows(rows)
	if err != nil {
		return nil, err
	}

	type scored struct {
		m     FeedMessage
		score float64
	}
	scoredItems := make([]scored, 0, len(items))
	for _, it := range items {
		age := now.Sub(it.CreatedAt).Hours()
		den := pow(age+2, 1.5)
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

	if offset >= len(scoredItems) {
		return nil, nil
	}
	end := offset + limit
	if end > len(scoredItems) {
		end = len(scoredItems)
	}
	out := make([]FeedMessage, 0, end-offset)
	for _, s := range scoredItems[offset:end] {
		out = append(out, s.m)
	}
	return out, nil
}

func scanFeedRows(rows *sql.Rows) ([]FeedMessage, error) {
	var out []FeedMessage
	for rows.Next() {
		var fm FeedMessage
		var parent sql.NullString
		var createdAt string
		var isDeleted int
		if err := rows.Scan(
			&fm.ID, &fm.RoomID, &fm.CommunityID, &fm.AuthorID, &fm.Content, &parent, &fm.Upvotes, &fm.Replies, &isDeleted, &createdAt,
			&fm.AuthorUsername, &fm.CommunityName, &fm.RoomName,
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

func pow(x, p float64) float64 {
	// tiny local helper to avoid pulling in more deps; math.Pow is fine.
	// Kept as a wrapper to make future fast-paths easy.
	return math.Pow(x, p)
}
