package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"go.etcd.io/bbolt"
)

var restartBucket = []byte("restart_commands")

// ErrRestartConflict rejects reuse of a command ID for a different request.
var ErrRestartConflict = errors.New("restart command identity conflict")

// RestartRecord retains the stop checkpoint and completion across reconnects.
// Request is the original protobuf command, including its instance identity.
type RestartRecord struct {
	Request    []byte `json:"request"`
	Generation int64  `json:"generation"`
	Stopped    bool   `json:"stopped"`
	Complete   bool   `json:"complete"`
}

// GetRestartCommand reads an existing checkpoint without creating a journal.
func (s *Store) GetRestartCommand(id string) (*RestartRecord, error) {
	var record *RestartRecord
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(restartBucket)
		if bucket == nil || bucket.Get([]byte(id)) == nil {
			return nil
		}
		record = &RestartRecord{}
		if err := json.Unmarshal(bucket.Get([]byte(id)), record); err != nil {
			return err
		}
		return validRestartRecord(*record)
	})
	return record, err
}

func validRestartRecord(record RestartRecord) error {
	if len(record.Request) == 0 || record.Generation < 1 || record.Complete && !record.Stopped {
		return fmt.Errorf("invalid restart checkpoint")
	}
	return nil
}

// PutRestartCommand atomically advances a request-bound restart checkpoint.
func (s *Store) PutRestartCommand(id string, record RestartRecord) error {
	if id == "" {
		return fmt.Errorf("missing command ID")
	}
	if err := validRestartRecord(record); err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(restartBucket)
		if err != nil {
			return err
		}
		if previous := bucket.Get([]byte(id)); previous != nil {
			var old RestartRecord
			if err := json.Unmarshal(previous, &old); err != nil {
				return err
			}
			if !bytes.Equal(old.Request, record.Request) {
				return ErrRestartConflict
			}
			if old.Generation > record.Generation || old.Stopped && !record.Stopped || old.Complete && !record.Complete {
				return fmt.Errorf("restart checkpoint regression")
			}
		}
		return bucket.Put([]byte(id), raw)
	})
}
