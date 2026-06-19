package cleaners

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func TestCargoCanRun(t *testing.T) {
	tests := []struct {
		name  string
		found map[string]bool
		want  bool
	}{
		{"both present", map[string]bool{"cargo": true, "cargo-cache": true}, true},
		{"only cargo", map[string]bool{"cargo": true}, false},
		{"only cargo-cache", map[string]bool{"cargo-cache": true}, false},
		{"neither", map[string]bool{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer setLookPath(tc.found)()
			if got := cargoCanRun(); got != tc.want {
				t.Fatalf("cargoCanRun = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCargoRun(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "registry", "cache")
	mustMkdir(t, cacheDir)
	mustWriteFile(t, filepath.Join(cacheDir, "crate"), []byte("aaaa"))

	t.Setenv("CARGO_HOME", dir)

	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		if name == "cargo" && len(arg) == 2 && arg[0] == "cache" && arg[1] == "--autoclean" {
			_ = os.RemoveAll(cacheDir)
			return []byte("autocleaned\n"), nil
		}
		return nil, errors.New("unexpected command")
	})()

	cfg := config.Defaults()

	t.Run("dry-run", func(t *testing.T) {
		res := cargoRun(true, cfg)
		if res.Status != "dry-run" || res.Saved == nil || *res.Saved != 4 {
			t.Fatalf("unexpected dry-run result: %+v", res)
		}
	})

	t.Run("real run", func(t *testing.T) {
		res := cargoRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q, detail=%s", res.Status, res.Detail)
		}
		if res.Saved == nil || *res.Saved != 4 {
			t.Fatalf("saved = %v, want 4", res.Saved)
		}
	})
}

func TestCargoCacheDirDefault(t *testing.T) {
	t.Setenv("CARGO_HOME", "")
	got := cargoCacheDir()
	want := filepath.Join(home(), ".cargo", "registry", "cache")
	if got != want {
		t.Fatalf("cargoCacheDir = %q, want %q", got, want)
	}
}

func TestCargoRunFailed(t *testing.T) {
	t.Setenv("CARGO_HOME", t.TempDir())
	defer setRunner(func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("cargo error")
	})()

	res := cargoRun(false, config.Defaults())
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Detail, "cargo error") {
		t.Fatalf("detail = %q, want cargo error", res.Detail)
	}
}
