package relaylimits

import (
	"encoding/json"
	"testing"
)

func TestDropStoreOptOut(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"model":"m"}`:                 true,
		`{"model":"m","store":false}`:   true,
		`{"model":"m","store": false }`: true,
		`{"model":"m","store":null}`:    true,
		`{"model":"m","store":true}`:    false,
		`{"model":"m","store":"false"}`: false,
		`{"model":"m","store":0}`:       false,
	} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		if got := DropStoreOptOut(fields); got != want {
			t.Fatalf("%s: got %v want %v", raw, got, want)
		}
		if _, left := fields["store"]; want && left {
			t.Fatalf("%s: opt-out was not removed", raw)
		}
		if fields["model"] == nil {
			t.Fatalf("%s: other fields must stay", raw)
		}
	}
}
