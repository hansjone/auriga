package main

import (
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
	Schema  int              `json:"schemaVersion"`
	Version string           `json:"version"`
	NAS     artifact         `json:"nas"`
	Images  map[string]image `json:"images"`
	Desktop *artifact        `json:"desktop,omitempty"`
	Seeds   *artifact        `json:"seeds,omitempty"`
	Build   *buildSpec       `json:"build,omitempty"`
	Source  *provenance      `json:"source,omitempty"`
}

type buildSpec struct {
	Inputs  artifact          `json:"inputs"`
	Recipes artifact          `json:"recipes"`
	Bases   map[string]string `json:"bases"`
	Tools   map[string]string `json:"tools"`
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

func fetch(a artifact, target string) error {
	if !hex64.MatchString(a.SHA256) {
		return errors.New("发行索引需要独立批准的 SHA256")
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return err
	}
	if u.Scheme == "" {
		if err := copyFile(a.URL, target, 0644); err != nil {
			return err
		}
	} else {
		if u.Scheme != "https" || u.User != nil {
			return errors.New("发行下载仅接受 HTTPS，无 URL 内凭据")
		}
		client := &http.Client{Timeout: 60 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
				return errors.New("拒绝跨主机或非 HTTPS 重定向")
			}
			return nil
		}}
		request, err := http.NewRequest(http.MethodGet, a.URL, nil)
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
			return err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != 200 {
			return fmt.Errorf("发行下载 HTTP %d", response.StatusCode)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(response.Body, (128<<30)+1))
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	hash, err := digest(target)
	if err != nil {
		return err
	}
	if hash != a.SHA256 {
		return errors.New("下载摘要不匹配，拒绝打包")
	}
	return nil
}

func bundle(args []string) error {
	f := flags("bundle")
	index := f.String("release", "auriga-release.json", "")
	out := f.String("out", "", "")
	build := f.Bool("build", false, "")
	offline := f.Bool("offline", false, "")
	version := f.String("version", "", "")
	sign := f.String("sign-key", "", "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *offline && !*build {
		return errors.New("--offline 须与 --build 同用，bundle 不接受多余参数")
	}
	var r release
	source, err := os.Getwd()
	if err != nil {
		return err
	}
	options := buildOptions{Offline: *offline, Source: source}
	var kit *devkitManifest
	kitRoot := ""
	if *offline {
		kitRoot, kit, err = locateDevkit(source)
		if err != nil {
			return err
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
	if err := dockerReady(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "auriga-offline-bundle-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if *build {
		options.Info, err = snapshotSource(source, filepath.Join(stage, "source"), *sign)
		if err != nil {
			return err
		}
		if kit != nil && kit.Source != nil {
			options.Info.UpstreamVersion = kit.Source.UpstreamVersion
			options.Info.SiteChanges = options.Info.Dirty || options.Info.Commit != kit.Source.Commit || options.Info.SnapshotSHA256 != kit.Source.SnapshotSHA256
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
	if err := unpack(archive, root); err != nil {
		return err
	}
	// The NAS artifact retains its original, narrower manifest for the existing
	// independently verified five-image loader.
	if err := os.Rename(filepath.Join(root, "SHA256SUMS"), filepath.Join(root, "NAS-SHA256SUMS")); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(root, "NAS-SHA256SUMS"), filepath.Join(root, "SHA256SUMS"), 0644); err != nil {
		return err
	}
	boot := filepath.Join(root, "deploy-nas/bin/auriga-nas-bootstrap")
	if _, err := os.Stat(filepath.Join(root, "deploy-nas/scripts/oneclick-compose.sh")); !*build && err != nil {
		return errors.New("NAS 发行缺少一键安装配方，需要包含本功能的发行制品")
	}
	if err := command(nil, os.Stdout, nil, boot, "image-check", root); err != nil {
		return err
	}
	if *offline {
		r, err = loadDevkitImages(kitRoot, *kit, boot)
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
	for name, entry := range r.Images {
		if !regexpName.MatchString(name) || !immutable.MatchString(entry.Ref) {
			return errors.New("镜像条目必须固定 digest")
		}
		entry.Tag = "auriga/offline-" + name + ":" + r.Version
		entry.Archive = "extra-images/" + name + ".tar"
		if !*offline && strings.Contains(entry.Ref, "@sha256:") {
			if err := command(nil, os.Stdout, nil, "docker", "pull", "--platform", "linux/amd64", entry.Ref); err != nil {
				return err
			}
		}
		if err := command(nil, os.Stdout, nil, "docker", "tag", entry.Ref, entry.Tag); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, "extra-images"), 0755); err != nil {
			return err
		}
		if err := command(nil, os.Stdout, nil, "docker", "save", "--platform", "linux/amd64", "-o", filepath.Join(root, entry.Archive), entry.Tag); err != nil {
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
		if err := copyFile(filepath.Join(source, file), target, 0755); err != nil {
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
	if err := copyFile(exe, filepath.Join(root, "bin/auriga-cli"), 0755); err != nil {
		return err
	}
	if err := jsonFile(filepath.Join(root, "offline-release.json"), r, 0644); err != nil {
		return err
	}
	if err := jsonFile(filepath.Join(root, "provenance.json"), r.Source, 0644); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(root, "SHA256SUMS")); err != nil {
		return err
	}
	if err := writeSums(root); err != nil {
		return err
	}
	if *sign != "" {
		if err := signSums(root, *sign); err != nil {
			return err
		}
	}
	if err := pack(root, *out); err != nil {
		return err
	}
	hash, err := digest(*out)
	if err != nil {
		return err
	}
	info, err := os.Stat(*out)
	if err != nil {
		return err
	}
	fmt.Printf("离线包 %s\n字节 %d\nSHA256 %s\n请通过受信的独立渠道传递此摘要。\n", *out, info.Size(), hash)
	return os.WriteFile(*out+".sha256", []byte(hash+"\n"), 0644)
}

func signSums(root, keyPath string) error {
	raw, err := os.ReadFile(keyPath)
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
	sums, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "SHA256SUMS.sig"), []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, sums))+"\n"), 0644)
}

func verifySignature(root, publicPath string) error {
	if publicPath == "" {
		return nil
	}
	raw, err := os.ReadFile(publicPath)
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
	sums, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return err
	}
	raw, err = os.ReadFile(filepath.Join(root, "SHA256SUMS.sig"))
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
