package cleaners

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func TestDockerCanRun(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		defer setLookPath(map[string]bool{"docker": true})()
		if !dockerCanRun() {
			t.Fatal("expected dockerCanRun true")
		}
	})
	t.Run("missing", func(t *testing.T) {
		defer setLookPath(map[string]bool{})()
		if dockerCanRun() {
			t.Fatal("expected dockerCanRun false")
		}
	})
}

func TestDockerRun(t *testing.T) {
	cfg := config.Defaults()

	t.Run("dry-run safe", func(t *testing.T) {
		res := dockerRun(true, cfg)
		if res.Status != "dry-run" {
			t.Fatalf("status = %q, want dry-run", res.Status)
		}
		if res.Saved != nil {
			t.Fatalf("saved should be nil in dry-run, got %v", *res.Saved)
		}
		if res.Command != "docker image prune -f" {
			t.Fatalf("command = %q", res.Command)
		}
	})

	t.Run("dry-run aggressive", func(t *testing.T) {
		cfg2 := config.Defaults()
		cfg2.DevCleanup["docker"] = config.CleanerConfig{Enabled: true, Mode: "aggressive"}
		res := dockerRun(true, cfg2)
		if res.Command != "docker system prune -af --volumes" {
			t.Fatalf("command = %q", res.Command)
		}
	})

	t.Run("ok parses reclaimed", func(t *testing.T) {
		defer setRunner(func(name string, arg ...string) ([]byte, error) {
			if name != "docker" {
				return nil, errors.New("unexpected command")
			}
			return []byte("Total reclaimed space: 1.5 GB\n"), nil
		})()
		res := dockerRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q, want ok", res.Status)
		}
		if res.SavedHuman != "1.5 GB" {
			t.Fatalf("saved_human = %q", res.SavedHuman)
		}
		if res.Saved == nil || *res.Saved != int64(1.5*1024*1024*1024) {
			t.Fatalf("saved = %v", res.Saved)
		}
	})

	t.Run("ok no reclaimed line", func(t *testing.T) {
		defer setRunner(func(name string, arg ...string) ([]byte, error) {
			return []byte("nothing to prune\n"), nil
		})()
		res := dockerRun(false, cfg)
		if res.Status != "ok" {
			t.Fatalf("status = %q", res.Status)
		}
		if res.SavedHuman != "0 B" || res.Saved == nil || *res.Saved != 0 {
			t.Fatalf("saved should be 0, got %v / %q", res.Saved, res.SavedHuman)
		}
	})

	t.Run("failed", func(t *testing.T) {
		defer setRunner(func(name string, arg ...string) ([]byte, error) {
			return nil, exec.ErrNotFound
		})()
		res := dockerRun(false, cfg)
		if res.Status != "failed" {
			t.Fatalf("status = %q", res.Status)
		}
	})
}
