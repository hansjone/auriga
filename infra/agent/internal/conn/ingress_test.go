package conn

import (
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
)

type helloCapture struct {
	grpc.ClientStream
	message *nodev1.SessionRequest
}

func (s *helloCapture) Send(r *nodev1.SessionRequest) error    { s.message = r; return nil }
func (s *helloCapture) Recv() (*nodev1.SessionResponse, error) { return nil, nil }

func TestHelloReportsIngressEndpoint(t *testing.T) {
	for _, endpoint := range []string{"https://node.example.com:7443", "unix:///var/lib/auriga/ingress.sock", "npipe:////./pipe/auriga-agent-ingress", ""} {
		s := New(Config{IngressEndpoint: endpoint})
		stream := &helloCapture{}
		if err := s.sendHello(stream, &identity.Material{NodeID: "node"}); err != nil {
			t.Fatal(err)
		}
		if stream.message.GetHello().GetIngressEndpoint() != endpoint {
			t.Fatalf("Hello lost %q", endpoint)
		}
	}
}
