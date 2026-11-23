package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

// Source is the operator's explicit request; execution only uses the pinned
// public snapshot outside DATA, never a newly read removable input on resume.
type installRuntimeOptions struct {
	Bundle string `json:"bundle"`
	Root   string `json:"root"`
	Image  string `json:"image"`
}

type installRuntime struct {
	Source installRuntimeOptions `json:"source"`
	SHA256 string                `json:"sha256"`
}

type preparedInstallRuntime struct {
	directory string
	identity  installRuntime
}

func (r installRuntimeOptions) enabled() bool {
	return r.Bundle != "" || r.Root != "" || r.Image != ""
}

func (r installRuntimeOptions) validate() error {
	if !r.enabled() {
		return nil
	}
	if r.Bundle == "" || r.Root == "" || r.Image == "" {
		return errors.New("--runtime-bundle、--runtime-root、--runtime-image 必须一并提供")
	}
	for _, p := range []string{r.Bundle, r.Root} {
		if !pathpkg.IsAbs(p) || pathpkg.Clean(p) != p || strings.ContainsAny(p, "\x00\r\n\t") {
			return errors.New("runtime 输入需要规范绝对物理路径")
		}
	}
	if !immutable.MatchString(r.Image) || !strings.Contains(r.Image, "@sha256:") || strings.ContainsAny(r.Image, "\x00\r\n\t") {
		return errors.New("--runtime-image 需要完整 repo@sha256 locator")
	}
	return nil
}

func uniqueRuntimeFlags(args []string) error {
	seen := map[string]bool{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "runtime-bundle", "runtime-root", "runtime-image":
			if seen[name] {
				return fmt.Errorf("重复 --%s", name)
			}
			seen[name] = true
		}
	}
	return nil
}

func runtimeSnapshot(data string) string { return filepath.Join(data+".oneclick", "runtime-inputs") }

func runtimeSnapshotOptions(dir, image string) installRuntimeOptions {
	return installRuntimeOptions{Bundle: filepath.Join(dir, "bundle"), Root: filepath.Join(dir, "root.pb"), Image: image}
}

// The caller must first verify the outer package against independently pinned
// release trust. Keep cryptography and the exact bundle/platform/locator policy
// in the original shipped runtime-release verifier.
func verifyInstallRuntime(pkg string, r installRuntimeOptions) error {
	return command(nil, nil, nil, filepath.Join(pkg, "deploy-nas/bin/runtime-release"),
		"verify", "--dir", r.Bundle, "--root", r.Root, "--os", "linux", "--arch", "amd64", "--allow-image", r.Image)
}

// 测试可注入点。
var (
	verifyInstallRuntimeFunc   = verifyInstallRuntime
	attachBootstrapRuntimeFunc = attachBootstrapRuntime
)

// prepareRuntimeSnapshot 验证原始 runtime 输入、复制四项公开材料与 root.pb 到临时目录，
// 在副本上再次验签并计算快照摘要。供首装（install / deploy）与已安装站点 runtime attach 共用。
func prepareRuntimeSnapshot(pkg string, req installRuntimeOptions) (*preparedInstallRuntime, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if !req.enabled() {
		return nil, nil
	}
	// Validate original physical inputs before copying; copying must not turn a
	// linked, malformed or otherwise rejected original into an accepted bundle.
	if err := verifyInstallRuntimeFunc(pkg, req); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "auriga-install-runtime-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	names, err := regularFiles(req.Bundle)
	if err != nil {
		return nil, err
	}
	if len(names) != 4 {
		return nil, errors.New("runtime bundle 不是原四项公开材料闭集")
	}
	for _, name := range names {
		if err := copyRuntimePublic(filepath.Join(req.Bundle, name), filepath.Join(dir, "bundle", name)); err != nil {
			return nil, err
		}
	}
	if err := copyRuntimePublic(req.Root, filepath.Join(dir, "root.pb")); err != nil {
		return nil, err
	}
	snapshot := runtimeSnapshotOptions(dir, req.Image)
	if err := verifyInstallRuntimeFunc(pkg, snapshot); err != nil {
		return nil, err
	}
	hash, err := runtimeSnapshotDigest(dir)
	if err != nil {
		return nil, err
	}
	keep = true
	return &preparedInstallRuntime{directory: dir, identity: installRuntime{Source: req, SHA256: hash}}, nil
}

