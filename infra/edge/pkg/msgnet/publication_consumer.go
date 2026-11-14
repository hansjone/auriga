package msgnet

import (
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"time"
)

// PublicationState preserves first-publication facts and terminal key identities
// across outages; a new signed predecessor cannot replace already known facts.
type PublicationState struct {
	Publication *api.Publication                `json:"publication,omitempty"`
	Keys        map[string]api.PublicKey        `json:"keys"`
	First       map[string]api.FirstPublication `json:"first"`
}

func AcceptPublication(p api.Publication, current api.SignedRootControl, domain TransportDomain, rootKid string, now time.Time, previous PublicationState) (PublicationState, error) {
	fail := func(e error) (PublicationState, error) { return previous, e }
	if rootKid == "" || p.Control.RootAuth.Kid != rootKid || p.Authority.RootAuth.Kid != rootKid || current.RootAuth.Kid != rootKid {
		return fail(Fault("scope_denied"))
	}
	if p.Predecessor != nil && (p.Predecessor.Control.RootAuth.Kid != rootKid || p.Predecessor.Authority.RootAuth.Kid != rootKid) {
		return fail(Fault("scope_denied"))
	}
	f := Facts{RootSPKI: domain.RootSPKI, Tenant: domain.Tenant, Realm: domain.Realm, Now: now, Latest: current, SeenControl: current.Payload.ControlRevision}
	if old := previous.Publication; old != nil {
		f.SeenGeneration = old.Keyset.Payload.Generation
		f.SeenVersion = old.Keyset.Payload.KeysetVersion
		f.SeenDigest, _ = ObjectDigest(old.Keyset)
		if q := p.Predecessor; q != nil && q.Keyset.Payload.Generation == f.SeenGeneration && q.Keyset.Payload.KeysetVersion == f.SeenVersion {
			f.PreviousDigest = f.SeenDigest
		}
	}
	for _, v := range previous.First {
		f.First = append(f.First, v)
	}
	if e := VerifyPublication(p, f); e != nil {
		return fail(e)
	}
	if p.Keyset.Payload.Mode == "bootstrap" && previous.Publication != nil && !Equal(*previous.Publication, p) {
		return fail(Fault("invalid_transition"))
	}
	next := PublicationState{Publication: &p, Keys: map[string]api.PublicKey{}, First: map[string]api.FirstPublication{}}
	for id, v := range previous.Keys {
		next.Keys[id] = v
	}
	for id, v := range previous.First {
		next.First[id] = v
	}
	for _, key := range p.Keyset.Payload.Keys {
		if old, ok := next.Keys[key.Kid]; ok && (old.Purpose != key.Purpose || old.PublicKeySpki != key.PublicKeySpki || old.NotBefore != key.NotBefore || old.NotAfter != key.NotAfter || (old.State == "retired" || old.State == "revoked") && old.State != key.State) {
			return fail(Fault("invalid_transition"))
		}
		for id, v := range next.Keys {
			if id != key.Kid && v.PublicKeySpki == key.PublicKeySpki {
				return fail(Fault("invalid_transition"))
			}
		}
		next.Keys[key.Kid] = key
	}
	if p.Predecessor != nil {
		for _, v := range p.Predecessor.Keyset.Payload.FirstPublications {
			next.First[v.Kid] = v
		}
	}
	for _, v := range p.Keyset.Payload.FirstPublications {
		next.First[v.Kid] = v
	}
	return next, nil
}
