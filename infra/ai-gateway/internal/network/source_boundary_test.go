package network

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSourceRequestStrictBoundaries(t *testing.T) {
	for _, op := range []string{"source_register", "source_get", "source_revoke"} {
		raw := string(sourceRequest(op))
		cases := map[string]string{
			"duplicate":         strings.Replace(raw, `"epoch":"42"`, `"epoch":"41","epoch":"42"`, 1),
			"escaped duplicate": strings.Replace(raw, `"epoch":"42"`, `"ep\u006fch":"42","epoch":"42"`, 1),
			"UTF8":              strings.Replace(raw, "boot-1", "boot-\xff", 1),
			"surrogate":         strings.Replace(raw, "ignored", `\ud800`, 1),
			"control":           strings.Replace(raw, "ignored", "bad\nstring", 1),
			"trailing":          raw + `{}`,
			"noninteger":        strings.Replace(raw, `"ignored"`, `1.5`, 1),
			"exponent":          strings.Replace(raw, `"ignored"`, `1e1`, 1),
			"unsafe number":     strings.Replace(raw, `"ignored"`, `9007199254740992`, 1),
			"depth":             strings.Replace(raw, `"ignored"`, strings.Repeat(`[`, 17)+`0`+strings.Repeat(`]`, 17), 1),
			"array":             strings.Replace(raw, `"ignored"`, `[`+strings.Repeat(`0,`, 256)+`0]`, 1),
			"string":            strings.Replace(raw, "ignored", strings.Repeat("x", 16385), 1),
			"size":              raw + strings.Repeat(" ", EncodingMaxPayloadBytes),
			"numeric epoch":     strings.Replace(raw, `"epoch":"42"`, `"epoch":42`, 1),
			"zero epoch":        strings.Replace(raw, `"epoch":"42"`, `"epoch":"0"`, 1),
			"padded epoch":      strings.Replace(raw, `"epoch":"42"`, `"epoch":"042"`, 1),
			"overflow epoch":    strings.Replace(raw, `"epoch":"42"`, `"epoch":"18446744073709551616"`, 1),
			"boot":              strings.Replace(raw, `"boot-1"`, `null`, 1),
			"required feature":  strings.Replace(raw, `"future":`, `"required_features":["unimplemented"],"future":`, 1),
			"major":             strings.Replace(raw, `"1.0"`, `"2.0"`, 1),
		}
		members := "{"
		for i := 0; i < 129; i++ {
			if i > 0 {
				members += ","
			}
			members += `"k` + strconv.Itoa(i) + `":0`
		}
		cases["members"] = strings.Replace(raw, `"ignored"`, members+"}", 1)
		array := "[" + strings.Repeat("0,", 255) + "0]"
		cases["total values"] = strings.Replace(raw, `"ignored"`, "["+strings.Repeat(array+",", 31)+array+"]", 1)
		for name, bad := range cases {
			t.Run(op+"/"+name, func(t *testing.T) {
				if _, ae := decodeSourceRequest([]byte(bad), op); ae == nil || ae.Code != CodeProtocolError {
					t.Fatalf("accepted %s: %v", name, ae)
				}
			})
		}
		for _, other := range []string{"source_register", "source_get", "source_revoke", "fetch"} {
			if other == op {
				continue
			}
			if _, ae := decodeSourceRequest(sourceRequest(op), other); ae == nil {
				t.Fatalf("cross-method accepted %s as %s", op, other)
			}
		}
		q := decodeObject(t, []byte(raw))
		q["app_version"], q["TYPE"], q["Epoch"], q["tenant"], q["node"], q["connection"] = "1.7", "fetch", "0", "forged", "999", map[string]any{"ingress": "forged"}
		if _, ae := decodeSourceRequest(reverseRegistrationKeys(t, q), op); ae != nil {
			t.Fatalf("optional fields rejected: %v", ae)
		}
	}
}

