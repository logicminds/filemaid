package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultsMatchPythonReference(t *testing.T) {
	cfg := Defaults()

	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("OllamaURL = %q", cfg.OllamaURL)
	}
	if cfg.Model != "filemaid-gemma4-26b" {
		t.Errorf("Model = %q", cfg.Model)
	}
	wantCleaners := []string{"docker", "npm", "cargo", "pip", "brew", "xcode"}
	if !reflect.DeepEqual(cfg.AllowedCleaners, wantCleaners) {
		t.Errorf("AllowedCleaners = %v, want %v", cfg.AllowedCleaners, wantCleaners)
	}
	if cfg.Tags != true {
		t.Errorf("Tags = %v, want true", cfg.Tags)
	}
	if cfg.MinAgeHours != 0 {
		t.Errorf("MinAgeHours = %d, want 0", cfg.MinAgeHours)
	}

	defaultCategories := []string{"Screenshots", "Documents", "Receipts", "Images", "Installers", "Code", "Archives", "Media", "Unknown"}
	for _, name := range defaultCategories {
		if cfg.Categories[name] == "" {
			t.Errorf("default category %q missing", name)
		}
	}

	for name := range cfg.DevCleanup {
		if !cfg.DevCleanup[name].Enabled {
			t.Errorf("DevCleanup[%s].Enabled = false, want true", name)
		}
		if cfg.DevCleanup[name].Mode != "safe" {
			t.Errorf("DevCleanup[%s].Mode = %q, want safe", name, cfg.DevCleanup[name].Mode)
		}
	}

	if !cfg.ReviewCleanup.Enabled {
		t.Error("ReviewCleanup.Enabled = false, want true")
	}
	if cfg.ReviewCleanup.Mode != "safe" {
		t.Errorf("ReviewCleanup.Mode = %q, want safe", cfg.ReviewCleanup.Mode)
	}
	if cfg.ReviewCleanup.MaxAgeDays != 30 {
		t.Errorf("ReviewCleanup.MaxAgeDays = %d, want 30", cfg.ReviewCleanup.MaxAgeDays)
	}
}

func TestExpandExpandsTilde(t *testing.T) {
	cfg := Defaults()
	cfg.expand()

	home := home()
	if home == "" {
		t.Skip("HOME not set")
	}

	if !strings.HasPrefix(cfg.ReviewDir, home) {
		t.Errorf("ReviewDir not expanded: got %q", cfg.ReviewDir)
	}
	if !strings.HasPrefix(cfg.LogPath, home) {
		t.Errorf("LogPath not expanded: got %q", cfg.LogPath)
	}
	if !strings.HasPrefix(cfg.DBPath, home) {
		t.Errorf("DBPath not expanded: got %q", cfg.DBPath)
	}
	for _, d := range cfg.WatchDirs {
		if !strings.HasPrefix(d, home) {
			t.Errorf("WatchDirs entry not expanded: got %q", d)
		}
	}
	for _, d := range cfg.AllowedDirs {
		if !strings.HasPrefix(d, home) {
			t.Errorf("AllowedDirs entry not expanded: got %q", d)
		}
	}
	for name, p := range cfg.Categories {
		if !strings.HasPrefix(p, home) {
			t.Errorf("Categories[%s] not expanded: got %q", name, p)
		}
	}
	if !strings.HasPrefix(cfg.AgeRules[0].Pattern, home) {
		t.Errorf("AgeRules pattern not expanded: got %q", cfg.AgeRules[0].Pattern)
	}
}

func TestExpandHandlesBareTilde(t *testing.T) {
	home := home()
	if home == "" {
		t.Skip("HOME not set")
	}
	if got := expandPath("~"); got != home {
		t.Errorf("expandPath(~) = %q, want %q", got, home)
	}
}

func TestEnsureUnknownAddsReviewDir(t *testing.T) {
	cfg := Defaults()
	delete(cfg.Categories, "Unknown")
	cfg.ensureUnknown()

	if cfg.Categories["Unknown"] != cfg.ReviewDir {
		t.Errorf("Unknown = %q, want %q", cfg.Categories["Unknown"], cfg.ReviewDir)
	}
}

