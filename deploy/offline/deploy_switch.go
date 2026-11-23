package main

// 只换容器切换（设计 D7、D8，遗留 P5）。
//
// 镜像 → compose 服务表写在本文件；单测离线解析 compose YAML 的 image: 插值来源双向交叉核对。
// 编排摘要覆盖 render 全部输入；摘要未变且仅 edge/platform（及 operator 特例）变化时走只换容器。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	switchModeFull       = "完整启动（render → preflight → start）"
	switchModeContainers = "只换容器"
	switchModeOperator   = "仅更新 stack.env + preflight（operator）"
	switchModePointer    = "仅切换发行指针（镜像与编排未变）"

	brokerArtifact = "auriga-network-broker"
)

// imageComposeServices：镜像键（大写，AURIGA_IMAGE_<键>）→ 主 compose（docker-compose.yml）里
// 用它的服务。collab overlay 的增量见 collabOverlayImageServices；operator 不是 compose 服务。
// LOKI/ALLOY 是 observability profile 的固定默认镜像，不参与切换，只为交叉核对完整。
var imageComposeServices = map[string][]string{
	"EDGE":     {"panel-prepare", "panel-renew", "ai-relay", "edge", "network-broker", "network", "egress"},
	"PLATFORM": {"platform"},
	"AGENT":    {"agent"},
	"BIFROST":  {"bifrost"},
	"POSTGRES": {"postgres", "audit-db-prepare"},
	"NATS":     {"nats"},
	"REGISTRY": {"registry"},
	"LOKI":     {"loki"},
	"ALLOY":    {"alloy-prepare", "alloy"},
}

// collabOverlayImageServices：collab-compose.yml 增加的服务 → 插值来源。
var collabOverlayImageServices = map[string]string{
	"collab":       "AURIGA_IMAGE_PLATFORM",
	"collab-db":    "AURIGA_IMAGE_POSTGRES",
	"collab-renew": "AURIGA_INSTALLER_IMAGE",
}

// composeCrossCheckFiles：run_compose（lib.sh）可能叠加的仓库内 compose 文件。交叉核对时
// 全部解析：除主 compose 与 collab overlay 外，其余文件不得引入 AURIGA 镜像插值。
var composeCrossCheckFiles = []string{
	"docker-compose.yml", "oneclick-compose.yml", "resource-downgrade-compose.yml",
	"shared-runtime-compose.yml", "managed-identity-compose.yml", "collab-compose.yml",
}

// edgeContainerRecreate：只换容器时重建的 edge 侧服务（跳过 panel-prepare 与 network-broker）。
var edgeContainerRecreate = []string{"edge", "ai-relay", "egress", "panel-renew", "network"}

// composeImageEnvRe 匹配 image: 值里的第一个（主）${AURIGA_IMAGE_*} / ${AURIGA_INSTALLER_IMAGE} 插值。
var composeImageEnvRe = regexp.MustCompile(`\$\{(AURIGA_(?:IMAGE_[A-Z0-9_]+|INSTALLER_IMAGE))(?::?-|\})`)

// expectedComposeImageEnv：服务 → 期望的环境变量名（交叉测试用）。
func expectedComposeImageEnv(collabOverlay bool) map[string]string {
	out := map[string]string{}
	for key, svcs := range imageComposeServices {
		env := "AURIGA_IMAGE_" + key
		for _, svc := range svcs {
			out[svc] = env
		}
	}
	if collabOverlay {
		for svc, env := range collabOverlayImageServices {
			out[svc] = env
		}
	}
	return out
}

