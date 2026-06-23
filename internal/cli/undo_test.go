package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func runUndoCmd(t *testing.T, args []string) string {
	t.Helper()
	var buf bytes.Buffer
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	os.Stderr = wErr
	undoCmd.SetArgs(args)
	if err := undoCmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := undoCmd.RunE(undoCmd, undoCmd.Flags().Args()); err != nil {
		t.Fatalf("undo failed: %v", err)
	}
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	buf.ReadFrom(rOut)
	buf.ReadFrom(rErr)
	return buf.String()
}

func TestUndoCommandLastRestoresRun(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	src2 := filepath.Join(tmp, "Desktop", "other.txt")
	final := filepath.Join(tmp, "Documents", "note.txt")
	final2 := filepath.Join(tmp, "Documents", "other.txt")
	for _, p := range []string{final, final2} {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("hello"), 0644)
	}

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})
	fake.Record(state.RecordInput{OriginalPath: src2, FinalPath: final2, SHA256: "h2", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last"})
	if !strings.Contains(out, "restored") {
		t.Errorf("expected restore output, got:\n%s", out)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("expected source to be restored: %v", err)
	}
	recs := fake.Records()
	var undoCount int
	for _, r := range recs {
		if r.Action == "undo" {
			undoCount++
		}
	}
	if undoCount != 2 {
		t.Errorf("expected 2 undo rows, got %d", undoCount)
	}
}

func TestUndoCommandRunFilter(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "run2.txt")
	final := filepath.Join(tmp, "Documents", "run2.txt")
	run1Final := filepath.Join(tmp, "Documents", "run1.txt")
	for _, p := range []string{final, run1Final} {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("hello"), 0644)
	}

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: filepath.Join(tmp, "Desktop", "run1.txt"), FinalPath: filepath.Join(tmp, "Documents", "run1.txt"), SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h2", Category: "Documents", Action: "move", Reason: "r", RunID: "run-2", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--run", "run-2"})
	if !strings.Contains(out, "restored") {
		t.Errorf("expected restore output, got:\n%s", out)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("expected run-2 source to be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "run1.txt")); err != nil {
		t.Errorf("expected run-1 item to remain: %v", err)
	}
}

func TestUndoCommandPathFilter(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	src := filepath.Join(tmp, "Desktop", "path.txt")
	final := filepath.Join(tmp, "Documents", "path.txt")
	otherFinal := filepath.Join(tmp, "Documents", "other.txt")
	for _, p := range []string{final, otherFinal} {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("hello"), 0644)
	}

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: filepath.Join(tmp, "Desktop", "other.txt"), FinalPath: otherFinal, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h2", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{final})
	if !strings.Contains(out, "restored") {
		t.Errorf("expected restore output, got:\n%s", out)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("expected source to be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "other.txt")); err != nil {
		t.Errorf("expected other item to remain: %v", err)
	}
}

func TestUndoCommandRenameReversed(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	original := filepath.Join(tmp, "Desktop", "oldname.txt")
	final := filepath.Join(tmp, "Documents", "newname.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("hello"), 0644)

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: original, FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last"})
	if !strings.Contains(out, original) {
		t.Errorf("expected output to mention original path, got:\n%s", out)
	}
	if _, err := os.Stat(original); err != nil {
		t.Errorf("expected file at original path: %v", err)
	}
}

func TestUndoCommandSkipsUndoRows(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	final := filepath.Join(tmp, "Documents", "note.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("hello"), 0644)

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	// First undo.
	runUndoCmd(t, []string{"--last"})
	// Second undo attempts the original run again, but the file is no longer at
	// the final path so it is skipped rather than re-restored.
	out := runUndoCmd(t, []string{"--last"})
	if strings.Contains(out, "restored") {
		t.Errorf("expected no restores after undo, got:\n%s", out)
	}
	if !strings.Contains(out, "not found at final path") {
		t.Errorf("expected missing final path warning, got:\n%s", out)
	}
}

func TestUndoCommandCollisionSkip(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	final := filepath.Join(tmp, "Documents", "note.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("final"), 0644)
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("existing"), 0644)

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last"})
	if !strings.Contains(out, "destination already exists") {
		t.Errorf("expected collision warning, got:\n%s", out)
	}
	if strings.Contains(out, "restored") {
		t.Errorf("expected no restore without force, got:\n%s", out)
	}
}

func TestUndoCommandCollisionForce(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	final := filepath.Join(tmp, "Documents", "note.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("final"), 0644)
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("existing"), 0644)

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last", "--force"})
	if !strings.Contains(out, "restored") {
		t.Errorf("expected restore with force, got:\n%s", out)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "final" {
		t.Errorf("expected destination overwritten with final content, got %q", string(data))
	}
}

func TestUndoCommandSkipsTrash(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: filepath.Join(tmp, "Desktop", "trash.txt"), FinalPath: "trash", SHA256: "h1", Category: "Unknown", Action: "delete", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})
	fake.Record(state.RecordInput{OriginalPath: filepath.Join(tmp, "Desktop", "note.txt"), FinalPath: filepath.Join(tmp, "Documents", "note.txt"), SHA256: "h2", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	final := filepath.Join(tmp, "Documents", "note.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("hello"), 0644)

	out := runUndoCmd(t, []string{"--last"})
	if !strings.Contains(out, "trashed items cannot be restored") {
		t.Errorf("expected trash skip warning, got:\n%s", out)
	}
	if !strings.Contains(out, "restored") {
		t.Errorf("expected valid move to be restored, got:\n%s", out)
	}
}

func TestUndoCommandRefusesOutsideAllowedDirs(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	final := filepath.Join(tmp, "Documents", "note.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("hello"), 0644)

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: filepath.Join(tmp, "Outside", "note.txt"), FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last"})
	if !strings.Contains(out, "restore destination outside allowed dirs") {
		t.Errorf("expected outside allowed dirs warning, got:\n%s", out)
	}
	if strings.Contains(out, "restored") {
		t.Errorf("expected no restore outside allowed dirs, got:\n%s", out)
	}
}

func TestUndoCommandDryRun(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	final := filepath.Join(tmp, "Documents", "note.txt")
	os.MkdirAll(filepath.Dir(final), 0755)
	os.WriteFile(final, []byte("hello"), 0644)

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: src, FinalPath: final, SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last", "--dry-run"})
	if !strings.Contains(out, "would restore") {
		t.Errorf("expected dry-run preview, got:\n%s", out)
	}
	if _, err := os.Stat(src); err == nil {
		t.Errorf("expected source not to be restored in dry-run")
	}
	recs := fake.Records()
	for _, r := range recs {
		if r.Action == "undo" {
			t.Errorf("expected no undo rows in dry-run")
		}
	}
}

func TestUndoCommandMissingFinalPath(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	undoFS = actions.NewRecordingFS()

	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: filepath.Join(tmp, "Desktop", "note.txt"), FinalPath: filepath.Join(tmp, "Documents", "note.txt"), SHA256: "h1", Category: "Documents", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})

	out := runUndoCmd(t, []string{"--last"})
	if !strings.Contains(out, "not found at final path") {
		t.Errorf("expected missing final path warning, got:\n%s", out)
	}
}
