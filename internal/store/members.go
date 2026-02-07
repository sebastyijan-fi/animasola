package store

import (
	"context"
)

func (s *Store) ListCommunityMembers(ctx context.Context, communityID string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.username
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.community_id = ?
		ORDER BY u.username ASC
		LIMIT ?
	`, communityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
