package projection

import (
	"slices"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// SelectorLedgerFacts refer to already verified, locally accepted signed
// snapshots. Authentication is supplied by the management transport, never JSON.
type SelectorLedgerFacts struct {
	Tenant, Realm string
	Now           time.Time
	Authenticated bool
}

func VerifyExpectedSelectors(r api.ExpectedSelectors, q api.ExpectedSelectorsQuery, bundle api.SignedTransportTrustBundle, control api.SignedRootControl, f SelectorLedgerFacts) error {
	if !f.Authenticated {
		return Fault("scope_denied")
	}
	b, c := bundle.Payload, control.Payload
	if r.TenantId != f.Tenant || q.TenantId != f.Tenant || b.TenantId != f.Tenant || c.TenantId != f.Tenant || r.Realm != f.Realm || q.Realm != f.Realm || b.Realm != f.Realm || c.Realm != f.Realm {
		return Fault("scope_denied")
	}
	bd, _ := ObjectDigest(bundle)
	if r.Generation != b.Generation || r.BundleVersion != b.BundleVersion || r.Phase != b.Phase || r.AuthorityId != b.AuthorityId || r.AuthorityDigest != b.AuthorityDigest || r.BundleDigest != bd || q.BundleDigest != bd || q.BundleVersion != b.BundleVersion {
		return Fault("invalid_chain")
	}
	cd, _ := ObjectDigest(control)
	if r.ControlRevision != c.ControlRevision || q.ControlRevision != c.ControlRevision || r.ControlDigest != cd || q.ControlDigest != cd {
		return Fault("control_required")
	}
	if r.Challenge != q.Challenge {
		return Fault("invalid_chain")
	}
	issued, e := time.Parse(time.RFC3339, r.IssuedAt)
	if e != nil || issued.After(f.Now) || f.Now.Sub(issued) > time.Minute {
		return Fault("projection_pending")
	}
	ids := slices.Clone(b.RequiredReaderIds)
	got := slices.Clone(r.RequiredReaderIds)
	slices.Sort(ids)
	slices.Sort(got)
	if !slices.Equal(ids, got) || len(ids) != len(r.ExpectedSelectors) || len(ids) != len(r.ReaderIdentities) {
		return Fault("invalid_chain")
	}
	for j, id := range ids {
		if j > 0 && id == ids[j-1] {
			return Fault("invalid_chain")
		}
		if _, ok := r.ExpectedSelectors[id]; !ok || r.ReaderIdentities[id] == "" {
			return Fault("invalid_chain")
		}
	}
	return nil
}
