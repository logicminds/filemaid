package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultsMatchPythonReference(t *testing.T) {
	cfg := Defaults()

	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("OllamaURL = %q", cfg.OllamaURL)
	}
	if cfg.Model != "filemaid-metadata" {
		t.Errorf("Model = %q", cfg.Model)
	}
	if cfg.ImageModel != "filemaid-gemma4-12b" {
		t.Errorf("ImageModel = %q", cfg.ImageModel)
	}
	if cfg.TextModel != "filemaid-metadata" {
		t.Errorf("TextModel = %q", cfg.TextModel)
	}
	wantCleaners := []string{"docker", "npm", "cargo", "pip", "brew", "xcode"}
	if !reflect.DeepEqual(cfg.AllowedCleaners, wantCleaners) {
		t.Errorf("AllowedCleaners = %v, want %v", cfg.AllowedCleaners, wantCleaners)
	}
	if cfg.Tags != true {
		t.Errorf("Tags = %v, want true", cfg.Tags)
	}
	if cfg.Comments != true {
		t.Errorf("Comments = %v, want true", cfg.Comments)
	}
	if cfg.SubcategorizeImages != true {
		t.Errorf("SubcategorizeImages = %v, want true", cfg.SubcategorizeImages)
	}
	if cfg.MinAgeHours != 0 {
		t.Errorf("MinAgeHours = %d, want 0", cfg.MinAgeHours)
	}
	if cfg.SmartFolders != true {
		t.Errorf("SmartFolders = %v, want true", cfg.SmartFolders)
	}
	if cfg.SmartFoldersDir != "~/Documents/Filemaid" {
		t.Errorf("SmartFoldersDir = %q, want ~/Documents/Filemaid", cfg.SmartFoldersDir)
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
	if time.Duration(cfg.RequestTimeout) != 5*time.Minute {
		t.Errorf("RequestTimeout = %v, want 5m", cfg.RequestTimeout)
	}
	if cfg.ProcessWorkers != 1 {
		t.Errorf("ProcessWorkers = %d, want 1", cfg.ProcessWorkers)
	}
}
func TestDefaultsRenameFields(t *testing.T) {
	cfg := Defaults()
	if cfg.Rename {
		t.Errorf("Rename = %v, want false", cfg.Rename)
	}
	if cfg.RenameLevel != 2 {
		t.Errorf("RenameLevel = %d, want 2", cfg.RenameLevel)
	}
	if cfg.RenameMaxLength != 120 {
		t.Errorf("RenameMaxLength = %d, want 120", cfg.RenameMaxLength)
	}
	if cfg.RenameMinLength != 20 {
		t.Errorf("RenameMinLength = %d, want 20", cfg.RenameMinLength)
	}
	wantInvalid := "<>:\"/\\\\|?*"
	if cfg.RenameInvalidChars != wantInvalid {
		t.Errorf("RenameInvalidChars = %q, want %q", cfg.RenameInvalidChars, wantInvalid)
	}
	if cfg.RenameImageSimilarityThreshold != 0.95 {
		t.Errorf("RenameImageSimilarityThreshold = %v, want 0.95", cfg.RenameImageSimilarityThreshold)
	}
	if cfg.RenameAVSimilarityThreshold != 0.90 {
		t.Errorf("RenameAVSimilarityThreshold = %v, want 0.90", cfg.RenameAVSimilarityThreshold)
	}
	if cfg.RenameUseFFmpeg {
		t.Error("RenameUseFFmpeg = true, want false")
	}
	if cfg.ProcessWorkers != 1 {
		t.Errorf("ProcessWorkers = %d, want 1", cfg.ProcessWorkers)
	}
	if cfg.MaxImageDimension != 1024 {
		t.Errorf("MaxImageDimension = %d, want 1024", cfg.MaxImageDimension)
	}
	// ProcessWorkers is asserted in TestDefaultsMatchPythonReference.
}
func TestDefaultsProjectMarkers(t *testing.T) {
	want := []string{".git", "node_modules", ".venv", "vendor", ".terraform", "build"}
	if !reflect.DeepEqual(Defaults().ProjectMarkers, want) {
		t.Errorf("ProjectMarkers = %v, want %v", Defaults().ProjectMarkers, want)
	}
}

