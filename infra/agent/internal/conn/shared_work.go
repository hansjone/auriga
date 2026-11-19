package conn

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

const sharedQueueSize = 64

type sharedJob struct {
	ctx     context.Context
	message *nodev1.SessionResponse
	key     [32]byte
}

type sharedReceipt struct {
	*nodev1.SharedDshRuntimeReceipt
	context context.Context
}

// One worker for the lifetime of Session. Reconnect cannot multiply workers if
// an OS helper disregards cancellation. Waiting entries are dropped on cancel;
// W's durable outbox retries any entry that could not be queued.
type sharedWorkQueue struct {
	s       *Session
	mu      sync.Mutex
	jobs    []sharedJob
	keys    map[[32]byte]bool
	started bool
	done    chan struct{}
	timeout time.Duration
}

func newSharedWorkQueue(s *Session) *sharedWorkQueue {
	return &sharedWorkQueue{s: s, keys: map[[32]byte]bool{}, timeout: 2 * time.Minute}
}

func (q *sharedWorkQueue) reset(ctx context.Context) {
	// A reset is ordered before this connection's views/intents. Old in-flight
	// work has its canceled context and may not publish a receipt into this one.
	q.mu.Lock()
	for _, job := range q.jobs {
		delete(q.keys, job.key)
	}
	q.jobs = []sharedJob{{ctx: ctx}}
	q.startLocked()
	q.mu.Unlock()
}

func (q *sharedWorkQueue) enqueue(ctx context.Context, msg *nodev1.SessionResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return err
	}
	key := sha256.Sum256(data)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.keys[key] {
		return nil
	}
	if len(q.jobs) >= sharedQueueSize {
		return fmt.Errorf("shared work queue full; awaiting durable retry")
	}
	q.keys[key] = true
	q.jobs = append(q.jobs, sharedJob{ctx: ctx, message: proto.Clone(msg).(*nodev1.SessionResponse), key: key})
	q.startLocked()
	return nil
}

func (q *sharedWorkQueue) startLocked() {
	if !q.started {
		q.started = true
		q.done = make(chan struct{})
		go q.run(q.done)
	}
}

func (q *sharedWorkQueue) wait(ctx context.Context) error {
	q.mu.Lock()
	done := q.done
	q.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *sharedWorkQueue) run(done chan struct{}) {
	defer close(done)
	for {
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.started = false
			q.mu.Unlock()
			return
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()
		if job.ctx.Err() == nil {
			q.execute(job)
		}
		q.mu.Lock()
		delete(q.keys, job.key)
		q.mu.Unlock()
	}
}

func (q *sharedWorkQueue) execute(job sharedJob) {
	ctx, cancel := context.WithTimeout(job.ctx, q.timeout)
	defer cancel()
	s := q.s
	if job.message == nil {
		if s.cfg.SharedRuntime != nil {
			s.cfg.SharedRuntime.ResetViews()
		}
		// Discard receipts left by the disconnected stream before its new work.
		for {
			select {
			case <-s.sharedReceiptCh:
				continue
			default:
				return
			}
		}
	}
	var receipt *nodev1.SharedDshRuntimeReceipt
	var err error
	if v := job.message.GetSharedDshResourceView(); v != nil {
		err = s.cfg.SharedRuntime.View(ctx, v)
	} else if intent := job.message.GetSharedDshRuntimeIntent(); intent != nil {
		if s.cfg.SharedRuntime != nil {
			receipt, err = s.cfg.SharedRuntime.Prepare(ctx, intent)
		} else {
			receipt, err = s.cfg.SharedPools.Apply(ctx, intent)
		}
	}
	if err != nil {
		s.cfg.Logger.Warn("shared work failed", "error", err)
	}
	if ctx.Err() != nil {
		s.cfg.Logger.Warn("shared work canceled", "error", ctx.Err())
	}
	if receipt != nil && ctx.Err() == nil {
		select {
		case s.sharedReceiptCh <- &sharedReceipt{receipt, job.ctx}:
		case <-ctx.Done():
		}
	}
}
