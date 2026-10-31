package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWANBudgetProjectionPreservesMaterialAndRejectsInvalidBudget(t *testing.T) {
	raw := []byte(`{"redeemTimeoutMillis":1000,"requestTimeoutMillis":250,"activeIntervalMillis":250,"protocolSigning":{"privateKeyFile":"/root/unchanged"},"maxConnections":1000}`)
	broker := []byte("tls {cert_file: key, timeout: 2}\nauthorization {timeout: 2, auth_callout {issuer: ROOT}}\n")
	for _, ms := range []int{30000, 60000} {
		r, b, e := projectNetworkBudget(raw, broker, ms)
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		if json.Unmarshal(r, &v) != nil {
			t.Fatal("json")
		}
		if v["operationTimeoutMillis"] != float64(ms) || v["requestTimeoutMillis"] != float64(ms/6) || v["redeemTimeoutMillis"] != float64(ms/3-2000) {
			t.Fatal(v)
		}
		if !strings.Contains(string(b), "issuer: ROOT") || v["maxConnections"] != float64(1000) {
			t.Fatal("material changed")
		}
	}
	for _, ms := range []int{0, 9999, 120001} {
		if _, _, e := projectNetworkBudget(raw, broker, ms); e == nil {
			t.Fatal("invalid budget")
		}
	}
	if _, _, e := projectNetworkBudget(raw, []byte("missing authorization"), 30000); e == nil {
		t.Fatal("unrecognized source")
	}
}
