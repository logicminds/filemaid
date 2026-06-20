package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func runHistoryCmd(t *testing.T, args []string) string {
	t.Helper()
	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	historyCmd.SetArgs(args)
	if err := historyCmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if err := historyCmd.RunE(historyCmd, nil); err != nil {
		t.Fatalf("history failed: %v", err)
	}
	_ = w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)
	return buf.String()
}

func TestHistoryCommandReturnsRows(t *testing.T) {
	resetGlobals(t)

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

	if err := processCmd.RunE(processCmd, []string{src}); err != nil {
		t.Fatalf("process failed: %v", err)
	}

	out := runHistoryCmd(t, nil)
	if !strings.Contains(out, "note.txt") {
		t.Errorf("expected history to include note.txt, got:\n%s", out)
	}
}

func TestHistoryCommandLastFilter(t *testing.T) {
	resetGlobals(t)

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

	var files []string
	for i := 0; i < 2; i++ {
		src := filepath.Join(tmp, "Desktop", fmt.Sprintf("note%d.txt", i))
		os.MkdirAll(filepath.Dir(src), 0755)
		os.WriteFile(src, []byte("hello"), 0644)
		files = append(files, src)
	}

	if err := processCmd.RunE(processCmd, files); err != nil {
		t.Fatalf("process failed: %v", err)
	}

	out := runHistoryCmd(t, []string{"--last"})
	if !strings.Contains(out, "2 files processed in run") {
		t.Errorf("expected count summary, got:\n%s", out)
	}
}

func TestHistoryCommandJSON(t *testing.T) {
	resetGlobals(t)

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

	if err := processCmd.RunE(processCmd, []string{src}); err != nil {
		t.Fatalf("process failed: %v", err)
	}

	out := runHistoryCmd(t, []string{"--json"})
	for _, want := range []string{`"run_id"`, `"original_path"`, `"action": "move"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

func TestQueryHistoryRespectsRunFilter(t *testing.T) {
	db = state.NewFake()
	fake := db.(*state.FakeRepo)
	fake.Record(state.RecordInput{OriginalPath: "/a", FinalPath: "/b", SHA256: "h1", Category: "C", Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})
	fake.Record(state.RecordInput{OriginalPath: "/c", FinalPath: "/d", SHA256: "h2", Category: "C", Action: "move", Reason: "r", RunID: "run-2", Metrics: llm.Metrics{}})

	records, runID, err := queryHistory("run-2", false, 100)
	if err != nil {
		t.Fatal(err)
	}
	if runID != "run-2" {
		t.Errorf("runID = %q, want run-2", runID)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RunID.String != "run-2" {
		t.Errorf("RunID = %q, want run-2", records[0].RunID.String)
	}
}
