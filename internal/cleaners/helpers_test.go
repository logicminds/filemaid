package cleaners

import (
	"os"
	"os/exec"
	"testing"
)

func setLookPath(found map[string]bool) func() {
	old := lookPath
	lookPath = func(name string) (string, error) {
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	return func() { lookPath = old }
}

func setRunner(fn func(name string, arg ...string) ([]byte, error)) func() {
	old := runner
	runner = fnRunner{fn: fn}
	return func() { runner = old }
}

type fnRunner struct {
	fn func(name string, arg ...string) ([]byte, error)
}

func (f fnRunner) Run(name string, arg ...string) ([]byte, error) {
	return f.fn(name, arg...)
}

func setClock(now int64) func() {
	old := sysClock
	sysClock = fixedClock(now)
	return func() { sysClock = old }
}

type fixedClock int64

func (f fixedClock) Now() int64 { return int64(f) }

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
