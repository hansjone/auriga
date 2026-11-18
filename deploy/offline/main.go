package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var hex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)
var immutable = regexp.MustCompile(`^([^\s]+@)?sha256:[a-f0-9]{64}$`)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "失败", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("离线部署和构建仅支持 Linux amd64；请把附件和源码拷到 Linux 服务器或独立 Linux 构建机运行")
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Println("verify/unpack/install 必须 --verify-key <批准公钥> 或 --trust-fingerprint <独立核对SHA256(SPKI)>；缺签名默认拒绝。站点构建：keygen site --out <私密目录>；服务器 trust add-site-key <pem> --confirm-fingerprint <sha>；bundle --build --offline 必须 --sign-key <已登记站点私钥>。")
		fmt.Println("浏览器下载Forgejo Release附件，现场无需Git/网络/语言工具链。\n校验附件：./auriga verify <文件或目录> [--verify-key <独立批准公钥>]\n自动解分卷：./auriga unpack <tar|part001|parts.json> --out <全新目录>\n校验源码依赖：./auriga devkit verify <套件目录> [--version <上游版本>]\n合并增量：./auriga devkit merge <增量tar或首卷> --kit <套件目录>\n现场补丁：./auriga changes export --out <补丁.tar> / changes apply <补丁.tar>\n维护者发行：./auriga release --help；参数、Windows 与 DSH 输入及 Forgejo 发布顺序见 MAINTAIN.md。")
		fmt.Println("Auriga 离线部署\n  ./auriga bundle --build --offline [--version 0.2.0-site.3] [--out 离线包.tar]\n  sudo ./auriga install --address 192.0.2.10 [--data /srv/auriga]\n  sudo ./auriga doctor|backup|status|stop|logs [--data /srv/auriga]\n  sudo ./auriga upgrade 新离线包.tar [--data /srv/auriga]\n维护者自行打包及发行见 MAINTAIN.md。")
		fmt.Println("构建资源诊断：./auriga doctor --build；默认工具容器4096MiB/2CPU并保留1GiB。Linux bundle可加--install --data /srv/auriga复用先备份再upgrade。")
		return nil
	}
	if args[0] == "keygen" {
		return keygenCommand(args[1:])
	}
	if args[0] == "doctor" && len(args) > 1 && args[1] == "--build" {
		return buildDoctor(args[2:])
	}
	if args[0] == "bundle" {
		return bundle(args[1:])
	}
	if args[0] == "internal-offline-build" {
		return isolatedGateBuild(args[1:])
	}
	if args[0] == "internal-offline-cleanup" {
		return gateNamespaceCleanup(args[1:])
	}
	if args[0] == "devkit" {
		return devkit(args[1:])
	}
	if args[0] == "unpack" {
		return unpackCommand(args[1:])
	}
	if args[0] == "verify" {
		return verifyCommand(args[1:])
	}
	if args[0] == "release" {
		return releaseCommand(args[1:])
	}
	if args[0] == "changes" {
		return changesCommand(args[1:])
	}
	if os.Geteuid() != 0 {
		return errors.New("现场操作需要 sudo/root，以保护服务密钥及备份权限")
	}
	if args[0] == "trust" {
		return trustCommand(args[1:])
	}
	if args[0] == "install" {
		o, err := parseInstall(args[1:])
		if err != nil {
			return err
		}
		return install(o)
	}
	if args[0] == "upgrade" {
		if len(args) < 2 || strings.HasPrefix(args[1], "--") {
			return errors.New("upgrade 需要新离线包路径")
		}
		f := flags("upgrade")
		dir := f.String("data", "/srv/auriga", "")
		allow := f.Bool("allow-unsigned", false, "")
		if err := f.Parse(args[2:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("多余upgrade参数；升级只信任state固定的公钥")
		}
		data, err := dataPath(*dir)
		if err != nil {
			return err
		}
		return withLock(data, func() error { return upgradeAllowed(data, args[1], *allow) })
	}
	data, err := dataArgs(args[1:])
	if err != nil {
		return err
	}
	s, err := readState(data)
	if err != nil {
		return err
	}
	switch args[0] {
	case "doctor":
		return doctor(s)
	case "backup":
		return withLock(data, func() error { _, err := backup(s); return err })
	case "status":
		return nas(s, args[0])
	case "logs":
		return compose(s, nil, "logs", "--tail", "100", "--timestamps")
	case "stop":
		return withLock(data, func() error { return nas(s, "stop") })
	default:
		return errors.New("未知命令，运行 ./auriga --help")
	}
}

