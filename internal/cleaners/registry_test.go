package cleaners

import "testing"

func TestRegistry(t *testing.T) {
	reg := Registry()
	if len(reg) != 7 {
		t.Fatalf("Registry length = %d, want 7", len(reg))
	}
	want := []string{"docker", "npm", "cargo", "pip", "brew", "xcode", "review"}
	for i, c := range reg {
		if c.Name != want[i] {
			t.Errorf("Registry[%d].Name = %q, want %q", i, c.Name, want[i])
		}
		if c.CanRun == nil || c.Run == nil {
			t.Errorf("Registry[%d] has nil CanRun or Run", i)
		}
	}
}