func TestLoadPath(t *testing.T) {
	tests := []struct {
		name string
		user string
		want func(t *testing.T, cfg *Config)
	}{
		{
			name: "missing file returns defaults",
			user: "",
			want: func(t *testing.T, cfg *Config) {
				if cfg.Model != "filemaid-gemma4-26b" {
					t.Errorf("Model = %q, want default", cfg.Model)
				}
				if !strings.HasPrefix(cfg.Categories["Unknown"], home()) {
					t.Errorf("Unknown not expanded: got %q", cfg.Categories["Unknown"])
				}
			},
		},
		{
			name: "top-level override",
			user: `{"model": "custom-model", "tags": false, "review_dir": "~/custom/review"}`,
			want: func(t *testing.T, cfg *Config) {
				if cfg.Model != "custom-model" {
					t.Errorf("Model = %q, want custom-model", cfg.Model)
				}
				if cfg.Tags {
					t.Error("Tags = true, want false")
				}
				if !strings.HasPrefix(cfg.ReviewDir, home()) || !strings.Contains(cfg.ReviewDir, "custom/review") {
					t.Errorf("ReviewDir not expanded correctly: got %q", cfg.ReviewDir)
				}
			},
		},
		{
			name: "deep merge categories keeps defaults",
			user: `{"categories": {"Screenshots": "~/Pictures"}}`,
			want: func(t *testing.T, cfg *Config) {
				if !strings.HasPrefix(cfg.Categories["Screenshots"], home()) || !strings.Contains(cfg.Categories["Screenshots"], "Pictures") {
					t.Errorf("Screenshots = %q", cfg.Categories["Screenshots"])
				}
				if cfg.Categories["Documents"] == "" {
					t.Error("Documents default was dropped")
				}
				if cfg.Categories["Unknown"] != cfg.ReviewDir {
					t.Errorf("Unknown = %q, want %q", cfg.Categories["Unknown"], cfg.ReviewDir)
				}
			},
		},
		{
			name: "deep merge dev_cleanup keeps defaults",
			user: `{"dev_cleanup": {"docker": {"enabled": false}}}`,
			want: func(t *testing.T, cfg *Config) {
				if cfg.DevCleanup["docker"].Enabled {
					t.Error("docker should be disabled by user override")
				}
				if !cfg.DevCleanup["npm"].Enabled {
					t.Error("npm default was dropped")
				}
				if cfg.DevCleanup["xcode"].Mode != "safe" {
					t.Errorf("xcode mode = %q, want safe", cfg.DevCleanup["xcode"].Mode)
				}
			},
		},
		{
			name: "deep merge review_cleanup keeps defaults",
			user: `{"review_cleanup": {"max_age_days": 7}}`,
			want: func(t *testing.T, cfg *Config) {
				if !cfg.ReviewCleanup.Enabled {
					t.Error("ReviewCleanup.Enabled was dropped")
				}
				if cfg.ReviewCleanup.Mode != "safe" {
					t.Errorf("ReviewCleanup.Mode = %q, want safe", cfg.ReviewCleanup.Mode)
				}
				if cfg.ReviewCleanup.MaxAgeDays != 7 {
					t.Errorf("ReviewCleanup.MaxAgeDays = %d, want 7", cfg.ReviewCleanup.MaxAgeDays)
				}
			},
		},
		{
			name: "slice override replaces not appends",
			user: `{"watch_dirs": ["~/tmp"] }`,
			want: func(t *testing.T, cfg *Config) {
				if len(cfg.WatchDirs) != 1 {
					t.Errorf("WatchDirs len = %d, want 1", len(cfg.WatchDirs))
				}
				if !strings.HasPrefix(cfg.WatchDirs[0], home()) {
					t.Errorf("WatchDirs[0] not expanded: got %q", cfg.WatchDirs[0])
				}
			},
		},
		{
			name: "recursive expansion in nested values",
			user: `{"categories": {"Custom": "~/custom"}, "allowed_dirs": ["~/custom"] }`,
			want: func(t *testing.T, cfg *Config) {
				if !strings.HasPrefix(cfg.Categories["Custom"], home()) {
					t.Errorf("Categories[Custom] not expanded: got %q", cfg.Categories["Custom"])
				}
				if !strings.HasPrefix(cfg.AllowedDirs[len(cfg.AllowedDirs)-1], home()) {
					t.Errorf("AllowedDirs last not expanded: got %q", cfg.AllowedDirs[len(cfg.AllowedDirs)-1])
				}
			},
		},
		{
			name: "unknown category preserved and expanded",
			user: `{"categories": {"Custom": "~/custom"}, "review_dir": "~/review"}`,
			want: func(t *testing.T, cfg *Config) {
				if cfg.Categories["Unknown"] == "" {
					t.Error("Unknown category missing")
				}
				if !strings.HasPrefix(cfg.Categories["Unknown"], home()) {
					t.Errorf("Unknown not expanded: got %q", cfg.Categories["Unknown"])
				}
				if cfg.Categories["Custom"] == "" {
					t.Error("Custom category missing")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if tt.user != "" {
				if err := os.WriteFile(path, []byte(tt.user), 0640); err != nil {
					t.Fatalf("write user config: %v", err)
				}
			}

			cfg, err := LoadPath(path)
			if err != nil {
				t.Fatalf("LoadPath: %v", err)
			}
			tt.want(t, cfg)
		})
	}
}

func TestLoadReadsFromConfigDir(t *testing.T) {
	homeDir := t.TempDir()
	configDir := filepath.Join(homeDir, ".config", "filemaid")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(path, []byte(`{"model": "loaded-from-home"}`), 0640); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("HOME", homeDir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model != "loaded-from-home" {
		t.Errorf("Model = %q, want loaded-from-home", cfg.Model)
	}
	if cfg.Categories["Screenshots"] == "" {
		t.Error("default category was dropped")
	}
}

func TestLoadPathInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0640); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadPath(path); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
