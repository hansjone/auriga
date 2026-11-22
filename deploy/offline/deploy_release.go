package main

// 不可变发行目录与镜像标签（设计 D1）。
//
//	<数据目录的父目录>/auriga-releases/<序号>/
//	    deploy-nas/                编排与脚本（源码快照的 deploy/nas），含 deploy-nas/bin/*
//	                               （脚本按 $NAS_ROOT/bin 找 bootstrap，与 bin/ 下为同一文件的硬链接）
//	    bin/auriga-nas-bootstrap
//	    bin/runtime-release
//	    release.json               序号、源码、镜像 ID、装配摘要、schemaCompatFloor 与 formatMax
//	    SHA256SUMS                 本地清单（不签名）
//
// state.current 指向当前序号（随 state 原子切换），state.history 保留最近 N 份（默认 3）。
// 历史有三种（deployRecord.Kind）：deploy（本机构建，目录结构如上）、upgrade（签名包 upgrade，
// 目录内是完整的签名离线包，同样在 auriga-releases/<序号>/）、install（签名包安装的旧布局
// <数据目录>.oneclick/release，第一次 deploy 或 upgrade 前登记为基线）。三种都用 releaseSums
// 锚定发行目录的 SHA256SUMS。
// 五个装配镜像除 auriga/<svc>:nas 外再打 auriga/<svc>:r<序号>；淘汰历史前确认没有容器在用。

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type deployRecord struct {
	Seq int `json:"seq"`
	// Kind：deploy（缺省）、upgrade、install，见文件头。
	Kind    string       `json:"kind,omitempty"`
	Status  string       `json:"status"` // installing | ok | failed
	At      string       `json:"at"`
	Source  deploySource `json:"source"`
	Release string       `json:"release"`
	// ReleaseSums 是发行目录 SHA256SUMS 文件本身的摘要：本地清单不签名，锚定在 root 所有的
	// state 里，回滚前核对，防止清单与文件被一并改写。
	ReleaseSums string            `json:"releaseSums"`
	Images      map[string]string `json:"images"`
	Assembly    map[string]string `json:"assembly,omitempty"`
	// AssemblyParts 各镜像装配分项，供下次对比触发项（2c）。
	AssemblyParts map[string]assemblyParts `json:"assemblyParts,omitempty"`
	// Fingerprints 产物输入指纹（2c）。
	Fingerprints map[string]string `json:"fingerprints,omitempty"`
	// 部署成功后读到的迁移版本与数据目录格式版本：之后回滚到本条时用来判断数据是否
	// 仍与本条运行时相同。
	Migrations map[string]string `json:"migrations,omitempty"`
	Formats    map[string]string `json:"formats,omitempty"`
	// 本发行声明的兼容下限（floor）与本版能读的格式上限；旧记录无字段则不能按 floor 回滚。
	SchemaCompatFloor string            `json:"schemaCompatFloor,omitempty"`
	FormatMax         map[string]string `json:"formatMax,omitempty"`
}

const (
	recordInstalling = "installing"
	recordOK         = "ok"
	recordFailed     = "failed"

	recordKindDeploy  = "deploy"
	recordKindUpgrade = "upgrade"
	recordKindInstall = "install"
)

func (r *deployRecord) kind() string {
	if r.Kind == "" {
		return recordKindDeploy
	}
	return r.Kind
}

// sourceForRecord：切到某条历史后 state.source 的取值（当前发行的来源）。
func sourceForRecord(r *deployRecord) string {
	if r.kind() == recordKindDeploy {
		return sourceLocalDeploy
	}
	return sourceSignedPackage
}

// legacyReleaseDir 是签名包 install 的旧布局发行目录。
func legacyReleaseDir(data string) string {
	return filepath.Join(data+".oneclick", "release")
}

