package msgnet

import (
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func exactReaderKeys[V any](values map[string]V, ids []string) bool {
	if len(values) != len(ids) {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, ok := values[id]; !ok || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// VerifyExpectedSelectors binds a separately authenticated C ledger snapshot to
// the exact accepted root and bundle. It never derives authorization from proof.
// Callers must schema-check the wire response before this semantic check.
func VerifyExpectedSelectors(response api.ExpectedSelectors, query api.ExpectedSelectorsQuery, bundle api.SignedTransportTrustBundle, control api.SignedRootControl, domain TransportDomain, now time.Time, authenticated bool) error {
	if !authenticated {
		return Fault("scope_denied")
	}
	r, b, c := response, bundle.Payload, control.Payload
	for _, scope := range [][2]string{{r.TenantId, r.Realm}, {b.TenantId, b.Realm}, {c.TenantId, c.Realm}, {query.TenantId, query.Realm}} {
		if scope[0] != domain.Tenant || scope[1] != domain.Realm {
			return Fault("scope_denied")
		}
	}
	if r.Generation != b.Generation || r.BundleVersion != b.BundleVersion || r.Phase != b.Phase || r.AuthorityId != b.AuthorityId || r.AuthorityDigest != b.AuthorityDigest {
		return Fault("invalid_chain")
	}
	bd, err := ObjectDigest(bundle)
	if err != nil || r.BundleDigest != bd || query.BundleDigest != bd || query.BundleVersion != b.BundleVersion {
		return Fault("invalid_chain")
	}
	cd, err := ObjectDigest(control)
	if err != nil || r.ControlRevision != c.ControlRevision || query.ControlRevision != c.ControlRevision || r.ControlDigest != cd || query.ControlDigest != cd {
		return Fault("control_required")
	}
	if r.Challenge != query.Challenge {
		return Fault("invalid_chain")
	}
	issued, err := time.Parse(time.RFC3339, r.IssuedAt)
	if err != nil || issued.After(now) || now.Sub(issued) > time.Minute {
		return Fault("projection_pending")
	}
	ids, required := slices.Clone(r.RequiredReaderIds), slices.Clone(b.RequiredReaderIds)
	slices.Sort(ids)
	slices.Sort(required)
	if !slices.Equal(ids, required) || !exactReaderKeys(r.ExpectedSelectors, required) || !exactReaderKeys(r.ReaderIdentities, required) {
		return Fault("invalid_chain")
	}
	return nil
}
