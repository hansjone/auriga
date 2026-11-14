package sharedauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type authorityFunc func(context.Context, Lookup) (Snapshot, error)

func (f authorityFunc) Resolve(c context.Context, q Lookup) (Snapshot, error) { return f(c, q) }

const testID = "11111111-1111-4111-8111-111111111111"

func TestCapabilityKWireAndFences(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	b := Binding{testID, testID, testID, testID, "shared", 1, 1, testID, testID, 1}
	active := true
	version := int64(1)
	fail := false
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	g, e := New(authorityFunc(func(context.Context, Lookup) (Snapshot, error) {
		if fail {
			return Snapshot{}, ErrUnavailable
		}
		return Snapshot{b, version, active, []string{"model.invoke"}}, nil
	}), map[string]ed25519.PublicKey{"model": pub}, func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	q := Lookup{TenantID: testID, SessionID: testID, TaskID: testID, Audience: Model}
	tok, e := g.Issue(context.Background(), q, []string{"model.invoke"}, 600*time.Second, "model", key)
	if e != nil {
		t.Fatal(e)
	}
	verify := func(token string) error {
		_, e := g.Verify(context.Background(), token, testID, testID, testID, Model, "model.invoke")
		return e
	}
	if e = verify(tok); e != nil {
		t.Fatal(e)
	}
	// Compare protocol field set to the frozen K schema, without copying schema.
	raw, e := os.ReadFile("../../../../contracts/shared-dsh/shared-dsh.schema.json")
	if e != nil {
		t.Fatal(e)
	}
	var schema struct {
		Defs map[string]struct {
			Required []string `json:"required"`
		} `json:"$defs"`
	}
	if e = json.Unmarshal(raw, &schema); e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(tok, ".")
	bytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var fields map[string]any
	json.Unmarshal(bytes, &fields)
	if len(fields) != len(schema.Defs["Capability"].Required) {
		t.Fatal("K field drift")
	}
	for _, k := range schema.Defs["Capability"].Required {
		if _, ok := fields[k]; !ok {
			t.Fatal(k)
		}
	}
	cases := []struct {
		name   string
		change func()
		want   error
	}{
		{"epoch", func() { b.Epoch++ }, ErrBinding}, {"credential", func() { version++ }, ErrRevoked}, {"logout", func() { active = false }, ErrRevoked}, {"authority-down", func() { fail = true }, ErrUnavailable}, {"expiry", func() { now = now.Add(600 * time.Second) }, ErrExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldB, oldV, oldN := b, version, now
			tc.change()
			if e := verify(tok); e != tc.want {
				t.Fatalf("%v want %v", e, tc.want)
			}
			b, version, now, active, fail = oldB, oldV, oldN, true, false
		})
	}
	for _, payload := range []string{string(bytes) + " ", strings.Replace(string(bytes), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1), strings.Replace(string(bytes), `"schemaVersion":1`, `"schemaVersion":1,"unknown":1`, 1)} {
		input := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
		bad := input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(input)))
		if verify(bad) == nil {
			t.Fatal("accepted noncanonical/duplicate/unknown JSON")
		}
	}
	if _, e := g.Verify(context.Background(), tok, testID, "", testID, Model, "model.invoke"); e == nil {
		t.Fatal("missing session accepted")
	}
	if _, e := g.Verify(context.Background(), tok, testID, testID, testID, Message, "message.send"); e == nil {
		t.Fatal("wrong audience accepted")
	}
	if _, e := g.Issue(context.Background(), q, []string{"model.invoke"}, 601*time.Second, "model", key); e == nil {
		t.Fatal("long capability")
	}
}