// releaseManifest 写在发行目录的 release.json 与 deploy-nas/.auriga-release（status.sh 显示）。
type releaseManifest struct {
	Schema            int                      `json:"schemaVersion"`
	Seq               int                      `json:"seq"`
	Source            deploySource             `json:"source"`
	Images            map[string]string        `json:"images"`
	Assembly          map[string]string        `json:"assembly"`
	AssemblyParts     map[string]assemblyParts `json:"assemblyParts,omitempty"`
	Fingerprints      map[string]string        `json:"fingerprints,omitempty"`
	SchemaCompatFloor *string                  `json:"schemaCompatFloor"`
	FormatMax         map[string]string        `json:"formatMax"`
	CreatedAt         string                   `json:"createdAt"`
}

func releasesRoot(data string) string {
	return filepath.Join(filepath.Dir(data), "auriga-releases")
}

func releaseDir(data string, seq int) string {
	return filepath.Join(releasesRoot(data), strconv.Itoa(seq))
}

func (s *state) record(seq int) *deployRecord {
	for i := range s.History {
		if s.History[i].Seq == seq {
			return &s.History[i]
		}
	}
	return nil
}

// nextSeq：历史与目录里已有的最大序号加一（失败留下的目录不复用序号）。
func nextSeq(s state) int {
	max := 0
	for _, r := range s.History {
		if r.Seq > max {
			max = r.Seq
		}
	}
	if entries, err := os.ReadDir(releasesRoot(s.Data)); err == nil {
		for _, e := range entries {
			name := strings.TrimPrefix(e.Name(), ".staging-")
			if i := strings.IndexByte(name, '-'); i > 0 {
				name = name[:i]
			}
			if n, err := strconv.Atoi(name); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

func imageTag(svc string, seq int) string {
	return "auriga/" + svc + ":r" + strconv.Itoa(seq)
}

// writeReleaseDir 在暂存目录写完整发行，写好本地 SHA256SUMS 后一次 rename 到 <序号>；
// 返回发行目录与其 SHA256SUMS 文件的摘要（记进 state 的 releaseSums）。
// artifactsOut 是 go/web/java 产物根（含各生态子目录），provenance 为运行时挂载的版本来源。
func writeReleaseDir(ctx context.Context, data string, seq int, source, goOut, artifactsOut string, m releaseManifest, prov *provenance, fpReport *fingerprintReport) (string, string, error) {
	root := releasesRoot(data)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", "", err
	}
	if err := depsTrustedSourceCheck("发行根目录 ", root); err != nil {
		return "", "", err
	}
	if err := checkArtifactSums(goOut); err != nil {
		return "", "", err
	}
	final := releaseDir(data, seq)
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		return "", "", fmt.Errorf("发行目录 %s 已存在，拒绝覆盖", final)
	}
	tmp, err := os.MkdirTemp(root, ".staging-"+strconv.Itoa(seq)+"-")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(tmp, 0755); err != nil {
		return "", "", err
	}
	fail := func(err error) (string, string, error) {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	if err := copyTreeContext(ctx, filepath.Join(source, "deploy", "nas"), filepath.Join(tmp, "deploy-nas")); err != nil {
		return fail(err)
	}
	for _, name := range []string{"auriga-nas-bootstrap", "runtime-release"} {
		target := filepath.Join(tmp, "bin", name)
		if err := copyFileContext(ctx, filepath.Join(goOut, name), target, 0755); err != nil {
			return fail(fmt.Errorf("发行目录缺少 %s：%w", name, err))
		}
		link := filepath.Join(tmp, "deploy-nas", "bin", name)
		if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
			return fail(err)
		}
		_ = os.Remove(link)
		if err := os.Link(target, link); err != nil {
			if err := copyFileContext(ctx, target, link, 0755); err != nil {
				return fail(err)
			}
		}
	}
	if artifactsOut != "" {
		if err := os.Mkdir(filepath.Join(tmp, "artifacts"), 0755); err != nil {
			return fail(err)
		}
		for _, kind := range []string{"go", "web", "java"} {
			src := filepath.Join(artifactsOut, kind)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			if err := copyTreeContext(ctx, src, filepath.Join(tmp, "artifacts", kind)); err != nil {
				return fail(fmt.Errorf("写入 artifacts/%s：%w", kind, err))
			}
		}
	}
	if prov != nil {
		if err := jsonFile(filepath.Join(tmp, "provenance.json"), prov, 0644); err != nil {
			return fail(err)
		}
	}
	if fpReport != nil {
		if err := writeFingerprintReport(filepath.Join(tmp, "fingerprints.json"), fpReport); err != nil {
			return fail(err)
		}
		m.Fingerprints = fingerprintMap(fpReport)
	}
	m.Schema, m.Seq = 1, seq
	if err := jsonFile(filepath.Join(tmp, "release.json"), m, 0644); err != nil {
		return fail(err)
	}
	if err := jsonFile(filepath.Join(tmp, "deploy-nas", ".auriga-release"), m, 0644); err != nil {
		return fail(err)
	}
	if err := writeSumsContext(ctx, tmp); err != nil {
		return fail(err)
	}
	sums, err := digest(filepath.Join(tmp, "SHA256SUMS"))
	if err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fail(err)
	}
	return final, sums, nil
}

