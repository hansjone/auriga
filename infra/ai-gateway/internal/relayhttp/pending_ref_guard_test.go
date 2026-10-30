package relayhttp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
)

type pendingPublic struct {
	hasResponse bool
	header      http.Header
	body        string
	handlerLog  string
	formats     [3]string
	json        string
}

type pendingSecret struct {
	label, value string
	identity     bool
}

func pendingPrivateValues(ref budget.Ref, tenant string) []pendingSecret {
	return []pendingSecret{
		{"ref-request", ref.RequestID, true}, {"tenant", tenant, true},
		{"account", account, true}, {"device", device, true},
		{"private-marker", "private", false}, {"sql-marker", "SQL", false},
		{"client-request", "forged-client-request", true}, {"client-tenant", "forged-tenant", true},
		{"client-account", "forged-account", true}, {"cause-code", "common.conflict", false},
	}
}

type pendingPublicSurface struct{ label, text string }

func (p pendingPublic) surfaces() []pendingPublicSurface {
	return []pendingPublicSurface{
		{"header", fmt.Sprint(p.header)}, {"body", p.body}, {"handler-log", p.handlerLog},
		{"pending-v", p.formats[0]}, {"pending-plus", p.formats[1]},
		{"pending-sharp", p.formats[2]}, {"pending-json", p.json},
	}
}

// Only fixed labels and locations may reach CI diagnostics, never matched data.
func pendingPrivacyHits(surfaces []pendingPublicSurface, secrets []pendingSecret) []string {
	var hits []string
	for _, surface := range surfaces {
		for _, secret := range secrets {
			if secret.value != "" && strings.Contains(surface.text, secret.value) {
				hits = append(hits, secret.label+"@"+surface.label)
			}
		}
	}
	return hits
}

// Admission errors retain ServeHTTP's independent crypto/rand.Text ID. Go
// 1.27.3 uses 26 RFC 4648 base32 characters, so a legitimate ID can contain SQL.
// Validate both designated slots and private identities BEFORE exempting them
// from short-marker matching; no other occurrence or surface is exempted.
func pendingPrivacyViolations(p pendingPublic, secrets []pendingSecret) []string {
	reject := func(label string) []string {
		return append([]string{label}, pendingPrivacyHits(p.surfaces(), secrets)...)
	}
	if p.hasResponse {
		ids := values(p.header, "X-Request-Id")
		if len(ids) != 1 || len(ids[0]) != 26 || strings.Trim(ids[0], "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
			return reject("invalid-public-id@header")
		}
		id := ids[0]
		for _, secret := range secrets {
			if secret.identity && secret.value != "" && strings.Contains(id, secret.value) {
				return reject(secret.label + "@public-id")
			}
		}
		fields, label := pendingPublicBody(p.body)
		if label != "" {
			return reject(label)
		}
		var bodyID string
		if json.Unmarshal(fields["requestId"], &bodyID) != nil || bodyID != id {
			return reject("mismatched-public-id@body")
		}
		// Work on detached values so checking cannot mutate the actual output.
		p.header = p.header.Clone()
		for key := range p.header {
			if strings.EqualFold(key, "X-Request-Id") {
				delete(p.header, key)
			}
		}
		delete(fields, "requestId")
		body, err := json.Marshal(fields)
		if err != nil {
			return reject("invalid-envelope@body")
		}
		p.body = string(body)
	}
	return pendingPrivacyHits(p.surfaces(), secrets)
}

// Do not let map decoding silently accept duplicate requestId or other keys.
func pendingPublicBody(body string) (map[string]json.RawMessage, string) {
	d := json.NewDecoder(strings.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, "invalid-envelope@body"
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, "invalid-envelope@body"
		}
		if _, exists := fields[key]; exists {
			return nil, "duplicate-field@body"
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, "invalid-envelope@body"
		}
		fields[key] = raw
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, "invalid-envelope@body"
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, "invalid-envelope@body"
	}
	return fields, ""
}

const pendingGuardSQLID = "SQLAAAAAAAAAAAAAAAAAAAAAAA"

func pendingGuardFixture(t *testing.T, id string) (pendingPublic, []pendingSecret) {
	t.Helper()
	ref := budget.Ref{TenantID: "01900000-0000-7000-8000-000000000001", AccountID: account, RequestID: "01900000-0000-7000-8000-000000000006"}
	// Use the real error encoder with a supplied ID; no global entropy override
	// or production request-ID hook is needed for deterministic helper coverage.
	w := &deadlineRecorder{httptest.NewRecorder()}
	w.Header().Set("X-Request-Id", id)
	s := response{w: w, rc: http.NewResponseController(w), timeout: time.Second, max: 1 << 20}
	s.failure(t.Context(), id, false, fault("ai.budget_unavailable"))
	pending := Pending{Ref: ref}
	b, err := json.Marshal(pending)
	if err != nil {
		t.Fatal("fixture Pending JSON failed")
	}
	return pendingPublic{
		hasResponse: true, header: w.Header(), body: w.Body.String(),
		formats: [3]string{fmt.Sprintf("%v", pending), fmt.Sprintf("%+v", pending), fmt.Sprintf("%#v", pending)}, json: string(b),
	}, pendingPrivateValues(ref, ref.TenantID)
}

