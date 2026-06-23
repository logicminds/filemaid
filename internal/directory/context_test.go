package directory

import "testing"

func TestContextNone(t *testing.T) {
	if !((*Context)(nil)).None() {
		t.Error("nil Context should report None")
	}
	if !(&Context{}).None() {
		t.Error("empty Context should report None")
	}
	if (&Context{Ancestor: "/tmp"}).None() {
		t.Error("Context with Ancestor should not report None")
	}
}
