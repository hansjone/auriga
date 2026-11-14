package projection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// Prepare verifies the chain, scope, private/public pairs and current authority
// fence, and returns an atomic in-memory commit. It must not publish
// anything before that commit is called. The reader serializes this with reload.
type Prepare func(*Generation) (commit func() error, err error)
type Reader struct {
	mu            sync.Mutex
	root, control string
	prepare       Prepare
	selected      *api.LocalProjectionSelector
	highest       *api.LocalProjectionSelector
	phase         string
	lastFailure   atomic.Int64
	healthFailure atomic.Int64
	Health        func() error
	Active        func() bool
}

func NewReader(root, control string, prepare Prepare) (*Reader, error) {
	if prepare == nil {
		return nil, errors.New("projection: prepare required")
	}
	if e := directory(root); e != nil {
		return nil, e
	}
	if e := directory(control); e != nil {
		return nil, e
	}
	r := &Reader{root: root, control: control, prepare: prepare}
	b, e := readPrivateFile(filepath.Join(control, "reader-selector.json"), 16384)
	if e == nil {
		var s api.LocalProjectionSelector
		if e = Decode(b, &s, true); e != nil {
			return nil, e
		}
		if e = CheckSelector(s); e != nil {
			return nil, e
		}
		r.highest = &s
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return r, nil
}
func response(s api.LocalProjectionSelector, phase, code string) api.LocalProjectionResponse {
	if phase == "failed" {
		return api.LocalProjectionResponseOneOf1AsLocalProjectionResponse(&api.LocalProjectionResponseOneOf1{Phase: "failed", FailureCode: code, AdditionalProperties: map[string]interface{}{"selector": s}})
	}
	return api.LocalProjectionResponseOneOfAsLocalProjectionResponse(&api.LocalProjectionResponseOneOf{Phase: phase, FailureCode: "none", AdditionalProperties: map[string]interface{}{"selector": s}})
}
func ResponseFields(r api.LocalProjectionResponse) (api.LocalProjectionSelector, string, string, error) {
	raw, e := Bytes(r)
	if e != nil {
		return api.LocalProjectionSelector{}, "", "", e
	}
	return parseResponse(raw)
}
func (r *Reader) Handle(q api.LocalProjectionRequest) api.LocalProjectionResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	fail := func(e error) api.LocalProjectionResponse {
		r.lastFailure.CompareAndSwap(0, time.Now().UnixNano())
		code := "invalid_chain"
		var f Fault
		if errors.As(e, &f) {
			switch string(f) {
			case "version_rollback", "same_version_conflict", "projection_pending":
				code = string(f)
			}
		}
		return response(q.Selector, "failed", code)
	}
	if len(q.AdditionalProperties) != 0 || (q.Operation != "reload" && q.Operation != "confirm") {
		return fail(Fault("invalid_chain"))
	}
	if e := CheckProgress(q.Selector, r.highest); e != nil {
		return fail(e)
	}
	g, e := ReadSelected(r.root, q.Selector)
	if e != nil {
		return fail(e)
	}
	defer g.Clear()
	if e = CheckFiles(g.Selector, g.Manifest, g.Files, q.Selector, r.highest); e != nil {
		return fail(e)
	}
	if q.Operation == "confirm" {
		if r.selected == nil || !Equal(q.Selector, *r.selected) || r.phase == "" || (r.Active != nil && !r.Active()) {
			return fail(Fault("projection_pending"))
		}
		return response(q.Selector, r.phase, "none")
	}
	if r.selected != nil && Equal(*r.selected, q.Selector) && r.phase != "" && (r.Active == nil || r.Active()) {
		r.lastFailure.Store(0)
		return response(q.Selector, r.phase, "none")
	}
	commit, e := r.prepare(g)
	if e != nil {
		return fail(e)
	}
	if commit == nil {
		return fail(Fault("projection_pending"))
	}
	b, e := Bytes(q.Selector)
	if e != nil {
		return fail(e)
	}
	if e = atomicFile(filepath.Join(r.control, "reader-selector.json"), b); e != nil {
		return fail(Fault("projection_pending"))
	}
	// A crash between the durable high-water mark and commit is safe: restart
	// restores the mark with phase empty and has to reload before confirming.
	high := q.Selector
	r.highest = &high
	if e = commit(); e != nil {
		return fail(e)
	}
	same := r.selected != nil && Equal(*r.selected, q.Selector)
	if !same || r.phase == "" {
		r.phase = "loaded"
	}
	s := q.Selector
	r.selected = &s
	r.lastFailure.Store(0)
	return response(s, r.phase, "none")
}

// ConfirmHandshake accepts only an actual handshake verifier, never an RPC
// assertion. A failed/old handshake cannot acknowledge a newer selector.
func (r *Reader) ConfirmHandshake(s api.LocalProjectionSelector, verify func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.selected == nil || !Equal(*r.selected, s) || r.phase == "" {
		return Fault("projection_pending")
	}
	if verify == nil {
		return Fault("projection_pending")
	}
	if e := verify(); e != nil {
		return e
	}
	r.phase = "handshake_verified"
	return nil
}
func (r *Reader) Withdrawn(now time.Time) bool {
	n := r.lastFailure.Load()
	h := r.healthFailure.Load()
	return (n != 0 && now.Sub(time.Unix(0, n)) >= time.Minute) || (h != 0 && now.Sub(time.Unix(0, h)) >= time.Minute)
}
func (r *Reader) RecordHealth(e error) {
	if e == nil {
		r.healthFailure.Store(0)
	} else {
		r.healthFailure.CompareAndSwap(0, time.Now().UnixNano())
	}
}
func (r *Reader) Watch(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		g, e := ReadGeneration(r.root)
		if e == nil {
			_ = r.Handle(api.LocalProjectionRequest{Operation: "reload", Selector: g.Selector})
			g.Clear()
		} else {
			r.lastFailure.CompareAndSwap(0, time.Now().UnixNano())
		}
		if r.Health != nil {
			r.RecordHealth(r.Health())
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func Retry(ctx context.Context, attempt func() error) error {
	delay := time.Second
	for {
		if e := attempt(); e == nil {
			return nil
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		delay *= 2
		if delay > time.Minute {
			delay = time.Minute
		}
	}
}
