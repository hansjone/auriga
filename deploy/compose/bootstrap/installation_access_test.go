package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func TestPublicAccessCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	proofRoot, image := runtimeProofFixture(t, "real")
	for _, mode := range []string{"domain", "ip"} {
		t.Run(mode, func(t *testing.T) {
			a := customAccess(mode)
			raw, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			input, out := filepath.Join(root, mode+".json"), filepath.Join(root, mode)
			if err := os.WriteFile(input, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"-access-file", input, "-edge-container", "public-test-edge", "-image", image, "-out", out, "-fixture", "real", "-runtime-proofs", proofRoot, "-heartbeat-observer"}
			if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("bootstrap: %v: %s", err, output)
			}
			got, err := os.ReadFile(filepath.Join(out, "access.json"))
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("final file differs from validated original: %v", err)
			}
			cfg, err := deploymentconfig.Load(got)
			if err != nil || cfg.Mode() != mode || cfg.InstanceControl().DnsAlias != "installation.internal" || cfg.InstanceControl().EdgeContainer != "public-test-edge" {
				t.Fatalf("final production load: %v", err)
			}
			env, err := os.ReadFile(filepath.Join(out, "access.env"))
			if err != nil || !bytes.Equal(env, composeAccessEnv(cfg, true)) || !strings.Contains(string(env), "AURIGA_PORTAL_TARGET_PORT='18080'") || !strings.Contains(string(env), "AURIGA_ADMIN_PORT='19084'") {
				t.Fatalf("derived mapping/issuer: %v", err)
			}
			bind := "0.0.0.0"
			if mode == "ip" {
				bind = "192.0.2.10"
			}
			if !strings.Contains(string(env), "AURIGA_ACCESS_BIND='"+bind+"'") || !strings.Contains(string(env), "AURIGA_TEAM_ORIGIN='"+cfg.PortalURL()+"'") || !strings.Contains(string(env), "AURIGA_TEAM_RP_ID='"+cfg.PortalOrigin().Host+"'") {
				t.Fatal("published authority or team Origin drifted")
			}
			// Access changes may never redirect the fixed authenticated internal hops.
			edge, err := os.ReadFile(filepath.Join(out, "edge.config.json"))
			if err != nil || !strings.Contains(string(edge), "https://host.docker.internal:8082") || !strings.Contains(string(edge), "host.docker.internal:9091") || strings.Contains(string(edge), cfg.PortalOrigin().Host) {
				t.Fatalf("internal hop changed: %v", err)
			}
			before, err := os.ReadFile(filepath.Join(out, "iam-installation", "edge.secret"))
			if err != nil {
				t.Fatal(err)
			}
			wholeBefore := installationSnapshot(t, out)
			if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("repeat: %v: %s", err, output)
			}
			after, err := os.ReadFile(filepath.Join(out, "iam-installation", "edge.secret"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("installation credential rotated: %v", err)
			}
			if wholeAfter := installationSnapshot(t, out); wholeAfter != wholeBefore {
				t.Fatal("repeated CLI changed installation bytes or modes")
			}
			for _, failure := range []string{"image", "fixture", "observer", "access", "damaged public", "missing public", "missing private", "missing receipt", "damaged private"} {
				t.Run("repeat rejects/"+failure, func(t *testing.T) {
					badArgs := append([]string{}, args...)
					changed := ""
					var restore []byte
					var fileMode os.FileMode
					switch failure {
					case "image":
						badArgs[5] = "127.0.0.1:5000/auriga/real-dsh@sha256:" + strings.Repeat("b", 64)
					case "fixture":
						badArgs[9] = "fake"
					case "observer":
						badArgs = badArgs[:len(badArgs)-1]
					case "access":
						bad := customAccess(mode)
						bad.AdminOrigin.Port++
						badRaw, _ := json.Marshal(bad)
						badInput := filepath.Join(t.TempDir(), "different.json")
						if err := os.WriteFile(badInput, badRaw, 0o600); err != nil {
							t.Fatal(err)
						}
						badArgs[1] = badInput
					default:
						name := "pki/iam.crt"
						if failure == "missing private" {
							name = "iam-installation/edge.secret"
						}
						if failure == "missing receipt" {
							name = "iam-installation/bootstrap.receipt"
						}
						if failure == "damaged private" {
							name = "iam-installation/compose.keys"
						}
						changed = filepath.Join(out, name)
						var err error
						restore, err = os.ReadFile(changed)
						if err != nil {
							t.Fatal(err)
						}
						info, err := os.Stat(changed)
						if err != nil {
							t.Fatal(err)
						}
						fileMode = info.Mode()
						if strings.HasPrefix(failure, "missing") {
							err = os.Remove(changed)
						} else {
							err = os.WriteFile(changed, []byte("damaged"), fileMode)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := os.WriteFile(changed, restore, fileMode); err != nil {
								t.Fatal(err)
							}
						}()
					}
					beforeFailure := installationSnapshot(t, out)
					if output, err := exec.Command(binary, badArgs...).CombinedOutput(); err == nil {
						t.Fatalf("changed/partial installation accepted: %s", output)
					}
					if afterFailure := installationSnapshot(t, out); afterFailure != beforeFailure {
						t.Fatal("failed repeat altered existing material")
					}
				})
			}
		})
	}
	valid, _ := json.Marshal(customAccess("domain"))
	invalid := map[string][]byte{
		"malformed": []byte(`{"mode":`),
		"unknown":   append(append([]byte{}, valid[:len(valid)-1]...), []byte(`,"unknown":true}`)...),
		"duplicate": append(append([]byte{}, valid[:len(valid)-1]...), []byte(`,"mode":"domain"}`)...),
	}
	for name, mutate := range map[string]func(*deployment.Access){
		"issuer":        func(a *deployment.Access) { a.IssuerOrigin.Port++ },
		"port conflict": func(a *deployment.Access) { a.Listeners.Admin.Port = a.Listeners.Portal.Port },
		"alias conflict": func(a *deployment.Access) {
			a.InstanceControl.DnsAlias = a.PortalOrigin.Host
			a.InstanceControl.Origin.Host = a.PortalOrigin.Host
		},
		"edge identity":         func(a *deployment.Access) { a.InstanceControl.EdgeContainer = "other-edge" },
		"seed":                  func(a *deployment.Access) { a.TenantId = "019a0000-0000-7000-8000-000000000001" },
		"host service conflict": func(a *deployment.Access) { a.AdminOrigin.Port = observerPort },
		"container loopback":    func(a *deployment.Access) { a.Listeners.Portal.Address = "127.0.0.1" },
		"https": func(a *deployment.Access) {
			a.PortalOrigin.Scheme = "https"
			a.IssuerOrigin.Scheme = "https"
			a.Listeners.Portal.Tls = true
			a.Instances.AccessInstancesSubdomain.Scheme = "https"
		},
		"control TLS": func(a *deployment.Access) {
			a.Listeners.InstanceControl.Tls = true
			a.InstanceControl.Origin.Scheme = "https"
		},
		"proxy": func(a *deployment.Access) {
			a.Listeners.Portal.Tls = true
			a.Listeners.Admin.Tls = true
			a.Ingress = deployment.AccessIngressTrustedProxyAsAccessIngress(&deployment.AccessIngressTrustedProxy{Kind: "trusted_proxy", PeerCidrs: []string{"192.0.2.0/24"}})
		},
	} {
		a := customAccess("domain")
		mutate(&a)
		invalid[name], _ = json.Marshal(a)
	}
	for name, raw := range invalid {
		t.Run("reject/"+name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "access.json")
			out := filepath.Join(t.TempDir(), "must-not-exist")
			if err := os.WriteFile(input, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(binary, "-access-file", input, "-edge-container", "public-test-edge", "-image", image, "-out", out).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "compose access:") {
				t.Fatalf("invalid input accepted or wrong failure: %v: %s", err, output)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("invalid access created output: %v", err)
			}
		})
	}
}

