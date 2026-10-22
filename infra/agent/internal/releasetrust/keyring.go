// Package releasetrust implements the runtime endorsement v1 trust graph.
// It is deliberately independent from resource/plugin signing trust.
package releasetrust

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrUntrusted = errors.New("runtime release trust unavailable; protected local bootstrap required")
var kidPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// Timestamp retains the exact protobuf seconds/nanos, including original expiry.
type Timestamp struct {
	Seconds int64 `json:"seconds"`
	Nanos   int32 `json:"nanos"`
}

func FromTime(t time.Time) Timestamp { return Timestamp{t.Unix(), int32(t.Nanosecond())} }
func (t Timestamp) Time() time.Time  { return time.Unix(t.Seconds, int64(t.Nanos)).UTC() }
func (t Timestamp) valid() bool {
	return (&timestamppb.Timestamp{Seconds: t.Seconds, Nanos: t.Nanos}).CheckValid() == nil
}

// Record is immutable once accepted. Deadline is a local bound, not a signed expiry.
type Record struct {
	Kid         string    `json:"kid"`
	PublicKey   []byte    `json:"public_key"`
	NotAfter    Timestamp `json:"not_after"`
	Parent      string    `json:"parent"`
	Endorsement []byte    `json:"endorsement"`
	Source      string    `json:"source"`
	Deadline    Timestamp `json:"deadline"`
}

type Ring struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

func (r Ring) Clone() Ring {
	out := Ring{Version: r.Version, Records: make([]Record, len(r.Records))}
	for i, k := range r.Records {
		out.Records[i] = k
		out.Records[i].PublicKey = bytes.Clone(k.PublicKey)
		out.Records[i].Endorsement = bytes.Clone(k.Endorsement)
	}
	return out
}

func record(k *nodev1.ArtifactSigningKey) (Record, error) {
	if k == nil || len(k.ProtoReflect().GetUnknown()) != 0 {
		return Record{}, fmt.Errorf("release key missing or has unknown protobuf fields")
	}
	if k.NotAfter == nil || len(k.NotAfter.ProtoReflect().GetUnknown()) != 0 || k.NotAfter.CheckValid() != nil {
		return Record{}, fmt.Errorf("release key has invalid expiry")
	}
	r := Record{Kid: k.Kid, PublicKey: bytes.Clone(k.PublicKey), NotAfter: Timestamp{k.NotAfter.Seconds, k.NotAfter.Nanos}, Parent: k.EndorsedByKid, Endorsement: bytes.Clone(k.Endorsement), Source: "endorsement"}
	if r.Parent == "" {
		r.Source = "bootstrap"
	}
	if err := r.shape(); err != nil {
		return Record{}, err
	}
	return r, nil
}

func (r Record) shape() error {
	if !kidPattern.MatchString(r.Kid) || len(r.PublicKey) != ed25519.PublicKeySize || !r.NotAfter.valid() {
		return fmt.Errorf("invalid release key identity or expiry")
	}
	if r.Parent == "" {
		if len(r.Endorsement) != 0 || r.Source != "bootstrap" {
			return fmt.Errorf("invalid release root endorsement")
		}
	} else if !kidPattern.MatchString(r.Parent) || len(r.Endorsement) != ed25519.SignatureSize || r.Source != "endorsement" {
		return fmt.Errorf("invalid release key endorsement")
	}
	return nil
}

func same(a, b Record) bool {
	return a.Kid == b.Kid && bytes.Equal(a.PublicKey, b.PublicKey) && a.NotAfter == b.NotAfter && a.Parent == b.Parent && bytes.Equal(a.Endorsement, b.Endorsement) && a.Source == b.Source
}
func verify(child, parent Record) bool {
	msg := append([]byte(child.Kid), child.PublicKey...)
	return ed25519.Verify(parent.PublicKey, msg, child.Endorsement)
}

