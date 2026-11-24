package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func waitHealthy(s state) error {
	return waitHealthyFor(s, 10*time.Minute)
}

// waitHealthyFor 等待全部服务健康，超时返回最后一次的不健康原因（deploy 用 server.healthTimeout，默认 10 分钟）。
func waitHealthyFor(s state, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := health(s)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("服务健康等待超过 %s（%v），运行 ./auriga doctor 检查", timeout, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func health(s state) error {
	var out bytes.Buffer
	if err := compose(s, &out, "ps", "--all", "--format", "json"); err != nil {
		return err
	}
	return healthyContainers(out.Bytes())
}

func healthyContainers(raw []byte) error {
	type container struct {
		Service, State, Health string
		ExitCode               int
	}
	var records []container
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		if err := json.Unmarshal(raw, &records); err != nil {
			return err
		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		for {
			var c container
			err := decoder.Decode(&c)
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			records = append(records, c)
		}
	}
	seen := map[string]bool{}
	for _, c := range records {
		seen[c.Service] = true
		if strings.HasSuffix(c.Service, "-prepare") && c.State == "exited" && c.ExitCode == 0 {
			continue
		}
		if c.State != "running" || c.Health != "" && c.Health != "healthy" {
			return fmt.Errorf("服务 %s state=%s health=%s", c.Service, c.State, c.Health)
		}
	}
	for _, name := range []string{"postgres", "nats", "registry", "platform", "edge", "agent", "bifrost", "egress"} {
		if !seen[name] {
			return fmt.Errorf("缺少服务 %s", name)
		}
	}
	return nil
}

func doctor(s state) error {
	var installed release
	if err := readJSON(filepath.Join(s.Package, "offline-release.json"), &installed); err == nil {
		fmt.Println(printProvenance(installed.Source))
		if line := siteDependenciesInfo(s.Package); line != "" {
			fmt.Println(line)
		}
	}
	failures := 0
	check := func(name, repair string, test func() error) {
		if err := test(); err != nil {
			failures++
			fmt.Printf("FAIL %s — %v\n  修复建议 %s\n", name, err, repair)
		} else {
			fmt.Printf("PASS %s\n", name)
		}
	}
	fmt.Printf("安装来源 %s\n", s.source())
	if s.TrustUnpinned {
		fmt.Println("INFO 发行钥未钉定（本机构建首装没有带 trustFingerprint 的套件，或 adopt 接管）：第一次签名包 upgrade 前须先 trust add-release-key 或 add-site-key")
	}
	check("Docker/Compose版本", "离线预装 Docker Engine 与 Compose 2.29+，检查本机 socket。", dockerReady)
	for _, c := range releaseChecks(s) {
		check(c.name, c.repair, c.test)
	}
	check("配置及原有安全门禁", "按诊断修复原配置，保留密钥与材料回执。", func() error { return nas(s, "preflight") })
	check("资源私钥权限", "从原备份恢复 UID10001、0700目录、0600私钥。", func() error {
		root := filepath.Join(s.Data, "resource-keys/platform")
		for path, want := range map[string]string{root: "10001:10001:700", filepath.Join(root, "release.pk8"): "10001:10001:600", filepath.Join(root, "revocation.pk8"): "10001:10001:600"} {
			if err := noSymlinks(path); err != nil {
				return err
			}
			got, err := capture("stat", "-c", "%u:%g:%a", path)
			if err != nil {
				return err
			}
			if got != want {
				return errors.New("资源私钥属主或权限不符")
			}
		}
		return nil
	})
	check("数据库", "检查 postgres 状态和磁盘，保留数据库及密钥，恢复前先备份。", func() error {
		return compose(s, nil, "exec", "-T", "postgres", "pg_isready", "-U", "auriga_bootstrap", "-d", "auriga")
	})
	check("各服务健康", "检查 ./auriga logs，修复失败服务。", func() error { return health(s) })
	check("正常管理台入口", "检查现场地址、DNS或受信代理及端口映射。", func() error { return browserEntry(s) })
	check("对象存储UID10001写入", "恢复资源对象目录属主10001及0700权限，检查磁盘。", func() error { return objectWrite(s) })
	check("真实登录及资源撤销签名", "私密TTY查看应急材料并核对时间与账户，使用正常页面登录。代理模式核对mTLS接线。", func() error { return loginProbe(s) })
	var r release
	if err := readJSON(filepath.Join(s.Package, "offline-release.json"), &r); err == nil && r.Desktop == nil {
		fmt.Println("SKIP 桌面发行 — 未随包附带，桌面端验收待单独执行")
	} else {
		var desktopSummary string
		check("桌面发行", "将公钥根传给桌面构建 --resource-trust-json，通过管理台导入已签名桌面发行。", func() error {
			var err error
			desktopSummary, err = desktopReleaseCheck(s)
			return err
		})
		if desktopSummary != "" {
			fmt.Printf("  %s\n", desktopSummary)
		}
	}
	fmt.Println(accessCapabilities(s).DoctorLine())
	fmt.Printf("doctor 结束，失败项 %d。输出不含凭据。\n", failures)
	if failures > 0 {
		return errors.New("请按 FAIL 修复建议处理")
	}
	return nil
}

func browserEntry(s state) error {
	var access struct {
		AdminOrigin struct {
			Scheme, Host string
			Port         int
		}
	}
	if err := readJSONLoose(s.Access, &access); err != nil {
		return err
	}
	u := fmt.Sprintf("%s://%s:%d/", access.AdminOrigin.Scheme, access.AdminOrigin.Host, access.AdminOrigin.Port)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errors.New("重定向过多")
		}
		return nil
	}}
	r, err := client.Get(u)
	if err != nil {
		return errors.New("管理台地址不可达或TLS校验失败")
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "text/html") {
		return fmt.Errorf("管理台未返回HTML，HTTP %d", r.StatusCode)
	}
	return nil
}

