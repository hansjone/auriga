package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	uidBase = 200000
	uidMax  = 299999
	uidFile = "instance-uids.json"
)

// UIDTable assigns persistent unique host UIDs per instance.
type UIDTable struct {
	Path string
	mu   sync.Mutex
}

type uidMap struct {
	Next  int               `json:"next"`
	ByID  map[string]int    `json:"byId"`
	ByUID map[string]string `json:"byUid"` // string key for JSON
}

// NewUIDTable stores allocations under agentDir.
func NewUIDTable(agentDir string) *UIDTable {
	return &UIDTable{Path: filepath.Join(agentDir, uidFile)}
}

// Allocate returns a stable uid for instanceID, rejecting collisions.
func (t *UIDTable) Allocate(instanceID string) (int, error) {
	if err := ValidateInstanceID(instanceID); err != nil {
		return 0, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m, err := t.load()
	if err != nil {
		return 0, err
	}
	if uid, ok := m.ByID[instanceID]; ok {
		return uid, nil
	}
	if m.Next < uidBase {
		m.Next = uidBase
	}
	for m.Next <= uidMax {
		uid := m.Next
		m.Next++
		key := fmt.Sprintf("%d", uid)
		if other, taken := m.ByUID[key]; taken && other != instanceID {
			continue
		}
		m.ByID[instanceID] = uid
		m.ByUID[key] = instanceID
		if err := t.save(m); err != nil {
			return 0, err
		}
		return uid, nil
	}
	return 0, fmt.Errorf("storage: uid pool exhausted")
}

// Release frees the uid for instanceID.
func (t *UIDTable) Release(instanceID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, err := t.load()
	if err != nil {
		return err
	}
	uid, ok := m.ByID[instanceID]
	if !ok {
		return nil
	}
	delete(m.ByID, instanceID)
	delete(m.ByUID, fmt.Sprintf("%d", uid))
	return t.save(m)
}

func (t *UIDTable) load() (*uidMap, error) {
	raw, err := os.ReadFile(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return &uidMap{Next: uidBase, ByID: map[string]int{}, ByUID: map[string]string{}}, nil
		}
		return nil, err
	}
	var m uidMap
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.ByID == nil {
		m.ByID = map[string]int{}
	}
	if m.ByUID == nil {
		m.ByUID = map[string]string{}
	}
	return &m, nil
}

func (t *UIDTable) save(m *uidMap) error {
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.Path)
}
