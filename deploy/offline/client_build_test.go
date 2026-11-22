package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clientTestConfig() *siteConfig {
	vk := `D:\approved\release-public.pem`
	rpk := `D:\site\desktop-public.pem`
	rtj := `D:\site\resource-roots.json`
	return &siteConfig{
		Client: &siteClient{
			Out:               `D:\out`,
			PlatformOrigin:    "https://auriga.example",
			VerifyKey:         &vk,
			ReleasePublicKey:  &rpk,
			ResourceTrustJSON: &rtj,
		},
		Deps: &siteDeps{Cache: `D:\cache`},
	}
}

var testManifestSHA = strings.Repeat("e", 64)

func TestMapClientBuildArgsRejectsMissingTrust(t *testing.T) {
	cfg := clientTestConfig()
	cfg.Client.VerifyKey = nil
	_, err := mapClientBuildArgs(cfg, "1.0.0", "stable", clientKit{Location: `D:\kit`, Source: "cache", DevkitSHA256: testManifestSHA}, 1)
	if err == nil || !strings.Contains(err.Error(), "verifyKey") {
		t.Fatalf("缺 verifyKey/trustFingerprint 应拒绝：%v", err)
	}
	cfg = clientTestConfig()
	cfg.Client.ReleasePublicKey = nil
	if _, err := mapClientBuildArgs(cfg, "1.0.0", "stable", clientKit{Location: `D:\kit`, Source: "cache", DevkitSHA256: testManifestSHA}, 1); err == nil || !strings.Contains(err.Error(), "releasePublicKey") {
		t.Fatalf("缺 releasePublicKey 应拒绝：%v", err)
	}
	if _, err := mapClientBuildArgs(clientTestConfig(), "1.0.0", "stable", clientKit{Location: `D:\kit`, Source: "cache"}, 1); err == nil {
		t.Fatal("缺记录的清单摘要应拒绝")
	}
	if _, err := mapClientBuildArgs(clientTestConfig(), "", "stable", clientKit{Location: `D:\kit`, Source: "cache", DevkitSHA256: testManifestSHA}, 1); err == nil {
		t.Fatal("缺 --version 应拒绝")
	}
}

func TestMapClientBuildArgsCacheKitUsesRecordedDigestAndSourceTrust(t *testing.T) {
	cfg := clientTestConfig()
	mapped, err := mapClientBuildArgs(cfg, "1.0.2-site.1", "stable", clientKit{Location: `D:\cache\inputs\client\x`, Source: "cache", DevkitSHA256: testManifestSHA}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !mapped.SiteKit || mapped.DevkitSHA256 != testManifestSHA {
		t.Fatalf("现场缓存套件应走 --site-kit 与记录的摘要：%+v", mapped)
	}
	if mapped.ToolsBaseline != `D:\cache` {
		t.Fatalf("应带上 deps.cache 作基线目录，得到 %q", mapped.ToolsBaseline)
	}
	joined := strings.Join(mapped.argv(), " ")
	for _, want := range []string{
		"build --offline", "--devkit " + `D:\cache\inputs\client\x`, "--out " + `D:\out`, "--version 1.0.2-site.1", "--sequence 3",
		"--platform-origin https://auriga.example", "--release-public-key " + `D:\site\desktop-public.pem`,
		"--resource-trust-json " + `D:\site\resource-roots.json`, "--verify-key " + `D:\approved\release-public.pem`,
		"--site-kit --devkit-sha256 " + testManifestSHA,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv 缺 %q：%s", want, joined)
		}
	}
	// trustFingerprint 去掉 SHA256: 前缀（cli.mjs 只接受 64 位十六进制）。
	cfg.Client.VerifyKey = nil
	fp := "SHA256:" + strings.Repeat("a", 64)
	cfg.Client.TrustFingerprint = &fp
	mapped, err = mapClientBuildArgs(cfg, "1.0.2", "beta", clientKit{Location: `D:\k`, Source: "cache", DevkitSHA256: testManifestSHA}, 4)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(mapped.argv(), " ")
	if !strings.Contains(joined, "--trust-fingerprint "+strings.Repeat("a", 64)+" ") || strings.Contains(joined, "SHA256:") || !strings.Contains(joined, "--channel beta") {
		t.Fatalf("指纹/渠道映射错误：%s", joined)
	}
}

