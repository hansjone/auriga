package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVersionAndJSONLog(t *testing.T) {
	t.Setenv("AURIGA_EGRESS_DENY_HOSTS", "portal.example.com,admin.example.com")
	var out, logs bytes.Buffer
	if err := run([]string{"--version"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auriga-egress "+version) {
		t.Fatal(out.String())
	}
	if logs.Len() != 0 {
		t.Fatal("version must not initialize services")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runContext(ctx, []string{"--listen", "127.0.0.1:0"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["service"] != "egress" {
		t.Fatal(record)
	}
}

func TestInvalidListenerAndCheckAreBounded(t *testing.T) {
	t.Setenv("AURIGA_EGRESS_DENY_HOSTS", "portal.example.com,admin.example.com")
	var out, logs bytes.Buffer
	if err := run([]string{"--listen", "not-an-address"}, &out, &logs); err == nil {
		t.Fatal("invalid listener accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if err := runContext(ctx, []string{"--check"}, &out, &logs); err == nil {
		t.Fatal("cancelled health request reported success")
	}
}
