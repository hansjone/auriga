package main

import (
	"archive/tar"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveRejectsTraversalLinksDuplicates(t *testing.T) {
	for _, kind := range []string{"../escape", "/escape", "symlink", "duplicate", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bad.tar")
			f, err := os.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			w := tar.NewWriter(f)
			h := &tar.Header{Name: kind, Mode: 0644, Typeflag: tar.TypeReg}
			if kind == "symlink" {
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "/etc/passwd"
			}
			if kind == "hardlink" {
				h.Typeflag = tar.TypeLink
				h.Linkname = "/etc/passwd"
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if kind == "duplicate" {
				if err := w.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := unpack(file, t.TempDir()); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}

func TestArchiveRoundTripAndSignature(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "payload"), []byte("data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(d); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDir := t.TempDir()
	privatePath := filepath.Join(keyDir, "private.pem")
	publicPath := filepath.Join(keyDir, "public.pem")
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	der, err = x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := signSums(d, privatePath); err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(d, publicPath); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "package.tar")
	if err := pack(d, archive); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if err := unpack(archive, output); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(output); err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(output, publicPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(output, publicPath); err == nil {
		t.Fatal("accepted bad signature")
	}
}

func TestHealthJudgments(t *testing.T) {
	good := []byte(`[{"Service":"postgres","State":"running","Health":"healthy"},{"Service":"nats","State":"running"},{"Service":"registry","State":"running"},{"Service":"platform","State":"running","Health":"healthy"},{"Service":"edge","State":"running"},{"Service":"agent","State":"running"},{"Service":"bifrost","State":"running"},{"Service":"egress","State":"running"},{"Service":"panel-prepare","State":"exited","ExitCode":0}]`)
	if err := healthyContainers(good); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`[]`, `{"Service":"postgres","State":"running"}`, `[{"Service":"postgres","State":"running","Health":"unhealthy"}]`, `[{"Service":"prepare","State":"exited","ExitCode":1}]`, `invalid`} {
		if err := healthyContainers([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestDefaultConfigGeneration(t *testing.T) {
	root, err := filepath.Abs("../nas")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "deploy-nas"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"env.example", "access-ip.example.json", "access-domain.example.json"} {
		if err := copyFile(filepath.Join(root, name), filepath.Join(base, "deploy-nas", name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := state{Data: t.TempDir(), Package: base, Access: filepath.Join(t.TempDir(), "access.json"), Images: map[string]string{"POSTGRES": "sha256:" + strings.Repeat("a", 64)}}
	if err := makeAccess(installOptions{Address: "192.0.2.10"}, s); err != nil {
		t.Fatal(err)
	}
	if err := makeEnv(s); err != nil {
		t.Fatal(err)
	}
	var access struct {
		Mode         string
		PortalOrigin struct{ Host string }
	}
	if err := readJSONLoose(s.Access, &access); err != nil {
		t.Fatal(err)
	}
	if access.Mode != "ip" || access.PortalOrigin.Host != "192.0.2.10" {
		t.Fatalf("%+v", access)
	}
}