func TestPendingPrivacyGuardPublicID(t *testing.T) {
	for _, tc := range []struct{ name, id string }{{"normal", strings.Repeat("A", 26)}, {"SQL", pendingGuardSQLID}} {
		t.Run(tc.name, func(t *testing.T) {
			p, secrets := pendingGuardFixture(t, tc.id)
			old := pendingPrivacyHits(p.surfaces(), secrets)
			if tc.name == "SQL" {
				if !slices.Equal(old, []string{"sql-marker@header", "sql-marker@body"}) {
					t.Fatal("old matcher did not reproduce the specific false positive; labels:", old)
				}
			} else if len(old) != 0 {
				t.Fatal("old matcher rejected the clean control; labels:", old)
			}
			if hits := pendingPrivacyViolations(p, secrets); len(hits) != 0 {
				t.Fatal("valid public correlation rejected; labels:", hits)
			}
		})
	}
}

func pendingGuardInject(t *testing.T, p pendingPublic, surface, value string) pendingPublic {
	t.Helper()
	p.header = p.header.Clone()
	switch surface {
	case "header":
		p.header.Set("X-Bad-Output", value)
	case "body":
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(p.body), &fields) != nil {
			t.Fatal("invalid baseline body")
		}
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal("bad-output fixture encoding failed")
		}
		fields["extra"] = b
		b, err = json.Marshal(fields)
		if err != nil {
			t.Fatal("bad-output body encoding failed")
		}
		p.body = string(b)
	case "handler-log":
		p.handlerLog += value
	case "pending-v":
		p.formats[0] += value
	case "pending-plus":
		p.formats[1] += value
	case "pending-sharp":
		p.formats[2] += value
	case "pending-json":
		p.json = `{"ref":"` + value + `"}`
	default:
		t.Fatal("unknown bad-output surface")
	}
	return p
}

func pendingGuardMarkers(secrets []pendingSecret) []pendingSecret {
	return append(slices.Clone(secrets),
		pendingSecret{"full-cause", "lost reply: common.conflict private SQL cause", false},
		pendingSecret{"before-cause", "private SQL before INSERT", false},
		pendingSecret{"wrapped-cause", "lost reply: common.conflict", false})
}

func pendingGuardSetID(t *testing.T, p pendingPublic, id string) pendingPublic {
	t.Helper()
	p.header = p.header.Clone()
	p.header.Set("X-Request-Id", id)
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(p.body), &fields) != nil {
		t.Fatal("invalid ID fixture body")
	}
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal("ID fixture encoding failed")
	}
	fields["requestId"] = b
	b, err = json.Marshal(fields)
	if err != nil {
		t.Fatal("ID fixture body encoding failed")
	}
	p.body = string(b)
	return p
}

func TestPendingPrivacyGuardLeakMatrix(t *testing.T) {
	p, secrets := pendingGuardFixture(t, pendingGuardSQLID)
	// 7 surfaces x 13 values, plus 13 private-ID substitutions and two cases
	// that catch whole-body masking / duplicate-key last-value-wins decoding.
	for _, surface := range p.surfaces() {
		for _, marker := range pendingGuardMarkers(secrets) {
			t.Run(surface.label+"/"+marker.label, func(t *testing.T) {
				bad := pendingGuardInject(t, p, surface.label, marker.value)
				label := marker.label
				switch label {
				case "full-cause", "wrapped-cause":
					label = "cause-code"
				case "before-cause":
					label = "private-marker"
				}
				if hits := pendingPrivacyViolations(bad, secrets); !slices.Contains(hits, label+"@"+surface.label) {
					t.Fatal("specific leak missed; safe labels:", hits)
				}
			})
		}
	}
	for _, marker := range pendingGuardMarkers(secrets) {
		t.Run("private-id/"+marker.label, func(t *testing.T) {
			if len(pendingPrivacyViolations(pendingGuardSetID(t, p, marker.value), secrets)) == 0 {
				t.Fatal("private value masked as public ID")
			}
		})
	}
	t.Run("id-in-extra-field", func(t *testing.T) {
		bad := pendingGuardInject(t, p, "body", pendingGuardSQLID)
		if !slices.Contains(pendingPrivacyViolations(bad, secrets), "sql-marker@body") {
			t.Fatal("public ID outside the allowed slot was masked")
		}
	})
	t.Run("duplicate-id", func(t *testing.T) {
		bad := p
		bad.body = `{"requestId":"` + device + `",` + strings.TrimPrefix(p.body, "{")
		if !slices.Contains(pendingPrivacyViolations(bad, secrets), "duplicate-field@body") {
			t.Fatal("duplicate requestId was not rejected")
		}
	})
}

