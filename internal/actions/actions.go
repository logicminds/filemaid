package actions

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

// Normalize returns an absolute, cleaned path.
func Normalize(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// WithinAllowed reports whether path is equal to, or a child of, any directory
// in allowedDirs. The comparison uses normalized absolute paths.
func WithinAllowed(path string, allowedDirs []string) bool {
	norm := Normalize(path)
	for _, d := range allowedDirs {
		allowed := Normalize(d)
		if norm == allowed {
			return true
		}
		if strings.HasPrefix(norm, allowed+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ComputeHash returns the SHA-256 hex digest of path, reading it in chunks.
func ComputeHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// MatchesPatterns reports whether path matches any glob pattern. Patterns are
// expanded for a leading ~ before matching.
func MatchesPatterns(path string, patterns []string) bool {
	for _, p := range patterns {
		expanded := expandTilde(p)
		if ok, _ := filepath.Match(expanded, path); ok {
			return true
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
	}
	return false
}

// UniqueDest returns dest if it does not exist; otherwise it appends an ISO
// timestamp to the filename stem.
func UniqueDest(dest string, fs FS) string {
	if !fs.Exists(dest) {
		return dest
	}
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ts := time.Now().Format("2006-01-02T15:04:05")
	ts = strings.ReplaceAll(ts, ":", "")
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", stem, ts, ext))
}

// Apply carries out a classification Decision for src.
//
// fileHash must be the SHA-256 hex digest of src computed before any LLM call;
// passing a pre-computed hash avoids a redundant file open and closes the race
// window that occurs when a concurrent filemaid process moves the file during
// classification.
//
// Safety rules:
//   - Sources outside cfg.AllowedDirs are skipped.
//   - Delete is only honored for files matching SafeDeletePatterns or when
//     isDuplicate is true; otherwise the action is coerced to review.
//   - Trash failures coerce the action to review.
//   - Destinations outside cfg.AllowedDirs redirect to the dated review queue.
//   - If the source file is no longer present (moved by a concurrent process),
//     Apply returns a "skipped" result without error.
func Apply(decision llm.Decision, src string, fileHash string, cfg *config.Config, db state.Repo, isDuplicate bool, fs FS, runID string, metrics llm.Metrics) (string, error) {
	if len(cfg.AllowedDirs) > 0 && !WithinAllowed(src, cfg.AllowedDirs) {
		return fmt.Sprintf("skipped (not allowed): %s", src), nil
	}

	reviewBase := datedReviewPath(cfg.ReviewDir, filepath.Base(src))

	if decision.Action == "delete" {
		safe := MatchesPatterns(src, cfg.SafeDeletePatterns) || isDuplicate
		if !safe {
			decision.Action = "review"
			decision.Reason = fmt.Sprintf("delete refused for safety; original reason: %s", decision.Reason)
		}
	}

	if decision.Action == "delete" {
		safe := MatchesPatterns(src, cfg.SafeDeletePatterns) || isDuplicate
		if !safe {
			decision.Action = "review"
			decision.Reason = fmt.Sprintf("delete refused for safety; original reason: %s", decision.Reason)
		}
	}

	if decision.Action == "delete" {
		if err := fs.Trash(src); err != nil {
			decision.Action = "review"
			decision.Reason = fmt.Sprintf("trash failed: %s", err)
		} else {
			if dbErr := db.Record(src, "trash", fileHash, decision.Category, decision.Tags, "delete", decision.Reason, runID, metrics); dbErr != nil {
				return "", fmt.Errorf("record history: %w", dbErr)
			}
			return "trash", nil
		}
	}

	var dest string
	if decision.Action == "review" {
		dest = reviewBase
	} else if decision.Destination != "" {
		dest = expandTilde(decision.Destination)
	} else if catDir, ok := cfg.Categories[decision.Category]; ok {
		dest = filepath.Join(expandTilde(catDir), filepath.Base(src))
	} else {
		dest = reviewBase
	}

	dest = UniqueDest(dest, fs)

	if len(cfg.AllowedDirs) > 0 && !WithinAllowed(dest, cfg.AllowedDirs) {
		dest = datedReviewPath(cfg.ReviewDir, filepath.Base(src))
		dest = UniqueDest(dest, fs)
		decision.Action = "review"
		decision.Reason += "; destination outside allowed dirs"
	}

	if err := fs.MkdirAll(filepath.Dir(dest)); err != nil {
		return "", fmt.Errorf("mkdir failed: %w", err)
	}
	if err := fs.Move(src, dest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf("skipped (%s no longer exists)", filepath.Base(src)), nil
		}
		return "", fmt.Errorf("move failed: %s -> %s: %w", src, dest, err)
	}

	var tags []string
	if cfg.Tags {
		tags = append(tags, decision.Tags...)
		if decision.Subcategory != "" && !stringSliceContains(tags, decision.Subcategory) {
			tags = append([]string{decision.Subcategory}, tags...)
		}
		if decision.Category != "" && !stringSliceContains(tags, decision.Category) {
			tags = append([]string{decision.Category}, tags...)
		}
	}
	if cfg.SmartFolders {
		tags = append([]string{"filemaid"}, tags...)
	}
	if len(tags) > 0 {
		fs.SetTags(dest, tags)
	}
	if cfg.Comments {
		fs.SetFinderComment(dest, decision.Reason)
	}

	if err := db.Record(src, dest, fileHash, decision.Category, tags, decision.Action, decision.Reason, runID, metrics); err != nil {
		return "", fmt.Errorf("record history: %w", err)
	}
	return dest, nil
}

func datedReviewPath(reviewDir, name string) string {
	return filepath.Join(expandTilde(reviewDir), time.Now().Format("2006-01-02"), name)
}

func expandTilde(s string) string {
	if s == "" || s[0] != '~' {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	if s == "~" {
		return home
	}
	if len(s) > 1 && (s[1] == '/' || s[1] == filepath.Separator) {
		return filepath.Join(home, s[2:])
	}
	return s
}
func stringSliceContains(ss []string, s string) bool {
	for _, item := range ss {
		if item == s {
			return true
		}
	}
	return false
}