func readJSONLoose(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

func objectWrite(s state) error {
	return command(nil, io.Discard, nil, "docker", "run", "--rm", "--pull", "never", "--network", "none", "--log-driver", "none", "--user", "10001:10001", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,src="+filepath.Join(s.Data, "resource-objects")+",dst=/objects", "--entrypoint", "sh", s.Images["POSTGRES"], "-ec", "umask 077; f=$(mktemp /objects/.doctor.XXXXXXXX); trap 'rm -f \"$f\"' EXIT; printf test >\"$f\"; test -s \"$f\"")
}

type doctorCheck struct {
	name, repair string
	test         func() error
}

// releaseChecks 是 doctor 里与发行信任、完整性、镜像相关的三项（local-deploy 来源按本地清单
// 与 releaseSums 核对，不要求签名）。
func releaseChecks(s state) []doctorCheck {
	return []doctorCheck{
		{"发行信任状态", "从批准公钥重新建立受信安装；无签名历史标记不会自动清除。", func() error {
			return trustHealth(s)
		}},
		{"离线包完整性", "从受信原包恢复发行文件；本机构建的发行用 deploy 重新构建或 rollback。", func() error {
			if err := verifySums(s.Package); err != nil {
				return err
			}
			_, err := verifyStatePackage(s, s.Package, "", false)
			return err
		}},
		{"镜像摘要与层", "重拷离线包并由 install/upgrade 重新 load，勿直接替换镜像；本机构建或 adopt 的发行用 deploy 重新构建或 rollback。", func() error {
			if s.source() == sourceLocalDeploy || s.source() == sourceAdopted {
				return checkLocalDeployImages(s)
			}
			var r release
			if err := readJSON(filepath.Join(s.Package, "offline-release.json"), &r); err != nil {
				return err
			}
			for name, id := range s.Images {
				platform, err := capture("docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", id)
				if err != nil || platform != "linux/amd64" {
					return fmt.Errorf("%s 镜像缺失或架构错误", name)
				}
			}
			return checkLoadedImages(s, r)
		}},
	}
}

// checkLocalDeployImages（local-deploy 来源）：state 里每个镜像都在本机且是 linux/amd64，
// 当前发行的 auriga/<svc>:r<序号> 标签仍指向历史记录的镜像。
func checkLocalDeployImages(s state) error {
	r := s.record(s.Current)
	if r == nil {
		return fmt.Errorf("state 缺少当前发行 r%d 的历史记录", s.Current)
	}
	ctx := context.Background()
	for name, id := range s.Images {
		if _, platform, ok := localImage(ctx, id); !ok || platform != "linux/amd64" {
			return fmt.Errorf("%s 镜像缺失或架构错误", name)
		}
	}
	return checkTaggedImages(ctx, r)
}

func checkLoadedImages(s state, r release) error {
	boot := filepath.Join(s.Package, "deploy-nas/bin/auriga-nas-bootstrap")
	for _, name := range []string{"platform", "edge", "agent", "bifrost", "operator"} {
		id, err := captureImageResolve(context.Background(), boot, filepath.Join(s.Package, "images", name+".tar"), "auriga/"+name+":nas", s.Images[strings.ToUpper(name)])
		if err != nil {
			return err
		}
		if s.Images[strings.ToUpper(name)] != id {
			return errors.New("当前服务镜像与安装时身份不符")
		}
	}
	for name, entry := range r.Images {
		id, err := captureImageResolve(context.Background(), boot, filepath.Join(s.Package, entry.Archive), entry.Tag, s.Images[strings.ToUpper(strings.ReplaceAll(name, "-", "_"))])
		if err != nil {
			return err
		}
		if s.Images[strings.ToUpper(strings.ReplaceAll(name, "-", "_"))] != id {
			return errors.New("附加镜像身份发生变化")
		}
	}
	return nil
}

func loginProbe(s state) error {
	edge, err := capture("docker", "ps", "-q", "--no-trunc", "--filter", "label=com.docker.compose.project=auriga-nas-test", "--filter", "label=com.docker.compose.service=edge")
	if err != nil || !hex64.MatchString(edge) {
		return errors.New("找不到唯一运行的edge")
	}
	args := []string{"run", "--rm", "--pull", "never", "--log-driver", "none", "--network", "container:" + edge, "--user", "10001:10001", "--cap-drop", "ALL", "--read-only", "--security-opt", "no-new-privileges:true", "--env", "PYTHONDONTWRITEBYTECODE=1"}
	for source, target := range map[string]string{filepath.Join(s.Package, "deploy-nas/scripts"): "/tools", s.Access: "/access.json", filepath.Join(s.Data, "enrollment/emergency-enrollment.json"): "/enrollment.json", filepath.Join(s.Data, "resource-keys/resource-roots.json"): "/roots.json"} {
		args = append(args, "--mount", "type=bind,src="+source+",dst="+target+",readonly")
	}
	args = append(args, "--entrypoint", "python", s.Images["OPERATOR"], "/tools/oneclick-doctor.py")
	return command(nil, os.Stdout, nil, "docker", args...)
}

type desktopReleaseManifest struct {
	Version   string `json:"version"`
	Sequence  int64  `json:"sequence"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

type desktopReleaseItem struct {
	Manifest  desktopReleaseManifest `json:"manifest"`
	Withdrawn bool                   `json:"withdrawn"`
}

type desktopReleaseState struct {
	Items []desktopReleaseItem `json:"items"`
}

func desktopPublisherTenant(s state) (string, error) {
	tenant := strings.TrimSpace(s.DesktopPublisherTenant)
	if tenant == "" && s.Data != "" {
		envPath := filepath.Join(s.Data, "stack.env")
		if err := noSymlinks(envPath); err == nil {
			if raw, err := os.ReadFile(envPath); err == nil {
				for _, line := range strings.Split(string(raw), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "AURIGA_DESKTOP_PUBLISHER_TENANT_ID=") {
						tenant = strings.TrimSpace(strings.TrimPrefix(line, "AURIGA_DESKTOP_PUBLISHER_TENANT_ID="))
						break
					}
				}
			}
		}
	}
	if !desktopTenant.MatchString(tenant) || tenant == "00000000-0000-0000-0000-000000000000" {
		return "", errors.New("未配置有效的桌面发行租户，请先配置桌面发行公钥与租户并通过管理台导入已签名桌面发行")
	}
	return tenant, nil
}

// desktopReleaseCheck 校验 <data>/desktop/<publisherTenant>/state.json 中最新未撤回的桌面发行记录
// 及其对应的 <sha256>.exe 安装包（无符号链接、文件存在、大小与 SHA-256 均匹配）。
func desktopReleaseCheck(s state) (string, error) {
	tenant, err := desktopPublisherTenant(s)
	if err != nil {
		return "", err
	}
	tenantDir := filepath.Join(s.Data, "desktop", tenant)
	statePath := filepath.Join(tenantDir, "state.json")
	if err := noSymlinks(statePath); err != nil {
		return "", err
	}
	info, err := os.Lstat(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("尚未导入桌面发行（缺少 state.json），请通过管理台导入已签名桌面发行")
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("桌面发行台账 state.json 不是普通文件")
	}
	var ledger desktopReleaseState
	if err := readJSONLoose(statePath, &ledger); err != nil {
		return "", fmt.Errorf("桌面发行台账 state.json 解析失败：%w", err)
	}
	var best *desktopReleaseManifest
	for i := range ledger.Items {
		item := ledger.Items[i]
		if item.Withdrawn || item.Manifest.Sequence < 1 {
			continue
		}
		if best == nil || item.Manifest.Sequence > best.Sequence {
			candidate := item.Manifest
			best = &candidate
		}
	}
	if best == nil {
		return "", errors.New("尚无未撤回的桌面发行记录，请通过管理台导入已签名桌面发行")
	}
	if strings.TrimSpace(best.Version) == "" || best.SizeBytes < 1 || !hex64.MatchString(best.SHA256) {
		return "", fmt.Errorf("桌面发行记录（sequence %d）元数据不完整或无效", best.Sequence)
	}
	blobPath := filepath.Join(tenantDir, best.SHA256+".exe")
	if err := noSymlinks(blobPath); err != nil {
		return "", err
	}
	blobInfo, err := os.Lstat(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("桌面安装包缺失：%s.exe", best.SHA256)
		}
		return "", err
	}
	if !blobInfo.Mode().IsRegular() {
		return "", fmt.Errorf("桌面安装包不是普通文件：%s.exe", best.SHA256)
	}
	if blobInfo.Size() != best.SizeBytes {
		return "", fmt.Errorf("桌面安装包大小不符：记录 %d 字节，实际 %d 字节", best.SizeBytes, blobInfo.Size())
	}
	actual, err := digest(blobPath)
	if err != nil {
		return "", err
	}
	if actual != best.SHA256 {
		return "", fmt.Errorf("桌面安装包 SHA-256 不符：记录 %s，实际 %s", best.SHA256, actual)
	}
	return fmt.Sprintf("已发布 %s（sequence %d，%d 字节，摘要校验通过；真实客户端安装验收仍由桌面端单独执行）", best.Version, best.Sequence, best.SizeBytes), nil
}
