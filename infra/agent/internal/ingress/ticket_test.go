package ingress

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func testTicketKeys(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub, "ticket-1"
}

func TestTicketVerifier(t *testing.T) {
	t.Parallel()
	priv, pub, kid := testTicketKeys(t)
	fixed := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	v := &TicketVerifier{
		Keys: map[string]ed25519.PublicKey{kid: pub},
		Now:  func() time.Time { return fixed },
	}
	mk := func(mod func(*FileTicketClaims)) string {
		c := FileTicketClaims{
			Iss: "auriga-workspace", Aud: "auriga-agent", IID: "inst-1",
			PathPrefix: "workspace", Ops: []string{"read", "write", "list", "delete", "zip"},
			Iat: fixed.Unix(), Exp: fixed.Add(time.Minute).Unix(), Jti: "j1",
		}
		if mod != nil {
			mod(&c)
		}
		tok, err := SignFileTicket(priv, kid, c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	tok := mk(nil)
	if _, err := v.Verify(tok, "inst-1", OpRead, "workspace/a.txt"); err != nil {
		t.Fatal(err)
	}

	// expired
	tok = mk(func(c *FileTicketClaims) { c.Exp = fixed.Add(-time.Minute).Unix() })
	_, err := v.Verify(tok, "inst-1", OpRead, "workspace/a.txt")
	if err == nil || !strings.Contains(err.Error(), CodeTicketExpired) {
		t.Fatalf("want expired, got %v", err)
	}

	// wrong instance
	tok = mk(nil)
	_, err = v.Verify(tok, "other", OpRead, "workspace/a.txt")
	if err == nil || !strings.Contains(err.Error(), CodeTicketInstanceMismatch) {
		t.Fatalf("want instance mismatch, got %v", err)
	}

	// path denied
	_, err = v.Verify(mk(nil), "inst-1", OpRead, "secrets/x")
	if err == nil || !strings.Contains(err.Error(), CodeTicketPathDenied) {
		t.Fatalf("want path denied, got %v", err)
	}

	// op denied
	tok = mk(func(c *FileTicketClaims) { c.Ops = []string{"read"} })
	_, err = v.Verify(tok, "inst-1", OpWrite, "workspace/a.txt")
	if err == nil || !strings.Contains(err.Error(), CodeTicketOpDenied) {
		t.Fatalf("want op denied, got %v", err)
	}
}
