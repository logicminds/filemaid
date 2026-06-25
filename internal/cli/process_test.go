package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/directory"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
	"github.com/spf13/cobra"
)

// inputs converts a list of path strings into processInput values for tests
// that do not need directory context.
func inputs(paths ...string) []processInput {
	out := make([]processInput, len(paths))
	for i, p := range paths {
		out[i] = processInput{path: p}
	}
	return out
}

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
	processPaths(context.Background(), inputs(filepath.Join(t.TempDir(), "nope.txt")), "run-test", io.Discard, "")

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
	processPaths(context.Background(), inputs(hidden), "run-test", io.Discard, "")

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
	processPaths(context.Background(), inputs(outside), "run-test", io.Discard, "")

	if !bytes.Contains(buf.Bytes(), []byte("outside allowed dirs")) {
		t.Errorf("expected 'outside allowed dirs' warning, got %q", buf.String())
	}
}

func TestProcessPathsClassifiesAndApplies(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
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

	if _, err := processPaths(context.Background(), inputs(src), "run-test", io.Discard, ""); err != nil {
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
	cfg.MoveFiles = true
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

	if _, err := processPaths(context.Background(), inputs(src1, src2), "run-test", io.Discard, ""); err != nil {
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
	mu          sync.Mutex
	decision    llm.Decision
	dirDecision llm.DirectoryDecision
	err         error
	validate    error
	calls       []string
	models      []string
	dirCalls    []string
	validated   bool
}

func (f *fakeClassifier) Classify(ctx context.Context, path string, fileHash string, cfg *config.Config, dirCtx *directory.Context) (llm.Decision, llm.Metrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	f.models = append(f.models, cfg.Model)
	return f.decision, llm.Metrics{}, f.err
}

func (f *fakeClassifier) ClassifyDirectory(ctx context.Context, meta *directory.Metadata, cfg *config.Config) (llm.DirectoryDecision, llm.Metrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirCalls = append(f.dirCalls, meta.Path)
	decision := f.dirDecision
	if decision.Recommendation == "" {
		decision.Recommendation = "review"
		decision.Reason = "fake default"
	}
	return decision, llm.Metrics{}, f.err
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

// Models returns a snapshot of cfg.Model values passed to Classify.
func (f *fakeClassifier) Models() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.models))
	copy(out, f.models)
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
		Model:              "text-model",
		ImageModel:         "image-model",
		TextModel:          "text-model",
		AllowedDirs:        []string{desktop, downloads, images, documents, reviewDir},
		ReviewDir:          reviewDir,
		Tags:               false,
		Comments:           false,
		SafeDeletePatterns: []string{},
		ProcessWorkers:     4,
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

	if _, err := processPaths(context.Background(), inputs(src), "run-test", io.Discard, ""); err != nil {
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
	applyDecision = func(llm.Decision, string, string, *config.Config, state.Repo, bool, actions.FS, string, llm.Metrics, bool) (string, error) {
		return "", errors.New("move failed")
	}
	t.Cleanup(func() { applyDecision = actions.Apply })

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	buf := captureSlog(t)
	processPaths(context.Background(), inputs(src), "run-test", io.Discard, "")

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

	results, err := processPaths(context.Background(), inputs(paths...), "run-test", io.Discard, "")
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

func (b *blockingClassifier) Classify(ctx context.Context, path string, fileHash string, cfg *config.Config, dirCtx *directory.Context) (llm.Decision, llm.Metrics, error) {
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
	decision, _, err := b.fakeClassifier.Classify(ctx, path, fileHash, cfg, dirCtx)
	return decision, llm.Metrics{}, err
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
		res, err := processPaths(context.Background(), inputs(paths...), "run-test", io.Discard, "")
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

func TestProcessPathsUsesConfiguredWorkers(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.ProcessWorkers = 2
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	bc := newBlockingClassifier()
	bc.decision = llm.Decision{Category: "Documents", Action: "review", Reason: "test"}
	classifier = bc
	t.Cleanup(func() { classifier = llm.NewClient(nil) })

	paths := make([]string, 5)
	for i := range paths {
		p := filepath.Join(tmp, "Desktop", fmt.Sprintf("worker%d.txt", i))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(fmt.Sprintf("content%d", i)), 0644)
		paths[i] = p
	}

	done := make(chan []processResult)
	go func() {
		res, err := processPaths(context.Background(), inputs(paths...), "run-test", io.Discard, "")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()

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
	if peak != 2 {
		t.Errorf("expected worker concurrency of 2, got max active %d", peak)
	}
}

// blockingApplier blocks Apply calls until release() is invoked so tests can
// observe concurrent applies.
type blockingApplier struct {
	mu        sync.Mutex
	cond      *sync.Cond
	active    int
	maxActive int
	proceed   bool
}

func newBlockingApplier() *blockingApplier {
	b := &blockingApplier{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *blockingApplier) Apply(decision llm.Decision, src string, fileHash string, cfg *config.Config, db state.Repo, isDuplicate bool, fs actions.FS, runID string, metrics llm.Metrics, force bool) (string, error) {
	b.mu.Lock()
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	b.mu.Unlock()

	b.mu.Lock()
	for !b.proceed {
		b.cond.Wait()
	}
	b.active--
	b.mu.Unlock()

	return "", nil
}

func (b *blockingApplier) release() {
	b.mu.Lock()
	b.proceed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

// pathClassifier returns a decision whose category depends on the input path.
type pathClassifier struct {
	mu    sync.Mutex
	calls []string
}

func (p *pathClassifier) Classify(ctx context.Context, src string, fileHash string, cfg *config.Config, dirCtx *directory.Context) (llm.Decision, llm.Metrics, error) {
	p.mu.Lock()
	p.calls = append(p.calls, src)
	p.mu.Unlock()
	if strings.Contains(filepath.Base(src), "image") {
		return llm.Decision{Category: "Images", Action: "move"}, llm.Metrics{}, nil
	}
	return llm.Decision{Category: "Documents", Action: "move"}, llm.Metrics{}, nil
}

func (p *pathClassifier) Validate(cfg *config.Config) error { return nil }

func TestProcessPathsAppliesConcurrentlyForDifferentDirs(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.ProcessWorkers = 2
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	pc := &pathClassifier{}
	classifier = pc
	t.Cleanup(func() { classifier = llm.NewClient(nil) })

	ba := newBlockingApplier()
	applyDecision = ba.Apply
	t.Cleanup(func() { applyDecision = actions.Apply })

	paths := []string{
		filepath.Join(tmp, "Desktop", "image1.jpg"),
		filepath.Join(tmp, "Desktop", "doc1.txt"),
	}
	for i, p := range paths {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(fmt.Sprintf("content%d", i)), 0644)
	}

	done := make(chan []processResult)
	go func() {
		res, err := processPaths(context.Background(), inputs(paths...), "run-test", io.Discard, "")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()

	for {
		ba.mu.Lock()
		ma := ba.maxActive
		ba.mu.Unlock()
		if ma >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ba.release()
	results := <-done

	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}

	ba.mu.Lock()
	peak := ba.maxActive
	ba.mu.Unlock()
	if peak < 2 {
		t.Errorf("expected concurrent applies for different dirs, got max active %d", peak)
	}
}

func TestProcessPathsAppliesSeriallyForSameDir(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.ProcessWorkers = 2
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	classifier = &fakeClassifier{decision: llm.Decision{Category: "Documents", Action: "move", Reason: "test"}}
	t.Cleanup(func() { classifier = llm.NewClient(nil) })

	ba := newBlockingApplier()
	applyDecision = ba.Apply
	t.Cleanup(func() { applyDecision = actions.Apply })

	paths := []string{
		filepath.Join(tmp, "Desktop", "doc1.txt"),
		filepath.Join(tmp, "Desktop", "doc2.txt"),
	}
	for i, p := range paths {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(fmt.Sprintf("content%d", i)), 0644)
	}

	done := make(chan []processResult)
	go func() {
		res, err := processPaths(context.Background(), inputs(paths...), "run-test", io.Discard, "")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()

	time.Sleep(50 * time.Millisecond)
	ba.release()
	results := <-done

	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}

	ba.mu.Lock()
	peak := ba.maxActive
	ba.mu.Unlock()
	if peak != 1 {
		t.Errorf("expected serial applies for same dir, got max active %d", peak)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	results := []processResult{
		{Path: "/tmp/note.txt", Category: "Documents", Tags: []string{"txt"}, Action: "move", Result: "/archive/note.txt", OriginalName: "note.txt", OK: true},
		{Path: filepath.Join(home, "Downloads", "receipt.pdf"), Category: "Receipts", Tags: []string{"pdf"}, Action: "move", Result: filepath.Join(home, "Documents", "Archive", "Receipts", "receipt.pdf"), OriginalName: "receipt.pdf", OK: true},
		{Path: "/tmp/unknown", Category: "Unknown", Tags: []string{}, Action: "review", Result: "/review/unknown", OriginalName: "unknown", OK: true},
		{Path: "/tmp/bad", Category: "Documents", Tags: []string{}, Action: "move", Result: "", OriginalName: "bad", OK: false, Error: "move failed"},
		{Path: "/tmp/photo.jpg", Category: "Images", Tags: []string{"jpg"}, Action: "move", Result: "/archive/vacation-photo.jpg", OriginalName: "photo.jpg", NewName: "vacation-photo.jpg", OK: true},
	}
	out := formatProcessTable(results)
	for _, want := range []string{"Item", "Category", "Tags", "Action", "Name", "Result", "Status", "Documents", "txt", "Receipts", "pdf", "✓", "⚠", "kept name", "photo.jpg -> vacation-photo.jpg"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"~/Downloads/receipt.pdf", "~/Documents/Archive/Receipts/receipt.pdf"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output should collapse home to %q:\n%s", want, out)
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

func TestFormatHuman(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	results := []processResult{
		{Path: filepath.Join(home, "Desktop", "note.txt"), Category: "Documents", Tags: []string{"txt"}, Action: "move", Result: filepath.Join(home, "Documents", "Archive", "note.txt"), OriginalName: "note.txt", OK: true},
		{Path: "/tmp/missing", Action: "skip", Result: "-", OriginalName: "missing", OK: false, Error: "path does not exist"},
		{Path: "/tmp/photo.jpg", Category: "Images", Tags: []string{"jpg"}, Action: "move", Result: "/archive/vacation-photo.jpg", OriginalName: "photo.jpg", NewName: "vacation-photo.jpg", OK: true},
	}
	out := formatHuman(results)
	for _, want := range []string{"note.txt", "Documents", "txt", "move", "kept name", "~/Documents/Archive/note.txt", "path does not exist", "3 files processed", "2 ok", "1 skip", "photo.jpg -> vacation-photo.jpg"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestProcessPathsIncludesSkippedResults(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewOSFS()

	valid := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(valid), 0755)
	os.WriteFile(valid, []byte("hello"), 0644)
	missing := filepath.Join(tmp, "Desktop", "gone.txt")

	results, err := processPaths(context.Background(), inputs(valid, missing), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Path != valid {
		t.Errorf("results[0].Path = %q, want %q", results[0].Path, valid)
	}
	if results[1].Path != missing {
		t.Errorf("results[1].Path = %q, want %q", results[1].Path, missing)
	}
	if results[1].Action != "skip" {
		t.Errorf("results[1].Action = %q, want skip", results[1].Action)
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

	processFormat = "table"
	t.Cleanup(func() { processFormat = "human" })

	out := captureStdout(t, func() {
		if err := processCmd.RunE(nil, []string{src}); err != nil {
			t.Fatalf("process failed: %v", err)
		}
	})
	if !strings.Contains(out, "Item") || !strings.Contains(out, "✓") {
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

func TestFormatProcessResultsJSONIncludesMetrics(t *testing.T) {
	results := []processResult{
		{
			Path:             "/tmp/note.txt",
			Category:         "Documents",
			Tags:             []string{"txt"},
			Action:           "move",
			Result:           "/archive/note.txt",
			OK:               true,
			DurationMs:       1234,
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
			TokensPerSec:     10.5,
			ContextSize:      4096,
		},
	}
	out, err := formatProcessResults(results, "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"duration_ms": 1234`,
		`"prompt_tokens": 10`,
		`"completion_tokens": 5`,
		`"total_tokens": 15`,
		`"tokens_per_sec": 10.5`,
		`"context_size": 4096`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

func TestModelForPathRoutesByExtension(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(tmp)

	tests := []struct {
		path string
		want string
	}{
		{"photo.png", "image-model"},
		{"photo.jpg", "image-model"},
		{"photo.jpeg", "image-model"},
		{"doc.txt", "text-model"},
		{"doc.pdf", "text-model"},
		{"archive.zip", "text-model"},
	}
	for _, tc := range tests {
		t.Run(filepath.Ext(tc.path), func(t *testing.T) {
			got := modelForPath(filepath.Join(tmp, tc.path), cfg)
			if got != tc.want {
				t.Errorf("modelForPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestProcessPathsGroupsByModel(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	fake := &fakeClassifier{decision: llm.Decision{Category: "Documents", Action: "move", Reason: "test"}}
	classifier = fake

	files := []string{
		filepath.Join(tmp, "Desktop", "a.txt"),
		filepath.Join(tmp, "Images", "b.png"),
		filepath.Join(tmp, "Documents", "c.txt"),
		filepath.Join(tmp, "Images", "d.jpg"),
	}
	for _, f := range files {
		os.MkdirAll(filepath.Dir(f), 0755)
		os.WriteFile(f, []byte(f), 0644)
	}

	if _, err := processPaths(context.Background(), inputs(files...), "run-test", io.Discard, ""); err != nil {
		t.Fatalf("processPaths failed: %v", err)
	}

	calls := fake.Calls()
	models := fake.Models()
	if len(models) != len(files) {
		t.Fatalf("got %d Classify calls, want %d", len(models), len(files))
	}

	pathToModel := make(map[string]string, len(files))
	for i, path := range calls {
		pathToModel[path] = models[i]
	}

	for _, path := range files {
		want := "text-model"
		if llm.IsImageFile(path) {
			want = "image-model"
		}
		got, ok := pathToModel[path]
		if !ok {
			t.Errorf("file %q was not classified", path)
			continue
		}
		if got != want {
			t.Errorf("file %q classified with model %q, want %q", path, got, want)
		}
	}
}

func TestProcessPathsFallsBackToModel(t *testing.T) {
	tmp := t.TempDir()
	cfg = &config.Config{
		Model:       "fallback-model",
		AllowedDirs: []string{tmp},
		ReviewDir:   tmp,
		Tags:        false,
		Categories:  map[string]string{"Unknown": tmp},
	}
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	fake := &fakeClassifier{decision: llm.Decision{Category: "Unknown", Action: "review"}}
	classifier = fake

	files := []string{
		filepath.Join(tmp, "a.txt"),
		filepath.Join(tmp, "b.png"),
	}
	for _, f := range files {
		os.WriteFile(f, []byte(f), 0644)
	}

	if _, err := processPaths(context.Background(), inputs(files...), "run-test", io.Discard, ""); err != nil {
		t.Fatalf("processPaths failed: %v", err)
	}

	for _, m := range fake.Models() {
		if m != "fallback-model" {
			t.Errorf("expected fallback model, got %q", m)
		}
	}
}
func TestProcessPathsUsesCachedDecisionWithoutClassifying(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	fc := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "move",
		Reason:   "should not run",
	}}
	classifier = fc

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("cached"), 0644)

	hash, err := actions.ComputeHash(src)
	if err != nil {
		t.Fatalf("compute hash: %v", err)
	}
	_ = db.RecordDecision(hash, llm.Decision{
		Category: "Images",
		Action:   "move",
		Reason:   "from cache",
	})

	if _, err := processPaths(context.Background(), inputs(src), "run-test", io.Discard, ""); err != nil {
		t.Fatal(err)
	}

	if len(fc.Calls()) != 0 {
		t.Errorf("expected classifier to be skipped, got %d calls", len(fc.Calls()))
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
	if records[0].Category != "Images" {
		t.Errorf("category = %q, want Images", records[0].Category)
	}
	if records[0].Action != "move" {
		t.Errorf("action = %q, want move", records[0].Action)
	}
}

func TestProcessPathsDuplicateSkipsClassification(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	fc := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "move",
		Reason:   "text",
	}}
	classifier = fc

	src1 := filepath.Join(tmp, "Desktop", "a.txt")
	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	os.MkdirAll(filepath.Dir(src1), 0755)
	os.WriteFile(src1, []byte("same"), 0644)
	os.WriteFile(src2, []byte("same"), 0644)

	if _, err := processPaths(context.Background(), inputs(src1, src2), "run-test", io.Discard, ""); err != nil {
		t.Fatal(err)
	}

	if len(fc.Calls()) != 1 {
		t.Errorf("expected 1 classification, got %d", len(fc.Calls()))
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
		t.Errorf("expected 1 move, got %d", moves)
	}
	if reviews != 1 {
		t.Errorf("expected 1 review, got %d", reviews)
	}
}

func TestProcessPathsDuplicateWithSafeDeleteStillClassifies(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	cfg.SafeDeletePatterns = []string{"~/Downloads/*.tmp"}
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	fc := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "move",
		Reason:   "text",
	}}
	classifier = fc

	downloads := filepath.Join(tmp, "Downloads")
	os.MkdirAll(downloads, 0755)
	src1 := filepath.Join(downloads, "a.tmp")
	src2 := filepath.Join(downloads, "b.tmp")
	os.WriteFile(src1, []byte("same"), 0644)
	os.WriteFile(src2, []byte("same"), 0644)

	if _, err := processPaths(context.Background(), inputs(src1, src2), "run-test", io.Discard, ""); err != nil {
		t.Fatal(err)
	}

	if len(fc.Calls()) != 2 {
		t.Errorf("expected 2 classifications for safe-delete duplicates, got %d", len(fc.Calls()))
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 history records, got %d", len(records))
	}

	var moves, deletes int
	for _, r := range records {
		switch r.Action {
		case "move":
			moves++
		case "delete":
			deletes++
		}
	}
	if moves != 1 {
		t.Errorf("expected 1 move, got %d", moves)
	}
	if deletes != 1 {
		t.Errorf("expected 1 delete, got %d", deletes)
	}
}

func TestProcessPathsCachedDecisionCoercedForDuplicate(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	fc := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "move",
		Reason:   "should not run",
	}}
	classifier = fc

	src1 := filepath.Join(tmp, "Desktop", "a.txt")
	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	os.MkdirAll(filepath.Dir(src1), 0755)
	os.WriteFile(src1, []byte("cached"), 0644)
	os.WriteFile(src2, []byte("cached"), 0644)

	hash, err := actions.ComputeHash(src1)
	if err != nil {
		t.Fatalf("compute hash: %v", err)
	}
	_ = db.RecordDecision(hash, llm.Decision{
		Category: "Images",
		Action:   "move",
		Reason:   "from cache",
	})

	if _, err := processPaths(context.Background(), inputs(src1, src2), "run-test", io.Discard, ""); err != nil {
		t.Fatal(err)
	}

	if len(fc.Calls()) != 0 {
		t.Errorf("expected classifier to be skipped, got %d calls", len(fc.Calls()))
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
		t.Errorf("expected 1 move, got %d", moves)
	}
	if reviews != 1 {
		t.Errorf("expected 1 review, got %d", reviews)
	}
}
func TestApplyRenameFlagsOverridesConfig(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Rename = false
	cfg.RenameLevel = 2

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringVar(&renameFlag, "rename", "", "")
	cmd.Flags().Lookup("rename").NoOptDefVal = "default"
	renameFlag = ""

	if err := cmd.Flags().Set("rename", "default"); err != nil {
		t.Fatalf("set rename flag: %v", err)
	}

	applyRenameFlags(cmd)

	if !cfg.Rename {
		t.Errorf("cfg.Rename = %v, want true", cfg.Rename)
	}
	if cfg.RenameLevel != 2 {
		t.Errorf("cfg.RenameLevel = %d, want 2", cfg.RenameLevel)
	}
}

func TestApplyRenameFlagsLeavesDefaultsWhenUnset(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Rename = true
	cfg.RenameLevel = 3

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringVar(&renameFlag, "rename", "", "")
	cmd.Flags().Lookup("rename").NoOptDefVal = "default"
	renameFlag = ""

	applyRenameFlags(cmd)

	if !cfg.Rename {
		t.Errorf("cfg.Rename = %v, want true (config value preserved)", cfg.Rename)
	}
	if cfg.RenameLevel != 3 {
		t.Errorf("cfg.RenameLevel = %d, want 3 (config value preserved)", cfg.RenameLevel)
	}
}

func TestApplyRenameFlagsNumericLevel(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Rename = false
	cfg.RenameLevel = 2

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringVar(&renameFlag, "rename", "", "")
	cmd.Flags().Lookup("rename").NoOptDefVal = "default"
	renameFlag = ""

	if err := cmd.Flags().Set("rename", "5"); err != nil {
		t.Fatalf("set rename flag: %v", err)
	}

	applyRenameFlags(cmd)

	if !cfg.Rename {
		t.Errorf("cfg.Rename = %v, want true", cfg.Rename)
	}
	if cfg.RenameLevel != 5 {
		t.Errorf("cfg.RenameLevel = %d, want 5", cfg.RenameLevel)
	}
}

func TestApplyRenameFlagsDefaultLevel(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Rename = false
	cfg.RenameLevel = 1

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringVar(&renameFlag, "rename", "", "")
	cmd.Flags().Lookup("rename").NoOptDefVal = "default"
	renameFlag = ""

	if err := cmd.Flags().Set("rename", "default"); err != nil {
		t.Fatalf("set rename flag: %v", err)
	}

	applyRenameFlags(cmd)

	if !cfg.Rename {
		t.Errorf("cfg.Rename = %v, want true", cfg.Rename)
	}
	if cfg.RenameLevel != 2 {
		t.Errorf("cfg.RenameLevel = %d, want 2", cfg.RenameLevel)
	}
}

func TestProcessCmdHasRenameFlags(t *testing.T) {
	if f := processCmd.Flags().Lookup("rename"); f == nil {
		t.Error("process command missing --rename flag")
	}
}

func TestScanCmdHasRenameFlags(t *testing.T) {
	if f := scanCmd.Flags().Lookup("rename"); f == nil {
		t.Error("scan command missing --rename flag")
	}
}

func TestApplyMoveFlagsOverridesConfig(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = false

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().BoolVar(&processMove, "move", false, "")
	processMove = false

	if err := cmd.Flags().Set("move", "true"); err != nil {
		t.Fatalf("set move flag: %v", err)
	}

	applyMoveFlags(cmd)

	if !cfg.MoveFiles {
		t.Errorf("cfg.MoveFiles = %v, want true", cfg.MoveFiles)
	}
}

func TestApplyMoveFlagsLeavesDefaultsWhenUnset(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true

	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().BoolVar(&processMove, "move", false, "")
	processMove = false

	applyMoveFlags(cmd)

	if !cfg.MoveFiles {
		t.Errorf("cfg.MoveFiles = %v, want true (config value preserved)", cfg.MoveFiles)
	}
}

func TestProcessCmdHasMoveFlag(t *testing.T) {
	if f := processCmd.Flags().Lookup("move"); f == nil {
		t.Error("process command missing --move flag")
	}
}

func TestScanCmdHasMoveFlag(t *testing.T) {
	if f := scanCmd.Flags().Lookup("move"); f == nil {
		t.Error("scan command missing --move flag")
	}
}

// blockingHash blocks hash calls until release() is invoked so tests can
// observe concurrent hashing.
type blockingHash struct {
	mu        sync.Mutex
	cond      *sync.Cond
	active    int
	maxActive int
	proceed   bool
}

func newBlockingHash() *blockingHash {
	b := &blockingHash{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *blockingHash) Hash(path string) (string, error) {
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
	return fmt.Sprintf("hash-%s", filepath.Base(path)), nil
}

func (b *blockingHash) release() {
	b.mu.Lock()
	b.proceed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

func TestProcessPathsHashesConcurrently(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.ProcessWorkers = 2
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "review",
		Reason:   "test",
	}}
	t.Cleanup(func() { classifier = llm.NewClient(nil) })

	bh := newBlockingHash()
	computeHash = bh.Hash
	t.Cleanup(func() { computeHash = actions.ComputeHash })

	paths := make([]string, 3)
	for i := range paths {
		p := filepath.Join(tmp, "Desktop", fmt.Sprintf("concurrent%d.txt", i))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(fmt.Sprintf("content%d", i)), 0644)
		paths[i] = p
	}

	done := make(chan []processResult)
	go func() {
		res, err := processPaths(context.Background(), inputs(paths...), "run-test", io.Discard, "")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()

	// Wait for at least two concurrent hash calls to confirm workers run
	// in parallel rather than sequentially.
	for {
		bh.mu.Lock()
		ma := bh.maxActive
		bh.mu.Unlock()
		if ma >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	bh.release()
	results := <-done

	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}

	bh.mu.Lock()
	peak := bh.maxActive
	bh.mu.Unlock()
	if peak < 2 {
		t.Errorf("expected concurrent hashing, got max active %d", peak)
	}

	for i, want := range paths {
		if results[i].Path != want {
			t.Errorf("results[%d].Path = %q, want %q", i, results[i].Path, want)
		}
	}
}
func TestProcessCmdHasDryRunFlag(t *testing.T) {
	if f := processCmd.Flags().Lookup("dry-run"); f == nil {
		t.Error("process command missing --dry-run flag")
	}
}

func TestScanCmdHasDryRunFlag(t *testing.T) {
	if f := scanCmd.Flags().Lookup("dry-run"); f == nil {
		t.Error("scan command missing --dry-run flag")
	}
}

func TestProcessCommandDryRunPreventsMove(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	cfg.Rename = false
	fakeDB := state.NewFake()
	db = fakeDB
	fs := actions.NewRecordingFS()
	processFS = fs
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	processDryRun = true
	t.Cleanup(func() { processDryRun = false })

	out := captureStdout(t, func() {
		if err := processCmd.RunE(nil, []string{src}); err != nil {
			t.Fatalf("process failed: %v", err)
		}
	})
	if !strings.Contains(out, "kept name") {
		t.Errorf("expected kept name in output, got:\n%s", out)
	}
	if len(fs.Moved) != 0 {
		t.Errorf("dry-run should not move files, got %d moves", len(fs.Moved))
	}
	if len(fakeDB.Records()) != 0 {
		t.Errorf("dry-run should not record history, got %d records", len(fakeDB.Records()))
	}
}
func TestProcessCommandShowsRename(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	cfg.Rename = true
	cfg.RenameLevel = 0
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category:    "Documents",
		Tags:        []string{"txt"},
		Action:      "move",
		Reason:      "text",
		NewName:     "renamed-note.txt",
		NameQuality: 2,
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	out := captureStdout(t, func() {
		if err := processCmd.RunE(nil, []string{src}); err != nil {
			t.Fatalf("process failed: %v", err)
		}
	})
	if !strings.Contains(out, "note.txt -> renamed-note.txt") {
		t.Errorf("expected rename in output, got:\n%s", out)
	}
}

func TestProcessCommandDryRunShowsRename(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	cfg.Rename = true
	cfg.RenameLevel = 0
	db = state.NewFake()
	fs := actions.NewRecordingFS()
	processFS = fs
	classifier = &fakeClassifier{decision: llm.Decision{
		Category:    "Documents",
		Tags:        []string{"txt"},
		Action:      "move",
		Reason:      "text",
		NewName:     "renamed-note.txt",
		NameQuality: 2,
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	processDryRun = true
	t.Cleanup(func() { processDryRun = false })

	out := captureStdout(t, func() {
		if err := processCmd.RunE(nil, []string{src}); err != nil {
			t.Fatalf("process failed: %v", err)
		}
	})
	if !strings.Contains(out, "note.txt -> renamed-note.txt") {
		t.Errorf("expected rename preview in output, got:\n%s", out)
	}
	if len(fs.Moved) != 0 {
		t.Errorf("dry-run should not move files, got %d moves", len(fs.Moved))
	}
}

func TestProcessPathsAcceptsDirectoryWhenFlagSet(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(dirPath), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != "directory" {
		t.Errorf("kind = %q, want directory", results[0].Kind)
	}
	if results[0].Action != "review" {
		t.Errorf("action = %q, want review", results[0].Action)
	}
	if !results[0].OK {
		t.Errorf("expected directory result to be OK")
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records for directory candidates, got %d", len(records))
	}
}

func TestProcessPathsSkipsDirectoryWhenFlagNotSet(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 0

	results, err := processPaths(context.Background(), inputs(dirPath), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != "" {
		t.Errorf("kind = %q, want empty", results[0].Kind)
	}
	if results[0].Action != "skip" {
		t.Errorf("action = %q, want skip", results[0].Action)
	}
	if results[0].Error != "not a regular file" {
		t.Errorf("error = %q, want not a regular file", results[0].Error)
	}
}

func TestProcessPathsSkipsHiddenDirectory(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", ".hidden-dir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(dirPath), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "skip" {
		t.Errorf("action = %q, want skip", results[0].Action)
	}
	if results[0].Error != "hidden directory" {
		t.Errorf("error = %q, want hidden directory", results[0].Error)
	}
}

func TestProcessPathsSkipsDirectoryOutsideAllowedDirs(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Outside", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(dirPath), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "skip" {
		t.Errorf("action = %q, want skip", results[0].Action)
	}
	if results[0].Error != "outside allowed dirs" {
		t.Errorf("error = %q, want outside allowed dirs", results[0].Error)
	}
}

func TestProcessCommandDepthFlagExists(t *testing.T) {
	if processCmd.Flags().Lookup("depth") == nil {
		t.Fatal("expected --depth flag on process command")
	}
}

func TestScanCommandDepthFlagExists(t *testing.T) {
	if scanCmd.Flags().Lookup("depth") == nil {
		t.Fatal("expected --depth flag on scan command")
	}
}
func TestProcessPathsPassesDirectoryContext(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fc := &pathContextClassifier{}
	classifier = fc

	dir := filepath.Join(tmp, "Downloads", "project")
	os.MkdirAll(dir, 0755)
	src := filepath.Join(dir, "note.txt")
	os.WriteFile(src, []byte("hello"), 0644)

	if _, err := processPaths(context.Background(), []processInput{{path: src, dirCtx: &directory.Context{Ancestor: dir, Depth: 1, Marker: ".git"}}}, "run-test", io.Discard, ""); err != nil {
		t.Fatal(err)
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.calls) != 1 {
		t.Fatalf("expected 1 classification call, got %d", len(fc.calls))
	}
	ctx := fc.contexts[0]
	if ctx == nil || ctx.Ancestor != dir {
		t.Errorf("context ancestor = %q, want %q", ctx.Ancestor, dir)
	}
	if ctx == nil || ctx.Depth != 1 {
		t.Errorf("context depth = %d, want 1", ctx.Depth)
	}
	if ctx == nil || ctx.Marker != ".git" {
		t.Errorf("context marker = %q, want .git", ctx.Marker)
	}
}

// pathContextClassifier records the directory context passed to Classify.
type pathContextClassifier struct {
	mu       sync.Mutex
	calls    []string
	contexts []*directory.Context
}

func (p *pathContextClassifier) Classify(ctx context.Context, src string, fileHash string, cfg *config.Config, dirCtx *directory.Context) (llm.Decision, llm.Metrics, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, src)
	p.contexts = append(p.contexts, dirCtx)
	return llm.Decision{Category: "Documents", Action: "move"}, llm.Metrics{}, nil
}

func (p *pathContextClassifier) Validate(cfg *config.Config) error { return nil }

func TestProcessPathsClassifiesDirectory(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fc := &fakeClassifier{
		decision:    llm.Decision{Category: "Documents", Action: "move"},
		dirDecision: llm.DirectoryDecision{Recommendation: "archive", Reason: "project folder", Category: "Projects", Tags: []string{"project"}},
	}
	classifier = fc

	dir := filepath.Join(tmp, "Downloads", "project")
	os.MkdirAll(dir, 0755)
	processDepth = 1
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(dir), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Kind != "directory" {
		t.Errorf("kind = %q, want directory", r.Kind)
	}
	if r.Recommendation != "archive" {
		t.Errorf("recommendation = %q, want archive", r.Recommendation)
	}
	if r.Action != "archive" {
		t.Errorf("action = %q, want archive", r.Action)
	}
	if r.Category != "Projects" {
		t.Errorf("category = %q, want Projects", r.Category)
	}
	if len(r.Tags) != 1 || r.Tags[0] != "project" {
		t.Errorf("tags = %v, want [project]", r.Tags)
	}
	if r.Reason != "project folder" {
		t.Errorf("reason = %q, want project folder", r.Reason)
	}
	if len(fc.dirCalls) != 1 || fc.dirCalls[0] != dir {
		t.Errorf("dirCalls = %v, want [%s]", fc.dirCalls, dir)
	}
}

func TestFormatProcessTableDirectoryAndInPlaceRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dest := filepath.Join(home, "Projects", "project")
	src := filepath.Join(home, "Downloads", "project")
	results := []processResult{
		{Path: src, Kind: "directory", Recommendation: "archive", Category: "Projects", Tags: []string{"project"}, Action: "move", Result: dest, OriginalName: "project", OK: true, Reason: "keep project"},
		{Path: filepath.Join(home, "Downloads", "img.jpg"), Category: "Images", Tags: []string{"jpg"}, Action: "move", Result: filepath.Join(home, "Downloads", "vacation.jpg"), OriginalName: "img.jpg", NewName: "vacation.jpg", OK: true},
	}
	out := formatProcessTable(results)
	for _, want := range []string{"Item", "dir:", "move", "Projects", "project", "in-place:", "vacation.jpg"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatHumanDirectoryAndInPlaceRename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dest := filepath.Join(home, "Projects", "project")
	src := filepath.Join(home, "Downloads", "project")
	results := []processResult{
		{Path: src, Kind: "directory", Recommendation: "archive", Category: "Projects", Tags: []string{"project"}, Action: "move", Result: dest, OriginalName: "project", OK: true, Reason: "keep project"},
		{Path: filepath.Join(home, "Downloads", "img.jpg"), Category: "Images", Tags: []string{"jpg"}, Action: "move", Result: filepath.Join(home, "Downloads", "vacation.jpg"), OriginalName: "img.jpg", NewName: "vacation.jpg", OK: true},
	}
	out := formatHuman(results)
	for _, want := range []string{"moved directory project", "→", "~/Projects/project", "Action:   move", "Reason:   keep project", "in-place:", "vacation.jpg"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatProcessResultsJSONDirectory(t *testing.T) {
	dest := "/archive/project"
	results := []processResult{
		{Path: "/Downloads/project", Kind: "directory", Recommendation: "archive", Category: "Projects", Tags: []string{"project"}, Action: "move", Result: dest, OriginalName: "project", OK: true, Reason: "keep project"},
	}
	out, err := formatProcessResults(results, "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"path": "/Downloads/project"`, `"kind": "directory"`, `"action": "move"`, `"result": "/archive/project"`, `"category": "Projects"`, `"ok": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}
func TestProcessCmdHasMoveProjectsFlag(t *testing.T) {
	if processCmd.Flags().Lookup("move-projects") == nil {
		t.Fatal("expected --move-projects flag on process command")
	}
}

func TestProcessPathsMoveProjectsWithMarkers(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	cfg.ProjectMarkers = []string{".git"}
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dir := filepath.Join(tmp, "Desktop", "project")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	moveProjects = true
	t.Cleanup(func() { processDepth = 0; moveProjects = false })

	results, err := processPaths(context.Background(), inputs(dir), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Kind != "directory" {
		t.Errorf("kind = %q, want directory", r.Kind)
	}
	if r.Action != "move" {
		t.Errorf("action = %q, want move", r.Action)
	}
	if r.Result == "" || r.Result == "-" {
		t.Errorf("result = %q, want destination path", r.Result)
	}
	if !r.OK {
		t.Errorf("expected directory move to be OK")
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Errorf("expected 1 history record, got %d", len(records))
	}
	fs := processFS.(*actions.RecordingFS)
	if len(fs.Moved) != 1 {
		t.Errorf("expected 1 move, got %d", len(fs.Moved))
	}
}

func TestProcessPathsMoveProjectsClassified(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fc := &fakeClassifier{
		decision:    llm.Decision{Category: "Documents", Action: "move"},
		dirDecision: llm.DirectoryDecision{Recommendation: "archive", Action: "move", Reason: "project folder", Category: "Projects", Tags: []string{"project"}},
	}
	classifier = fc

	dir := filepath.Join(tmp, "Desktop", "project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	moveProjects = true
	t.Cleanup(func() { processDepth = 0; moveProjects = false; classifier = llm.NewClient(nil) })

	results, err := processPaths(context.Background(), inputs(dir), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Kind != "directory" {
		t.Errorf("kind = %q, want directory", r.Kind)
	}
	if r.Action != "move" {
		t.Errorf("action = %q, want move", r.Action)
	}
	if r.Recommendation != "archive" {
		t.Errorf("recommendation = %q, want archive", r.Recommendation)
	}
	if r.Category != "Projects" {
		t.Errorf("category = %q, want Projects", r.Category)
	}
	if r.Result == "" || r.Result == "-" {
		t.Errorf("result = %q, want destination path", r.Result)
	}
	if !r.OK {
		t.Errorf("expected directory move to be OK")
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Errorf("expected 1 history record, got %d", len(records))
	}
}
func TestProcessPathsMoveProjectsWithoutFlagKeepsReadOnly(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.ProjectMarkers = []string{".git"}
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dir := filepath.Join(tmp, "Desktop", "project")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	moveProjects = false
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(dir), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "archive" {
		t.Errorf("action = %q, want archive recommendation", results[0].Action)
	}
	if results[0].Result != "-" {
		t.Errorf("result = %q, want '-' for read-only", results[0].Result)
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records without --move-projects, got %d", len(records))
	}
}

func TestProcessPathsMoveProjectsNonProjectReadOnly(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fc := &fakeClassifier{dirDecision: llm.DirectoryDecision{Recommendation: "keep", Action: "", Reason: "keep it"}}
	classifier = fc

	dir := filepath.Join(tmp, "Desktop", "other")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	moveProjects = true
	t.Cleanup(func() { processDepth = 0; moveProjects = false; classifier = llm.NewClient(nil) })

	results, err := processPaths(context.Background(), inputs(dir), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "keep" {
		t.Errorf("action = %q, want keep", results[0].Action)
	}
	if results[0].Result != "-" {
		t.Errorf("result = %q, want '-' for read-only", results[0].Result)
	}
	if !results[0].OK {
		t.Errorf("expected read-only directory result to be OK")
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records for non-project directory, got %d", len(records))
	}
}