func TestPendingPrivacyGuardInvalidEnvelope(t *testing.T) {
	p, secrets := pendingGuardFixture(t, strings.Repeat("A", 26))
	for _, tc := range []struct {
		name, label string
		mutate      func(*pendingPublic)
	}{
		{"missing-header", "invalid-public-id@header", func(p *pendingPublic) { p.header.Del("X-Request-Id") }},
		{"duplicate-header", "invalid-public-id@header", func(p *pendingPublic) { p.header.Add("X-Request-Id", p.header.Get("X-Request-Id")) }},
		{"noncanonical-duplicate-header", "invalid-public-id@header", func(p *pendingPublic) { p.header["x-request-id"] = []string{p.header.Get("X-Request-Id")} }},
		{"illegal-header", "invalid-public-id@header", func(p *pendingPublic) { p.header.Set("X-Request-Id", strings.Repeat("a", 26)) }},
		{"short-header", "invalid-public-id@header", func(p *pendingPublic) { p.header.Set("X-Request-Id", strings.Repeat("A", 25)) }},
		{"long-header", "invalid-public-id@header", func(p *pendingPublic) { p.header.Set("X-Request-Id", strings.Repeat("A", 27)) }},
		{"missing-body-id", "mismatched-public-id@body", func(p *pendingPublic) { p.body = `{}` }},
		{"different-body-id", "mismatched-public-id@body", func(p *pendingPublic) { p.body = `{"requestId":"` + strings.Repeat("B", 26) + `"}` }},
		{"nonstring-body-id", "mismatched-public-id@body", func(p *pendingPublic) { p.body = `{"requestId":1}` }},
		{"duplicate-other-field", "duplicate-field@body", func(p *pendingPublic) { p.body = `{"extra":1,"extra":2,` + strings.TrimPrefix(p.body, "{") }},
		{"malformed-json", "invalid-envelope@body", func(p *pendingPublic) { p.body = `{` }},
		{"array-json", "invalid-envelope@body", func(p *pendingPublic) { p.body = `[]` }},
		{"null-json", "invalid-envelope@body", func(p *pendingPublic) { p.body = `null` }},
		{"trailing-json", "invalid-envelope@body", func(p *pendingPublic) { p.body += `{}` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := p
			bad.header = p.header.Clone()
			tc.mutate(&bad)
			if hits := pendingPrivacyViolations(bad, secrets); !slices.Contains(hits, tc.label) {
				t.Fatal("envelope violation missed; labels:", hits)
			}
		})
	}
}

func TestPendingPrivacyGuardIdentityBeforeMask(t *testing.T) {
	p, secrets := pendingGuardFixture(t, strings.Repeat("A", 26))
	for i, secret := range secrets {
		if !secret.identity {
			continue
		}
		t.Run(secret.label, func(t *testing.T) {
			// Even a syntactically legal ID must not hide a private identity.
			poisoned := slices.Clone(secrets)
			poisoned[i].value = strings.Repeat("B", 26)
			bad := pendingGuardSetID(t, p, poisoned[i].value)
			if hits := pendingPrivacyViolations(bad, poisoned); !slices.Contains(hits, secret.label+"@public-id") {
				t.Fatal("identity was masked before being checked; safe labels:", hits)
			}
			// A complete private value embedded in a legal ID is also forbidden.
			poisoned[i].value = strings.Repeat("B", 20)
			if !slices.Contains(pendingPrivacyViolations(bad, poisoned), secret.label+"@public-id") {
				t.Fatal("embedded private identity was masked")
			}
		})
	}
}

func TestPendingPrivacyGuardCancelled(t *testing.T) {
	p, secrets := pendingGuardFixture(t, pendingGuardSQLID)
	p.hasResponse, p.header, p.body = false, nil, ""
	if hits := pendingPrivacyViolations(p, secrets); len(hits) != 0 {
		t.Fatal("disconnect without an HTTP response was rejected; labels:", hits)
	}
	for _, surface := range p.surfaces()[2:] {
		t.Run(surface.label, func(t *testing.T) {
			bad := pendingGuardInject(t, p, surface.label, secrets[0].value)
			if !slices.Contains(pendingPrivacyViolations(bad, secrets), "ref-request@"+surface.label) {
				t.Fatal("disconnect suppressed non-HTTP leak checks")
			}
		})
	}
}
