package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const runtimeExpectedManifestName = "runtime-expected.json"

// runtimeExpectedManifest 随离线包与 local-deploy 发行目录写入发行根，
// 独立于 offline-release.json / release.json 以保持对旧版 CLI 严格解码的兼容。
type runtimeExpectedManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	DSHVersion    string `json:"dshVersion,omitempty"`
	PluginVersion string `json:"pluginVersion,omitempty"`
}

// runtimeActivation 对应 <data>/config/runtime-source/active.json（由 auriga-nas-bootstrap 维护）。
type runtimeActivation struct {
	Current  string `json:"Current"`
	Previous string `json:"Previous"`
	Pending  bool   `json:"Pending"`
}

// activeRuntimeInfo 描述站点当前生效（或待提交）的 DSH 运行时信息。
type activeRuntimeInfo struct {
	Configured      bool
	SourceKind      string // "runtime-source" | "base-materials"
	Slot            string
	Pending         bool
	PreviousSlot    string
	SlotDir         string
	Image           string
	ImageDigest     string
	DSHVersion      string
	PluginVersion   string
	ReleaseSequence uint64
	ParseError      error
}

// expectedRuntimeVersions 描述当前源码/发行包中期望的 DSH 与插件版本。
type expectedRuntimeVersions struct {
	DSHVersion    string
	PluginVersion string
	SourceDesc    string
}

// runtimeImageMetadata 描述从本地 Docker 镜像读取的轻量元数据。
type runtimeImageMetadata struct {
	DSHVersion    string `json:"dshVersion"`
	PluginVersion string `json:"pluginVersion"`
}

// runtimeConsistencyReport 汇总一次 doctor/status 运行时检查结果。
type runtimeConsistencyReport struct {
	PinnedImage  string
	PinnedSource string // "state.json" | "stack.env" | ""
	Active       activeRuntimeInfo
	Expected     expectedRuntimeVersions
	Status       string // "SKIP" | "PASS" | "INFO" | "WARN"
	Warnings     []string
	Notes        []string
}

// 测试可注入点。
var (
	inspectRuntimeImageMetadataFunc = inspectRuntimeImageMetadata
	runtimeExpectedSourceDirsFunc   = defaultRuntimeExpectedSourceDirs
)

func readSafeSmallFile(path string, limit int64) ([]byte, error) {
	if err := noSymlinks(path); err != nil {
		return nil, err
	}
	return smallFile(path, limit)
}

// readSourceRuntimeVersions 从源码目录读取预期 DSH 与 auriga-dsh 插件版本（带 noSymlinks 检查）。
func readSourceRuntimeVersions(dir string) (dshVersion, pluginVersion string, sources []string) {
	if dir == "" {
		return "", "", nil
	}
	vPath := filepath.Join(dir, "deploy", "compose", "real-dsh", "versions.json")
	if raw, err := readSafeSmallFile(vPath, 1<<20); err == nil {
		var v struct {
			DSHVersion string `json:"dshVersion"`
		}
		if json.Unmarshal(raw, &v) == nil && strings.TrimSpace(v.DSHVersion) != "" {
			dshVersion = strings.TrimSpace(v.DSHVersion)
			sources = append(sources, vPath)
		}
	}
	pPath := filepath.Join(dir, "clients", "dsh-plugin", "package.json")
	if raw, err := readSafeSmallFile(pPath, 1<<20); err == nil {
		var p struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &p) == nil && strings.TrimSpace(p.Version) != "" {
			pluginVersion = strings.TrimSpace(p.Version)
			sources = append(sources, pPath)
		}
	}
	return dshVersion, pluginVersion, sources
}

// writeRuntimeExpectedManifest 从源码目录提取预期 DSH/插件版本并写入发行目录的 runtime-expected.json。
// 若源码目录不含版本清单（如最小测试 fixture），静默跳过不报错。
func writeRuntimeExpectedManifest(sourceDir, targetDir string) error {
	dshVer, pluginVer, _ := readSourceRuntimeVersions(sourceDir)
	if dshVer == "" && pluginVer == "" {
		return nil
	}
	m := runtimeExpectedManifest{
		SchemaVersion: 1,
		DSHVersion:    dshVer,
		PluginVersion: pluginVer,
	}
	return jsonFile(filepath.Join(targetDir, runtimeExpectedManifestName), m, 0644)
}

