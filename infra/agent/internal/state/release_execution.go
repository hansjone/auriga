package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
)

var (
	bucketReleaseExecution = []byte("release_execution_v1")
	bucketReleaseIntent    = []byte("release_intent_v1")
	ErrReleaseUnresolved   = errors.New("runtime release has unresolved execution; protected recovery required")
	ErrExecutionCorrupt    = errors.New("invalid persisted runtime execution")
)

// ExecutionRecord is independent of observed status and desired state. DSH is
// the original complete signed runtime, not the runtime from a newer desired.
type ExecutionRecord struct {
	Version     int                `json:"version"`
	InstanceID  string             `json:"instanceId"`
	Generation  int64              `json:"generation"`
	DSH         *nodev1.DshRuntime `json:"dsh"`
	Candidate   ReleaseCandidate   `json:"candidate"`
	ContainerID string             `json:"containerId"`
	ConfigID    string             `json:"configId"`
	ProofDigest string             `json:"proofDigest"`
	Phase       string             `json:"phase"`
}

func digestID(s string) bool {
	if len(s) != 71 || s[:7] != "sha256:" {
		return false
	}
	b, err := hex.DecodeString(s[7:])
	return err == nil && hex.EncodeToString(b) == s[7:]
}

func (r ExecutionRecord) validate() error {
	if r.Version != 1 || storage.ValidateInstanceID(r.InstanceID) != nil || r.Generation <= 0 || r.DSH == nil || r.DSH.ReleaseEnvelope == nil || r.Candidate.validate() != nil || !digestID(r.ConfigID) || !digestID(r.ProofDigest) || !validContainerID(r.ContainerID) || (r.Phase != "created" && r.Phase != "started") {
		return ErrExecutionCorrupt
	}
	e := r.DSH.ReleaseEnvelope
	if r.ProofDigest != e.ImageDigest || r.Candidate.Sequence != e.ReleaseSequence || r.Candidate.Partition != (ReleasePartition{e.Purpose, e.Name, e.Os, e.Arch}) {
		return ErrExecutionCorrupt
	}
	return nil
}

func validContainerID(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && hex.EncodeToString(b) == s
}

func readExecutions(tx *bbolt.Tx) ([]ExecutionRecord, error) {
	var out []ExecutionRecord
	b := tx.Bucket(bucketReleaseExecution)
	if b == nil {
		return out, nil
	}
	err := b.ForEach(func(k, v []byte) error {
		var r ExecutionRecord
		if v == nil || json.Unmarshal(v, &r) != nil || r.validate() != nil || string(k) != r.InstanceID {
			return ErrExecutionCorrupt
		}
		raw, err := json.Marshal(r)
		if err != nil || !bytes.Equal(raw, v) {
			return ErrExecutionCorrupt
		}
		out = append(out, r)
		return nil
	})
	return out, err
}

func (s *Store) ListExecutions() ([]ExecutionRecord, error) {
	var out []ExecutionRecord
	err := s.db.View(func(tx *bbolt.Tx) error { var err error; out, err = readExecutions(tx); return err })
	return out, err
}

func (s *Store) GetExecution(id string) (*ExecutionRecord, error) {
	if err := storage.ValidateInstanceID(id); err != nil {
		return nil, err
	}
	all, err := s.ListExecutions()
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		if r.InstanceID == id {
			return &r, nil
		}
	}
	return nil, nil
}

func (s *Store) PutExecution(r ExecutionRecord) error {
	if err := r.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err = s.db.Update(func(tx *bbolt.Tx) error {
		if _, err := readExecutions(tx); err != nil {
			return err
		}
		b, err := tx.CreateBucketIfNotExists(bucketReleaseExecution)
		if err != nil {
			return err
		}
		return b.Put([]byte(r.InstanceID), raw)
	}); err != nil {
		return err
	}
	return s.db.Sync()
}

// DeleteExecution requires the caller to have confirmed removal of this exact
// container. A stale cleanup cannot drop a replacement container's reference.
func (s *Store) DeleteExecution(id, containerID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		all, err := readExecutions(tx)
		if err != nil {
			return err
		}
		for _, r := range all {
			if r.InstanceID == id {
				if r.ContainerID != containerID {
					return ErrExecutionCorrupt
				}
				return tx.Bucket(bucketReleaseExecution).Delete([]byte(id))
			}
		}
		return nil
	})
}

type releaseIntent struct {
	Version   int              `json:"version"`
	Token     string           `json:"token"`
	Candidate ReleaseCandidate `json:"candidate"`
}

