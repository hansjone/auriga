package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

// The bootstrap receipt and collab activation remain immutable. Each additional
// receipt binds a complete public projection to that original installation.
type runtimeUpgradeReceipt struct {
	Base, Previous, Body, Files string
	Sequence                    uint64
}
type runtimeActivation struct {
	Current, Previous string
	Pending           bool
}
type runtimeGeneration struct {
	name     string
	receipt  runtimeUpgradeReceipt
	material *runtimebundle.Material
	hash     string
}

func nasRuntimeUpgrade(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("runtime-upgrade", flag.ContinueOnError)
	base := f.String("base", "", "original sealed material directory")
	out := f.String("out", "", "root-owned append-only runtime source")
	action := f.String("action", "path", "stage, commit, rollback or path")
	bundle := f.String("bundle", "", "operator-approved public candidate bundle")
	image := f.String("image", "", "operator-approved exact image locator")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || os.Geteuid() != 0 || *base == "" || *out == "" {
		return fmt.Errorf("runtime upgrade requires root, base and output")
	}
	if *action != "stage" && *action != "commit" && *action != "rollback" && *action != "path" {
		return fmt.Errorf("unsupported runtime upgrade action")
	}
	if (*action != "path") != (*bundle != "") || (*action != "path") != (*image != "") {
		return fmt.Errorf("invalid runtime upgrade arguments")
	}
	if !filepath.IsAbs(*out) || filepath.Clean(*out) != *out || strings.ContainsAny(*out, "\\\n\r") {
		return fmt.Errorf("canonical runtime output required")
	}
	if err := checkNASCollabParent(filepath.Dir(*out)); err != nil {
		return err
	}
	baseHash, err := verifyNASCollabBase(*base)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(*base, "templates/real-dsh.pb"))
	if err != nil {
		return fmt.Errorf("signed base runtime required: %w", err)
	}
	spec := new(nodev1.InstanceSpec)
	if err := proto.Unmarshal(raw, spec); err != nil || spec.GetDsh().GetReleaseEnvelope().GetReleaseSequence() == 0 {
		return fmt.Errorf("signed base template required")
	}
	if _, err := os.Lstat(*out); os.IsNotExist(err) {
		if *action == "path" {
			_, err = fmt.Fprint(stdout, *base)
			return err
		}
		if *action != "stage" {
			return fmt.Errorf("no runtime activation")
		}
		if err := os.Mkdir(*out, 0755); err != nil {
			return err
		}
		if err := os.Chmod(*out, 0755); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := checkNASCollabParent(*out); err != nil {
		return err
	}
	// Serialize readers and pointer mutations. Never follow a supplied lock link.
	fd, err := unix.Open(filepath.Join(*out, ".lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("unsafe runtime lock")
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(fd, unix.LOCK_UN) }()
	root := filepath.Join(*base, "runtime-keys/root.pb") // sole authority; never candidate root
	gens, err := readRuntimeGenerations(*out, baseHash, root)
	if err != nil {
		return err
	}
	active := runtimeActivation{}
	if b, err := runtimeReadLeaf(filepath.Join(*out, "active.json"), 0600, 65536); err == nil {
		if err := runtimeDecode(b, &active); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if len(gens) > 0 && *action != "stage" {
		return fmt.Errorf("runtime pointer absent; retry the staged candidate")
	}
	byName := map[string]*runtimeGeneration{}
	for i := range gens {
		byName[gens[i].name] = &gens[i]
	}
	for _, name := range []string{active.Current, active.Previous} {
		if name != "" && byName[name] == nil {
			return fmt.Errorf("runtime pointer names absent generation")
		}
	}
	if active.Pending && (active.Current == "" || active.Current == active.Previous) {
		return fmt.Errorf("invalid pending runtime pointer")
	}
	if !active.Pending && active.Previous != "" {
		return fmt.Errorf("invalid committed runtime pointer")
	}
	if *action == "path" {
		path := *base
		if active.Current != "" {
			path = filepath.Join(*out, active.Current)
		}
		_, err := fmt.Fprint(stdout, path)
		return err
	}
	m, err := runtimebundle.Verify(runtimebundle.VerifyOptions{Dir: *bundle, TrustedRootPath: root, Policy: runtimebundle.Policy{OS: "linux", Arch: "amd64", AllowedImage: *image}})
	if err != nil {
		return err
	}
	if *action == "commit" || *action == "rollback" {
		g := byName[active.Current]
		if g == nil || g.material.Runtime().Image != *image || g.receipt.Body != m.BodyDigest() {
			return fmt.Errorf("activation differs from requested image")
		}
		if !active.Pending {
			if *action == "commit" {
				return nil
			}
			return fmt.Errorf("only pending activation can roll back")
		}
		if *action == "rollback" {
			active.Current = active.Previous
		}
		active.Previous = ""
		active.Pending = false
		return runtimeWriteActive(*out, active)
	}
	seq := m.Runtime().GetReleaseEnvelope().GetReleaseSequence()
	high := spec.GetDsh().GetReleaseEnvelope().GetReleaseSequence()
	if len(gens) > 0 {
		high = gens[len(gens)-1].receipt.Sequence
	}
	name := fmt.Sprintf("s-%020d", seq)
	if seq < high || seq == high && byName[name] == nil {
		return fmt.Errorf("runtime sequence rollback refused")
	}
	if existing := byName[name]; existing != nil {
		if existing.receipt.Body != m.BodyDigest() || existing.material.Runtime().Image != *image {
			return fmt.Errorf("runtime sequence conflicts with prior receipt")
		}
		if active.Current == name {
			return nil
		}
		if seq != high || active.Pending {
			return fmt.Errorf("another activation is pending or newer")
		}
	} else {
		if active.Pending {
			return fmt.Errorf("finish or roll back pending activation first")
		}
		previous := ""
		if len(gens) > 0 {
			previous = gens[len(gens)-1].hash
		}
		stage, err := os.MkdirTemp(*out, ".pending-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		if err := os.Chmod(stage, 0755); err != nil {
			return err
		}
		if err := writeRuntimeGeneration(stage, *base, spec, m, gens); err != nil {
			return err
		}
		digest, err := runtimeTreeDigest(stage)
		if err != nil {
			return err
		}
		receipt := runtimeUpgradeReceipt{Base: baseHash, Previous: previous, Body: m.BodyDigest(), Files: digest, Sequence: seq}
		b, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if err := runtimeCreate(filepath.Join(stage, "upgrade.receipt"), b); err != nil {
			return err
		}
		if fresh, err := verifyNASCollabBase(*base); err != nil || fresh != baseHash {
			return fmt.Errorf("sealed source changed during upgrade")
		}
		if err := runtimeSyncTree(stage); err != nil {
			return err
		}
		if err := os.Rename(stage, filepath.Join(*out, name)); err != nil {
			return err
		}
		if err := runtimeSyncDir(*out); err != nil {
			return err
		}
	}
	return runtimeWriteActive(*out, runtimeActivation{Current: name, Previous: active.Current, Pending: true})
}

func runtimeReadLeaf(path string, mode os.FileMode, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	s, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok || !s.Mode().IsRegular() || s.Mode().Perm() != mode || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || s.Size() > limit {
		return nil, fmt.Errorf("runtime leaf metadata refused")
	}
	if s.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, fmt.Errorf("runtime special mode refused")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("runtime leaf read refused")
	}
	return b, nil
}

func runtimeDecode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("runtime receipt or pointer refused")
	}
	return nil
}

