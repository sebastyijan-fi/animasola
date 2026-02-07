package store

import (
	"context"
	"database/sql"
)

func (s *Store) DeleteRoom(ctx context.Context, roomID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE id = ?`, roomID)
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

func (s *Store) DeleteCommunity(ctx context.Context, communityID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM communities WHERE id = ?`, communityID)
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
