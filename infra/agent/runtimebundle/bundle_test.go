package runtimebundle

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fixture struct {
	o              SignOptions
	key            *nodev1.ArtifactSigningKey
	manifest, conf []byte
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func put(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fresh(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{o: SignOptions{Image: "", ArtifactID: "dsh-test", Version: "1.2.3", OS: "linux", Arch: "amd64", Sequence: 7, IssuedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)), RootPath: filepath.Join(dir, "root.pb"), PrivateKeyPath: filepath.Join(dir, "publisher.pem"), ManifestPath: filepath.Join(dir, "manifest.json"), ConfigPath: filepath.Join(dir, "config.json"), OutputDir: filepath.Join(dir, "bundle")}}
	f.key = &nodev1.ArtifactSigningKey{Kid: "publisher-root", PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))}
	put(t, f.o.RootPath, marshal(t, f.key))
	put(t, f.o.PrivateKeyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	protectPrivate(t, f.o.PrivateKeyPath)
	f.setConfig(t, []byte(`{ "architecture":"amd64", "os":"linux", "config":{"Cmd":["/dsh"]}, "rootfs":{"type":"layers","diff_ids":["sha256:`+strings.Repeat("a", 64)+`"]} }`+"\n"))
	return f
}

func (f *fixture) setConfig(t *testing.T, config []byte) {
	t.Helper()
	f.conf = bytes.Clone(config)
	f.setManifest(t, []byte(fmt.Sprintf("{\n\"schemaVersion\":2,\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"config\":{\"mediaType\":\"application/vnd.oci.image.config.v1+json\",\"digest\":%q,\"size\":%d},\"layers\":[{\"mediaType\":\"application/vnd.oci.image.layer.v1.tar\",\"digest\":%q,\"size\":42}]}\n", hash(config), len(config), "sha256:"+strings.Repeat("a", 64))))
	put(t, f.o.ConfigPath, config)
}

func (f *fixture) setManifest(t *testing.T, m []byte) {
	t.Helper()
	f.manifest = bytes.Clone(m)
	f.o.Image = "registry.example.com/auriga/dsh@" + hash(m)
	put(t, f.o.ManifestPath, m)
}

func (f fixture) verify() VerifyOptions {
	return VerifyOptions{Dir: f.o.OutputDir, TrustedRootPath: f.o.RootPath, Policy: Policy{OS: f.o.OS, Arch: f.o.Arch, AllowedImage: f.o.Image}}
}

func invalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func signOK(t *testing.T, f fixture) *Material {
	t.Helper()
	m, err := Sign(f.o)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestProductionConsumptionAndRepeatRead(t *testing.T) {
	for _, repo := range []string{"registry.example.com/auriga/dsh", "192.0.2.8:5000/auriga/dsh"} {
		t.Run(repo, func(t *testing.T) {
			f := fresh(t)
			f.o.Image = repo + "@" + hash(f.manifest)
			m := signOK(t, f)
			ring, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{m.Root()}, true, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			v, err := releaseverify.Verify(m.Runtime(), ring, releaseverify.Target{OS: "linux", Arch: "amd64", Driver: "docker", AllowImage: func(s string) error {
				if s != f.o.Image {
					return errors.New("wrong image")
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := releaseverify.VerifyOCI(v, ring, m.Manifest(), m.Config()); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(m.Manifest(), f.manifest) || !bytes.Equal(m.Config(), f.conf) {
				t.Fatal("original OCI bytes changed")
			}
			before := snapshot(t, f.o.OutputDir)
			for range 2 {
				read, err := Verify(f.verify())
				if err != nil {
					t.Fatal(err)
				}
				if read.BodyDigest() != v.BodyDigest() || read.ConfigID() != hash(f.conf) || !bytes.Equal(read.RuntimeBytes(), m.RuntimeBytes()) || !bytes.Equal(read.RootBytes(), m.RootBytes()) {
					t.Fatal("read did not preserve material")
				}
				read.RuntimeBytes()[0] ^= 1
				read.RootBytes()[0] ^= 1
				read.Manifest()[0] ^= 1
				read.Config()[0] ^= 1
				read.Runtime().Image = "changed"
				read.Root().Kid = "changed"
			}
			after := snapshot(t, f.o.OutputDir)
			if len(before) != 4 || len(after) != 4 {
				t.Fatal("unexpected public output count")
			}
			private, err := os.ReadFile(f.o.PrivateKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			for name, raw := range before {
				if !bytes.Equal(raw, after[name]) || bytes.Contains(raw, []byte("PRIVATE KEY")) || bytes.Contains(raw, private) {
					t.Fatal("modified public bytes or leaked private key")
				}
			}
		})
	}
}

func snapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		m[strings.TrimPrefix(path, dir)] = raw
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSignInvalidInputsLeaveNoOutput(t *testing.T) {
	cases := map[string]func(*testing.T, *fixture){
		"wrong private key": func(t *testing.T, f *fixture) {
			other := fresh(t)
			raw, err := os.ReadFile(other.o.PrivateKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			put(t, f.o.PrivateKeyPath, raw)
		},
		"expired root": func(t *testing.T, f *fixture) {
			f.key.NotAfter = timestamppb.New(time.Now().Add(-time.Hour))
			put(t, f.o.RootPath, marshal(t, f.key))
		},
		"malformed root": func(t *testing.T, f *fixture) {
			f.key.PublicKey = []byte("short")
			put(t, f.o.RootPath, marshal(t, f.key))
		},
		"unknown root": func(t *testing.T, f *fixture) {
			f.key.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 1})
			put(t, f.o.RootPath, marshal(t, f.key))
		},
		"unknown root expiry": func(t *testing.T, f *fixture) {
			f.key.NotAfter.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 1})
			put(t, f.o.RootPath, marshal(t, f.key))
		},
		"endorsed root":    func(t *testing.T, f *fixture) { f.key.EndorsedByKid = "other"; put(t, f.o.RootPath, marshal(t, f.key)) },
		"zero sequence":    func(t *testing.T, f *fixture) { f.o.Sequence = 0 },
		"unsafe sequence":  func(t *testing.T, f *fixture) { f.o.Sequence = 9007199254740992 },
		"missing time":     func(t *testing.T, f *fixture) { f.o.IssuedAt = nil },
		"bad time nanos":   func(t *testing.T, f *fixture) { f.o.IssuedAt.Nanos = -1 },
		"bad time range":   func(t *testing.T, f *fixture) { f.o.IssuedAt.Seconds = 253402300800 },
		"unknown time":     func(t *testing.T, f *fixture) { f.o.IssuedAt.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 1}) },
		"missing artifact": func(t *testing.T, f *fixture) { f.o.ArtifactID = "" },
		"missing version":  func(t *testing.T, f *fixture) { f.o.Version = "" },
		"image tag":        func(t *testing.T, f *fixture) { f.o.Image = "registry.example.com/auriga/dsh:latest" },
		"wrong image digest": func(t *testing.T, f *fixture) {
			f.o.Image = "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("b", 64)
		},
		"wrong platform": func(t *testing.T, f *fixture) { f.o.Arch = "arm64" },
		"native":         func(t *testing.T, f *fixture) { f.o.Image = "" },
		"manifest drift": func(t *testing.T, f *fixture) { put(t, f.o.ManifestPath, append(bytes.Clone(f.manifest), ' ')) },
		"config drift":   func(t *testing.T, f *fixture) { put(t, f.o.ConfigPath, append(bytes.Clone(f.conf), ' ')) },
		"pem suffix": func(t *testing.T, f *fixture) {
			b, err := os.ReadFile(f.o.PrivateKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			put(t, f.o.PrivateKeyPath, append(b, []byte("extra")...))
		},
		"pem second block": func(t *testing.T, f *fixture) {
			b, err := os.ReadFile(f.o.PrivateKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			put(t, f.o.PrivateKeyPath, append(b, b...))
		},
		"pem prefix": func(t *testing.T, f *fixture) {
			b, err := os.ReadFile(f.o.PrivateKeyPath)
			if err != nil {
				t.Fatal(err)
			}
			put(t, f.o.PrivateKeyPath, append([]byte("prefix\n"), b...))
		},
		"oversized private": func(t *testing.T, f *fixture) {
			put(t, f.o.PrivateKeyPath, bytes.Repeat([]byte("x"), maxPrivateBytes+1))
		},
		"oversized root": func(t *testing.T, f *fixture) { put(t, f.o.RootPath, bytes.Repeat([]byte("x"), MaxRootBytes+1)) },
		"oversized manifest": func(t *testing.T, f *fixture) {
			put(t, f.o.ManifestPath, bytes.Repeat([]byte("x"), releaseverify.MaxManifestBytes+1))
		},
		"oversized config": func(t *testing.T, f *fixture) {
			put(t, f.o.ConfigPath, bytes.Repeat([]byte("x"), releaseverify.MaxConfigBytes+1))
		},
		"traversal": func(t *testing.T, f *fixture) {
			f.o.OutputDir = filepath.Dir(f.o.OutputDir) + string(filepath.Separator) + ".." + string(filepath.Separator) + "bundle"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := fresh(t)
			out := f.o.OutputDir
			mutate(t, &f)
			_, err := Sign(f.o)
			invalid(t, err)
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatalf("invalid input wrote output: %v", err)
			}
		})
	}
}

func TestMalformedOCIUsesProductionRejection(t *testing.T) {
	cases := map[string]func(*testing.T, *fixture){
		"index": func(t *testing.T, f *fixture) {
			f.setManifest(t, []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`))
		},
		"duplicate manifest key": func(t *testing.T, f *fixture) {
			f.setManifest(t, bytes.Replace(f.manifest, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1))
		},
		"manifest alias": func(t *testing.T, f *fixture) {
			f.setManifest(t, bytes.Replace(f.manifest, []byte(`"schemaVersion"`), []byte(`"SchemaVersion"`), 1))
		},
		"duplicate config key": func(t *testing.T, f *fixture) {
			f.setConfig(t, bytes.Replace(f.conf, []byte(`"os":"linux"`), []byte(`"os":"linux","os":"linux"`), 1))
		},
		"config platform": func(t *testing.T, f *fixture) {
			f.setConfig(t, bytes.Replace(f.conf, []byte(`"amd64"`), []byte(`"arm64"`), 1))
		},
		"execution alias": func(t *testing.T, f *fixture) {
			f.setConfig(t, bytes.Replace(f.conf, []byte(`"Cmd"`), []byte(`"cmd"`), 1))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := fresh(t)
			mutate(t, &f)
			_, err := Sign(f.o)
			invalid(t, err)
			if !errors.Is(err, releaseverify.ErrInvalid) {
				t.Fatalf("did not use production rejection: %v", err)
			}
		})
	}
}

func TestVerifyRejectsTamperedOrIncompleteMaterials(t *testing.T) {
	cases := map[string]func(*testing.T, *fixture){
		"external root missing": func(t *testing.T, f *fixture) { f.o.RootPath = "" },
		"external wrong root":   func(t *testing.T, f *fixture) { f.o.RootPath = fresh(t).o.RootPath },
		"root drift": func(t *testing.T, f *fixture) {
			f.key.NotAfter.Seconds++
			put(t, filepath.Join(f.o.OutputDir, "runtime-keys/root.pb"), marshal(t, f.key))
		},
		"manifest drift": func(t *testing.T, f *fixture) {
			put(t, proofFile(f, "manifest.json"), append(bytes.Clone(f.manifest), ' '))
		},
		"config drift": func(t *testing.T, f *fixture) { put(t, proofFile(f, "config.json"), append(bytes.Clone(f.conf), ' ')) },
		"missing config": func(t *testing.T, f *fixture) {
			if err := os.Remove(proofFile(f, "config.json")); err != nil {
				t.Fatal(err)
			}
		},
		"missing declaration": func(t *testing.T, f *fixture) {
			if err := os.Remove(filepath.Join(f.o.OutputDir, "runtime.pb")); err != nil {
				t.Fatal(err)
			}
		},
		"extra top": func(t *testing.T, f *fixture) { put(t, filepath.Join(f.o.OutputDir, "extra"), []byte("x")) },
		"extra root": func(t *testing.T, f *fixture) {
			put(t, filepath.Join(f.o.OutputDir, "runtime-keys/other.pb"), []byte("x"))
		},
		"extra proof": func(t *testing.T, f *fixture) { put(t, proofFile(f, "extra.json"), []byte("x")) },
		"extra proof directory": func(t *testing.T, f *fixture) {
			if err := os.Mkdir(filepath.Join(f.o.OutputDir, "runtime-proofs/extra"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"oversized declaration": func(t *testing.T, f *fixture) {
			put(t, filepath.Join(f.o.OutputDir, "runtime.pb"), bytes.Repeat([]byte("x"), MaxRuntimeBytes+1))
		},
		"unknown declaration": func(t *testing.T, f *fixture) {
			mutateRuntime(t, f, func(d *nodev1.DshRuntime) { d.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 1}) })
		},
		"unknown signature": func(t *testing.T, f *fixture) {
			mutateRuntime(t, f, func(d *nodev1.DshRuntime) {
				d.ReleaseEnvelope.Signature.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 1})
			})
		},
		"bad signature": func(t *testing.T, f *fixture) {
			mutateRuntime(t, f, func(d *nodev1.DshRuntime) { d.ReleaseEnvelope.Signature.Value = strings.Repeat("A", 86) })
		},
		"legacy extras": func(t *testing.T, f *fixture) {
			mutateRuntime(t, f, func(d *nodev1.DshRuntime) { d.Signature = []byte{1} })
		},
		"duplicate protobuf field": func(t *testing.T, f *fixture) {
			path := filepath.Join(f.o.OutputDir, "runtime.pb")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			put(t, path, append(raw, []byte{0x12, 0x05, '1', '.', '2', '.', '3'}...))
		},
		"empty policy": func(t *testing.T, f *fixture) { f.o.Image = "" },
		"wrong policy": func(t *testing.T, f *fixture) { f.o.Image = "192.0.2.8/auriga/dsh@" + hash(f.manifest) },
		"wrong target": func(t *testing.T, f *fixture) { f.o.OS = "windows" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := fresh(t)
			signOK(t, f)
			mutate(t, &f)
			_, err := Verify(f.verify())
			invalid(t, err)
		})
	}
}

func proofFile(f *fixture, leaf string) string {
	return filepath.Join(f.o.OutputDir, "runtime-proofs", strings.TrimPrefix(hash(f.manifest), "sha256:"), leaf)
}

func mutateRuntime(t *testing.T, f *fixture, mutate func(*nodev1.DshRuntime)) {
	t.Helper()
	path := filepath.Join(f.o.OutputDir, "runtime.pb")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := new(nodev1.DshRuntime)
	if err := proto.Unmarshal(raw, d); err != nil {
		t.Fatal(err)
	}
	mutate(d)
	put(t, path, marshal(t, d))
}

func TestExistingOutputNeverOverwritten(t *testing.T) {
	f := fresh(t)
	if err := os.Mkdir(f.o.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(f.o.OutputDir, "keep")
	put(t, sentinel, []byte("keep"))
	_, err := Sign(f.o)
	invalid(t, err)
	raw, err := os.ReadFile(sentinel)
	if err != nil || string(raw) != "keep" {
		t.Fatal("existing output changed")
	}
}

func TestPrivateKeyFormats(t *testing.T) {
	for _, format := range []string{"ecdsa", "bad-der", "headers", "empty"} {
		t.Run(format, func(t *testing.T) {
			f := fresh(t)
			var raw []byte
			switch format {
			case "ecdsa":
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				der, err := x509.MarshalPKCS8PrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				raw = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
			case "bad-der":
				raw = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("bad")})
			case "headers":
				b, err := os.ReadFile(f.o.PrivateKeyPath)
				if err != nil {
					t.Fatal(err)
				}
				block, _ := pem.Decode(b)
				block.Headers = map[string]string{"unexpected": "header"}
				raw = pem.EncodeToMemory(block)
			}
			put(t, f.o.PrivateKeyPath, raw)
			_, err := Sign(f.o)
			invalid(t, err)
		})
	}
}

func TestPrivateGuardUsesOpenedHandle(t *testing.T) {
	f := fresh(t)
	weakenPrivate(t, f.o.PrivateKeyPath)
	opened, err := openOrdinary(f.o.PrivateKeyPath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	// A secure replacement at the original pathname cannot authorize the weak
	// file descriptor which will actually supply the private bytes.
	if err := os.Rename(f.o.PrivateKeyPath, f.o.PrivateKeyPath+"-weak"); err != nil {
		t.Fatal(err)
	}
	put(t, f.o.PrivateKeyPath, []byte("replacement"))
	protectPrivate(t, f.o.PrivateKeyPath)
	if err := privatePermissions(opened); err == nil {
		t.Fatal("private guard checked a replacement path instead of the opened file")
	}
}

func TestVerifyCannotTrustItsOwnRootOrExpiredRoot(t *testing.T) {
	f := fresh(t)
	signOK(t, f)
	o := f.verify()
	o.TrustedRootPath = filepath.Join(f.o.OutputDir, "runtime-keys/root.pb")
	_, err := Verify(o)
	invalid(t, err)
	f.key.NotAfter = timestamppb.New(time.Now().Add(-time.Hour))
	put(t, f.o.RootPath, marshal(t, f.key))
	put(t, filepath.Join(f.o.OutputDir, "runtime-keys/root.pb"), marshal(t, f.key))
	_, err = Verify(f.verify())
	invalid(t, err)
}

func TestOCIEqualSizeSingleByteSubstitution(t *testing.T) {
	for _, leaf := range []string{"manifest.json", "config.json"} {
		t.Run(leaf, func(t *testing.T) {
			f := fresh(t)
			signOK(t, f)
			original := f.manifest
			if leaf == "config.json" {
				original = f.conf
			}
			raw := bytes.Clone(original)
			raw[len(raw)-1] = ' ' // Valid JSON remains valid; only the digest differs.
			put(t, proofFile(&f, leaf), raw)
			_, err := Verify(f.verify())
			invalid(t, err)
			if !errors.Is(err, releaseverify.ErrInvalid) {
				t.Fatalf("production digest check not reached: %v", err)
			}
		})
	}
}

func TestLinksAreRejected(t *testing.T) {
	for _, kind := range []string{"private", "manifest", "root", "bundle", "proof-dir", "proof-file", "parent"} {
		t.Run(kind, func(t *testing.T) {
			f := fresh(t)
			if kind == "bundle" || strings.HasPrefix(kind, "proof") || kind == "parent" {
				signOK(t, f)
			}
			var path string
			switch kind {
			case "private":
				path = f.o.PrivateKeyPath
			case "manifest":
				path = f.o.ManifestPath
			case "root":
				path = f.o.RootPath
			case "bundle":
				path = f.o.OutputDir
			case "proof-dir":
				path = filepath.Dir(proofFile(&f, "manifest.json"))
			case "proof-file":
				path = proofFile(&f, "config.json")
			case "parent":
				path = filepath.Join(filepath.Dir(f.o.OutputDir), "alias")
				if err := os.Symlink(filepath.Dir(f.o.OutputDir), path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				f.o.OutputDir = filepath.Join(path, "bundle")
			}
			if kind != "parent" {
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-original", path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if kind == "private" || kind == "manifest" || kind == "root" {
				_, err := Sign(f.o)
				invalid(t, err)
			} else {
				_, err := Verify(f.verify())
				invalid(t, err)
			}
		})
	}
	for _, kind := range []string{"private", "proof-file"} {
		t.Run("hardlink-"+kind, func(t *testing.T) {
			f := fresh(t)
			path := f.o.PrivateKeyPath
			if kind == "proof-file" {
				signOK(t, f)
				path = proofFile(&f, "manifest.json")
			}
			if err := os.Link(path, filepath.Join(filepath.Dir(f.o.OutputDir), "hardlink")); err != nil {
				t.Fatal(err)
			}
			if kind == "private" {
				_, err := Sign(f.o)
				invalid(t, err)
			} else {
				_, err := Verify(f.verify())
				invalid(t, err)
			}
		})
	}
}
