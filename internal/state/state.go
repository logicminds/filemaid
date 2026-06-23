// Package state provides a SQLite-backed repository for tracking processed files.
package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/logicminds/filemaid/internal/llm"

	_ "modernc.org/sqlite"
)

type Repo interface {
	Record(input RecordInput) error
	FindByHash(sha256 string) (*Record, error)
	FindDuplicatesByHash(sha256 string) ([]Record, error)
	FindSimilarByFingerprints(perceptualHash, avSignature, textSignature string) ([]Record, error)
	DistinctTags() ([]string, error)
	FindDecisionByHash(sha256 string) (llm.Decision, bool, error)
	RecordDecision(sha256 string, decision llm.Decision) error
	FindDirectoryDecision(key string) (llm.DirectoryDecision, bool, error)
	RecordDirectoryDecision(key string, decision llm.DirectoryDecision) error
	History(limit int, runID string) ([]Record, error)
	HistoryByRunID(runID string) ([]Record, error)
	HistoryByFinalPath(finalPath string) (*Record, error)
	LastRunID() (string, error)
	Close() error
}

// RecordInput holds all data persisted by Record.
type RecordInput struct {
	OriginalPath   string
	FinalPath      string
	SHA256         string
	Category       string
	Tags           []string
	Action         string
	Reason         string
	RunID          string
	Metrics        llm.Metrics
	OriginalName   string
	NewName        string
	NameQuality    float64
	MediaKind      string
	PerceptualHash string
	AVSignature    string
	TextSignature  string
	HashAlgorithm  string
}

// Record is a single row from the history table.
type Record struct {
	ID               int64
	OriginalPath     string
	FinalPath        string
	SHA256           string
	Category         string
	Tags             string
	Action           string
	Reason           string
	CreatedAt        string
	RunID            sql.NullString
	LLMDurationMs    sql.NullInt64
	PromptTokens     sql.NullInt64
	CompletionTokens sql.NullInt64
	TotalTokens      sql.NullInt64
	TokensPerSec     sql.NullFloat64
	ContextSize      sql.NullInt64
	OriginalName     string
	NewName          string
	NameQuality      sql.NullFloat64
	MediaKind        string
	PerceptualHash   string
	AVSignature      string
	TextSignature    string
	HashAlgorithm    string
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
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    original_name TEXT,
    new_name TEXT,
    name_quality REAL,
    media_kind TEXT,
    perceptual_hash TEXT,
    av_signature TEXT,
    text_signature TEXT,
    hash_algorithm TEXT
);
CREATE TABLE IF NOT EXISTS decisions (
    sha256 TEXT PRIMARY KEY,
    decision TEXT NOT NULL,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS directory_decisions (
    key TEXT PRIMARY KEY,
    decision TEXT NOT NULL,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
`

var historyIndexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_sha256 ON history(sha256)",
	"CREATE INDEX IF NOT EXISTS idx_perceptual_hash ON history(perceptual_hash)",
	"CREATE INDEX IF NOT EXISTS idx_av_signature ON history(av_signature)",
	"CREATE INDEX IF NOT EXISTS idx_text_signature ON history(text_signature)",
}

var historyColumns = []string{
	"run_id TEXT",
	"llm_duration_ms INTEGER",
	"prompt_tokens INTEGER",
	"completion_tokens INTEGER",
	"total_tokens INTEGER",
	"tokens_per_sec REAL",
	"context_size INTEGER",
	"original_name TEXT",
	"new_name TEXT",
	"name_quality REAL",
	"media_kind TEXT",
	"perceptual_hash TEXT",
	"av_signature TEXT",
	"text_signature TEXT",
	"hash_algorithm TEXT",
}

const historySelectColumns = `id, original_path, final_path, sha256, category, tags, action, reason, created_at, run_id, llm_duration_ms, prompt_tokens, completion_tokens, total_tokens, tokens_per_sec, context_size, original_name, new_name, name_quality, media_kind, perceptual_hash, av_signature, text_signature, hash_algorithm`

func scanRecord(row interface {
	Scan(dest ...interface{}) error
}) (*Record, error) {
	var r Record
	var finalPath, sha, category, tags, action, reason sql.NullString
	var origName, newName, mediaKind, pHash, avSig, textSig, hashAlgo sql.NullString
	err := row.Scan(
		&r.ID, &r.OriginalPath, &finalPath, &sha, &category, &tags, &action, &reason, &r.CreatedAt,
		&r.RunID, &r.LLMDurationMs, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.TokensPerSec, &r.ContextSize,
		&origName, &newName, &r.NameQuality, &mediaKind, &pHash, &avSig, &textSig, &hashAlgo,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.FinalPath = finalPath.String
	r.SHA256 = sha.String
	r.Category = category.String
	r.Tags = tags.String
	r.Action = action.String
	r.Reason = reason.String
	r.OriginalName = origName.String
	r.NewName = newName.String
	r.MediaKind = mediaKind.String
	r.PerceptualHash = pHash.String
	r.AVSignature = avSig.String
	r.TextSignature = textSig.String
	r.HashAlgorithm = hashAlgo.String
	return &r, nil
}

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
	enableWAL(db)

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	if err := migrateHistoryColumns(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate history columns: %w", err)
	}

	if err := migrateIndexes(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate indexes: %w", err)
	}

	return &State{db: db}, nil
}

// migrateIndexes creates any missing history indexes. It runs after column
// migrations so that indexes on added columns do not fail on legacy databases.
func migrateIndexes(db *sql.DB) error {
	for _, stmt := range historyIndexes {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// enableWAL attempts to set the SQLite journal mode to WAL. If the database
// engine does not support WAL or refuses the change, it logs a warning and
// returns so that Open can continue normally.
func enableWAL(db *sql.DB) {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		slog.Warn("sqlite WAL mode query failed", "error", err)
		return
	}
	if mode != "wal" {
		slog.Warn("sqlite WAL mode not supported or enabled", "mode", mode)
	}
}

// migrateHistoryColumns adds any missing metric/run columns to existing databases.
func migrateHistoryColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(history)`)
	if err != nil {
		return fmt.Errorf("read table info: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan table info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table info: %w", err)
	}

	for _, col := range historyColumns {
		parts := strings.SplitN(col, " ", 2)
		name := parts[0]
		if existing[name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE history ADD COLUMN %s %s", name, parts[1])); err != nil {
			return fmt.Errorf("add column %s: %w", name, err)
		}
	}
	return nil
}

