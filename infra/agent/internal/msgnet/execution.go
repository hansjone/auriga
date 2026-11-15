package msgnet

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// NodeExecution uses native node mTLS to the deployment-pinned proof relay.
// The relay maps that certificate to D's independent NodeControlPin, commits
// IAM's deny fence, verifies the broker disconnect, then confirms C's ledger.
// No service JWT, local shell command or DSH lifecycle operation is used here.
type NodeExecution struct {
	mu      sync.Mutex
	origin  *url.URL
	client  *http.Client
	schemas *trust.Schemas
	control *api.ControlPackage
}

func NewNodeExecution(origin string, transport *http.Transport, schemas *trust.Schemas) (*NodeExecution, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || schemas == nil || transport == nil || transport.Proxy != nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify || len(transport.TLSClientConfig.Certificates) == 0 && transport.TLSClientConfig.GetClientCertificate == nil {
		return nil, fmt.Errorf("fixed verified native-mTLS proof relay required")
	}
	tr := transport.Clone()
	// A renewed native certificate must be used even during continuous polling.
	tr.DisableKeepAlives = true
	if tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		tr.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return &NodeExecution{origin: u, schemas: schemas, client: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (n *NodeExecution) Close() { n.client.CloseIdleConnections() }

func (n *NodeExecution) read(ctx context.Context, path, definition string, body any, result any) error {
	u := *n.origin
	u.Path = "/api/collab/v2/network" + path
	var raw []byte
	var e error
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
		raw, e = Bytes(body)
		if e != nil {
			return e
		}
	}
	request, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(raw))
	if e != nil {
		return e
	}
	request.Header.Set("Cache-Control", "no-store")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := n.client.Do(request)
	if e != nil {
		return fmt.Errorf("native proof relay unavailable: %w", e)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || !bytes.Contains(bytes.ToLower([]byte(response.Header.Get("Cache-Control"))), []byte("no-store")) {
		return trust.Fault("projection_pending")
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, 32<<20+1))
	if e != nil {
		return e
	}
	if len(raw) > 32<<20 {
		return fmt.Errorf("oversize public proof")
	}
	return n.schemas.Decode(definition, raw, result)
}

func (n *NodeExecution) ResumeControl(p api.ControlPackage) {
	n.mu.Lock()
	defer n.mu.Unlock()
	copy := p
	n.control = &copy
}
func (n *NodeExecution) current() (api.ControlPackage, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.control == nil {
		return api.ControlPackage{}, fmt.Errorf("verified control required")
	}
	return *n.control, nil
}
func (n *NodeExecution) record(p api.ControlPackage) api.IsolationRecord {
	return api.IsolationRecord{TenantId: p.TenantId, Realm: p.Realm, TargetId: p.TargetId, MinimumControlRevision: p.Control.Payload.ControlRevision, ControlDigest: p.ControlDigest, Challenge: p.Challenge, State: "quarantined"}
}
func matchExecution(p api.ControlPackage, r api.IsolationRecord) error {
	if r.TenantId != p.TenantId || r.Realm != p.Realm || r.TargetId != p.TargetId || r.MinimumControlRevision != p.Control.Payload.ControlRevision || r.ControlDigest != p.ControlDigest || r.Challenge != p.Challenge {
		return fmt.Errorf("isolation execution does not match verified control")
	}
	return nil
}

func (n *NodeExecution) ApplyControl(ctx context.Context, p api.ControlPackage) error {
	n.ResumeControl(p)
	var result api.IsolationRecord
	if e := n.read(ctx, "/isolation", "IsolationRecord", n.record(p), &result); e != nil {
		return e
	}
	if result.IdentityCompromised {
		return trust.Fault("identity_quarantined")
	}
	actual, actualError := number(result.MinimumControlRevision)
	requested, requestedError := number(p.Control.Payload.ControlRevision)
	if actualError == nil && requestedError == nil && actual > requested && result.TenantId == p.TenantId && result.Realm == p.Realm && result.TargetId == p.TargetId && result.State != "ready" && !result.NewChainReady {
		// C retained an older outstanding challenge while the server already
		// enforces a stronger control. This receipt drains only that audit item.
		return nil
	}
	return matchExecution(p, result)
}

// Isolated re-executes the authenticated server-side fence/disconnect check.
// The result is bound to the installed package, not a remotely supplied boolean.
func (n *NodeExecution) Isolated(ctx context.Context) (bool, error) {
	p, e := n.current()
	if e != nil {
		return false, e
	}
	var record api.IsolationRecord
	if e = n.read(ctx, "/isolation/"+p.TargetId, "IsolationRecord", nil, &record); e != nil {
		return false, e
	}
	if e = matchExecution(p, record); e != nil {
		return false, e
	}
	return record.State != "ready" && !record.NewChainReady, nil
}

func (n *NodeExecution) ApplyTransport(ctx context.Context, b api.SignedTransportTrustBundle) error {
	p, e := n.current()
	if e != nil {
		return e
	}
	if b.Payload.TenantId != p.TenantId || b.Payload.Realm != p.Realm {
		return fmt.Errorf("node transport scope mismatch")
	}
	request := n.record(p)
	request.State = "ready"
	request.NewChainReady = true
	var record api.IsolationRecord
	if e = n.read(ctx, "/isolation", "IsolationRecord", request, &record); e != nil {
		return e
	}
	if e = matchExecution(p, record); e != nil {
		return e
	}
	if record.State != "ready" || !record.NewChainReady || record.IdentityCompromised {
		return trust.Fault("identity_quarantined")
	}
	return nil
}

func (n *NodeExecution) Predecessor(ctx context.Context, digest string) (api.TransportPredecessor, error) {
	var result api.TransportPredecessor
	if len(digest) != 71 || digest[:7] != "sha256:" {
		return result, fmt.Errorf("invalid history digest")
	}
	e := n.read(ctx, "/transport/history/"+digest, "TransportPredecessor", nil, &result)
	return result, e
}
func (n *NodeExecution) Transition(ctx context.Context, digest string) (api.TransportTransitionProof, error) {
	var result api.TransportTransitionProof
	if len(digest) != 71 || digest[:7] != "sha256:" {
		return result, fmt.Errorf("invalid authority digest")
	}
	e := n.read(ctx, "/transport/transitions/"+digest, "TransportTransitionProof", nil, &result)
	return result, e
}
func (n *NodeExecution) Confirmation(ctx context.Context, query api.TransportConfirmationQuery) (api.SignedTransportConfirmation, error) {
	var result api.SignedTransportConfirmation
	e := n.read(ctx, "/transport/confirmation", "SignedTransportConfirmation", query, &result)
	return result, e
}

func (n *NodeExecution) ResolveAuthority(ctx context.Context, id, digest string) (api.SignedRuntimeAuthority, error) {
	var publication api.Publication
	if e := n.read(ctx, "/bootstrap/keys", "Publication", nil, &publication); e != nil {
		return publication.Authority, e
	}
	actual, e := trust.ObjectDigest(publication.Authority)
	if e != nil || actual != digest || publication.Authority.Payload.AuthorityId != id {
		return api.SignedRuntimeAuthority{}, fmt.Errorf("proof relay authority mismatch")
	}
	return publication.Authority, nil
}
