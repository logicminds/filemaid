package assets

import (
	"strings"
	"testing"
)

func TestReadConfig(t *testing.T) {
	b, err := ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("ReadConfig returned empty config")
	}
	if !strings.Contains(string(b), "{") {
		t.Fatal("ReadConfig did not return JSON object")
	}
}

func TestListModelfiles(t *testing.T) {
	names, err := ListModelfiles()
	if err != nil {
		t.Fatalf("ListModelfiles: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("ListModelfiles returned no Modelfiles")
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "Modelfile.") {
			t.Fatalf("unexpected modelfile name: %s", name)
		}
	}
}

func TestReadModelfile(t *testing.T) {
	names, err := ListModelfiles()
	if err != nil {
		t.Fatalf("ListModelfiles: %v", err)
	}
	for _, name := range names {
		b, err := ReadModelfile(name)
		if err != nil {
			t.Fatalf("ReadModelfile(%q): %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("ReadModelfile(%q) returned empty content", name)
		}
	}
}

func TestReadModelfileMissing(t *testing.T) {
	_, err := ReadModelfile("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing modelfile")
	}
}