// crossCheckComposeImages 双向核对：代码表里的服务必须在 compose 里用同一插值，compose 里
// 每个带 AURIGA 镜像插值的服务也必须在代码表里（新增、改名都会失败）。
func crossCheckComposeImages(got, want map[string]string) error {
	var problems []string
	for svc, env := range want {
		if got[svc] != env {
			problems = append(problems, fmt.Sprintf("代码表 %s→%s，compose 为 %q", svc, env, got[svc]))
		}
	}
	for svc, env := range got {
		if _, ok := want[svc]; !ok {
			problems = append(problems, fmt.Sprintf("compose 服务 %s（%s）不在代码表", svc, env))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("镜像→服务表与 compose 不一致：" + strings.Join(problems, "；"))
	}
	return nil
}

// parseComposeImageEnvs 离线解析 compose YAML：各服务 image: 字面量里的主 ${AURIGA_*} 插值来源。
// 选择字面量解析而不是 docker compose config：CI 单测环境没有 docker，且插值来源就写在 YAML
// 字面量里（config 会把它展开成具体值反而丢失来源）。只接受本仓库 compose 的两空格缩进写法；
// image: 不是 AURIGA 插值（固定镜像）时不纳入。
func parseComposeImageEnvs(paths ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var service string
		inServices := false
		for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if indent == 0 {
				inServices = trimmed == "services:"
				service = ""
				continue
			}
			if !inServices {
				continue
			}
			if indent == 2 {
				service = ""
				if name, rest, ok := strings.Cut(trimmed, ":"); ok && !strings.ContainsAny(name, " \"'") {
					rest = strings.TrimSpace(rest)
					if rest == "" || strings.HasPrefix(rest, "!") || strings.HasPrefix(rest, "&") {
						service = name
					}
				}
				continue
			}
			if service == "" || indent != 4 || !strings.HasPrefix(trimmed, "image:") {
				continue
			}
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
			if m := composeImageEnvRe.FindStringSubmatch(val); m != nil {
				out[service] = m[1]
			}
		}
	}
	return out, nil
}

// orchInputs：编排摘要的站点输入（数据目录与 access 文件）。
type orchInputs struct{ Data, Access string }

// orchestrationDigest 计算编排摘要（写入发行记录、release.json 与收据）。
// deployNasRoot：发行里的 deploy-nas；data：站点数据目录（stack.env、collab-source）；access：访问配置文件。
func orchestrationDigest(deployNasRoot, data, access string) (string, error) {
	parts, err := orchestrationParts(deployNasRoot, filepath.Join(deployNasRoot, "bin"), data, access)
	if err != nil {
		return "", err
	}
	return parts.Total, nil
}

type orchParts struct {
	DeployNas string `json:"deployNas"`
	Bins      string `json:"bins"`
	StackEnv  string `json:"stackEnv"`
	Access    string `json:"access"`
	Collab    string `json:"collabSource"`
	Total     string `json:"total"`
}

