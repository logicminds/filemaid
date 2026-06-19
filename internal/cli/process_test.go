package cli

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func TestIsHidden(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".DS_Store", true},
		{".localized", true},
		{".secret", true},
		{"file.txt", false},
		{"/tmp/.hidden", true},
	}
	for _, c := range cases {
		if got := isHidden(c.path); got != c.want {
			t.Errorf("isHidden(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestCheckAgeRuleMatchesOldFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	downloads := filepath.Join(tmp, "Downloads")
	os.MkdirAll(downloads, 0755)
	old := filepath.Join(downloads, "old.dmg")
	if err := os.WriteFile(old, []byte("installer"), 0644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		AgeRules: []config.AgeRule{
			{Pattern: "~/Downloads/*.dmg", Days: 30, Action: "review"},
		},
	}

	decision, matched := checkAgeRule(old, cfg)
	if !matched {
		t.Fatalf("expected age rule to match")
	}
	if decision.Action != "review" {
		t.Errorf("action = %q, want review", decision.Action)
	}
	if decision.Category != "Unknown" {
		t.Errorf("category = %q, want Unknown", decision.Category)
	}
}

func TestCheckAgeRuleNoMatchForRecent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	downloads := filepath.Join(tmp, "Downloads")
	os.MkdirAll(downloads, 0755)
	recent := filepath.Join(downloads, "new.dmg")
	if err := os.WriteFile(recent, []byte("installer"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		AgeRules: []config.AgeRule{
			{Pattern: "~/Downloads/*.dmg", Days: 30, Action: "review"},
		},
	}

	_, matched := checkAgeRule(recent, cfg)
	if matched {
		t.Fatalf("expected age rule not to match recent file")
	}
}

func TestProcessPathsSkipsNonexistent(t *testing.T) {
	cfg = testConfig(t.TempDir())
	db = state.NewFake()
	processFS = actions.NewOSFS()

	buf := captureSlog(t)
	processPaths([]string{filepath.Join(t.TempDir(), "nope.txt")})

	if !bytes.Contains(buf.Bytes(), []byte("path does not exist")) {
		t.Errorf("expected 'path does not exist' warning, got %q", buf.String())
	}
}

func TestProcessPathsSkipsHidden(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewOSFS()

	hidden := filepath.Join(tmp, "Desktop", ".secret")
	os.MkdirAll(filepath.Dir(hidden), 0755)
	os.WriteFile(hidden, []byte("secret"), 0644)

	buf := captureSlog(t)
	processPaths([]string{hidden})

	if !bytes.Contains(buf.Bytes(), []byte("skipping hidden file")) {
		t.Errorf("expected 'skipping hidden file' log, got %q", buf.String())
	}
}

func TestProcessPathsSkipsOutsideAllowed(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewOSFS()

	outside := filepath.Join(tmp, "outside.txt")
	os.WriteFile(outside, []byte("outside"), 0644)

	buf := captureSlog(t)
	processPaths([]string{outside})

	if !bytes.Contains(buf.Bytes(), []byte("outside allowed dirs")) {
		t.Errorf("expected 'outside allowed dirs' warning, got %q", buf.String())
	}
}

func TestProcessPathsClassifiesAndApplies(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	if _, err := processPaths([]string{src}); err != nil {
		t.Fatal(err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}

	dest := filepath.Join(tmp, "Documents", "note.txt")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file at %s: %v", dest, err)
	}
}

func TestProcessPathsDuplicateForcesReview(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "dup",
	}}

	src1 := filepath.Join(tmp, "Desktop", "a.txt")
	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	os.MkdirAll(filepath.Dir(src1), 0755)
	os.WriteFile(src1, []byte("same"), 0644)
	os.WriteFile(src2, []byte("same"), 0644)

	if _, err := processPaths([]string{src1, src2}); err != nil {
		t.Fatal(err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 history records, got %d", len(records))
	}

	var moves, reviews int
	for _, r := range records {
		switch r.Action {
		case "move":
			moves++
		case "review":
			reviews++
		}
	}
	if moves != 1 {
		t.Errorf("expected 1 move action, got %d", moves)
	}
	if reviews != 1 {
		t.Errorf("expected 1 review action, got %d", reviews)
	}
}

type fakeClassifier struct {
	mu        sync.Mutex
	decision  llm.Decision
	err       error
	validate  error
	calls     []string
	validated bool
}

func (f *fakeClassifier) Classify(path string, fileHash string, cfg *config.Config) (llm.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	return f.decision, f.err
}

func (f *fakeClassifier) Validate(cfg *config.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validated = true
	return f.validate
}

// Calls returns a snapshot of Classify calls seen so far.
func (f *fakeClassifier) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func testConfig(tmpDir string) *config.Config {
	reviewDir := filepath.Join(tmpDir, "review")
	desktop := filepath.Join(tmpDir, "Desktop")
	downloads := filepath.Join(tmpDir, "Downloads")
	images := filepath.Join(tmpDir, "Images")
	documents := filepath.Join(tmpDir, "Documents")
	for _, d := range []string{reviewDir, desktop, downloads, images, documents} {
		os.MkdirAll(d, 0755)
	}
	return &config.Config{
		AllowedDirs:        []string{desktop, downloads, images, documents, reviewDir},
		ReviewDir:          reviewDir,
		Tags:               false,
		SafeDeletePatterns: []string{},
		Categories: map[string]string{
			"Images":    images,
			"Documents": documents,
			"Unknown":   reviewDir,
		},
		AgeRules: []config.AgeRule{
			{Pattern: "~/Downloads/*.dmg", Days: 30, Action: "review"},
		},
	}
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	})
	return &buf
}
func TestProcessPathsFallsBackToReviewOnClassifyError(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{err: errors.New("ollama unreachable")}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	if _, err := processPaths([]string{src}); err != nil {
		t.Fatal(err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
	if records[0].Action != "review" {
		t.Errorf("action = %q, want review fallback", records[0].Action)
	}
}

func TestProcessPathsLogsApplyError(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	classifier = &fakeClassifier{decision: llm.Decision{Category: "Documents", Action: "move", Reason: "text"}}
	applyDecision = func(llm.Decision, string, string, *config.Config, state.Repo, bool, actions.FS) (string, error) {
		return "", errors.New("move failed")
	}
	t.Cleanup(func() { applyDecision = actions.Apply })

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	buf := captureSlog(t)
	processPaths([]string{src})

	if !bytes.Contains(buf.Bytes(), []byte("apply failed")) {
		t.Errorf("expected 'apply failed' log, got %q", buf.String())
	}
}

func TestProcessPathsResultsInInputOrder(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}

	paths := make([]string, 5)
	for i := range paths {
		p := filepath.Join(tmp, "Desktop", fmt.Sprintf("file%d.txt", i))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(fmt.Sprintf("content%d", i)), 0644)
		paths[i] = p
	}

	results, err := processPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}
	for i, want := range paths {
		if results[i].Path != want {
			t.Errorf("results[%d].Path = %q, want %q", i, results[i].Path, want)
		}
	}
}

