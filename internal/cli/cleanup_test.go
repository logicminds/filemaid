package cli

import (
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/cleaners"
	"github.com/logicminds/filemaid/internal/config"
)

func TestFormatCleanupTable(t *testing.T) {
	saved := int64(1024)
	results := []cleaners.CleanupResult{
		{Name: "pip", Status: "ok", Saved: &saved, SavedHuman: "1.0 kB", Detail: "pip: cleaned"},
		{Name: "docker", Status: "not_allowed", Detail: "not in allowed_cleaners whitelist"},
	}
	out := formatCleanupTable(results)
	if !strings.Contains(out, "pip") {
		t.Errorf("table missing pip: %s", out)
	}
	if !strings.Contains(out, "1.0 kB") {
		t.Errorf("table missing saved space: %s", out)
	}
	if !strings.Contains(out, "not_allowed") {
		t.Errorf("table missing status: %s", out)
	}
}

func TestFormatCleanupResultsJSON(t *testing.T) {
	results := []cleaners.CleanupResult{
		{Name: "pip", Status: "ok", Detail: "pip: cleaned"},
	}
	out, err := formatCleanupResults(results, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name": "pip"`) {
		t.Errorf("json missing pip: %s", out)
	}
}

func TestRunCleanupFiltersByAllowedCleaners(t *testing.T) {
	cfg = &config.Config{
		AllowedCleaners: []string{"pip"},
		DevCleanup: map[string]config.CleanerConfig{
			"pip": {Enabled: true, Mode: "safe"},
		},
	}

	cleanerRegistry = func() []cleaners.Cleaner {
		return []cleaners.Cleaner{
			{Name: "docker", CanRun: func() bool { return true }, Run: func(bool, *config.Config) cleaners.CleanupResult {
				return cleaners.CleanupResult{Status: "ok", Detail: "docker: cleaned"}
			}},
			{Name: "pip", CanRun: func() bool { return true }, Run: func(bool, *config.Config) cleaners.CleanupResult {
				return cleaners.CleanupResult{Status: "ok", Detail: "pip: cleaned"}
			}},
		}
	}
	t.Cleanup(func() { cleanerRegistry = cleaners.Registry })

	results := runCleanup(true)
	names := make(map[string]string)
	for _, r := range results {
		names[r.Name] = r.Status
	}
	if names["docker"] != "not_allowed" {
		t.Errorf("docker status = %q, want not_allowed", names["docker"])
	}
	if names["pip"] != "ok" {
		t.Errorf("pip status = %q, want ok", names["pip"])
	}
}
