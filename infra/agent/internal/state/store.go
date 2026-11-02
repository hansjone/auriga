// Package state persists node metadata and instance desired/actual status (bbolt).
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/telemetry"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

var (
	bucketMeta    = []byte("meta")
	bucketDesired = []byte("desired")
	bucketActual  = []byte("actual")
	bucketStartup = []byte("startup-observation")
	keyNodeMeta   = []byte("node")
)

// Store is the local agent state database under <data>/agent/state.db.
type Store struct {
	db                *bbolt.DB
	agentDir          string
	activationMu      sync.Mutex
	activationClosing bool
	activationStop    chan struct{}
	activationDone    chan struct{}
	activationActive  sync.WaitGroup
	activationLocks   [4]chan struct{}
	closeErr          error
}

// Open creates or opens state.db under agentDir.
func Open(agentDir string) (*Store, error) {
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(agentDir, "state.db")
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{bucketMeta, bucketDesired, bucketActual, bucketStartup} {
			if _, e := tx.CreateBucketIfNotExists(name); e != nil {
				return e
			}
		}
		if _, err := readExecutions(tx); err != nil {
			return err
		}
		if _, err := readIntents(tx, ReleasePartition{}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, agentDir: agentDir, activationStop: make(chan struct{}), activationDone: make(chan struct{})}
	for i := range s.activationLocks {
		s.activationLocks[i] = make(chan struct{}, 1)
		s.activationLocks[i] <- struct{}{}
	}
	return s, nil
}

// Close rejects new activation qualifications, wakes queued callers and waits
// for qualified callbacks to return before closing the database. A callback must
// not call Close on its own Store: Close necessarily waits for that callback.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.activationMu.Lock()
	if s.activationClosing {
		s.activationMu.Unlock()
		<-s.activationDone
		return s.closeErr
	}
	s.activationClosing = true
	close(s.activationStop)
	s.activationMu.Unlock()
	s.activationActive.Wait()
	s.closeErr = s.db.Close()
	close(s.activationDone)
	return s.closeErr
}

// NodeMeta is node-level metadata kept alongside cert files.
type NodeMeta struct {
	NodeID          string    `json:"node_id"`
	ControlEndpoint string    `json:"control_endpoint"`
	ExpiresAt       time.Time `json:"expires_at"`
	CAFingerprint   string    `json:"ca_fingerprint"`
	AgentVersion    string    `json:"agent_version,omitempty"`
}

// PutNodeMeta writes node metadata.
func (s *Store) PutNodeMeta(m NodeMeta) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(keyNodeMeta, raw)
	})
}

// GetNodeMeta reads node metadata.
func (s *Store) GetNodeMeta() (NodeMeta, error) {
	var m NodeMeta
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucketMeta).Get(keyNodeMeta)
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &m)
	})
	return m, err
}

// PutDesired stores the desired InstanceSpec for an instance.
func (s *Store) PutDesired(spec *nodev1.InstanceSpec) error {
	return s.PutDesiredWithTrace(spec, "")
}

// PutDesiredWithTrace commits the spec and its optional startup cause atomically.
// A replay cannot resurrect a completed operation or replace its cause.
func (s *Store) PutDesiredWithTrace(spec *nodev1.InstanceSpec, parent string) error {
	if spec == nil || spec.InstanceId == "" {
		return fmt.Errorf("spec requires instance_id")
	}
	raw, err := proto.Marshal(spec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		var previous startupObservation
		_ = json.Unmarshal(tx.Bucket(bucketStartup).Get([]byte(spec.InstanceId)), &previous)
		if previous.Complete && previous.Generation > previous.CompletedGeneration {
			previous.CompletedGeneration = previous.Generation
		}
		if !telemetry.Valid(parent) {
			parent = ""
		}
		// The desired write below retains its existing business semantics. Only
		// this independent observation record is monotonic across receiver overlap.
		changed := spec.Generation > previous.Generation
		if changed {
			previous = startupObservation{Generation: spec.Generation, Parent: parent, CompletedGeneration: previous.CompletedGeneration, Complete: spec.Generation <= previous.CompletedGeneration}
		} else if spec.Generation == previous.Generation && !previous.Complete && parent != "" && (!telemetry.Valid(previous.Parent) || previous.Parent[3:35] == parent[3:35]) {
			// A redelivery is a new dispatch span of the SAME operation. A different
			// trace cannot rebind a generation or resurrect a completed startup.
			previous.Parent = parent
			changed = true
		}
		if changed {
			observation, encodeErr := json.Marshal(previous)
			if encodeErr != nil {
				return encodeErr
			}
			if putErr := tx.Bucket(bucketStartup).Put([]byte(spec.InstanceId), observation); putErr != nil {
				return putErr
			}
		}
		return tx.Bucket(bucketDesired).Put([]byte(spec.InstanceId), raw)
	})
}

