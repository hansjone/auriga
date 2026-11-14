package projection

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// Projector can only retrieve materials for its pinned installation. It has no
// KEK, PG, Docker, arbitrary command, or cross-component key interface.
type Projector struct {
	Root, Control, Origin string
	ControlSnapshot       string
	Schemas               *Schemas
	Trust                 *Trust
	Pin                   api.InstallationPin
	Signing               ed25519.PrivateKey
	Sealing               *ecdh.PrivateKey
	Client                *http.Client
	ReceiptClient         *http.Client
	ServiceTokenFile      string
	OnFailure             func(error, time.Duration)
}

func (p *Projector) request(ctx context.Context, method, path string, v any) ([]byte, error) {
	u, e := url.Parse(p.Origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, Fault("scope_denied")
	}
	var body []byte
	if v != nil {
		body, e = Bytes(v)
		if e != nil {
			return nil, e
		}
	}
	q, e := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(p.Origin, "/")+path, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Cache-Control", "no-store")
	client := p.Client
	if !strings.HasPrefix(path, "/api/collab/v2/network/bootstrap/") {
		if p.ReceiptClient != nil {
			client = p.ReceiptClient
		}
		if p.ServiceTokenFile != "" {
			token, e := readPrivateFile(p.ServiceTokenFile, 16384)
			if e != nil {
				return nil, e
			}
			q.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
			clear(token)
		}
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	local := *client
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return Fault("scope_denied") }
	res, e := local.Do(q)
	if e != nil {
		return nil, Fault("projection_pending")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusConflict {
			return nil, Fault("version_rollback")
		}
		return nil, Fault("projection_pending")
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 16*MaxFile+1))
	if e != nil || len(raw) > 16*MaxFile {
		return nil, Fault("invalid_chain")
	}
	return raw, nil
}
func signIdentity(v any, key ed25519.PrivateKey, kid, domain string) (api.Signature, error) {
	raw, e := Bytes(v)
	if e != nil {
		return api.Signature{}, e
	}
	var body map[string]json.RawMessage
	if e = json.Unmarshal(raw, &body); e != nil {
		return api.Signature{}, e
	}
	delete(body, "identity_auth")
	raw, e = Bytes(body)
	if e != nil {
		return api.Signature{}, e
	}
	return api.Signature{Alg: "Ed25519", Kid: kid, Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte(domain+"\x00"), raw...)))}, nil
}
func (p *Projector) ack(ctx context.Context, s api.LocalProjectionSelector) error {
	reply, e := Exchange(ctx, p.Control, p.Pin.Component, api.LocalProjectionRequest{Operation: "reload", Selector: s})
	if e != nil {
		return e
	}
	selected, phase, code, e := ResponseFields(reply)
	if e != nil {
		return e
	}
	if code != "none" {
		return Fault(code)
	}
	receipt := api.ProjectionReceipt{TenantId: s.TenantId, Realm: s.Realm, InstallationId: s.InstallationId, Component: s.Component, Nonce: s.Nonce, Generation: s.Generation, KeysetVersion: s.KeysetVersion, PublicationDigest: s.PublicationDigest, BundleVersion: s.BundleVersion, BundleDigest: s.BundleDigest, Phase: phase, ObservedAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), LocalSelector: &selected}
	// Persist exact signed bytes before send. A lost HTTP confirmation is retried
	// byte-for-byte rather than generating a different timestamp/signature.
	path := filepath.Join(p.Control, "projection-receipt.json")
	deliveredPath := filepath.Join(p.Control, "delivered-receipt.json")
	if delivered, e := readFile(deliveredPath, 65536); e == nil {
		var previous api.ProjectionReceipt
		if e = Decode(delivered, &previous, true); e != nil {
			return e
		}
		if previous.LocalSelector != nil && Equal(*previous.LocalSelector, s) && (previous.Phase == phase || (previous.Phase == "handshake_verified" && phase == "loaded")) {
			if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
				return e
			}
			return p.Trust.RecordLoaded(s, previous.Phase == "handshake_verified")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	var pending api.ProjectionReceipt
	raw, e := readFile(path, 65536)
	if e == nil {
		if e = Decode(raw, &pending, true); e != nil {
			return e
		}
		if pending.LocalSelector == nil || !Equal(*pending.LocalSelector, s) {
			return Fault("projection_pending")
		}
		receipt = pending
	} else if !os.IsNotExist(e) {
		return e
	} else {
		receipt.IdentityAuth, e = signIdentity(receipt, p.Signing, p.Pin.IdentityId, "Auriga.NetworkProjectionReceipt.v2")
		if e != nil {
			return e
		}
		raw, e = Bytes(receipt)
		if e != nil {
			return e
		}
		if e = atomicFile(path, raw); e != nil {
			return e
		}
	}
	if _, e = p.request(ctx, "POST", "/api/collab/v2/network/projections/receipts", receipt); e != nil {
		return e
	}
	if e = atomicFile(deliveredPath, raw); e != nil {
		return e
	}
	if e = os.Remove(path); e != nil {
		return e
	}
	if e = syncDir(p.Control); e != nil {
		return e
	}
	return p.Trust.RecordLoaded(s, receipt.Phase == "handshake_verified")
}
func (p *Projector) Reconcile(ctx context.Context) error {
	if p.Schemas == nil || p.Trust == nil || len(p.Signing) != ed25519.PrivateKeySize || p.Sealing == nil {
		return Fault("invalid_chain")
	}
	publication, bundle, e := p.publication(ctx)
	if e != nil {
		return e
	}
	pd, _ := ObjectDigest(publication.Keyset)
	bd, _ := ObjectDigest(bundle)
	// Finish a published generation and its acknowledgement before requesting any
	// new nonce. This keeps interrupted installs on their exact persisted version.
	if _, e := os.Lstat(filepath.Join(p.Root, "transaction.json")); e == nil {
		path := filepath.Join(p.Root, "transaction.json")
		raw, e := readPrivateFile(path, 180*MaxFile)
		if e != nil {
			return e
		}
		defer clear(raw)
		var pending Generation
		if e = Decode(raw, &pending, true); e != nil {
			return e
		}
		defer pending.Clear()
		if pending.Selector.PublicationDigest != pd || pending.Selector.BundleDigest != bd {
			if e = p.Trust.VerifyCandidate(publication, bundle); e != nil {
				return e
			}
			if lt(publication.Keyset.Payload.Generation, pending.Selector.Generation) || (publication.Keyset.Payload.Generation == pending.Selector.Generation && lt(publication.Keyset.Payload.KeysetVersion, pending.Selector.KeysetVersion)) {
				return Fault("version_rollback")
			}
			history, e := p.Trust.load(&pending, true)
			if history != nil {
				history.Clear()
			}
			if e != nil {
				return e
			}
			if e = p.rememberRevision(pending.Selector.MaterialRevision); e != nil {
				return e
			}
			if e = preserveFile(path, filepath.Join(p.Root, "transaction-history"), raw); e != nil {
				return e
			}
		} else {
			if e = Recover(p.Root, func(g *Generation) error {
				s, e := p.Trust.Load(g)
				if s != nil {
					s.Clear()
				}
				return e
			}); e != nil {
				return e
			}
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if current, e := ReadGeneration(p.Root); e == nil {
		defer current.Clear()
		acked, e := readFile(filepath.Join(p.Control, "acked-selector.json"), 16384)
		selector, _ := Bytes(current.Selector)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if current.Selector.PublicationDigest != pd || current.Selector.BundleDigest != bd {
			path := filepath.Join(p.Control, "projection-receipt.json")
			if raw, e := readPrivateFile(path, 65536); e == nil {
				var receipt api.ProjectionReceipt
				if e = Decode(raw, &receipt, true); e != nil {
					return e
				}
				if e = VerifyIdentity(receipt, receipt.IdentityAuth, "Auriga.NetworkProjectionReceipt.v2", p.Pin.SigningSpki); e != nil {
					return e
				}
				if e = preserveFile(path, filepath.Join(p.Control, "receipt-history"), raw); e != nil {
					return e
				}
			} else if !os.IsNotExist(e) {
				return e
			}
		} else if !bytes.Equal(acked, selector) {
			if e = p.ack(ctx, current.Selector); e != nil {
				return e
			}
			if e = atomicFile(filepath.Join(p.Control, "acked-selector.json"), selector); e != nil {
				return e
			}
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// The installation request is signed independently of service JWT/mTLS.
	nonce := make([]byte, 32)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	q := api.ProjectionRequest{TenantId: publication.Keyset.Payload.TenantId, Realm: publication.Keyset.Payload.Realm, InstallationId: p.Pin.IdentityId, Component: p.Pin.Component, GatewayId: p.Pin.GatewayId, Nonce: base64.RawURLEncoding.EncodeToString(nonce), ExpectedGeneration: publication.Keyset.Payload.Generation, ExpectedKeysetVersion: publication.Keyset.Payload.KeysetVersion}
	q.IdentityAuth, e = signIdentity(q, p.Signing, p.Pin.IdentityId, "Auriga.NetworkProjectionRequest.v2")
	if e != nil {
		return e
	}
	raw, e := p.request(ctx, "POST", "/api/collab/v2/network/bootstrap/materials", q)
	if e != nil {
		return e
	}
	var sealed api.SealedMaterial
	if e = p.Schemas.Decode("SealedMaterial", raw, &sealed); e != nil {
		return e
	}
	if sealed.TenantId != q.TenantId || sealed.Realm != q.Realm || sealed.InstallationId != q.InstallationId || sealed.Component != q.Component || sealed.Nonce != q.Nonce || sealed.Generation != q.ExpectedGeneration || sealed.KeysetVersion != q.ExpectedKeysetVersion || sealed.BundleDigest != bd {
		return Fault("scope_denied")
	}
	cleartext, e := OpenSealed(sealed, p.Sealing)
	if e != nil {
		return e
	}
	defer clear(cleartext)
	var m api.ProjectionMaterial
	if e = p.Schemas.Decode("ProjectionMaterial", cleartext, &m); e != nil {
		return e
	}
	if m.TenantId != q.TenantId || m.Realm != q.Realm || m.InstallationId != q.InstallationId || m.Component != q.Component || m.Nonce != q.Nonce || m.Generation != q.ExpectedGeneration || m.KeysetVersion != q.ExpectedKeysetVersion || !Equal(m.Bundle, bundle) {
		return Fault("scope_denied")
	}
	revision := uint64(0)
	high, e := readPrivateFile(filepath.Join(p.Control, "material-high-water.json"), 128)
	if e == nil {
		var s string
		if e = Decode(high, &s, true); e != nil {
			return e
		}
		revision, e = number(s)
		if e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	old, e := ReadGeneration(p.Root)
	if e == nil {
		defer old.Clear()
		previous, e := number(old.Selector.MaterialRevision)
		if e != nil {
			return e
		}
		if previous > revision {
			revision = previous
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if revision == ^uint64(0) {
		return Fault("version_rollback")
	}
	revision++
	g, e := BuildGeneration(m, publication, strconv.FormatUint(revision, 10))
	if e != nil {
		return e
	}
	defer g.Clear()
	if old != nil && Equal(g.Files, old.Files) {
		return p.ack(ctx, old.Selector)
	}
	b, e := Bytes(g.Selector)
	if e != nil {
		return e
	}
	if e = Publish(p.Root, g, func(g *Generation) error {
		snap, e := p.Trust.Load(g)
		if snap != nil {
			snap.Clear()
		}
		return e
	}); e != nil {
		return e
	}
	if e = p.ack(ctx, g.Selector); e != nil {
		return e
	}
	return atomicFile(filepath.Join(p.Control, "acked-selector.json"), b)
}

func preserveFile(path, history string, raw []byte) error {
	if e := privateDir(history); e != nil {
		return e
	}
	target := filepath.Join(history, Digest(raw)[7:]+".json")
	existing, e := readPrivateFile(target, len(raw))
	if e == nil {
		if !bytes.Equal(existing, raw) {
			return Fault("invalid_chain")
		}
	} else if os.IsNotExist(e) {
		if e = atomicFile(target, raw); e != nil {
			return e
		}
	} else {
		return e
	}
	if e = os.Remove(path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func (p *Projector) rememberRevision(revision string) error {
	path := filepath.Join(p.Control, "material-high-water.json")
	b, e := readPrivateFile(path, 128)
	if e == nil {
		var old string
		if e = Decode(b, &old, true); e != nil {
			return e
		}
		if !lt(old, revision) {
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	b, e = Bytes(revision)
	if e != nil {
		return e
	}
	return atomicFile(path, b)
}

func (p *Projector) publication(ctx context.Context) (api.Publication, api.SignedTransportTrustBundle, error) {
	var publication api.Publication
	var bundle api.SignedTransportTrustBundle
	raw, e := p.request(ctx, "GET", "/api/collab/v2/network/bootstrap/controls", nil)
	if e != nil {
		return publication, bundle, e
	}
	var control api.SignedRootControl
	if e = p.Schemas.Decode("SignedRootControl", raw, &control); e != nil {
		return publication, bundle, e
	}
	if e = p.Trust.ObserveControl(raw); e != nil {
		return publication, bundle, e
	}
	if p.ControlSnapshot != "" {
		if e = p.Trust.PublishObservedControl(control, p.ControlSnapshot); e != nil {
			return publication, bundle, e
		}
	}
	raw, e = p.request(ctx, "GET", "/api/collab/v2/network/bootstrap/keys", nil)
	if e != nil {
		return publication, bundle, e
	}
	if e = p.Schemas.Decode("Publication", raw, &publication); e != nil {
		return publication, bundle, e
	}
	raw, e = p.request(ctx, "GET", "/api/collab/v2/network/bootstrap/transport", nil)
	if e != nil {
		return publication, bundle, e
	}
	if e = p.Schemas.Decode("SignedTransportTrustBundle", raw, &bundle); e != nil {
		return publication, bundle, e
	}
	if e = p.Trust.VerifyCandidate(publication, bundle); e != nil {
		return publication, bundle, e
	}
	return publication, bundle, nil
}
func (p *Projector) Run(ctx context.Context) error {
	var failureSince time.Time
	for {
		if e := Retry(ctx, func() error {
			e := p.Reconcile(ctx)
			if e != nil {
				if failureSince.IsZero() {
					failureSince = time.Now()
				}
				if p.OnFailure != nil {
					p.OnFailure(e, time.Since(failureSince))
				}
			} else if !failureSince.IsZero() {
				if p.OnFailure != nil {
					p.OnFailure(nil, time.Since(failureSince))
				}
				failureSince = time.Time{}
			}
			return e
		}); e != nil {
			return e
		}
		t := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
