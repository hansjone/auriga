package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type uploadOptions struct {
	Server, Repo, Dir, Key string
	Execute, Publish       bool
}
type forgejoRelease struct {
	ID    int64  `json:"id"`
	Draft bool   `json:"draft"`
	Tag   string `json:"tag_name"`
}
type forgejoAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}
type forgejoAPI struct {
	base   string
	origin *url.URL
	client *http.Client
	token  string
}

var repoPart = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func uploadCommand(args []string) error {
	f := flags("release upload")
	var o uploadOptions
	f.StringVar(&o.Server, "server", "", "")
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
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, "..") {
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

// All requests, including attachment verification, remain on the approved
// HTTPS origin. Redirects cannot send the token to an object store or GitHub.
func (a *forgejoAPI) request(method, address string, body io.Reader, contentType string, length int64) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Host != a.origin.Host || u.User != nil {
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
		return nil, fmt.Errorf("发布 Forgejo %s 请求失败: %w", method, err)
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
		return fmt.Errorf("发布 Forgejo %s HTTP %d（保留草稿，处理后重跑）", method, response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
	}
	return nil
}

func (a *forgejoAPI) assets(path string) (map[string]forgejoAsset, error) {
	result := map[string]forgejoAsset{}
	for page := 1; page <= 10001; page++ {
		var entries []forgejoAsset
		if err := a.json("GET", fmt.Sprintf("%s?limit=50&page=%d", path, page), nil, &entries, 200); err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return result, nil
		}
		for _, entry := range entries {
			if _, ok := result[entry.Name]; ok {
				return nil, errors.New("远端附件重名")
			}
			result[entry.Name] = entry
		}
	}
	return nil, errors.New("远端附件分页超限")
}

func (a *forgejoAPI) verifyAsset(remote forgejoAsset, local assetInfo) error {
	if remote.Name != local.Name || remote.Size != local.Size {
		return fmt.Errorf("远端附件冲突 %s，不覆盖或删除", local.Name)
	}
	response, err := a.request("GET", remote.URL, nil, "", -1)
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
	var header bytes.Buffer
	writer := multipart.NewWriter(&header)
	if _, err := writer.CreateFormFile("attachment", info.Name); err != nil {
		return err
	}
	footer := "\r\n--" + writer.Boundary() + "--\r\n"
	body := io.MultiReader(bytes.NewReader(header.Bytes()), in, strings.NewReader(footer))
	response, err := a.request("POST", a.base+path+"?name="+url.QueryEscape(info.Name), body, writer.FormDataContentType(), int64(header.Len())+info.Size+int64(len(footer)))
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 201 {
		return fmt.Errorf("上传 %s HTTP %d（保留草稿，重跑可续传已完成附件）", info.Name, response.StatusCode)
	}
	return nil
}

func uploadRelease(o uploadOptions, client *http.Client) error {
	origin, err := forgejoOrigin(o.Server, o.Repo)
	if err != nil {
		return err
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
	scoped.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Host != origin.Host || req.URL.User != nil {
			return errors.New("拒绝跨主机或降级重定向")
		}
		return nil
	}
	a := forgejoAPI{base: strings.TrimSuffix(o.Server, "/"), origin: origin, client: &scoped, token: token}
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
	response, err := a.request("GET", a.base+repo+"/releases/tags/"+url.PathEscape(manifest.Tag), nil, "", -1)
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
		body := map[string]any{"tag_name": manifest.Tag, "target_commitish": manifest.Commit, "name": manifest.Tag, "draft": true, "body": "Auriga 离线安装包与开发套件。下载 SHA256SUMS 和签名，从独立渠道核准发行公钥；使用源码中的 DEPLOY.md / MAINTAIN.md。"}
		if err := a.json("POST", repo+"/releases", body, &remote, 201); err != nil {
			return err
		}
	} else if status != 200 {
		return fmt.Errorf("查询发行 HTTP %d", status)
	}
	if remote.ID <= 0 || remote.Tag != manifest.Tag {
		return errors.New("远端 Forgejo 发行身份不符")
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
		if !remote.Draft {
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
	if o.Publish && remote.Draft {
		if err := a.json("PATCH", releasePath, map[string]bool{"draft": false}, &remote, 200); err != nil {
			return err
		}
	}
	fmt.Printf("Forgejo Release 附件已逐字节校验，draft=%t\n", remote.Draft)
	return nil
}
