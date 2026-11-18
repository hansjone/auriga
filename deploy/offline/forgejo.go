package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type uploadOptions struct {
	Server, Repo, Dir, Key string
	InternalHTTP           string
	Execute, Publish       bool
}
type forgejoRelease struct {
	ID    int64  `json:"id"`
	Draft *bool  `json:"draft"`
	Tag   string `json:"tag_name"`
}

func (r forgejoRelease) publishedAs(expected forgejoRelease) bool {
	return r.ID == expected.ID && r.Tag == expected.Tag && r.Draft != nil && !*r.Draft
}

type forgejoAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}
type forgejoAPI struct {
	base   string
	origin *url.URL
	public *url.URL
	client *http.Client
	token  string
}

var repoPart = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
var attachmentRoute = regexp.MustCompile(`^/attachments/[a-fA-F0-9-]+$`)

func uploadCommand(args []string) error {
	f := flags("release upload")
	var o uploadOptions
	f.StringVar(&o.Server, "server", "", "")
	f.StringVar(&o.InternalHTTP, "internal-http", "", "仅在受控内网直连 Forgejo HTTP（私有或回环 IP）")
	f.StringVar(&o.Repo, "repo", "", "")
	f.StringVar(&o.Dir, "dir", "", "")
	f.StringVar(&o.Key, "verify-key", "", "")
	f.BoolVar(&o.Execute, "execute", false, "")
	f.BoolVar(&o.Publish, "publish", false, "")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("多余发布参数")
	}
	return uploadRelease(o, &http.Client{Timeout: 60 * time.Minute})
}

func forgejoOrigin(server, repo string) (*url.URL, error) {
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(u.Path, "..") {
		return nil, errors.New("发布 --server 需要无凭据的 Forgejo HTTPS 站点地址")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, blocked := range []string{"github.com", "githubusercontent.com", "github.io"} {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return nil, errors.New("附件只发布 Forgejo Release，禁止 GitHub")
		}
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return nil, errors.New("--repo 需要 owner/repo")
	}
	for _, part := range parts {
		if !repoPart.MatchString(part) || part == "." || part == ".." {
			return nil, errors.New("无效 Forgejo owner/repo")
		}
	}
	return u, nil
}

func forgejoInternalOrigin(server string) (*url.URL, error) {
	u, err := url.Parse(server)
	invalid := errors.New("--internal-http 需要无凭据、带端口的 Forgejo 内网 HTTP 地址（私有或回环 IP）")
	if err != nil || u.Scheme != "http" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(u.Path, "..") {
		return nil, invalid
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || ip.Zone() != "" || (!ip.Unmap().IsPrivate() && !ip.Unmap().IsLoopback()) {
		return nil, invalid
	}
	return u, nil
}

func sameForgejoOrigin(u, origin *url.URL) bool {
	return u.Scheme == origin.Scheme && u.Host == origin.Host && u.User == nil
}

// Only remap Forgejo's own attachment route when its canonical HTTPS address
// is different from the approved internal transport.
func (a *forgejoAPI) attachmentAddress(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", errors.New("无效 Forgejo 附件地址")
	}
	if a.public == nil {
		return address, nil
	}
	if u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == "" {
		for _, origin := range []*url.URL{a.public, a.origin} {
			route, ok := strings.CutPrefix(u.Path, strings.TrimSuffix(origin.Path, "/"))
			if ok && sameForgejoOrigin(u, origin) && attachmentRoute.MatchString(route) {
				return a.base + route, nil
			}
		}
	}
	return "", errors.New("拒绝跨站点或非 Forgejo 附件地址")
}

func (a *forgejoAPI) request(method, address string, body io.Reader, contentType string, length int64) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !sameForgejoOrigin(u, a.origin) {
		return nil, errors.New("拒绝跨站点附件地址")
	}
	req, err := http.NewRequest(method, address, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+a.token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if length >= 0 {
		req.ContentLength = length
	}
	response, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发布 Forgejo %s 请求失败: %s", method, strings.ReplaceAll(err.Error(), a.token, "[已隐藏]"))
	}
	return response, nil
}

