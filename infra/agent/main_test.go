package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/app"
)

func TestVersionAndJSONLog(t *testing.T) {
	app.Version = version
	var out, logs bytes.Buffer
	if err := app.RunCLI([]string{"--version"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auriga-agent "+version) {
		t.Fatal(out.String())
	}
	if logs.Len() != 0 {
		t.Fatal("version must not initialize services")
	}
	logs.Reset()
	if err := app.RunCLI(nil, &out, &logs); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["service"] != "agent" {
		t.Fatal(record)
	}
}
