package main

// Maven 锁文件 services/maven-deps.sha256（设计 D5 / 统一现场部署第 8 步）。
//
// 格式（文件头注释可说明；解析时忽略空行与 # 开头行）：
//
//	groupId:artifactId[:classifier]:type:version<空格>sha256
//
// 每行恰好一个空格分隔坐标与 64 位小写十六进制摘要；坐标内不得再含空白。
// classifier 可省略（三元组 group:artifact:type:version）；有 classifier 时为五段。
// 覆盖 java-inputs.sh prepare 产出的本地仓库全部构件（含插件、插件依赖、parent/BOM pom、
// frontend 插件下载的 Node 发行包）。下列非构件文件不参与摘要、也不影响构建结果：
// _remote.repositories、*.lastUpdated、*.part、*.lock、*.tmp、maven-metadata*.xml、
// resolver-status.properties、以及同目录下的 .sha1/.md5 旁路校验文件。其余普通文件推不出坐标、
// 以及符号链接和特殊文件一律失败。Maven 发行包不在本地仓库里，由 maven-wrapper.properties 的
// distributionSha256Sum 钉定（wrapper 下载时核对），不进本文件。

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const mavenDepsLockRel = "services/maven-deps.sha256"

// mavenLockEntry 是锁文件中的一行。
type mavenLockEntry struct {
	Coord  string // groupId:artifactId[:classifier]:type:version
	SHA256 string
}

// parseMavenDepsLock 解析锁文件字节；格式错、重复坐标、摘要非 64 位十六进制均失败。
func parseMavenDepsLock(raw []byte) ([]mavenLockEntry, error) {
	var out []mavenLockEntry
	seen := map[string]bool{}
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 64<<10), 1<<20)
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		coord, sum, ok := strings.Cut(line, " ")
		if !ok || strings.Contains(sum, " ") || coord == "" {
			return nil, fmt.Errorf("maven 锁文件第 %d 行格式无效（期望 坐标 sha256）", lineNo)
		}
		if !hex64.MatchString(sum) {
			return nil, fmt.Errorf("maven 锁文件第 %d 行摘要不是 64 位十六进制", lineNo)
		}
		if err := validateMavenCoord(coord); err != nil {
			return nil, fmt.Errorf("maven 锁文件第 %d 行：%w", lineNo, err)
		}
		if seen[coord] {
			return nil, fmt.Errorf("maven 锁文件重复坐标 %s", coord)
		}
		seen[coord] = true
		out = append(out, mavenLockEntry{Coord: coord, SHA256: sum})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateMavenCoord(coord string) error {
	parts := strings.Split(coord, ":")
	switch len(parts) {
	case 4, 5:
	default:
		return fmt.Errorf("坐标须为 group:artifact[:classifier]:type:version，得到 %q", coord)
	}
	for i, p := range parts {
		if p == "" || strings.ContainsAny(p, " \t\r\n") {
			return fmt.Errorf("坐标第 %d 段为空或含空白", i+1)
		}
	}
	return nil
}

// mavenLockNonEmpty 源码里锁文件存在且至少有一条有效条目。
func mavenLockNonEmpty(source string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(mavenDepsLockRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	entries, err := parseMavenDepsLock(raw)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// mavenArtifactFile 判定本地仓库中的普通文件是否为参与摘要的构件；其余普通文件都必须能推出
// 坐标（见 listMavenRepoArtifacts），推不出即失败。规则与 java-inputs.sh 的 maven_lock_list 相同。
func mavenArtifactFile(name string) bool {
	switch {
	case name == "_remote.repositories",
		name == "resolver-status.properties",
		strings.HasSuffix(name, ".lastUpdated"),
		strings.HasSuffix(name, ".part"),
		strings.HasSuffix(name, ".lock"),
		strings.HasSuffix(name, ".tmp"),
		strings.HasSuffix(name, ".sha1"),
		strings.HasSuffix(name, ".md5"),
		strings.HasPrefix(name, "maven-metadata") && (strings.HasSuffix(name, ".xml") || strings.Contains(name, ".xml.")):
		return false
	}
	return true
}

// mavenCoordFromRepoPath 从 repository 相对路径推出坐标。
// 路径形如 group/path/artifact/version/artifact-version[-classifier].ext
func mavenCoordFromRepoPath(rel string) (string, bool) {
	rel = filepath.ToSlash(rel)
	dir := filepath.ToSlash(filepath.Dir(rel))
	base := filepath.Base(rel)
	parts := strings.Split(dir, "/")
	if len(parts) < 3 {
		return "", false
	}
	version := parts[len(parts)-1]
	artifact := parts[len(parts)-2]
	group := strings.Join(parts[:len(parts)-2], ".")
	prefix := artifact + "-" + version
	if !strings.HasPrefix(base, prefix) {
		return "", false
	}
	rest := base[len(prefix):] // 例如 .jar / -sources.jar / -linux-x64.tar.gz
	if rest == "" || group == "" {
		return "", false
	}
	classifier, typ := "", ""
	if strings.HasPrefix(rest, "-") {
		// -classifier.type 或 -classifier.复合扩展
		rest = rest[1:]
		dot := strings.IndexByte(rest, '.')
		if dot <= 0 || dot == len(rest)-1 {
			return "", false
		}
		classifier, typ = rest[:dot], rest[dot+1:]
	} else if strings.HasPrefix(rest, ".") {
		typ = rest[1:]
	} else {
		return "", false
	}
	if typ == "" || strings.ContainsAny(typ, "/\\") {
		return "", false
	}
	if classifier != "" {
		return group + ":" + artifact + ":" + classifier + ":" + typ + ":" + version, true
	}
	return group + ":" + artifact + ":" + typ + ":" + version, true
}

// listMavenRepoArtifacts 枚举本地仓库中的构件坐标 → 文件路径。
func listMavenRepoArtifacts(repo string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			// 符号链接或特殊文件可能指向仓库外，Maven 会照读：一律拒绝，不悄悄跳过。
			return fmt.Errorf("Maven 本地仓库含符号链接或特殊文件，拒绝核对：%s", p)
		}
		if !mavenArtifactFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return err
		}
		if strings.ContainsAny(rel, " \t\r\n") {
			return fmt.Errorf("Maven 本地仓库文件名含空白：%s", p)
		}
		coord, ok := mavenCoordFromRepoPath(rel)
		if !ok {
			return fmt.Errorf("无法识别的 Maven 仓库文件（推不出坐标），拒绝核对：%s", filepath.ToSlash(rel))
		}
		if prior, exists := out[coord]; exists {
			return fmt.Errorf("仓库内坐标重复 %s（%s 与 %s）", coord, prior, p)
		}
		out[coord] = p
		return nil
	})
	return out, err
}

