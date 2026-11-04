package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestNASRenewalWarnsWithMessageOnlyRecoveryWhenConsumersMissing(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	for id, c := range f.containers {
		if c.Config.Labels["com.docker.compose.service"] == "collab" {
			delete(f.containers, id)
		}
	}
	var out bytes.Buffer
	if err := f.r.queueTick(nil, &out); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	command, _ := result["messageRecoveryCommand"].(string)
	warning, _ := result["warning"].(string)
	if !strings.Contains(command, "start-collab-network.sh") || !strings.Contains(command, "AURIGA_DATA_ROOT=") || !strings.Contains(warning, command) {
		t.Fatal(out.String())
	}
	if strings.Contains(command, "scripts/start.sh") {
		t.Fatal("message recovery restarts core")
	}
}

func TestNASMessageRecoveryCommandPreservesQuotedPaths(t *testing.T) {
	r := &nasRotation{data: "/srv/data $(printf BAD)'s directory", recipe: "/srv/recipe's directory"}
	command := `sh() { printf '%s\n' "$AURIGA_DATA_ROOT" "$1"; }; ` + r.messageRecoveryCommand()
	output, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("recovery command cannot be parsed: %v: %s", err, output)
	}
	want := r.data + "\n" + r.recipe + "/scripts/start-collab-network.sh\n"
	if string(output) != want {
		t.Fatalf("recovery path was expanded or split: %q", output)
	}
}

func TestNASOperatorPauseDoesNotRequestAutomaticMessageRecovery(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	if err := f.r.schedulerControl("pause"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := f.r.queueTick(nil, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "messageRecoveryCommand") {
		t.Fatal("operator maintenance was overridden")
	}
}