func readIntents(tx *bbolt.Tx, p ReleasePartition) (*releaseIntent, error) {
	var found *releaseIntent
	b := tx.Bucket(bucketReleaseIntent)
	if b == nil {
		return nil, nil
	}
	err := b.ForEach(func(k, v []byte) error {
		var r releaseIntent
		if v == nil || json.Unmarshal(v, &r) != nil || r.Version != 1 || !validContainerID(r.Token) || r.Candidate.validate() != nil || string(k) != r.Candidate.Partition.key() {
			return ErrExecutionCorrupt
		}
		raw, err := json.Marshal(r)
		if err != nil || !bytes.Equal(raw, v) {
			return ErrExecutionCorrupt
		}
		if r.Candidate.Partition == p {
			found = &r
		}
		return nil
	})
	return found, err
}

func (s *Store) checkIntent(p ReleasePartition) error {
	return s.db.View(func(tx *bbolt.Tx) error {
		r, err := readIntents(tx, p)
		if err != nil {
			return err
		}
		if r != nil {
			return ErrReleaseUnresolved
		}
		return nil
	})
}

// CheckReleaseExecution is a fail-closed observation check, not qualification.
func (s *Store) CheckReleaseExecution(c ReleaseCandidate) error {
	if err := c.validate(); err != nil {
		return err
	}
	return s.db.View(func(tx *bbolt.Tx) error {
		intent, err := readIntents(tx, c.Partition)
		if err != nil {
			return err
		}
		if intent != nil {
			return ErrReleaseUnresolved
		}
		high, found, err := readReleaseActivation(tx, c.Partition)
		if err != nil {
			return err
		}
		if !found {
			return ErrExecutionCorrupt
		}
		if c.Sequence < high.Sequence {
			return ErrReleaseRollback
		}
		if c != high {
			return ErrReleaseConflict
		}
		return nil
	})
}

// ReleaseExecution exists only in the original synchronous qualification. A
// failed request remains pending even if a later request returns successfully.
// There is deliberately no public API for clearing a persisted unresolved intent.
type ReleaseExecution struct {
	mu      sync.Mutex
	pending int
	active  bool
}

func (e *ReleaseExecution) BeginAction() (func(bool), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.active {
		return nil, ErrReleaseUnresolved
	}
	e.pending++
	var once sync.Once
	return func(completed bool) {
		once.Do(func() {
			if completed {
				e.mu.Lock()
				e.pending--
				e.mu.Unlock()
			}
		})
	}, nil
}

// WithReleaseExecution adds crash persistence around RT2S's unchanged continuous
// lock. An intent is synced before any Engine mutation. Only this invocation may
// compare/delete its own intent after all actions have definite replies. Panic,
// cancellation or loss of a reply leaves the partition blocked across reopen.
func (s *Store) WithReleaseExecution(ctx context.Context, c ReleaseCandidate, authorize func(context.Context) error, activate func(context.Context, *ReleaseExecution) error) error {
	if authorize == nil || activate == nil {
		return fmt.Errorf("execution requires both callbacks")
	}
	return s.WithReleaseActivation(ctx, c, func(ctx context.Context) error {
		if err := s.checkIntent(c.Partition); err != nil {
			return err
		}
		return authorize(ctx)
	}, func(ctx context.Context) error {
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		intent := releaseIntent{1, hex.EncodeToString(token[:]), c}
		raw, err := json.Marshal(intent)
		if err != nil {
			return err
		}
		if err = s.db.Update(func(tx *bbolt.Tx) error {
			old, err := readIntents(tx, c.Partition)
			if err != nil {
				return err
			}
			if old != nil {
				return ErrReleaseUnresolved
			}
			b, err := tx.CreateBucketIfNotExists(bucketReleaseIntent)
			if err != nil {
				return err
			}
			return b.Put([]byte(c.Partition.key()), raw)
		}); err != nil {
			return err
		}
		if err = s.db.Sync(); err != nil {
			return err
		}
		e := &ReleaseExecution{active: true}
		defer func() { e.mu.Lock(); e.active = false; e.mu.Unlock() }()
		err = activate(ctx, e)
		e.mu.Lock()
		complete := e.pending == 0
		e.active = false
		e.mu.Unlock()
		if !complete || ctx.Err() != nil {
			return errors.Join(err, ErrReleaseUnresolved)
		}
		clearErr := s.db.Update(func(tx *bbolt.Tx) error {
			old, err := readIntents(tx, c.Partition)
			if err != nil {
				return err
			}
			if old == nil || *old != intent {
				return ErrReleaseUnresolved
			}
			high, found, err := readReleaseActivation(tx, c.Partition)
			if err != nil {
				return err
			}
			if !found || high != c {
				return ErrReleaseUnresolved
			}
			return tx.Bucket(bucketReleaseIntent).Delete([]byte(c.Partition.key()))
		})
		// Open uses bbolt's synchronous commits. A successful Update is the
		// definite durable deletion; a failed commit preserves fail-closed state.
		return errors.Join(err, clearErr)
	})
}
