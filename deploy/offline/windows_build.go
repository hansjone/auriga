package main

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type windowsBackend struct{ Kind, Distro string }

var wslDistroName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func parseWindowsBackend(args []string) (windowsBackend, []string, error) {
	b := windowsBackend{Kind: "auto", Distro: os.Getenv("AURIGA_WSL_DISTRO")}
	var rest []string
	for i := 0; i < len(args); i++ {
		name, value, equal := strings.Cut(args[i], "=")
		if name != "--backend" && name != "--wsl-distro" {
			rest = append(rest, args[i])
			continue
		}
		if !equal {
			i++
			if i == len(args) {
				return b, nil, errors.New("当前Windows后端参数缺少值")
			}
			value = args[i]
		}
		if name == "--backend" {
			b.Kind = value
		} else {
			b.Distro = value
		}
	}
	if b.Kind != "auto" && b.Kind != "docker" && b.Kind != "wsl" {
		return b, nil, errors.New("--backend 需要 auto、docker 或 wsl")
	}
	if b.Distro != "" && !wslDistroName.MatchString(b.Distro) {
		return b, nil, errors.New("无效WSL发行版名称")
	}
	if b.Kind == "auto" {
		if _, err := exec.LookPath("docker"); err == nil {
			b.Kind = "docker"
		} else {
			b.Kind = "wsl"
		}
	}
	return b, rest, nil
}

func (b windowsBackend) wslArgs(args ...string) []string {
	prefix := []string{"--user", "root"}
	if b.Distro != "" {
		prefix = append(prefix, "--distribution", b.Distro)
	}
	return append(append(prefix, "--exec"), args...)
}

func (b windowsBackend) doctor(source string, budget buildBudget) error {
	if b.Kind == "docker" {
		return buildEnvironment(budget)
	}
	linuxSource, err := capture("wsl.exe", b.wslArgs("wslpath", "-a", "-u", source)...)
	if err != nil {
		return fmt.Errorf("WSL2/发行版不可用；可改用Windows编辑后拷到Linux构建，离线安装步骤见MAINTAIN.md: %w", err)
	}
	return command(nil, os.Stderr, nil, "wsl.exe", b.wslArgs(strings.TrimSuffix(linuxSource, "/")+"/bin/auriga-cli", "doctor", "--build", "--memory-mib", fmt.Sprint(budget.MemoryMiB), "--cpus", fmt.Sprint(budget.CPUs))...)
}

type siteTransfer struct {
	Root, Version, Out, KitRoot string
	Entry                       devkitVersion
	Budget                      buildBudget
	Modes                       map[string]sourceFile
}

type siteBuildOptions struct {
	Out, Version, Key string
	Budget            buildBudget
}

func parseSiteBuild(args []string) (siteBuildOptions, error) {
	var o siteBuildOptions
	f := flags("当前Windows bundle")
	out := f.String("out", "", "")
	version := f.String("version", "", "")
	key := f.String("sign-key", "", "")
	build := f.Bool("build", false, "")
	offline := f.Bool("offline", false, "")
	memory := f.Int("memory-mib", 4096, "")
	cpus := f.Int("cpus", 2, "")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 || !*build || !*offline {
		return o, errors.New("当前Windows服务端打包需要 bundle --build --offline；安装/upgrade请在Linux服务器执行")
	}
	if *key == "" {
		return o, errors.New("离线现场构建必须 --sign-key <已登记站点构建私钥>")
	}
	return siteBuildOptions{Out: *out, Version: *version, Key: *key, Budget: buildBudget{*memory, *cpus}}, nil
}

func prepareSiteTransfer(args []string) (*siteTransfer, error) {
	o, err := parseSiteBuild(args)
	if err != nil {
		return nil, err
	}
	return materializeSiteTransfer(o)
}

