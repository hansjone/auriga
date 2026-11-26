package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type image struct {
	Ref     string `json:"ref"`
	Tag     string `json:"tag"`
	Archive string `json:"archive"`
}
type release struct {
	Schema            int               `json:"schemaVersion"`
	Version           string            `json:"version"`
	NAS               artifact          `json:"nas"`
	Images            map[string]image  `json:"images"`
	Desktop           *artifact         `json:"desktop,omitempty"`
	Seeds             *artifact         `json:"seeds,omitempty"`
	Build             *buildSpec        `json:"build,omitempty"`
	Source            *provenance       `json:"source,omitempty"`
	SchemaCompatFloor string            `json:"schemaCompatFloor,omitempty"`
	FormatMax         map[string]string `json:"formatMax,omitempty"`
}

type buildSpec struct {
	Inputs              artifact                 `json:"inputs"`
	Recipes             artifact                 `json:"recipes"`
	Bases               map[string]string        `json:"bases"`
	Tools               map[string]string        `json:"tools"`
	RuntimeTooling      *artifact                `json:"runtimeTooling,omitempty"`
	RuntimeDependencies *runtimeDependencyInputs `json:"runtimeDependencies,omitempty"`
}

type runtimeDependencyInputs struct {
	Manifest artifact `json:"manifest"`
	Corepack artifact `json:"corepack"`
	WebCache artifact `json:"webCache"`
	NpmCache artifact `json:"npmCache"`
}

func (r *runtimeDependencyInputs) artifacts() map[string]*artifact {
	return map[string]*artifact{"runtime-dependencies.json": &r.Manifest, "corepack.tgz": &r.Corepack, "web-cache.tar.gz": &r.WebCache, "npm-cache.tar.gz": &r.NpmCache}
}

func readJSON(path string, output any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return errors.New("JSON 尾部有多余数据")
	}
	return nil
}

type artifactLocationKind int

const (
	artifactLocationLocal artifactLocationKind = iota
	artifactLocationHTTPS
)

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// classifyArtifactLocation distinguishes approved local filesystem paths from
// remote HTTPS download URLs according to host OS semantics without weakening
// HTTPS-only download enforcement or expanding local file access to UNC/device
// paths or Windows drive-relative paths.
func classifyArtifactLocation(raw string, isWindows bool) (artifactLocationKind, *url.URL, error) {
	if raw == "" {
		return artifactLocationLocal, nil, errors.New("发行路径不能为空")
	}
	if strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//") {
		return artifactLocationLocal, nil, errors.New("发行本地路径不支持 UNC 或设备路径")
	}
	if isWindows && len(raw) >= 2 && isASCIIAlpha(raw[0]) && raw[1] == ':' {
		if len(raw) >= 3 && (raw[2] == '\\' || raw[2] == '/') {
			return artifactLocationLocal, nil, nil
		}
		return artifactLocationLocal, nil, errors.New("发行本地路径不支持盘符相对路径")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return artifactLocationLocal, nil, err
	}
	if u.Scheme == "" {
		return artifactLocationLocal, nil, nil
	}
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return artifactLocationHTTPS, nil, errors.New("发行下载仅接受 HTTPS，无 URL 内凭据")
	}
	return artifactLocationHTTPS, u, nil
}

func fetchRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
		return errors.New("拒绝跨主机或非 HTTPS 重定向")
	}
	return nil
}

func fetchContext(ctx context.Context, a artifact, target string) error {
	if !hex64.MatchString(a.SHA256) {
		return errors.New("发行索引需要独立批准的 SHA256")
	}
	kind, u, err := classifyArtifactLocation(a.URL, runtime.GOOS == "windows")
	if err != nil {
		return err
	}
	if kind == artifactLocationLocal {
		if err := copyFileContext(ctx, a.URL, target, 0644); err != nil {
			return err
		}
	} else {
		client := &http.Client{Timeout: 60 * time.Minute, CheckRedirect: fetchRedirectPolicy}
		return fetchHTTPSContext(ctx, client, a, u, target)
	}
	return verifyFetchedDigest(ctx, target, a.SHA256)
}