func prepareInstallRuntime(pkg string, o installOptions, unsigned bool) (*preparedInstallRuntime, error) {
	if err := o.Runtime.validate(); err != nil {
		return nil, err
	}
	existing, existingErr := readState(o.Data)
	if unsigned && (o.Runtime.enabled() || existing.Runtime != nil) {
		return nil, errors.New("runtime 首装或恢复需要已验签的外层安装包，不能使用 --allow-unsigned 缺签名例外")
	}
	if existingErr == nil && existing.Runtime == nil && o.Runtime.enabled() {
		return nil, errors.New("已有安装未登记 runtime 首装输入；拒绝通过 install 添加根或模板")
	}
	if existingErr == nil && existing.Runtime != nil {
		if err := checkPinnedInstallRuntime(pkg, existing); err != nil {
			return nil, err
		}
		if o.Runtime.enabled() && o.Runtime != existing.Runtime.Source {
			return nil, errors.New("runtime 首装请求与安装 state 不同；不得通过 install 替换")
		}
	}
	if !o.Runtime.enabled() {
		return nil, nil
	}
	prepared, err := prepareRuntimeSnapshot(pkg, o.Runtime)
	if err != nil {
		return nil, err
	}
	if existingErr == nil && existing.Runtime != nil && prepared.identity != *existing.Runtime {
		_ = os.RemoveAll(prepared.directory)
		return nil, errors.New("同路径 runtime 输入字节已变，拒绝重试漂移")
	}
	return prepared, nil
}

func copyRuntimePublic(source, target string) error {
	if err := noSymlinks(source); err != nil {
		return err
	}
	// Every original leaf is public and bounded by runtime-release's own limits.
	// Read a bounded snapshot instead of allowing an expanding source to fill DATA.
	raw, err := smallFile(source, 4<<20)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	return errors.Join(writeErr, f.Close())
}

func runtimeSnapshotDigest(dir string) (string, error) {
	if err := noSymlinks(dir); err != nil {
		return "", err
	}
	names, err := regularFiles(dir)
	if err != nil {
		return "", err
	}
	if len(names) != 5 {
		return "", errors.New("runtime 快照缺失或含额外材料")
	}
	sums := map[string]string{}
	for _, name := range names {
		raw, err := smallFile(filepath.Join(dir, name), 4<<20)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		sums[name] = hex.EncodeToString(sum[:])
	}
	raw, err := json.Marshal(sums)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func pinInstallRuntime(s *state, p *preparedInstallRuntime) error {
	if p == nil {
		return nil
	}
	dir := runtimeSnapshot(s.Data)
	if err := copyTree(p.directory, dir); err != nil {
		return err
	}
	s.Runtime = &p.identity
	return checkPinnedInstallRuntime(s.Package, *s)
}

func checkPinnedInstallRuntime(pkg string, s state) error {
	if s.Runtime == nil {
		return nil
	}
	if !s.Runtime.Source.enabled() || s.Runtime.Source.validate() != nil || !hex64.MatchString(s.Runtime.SHA256) {
		return errors.New("runtime 安装 state 无效")
	}
	dir := runtimeSnapshot(s.Data)
	hash, err := runtimeSnapshotDigest(dir)
	if err != nil {
		return err
	}
	if hash != s.Runtime.SHA256 {
		return errors.New("已固定 runtime 快照摘要不符，保留现场并拒绝替换")
	}
	if err := verifyInstallRuntimeFunc(pkg, runtimeSnapshotOptions(dir, s.Runtime.Source.Image)); err != nil {
		return err
	}
	after, err := runtimeSnapshotDigest(dir)
	if err != nil || hash != after {
		return errors.New("runtime 快照在校验期间变化")
	}
	return nil
}

func installRuntimeEnv(s state) map[string]string {
	if s.Runtime == nil {
		return nil
	}
	r := runtimeSnapshotOptions(runtimeSnapshot(s.Data), s.Runtime.Source.Image)
	return map[string]string{"AURIGA_RUNTIME_BUNDLE": r.Bundle, "AURIGA_RUNTIME_ROOT": r.Root, "AURIGA_RUNTIME_IMAGE": r.Image}
}

func resumeInstallRuntime(s state, requested installRuntimeOptions, prepared *preparedInstallRuntime) error {
	if s.Runtime == nil {
		if requested.enabled() || prepared != nil {
			return errors.New("已有安装未登记 runtime 首装输入；拒绝添加根或模板")
		}
		return nil
	}
	if requested.enabled() && (prepared == nil || prepared.identity != *s.Runtime) {
		return errors.New("runtime 首装请求与安装 state 不符")
	}
	if err := checkPinnedInstallRuntime(s.Package, s); err != nil {
		return err
	}
	raw, err := smallFile(filepath.Join(s.Data, "stack.env"), 1<<20)
	if err != nil {
		return err
	}
	want := installRuntimeEnv(s)
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		runtimeKey := strings.TrimSpace(key)
		if !ok || (runtimeKey != "AURIGA_RUNTIME_BUNDLE" && runtimeKey != "AURIGA_RUNTIME_ROOT" && runtimeKey != "AURIGA_RUNTIME_IMAGE") {
			continue
		}
		if key != runtimeKey {
			return errors.New("stack.env runtime 键含额外空白")
		}
		if expected, ok := want[key]; !ok || value != expected {
			return errors.New("stack.env runtime 输入与固定 state 不符或重复")
		}
		delete(want, key)
	}
	if len(want) != 0 {
		return errors.New("stack.env 缺少固定 runtime 输入")
	}
	return nil
}