type parsedDshRuntime struct {
	ArtifactID      string
	Version         string
	Image           string
	DSHVersion      string
	ImageDigest     string
	ReleaseSequence uint64
}

func (p *parsedDshRuntime) merge(other parsedDshRuntime) {
	if p.ArtifactID == "" {
		p.ArtifactID = other.ArtifactID
	}
	if p.Version == "" {
		p.Version = other.Version
	}
	if p.Image == "" {
		p.Image = other.Image
	}
	if p.DSHVersion == "" {
		p.DSHVersion = other.DSHVersion
	}
	if p.ImageDigest == "" {
		p.ImageDigest = other.ImageDigest
	}
	if p.ReleaseSequence == 0 {
		p.ReleaseSequence = other.ReleaseSequence
	}
}

func (p parsedDshRuntime) effectiveDSHVersion() string {
	if p.DSHVersion != "" {
		return p.DSHVersion
	}
	return p.Version
}

// consumeProtoField 从 protobuf 二进制流中读取下一个字段（纯标准库实现，不引入额外依赖）。
func consumeProtoField(b []byte) (fieldNum uint64, wireType uint64, varintVal uint64, bytesVal []byte, rest []byte, err error) {
	tag, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, 0, nil, nil, errors.New("protobuf tag 无效或截断")
	}
	b = b[n:]
	fieldNum = tag >> 3
	wireType = tag & 0x7
	if fieldNum == 0 {
		return 0, 0, 0, nil, nil, errors.New("protobuf 字段号为 0")
	}
	switch wireType {
	case 0: // varint
		v, m := binary.Uvarint(b)
		if m <= 0 {
			return 0, 0, 0, nil, nil, errors.New("protobuf varint 截断")
		}
		return fieldNum, wireType, v, nil, b[m:], nil
	case 1: // 64-bit
		if len(b) < 8 {
			return 0, 0, 0, nil, nil, errors.New("protobuf fixed64 截断")
		}
		return fieldNum, wireType, 0, b[:8], b[8:], nil
	case 2: // length-delimited
		l, m := binary.Uvarint(b)
		if m <= 0 || l > uint64(len(b)-m) {
			return 0, 0, 0, nil, nil, errors.New("protobuf length-delimited 截断")
		}
		b = b[m:]
		return fieldNum, wireType, 0, b[:l], b[l:], nil
	case 5: // 32-bit
		if len(b) < 4 {
			return 0, 0, 0, nil, nil, errors.New("protobuf fixed32 截断")
		}
		return fieldNum, wireType, 0, b[:4], b[4:], nil
	default:
		return 0, 0, 0, nil, nil, fmt.Errorf("不支持的 protobuf wireType %d", wireType)
	}
}

// parseRuntimeReleaseEnvelopeProto 解析 RuntimeReleaseEnvelope 关键字段：
// field 4: artifact_id, field 5: dsh_version, field 9: image_digest, field 10: release_sequence。
func parseRuntimeReleaseEnvelopeProto(raw []byte) (parsedDshRuntime, error) {
	var out parsedDshRuntime
	for len(raw) > 0 {
		num, wt, v, b, rest, err := consumeProtoField(raw)
		if err != nil {
			return out, err
		}
		raw = rest
		switch {
		case num == 4 && wt == 2:
			out.ArtifactID = string(b)
		case num == 5 && wt == 2:
			out.DSHVersion = string(b)
		case num == 9 && wt == 2:
			out.ImageDigest = string(b)
		case num == 10 && wt == 0:
			out.ReleaseSequence = v
		}
	}
	return out, nil
}

// parseDshRuntimeProto 解析 DshRuntime（release/runtime.pb）：
// field 1: artifact_id, field 2: version, field 7: image, field 9: release_envelope。
func parseDshRuntimeProto(raw []byte) (parsedDshRuntime, error) {
	var out parsedDshRuntime
	for len(raw) > 0 {
		num, wt, _, b, rest, err := consumeProtoField(raw)
		if err != nil {
			return out, err
		}
		raw = rest
		switch {
		case num == 1 && wt == 2:
			out.ArtifactID = string(b)
		case num == 2 && wt == 2:
			out.Version = string(b)
		case num == 7 && wt == 2:
			out.Image = string(b)
		case num == 9 && wt == 2:
			env, err := parseRuntimeReleaseEnvelopeProto(b)
			if err != nil {
				return out, err
			}
			out.merge(env)
		}
	}
	return out, nil
}

