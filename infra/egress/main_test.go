package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionAndJSONLog(t *testing.T) {
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
	if err := run(nil, &out, &logs); err != nil {
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
