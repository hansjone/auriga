package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var regexpVersion = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,95}$`)
var regexpName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
var domainName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}$`)

type installOptions struct {
	Address, Domain, Proxy, Data, PublicKey, Package string
	DesktopPublicKey, DesktopPublisherTenant         string
	DesktopConfirmFingerprint                        string
	Fingerprint                                      string
	AllowUnsigned                                    bool
	Runtime                                          installRuntimeOptions
}
type state struct {
	Data                        string            `json:"data"`
	Package                     string            `json:"package"`
	Access                      string            `json:"access"`
	Images                      map[string]string `json:"images"`
	PublicKey                   string            `json:"publicKey,omitempty"`
	TrustedKeys                 []string          `json:"trustedKeys,omitempty"`
	AllowUnsigned               bool              `json:"allowUnsigned,omitempty"`
	ResourceReady               bool              `json:"resourceReady"`
	DesktopPublicKeyFingerprint string            `json:"desktopPublicKeyFingerprint,omitempty"`
	DesktopPublisherTenant      string            `json:"desktopPublisherTenant,omitempty"`
	Runtime                     *installRuntime   `json:"runtime,omitempty"`
}

func parseInstall(args []string) (installOptions, error) {
	var o installOptions
	f := flags("install")
	f.StringVar(&o.Address, "address", "", "")
	f.StringVar(&o.Domain, "domain", "", "")
	f.StringVar(&o.Proxy, "proxy", "", "")
	f.StringVar(&o.Data, "data", "/srv/auriga", "")
	f.StringVar(&o.PublicKey, "verify-key", "", "")
	f.StringVar(&o.Fingerprint, "trust-fingerprint", "", "")
	f.BoolVar(&o.AllowUnsigned, "allow-unsigned", false, "")
	f.StringVar(&o.Package, "package", "", "")
	f.StringVar(&o.DesktopPublicKey, "desktop-public-key", "", "")
	f.StringVar(&o.DesktopPublisherTenant, "desktop-publisher-tenant", "", "")
	f.StringVar(&o.DesktopConfirmFingerprint, "desktop-confirm-fingerprint", "", "")
	f.StringVar(&o.Runtime.Bundle, "runtime-bundle", "", "approved signed runtime bundle directory")
	f.StringVar(&o.Runtime.Root, "runtime-root", "", "independently approved external runtime root protobuf")
	f.StringVar(&o.Runtime.Image, "runtime-image", "", "approved exact runtime repo@sha256 locator")
	if err := uniqueRuntimeFlags(args); err != nil {
		return o, err
	}
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("多余安装参数")
	}
	if err := o.Runtime.validate(); err != nil {
		return o, err
	}
	runtimeFlags := 0
	f.Visit(func(item *flag.Flag) {
		if strings.HasPrefix(item.Name, "runtime-") {
			runtimeFlags++
		}
	})
	if runtimeFlags > 0 && !o.Runtime.enabled() {
		return o, errors.New("runtime 首装参数不得显式留空")
	}
	count := 0
	if (o.DesktopPublicKey == "") != (o.DesktopPublisherTenant == "") || (o.DesktopPublicKey == "") != (o.DesktopConfirmFingerprint == "") {
		return o, errors.New("--desktop-public-key、--desktop-publisher-tenant 和 --desktop-confirm-fingerprint 必须一并提供")
	}
	if o.DesktopConfirmFingerprint != "" && !hex64.MatchString(o.DesktopConfirmFingerprint) {
		return o, errors.New("--desktop-confirm-fingerprint 需要64位小写SHA256（SPKI DER）")
	}
	if o.Address != "" {
		count++
		ip := net.ParseIP(o.Address)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return o, errors.New("--address 需要可达的 IPv4")
		}
	}
	if o.Domain != "" {
		count++
		if !domainName.MatchString(o.Domain) || strings.Contains(o.Domain, "..") {
			return o, errors.New("--domain 需要规范域名，例如 example.com")
		}
	}
	if o.Proxy != "" {
		count++
	}
	if count != 1 {
		return o, errors.New("选择一个 --address IPv4、--domain 域名或 --proxy 已批准的access.json")
	}
	data, err := dataPath(o.Data)
	o.Data = data
	return o, err
}

