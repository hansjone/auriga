package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestComposeDirectHTTPSUmask077(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Change umask only in the child; parallel Go tests keep their own mask.
	output, err := exec.Command("sh", "-c", `umask 077; exec "$1" -test.v -test.run '^TestComposeDirectHTTPSCLI$'`, "sh", binary).CombinedOutput()
	if err != nil {
		t.Fatalf("umask077 HTTPS generation/reentry: %v %s", err, output)
	}
	for _, mode := range []string{"domain", "ip"} {
		if !strings.Contains(string(output), "--- PASS: TestComposeDirectHTTPSCLI/"+mode+" (") {
			t.Fatalf("missing HTTPS %s positive control: %s", mode, output)
		}
	}
}