func fetchHTTPSContext(ctx context.Context, client *http.Client, a artifact, u *url.URL, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	if token := os.Getenv("AURIGA_BUNDLE_TOKEN"); token != "" {
		if os.Getenv("AURIGA_BUNDLE_AUTH_HOST") == u.Host {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.Join(err, ctx.Err())
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return fmt.Errorf("发行下载 HTTP %d", response.StatusCode)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, cancellationReader(ctx, io.LimitReader(response.Body, (128<<30)+1)))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return verifyFetchedDigest(ctx, target, a.SHA256)
}

func verifyFetchedDigest(ctx context.Context, target, expectedSHA string) error {
	hash, err := digestContext(ctx, target)
	if err != nil {
		return err
	}
	if hash != expectedSHA {
		return errors.New("下载摘要不匹配，拒绝打包")
	}
	return nil
}

func bundle(args []string) error {
	return bundleExpected(args, "")
}

func bundleExpected(args []string, expectedCommit string) error {
	return bundleExpectedContext(context.Background(), args, expectedCommit)
}

func bundleExpectedContext(ctx context.Context, args []string, expectedCommit string) error {
	f := flags("bundle")
	index := f.String("release", "auriga-release.json", "")
	out := f.String("out", "", "")
	build := f.Bool("build", false, "")
	offline := f.Bool("offline", false, "")
	online := f.Bool("online", false, "")
	version := f.String("version", "", "")
	sign := f.String("sign-key", "", "")
	memory := f.Int("memory-mib", 4096, "")
	cpus := f.Int("cpus", 2, "")
	installAfter := f.Bool("install", false, "")
	data := f.String("data", "/srv/auriga", "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *build {
		if err := validateDockerImageTimeoutEnv(os.LookupEnv); err != nil {
			return err
		}
	}
	if err := validateOnlineFlags(*online, *offline, *build, *sign); err != nil {
		return err
	}
	if f.NArg() != 0 || *offline && !*build {
		return errors.New("--offline 须与 --build 同用，bundle 不接受多余参数")
	}
	// kitMode：基础材料取自已绑定的开发套件（--offline 全离线；--online 仅锁变化的语言依赖联网）。
	kitMode := *offline || *online
	if *installAfter && (!kitMode || !*build) {
		return errors.New("--install 只支持 bundle --build --offline 或 --online，先备份再upgrade")
	}
	if *build && *offline && *sign == "" {
		return errors.New("离线现场构建必须 --sign-key <已登记站点构建私钥>；先运行 keygen site 并在服务器 trust add-site-key")
	}
	var r release
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	options := buildOptions{Context: ctx, Offline: kitMode, Online: *online, Source: source, Budget: buildBudget{MemoryMiB: *memory, CPUs: *cpus}}
	if *build {
		if err := buildEnvironmentContext(ctx, options.Budget); err != nil {
			return err
		}
	}
	var kit *devkitManifest
	kitRoot := ""
	if kitMode {
		kitRoot, kit, err = locateDevkitLocks(source, *online)
		if err != nil {
			return err
		}
		if kit.cleanup != nil {
			defer kit.cleanup()
		}
		if *online {
			current, err := dependencyLocksContext(ctx, source, kit.Release)
			if err != nil {
				return err
			}
			if err := checkOnlineScope(*kit.Locks, current); err != nil {
				return err
			}
			options.LockDigest, options.ChangedLockFiles = current.Digest, changedLockFiles(*kit.Locks, current)
			if kit.Source != nil {
				options.BaselineCommit = kit.Source.Commit
			}
		}
		r = kit.Release
		options.Cache = filepath.Join(kitRoot, "cache")
	} else if err := readJSON(*index, &r); err != nil {
		return fmt.Errorf("读取批准的发行索引失败，参考 deploy/offline/release.example.json %w", err)
	}
	if r.Schema != 1 || !regexpVersion.MatchString(r.Version) {
		return errors.New("发行版本无效")
	}
	for _, name := range []string{"postgres", "nats", "registry"} {
		if !immutable.MatchString(r.Images[name].Ref) {
			return fmt.Errorf("%s 必须按仓库 digest 固定", name)
		}
	}
	if err := dockerReadyContext(ctx); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "auriga-offline-bundle-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if *build {
		options.Info, err = snapshotSourceContext(ctx, source, filepath.Join(stage, "source"), *sign)
		if err != nil {
			return err
		}
		_, archiveSourceErr := os.Lstat(filepath.Join(source, "auriga-source.json"))
		if kit != nil && kit.Source != nil && os.IsNotExist(archiveSourceErr) {
			options.Info.UpstreamVersion = kit.Source.UpstreamVersion
			options.Info.SiteChanges = options.Info.Dirty || options.Info.Commit != kit.Source.Commit || kit.Source.SnapshotSHA256 != "" && options.Info.SnapshotSHA256 != kit.Source.SnapshotSHA256
		}
		if archiveSourceErr == nil {
			r.Version = options.Info.UpstreamVersion
		}
		if err := sourceVersion(options.Info, r, *version, true); err != nil {
			return err
		}
		r.Source = options.Info
		r.Version = options.Info.Version
	} else if *version != "" {
		r.Version = *version
		if !regexpVersion.MatchString(r.Version) {
			return errors.New("版本名无效")
		}
	}
	if *out == "" {
		*out = "auriga-offline-" + r.Version + ".tar"
	}
	archive := filepath.Join(stage, "nas.tar")
	if err := fetchInput(r.NAS, archive, options); err != nil {
		return err
	}
	root := filepath.Join(stage, "package")
	if err := os.Mkdir(root, 0755); err != nil {
		return err
	}
	if err := unpackContext(ctx, archive, root); err != nil {
		return err
	}
	// The NAS artifact retains its original, narrower manifest for the existing
	// independently verified five-image loader.
	if err := os.Rename(filepath.Join(root, "SHA256SUMS"), filepath.Join(root, "NAS-SHA256SUMS")); err != nil {
		return err
	}
	if err := copyFileContext(ctx, filepath.Join(root, "NAS-SHA256SUMS"), filepath.Join(root, "SHA256SUMS"), 0644); err != nil {
		return err
	}
	boot := filepath.Join(root, "deploy-nas/bin/auriga-nas-bootstrap")
	if _, err := os.Stat(filepath.Join(root, "deploy-nas/scripts/oneclick-compose.sh")); !*build && err != nil {
		return errors.New("NAS 发行缺少一键安装配方，需要包含本功能的发行制品")
	}
	if err := commandContext(ctx, nil, os.Stdout, nil, boot, "image-check", root); err != nil {
		return err
	}
	if kitMode {
		r, err = loadDevkitImagesContext(ctx, kitRoot, *kit, boot)
		if err != nil {
			return err
		}
		r.Source = options.Info
		r.Version = options.Info.Version
	}
	if r.Source == nil {
		var manifest struct {
			Commit string `json:"commit"`
		}
		if err := readJSONLoose(filepath.Join(root, "images/manifest.json"), &manifest); err != nil {
			return err
		}
		r.Source = &provenance{Version: r.Version, UpstreamVersion: r.Version, Commit: manifest.Commit, Branch: "release"}
	}
	if *build {
		if err := buildRelease(root, stage, r, options); err != nil {
			return err
		}
	}
	if expectedCommit != "" {
		var manifest struct {
			Commit string `json:"commit"`
		}
		if err := readJSONLoose(filepath.Join(root, "images/manifest.json"), &manifest); err != nil {
			return err
		}
		if manifest.Commit != expectedCommit {
			return errors.New("NAS 镜像制品提交与发行标签不符，重新构建该标签制品或使用 release --build")
		}
	}
	for name, entry := range r.Images {
		if !regexpName.MatchString(name) || !immutable.MatchString(entry.Ref) {
			return errors.New("镜像条目必须固定 digest")
		}
		entry.Tag = "auriga/offline-" + name + ":" + r.Version
		entry.Archive = "extra-images/" + name + ".tar"
		if !kitMode && strings.Contains(entry.Ref, "@sha256:") {
			if err := commandContext(ctx, nil, os.Stdout, nil, "docker", "pull", "--platform", "linux/amd64", entry.Ref); err != nil {
				return err
			}
		}
		if err := commandContext(ctx, nil, os.Stdout, nil, "docker", "tag", entry.Ref, entry.Tag); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, "extra-images"), 0755); err != nil {
			return err
		}
		if err := commandContext(ctx, nil, os.Stdout, nil, "docker", "save", "--platform", "linux/amd64", "-o", filepath.Join(root, entry.Archive), entry.Tag); err != nil {
			return err
		}
		r.Images[name] = entry
	}
	for name, a := range map[string]*artifact{"desktop": r.Desktop, "seeds": r.Seeds} {
		if a == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Join(root, "optional"), 0755); err != nil {
			return err
		}
		if err := fetchInput(*a, filepath.Join(root, "optional", name+".tar"), options); err != nil {
			return err
		}
	}
	for _, file := range []string{"auriga", "deploy/offline/README-OFFLINE.md"} {
		target := filepath.Join(root, filepath.Base(file))
		if err := copyFileContext(ctx, filepath.Join(source, file), target, 0755); err != nil {
			return err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if *build {
		exe = filepath.Join(stage, "output/go/auriga-cli")
	}
	if err := copyFileContext(ctx, exe, filepath.Join(root, "bin/auriga-cli"), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(source, filepath.FromSlash(siteCompatRel))); err == nil {
		if err := applySchemaCompatToRelease(source, &r); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := jsonFile(filepath.Join(root, "offline-release.json"), r, 0644); err != nil {
		return err
	}
	if err := jsonFile(filepath.Join(root, "provenance.json"), r.Source, 0644); err != nil {
		return err
	}
	if err := writeRuntimeExpectedManifest(source, root); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, "SHA256SUMS")); err != nil {
		return err
	}
	if *sign != "" {
		if err := signingPublic(*sign, filepath.Join(root, "release-public.pem")); err != nil {
			return err
		}
	}
	if err := writeSumsContext(ctx, root); err != nil {
		return err
	}
	if *sign != "" {
		if err := signSums(root, *sign); err != nil {
			return err
		}
	}
	if err := packContext(ctx, root, *out); err != nil {
		return err
	}
	hash, err := digestContext(ctx, *out)
	if err != nil {
		return err
	}
	info, err := os.Stat(*out)
	if err != nil {
		return err
	}
	fmt.Printf("离线包 %s\n字节 %d\nSHA256 %s\n请通过受信的独立渠道传递此摘要。\n", *out, info.Size(), hash)
	if err := os.WriteFile(*out+".sha256", []byte(hash+"\n"), 0644); err != nil {
		return err
	}
	if *installAfter {
		absolute, err := filepath.Abs(*out)
		if err != nil {
			return err
		}
		return dispatch([]string{"upgrade", absolute, "--data", *data})
	}
	return nil
}

