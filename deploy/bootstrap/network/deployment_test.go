package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func serviceBlock(t *testing.T, raw, name string) string {
	t.Helper()
	_, block, ok := strings.Cut(raw, "\n  "+name+":\n")
	if !ok {
		t.Fatalf("service absent: %s", name)
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if len(line) > 0 && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "#") {
			return strings.Join(lines[:i], "\n")
		}
	}
	return block
}

func TestDeploymentNetworkIsolation(t *testing.T) {
	for _, recipe := range []string{"compose", "nas"} {
		t.Run(recipe, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", recipe, "docker-compose.yml"))
			if err != nil {
				t.Fatal(err)
			}
			raw := strings.ReplaceAll(string(content), "\r\n", "\n")
			for service, files := range map[string][]string{
				"platform":       {"platform.properties", "gateway.secret"},
				"network-broker": {"broker.conf", "broker.pem", "broker.key", "server-ca.pem"},
			} {
				block := serviceBlock(t, raw, service)
				if service == "platform" {
					if !strings.Contains(block, "/network-platform:/etc/auriga/network:ro") || strings.Contains(block, "/network:/etc/auriga/network:ro") || strings.Contains(block, "/network/platform.properties:") {
						t.Fatal("platform must mount only its real private credential directory")
					}
					continue
				}
				var mounts []string
				for _, line := range strings.Split(block, "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "- ") && strings.Contains(line, ":/etc/auriga/network") {
						mounts = append(mounts, line)
					}
				}
				if len(mounts) != len(files) {
					t.Fatalf("%s excessive/missing mounts: %v", service, mounts)
				}
				for _, file := range files {
					found := false
					for _, mount := range mounts {
						if strings.Contains(mount, "/network/"+file+":/etc/auriga/network/"+file+":ro") {
							found = true
						}
					}
					if !found {
						t.Fatalf("%s missing least-privilege mount %s", service, file)
					}
				}
			}
			for _, service := range []string{"network", "network-broker"} {
				block := serviceBlock(t, raw, service)
				for _, setting := range []string{"user: '10001:10001'", "read_only: true"} {
					if !strings.Contains(block, setting) {
						t.Fatalf("%s missing %s", service, setting)
					}
				}
				security := block
				if strings.Contains(block, "<<: *cap_drop_all") {
					security += raw[:strings.Index(raw, "\nservices:")]
				}
				for _, setting := range []string{"cap_drop: [ALL]", "no-new-privileges:true"} {
					if !strings.Contains(security, setting) {
						t.Fatalf("%s missing %s", service, setting)
					}
				}
				if strings.Contains(block, "cap_add:") {
					t.Fatalf("%s regains capabilities", service)
				}
			}
			for _, name := range []string{"platform.properties", "gateway.secret", "broker.conf", "broker.pem", "broker.key", "server-ca.pem"} {
				if !strings.Contains("|"+strings.Join(append(append([]string{}, Files...), CredentialFiles...), "|")+"|", "|"+name+"|") {
					t.Fatalf("unmanaged mount: %s", name)
				}
			}
		})
	}
}
