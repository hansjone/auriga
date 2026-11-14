// Package msgnet installs independent root controls before issuing node receipts.
package msgnet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/identity"
	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// Backend must install the control fence durably and disconnect affected message
// sessions without stopping DSH or NodeControl. Failure never emits a receipt.
// All calls must honor their context; the control transport uses bounded workers.
type Backend interface {
	ApplyControl(context.Context, api.ControlPackage) error
	ApplyTransport(context.Context, api.SignedTransportTrustBundle) error
}
type Options struct {
	Directory, Tenant, Realm, RootKid, NodeId string
	RootKey                                   ed25519.PublicKey
	Pin                                       api.NodeControlPin
	SigningKey                                ed25519.PrivateKey
	Backend                                   Backend
	ResolveAuthority                          func(context.Context, string, string) (api.SignedRuntimeAuthority, error)
	Schemas                                   *trust.Schemas
	TransportSource                           trust.TransportSource
	Now                                       func() time.Time
}
type state struct {
	IdentityId       string                          `json:"identity_id"`
	IdentityRevision string                          `json:"identity_revision"`
	NodeId           string                          `json:"node_id"`
	SigningSPKI      string                          `json:"signing_spki,omitempty"`
	Package          api.ControlPackage              `json:"package"`
	Receipt          *api.InstallationReceipt        `json:"receipt,omitempty"`
	Bundle           *api.SignedTransportTrustBundle `json:"bundle,omitempty"`
	Transport        trust.TransportState            `json:"transport"`
}
type Installer struct {
	options     Options
	mu          sync.Mutex
	state       *state
	replacement bool
}

