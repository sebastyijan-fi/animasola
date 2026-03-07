package main

import (
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func newJSONBody(body string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(body))
}

func newTestRegistryDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := initDB(db); err != nil {
		t.Fatalf("init registry db: %v", err)
	}
	return db
}

func testUsername(i int) string {
	return fmt.Sprintf("testuser%d", i)
}

func testPeerID(i int) string {
	return fmt.Sprintf("peer-%d", i)
}

func securityCounterCount(t *testing.T, db *sql.DB, source, category string) int {
	t.Helper()

	var count int
	if err := db.QueryRow(`SELECT count FROM security_counters WHERE source = ? AND category = ?`, source, category).Scan(&count); err != nil {
		t.Fatalf("query security counter: %v", err)
	}
	return count
}