func readRuntimeGenerations(out, baseHash, root string) ([]runtimeGeneration, error) {
	entries, err := os.ReadDir(out)
	if err != nil {
		return nil, err
	}
	var result []runtimeGeneration
	previous := ""
	for _, e := range entries {
		name := e.Name()
		if name == ".lock" || name == "active.json" {
			continue
		}
		if len(name) != 22 || !strings.HasPrefix(name, "s-") {
			return nil, fmt.Errorf("unknown or interrupted runtime material")
		}
		path := filepath.Join(out, name)
		b, err := runtimeReadLeaf(filepath.Join(path, "upgrade.receipt"), 0644, 65536)
		if err != nil {
			return nil, err
		}
		var r runtimeUpgradeReceipt
		if err := runtimeDecode(b, &r); err != nil {
			return nil, err
		}
		if r.Base != baseHash || r.Previous != previous || r.Sequence == 0 || name != fmt.Sprintf("s-%020d", r.Sequence) {
			return nil, fmt.Errorf("runtime receipt chain refused")
		}
		digest, err := runtimeTreeDigest(path)
		if err != nil || digest != r.Files {
			return nil, fmt.Errorf("runtime generation changed")
		}
		m, err := runtimebundle.Verify(runtimebundle.VerifyOptions{Dir: filepath.Join(path, "release"), TrustedRootPath: root, Policy: runtimebundle.Policy{OS: "linux", Arch: "amd64", AllowedImage: runtimeImageFromTemplate(path)}})
		if err != nil {
			return nil, err
		}
		if m.BodyDigest() != r.Body || m.Runtime().GetReleaseEnvelope().GetReleaseSequence() != r.Sequence {
			return nil, fmt.Errorf("runtime receipt declaration mismatch")
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(b))
		previous = hash
		result = append(result, runtimeGeneration{name, r, m, hash})
	}
	return result, nil
}

