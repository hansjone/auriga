package conn

import (
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type captureHelloStream struct {
	nodev1.NodeControlService_SessionClient
	sent []*nodev1.SessionRequest
}

func (c *captureHelloStream) Send(req *nodev1.SessionRequest) error {
	c.sent = append(c.sent, req)
	return nil
}

func TestSendHelloLabels(t *testing.T) {
	mat := &identity.Material{NodeID: "n_labels01"}

	t.Run("dedicated_labels_reported", func(t *testing.T) {
		input := map[string]string{"template": "real-dsh", "zone": "nas"}
		s := New(Config{Labels: input})
		// Mutating caller map after New must not affect Session.
		input["template"] = "mutated"

		stream := &captureHelloStream{}
		if err := s.sendHello(stream, mat); err != nil {
			t.Fatal(err)
		}
		if len(stream.sent) != 1 {
			t.Fatalf("sent=%d", len(stream.sent))
		}
		got := stream.sent[0].GetHello().GetLabels()
		if len(got) != 2 || got["template"] != "real-dsh" || got["zone"] != "nas" {
			t.Fatalf("hello labels = %#v", got)
		}
	})

	t.Run("reserved_labels_cannot_be_spoofed_without_shared", func(t *testing.T) {
		s := New(Config{Labels: map[string]string{
			"template":                  "real-dsh",
			"auriga.shared-dsh-runtime": "spoofed",
			"auriga.shared-dsh-process": "spoofed",
			"AURIGA.custom":             "spoofed",
		}})
		stream := &captureHelloStream{}
		if err := s.sendHello(stream, mat); err != nil {
			t.Fatal(err)
		}
		got := stream.sent[0].GetHello().GetLabels()
		if len(got) != 1 || got["template"] != "real-dsh" {
			t.Fatalf("hello labels = %#v", got)
		}
	})

	t.Run("shared_capability_labels_merged_and_protected", func(t *testing.T) {
		s := New(Config{
			Labels: map[string]string{
				"template":                  "real-dsh",
				"auriga.shared-dsh-runtime": "spoofed",
				"auriga.shared-dsh-process": "spoofed",
			},
			SharedPools:   &agentruntime.SharedPools{},
			SharedRuntime: &agentruntime.PersistentPools{},
		})
		stream := &captureHelloStream{}
		if err := s.sendHello(stream, mat); err != nil {
			t.Fatal(err)
		}
		got := stream.sent[0].GetHello().GetLabels()
		if len(got) != 3 ||
			got["template"] != "real-dsh" ||
			got["auriga.shared-dsh-runtime"] != "prepare-v1" ||
			got["auriga.shared-dsh-process"] != "persistent-v1" {
			t.Fatalf("hello labels = %#v", got)
		}
	})
}
