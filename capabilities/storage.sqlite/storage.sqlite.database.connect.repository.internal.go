package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed storage.sqlite.schema.define.utility.internal.sql
var schemaSQL string

type syncJob struct {
	ctx context.Context
	m   *Message
}

type Store struct {
	db        *sql.DB
	syncQueue chan syncJob
	ctx       context.Context
	cancel    context.CancelFunc
}

func Open(dbPath string) (*Store, error) {
	// Enable foreign keys, WAL mode, and a busy timeout for better concurrency/safety
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	store := &Store{
		db:        db,
		syncQueue: make(chan syncJob, 10000), // Buffer against DHT floods
		ctx:       ctx,
		cancel:    cancel,
	}

	go store.processSyncQueue()
	return store, nil
}

func (s *Store) processSyncQueue() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.syncQueue:
			createdAtStr := job.m.CreatedAt.Format(SortableTimeFormat)
			_, err := s.db.ExecContext(job.ctx, `
				INSERT INTO messages (id, room_id, author_id, content, created_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING
			`, job.m.ID, job.m.RoomID, job.m.AuthorID, job.m.Content, createdAtStr)
			if err != nil {
				fmt.Printf("Warning: failed to sync message asynchronously: %v\n", err)
			}
		}
	}
}

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaSQL)
	if err != nil {
		return fmt.Errorf("exec schema: %w", err)
	}

	// Simple migrations for Alpha: add new columns if they are missing.
	// We ignore errors here because they will error if the column already exists.
	// In a real production app, we would use a proper migration tool/table.
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN is_private BOOLEAN DEFAULT 0")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN room_key TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE memberships ADD COLUMN last_read_at TEXT")

	return nil
}

func (s *Store) Close() error {
	s.cancel()
	return s.db.Close()
}