func materializeSiteTransfer(o siteBuildOptions) (*siteTransfer, error) {
	source, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	m, err := readSourceManifest(source)
	if err != nil {
		return nil, fmt.Errorf("需要带auriga-source.json及双平台CLI的发行源码归档: %w", err)
	}
	var binding devkitBinding
	if err := readJSON(filepath.Join(source, ".auriga-devkit.json"), &binding); err != nil {
		return nil, errors.New("先运行 .\\auriga.ps1 devkit verify <完整套件目录>")
	}
	cat, err := readCatalog(binding.Root, filepath.Join(source, "release-public.pem"))
	if err != nil {
		return nil, err
	}
	entry, err := catalogVersion(cat, m.Source.UpstreamVersion)
	if err != nil {
		return nil, err
	}
	if entry.Commit != m.Source.Commit {
		return nil, errors.New("源码提交与累积套件版本不符")
	}
	if err := checkKitLocks(source, entry.Kit); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "auriga-site-transfer-")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(root)
		}
	}()
	if err := protectTransferRoot(root); err != nil {
		return nil, err
	}
	snap := filepath.Join(root, "source")
	info, err := snapshotSource(source, snap, o.Key)
	if err != nil {
		return nil, err
	}
	r := entry.Kit.Release
	r.Version = m.Source.UpstreamVersion
	if err := sourceVersion(info, r, o.Version, true); err != nil {
		return nil, err
	}
	names, err := archiveSourceNames(snap)
	if err != nil {
		return nil, err
	}
	modes, _, err := fileInventory(snap, names, m.Files)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"auriga-source.json", "SHA256SUMS", "SHA256SUMS.sig", "release-public.pem", "bin/auriga-cli"} {
		if _, err := os.Stat(filepath.Join(source, name)); os.IsNotExist(err) && name != "auriga-source.json" && name != "bin/auriga-cli" {
			continue
		}
		if err := copyFile(filepath.Join(source, name), filepath.Join(snap, name), 0644); err != nil {
			return nil, err
		}
	}
	modes["bin/auriga-cli"] = sourceFile{Mode: 0755}
	// Preserve the original signed catalog and its content-addressed blobs.
	// A locally rewritten unsigned devkit would lose the release trust chain.
	if err := copyTree(binding.Root, filepath.Join(root, "kit")); err != nil {
		return nil, err
	}
	if o.Key != "" {
		if err := copyFile(o.Key, filepath.Join(root, "signing-key.pem"), 0600); err != nil {
			return nil, err
		}
	}
	if o.Out == "" {
		o.Out = "auriga-offline-" + info.Version + ".tar"
	}
	absolute, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, err
	}
	if err := noSymlinks(absolute); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(absolute); !os.IsNotExist(err) {
		return nil, errors.New("输出包已存在或不可访问，不覆盖原包")
	}
	success = true
	return &siteTransfer{Root: root, Version: info.Version, Out: absolute, KitRoot: binding.Root, Entry: entry, Budget: o.Budget, Modes: modes}, nil
}

func (t *siteTransfer) stream(w io.Writer) error {
	names, err := regularFiles(t.Root)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	for _, name := range names {
		path := filepath.Join(t.Root, name)
		st, err := os.Stat(path)
		if err != nil {
			_ = tw.Close()
			return err
		}
		h, err := tar.FileInfoHeader(st, "")
		if err != nil {
			_ = tw.Close()
			return err
		}
		h.Name = name
		h.Mode = 0644
		if file, ok := t.Modes[strings.TrimPrefix(name, "source/")]; ok && strings.HasPrefix(name, "source/") {
			h.Mode = int64(file.Mode)
		}
		if name == "signing-key.pem" {
			h.Mode = 0600
		}
		if err := tw.WriteHeader(h); err != nil {
			_ = tw.Close()
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			_ = tw.Close()
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil || closeErr != nil {
			_ = tw.Close()
			return errors.Join(copyErr, closeErr)
		}
	}
	return tw.Close()
}

// Scripts are constants; paths and versions are positional arguments, never
// interpolated shell fragments. Stdout carries only the final binary tar.
const siteBuildScript = `set -eu
umask 077
mkdir -p "$site_root/tmp"
tar -xf - -C "$site_root"
export TMPDIR="$site_root/tmp"
cd "$site_root/source"
chmod 0755 bin/auriga-cli
./bin/auriga-cli devkit verify "$site_root/kit" >&2
if [ -f "$site_root/signing-key.pem" ]; then
 ./bin/auriga-cli bundle --build --offline --version "$1" --memory-mib "$2" --cpus "$3" --out "$site_root/result.tar" --sign-key "$site_root/signing-key.pem" >&2
else
 ./bin/auriga-cli bundle --build --offline --version "$1" --memory-mib "$2" --cpus "$3" --out "$site_root/result.tar" >&2
fi
cat "$site_root/result.tar"
`

func transferCommand(t *siteTransfer, name string, args []string) error {
	output, err := os.OpenFile(t.Out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(t.Out)
		}
	}()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { err := t.stream(writer); _ = writer.CloseWithError(err); done <- err }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = reader
	cmd.Stdout = output
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	_ = reader.Close()
	streamErr := <-done
	if err := errors.Join(runErr, streamErr, output.Close()); err != nil {
		return fmt.Errorf("当前Windows离线构建未成功（不保留半包）: %w", err)
	}
	hash, err := digest(t.Out)
	if err != nil {
		return err
	}
	if err := os.WriteFile(t.Out+".sha256", []byte(hash+"\n"), 0644); err != nil {
		return err
	}
	ok = true
	fmt.Printf("Linux离线安装包 %s\nSHA256 %s\n传给服务器执行 upgrade（保留数据，先备份）。\n", t.Out, hash)
	return nil
}

