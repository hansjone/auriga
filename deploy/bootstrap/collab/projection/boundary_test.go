package projection

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestIssuerVectors(t *testing.T) {
	var fixtureData struct {
		Issuer []struct {
			ID, Expected string
			Input        struct {
				Clock struct {
					Skew float64 `json:"pg_skew"`
					Wall float64 `json:"wall_delta"`
					Mono float64 `json:"monotonic_delta"`
				}
				First  bool `json:"write_first_publication"`
				Error  bool `json:"clock_error_persisted"`
				Stable bool `json:"stable_samples"`
			}
		} `json:"issuer_cases"`
	}
	fixture(t, "auto-renewal-v2-vectors.json", &fixtureData)
	for _, v := range fixtureData.Issuer {
		t.Run(v.ID, func(t *testing.T) {
			i := v.Input
			if got := verdict(CheckClock(i.Clock.Skew, i.Clock.Wall, i.Clock.Mono, i.First, i.Error, i.Stable)); got != v.Expected {
				t.Fatalf("%s want %s", got, v.Expected)
			}
		})
	}
}
func TestBoundaryVectors(t *testing.T) {
	schemas, e := LoadSchemas(filepath.Join("../../../..", "contracts/network/auto-renewal-v2.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		Boundary []struct {
			ID, Expected, Operation string
			Input                   map[string]json.RawMessage
		} `json:"boundary_cases"`
	}
	fixture(t, "auto-renewal-v2-vectors.json", &data)
	for _, v := range data.Boundary {
		t.Run(v.ID, func(t *testing.T) {
			i := v.Input
			decode := func(key string, out any) {
				t.Helper()
				if e := json.Unmarshal(i[key], out); e != nil {
					t.Fatalf("%s: %v", key, e)
				}
			}
			text := func(key string) string { var s string; decode(key, &s); return s }
			boolean := func(key string) bool {
				var b bool
				if i[key] != nil {
					decode(key, &b)
				}
				return b
			}
			numeric := func(key string) float64 { var n float64; decode(key, &n); return n }
			var err error
			switch v.Operation {
			case "signature_domain":
				var signed struct {
					Payload json.RawMessage
					Root    *api.Signature `json:"root_auth"`
					Runtime *api.Signature `json:"runtime_auth"`
				}
				decode("signed", &signed)
				auth := signed.Root
				if auth == nil {
					auth = signed.Runtime
				}
				if auth == nil {
					t.Fatal("missing signature")
				}
				err = VerifySignature(signed.Payload, *auth, text("verification_domain"), text("verification_spki"))
			case "announcement":
				var context struct {
					Root           string `json:"root_spki"`
					Tenant         string `json:"tenant_id"`
					Realm, Now     string
					Latest         api.SignedRootControl  `json:"latest_control"`
					SeenControl    string                 `json:"seen_control_revision"`
					SeenGeneration string                 `json:"seen_generation"`
					SeenVersion    string                 `json:"seen_version"`
					SeenDigest     string                 `json:"seen_digest"`
					PreviousDigest string                 `json:"previous_digest"`
					First          []api.FirstPublication `json:"first_publication_ledger"`
					Publication    api.Publication
				}
				decode("context", &context)
				f := Facts{RootSPKI: context.Root, Tenant: context.Tenant, Realm: context.Realm, Now: stamp(context.Now), Latest: context.Latest, SeenControl: context.SeenControl, SeenGeneration: context.SeenGeneration, SeenVersion: context.SeenVersion, SeenDigest: context.SeenDigest, PreviousDigest: context.PreviousDigest, First: context.First}
				err = VerifyPublication(context.Publication, f)
				if err == nil {
					var n api.SignedKeysetAnnouncementV2
					decode("announcement", &n)
					err = VerifyAnnouncement(n, context.Publication, boolean("persisted"))
				}
			case "control_receipt":
				var q api.ControlPackage
				var r api.InstallationReceipt
				decode("request", &q)
				decode("receipt", &r)
				err = VerifyControlReceipt(q, r, text("identity_spki"), text("identity_id"), boolean("persisted"), boolean("used_challenge"), boolean("exact_replay"), boolean("identity_compromised"), boolean("new_chain_ready"))
			case "projection":
				var q api.ProjectionRequest
				var m api.ProjectionMaterial
				var purposes []string
				decode("request", &q)
				decode("response", &m)
				decode("exported_purposes", &purposes)
				err = VerifyProjection(q, m, text("identity_spki"), boolean("used_nonce"), purposes)
			case "hpke":
				var s api.SealedMaterial
				decode("sealed", &s)
				seed := sha256.Sum256([]byte("Auriga synthetic HPKE test only receiver"))
				key, e := ecdh.X25519().NewPrivateKey(seed[:])
				if e != nil {
					t.Fatal(e)
				}
				plain, e := OpenSealed(s, key)
				err = e
				if e == nil {
					expected, e := Canonical(i["expected_plaintext"])
					if e != nil {
						t.Fatal(e)
					}
					if string(plain) != string(expected) {
						err = Fault("invalid_chain")
					} else {
						var m api.ProjectionMaterial
						decode("expected_plaintext", &m)
						if s.TenantId != m.TenantId || s.Realm != m.Realm || s.InstallationId != m.InstallationId || s.Component != m.Component || s.Nonce != m.Nonce || s.Generation != m.Generation || s.KeysetVersion != m.KeysetVersion {
							err = Fault("scope_denied")
						}
					}
				}
				clear(plain)
			case "migration":
				var p api.SignedMigrationPermit
				var members []api.MigrationCommitmentMember
				decode("permit", &p)
				decode("members", &members)
				err = VerifyMigration(p, text("root_spki"), members, text("current_version"), text("current_digest"), text("first_v2_version"), numeric("frozen_elapsed_seconds"), numeric("remaining_seconds"), boolean("barrier_committed"), boolean("legacy_disabled"))
			case "archive":
				var removed, reappeared, tombs []api.Tombstone
				decode("removed", &removed)
				decode("reappeared", &reappeared)
				decode("tombstones", &tombs)
				err = VerifyArchive(text("issued_at"), text("before"), numeric("grace_seconds"), removed, reappeared, tombs)
			case "config":
				err = CheckConfig(schemas, i["config"])
			case "transport_transition":
				proof, facts := transitionInput(t, i)
				err = VerifyTransportTransition(proof, facts)
			case "transport_confirmation":
				err = confirmationInput(t, i)
			case "transport_catchup":
				var transition map[string]json.RawMessage
				decode("transition", &transition)
				proof, facts := transitionInput(t, transition)
				var current api.SignedTransportTrustBundle
				var control api.SignedRootControl
				decode("current", &current)
				decode("control", &control)
				var seen, digest string
				_ = json.Unmarshal(i["seen_version"], &seen)
				_ = json.Unmarshal(i["seen_digest"], &digest)
				err = VerifyTransportCatchup(proof, current, control, facts, seen, digest, func() error {
					var confirmation map[string]json.RawMessage
					if json.Unmarshal(i["confirmation"], &confirmation) != nil || confirmationInput(t, confirmation) != nil {
						return Fault("projection_pending")
					}
					var bundle api.SignedTransportTrustBundle
					var required string
					_ = json.Unmarshal(confirmation["bundle"], &bundle)
					_ = json.Unmarshal(confirmation["required_phase"], &required)
					return CheckTransportPredecessor(current, bundle, required)
				})
			case "node_control_binding":
				var pin api.NodeControlPin
				decode("pin", &pin)
				err = BindNodeControl(pin, text("authenticated_node_id"), text("target_id"), text("identity_id"), text("identity_revision"), boolean("compromised"), boolean("requests_private_material"), boolean("independent_enrollment"))
			case "projection_access":
				err = CheckProjectionAccess(text("component"), uint32(numeric("projector_uid")), uint32(numeric("reader_uid")), uint32(numeric("peer_uid")), uint32(numeric("owner_uid")), uint32(numeric("directory_mode")), uint32(numeric("file_mode")), boolean("component_private_mount"), boolean("cap_drop_all"))
			default:
				t.Fatalf("unknown production boundary %s", v.Operation)
			}
			if got := verdict(err); got != v.Expected {
				t.Fatalf("%s want %s", got, v.Expected)
			}
		})
	}
}

