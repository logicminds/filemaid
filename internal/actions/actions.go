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
	"github.com/logicminds/filemaid/internal/fingerprint"
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
func Apply(decision llm.Decision, src string, fileHash string, cfg *config.Config, db state.Repo, isDuplicate bool, fs FS, runID string, metrics llm.Metrics, force bool) (string, error) {
	if len(cfg.AllowedDirs) > 0 && !WithinAllowed(src, cfg.AllowedDirs) {
		return fmt.Sprintf("skipped (not allowed): %s", src), nil
	}

	originalName := filepath.Base(src)
	reviewBase := datedReviewPath(cfg.ReviewDir, originalName)

	var fp fingerprint.MediaFingerprint
	var fpErr error
	if cfg.Rename {
		fp, fpErr = fingerprint.ComputeFingerprint(src, cfg)
	}

	var duplicates []state.Record
	var similar []state.Record
	if cfg.Rename && fpErr == nil {
		duplicates, _ = db.FindDuplicatesByHash(fileHash)
		similar, _ = db.FindSimilarByFingerprints(fp.PerceptualHash, fp.AVSignature, fp.TextSignature)
	}

	if cfg.Rename && decision.Action != "review" && fpErr == nil && !force {
		if len(duplicates) > 0 {
			decision.Action = "review"
			decision.Reason = fmt.Sprintf("duplicate content detected; original reason: %s", decision.Reason)
		} else if len(similar) > 0 {
			threshold := cfg.RenameImageSimilarityThreshold
			if fp.MediaKind == "audio" || fp.MediaKind == "video" {
				threshold = cfg.RenameAVSimilarityThreshold
			}
			for _, rec := range similar {
				recFP := fingerprint.MediaFingerprint{
					MediaKind:      rec.MediaKind,
					PerceptualHash: rec.PerceptualHash,
					AVSignature:    rec.AVSignature,
					TextSignature:  rec.TextSignature,
					HashAlgorithm:  rec.HashAlgorithm,
				}
				score := fingerprint.CompareFiles(fp, recFP)
				if score >= threshold {
					decision.Action = "review"
					decision.Reason = fmt.Sprintf("similar content detected (%.2f); original reason: %s", score, decision.Reason)
					break
				}
			}
		}
	}

	if decision.Action == "delete" {
		safe := MatchesPatterns(src, cfg.SafeDeletePatterns) || isDuplicate
		if !safe {
			decision.Action = "review"
			decision.Reason = fmt.Sprintf("delete refused for safety; original reason: %s", decision.Reason)
		}
	}

	// Moving files is opt-in. When disabled, relocation decisions classify in place.
	if !cfg.MoveFiles && (decision.Action == "move" || decision.Action == "review") {
		decision.Action = "classify"
	}

	if decision.Action == "delete" {
		if err := fs.Trash(src); err != nil {
			decision.Action = "review"
			decision.Reason = fmt.Sprintf("trash failed: %s", err)
		} else {
			if dbErr := db.Record(state.RecordInput{
				OriginalPath:   src,
				FinalPath:      "trash",
				SHA256:         fileHash,
				Category:       decision.Category,
				Tags:           []string{},
				Action:         "delete",
				Reason:         decision.Reason,
				RunID:          runID,
				Metrics:        metrics,
				OriginalName:   originalName,
				NewName:        "",
				NameQuality:    float64(decision.NameQuality),
				MediaKind:      fp.MediaKind,
				PerceptualHash: fp.PerceptualHash,
				AVSignature:    fp.AVSignature,
				TextSignature:  fp.TextSignature,
				HashAlgorithm:  fp.HashAlgorithm,
			}); dbErr != nil {
				return "", fmt.Errorf("record history: %w", dbErr)
			}
			return "trash", nil
		}
	}

	var destDir string
	var destFileName string
	noCategory := false
	if decision.Action == "review" {
		destDir = filepath.Dir(reviewBase)
		destFileName = originalName
	} else if decision.Action == "classify" {
		destDir = filepath.Dir(src)
		destFileName = originalName
	} else if decision.Destination != "" {
		dest := expandTilde(decision.Destination)
		destDir = filepath.Dir(dest)
		destFileName = filepath.Base(dest)
	} else if catDir, ok := cfg.Categories[decision.Category]; ok {
		destDir = expandTilde(catDir)
		destFileName = originalName
	} else {
		destDir = filepath.Dir(reviewBase)
		destFileName = originalName
		noCategory = true
	}

	renamedTo := ""
	if cfg.Rename && decision.Action != "review" && decision.Action != "delete" && decision.NameQuality >= cfg.RenameLevel && fpErr == nil {
		if sanitized, ok := sanitizeName(decision.NewName, originalName, cfg); ok {
			destFileName = uniqueNameWithCounter(destDir, sanitized, src, fs)
			renamedTo = destFileName
		}
	}

	// A "move" decision with no known category and no valid rename is ambiguous;
	// fall back to review rather than leaving the file untouched.
	if decision.Action == "move" && renamedTo == "" && noCategory {
		destDir = filepath.Dir(reviewBase)
		destFileName = originalName
		decision.Action = "review"
		decision.Reason += "; no category or valid rename"
	}

	dest := filepath.Join(destDir, destFileName)
	if cfg.Rename && renamedTo != "" {
		// Renamed destinations already resolved collisions with a counter suffix.
	} else if dest != src {
		dest = UniqueDest(dest, fs)
	}
	if len(cfg.AllowedDirs) > 0 && !WithinAllowed(dest, cfg.AllowedDirs) {
		dest = datedReviewPath(cfg.ReviewDir, originalName)
		dest = UniqueDest(dest, fs)
		decision.Action = "review"
		decision.Reason += "; destination outside allowed dirs"
	}

	if err := fs.MkdirAll(filepath.Dir(dest)); err != nil {
		return "", fmt.Errorf("mkdir failed: %w", err)
	}
	if dest != src {
		if err := fs.Move(src, dest); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Sprintf("skipped (%s no longer exists)", originalName), nil
			}
			return "", fmt.Errorf("move failed: %s -> %s: %w", src, dest, err)
		}
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

	if err := db.Record(state.RecordInput{
		OriginalPath:   src,
		FinalPath:      dest,
		SHA256:         fileHash,
		Category:       decision.Category,
		Tags:           tags,
		Action:         decision.Action,
		Reason:         decision.Reason,
		RunID:          runID,
		Metrics:        metrics,
		OriginalName:   originalName,
		NewName:        renamedTo,
		NameQuality:    float64(decision.NameQuality),
		MediaKind:      fp.MediaKind,
		PerceptualHash: fp.PerceptualHash,
		AVSignature:    fp.AVSignature,
		TextSignature:  fp.TextSignature,
		HashAlgorithm:  fp.HashAlgorithm,
	}); err != nil {
		return "", fmt.Errorf("record history: %w", err)
	}
	return dest, nil
}

