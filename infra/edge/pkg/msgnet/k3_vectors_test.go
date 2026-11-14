package msgnet

import (
	"encoding/json"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// These structs describe only the shared fixture protocol. All verdicts come
// from the production methods used by the consumers, never case IDs or oracle JS.
type transitionVector struct {
	Root           string `json:"root_spki"`
	Tenant         string `json:"tenant_id"`
	Realm          string
	Latest         api.SignedRootControl `json:"latest_control"`
	Authority      api.SignedRuntimeAuthority
	Predecessor    api.TransportPredecessor
	Transport      api.SignedTransportTrustBundle
	SeenGeneration string `json:"seen_generation"`
	SeenVersion    string `json:"seen_bundle_version"`
	SeenDigest     string `json:"seen_bundle_digest"`
	Isolation      bool   `json:"isolation_enforced"`
}

func (v transitionVector) proof() api.TransportTransitionProof {
	return api.TransportTransitionProof{Control: v.Latest, Authority: v.Authority, Predecessor: v.Predecessor, FirstBundle: v.Transport}
}
func (v transitionVector) domain() TransportDomain {
	return TransportDomain{RootSPKI: v.Root, Tenant: v.Tenant, Realm: v.Realm}
}
func (v transitionVector) history() TransportHistory {
	return TransportHistory{Generation: v.SeenGeneration, Version: v.SeenVersion, Digest: v.SeenDigest, IsolationEnforced: v.Isolation}
}

type confirmationVector struct {
	Root                  string `json:"root_spki"`
	Tenant                string `json:"tenant_id"`
	Realm, Now, Challenge string
	RequiredPhase         string            `json:"required_phase"`
	Selectors             map[string]string `json:"expected_selectors"`
	Evidence              api.SignedTransportConfirmation
	Bundle                api.SignedTransportTrustBundle
	Authority             api.SignedRuntimeAuthority
	Control               api.SignedRootControl
}

func (v confirmationVector) stage() StageConfirmation {
	return StageConfirmation{Evidence: v.Evidence, Bundle: v.Bundle, Authority: v.Authority, Control: v.Control, Facts: ConfirmationFacts{Domain: TransportDomain{RootSPKI: v.Root, Tenant: v.Tenant, Realm: v.Realm}, Now: stamp(v.Now), Challenge: v.Challenge, RequiredPhase: v.RequiredPhase, ExpectedSelectors: v.Selectors}}
}

func k3Verdict(t *testing.T, operation string, raw map[string]json.RawMessage) error {
	t.Helper()
	data, e := json.Marshal(raw)
	if e != nil {
		t.Fatal(e)
	}
	decode := func(target any) {
		t.Helper()
		if e := json.Unmarshal(data, target); e != nil {
			t.Fatal(e)
		}
	}
	switch operation {
	case "transport_transition":
		var v transitionVector
		decode(&v)
		return VerifyTransportTransition(v.proof(), v.domain(), v.history())
	case "transport_confirmation":
		var v confirmationVector
		decode(&v)
		s := v.stage()
		return VerifyTransportConfirmation(s.Evidence, s.Bundle, s.Authority, s.Control, s.Facts)
	case "transport_catchup":
		var v struct {
			Transition   transitionVector
			Current      api.SignedTransportTrustBundle
			Control      api.SignedRootControl
			SeenVersion  string `json:"seen_version"`
			SeenDigest   string `json:"seen_digest"`
			Confirmation *confirmationVector
		}
		decode(&v)
		var stage *StageConfirmation
		if v.Confirmation != nil {
			s := v.Confirmation.stage()
			stage = &s
		}
		return VerifyTransportCatchup(v.Transition.proof(), v.Current, v.Control, v.Transition.domain(), v.Transition.history(), TransportHistory{Version: v.SeenVersion, Digest: v.SeenDigest}, stage)
	case "node_control_binding":
		var v struct {
			Pin         api.NodeControlPin
			Node        string `json:"authenticated_node_id"`
			Target      string `json:"target_id"`
			Identity    string `json:"identity_id"`
			Revision    string `json:"identity_revision"`
			Compromised bool
			Private     bool `json:"requests_private_material"`
			Enrolled    bool `json:"independent_enrollment"`
		}
		decode(&v)
		return VerifyNodeBinding(v.Pin, NodeBinding{AuthenticatedNodeID: v.Node, TargetID: v.Target, IdentityID: v.Identity, IdentityRevision: v.Revision, Compromised: v.Compromised, RequestsPrivateMaterial: v.Private, IndependentlyEnrolled: v.Enrolled})
	case "projection_access":
		var v struct {
			Component string
			Projector int  `json:"projector_uid"`
			Reader    int  `json:"reader_uid"`
			Peer      int  `json:"peer_uid"`
			Owner     int  `json:"owner_uid"`
			Directory int  `json:"directory_mode"`
			File      int  `json:"file_mode"`
			Private   bool `json:"component_private_mount"`
			Caps      bool `json:"cap_drop_all"`
		}
		decode(&v)
		return VerifyProjectionAccess(ProjectionAccess{Component: v.Component, ProjectorUID: v.Projector, ReaderUID: v.Reader, PeerUID: v.Peer, OwnerUID: v.Owner, DirectoryMode: v.Directory, FileMode: v.File, ComponentPrivateMount: v.Private, CapDropAll: v.Caps})
	default:
		t.Fatalf("unsupported production operation %s", operation)
		return nil
	}
}
