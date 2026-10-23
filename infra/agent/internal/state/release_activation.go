package state

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"go.etcd.io/bbolt"
	bbolterrors "go.etcd.io/bbolt/errors"
)

const maxReleaseSequence = uint64(9007199254740991)

var (
	bucketReleaseActivation = []byte("release_activation_v1")
	ErrReleaseRollback      = errors.New("release sequence below high-water mark")
	ErrReleaseConflict      = errors.New("release sequence has a different body digest")
	ErrReleaseStateCorrupt  = errors.New("invalid persisted release high-water mark")
)

// ReleasePartition identifies one of the four runtime release streams. Instance,
// artifact, generation and version deliberately are not partition dimensions.
type ReleasePartition struct {
	Purpose string `json:"purpose"`
	Name    string `json:"name"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// ReleaseCandidate is supplied only by a trusted internal verifier. BodyDigest
// must be sha256:<64 lowercase hex> of the complete JCS body without signature.
// This storage API neither reconstructs that body nor verifies signatures.
type ReleaseCandidate struct {
	Partition  ReleasePartition `json:"partition"`
	Sequence   uint64           `json:"sequence"`
	BodyDigest string           `json:"bodyDigest"`
}

type releaseActivationRecord struct {
	Version   int              `json:"version"`
	Candidate ReleaseCandidate `json:"candidate"`
}

func (p ReleasePartition) lockIndex() (int, error) {
	if p.Purpose != "dsh_runtime" || p.Name != "dsh" ||
		(p.OS != "linux" && p.OS != "windows") || (p.Arch != "amd64" && p.Arch != "arm64") {
		return 0, fmt.Errorf("invalid release partition")
	}
	i := 0
	if p.OS == "windows" {
		i += 2
	}
	if p.Arch == "arm64" {
		i++
	}
	return i, nil
}

func (p ReleasePartition) key() string {
	return p.Purpose + "/" + p.Name + "/" + p.OS + "/" + p.Arch
}

func (c ReleaseCandidate) validate() error {
	if _, err := c.Partition.lockIndex(); err != nil {
		return err
	}
	if c.Sequence == 0 || c.Sequence > maxReleaseSequence {
		return fmt.Errorf("invalid release sequence")
	}
	if len(c.BodyDigest) != 71 || c.BodyDigest[:7] != "sha256:" {
		return fmt.Errorf("invalid release body digest")
	}
	raw, err := hex.DecodeString(c.BodyDigest[7:])
	if err != nil || hex.EncodeToString(raw) != c.BodyDigest[7:] {
		return fmt.Errorf("invalid release body digest")
	}
	return nil
}

// readReleaseActivation validates every entry in the bounded four-record bucket,
// including key binding and exact internal JSON shape. Noncanonical encodings,
// duplicate/unknown/missing fields, nulls and nested buckets fail closed.
func readReleaseActivation(tx *bbolt.Tx, p ReleasePartition) (ReleaseCandidate, bool, error) {
	var out ReleaseCandidate
	var found bool
	b := tx.Bucket(bucketReleaseActivation)
	if b == nil {
		return out, false, nil
	}
	err := b.ForEach(func(k, v []byte) error {
		var record releaseActivationRecord
		if v == nil || json.Unmarshal(v, &record) != nil || record.Version != 1 || record.Candidate.validate() != nil || string(k) != record.Candidate.Partition.key() {
			return ErrReleaseStateCorrupt
		}
		raw, err := json.Marshal(record)
		if err != nil || !bytes.Equal(raw, v) {
			return ErrReleaseStateCorrupt
		}
		if record.Candidate.Partition == p {
			out, found = record.Candidate, true
		}
		return nil
	})
	return out, found, err
}

// GetReleaseHighWater returns a snapshot; absence is reported by found=false.
// The snapshot never grants activation qualification.
func (s *Store) GetReleaseHighWater(p ReleasePartition) (candidate ReleaseCandidate, found bool, err error) {
	if _, err = p.lockIndex(); err != nil {
		return candidate, false, err
	}
	err = s.db.View(func(tx *bbolt.Tx) error {
		candidate, found, err = readReleaseActivation(tx, p)
		return err
	})
	return candidate, found, err
}

func (s *Store) acquireReleaseActivation(ctx context.Context, index int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.activationStop:
		return bbolterrors.ErrDatabaseNotOpen
	case <-s.activationLocks[index]:
	}
	s.activationMu.Lock()
	defer s.activationMu.Unlock()
	var err error
	if s.activationClosing {
		err = bbolterrors.ErrDatabaseNotOpen
	} else {
		err = ctx.Err()
	}
	if err != nil {
		s.activationLocks[index] <- struct{}{}
		return err
	}
	s.activationActive.Add(1)
	return nil
}

// WithReleaseActivation synchronously holds a partition qualification across
// fresh authorization, the bbolt commit and ALL actual create/start actions.
// authorize must recheck current key validity and prepared content evidence;
// activate must join all work before returning, including on cancellation/panic.
// Neither callback may be nil or reenter this partition/Close on this Store.
// The callbacks receive ctx, but cancellation never releases a running callback.
// Errors/panics retain committed high-water state; panics propagate after release.
//
// This is a trusted single-agent/local-DB lifecycle boundary, not a distributed
// fence. All runtime entry points must use this Store and obey callback lifetime.
// Independent databases, forced process replacement and detached driver actions
// require external lifecycle control; deleting the DB is not supported recovery.
func (s *Store) WithReleaseActivation(ctx context.Context, candidate ReleaseCandidate, authorize, activate func(context.Context) error) error {
	if ctx == nil || authorize == nil || activate == nil {
		return fmt.Errorf("release activation requires context and both callbacks")
	}
	if err := candidate.validate(); err != nil {
		return err
	}
	index, err := candidate.Partition.lockIndex()
	if err != nil {
		return err
	}
	if err := s.acquireReleaseActivation(ctx, index); err != nil {
		return err
	}
	defer func() {
		s.activationLocks[index] <- struct{}{}
		s.activationActive.Done()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := authorize(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = s.db.Update(func(tx *bbolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		old, found, err := readReleaseActivation(tx, candidate.Partition)
		if err != nil {
			return err
		}
		if found {
			if candidate.Sequence < old.Sequence {
				return ErrReleaseRollback
			}
			if candidate.Sequence == old.Sequence {
				if candidate.BodyDigest != old.BodyDigest {
					return ErrReleaseConflict
				}
				return nil
			}
		}
		b, err := tx.CreateBucketIfNotExists(bucketReleaseActivation)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(releaseActivationRecord{Version: 1, Candidate: candidate})
		if err != nil {
			return err
		}
		return b.Put([]byte(candidate.Partition.key()), raw)
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return activate(ctx)
}
