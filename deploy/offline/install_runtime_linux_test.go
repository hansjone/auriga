package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This executable is only a protocol/ordering fixture. It deliberately does not
// claim cryptographic verification; production invokes the signed package's
// original runtime-release authority, which has its own real verification tests.
func runtimeInstallProtocolFixture(t *testing.T) (string, installOptions) {
	t.Helper()
	base := t.TempDir()
	pkg := filepath.Join(base, "package")
	tool := filepath.Join(pkg, "deploy-nas/bin/runtime-release")
	if err := os.MkdirAll(filepath.Dir(tool), 0755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nset -eu\n[ \"$#\" = 11 ]\n[ \"$1\" = verify ] && [ \"$2\" = --dir ] && [ -d \"$3\" ]\n[ \"$4\" = --root ] && [ -f \"$5\" ]\n[ \"$6\" = --os ] && [ \"$7\" = linux ] && [ \"$8\" = --arch ] && [ \"$9\" = amd64 ]\nshift 9\n[ \"$1\" = --allow-image ]\nprintf '%s\\n' \"$2\" >>\"$AURIGA_TEST_RUNTIME_VERIFY_LOG\"\n[ \"${AURIGA_TEST_RUNTIME_VERIFY_FAIL:-}\" != 1 ]\n"
	if err := os.WriteFile(tool, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURIGA_TEST_RUNTIME_VERIFY_LOG", filepath.Join(base, "verify.calls"))
	t.Setenv("AURIGA_TEST_RUNTIME_VERIFY_FAIL", "")
	bundle := filepath.Join(base, "approved-bundle")
	for name, value := range map[string]string{
		"runtime.pb":                           "public runtime fixture",
		"runtime-keys/root.pb":                 "public root fixture",
		"runtime-proofs/fixture/manifest.json": "public manifest fixture",
		"runtime-proofs/fixture/config.json":   "public config fixture",
	} {
		path := filepath.Join(bundle, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(base, "operator-root.pb")
	if err := os.WriteFile(root, []byte("independent root fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	return pkg, installOptions{Data: filepath.Join(base, "auriga-data"), Runtime: installRuntimeOptions{Bundle: bundle, Root: root, Image: "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("a", 64)}}
}

func pinRuntimeProtocolFixture(t *testing.T, pkg string, o installOptions) state {
	t.Helper()
	p, err := prepareInstallRuntime(pkg, o, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(p.directory) })
	for _, path := range []string{o.Data, o.Data + ".oneclick"} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	s := state{Data: o.Data, Package: pkg}
	if err := pinInstallRuntime(&s, p); err != nil {
		t.Fatal(err)
	}
	var env strings.Builder
	for key, value := range installRuntimeEnv(s) {
		env.WriteString(key + "=" + value + "\n")
	}
	if err := os.WriteFile(filepath.Join(s.Data, "stack.env"), []byte(env.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInstallRuntimeSnapshotAndResumeProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	s := pinRuntimeProtocolFixture(t, pkg, o)
	p, err := prepareInstallRuntime(pkg, o, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(p.directory) }()
	if err := resumeInstallRuntime(s, o.Runtime, p); err != nil {
		t.Fatal("same request cannot resume before init", err)
	}
	before, err := os.ReadFile(filepath.Join(s.Data+".oneclick", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A caller omitting the initial three flags does not need old removable
	// media: both verification and NAS init retain the fixed public snapshot.
	if err := os.RemoveAll(o.Runtime.Bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(o.Runtime.Root); err != nil {
		t.Fatal(err)
	}
	blank := o
	blank.Runtime = installRuntimeOptions{}
	if p, err := prepareInstallRuntime(pkg, blank, false); err != nil || p != nil {
		t.Fatal("pinned resume still requires mutable original media", err)
	}
	if err := resumeInstallRuntime(s, blank.Runtime, nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(s.Data+".oneclick", "state.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("resume rewrote original installation state", err)
	}
}

func TestInstallRuntimeChangedRequestOrBytesRejectedProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	s := pinRuntimeProtocolFixture(t, pkg, o)
	changed := o
	changed.Runtime.Image = "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("b", 64)
	if _, err := prepareInstallRuntime(pkg, changed, false); err == nil {
		t.Fatal("different runtime requested on existing state")
	}
	if err := os.WriteFile(filepath.Join(o.Runtime.Bundle, "runtime.pb"), []byte("different runtime bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareInstallRuntime(pkg, o, false); err == nil {
		t.Fatal("same input path silently changed runtime bytes")
	}
	leaf := filepath.Join(runtimeSnapshot(s.Data), "root.pb")
	if err := os.Chmod(leaf, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, []byte("changed pinned root"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := resumeInstallRuntime(s, installRuntimeOptions{}, nil); err == nil {
		t.Fatal("changed pinned root accepted on resume")
	}
}

func TestInstallRuntimeResumeRejectsChangedEnvProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	s := pinRuntimeProtocolFixture(t, pkg, o)
	path := filepath.Join(s.Data, "stack.env")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rootLine := "AURIGA_RUNTIME_ROOT=" + installRuntimeEnv(s)["AURIGA_RUNTIME_ROOT"]
	for name, raw := range map[string][]byte{
		"missing":                   []byte("AURIGA_RUNTIME_IMAGE=" + o.Runtime.Image + "\n"),
		"duplicate":                 append(append([]byte{}, before...), []byte("AURIGA_RUNTIME_ROOT=/other/root.pb\n")...),
		"changed":                   bytes.ReplaceAll(before, []byte(o.Runtime.Image), []byte("other@sha256:"+strings.Repeat("a", 64))),
		"leading-key-whitespace":    bytes.ReplaceAll(before, []byte(rootLine), []byte(" \t"+rootLine)),
		"trailing-value-whitespace": bytes.ReplaceAll(before, []byte(rootLine), []byte(rootLine+" \t")),
		"whitespace-before-equals":  bytes.ReplaceAll(before, []byte("AURIGA_RUNTIME_ROOT="), []byte("AURIGA_RUNTIME_ROOT =")),
		"extra-indented-alias":      append(append([]byte{}, before...), []byte(" "+rootLine+"\n")...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := resumeInstallRuntime(s, installRuntimeOptions{}, nil); err == nil {
				t.Fatal("runtime environment drift accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("rejected resume rewrote live environment", err)
			}
		})
	}
}

func TestInstallRuntimeVerifierFailureBeforeDeploymentProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	t.Setenv("AURIGA_TEST_RUNTIME_VERIFY_FAIL", "1")
	if _, err := prepareInstallRuntime(pkg, o, false); err == nil {
		t.Fatal("original verifier failure ignored")
	}
	for _, path := range []string{o.Data, o.Data + ".oneclick", o.Data + ".oneclick-lock"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("rejected runtime created deployment state", path, err)
		}
	}
	if _, err := prepareInstallRuntime(pkg, o, true); err == nil {
		t.Fatal("unsigned outer executable accepted for runtime trust")
	}
}

func TestInstallRuntimeLegacyStateCannotAcquireRootProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	if err := os.Mkdir(o.Data+".oneclick", 0755); err != nil {
		t.Fatal(err)
	}
	s := state{Data: o.Data, Package: pkg}
	if err := saveState(s); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareInstallRuntime(pkg, o, false); err == nil {
		t.Fatal("legacy installation gained a new root through install")
	}
	if err := resumeInstallRuntime(s, o.Runtime, &preparedInstallRuntime{identity: installRuntime{Source: o.Runtime}}); err == nil {
		t.Fatal("state created by another installer before the lock silently ignored the new runtime request")
	}
	o.Runtime = installRuntimeOptions{}
	if p, err := prepareInstallRuntime(pkg, o, false); err != nil || p != nil {
		t.Fatal("legacy no-runtime reinstall compatibility lost", err)
	}
}

func TestInstallRuntimeSnapshotRejectsLinkedPublicInputProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	if err := os.Remove(o.Runtime.Root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(o.Runtime.Bundle, "runtime-keys/root.pb"), o.Runtime.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareInstallRuntime(pkg, o, false); err == nil {
		t.Fatal("copy normalized a linked external root")
	}
}

func TestInstallRuntimeOuterTrustBeforeVerifierProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	issuer, _ := testSigningKeys(t)
	signTestRoot(t, pkg, issuer)
	_, wrongPublic := testSigningKeys(t)
	o.PublicKey = wrongPublic
	o.Package = filepath.Join(t.TempDir(), "installation.tar")
	if err := pack(pkg, o.Package); err != nil {
		t.Fatal(err)
	}
	if err := install(o); err == nil {
		t.Fatal("untrusted package accepted")
	}
	if _, err := os.Lstat(os.Getenv("AURIGA_TEST_RUNTIME_VERIFY_LOG")); !os.IsNotExist(err) {
		t.Fatal("executed bundled verifier before outer signature trust", err)
	}
	if _, err := os.Lstat(o.Data + ".oneclick"); !os.IsNotExist(err) {
		t.Fatal("untrusted request wrote installation state", err)
	}
}

func TestInstallRuntimeInterruptedInitResumesSameSnapshotProtocolFixture(t *testing.T) {
	pkg, o := runtimeInstallProtocolFixture(t)
	nas, err := filepath.Abs("../nas")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"env.example", "access-ip.example.json"} {
		if err := copyFile(filepath.Join(nas, name), filepath.Join(pkg, "deploy-nas", name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	trace := filepath.Join(filepath.Dir(o.Data), "init.trace")
	t.Setenv("AURIGA_TEST_RUNTIME_INIT_TRACE", trace)
	init := `set -eu
test -s "$AURIGA_DATA_ROOT.oneclick/state.json"
for key in AURIGA_RUNTIME_BUNDLE AURIGA_RUNTIME_ROOT AURIGA_RUNTIME_IMAGE; do
  line=$(grep "^$key=" "$STACK_ENV")
  printf '%s\n' "$line" >>"$AURIGA_TEST_RUNTIME_INIT_TRACE"
done
exit 29
`
	load := ""
	for _, name := range []string{"POSTGRES", "NATS", "REGISTRY", "PLATFORM", "EDGE"} {
		load += "echo AURIGA_IMAGE_" + name + "=sha256:" + strings.Repeat("a", 64) + "\n"
	}
	for name, body := range map[string]string{"init.sh": init, "load-images.sh": load, "start.sh": "echo unexpected-start >>\"$AURIGA_TEST_RUNTIME_INIT_TRACE\"; exit 37\n"} {
		path := filepath.Join(pkg, "deploy-nas/scripts", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(pkg, "images"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"images/fixture": []byte("fake image bytes"), "NAS-SHA256SUMS": []byte("fixture"), "versions.txt": []byte("fixture")} {
		if err := os.WriteFile(filepath.Join(pkg, name), raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := jsonFile(filepath.Join(pkg, "offline-release.json"), release{Schema: 1, Version: "test-runtime-install"}, 0644); err != nil {
		t.Fatal(err)
	}
	issuer, public := testSigningKeys(t)
	signTestRoot(t, pkg, issuer)
	o.Package, o.PublicKey, o.Address = filepath.Join(t.TempDir(), "installation.tar"), public, "192.0.2.10"
	if err := pack(pkg, o.Package); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ncase \"$1\" in compose) echo 2.29.0;; info) echo 28.5.2;; esac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "unix:///not-a-real-docker-fixture.sock")
	t.Setenv("DOCKER_CONTEXT", "")
	if err := install(o); err == nil || !strings.Contains(err.Error(), "exit status 29") {
		t.Fatal("first install did not reach the controlled init interruption", err)
	}
	s, err := readState(o.Data)
	if err != nil || s.Runtime == nil || s.ResourceReady {
		t.Fatal("runtime input not pinned before init or resources marked early", err)
	}
	statePath := filepath.Join(o.Data+".oneclick", "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	// Restore with omitted inputs after removing the original media. The actual
	// install path must resume its saved snapshot instead of creating new trust.
	if err := os.RemoveAll(o.Runtime.Bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(o.Runtime.Root); err != nil {
		t.Fatal(err)
	}
	o.Runtime = installRuntimeOptions{}
	if err := install(o); err == nil || !strings.Contains(err.Error(), "exit status 29") {
		t.Fatal("saved pre-init state cannot safely resume same snapshot", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("interrupted init resume rewrote original state", err)
	}
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 6 || strings.Join(lines[:3], "\n") != strings.Join(lines[3:], "\n") || strings.Contains(string(raw), "unexpected-start") {
		t.Fatal("init did not consume exactly the same runtime snapshot or start ran early", string(raw))
	}
}

func TestInstallRuntimeUnsignedRequestCannotDowngradeConcurrentSignedInstallProtocolFixture(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		runtime, outerUnsigned, storedUnsigned bool
		wantReject                             bool
	}{
		{name: "unsigned-request-sees-new-runtime", runtime: true, outerUnsigned: true, wantReject: true},
		{name: "signed-request-sees-unsigned-stored-package", runtime: true, storedUnsigned: true, wantReject: true},
		{name: "legacy-keeps-explicit-unsigned-exception", outerUnsigned: true},
		{name: "signed-runtime-allows-flag-without-missing-signature", runtime: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg, runtimeOptions := runtimeInstallProtocolFixture(t)
			data := runtimeOptions.Data
			lateCalls := filepath.Join(filepath.Dir(data), "late-calls")
			t.Setenv("AURIGA_TEST_RUNTIME_LATE_CALLS", lateCalls)
			for _, name := range []string{"init.sh", "start.sh"} {
				path := filepath.Join(pkg, "deploy-nas/scripts", name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' called >>\"$AURIGA_TEST_RUNTIME_LATE_CALLS\"\nexit 31\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			issuer, public := testSigningKeys(t)
			signTestRoot(t, pkg, issuer)
			s := state{Data: data, Package: pkg, ResourceReady: true}
			if tc.runtime {
				s = pinRuntimeProtocolFixture(t, pkg, runtimeOptions)
				s.ResourceReady = true
			} else {
				for _, path := range []string{data, data + ".oneclick"} {
					if err := os.Mkdir(path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(data, "stack.env"), []byte("AURIGA_PLATFORM_SEED=false\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := pinInitialTrust(&s, public); err != nil {
				t.Fatal(err)
			}
			if err := saveState(s); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, ".auriga-initialized"), []byte("fixture"), 0644); err != nil {
				t.Fatal(err)
			}
			stateBefore, err := os.ReadFile(filepath.Join(data+".oneclick", "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			incoming := pkg
			if tc.outerUnsigned {
				incoming = t.TempDir()
				if err := os.WriteFile(filepath.Join(incoming, "payload"), []byte("explicit unsigned outer package fixture"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := writeSums(incoming); err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(t.TempDir(), "incoming.tar")
			if err := pack(incoming, archive); err != nil {
				t.Fatal(err)
			}
			if tc.storedUnsigned {
				if err := os.Remove(filepath.Join(pkg, "SHA256SUMS.sig")); err != nil {
					t.Fatal(err)
				}
			}
			// Hide B's signed installation while A verifies its incoming request.
			// The fake dockerReady info call restores it before withLock reads state.
			for _, path := range []string{data, data + ".oneclick"} {
				if err := os.Rename(path, path+".before-lock"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readState(data); err == nil {
				t.Fatal("A unexpectedly sees B's installation before the controlled boundary")
			}
			bin := t.TempDir()
			docker := "#!/bin/sh\nset -eu\ncase \"$1\" in\ninfo) mv \"$AURIGA_TEST_RUNTIME_RACE_DATA.before-lock\" \"$AURIGA_TEST_RUNTIME_RACE_DATA\"; mv \"$AURIGA_TEST_RUNTIME_RACE_DATA.oneclick.before-lock\" \"$AURIGA_TEST_RUNTIME_RACE_DATA.oneclick\"; echo 28.5.2;;\ncompose) echo 2.29.0;;\nesac\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(docker), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			t.Setenv("DOCKER_HOST", "unix:///not-a-real-docker-fixture.sock")
			t.Setenv("DOCKER_CONTEXT", "")
			t.Setenv("AURIGA_TEST_RUNTIME_RACE_DATA", data)
			opts := installOptions{Data: data, Package: archive, Address: "192.0.2.10", AllowUnsigned: true}
			if !tc.outerUnsigned {
				opts.PublicKey = public
			}
			err = install(opts)
			after, readErr := readState(data)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.wantReject {
				stateAfter, readErr := os.ReadFile(filepath.Join(data+".oneclick", "state.json"))
				if err == nil || !strings.Contains(err.Error(), "--allow-unsigned") || readErr != nil || !bytes.Equal(stateBefore, stateAfter) || after.AllowUnsigned {
					t.Fatalf("unsigned request altered concurrent signed runtime state or was not rejected: err=%v readErr=%v unsigned=%v", err, readErr, after.AllowUnsigned)
				}
				if _, err := os.Lstat(lateCalls); !os.IsNotExist(err) {
					t.Fatal("rejected unsigned request reached init/start", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "exit status 31") || after.AllowUnsigned != tc.outerUnsigned {
				t.Fatalf("legitimate explicit exception/signature behavior changed: err=%v unsigned=%v", err, after.AllowUnsigned)
			}
		})
	}
}
