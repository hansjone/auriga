package projection

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// TransportSource reads only the fixed K3 endpoints through an authenticated
// HTTPS client. Selectors must be supplied from C's independent material ledger;
// an absent provider fails closed rather than copying the evidence rows.
type TransportSource struct {
	Schemas                     *Schemas
	Root, Tenant, Realm, Target string
	Request                     func(context.Context, string, string, any) ([]byte, error)
	ExpectedSelectors           func(context.Context, api.SignedTransportTrustBundle) (map[string]string, error)
	Now                         func() time.Time
}

func (s *TransportSource) Bind(ctx context.Context, trust *Trust) {
	trust.SetTransportConfirmation(func(p api.Publication, b api.SignedTransportTrustBundle, c api.SignedRootControl, previous *api.SignedTransportTrustBundle, now time.Time) error {
		return s.Verify(ctx, p, b, c, previous, now)
	})
}

func (s *TransportSource) read(ctx context.Context, path, name string, out any) error {
	raw, e := s.Request(ctx, "GET", path, nil)
	if e != nil {
		return e
	}
	return s.Schemas.Decode(name, raw, out)
}

func (s *TransportSource) confirm(ctx context.Context, current api.SignedTransportTrustBundle, a api.SignedRuntimeAuthority, c api.SignedRootControl) error {
	if current.Payload.Phase == "dual_trust" {
		return nil
	}
	var history api.TransportPredecessor
	if e := s.read(ctx, "/api/collab/v2/network/transport/history/"+url.PathEscape(current.Payload.PreviousBundleDigest), "TransportPredecessor", &history); e != nil {
		return pendingStatus(e)
	}
	required := "loaded"
	if current.Payload.Phase == "retire_old" {
		required = "handshake_verified"
	}
	if e := CheckTransportPredecessor(current, history.Bundle, required); e != nil {
		return e
	}
	if !Equal(history.Authority, a) || !Equal(current.Payload.Scope, history.Bundle.Payload.Scope) || !Equal(current.Payload.RequiredReaderIds, history.Bundle.Payload.RequiredReaderIds) || current.Payload.Generation != history.Bundle.Payload.Generation || !lt(history.Bundle.Payload.BundleVersion, current.Payload.BundleVersion) {
		return Fault("invalid_transition")
	}
	if current.Payload.Phase == "new_leaves" && !Equal(current.Payload.Cas, history.Bundle.Payload.Cas) {
		return Fault("invalid_transition")
	}
	if current.Payload.Phase == "retire_old" {
		for _, ca := range current.Payload.Cas {
			if !slices.ContainsFunc(history.Bundle.Payload.Cas, func(old api.TransportCa) bool { return Equal(ca, old) }) {
				return Fault("invalid_transition")
			}
		}
	}
	if e := VerifySignature(history.Control.Payload, history.Control.RootAuth, "Auriga.NetworkRootControl.v2", s.Root); e != nil {
		return e
	}
	if history.Control.Payload.TenantId != s.Tenant || history.Control.Payload.Realm != s.Realm || !allowed(history.Control.Payload, a) || slices.Contains(history.Control.Payload.RevokedAuthorityIds, a.Payload.AuthorityId) || lt(history.Control.Payload.ControlRevision, history.Bundle.Payload.ControlRevision) {
		return Fault("invalid_chain")
	}
	nonce := make([]byte, 32)
	if _, e := rand.Read(nonce); e != nil {
		return e
	}
	d, _ := ObjectDigest(history.Bundle)
	q := api.TransportConfirmationQuery{TenantId: s.Tenant, Realm: s.Realm, BundleVersion: history.Bundle.Payload.BundleVersion, BundleDigest: d, Challenge: base64.RawURLEncoding.EncodeToString(nonce)}
	raw, e := s.Request(ctx, "POST", "/api/collab/v2/network/transport/confirmation", q)
	if e != nil {
		return pendingStatus(e)
	}
	var evidence api.SignedTransportConfirmation
	if e = s.Schemas.Decode("SignedTransportConfirmation", raw, &evidence); e != nil {
		return e
	}
	if s.ExpectedSelectors == nil {
		return Fault("projection_pending")
	}
	expected, e := s.ExpectedSelectors(ctx, history.Bundle)
	if e != nil {
		return e
	}
	observed := time.Now()
	if s.Now != nil {
		observed = s.Now()
	}
	return VerifyTransportConfirmation(evidence, history.Bundle, a, c, ConfirmationFacts{Root: s.Root, Tenant: s.Tenant, Realm: s.Realm, Challenge: q.Challenge, RequiredPhase: required, Now: observed, ExpectedSelectors: expected})
}

