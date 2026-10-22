package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"go.etcd.io/bbolt"
)

var bucketReleaseTrust = []byte("release_trust_v1")
var keyReleaseTrust = []byte("ring")

type releaseState struct {
	Version int                 `json:"version"`
	Phase   string              `json:"phase"`
	Guard   identity.TrustGuard `json:"guard"`
	Ring    releasetrust.Ring   `json:"ring"`
}

func readRelease(tx *bbolt.Tx) (releaseState, error) {
	var out releaseState
	b := tx.Bucket(bucketReleaseTrust)
	if b == nil {
		return out, releasetrust.ErrUntrusted
	}
	raw := b.Get(keyReleaseTrust)
	if raw == nil {
		return out, releasetrust.ErrUntrusted
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid persisted release trust: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return out, fmt.Errorf("trailing persisted release trust data")
	}
	if out.Version != 1 || (out.Phase != "pending" && out.Phase != "active") {
		return out, fmt.Errorf("invalid release initialization state")
	}
	if out.Guard.Trusted {
		if err := out.Ring.Validate(); err != nil {
			return out, err
		}
		digest, err := out.Ring.RootDigest()
		if err != nil || digest != out.Guard.Roots {
			return out, fmt.Errorf("release roots differ from initialization")
		}
	} else if len(out.Ring.Records) != 0 {
		return out, fmt.Errorf("untrusted enrollment contains release keys")
	}
	return out, nil
}

func writeRelease(tx *bbolt.Tx, r releaseState) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b, err := tx.CreateBucketIfNotExists(bucketReleaseTrust)
	if err != nil {
		return err
	}
	return b.Put(keyReleaseTrust, raw)
}

func (s *Store) validateBinding(tx *bbolt.Tx, r releaseState, requireActive bool) error {
	guard, err := identity.ReadTrustGuard(s.agentDir)
	if err != nil {
		return fmt.Errorf("release trust guard unavailable: %w", err)
	}
	if guard != r.Guard {
		return fmt.Errorf("release trust guard/database mismatch")
	}
	if requireActive && r.Phase != "active" {
		return fmt.Errorf("release initialization incomplete; protected local recovery required")
	}
	mat, err := identity.Load(s.agentDir)
	if err != nil {
		return fmt.Errorf("release identity unavailable: %w", err)
	}
	binding, err := identity.TrustBinding(mat)
	if err != nil {
		return err
	}
	if binding != guard.Binding {
		return fmt.Errorf("release identity binding mismatch")
	}
	var meta NodeMeta
	if raw := tx.Bucket(bucketMeta).Get(keyNodeMeta); raw == nil {
		return fmt.Errorf("release node metadata missing")
	} else if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("release node metadata invalid: %w", err)
	}
	if meta.NodeID != mat.NodeID {
		return fmt.Errorf("release node metadata identity mismatch")
	}
	if r.Phase == "pending" && identity.CertificateDigest(mat.CertificatePEM) != guard.Certificate {
		return fmt.Errorf("pending certificate binding mismatch")
	}
	return nil
}

// BeginReleaseInitialization commits node metadata and the complete initial ring
// together, before the independent identity files are published.
func (s *Store) BeginReleaseInitialization(meta NodeMeta, guard identity.TrustGuard, ring releasetrust.Ring) error {
	actual, err := identity.ReadTrustGuard(s.agentDir)
	if err != nil {
		return err
	}
	if actual != guard {
		return fmt.Errorf("initialization guard changed")
	}
	if guard.Trusted {
		if err := ring.Validate(); err != nil {
			return err
		}
		digest, err := ring.RootDigest()
		if err != nil || digest != guard.Roots {
			return fmt.Errorf("initial roots binding mismatch")
		}
	} else if len(ring.Records) != 0 {
		return fmt.Errorf("plaintext bootstrap cannot establish roots")
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if tx.Bucket(bucketReleaseTrust) != nil || tx.Bucket(bucketMeta).Get(keyNodeMeta) != nil {
			return fmt.Errorf("existing initialization cannot be replaced")
		}
		if err := tx.Bucket(bucketMeta).Put(keyNodeMeta, raw); err != nil {
			return err
		}
		return writeRelease(tx, releaseState{Version: 1, Phase: "pending", Guard: guard, Ring: ring.Clone()})
	})
}

// CompleteReleaseInitialization requires all local identity files to match the
// committed binding. No RPC or new trust bootstrap can repair an incomplete save.
func (s *Store) CompleteReleaseInitialization() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		r, err := readRelease(tx)
		if err != nil {
			return err
		}
		if err := s.validateBinding(tx, r, false); err != nil {
			return err
		}
		r.Phase = "active"
		return writeRelease(tx, r)
	})
}

func (s *Store) GetReleaseTrust() (releasetrust.Ring, error) {
	var out releasetrust.Ring
	err := s.db.View(func(tx *bbolt.Tx) error {
		r, err := readRelease(tx)
		if err != nil {
			return err
		}
		if err := s.validateBinding(tx, r, true); err != nil {
			return err
		}
		if !r.Guard.Trusted {
			return releasetrust.ErrUntrusted
		}
		out = r.Ring.Clone()
		return nil
	})
	return out, err
}

// MergeReleaseKeys reloads and validates inside the bbolt write transaction so
// concurrent connections cannot replace one another's accepted keys.
func (s *Store) MergeReleaseKeys(keys []*nodev1.ArtifactSigningKey, now time.Time) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		r, err := readRelease(tx)
		if err != nil {
			return err
		}
		if err := s.validateBinding(tx, r, true); err != nil {
			return err
		}
		if !r.Guard.Trusted {
			if len(keys) == 0 {
				return nil
			}
			return releasetrust.ErrUntrusted
		}
		r.Ring, err = releasetrust.Merge(r.Ring, keys, false, now)
		if err != nil {
			return err
		}
		return writeRelease(tx, r)
	})
}
