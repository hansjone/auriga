package relayui

import (
	"os"
	"testing"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func TestIndependentUIOrigins(t *testing.T) {
	for _, fixture := range []string{"ip-http.json", "domain-proxy.json"} {
		t.Run(fixture, func(t *testing.T) {
			raw, err := os.ReadFile("../../../contracts/deployment/fixtures/" + fixture)
			if err != nil {
				t.Fatal(err)
			}
			access, err := deploymentconfig.Load(raw)
			if err != nil {
				t.Fatal(err)
			}
			ui, err := Resolve(access, "")
			if err != nil || ui.Origin.Port != DefaultPort || ui.Listener.Port != DefaultPort || ui.URL() == access.AdminURL() {
				t.Fatalf("default UI=%+v err=%v", ui, err)
			}
			for _, bad := range []string{access.AdminURL(), access.PortalURL(), "https://user:pass@litellm.example.com", "https://litellm.example.com/ui", "https://litellm.example.com?x=1", "https://litellm.example.com#fragment", "https://127.01:8085", "https://0x7f000001:8085", "https://litellm.example.com:0", "https://litellm.example.com:65536"} {
				if _, err := Resolve(access, bad); err == nil {
					t.Errorf("accepted invalid UI origin %s", bad)
				}
			}
			if access.Mode() == "domain" {
				custom, err := Resolve(access, "https://litellm.example.com")
				if err != nil || custom.URL() != "https://litellm.example.com" || custom.Listener.Port != DefaultPort || custom.Digest() == ui.Digest() {
					t.Fatalf("custom UI=%+v err=%v", custom, err)
				}
			}
		})
	}
}