type startupObservation struct {
	Generation          int64  `json:"generation"`
	Parent              string `json:"parent,omitempty"`
	Complete            bool   `json:"complete,omitempty"`
	CompletedGeneration int64  `json:"completed_generation,omitempty"`
}

func (s *Store) StartupParent(instanceID string, generation int64) string {
	var observation startupObservation
	_ = s.db.View(func(tx *bbolt.Tx) error {
		return json.Unmarshal(tx.Bucket(bucketStartup).Get([]byte(instanceID)), &observation)
	})
	if observation.Generation == generation && !observation.Complete && generation > observation.CompletedGeneration && telemetry.Valid(observation.Parent) {
		return observation.Parent
	}
	return ""
}

func (s *Store) StartupComplete(instanceID string, generation int64) bool {
	var observation startupObservation
	_ = s.db.View(func(tx *bbolt.Tx) error {
		return json.Unmarshal(tx.Bucket(bucketStartup).Get([]byte(instanceID)), &observation)
	})
	return generation > 0 && (generation <= observation.CompletedGeneration || observation.Generation == generation && observation.Complete)
}

func (s *Store) completeStartup(tx *bbolt.Tx, status *nodev1.InstanceStatus) error {
	if status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_STOPPED {
		var desired nodev1.InstanceSpec
		if proto.Unmarshal(tx.Bucket(bucketDesired).Get([]byte(status.InstanceId)), &desired) == nil && desired.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING {
			return nil // An authorized restart's stopped checkpoint is not a terminal startup.
		}
	}
	terminal := status.Ready && status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_STOPPED || status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED && status.GetError().GetAttempts() >= 5
	if !terminal {
		return nil
	}
	var observation startupObservation
	rawObservation := tx.Bucket(bucketStartup).Get([]byte(status.InstanceId))
	if len(rawObservation) > 0 && json.Unmarshal(rawObservation, &observation) != nil || status.ObservedGeneration < 1 {
		return nil
	}
	// Completion is itself an observed high-water mark, even if the corresponding
	// desired observation was absent (for example an older on-disk record).
	if status.ObservedGeneration > observation.Generation {
		observation.Generation = status.ObservedGeneration
		observation.Parent = ""
	}
	if status.ObservedGeneration > observation.CompletedGeneration {
		observation.CompletedGeneration = status.ObservedGeneration
	}
	if observation.Generation == status.ObservedGeneration {
		observation.Complete = true
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	return tx.Bucket(bucketStartup).Put([]byte(status.InstanceId), raw)
}

// GetDesired loads a desired spec.
func (s *Store) GetDesired(instanceID string) (*nodev1.InstanceSpec, error) {
	var out *nodev1.InstanceSpec
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucketDesired).Get([]byte(instanceID))
		if raw == nil {
			return nil
		}
		out = &nodev1.InstanceSpec{}
		return proto.Unmarshal(raw, out)
	})
	return out, err
}

// DeleteDesired removes desired + actual for an instance.
func (s *Store) DeleteDesired(instanceID string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		_ = tx.Bucket(bucketDesired).Delete([]byte(instanceID))
		_ = tx.Bucket(bucketActual).Delete([]byte(instanceID))
		_ = tx.Bucket(bucketStartup).Delete([]byte(instanceID))
		return nil
	})
}

// ListDesiredIDs returns all instance ids with a desired spec.
func (s *Store) ListDesiredIDs() ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketDesired).ForEach(func(k, _ []byte) error {
			ids = append(ids, string(k))
			return nil
		})
	})
	return ids, err
}

// ActualRecord is persisted observed status.
type ActualRecord struct {
	Status   *nodev1.InstanceStatus `json:"status"`
	Attempts uint32                 `json:"attempts"`
}

// PutActual stores observed status.
func (s *Store) PutActual(rec ActualRecord) error {
	if rec.Status == nil || rec.Status.InstanceId == "" {
		return fmt.Errorf("status requires instance_id")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.completeStartup(tx, rec.Status); err != nil {
			return err
		}
		return tx.Bucket(bucketActual).Put([]byte(rec.Status.InstanceId), raw)
	})
}

// GetActual loads observed status.
func (s *Store) GetActual(instanceID string) (ActualRecord, error) {
	var rec ActualRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucketActual).Get([]byte(instanceID))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &rec)
	})
	return rec, err
}

// ListActual returns all actual records.
func (s *Store) ListActual() ([]ActualRecord, error) {
	var out []ActualRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketActual).ForEach(func(_, v []byte) error {
			var rec ActualRecord
			if e := json.Unmarshal(v, &rec); e != nil {
				return e
			}
			out = append(out, rec)
			return nil
		})
	})
	return out, err
}