// mavenLockDiff 描述核对差异。
type mavenLockDiff struct {
	Missing  []string // 锁有、仓库无
	Extra    []string // 仓库有、锁无
	Mismatch []string // 摘要不符（坐标）
}

func (d mavenLockDiff) empty() bool {
	return len(d.Missing) == 0 && len(d.Extra) == 0 && len(d.Mismatch) == 0
}

func (d mavenLockDiff) Error() string {
	var b strings.Builder
	b.WriteString("Maven 锁文件与本地仓库不符：")
	if len(d.Missing) > 0 {
		fmt.Fprintf(&b, " 缺少 %d 项（%s）", len(d.Missing), limitList(d.Missing, 20))
	}
	if len(d.Extra) > 0 {
		fmt.Fprintf(&b, " 多出 %d 项（%s）", len(d.Extra), limitList(d.Extra, 20))
	}
	if len(d.Mismatch) > 0 {
		fmt.Fprintf(&b, " 摘要不符 %d 项（%s）", len(d.Mismatch), limitList(d.Mismatch, 20))
	}
	return b.String()
}

// verifyMavenRepoAgainstLock 用锁文件逐个核对仓库构件。
func verifyMavenRepoAgainstLock(repo string, lock []mavenLockEntry) error {
	want := map[string]string{}
	for _, e := range lock {
		want[e.Coord] = e.SHA256
	}
	got, err := listMavenRepoArtifacts(repo)
	if err != nil {
		return err
	}
	var diff mavenLockDiff
	for coord, sum := range want {
		path, ok := got[coord]
		if !ok {
			diff.Missing = append(diff.Missing, coord)
			continue
		}
		h, err := digest(path)
		if err != nil {
			return err
		}
		if h != sum {
			diff.Mismatch = append(diff.Mismatch, coord)
		}
	}
	for coord := range got {
		if _, ok := want[coord]; !ok {
			diff.Extra = append(diff.Extra, coord)
		}
	}
	sort.Strings(diff.Missing)
	sort.Strings(diff.Extra)
	sort.Strings(diff.Mismatch)
	if !diff.empty() {
		return diff
	}
	return nil
}

// generateMavenDepsLock 从本地仓库枚举构件并生成锁文件内容（稳定排序）。
func generateMavenDepsLock(repo string) ([]byte, error) {
	arts, err := listMavenRepoArtifacts(repo)
	if err != nil {
		return nil, err
	}
	coords := make([]string, 0, len(arts))
	for c := range arts {
		coords = append(coords, c)
	}
	sort.Strings(coords)
	var b bytes.Buffer
	b.WriteString("# Auriga Maven 依赖锁文件（services/maven-deps.sha256）\n")
	b.WriteString("# 格式：groupId:artifactId[:classifier]:type:version<空格>sha256\n")
	b.WriteString("# 覆盖 java-inputs.sh prepare 产出的本地仓库全部构件；非构件文件（_remote.repositories、\n")
	b.WriteString("# *.lastUpdated、maven-metadata*.xml、*.sha1/*.md5 等）不参与摘要、不影响构建。\n")
	b.WriteString("# 生成：deploy/nas/offline-build/java-inputs.sh hash-lock <prepare 输出目录>\n")
	for _, c := range coords {
		sum, err := digest(arts[c])
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s %s\n", c, sum)
	}
	return b.Bytes(), nil
}

// verifyImportedJavaAgainstLock 导入的 java 条目须与源码锁文件一致（有锁时）。
func verifyImportedJavaAgainstLock(source, entryDir string) error {
	raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(mavenDepsLockRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("源码缺少 %s，拒绝导入未钉定的 Java 依赖；请先生成锁文件", mavenDepsLockRel)
		}
		return err
	}
	lock, err := parseMavenDepsLock(raw)
	if err != nil {
		return err
	}
	if len(lock) == 0 {
		return fmt.Errorf("%s 为空，拒绝导入 Java 依赖", mavenDepsLockRel)
	}
	repo := filepath.Join(entryDir, "repository")
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return fmt.Errorf("Java 依赖条目缺少 repository/ 目录")
	}
	return verifyMavenRepoAgainstLock(repo, lock)
}