func readState(data string) (state, error) {
	var s state
	if err := noSymlinks(data + ".oneclick"); err != nil {
		return s, err
	}
	err := readJSON(filepath.Join(data+".oneclick", "state.json"), &s)
	if err != nil {
		return s, fmt.Errorf("未找到一键安装状态，请先 install %w", err)
	}
	if s.Data != data {
		return s, errors.New("安装状态的数据目录不匹配")
	}
	return s, nil
}

func saveState(s state) error {
	path := filepath.Join(s.Data+".oneclick", "state.json")
	if err := jsonFile(path+".new", s, 0600); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

func nas(s state, operation string, args ...string) error {
	env := []string{"AURIGA_DATA_ROOT=" + s.Data, "NAS_ROOT=" + filepath.Join(s.Package, "deploy-nas"), "STACK_ENV=" + filepath.Join(s.Data, "stack.env")}
	all := append([]string{filepath.Join(s.Package, "deploy-nas/scripts", operation+".sh")}, args...)
	return command(os.Stdin, os.Stdout, env, "sh", all...)
}

func compose(s state, output *bytes.Buffer, args ...string) error {
	env := []string{"AURIGA_DATA_ROOT=" + s.Data, "NAS_ROOT=" + filepath.Join(s.Package, "deploy-nas"), "STACK_ENV=" + filepath.Join(s.Data, "stack.env")}
	all := append([]string{filepath.Join(s.Package, "deploy-nas/scripts/oneclick-compose.sh")}, args...)
	if output != nil {
		return command(nil, output, env, "sh", all...)
	}
	return command(os.Stdin, os.Stdout, env, "sh", all...)
}

func install(o installOptions) error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	if o.Package != "" {
		stage, err := os.MkdirTemp("", "auriga-install-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		root = filepath.Join(stage, "package")
		if err := unpack(o.Package, root); err != nil {
			return err
		}
	}
	if err := verifySums(root); err != nil {
		return err
	}
	// Re-running install cannot replace an already pinned trust set.
	var key string
	var unsigned bool
	if existing, err := readState(o.Data); err == nil {
		unsigned, err = verifyStatePackage(existing, root, archiveIndex(o.Package), o.AllowUnsigned)
		if err != nil {
			return err
		}
	} else {
		var err error
		key, unsigned, err = verifyRootTrust(root, trustOptions{Key: o.PublicKey, Fingerprint: o.Fingerprint, AllowUnsigned: o.AllowUnsigned})
		if err != nil {
			return err
		}
		if index := archiveIndex(o.Package); index != "" && !unsigned {
			if err := verifyFileSignature(index, key); err != nil {
				return err
			}
		}
	}
	prepared, err := prepareInstallRuntime(root, o, unsigned)
	if err != nil {
		return err
	}
	if prepared != nil {
		defer func() { _ = os.RemoveAll(prepared.directory) }()
	}
	if err := dockerReady(); err != nil {
		return err
	}
	return withLock(o.Data, func() error {
		if _, err := os.Lstat(o.Data + ".upgrade.json"); err == nil {
			return errors.New("存在中断的升级，请先重新运行 upgrade 恢复")
		}
		if s, err := readState(o.Data); err == nil {
			if err := verifySums(s.Package); err != nil {
				return err
			}
			storedUnsigned, err := verifyStatePackage(s, s.Package, "", o.AllowUnsigned)
			if err != nil {
				return err
			}
			if s.Runtime != nil && (unsigned || storedUnsigned) {
				return errors.New("runtime 首装或恢复需要已验签的外层安装包，不能使用 --allow-unsigned 缺签名例外")
			}
			if err := resumeInstallRuntime(s, o.Runtime, prepared); err != nil {
				return err
			}
			if unsigned || storedUnsigned {
				s.AllowUnsigned = true
				if err := saveState(s); err != nil {
					return err
				}
			}
			fmt.Println("已存在安装，复核配置并继续启动。更换发行请用 upgrade。")
			if _, err := os.Stat(filepath.Join(s.Data, ".auriga-initialized")); err != nil {
				if err := nas(s, "init"); err != nil {
					return err
				}
			}
			if !s.ResourceReady {
				if err := provisionResource(s); err != nil {
					return fmt.Errorf("资源密钥准备未完成，保留现有材料并处理错误 %w", err)
				}
				s.ResourceReady = true
				if err := saveState(s); err != nil {
					return err
				}
			}
			if err := configureDesktop(&s, o.DesktopPublicKey, o.DesktopPublisherTenant, o.DesktopConfirmFingerprint); err != nil {
				return err
			}
			if err := saveState(s); err != nil {
				return err
			}
			if err := nas(s, "start", "--network-offline"); err != nil {
				return err
			}
			return finishInstall(s)
		}
		meta := o.Data + ".oneclick"
		if _, err := os.Lstat(meta); !os.IsNotExist(err) {
			return errors.New("一键安装目录已存在但状态不完整，请保留目录并运行诊断，不覆盖现场材料")
		}
		if entries, err := os.ReadDir(o.Data); err == nil && len(entries) > 0 {
			return errors.New("数据目录非空，拒绝接管已有安装")
		}
		if err := os.Mkdir(meta, 0755); err != nil {
			return err
		}
		s := state{Data: o.Data, Package: filepath.Join(meta, "release"), Access: filepath.Join(meta, "access.json")}
		if err := copyTree(root, s.Package); err != nil {
			return err
		}
		if err := verifySums(s.Package); err != nil {
			return err
		}
		s.AllowUnsigned = unsigned
		if key != "" {
			if err := pinInitialTrust(&s, key); err != nil {
				return err
			}
		}
		if !unsigned {
			if _, err := verifyStatePackage(s, s.Package, "", false); err != nil {
				return err
			}
		}
		if err := generateSiteKey(filepath.Join(meta, "site-build-key")); err != nil {
			return err
		}
		if key != "" {
			// Local server builds use the same explicit registration mechanism.
			pub := filepath.Join(meta, "site-build-key/site-public.pem")
			fp, err := publicFingerprint(pub)
			if err != nil {
				return err
			}
			if err := registerSiteKey(&s, pub, fp); err != nil {
				return err
			}
		}
		if err := makeAccess(o, s); err != nil {
			return err
		}
		images, err := loadImages(s.Package)
		if err != nil {
			return err
		}
		s.Images = images
		if err := createLAN(); err != nil {
			return err
		}
		if err := os.MkdirAll(o.Data, 0755); err != nil {
			return err
		}
		if err := pinInstallRuntime(&s, prepared); err != nil {
			return err
		}
		if err := makeEnv(s); err != nil {
			return err
		}
		if err := saveState(s); err != nil {
			return err
		}
		if err := nas(s, "init"); err != nil {
			return err
		}
		if err := provisionResource(s); err != nil {
			return err
		}
		s.ResourceReady = true
		if err := saveState(s); err != nil {
			return err
		}
		if err := configureDesktop(&s, o.DesktopPublicKey, o.DesktopPublisherTenant, o.DesktopConfirmFingerprint); err != nil {
			return err
		}
		if err := saveState(s); err != nil {
			return err
		}
		if err := nas(s, "preflight"); err != nil {
			return err
		}
		if err := nas(s, "start", "--network-offline"); err != nil {
			return err
		}
		return finishInstall(s)
	})
}

func finishInstall(s state) error {
	if err := waitHealthy(s); err != nil {
		return err
	}
	envFile := filepath.Join(s.Data, "stack.env")
	raw, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}
	updated := bytes.ReplaceAll(raw, []byte("AURIGA_PLATFORM_SEED=true"), []byte("AURIGA_PLATFORM_SEED=false"))
	if !bytes.Equal(raw, updated) {
		if err := os.WriteFile(envFile, updated, 0600); err != nil {
			return err
		}
		if err := nas(s, "start", "--network-offline"); err != nil {
			return err
		}
		if err := waitHealthy(s); err != nil {
			return err
		}
	}
	var access struct {
		AdminOrigin struct {
			Scheme, Host string
			Port         int
		}
	}
	if err := readJSONLoose(s.Access, &access); err != nil {
		return err
	}
	fmt.Printf("核心服务已启动。管理台 %s://%s:%d/\n应急材料仅在私密 TTY 显示。\n", access.AdminOrigin.Scheme, access.AdminOrigin.Host, access.AdminOrigin.Port)
	fmt.Printf("资源公钥根 %s\n降级私钥请导出至受控离线介质 %s\n", filepath.Join(s.Data, "resource-keys/resource-roots.json"), filepath.Join(s.Data, "resource-keys/offline/downgrade.pk8"))
	fmt.Printf("服务器站点构建私钥（仅root）%s；独立Linux构建机另行 keygen site，公钥通过 trust add-site-key 登记。\n", filepath.Join(s.Data+".oneclick", "site-build-key/site-private.pem"))
	return nil
}

