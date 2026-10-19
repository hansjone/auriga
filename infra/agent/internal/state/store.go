// Package state persists node metadata and instance desired/actual status (bbolt).
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

var (
	bucketMeta    = []byte("meta")
	bucketDesired = []byte("desired")
	bucketActual  = []byte("actual")
	keyNodeMeta   = []byte("node")
)

// Store is the local agent state database under <data>/agent/state.db.
type Store struct {
	db *bbolt.DB
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
		for _, name := range [][]byte{bucketMeta, bucketDesired, bucketActual} {
			if _, e := tx.CreateBucketIfNotExists(name); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
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
	if spec == nil || spec.InstanceId == "" {
		return fmt.Errorf("spec requires instance_id")
	}
	raw, err := proto.Marshal(spec)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketDesired).Put([]byte(spec.InstanceId), raw)
	})
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
