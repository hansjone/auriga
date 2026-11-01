package main

import (
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNASRotationStrictInput(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"version":2}`,
		`{"Version":1}`,
		`{"version":1,"extra":0}`,
		`{"version":1} {}`,
	} {
		var value struct {
			Version int `json:"version"`
		}
		if nasRotationDecode([]byte(raw), &value) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestNASRotationGatewayOnlyChangesProtocol(t *testing.T) {
	old := []byte(`{"qos":{"max":9007199254740993},"collab":{"Target":"collab:9092"},"protocolSigning":{"keyId":"old"}}`)
	key := nasRotationKey{Kid: "new", Purpose: "protocol_response", State: "active", Before: "2026-01-01T00:00:00Z", After: "2026-01-08T00:00:00Z"}
	raw, err := nasRotationGateway(old, "7", key)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	if string(value["qos"]) != `{"max":9007199254740993}` || string(value["collab"]) != `{"Target":"collab:9092"}` {
		t.Fatal("changed non-protocol fields")
	}
	key.State = "next"
	if _, err = nasRotationGateway(old, "7", key); err == nil {
		t.Fatal("next protocol activated")
	}
}

func TestNASRotationRequestRetainsExpiredHistoryAndNeverActivatesEarly(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	keys := []nasRotationKey{}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		keys = append(keys, nasRotationKey{Kid: purpose, Alg: "Ed25519", Purpose: purpose, State: "active", Public: "unchanged", Before: now.Add(-48 * time.Hour).Format(time.RFC3339), After: now.Add(-time.Hour).Format(time.RFC3339)})
	}
	p := nasRotationPayload{Schema: 1, Realm: "1", Version: "1", Issued: now.Add(-24 * time.Hour).Format(time.RFC3339), Until: now.Format(time.RFC3339), Keys: keys}
	next, private, err := nasRotationRequest(p, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != "2" || len(next.Keys) != 6 || len(private) != 3 {
		t.Fatal("incomplete request")
	}
	for i, k := range next.Keys[:3] {
		expected := keys[i]
		expected.State = "retired"
		if k != expected {
			t.Fatal("old history changed")
		}
	}
	for _, k := range next.Keys[3:] {
		key, err := x509.ParsePKCS8PrivateKey(private[k.Kid])
		if err != nil {
			t.Fatal(err)
		}
		public, err := x509.MarshalPKIXPublicKey(key.(crypto.Signer).Public())
		if err != nil {
			t.Fatal(err)
		}
		if base64.RawURLEncoding.EncodeToString(public) != k.Public {
			t.Fatal("new private/public pair mismatch")
		}
		if k.State != "next" || timeMustParse(t, k.After).Sub(timeMustParse(t, k.Before)) != 7*24*time.Hour {
			t.Fatal("incorrect new next lifetime")
		}
	}
	for i := 3; i < 6; i++ {
		next.Keys[i].State = "next"
	}
	history := map[string]string{}
	for _, k := range next.Keys[3:] {
		history[k.Kid] = now.Add(-24*time.Hour + time.Nanosecond).Format(time.RFC3339Nano)
	}
	request, _, err := nasRotationRequest(next, history, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range request.Keys[3:] {
		if k.State != "next" {
			t.Fatal("rounded publication maturity down")
		}
	}
	request, _, err = nasRotationRequest(next, history, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range request.Keys[3:] {
		if k.State != "active" {
			t.Fatal("mature next did not activate")
		}
	}
}

func timeMustParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestNASExpiredRecoveryRequestRequiresExhaustedTrust(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	old := nasRotationPayload{Schema: 1, Realm: "1", Version: "1", Issued: now.Add(-26 * time.Hour).Format(time.RFC3339), Until: now.Add(-2 * time.Hour).Format(time.RFC3339)}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		old.Keys = append(old.Keys, nasRotationKey{Kid: purpose, Alg: "Ed25519", Purpose: purpose, Public: "unchanged", State: "active", Before: old.Issued, After: old.Until})
	}
	p, private, e := nasRotationRecoveryRequest(old, now, now)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Keys) != 6 || len(private) != 3 {
		t.Fatal("incomplete recovery")
	}
	for _, k := range p.Keys[:3] {
		if k.State != "retired" {
			t.Fatal("old authority survived")
		}
	}
	for _, k := range p.Keys[3:] {
		if k.State != "active" || k.Before != p.Issued {
			t.Fatal("recovery requires fresh root-approved active keys")
		}
	}
	for _, bad := range []string{"live-key", "live-manifest", "clearance", "clock-skew"} {
		t.Run(bad, func(t *testing.T) {
			candidate := old
			candidate.Keys = append([]nasRotationKey{}, old.Keys...)
			db := now
			switch bad {
			case "live-key":
				candidate.Keys[0].After = now.Add(time.Hour).Format(time.RFC3339)
			case "live-manifest":
				candidate.Until = now.Add(time.Hour).Format(time.RFC3339)
			case "clearance":
				candidate.Keys[0].After = now.Add(-359 * time.Second).Format(time.RFC3339)
			case "clock-skew":
				db = now.Add(120 * time.Second)
			}
			if _, _, e := nasRotationRecoveryRequest(candidate, now, db); e == nil {
				t.Fatal("unsafe recovery request accepted")
			}
		})
	}
}

func TestNASRotationArchiveModesAndTamper(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("protected NAS rotation archives require Linux root")
	}
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			r := &nasRotation{root: t.TempDir(), binding: nasRotationBinding{Base: strings.Repeat("a", 64), Source: strings.Repeat("b", 64), Image: "sha256:" + strings.Repeat("c", 64)}}
			if e := os.Mkdir(filepath.Join(r.root, "releases"), 0700); e != nil {
				t.Fatal(e)
			}
			d := filepath.Join(r.root, "releases", "2")
			if e := os.Mkdir(d, 0700); e != nil {
				t.Fatal(e)
			}
			pair := []byte(`{"keyset":{"keyset_version":"2"}}`)
			if e := nasRotationWrite(filepath.Join(d, "signed.json"), pair); e != nil {
				t.Fatal(e)
			}
			if e := nasRotationWrite(filepath.Join(d, "announcement.json"), []byte(`{}`)); e != nil {
				t.Fatal(e)
			}
			if e := nasRotationWrite(filepath.Join(d, "pg-status.json"), []byte(`{}`)); e != nil {
				t.Fatal(e)
			}
			receipt := nasRotationRelease{Binding: r.binding, Version: "2", Mode: "regular", Files: map[string]string{"signed.json": nasRotationHash(pair), "announcement.json": nasRotationHash([]byte(`{}`)), "pg-status.json": nasRotationHash([]byte(`{}`))}}
			if e := nasRotationWriteJSON(filepath.Join(d, "publication.receipt"), receipt); e != nil {
				t.Fatal(e)
			}
			if _, e := r.releases(); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(d, "signed.json"), []byte(`changed`), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := r.releases(); e == nil {
				t.Fatal("tampered archive accepted")
			}
		})
	}
}

func TestNASRotationExpiryWarningIncludesBusinessKeys(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	p := nasRotationPayload{Until: now.Add(24 * time.Hour).Format(time.RFC3339)}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		p.Keys = append(p.Keys, nasRotationKey{Purpose: purpose, State: "active", After: now.Add(7 * time.Hour).Format(time.RFC3339)})
	}
	if nasRotationExpiryWarning(p, now) != "" {
		t.Fatal("early business alarm")
	}
	p.Keys[0].After = now.Add(6 * time.Hour).Format(time.RFC3339)
	if nasRotationExpiryWarning(p, now) != "business_keys_expire_within_6h_operator_action_required" {
		t.Fatal("fresh keyset masked expiring business key")
	}
	p.Until = now.Add(6 * time.Hour).Format(time.RFC3339)
	if nasRotationExpiryWarning(p, now) != "keyset_expires_within_6h_operator_signature_required" {
		t.Fatal("manifest deadline missing")
	}
}
