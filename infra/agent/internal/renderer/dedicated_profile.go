package renderer

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

const DedicatedModule = "file:///opt/auriga-dsh/src/dedicated/plugin.js"
const DedicatedBaselineDigest = "sha256:52ad8d928ddbde5972bbedfcb7d1ba3b6f3c851258feaf502ae285876661ca87"

//go:embed dedicated_prepare.mjs
var dedicatedPrepare []byte

//go:embed dedicated_launcher.mjs
var dedicatedLauncher []byte

// DedicatedPreparation is installation-owned input. The ResourceView and
// binding are the public descriptor projections of the already verified W view.
type DedicatedPreparation struct {
	SchemaVersion  int             `json:"schemaVersion"`
	AttemptID      string          `json:"attemptId"`
	Binding        json.RawMessage `json:"binding"`
	View           json.RawMessage `json:"view"`
	IntentDigest   string          `json:"intentDigest"`
	BaselineDigest string          `json:"baselineDigest"`
	TemplateDigest string          `json:"templateDigest"`
	ImageID        string          `json:"imageId"`
}

// RenderDedicatedPool keeps this separate from the shared whitelist. Each
// attempt has a fresh Agent-private immutable input directory and output mount.
func RenderDedicatedPool(profile []byte, pool, meta string, port uint32, input DedicatedPreparation, intent []byte) (string, error) {
	if port < 1 || port > 65535 || !dedicatedHostConfig.MatchString("/var/lib/auriga/pools/"+pool+"/control/dedicated-host.json") {
		return "", fmt.Errorf("fixed dedicated pool and listener required")
	}
	if err := ValidateDedicatedPoolProfile(profile); err != nil {
		return "", err
	}
	var rows []map[string]any
	if err := yaml.Unmarshal(profile, &rows); err != nil {
		return "", err
	}
	for _, row := range rows {
		config, _ := row["config"].(map[string]any)
		switch row["id"] {
		case "auriga-dsh":
			config["configFile"] = "/var/lib/auriga/pools/" + pool + "/control/dedicated-host.json"
		case "webserver":
			config["host"], config["port"] = "0.0.0.0", port
		}
	}
	logical, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(logical)
	input.TemplateDigest, input.BaselineDigest = "sha256:"+hex.EncodeToString(sum[:]), DedicatedBaselineDigest
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	files := []struct {
		name  string
		bytes []byte
	}{
		{"dedicated-profile.json", logical}, {"dedicated-input.json", raw}, {"dedicated-intent.json", intent},
		{"dedicated-prepare.mjs", dedicatedPrepare}, {"dedicated-launcher.mjs", dedicatedLauncher},
		{"resource-supervisor.mjs", resourceSupervisor}, {"resource_plugins.mjs", resourcePlugins},
	}
	if err := os.MkdirAll(meta, 0755); err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, file := range files {
		_, _ = hash.Write([]byte(file.name + "\x00"))
		_, _ = hash.Write(file.bytes)
		_, _ = hash.Write([]byte{0})
		path := filepath.Join(meta, file.name)
		if old, err := os.ReadFile(path); err == nil {
			if !bytes.Equal(old, file.bytes) {
				return "", fmt.Errorf("dedicated preparation inputs changed")
			}
		} else if !os.IsNotExist(err) {
			return "", err
		} else if err = writeAtomic(path, file.bytes, 0444, nil, Options{}); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

var dedicatedHostConfig = regexp.MustCompile(`^/var/lib/auriga/pools/[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}/control/dedicated-host\.json$`)

// ValidateDedicatedPoolProfile mirrors the frozen 2819 baseline seal, not the
// shared allowlist. It approves no resource/user layer and no arbitrary import.
func ValidateDedicatedPoolProfile(raw []byte) error {
	if len(raw) > 1024*1024 {
		return fmt.Errorf("dedicated profile budget exceeded")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	count := 0
	var literal func(*yaml.Node, int) error
	literal = func(n *yaml.Node, depth int) error {
		count++
		if count > 16384 || depth > 32 || n.Kind == yaml.AliasNode {
			return fmt.Errorf("nonliteral dedicated profile")
		}
		switch n.Tag {
		case "", "!!map", "!!seq", "!!str", "!!int", "!!bool", "!!null":
		default:
			return fmt.Errorf("nonliteral dedicated profile tag")
		}
		for _, child := range n.Content {
			if err := literal(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := literal(&doc, 0); err != nil {
		return err
	}
	var closed []poolProfileRow
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&closed); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple dedicated profile documents")
	}
	if len(closed) != 83 {
		return fmt.Errorf("complete dedicated profile requires 83 rows")
	}
	var rows []map[string]any
	if err := yaml.Unmarshal(raw, &rows); err != nil {
		return err
	}
	ids := map[string]bool{}
	var host, listener, model map[string]any
	for _, row := range rows {
		id, ok := row["id"].(string)
		if !ok || !poolRowID.MatchString(id) || ids[id] {
			return fmt.Errorf("duplicate or invalid dedicated role")
		}
		ids[id] = true
		cfg, _ := row["config"].(map[string]any)
		switch id {
		case "auriga-dsh":
			if row["name"] != DedicatedModule {
				return fmt.Errorf("fixed dedicated entry required")
			}
			host = cfg
		case "webserver":
			listener = cfg
		case "default-model":
			model = cfg
		}
	}
	file, _ := host["configFile"].(string)
	port, ok := listener["port"].(int)
	name, nameOK := model["model"].(string)
	if !dedicatedHostConfig.MatchString(file) || !ok || port < 1 || port > 65535 || (listener["host"] != "127.0.0.1" && listener["host"] != "0.0.0.0") || !nameOK || name == "" || len(name) > 128 {
		return fmt.Errorf("invalid dedicated renderer fields")
	}
	host["configFile"] = "/var/lib/auriga/pools/00000000-0000-4000-8000-000000000001/control/dedicated-host.json"
	listener["host"], listener["port"] = "127.0.0.1", 7860
	model["model"] = "gpt-6.1-sol"
	var jsonBytes bytes.Buffer
	encoder := json.NewEncoder(&jsonBytes)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rows); err != nil {
		return err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(jsonBytes.Bytes(), []byte("\n")))
	if "sha256:"+hex.EncodeToString(sum[:]) != DedicatedBaselineDigest {
		return fmt.Errorf("dedicated baseline seal mismatch")
	}
	return nil
}
