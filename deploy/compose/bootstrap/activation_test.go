package main

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestActivationRejectsUnsealedBase(t *testing.T) {
	if _, err := activationBase(t.TempDir(), "", ""); err == nil {
		t.Fatal("unsealed base accepted")
	}
}

type activationFixtureData struct {
	binary, out, input, base, identity string
	args                               []string
}

func activationFixture(t *testing.T) activationFixtureData {
	t.Helper()
	input, _ := collabFixture(t)
	root, e := os.MkdirTemp("", "auriga-activation-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if e = os.Chmod(root, 0755); e != nil {
		t.Fatal(e)
	}
	binary := os.Getenv("AURIGA_ACTIVATION_TEST_BOOTSTRAP")
	if binary == "" {
		binary = filepath.Join(root, "bootstrap")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		if b, e := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); e != nil {
			t.Fatalf("build: %v %s", e, b)
		}
	}
	a := customAccess("domain")
	a.PortalOrigin.Scheme = "https"
	a.AdminOrigin.Scheme = "https"
	a.IssuerOrigin.Scheme = "https"
	a.Listeners.Portal.Tls = true
	a.Listeners.Admin.Tls = true
	a.InsecureHttpAcknowledged = false
	a.Instances.AccessInstancesSubdomain.Scheme = "https"
	write := func(name string, raw []byte) string {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	raw, _ := json.Marshal(a)
	access := write("access.json", raw)
	ca, cert, _ := mustCA()
	chain, key := mustIssue(ca, cert, "browser", "auriga://test/browser", []string{a.PortalOrigin.Host, a.AdminOrigin.Host, "*." + a.Instances.AccessInstancesSubdomain.ParentDomain}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	certFile, keyFile := write("browser.crt", chain), write("browser.key", key)
	proofs, image := runtimeProofFixture(t, "real")
	out := filepath.Join(root, "var")
	if b, e := exec.Command(binary, "-out", out, "-access-file", access, "-edge-container", "public-test-edge", "-fixture", "real", "-image", image, "-runtime-proofs", proofs, "-https-cert-file", certFile, "-https-key-file", keyFile).CombinedOutput(); e != nil {
		t.Fatalf("base CLI: %v %s", e, b)
	}
	receipt, e := os.ReadFile(filepath.Join(out, "iam-installation", "bootstrap.receipt"))
	if e != nil {
		t.Fatal(e)
	}
	identity, e := os.ReadFile(filepath.Join(out, "identity-keys.json"))
	if e != nil {
		t.Fatal(e)
	}
	f := activationFixtureData{binary: binary, out: out, input: input, base: digestBytes(receipt), identity: digestBytes(identity)}
	f.args = []string{"-activate-collab", "-out", out, "-network-collab-file", input, "-base-receipt-sha256", f.base, "-base-identity-sha256", f.identity}
	return f
}

func originalBase(t *testing.T, out string) map[string]string {
	t.Helper()
	result := map[string]string{}
	e := filepath.WalkDir(out, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(out, path)
		if e != nil {
			return e
		}
		if strings.HasPrefix(rel, "collab-activation") || strings.HasPrefix(rel, ".collab-activation") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		value := fmt.Sprintf("%o", info.Mode())
		if !entry.IsDir() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			value += digestBytes(raw)
		}
		result[rel] = value
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return result
}

func assertBase(t *testing.T, out string, before map[string]string) {
	t.Helper()
	after := originalBase(t, out)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("sealed base bytes/permissions/path set changed")
	}
}

