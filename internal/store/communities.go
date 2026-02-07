package store

import (
	"context"
	"database/sql"
	"time"

	"animasola/internal/id"
)

func (s *Store) CreateCommunity(ctx context.Context, createdByUserID, name, description string) (*Community, *Room, error) {
	cid, err := id.New()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO communities (id, name, description, created_by, created_at, member_count)
		VALUES (?, ?, ?, ?, ?, 0)
	`, cid, name, description, createdByUserID, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, nil, err
	}

	// Default room: #general stored as "general".
	r, err := s.CreateRoom(ctx, cid, "general")
	if err != nil {
		return nil, nil, err
	}

	// Creator becomes admin member (member_count updated by trigger).
	if err := s.addMembership(ctx, createdByUserID, cid, "admin"); err != nil {
		return nil, nil, err
	}

	c, err := s.GetCommunityByID(ctx, cid)
	if err != nil {
		return nil, nil, err
	}
	return c, r, nil
}

func (s *Store) GetCommunityByID(ctx context.Context, id string) (*Community, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, created_by, created_at, member_count
		FROM communities
		WHERE id = ?
	`, id)
	var c Community
	var createdAt string
	if err := row.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedBy, &createdAt, &c.MemberCount); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	return &c, nil
}

func (s *Store) GetCommunityByName(ctx context.Context, name string) (*Community, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, created_by, created_at, member_count
		FROM communities
		WHERE name = ?
	`, name)
	var c Community
	var createdAt string
	if err := row.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedBy, &createdAt, &c.MemberCount); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	return &c, nil
}

func (s *Store) ListJoinedCommunities(ctx context.Context, userID string) ([]Community, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.description, c.created_by, c.created_at, c.member_count
		FROM communities c
		JOIN memberships m ON m.community_id = c.id
		WHERE m.user_id = ?
		ORDER BY c.name ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Community
	for rows.Next() {
		var c Community
		var createdAt string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedBy, &createdAt, &c.MemberCount); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		c.CreatedAt = t
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListExploreCommunities(ctx context.Context, userID string, limit int) ([]Community, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.description, c.created_by, c.created_at, c.member_count
		FROM communities c
		WHERE NOT EXISTS (
			SELECT 1 FROM memberships m
			WHERE m.user_id = ? AND m.community_id = c.id
		)
		ORDER BY c.member_count DESC, c.name ASC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Community
	for rows.Next() {
		var c Community
		var createdAt string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedBy, &createdAt, &c.MemberCount); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		c.CreatedAt = t
		out = append(out, c)
	}
	return out, rows.Err()
}
