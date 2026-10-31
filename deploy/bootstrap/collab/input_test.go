package collab_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/collab"
	"github.com/hansjone/auriga/deploy/bootstrap/collab/testfixture"
)

func TestApprovedInputAndInstalledState(t *testing.T) {
	const tenant = "00000000-0000-0000-0000-000000000001"
	for _, mutation := range []string{"valid", "tenant", "state", "missing-key", "wrong-root", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			path := testfixture.Write(t, tenant)
			var doc map[string]any
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "tenant":
				doc["tenantId"] = "00000000-0000-0000-0000-000000000002"
			case "state":
				if err := os.WriteFile(doc["stateFile"].(string), []byte(`{"version":"1"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				delete(doc["privateFiles"].(map[string]any), "approved-0")
			case "wrong-root":
				doc["rootFingerprint"] = "sha256:invalid"
			case "symlink":
				link := path + ".link"
				if err := os.Symlink(path, link); err != nil {
					t.Skip("symlink unavailable")
				}
				path = link
			}
			if mutation != "symlink" {
				raw, err = json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := collab.Read(path, tenant)
			defer c.Clear()
			if err == nil {
				err = c.CheckState()
			}
			if (err == nil) != (mutation == "valid") {
				t.Fatalf("approval result %v", err)
			}
		})
	}
}
