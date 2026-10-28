package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

const activationDirectory = "collab-activation"

type activationReceipt struct {
	Version   int    `json:"version"`
	Base      string `json:"baseReceiptSha256"`
	Identity  string `json:"baseIdentitySha256"`
	Input     string `json:"inputSha256"`
	Materials string `json:"materialsSha256"`
}

func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func activationBase(out, expectedReceipt, expectedIdentity string) (deploymentconfig.Config, error) {
	var empty deploymentconfig.Config
	if _, err := os.Lstat(filepath.Join(out, "collab-installation")); !os.IsNotExist(err) {
		return empty, fmt.Errorf("activation requires base without N1")
	}
	receipt, err := collabRead(filepath.Join(out, "iam-installation", "bootstrap.receipt"), 65536, true)
	if err != nil {
		return empty, fmt.Errorf("activation base receipt refused")
	}
	identity, err := collabRead(filepath.Join(out, "identity-keys.json"), 65536, false)
	if err != nil {
		return empty, fmt.Errorf("activation base identity refused")
	}
	if digestBytes(receipt) != expectedReceipt || digestBytes(identity) != expectedIdentity {
		return empty, fmt.Errorf("activation base checkpoint mismatch")
	}
	lines := strings.Split(string(receipt), "\n")
	if len(lines) != 3 || len(lines[0]) != 64 {
		return empty, fmt.Errorf("activation base receipt invalid")
	}
	if _, _, reuse, err := existingIdentityMaterials(out, lines[0]); err != nil || !reuse {
		return empty, fmt.Errorf("activation base seal invalid")
	}
	set, err := composeMaterialSet(out, true)
	if err != nil {
		return empty, err
	}
	for _, name := range set.PublicFiles {
		path := filepath.Join(out, filepath.FromSlash(name))
		info, e := os.Lstat(path)
		if e != nil || !collabSourceMetadata(info, false) {
			return empty, fmt.Errorf("activation base metadata refused")
		}
		for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
			info, e := os.Lstat(parent)
			if e != nil || !activationBaseDirectory(info) {
				return empty, fmt.Errorf("activation base directory metadata refused")
			}
			if parent == filepath.Clean(out) {
				break
			}
			if parent == filepath.Dir(parent) {
				return empty, fmt.Errorf("activation base root refused")
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "access.json"))
	if err != nil {
		return empty, fmt.Errorf("activation access unavailable")
	}
	access, err := deploymentconfig.Load(raw)
	if err != nil || access.PortalOrigin().Scheme != "https" {
		return empty, fmt.Errorf("activation requires sealed HTTPS base")
	}
	return access, nil
}

func activationFiles() []string {
	files := []string{"edge.config.json", "pki/ca.crt", "pki/edge-collab.crt", "pki/edge-collab.key", "network/runtime.json", "network/protocol.key", "network-platform/gateway.secret", "network-platform/collab.secret", "network-platform/platform.properties", "collab-installation/input.json"}
	for _, name := range collabLeaves {
		files = append(files, "collab-installation/"+name)
	}
	return files
}