// ApplyDirectory carries out a DirectoryDecision for a project directory.
//
// Safety rules:
//   - Sources outside cfg.AllowedDirs are skipped.
//   - Destinations outside cfg.AllowedDirs redirect to the dated review queue.
//   - Same-volume moves use a single fs.Move. Cross-device moves fall back to a
//     recursive copy+remove, which is documented as non-atomic.
//   - A single history row is recorded for the whole directory.
func ApplyDirectory(decision llm.DirectoryDecision, src string, cfg *config.Config, db state.Repo, fs FS, runID string) (string, error) {
	originalName := filepath.Base(src)

	if len(cfg.AllowedDirs) > 0 && !WithinAllowed(src, cfg.AllowedDirs) {
		return fmt.Sprintf("skipped (not allowed): %s", src), nil
	}

	var dest string
	var action string
	if decision.Action == "move" {
		action = "move"
		if decision.Destination != "" {
			dest = expandTilde(decision.Destination)
		} else if cfg.ProjectDirCategory != "" {
			if catDir, ok := cfg.Categories[cfg.ProjectDirCategory]; ok {
				dest = filepath.Join(expandTilde(catDir), originalName)
			}
		}
		if dest == "" {
			dest = datedReviewPath(cfg.ReviewDir, originalName)
		}
	} else {
		action = "review"
		dest = datedReviewPath(cfg.ReviewDir, originalName)
	}

	dest = UniqueDest(dest, fs)

	if len(cfg.AllowedDirs) > 0 && !WithinAllowed(dest, cfg.AllowedDirs) {
		action = "review"
		dest = datedReviewPath(cfg.ReviewDir, originalName)
		dest = UniqueDest(dest, fs)
		decision.Reason += "; destination outside allowed dirs"
	}

	if err := fs.MkdirAll(filepath.Dir(dest)); err != nil {
		return "", fmt.Errorf("mkdir failed: %w", err)
	}

	if err := fs.Move(src, dest); err != nil {
		if !isCrossDeviceError(err) {
			return "", fmt.Errorf("move failed: %s -> %s: %w", src, dest, err)
		}
		if cpErr := copyTree(src, dest); cpErr != nil {
			os.RemoveAll(dest)
			return "", fmt.Errorf("cross-device copy failed: %s -> %s: %w", src, dest, cpErr)
		}
		if rmErr := removeTree(src); rmErr != nil {
			return "", fmt.Errorf("cross-device copy succeeded but remove source failed: %s: %w", src, rmErr)
		}
	}

	var tags []string
	if cfg.Tags {
		tags = append(tags, decision.Tags...)
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

	if err := db.Record(state.RecordInput{
		OriginalPath: src,
		FinalPath:    dest,
		SHA256:       "",
		Category:     decision.Category,
		Tags:         tags,
		Action:       action,
		Reason:       decision.Reason,
		RunID:        runID,
		OriginalName: originalName,
		NewName:      "",
	}); err != nil {
		return "", fmt.Errorf("record history: %w", err)
	}
	return dest, nil
}

// DestinationDir returns the directory a file would be moved to based on the
// classification decision and configuration, before unique-name resolution.
// It is exported so callers (e.g. the process worker pool) can serialize
// applies that target the same destination directory.
func DestinationDir(decision llm.Decision, src string, cfg *config.Config) string {
	originalName := filepath.Base(src)
	reviewBase := datedReviewPath(cfg.ReviewDir, originalName)

	var destDir string
	if decision.Action == "review" {
		destDir = filepath.Dir(reviewBase)
	} else if decision.Action == "classify" {
		destDir = filepath.Dir(src)
	} else if decision.Destination != "" {
		destDir = filepath.Dir(expandTilde(decision.Destination))
	} else if catDir, ok := cfg.Categories[decision.Category]; ok {
		destDir = expandTilde(catDir)
	} else {
		destDir = filepath.Dir(reviewBase)
	}
	return destDir
}

func sanitizeName(newName, original string, cfg *config.Config) (string, bool) {
	if newName == "" {
		return "", false
	}
	ext := filepath.Ext(original)
	stem := strings.TrimSuffix(filepath.Base(newName), filepath.Ext(newName))
	if stem == "" {
		return "", false
	}
	invalid := cfg.RenameInvalidChars
	if invalid == "" {
		invalid = "<>:\"/\\\\|?*"
	}
	pairs := make([]string, 0, len(invalid)*2)
	for _, r := range invalid {
		pairs = append(pairs, string(r), "")
	}
	stem = strings.NewReplacer(pairs...).Replace(stem)
	stem = strings.TrimSpace(stem)
	if stem == "" {
		return "", false
	}
	maxLen := cfg.RenameMaxLength
	if maxLen <= 0 {
		maxLen = 120
	}
	minLen := cfg.RenameMinLength
	if minLen < 0 {
		minLen = 0
	}
	available := maxLen - len(ext)
	if available < minLen {
		available = minLen
	}
	if len(stem) > available {
		stem = stem[:available]
		stem = strings.TrimSpace(stem)
	}
	if len(stem) < minLen {
		return "", false
	}
	return stem + ext, true
}

func uniqueNameWithCounter(dir, name, src string, fs FS) string {
	candidate := filepath.Join(dir, name)
	if candidate == src || !fs.Exists(candidate) {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("%s %d%s", stem, i, ext)
		full := filepath.Join(dir, candidate)
		if full == src || !fs.Exists(full) {
			return candidate
		}
	}
	return filepath.Base(UniqueDest(filepath.Join(dir, name), fs))
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