// parseInstanceSpecDshProto 解析 InstanceSpec（templates/real-dsh.pb）中的 field 6 (dsh)。
func parseInstanceSpecDshProto(raw []byte) (parsedDshRuntime, error) {
	var out parsedDshRuntime
	for len(raw) > 0 {
		num, wt, _, b, rest, err := consumeProtoField(raw)
		if err != nil {
			return out, err
		}
		raw = rest
		if num == 6 && wt == 2 {
			dsh, err := parseDshRuntimeProto(b)
			if err != nil {
				return out, err
			}
			out.merge(dsh)
		}
	}
	return out, nil
}

func validRuntimeSlotName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`) && !strings.Contains(name, "..")
}

func readRuntimeSlotMetadata(slotDir string, info *activeRuntimeInfo) {
	var parsed parsedDshRuntime
	if raw, err := readSafeSmallFile(filepath.Join(slotDir, "release", "runtime.pb"), 1<<20); err == nil {
		if d, err := parseDshRuntimeProto(raw); err == nil {
			parsed.merge(d)
		}
	}
	if raw, err := readSafeSmallFile(filepath.Join(slotDir, "templates", "real-dsh.pb"), 1<<20); err == nil {
		if d, err := parseInstanceSpecDshProto(raw); err == nil {
			parsed.merge(d)
		}
	}
	info.Image = parsed.Image
	info.ImageDigest = parsed.ImageDigest
	info.DSHVersion = parsed.effectiveDSHVersion()
	info.ReleaseSequence = parsed.ReleaseSequence
	if info.ImageDigest == "" && strings.Contains(info.Image, "@sha256:") {
		_, digestPart, _ := strings.Cut(info.Image, "@")
		info.ImageDigest = digestPart
	}
	if info.ReleaseSequence == 0 {
		var receipt struct {
			Sequence uint64 `json:"Sequence"`
		}
		if raw, err := readSafeSmallFile(filepath.Join(slotDir, "upgrade.receipt"), 65536); err == nil {
			if json.Unmarshal(raw, &receipt) == nil && receipt.Sequence > 0 {
				info.ReleaseSequence = receipt.Sequence
			}
		}
	}
	// 从 release/runtime-proofs/<digest>（单代）或 runtime-proofs/<digest> 补充 digest 与镜像标签。
	for _, proofsRel := range []string{filepath.Join("release", "runtime-proofs"), "runtime-proofs"} {
		proofsDir := filepath.Join(slotDir, proofsRel)
		if err := noSymlinks(proofsDir); err != nil {
			continue
		}
		entries, err := os.ReadDir(proofsDir)
		if err != nil {
			continue
		}
		var hexDirs []string
		for _, e := range entries {
			if e.IsDir() && hex64.MatchString(e.Name()) {
				hexDirs = append(hexDirs, e.Name())
			}
		}
		targetHex := strings.TrimPrefix(info.ImageDigest, "sha256:")
		if targetHex == "" && len(hexDirs) == 1 {
			targetHex = hexDirs[0]
			info.ImageDigest = "sha256:" + targetHex
		}
		if targetHex != "" {
			cfgPath := filepath.Join(proofsDir, targetHex, "config.json")
			if raw, err := readSafeSmallFile(cfgPath, 1<<20); err == nil {
				var ociCfg struct {
					Config struct {
						Labels map[string]string `json:"Labels"`
					} `json:"config"`
				}
				if json.Unmarshal(raw, &ociCfg) == nil {
					if info.DSHVersion == "" {
						if v := strings.TrimSpace(ociCfg.Config.Labels["io.auriga.dsh.version"]); v != "" {
							info.DSHVersion = v
						}
					}
					if info.PluginVersion == "" {
						if v := strings.TrimSpace(ociCfg.Config.Labels["io.auriga.dsh.plugin.version"]); v != "" {
							info.PluginVersion = v
						}
					}
				}
			}
		}
		if info.ImageDigest != "" {
			break
		}
	}
}

// readActiveRuntimeInfo 读取站点当前生效的 DSH 运行时信息：
// 优先读取 <data>/config/runtime-source/active.json；不存在时回退检查首装封存的
// <data>/config/materials/templates/real-dsh.pb。
func readActiveRuntimeInfo(data string) activeRuntimeInfo {
	var info activeRuntimeInfo
	if data == "" {
		return info
	}
	sourceDir := filepath.Join(data, "config", "runtime-source")
	activePath := filepath.Join(sourceDir, "active.json")
	if err := noSymlinks(activePath); err != nil {
		info.Configured = true
		info.SourceKind = "runtime-source"
		info.ParseError = err
		return info
	}
	if raw, err := readSafeSmallFile(activePath, 65536); err == nil {
		info.Configured = true
		info.SourceKind = "runtime-source"
		var active runtimeActivation
		if err := json.Unmarshal(raw, &active); err != nil {
			info.ParseError = fmt.Errorf("解析 active.json 失败：%w", err)
			return info
		}
		info.Slot = active.Current
		info.PreviousSlot = active.Previous
		info.Pending = active.Pending
		if active.Current != "" {
			if !validRuntimeSlotName(active.Current) {
				info.ParseError = fmt.Errorf("active.json 槽位名非法：%q", active.Current)
				return info
			}
			slotDir := filepath.Join(sourceDir, active.Current)
			if err := noSymlinks(slotDir); err != nil {
				info.ParseError = err
				return info
			}
			if st, err := os.Stat(slotDir); err != nil || !st.IsDir() {
				info.ParseError = fmt.Errorf("active.json 指向的槽位目录不存在：%s", active.Current)
				return info
			}
			info.SlotDir = slotDir
			readRuntimeSlotMetadata(slotDir, &info)
		}
		return info
	} else if !os.IsNotExist(err) {
		info.Configured = true
		info.SourceKind = "runtime-source"
		info.ParseError = err
		return info
	}

	// 回退检查首装封存在 config/materials 的运行时模板。
	baseDir := filepath.Join(data, "config", "materials")
	baseTemplate := filepath.Join(baseDir, "templates", "real-dsh.pb")
	if err := noSymlinks(baseTemplate); err == nil {
		if st, err := os.Lstat(baseTemplate); err == nil && st.Mode().IsRegular() {
			info.Configured = true
			info.SourceKind = "base-materials"
			info.SlotDir = baseDir
			readRuntimeSlotMetadata(baseDir, &info)
		}
	}
	return info
}

// captureQuietContext 执行只读轻量探测命令并丢弃 stderr，避免降级跳过时污染 doctor 输出。
func captureQuietContext(ctx context.Context, name string, args ...string) (string, error) {
	var stdout bytes.Buffer
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout = &stdout
	c.Stderr = io.Discard
	c.WaitDelay = 3 * time.Second
	if err := c.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func inspectRuntimeImageMetadata(ctx context.Context, image string) (runtimeImageMetadata, bool) {
	var meta runtimeImageMetadata
	if strings.TrimSpace(image) == "" {
		return meta, false
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	labelsRaw, err := captureQuietContext(inspectCtx, "docker", "image", "inspect", "--format", "{{json .Config.Labels}}", image)
	if err != nil {
		return meta, false
	}
	var labels map[string]string
	if json.Unmarshal([]byte(labelsRaw), &labels) == nil {
		meta.DSHVersion = strings.TrimSpace(labels["io.auriga.dsh.version"])
		meta.PluginVersion = strings.TrimSpace(labels["io.auriga.dsh.plugin.version"])
	}
	if meta.DSHVersion != "" && meta.PluginVersion != "" {
		return meta, true
	}
	// real-dsh 镜像构建时将版本清单写入 /opt/fixture/manifest.json。
	runCtx, runCancel := context.WithTimeout(ctx, 10*time.Second)
	defer runCancel()
	manifestRaw, err := captureQuietContext(runCtx, "docker", "run", "--rm", "--pull", "never", "--network", "none",
		"--log-driver", "none", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true",
		"--entrypoint", "cat", image, "/opt/fixture/manifest.json")
	if err != nil {
		return meta, meta.DSHVersion != "" || meta.PluginVersion != ""
	}
	var imgManifest runtimeImageMetadata
	if json.Unmarshal([]byte(manifestRaw), &imgManifest) == nil {
		if meta.DSHVersion == "" {
			meta.DSHVersion = strings.TrimSpace(imgManifest.DSHVersion)
		}
		if meta.PluginVersion == "" {
			meta.PluginVersion = strings.TrimSpace(imgManifest.PluginVersion)
		}
	}
	return meta, meta.DSHVersion != "" || meta.PluginVersion != ""
}

func defaultRuntimeExpectedSourceDirs(s state) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" {
			return
		}
		clean := filepath.Clean(d)
		if !seen[clean] {
			seen[clean] = true
			dirs = append(dirs, clean)
		}
	}
	add(s.Package)
	if cfgPath := defaultSiteConfigPath(); cfgPath != "" {
		if raw, err := readSafeSmallFile(cfgPath, 1<<20); err == nil {
			var partial struct {
				Source *struct {
					Dir string `json:"dir"`
				} `json:"source"`
			}
			if json.Unmarshal(raw, &partial) == nil && partial.Source != nil {
				add(partial.Source.Dir)
			}
		}
	}
	return dirs
}

func readExpectedRuntimeVersions(s state) expectedRuntimeVersions {
	var out expectedRuntimeVersions
	var sources []string
	addSource := func(src string) {
		for _, existing := range sources {
			if existing == src {
				return
			}
		}
		sources = append(sources, src)
	}
	for _, dir := range runtimeExpectedSourceDirsFunc(s) {
		// 1. 优先读取随离线发布包或 local-deploy 发行目录携带的 runtime-expected.json。
		if out.DSHVersion == "" || out.PluginVersion == "" {
			mPath := filepath.Join(dir, runtimeExpectedManifestName)
			if raw, err := readSafeSmallFile(mPath, 1<<20); err == nil {
				var m runtimeExpectedManifest
				if json.Unmarshal(raw, &m) == nil {
					used := false
					if out.DSHVersion == "" && strings.TrimSpace(m.DSHVersion) != "" {
						out.DSHVersion = strings.TrimSpace(m.DSHVersion)
						used = true
					}
					if out.PluginVersion == "" && strings.TrimSpace(m.PluginVersion) != "" {
						out.PluginVersion = strings.TrimSpace(m.PluginVersion)
						used = true
					}
					if used {
						addSource(mPath)
					}
				}
			}
		}
		// 2. 回退读取源码树中的 versions.json 与 clients/dsh-plugin/package.json。
		if out.DSHVersion == "" || out.PluginVersion == "" {
			dshVer, pluginVer, srcList := readSourceRuntimeVersions(dir)
			if out.DSHVersion == "" && dshVer != "" {
				out.DSHVersion = dshVer
				for _, p := range srcList {
					if strings.HasSuffix(p, "versions.json") {
						addSource(p)
					}
				}
			}
			if out.PluginVersion == "" && pluginVer != "" {
				out.PluginVersion = pluginVer
				for _, p := range srcList {
					if strings.HasSuffix(p, "package.json") {
						addSource(p)
					}
				}
			}
		}
		if out.DSHVersion != "" && out.PluginVersion != "" {
			break
		}
	}
	out.SourceDesc = strings.Join(sources, ", ")
	return out
}

func evaluateRuntimeConsistency(s state, inspectImage bool) runtimeConsistencyReport {
	var rep runtimeConsistencyReport
	if s.Runtime != nil && strings.TrimSpace(s.Runtime.Source.Image) != "" {
		rep.PinnedImage = strings.TrimSpace(s.Runtime.Source.Image)
		rep.PinnedSource = "state.json"
	} else if img := readStackEnvValue(s.Data, "AURIGA_RUNTIME_IMAGE"); img != "" {
		rep.PinnedImage = img
		rep.PinnedSource = "stack.env"
	}

	rep.Active = readActiveRuntimeInfo(s.Data)
	if !rep.Active.Configured && rep.PinnedImage == "" {
		rep.Status = "SKIP"
		return rep
	}

	if inspectImage && rep.Active.Configured && rep.Active.ParseError == nil && rep.Active.Image != "" {
		if meta, ok := inspectRuntimeImageMetadataFunc(context.Background(), rep.Active.Image); ok {
			if rep.Active.DSHVersion == "" && meta.DSHVersion != "" {
				rep.Active.DSHVersion = meta.DSHVersion
			}
			if rep.Active.PluginVersion == "" && meta.PluginVersion != "" {
				rep.Active.PluginVersion = meta.PluginVersion
			}
		}
	}

	rep.Expected = readExpectedRuntimeVersions(s)

	if rep.Active.ParseError != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("读取 config/runtime-source/active.json 异常：%v；请核查运行时目录或执行 runtime-upgrade.sh 修复。", rep.Active.ParseError))
	} else if rep.Active.Pending {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("运行时升级处于未提交状态（pending，槽位 %s）；请核查并执行 runtime-upgrade.sh 完成 commit 或 rollback。", rep.Active.Slot))
	}

	// #25：安装钉定运行时与当前生效运行时一致性检查。
	if rep.PinnedImage != "" && !rep.Active.Configured {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("已登记安装钉定运行时（%s，来自 %s），但未在 config/runtime-source/active.json 或 config/materials 中检测到生效运行时模板。", rep.PinnedImage, rep.PinnedSource))
	} else if rep.PinnedImage == "" && rep.Active.Configured && rep.Active.Image != "" {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("检测到生效运行时（%s），但 state.json/stack.env 未记录安装钉定运行时；干净重装前须在 site.json 的 server.install.runtime 中补齐。", rep.Active.Image))
	} else if rep.PinnedImage != "" && rep.Active.Configured && rep.Active.Image != "" && rep.PinnedImage != rep.Active.Image {
		slotDesc := rep.Active.SourceKind
		if rep.Active.Slot != "" {
			slotDesc = "config/runtime-source/active.json 槽位 " + rep.Active.Slot
		}
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"安装钉定运行时（%s，来自 %s）与当前生效运行时（%s，来自 %s）不一致：日常排障请以 config/runtime-source/active.json 为准；干净重装前须先更新 site.json 的 server.install.runtime；可运行 `sudo ./auriga runtime sync-pin --bundle <目录> --root <root.pb> --image %s --data %s` 同步安装钉定值。",
			rep.PinnedImage, rep.PinnedSource, rep.Active.Image, slotDesc, rep.Active.Image, s.Data,
		))
	}

	// #23：生效运行时与当前源码/发行包预期版本一致性检查。
	if rep.Active.Configured && rep.Active.ParseError == nil {
		if rep.Expected.DSHVersion == "" && rep.Expected.PluginVersion == "" {
			rep.Notes = append(rep.Notes, "当前发行包与站点配置缺少源码预期版本元数据（runtime-expected.json / versions.json / package.json），未完成 DSH/插件版本漂移比对。")
		} else {
			if rep.Expected.DSHVersion == "" {
				rep.Notes = append(rep.Notes, "当前发行包缺少预期 DSH 版本元数据，未完成 DSH 版本漂移比对。")
			} else if rep.Active.DSHVersion == "" {
				rep.Notes = append(rep.Notes, "未能读取生效运行时的 DSH 版本，未完成 DSH 版本漂移比对。")
			} else if rep.Active.DSHVersion != rep.Expected.DSHVersion {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf(
					"生效运行时 DSH 版本（%s）与当前源码/发行包预期版本（%s，来源 %s）不一致；auriga deploy 不会自动升级 DSH 运行时，请重新构建运行时并运行 runtime-upgrade.sh 升级。",
					rep.Active.DSHVersion, rep.Expected.DSHVersion, rep.Expected.SourceDesc,
				))
			}

			if rep.Expected.PluginVersion == "" {
				rep.Notes = append(rep.Notes, "当前发行包缺少预期 auriga-dsh 插件版本元数据，未完成插件版本漂移比对。")
			} else if rep.Active.PluginVersion == "" {
				if inspectImage {
					rep.Notes = append(rep.Notes, "未能从本地运行时镜像读取内嵌插件 auriga-dsh 版本，未完成插件版本漂移比对。")
				}
			} else if rep.Active.PluginVersion != rep.Expected.PluginVersion {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf(
					"生效运行时内嵌插件 auriga-dsh 版本（%s）与当前源码/发行包预期版本（%s，来源 %s）不一致；auriga deploy 不会自动升级 DSH 运行时，请重新构建运行时镜像并运行 runtime-upgrade.sh 升级。",
					rep.Active.PluginVersion, rep.Expected.PluginVersion, rep.Expected.SourceDesc,
				))
			}
		}
	}

	switch {
	case len(rep.Warnings) > 0:
		rep.Status = "WARN"
	case len(rep.Notes) > 0:
		rep.Status = "INFO"
	default:
		rep.Status = "PASS"
	}
	return rep
}

func formatActiveRuntimeDesc(a activeRuntimeInfo) string {
	if !a.Configured {
		return "未配置"
	}
	if a.ParseError != nil {
		return fmt.Sprintf("读取异常（%v）", a.ParseError)
	}
	img := a.Image
	if img == "" && a.ImageDigest != "" {
		img = a.ImageDigest
	}
	if img == "" {
		img = "未知镜像"
	}
	var parts []string
	if a.Slot != "" {
		parts = append(parts, "槽位 "+a.Slot)
	} else if a.SourceKind != "" {
		parts = append(parts, a.SourceKind)
	}
	if a.Pending {
		parts = append(parts, "pending 未提交")
	}
	if a.DSHVersion != "" {
		parts = append(parts, "dsh="+a.DSHVersion)
	}
	if a.PluginVersion != "" {
		parts = append(parts, "auriga-dsh="+a.PluginVersion)
	}
	if len(parts) == 0 {
		return img
	}
	return fmt.Sprintf("%s（%s）", img, strings.Join(parts, "，"))
}

// printDoctorRuntimeCheck 输出 doctor 的“运行时一致性”检查结果（不计入致命 failures）。
func printDoctorRuntimeCheck(w io.Writer, s state) runtimeConsistencyReport {
	rep := evaluateRuntimeConsistency(s, true)
	switch rep.Status {
	case "SKIP":
		fmt.Fprintln(w, "SKIP 运行时一致性 — 未配置独立 runtime-source 与安装钉定运行时")
	case "PASS":
		fmt.Fprintf(w, "PASS 运行时一致性 — 安装钉定与生效运行时一致：%s\n", formatActiveRuntimeDesc(rep.Active))
	case "INFO":
		fmt.Fprintf(w, "INFO 运行时一致性 — 安装钉定与生效运行时一致：%s（版本漂移检查未完整覆盖，见下方说明）\n", formatActiveRuntimeDesc(rep.Active))
		for _, note := range rep.Notes {
			fmt.Fprintf(w, "INFO 运行时一致性 — %s\n", note)
		}
	case "WARN":
		pinnedDesc := rep.PinnedImage
		if pinnedDesc == "" {
			pinnedDesc = "未登记"
		} else if rep.PinnedSource != "" {
			pinnedDesc = fmt.Sprintf("%s（%s）", pinnedDesc, rep.PinnedSource)
		}
		fmt.Fprintf(w, "INFO 运行时视图 — 安装钉定：%s；当前生效：%s\n", pinnedDesc, formatActiveRuntimeDesc(rep.Active))
		for _, warn := range rep.Warnings {
			fmt.Fprintf(w, "WARN 运行时一致性 — %s\n", warn)
		}
		for _, note := range rep.Notes {
			fmt.Fprintf(w, "INFO 运行时一致性 — %s\n", note)
		}
	}
	return rep
}

// printStatusRuntimeSummary 在 auriga status 中展示安装钉定运行时、当前生效运行时及一致性提示。
func printStatusRuntimeSummary(w io.Writer, s state) {
	rep := evaluateRuntimeConsistency(s, false)
	if rep.Status == "SKIP" {
		return
	}
	pinnedDesc := rep.PinnedImage
	if pinnedDesc == "" {
		pinnedDesc = "未登记"
	} else if rep.PinnedSource != "" {
		pinnedDesc = fmt.Sprintf("%s（%s）", pinnedDesc, rep.PinnedSource)
	}
	fmt.Fprintf(w, "安装钉定运行时（install pinned）：%s\n", pinnedDesc)
	fmt.Fprintf(w, "当前生效运行时（active runtime）：%s\n", formatActiveRuntimeDesc(rep.Active))
	switch rep.Status {
	case "PASS":
		fmt.Fprintln(w, "运行时一致性：一致")
	case "INFO":
		fmt.Fprintln(w, "运行时一致性：安装钉定与生效运行时一致（部分版本漂移未核对，详查请运行 auriga doctor）")
		for _, note := range rep.Notes {
			fmt.Fprintf(w, "INFO %s\n", note)
		}
	default:
		for _, warn := range rep.Warnings {
			fmt.Fprintf(w, "WARN %s\n", warn)
		}
		for _, note := range rep.Notes {
			fmt.Fprintf(w, "INFO %s\n", note)
		}
	}
}
