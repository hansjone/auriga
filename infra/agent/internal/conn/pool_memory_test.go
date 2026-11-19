package conn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

func poolSample() *nodev1.SharedDshPoolMemorySample {
	now := time.Now().Unix()
	return &nodev1.SharedDshPoolMemorySample{SchemaVersion: 1, TenantId: "00000000-0000-4000-8000-000000000001", PhysicalInstanceId: "00000000-0000-4000-8000-000000000002", ObservedGeneration: 1,
		ObservedAt: now, ValidUntil: now + 30, MemoryUsedBytes: 79, MemoryLimitBytes: 100}
}

func TestPoolMemoryProductionSendLoopCarriesFieldTwelve(t *testing.T) {
	initial := poolSample()
	fresh := proto.Clone(initial).(*nodev1.SharedDshPoolMemorySample)
	fresh.MemoryUsedBytes = 81
	reads := 0
	s := New(Config{SharedRuntime: &runtime.PersistentPools{Memory: func(context.Context, string) (*nodev1.SharedDshPoolMemorySample, error) { reads++; return fresh, nil }}})
	s.connected, s.connectionEpoch = true, 1
	s.ReportShared(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshPoolMemorySample{SharedDshPoolMemorySample: initial}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := &blockedUplinkStream{ctx: ctx, frames: make(chan *nodev1.SessionRequest, 1)}
	done := make(chan error, 1)
	go func() { done <- s.sendLoop(ctx, stream, time.Hour, nil) }()
	select {
	case frame := <-stream.frames:
		if frame.GetSharedDshPoolMemorySample() == nil || frame.GetSharedDshPoolMemorySample().MemoryUsedBytes != 81 {
			t.Fatal("send loop used a stale counter or wrong carrier", frame)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("memory field12 never reached the production send loop")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("unexpected send-loop exit", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("send loop did not cancel")
	}
	if reads != 1 {
		t.Fatal("send path did not resample exactly once", reads)
	}
}

func TestPoolMemoryConnectionGenerationAndFreshnessFences(t *testing.T) {
	for _, scenario := range []string{"disconnected", "reconnect", "stale-queue", "stale-source", "old-generation", "foreign-tenant", "foreign-pool", "time-reversed", "source-failure", "source-absent", "canceled", "backward-highwater", "unknown-source-fields"} {
		t.Run(scenario, func(t *testing.T) {
			initial := poolSample()
			fresh := proto.Clone(initial).(*nodev1.SharedDshPoolMemorySample)
			s := New(Config{SharedRuntime: &runtime.PersistentPools{}})
			s.connected, s.connectionEpoch = true, 1
			s.cfg.SharedRuntime.Memory = func(context.Context, string) (*nodev1.SharedDshPoolMemorySample, error) {
				if scenario == "source-failure" {
					return nil, errors.New("synthetic sampler outage")
				}
				return fresh, nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "disconnected" {
				s.connected = false
			}
			s.ReportShared(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshPoolMemorySample{SharedDshPoolMemorySample: initial}})
			if scenario == "disconnected" {
				if len(s.poolMemoryCh) != 0 {
					t.Fatal("offline telemetry survived into a new connection")
				}
				return
			}
			queued := <-s.poolMemoryCh
			switch scenario {
			case "reconnect":
				s.connectionEpoch++
			case "stale-queue":
				queued.sample.ValidUntil = time.Now().Unix()
			case "stale-source":
				fresh.ValidUntil = time.Now().Unix()
			case "old-generation":
				fresh.ObservedGeneration++
			case "foreign-tenant":
				fresh.TenantId = "00000000-0000-4000-8000-000000000003"
			case "foreign-pool":
				fresh.PhysicalInstanceId = "00000000-0000-4000-8000-000000000003"
			case "time-reversed":
				fresh.ObservedAt--
			case "source-absent":
				s.cfg.SharedRuntime.Memory = nil
			case "unknown-source-fields":
				fresh.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			case "canceled":
				cancel()
			case "backward-highwater":
				prior := proto.Clone(initial).(*nodev1.SharedDshPoolMemorySample)
				prior.ObservedGeneration++
				s.lastPoolMemory = map[string]*nodev1.SharedDshPoolMemorySample{initial.PhysicalInstanceId: prior}
			}
			if sample := s.poolMemoryForSend(ctx, queued); sample != nil {
				t.Fatal("invalid telemetry became a valid current sample", sample)
			}
		})
	}
}
