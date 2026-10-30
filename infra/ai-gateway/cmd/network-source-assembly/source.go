package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Only real generated Read/Fence/Get probes. There is intentionally no direct
// Register fallback in this test control port. Cancellation owns one exact call.
type sourceCalls struct {
	ctx       context.Context
	conn      grpc.ClientConnInterface
	tenant    string
	mu        sync.Mutex
	cancelers map[string]context.CancelFunc
	results   map[string]map[string]any
	work      sync.WaitGroup
}

func newSourceCalls(ctx context.Context, conn grpc.ClientConnInterface, tenant string) *sourceCalls {
	return &sourceCalls{ctx: ctx, conn: conn, tenant: tenant, cancelers: map[string]context.CancelFunc{}, results: map[string]map[string]any{}}
}
func (s *sourceCalls) start(cmd command) error {
	if cmd.Method != "ReadAgentSourceReceipt" && cmd.Method != "FenceAgentSourceReceipt" && cmd.Method != "GetAgentSource" {
		return errors.New("source probe must not issue")
	}
	var original iam.RegisterAgentSourceRequest
	if err := protojson.Unmarshal(cmd.Original, &original); err != nil || original.GetConnection().GetTenantId() != s.tenant || original.GetOperationId() == "" {
		return errors.New("invalid exact source probe request")
	}
	timeout := cmd.TimeoutMillis
	if timeout == 0 {
		timeout = 5000
	}
	if timeout < 100 || timeout > 5000 {
		return errors.New("source probe deadline out of bounds")
	}
	s.mu.Lock()
	if len(s.results) >= 64 || s.results[cmd.ID] != nil {
		s.mu.Unlock()
		return errors.New("source probe capacity or duplicate id")
	}
	ctx, cancel := context.WithTimeout(team.WithActor(s.ctx, &collab.Actor{TenantId: s.tenant}), time.Duration(timeout)*time.Millisecond)
	s.cancelers[cmd.ID] = cancel
	s.results[cmd.ID] = map[string]any{"done": false}
	s.mu.Unlock()
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		defer cancel()
		result := map[string]any{"done": true, "method": cmd.Method, "hasSource": false}
		var response proto.Message
		var err error
		if cmd.RequireLive {
			var live *iam.ResolveNetworkTargetResponse
			live, err = iam.NewNetworkAuthorityServiceClient(s.conn).ResolveNetworkTarget(ctx, &iam.ResolveNetworkTargetRequest{Connection: original.Connection, Local: "0"})
			result["live"] = err == nil && live.GetAllowed() && proto.Equal(live.GetConnection(), original.Connection)
			if err == nil && result["live"] != true {
				err = errors.New("live connection positive control failed")
			}
		}
		if err == nil {
			client := iam.NewNetworkSourceAuthorityServiceClient(s.conn)
			switch cmd.Method {
			case "ReadAgentSourceReceipt":
				var p *iam.RegisterAgentSourceResponse
				p, err = client.ReadAgentSourceReceipt(ctx, &iam.ReadAgentSourceReceiptRequest{OriginalRequest: &original})
				response = p
				if err == nil {
					result["hasSource"] = p.GetSource() != nil
				}
			case "FenceAgentSourceReceipt":
				var p *iam.RegisterAgentSourceResponse
				p, err = client.FenceAgentSourceReceipt(ctx, &iam.FenceAgentSourceReceiptRequest{OriginalRequest: &original})
				response = p
				if err == nil {
					result["hasSource"] = p.GetSource() != nil
				}
			case "GetAgentSource":
				var p *iam.GetAgentSourceResponse
				p, err = client.GetAgentSource(ctx, &iam.GetAgentSourceRequest{Connection: original.Connection, Registration: original.Registration, SourceBinding: cmd.Binding})
				response = p
				if err == nil {
					result["hasSource"] = p.GetSource() != nil
				}
			}
		}
		result["code"] = status.Code(err).String()
		if err == nil {
			raw, e := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(response)
			if e != nil {
				result["code"] = "Internal"
			} else {
				result["response"] = json.RawMessage(raw)
			}
		} else {
			reason := status.Convert(err).Message()
			if reason == "common.forbidden" || reason == "common.unavailable" || reason == "common.validation_failed" {
				result["reason"] = reason
			}
		}
		s.mu.Lock()
		s.results[cmd.ID] = result
		delete(s.cancelers, cmd.ID)
		s.mu.Unlock()
	}()
	return nil
}
func (s *sourceCalls) result(id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]any{}
	for key, value := range s.results[id] {
		result[key] = value
	}
	return result
}
func (s *sourceCalls) cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancel := s.cancelers[id]
	if cancel == nil {
		return false
	}
	cancel()
	return true
}
func (s *sourceCalls) close() {
	s.mu.Lock()
	for _, cancel := range s.cancelers {
		cancel()
	}
	s.mu.Unlock()
	s.work.Wait()
}
