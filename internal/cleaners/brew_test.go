package cleaners

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func TestBrewCanRun(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		defer setLookPath(map[string]bool{"brew": true})()
		if !brewCanRun() {
			t.Fatal("expected brewCanRun true")
		}
	})
	t.Run("missing", func(t *testing.T) {
		defer setLookPath(map[string]bool{})()
		if brewCanRun() {
			t.Fatal("expected brewCanRun false")
		}
	})
}

func TestBrewRun(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "homebrew-cache")
	mustMkdir(t, cacheDir)
	mustWriteFile(t, filepath.Join(cacheDir, "bottle"), []byte("ccccc"))

	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name != "brew" {
			return nil, errors.New("unexpected command")
		}
		if len(arg) == 1 && arg[0] == "--cache" {
			return []byte(cacheDir + "\n"), nil
		}
		if len(arg) == 2 && arg[0] == "cleanup" && strings.HasPrefix(arg[1], "--prune=") {
			_ = os.RemoveAll(cacheDir)
			return []byte("cleaned\n"), nil
		}
		return nil, errors.New("unexpected brew command")
	})()

	cfg := config.Defaults()

	t.Run("dry-run safe", func(t *testing.T) {
		res := brewRun(true, cfg)
		if res.Status != "dry-run" || res.Saved == nil || *res.Saved != 5 {
			t.Fatalf("unexpected dry-run result: %+v", res)
		}
		if res.Command != "brew cleanup --prune=7" {
			t.Fatalf("command = %q", res.Command)
		}
	})

	t.Run("dry-run aggressive", func(t *testing.T) {
		cfg2 := config.Defaults()
		cfg2.DevCleanup["brew"] = config.CleanerConfig{Enabled: true, Mode: "aggressive"}
		res := brewRun(true, cfg2)
		if res.Command != "brew cleanup --prune=all" {
			t.Fatalf("command = %q", res.Command)
		}
	})

	t.Run("real run", func(t *testing.T) {
		res := brewRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q, detail=%s", res.Status, res.Detail)
		}
		if res.Saved == nil || *res.Saved != 5 {
			t.Fatalf("saved = %v, want 5", res.Saved)
		}
	})
}

func TestBrewCacheDirFallback(t *testing.T) {
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("brew missing")
	})()
	got := brewCacheDir()
	want := filepath.Join(home(), "Library", "Caches", "Homebrew")
	if got != want {
		t.Fatalf("brewCacheDir = %q, want %q", got, want)
	}
}

func TestBrewRunFailed(t *testing.T) {
	cacheDir := t.TempDir()
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name != "brew" {
			return nil, errors.New("unexpected command")
		}
		if len(arg) == 1 && arg[0] == "--cache" {
			return []byte(cacheDir + "\n"), nil
		}
		return nil, errors.New("brew error")
	})()

	res := brewRun(false, config.Defaults())
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Detail, "brew error") {
		t.Fatalf("detail = %q, want brew error", res.Detail)
	}
}
