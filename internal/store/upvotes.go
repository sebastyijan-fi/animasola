package store

import (
	"context"
	"database/sql"
	"time"
)

type UpvoteState struct {
	Upvoted bool
	Count   int
}

func (s *Store) ToggleUpvote(ctx context.Context, userID, messageID string) (*UpvoteState, error) {
	// Find author to enforce "cannot upvote your own message" (silently ignored per spec).
	row := s.db.QueryRowContext(ctx, `SELECT author_id, upvote_count FROM messages WHERE id = ?`, messageID)
	var authorID string
	var count int
	if err := row.Scan(&authorID, &count); err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	if authorID == userID {
		return &UpvoteState{Upvoted: false, Count: count}, nil
	}

	// Toggle by attempting insert; on conflict delete.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO upvotes (user_id, message_id, created_at)
		VALUES (?, ?, ?)
	`, userID, messageID, now)
	if err == nil {
		// Trigger bumped count; re-read.
		return s.getUpvoteState(ctx, userID, messageID)
	}

	// If insert failed, only toggle off if it already exists. Otherwise return the real error.
	row = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upvotes WHERE user_id = ? AND message_id = ?)`, userID, messageID)
	var exists int
	if err2 := row.Scan(&exists); err2 != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, err
	}

	// Toggle off.
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM upvotes
		WHERE user_id = ? AND message_id = ?
	`, userID, messageID)
	if err != nil {
		return nil, err
	}
	return s.getUpvoteState(ctx, userID, messageID)
}

func (s *Store) getUpvoteState(ctx context.Context, userID, messageID string) (*UpvoteState, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM upvotes WHERE user_id = ? AND message_id = ?) AS upvoted,
			(SELECT upvote_count FROM messages WHERE id = ?) AS cnt
	`, userID, messageID, messageID)
	var upvoted int
	var cnt sql.NullInt64
	if err := row.Scan(&upvoted, &cnt); err != nil {
		return nil, err
	}
	if !cnt.Valid {
		return nil, sql.ErrNoRows
	}
	return &UpvoteState{Upvoted: upvoted != 0, Count: int(cnt.Int64)}, nil
}
