package cleaners

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func TestNpmCanRun(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		defer setLookPath(map[string]bool{"npm": true})()
		if !npmCanRun() {
			t.Fatal("expected npmCanRun true")
		}
	})
	t.Run("missing", func(t *testing.T) {
		defer setLookPath(map[string]bool{})()
		if npmCanRun() {
			t.Fatal("expected npmCanRun false")
		}
	})
}

func TestNpmRun(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "npm-cache")
	contentDir := filepath.Join(cacheDir, "_cacache")
	mustMkdir(t, contentDir)
	mustWriteFile(t, filepath.Join(contentDir, "a"), []byte("12345"))

	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name != "npm" {
			return nil, errors.New("unexpected command")
		}
		if len(arg) == 3 && arg[0] == "config" && arg[1] == "get" && arg[2] == "cache" {
			return []byte(cacheDir + "\n"), nil
		}
		if len(arg) == 3 && arg[0] == "cache" && arg[1] == "clean" && arg[2] == "--force" {
			_ = os.RemoveAll(contentDir)
			return []byte("cleaned\n"), nil
		}
		return nil, errors.New("unexpected npm command")
	})()

	cfg := config.Defaults()

	t.Run("dry-run", func(t *testing.T) {
		res := npmRun(true, cfg)
		if res.Status != "dry-run" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.Saved == nil || *res.Saved != 5 {
			t.Fatalf("saved = %v, want 5", res.Saved)
		}
	})

	t.Run("real run", func(t *testing.T) {
		res := npmRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q, detail=%s", res.Status, res.Detail)
		}
		if res.Saved == nil || *res.Saved != 5 {
			t.Fatalf("saved = %v, want 5", res.Saved)
		}
		if res.SavedHuman != "5 B" {
			t.Fatalf("saved_human = %q", res.SavedHuman)
		}
		if res.Detail != "cache cleaned\ncleaned" {
			t.Fatalf("detail = %q", res.Detail)
		}
	})
}

func TestNpmCacheDirFallback(t *testing.T) {
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("npm not found")
	})()
	got := npmCacheDir()
	want := filepath.Join(home(), ".npm")
	if got != want {
		t.Fatalf("npmCacheDir = %q, want %q", got, want)
	}
}

func TestNpmCacheDirUndefined(t *testing.T) {
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		return []byte("undefined\n"), nil
	})()
	got := npmCacheDir()
	want := filepath.Join(home(), ".npm")
	if got != want {
		t.Fatalf("npmCacheDir = %q, want %q", got, want)
	}
}

func TestNpmRunFailed(t *testing.T) {
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name == "npm" && len(arg) == 3 && arg[0] == "config" && arg[1] == "get" && arg[2] == "cache" {
			return []byte("undefined\n"), nil
		}
		return nil, errors.New("npm error")
	})()

	res := npmRun(false, config.Defaults())
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Detail, "npm error") {
		t.Fatalf("detail = %q, want npm error", res.Detail)
	}
}
