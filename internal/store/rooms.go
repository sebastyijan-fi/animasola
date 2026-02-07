package store

import (
	"context"
	"database/sql"
	"time"

	"animasola/internal/id"
)

func (s *Store) CreateRoom(ctx context.Context, communityID, name string) (*Room, error) {
	rid, err := id.New()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO rooms (id, name, community_id, pinned_message_id, created_at)
		VALUES (?, ?, ?, NULL, ?)
	`, rid, name, communityID, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}

	return &Room{
		ID:          rid,
		Name:        name,
		CommunityID: communityID,
		CreatedAt:   now,
	}, nil
}

func (s *Store) GetRoomByID(ctx context.Context, id string) (*Room, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, community_id, pinned_message_id, created_at
		FROM rooms
		WHERE id = ?
	`, id)
	var r Room
	var pinned sql.NullString
	var createdAt string
	if err := row.Scan(&r.ID, &r.Name, &r.CommunityID, &pinned, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if pinned.Valid {
		r.PinnedMessageID = &pinned.String
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = t
	return &r, nil
}

func (s *Store) GetRoomByName(ctx context.Context, communityID, name string) (*Room, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, community_id, pinned_message_id, created_at
		FROM rooms
		WHERE community_id = ? AND name = ?
	`, communityID, name)
	var r Room
	var pinned sql.NullString
	var createdAt string
	if err := row.Scan(&r.ID, &r.Name, &r.CommunityID, &pinned, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if pinned.Valid {
		r.PinnedMessageID = &pinned.String
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = t
	return &r, nil
}

func (s *Store) ListRoomsByCommunity(ctx context.Context, communityID string) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, community_id, pinned_message_id, created_at
		FROM rooms
		WHERE community_id = ?
		ORDER BY name ASC
	`, communityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Room
	for rows.Next() {
		var r Room
		var pinned sql.NullString
		var createdAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.CommunityID, &pinned, &createdAt); err != nil {
			return nil, err
		}
		if pinned.Valid {
			r.PinnedMessageID = &pinned.String
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		r.CreatedAt = t
		out = append(out, r)
	}
	return out, rows.Err()
}