func installationSnapshot(t *testing.T, out string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(out, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(out, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %o %x\n", rel, info.Mode(), sha256.Sum256(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestPartialInstallationCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	cases := []string{"fake-template", "real-template", "unknown-template", "template-subdirectory", "templates-file", "unknown-pki"}
	if runtime.GOOS == "linux" {
		cases = append(cases, "templates-empty-directory-symlink", "pki-empty-directory-symlink")
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			caseRoot := t.TempDir()
			out := filepath.Join(caseRoot, "installation")
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			fixture := "fake"
			dir := filepath.Join(out, "templates")
			switch name {
			case "templates-empty-directory-symlink", "pki-empty-directory-symlink":
				external := filepath.Join(caseRoot, "external")
				if err := os.Mkdir(external, 0o755); err != nil {
					t.Fatal(err)
				}
				if name == "pki-empty-directory-symlink" {
					dir = filepath.Join(out, "pki")
				}
				if err := os.Symlink(external, dir); err != nil {
					t.Fatal(err)
				}
			case "templates-file":
				if err := os.WriteFile(dir, []byte("retained"), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				if name == "unknown-pki" {
					dir = filepath.Join(out, "pki")
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				file := "fake-dsh.pb"
				if name == "real-template" {
					file = "real-dsh.pb"
					fixture = "real"
				}
				if name == "unknown-template" || name == "unknown-pki" {
					file = "unknown.material"
				}
				if name == "template-subdirectory" {
					if err := os.Mkdir(filepath.Join(dir, "unknown"), 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(filepath.Join(dir, file), []byte("retained"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := safeInstallationFiles(t, caseRoot)
			image := "127.0.0.1:5000/auriga/real-dsh@sha256:" + strings.Repeat("a", 64)
			output, err := exec.Command(binary, "-out", out, "-fixture", fixture, "-image", image).CombinedOutput()
			if err == nil {
				t.Errorf("partial installation accepted: %s", output)
			}
			if after := safeInstallationFiles(t, caseRoot); after != before {
				t.Error("failed installation modified existing or external materials")
			}
		})
	}
}

func safeInstallationFiles(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, "%s %o link=%s\n", rel, info.Mode(), target)
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %o %x\n", rel, info.Mode(), sha256.Sum256(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func customAccess(mode string) deployment.Access {
	a := composeAccess("public-test-edge")
	a.PortalOrigin.Host = "example.com"
	a.PortalOrigin.Port = 19080
	a.AdminOrigin.Port = 19084
	a.Listeners.Portal.Port = 18080
	a.Listeners.Admin.Port = 18084
	a.Listeners.InstanceControl.Port = 18083
	a.InstanceControl.DnsAlias = "installation.internal"
	a.InstanceControl.Origin.Host = "installation.internal"
	a.InstanceControl.Origin.Port = 18083
	a.Instances.AccessInstancesSubdomain.Port = 19080
	if mode == "ip" {
		a.Mode = "ip"
		a.PortalOrigin.Host = "192.0.2.10"
		a.AdminOrigin.Host = "192.0.2.10"
		a.Instances = deployment.AccessInstancesPathAsAccessInstances(&deployment.AccessInstancesPath{Mode: "path", Prefix: "/i/", TrustedTeamAcknowledged: true})
	}
	a.IssuerOrigin = a.PortalOrigin
	return a
}
