//go:build acceptance

package llm

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

// acceptance is set by `go test -tags=acceptance ./internal/llm`.
// To run against the real Ollama server:
//
//	go test -tags=acceptance -run TestAcceptance ./internal/llm
var acceptance = flag.Bool("acceptance", false, "run acceptance tests against a real Ollama server")

// acceptanceConfig returns a configuration that points at the local Ollama
// instance and uses the default model. Callers may override OLLAMA_URL or
// FILEMAID_MODEL in the environment.
func acceptanceConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	if u := os.Getenv("OLLAMA_URL"); u != "" {
		cfg.OllamaURL = u
	}
	if m := os.Getenv("FILEMAID_MODEL"); m != "" {
		cfg.Model = m
	}
	return cfg
}

func skipUnlessAcceptance(t *testing.T) {
	t.Helper()
	if !*acceptance {
		t.Skip("skipping acceptance test; run with -tags=acceptance -acceptance")
	}
}

// TestAcceptanceClassifyRealFiles sends the testdata files to the configured
// Ollama model and asserts that the returned decisions are sensible.
func TestAcceptanceClassifyRealFiles(t *testing.T) {
	skipUnlessAcceptance(t)

	cfg := acceptanceConfig(t)
	client := NewClient(nil)

	if err := client.Validate(cfg); err != nil {
		t.Fatalf("ollama validation failed: %v", err)
	}

	cases := []struct {
		file         string
		wantCategory string
		wantAction   string
	}{
		{"../../testdata/acceptance/sample.md", "Documents", "move"},
		{"../../testdata/acceptance/sample.pdf", "Documents", "move"},
		{"../../testdata/acceptance/photo.jpg", "Images", "move"},
		{"../../testdata/acceptance/screenshot.png", "Screenshots", "move"},
	}

	for _, tc := range cases {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			path, err := filepath.Abs(tc.file)
			if err != nil {
				t.Fatalf("abs path: %v", err)
			}

			decision, err := client.Classify(path, "", cfg)
			if err != nil {
				t.Fatalf("classify %s: %v", path, err)
			}

			t.Logf("decision for %s: category=%q subcategory=%q tags=%v action=%q reason=%q",
				filepath.Base(path), decision.Category, decision.Subcategory,
				decision.Tags, decision.Action, decision.Reason)

			if decision.Category != tc.wantCategory {
				t.Errorf("category = %q, want %q", decision.Category, tc.wantCategory)
			}
			if decision.Action != tc.wantAction {
				t.Errorf("action = %q, want %q", decision.Action, tc.wantAction)
			}
			if len(decision.Tags) == 0 {
				t.Errorf("expected at least one tag, got none")
			}
			if strings.TrimSpace(decision.Reason) == "" {
				t.Logf("reason is empty for %s (acceptable for metadata-only models)", filepath.Base(path))
			}
		})
	}
}

// TestAcceptanceParseableResponse verifies that Classify returns a parseable
// decision for every testdata file rather than falling back to Unknown/review
// because the model response could not be parsed.
func TestAcceptanceParseableResponse(t *testing.T) {
	skipUnlessAcceptance(t)

	cfg := acceptanceConfig(t)
	client := NewClient(nil)

	files := []string{
		"../../testdata/acceptance/sample.md",
		"../../testdata/acceptance/sample.pdf",
		"../../testdata/acceptance/photo.jpg",
		"../../testdata/acceptance/screenshot.png",
	}

	var unparsable []string
	for _, f := range files {
		path, err := filepath.Abs(f)
		if err != nil {
			t.Fatalf("abs path: %v", err)
		}

		decision, err := client.Classify(path, "", cfg)
		if err != nil {
			t.Fatalf("classify %s: %v", path, err)
		}

		// A parseable response must not have defaulted to Unknown/review for
		// parsing reasons. Real ambiguity is fine, but the acceptance suite
		// uses clear samples so review is treated as a parsing/classification
		// failure here.
		if decision.Category == "Unknown" && decision.Action == "review" &&
			strings.Contains(decision.Reason, "could not parse model response") {
			unparsable = append(unparsable, fmt.Sprintf("%s: %s", filepath.Base(path), decision.Reason))
		}
	}

	if len(unparsable) > 0 {
		t.Fatalf("unparsable model responses:\n%s", strings.Join(unparsable, "\n"))
	}
}
