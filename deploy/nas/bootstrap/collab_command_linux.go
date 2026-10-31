package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hansjone/auriga/deploy/bootstrap/collab"
	"github.com/hansjone/auriga/deploy/bootstrap/materials"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
	"google.golang.org/protobuf/proto"
)

type nasCollabReceipt struct {
	Version   int    `json:"version"`
	Base      string `json:"base"`
	Input     string `json:"input"`
	Materials string `json:"materials"`
	Origin    string `json:"origin"`
}

func nasCollabCommand(args []string) error {
	if args[0] == "collab-base" {
		if len(args) != 2 || os.Geteuid() != 10001 || os.Getegid() != 10001 {
			return fmt.Errorf("fixed NAS base verifier required")
		}
		digest, err := checkNASCollabBase(args[1])
		if err != nil {
			return err
		}
		fmt.Print(digest)
		return nil
	}
	f := flag.NewFlagSet("collab", flag.ContinueOnError)
	base := f.String("base", "", "sealed NAS materials")
	out := f.String("out", "", "separate root-owned activation source")
	input := f.String("input", "", "explicit offline approved installation input")
	origin := f.String("public-origin", "", "approved HTTPS presentation origin")
	check := f.Bool("check", false, "verify existing activation without writing")
	stopped := f.Bool("stopped-confirmed", false, "operator stopped platform, edge, network and collab")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *base == "" || *out == "" || os.Geteuid() != 0 {
		return fmt.Errorf("collab requires root, base and output")
	}
	// Every output ancestor belongs to root; never adopt or repair foreign trees.
	if err := checkNASCollabParent(filepath.Dir(*out)); err != nil {
		return err
	}
	if !filepath.IsAbs(*out) || filepath.Clean(*out) != *out || strings.Contains(*out, "\\") {
		return fmt.Errorf("canonical collab output required")
	}
	if _, err := os.Lstat(*out + ".pending"); !os.IsNotExist(err) {
		return fmt.Errorf("interrupted NAS collab activation; inspect pending source")
	}
	accessRaw, err := os.ReadFile(filepath.Join(*base, "access.json"))
	if err != nil {
		return err
	}
	access, err := deploymentconfig.Load(accessRaw)
	if err != nil {
		return err
	}
	presentation, err := nasCollabOrigin(access, *origin, os.Getenv("AURIGA_LINKS_ALLOWED_ORIGINS"))
	if err != nil {
		return err
	}
	baseHash, err := verifyNASCollabBase(*base)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(*out); err == nil {
		receipt, err := checkNASCollabSource(*out, baseHash)
		if err != nil {
			return err
		}
		if receipt.Origin != presentation {
			return fmt.Errorf("NAS collab presentation changed")
		}
		if *check {
			if *input != "" || *stopped {
				return fmt.Errorf("invalid collab check arguments")
			}
			return nil
		}
		c, err := readNASCollabInput(*input, *base)
		if err != nil {
			return err
		}
		defer c.Clear()
		if c.Digest() != receipt.Input {
			return fmt.Errorf("NAS collab inputs changed; rotation is a separate operation")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if *check {
		return fmt.Errorf("NAS collab activation is absent")
	}
	if !*stopped {
		return fmt.Errorf("stop NAS platform edge network collab before first activation")
	}
	c, err := readNASCollabInput(*input, *base)
	if err != nil {
		return err
	}
	defer c.Clear()
	inputHash := c.Digest()
	raw, err := os.ReadFile(filepath.Join(*base, "access.json"))
	if err != nil {
		return err
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return err
	}
	stage := *out + ".pending"
	if err := os.Mkdir(stage, 0700); err != nil {
		return fmt.Errorf("NAS collab activation lock refused")
	}
	// Keep interrupted material for explicit inspection; no automatic repair.
	if err := writeNASCollab(stage, *base, c, cfg, presentation); err != nil {
		return err
	}
	digest, err := nasCollabSourceDigest(stage)
	if err != nil {
		return err
	}
	fresh, err := verifyNASCollabBase(*base)
	if err != nil || fresh != baseHash {
		return fmt.Errorf("NAS base changed during activation")
	}
	raw, err = json.Marshal(nasCollabReceipt{1, baseHash, inputHash, digest, presentation})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(stage, "activation.receipt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(stage, *out); err != nil {
		return err
	}
	return nil
}

func readNASCollabInput(path, base string) (*collab.Input, error) {
	raw, err := os.ReadFile(filepath.Join(base, "access.json"))
	if err != nil {
		return nil, err
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return nil, err
	}
	c, err := collab.Read(path, cfg.TenantID())
	if err != nil || c == nil {
		return nil, fmt.Errorf("explicit NAS collab approval required")
	}
	if err := c.CheckState(); err != nil {
		c.Clear()
		return nil, err
	}
	return c, nil
}

func verifyNASCollabBase(base string) (string, error) {
	binary, err := os.Open("/proc/self/exe")
	if err != nil {
		return "", err
	}
	defer binary.Close()
	cmd := exec.Command("/proc/self/fd/3", "collab-base", base)
	cmd.ExtraFiles = []*os.File{binary}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001, Groups: []uint32{10001}}}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("NAS base verifier failed: %s", strings.TrimSpace(stderr.String()))
	}
	if len(raw) != 64 {
		return "", fmt.Errorf("NAS base verifier output refused")
	}
	if _, err := hex.DecodeString(string(raw)); err != nil {
		return "", err
	}
	return string(raw), nil
}