func TestSourceKnownSchemaFields(t *testing.T) {
	for _, op := range []string{"source_register", "source_get"} {
		for _, field := range []string{"registration_id", "allocation_op_id", "allocation_fingerprint", "registration_revision", "local", "address_type"} {
			t.Run(op+"/"+field, func(t *testing.T) {
				q := decodeObject(t, sourceRequest(op))
				delete(q["registration"].(map[string]any), field)
				if _, ae := decodeSourceRequest(marshalRegister(t, q), op); ae == nil {
					t.Fatal("missing typed registration field accepted")
				}
			})
		}
		for _, field := range []string{"witness_id", "binding_id", "binding_revision", "observed_at", "valid_until"} {
			q := decodeObject(t, sourceRequest(op))
			delete(q["native_witness"].(map[string]any), field)
			if _, ae := decodeSourceRequest(marshalRegister(t, q), op); ae == nil {
				t.Fatalf("missing witness %s accepted", field)
			}
		}
	}
	q := decodeObject(t, sourceRequest("source_register"))
	q["mode"], q["native_witness"] = "reconcile", nil
	if _, ae := decodeSourceRequest(marshalRegister(t, q), "source_register"); ae != nil {
		t.Fatal(ae)
	}
	q["mode"] = "issue"
	if _, ae := decodeSourceRequest(marshalRegister(t, q), "source_register"); ae == nil {
		t.Fatal("issue without witness")
	}
	q = decodeObject(t, sourceRequest("source_register"))
	q["expected_revision"] = "1"
	if _, ae := decodeSourceRequest(marshalRegister(t, q), "source_register"); ae == nil {
		t.Fatal("first generation has nonzero CAS")
	}
	q["predecessor_binding"] = "previous-1"
	if _, ae := decodeSourceRequest(marshalRegister(t, q), "source_register"); ae != nil {
		t.Fatal(ae)
	}
}

func TestProductionSourceRejectsRequestsBeforeRPC(t *testing.T) {
	for _, variant := range []string{"duplicate", "epoch", "boot", "witness", "subagent", "invalid UTF8"} {
		t.Run(variant, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := sourceFixture(t, f, f)
			raw, code := string(sourceRequest("source_get")), CodeProtocolError
			switch variant {
			case "duplicate":
				raw = strings.Replace(raw, `"epoch":"42"`, `"epoch":"42","epoch":"42"`, 1)
			case "epoch":
				raw, code = strings.Replace(raw, `"epoch":"42"`, `"epoch":"41"`, 1), CodeStaleEpoch
			case "boot":
				raw, code = strings.Replace(raw, `"boot-1"`, `"boot-old"`, 1), CodeStaleEpoch
			case "witness":
				raw = strings.Replace(raw, `"binding_revision":"1"`, `"binding_revision":"0"`, 1)
			case "subagent":
				raw = strings.Replace(raw, `"session"`, `"subagent"`, 1)
			case "invalid UTF8":
				raw = strings.Replace(raw, "ignored", "\xff", 1)
			}
			publishRegistration(t, nc, b, []byte(raw))
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, code)
			run.mailbox.workers.Wait()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.calls) != 1 {
				t.Fatalf("invalid source request reached RPC: %v", f.calls)
			}
		})
	}
}