// checkReleaseDir 回滚前核对发行目录：根目录 root 所有、路径即 auriga-releases/<序号>（install
// 记录为旧布局 <数据目录>.oneclick/release）、SHA256SUMS 与 state 锚定的摘要一致、本地清单完整。
func checkReleaseDir(data string, r *deployRecord) error {
	root := releasesRoot(data)
	switch {
	case r.Release == releaseDir(data, r.Seq) && r.kind() != recordKindInstall:
	case r.Release == legacyReleaseDir(data) && r.kind() == recordKindInstall:
		root = data + ".oneclick"
	default:
		return fmt.Errorf("历史 %d 的发行目录路径异常 %s", r.Seq, r.Release)
	}
	if err := depsTrustedSourceCheck("发行根目录 ", root); err != nil {
		return err
	}
	if err := noSymlinks(filepath.Join(r.Release, "SHA256SUMS")); err != nil {
		return err
	}
	got, err := digest(filepath.Join(r.Release, "SHA256SUMS"))
	if err != nil || r.ReleaseSums == "" || got != r.ReleaseSums {
		return fmt.Errorf("发行目录 r%d 的 SHA256SUMS 与 state 记录的摘要不一致（或未记录），拒绝使用", r.Seq)
	}
	if err := verifySums(r.Release); err != nil {
		return fmt.Errorf("发行目录 r%d 本地清单校验失败：%w", r.Seq, err)
	}
	return nil
}

func tagImages(ctx context.Context, images map[string]string, tag func(svc string) string) error {
	for _, svc := range deployServices {
		id := images[imageKey(svc)]
		if id == "" {
			continue
		}
		if err := deployRun(ctx, runSpec{Name: "docker", Args: []string{"tag", id, tag(svc)}}); err != nil {
			return err
		}
	}
	return nil
}

// checkTaggedImages 回滚前确认 auriga/<svc>:r<序号> 仍指向历史记录的镜像 ID（签名包记录的镜像
// 可能写成 仓库@sha256:，先解析为本机镜像 ID 再比较）。
func checkTaggedImages(ctx context.Context, r *deployRecord) error {
	for _, svc := range deployServices {
		want := r.Images[imageKey(svc)]
		if want == "" {
			continue
		}
		if !imageIDPattern.MatchString(want) {
			if id, _, ok := localImage(ctx, want); ok {
				want = id
			}
		}
		id, _, ok := localImage(ctx, imageTag(svc, r.Seq))
		if !ok || id != want {
			return fmt.Errorf("镜像 %s 不在本机或已不是历史记录的镜像（%s）", imageTag(svc, r.Seq), r.Images[imageKey(svc)])
		}
	}
	for _, svc := range deployExtraServices {
		if want := r.Images[imageKey(svc)]; want != "" {
			if _, _, ok := localImage(ctx, want); !ok {
				return fmt.Errorf("历史 r%d 的 %s 镜像 %s 已不在本机", r.Seq, svc, want)
			}
		}
	}
	return nil
}