func (a *forgejoAPI) json(method, path string, input, output any, expected int) error {
	var raw []byte
	var err error
	if input != nil {
		raw, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	response, err := a.request(method, a.base+path, bytes.NewReader(raw), "application/json", int64(len(raw)))
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != expected {
		return fmt.Errorf("发布 Forgejo %s HTTP %d", method, response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
	}
	return nil
}

func (a *forgejoAPI) assets(path string) (map[string]forgejoAsset, error) {
	// Forgejo returns the complete attachment list and ignores page parameters.
	var entries []forgejoAsset
	if err := a.json("GET", path, nil, &entries, 200); err != nil {
		return nil, err
	}
	result := map[string]forgejoAsset{}
	for _, entry := range entries {
		if _, ok := result[entry.Name]; ok {
			return nil, errors.New("远端附件重名")
		}
		result[entry.Name] = entry
	}
	return result, nil
}

func (a *forgejoAPI) verifyAsset(remote forgejoAsset, local assetInfo) error {
	if remote.Name != local.Name || remote.Size != local.Size {
		return fmt.Errorf("远端附件冲突 %s，不覆盖或删除", local.Name)
	}
	address, err := a.attachmentAddress(remote.URL)
	if err != nil {
		return err
	}
	response, err := a.request("GET", address, nil, "", -1)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return fmt.Errorf("读取远端附件 HTTP %d", response.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(response.Body, local.Size+1))
	if err != nil {
		return err
	}
	if n != local.Size || hex.EncodeToString(h.Sum(nil)) != local.SHA256 {
		return fmt.Errorf("远端附件摘要冲突 %s，不覆盖或删除", local.Name)
	}
	return nil
}

func (a *forgejoAPI) upload(path, root string, info assetInfo) error {
	file := filepath.Join(root, info.Name)
	actual, err := describeAsset(file)
	if err != nil {
		return err
	}
	if actual != info {
		return errors.New("上传前附件发生变化")
	}
	in, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	var body io.Reader = in
	if info.Size == 0 {
		body = http.NoBody
	}
	// Forgejo can stream the request body directly to attachment storage.
	response, err := a.request("POST", a.base+path+"?name="+url.QueryEscape(info.Name), body, "application/octet-stream", info.Size)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 201 {
		return fmt.Errorf("上传 %s HTTP %d（保留草稿，重跑可续传已完成附件）", info.Name, response.StatusCode)
	}
	return nil
}

func forgejoReleaseBody(m distributionManifest, key string) (string, error) {
	fp, err := publicFingerprint(key)
	if err != nil {
		return "", err
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Auriga 离线安装包与开发套件。发行公钥指纹 SHA256(SPKI DER)：`%s`。从独立渠道核对后使用 `--trust-fingerprint %s` 或 `--verify-key`，参见 DEPLOY.md / MAINTAIN.md。\n\n", fp, fp)
	if m.Windows != nil {
		fmt.Fprintf(&body, "Windows 先用 Get-FileHash 核验源码和套件归档，再解包，核验引导 Node 后执行客户端命令。\n\n- `%s` SHA256 `%s`\n", windowsBuildNodePath, m.Windows.BuildNodeSHA)
		for _, file := range m.Archives {
			if file.Name == "auriga-source-"+m.Tag+".tar" || strings.HasPrefix(file.Name, windowsClientPrefix) {
				fmt.Fprintf(&body, "- `%s` SHA256 `%s`\n", file.Name, file.SHA256)
			}
		}
	}
	body.WriteString("\n附件选择、分卷与 DSH 上游参考材料见 DOWNLOADS.txt。")
	return body.String(), nil
}

func uploadRelease(o uploadOptions, client *http.Client) error {
	origin, err := forgejoOrigin(o.Server, o.Repo)
	if err != nil {
		return err
	}
	var public *url.URL
	if o.InternalHTTP != "" {
		public = origin
		origin, err = forgejoInternalOrigin(o.InternalHTTP)
		if err != nil {
			return err
		}
	}
	if o.Key == "" {
		return errors.New("发布须 --verify-key 独立批准的发行公钥")
	}
	if err := verifySignature(o.Dir, o.Key); err != nil {
		return err
	}
	if err := verifySums(o.Dir); err != nil {
		return err
	}
	var manifest distributionManifest
	if err := readJSON(filepath.Join(o.Dir, "release-manifest.json"), &manifest); err != nil {
		return err
	}
	if manifest.Schema != 1 || !regexpVersion.MatchString(manifest.Tag) || !regexp.MustCompile(`^[a-f0-9]{40,64}$`).MatchString(manifest.Commit) {
		return errors.New("发行清单标签或提交无效")
	}
	if err := validateBuildGate(o.Dir, manifest); err != nil {
		return err
	}
	releaseBody, err := forgejoReleaseBody(manifest, o.Key)
	if err != nil {
		return err
	}
	names, err := regularFiles(o.Dir)
	if err != nil {
		return err
	}
	local := map[string]assetInfo{}
	for _, name := range names {
		if filepath.Base(name) != name {
			return errors.New("发行附件必须平铺")
		}
		info, err := describeAsset(filepath.Join(o.Dir, name))
		if err != nil {
			return err
		}
		local[name] = info
	}
	seen := map[string]bool{"release-manifest.json": true, "SHA256SUMS": true, "SHA256SUMS.sig": true}
	for _, info := range manifest.Assets {
		if seen[info.Name] || local[info.Name] != info {
			return errors.New("发行清单附件无效")
		}
		seen[info.Name] = true
	}
	if len(seen) != len(local) {
		return errors.New("发行清单未覆盖全部附件")
	}
	fmt.Printf("Forgejo Release %s/%s 标签 %s 提交 %s；附件 %d 件\n", strings.TrimSuffix(o.Server, "/"), o.Repo, manifest.Tag, manifest.Commit, len(names))
	for _, name := range names {
		fmt.Printf("  %s %d B %s\n", name, local[name].Size, local[name].SHA256)
	}
	if !o.Execute {
		fmt.Println("预演完成：未访问网络。主控加 --execute 上传草稿，另加 --publish 发布。无 CI 冷建。 ")
		return nil
	}
	token := os.Getenv("FORGEJO_TOKEN")
	if token == "" {
		return errors.New("设置 FORGEJO_TOKEN 环境变量，不把令牌放参数中")
	}
	scoped := *client
	if public != nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		defer transport.CloseIdleConnections()
		scoped.Transport = transport
	}
	scoped.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// A 302 or 303 may have already rewritten req.Method to GET.
		if via[0].Method != http.MethodGet && via[0].Method != http.MethodHead {
			return errors.New("拒绝 Forgejo 写请求重定向")
		}
		if len(via) >= 5 || !sameForgejoOrigin(req.URL, origin) {
			return errors.New("拒绝跨主机或降级重定向")
		}
		return nil
	}
	a := forgejoAPI{base: strings.TrimSuffix(origin.String(), "/"), origin: origin, public: public, client: &scoped, token: token}
	var version struct {
		Version string `json:"version"`
	}
	if err := a.json("GET", "/api/forgejo/v1/version", nil, &version, 200); err != nil {
		return fmt.Errorf("目标必须是 Forgejo: %w", err)
	}
	if version.Version == "" {
		return errors.New("目标未返回 Forgejo 版本")
	}
	repo := "/api/v1/repos/" + o.Repo
	var tag struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := a.json("GET", repo+"/tags/"+url.PathEscape(manifest.Tag), nil, &tag, 200); err != nil {
		return err
	}
	if tag.Commit.SHA != manifest.Commit {
		return errors.New("远端 Forgejo 标签提交与本地签名发行清单不符；先由主控同步精确标签")
	}
	var remote forgejoRelease
	tagReleasePath := repo + "/releases/tags/" + url.PathEscape(manifest.Tag)
	response, err := a.request("GET", a.base+tagReleasePath, nil, "", -1)
	if err != nil {
		return err
	}
	status := response.StatusCode
	if status == 200 {
		err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&remote)
	}
	_ = response.Body.Close()
	if err != nil {
		return err
	}
	if status == 404 {
		body := map[string]any{"tag_name": manifest.Tag, "target_commitish": manifest.Commit, "name": manifest.Tag, "draft": true, "body": releaseBody}
		if err := a.json("POST", repo+"/releases", body, &remote, 201); err != nil {
			return err
		}
	} else if status != 200 {
		return fmt.Errorf("查询发行 HTTP %d", status)
	}
	if remote.ID <= 0 || remote.Tag != manifest.Tag {
		return errors.New("远端 Forgejo 发行身份不符")
	}
	if remote.Draft == nil {
		return errors.New("远端 Forgejo 发行缺少草稿状态")
	}
	releasePath := fmt.Sprintf("%s/releases/%d", repo, remote.ID)
	assetsPath := releasePath + "/assets"
	existing, err := a.assets(assetsPath)
	if err != nil {
		return err
	}
	for name, asset := range existing {
		info, ok := local[name]
		if !ok {
			return fmt.Errorf("远端存在未批准附件 %s，保留现场交主控处理", name)
		}
		if err := a.verifyAsset(asset, info); err != nil {
			return err
		}
	}
	for _, name := range names {
		if _, ok := existing[name]; ok {
			continue
		}
		if !*remote.Draft {
			return errors.New("已发布发行缺少附件，不直接修改已发布内容")
		}
		if err := a.upload(assetsPath, o.Dir, local[name]); err != nil {
			return err
		}
	}
	final, err := a.assets(assetsPath)
	if err != nil {
		return err
	}
	if len(final) != len(local) {
		return errors.New("上传后附件数量不符，保留草稿")
	}
	for name, info := range local {
		if err := a.verifyAsset(final[name], info); err != nil {
			return err
		}
	}
	if o.Publish && *remote.Draft {
		const retry = "请先核对 Forgejo 上该标签的实际发行状态，再使用同一发行目录和参数重试"
		var published forgejoRelease
		if err := a.json("PATCH", releasePath, map[string]any{"draft": false, "body": releaseBody}, &published, 200); err != nil {
			return fmt.Errorf("%s: %w", retry, err)
		}
		if !published.publishedAs(remote) {
			return fmt.Errorf("Forgejo 发布响应的发行身份或草稿状态不符，%s", retry)
		}
		var confirmed forgejoRelease
		if err := a.json("GET", tagReleasePath, nil, &confirmed, 200); err != nil {
			return fmt.Errorf("Forgejo 发布回读失败，%s: %w", retry, err)
		}
		if !confirmed.publishedAs(remote) {
			return fmt.Errorf("Forgejo 发布回读的发行身份或草稿状态不符，%s", retry)
		}
		remote = confirmed
	}
	fmt.Printf("Forgejo Release 附件已逐字节校验，draft=%t\n", *remote.Draft)
	return nil
}
