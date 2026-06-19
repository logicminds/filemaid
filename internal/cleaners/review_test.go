package cleaners

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/config"
)

func TestReviewCanRun(t *testing.T) {
	if !reviewCanRun() {
		t.Fatal("reviewCanRun should always be true")
	}
}

func TestReviewRun(t *testing.T) {
	now := time.Now().Unix()
	defer setClock(now)()

	makeQueue := func(t *testing.T) string {
		dir := t.TempDir()

		oldFile := filepath.Join(dir, "old.txt")
		mustWriteFile(t, oldFile, []byte("stale"))
		_ = os.Chtimes(oldFile, time.Unix(now-60*24*60*60, 0), time.Unix(now-60*24*60*60, 0))

		newFile := filepath.Join(dir, "new.txt")
		mustWriteFile(t, newFile, []byte("fresh"))
		_ = os.Chtimes(newFile, time.Unix(now-1*24*60*60, 0), time.Unix(now-1*24*60*60, 0))

		subDir := filepath.Join(dir, "sub")
		mustMkdir(t, subDir)
		oldSub := filepath.Join(subDir, "oldsub.txt")
		mustWriteFile(t, oldSub, []byte("sub"))
		_ = os.Chtimes(oldSub, time.Unix(now-60*24*60*60, 0), time.Unix(now-60*24*60*60, 0))

		return dir
	}

	t.Run("skipped when missing", func(t *testing.T) {
		cfg := &config.Config{ReviewDir: filepath.Join(t.TempDir(), "missing")}
		res := reviewRun(false, cfg)
		if res.Status != "skipped" {
			t.Fatalf("status = %q", res.Status)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		cfg := &config.Config{
			ReviewDir: t.TempDir(),
			ReviewCleanup: config.ReviewCleanupConfig{
				Enabled: false,
			},
		}
		res := reviewRun(false, cfg)
		if res.Status != "disabled" {
			t.Fatalf("status = %q", res.Status)
		}
	})

	t.Run("dry-run safe", func(t *testing.T) {
		dir := makeQueue(t)
		cfg := &config.Config{
			ReviewDir: dir,
			ReviewCleanup: config.ReviewCleanupConfig{
				Enabled:    true,
				Mode:       "safe",
				MaxAgeDays: 30,
			},
		}
		res := reviewRun(true, cfg)
		if res.Status != "dry-run" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 8 {
			t.Fatalf("saved = %v, want 8", res.Saved)
		}
	})

	t.Run("real run safe", func(t *testing.T) {
		dir := makeQueue(t)
		cfg := &config.Config{
			ReviewDir: dir,
			ReviewCleanup: config.ReviewCleanupConfig{
				Enabled:    true,
				Mode:       "safe",
				MaxAgeDays: 30,
			},
		}
		res := reviewRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 8 {
			t.Fatalf("saved = %v, want 8", res.Saved)
		}
		if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
			t.Fatal("old file should be removed")
		}
		if _, err := os.Stat(filepath.Join(dir, "new.txt")); os.IsNotExist(err) {
			t.Fatal("new file should remain")
		}
		if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
			t.Fatal("sub dir should be removed")
		}
	})

	t.Run("dry-run aggressive", func(t *testing.T) {
		dir := makeQueue(t)
		cfg := &config.Config{
			ReviewDir: dir,
			ReviewCleanup: config.ReviewCleanupConfig{
				Enabled:    true,
				Mode:       "aggressive",
				MaxAgeDays: 30,
			},
		}
		res := reviewRun(true, cfg)
		if res.Saved == nil || *res.Saved != 13 {
			t.Fatalf("saved = %v, want 13", res.Saved)
		}
	})

	t.Run("real run aggressive", func(t *testing.T) {
		dir := makeQueue(t)
		cfg := &config.Config{
			ReviewDir: dir,
			ReviewCleanup: config.ReviewCleanupConfig{
				Enabled:    true,
				Mode:       "aggressive",
				MaxAgeDays: 30,
			},
		}
		res := reviewRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 13 {
			t.Fatalf("saved = %v, want 13", res.Saved)
		}
		if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
			t.Fatal("recent file should be removed in aggressive mode")
		}
		if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
			t.Fatal("old file should be removed")
		}
	})

	t.Run("no stale items", func(t *testing.T) {
		dir := t.TempDir()
		newFile := filepath.Join(dir, "new.txt")
		mustWriteFile(t, newFile, []byte("fresh"))
		_ = os.Chtimes(newFile, time.Unix(now-1*24*60*60, 0), time.Unix(now-1*24*60*60, 0))
		cfg := &config.Config{
			ReviewDir: dir,
			ReviewCleanup: config.ReviewCleanupConfig{
				Enabled:    true,
				Mode:       "safe",
				MaxAgeDays: 30,
			},
		}
		res := reviewRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Detail != "no stale review items" {
			t.Fatalf("detail = %q", res.Detail)
		}
	})
}
