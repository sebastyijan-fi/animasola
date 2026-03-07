package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

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
	dbPath    string
	syncQueue chan syncJob
	ctx       context.Context
	cancel    context.CancelFunc
}

func Open(dbPath string) (*Store, error) {
	db, err := openSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	store := &Store{
		db:        db,
		dbPath:    dbPath,
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
				INSERT INTO messages (id, room_id, author_id, author_username, content, created_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING
			`, job.m.ID, job.m.RoomID, job.m.AuthorID, job.m.AuthorUsername, job.m.Content, createdAtStr)
			if err != nil {
				fmt.Printf("Warning: failed to sync message asynchronously: %v\n", err)
				continue
			}
			if err := s.enforceRoomMessageLimit(job.ctx, job.m.RoomID); err != nil {
				fmt.Printf("Warning: failed to enforce room message cap: %v\n", err)
			}
		}
	}
}

func (s *Store) Migrate(ctx context.Context) error {
	if err := s.migrateOnce(ctx); err != nil {
		if resetErr := s.resetDatabase(ctx); resetErr != nil {
			return fmt.Errorf("migrate failed: %v; reset failed: %w", err, resetErr)
		}
		if err := s.migrateOnce(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateOnce(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schemaSQL)
	if err != nil {
		return fmt.Errorf("exec schema: %w", err)
	}
	// Simple migrations for Alpha: add new columns if they are missing.
	// We ignore errors here because they will error if the column already exists.
	// In a real production app, we would use a proper migration tool/table.
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN is_private BOOLEAN DEFAULT 0")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN room_key TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN creator_id TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN signature TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN last_seen_at TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN updated_at TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN version INTEGER DEFAULT 1")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE rooms ADD COLUMN announce_count INTEGER DEFAULT 0")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE memberships ADD COLUMN last_read_at TEXT")
	_, _ = s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS public_room_index (room_id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, creator_id TEXT, signature TEXT, created_at TEXT NOT NULL, updated_at TEXT, last_seen_at TEXT, version INTEGER DEFAULT 1, announce_count INTEGER DEFAULT 0)")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE public_room_index ADD COLUMN updated_at TEXT")
	_, _ = s.db.ExecContext(ctx, "ALTER TABLE public_room_index ADD COLUMN version INTEGER DEFAULT 1")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_messages_room_created_at ON messages(room_id, created_at)")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_memberships_user_room ON memberships(user_id, room_id)")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_memberships_room_user ON memberships(room_id, user_id)")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_rooms_public_last_seen ON rooms(is_private, last_seen_at, name)")
	_, _ = s.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_public_room_index_last_seen ON public_room_index(last_seen_at, name)")

	if err := s.migrateRoomsNameUniqueness(ctx); err != nil {
		return err
	}
	if err := s.backfillPublicRoomIndex(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) resetDatabase(ctx context.Context) error {
	_ = ctx
	s.cancel()
	if err := s.db.Close(); err != nil {
		return err
	}

	backupPath := fmt.Sprintf("%s.reset-%s.bak", s.dbPath, time.Now().UTC().Format("20060102T150405"))
	if err := archiveSQLiteArtifact(s.dbPath, backupPath); err != nil {
		return err
	}
	if err := archiveSQLiteArtifact(s.dbPath+"-wal", backupPath+"-wal"); err != nil {
		return err
	}
	if err := archiveSQLiteArtifact(s.dbPath+"-shm", backupPath+"-shm"); err != nil {
		return err
	}

	db, err := openSQLiteDB(s.dbPath)
	if err != nil {
		return err
	}
	newCtx, cancel := context.WithCancel(context.Background())
	s.db = db
	s.ctx = newCtx
	s.cancel = cancel
	s.syncQueue = make(chan syncJob, 10000)
	go s.processSyncQueue()
	return nil
}

func archiveSQLiteArtifact(srcPath, dstPath string) error {
	if err := os.Rename(srcPath, dstPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func openSQLiteDB(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}

func (s *Store) migrateRoomsNameUniqueness(ctx context.Context) error {
	var createSQL string
	err := s.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'rooms'").Scan(&createSQL)
	if err != nil {
		return fmt.Errorf("read rooms schema: %w", err)
	}

	if !strings.Contains(createSQL, "name TEXT NOT NULL UNIQUE") {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys for rooms migration: %w", err)
	}
	defer s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rooms migration tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE rooms_new (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			creator_id TEXT,
			signature TEXT,
			is_private BOOLEAN DEFAULT 0,
			room_key TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT,
			last_seen_at TEXT,
			version INTEGER DEFAULT 1,
			announce_count INTEGER DEFAULT 0
		)
	`); err != nil {
		return fmt.Errorf("create replacement rooms table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rooms_new (id, name, description, creator_id, signature, is_private, room_key, created_at, updated_at, last_seen_at, version, announce_count)
		SELECT id, name, description, creator_id, signature, COALESCE(is_private, 0), room_key, created_at, COALESCE(updated_at, created_at), COALESCE(last_seen_at, created_at), COALESCE(version, 1), COALESCE(announce_count, 0)
		FROM rooms
	`); err != nil {
		return fmt.Errorf("copy rooms data: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DROP TABLE rooms`); err != nil {
		return fmt.Errorf("drop legacy rooms table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE rooms_new RENAME TO rooms`); err != nil {
		return fmt.Errorf("rename replacement rooms table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_rooms_public_last_seen ON rooms(is_private, last_seen_at, name)`); err != nil {
		return fmt.Errorf("recreate rooms indexes: %w", err)
	}

	return tx.Commit()
}

func (s *Store) backfillPublicRoomIndex(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO public_room_index (room_id, name, description, creator_id, signature, created_at, updated_at, last_seen_at, version, announce_count)
		SELECT id, name, description, creator_id, signature, created_at, COALESCE(updated_at, created_at), COALESCE(last_seen_at, created_at), COALESCE(version, 1), COALESCE(announce_count, 0)
		FROM rooms
		WHERE is_private = 0
		ON CONFLICT(room_id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			creator_id = COALESCE(excluded.creator_id, public_room_index.creator_id),
			signature = COALESCE(excluded.signature, public_room_index.signature),
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			last_seen_at = excluded.last_seen_at,
			version = MAX(public_room_index.version, excluded.version),
			announce_count = MAX(public_room_index.announce_count, excluded.announce_count)
	`)
	if err != nil {
		return fmt.Errorf("backfill public room index: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	s.cancel()
	return s.db.Close()
}
