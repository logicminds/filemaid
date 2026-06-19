package cleaners

import (
	"encoding/json"
	"testing"
)

func TestCleanupResultJSON(t *testing.T) {
	saved := int64(42)
	r := CleanupResult{
		Name:       "test",
		Status:     "ok",
		Saved:      &saved,
		SavedHuman: "42 B",
		Detail:     "detail",
		Command:    "cmd",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["saved"] != float64(42) {
		t.Fatalf("saved = %v, want 42", got["saved"])
	}

	var nullResult CleanupResult
	data, _ = json.Marshal(nullResult)
	want := `{"name":"","status":"","saved":null,"saved_human":"","detail":"","command":""}`
	if string(data) != want {
		t.Fatalf("unexpected null JSON: %s", data)
	}
}
