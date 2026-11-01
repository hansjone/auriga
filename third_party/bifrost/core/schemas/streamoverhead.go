package schemas

import (
	"context"
	"sync/atomic"
	"time"
)

type streamOverhead struct {
	parseNs        atomic.Int64
	convertNs      atomic.Int64
	backpressureNs atomic.Int64

	attemptParseNs        atomic.Int64
	attemptConvertNs      atomic.Int64
	attemptBackpressureNs atomic.Int64
}

func (bc *BifrostContext) MarkStreamAttemptStart() {
	acc := streamOverheadFrom(bc)
	if acc == nil {
		return
	}
	acc.attemptParseNs.Store(acc.parseNs.Load())
	acc.attemptConvertNs.Store(acc.convertNs.Load())
	acc.attemptBackpressureNs.Store(acc.backpressureNs.Load())
}

func (bc *BifrostContext) ResetStreamOverhead() {
	if bc == nil {
		return
	}
	bc.setReservedValue(BifrostContextKeyStreamOverhead, &streamOverhead{})
}

func streamOverheadFrom(ctx context.Context) *streamOverhead {
	if isNilContext(ctx) {
		return nil
	}
	acc, _ := ctx.Value(BifrostContextKeyStreamOverhead).(*streamOverhead)
	return acc
}

func AddStreamParse(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	if acc := streamOverheadFrom(ctx); acc != nil {
		acc.parseNs.Add(int64(d))
	}
}

func AddStreamConvert(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	if acc := streamOverheadFrom(ctx); acc != nil {
		acc.convertNs.Add(int64(d))
	}
}

func AddStreamBackpressure(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	if acc := streamOverheadFrom(ctx); acc != nil {
		acc.backpressureNs.Add(int64(d))
	}
}

func (bc *BifrostContext) StampStreamOverhead() {
	if bc == nil {
		return
	}
	acc := streamOverheadFrom(bc)
	if acc == nil {
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
	if parse := acc.parseNs.Load(); parse > 0 {
		tracer.SetAttribute(handle, AttrBifrostStreamParseMs, float64(parse)/float64(time.Millisecond))
	}
	if convert := acc.convertNs.Load(); convert > 0 {
		tracer.SetAttribute(handle, AttrBifrostStreamConvertMs, float64(convert)/float64(time.Millisecond))
	}
	if bp := acc.backpressureNs.Load(); bp > 0 {
		tracer.SetAttribute(handle, AttrBifrostStreamBackpressureMs, float64(bp)/float64(time.Millisecond))
	}
}

func (bc *BifrostContext) StampWorkerHandoff(d time.Duration) {
	if bc == nil || d <= 0 {
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
	tracer.SetAttribute(handle, AttrBifrostWorkerHandoffMs, float64(d)/float64(time.Millisecond))
}
