// Package relaysupervisor owns only process-local, explicitly handed-off calls.
package relaysupervisor

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
)

type Pending struct {
	Ref       budget.Ref      `json:"-"`
	StopProof <-chan struct{} `json:"-"`
}

func (p Pending) String() string   { return "AI relay pending" }
func (p Pending) GoString() string { return p.String() }

type ledger interface {
	GetRecoveryCandidate(context.Context, budget.Ref) (*budget.RecoveryCandidate, error)
	Recover(context.Context, budget.Ref, int64, budget.RecoveryEvidence) (*budget.Reservation, error)
}
type cell struct {
	occupied, handed, fault bool
	pending                 Pending
}
type Stats struct {
	Occupied, Pending, Faulted int
	Closed                     bool
}
type Supervisor struct {
	mu      sync.Mutex
	cells   []cell
	free    []int
	closed  bool
	l       ledger
	timeout time.Duration
	wake    chan struct{}
	stop    context.CancelFunc
	done    chan struct{}
}

func New(l ledger, capacity int, timeout, backoff time.Duration) (*Supervisor, error) {
	if l == nil || capacity < 1 || capacity > 4096 || timeout <= 0 || timeout > time.Second || backoff < time.Millisecond || backoff > time.Second {
		return nil, errors.New("common.validation_failed")
	}
	s := &Supervisor{l: l, cells: make([]cell, capacity), free: make([]int, capacity), timeout: timeout, wake: make(chan struct{}, 1), done: make(chan struct{})}
	for i := range s.free {
		s.free[i] = i
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	go s.run(ctx, backoff)
	return s, nil
}

// Reserve and finish use fixed cells and never perform I/O. finish is idempotent;
// closure-local once prevents an old token from altering a reused cell.
func (s *Supervisor) Reserve() (func(*Pending), bool) {
	s.mu.Lock()
	if s.closed || len(s.free) == 0 {
		s.mu.Unlock()
		return nil, false
	}
	i := s.free[len(s.free)-1]
	s.free = s.free[:len(s.free)-1]
	s.cells[i] = cell{occupied: true}
	s.mu.Unlock()
	var once sync.Once
	return func(p *Pending) {
		once.Do(func() {
			s.mu.Lock()
			if p == nil {
				s.release(i)
			} else {
				s.cells[i].pending = *p
				s.cells[i].handed = true
			}
			s.mu.Unlock()
			s.signal()
		})
	}, true
}
func (s *Supervisor) release(i int) { s.cells[i] = cell{}; s.free = append(s.free, i) }
func (s *Supervisor) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Supervisor) CloseAdmission() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); s.signal() }
func (s *Supervisor) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Stats{Closed: s.closed}
	for _, c := range s.cells {
		if c.occupied {
			r.Occupied++
		}
		if c.handed {
			r.Pending++
		}
		if c.fault {
			r.Faulted++
		}
	}
	return r
}

// Drain includes reservations not yet handed off. A timeout does not free cells
// or claim stopped execution; late finish calls still retain their original cell.
func (s *Supervisor) Drain(ctx context.Context) error {
	s.CloseAdmission()
	tick := time.NewTicker(time.Millisecond * 5)
	defer tick.Stop()
	for s.Stats().Occupied != 0 {
		select {
		case <-ctx.Done():
			s.stop()
			<-s.done
			return errors.New("device recovery unresolved")
		case <-tick.C:
		}
	}
	s.stop()
	<-s.done
	return nil
}
func (s *Supervisor) run(ctx context.Context, backoff time.Duration) {
	defer close(s.done)
	tick := time.NewTicker(backoff)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
		for i := range s.cells {
			if ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			c := s.cells[i]
			s.mu.Unlock()
			if !c.handed {
				continue
			}
			complete, fault := s.reconcile(ctx, c.pending)
			s.mu.Lock()
			if complete {
				s.release(i)
			} else {
				s.cells[i].fault = fault
			}
			s.mu.Unlock()
		}
	}
}
func (s *Supervisor) reconcile(ctx context.Context, p Pending) (bool, bool) {
	// Missing/invalid references are unresolved responsibility, never rollback.
	if p.Ref.TenantID == "" || p.Ref.AccountID == "" || p.Ref.RequestID == "" {
		return false, true
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	c, err := s.l.GetRecoveryCandidate(ctx, p.Ref)
	if err != nil || c == nil || c.Ref != p.Ref {
		return false, true
	}
	proof := budget.RecoveryEvidence{}
	switch c.State {
	case "reserved":
		proof.NeverSubmitted = true // Execute returned; same Core cannot dispatch this call again.
	case "dispatching", "unknown":
		if c.State == "unknown" && !c.SlotHeld {
			return true, false
		}
		select {
		case _, open := <-p.StopProof:
			proof.ExecutionStopped = !open
		default:
		}
		if c.State == "unknown" && !proof.ExecutionStopped {
			return false, false
		}
	case "completed", "failed", "cancelled":
		return !c.SlotHeld, c.SlotHeld
	default:
		return false, true
	}
	_, err = s.l.Recover(ctx, p.Ref, c.Revision, proof)
	// Always reread, even after success: a COMMIT response is not a new dispatch
	// right, and concurrent recovery/CAS or ambiguous commit needs current state.
	return false, err != nil
}
