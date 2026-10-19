package state_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta := state.NodeMeta{
		NodeID:          "n1",
		ControlEndpoint: "https://platform.example.com:7443",
		ExpiresAt:       time.Now().UTC().Truncate(time.Second),
		CAFingerprint:   "abc",
	}
	if err := store.PutNodeMeta(meta); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNodeMeta()
	if err != nil || got.NodeID != "n1" || got.CAFingerprint != "abc" {
		t.Fatalf("%+v %v", got, err)
	}
	spec := &nodev1.InstanceSpec{InstanceId: "i1", Generation: 3}
	if err := store.PutDesired(spec); err != nil {
		t.Fatal(err)
	}
	store.Close()

	store2, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	s2, err := store2.GetDesired("i1")
	if err != nil || s2 == nil || s2.Generation != 3 {
		t.Fatalf("%v %v", s2, err)
	}
}