func TestProductionSourceResponseBoundaries(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"wrong result": func(m map[string]any) { m["type"] = "source_revoke_result" },
		"request id":   func(m map[string]any) { m["request_id"] = "other" },
		"realm":        func(m map[string]any) { m["server_auth"].(map[string]any)["audience"].(map[string]any)["realm"] = "3" },
		"node":         func(m map[string]any) { m["server_auth"].(map[string]any)["audience"].(map[string]any)["node"] = "999" },
		"epoch":        func(m map[string]any) { m["server_auth"].(map[string]any)["audience"].(map[string]any)["epoch"] = "43" },
		"incarnation": func(m map[string]any) {
			m["server_auth"].(map[string]any)["audience"].(map[string]any)["incarnation"] = "other"
		},
		"nonce":         func(m map[string]any) { m["server_auth"].(map[string]any)["channel_nonce"] = "other" },
		"auth request":  func(m map[string]any) { m["server_auth"].(map[string]any)["request_id"] = "other" },
		"purpose":       func(m map[string]any) { m["server_auth"].(map[string]any)["purpose"] = "message" },
		"missing auth":  func(m map[string]any) { delete(m, "server_auth") },
		"signature":     func(m map[string]any) { m["server_auth"].(map[string]any)["signature"] = "bad" },
		"key":           func(m map[string]any) { m["server_auth"].(map[string]any)["kid"] = "stub-unsigned" },
		"algorithm":     func(m map[string]any) { m["server_auth"].(map[string]any)["alg"] = "HS256" },
		"unknown state": func(m map[string]any) { m["source"].(map[string]any)["state"] = "unknown" },
		"registration":  func(m map[string]any) { m["source"].(map[string]any)["registration"].(map[string]any)["local"] = "999" },
		"native revision": func(m map[string]any) {
			m["source"].(map[string]any)["native_binding"].(map[string]any)["binding_revision"] = "2"
		},
		"binding":             func(m map[string]any) { m["source"].(map[string]any)["source_binding"] = "other" },
		"witness":             func(m map[string]any) { m["witness_id"] = "other" },
		"missing valid until": func(m map[string]any) { delete(m, "valid_until") },
		"null valid until":    func(m map[string]any) { m["valid_until"] = nil },
		"policy revision":     func(m map[string]any) { m["policy_revision"] = "0" },
		"time":                func(m map[string]any) { m["observed_at"] = "2026-10-27T00:00:00.1Z" },
		"required feature":    func(m map[string]any) { m["required_features"] = []any{"unknown"} },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			f := &fakeMailboxRPC{}
			run, nc, down, b, cfg := sourceFixture(t, f, f)
			m := decodeObject(t, sourceResult(t, cfg.ProtocolSigning, b, "source_get", "active"))
			change(m)
			f.mu.Lock()
			f.response = marshalRegister(t, m)
			f.mu.Unlock()
			publishRegistration(t, nc, b, sourceRequest("source_get"))
			expectRegistrationCode(t, run, down, cfg.ProtocolSigning, b, CodeProtocolError)
			run.mailbox.workers.Wait()
		})
	}
}

func TestSourceResultsTerminalAndMutationFacts(t *testing.T) {
	f := &fakeMailboxRPC{}
	run, nc, down, b, cfg := sourceFixture(t, f, f)
	for _, state := range []string{"expired", "revoked", "stopped"} {
		raw := sourceResult(t, cfg.ProtocolSigning, b, "source_get", state)
		f.mu.Lock()
		f.response = raw
		f.mu.Unlock()
		publishRegistration(t, nc, b, sourceRequest("source_get"))
		msg, err := down.NextMsg(time.Second)
		if err != nil || string(msg.Data) != string(raw) {
			t.Fatalf("terminal %s rejected: %v %v", state, msg, err)
		}
		run.mailbox.workers.Wait()
		m := decodeObject(t, raw)
		m["valid_until"] = nil
		if _, err := validateMailboxResponse(b, "source_get", "req-1", sourceRequest("source_get"), marshalRegister(t, m)); err == nil {
			t.Fatal("terminal Get accepted null valid_until")
		}
	}
	// A historical creation receipt remains transportable after expires_at.
	raw := sourceResult(t, cfg.ProtocolSigning, b, "source_register", "active")
	if _, err := validateMailboxResponse(b, "source_register", "req-1", sourceRequest("source_register"), raw); err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{"not_accepted", "not_committed"} {
		m := decodeObject(t, raw)
		authority, reason := "collab", "rejected_before_pending"
		if result == "not_committed" {
			authority, reason = "iam", "lineage_stopped"
		}
		m["result"], m["source"], m["failure_evidence"] = result, nil, map[string]any{"authority": authority, "reason": reason, "request_digest": "sha256:" + strings.Repeat("0", 64)}
		if _, err := validateMailboxResponse(b, "source_register", "req-1", sourceRequest("source_register"), marshalRegister(t, m)); err != nil {
			t.Fatal(err)
		}
		m["failure_evidence"].(map[string]any)["authority"] = "forged"
		if _, err := validateMailboxResponse(b, "source_register", "req-1", sourceRequest("source_register"), marshalRegister(t, m)); err == nil {
			t.Fatal("forged definitive failure")
		}
	}
	for _, op := range []string{"source_register", "source_revoke"} {
		m := decodeObject(t, sourceResult(t, cfg.ProtocolSigning, b, op, "active"))
		m["operation_id"] = "other"
		if _, err := validateMailboxResponse(b, op, "req-1", sourceRequest(op), marshalRegister(t, m)); err == nil {
			t.Fatal("wrong mutation operation accepted")
		}
	}
	for _, state := range []string{"expired", "revoked", "active", "stopped"} {
		m := decodeObject(t, sourceResult(t, cfg.ProtocolSigning, b, "source_revoke", state))
		m["state"] = state
		_, err := validateMailboxResponse(b, "source_revoke", "req-1", sourceRequest("source_revoke"), marshalRegister(t, m))
		if (err == nil) != (state == "expired" || state == "revoked") {
			t.Fatalf("wrong revoke state acceptance: %s %v", state, err)
		}
		m["lineage_stopped"] = false
		if _, err = validateMailboxResponse(b, "source_revoke", "req-1", sourceRequest("source_revoke"), marshalRegister(t, m)); err == nil {
			t.Fatal("unconfirmed revoke accepted")
		}
	}
}

