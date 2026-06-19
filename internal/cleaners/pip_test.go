package cleaners

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func TestPipCanRun(t *testing.T) {
	if !pipCanRun() {
		t.Fatal("pipCanRun should always be true")
	}
}

func TestPipRun(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "pip-cache")
	mustMkdir(t, cacheDir)
	mustWriteFile(t, filepath.Join(cacheDir, "wheel"), []byte("bb"))

	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name != "python3" || len(arg) != 4 || arg[0] != "-m" || arg[1] != "pip" || arg[2] != "cache" {
			return nil, errors.New("unexpected command")
		}
		switch arg[3] {
		case "dir":
			return []byte(cacheDir + "\n"), nil
		case "purge":
			_ = os.RemoveAll(cacheDir)
			return []byte("purged\n"), nil
		}
		return nil, errors.New("unexpected pip cache subcommand")
	})()

	cfg := config.Defaults()

	t.Run("dry-run", func(t *testing.T) {
		res := pipRun(true, cfg)
		if res.Status != "dry-run" || res.Saved == nil || *res.Saved != 2 {
			t.Fatalf("unexpected dry-run result: %+v", res)
		}
	})

	t.Run("real run", func(t *testing.T) {
		res := pipRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 2 {
			t.Fatalf("saved = %v, want 2", res.Saved)
		}
	})
}

func TestPipCacheDirFallback(t *testing.T) {
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("python missing")
	})()
	got := pipCacheDir()
	want := filepath.Join(home(), "Library", "Caches", "pip")
	if got != want {
		t.Fatalf("pipCacheDir = %q, want %q", got, want)
	}
}

func TestPipRunFailed(t *testing.T) {
	cacheDir := t.TempDir()
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name != "python3" || len(arg) != 4 || arg[0] != "-m" || arg[1] != "pip" || arg[2] != "cache" {
			return nil, errors.New("unexpected command")
		}
		switch arg[3] {
		case "dir":
			return []byte(cacheDir + "\n"), nil
		case "purge":
			return nil, errors.New("pip error")
		}
		return nil, errors.New("unexpected pip cache subcommand")
	})()

	res := pipRun(false, config.Defaults())
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Detail, "pip error") {
		t.Fatalf("detail = %q, want pip error", res.Detail)
	}
}