func (b windowsBackend) build(args []string) error {
	o, err := parseSiteBuild(args)
	if err != nil {
		return err
	}
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := b.doctor(source, o.Budget); err != nil {
		return err
	}
	t, err := materializeSiteTransfer(o)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(t.Root) }()
	positional := []string{t.Version, fmt.Sprint(t.Budget.MemoryMiB), fmt.Sprint(t.Budget.CPUs)}
	if b.Kind == "wsl" {
		script := "set -eu\nsite_root=$(mktemp -d /var/tmp/auriga-windows-XXXXXX)\ntrap 'rm -rf -- \"$site_root\"' EXIT HUP INT TERM\n" + siteBuildScript
		return transferCommand(t, "wsl.exe", b.wslArgs(append([]string{"sh", "-c", script, "auriga-build"}, positional...)...))
	}
	return dockerVolumeBuild(t, positional)
}

func dockerVolumeBuild(t *siteTransfer, positional []string) (result error) {
	var runner *kitImage
	for i, entry := range t.Entry.Kit.Images {
		if entry.Role == "tool-runner" {
			runner = &t.Entry.Kit.Images[i]
			break
		}
	}
	if runner == nil {
		return errors.New("devkit缺少tool-runner（含Docker CLI/Compose/sh/tar的Linux镜像）；请维护者补入tools.runner，或 --backend wsl 使用发行版内Engine")
	}
	blob := t.Entry.Files[runner.File]
	if err := command(nil, os.Stderr, nil, "docker", "load", "-i", filepath.Join(t.KitRoot, "blobs", blob)); err != nil {
		return err
	}
	if err := prepareBuildImage(runner.Tag, true); err != nil {
		return err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	name := "auriga-windows-" + hex.EncodeToString(random[:])
	created, err := capture("docker", "volume", "create", "--label", "auriga.offline.windows="+name, name)
	if err != nil {
		return err
	}
	if created != name {
		return errors.New("临时卷名称不符")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
		if err := exec.CommandContext(ctx, "docker", "volume", "rm", name).Run(); err != nil {
			result = errors.Join(result, fmt.Errorf("临时卷 %s 清理失败，确认无本批容器运行后清理此卷: %w", name, err))
		}
	}()
	mount, err := capture("docker", "volume", "inspect", "--format", "{{.Mountpoint}}", name)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(mount, "/") || !strings.HasSuffix(mount, "/"+name+"/_data") || strings.ContainsAny(mount, ",\r\n") {
		return errors.New("不支持的Docker卷路径")
	}
	args := []string{"run", "--interactive", "--rm", "--name", name, "--pull", "never", "--network", "none", "--platform", "linux/amd64", "--memory", "512m", "--cpus", "1", "--mount", "type=volume,src=" + name + ",dst=" + mount, "--mount", "type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock", "--workdir", mount, "--entrypoint", "sh", runner.Tag, "-c", "site_root=$PWD\n" + siteBuildScript, "auriga-build"}
	return transferCommand(t, "docker", append(args, positional...))
}
