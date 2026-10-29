// Package store is the panel's SQLite database (ADR 0004): embedded
// migrations, background jobs, workspaces and the projects found in them.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
)

var (
	// ErrNotFound is returned when a row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrAlreadyAdded is returned when a workspace path is already stored.
	ErrAlreadyAdded = errors.New("store: workspace already added")
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the panel database. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens the database at path, creating its directory (mode 0700) when
// missing, applies pending migrations, and marks jobs left running by a
// previous process as interrupted.
//
// The pragmas are in the DSN so that every pooled connection has them; a
// PRAGMA statement would only affect one. _txlock=immediate takes the write
// lock when a transaction begins, so a read-then-write transaction never fails
// halfway with SQLITE_BUSY_SNAPSHOT.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data directory: %w", err)
	}
	u := url.URL{Scheme: "file", Path: abs}
	u.RawQuery = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.interruptRunningJobs(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrate applies the embedded migrations that schema_migrations does not list
// yet, in version order, each in its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	type migration struct {
		version int
		name    string
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		prefix, _, _ := strings.Cut(name, "_")
		v, err := strconv.Atoi(prefix)
		if err != nil || !strings.HasSuffix(name, ".sql") {
			return fmt.Errorf("store: bad migration file name %q", name)
		}
		ms = append(ms, migration{v, name})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })

	for _, m := range ms {
		var applied int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&applied); err != nil {
			return fmt.Errorf("store: read schema_migrations: %w", err)
		}
		if applied > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", m.name, err)
		}
		if err := s.applyMigration(ctx, m.version, string(body)); err != nil {
			return fmt.Errorf("store: apply %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, version int, body string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, formatTime(time.Now())); err != nil {
		return err
	}
	return tx.Commit()
}

// formatTime renders a time as UTC RFC 3339 text.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// isUnique reports whether err is a UNIQUE constraint failure on column
// ("table.column").
func isUnique(err error, column string) bool {
	const sqliteConstraintUnique = 2067
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqliteConstraintUnique && strings.Contains(se.Error(), column)
}
