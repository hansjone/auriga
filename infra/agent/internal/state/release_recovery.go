package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

// OpenOfflineRecovery never creates an installation or initializes buckets.
// Its exclusive bbolt lock is retained until Close, excluding the live agent.
func OpenOfflineRecovery(agentDir string) (*Store, error) {
	path := filepath.Join(agentDir, "state.db")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("offline recovery requires an existing regular state.db")
	}
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("offline recovery requires the agent stopped and exclusive state access: %w", err)
	}
	s := newStore(db, agentDir)
	s.offlineRecovery = true
	if err := db.View(func(tx *bbolt.Tx) error {
		for _, b := range [][]byte{bucketMeta, bucketDesired, bucketActual, bucketStartup} {
			if tx.Bucket(b) == nil {
				return ErrExecutionCorrupt
			}
		}
		_, err := readExecutions(tx)
		if err != nil {
			return err
		}
		_, err = readIntents(tx, ReleasePartition{})
		return err
	}); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// RecoverySnapshot exposes only public metadata. The expectation digest binds
// the original intent, committed candidate, node and complete execution set.
type RecoverySnapshot struct {
	InstanceID  string            `json:"instanceId"`
	Candidate   ReleaseCandidate  `json:"candidate"`
	Expectation string            `json:"expectation"`
	Executions  []ExecutionRecord `json:"-"`
}

func recoverySnapshot(tx *bbolt.Tx, id string) (RecoverySnapshot, error) {
	var out RecoverySnapshot
	records, err := readExecutions(tx)
	if err != nil {
		return out, err
	}
	var target *ExecutionRecord
	for i := range records {
		if records[i].InstanceID == id {
			target = &records[i]
		}
	}
	if target == nil {
		return out, fmt.Errorf("recovery requires a recorded actual execution")
	}
	intent, err := readIntents(tx, target.Candidate.Partition)
	if err != nil {
		return out, err
	}
	if intent == nil || intent.Candidate != target.Candidate {
		return out, ErrReleaseUnresolved
	}
	high, found, err := readReleaseActivation(tx, target.Candidate.Partition)
	if err != nil {
		return out, err
	}
	if !found || high != intent.Candidate {
		return out, ErrReleaseConflict
	}
	node := tx.Bucket(bucketMeta).Get(keyNodeMeta)
	var meta NodeMeta
	if json.Unmarshal(node, &meta) != nil || meta.NodeID == "" {
		return out, ErrExecutionCorrupt
	}
	raw, err := json.Marshal(struct {
		Intent     *releaseIntent
		High       ReleaseCandidate
		Node       string
		Executions []ExecutionRecord
	}{intent, high, meta.NodeID, records})
	if err != nil {
		return out, err
	}
	return RecoverySnapshot{InstanceID: id, Candidate: high, Expectation: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), Executions: records}, nil
}

func (s *Store) InspectInterruptedExecution(id string) (RecoverySnapshot, error) {
	var out RecoverySnapshot
	if !s.offlineRecovery {
		return out, fmt.Errorf("recovery requires the offline store")
	}
	err := s.db.View(func(tx *bbolt.Tx) error { var err error; out, err = recoverySnapshot(tx, id); return err })
	return out, err
}

// RecoverInterruptedExecution is the explicit operator path for an interrupted
// execution of an ALREADY COMMITTED identical candidate. The trusted runtime
// callback must verify signed evidence and definite stopped Engine identities.
// This never runs from normal startup and changes only the exact bound intent.
func (s *Store) RecoverInterruptedExecution(ctx context.Context, id, expect string, verify func(context.Context, RecoverySnapshot) error) error {
	if !s.offlineRecovery || !digestID(expect) || verify == nil {
		return fmt.Errorf("offline recovery requires exact expectation and verifier")
	}
	snapshot, err := s.InspectInterruptedExecution(id)
	if err != nil {
		return err
	}
	if snapshot.Expectation != expect {
		return fmt.Errorf("recovery expectation changed")
	}
	if err := verify(ctx, snapshot); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		fresh, err := recoverySnapshot(tx, id)
		if err != nil {
			return err
		}
		if fresh.Expectation != expect {
			return fmt.Errorf("recovery expectation changed")
		}
		return tx.Bucket(bucketReleaseIntent).Delete([]byte(fresh.Candidate.Partition.key()))
	})
}