func copyTree(source, target string) error {
	return copyTreeContext(context.Background(), source, target)
}

func copyTreeContext(ctx context.Context, source, target string) error {
	names, err := regularFilesContext(ctx, source)
	if err != nil {
		return err
	}
	if err := os.Mkdir(target, 0755); err != nil {
		return err
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(source, name))
		if err != nil {
			return err
		}
		if err := copyFileContext(ctx, filepath.Join(source, name), filepath.Join(target, name), info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func loadImages(root string) (map[string]string, error) {
	var r release
	if err := readJSON(filepath.Join(root, "offline-release.json"), &r); err != nil {
		return nil, err
	}
	// Keep the original NAS checksums in a regular-file view. Image layers are
	// hard-linked to avoid copying multi-gigabyte archives during offline load.
	view, err := nasView(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(view) }()
	var out bytes.Buffer
	if err := command(nil, &out, []string{"AURIGA_RELEASE_DIR=" + view}, "sh", filepath.Join(root, "deploy-nas/scripts/load-images.sh"), view); err != nil {
		return nil, err
	}
	images := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.HasPrefix(key, "AURIGA_IMAGE_") && immutable.MatchString(value) {
			images[strings.TrimPrefix(key, "AURIGA_IMAGE_")] = value
		}
	}
	if len(images) != 5 {
		return nil, errors.New("NAS 镜像解析结果不完整")
	}
	for name, entry := range r.Images {
		if !regexpName.MatchString(name) || entry.Archive != "extra-images/"+name+".tar" || entry.Tag != "auriga/offline-"+name+":"+r.Version {
			return nil, errors.New("附加镜像元数据无效")
		}
		archive := filepath.Join(root, entry.Archive)
		if err := command(nil, os.Stdout, nil, "docker", "load", "-i", archive); err != nil {
			return nil, err
		}
		id, err := capture(filepath.Join(root, "deploy-nas/bin/auriga-nas-bootstrap"), "image-extra-resolve", archive, entry.Tag)
		if err != nil {
			return nil, err
		}
		if !immutable.MatchString(id) {
			return nil, errors.New("镜像解析未返回精确本机 ID")
		}
		images[strings.ToUpper(strings.ReplaceAll(name, "-", "_"))] = id
	}
	for _, name := range []string{"POSTGRES", "NATS", "REGISTRY"} {
		if images[name] == "" {
			return nil, fmt.Errorf("缺少 %s", name)
		}
	}
	return images, nil
}

func linkTree(source, target string) error {
	names, err := regularFiles(source)
	if err != nil {
		return err
	}
	for _, name := range names {
		path := filepath.Join(target, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.Link(filepath.Join(source, name), path); err != nil {
			return err
		}
	}
	return nil
}

func createLAN() error {
	if _, err := capture("docker", "network", "inspect", "auriga-offline-lan"); err == nil {
		return nil
	}
	return command(nil, os.Stdout, nil, "docker", "network", "create", "--driver", "bridge", "--subnet", "192.0.2.0/26", "--ip-range", "192.0.2.32/27", "--label", "auriga.offline=true", "auriga-offline-lan")
}

func makeAccess(o installOptions, s state) error {
	if o.Proxy != "" {
		return copyFile(o.Proxy, s.Access, 0644)
	}
	name := "access-ip.example.json"
	if o.Domain != "" {
		name = "access-domain.example.json"
	}
	raw, err := os.ReadFile(filepath.Join(s.Package, "deploy-nas", name))
	if err != nil {
		return err
	}
	if o.Domain != "" {
		raw = bytes.ReplaceAll(raw, []byte("example.com"), []byte(o.Domain))
	} else {
		raw = bytes.ReplaceAll(raw, []byte("192.0.2.10"), []byte(o.Address))
	}
	return os.WriteFile(s.Access, raw, 0644)
}

func makeEnv(s state) error {
	raw, err := os.ReadFile(filepath.Join(s.Package, "deploy-nas/env.example"))
	if err != nil {
		return err
	}
	values := map[string]string{
		"AURIGA_DATA_ROOT": s.Data, "AURIGA_ACCESS_FILE": s.Access,
		"AURIGA_INSTALL_MODE": "container", "AURIGA_INSTALLER_IMAGE": s.Images["POSTGRES"],
		"AURIGA_LAN_NETWORK": "auriga-offline-lan", "AURIGA_LAN_SUBNET": "192.0.2.0/26", "AURIGA_EDGE_LAN_IP": "192.0.2.10",
		"AURIGA_INTERNAL_SUBNET": "192.0.2.128/26", "AURIGA_INTERNAL_EDGE_IP": "192.0.2.138", "AURIGA_INTERNAL_DYNAMIC_RANGE": "192.0.2.160/27",
		"AURIGA_ONECLICK_PUBLISH": "true",
	}
	for key, value := range installRuntimeEnv(s) {
		values[key] = value
	}
	for key, value := range s.Images {
		values["AURIGA_IMAGE_"+key] = value
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			if value, ok := values[key]; ok {
				lines[i] = key + "=" + value
				delete(values, key)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, key+"="+values[key])
	}
	return os.WriteFile(filepath.Join(s.Data, "stack.env"), []byte(strings.Join(lines, "\n")), 0600)
}

func provisionResource(s state) error {
	root := filepath.Join(s.Data, "resource-keys")
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		stage, err := os.MkdirTemp(s.Data, ".auriga-resource-keys-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		if err := writeResourceKeys(stage); err != nil {
			return err
		}
		if err := os.Chmod(stage, 0755); err != nil {
			return err
		}
		platform := filepath.Join(stage, "platform")
		for _, path := range []string{platform, filepath.Join(platform, "release.pk8"), filepath.Join(platform, "revocation.pk8")} {
			if err := os.Chown(path, 10001, 10001); err != nil {
				return err
			}
		}
		if err := os.Rename(stage, root); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := validateResourceMaterial(root); err != nil {
		return err
	}
	for name, want := range map[string]string{"platform": "10001:10001:700", "platform/release.pk8": "10001:10001:600", "platform/revocation.pk8": "10001:10001:600", "offline": "0:0:700", "offline/downgrade.pk8": "0:0:600"} {
		got, err := capture("stat", "-c", "%u:%g:%a", filepath.Join(root, name))
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("残缺或不安全的资源密钥权限 %s，保留材料并恢复属主/权限后重试", name)
		}
	}
	objects := filepath.Join(s.Data, "resource-objects")
	if _, err := os.Lstat(objects); os.IsNotExist(err) {
		stage, err := os.MkdirTemp(s.Data, ".auriga-resource-objects-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(stage) }()
		if err := os.Chown(stage, 10001, 10001); err != nil {
			return err
		}
		if err := os.Rename(stage, objects); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := noSymlinks(objects); err != nil {
		return err
	}
	permission, err := capture("stat", "-c", "%u:%g:%a", objects)
	if err != nil {
		return err
	}
	if permission != "10001:10001:700" {
		return fmt.Errorf("残缺或不安全的对象目录 %s；保留并从备份恢复正确属主/0700权限后重试", objects)
	}
	var roots map[string]map[string]string
	if err := readJSON(filepath.Join(root, "resource-roots.json"), &roots); err != nil {
		return err
	}
	env := filepath.Join(s.Data, "stack.env")
	raw, err := os.ReadFile(env)
	if err != nil {
		return err
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "AURIGA_RESOURCE_RELEASE_KID=") && !strings.HasPrefix(line, "AURIGA_RESOURCE_REVOCATION_KID=") {
			lines = append(lines, line)
		}
	}
	lines = append(lines, "AURIGA_RESOURCE_RELEASE_KID="+roots["release"]["kid"], "AURIGA_RESOURCE_REVOCATION_KID="+roots["revocation"]["kid"], "")
	return os.WriteFile(env, []byte(strings.Join(lines, "\n")), 0600)
}
