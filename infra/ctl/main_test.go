package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionAndJSONLog(t *testing.T) {
	var out, logs bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auriga-ctl "+version) || logs.Len() != 0 {
		t.Fatal(out.String(), logs.String())
	}
	if err := run(context.Background(), nil, &out, &logs); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["service"] != "ctl" {
		t.Fatal(record)
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"},
		{"dev", "--pg-port=0"},
		{"dev", "--pg-port=70000"},
		{"dev", "--nats-port=-1"},
		{"dev", "unexpected"},
		{"start", "--pg-port=0"},
		{"start", "unexpected"},
		{"stop", "unexpected"},
		{"status", "unexpected"},
	} {
		var out, logs bytes.Buffer
		if err := run(context.Background(), args, &out, &logs); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
