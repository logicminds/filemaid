package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Duration is a time.Duration that can be unmarshaled from either a JSON
// number (nanoseconds) or a JSON string accepted by time.ParseDuration.
type Duration time.Duration

// UnmarshalJSON supports "120s", "2m", or a nanosecond integer.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid request_timeout %q: %w", s, err)
		}
		*d = Duration(parsed)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("request_timeout must be a duration string or nanosecond integer: %w", err)
	}
	*d = Duration(n)
	return nil
}

// MarshalJSON emits a human-readable duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Config holds the filemaid configuration.
type Config struct {
	OllamaURL           string                   `json:"ollama_url"`
	Model               string                   `json:"model"`
	ImageModel          string                   `json:"image_model"`
	TextModel           string                   `json:"text_model"`
	WatchDirs           []string                 `json:"watch_dirs"`
	AllowedDirs         []string                 `json:"allowed_dirs"`
	AllowedCleaners     []string                 `json:"allowed_cleaners"`
	ReviewDir           string                   `json:"review_dir"`
	LogPath             string                   `json:"log_path"`
	DBPath              string                   `json:"db_path"`
	Tags                bool                     `json:"tags"`
	Comments            bool                     `json:"comments"`
	SubcategorizeImages bool                     `json:"subcategorize_images"`
	MinAgeHours         int                      `json:"min_age_hours"`
	SmartFolders        bool                     `json:"smart_folders"`
	SmartFoldersDir     string                   `json:"smart_folders_dir"`
	RequestTimeout      Duration                 `json:"request_timeout"`
	Categories          map[string]string        `json:"categories"`
	SafeDeletePatterns  []string                 `json:"safe_delete_patterns"`
	AgeRules            []AgeRule                `json:"age_rules"`
	DevCleanup          map[string]CleanerConfig `json:"dev_cleanup"`
	ReviewCleanup       ReviewCleanupConfig      `json:"review_cleanup"`
}

// AgeRule describes a pattern-based automatic action.
type AgeRule struct {
	Pattern string `json:"pattern"`
	Days    int    `json:"days"`
	Action  string `json:"action"`
}

// CleanerConfig enables and configures a single dev cleaner.
type CleanerConfig struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

// ReviewCleanupConfig configures review-queue cleanup.
type ReviewCleanupConfig struct {
	Enabled    bool   `json:"enabled"`
	Mode       string `json:"mode"`
	MaxAgeDays int    `json:"max_age_days"`
}

// Defaults returns a fully populated default configuration.
func Defaults() *Config {
	return &Config{
		OllamaURL:           "http://localhost:11434",
		Model:               "filemaid-metadata",
		ImageModel:          "filemaid-gemma4-12b",
		TextModel:           "filemaid-metadata",
		AllowedDirs:         []string{"~/Desktop", "~/Downloads", "~/Documents/Archive", "~/.filemaid/review"},
		AllowedCleaners:     []string{"docker", "npm", "cargo", "pip", "brew", "xcode"},
		ReviewDir:           "~/.filemaid/review",
		LogPath:             "~/.local/share/filemaid/filemaid.log",
		DBPath:              "~/.local/share/filemaid/filemaid.db",
		Tags:                true,
		Comments:            true,
		SubcategorizeImages: true,
		MinAgeHours:         0,
		SmartFolders:        true,
		SmartFoldersDir:     "~/Documents/Filemaid",
		RequestTimeout:      Duration(120 * time.Second),
		Categories: map[string]string{
			"Screenshots": "~/Documents/Archive/Screenshots",
			"Documents":   "~/Documents/Archive/Documents",
			"Receipts":    "~/Documents/Archive/Receipts",
			"Images":      "~/Documents/Archive/Images",
			"Installers":  "~/Documents/Archive/Installers",
			"Code":        "~/Documents/Archive/Code",
			"Archives":    "~/Documents/Archive/Archives",
			"Media":       "~/Documents/Archive/Media",
			"Unknown":     "~/.filemaid/review",
		},
		SafeDeletePatterns: []string{},
		AgeRules: []AgeRule{
			{Pattern: "~/Downloads/*.dmg", Days: 30, Action: "review"},
		},
		DevCleanup: map[string]CleanerConfig{
			"docker": {Enabled: true, Mode: "safe"},
			"npm":    {Enabled: true, Mode: "safe"},
			"cargo":  {Enabled: true, Mode: "safe"},
			"pip":    {Enabled: true, Mode: "safe"},
			"brew":   {Enabled: true, Mode: "safe"},
			"xcode":  {Enabled: true, Mode: "safe"},
		},
		ReviewCleanup: ReviewCleanupConfig{
			Enabled:    true,
			Mode:       "safe",
			MaxAgeDays: 30,
		},
	}
}