func checkNASCollabBase(base string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(base, "access.json"))
	if err != nil {
		return "", err
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return "", err
	}
	set := nasMaterialSet(cfg.IngressKind() == "direct" && cfg.PortalOrigin().Scheme == "https")
	if raw, err := os.ReadFile(filepath.Join(base, "templates", "real-dsh.pb")); err == nil {
		var spec nodev1.InstanceSpec
		if proto.Unmarshal(raw, &spec) != nil {
			return "", fmt.Errorf("NAS template refused")
		}
		digest := spec.GetDsh().GetReleaseEnvelope().GetImageDigest()
		if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
			return "", fmt.Errorf("NAS runtime proof refused")
		}
		if _, err := hex.DecodeString(digest[7:]); err != nil {
			return "", err
		}
		proof := "runtime-proofs/" + digest[7:]
		set.PublicFiles = append(set.PublicFiles, "templates/real-dsh.pb", "runtime-keys/root.pb", proof+"/manifest.json", proof+"/config.json")
		set.PublicFileLimits = map[string]int64{proof + "/manifest.json": 4 << 20, proof + "/config.json": 4 << 20}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	guard, err := openNASMaterialGuard(base, set)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	if err := guard.Validate(false); err != nil {
		return "", err
	}
	installation, err := materials.Open(base)
	if err != nil {
		return "", err
	}
	receipt, err := installation.Read("bootstrap.receipt")
	installation.Close()
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(receipt), "\n")
	if len(parts) != 3 {
		return "", fmt.Errorf("NAS receipt refused")
	}
	reuse, err := materials.CheckReceipt(base, parts[0], set)
	if err != nil || !reuse {
		return "", fmt.Errorf("NAS base receipt does not match materials")
	}
	sum := sha256.Sum256(receipt)
	return hex.EncodeToString(sum[:]), nil
}

func checkNASCollabParent(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("canonical collab parent required")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("NAS collab parent refused")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("NAS collab parent ownership refused")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}

func nasCollabFiles() []string {
	files := []string{"edge.config.json", "roles.sql", "pki/ca.crt", "pki/edge-collab.crt", "pki/edge-collab.key", "network/runtime.json", "network/protocol.key", "network-platform/platform.properties", "network-platform/gateway.secret", "network-platform/collab.secret", "network-platform/managed-network.json"}
	for _, name := range collab.Leaves {
		files = append(files, "collab-installation/"+name)
	}
	return files
}

func nasCollabSourceDigest(dir string) (string, error) {
	allowed := map[string]bool{".": true, "activation.receipt": true}
	for _, name := range nasCollabFiles() {
		allowed[name] = true
		allowed[filepath.ToSlash(filepath.Dir(name))] = true
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || !allowed[filepath.ToSlash(rel)] {
			return fmt.Errorf("unknown NAS collab material")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 {
			return fmt.Errorf("NAS collab owner refused")
		}
		if entry.IsDir() {
			if info.Mode().Perm() != 0700 {
				return fmt.Errorf("NAS collab directory refused")
			}
		} else if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Nlink != 1 {
			return fmt.Errorf("NAS collab leaf refused")
		}
		return nil
	}); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, name := range nasCollabFiles() {
		raw, err := collab.ReadFile(filepath.Join(dir, filepath.FromSlash(name)), 1<<20, true)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		clear(raw)
		_, _ = fmt.Fprintf(h, "%s:%x\n", name, sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkNASCollabSource(out, baseHash string) (*nasCollabReceipt, error) {
	raw, err := collab.ReadFile(filepath.Join(out, "activation.receipt"), 65536, true)
	if err != nil {
		return nil, err
	}
	var receipt nasCollabReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.Version != 1 || receipt.Base != baseHash || len(receipt.Input) != 64 {
		return nil, fmt.Errorf("NAS collab receipt refused")
	}
	digest, err := nasCollabSourceDigest(out)
	if err != nil || digest != receipt.Materials {
		return nil, fmt.Errorf("NAS collab source changed")
	}
	return &receipt, nil
}