func TestSourceRPCWrongMethodAndDependencyFailures(t *testing.T) {
	f := &fakeMailboxRPC{}
	conn, err := team.DialRPC(startMailboxRPC(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := NewMailboxClient(conn, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"source_register", "source_get", "source_revoke"} {
		if _, ae := client.Call(context.Background(), liveBinding{}, op, sourceRequest("fetch")); ae == nil || ae.Code != CodeProtocolError {
			t.Fatal("wrong RPC method accepted")
		}
		for _, failure := range []error{status.Error(codes.Unimplemented, "private backend"), status.Error(codes.Unavailable, "private backend")} {
			f.mu.Lock()
			f.failure = failure
			f.mu.Unlock()
			if data, ae := client.Call(context.Background(), liveBinding{}, op, sourceRequest(op)); ae == nil || data != nil || ae.Code != "network.overloaded" {
				t.Fatalf("dependency failure made success: %s %v", data, ae)
			}
		}
	}
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	if calls != 6 {
		t.Fatalf("wrong-method calls reached backend: %d", calls)
	}
}

// Keep malformed raw objects as bytes: unmarshalling them would erase precisely
// the duplicate/UTF8/size boundary under test.
func TestSourceResponseRawBoundaries(t *testing.T) {
	_, cfg, _ := runtimeFixture(t)
	b := liveBinding{Realm: "1", Node: "1", Epoch: 42, Incarnation: "inc-1", ChannelNonce: "nonce-1", NotAfter: time.Now().Add(time.Hour)}
	base := sourceResult(t, cfg.ProtocolSigning, b, "source_get", "active")
	for name, raw := range map[string][]byte{
		"duplicate": []byte(strings.Replace(string(base), `"request_id":"req-1"`, `"request_id":"other","request_id":"req-1"`, 1)),
		"UTF8":      []byte(strings.Replace(string(base), "原字节", "\xff", 1)),
		"oversize":  append(append([]byte{}, base...), []byte(strings.Repeat(" ", EncodingMaxPayloadBytes))...),
		"depth":     []byte(strings.Replace(string(base), `"原字节"`, strings.Repeat(`[`, 17)+`0`+strings.Repeat(`]`, 17), 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateMailboxResponse(b, "source_get", "req-1", sourceRequest("source_get"), raw); err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
}
