package relayhttp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdempotencyHeaderUnicodeAndInvalidValues(t *testing.T) {
	for _, c := range []struct {
		name  string
		keys  []string
		valid bool
	}{
		{"omitted", nil, true},
		{"han85", []string{strings.Repeat("中", 85)}, true},
		{"han86", []string{strings.Repeat("中", 86)}, true},
		{"han128", []string{strings.Repeat("中", 128)}, true},
		{"fourbyte128", []string{strings.Repeat("😀", 128)}, true},
		{"129", []string{strings.Repeat("中", 129)}, false},
		{"badUTF8", []string{string([]byte{0xff})}, false},
		{"duplicate", []string{"one", "two"}, false},
		// Preserve the reviewed postHeaders behavior, without extending its
		// restrictions. HTTP wire syntax is separately enforced by net/http.
		{"empty", []string{""}, true},
		{"nul", []string{"a\x00b"}, true},
		{"tab", []string{"a\tb"}, true},
		{"del", []string{"a\x7fb"}, true},
		{"unicodeControl", []string{"a\u0085b"}, true},
		{"comma", []string{"a,b"}, false},
		{"cr", []string{"a\rb"}, false},
		{"lf", []string{"a\nb"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", chatPath, nil)
			r.Header.Set("Content-Type", "application/json")
			if c.keys != nil {
				r.Header["Idempotency-Key"] = c.keys
			}
			key, err := postHeaders(r)
			if c.valid {
				if err != nil {
					t.Fatal(err)
				}
				if len(c.keys) == 1 && key != c.keys[0] {
					t.Fatal("original key transformed")
				}
			} else if err == nil || err.Error() != "common.validation_failed" {
				t.Fatalf("invalid key accepted: %v", err)
			}
		})
	}
}

func TestHTTPInvalidUTF8KeysRejectedBeforeExecution(t *testing.T) {
	// The zero-value Core from testConfig cannot execute: a missed boundary
	// rejection fails the real HTTP request rather than reaching a fake success.
	h, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	f := &fixture{raw: "original"}
	for _, c := range []struct {
		name string
		keys []string
	}{
		{"ascii129", []string{strings.Repeat("a", 129)}},
		{"han129", []string{strings.Repeat("中", 129)}},
		{"fourbyte129", []string{strings.Repeat("😀", 129)}},
		{"badUTF8", []string{string([]byte{0xff})}},
		{"duplicate", []string{"same", "same"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			keyError(t, f.keyRequest(t.Context(), s, c.keys, plainRequest), 400, "common.validation_failed")
		})
	}
}