// orchestrationParts：binRoot 单独给出是为了 --dry-run 能用源码 deploy/nas（没有 bin/）配
// 当前发行的 bin/ 估算。
func orchestrationParts(deployNasRoot, binRoot, data, access string) (orchParts, error) {
	var p orchParts
	var err error
	p.DeployNas, err = digestDeployNas(deployNasRoot)
	if err != nil {
		return p, fmt.Errorf("编排摘要 deploy-nas：%w", err)
	}
	p.Bins, err = digestBins(binRoot)
	if err != nil {
		return p, fmt.Errorf("编排摘要 deploy-nas/bin：%w", err)
	}
	p.StackEnv, err = digestStackEnvNoImages(filepath.Join(data, "stack.env"))
	if err != nil {
		return p, fmt.Errorf("编排摘要 stack.env：%w", err)
	}
	p.Access, err = digestOptionalFile(access)
	if err != nil {
		return p, fmt.Errorf("编排摘要 access：%w", err)
	}
	receipt := filepath.Join(data, "config", "collab-source", "activation.receipt")
	p.Collab, err = digestOptionalFile(receipt)
	if err != nil {
		return p, fmt.Errorf("编排摘要 collab-source：%w", err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "deployNas=%s\nbins=%s\nstackEnv=%s\naccess=%s\ncollab=%s\n", p.DeployNas, p.Bins, p.StackEnv, p.Access, p.Collab)
	p.Total = hex.EncodeToString(h.Sum(nil))
	return p, nil
}

// digestDeployNas：deploy-nas 文件树摘要；排除 *.md 与 test/（设计 L1：排除文档与测试），
// 排除顶层 bin/（由 digestBins 单独计入）与发行标记 .auriga-release（按序号与镜像变化，
// 不是 render 输入；不排除就会让每次部署都“编排变化”）。
func digestDeployNas(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := d.Name()
			if rel == "bin" || base == "test" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".auriga-release" || strings.HasSuffix(strings.ToLower(rel), ".md") {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("拒绝符号链接 %s", path)
		}
		sum, err := digest(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %s\n", sum, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// digestBins：deploy-nas/bin 下的程序（auriga-nas-bootstrap、runtime-release；render 会调用）。
func digestBins(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return sha256Hex([]byte("absent\n")), nil
	}
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range entries { // ReadDir 已按名排序
		if e.IsDir() {
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("拒绝符号链接 %s", filepath.Join(root, e.Name()))
		}
		sum, err := digest(filepath.Join(root, e.Name()))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %s\n", sum, e.Name())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// digestStackEnvNoImages：去掉 AURIGA_IMAGE_* 与 AURIGA_INSTALLER_IMAGE、注释与空行，
// 去掉 CR，按行排序后摘要（顺序与换行风格不影响结果）。
func digestStackEnvNoImages(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sha256Hex([]byte("absent\n")), nil
		}
		return "", err
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, _, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		if key == "AURIGA_INSTALLER_IMAGE" || strings.HasPrefix(key, "AURIGA_IMAGE_") {
			continue
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return sha256Hex([]byte(strings.Join(lines, "\n") + "\n")), nil
}

func digestOptionalFile(path string) (string, error) {
	if path == "" {
		return sha256Hex([]byte("absent\n")), nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return sha256Hex([]byte("absent\n")), nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("拒绝符号链接 %s", path)
	}
	return digest(path)
}

// switchPlan 描述本次切换走哪条路径。
type switchPlan struct {
	Mode                 string
	Reason               string
	OrchestrationChanged bool
	Orchestration        string
	Edge                 bool
	Platform             bool
	OperatorOnly         bool
	// HasCollab：数据目录有 config/collab-source（与 start.sh 调 start-collab-network.sh 的条件相同）。
	HasCollab bool
	// BrokerSeq/BrokerImage：切换完成后 network-broker 实际运行的 edge 镜像及其序号。
	BrokerSeq   int
	BrokerImage string
	// MixedBroker：network-broker 运行的镜像与本条 edge 镜像不同（收据写混合版本）。
	MixedBroker bool
	Recreate    []string // 将 force-recreate 的服务（不含 agent restart / collab 脚本）
}

func collabSourcePresent(data string) bool {
	_, err := os.Lstat(filepath.Join(data, "config", "collab-source"))
	return err == nil
}

// recordBroker 返回记录 r 为当前时 network-broker 运行的镜像与序号（旧记录无字段时视为与 edge 相同）。
func recordBroker(r *deployRecord) (int, string) {
	if r.BrokerImage != "" && r.BrokerSeq != 0 {
		return r.BrokerSeq, r.BrokerImage
	}
	return r.Seq, r.Images["EDGE"]
}

// planSwitch 根据编排摘要与镜像差决定完整启动 / 只换容器 / operator / 仅指针。
// 编排摘要按 rec 发行目录的 deploy-nas 与当前站点输入现算，与 prev 记录里存的摘要比较；
// prev 没有记录摘要（2d 之前的历史、签名包 install/upgrade）一律视为编排变化，走完整启动。
func planSwitch(prev, rec *deployRecord, data, access string) (switchPlan, error) {
	orch, err := orchestrationDigest(filepath.Join(rec.Release, "deploy-nas"), data, access)
	if err != nil {
		return switchPlan{}, err
	}
	return planSwitchWith(prev, rec, orch, data), nil
}

func planSwitchWith(prev, rec *deployRecord, orch, data string) switchPlan {
	p := switchPlan{HasCollab: collabSourcePresent(data), Orchestration: orch}
	prevOrch := ""
	if prev != nil {
		prevOrch = prev.Orchestration
	}
	p.OrchestrationChanged = prevOrch == "" || prevOrch != orch

	changed := map[string]bool{}
	for _, svc := range append(append([]string(nil), deployServices...), deployExtraServices...) {
		key := imageKey(svc)
		if prev == nil || prev.Images[key] != rec.Images[key] {
			changed[key] = true
		}
	}
	p.Edge = changed["EDGE"]
	p.Platform = changed["PLATFORM"]
	operatorChanged := changed["OPERATOR"]
	agentChanged := changed["AGENT"]
	bifrostChanged := changed["BIFROST"]

	// broker 程序摘要：用 2c 的产物输入指纹（输入不变则产物不变；输入变了保守按产物变处理）。
	// 与 broker 实际运行的那条记录比较不必要：只换容器只在指纹相同时发生，相等可传递。
	brokerChanged := true
	if prev != nil {
		a, b := prev.Fingerprints[brokerArtifact], rec.Fingerprints[brokerArtifact]
		brokerChanged = a == "" || b == "" || a != b
	}

	switch {
	case prev != nil && prev.Status != recordOK:
		p.Mode, p.Reason = switchModeFull, fmt.Sprintf("上一版 r%d 状态为 %s，无法作为已运行基线，走完整启动", prev.Seq, prev.Status)
	case prev != nil && (prev.kind() == recordKindAdopt || rec.kind() == recordKindAdopt):
		// adopt 接管记录没有可信的产物指纹与装配摘要，现场容器也不是本工具启动的：与它之间的
		// 切换（adopt 后第一次 deploy、回滚到 adopt 版）一律完整启动，不走只换容器或仅切指针。
		p.Mode, p.Reason = switchModeFull, "adopt 接管记录没有可信的产物指纹/装配摘要，与它之间的切换一律完整启动"
	case p.OrchestrationChanged:
		p.Mode, p.Reason = switchModeFull, "编排摘要变化"
		if prev != nil && prev.Orchestration == "" {
			p.Reason = "上一版没有编排摘要记录（2d 之前的历史或签名包），按编排变化处理"
		}
	case agentChanged || bifrostChanged:
		p.Mode, p.Reason = switchModeFull, "agent 或 bifrost 镜像变化"
	case p.Edge && brokerChanged:
		p.Mode, p.Reason = switchModeFull, "edge 镜像变化且 auriga-network-broker 产物指纹也变（或无法证明未变）"
	case !p.Edge && !p.Platform && operatorChanged && !anyOtherChanged(changed, "OPERATOR"):
		p.Mode, p.Reason = switchModeOperator, "仅 operator 镜像变化"
		p.OperatorOnly = true
	case onlyEdgePlatformOperator(changed):
		p.Mode = switchModeContainers
		var why []string
		if p.Edge {
			why = append(why, "edge")
		}
		if p.Platform {
			why = append(why, "platform")
		}
		if operatorChanged {
			why = append(why, "operator")
		}
		p.Reason = "编排未变，仅 " + strings.Join(why, "/") + " 镜像变化"
		if p.Platform {
			p.Recreate = append(p.Recreate, "platform")
		}
		if p.Edge {
			for _, svc := range edgeContainerRecreate {
				// collab-source 存在时 network 属消息网络（overlay 里是 collab-network profile），
				// 部署一律离线维护（start --network-offline 会停掉它），交给 start-collab-network.sh。
				if svc == "network" && p.HasCollab {
					continue
				}
				p.Recreate = append(p.Recreate, svc)
			}
		}
	case len(changed) == 0:
		p.Mode, p.Reason = switchModePointer, "镜像与编排均未变，仅切换发行指针"
	default:
		p.Mode, p.Reason = switchModeFull, "其它镜像变化，不在只换容器范围"
	}
	p.setBroker(prev, rec)
	return p
}

// setBroker：完整启动会按新 edge 镜像重建 broker；其它路径不碰 broker，沿用 prev 时的运行镜像。
func (p *switchPlan) setBroker(prev, rec *deployRecord) {
	if p.Mode == switchModeFull || prev == nil {
		p.BrokerSeq, p.BrokerImage = rec.Seq, rec.Images["EDGE"]
	} else {
		p.BrokerSeq, p.BrokerImage = recordBroker(prev)
	}
	p.MixedBroker = p.BrokerImage != rec.Images["EDGE"]
}

func onlyEdgePlatformOperator(changed map[string]bool) bool {
	for k, v := range changed {
		if !v {
			continue
		}
		switch k {
		case "EDGE", "PLATFORM", "OPERATOR":
		default:
			// postgres/nats/registry 配方固定，正常不应变；若变了走完整启动
			return false
		}
	}
	return changed["EDGE"] || changed["PLATFORM"]
}

func anyOtherChanged(changed map[string]bool, except string) bool {
	for k, v := range changed {
		if v && k != except {
			return true
		}
	}
	return false
}

// siteCompose 可注入的 compose 调用（只换容器路径）。
func (ops siteOps) siteCompose() func(s state, out *bytes.Buffer, args ...string) error {
	if ops.compose != nil {
		return ops.compose
	}
	return compose
}

func (ops siteOps) siteCollabNetwork() func(s state) error {
	if ops.collabNetwork != nil {
		return ops.collabNetwork
	}
	return startCollabNetworkOffline
}

// startCollabNetworkOffline 与 start.sh --network-offline 末尾相同：以消息网络离线维护模式运行
// start-collab-network.sh（暂停调度、停掉 collab/network/network-broker）。部署的完整启动一律
// --network-offline，只换容器保持同样的结束状态；消息网络由运营者按 nas-network-renewal 说明恢复。
func startCollabNetworkOffline(s state) error {
	env := []string{"AURIGA_DATA_ROOT=" + s.Data, "NAS_ROOT=" + filepath.Join(s.Package, "deploy-nas"),
		"STACK_ENV=" + filepath.Join(s.Data, "stack.env"), "AURIGA_MESSAGE_NETWORK_OFFLINE=1"}
	return command(os.Stdin, os.Stdout, env, "sh", filepath.Join(s.Package, "deploy-nas/scripts/start-collab-network.sh"))
}

// startRecord 按 plan 切换到历史记录 r：先把 r 的镜像写进 stack.env、原子保存 state，再按路径启动。
func startRecord(ops siteOps, s *state, r *deployRecord, timeout time.Duration, plan switchPlan) error {
	if err := updateStackEnv(s.Data, r.Images); err != nil {
		return err
	}
	s.Current, s.Package, s.Images = r.Seq, r.Release, cloneImages(r.Images)
	s.Source = sourceForRecord(r)
	r.BrokerSeq, r.BrokerImage = plan.BrokerSeq, plan.BrokerImage
	if err := saveState(*s); err != nil {
		return err
	}
	// 成功后记下本条实际按哪个编排运行（回滚完整启动时按目标发行 + 当前站点输入重算）。
	done := func() error {
		if plan.Orchestration != "" {
			r.Orchestration = plan.Orchestration
			return saveState(*s)
		}
		return nil
	}
	switch plan.Mode {
	case switchModeOperator:
		if err := ops.nas(*s, "preflight"); err != nil {
			return fmt.Errorf("preflight 失败：%w", err)
		}
		return done()
	case switchModePointer:
		return done()
	case switchModeContainers:
		if err := recreateContainers(ops, *s, plan, timeout); err != nil {
			return err
		}
	default:
		for _, step := range [][]string{{"render"}, {"preflight"}, {"start", "--network-offline"}} {
			if err := ops.nas(*s, step[0], step[1:]...); err != nil {
				return fmt.Errorf("%s 失败：%w", step[0], err)
			}
		}
	}
	if err := ops.health(*s, timeout); err != nil {
		return fmt.Errorf("健康检查失败：%w", err)
	}
	return done()
}

// recreateContainers：只换容器。compose 一律 --no-deps（不拉起、不重建依赖，尤其 network-broker）、
// --no-build --pull never；stack.env 已在 startRecord 写好新镜像。之后照 start.sh：重启 agent 恢复
// 实例网络端点；collab-source 存在时以离线模式运行 start-collab-network.sh（失败不影响核心，同 start.sh 的 `|| :`）。
func recreateContainers(ops siteOps, s state, plan switchPlan, timeout time.Duration) error {
	c := ops.siteCompose()
	var discard bytes.Buffer
	wait := int(timeout / time.Second)
	if wait <= 0 {
		wait = 1
	}
	up := func(svcs ...string) error {
		if len(svcs) == 0 {
			return nil
		}
		args := append([]string{"up", "-d", "--no-deps", "--no-build", "--pull", "never", "--force-recreate", "--wait", "--wait-timeout", strconv.Itoa(wait)}, svcs...)
		if err := c(s, &discard, args...); err != nil {
			return fmt.Errorf("重建 %s 失败：%w", strings.Join(svcs, "、"), err)
		}
		return nil
	}
	// platform 先于 edge（edge 依赖 platform 健康）。
	var first, rest []string
	for _, svc := range plan.Recreate {
		if svc == "platform" {
			first = append(first, svc)
		} else {
			rest = append(rest, svc)
		}
	}
	if err := up(first...); err != nil {
		return err
	}
	if err := up(rest...); err != nil {
		return err
	}
	// start.sh：Compose 换掉 edge 后旧的实例网络端点随之消失；重启 agent 只恢复已核验的实例桥。
	if err := c(s, &discard, "restart", "--no-deps", "agent"); err != nil {
		return fmt.Errorf("重启 agent 失败：%w", err)
	}
	if plan.HasCollab {
		if err := ops.siteCollabNetwork()(s); err != nil {
			fmt.Fprintf(os.Stderr, "start-collab-network.sh（离线维护模式）返回 %v；消息网络保持离线，核心服务不受影响\n", err)
		}
	}
	return nil
}

// planRollbackSwitch：回滚到 target。target 相对当前运行记录（current）编排未变则只换容器
// （把涉及服务的 AURIGA_IMAGE_* 写回 target），否则切回 target 发行目录完整启动（D8）。
// current 没有编排摘要（旧记录）按编排变化处理。
func planRollbackSwitch(current, target *deployRecord, data, access string) (switchPlan, error) {
	if current == nil || target == nil {
		p := switchPlan{Mode: switchModeFull, Reason: "回滚：缺少当前或目标记录", HasCollab: collabSourcePresent(data), OrchestrationChanged: true}
		if target != nil {
			p.setBroker(nil, target)
		}
		return p, nil
	}
	p, err := planSwitch(current, target, data, access)
	if err != nil {
		return p, err
	}
	p.Reason = "回滚：" + p.Reason
	return p, nil
}

// writeReleaseMarkers 写入与 load-images.sh 等价的发行标记与 versions.txt（供 status.sh 显示）：
// .auriga-release 与包内 images/manifest.json 同构（commit、images.<服务>.id），versions.txt 为 key=value。
func writeReleaseMarkers(releaseDir string, m releaseManifest) error {
	images := map[string]map[string]string{}
	for k, id := range m.Images {
		images[strings.ToLower(k)] = map[string]string{"id": id}
	}
	marker := map[string]any{
		"commit": m.Source.Commit,
		"images": images,
		"seq":    m.Seq,
		"source": m.Source,
	}
	if err := jsonFile(filepath.Join(releaseDir, "deploy-nas", ".auriga-release"), marker, 0644); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "commit=%s\n", m.Source.Commit)
	fmt.Fprintf(&b, "seq=%d\n", m.Seq)
	if m.CreatedAt != "" {
		fmt.Fprintf(&b, "created=%s\n", m.CreatedAt)
	}
	return writeFileAtomic(filepath.Join(releaseDir, "versions.txt"), []byte(b.String()), 0644)
}

// mixedVersionsNote 生成收据混合版本说明。
func mixedVersionsNote(plan switchPlan) string {
	if plan.MixedBroker {
		return fmt.Sprintf("混合版本：network-broker 仍为 r%d", plan.BrokerSeq)
	}
	return "无"
}

// formatSwitchPlanLine 供 dry-run / 日志一行说明。
func formatSwitchPlanLine(plan switchPlan) string {
	return fmt.Sprintf("%s（%s）", plan.Mode, plan.Reason)
}
