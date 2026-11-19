package renderer

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed pool_launcher.mjs
var poolLauncher []byte

// RenderPool installs a complete, immutable shared-host configuration. The ports
// module is a separately reviewed installation input supplied by host composition.
func RenderPool(profile, ports []byte, pool, tenant, meta string, port uint32) (string, error) {
	return RenderPoolMode(profile, ports, pool, tenant, "shared", meta, port)
}

func RenderPoolMode(profile, ports []byte, pool, tenant, mode, meta string, port uint32) (string, error) {
	if mode != "shared" && mode != "dedicated" {
		return "", fmt.Errorf("invalid pool mode")
	}
	if err := ValidatePoolProfile(profile); err != nil {
		return "", err
	}
	var rows []map[string]any
	if err := yaml.Unmarshal(profile, &rows); err != nil {
		return "", err
	}
	shared, tools, web, bridge := false, false, false, false
	for _, row := range rows {
		name, _ := row["name"].(string)
		cfg, _ := row["config"].(map[string]any)
		if cfg == nil {
			cfg = map[string]any{}
			row["config"] = cfg
		}
		switch name {
		case "auriga-dsh/shared-browser":
			if mode != "shared" {
				return "", fmt.Errorf("shared browser cannot load in dedicated mode")
			}
		case "auriga-dsh":
			return "", fmt.Errorf("shared profile contains the owner entry")
		case "auriga-dsh/shared":
			shared = true
		case "file:///run/auriga/shared-ports.mjs":
			bridge = true
		case "auriga-dsh/shared-ports":
			bridge = true
			cfg["configFile"] = "/var/lib/auriga/pools/" + pool + "/control/shared-host.json"
		case "@deepseek-ai/dsh-tools":
			if cfg["mode"] != "native" {
				return "", fmt.Errorf("shared tools require native presentation")
			}
			tools = true
		case "@deepseek-ai/dsh-host-webserver":
			if web {
				return "", fmt.Errorf("duplicate shared listener")
			}
			cfg["port"] = port
			cfg["host"] = jsExpr(`(() => { const net = process.getBuiltinModule('node:net'); const addresses = [...new Set(Object.values(process.getBuiltinModule('node:os').networkInterfaces()).flatMap(entries => (entries ?? []).filter(entry => entry.internal === false && entry.family === 'IPv4' && net.isIPv4(entry.address) && !entry.address.startsWith('127.')).map(entry => entry.address)))]; if (addresses.length !== 1) throw new Error('Shared host requires one isolated IPv4 address'); return addresses[0]; })()`)
			web = true
		}
	}
	if !shared || !tools || !web || !bridge {
		return "", fmt.Errorf("incomplete shared host composition")
	}
	raw, err := yaml.Marshal(rows)
	if err != nil {
		return "", err
	}
	binding, err := json.Marshal(map[string]any{"schemaVersion": 1, "physicalInstanceId": pool, "tenantId": tenant, "mode": mode})
	if err != nil {
		return "", err
	}
	files := []struct {
		name string
		body []byte
	}{
		{"pool-profile.yml", raw}, {"shared-ports.mjs", ports}, {"pool-binding.json", binding},
		{"pool-launcher.mjs", poolLauncher}, {"resource-supervisor.mjs", resourceSupervisor},
		{"resource_plugins.mjs", resourcePlugins},
	}
	hash := sha256.New()
	if err := os.MkdirAll(meta, 0755); err != nil {
		return "", err
	}
	for _, file := range files {
		_, _ = hash.Write([]byte(file.name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(file.body)
		_, _ = hash.Write([]byte{0})
		path := filepath.Join(meta, file.name)
		if old, err := os.ReadFile(path); err == nil {
			if string(old) != string(file.body) {
				return "", fmt.Errorf("running pool configuration requires explicit maintenance")
			}
		} else if os.IsNotExist(err) {
			if err := writeAtomic(path, file.body, 0444, nil, Options{}); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
