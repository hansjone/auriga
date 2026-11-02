package renderer_test

import (
	"encoding/json"
	"os/exec"
	"testing"

	"gopkg.in/yaml.v3"
)

// Evaluate the emitted Loader expression with Node, including the built-in
// network parser. Only interface enumeration is replaced with fixture data.
func TestDockerListenerExpression(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required to verify the DSH Loader expression")
	}
	raw, err := renderCordis(t, baseSpec(), "")
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	row := document.Content[0].Content[1]
	var host *yaml.Node
	for i := 0; i < len(row.Content); i += 2 {
		if row.Content[i].Value != "config" {
			continue
		}
		config := row.Content[i+1]
		for j := 0; j < len(config.Content); j += 2 {
			if config.Content[j].Value == "host" {
				host = config.Content[j+1]
			}
		}
	}
	if host == nil || host.Tag != "!!js" {
		t.Fatalf("Docker listener must be a Loader expression: %#v", host)
	}
	const script = `const vm = require('node:vm'); const interfaces = JSON.parse(process.argv[2]); const actual = process.getBuiltinModule.bind(process); try { const address = vm.runInNewContext(process.argv[1], {process: {getBuiltinModule: name => name === 'node:os' ? {networkInterfaces: () => interfaces} : actual(name)}}, {timeout: 1000}); console.log(JSON.stringify({address})); } catch { console.log(JSON.stringify({failed: true})); }`
	tests := []struct {
		name, interfaces, want string
	}{
		{"isolated-ipv4", `{"lo":[{"internal":true,"family":"IPv4","address":"127.0.0.1"}],"eth0":[{"internal":false,"family":"IPv4","address":"192.0.2.7"},{"internal":false,"family":"IPv6","address":"fe80::2"}]}`, "192.0.2.7"},
		{"duplicate-address", `{"eth0":[{"internal":false,"family":"IPv4","address":"192.0.2.7"},{"internal":false,"family":"IPv4","address":"192.0.2.7"}]}`, "192.0.2.7"},
		{"no-interface", `{}`, ""},
		{"loopback-only", `{"lo":[{"internal":true,"family":"IPv4","address":"127.0.0.1"}]}`, ""},
		{"ipv6-only", `{"eth0":[{"internal":false,"family":"IPv6","address":"2001:db8::2"}]}`, ""},
		{"ambiguous", `{"eth0":[{"internal":false,"family":"IPv4","address":"192.0.2.7"}],"eth1":[{"internal":false,"family":"IPv4","address":"198.51.100.8"}]}`, ""},
		{"invalid-and-unspecified", `{"eth0":[{"internal":false,"family":"IPv4","address":"0.0.0.0"},{"internal":false,"family":"IPv4","address":"hostname.example"},{"internal":false,"family":"IPv4","address":"127.0.0.2"}]}`, ""},
		{"missing-list", `{"eth0":null}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := exec.Command(node, "-e", script, host.Value, tt.interfaces).CombinedOutput()
			if err != nil {
				t.Fatalf("Node expression evaluation: %v: %s", err, out)
			}
			var result struct {
				Address string `json:"address"`
				Failed  bool   `json:"failed"`
			}
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatal(err)
			}
			if result.Address != tt.want || result.Failed != (tt.want == "") {
				t.Fatalf("result=%s, want address=%q", out, tt.want)
			}
		})
	}
}
