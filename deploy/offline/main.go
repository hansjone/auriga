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
		fmt.Println("Auriga 离线部署\n  ./auriga devkit --release 发行索引.json --out 开发套件.tar\n  ./auriga devkit --verify 套件目录 [--verify-key 公钥.pem]\n  ./auriga bundle --release 发行索引.json --out 离线包.tar [--build]\n  ./auriga bundle --build --offline [--version 0.2.0-site.3] [--out 离线包.tar]\n  sudo ./auriga install --address 192.0.2.10 [--data /srv/auriga]\n  sudo ./auriga doctor|backup|status|stop|logs [--data /srv/auriga]\n  sudo ./auriga upgrade 新离线包.tar [--data /srv/auriga]")
		return nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("请在 Linux amd64 或 WSL 中运行")
	}
	if args[0] == "bundle" {
		return bundle(args[1:])
	}
	if args[0] == "devkit" {
		return devkit(args[1:])
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
