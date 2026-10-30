package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// These are independent operator inputs, never inferred from the bundle.
type runtimeInputs struct{ bundle, root, image string }

func (r runtimeInputs) enabled() bool { return r.bundle != "" || r.root != "" || r.image != "" }

func (r runtimeInputs) validate(memory int64) error {
	if !r.enabled() {
		return nil
	}
	if r.bundle == "" || r.root == "" || r.image == "" {
		return fmt.Errorf("runtime-bundle, runtime-root and runtime-image must be supplied together")
	}
	if memory < 256 {
		return fmt.Errorf("runtime template requires instance-max-memory-mb >=256 for WorkspaceTemplates")
	}
	return nil
}

func (r runtimeInputs) verify() (*runtimebundle.Material, error) {
	if !r.enabled() {
		return nil, nil
	}
	return runtimebundle.Verify(runtimebundle.VerifyOptions{Dir: r.bundle, TrustedRootPath: r.root,
		Policy: runtimebundle.Policy{OS: "linux", Arch: "amd64", AllowedImage: r.image}})
}

func runtimeProofDirectory(m *runtimebundle.Material) string {
	return "runtime-proofs/" + strings.TrimPrefix(m.Runtime().GetReleaseEnvelope().GetImageDigest(), "sha256:")
}

func runtimeMaterialSet(m *runtimebundle.Material, https ...bool) materials.MaterialSet {
	set := nasMaterialSet(https...)
	if m == nil {
		return set
	}
	proof := runtimeProofDirectory(m)
	set.PublicFiles = append(set.PublicFiles, "templates/real-dsh.pb", "runtime-keys/root.pb", proof+"/manifest.json", proof+"/config.json")
	set.PublicFileLimits = map[string]int64{proof + "/manifest.json": 4 << 20, proof + "/config.json": 4 << 20}
	return set
}

func nasInputDigest(raw []byte, peer, subnet string, registry int, lanIP, lanSubnet string, memory int64, r runtimeInputs, m *runtimebundle.Material, https ...string) string {
	h := sha256.New()
	_, _ = h.Write(raw)
	_, _ = fmt.Fprintf(h, "\n%s\n%s\n%d\n%s\n%s\n%d", peer, subnet, registry, lanIP, lanSubnet, memory)
	// Old receipts must not silently retain the pre-WSS edge configuration.
	input := "http-or-proxy"
	if len(https) > 0 {
		input = https[0]
	}
	_, _ = fmt.Fprint(h, "\nnetwork-wss-v2-https-litellm-v1\n"+input)
	// Runtime mode also binds the independently supplied locators and snapshot.
	if m != nil {
		inputs, _ := json.Marshal([]string{r.bundle, r.root, r.image})
		_, _ = fmt.Fprintf(h, "\nruntime-v1:%x", inputs)
		for _, b := range [][]byte{m.RuntimeBytes(), m.RootBytes(), m.Manifest(), m.Config()} {
			_, _ = fmt.Fprintf(h, "\n%x", sha256.Sum256(b))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeRuntimeMaterials(out string, m *runtimebundle.Material, memory int64) error {
	if m == nil {
		return nil
	}
	// This public file was just created by this installation, under the guard;
	// publisher material supplies neither this key nor any installation secret.
	raw, err := os.ReadFile(filepath.Join(out, "identity-keys.json"))
	if err != nil {
		return err
	}
	var identity struct {
		Keys []struct{ Kid, Alg, X string }
	}
	if err := json.Unmarshal(raw, &identity); err != nil || len(identity.Keys) != 1 {
		return fmt.Errorf("invalid installation public identity")
	}
	key := identity.Keys[0]
	public, err := base64.RawURLEncoding.DecodeString(key.X)
	if err != nil || len(public) != 32 || key.Alg != "EdDSA" || key.Kid == "" {
		return fmt.Errorf("invalid installation public identity")
	}
	spec := &nodev1.InstanceSpec{
		Runtime:   nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Resources: &nodev1.Resources{CpuMillis: 500, MemoryMb: memory, DiskGb: 1, Pids: 128},
		Dsh:       m.Runtime(),
		Profile: &nodev1.Profile{ListenPort: 7860, Patches: []*nodev1.ConfigPatch{
			{Path: "plugins.auriga-dsh.imBots", ValueJson: `"none"`},
			{Path: "plugins.auriga-dsh.terminalSources", ValueJson: `"browser-only"`},
		}},
		Secrets: &nodev1.Secrets{IdentityKeys: []*nodev1.IdentityPublicKey{{Kid: key.Kid, Alg: key.Alg, PublicKey: public}}},
		Labels:  map[string]string{"template": "real-dsh"},
	}
	template, err := proto.MarshalOptions{Deterministic: true}.Marshal(spec)
	if err != nil {
		return err
	}
	proof := runtimeProofDirectory(m)
	if err := os.Mkdir(filepath.Join(out, filepath.FromSlash(proof)), 0o755); err != nil {
		return fmt.Errorf("create runtime proof directory: %w", err)
	}
	for _, leaf := range []struct {
		name string
		raw  []byte
	}{
		{"templates/real-dsh.pb", template}, {"runtime-keys/root.pb", m.RootBytes()},
		{proof + "/manifest.json", m.Manifest()}, {proof + "/config.json", m.Config()},
	} {
		f, err := os.OpenFile(filepath.Join(out, filepath.FromSlash(leaf.name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(leaf.raw)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func writeRuntimePolicy(out, image string, memory int64) {
	// The verified locator has already passed the production image parser. This
	// only separates its repository for the existing prefix policy field.
	repo, _, _ := strings.Cut(image, "@")
	writeFile(filepath.Join(out, "agent-policy.yaml"), []byte(fmt.Sprintf("allowedImageRepos:\n  - %q\nallowedImageRefs:\n  - %q\nmaxCPUMillis: 500\nmaxMemoryMB: %d\nmaxPIDs: 128\nmaxArtifactBytes: 2147483648\n", repo, image, memory)), 0o644)
}