// Load returns defaults merged with ~/.config/filemaid/config.json.
func Load() (*Config, error) {
	return LoadPath(filepath.Join(home(), ".config", "filemaid", "config.json"))
}

// LoadPath returns defaults merged with the JSON config at path.
// Nested objects (categories, dev_cleanup, review_cleanup) are merged deeply
// so that a user override for one key does not erase the remaining defaults.
// All string values containing "~" are expanded to the user's home directory,
// including strings inside slices and maps.
func LoadPath(path string) (*Config, error) {
	merged, err := defaultsAsMap()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			expandTildeInMap(merged)
			ensureUnknownInMap(merged)
			return configFromMap(merged)
		}
		return nil, err
	}

	var user map[string]interface{}
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	deepMerge(merged, user)
	expandTildeInMap(merged)
	ensureUnknownInMap(merged)
	return configFromMap(merged)
}

func defaultsAsMap() (map[string]interface{}, error) {
	data, err := json.Marshal(Defaults())
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func configFromMap(m map[string]interface{}) (*Config, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// deepMerge copies src into dst. Map values are merged recursively;
// all other values (including slices) are replaced.
func deepMerge(dst, src map[string]interface{}) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]interface{}); ok {
			if dm, ok := dst[k].(map[string]interface{}); ok {
				deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

// expandTildeInMap recursively expands "~" to the user's home directory for
// every string value, including strings nested inside slices and maps.
func expandTildeInMap(m map[string]interface{}) {
	for k, v := range m {
		m[k] = expandTilde(v)
	}
}

func expandTilde(v interface{}) interface{} {
	switch x := v.(type) {
	case string:
		return expandPath(x)
	case []interface{}:
		for i := range x {
			x[i] = expandTilde(x[i])
		}
		return x
	case map[string]interface{}:
		for k, val := range x {
			x[k] = expandTilde(val)
		}
		return x
	default:
		return v
	}
}

func ensureUnknownInMap(m map[string]interface{}) {
	reviewDir, _ := m["review_dir"].(string)
	cats, ok := m["categories"].(map[string]interface{})
	if !ok {
		cats = make(map[string]interface{})
		m["categories"] = cats
	}
	if _, ok := cats["Unknown"]; !ok {
		cats["Unknown"] = reviewDir
	}
}

// expand expands remaining "~" placeholders in the typed struct fields.
// It is kept for callers that build a Config directly from Defaults().
func (c *Config) expand() {
	c.ReviewDir = expandPath(c.ReviewDir)
	c.LogPath = expandPath(c.LogPath)
	c.DBPath = expandPath(c.DBPath)

	expandSlice(&c.WatchDirs)
	expandSlice(&c.AllowedDirs)
	expandMap(&c.Categories)
	expandSlice(&c.SafeDeletePatterns)

	for i := range c.AgeRules {
		c.AgeRules[i].Pattern = expandPath(c.AgeRules[i].Pattern)
	}
}

func (c *Config) ensureUnknown() {
	if c.Categories == nil {
		c.Categories = make(map[string]string)
	}
	if _, ok := c.Categories["Unknown"]; !ok {
		c.Categories["Unknown"] = c.ReviewDir
	}
}

func expandPath(s string) string {
	if s == "" {
		return s
	}
	if s == "~" {
		return home()
	}
	if strings.HasPrefix(s, "~/") {
		h := home()
		if h == "" {
			return s
		}
		return filepath.Join(h, s[2:])
	}
	return s
}

func expandSlice(ss *[]string) {
	for i := range *ss {
		(*ss)[i] = expandPath((*ss)[i])
	}
}

func expandMap(m *map[string]string) {
	if *m == nil {
		return
	}
	for k, v := range *m {
		(*m)[k] = expandPath(v)
	}
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}
