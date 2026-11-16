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
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		if runtime.GOOS == "windows" {
			fmt.Println("Windows PowerShell 5.1离线开发（无需Go/Node/Git；客户端扩展自带Node）\n  .\\auriga.ps1 verify <文件或目录>\n  .\\auriga.ps1 unpack <tar或首卷> --out <新目录>\n  .\\auriga.ps1 devkit verify <套件目录> [--backend wsl --wsl-distro Ubuntu]\n  .\\auriga.ps1 devkit merge <增量包> --kit <套件目录>\n  .\\auriga.ps1 doctor --build [--backend wsl]\n  .\\auriga.ps1 changes export --out <补丁.tar>\n  .\\auriga.ps1 changes apply <补丁.tar>\n  .\\auriga.ps1 bundle --build --offline [--backend docker|wsl] [--memory-mib 4096 --cpus 2] [--out <安装包.tar>]\n客户端扩展合入后使用 .\\auriga.ps1 client ...；安装包交Linux服务器upgrade。前置离线安装见MAINTAIN.md。")
			return nil
		}
		fmt.Println("浏览器下载Forgejo Release附件，现场无需Git/网络/语言工具链。\n校验附件：./auriga verify <文件或目录> [--verify-key <独立批准公钥>]\n自动解分卷：./auriga unpack <tar|part001|parts.json> --out <全新目录>\n校验源码依赖：./auriga devkit verify <套件目录> [--version <上游版本>]\n合并增量：./auriga devkit merge <增量tar或首卷> --kit <套件目录>\n现场补丁：./auriga changes export --out <补丁.tar> / changes apply <补丁.tar>\n维护者发行：./auriga release --tag <版本> --release <批准索引> --sign-key <私钥> --previous-devkit <上版目录>（首次 --new-baseline）")
		fmt.Println("Auriga 离线部署\n  ./auriga bundle --build --offline [--version 0.2.0-site.3] [--out 离线包.tar]\n  sudo ./auriga install --address 192.0.2.10 [--data /srv/auriga]\n  sudo ./auriga doctor|backup|status|stop|logs [--data /srv/auriga]\n  sudo ./auriga upgrade 新离线包.tar [--data /srv/auriga]\n维护者自行打包及发行见 MAINTAIN.md。")
		fmt.Println("构建资源诊断：./auriga doctor --build；默认工具容器4096MiB/2CPU并保留1GiB。Linux bundle可加--install --data /srv/auriga复用先备份再upgrade。")
		return nil
	}
	if runtime.GOARCH != "amd64" {
		return errors.New("当前离线工具支持amd64")
	}
	if runtime.GOOS == "windows" {
		return windowsDispatch(args)
	}
	if runtime.GOOS != "linux" {
		return errors.New("请在 Linux amd64 或 WSL 中运行")
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
	if args[0] == "devkit" {
		if err := devkit(args[1:]); err != nil {
			return err
		}
		if len(args) > 1 && args[1] == "verify" {
			return buildEnvironment(buildBudget{4096, 2})
		}
		return nil
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
		data, err := dataArgs(args[2:])
		if err != nil {
			return err
		}
		return withLock(data, func() error { return upgrade(data, args[1]) })
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = input, output, os.Stderr
	c.Env = append(os.Environ(), env...)
	c.WaitDelay = 3 * time.Second
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s 执行失败 %w", name, err)
	}
	return nil
}

func capture(name string, args ...string) (string, error) {
	var b bytes.Buffer
	err := command(nil, &b, nil, name, args...)
	return strings.TrimSpace(b.String()), err
}

func dockerReady() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("缺少 Docker Engine 和 Compose 插件，请用受信离线安装介质预装 Docker，随后重试")
	}
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		var err error
		endpoint, err = capture("docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
		if err != nil {
			return err
		}
	}
	if err := localDockerEndpoint(endpoint); err != nil {
		return err
	}
	if _, err := capture("docker", "info", "--format", "{{.ServerVersion}}"); err != nil {
		return errors.New("本机 Docker Engine 不可用，检查 docker 服务和 socket 权限")
	}
	v, err := capture("docker", "compose", "version", "--short")
	if err != nil {
		return errors.New("缺少 Docker Compose 插件，需要 2.29+ ")
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
