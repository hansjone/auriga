// Package testfixture creates ephemeral offline approvals exclusively for tests.
package testfixture

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/deploy/bootstrap/collab"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func Write(t *testing.T, tenant string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, value any) string {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	root, private, _ := ed25519.GenerateKey(rand.Reader)
	rootDER, _ := x509.MarshalPKIXPublicKey(root)
	if err := os.WriteFile(filepath.Join(dir, "root.der"), rootDER, 0600); err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	until := issued.Add(time.Hour)
	keys := []map[string]any{}
	stateKeys := []map[string]any{}
	secrets := map[string]string{}
	for i, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		kid := fmt.Sprintf("approved-%d", i)
		pub, secret, _ := ed25519.GenerateKey(rand.Reader)
		der, _ := x509.MarshalPKIXPublicKey(pub)
		pk, _ := x509.MarshalPKCS8PrivateKey(secret)
		path := filepath.Join(dir, kid+".der")
		if err := os.WriteFile(path, pk, 0600); err != nil {
			t.Fatal(err)
		}
		secrets[kid] = path
		keys = append(keys, map[string]any{"kid": kid, "alg": "Ed25519", "purpose": purpose, "public_key_spki": base64.RawURLEncoding.EncodeToString(der), "not_before": issued.Format(time.RFC3339), "not_after": until.Format(time.RFC3339), "state": "active"})
		stateKeys = append(stateKeys, map[string]any{"kid": kid, "realm": "1", "purpose": purpose, "public_key_spki": base64.RawURLEncoding.EncodeToString(der), "not_before": issued.Format(time.RFC3339), "not_after": until.Format(time.RFC3339), "state": "ACTIVE"})
	}
	payload := map[string]any{"schema_version": 1, "realm": "1", "keyset_version": "7", "issued_at": issued.Format(time.RFC3339), "valid_until": until.Format(time.RFC3339), "keys": keys}
	b, _ := json.Marshal(payload)
	signature := ed25519.Sign(private, append([]byte("Auriga.NetworkKeyset.v1\x00"), b...))
	sum := sha256.Sum256(b)
	announcement := map[string]any{"keyset_version": "7", "valid_until": until.Format(time.RFC3339), "manifest_digest": fmt.Sprintf("sha256:%x", sum), "root_kid": "offline-root"}
	ab, _ := json.Marshal(announcement)
	announcement["root_signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, append([]byte("Auriga.NetworkKeysetAnnouncement.v1\x00"), ab...)))
	rootSum := sha256.Sum256(rootDER)
	input := collab.Input{TenantID: tenant, Realm: "1", RootKid: "offline-root", RootFingerprint: fmt.Sprintf("sha256:%x", rootSum), MinimumVersion: "7", RootPublicFile: filepath.Join(dir, "root.der"), SignedFile: write("signed.json", map[string]any{"keyset": payload, "root_auth": map[string]any{"alg": "Ed25519", "kid": "offline-root", "signature": base64.RawURLEncoding.EncodeToString(signature)}}), AnnouncementFile: write("announcement.json", announcement), StateFile: write("state.json", map[string]any{"version": "7", "issued_at": issued.Format(time.RFC3339), "keys": stateKeys}), PrivateFiles: secrets}
	return write("input.json", input)
}
