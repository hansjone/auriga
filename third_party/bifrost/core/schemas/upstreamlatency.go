// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type upstreamWaitState struct {
	mu       sync.Mutex
	inFlight int
	since    time.Time
}
type UpstreamWait struct {
	acc *upstreamAccumulator
	ws  *upstreamWaitState
}
type upstreamAccumulator struct {
	total             atomic.Int64
	attemptStartNanos atomic.Int64
	attemptUpstream   atomic.Int64
	attempts          atomic.Int64
	wait              atomic.Pointer[upstreamWaitState]
}

func (bc *BifrostContext) ResetUpstreamLatency() {
	if bc == nil {
		return
	}
	bc.setReservedValue(BifrostContextKeyUpstreamLatency, &upstreamAccumulator{})
}

func upstreamAccumulatorFrom(ctx context.Context) *upstreamAccumulator {
	if isNilContext(ctx) {
		return nil
	}
	acc, _ := ctx.Value(BifrostContextKeyUpstreamLatency).(*upstreamAccumulator)
	return acc
}

func (bc *BifrostContext) MarkAttemptStart() {
	acc := upstreamAccumulatorFrom(bc)
	if acc == nil {
		return
	}
	acc.attemptUpstream.Store(acc.total.Load())
	acc.attemptStartNanos.Store(time.Now().UnixNano())
	acc.attempts.Add(1)
}

func AddUpstreamLatency(ctx context.Context, d time.Duration) {
	if d <= 0 || isNilContext(ctx) {
		return
	}
	if acc, ok := ctx.Value(BifrostContextKeyUpstreamLatency).(*upstreamAccumulator); ok && acc != nil {
		acc.total.Add(int64(d))
	}
}

func (w UpstreamWait) End() {
	ws := w.ws
	if ws == nil {
		return
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.inFlight == 0 {
		return
	}
	ws.inFlight--
	if ws.inFlight == 0 {
		w.acc.total.Add(int64(time.Since(ws.since)))
	}
}

func isNilContext(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	bc, ok := ctx.(*BifrostContext)
	return ok && bc == nil
}

func GetUpstreamLatency(ctx context.Context) (time.Duration, bool) {
	if isNilContext(ctx) {
		return 0, false
	}
	if acc, ok := ctx.Value(BifrostContextKeyUpstreamLatency).(*upstreamAccumulator); ok && acc != nil {
		return time.Duration(acc.total.Load()), true
	}
	return 0, false
}

func (bc *BifrostContext) StampUpstreamLatency() {
	if bc == nil {
		return
	}
	upstream, ok := GetUpstreamLatency(bc)
	if !ok {
		return
	}
	traceID, _ := bc.Value(BifrostContextKeyTraceID).(string)
	if traceID == "" {
		return
	}
	tracer, _ := bc.Value(BifrostContextKeyTracer).(Tracer)
	if tracer == nil {
		return
	}

	handle := tracer.GetSpanHandleByID(traceID, nil)
	if handle == nil {
		return
	}
	tracer.SetAttribute(handle, AttrBifrostUpstreamDurationMs, float64(upstream)/float64(time.Millisecond))
}

func (r *BifrostResponse) PopulateUpstreamLatency(ctx context.Context) {
	if r == nil {
		return
	}
	upstream, ok := GetUpstreamLatency(ctx)
	if !ok {
		return
	}
	if ef := r.GetExtraFields(); ef != nil {
		ms := int64(upstream / time.Millisecond)
		ef.UpstreamLatency = &ms
	}
}

func (r *BifrostResponse) PopulateOverheadLatency(ctx context.Context, total time.Duration) {
	if r == nil {
		return
	}
	overhead, ok := CalculateOverhead(ctx, total)
	if !ok {
		return
	}
	if ef := r.GetExtraFields(); ef != nil {
		ms := int64(overhead / time.Millisecond)
		ef.OverheadLatency = &ms
	}
}

func (r *BifrostA2AResponse) PopulateUpstreamLatency(ctx context.Context) {
	if r == nil {
		return
	}
	upstream, ok := GetUpstreamLatency(ctx)
	if !ok {
		return
	}
	ms := int64(upstream / time.Millisecond)
	r.ExtraFields.UpstreamLatency = &ms
}

func (r *BifrostA2AResponse) PopulateOverheadLatency(ctx context.Context, total time.Duration) {
	if r == nil {
		return
	}
	overhead, ok := CalculateOverhead(ctx, total)
	if !ok {
		return
	}
	ms := int64(overhead / time.Millisecond)
	r.ExtraFields.OverheadLatency = &ms
}

func CalculateOverhead(ctx context.Context, total time.Duration) (time.Duration, bool) {
	upstream, ok := GetUpstreamLatency(ctx)
	if !ok {
		return 0, false
	}
	overhead := total - upstream
	if overhead < 0 {
		overhead = 0
	}
	return overhead, true
}