func transitionInput(t *testing.T, i map[string]json.RawMessage) (api.TransportTransitionProof, TransportTransitionFacts) {
	t.Helper()
	var input struct {
		Root        string `json:"root_spki"`
		Tenant      string `json:"tenant_id"`
		Realm       string
		Control     api.SignedRootControl `json:"latest_control"`
		Authority   api.SignedRuntimeAuthority
		Transport   api.SignedTransportTrustBundle
		Predecessor api.TransportPredecessor
		Generation  string `json:"seen_generation"`
		Version     string `json:"seen_bundle_version"`
		Digest      string `json:"seen_bundle_digest"`
		Isolated    bool   `json:"isolation_enforced"`
	}
	b, _ := json.Marshal(i)
	if e := json.Unmarshal(b, &input); e != nil {
		t.Fatal(e)
	}
	return api.TransportTransitionProof{Control: input.Control, Authority: input.Authority, FirstBundle: input.Transport, Predecessor: input.Predecessor}, TransportTransitionFacts{Root: input.Root, Tenant: input.Tenant, Realm: input.Realm, SeenGeneration: input.Generation, SeenVersion: input.Version, SeenDigest: input.Digest, IsolationEnforced: input.Isolated}
}

func confirmationInput(t *testing.T, i map[string]json.RawMessage) error {
	t.Helper()
	var input struct {
		Root                  string `json:"root_spki"`
		Tenant                string `json:"tenant_id"`
		Realm, Challenge, Now string
		Required              string            `json:"required_phase"`
		Selectors             map[string]string `json:"expected_selectors"`
		Evidence              api.SignedTransportConfirmation
		Bundle                api.SignedTransportTrustBundle
		Authority             api.SignedRuntimeAuthority
		Control               api.SignedRootControl
	}
	b, _ := json.Marshal(i)
	if e := json.Unmarshal(b, &input); e != nil {
		t.Fatal(e)
	}
	return VerifyTransportConfirmation(input.Evidence, input.Bundle, input.Authority, input.Control, ConfirmationFacts{Root: input.Root, Tenant: input.Tenant, Realm: input.Realm, Challenge: input.Challenge, Now: stamp(input.Now), RequiredPhase: input.Required, ExpectedSelectors: input.Selectors})
}
