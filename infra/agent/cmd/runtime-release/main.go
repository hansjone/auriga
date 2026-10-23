// runtime-release signs and verifies public Docker-only release materials.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "sign" && args[0] != "verify" && args[0] != "stage-layout") {
		return fmt.Errorf("usage: runtime-release sign|verify|stage-layout --help")
	}
	if args[0] == "stage-layout" {
		seen := map[string]bool{}
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-") {
				continue
			}
			name := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
			if seen[name] {
				return fmt.Errorf("duplicate --%s", name)
			}
			seen[name] = true
		}
	}
	fs := flag.NewFlagSet("runtime-release "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "independently fixed ArtifactSigningKey protobuf file")
	osName := fs.String("os", "", "explicit target OS: linux or windows")
	arch := fs.String("arch", "", "explicit target architecture: amd64 or arm64")
	var image, artifactID, version, issued, private, manifest, config, out, dir, allowed, layout *string
	var sequence *uint64
	var maxBytes *int64
	required := []string{"root", "os", "arch"}
	switch args[0] {
	case "sign":
		image = fs.String("image", "", "explicit publisher repo@sha256 locator")
		artifactID = fs.String("artifact", "", "artifact identifier")
		version = fs.String("version", "", "DSH version")
		sequence = fs.Uint64("sequence", 0, "publisher-assigned partition sequence")
		issued = fs.String("issued-at", "", "explicit canonical UTC RFC3339 timestamp")
		private = fs.String("private-key", "", "protected Ed25519 PKCS8 PEM file (never key bytes)")
		manifest = fs.String("manifest", "", "original single-platform manifest file")
		config = fs.String("config", "", "original config file")
		out = fs.String("out", "", "new output directory; parent must already exist")
		required = append(required, "image", "artifact", "version", "sequence", "issued-at", "private-key", "manifest", "config", "out")
	case "verify":
		dir = fs.String("dir", "", "public bundle directory")
		allowed = fs.String("allow-image", "", "independently approved exact repo@sha256 locator")
		required = append(required, "dir", "allow-image")
	case "stage-layout":
		dir = fs.String("dir", "", "signed public bundle directory")
		allowed = fs.String("allow-image", "", "independently approved exact repo@sha256 locator")
		layout = fs.String("layout", "", "untrusted single-image OCI layout directory")
		out = fs.String("out", "", "new minimal OCI layout directory")
		maxBytes = fs.Int64("max-bytes", 0, "explicit positive total byte budget")
		required = append(required, "dir", "allow-image", "layout", "out", "max-bytes")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	present := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { present[f.Name] = true })
	for _, name := range required {
		if !present[name] || fs.Lookup(name).Value.String() == "" {
			return fmt.Errorf("explicit --%s is required", name)
		}
	}
	if args[0] == "stage-layout" {
		if *maxBytes <= 0 {
			return fmt.Errorf("--max-bytes must be positive")
		}
		snapshot, err := runtimebundle.StageLayout(runtimebundle.StageLayoutOptions{Verify: runtimebundle.VerifyOptions{Dir: *dir, TrustedRootPath: *root, Policy: runtimebundle.Policy{OS: *osName, Arch: *arch, AllowedImage: *allowed}}, LayoutDir: *layout, OutputDir: *out, MaxBytes: *maxBytes})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "staged manifest=%s config=%s blobs=%d bytes=%d\n", snapshot.ManifestDigest, snapshot.ConfigDigest, snapshot.BlobCount, snapshot.TotalBytes)
		return err
	}
	var material *runtimebundle.Material
	var err error
	if args[0] == "sign" {
		t, parseErr := time.Parse(time.RFC3339Nano, *issued)
		if parseErr != nil || t.UTC().Format(time.RFC3339Nano) != *issued {
			return fmt.Errorf("issued-at must be canonical UTC RFC3339 with Z and shortest fractional seconds")
		}
		material, err = runtimebundle.Sign(runtimebundle.SignOptions{Image: *image, ArtifactID: *artifactID, Version: *version, OS: *osName, Arch: *arch, Sequence: *sequence, IssuedAt: timestamppb.New(t), RootPath: *root, PrivateKeyPath: *private, ManifestPath: *manifest, ConfigPath: *config, OutputDir: *out})
	} else {
		material, err = runtimebundle.Verify(runtimebundle.VerifyOptions{Dir: *dir, TrustedRootPath: *root, Policy: runtimebundle.Policy{OS: *osName, Arch: *arch, AllowedImage: *allowed}})
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "verified body=%s config=%s\n", material.BodyDigest(), material.ConfigID())
	return err
}