func (s *TransportSource) Verify(ctx context.Context, p api.Publication, b api.SignedTransportTrustBundle, c api.SignedRootControl, previous *api.SignedTransportTrustBundle, now time.Time) error {
	if s.Schemas == nil || s.Request == nil {
		return Fault("projection_pending")
	}
	if b.Payload.Generation != p.Keyset.Payload.Generation {
		return Fault("scope_denied")
	}
	// VerifyTransport also enforces the authority's transport scope and signing
	// kid. Phase and high-water checks below use the independent bundle sequence.
	if e := VerifyTransport(b, p, TransportFacts{ControlRevision: c.Payload.ControlRevision, ReadersPersisted: true, HandshakesVerified: true}); e != nil {
		return e
	}
	if stamp(b.Payload.IssuedAt).After(now.Add(time.Minute)) || stamp(p.Authority.Payload.NotBefore).After(now) {
		return Fault("invalid_transition")
	}
	seenVersion, seenDigest := "", ""
	if previous != nil {
		seenDigest, _ = ObjectDigest(*previous)
		if lt(b.Payload.Generation, previous.Payload.Generation) {
			return Fault("version_rollback")
		}
		if b.Payload.Generation == previous.Payload.Generation {
			seenVersion = previous.Payload.BundleVersion
			if lt(b.Payload.BundleVersion, seenVersion) {
				return Fault("version_rollback")
			}
			d, _ := ObjectDigest(b)
			if b.Payload.BundleVersion == seenVersion && d != seenDigest {
				return Fault("same_version_conflict")
			}
		}
	}
	changed := previous != nil && (previous.Payload.AuthorityDigest != b.Payload.AuthorityDigest || previous.Payload.Generation != b.Payload.Generation)
	anchored := c.Payload.TransportTransition != nil && c.Payload.TransportTransition.AuthorityDigest == b.Payload.AuthorityDigest
	if previous == nil || changed || anchored {
		var proof api.TransportTransitionProof
		e := s.read(ctx, "/api/collab/v2/network/transport/transitions/"+url.PathEscape(b.Payload.AuthorityDigest), "TransportTransitionProof", &proof)
		if e == nil {
			f := TransportTransitionFacts{Root: s.Root, Tenant: s.Tenant, Realm: s.Realm}
			// Once the first snapshot has been accepted, use its historical start
			// without pretending the current local snapshot was the predecessor.
			if previous != nil && changed {
				f.SeenGeneration = previous.Payload.Generation
				f.SeenVersion = previous.Payload.BundleVersion
				f.SeenDigest = seenDigest
			}
			if proof.Control.Payload.TransportTransition != nil && proof.Control.Payload.TransportTransition.Mode == "emergency_recovery" {
				var isolation api.IsolationRecord
				if e = s.read(ctx, "/api/collab/v2/network/isolation/"+url.PathEscape(s.Target), "IsolationRecord", &isolation); e != nil {
					return e
				}
				f.IsolationEnforced = isolation.TenantId == s.Tenant && isolation.Realm == s.Realm && isolation.TargetId == s.Target && isolation.State == "isolated" && !lt(isolation.MinimumControlRevision, c.Payload.ControlRevision)
			}
			if !Equal(proof.Authority, p.Authority) {
				return Fault("invalid_chain")
			}
			return VerifyTransportCatchup(proof, b, c, f, seenVersion, seenDigest, func() error { return s.confirm(ctx, b, p.Authority, c) })
		}
		if changed || anchored || !isNotFound(e) {
			return pendingStatus(e)
		}
	}
	if previous != nil && !Equal(*previous, b) {
		if previous.Payload.Phase == b.Payload.Phase {
			// Refreshing leaves within a stage still requires its original gate.
			if !Equal(previous.Payload.Scope, b.Payload.Scope) || !Equal(previous.Payload.RequiredReaderIds, b.Payload.RequiredReaderIds) || !Equal(previous.Payload.Cas, b.Payload.Cas) {
				return Fault("invalid_transition")
			}
		} else if e := VerifyTransport(b, p, TransportFacts{ControlRevision: c.Payload.ControlRevision, SeenVersion: previous.Payload.BundleVersion, PreviousPhase: previous.Payload.Phase, Previous: previous, ReadersPersisted: true, HandshakesVerified: true}); e != nil {
			return e
		}
	}
	return s.confirm(ctx, b, p.Authority, c)
}

type responseStatus int

func (s responseStatus) Error() string { return "projection: authority response unavailable" }
func isNotFound(e error) bool          { v, ok := e.(responseStatus); return ok && v == 404 }
func pendingStatus(e error) error {
	if isNotFound(e) {
		return Fault("projection_pending")
	}
	return e
}