func activationDigest(dir string) (string, error) {
	want := map[string]bool{"activation.receipt": true}
	for _, name := range activationFiles() {
		want[name] = true
		for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			want[parent] = true
		}
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("activation source unavailable")
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil || (rel != "." && !want[filepath.ToSlash(rel)]) {
			return fmt.Errorf("unknown activation source")
		}
		info, e := os.Lstat(path)
		if e != nil || !activationMetadata(info, entry.IsDir()) {
			return fmt.Errorf("activation source metadata refused")
		}
		return nil
	}); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, name := range activationFiles() {
		raw, err := collabRead(filepath.Join(dir, filepath.FromSlash(name)), 1<<20, true)
		if err != nil {
			return "", fmt.Errorf("activation materials incomplete")
		}
		sum := sha256.Sum256(raw)
		clear(raw)
		_, _ = fmt.Fprintf(h, "%s:%x\n", name, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkActivation(out string) (*activationReceipt, error) {
	if err := activationIdle(out); err != nil {
		return nil, err
	}
	dir := filepath.Join(out, activationDirectory)
	raw, err := collabRead(filepath.Join(dir, "activation.receipt"), 65536, true)
	if err != nil {
		return nil, fmt.Errorf("activation is not sealed")
	}
	var receipt activationReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.Version != 1 || len(receipt.Input) != 64 {
		return nil, fmt.Errorf("activation receipt invalid")
	}
	if _, err := activationBase(out, receipt.Base, receipt.Identity); err != nil {
		return nil, err
	}
	digest, err := activationDigest(dir)
	if err != nil || digest != receipt.Materials {
		return nil, fmt.Errorf("activation seal changed or incomplete")
	}
	return &receipt, nil
}

func activationIdle(out string) error {
	for _, name := range []string{".collab-activation.lock", ".collab-activation.pending"} {
		if _, err := os.Lstat(filepath.Join(out, name)); !os.IsNotExist(err) {
			return fmt.Errorf("activation interrupted or locked; no automatic repair")
		}
	}
	return nil
}

func runActivation(out, input, baseHash, identityHash string, check, validate, stopped bool) error {
	if check {
		if input != "" || baseHash != "" || identityHash != "" || validate || stopped {
			return fmt.Errorf("invalid activation check arguments")
		}
		_, err := checkActivation(out)
		return err
	}
	access, err := activationBase(out, baseHash, identityHash)
	if err != nil {
		return err
	}
	c, err := readCollabInput(input, access)
	if err != nil || c == nil {
		return fmt.Errorf("activation input refused")
	}
	defer c.clear()
	if err := checkActivationState(c); err != nil {
		return err
	}
	inputHash := c.digest()
	if !validate && runtime.GOOS != "linux" {
		return fmt.Errorf("collab activation requires Linux; use -validate-access")
	}
	if err := activationIdle(out); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(out, activationDirectory)); err == nil {
		receipt, e := checkActivation(out)
		if e != nil {
			return e
		}
		if receipt.Input != inputHash || receipt.Base != baseHash || receipt.Identity != identityHash {
			return fmt.Errorf("activation inputs changed; first activation only")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("activation directory refused")
	}
	if validate {
		return nil
	}
	if !stopped {
		return fmt.Errorf("stop platform edge network before activation")
	}
	lockPath := filepath.Join(out, ".collab-activation.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("activation lock refused; inspect interrupted activation")
	}
	original, _ := lock.Stat()
	defer func() {
		_ = lock.Close()
		if current, e := os.Lstat(lockPath); e == nil && os.SameFile(original, current) {
			_ = os.Remove(lockPath)
		}
	}()
	stage := filepath.Join(out, ".collab-activation.pending")
	if err := os.Mkdir(stage, 0700); err != nil {
		return fmt.Errorf("activation pending directory refused; no automatic repair")
	}
	for _, name := range []string{"pki", "network", "network-platform"} {
		if err := os.Mkdir(filepath.Join(stage, name), 0700); err != nil {
			return err
		}
	}
	for _, name := range []string{"pki/ca.crt", "network-platform/gateway.secret"} {
		raw, e := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if e != nil {
			return e
		}
		writeFile(filepath.Join(stage, filepath.FromSlash(name)), raw, 0600)
		clear(raw)
	}
	if err := writeCollabFrom(stage, out, c, access.Issuer()); err != nil {
		return err
	}
	var edge map[string]any
	raw, err := os.ReadFile(filepath.Join(out, "edge.config.json"))
	if err != nil || json.Unmarshal(raw, &edge) != nil {
		return fmt.Errorf("activation edge source refused")
	}
	upstreams, ok := edge["upstreams"].(map[string]any)
	if !ok {
		return fmt.Errorf("activation edge upstreams invalid")
	}
	upstreams["collab"] = "https://172.29.240.1:8085" // gitleaks:allow disposable M2 bridge
	edge["collabHop"] = map[string]string{"certFile": "/etc/auriga/collab-edge/edge-collab.crt", "keyFile": "/etc/auriga/collab-edge/edge-collab.key", "caFile": "/etc/auriga/collab-edge/ca.crt", "serverName": "collab.network.internal"}
	writeJSON(filepath.Join(stage, "edge.config.json"), edge, 0600)
	if err := writeActivationInput(stage, c); err != nil {
		return err
	}
	for _, name := range activationFiles() {
		if err := os.Chmod(filepath.Join(stage, filepath.FromSlash(name)), 0600); err != nil {
			return err
		}
	}
	digest, err := activationDigest(stage)
	if err != nil {
		return err
	}
	// Recheck every base byte before the separate activation becomes selectable.
	if _, err := activationBase(out, baseHash, identityHash); err != nil {
		return err
	}
	receipt := activationReceipt{1, baseHash, identityHash, inputHash, digest}
	b, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(stage, "activation.receipt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(stage, filepath.Join(out, activationDirectory)); err != nil {
		return fmt.Errorf("activation commit refused")
	}
	return nil
}

// Compare public installed state to the approved manifest; Java remains the
// root-signature/schema/PG authority. This never constructs or signs a state.
func checkActivationState(c *collabInput) error {
	var signed struct {
		Keyset struct {
			Issued  string `json:"issued_at"`
			Version string `json:"keyset_version"`
			Keys    []map[string]any
		}
	}
	var state struct {
		Version string
		Issued  string `json:"issued_at"`
		Keys    []map[string]any
	}
	if json.Unmarshal(c.files["signed.json"], &signed) != nil || len(signed.Keyset.Keys) != 3 {
		return fmt.Errorf("activation manifest refused")
	}
	decoder := json.NewDecoder(bytes.NewReader(c.files["state.json"]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.Version != signed.Keyset.Version || state.Issued != signed.Keyset.Issued || len(state.Keys) != 3 {
		return fmt.Errorf("activation state mismatch")
	}
	for i, key := range signed.Keyset.Keys {
		expected := map[string]any{"kid": key["kid"], "realm": c.Realm, "purpose": key["purpose"], "state": "ACTIVE", "not_before": key["not_before"], "not_after": key["not_after"], "public_key_spki": key["public_key_spki"]}
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(state.Keys[i])
		if !bytes.Equal(a, b) {
			return fmt.Errorf("activation state mismatch")
		}
	}
	return nil
}

func writeActivationInput(stage string, c *collabInput) error {
	copy := *c
	copy.RootPublicFile = "root.der"
	copy.SignedFile = "signed.json"
	copy.AnnouncementFile = "announcement.json"
	copy.StateFile = "state.json"
	copy.PrivateFiles = map[string]string{}
	var release struct {
		Keyset struct {
			Keys []struct{ Kid, Purpose string }
		}
	}
	if json.Unmarshal(c.files["signed.json"], &release) != nil {
		return fmt.Errorf("activation manifest refused")
	}
	for _, key := range release.Keyset.Keys {
		copy.PrivateFiles[key.Kid] = map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}[key.Purpose]
	}
	writeJSON(filepath.Join(stage, "collab-installation", "input.json"), copy, 0600)
	return nil
}