func TestActivationInputAndWindowsGate(t *testing.T) {
	f := activationFixture(t)
	before := originalBase(t, f.out)
	if b, e := exec.Command(f.binary, append(f.args, "-validate-access")...).CombinedOutput(); e != nil {
		t.Fatalf("valid input: %v %s", e, b)
	}
	for _, name := range []string{"checkpoint", "identity", "bad-state", "tenant", "realm", "minimum", "fingerprint", "missing-secret"} {
		t.Run(name, func(t *testing.T) {
			raw, e := os.ReadFile(f.input)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = os.WriteFile(f.input, raw, 0600) }()
			var input map[string]any
			_ = json.Unmarshal(raw, &input)
			args := append([]string{}, f.args...)
			switch name {
			case "checkpoint":
				args[len(args)-3] = strings.Repeat("0", 64)
			case "identity":
				args[len(args)-1] = strings.Repeat("0", 64)
			case "bad-state":
				input["stateFile"] = f.input
			case "tenant":
				input["tenantId"] = "00000000-0000-0000-0000-000000000002"
			case "realm":
				input["realm"] = "2"
			case "minimum":
				input["minimumVersion"] = "8"
			case "fingerprint":
				input["rootFingerprint"] = "sha256:" + strings.Repeat("0", 64)
			case "missing-secret":
				delete(input["privateFiles"].(map[string]any), "approved-1")
			}
			changed, _ := json.Marshal(input)
			if e := os.WriteFile(f.input, changed, 0600); e != nil {
				t.Fatal(e)
			}
			if b, e := exec.Command(f.binary, append(args, "-validate-access")...).CombinedOutput(); e == nil {
				t.Fatalf("invalid input accepted: %s", b)
			}
			assertBase(t, f.out, before)
		})
	}
	if runtime.GOOS != "linux" {
		if b, e := exec.Command(f.binary, append(f.args, "-stopped-confirmed")...).CombinedOutput(); e == nil || !strings.Contains(string(b), "requires Linux") {
			t.Fatalf("non-Linux activation: %v %s", e, b)
		}
		assertBase(t, f.out, before)
	}
}

