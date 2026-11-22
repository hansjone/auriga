package main

// 日志、收据与退出码（设计 D11）。
//
// 每次 deploy/rollback 写 <logs.dir>/<时间>-<命令>.log 与同名 .json 收据。日志目录要求 root
// 所有、组和其他用户不可写，写入前 noSymlinks、打开用 O_NOFOLLOW|O_EXCL。运行期间标准输出与
// 标准错误（含子进程）经脱敏后同时写终端与日志；--json 时终端进度改写到标准错误，标准输出
// 只打印收据。退出码：0 成功；1 未切换就失败（现场未受影响，首装失败也是 1）；2 切换失败且
// 已自动回滚；3 回滚失败或拒绝自动回滚，需要人工。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string { return e.err.Error() }
func (e *exitCodeError) Unwrap() error { return e.err }

// exitWith 给错误标上退出码；已经带退出码的错误保持原值。
func exitWith(code int, err error) error {
	if err == nil {
		return nil
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return err
	}
	return &exitCodeError{code: code, err: err}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return ec.code
	}
	return 1
}

type receiptImage struct {
	Service  string `json:"service"`
	Action   string `json:"action"` // built | unchanged | not-selected | service | rollback
	Reason   string `json:"reason,omitempty"`
	OldID    string `json:"oldId,omitempty"`
	NewID    string `json:"newId,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Assembly string `json:"assembly,omitempty"`
	Triggers string `json:"triggers,omitempty"` // 装配切换触发项说明
}

type receiptArtifact struct {
	Name   string `json:"name"`
	Action string `json:"action"` // built | reused | skipped
	Reason string `json:"reason,omitempty"`
	Digest string `json:"digest,omitempty"`
}

type receiptRelease struct {
	Seq      int    `json:"seq"`
	Dir      string `json:"dir,omitempty"`
	Previous int    `json:"previous,omitempty"`
}

type siteReceipt struct {
	Schema          int           `json:"schemaVersion"`
	Command         string        `json:"command"`
	Args            []string      `json:"args"`
	Config          string        `json:"config"`
	ConfigSHA256    string        `json:"configSha256"`
	StartedAt       string        `json:"startedAt"`
	FinishedAt      string        `json:"finishedAt"`
	DurationSeconds float64       `json:"durationSeconds"`
	ExitCode        int           `json:"exitCode"`
	Result          string        `json:"result"`
	Error           string        `json:"error,omitempty"`
	Phase           string        `json:"phase"` // build | switch | rollback
	DryRun          bool          `json:"dryRun,omitempty"`
	FirstInstall    bool          `json:"firstInstall,omitempty"`
	Source          *deploySource `json:"source,omitempty"`
	Deps            struct {
		Inputs  []resolvedInput   `json:"inputs"`
		Missing []string          `json:"missing"`
		Sources map[string]string `json:"sources,omitempty"`
		NewDeps string            `json:"newDeps"`
	} `json:"deps"`
	BuildImages          []resolvedImage   `json:"buildImages"`
	Artifacts            []receiptArtifact `json:"artifacts,omitempty"`
	Fingerprints         map[string]string `json:"fingerprints,omitempty"`
	Images               []receiptImage    `json:"images"`
	SwitchedServices     []string          `json:"switchedServices"`
	SwitchMode           string            `json:"switchMode,omitempty"`
	MixedVersions        string            `json:"mixedVersions"`
	OrchestrationChanged *bool             `json:"orchestrationChanged"`
	Orchestration        string            `json:"orchestration,omitempty"`
	Data                 struct {
		Before *dataVersions `json:"before"`
		After  *dataVersions `json:"after"`
	} `json:"data"`
	Health         string             `json:"health,omitempty"`
	Smoke          string             `json:"smoke,omitempty"`
	RolledBack     bool               `json:"rolledBack"`
	RollbackReason string             `json:"rollbackReason,omitempty"`
	Release        *receiptRelease    `json:"release,omitempty"`
	Pruned         []int              `json:"pruned,omitempty"`
	DBBackups      []receiptDBBackup  `json:"dbBackups,omitempty"`
	BuildCache     *receiptBuildCache `json:"buildCache,omitempty"`
	Notes          []string           `json:"notes"`
	Log            string             `json:"log"`
}

// receiptBuildCache 记录本次是否使用 Go 构建缓存与装配层缓存（设计 D14）。
// 只记路径与工具镜像引用，不记缓存内容。
type receiptBuildCache struct {
	GoUsed             bool   `json:"goUsed"`
	GoPath             string `json:"goPath,omitempty"`
	GoToolImage        string `json:"goToolImage,omitempty"`
	AssemblyLayerCache bool   `json:"assemblyLayerCache"`
}

// siteLogDirCheck 是可注入点（Windows 与非 root 单测替换属主校验）。
var siteLogDirCheck = func(dir string) error { return checkRootOwnedDir("日志目录 logs.dir ", dir) }

type siteRun struct {
	receipt     *siteReceipt
	out         io.Writer
	logPath     string
	receiptPath string
	asJSON      bool
	started     time.Time
	stop        func()
	terminal    *os.File
}

func logsDir(cfg *siteConfig) string {
	if cfg != nil && cfg.Logs != nil && cfg.Logs.Dir != "" {
		return cfg.Logs.Dir
	}
	return "/var/log/auriga"
}

// ensureLogDir 确认日志目录 root 所有、组和其他用户不可写；不存在时在受信父目录下创建。
func ensureLogDir(dir string) error {
	if err := noSymlinks(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		parent := filepath.Dir(dir)
		if err := os.MkdirAll(parent, 0755); err != nil {
			return err
		}
		if err := siteParentCheck(parent); err != nil {
			return fmt.Errorf("日志目录的父目录不受信：%w", err)
		}
		if err := os.Mkdir(dir, 0750); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return siteLogDirCheck(dir)
}

// startRun 打开日志与收据并开始捕获输出。
func startRun(cfg *siteConfig, cfgPath, cmd string, args []string, asJSON bool) (*siteRun, error) {
	dir := logsDir(cfg)
	if err := ensureLogDir(dir); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	base := strings.ReplaceAll(now.Format("20060102T150405.000Z"), ".", "") + "-" + cmd
	run := &siteRun{asJSON: asJSON, started: now}
	var logFile *os.File
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		run.logPath = filepath.Join(dir, name+".log")
		run.receiptPath = filepath.Join(dir, name+".json")
		if _, err := os.Lstat(run.receiptPath); err == nil {
			continue
		}
		f, err := openNoFollow(run.logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			logFile = f
			break
		}
		if !os.IsExist(err) || i > 20 {
			return nil, err
		}
	}
	sum := ""
	if raw, err := readSiteConfigFile(cfgPath); err == nil {
		sum = sha256Hex(raw)
	}
	run.receipt = &siteReceipt{Schema: 1, Command: cmd, Args: append([]string{}, args...), Config: cfgPath, ConfigSHA256: sum,
		StartedAt: now.Format(time.RFC3339), Phase: "build", Log: run.logPath,
		Images: []receiptImage{}, SwitchedServices: []string{}, BuildImages: []resolvedImage{}, Notes: []string{},
		MixedVersions: "无"}
	run.receipt.Deps.Inputs, run.receipt.Deps.Missing = []resolvedInput{}, []string{}
	run.receipt.Deps.NewDeps = "新增依赖清单见日志与缓存条目的 site-dependencies.json"
	run.terminal = os.Stdout
	if asJSON {
		run.terminal = os.Stderr
	}
	run.stop = captureOutput(logFile, run.terminal)
	run.out = os.Stdout
	return run, nil
}

// captureOutput 把 os.Stdout/os.Stderr 换成管道，脱敏后写日志与终端；返回的函数恢复并等待写完。
var captureOutput = func(logFile *os.File, terminal *os.File) func() {
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		return func() { _ = logFile.Close() }
	}
	os.Stdout, os.Stderr = w, w
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		copyRedacted(r, logFile, terminal)
	}()
	return func() {
		os.Stdout, os.Stderr = origOut, origErr
		_ = w.Close()
		wg.Wait()
		_ = r.Close()
		_ = logFile.Sync()
		_ = logFile.Close()
	}
}

// copyRedacted 逐行脱敏后写到各输出。
func copyRedacted(r io.Reader, outs ...io.Writer) {
	red := &redactor{}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if clean, keep := red.line(line); keep {
				for _, o := range outs {
					_, _ = io.WriteString(o, clean)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

var (
	// 口令/令牌/私钥字段名；另含 totp / otp_secret，避免应急 TOTP 种子进日志与收据。
	secretAssign = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:password|passwd|secret|token|private_key|privatekey|api_key|apikey|totp_secret|otp_secret|totp)[A-Za-z0-9_.-]*)("?\s*[=:]\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^\s,;"']+)`)
	bearer       = regexp.MustCompile(`(?i)(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	urlUserinfo  = regexp.MustCompile(`([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
	// 同行内完整 PEM（含 JSON 字符串里的 \n 转义）；只替换块本身，避免整行替换破坏收据 JSON。
	pemInline = regexp.MustCompile(`-----BEGIN [^-]*PRIVATE KEY-----[\s\\n]+.*?-----END [^-]*PRIVATE KEY-----`)
)

// redactor 去掉密钥材料：PEM 私钥块替换为标记；KEY=值 形式的口令、令牌、私钥、TOTP 种子替换为 ***；
// Authorization 凭据与 URL 内嵌口令同样替换。密钥文件路径不算密钥材料，但写成
// xxx_PASSWORD=路径 时也会被替换（宁可多遮）。access 文件内容若误进输出，其中的口令/TOTP 字段同样被遮。
type redactor struct{ inKey bool }

func (r *redactor) line(s string) (string, bool) {
	if r.inKey {
		if strings.Contains(s, "-----END") {
			r.inKey = false
		}
		return "", false
	}
	if strings.Contains(s, "-----BEGIN") && strings.Contains(s, "PRIVATE KEY-----") {
		if strings.Contains(s, "-----END") {
			s = pemInline.ReplaceAllString(s, "[已脱敏：私钥材料]")
		}
		// 同行替换后仍残留私钥头（不成对、头尾类型不一致、头后直接接正文等）：按旧规则整行替换，宁可多遮。
		if strings.Contains(s, "-----BEGIN") && strings.Contains(s, "PRIVATE KEY-----") {
			r.inKey = !strings.Contains(s, "-----END")
			return "[已脱敏：私钥材料]\n", true
		}
	}
	s = secretAssign.ReplaceAllString(s, "${1}${2}***")
	s = bearer.ReplaceAllString(s, "${1} ***")
	s = urlUserinfo.ReplaceAllString(s, "${1}***@")
	return s, true
}

func redactText(s string) string {
	var out strings.Builder
	red := &redactor{}
	for _, line := range strings.SplitAfter(s, "\n") {
		if clean, keep := red.line(line); keep {
			out.WriteString(clean)
		}
	}
	return out.String()
}

// secretKeyName：JSON 键名像口令/令牌/私钥/TOTP 种子时，其字符串值整体替换为 ***。
var secretKeyName = regexp.MustCompile(`(?i)(password|passwd|secret|token|private_?key|api_?key|totp|otp_secret)`)

// redactJSON 按 JSON 结构逐个字符串值脱敏后重新缩进输出：字段顺序不变、结果仍是合法 JSON。
// 不能对整段 JSON 文本做逐行脱敏：字符串里的换行被转义成 \n、引号被转义成 \"，
// 多行私钥与 password: "值" 这类内容会漏遮，私钥头不成对时还会吞掉后续行、破坏 JSON。
// 这里先解码出原始字符串再交给 redactText，键名敏感的字符串值直接替换为 ***。
func redactJSON(raw []byte) ([]byte, error) {
	type frame struct {
		object, wantKey bool
		n               int
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	var stack []frame
	secretValue := false
	sep := func() {
		if len(stack) == 0 {
			return
		}
		top := stack[len(stack)-1]
		if top.n > 0 && (!top.object || top.wantKey) {
			out.WriteByte(',')
		}
	}
	done := func() {
		secretValue = false
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			top.n++
			top.wantKey = top.object
		}
	}
	writeString := func(s string) error {
		enc, err := json.Marshal(s)
		out.Write(enc)
		return err
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := tok.(type) {
		case json.Delim:
			if v == '{' || v == '[' {
				sep()
				out.WriteByte(byte(v))
				secretValue = false
				stack = append(stack, frame{object: v == '{', wantKey: v == '{'})
				continue
			}
			out.WriteByte(byte(v))
			stack = stack[:len(stack)-1]
			done()
		case string:
			if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].wantKey {
				sep()
				if err := writeString(v); err != nil {
					return nil, err
				}
				out.WriteByte(':')
				stack[n-1].wantKey = false
				secretValue = secretKeyName.MatchString(v)
				continue
			}
			sep()
			clean := redactText(v)
			if secretValue && v != "" {
				clean = "***"
			}
			if err := writeString(clean); err != nil {
				return nil, err
			}
			done()
		default:
			sep()
			enc, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			out.Write(enc)
			done()
		}
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	return pretty.Bytes(), nil
}

// finish 写收据、恢复输出；--json 时把收据打印到标准输出。返回原错误（保留退出码）。
func (run *siteRun) finish(err error) error {
	rc := run.receipt
	end := time.Now().UTC()
	rc.FinishedAt = end.Format(time.RFC3339)
	rc.DurationSeconds = end.Sub(run.started).Seconds()
	rc.ExitCode = exitCodeOf(err)
	switch rc.ExitCode {
	case 0:
		rc.Result = "success"
	case 1:
		rc.Result = "failed"
	case 2:
		rc.Result = "rolled-back"
	default:
		rc.Result = "manual"
	}
	if err != nil {
		rc.Error = strings.TrimSpace(redactText(err.Error()))
		fmt.Fprintf(os.Stdout, "失败（退出码 %d）：%s\n", rc.ExitCode, rc.Error)
	}
	fmt.Fprintf(os.Stdout, "收据 %s\n", run.receiptPath)
	run.stop()
	raw, mErr := json.MarshalIndent(rc, "", "  ")
	if mErr == nil {
		raw, mErr = redactJSON(raw)
	}
	if mErr == nil {
		f, oErr := openNoFollow(run.receiptPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if oErr == nil {
			_, mErr = f.Write(append(raw, '\n'))
			if cErr := f.Close(); mErr == nil {
				mErr = cErr
			}
		} else {
			mErr = oErr
		}
	}
	if mErr != nil {
		fmt.Fprintf(os.Stderr, "警告：收据写入失败 %s：%v\n", run.receiptPath, mErr)
	}
	if run.asJSON && raw != nil {
		_, _ = os.Stdout.Write(append(raw, '\n'))
	}
	if err != nil {
		return &reportedError{exitWith(1, err)}
	}
	return nil
}

// reportedError：错误已写进日志与收据，main 只按退出码退出，不再重复打印。
type reportedError struct{ err error }

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }
