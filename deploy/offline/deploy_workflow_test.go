package main

// 第 7 步：sudoers 静态检查、示例工作流可解析、含敏感值的收据脱敏。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestSudoersFragmentStatic(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "site", "sudoers", "auriga-runner"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.TrimSpace(text) == "" {
		t.Fatal("sudoers 片段为空")
	}
	if !strings.Contains(text, "server.runner") {
		t.Fatal("须注明先登记 site.json 的 server.runner")
	}
	if !strings.Contains(text, "env_reset") || !strings.Contains(text, "secure_path=") {
		t.Fatal("Defaults 须含 env_reset 与 secure_path")
	}
	// 去掉注释后再核对授权行与禁词，避免说明文字干扰。
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		lines = append(lines, trim)
	}
	body := strings.Join(lines, "\n")
	upper := strings.ToUpper(body)
	for _, bad := range []string{"SETENV", "NOPASSWD: ALL", "NOPASSWD:ALL"} {
		if strings.Contains(upper, bad) {
			t.Fatalf("生效行禁止出现 %s", bad)
		}
	}
	wantCmds := []string{
		"/usr/local/bin/auriga deploy *",
		"/usr/local/bin/auriga rollback *",
		"/usr/local/bin/auriga status",
		"/usr/local/bin/auriga history",
	}
	for _, cmd := range wantCmds {
		if !strings.Contains(body, cmd) {
			t.Fatalf("缺少放行命令 %q", cmd)
		}
	}
	// 授权行里不得出现其它可执行路径（Defaults 的 secure_path 除外）。
	cmdLine := regexp.MustCompile(`(?m)^[A-Za-z_][A-Za-z0-9_-]*\s+ALL=`)
	binPath := regexp.MustCompile(`/[A-Za-z0-9._/-]+`)
	for _, line := range lines {
		if strings.HasPrefix(line, "Defaults") {
			continue
		}
		if !cmdLine.MatchString(line) {
			t.Fatalf("非 Defaults 行无法识别为授权：%s", line)
		}
		for _, p := range binPath.FindAllString(line, -1) {
			switch p {
			case "/usr/local/bin/auriga":
				continue
			default:
				t.Fatalf("授权行出现未预期路径 %q：%s", p, line)
			}
		}
		if !strings.Contains(line, "NOPASSWD:") {
			t.Fatalf("授权行须 NOPASSWD：%s", line)
		}
	}
	if visudo, err := exec.LookPath("visudo"); err == nil {
		cmd := exec.Command(visudo, "-cf", filepath.Join(repoRoot(t), "deploy", "site", "sudoers", "auriga-runner"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("visudo -cf 失败：%v\n%s", err, out)
		}
	} else {
		t.Log("本机无 visudo，跳过 visudo -cf（Linux CI / WSL 有则跑）")
	}
}

func TestWorkflowYAMLParse(t *testing.T) {
	root := repoRoot(t)
	files := []string{
		filepath.Join(root, "deploy", "site", "workflows", "site-deploy.yml"),
		filepath.Join(root, "deploy", "site", "workflows", "site-rollback.yml"),
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if err := assertWorkflowShape(filepath.Base(path), text); err != nil {
			t.Fatal(err)
		}
		// 优先用本机 python3 + PyYAML；没有则靠上面的结构断言。
		if py, err := exec.LookPath("python3"); err == nil {
			cmd := exec.Command(py, "-c", "import sys,yaml; yaml.safe_load(open(sys.argv[1],encoding='utf-8')); print('ok')", path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				if strings.Contains(string(out), "No module named") || strings.Contains(string(out), "ModuleNotFoundError") {
					t.Logf("%s：无 PyYAML，跳过 yaml.safe_load（结构断言已通过）", filepath.Base(path))
					continue
				}
				t.Fatalf("%s yaml.safe_load 失败：%v\n%s", path, err, out)
			}
		}
	}
	if _, err := exec.LookPath("actionlint"); err == nil {
		cmd := exec.Command("actionlint", files...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("actionlint 失败：%v\n%s", err, out)
		}
	} else {
		t.Log("本机无 actionlint，跳过（不安装）")
	}
}

func assertWorkflowShape(name, text string) error {
	need := []string{"name:", "on:", "workflow_dispatch:", "concurrency:", "jobs:", "runs-on:", "timeout-minutes:",
		"sudo -n /usr/local/bin/auriga", "set -euo pipefail", "if: always()", "shell: bash"}
	for _, s := range need {
		if !strings.Contains(text, s) {
			return fmt.Errorf("%s 缺少 %q", name, s)
		}
	}
	// run 脚本块里不得出现任何 ${{ 表达式（连注释也会被展开）；输入只能经 env 传入。
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		trim := strings.TrimLeft(lines[i], " ")
		if !strings.HasPrefix(trim, "run:") {
			continue
		}
		indent := len(lines[i]) - len(trim)
		if strings.Contains(trim, "${{") {
			return fmt.Errorf("%s 第 %d 行 run 里出现表达式", name, i+1)
		}
		for j := i + 1; j < len(lines); j++ {
			body := strings.TrimLeft(lines[j], " ")
			if body != "" && len(lines[j])-len(body) <= indent {
				break
			}
			if strings.Contains(lines[j], "${{") {
				return fmt.Errorf("%s 第 %d 行：run 脚本里不得出现 ${{ }}，输入须经 env 传入", name, j+1)
			}
		}
	}
	for _, bad := range []string{"${{ github.event.", "${{github.event."} {
		if strings.Contains(text, bad) {
			return fmt.Errorf("%s 不应引用 github.event 上下文", name)
		}
	}
	switch name {
	case "site-deploy.yml":
		for _, s := range []string{"deploy", "--json", "receipt.json", "退出码 0", "退出码 1", "退出码 2", "退出码 3", "[0-9a-f]{40}"} {
			if !strings.Contains(text, s) {
				return fmt.Errorf("%s 缺少 %q", name, s)
			}
		}
	case "site-rollback.yml":
		for _, s := range []string{"rollback", "--json", "receipt.json", "--to", "退出码 0"} {
			if !strings.Contains(text, s) {
				return fmt.Errorf("%s 缺少 %q", name, s)
			}
		}
	}
	return nil
}

// TestReceiptJSONOmitsSiteSecrets：构造含敏感值的现场材料，断言收据 JSON / 日志不含这些值。
func TestReceiptJSONOmitsSiteSecrets(t *testing.T) {
	dir := t.TempDir()
	old := siteLogDirCheck
	siteLogDirCheck = func(string) error { return nil }
	t.Cleanup(func() { siteLogDirCheck = old })

	privPEM := "-----BEGIN PRIVATE KEY-----\nSITEPRIVKEYMATERIAL99\n-----END PRIVATE KEY-----"
	password := "site-pg-password-LEAKME"
	token := "site-bearer-token-LEAKME99"
	totpSeed := "JBSWY3DPEHPK3PXP"
	accessBody := `{"adminPassword":"access-file-password-LEAK","totpSeed":"` + totpSeed + `"}`
	accessPath := filepath.Join(dir, "access.json")
	if err := os.WriteFile(accessPath, []byte(accessBody), 0600); err != nil {
		t.Fatal(err)
	}

	// site.json 禁止依赖源内嵌凭据（复核；路径用 Unix 形式，与既有配置单测一致）。
	bad := `{"schemaVersion":1,"deps":{"cache":"/srv/auriga/cache","sources":{"go":["https://user:pass@proxy.example/go"]}},"server":{"data":"/srv/auriga/data","runner":"auriga-runner"}}`
	if _, err := parseSiteConfig([]byte(bad)); err == nil || !strings.Contains(err.Error(), "凭据") && !strings.Contains(err.Error(), "用户名或口令") {
		t.Fatalf("依赖源内嵌凭据应被拒绝：%v", err)
	}

	cfgPath := filepath.Join(dir, "site.json")
	// Logs.Dir 用已存在的临时目录，避免 Windows 上 ensureLogDir 走 stat 校验父目录。
	cfg := &siteConfig{
		Logs:   &siteLogs{Dir: dir},
		Server: &siteServer{Data: filepath.Join(dir, "data"), Runner: strPtr("auriga-runner")},
		Sign:   &siteSign{KeyFile: "/etc/auriga/site-sign.key"},
	}
	run, err := startRun(cfg, cfgPath, "deploy", []string{"--json"}, true)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("AURIGA_PG_PASSWORD=%s\n", password)
	fmt.Printf("Authorization: Bearer %s\n", token)
	fmt.Println(privPEM)
	fmt.Printf("TOTP_SECRET=%s\n", totpSeed)
	fmt.Printf("access 文件内容误打印：%s\n", accessBody)

	run.receipt.Notes = append(run.receipt.Notes,
		"误带私钥 "+privPEM,
		"口令字段 password="+password,
		"令牌 token="+token,
		"totpSeed="+totpSeed,
		"accessPath="+accessPath,
		`脚本输出 password: "quoted-pass-LEAK"`,
		"截断的私钥 -----BEGIN OPENSSH PRIVATE KEY-----\nTRUNCATEDKEYLEAK",
	)
	run.receipt.Deps.Sources = map[string]string{
		"go":   "https://goproxy.example",
		"web":  "https://npm.example",
		"java": "https://maven.example",
	}
	errMsg := fmt.Sprintf("切换失败 password=%s token=%s totp_secret=%s", password, token, totpSeed)
	_ = run.finish(exitWith(2, errors.New(errMsg)))

	rcRaw, err := os.ReadFile(run.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	logRaw, _ := os.ReadFile(run.logPath)
	secrets := []string{
		password, token, totpSeed, "SITEPRIVKEYMATERIAL99",
		"access-file-password-LEAK", "user:pass@", "quoted-pass-LEAK", "TRUNCATEDKEYLEAK",
	}
	for _, secret := range secrets {
		if strings.Contains(string(rcRaw), secret) {
			t.Fatalf("收据 JSON 泄露 %q：\n%s", secret, rcRaw)
		}
		if strings.Contains(string(logRaw), secret) {
			t.Fatalf("日志泄露 %q：\n%s", secret, logRaw)
		}
	}
	var rc map[string]any
	if err := json.Unmarshal(rcRaw, &rc); err != nil {
		t.Fatalf("收据不是合法 JSON：%v\n%s", err, rcRaw)
	}
	if rc["exitCode"].(float64) != 2 {
		t.Fatalf("退出码：%v", rc["exitCode"])
	}
	// 私钥头不成对时不得吞掉后续字段（旧的逐行脱敏会一直丢行到 -----END，破坏收据）。
	if rc["finishedAt"] == nil || rc["result"] == nil {
		t.Fatalf("收据字段缺失：\n%s", rcRaw)
	}

	// history --json 只含发行元数据路径，不含 access 文件内容或私钥材料。
	s := state{
		Data:    filepath.Join(dir, "data"),
		Access:  accessPath,
		Source:  sourceLocalDeploy,
		Current: 1,
		History: []deployRecord{{
			Seq: 1, Status: recordOK, At: "2026-01-01T00:00:00Z",
			Source:      deploySource{Mode: "git", Commit: strings.Repeat("a", 40), SnapshotSHA256: strings.Repeat("b", 64)},
			Release:     filepath.Join(dir, "releases", "1"),
			ReleaseSums: strings.Repeat("c", 64),
			Images:      map[string]string{"edge": "sha256:" + strings.Repeat("d", 64)},
		}},
	}
	raw, err := historyJSON(s)
	if err != nil {
		t.Fatal(err)
	}
	hist := string(raw)
	for _, secret := range []string{password, token, totpSeed, "SITEPRIVKEYMATERIAL99", "access-file-password-LEAK", accessBody} {
		if strings.Contains(hist, secret) {
			t.Fatalf("history JSON 泄露 %q：\n%s", secret, hist)
		}
	}
	out := redactText("status 误读 access：" + accessBody + "\npassword=" + password + "\n")
	for _, secret := range []string{"access-file-password-LEAK", password, totpSeed} {
		if strings.Contains(out, secret) {
			t.Fatalf("status 相关输出脱敏失败 %q：%s", secret, out)
		}
	}
}

// redactJSON：按字符串值脱敏，保持合法 JSON 与字段顺序；转义引号、多行私钥、敏感键名都要遮住。
func TestRedactJSONStructure(t *testing.T) {
	in := struct {
		Z       string   `json:"z"`
		Notes   []string `json:"notes"`
		Token   string   `json:"apiToken"`
		Passed  bool     `json:"passed"`
		Count   int      `json:"count"`
		Trailer string   `json:"trailer"`
	}{
		Z: "first",
		Notes: []string{
			`password: "esc-quoted-LEAK"`,
			"-----BEGIN RSA PRIVATE KEY-----\nMULTILINEKEYLEAK\n-----END RSA PRIVATE KEY-----\n后文保留",
			"-----BEGIN PRIVATE KEY-----NOSPACEKEYLEAK-----END PRIVATE KEY-----",
			"开头 -----BEGIN EC PRIVATE KEY-----\nUNPAIREDLEAK",
			"https://u:urlpass-LEAK@host/x",
		},
		Token:   "opaque-LEAK",
		Passed:  true,
		Count:   3,
		Trailer: "<tail>&",
	}
	raw, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out, err := redactJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, leak := range []string{"esc-quoted-LEAK", "MULTILINEKEYLEAK", "NOSPACEKEYLEAK", "UNPAIREDLEAK", "urlpass-LEAK", "opaque-LEAK"} {
		if strings.Contains(text, leak) {
			t.Fatalf("泄露 %q：\n%s", leak, text)
		}
	}
	var back map[string]any
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("脱敏后不是合法 JSON：%v\n%s", err, text)
	}
	if back["trailer"] != "<tail>&" || back["passed"] != true || back["count"].(float64) != 3 || back["apiToken"] != "***" {
		t.Fatalf("非敏感字段被改动：\n%s", text)
	}
	if !strings.Contains(back["notes"].([]any)[1].(string), "后文保留") {
		t.Fatalf("私钥块之后的正文不应丢失：\n%s", text)
	}
	if strings.Index(text, `"z"`) > strings.Index(text, `"trailer"`) {
		t.Fatalf("字段顺序被打乱：\n%s", text)
	}
}

func TestHelpMentionsWorkflowArtifacts(t *testing.T) {
	var buf strings.Builder
	printTopHelp(&buf)
	out := buf.String()
	for _, s := range []string{"deploy/site/sudoers/auriga-runner", "deploy/site/workflows/"} {
		if !strings.Contains(out, s) {
			t.Fatalf("根帮助应指向 %s", s)
		}
	}
	_ = runtime.GOOS
}