func runtimeImageFromTemplate(path string) string {
	b, err := runtimeReadLeaf(filepath.Join(path, "templates/real-dsh.pb"), 0644, 1<<20)
	if err != nil {
		return ""
	}
	s := new(nodev1.InstanceSpec)
	if proto.Unmarshal(b, s) != nil {
		return ""
	}
	return s.GetDsh().GetImage()
}

func runtimeTreeDigest(path string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(path, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		s, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := s.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 {
			return fmt.Errorf("runtime owner refused")
		}
		if e.IsDir() {
			if s.Mode().Perm() != 0755 || s.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
				return fmt.Errorf("runtime directory refused")
			}
			return nil
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		b, err := runtimeReadLeaf(p, 0644, 4<<20)
		if err != nil {
			return err
		}
		if rel != "upgrade.receipt" {
			_, err = fmt.Fprintf(h, "%s:%x\n", rel, sha256.Sum256(b))
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func runtimeCreate(path string, b []byte) error {
	if err := runtimeMkdir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if err := f.Chmod(0644); err != nil {
		_ = f.Close()
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
	return closeErr
}

func runtimeMkdir(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := runtimeMkdir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0755); err != nil {
		return err
	}
	return os.Chmod(path, 0755)
}

func runtimeSyncTree(path string) error {
	var dirs []string
	if err := filepath.WalkDir(path, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := runtimeSyncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func writeRuntimeGeneration(out, base string, spec *nodev1.InstanceSpec, m *runtimebundle.Material, gens []runtimeGeneration) error {
	// Retain all previously approved proofs/locators for instances still running
	// older generations; the template alone selects the new default runtime.
	proofs := map[string][]byte{}
	add := func(prefix string, material *runtimebundle.Material) {
		proofs[prefix+"/manifest.json"] = material.Manifest()
		proofs[prefix+"/config.json"] = material.Config()
	}
	baseProof := "runtime-proofs/" + strings.TrimPrefix(spec.GetDsh().GetReleaseEnvelope().GetImageDigest(), "sha256:")
	for _, leaf := range []string{"manifest.json", "config.json"} {
		b, err := os.ReadFile(filepath.Join(base, baseProof, leaf))
		if err != nil {
			return err
		}
		proofs[baseProof+"/"+leaf] = b
	}
	images := []string{spec.GetDsh().Image}
	for _, g := range gens {
		add(runtimeProofDirectory(g.material), g.material)
		images = append(images, g.material.Runtime().Image)
	}
	add(runtimeProofDirectory(m), m)
	images = append(images, m.Runtime().Image)
	for p, b := range proofs {
		if err := runtimeCreate(filepath.Join(out, p), b); err != nil {
			return err
		}
	}
	copySpec := proto.Clone(spec).(*nodev1.InstanceSpec)
	copySpec.Dsh = m.Runtime()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(copySpec)
	if err != nil {
		return err
	}
	for p, raw := range map[string][]byte{"templates/real-dsh.pb": b, "runtime-keys/root.pb": m.RootBytes(), "release/runtime.pb": m.RuntimeBytes(), "release/runtime-keys/root.pb": m.RootBytes(), "release/" + runtimeProofDirectory(m) + "/manifest.json": m.Manifest(), "release/" + runtimeProofDirectory(m) + "/config.json": m.Config()} {
		if err := runtimeCreate(filepath.Join(out, p), raw); err != nil {
			return err
		}
	}
	sort.Strings(images)
	var policy strings.Builder
	policy.WriteString("allowedImageRefs:\n")
	for i, image := range images {
		if i == 0 || image != images[i-1] {
			fmt.Fprintf(&policy, "  - %q\n", image)
		}
	}
	policy.WriteString("allowedImageRepos:\n")
	repos := map[string]bool{}
	for _, image := range images {
		repo, _, _ := strings.Cut(image, "@")
		if !repos[repo] {
			fmt.Fprintf(&policy, "  - %q\n", repo)
			repos[repo] = true
		}
	}
	fmt.Fprintf(&policy, "maxCPUMillis: %d\nmaxMemoryMB: %d\nmaxPIDs: %d\nmaxArtifactBytes: 2147483648\n", spec.GetResources().GetCpuMillis(), spec.GetResources().GetMemoryMb(), spec.GetResources().GetPids())
	return runtimeCreate(filepath.Join(out, "agent-policy.yaml"), []byte(policy.String()))
}

func runtimeWriteActive(out string, active runtimeActivation) error {
	b, err := json.Marshal(active)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(out, ".active-")
	if err != nil {
		return err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
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
	if err := os.Rename(path, filepath.Join(out, "active.json")); err != nil {
		return err
	}
	return runtimeSyncDir(out)
}

func runtimeSyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
