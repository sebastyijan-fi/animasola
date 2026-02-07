package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) addMembership(ctx context.Context, userID, communityID, role string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memberships (user_id, community_id, role, joined_at)
		VALUES (?, ?, ?, ?)
	`, userID, communityID, role, now)
	return err
}

func (s *Store) JoinCommunity(ctx context.Context, userID, communityID string) error {
	return s.addMembership(ctx, userID, communityID, "member")
}

func (s *Store) LeaveCommunity(ctx context.Context, userID, communityID string) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM memberships
		WHERE user_id = ? AND community_id = ?
	`, userID, communityID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