func TestActivationSealAndReentry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux activation only")
	}
	f := activationFixture(t)
	before := originalBase(t, f.out)
	if b, e := exec.Command(f.binary, f.args...).CombinedOutput(); e == nil || !strings.Contains(string(b), "stop platform") {
		t.Fatalf("unstopped activation: %v %s", e, b)
	}
	pending := filepath.Join(f.out, ".collab-activation.pending")
	if e := os.Mkdir(pending, 0700); e != nil {
		t.Fatal(e)
	}
	if b, e := exec.Command(f.binary, append(f.args, "-stopped-confirmed")...).CombinedOutput(); e == nil {
		t.Fatalf("partial activation repaired: %s", b)
	}
	if _, e := os.Stat(pending); e != nil {
		t.Fatal("partial activation removed")
	}
	assertBase(t, f.out, before)
	if e := os.Remove(pending); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if b, e := exec.Command(f.binary, append(f.args, "-stopped-confirmed")...).CombinedOutput(); e != nil {
			t.Fatalf("activate/reenter: %v %s", e, b)
		}
		assertBase(t, f.out, before)
	}
	check := func() error {
		b, e := exec.Command(f.binary, "-check-collab-activation", "-out", f.out).CombinedOutput()
		if e != nil {
			t.Logf("check rejected: %v %s", e, b)
		}
		return e
	}
	if check() != nil {
		t.Fatal("seal refused")
	}
	inputRaw, _ := os.ReadFile(f.input)
	var changedInput map[string]any
	_ = json.Unmarshal(inputRaw, &changedInput)
	changedInput["minimumVersion"] = "6"
	changedRaw, _ := json.Marshal(changedInput)
	if e := os.WriteFile(f.input, changedRaw, 0600); e != nil {
		t.Fatal(e)
	}
	if b, e := exec.Command(f.binary, append(f.args, "-stopped-confirmed")...).CombinedOutput(); e == nil || !strings.Contains(string(b), "inputs changed") {
		t.Fatalf("changed reentry input: %v %s", e, b)
	}
	if e := os.WriteFile(f.input, inputRaw, 0600); e != nil {
		t.Fatal(e)
	}
	assertBase(t, f.out, before)
	activation := filepath.Join(f.out, activationDirectory)
	for _, name := range []string{"network/protocol.key", "network-platform/platform.properties", "collab-installation/state.json", "activation.receipt"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(activation, filepath.FromSlash(name))
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(path, []byte("changed"), 0600); e != nil {
				t.Fatal(e)
			}
			if check() == nil {
				t.Fatal("activation drift accepted")
			}
			if e := os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			assertBase(t, f.out, before)
		})
	}
	for _, name := range []string{".collab-activation.pending", ".collab-activation.lock"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(f.out, name)
			if e := os.WriteFile(p, []byte("interrupted"), 0600); e != nil {
				t.Fatal(e)
			}
			if check() == nil {
				t.Fatal("interruption accepted")
			}
			if e := os.Remove(p); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, name := range []string{"unknown", "missing", "hardlink", "symlink", "owner", "mode"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(activation, "network", "protocol.key")
			raw, _ := os.ReadFile(path)
			switch name {
			case "missing":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			case "unknown":
				path = filepath.Join(activation, "extra.key")
				if e := os.WriteFile(path, []byte("extra"), 0600); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(path, path+".linked"); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Join(activation, "network-platform", "gateway.secret"), path); e != nil {
					t.Fatal(e)
				}
			case "owner":
				if os.Geteuid() != 0 {
					t.Skip("requires root")
				}
				if e := os.Chown(path, 10001, 10001); e != nil {
					t.Fatal(e)
				}
			case "mode":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			}
			if check() == nil {
				t.Fatal("bad activation metadata accepted")
			}
			switch name {
			case "hardlink":
				if e := os.Remove(path + ".linked"); e != nil {
					t.Fatal(e)
				}
			case "unknown":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			case "owner":
				if e := os.Chown(path, os.Getuid(), os.Getgid()); e != nil {
					t.Fatal(e)
				}
			case "mode":
				if e := os.Chmod(path, 0600); e != nil {
					t.Fatal(e)
				}
			case "symlink", "missing":
				if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
	path := filepath.Join(f.out, "identity-keys.json")
	raw, _ := os.ReadFile(path)
	if e := os.WriteFile(path, []byte("changed base"), 0644); e != nil {
		t.Fatal(e)
	}
	if check() == nil {
		t.Fatal("base identity drift accepted")
	}
	if e := os.WriteFile(path, raw, 0644); e != nil {
		t.Fatal(e)
	}
	if check() != nil {
		t.Fatal("restored fixture invalid")
	}
	pki := filepath.Join(f.out, "pki")
	if e := os.Chmod(pki, 0777); e != nil {
		t.Fatal(e)
	}
	if check() == nil {
		t.Fatal("writable base parent accepted")
	}
	if e := os.Chmod(pki, 0755); e != nil {
		t.Fatal(e)
	}
	assertBase(t, f.out, before)
	if os.Geteuid() == 0 {
		activationRuntimeChecks(t, f)
	}
}

func activationRuntimeChecks(t *testing.T, f activationFixtureData) {
	t.Helper()
	runtimeRoot := filepath.Join(f.out, "collab-activation-runtime")
	source := filepath.Join(f.out, activationDirectory)
	project := func(check bool) error {
		args := []string{"../scripts/collab-materials.sh"}
		if check {
			args = append(args, "--check")
		}
		args = append(args, source, runtimeRoot)
		b, e := exec.Command("sh", args...).CombinedOutput()
		if e != nil {
			t.Logf("projection: %v %s", e, b)
		}
		return e
	}
	if project(true) == nil {
		t.Fatal("missing projection accepted")
	}
	if project(false) != nil || project(true) != nil {
		t.Fatal("activation projection failed")
	}
	for _, probe := range []struct{ uid, dir, script string }{{"10002", "collab-runtime/config", `test -r business.der && test -r ../state/state.json && (umask 077; : > ../state/state.json.lock)`}, {"10001", "network", `test -r protocol.key && ! test -r ../collab-runtime/config/business.der`}, {"65532", "collab-edge", `test -r edge.config.json && test -r edge-collab.key && ! test -r ../network/protocol.key`}} {
		cmd := exec.Command("setpriv", "--reuid="+probe.uid, "--regid="+probe.uid, "--clear-groups", "sh", "-c", probe.script)
		cmd.Dir = filepath.Join(runtimeRoot, filepath.FromSlash(probe.dir))
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("UID %s: %v %s", probe.uid, e, b)
		}
	}
	if project(true) != nil {
		t.Fatal("state lock polluted source")
	}
	stateLock := filepath.Join(runtimeRoot, "collab-runtime", "state", "state.json.lock")
	if e := os.Chmod(stateLock, 0644); e != nil {
		t.Fatal(e)
	}
	if project(true) == nil {
		t.Fatal("exposed state lock accepted")
	}
	if e := os.Chmod(stateLock, 0600); e != nil {
		t.Fatal(e)
	}
	key := filepath.Join(runtimeRoot, "network", "protocol.key")
	if e := os.Chmod(key, 0644); e != nil {
		t.Fatal(e)
	}
	if project(true) == nil {
		t.Fatal("runtime exposure accepted")
	}
	if e := os.Chmod(key, 0600); e != nil {
		t.Fatal(e)
	}
	activationLifecycleChecks(t, f, runtimeRoot)
}

