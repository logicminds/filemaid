package cleaners

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/config"
)

func TestXcodeCanRun(t *testing.T) {
	t.Run("xcodebuild present", func(t *testing.T) {
		defer setLookPath(map[string]bool{"xcodebuild": true})()
		if !xcodeCanRun() {
			t.Fatal("expected true")
		}
	})
	t.Run("DerivedData exists", func(t *testing.T) {
		defer setLookPath(map[string]bool{})()
		dd := derivedDataPath()
		mustMkdir(t, dd)
		defer os.RemoveAll(dd)
		if !xcodeCanRun() {
			t.Fatal("expected true")
		}
	})
	t.Run("missing", func(t *testing.T) {
		defer setLookPath(map[string]bool{})()
		_ = os.RemoveAll(derivedDataPath())
		if xcodeCanRun() {
			t.Fatal("expected false")
		}
	})
}

func TestXcodeRun(t *testing.T) {
	now := time.Now().Unix()
	defer setClock(now)()

	makeDerivedData := func() string {
		dd := derivedDataPath()
		_ = os.RemoveAll(dd)
		mustMkdir(t, dd)

		oldDir := filepath.Join(dd, "old-project")
		mustMkdir(t, oldDir)
		mustWriteFile(t, filepath.Join(oldDir, "build"), []byte("old"))
		_ = os.Chtimes(oldDir, time.Unix(now-60*24*60*60, 0), time.Unix(now-60*24*60*60, 0))

		newDir := filepath.Join(dd, "new-project")
		mustMkdir(t, newDir)
		mustWriteFile(t, filepath.Join(newDir, "build"), []byte("new"))
		_ = os.Chtimes(newDir, time.Unix(now-1*24*60*60, 0), time.Unix(now-1*24*60*60, 0))

		return dd
	}

	t.Run("skipped when missing", func(t *testing.T) {
		_ = os.RemoveAll(derivedDataPath())
		res := xcodeRun(false, config.Defaults())
		if res.Status != "skipped" {
			t.Fatalf("status = %q", res.Status)
		}
	})

	t.Run("dry-run safe", func(t *testing.T) {
		makeDerivedData()
		defer os.RemoveAll(derivedDataPath())
		res := xcodeRun(true, config.Defaults())
		if res.Status != "dry-run" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 3 {
			t.Fatalf("saved = %v, want 3", res.Saved)
		}
	})

	t.Run("real run safe", func(t *testing.T) {
		makeDerivedData()
		defer os.RemoveAll(derivedDataPath())
		cfg := config.Defaults()
		res := xcodeRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 3 {
			t.Fatalf("saved = %v, want 3", res.Saved)
		}
		if _, err := os.Stat(filepath.Join(derivedDataPath(), "old-project", "build")); !os.IsNotExist(err) {
			t.Fatal("old build file should be removed")
		}
		if _, err := os.Stat(filepath.Join(derivedDataPath(), "new-project", "build")); os.IsNotExist(err) {
			t.Fatal("new build file should remain")
		}
	})

	t.Run("dry-run aggressive", func(t *testing.T) {
		makeDerivedData()
		defer os.RemoveAll(derivedDataPath())
		cfg := config.Defaults()
		cfg.DevCleanup["xcode"] = config.CleanerConfig{Enabled: true, Mode: "aggressive"}
		res := xcodeRun(true, cfg)
		if res.Saved == nil || *res.Saved != 6 {
			t.Fatalf("saved = %v, want 6", res.Saved)
		}
	})
}