func TestMapClientBuildArgsSignedKit(t *testing.T) {
	mapped, err := mapClientBuildArgs(clientTestConfig(), "1.0.0", "stable", clientKit{Location: `D:\kits\a`, Source: "kit", Signed: true, DevkitSHA256: testManifestSHA}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.SiteKit || strings.Contains(strings.Join(mapped.argv(), " "), "--devkit-sha256") {
		t.Fatalf("发行签名套件不走 --site-kit：%+v", mapped)
	}
	if _, err := mapClientBuildArgs(clientTestConfig(), "1.0.0", "stable", clientKit{Location: `D:\kits\a`, Source: "kit", DevkitSHA256: testManifestSHA}, 2); err == nil {
		t.Fatal("未验签的 deps.kits 套件应拒绝")
	}
}

func TestVerifyClientBuildNode(t *testing.T) {
	dir := t.TempDir()
	node := []byte("fake node.exe")
	if err := os.MkdirAll(filepath.Join(dir, "build-node"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-node", "node.exe"), node, 0755); err != nil {
		t.Fatal(err)
	}
	nodeSum := sha256.Sum256(node)
	manifest := []byte(fmt.Sprintf(`{"files":[{"path":"build-node/node.exe","sha256":"%s"}]}`, hex.EncodeToString(nodeSum[:])))
	if err := os.WriteFile(filepath.Join(dir, "windows-client.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(manifest)
	recorded := hex.EncodeToString(sum[:])
	if _, err := verifyClientBuildNode(dir, recorded); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyClientBuildNode(dir, testManifestSHA); err == nil || !strings.Contains(err.Error(), "清单摘要") {
		t.Fatalf("清单与记录不符应拒绝：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-node", "node.exe"), []byte("system node"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyClientBuildNode(dir, recorded); err == nil || !strings.Contains(err.Error(), "构建 Node") {
		t.Fatalf("构建 Node 被替换应拒绝：%v", err)
	}
}

func TestClientBuildEnvOverridesInherited(t *testing.T) {
	t.Setenv("AURIGA_CLIENT_TOOLS_BASELINE", `C:\evil`)
	t.Setenv("NODE_OPTIONS", "--require evil.js")
	env := strings.Join(clientBuildEnv(clientOfflineBuildArgs{Devkit: `D:\kit`, ToolsBaseline: `D:\cache`}), "\n")
	if strings.Contains(env, `C:\evil`) || strings.Contains(env, "evil.js") {
		t.Fatalf("继承的基线/NODE_OPTIONS 应被丢弃：%s", env)
	}
	if !strings.Contains(env, `AURIGA_CLIENT_TOOLS_BASELINE=D:\cache`) || !strings.Contains(env, `AURIGA_CLIENT_DEVKIT=D:\kit`) {
		t.Fatal("应设置配置中的套件与基线目录")
	}
}

func TestFetchDesktopLatestSequence(t *testing.T) {
	latest := map[string]string{}
	status := map[string]int{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect/api/resource/v1/desktop/latest" {
			http.Redirect(w, r, "http://evil.example/x", http.StatusFound)
			return
		}
		if r.URL.Path != desktopLatestPath {
			http.NotFound(w, r)
			return
		}
		ch := r.URL.Query().Get("channel")
		if code := status[ch]; code != 0 {
			w.WriteHeader(code)
			return
		}
		body, ok := latest[ch]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	old := desktopTransport
	desktopTransport = srv.Client().Transport
	t.Cleanup(func() { desktopTransport = old })
	ctx := context.Background()

	if n, err := fetchDesktopLatestSequence(ctx, srv.URL); err != nil || n != 1 {
		t.Fatalf("各渠道 404 应从 1 起：%d %v", n, err)
	}
	latest["stable"] = `{"sequence":5,"version":"1.0.0"}`
	latest["beta"] = `{"sequence":7}`
	if n, err := fetchDesktopLatestSequence(ctx, srv.URL); err != nil || n != 8 {
		t.Fatalf("应取各渠道最大序号 +1：%d %v", n, err)
	}
	for _, body := range []string{`{"sequence":0}`, `{"sequence":1.5}`, `{"sequence":"x"}`, `not json`, `{}`} {
		latest["beta"] = body
		if _, err := fetchDesktopLatestSequence(ctx, srv.URL); err == nil {
			t.Errorf("无效响应 %s 应失败", body)
		}
	}
	latest["beta"] = `{"sequence":7}`
	status["beta"] = http.StatusInternalServerError
	if _, err := fetchDesktopLatestSequence(ctx, srv.URL); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("非 404 错误必须失败：%v", err)
	}
	status["beta"] = 0
	if _, err := fetchDesktopLatestSequence(ctx, srv.URL+"/redirect"); err == nil || !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("重定向到非 https/他源应失败：%v", err)
	}
	if _, err := fetchDesktopLatestSequence(ctx, strings.Replace(srv.URL, "https://", "http://", 1)); err == nil {
		t.Fatal("http 源应拒绝")
	}
}

func stubClientDeps(t *testing.T) (*[]string, *bool) {
	t.Helper()
	oldRunner, oldGuard, oldTrusted := clientNodeRunner, clientCacheGuard, depsTrustedSourceCheck
	t.Cleanup(func() { clientNodeRunner, clientCacheGuard, depsTrustedSourceCheck = oldRunner, oldGuard, oldTrusted })
	var got []string
	var wrote bool
	clientNodeRunner = func(_ context.Context, args []string, _ io.Writer) error {
		got = append([]string{}, args...)
		return nil
	}
	clientCacheGuard = func(_ *siteConfig, write bool) (func(), error) {
		wrote = write
		return func() {}, nil
	}
	depsTrustedSourceCheck = func(string, string) error { return nil }
	return &got, &wrote
}

func clientSourceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rel := filepath.Join(root, "scripts", "client-release")
	if err := os.MkdirAll(rel, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "site-deps.mjs"), []byte("// stub\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestClientDepsArgvMapping(t *testing.T) {
	got, wrote := stubClientDeps(t)
	root := clientSourceRoot(t)
	files := "file:///D:/files"
	npm := "https://npm.internal.example"
	ca := `D:\site\ca.pem`
	cfg := &siteConfig{
		Deps: &siteDeps{
			Cache:       `D:\cache`,
			AllowPublic: true,
			CAFile:      &ca,
			Kits: []siteKit{
				{Path: `D:\kit`, TrustFingerprint: "SHA256:" + strings.Repeat("a", 64)},
				{Path: `D:\state-kit`, TrustState: true},
			},
			Sources: siteSources{Files: &files, NPM: &npm},
		},
	}
	if err := runClientDeps(context.Background(), cfg, root, clientDepsRequest{Action: "status", JSON: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*got, " ")
	for _, want := range []string{"--kit " + `D:\kit` + " --kit-fingerprint " + strings.Repeat("a", 64), "--files " + files, "--npm " + npm, "--ca-file " + ca, "--allow-public", "--json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("应传 %q：%v", want, *got)
		}
	}
	if strings.Contains(joined, "state-kit") {
		t.Fatalf("trustState 套件不能传给客户端（无 state，避免自证）：%v", *got)
	}
	if *wrote {
		t.Fatal("status 不应加写锁")
	}
	if err := runClientDeps(context.Background(), cfg, root, clientDepsRequest{Action: "fetch"}, io.Discard); err != nil || !*wrote {
		t.Fatalf("fetch 应在写锁内运行：%v", err)
	}
	if err := runClientDeps(context.Background(), cfg, root, clientDepsRequest{Action: "import", From: `D:\x`, TrustState: true}, io.Discard); err == nil || !strings.Contains(err.Error(), "--trust-fingerprint") {
		t.Fatalf("客户端 --trust-state 应拒绝：%v", err)
	}
}

func TestClientDepsImportChecksSource(t *testing.T) {
	got, _ := stubClientDeps(t)
	root := clientSourceRoot(t)
	cfg := &siteConfig{Deps: &siteDeps{Cache: filepath.Join(t.TempDir(), "cache")}}
	from := t.TempDir()
	var checked string
	depsTrustedSourceCheck = func(_ string, p string) error { checked = p; return errors.New("普通用户可写") }
	err := runClientDeps(context.Background(), cfg, root, clientDepsRequest{Action: "import", From: from}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "普通用户可写") || checked != from {
		t.Fatalf("导入源须做受信校验：%v（校验 %q）", err, checked)
	}
	depsTrustedSourceCheck = func(string, string) error { return nil }
	fp := "SHA256:" + strings.Repeat("b", 64)
	if err := runClientDeps(context.Background(), cfg, root, clientDepsRequest{Action: "import", From: from, Fingerprint: fp}, io.Discard); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*got, " ")
	if !strings.Contains(joined, "--from "+from) || !strings.Contains(joined, "--trust-fingerprint "+strings.Repeat("b", 64)) {
		t.Fatalf("import 参数映射错误：%v", *got)
	}
	cfg.Deps.Cache = from
	if err := runClientDeps(context.Background(), cfg, root, clientDepsRequest{Action: "import", From: filepath.Join(from, "sub")}, io.Discard); err == nil {
		t.Fatal("不能从缓存目录内导入")
	}
}
