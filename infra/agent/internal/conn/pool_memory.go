package conn

import (
	"context"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

type queuedPoolMemory struct {
	sample *nodev1.SharedDshPoolMemorySample
	epoch  uint64
	sent   chan error
}

func (q queuedPoolMemory) complete(err error) {
	if q.sent != nil {
		q.sent <- err
	}
}

func validPoolMemory(v *nodev1.SharedDshPoolMemorySample) bool {
	const safe = 9007199254740991
	now := time.Now().Unix()
	return v != nil && len(v.ProtoReflect().GetUnknown()) == 0 && v.SchemaVersion == 1 && v.TenantId != "" && v.PhysicalInstanceId != "" &&
		v.ObservedGeneration > 0 && v.ObservedGeneration <= safe && v.ObservedAt >= 0 && v.ObservedAt <= now &&
		v.ValidUntil > now && v.ValidUntil-v.ObservedAt <= 30 && v.MemoryUsedBytes >= 0 && v.MemoryUsedBytes <= safe &&
		v.MemoryLimitBytes > 0 && v.MemoryLimitBytes <= safe
}

func (s *Session) reportPoolMemory(sample *nodev1.SharedDshPoolMemorySample) {
	if !validPoolMemory(sample) {
		return
	}
	s.mu.Lock()
	connected, epoch := s.connected, s.connectionEpoch
	s.mu.Unlock()
	if !connected {
		return
	}
	queued := queuedPoolMemory{sample: proto.Clone(sample).(*nodev1.SharedDshPoolMemorySample), epoch: epoch}
	select {
	case s.poolMemoryCh <- queued:
	default:
	}
}

func (s *Session) poolMemoryForSend(ctx context.Context, queued queuedPoolMemory) *nodev1.SharedDshPoolMemorySample {
	s.mu.Lock()
	current := s.connected && queued.epoch == s.connectionEpoch
	s.mu.Unlock()
	if !current || !validPoolMemory(queued.sample) || s.cfg.SharedRuntime == nil || s.cfg.SharedRuntime.Memory == nil {
		return nil
	}
	// Re-read the signed execution and actual Engine counters at send time.
	// Queued telemetry cannot cross reconnect or revive an earlier generation.
	sample, err := s.cfg.SharedRuntime.Memory(ctx, queued.sample.PhysicalInstanceId)
	if err != nil || !validPoolMemory(sample) || sample.TenantId != queued.sample.TenantId || sample.PhysicalInstanceId != queued.sample.PhysicalInstanceId || sample.ObservedGeneration != queued.sample.ObservedGeneration || sample.ObservedAt < queued.sample.ObservedAt {
		return nil
	}
	s.mu.Lock()
	current = s.connected && queued.epoch == s.connectionEpoch
	if prior := s.lastPoolMemory[sample.PhysicalInstanceId]; prior != nil && (prior.ObservedGeneration > sample.ObservedGeneration || (prior.ObservedGeneration == sample.ObservedGeneration && prior.ObservedAt > sample.ObservedAt)) {
		current = false
	}
	if current {
		if s.lastPoolMemory == nil {
			s.lastPoolMemory = map[string]*nodev1.SharedDshPoolMemorySample{}
		}
		s.lastPoolMemory[sample.PhysicalInstanceId] = proto.Clone(sample).(*nodev1.SharedDshPoolMemorySample)
	}
	s.mu.Unlock()
	if !current || ctx.Err() != nil {
		return nil
	}
	return sample
}