func activationLifecycleChecks(t *testing.T, f activationFixtureData, runtimeRoot string) {
	t.Helper()
	root := filepath.Dir(f.out)
	scripts := filepath.Join(root, "scripts")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{scripts, bin} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"compose.sh", "collab-materials.sh", "activate-collab.sh"} {
		b, e := os.ReadFile("../scripts/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(scripts, name), b, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for name, body := range map[string]string{"docker": "#!/bin/sh\nprintf '%s\\n' \"$@\"\n", "go": "#!/bin/sh\nshift 4\nexec \"$AURIGA_ACTIVATION_TEST_BOOTSTRAP\" \"$@\"\n"} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(f.out, "compose.env"), []byte("COMPOSE_FILE=foreign.yml\n"), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(action string) (string, error) {
		cmd := exec.Command("bash", filepath.Join(scripts, "compose.sh"), action)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "AURIGA_ACTIVATION_TEST_BOOTSTRAP="+f.binary)
		b, e := cmd.CombinedOutput()
		return string(b), e
	}
	for _, action := range []string{"up", "start", "stop", "ps", "logs", "config", "bootstrap-registry"} {
		b, e := run(action)
		if e != nil || !strings.Contains(b, "collab-activation.yml") || !strings.Contains(b, "network-collab") || strings.Contains(b, "foreign.yml") {
			t.Fatalf("actual lifecycle %s: %v %s", action, e, b)
		}
	}
	lock := filepath.Join(f.out, ".collab-activation.lock")
	if e := os.WriteFile(lock, []byte("busy"), 0600); e != nil {
		t.Fatal(e)
	}
	if b, e := run("up"); e == nil {
		t.Fatalf("locked activation started: %s", b)
	}
	if b, e := run("activation-stop"); e != nil || !strings.Contains(b, "stop") {
		t.Fatalf("bounded emergency stop: %v %s", e, b)
	}
	if e := os.Remove(lock); e != nil {
		t.Fatal(e)
	}
	key := filepath.Join(runtimeRoot, "network", "protocol.key")
	if e := os.Chmod(key, 0644); e != nil {
		t.Fatal(e)
	}
	if b, e := run("ps"); e == nil {
		t.Fatalf("invalid runtime status accepted: %s", b)
	}
	cmd := exec.Command("bash", filepath.Join(scripts, "activate-collab.sh"), f.input, f.base, f.identity)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "AURIGA_ACTIVATION_TEST_BOOTSTRAP="+f.binary)
	if b, e := cmd.CombinedOutput(); e == nil || !strings.Contains(string(b), "still running") {
		t.Fatalf("running-process activation gate: %v %s", e, b)
	}
}
