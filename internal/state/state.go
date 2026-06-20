// Package state provides a SQLite-backed repository for tracking processed files.
package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/logicminds/filemaid/internal/llm"

	_ "modernc.org/sqlite"
)

// Repo persists and queries file processing history and cached classification
// decisions.
type Repo interface {
	Record(original, final, sha256, category string, tags []string, action, reason string) error
	FindByHash(sha256 string) (*Record, error)
	FindDecisionByHash(sha256 string) (llm.Decision, bool, error)
	RecordDecision(sha256 string, decision llm.Decision) error
	Close() error
}

// Record is a single row from the history table.
type Record struct {
	ID           int64
	OriginalPath string
	FinalPath    string
	SHA256       string
	Category     string
	Tags         string
	Action       string
	Reason       string
	CreatedAt    string
}

// State is the SQLite-backed implementation of Repo.
type State struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY,
    original_path TEXT NOT NULL,
    final_path TEXT,
    sha256 TEXT,
    category TEXT,
    tags TEXT,
    action TEXT,
    reason TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sha256 ON history(sha256);
CREATE TABLE IF NOT EXISTS decisions (
    sha256 TEXT PRIMARY KEY,
    decision TEXT NOT NULL,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
`

// Open creates the parent directories for path, opens the SQLite database, and
// initializes the schema. Existing databases are opened without modification.
func Open(path string) (*State, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &State{db: db}, nil
}

// Record inserts a new history row.
func (s *State) Record(original, final, sha256, category string, tags []string, action, reason string) error {
	tagsStr := strings.Join(tags, ",")
	_, err := s.db.Exec(
		`INSERT INTO history (original_path, final_path, sha256, category, tags, action, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		original, final, sha256, category, tagsStr, action, reason,
	)
	if err != nil {
		return fmt.Errorf("insert history: %w", err)
	}
	return nil
}

// FindByHash returns the most recent history row matching sha256, or nil if none exists.
func (s *State) FindByHash(sha256 string) (*Record, error) {
	row := s.db.QueryRow(
		`SELECT id, original_path, final_path, sha256, category, tags, action, reason, created_at
		 FROM history
		 WHERE sha256 = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		sha256,
	)

	var r Record
	var finalPath, sha, category, tags, action, reason sql.NullString
	err := row.Scan(&r.ID, &r.OriginalPath, &finalPath, &sha, &category, &tags, &action, &reason, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find by hash: %w", err)
	}

	r.FinalPath = finalPath.String
	r.SHA256 = sha.String
	r.Category = category.String
	r.Tags = tags.String
	r.Action = action.String
	r.Reason = reason.String
	return &r, nil
}

// FindDecisionByHash returns a cached decision for sha256, if one exists.
func (s *State) FindDecisionByHash(sha256 string) (llm.Decision, bool, error) {
	row := s.db.QueryRow(
		`SELECT decision FROM decisions WHERE sha256 = ?`,
		sha256,
	)

	var raw string
	err := row.Scan(&raw)
	if err == sql.ErrNoRows {
		return llm.Decision{}, false, nil
	}
	if err != nil {
		return llm.Decision{}, false, fmt.Errorf("find decision by hash: %w", err)
	}

	var d llm.Decision
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return llm.Decision{}, false, fmt.Errorf("parse cached decision: %w", err)
	}
	return d, true, nil
}

// RecordDecision stores a decision keyed by sha256, replacing any existing
// entry for the same hash.
func (s *State) RecordDecision(sha256 string, decision llm.Decision) error {
	raw, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal decision: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO decisions (sha256, decision) VALUES (?, ?)
		 ON CONFLICT(sha256) DO UPDATE SET decision = excluded.decision, created_at = CURRENT_TIMESTAMP`,
		sha256, string(raw),
	)
	if err != nil {
		return fmt.Errorf("record decision: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *State) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