func TestLoadPathProjectMarkers(t *testing.T) {
	tests := []struct {
		name string
		user string
		want []string
	}{
		{
			name: "missing uses defaults",
			user: "",
			want: []string{".git", "node_modules", ".venv", "vendor", ".terraform", "build"},
		},
		{
			name: "additive merge preserves defaults",
			user: `{"project_markers": ["Cargo.lock"]}`,
			want: []string{".git", "node_modules", ".venv", "vendor", ".terraform", "build", "cargo.lock"},
		},
		{
			name: "normalization trims spaces leading dot slash and separators",
			user: `{"project_markers": ["  Cargo.lock  ", "./cargo.lock", "Cargo.lock/", ".GIT"]}`,
			want: []string{".git", "node_modules", ".venv", "vendor", ".terraform", "build", "cargo.lock"},
		},
		{
			name: "deduplication keeps first occurrence",
			user: `{"project_markers": ["node_modules", "vendor", "custom"]}`,
			want: []string{".git", "node_modules", ".venv", "vendor", ".terraform", "build", "custom"},
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
			if !reflect.DeepEqual(cfg.ProjectMarkers, tt.want) {
				t.Errorf("ProjectMarkers = %v, want %v", cfg.ProjectMarkers, tt.want)
			}
		})
	}
}