func signSums(root, keyPath string) error {
	return signFile(filepath.Join(root, "SHA256SUMS"), keyPath)
}

func signFile(file, keyPath string) error {
	raw, err := smallFile(keyPath, 64<<10)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return errors.New("签名私钥需要 PKCS8 PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return errors.New("签名需要 Ed25519")
	}
	sums, err := smallFile(file, 4<<20)
	if err != nil {
		return err
	}
	return os.WriteFile(file+".sig", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, sums))+"\n"), 0644)
}

func verifySignature(root, publicPath string) error {
	if publicPath == "" {
		if _, err := os.Stat(filepath.Join(root, "release-public.pem")); err == nil {
			publicPath = filepath.Join(root, "release-public.pem")
		}
	}
	return verifyFileSignature(filepath.Join(root, "SHA256SUMS"), publicPath)
}

func verifyFileSignature(file, publicPath string) error {
	if publicPath == "" {
		return errors.New("缺少签名公钥，默认拒绝未签名材料")
	}
	raw, err := smallFile(publicPath, 64<<10)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return errors.New("需要独立批准的 SPKI PEM 公钥")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	public, ok := key.(ed25519.PublicKey)
	if !ok {
		return errors.New("签名公钥需要 Ed25519")
	}
	sums, err := smallFile(file, 4<<20)
	if err != nil {
		return err
	}
	raw, err = smallFile(file+".sig", 1024)
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return err
	}
	if !ed25519.Verify(public, sums, signature) {
		return errors.New("离线包签名不匹配")
	}
	return nil
}

func smallFile(path string, limit int64) ([]byte, error) {
	return smallFileContext(context.Background(), path, limit)
}

func smallFileContext(ctx context.Context, path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("签名材料超出大小限制或不是普通文件 %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	raw, err := io.ReadAll(cancellationReader(ctx, io.LimitReader(f, limit+1)))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("签名材料超出大小限制")
	}
	return raw, nil
}