// Validate checks persisted structure without reviving or discarding expired history.
func (r Ring) Validate() error {
	if r.Version != 1 || len(r.Records) == 0 {
		return ErrUntrusted
	}
	all := make(map[string]Record, len(r.Records))
	roots := 0
	for _, k := range r.Records {
		if err := k.shape(); err != nil {
			return err
		}
		if _, ok := all[k.Kid]; ok {
			return fmt.Errorf("duplicate persisted release kid")
		}
		if !k.Deadline.valid() || k.Deadline.Time().After(k.NotAfter.Time()) {
			return fmt.Errorf("invalid persisted release deadline")
		}
		all[k.Kid] = k
		if k.Parent == "" {
			roots++
		}
	}
	if roots == 0 {
		return ErrUntrusted
	}
	seen := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] == 1 {
			return fmt.Errorf("release endorsement cycle")
		}
		if seen[id] == 2 {
			return nil
		}
		seen[id] = 1
		k := all[id]
		if k.Parent != "" {
			p, ok := all[k.Parent]
			if !ok {
				return fmt.Errorf("unknown persisted release parent")
			}
			if err := visit(p.Kid); err != nil {
				return err
			}
			if k.Deadline.Time().After(p.Deadline.Time()) || !verify(k, p) {
				return fmt.Errorf("invalid persisted release endorsement or bound")
			}
		}
		seen[id] = 2
		return nil
	}
	for id := range all {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// Merge verifies the complete candidate graph before returning a new independent ring.
// Callers persisting a ring must execute this against the latest ring inside their write transaction.
func Merge(current Ring, keys []*nodev1.ArtifactSigningKey, bootstrap bool, now time.Time) (Ring, error) {
	if !FromTime(now).valid() {
		return Ring{}, fmt.Errorf("invalid release trust clock")
	}
	if bootstrap {
		if len(current.Records) != 0 {
			return Ring{}, fmt.Errorf("release roots already initialized")
		}
	} else if err := current.Validate(); err != nil {
		return Ring{}, err
	}
	all := map[string]Record{}
	for _, k := range current.Clone().Records {
		all[k.Kid] = k
	}
	incoming := map[string]Record{}
	for _, key := range keys {
		k, err := record(key)
		if err != nil {
			return Ring{}, err
		}
		if _, ok := incoming[k.Kid]; ok {
			return Ring{}, fmt.Errorf("duplicate incoming release kid")
		}
		if !now.Before(k.NotAfter.Time()) {
			return Ring{}, fmt.Errorf("expired incoming release key")
		}
		if old, ok := all[k.Kid]; ok {
			if !same(old, k) {
				return Ring{}, fmt.Errorf("immutable release kid conflict")
			}
			k = old
		} else if k.Parent == "" && !bootstrap {
			return Ring{}, fmt.Errorf("renew cannot add release root")
		}
		incoming[k.Kid] = k
	}
	seen := map[string]uint8{}
	var visit func(string) (Record, error)
	visit = func(id string) (Record, error) {
		k, changed := incoming[id]
		if !changed {
			var ok bool
			k, ok = all[id]
			if !ok {
				return Record{}, fmt.Errorf("unknown release endorsement parent")
			}
			if !now.Before(k.Deadline.Time()) {
				return Record{}, fmt.Errorf("expired release parent")
			}
			return k, nil
		}
		if seen[id] == 1 {
			return Record{}, fmt.Errorf("release endorsement cycle")
		}
		if seen[id] == 2 {
			return all[id], nil
		}
		seen[id] = 1
		deadline := k.NotAfter
		if k.Parent != "" {
			p, err := visit(k.Parent)
			if err != nil {
				return Record{}, err
			}
			if !verify(k, p) {
				return Record{}, fmt.Errorf("invalid release endorsement signature")
			}
			if p.Deadline.Time().Before(deadline.Time()) {
				deadline = p.Deadline
			}
		}
		if old, ok := all[id]; ok {
			deadline = old.Deadline
		}
		if !now.Before(deadline.Time()) {
			return Record{}, fmt.Errorf("expired release key local bound")
		}
		k.Deadline = deadline
		all[id] = k
		seen[id] = 2
		return k, nil
	}
	for id := range incoming {
		if _, err := visit(id); err != nil {
			return Ring{}, err
		}
	}
	out := Ring{Version: 1}
	for _, k := range all {
		out.Records = append(out.Records, k)
	}
	sort.Slice(out.Records, func(i, j int) bool { return out.Records[i].Kid < out.Records[j].Kid })
	if err := out.Validate(); err != nil {
		return Ring{}, err
	}
	return out, nil
}

// Lookup always rechecks time and returns a copy, even for a cached ring.
func (r Ring) Lookup(kid string, now time.Time) (ed25519.PublicKey, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !FromTime(now).valid() {
		return nil, fmt.Errorf("invalid release trust clock")
	}
	for _, k := range r.Records {
		if k.Kid == kid {
			if !now.Before(k.Deadline.Time()) {
				return nil, fmt.Errorf("release key expired")
			}
			return bytes.Clone(k.PublicKey), nil
		}
	}
	return nil, fmt.Errorf("release kid is not trusted")
}

// RootDigest binds the fixed initial public roots to the separate initialization guard.
func (r Ring) RootDigest() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	var roots []Record
	for _, k := range r.Records {
		if k.Parent == "" {
			roots = append(roots, k)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Kid < roots[j].Kid })
	raw, err := json.Marshal(roots)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
