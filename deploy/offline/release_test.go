package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testSigningKeys(t *testing.T) (string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	private := filepath.Join(root, "private.pem")
	public := filepath.Join(root, "public.pem")
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	der, err = x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	return private, public
}

func TestReleaseCacheSignedPartsAndTagGate(t *testing.T) {
	source := t.TempDir()
	key, public := testSigningKeys(t)
	dshSource := fixtureDSHReleaseInput(t, source)
	windowsArchive, windowsSHA, nodeSHA := fixtureWindowsReleaseInput(t, source, t.TempDir(), key)
	for _, args := range [][]string{{"init"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.com"}} {
		if _, err := gitAt(source, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "commit", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.0-alias"); err != nil {
		t.Fatal(err)
	}
	single := fixtureSingleKit(t, "1.0", "a")
	fixtureKit, err := readDevkit(single)
	if err != nil {
		t.Fatal(err)
	}
	r := fixtureKit.Release
	r.NAS.URL = filepath.Join(single, "cache", "nas.tar")
	r.NAS.SHA256, err = digest(r.NAS.URL)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(t.TempDir(), "release.json")
	if err := jsonFile(index, r, 0644); err != nil {
		t.Fatal(err)
	}
	builds := 0
	fake := func(file string) error {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "payload"), []byte(strings.Repeat("data", 20000)), 0644); err != nil {
			return err
		}
		if err := writeSums(root); err != nil {
			return err
		}
		if err := signSums(root, key); err != nil {
			return err
		}
		return pack(root, file)
	}
	builders := releaseBuilders{Bundle: func(_ []string, _ string) error { return nil }, Devkit: func(_ []string) error { return nil }}
	builders.Bundle = func(args []string, _ string) error { return fake(flagValue(args, "--out")) }
	builders.Devkit = func(args []string) error {
		builds++
		locks, err := dependencyLocks(source, r)
		if err != nil {
			return err
		}
		fixtureKit.Locks = &locks
		fixtureKit.Release.NAS.SHA256 = r.NAS.SHA256
		if err := jsonFile(filepath.Join(single, "devkit.json"), fixtureKit, 0644); err != nil {
			return err
		}
		if err := writeSums(single); err != nil {
			return err
		}
		if err := signingPublic(key, filepath.Join(single, "release-public.pem")); err != nil {
			return err
		}
		if err := writeSums(single); err != nil {
			return err
		}
		if err := signSums(single, key); err != nil {
			return err
		}
		return pack(single, flagValue(args, "--out"))
	}
	builders.Gate = func(o gateRequest) (buildGate, error) {
		if err := fake(o.Out); err != nil {
			return buildGate{}, err
		}
		hash, err := digest(o.Out)
		return buildGate{Passed: true, Commit: o.Commit, LockDigest: o.LockDigest, SourceSHA: o.SourceSHA, CatalogSHA: o.CatalogSHA, DevkitSHA: o.DevkitSHA, OutputSHA: hash, Network: gateNetwork}, err
	}
	builders.Executable = filepath.Join(t.TempDir(), "cli")
	if err := os.WriteFile(builders.Executable, []byte("fixture cli"), 0755); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	opts := releaseOptions{Source: source, Tag: "1.0", Index: index, Out: filepath.Join(parent, "v1"), Cache: filepath.Join(parent, "cache"), Key: key, Limit: 65536, NewBaseline: true, WindowsClient: windowsArchive, WindowsSHA: windowsSHA, DSHSource: dshSource}
	if err := generateRelease(opts, builders); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" {
		for _, interrupted := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
			t.Run("cancel-after-gate-"+interrupted.String(), func(t *testing.T) {
				observed := make(chan os.Signal, 1)
				signal.Notify(observed, interrupted)
				defer signal.Stop(observed)
				cancelled := builders
				cancelled.Gate = func(request gateRequest) (buildGate, error) {
					proof, err := builders.Gate(request)
					process, findErr := os.FindProcess(os.Getpid())
					if findErr != nil {
						t.Fatal(findErr)
					}
					if err := process.Signal(interrupted); err != nil {
						t.Fatal(err)
					}
					<-observed
					// Allow both signal.Notify subscribers to receive this signal.
					time.Sleep(20 * time.Millisecond)
					return proof, err
				}
				cancelOpts := opts
				cancelOpts.Out = filepath.Join(parent, "cancelled-"+interrupted.String())
				if err := generateRelease(cancelOpts, cancelled); err == nil {
					t.Error("cancelled release committed its signed distribution")
				}
				if _, err := os.Stat(cancelOpts.Out); !os.IsNotExist(err) {
					t.Error("cancelled release left its final output directory", err)
				}
			})
		}
		t.Run("cancel-before-final-rename", func(t *testing.T) {
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			arm := filepath.Join(bin, "armed")
			program := `#!/bin/sh
set -eu
if test -f "$GATE_COMMIT_CANCEL_ARM"; then
  rm "$GATE_COMMIT_CANCEL_ARM"
  kill -INT "$PPID"
  sleep .02
fi
exec "$GATE_COMMIT_REAL_GIT" "$@"
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(program), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GATE_COMMIT_REAL_GIT", realGit)
			t.Setenv("GATE_COMMIT_CANCEL_ARM", arm)
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			observed := make(chan os.Signal, 1)
			signal.Notify(observed, os.Interrupt)
			defer signal.Stop(observed)
			cancelled := builders
			cancelled.Gate = func(request gateRequest) (buildGate, error) {
				proof, err := builders.Gate(request)
				if err != nil {
					return proof, err
				}
				return proof, os.WriteFile(arm, nil, 0600)
			}
			cancelOpts := opts
			cancelOpts.Out = filepath.Join(parent, "cancelled-before-rename")
			if err := generateRelease(cancelOpts, cancelled); err == nil {
				t.Error("late cancellation committed signed output")
			}
			select {
			case <-observed:
			default:
				t.Error("fixture did not reach final source validation")
			}
			if _, err := os.Stat(cancelOpts.Out); !os.IsNotExist(err) {
				t.Error("late cancellation left final output", err)
			}
		})
	}
	if err := verifySums(opts.Out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.Out, "auriga.exe")); !os.IsNotExist(err) {
		t.Fatal("Linux release unexpectedly includes a Windows launcher", err)
	}
	fp, err := publicFingerprint(public)
	if err != nil {
		t.Fatal(err)
	}
	downloads, err := os.ReadFile(filepath.Join(opts.Out, "DOWNLOADS.txt"))
	if err != nil || !strings.Contains(string(downloads), fp) {
		t.Fatal("download guide missing fingerprint", err)
	}
	if err := verifySignature(opts.Out, public); err != nil {
		t.Fatal(err)
	}
	var manifest distributionManifest
	if err := readJSON(filepath.Join(opts.Out, "release-manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if err := validateBuildGate(opts.Out, manifest); err != nil {
		t.Fatal(err)
	}
	for _, asset := range manifest.Archives {
		if strings.Contains(asset.Name, "-delta.tar") {
			t.Fatalf("fresh baseline ships a redundant full delta: %s", asset.Name)
		}
	}
	if manifest.Windows == nil || manifest.Windows.BuildNodeSHA != nodeSHA {
		t.Fatal("release manifest missing verified Windows bootstrap digest")
	}
	for _, attachment := range []string{filepath.Base(windowsArchive), "auriga-dsh-source-0.2.1.tar.gz", "auriga-dsh-source-0.2.1-LICENSE.txt", "auriga-dsh-source-0.2.1-provenance.json"} {
		found := false
		for _, asset := range manifest.Assets {
			found = found || asset.Name == attachment
		}
		if !found || !strings.Contains(string(downloads), attachment) {
			t.Fatal("release manifest or downloads missing", attachment)
		}
	}
	if !strings.Contains(string(downloads), nodeSHA) || !strings.Contains(string(downloads), manifest.Gate.SourceSHA) {
		t.Fatal("download guide missing Windows trust bootstrap digests")
	}
	archived := filepath.Join(parent, "source")
	if err := extractVerified(filepath.Join(opts.Out, "auriga-source-1.0.tar"), archived, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(archived, "bin/auriga-cli.exe")); !os.IsNotExist(err) {
		t.Fatal("source archive unexpectedly includes a Windows executable", err)
	}
	m, err := readSourceManifest(archived)
	if err != nil || m.Source.UpstreamVersion != "1.0" {
		t.Fatal("explicit release tag must win over other same-commit tags", m, err)
	}
	guide, err := os.ReadFile(filepath.Join(opts.Out, "DOWNLOADS.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.sig", "DOWNLOADS.txt", "release-manifest.json"} {
		a, err := describeAsset(filepath.Join(opts.Out, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(guide), fmt.Sprintf("%s  %d B", name, a.Size)) {
			t.Fatal("download guide missing exact metadata size", name)
		}
	}
	previous := filepath.Join(parent, "previous")
	if err := extractVerified(filepath.Join(opts.Out, "auriga-devkit-1.0.tar"), previous, public); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(filepath.Join(opts.Out, "auriga-offline-1.0.tar.part001"), filepath.Join(parent, "unpack"), public); err != nil {
		t.Fatal(err)
	}
	_, wrong := testSigningKeys(t)
	if err := extractVerified(filepath.Join(opts.Out, "auriga-offline-1.0.tar.part001"), filepath.Join(parent, "badkey"), wrong); err == nil {
		t.Fatal("accepted wrong signature")
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("new code"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "commit", "-m", "code only"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.1"); err != nil {
		t.Fatal(err)
	}
	opts.Tag = "1.1"
	opts.Out = filepath.Join(parent, "v2")
	opts.Previous = previous
	opts.NewBaseline = false
	opts.Limit = 0
	if err := generateRelease(opts, builders); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatal("did not reuse lock-identical devkit", builds)
	}
	if _, err := os.Stat(filepath.Join(opts.Out, "auriga-devkit-1.1-delta.tar")); err != nil {
		t.Fatal("same baseline did not ship a delta", err)
	}
	if err := verifySignature(opts.Out, public); err != nil {
		t.Fatal(err)
	}
	var whole distributionManifest
	if err := readJSON(filepath.Join(opts.Out, "release-manifest.json"), &whole); err != nil {
		t.Fatal(err)
	}
	if err := validateBuildGate(opts.Out, whole); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(filepath.Join(opts.Out, "auriga-offline-1.1.tar"), filepath.Join(parent, "whole"), public); err != nil {
		t.Fatal(err)
	}
	if parts, err := filepath.Glob(filepath.Join(opts.Out, "*.part*")); err != nil || len(parts) != 0 {
		t.Fatal("whole release produced split files", parts, err)
	}
	failing := builders
	failing.Gate = func(gateRequest) (buildGate, error) { return buildGate{}, errors.New("missing fixture dependency") }
	opts.Out = filepath.Join(parent, "gate-failure")
	if err := generateRelease(opts, failing); err == nil {
		t.Fatal("released without offline build")
	}
	if _, err := os.Stat(opts.Out); !os.IsNotExist(err) {
		t.Fatal("failed gate left publishable output")
	}
	opts.Tag = "1.0"
	opts.Out = filepath.Join(parent, "mismatch")
	if err := generateRelease(opts, builders); err == nil {
		t.Fatal("accepted HEAD not at tag")
	}
	caches, err := filepath.Glob(filepath.Join(opts.Cache, "*", "auriga-devkit-*.tar"))
	if err != nil || len(caches) != 1 {
		t.Fatal(caches, err)
	}
	f, err := os.OpenFile(caches[0], os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("tamper"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	opts.Tag = "1.1"
	opts.Out = filepath.Join(parent, "tampered")
	if err := generateRelease(opts, builders); err == nil {
		t.Fatal("accepted tampered cached devkit")
	}
}

func TestRefreshVersionedInputsOnDependencyReuse(t *testing.T) {
	root := fixtureSingleKit(t, "1.0", "a")
	key, _ := testSigningKeys(t)
	kit, err := readDevkit(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	next := "new-nas.tar"
	if err := os.WriteFile(next, []byte("new deployment scripts"), 0644); err != nil {
		t.Fatal(err)
	}
	r := kit.Release
	r.Version = "1.1"
	r.NAS.URL = next
	r.NAS.SHA256, err = digest(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshReleaseInputs(root, r, key); err != nil {
		t.Fatal(err)
	}
	updated, err := readDevkit(root)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := digest(filepath.Join(root, "cache/nas.tar"))
	if err != nil || hash != r.NAS.SHA256 || updated.Release.Version != "1.1" {
		t.Fatal(updated, err)
	}
}

func flagValue(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