// updateStackEnv 把 AURIGA_IMAGE_* 与 AURIGA_INSTALLER_IMAGE（跟 postgres）写成给定镜像，
// 其余配置原样保留；缺的键追加在末尾。
func updateStackEnv(data string, images map[string]string) error {
	path := filepath.Join(data, "stack.env")
	if err := noSymlinks(path); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updates := map[string]string{}
	for key, id := range images {
		updates["AURIGA_IMAGE_"+key] = id
	}
	if pg := images["POSTGRES"]; pg != "" {
		updates["AURIGA_INSTALLER_IMAGE"] = pg
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if value, hit := updates[key]; ok && hit {
			lines[i] = key + "=" + value
			delete(updates, key)
		}
	}
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tail := []string{}
	for _, k := range keys {
		tail = append(tail, k+"="+updates[k])
	}
	if len(tail) > 0 {
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(append(lines, tail...), "")
	}
	return writeFileAtomic(path, []byte(strings.Join(lines, "\n")), 0600)
}

// containerImageIDs 返回所有容器（含已停止）使用的镜像 ID。
func containerImageIDs(ctx context.Context) (map[string]bool, error) {
	ids, err := deployCapture(ctx, "docker", "ps", "-aq", "--no-trunc")
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	if strings.TrimSpace(ids) == "" {
		return used, nil
	}
	out, err := deployCapture(ctx, "docker", append([]string{"inspect", "--format", "{{.Image}}"}, strings.Fields(ids)...)...)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Fields(out) {
		used[line] = true
	}
	return used, nil
}

// pruneReleases 保留最近 keep 份成功（ok）的历史，当前序号总是保留；失败与未完成的记录
// 不占保留名额（连续失败不会把更早的成功发行挤掉）。淘汰一份前确认它独有的镜像没有任何
// 容器在用；在用就跳过并提示。返回被淘汰的序号。调用方负责保存 state。
func pruneReleases(ctx context.Context, s *state, keep int, w io.Writer) ([]int, error) {
	if keep < 1 {
		keep = 1
	}
	sorted := append([]deployRecord(nil), s.History...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq > sorted[j].Seq })
	kept := map[int]bool{s.Current: true}
	for i, n := 0, 0; i < len(sorted) && n < keep; i++ {
		if sorted[i].Status == recordOK {
			kept[sorted[i].Seq] = true
			n++
		}
	}
	var candidates []deployRecord
	keptIDs := map[string]bool{}
	for _, r := range sorted {
		if kept[r.Seq] {
			for _, id := range r.Images {
				keptIDs[id] = true
			}
		} else {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	used, err := containerImageIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("无法确认容器使用的镜像，本次不淘汰历史：%w", err)
	}
	var removed []int
	for _, r := range candidates {
		busy := ""
		for _, svc := range deployServices {
			id := r.Images[imageKey(svc)]
			if id != "" && !keptIDs[id] && used[id] {
				busy = svc
				break
			}
		}
		if busy != "" {
			fmt.Fprintf(w, "历史 r%d 的 %s 镜像仍有容器在用，暂不淘汰\n", r.Seq, busy)
			continue
		}
		for _, svc := range deployServices {
			if r.Images[imageKey(svc)] == "" {
				continue
			}
			if err := deployRun(ctx, runSpec{Name: "docker", Args: []string{"image", "rm", imageTag(svc, r.Seq)}, Stdout: io.Discard}); err != nil {
				fmt.Fprintf(w, "警告：删除镜像标签 %s 失败：%v\n", imageTag(svc, r.Seq), err)
			}
		}
		if r.Release == releaseDir(s.Data, r.Seq) {
			if err := os.RemoveAll(r.Release); err != nil {
				fmt.Fprintf(w, "警告：删除发行目录 %s 失败：%v\n", r.Release, err)
			}
		}
		removed = append(removed, r.Seq)
	}
	if len(removed) > 0 {
		drop := map[int]bool{}
		for _, n := range removed {
			drop[n] = true
		}
		var keepHistory []deployRecord
		for _, r := range s.History {
			if !drop[r.Seq] {
				keepHistory = append(keepHistory, r)
			}
		}
		s.History = keepHistory
	}
	return removed, nil
}

func cloneImages(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