// blockingClassifier blocks Classify calls until release() is invoked so tests
// can observe concurrent classification.
type blockingClassifier struct {
	fakeClassifier
	mu        sync.Mutex
	cond      *sync.Cond
	active    int
	maxActive int
	proceed   bool
}

func newBlockingClassifier() *blockingClassifier {
	b := &blockingClassifier{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *blockingClassifier) Classify(path string, fileHash string, cfg *config.Config) (llm.Decision, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	for !b.proceed {
		b.cond.Wait()
	}
	b.active--
	b.mu.Unlock()
	return b.fakeClassifier.Classify(path, fileHash, cfg)
}

func (b *blockingClassifier) release() {
	b.mu.Lock()
	b.proceed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

func TestProcessPathsProcessesConcurrently(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	bc := newBlockingClassifier()
	bc.decision = llm.Decision{Category: "Documents", Action: "review", Reason: "test"}
	classifier = bc
	t.Cleanup(func() { classifier = llm.NewClient(nil) })

	paths := make([]string, 5)
	for i := range paths {
		p := filepath.Join(tmp, "Desktop", fmt.Sprintf("concurrent%d.txt", i))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(fmt.Sprintf("content%d", i)), 0644)
		paths[i] = p
	}

	done := make(chan []processResult)
	go func() {
		res, err := processPaths(paths)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()

	// Wait for at least two concurrent Classify calls to confirm workers run
	// in parallel rather than sequentially.
	for {
		bc.mu.Lock()
		ma := bc.maxActive
		bc.mu.Unlock()
		if ma >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	bc.release()
	results := <-done

	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}

	bc.mu.Lock()
	peak := bc.maxActive
	bc.mu.Unlock()
	if peak < 2 {
		t.Errorf("expected concurrent classification, got max active %d", peak)
	}
}

func TestProcessCommandFailsValidationBeforeTouchingFiles(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fake := &fakeClassifier{validate: errors.New("model not ready")}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	err := processCmd.RunE(nil, []string{src})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "model validation failed") {
		t.Errorf("expected model validation failed error, got %v", err)
	}
	if !fake.validated {
		t.Error("expected Validate to be called")
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records, got %d", len(records))
	}

	// Ensure the file was not moved.
	dest := filepath.Join(tmp, "Documents", "note.txt")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("expected no file at %s", dest)
	}
}

func TestProcessCommandSucceedsWhenValidationPasses(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fake := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	if err := processCmd.RunE(nil, []string{src}); err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if !fake.validated {
		t.Error("expected Validate to be called")
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
}

func TestFormatProcessTable(t *testing.T) {
	results := []processResult{
		{Path: "/tmp/note.txt", Category: "Documents", Tags: []string{"txt"}, Action: "move", Result: "/archive/note.txt", OK: true},
		{Path: "/tmp/unknown", Category: "Unknown", Tags: []string{}, Action: "review", Result: "/review/unknown", OK: true},
		{Path: "/tmp/bad", Category: "Documents", Tags: []string{}, Action: "move", Result: "", OK: false, Error: "move failed"},
	}
	out := formatProcessTable(results)
	for _, want := range []string{"File", "Category", "Tags", "Action", "Result", "Status", "Documents", "txt", "✅", "❌"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatProcessResultsJSON(t *testing.T) {
	results := []processResult{
		{Path: "/tmp/note.txt", Category: "Documents", Tags: []string{"txt"}, Action: "move", Result: "/archive/note.txt", OK: true},
	}
	out, err := formatProcessResults(results, "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"path": "/tmp/note.txt"`, `"category": "Documents"`, `"tags": [`, `"action": "move"`, `"result": "/archive/note.txt"`, `"ok": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

func TestProcessCommandPrintsTable(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	out := captureStdout(t, func() {
		if err := processCmd.RunE(nil, []string{src}); err != nil {
			t.Fatalf("process failed: %v", err)
		}
	})
	if !strings.Contains(out, "File") || !strings.Contains(out, "✅") {
		t.Errorf("expected table output, got:\n%s", out)
	}
}

func TestProcessCommandPrintsJSON(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	processJSON = true
	t.Cleanup(func() { processJSON = false })

	out := captureStdout(t, func() {
		if err := processCmd.RunE(nil, []string{src}); err != nil {
			t.Fatalf("process failed: %v", err)
		}
	})
	if !strings.Contains(out, `"ok": true`) {
		t.Errorf("expected JSON output, got:\n%s", out)
	}
}