func New(o Options) (*Installer, error) {
	if !filepath.IsAbs(o.Directory) || o.Tenant == "" || o.Realm == "" || o.RootKid == "" || len(o.RootKey) != 32 || len(o.SigningKey) != 64 || o.Backend == nil {
		return nil, fmt.Errorf("incomplete independent control installation")
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(o.Pin.SigningSpki)
	if e != nil {
		return nil, e
	}
	public, e := x509.ParsePKIXPublicKey(raw)
	if e != nil {
		return nil, e
	}
	ed, ok := public.(ed25519.PublicKey)
	if !ok || !bytes.Equal(ed, o.SigningKey.Public().(ed25519.PublicKey)) {
		return nil, fmt.Errorf("installation signing key does not match pin")
	}
	canonical, _ := x509.MarshalPKIXPublicKey(ed)
	if !bytes.Equal(canonical, raw) || base64.RawURLEncoding.EncodeToString(raw) != o.Pin.SigningSpki {
		return nil, fmt.Errorf("noncanonical pin key")
	}
	if o.NodeId == "" || o.Pin.NodeId != o.NodeId || o.Pin.IdentityId == "" || o.Pin.TargetId == "" || len(o.Pin.IdentityId) > 128 || len(o.Pin.TargetId) > 128 || len(o.NodeId) > 128 || len(o.Pin.AdditionalProperties) != 0 {
		return nil, fmt.Errorf("node control pin does not match authenticated node")
	}
	if _, e = number(o.Pin.IdentityRevision); e != nil {
		return nil, e
	}
	if bytes.Equal(o.RootKey, ed) {
		return nil, fmt.Errorf("node confirmation key reuses root role")
	}
	for p := o.Directory; ; p = filepath.Dir(p) {
		if info, e := os.Lstat(p); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("linked state directory")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if e = identity.EnsureDir(o.Directory); e != nil {
		return nil, e
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	installer := &Installer{options: o}
	path := filepath.Join(o.Directory, "control-state.json")
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Size() > 32<<20 {
			return nil, fmt.Errorf("invalid control journal")
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		var saved state
		if e = strict(raw, &saved); e != nil {
			return nil, e
		}
		if e = installer.control(saved.Package); e != nil {
			return nil, e
		}
		if saved.IdentityId == "" || saved.NodeId != o.NodeId {
			return nil, fmt.Errorf("missing journal identity")
		}
		oldRevision, e := number(saved.IdentityRevision)
		newRevision, _ := number(o.Pin.IdentityRevision)
		if e != nil || newRevision < oldRevision || saved.IdentityId != o.Pin.IdentityId && newRevision <= oldRevision || saved.IdentityId == o.Pin.IdentityId && newRevision != oldRevision {
			return nil, fmt.Errorf("node identity revision rollback or reuse")
		}
		if saved.Receipt != nil && saved.Receipt.IdentityId != saved.IdentityId {
			return nil, fmt.Errorf("receipt identity does not match journal")
		}
		if saved.IdentityId != o.Pin.IdentityId {
			if saved.SigningSPKI != "" && saved.SigningSPKI == o.Pin.SigningSpki {
				return nil, fmt.Errorf("replacement identity reuses the previous confirmation key")
			}
			installer.replacement = true
			saved.Receipt = nil
		} else if saved.SigningSPKI != "" && saved.SigningSPKI != o.Pin.SigningSpki {
			return nil, fmt.Errorf("node confirmation key changed without replacement identity")
		}
		if saved.Receipt != nil {
			if e = installer.receipt(*saved.Receipt, saved.Package); e != nil {
				return nil, e
			}
		}
		installer.state = &saved
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return installer, nil
}

func strict(raw []byte, target any) error {
	canonical, e := Canonical(raw)
	if e != nil || !bytes.Equal(canonical, raw) {
		return fmt.Errorf("noncanonical control JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(target); e != nil {
		return e
	}
	if !knownFields(reflect.ValueOf(target)) {
		return fmt.Errorf("unknown signed control fields")
	}
	encoded, e := Bytes(target)
	if e != nil || !bytes.Equal(encoded, raw) {
		return fmt.Errorf("unknown control fields")
	}
	return nil
}

// The generated models implement UnmarshalJSON and collect unknown keys rather
// than letting json.Decoder reject them. Frozen signed models permit none.
func knownFields(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		return v.IsNil() || knownFields(v.Elem())
	}
	switch v.Kind() {
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			if v.Type().Field(n).Name == "AdditionalProperties" && v.Field(n).Len() != 0 {
				return false
			}
			if !knownFields(v.Field(n)) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for n := 0; n < v.Len(); n++ {
			if !knownFields(v.Index(n)) {
				return false
			}
		}
	}
	return true
}
func verify(key ed25519.PublicKey, kid, domain string, payload any, auth api.Signature) error {
	signature, e := base64.RawURLEncoding.Strict().DecodeString(auth.Signature)
	if e != nil || auth.Alg != "Ed25519" || auth.Kid != kid || len(signature) != 64 || base64.RawURLEncoding.EncodeToString(signature) != auth.Signature {
		return fmt.Errorf("invalid identity signature")
	}
	raw, e := Bytes(payload)
	if e != nil {
		return e
	}
	input := append([]byte(domain+"\x00"), raw...)
	if !ed25519.Verify(key, input, signature) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
func (i *Installer) control(p api.ControlPackage) error {
	o := i.options
	c := p.Control.Payload
	if fmt.Sprint(c.SchemaVersion) != "2" || len(p.PackageId) == 0 || len(p.PackageId) > 128 || p.TenantId != o.Tenant || p.Realm != o.Realm || p.TargetId != o.Pin.TargetId || c.TenantId != o.Tenant || c.Realm != o.Realm {
		return fmt.Errorf("control scope denied")
	}
	if len(c.AllowedAuthorities) > 32 || len(c.RevokedAuthorityIds) > 65536 || len(c.RevokedInstallationIds) > 65536 {
		return fmt.Errorf("control set too large")
	}
	if timestamp, e := time.Parse(time.RFC3339, c.IssuedAt); e != nil || timestamp.UTC().Format(time.RFC3339) != c.IssuedAt {
		return fmt.Errorf("invalid control timestamp")
	}
	if e := verify(o.RootKey, o.RootKid, "Auriga.NetworkRootControl.v2", c, p.Control.RootAuth); e != nil {
		return e
	}
	for _, n := range []string{c.ControlRevision, c.MinimumGeneration, c.MinimumKeysetVersion} {
		if _, e := number(n); e != nil {
			return e
		}
	}
	raw, e := Bytes(p.Control)
	if e != nil || Digest(raw) != p.ControlDigest {
		return fmt.Errorf("control digest mismatch")
	}
	challenge, e := base64.RawURLEncoding.Strict().DecodeString(p.Challenge)
	if e != nil || len(challenge) < 32 || base64.RawURLEncoding.EncodeToString(challenge) != p.Challenge {
		return fmt.Errorf("invalid control challenge")
	}
	if i.state != nil {
		previous := i.state.Package.Control.Payload
		revision, _ := number(c.ControlRevision)
		old, _ := number(previous.ControlRevision)
		if revision < old || (i.replacement && revision <= old) {
			return fmt.Errorf("control rollback")
		}
		if revision == old && !Equal(p, i.state.Package) {
			return fmt.Errorf("control conflict")
		}
		for _, id := range previous.RevokedAuthorityIds {
			if !slices.Contains(c.RevokedAuthorityIds, id) {
				return fmt.Errorf("revocation removed")
			}
		}
		for _, id := range previous.RevokedInstallationIds {
			if !slices.Contains(c.RevokedInstallationIds, id) {
				return fmt.Errorf("installation revocation removed")
			}
		}
		generation, _ := number(c.MinimumGeneration)
		before, _ := number(previous.MinimumGeneration)
		version, _ := number(c.MinimumKeysetVersion)
		prior, _ := number(previous.MinimumKeysetVersion)
		if generation < before || (generation == before && version < prior) {
			return fmt.Errorf("control minimum rollback")
		}
	}
	return nil
}
func (i *Installer) persist(s *state) error {
	raw, e := Bytes(s)
	if e != nil {
		return e
	}
	return identity.WritePrivateSnapshot(filepath.Join(i.options.Directory, "control-state.json"), raw)
}

func (i *Installer) InstallControl(ctx context.Context, raw []byte) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(raw) > 32<<20 {
		return nil, fmt.Errorf("control too large")
	}
	var p api.ControlPackage
	if e := strict(raw, &p); e != nil {
		return nil, e
	}
	if e := i.control(p); e != nil {
		return nil, e
	}
	// Persist the fence before touching runtime state. An interrupted application
	// retries this exact package; it cannot roll back to a less restrictive root.
	if i.state != nil && i.state.Receipt != nil && Equal(p, i.state.Package) && !slices.Contains(p.Control.Payload.RevokedInstallationIds, i.options.Pin.IdentityId) {
		// Reapply after a node restart: a prior receipt is not proof of live isolation.
		if e := i.options.Backend.ApplyControl(ctx, p); e != nil {
			return nil, e
		}
		return Bytes(i.state.Receipt)
	}
	next := &state{Package: p, IdentityId: i.options.Pin.IdentityId, IdentityRevision: i.options.Pin.IdentityRevision, NodeId: i.options.NodeId, SigningSPKI: i.options.Pin.SigningSpki}
	if i.state != nil {
		next.Bundle = i.state.Bundle
		next.Transport = i.state.Transport
	}
	next.Transport.Control = &p.Control
	if e := i.persist(next); e != nil {
		return nil, e
	}
	i.state = next
	i.replacement = false
	if e := i.options.Backend.ApplyControl(ctx, p); e != nil {
		return nil, e
	}
	if e := trust.VerifyNodeBinding(i.options.Pin, trust.NodeBinding{AuthenticatedNodeID: i.options.NodeId, TargetID: p.TargetId, IdentityID: i.options.Pin.IdentityId, IdentityRevision: i.options.Pin.IdentityRevision, Compromised: slices.Contains(p.Control.Payload.RevokedInstallationIds, i.options.Pin.IdentityId), IndependentlyEnrolled: true}); e != nil {
		return nil, e
	}
	receipt := api.InstallationReceipt{PackageId: p.PackageId, TenantId: p.TenantId, Realm: p.Realm, TargetId: p.TargetId, IdentityId: i.options.Pin.IdentityId, Challenge: p.Challenge, ControlRevision: p.Control.Payload.ControlRevision, ControlDigest: p.ControlDigest, Result: "installed", PersistedAt: i.options.Now().UTC().Truncate(time.Second).Format(time.RFC3339)}
	encoded, e := Bytes(receipt)
	if e != nil {
		return nil, e
	}
	var body map[string]any
	_ = json.Unmarshal(encoded, &body)
	delete(body, "identity_auth")
	encoded, e = Bytes(body)
	if e != nil {
		return nil, e
	}
	signature := ed25519.Sign(i.options.SigningKey, append([]byte("Auriga.NetworkControlReceipt.v2\x00"), encoded...))
	receipt.IdentityAuth = api.Signature{Alg: "Ed25519", Kid: i.options.Pin.IdentityId, Signature: base64.RawURLEncoding.EncodeToString(signature)}
	next.Receipt = &receipt
	if e = i.persist(next); e != nil {
		next.Receipt = nil
		return nil, e
	}
	return Bytes(receipt)
}

func (i *Installer) receipt(r api.InstallationReceipt, p api.ControlPackage) error {
	if r.PackageId != p.PackageId || r.TenantId != p.TenantId || r.Realm != p.Realm || r.TargetId != p.TargetId || r.IdentityId != i.options.Pin.IdentityId || r.Challenge != p.Challenge || r.ControlRevision != p.Control.Payload.ControlRevision || r.ControlDigest != p.ControlDigest || r.Result != "installed" {
		return fmt.Errorf("receipt journal binding mismatch")
	}
	raw, e := Bytes(r)
	if e != nil {
		return e
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	delete(body, "identity_auth")
	return verify(i.options.SigningKey.Public().(ed25519.PublicKey), i.options.Pin.IdentityId, "Auriga.NetworkControlReceipt.v2", body, r.IdentityAuth)
}