func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func dataArgs(args []string) (string, error) {
	f := flags("data")
	data := f.String("data", "/srv/auriga", "")
	if err := f.Parse(args); err != nil {
		return "", err
	}
	if f.NArg() != 0 {
		return "", errors.New("多余参数")
	}
	return dataPath(*data)
}

func dataPath(path string) (string, error) {
	if !pathpkg.IsAbs(path) || pathpkg.Clean(path) != path || strings.ContainsAny(path, " ,:$\n\r\t'\"\\") {
		return "", errors.New("数据目录必须是规范绝对物理路径")
	}
	if !strings.HasPrefix(pathpkg.Base(path), "auriga-") || len(strings.Split(strings.Trim(path, "/"), "/")) < 3 {
		path = pathpkg.Join(path, "auriga-data")
	}
	if len(strings.Split(strings.Trim(path, "/"), "/")) < 3 {
		return "", errors.New("数据目录至少需要三层路径")
	}
	return path, noSymlinks(path)
}

func noSymlinks(path string) error {
	for p := path; p != filepath.Dir(p); p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝符号链接路径 %s", p)
		}
	}
	return nil
}

func command(input io.Reader, output io.Writer, env []string, name string, args ...string) error {
	return commandContext(context.Background(), input, output, env, name, args...)
}

func commandContext(parent context.Context, input io.Reader, output io.Writer, env []string, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, 60*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = input, output, os.Stderr
	c.Env = append(os.Environ(), env...)
	c.WaitDelay = 3 * time.Second
	if parent.Done() != nil {
		setGateProcessGroup(c)
		c.WaitDelay = 3 * time.Second
		defer func() {
			if ctx.Err() != nil && c.Process != nil {
				stopGateProcessGroup(c, true)
			}
		}()
	}
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s 执行失败 %w", name, errors.Join(err, ctx.Err()))
	}
	return ctx.Err()
}

func capture(name string, args ...string) (string, error) {
	return captureContext(context.Background(), name, args...)
}

func captureContext(ctx context.Context, name string, args ...string) (string, error) {
	var b bytes.Buffer
	err := commandContext(ctx, nil, &b, nil, name, args...)
	return strings.TrimSpace(b.String()), err
}

func dockerReady() error {
	return dockerReadyContext(context.Background())
}

func dockerReadyContext(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("缺少 Docker Engine 和 Compose 插件，请用受信离线安装介质预装 Docker，随后重试")
	}
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		var err error
		endpoint, err = captureContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
		if err != nil {
			return err
		}
	}
	if err := localDockerEndpoint(endpoint); err != nil {
		return err
	}
	if _, err := captureContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}"); err != nil {
		return errors.Join(errors.New("本机 Docker Engine 不可用，检查 docker 服务和 socket 权限"), ctx.Err())
	}
	v, err := captureContext(ctx, "docker", "compose", "version", "--short")
	if err != nil {
		return errors.Join(errors.New("缺少 Docker Compose 插件，需要 2.29+ "), ctx.Err())
	}
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimPrefix(v, "v"), "%d.%d", &major, &minor); err != nil || major < 2 || major == 2 && minor < 29 {
		return errors.New("需要 Compose 2.29+，请预装受信离线版本")
	}
	return nil
}

func packageRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(exe)), nil
}

func withLock(data string, work func() error) error {
	if err := os.MkdirAll(filepath.Dir(data), 0755); err != nil {
		return err
	}
	if err := trustedParent(filepath.Dir(data)); err != nil {
		return err
	}
	lock := data + ".oneclick-lock"
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("已有操作或未清理的锁 %s，确认原进程已停止后处理 %w", lock, err)
	}
	defer func() { _ = os.Remove(lock) }()
	return work()
}

func trustedParent(parent string) error {
	if err := noSymlinks(parent); err != nil {
		return err
	}
	for p := parent; ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		owner, err := capture("stat", "-c", "%u", p)
		if err != nil {
			return err
		}
		if owner != "0" || !info.IsDir() || info.Mode().Perm()&0022 != 0 && (p == parent || info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("数据目录祖先必须由root管理且不可被组或其他用户改写 %s", p)
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