// Record inserts a new history row.
func (s *State) Record(input RecordInput) error {
	tagsStr := strings.Join(input.Tags, ",")
	_, err := s.db.Exec(
		`INSERT INTO history (original_path, final_path, sha256, category, tags, action, reason, run_id, llm_duration_ms, prompt_tokens, completion_tokens, total_tokens, tokens_per_sec, context_size, original_name, new_name, name_quality, media_kind, perceptual_hash, av_signature, text_signature, hash_algorithm)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.OriginalPath, input.FinalPath, input.SHA256, input.Category, tagsStr, input.Action, input.Reason, input.RunID,
		input.Metrics.DurationMs, input.Metrics.PromptTokens, input.Metrics.CompletionTokens, input.Metrics.TotalTokens, input.Metrics.TokensPerSec, input.Metrics.ContextSize,
		input.OriginalName, input.NewName, input.NameQuality, input.MediaKind, input.PerceptualHash, input.AVSignature, input.TextSignature, input.HashAlgorithm,
	)
	if err != nil {
		return fmt.Errorf("insert history: %w", err)
	}
	return nil
}

// FindByHash returns the most recent history row matching sha256, or nil if none exists.
func (s *State) FindByHash(sha256 string) (*Record, error) {
	row := s.db.QueryRow(
		`SELECT `+historySelectColumns+
			` FROM history
			 WHERE sha256 = ?
			 ORDER BY created_at DESC, id DESC
			 LIMIT 1`,
		sha256,
	)

	r, err := scanRecord(row)
	if err != nil {
		return nil, fmt.Errorf("find by hash: %w", err)
	}
	return r, nil
}

// DistinctTags returns all unique tags stored across history rows, excluding the
// reserved 'filemaid' tag and any empty or whitespace-only values.
func (s *State) DistinctTags() ([]string, error) {
	rows, err := s.db.Query("SELECT tags FROM history")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	var tags sql.NullString
	for rows.Next() {
		if err := rows.Scan(&tags); err != nil {
			return nil, err
		}
		if !tags.Valid || tags.String == "" {
			continue
		}
		for _, t := range strings.Split(tags.String, ",") {
			t = strings.TrimSpace(t)
			if t == "" || t == "filemaid" {
				continue
			}
			seen[t] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// History returns up to limit history rows ordered by most recent first.
// If runID is non-empty, only rows for that run are returned.
func (s *State) History(limit int, runID string) ([]Record, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if runID != "" {
		rows, err = s.db.Query(
			`SELECT `+historySelectColumns+
				` FROM history WHERE run_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
			runID, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT `+historySelectColumns+
				` FROM history ORDER BY created_at DESC, id DESC LIMIT ?`,
			limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var finalPath, sha, category, tags, action, reason sql.NullString
		var origName, newName, mediaKind, pHash, avSig, textSig, hashAlgo sql.NullString
		if err := rows.Scan(
			&r.ID, &r.OriginalPath, &finalPath, &sha, &category, &tags, &action, &reason, &r.CreatedAt,
			&r.RunID, &r.LLMDurationMs, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.TokensPerSec, &r.ContextSize,
			&origName, &newName, &r.NameQuality, &mediaKind, &pHash, &avSig, &textSig, &hashAlgo,
		); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		r.FinalPath = finalPath.String
		r.SHA256 = sha.String
		r.Category = category.String
		r.Tags = tags.String
		r.Action = action.String
		r.Reason = reason.String
		r.OriginalName = origName.String
		r.NewName = newName.String
		r.MediaKind = mediaKind.String
		r.PerceptualHash = pHash.String
		r.AVSignature = avSig.String
		r.TextSignature = textSig.String
		r.HashAlgorithm = hashAlgo.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}
	return out, nil
}

// HistoryByRunID returns all history rows for the given run, excluding undo rows.
func (s *State) HistoryByRunID(runID string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT `+historySelectColumns+
			` FROM history WHERE run_id = ? AND action != 'undo' ORDER BY created_at DESC, id DESC`,
		runID)
	if err != nil {
		return nil, fmt.Errorf("query history by run id: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var finalPath, sha, category, tags, action, reason sql.NullString
		var origName, newName, mediaKind, pHash, avSig, textSig, hashAlgo sql.NullString
		if err := rows.Scan(
			&r.ID, &r.OriginalPath, &finalPath, &sha, &category, &tags, &action, &reason, &r.CreatedAt,
			&r.RunID, &r.LLMDurationMs, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &r.TokensPerSec, &r.ContextSize,
			&origName, &newName, &r.NameQuality, &mediaKind, &pHash, &avSig, &textSig, &hashAlgo,
		); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		r.FinalPath = finalPath.String
		r.SHA256 = sha.String
		r.Category = category.String
		r.Tags = tags.String
		r.Action = action.String
		r.Reason = reason.String
		r.OriginalName = origName.String
		r.NewName = newName.String
		r.MediaKind = mediaKind.String
		r.PerceptualHash = pHash.String
		r.AVSignature = avSig.String
		r.TextSignature = textSig.String
		r.HashAlgorithm = hashAlgo.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}
	return out, nil
}

// HistoryByFinalPath returns the most recent history row with the given final path,
// excluding undo rows.
func (s *State) HistoryByFinalPath(finalPath string) (*Record, error) {
	row := s.db.QueryRow(
		`SELECT `+historySelectColumns+
			` FROM history WHERE final_path = ? AND action != 'undo' ORDER BY created_at DESC, id DESC LIMIT 1`,
		finalPath)
	return scanRecord(row)
}

// LastRunID returns the most recent run_id that has move actions, ignoring undo
// rows and trashed items.
func (s *State) LastRunID() (string, error) {
	var runID sql.NullString
	err := s.db.QueryRow(
		`SELECT run_id FROM history WHERE action != 'undo' AND final_path != 'trash' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&runID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query last run id: %w", err)
	}
	return runID.String, nil
}

// FindDuplicatesByHash returns all history rows matching sha256, ordered by most recent first.
func (s *State) FindDuplicatesByHash(sha256 string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT `+historySelectColumns+
			` FROM history WHERE sha256 = ? ORDER BY created_at DESC, id DESC`,
		sha256,
	)
	if err != nil {
		return nil, fmt.Errorf("find duplicates by hash: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan duplicate: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duplicates: %w", err)
	}
	return out, nil
}

// FindSimilarByFingerprints returns history rows that share any of the provided
// non-empty fingerprint signatures, ordered by most recent first.
func (s *State) FindSimilarByFingerprints(perceptualHash, avSignature, textSignature string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT `+historySelectColumns+
			` FROM history
			 WHERE (?1 <> '' AND perceptual_hash = ?1)
			    OR (?2 <> '' AND av_signature = ?2)
			    OR (?3 <> '' AND text_signature = ?3)
			 ORDER BY created_at DESC, id DESC`,
		perceptualHash, avSignature, textSignature,
	)
	if err != nil {
		return nil, fmt.Errorf("find similar by fingerprints: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan similar: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate similar: %w", err)
	}
	return out, nil
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

// FindDirectoryDecision returns a cached directory decision for key, if one exists.
func (s *State) FindDirectoryDecision(key string) (llm.DirectoryDecision, bool, error) {
	row := s.db.QueryRow(
		`SELECT decision FROM directory_decisions WHERE key = ?`,
		key,
	)

	var raw string
	err := row.Scan(&raw)
	if err == sql.ErrNoRows {
		return llm.DirectoryDecision{}, false, nil
	}
	if err != nil {
		return llm.DirectoryDecision{}, false, fmt.Errorf("find directory decision: %w", err)
	}

	var d llm.DirectoryDecision
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return llm.DirectoryDecision{}, false, fmt.Errorf("parse cached directory decision: %w", err)
	}
	return d, true, nil
}

// RecordDirectoryDecision stores a directory decision keyed by key, replacing any existing
// entry for the same key.
func (s *State) RecordDirectoryDecision(key string, decision llm.DirectoryDecision) error {
	raw, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal directory decision: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO directory_decisions (key, decision) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET decision = excluded.decision, created_at = CURRENT_TIMESTAMP`,
		key, string(raw),
	)
	if err != nil {
		return fmt.Errorf("record directory decision: %w", err)
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
