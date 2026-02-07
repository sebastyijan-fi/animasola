package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"animasola/internal/id"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	// modernc sqlite uses "file:" DSNs; keep it simple for local paths.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate(migrationsDir string) error {
	// If this is an existing DB created by an older dev schema (no migrations table),
	// fail fast with a clear error. v1 is still pre-release so we prefer a manual reset.
	hasMig, err := s.tableExists("schema_migrations")
	if err != nil {
		return err
	}
	if !hasMig {
		hasUsers, err := s.tableExists("users")
		if err != nil {
			return err
		}
		if hasUsers {
			return fmt.Errorf("incompatible database schema (pre-migrations); delete the sqlite file and restart")
		}
	}

	if err := s.ensureMigrationsTable(); err != nil {
		return err
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}

	type mf struct {
		name string
		path string
	}
	var files []mf
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fn := e.Name()
		if strings.HasSuffix(fn, ".sql") {
			files = append(files, mf{name: fn, path: filepath.Join(migrationsDir, fn)})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	applied, err := s.appliedMigrations()
	if err != nil {
		return err
	}

	for _, f := range files {
		if applied[f.name] {
			continue
		}

		b, err := os.ReadFile(f.path)
		if err != nil {
			return err
		}

		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(b)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", f.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, f.name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s: %w", f.name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureMigrationsTable() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT (CURRENT_TIMESTAMP)
		);
	`)
	return err
}

func (s *Store) appliedMigrations() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) tableExists(name string) (bool, error) {
	row := s.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, name)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type User struct {
	ID           string
	Username     string
	Fingerprint  string
	PublicKey    string
	CreatedAtUTC string
}

func (s *Store) GetUserByFingerprint(ctx context.Context, fp string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, key_fingerprint, public_key, created_at
		FROM users
		WHERE key_fingerprint = ?
	`, fp)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.Fingerprint, &u.PublicKey, &u.CreatedAtUTC); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, username, fp, publicKey string) (*User, error) {
	uid, err := id.New()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, key_fingerprint, public_key, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, uid, username, fp, publicKey, now)
	if err != nil {
		return nil, err
	}

	return &User{
		ID:           uid,
		Username:     username,
		Fingerprint:  fp,
		PublicKey:    publicKey,
		CreatedAtUTC: now,
	}, nil
}