func TestLoadPathOverridesProcessWorkersAndMaxImageDimension(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	content := `{"process_workers": 8, "max_image_dimension": 512}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProcessWorkers != 8 {
		t.Errorf("ProcessWorkers = %d, want 8", cfg.ProcessWorkers)
	}
	if cfg.MaxImageDimension != 512 {
		t.Errorf("MaxImageDimension = %d, want 512", cfg.MaxImageDimension)
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

func TestSmartFoldersMarshalUnmarshal(t *testing.T) {
	orig := Defaults()
	orig.SmartFolders = true
	orig.SmartFoldersDir = "~/Documents/Filemaid"

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !strings.Contains(string(b), `"smart_folders":true`) {
		t.Errorf("marshaled JSON missing smart_folders: %s", b)
	}
	if !strings.Contains(string(b), `"smart_folders_dir":"~/Documents/Filemaid"`) {
		t.Errorf("marshaled JSON missing smart_folders_dir: %s", b)
	}

	var got Config
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SmartFolders != true {
		t.Errorf("SmartFolders = %v, want true", got.SmartFolders)
	}
	if got.SmartFoldersDir != "~/Documents/Filemaid" {
		t.Errorf("SmartFoldersDir = %q, want ~/Documents/Filemaid", got.SmartFoldersDir)
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
				if cfg.Model != "filemaid-metadata" {
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
		{
			name: "missing smart folders uses defaults",
			user: `{"smart_folders": false, "smart_folders_dir": "~/Custom/Smart"}`,
			want: func(t *testing.T, cfg *Config) {
				if cfg.SmartFolders {
					t.Error("SmartFolders = true, want false")
				}
				if !strings.HasPrefix(cfg.SmartFoldersDir, home()) || !strings.Contains(cfg.SmartFoldersDir, "Custom/Smart") {
					t.Errorf("SmartFoldersDir not expanded correctly: got %q", cfg.SmartFoldersDir)
				}
			},
		},
		{
			name: "omitted smart folders keys use defaults",
			user: `{"model": "custom-model"}`,
			want: func(t *testing.T, cfg *Config) {
				if !cfg.SmartFolders {
					t.Error("SmartFolders = false, want default true")
				}
				if !strings.HasPrefix(cfg.SmartFoldersDir, home()) || !strings.Contains(cfg.SmartFoldersDir, "Documents/Filemaid") {
					t.Errorf("SmartFoldersDir not expanded correctly: got %q", cfg.SmartFoldersDir)
				}
			},
		},
		{
			name: "rename top-level override",
			user: `{"rename": true, "rename_level": 3, "rename_max_length": 80, "rename_min_length": 10, "rename_image_similarity_threshold": 0.85}`,
			want: func(t *testing.T, cfg *Config) {
				if !cfg.Rename {
					t.Error("Rename = false, want true")
				}
				if cfg.RenameLevel != 3 {
					t.Errorf("RenameLevel = %d, want 3", cfg.RenameLevel)
				}
				if cfg.RenameMaxLength != 80 {
					t.Errorf("RenameMaxLength = %d, want 80", cfg.RenameMaxLength)
				}
				if cfg.RenameMinLength != 10 {
					t.Errorf("RenameMinLength = %d, want 10", cfg.RenameMinLength)
				}
				if cfg.RenameImageSimilarityThreshold != 0.85 {
					t.Errorf("RenameImageSimilarityThreshold = %v, want 0.85", cfg.RenameImageSimilarityThreshold)
				}
				if cfg.RenameInvalidChars == "" {
					t.Error("RenameInvalidChars default was dropped")
				}
			},
		},
		{
			name: "deep merge external_tools keeps defaults",
			user: `{"external_tools": {"ffmpeg": "~/bin/ffmpeg"}}`,
			want: func(t *testing.T, cfg *Config) {
				if !strings.HasPrefix(cfg.ExternalTools.FFmpeg, home()) || !strings.Contains(cfg.ExternalTools.FFmpeg, "bin/ffmpeg") {
					t.Errorf("ExternalTools.FFmpeg = %q, want expanded ~/bin/ffmpeg", cfg.ExternalTools.FFmpeg)
				}
				if cfg.RenameLevel != 2 {
					t.Errorf("RenameLevel default dropped: got %d", cfg.RenameLevel)
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

func TestDurationUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"string seconds", `"120s"`, 120 * time.Second, false},
		{"string minutes", `"2m"`, 2 * time.Minute, false},
		{"nanoseconds", `120000000000`, 120 * time.Second, false},
		{"zero string", `"0s"`, 0, false},
		{"zero number", `0`, 0, false},
		{"negative string", `"-30s"`, -30 * time.Second, false},
		{"invalid string", `"not-a-duration"`, 0, true},
		{"invalid type", `true`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := json.Unmarshal([]byte(tt.input), &d)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalJSON(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && time.Duration(d) != tt.want {
				t.Errorf("UnmarshalJSON(%q) = %v, want %v", tt.input, time.Duration(d), tt.want)
			}
		})
	}
}

func TestDurationMarshalJSON(t *testing.T) {
	var d Duration = Duration(120 * time.Second)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"2m0s"` {
		t.Errorf("MarshalJSON = %s, want \"2m0s\"", b)
	}
}

func TestLoadPathRequestTimeout(t *testing.T) {
	tests := []struct {
		name    string
		user    string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", user: "", want: 5 * time.Minute, wantErr: false},
		{name: "string override", user: `{"request_timeout": "30s"}`, want: 30 * time.Second, wantErr: false},
		{name: "numeric override", user: `{"request_timeout": 30000000000}`, want: 30 * time.Second, wantErr: false},
		{name: "zero", user: `{"request_timeout": "0s"}`, want: 0, wantErr: false},
		{name: "negative", user: `{"request_timeout": "-10s"}`, want: -10 * time.Second, wantErr: false},
		{name: "invalid string", user: `{"request_timeout": "abc"}`, want: 0, wantErr: true},
		{name: "invalid type", user: `{"request_timeout": true}`, want: 0, wantErr: true},
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
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadPath error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if time.Duration(cfg.RequestTimeout) != tt.want {
				t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, tt.want)
			}
		})
	}
}
