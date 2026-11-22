package main

// schemaCompatFloor 与格式上限（设计 D2 / 统一现场部署 2b）。
//
// 源码声明在 deploy/site/schema-compat.json 与 deploy/site/format-versions.json。
// 破坏性迁移在 SQL 文件头用 `-- auriga:destructive` 标记；CI（.github/workflows/n1-compat.yml）
// 用 ValidateFloorAgainstParent 校验 floor 与迁移一致，用关键字辅助检查未标记的可疑变更。

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 发行清单声明的「本版代码能读」的格式键（与数据目录 format-versions.json 的业务键对应）。
var requiredFormatKeys = []string{"materials", "collab-keys", "materials-upgrades", "relay"}

// SchemaCompat 对应 deploy/site/schema-compat.json。
type SchemaCompat struct {
	SchemaVersion     int               `json:"schemaVersion"`
	SchemaCompatFloor string            `json:"schemaCompatFloor"`
	FormatMax         map[string]string `json:"formatMax"`
}

// DestructiveHint 是未标破坏性、但关键字看起来像破坏性变更的迁移。
type DestructiveHint struct {
	Path   string
	Hints  []string
	Marked bool
}

var (
	destructiveMarkerRe = regexp.MustCompile(`(?i)^\s*--\s*auriga:destructive\b`)
	// 关键字辅助：真实门禁仍是 CI 回退测试；此处只抓明显破坏旧代码读写的写法。
	hintDropTableRe    = regexp.MustCompile(`(?i)\bDROP\s+(TABLE|COLUMN|TYPE|SCHEMA)\b`)
	hintRenameRe       = regexp.MustCompile(`(?i)\bRENAME\s+(COLUMN\s+\S+\s+)?TO\b|\bALTER\s+TABLE\s+\S+\s+RENAME\b`)
	hintAddUniqueRe    = regexp.MustCompile(`(?i)\bALTER\s+TABLE\b[^;]*\bADD\s+(CONSTRAINT\s+\S+\s+)?UNIQUE\b`)
	hintAddCheckRe     = regexp.MustCompile(`(?i)\bALTER\s+TABLE\b[^;]*\bADD\s+(CONSTRAINT\s+\S+\s+)?CHECK\b`)
	hintRLSTightenRe   = regexp.MustCompile(`(?i)\bALTER\s+POLICY\b|\bFORCE\s+ROW\s+LEVEL\s+SECURITY\b`)
	hintNotNullNoDefRe = regexp.MustCompile(`(?i)\bALTER\s+TABLE\b[^;]*\bADD\s+COLUMN\b[^;]*\bNOT\s+NULL\b`)
	hintHasDefaultRe   = regexp.MustCompile(`(?i)\bDEFAULT\b`)
)

const siteCompatRel = "deploy/site/schema-compat.json"
const siteFormatRel = "deploy/site/format-versions.json"

// ComputeSchemaCompatFloor：无破坏性继承 parentFloor；有破坏性则 floor=headParentCommit
// （本提交的父提交：本提交迁移之后，仍能跑在新数据上的最旧提交不早于父提交）。
// 贡献者据此填写 deploy/site/schema-compat.json；CI 用 ValidateFloorAgainstParent 复核。
func ComputeSchemaCompatFloor(repoRoot, parentFloor, headParentCommit string, destructiveInThisCommit bool) string {
	_ = repoRoot
	if !destructiveInThisCommit {
		return parentFloor
	}
	return headParentCommit
}

// ValidateFloorAgainstParent 校验本提交声明的 floor 与迁移内容一致（设计 D2“CI 校验”）：
//
//   - 没有新增/改动带 `-- auriga:destructive` 的迁移：floor 必须与父提交声明的完全相同（继承）；
//   - 有：floor 必须前移——不为空、不同于父提交的 floor、是父提交本身或其祖先、且父提交的 floor
//     （非空时）是它的祖先。推荐值就是父提交（PR 的 main 头）；允许落后于真实父提交，是因为
//     合并前 main 可能前进（squash 后真实父提交变了），回退冒烟会对 floor 提交的镜像单独再测。
//
// isAncestor(a, b)：a 是否为 b 的祖先（或就是 b）。
func ValidateFloorAgainstParent(headFloor, parentFloor, parent string, destructive bool, isAncestor func(a, b string) (bool, error)) error {
	if !destructive {
		if headFloor != parentFloor {
			return fmt.Errorf("本提交没有新增带 -- auriga:destructive 的迁移，schemaCompatFloor 必须继承父提交的值 %q，实际 %q", parentFloor, headFloor)
		}
		return nil
	}
	if headFloor == "" || headFloor == parentFloor {
		return fmt.Errorf("本提交含破坏性迁移，schemaCompatFloor 必须前移到父提交 %s（父提交声明 %q，本提交 %q）", parent, parentFloor, headFloor)
	}
	ok, err := isAncestor(headFloor, parent)
	if err != nil {
		return fmt.Errorf("判定 floor %s 是否为父提交 %s 的祖先失败：%w", headFloor, parent, err)
	}
	if !ok {
		return fmt.Errorf("schemaCompatFloor %s 不是父提交 %s 本身或其祖先（应填父提交）", headFloor, parent)
	}
	if parentFloor != "" {
		ok, err := isAncestor(parentFloor, headFloor)
		if err != nil {
			return fmt.Errorf("判定父提交的 floor %s 是否早于新 floor 失败：%w", parentFloor, err)
		}
		if !ok {
			return fmt.Errorf("新 schemaCompatFloor %s 必须晚于父提交声明的 floor %s", headFloor, parentFloor)
		}
	}
	return nil
}

// ParseSchemaCompat 解析 schema-compat.json 内容（CI 读父提交的版本用）；空内容视为本设计之前，floor 为空。
func ParseSchemaCompat(raw []byte) (*SchemaCompat, error) {
	var c SchemaCompat
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s 无效：%w", siteCompatRel, err)
	}
	if c.SchemaCompatFloor != "" && !commitPattern.MatchString(c.SchemaCompatFloor) {
		return nil, fmt.Errorf("%s 的 schemaCompatFloor 须为空或 40 位小写十六进制提交号", siteCompatRel)
	}
	return &c, nil
}

// LoadSchemaCompat 读取源码声明的 schema-compat.json。
func LoadSchemaCompat(repoRoot string) (*SchemaCompat, error) {
	path := filepath.Join(repoRoot, filepath.FromSlash(siteCompatRel))
	var c SchemaCompat
	if err := readJSON(path, &c); err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%w", siteCompatRel, err)
	}
	return &c, nil
}

// LoadFormatMax 读取 deploy/site/format-versions.json（本版能读的最高格式版本）。
func LoadFormatMax(repoRoot string) (map[string]string, error) {
	path := filepath.Join(repoRoot, filepath.FromSlash(siteFormatRel))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败：%w", siteFormatRel, err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s 无效：%w", siteFormatRel, err)
	}
	return m, nil
}

// ValidateSchemaCompat 校验 deploy/site 声明文件自身一致（与 floor 计算输入无关）。
// 关键字破坏性检查见 ValidateMigrationDestructiveness（CI 对变更的 SQL 路径调用）。
func ValidateSchemaCompat(repoRoot string) error {
	c, err := LoadSchemaCompat(repoRoot)
	if err != nil {
		return err
	}
	if c.SchemaVersion != 1 {
		return fmt.Errorf("%s 的 schemaVersion 须为 1，实际 %d", siteCompatRel, c.SchemaVersion)
	}
	if c.SchemaCompatFloor != "" && !commitPattern.MatchString(c.SchemaCompatFloor) {
		return fmt.Errorf("%s 的 schemaCompatFloor 须为空或 40 位小写十六进制提交号", siteCompatRel)
	}
	formats, err := LoadFormatMax(repoRoot)
	if err != nil {
		return err
	}
	if err := checkFormatMaxKeys("format-versions.json", formats); err != nil {
		return err
	}
	if err := checkFormatMaxKeys("schema-compat.json formatMax", c.FormatMax); err != nil {
		return err
	}
	for k, v := range formats {
		if n, err := parseFormatInt(v); err != nil || n < 1 {
			return fmt.Errorf("%s 的 %s 须为正整数，实际 %q", siteFormatRel, k, v)
		}
	}
	if !sameMap(formats, c.FormatMax) {
		return fmt.Errorf("%s 的 formatMax 须与 %s 完全一致", siteCompatRel, siteFormatRel)
	}
	return nil
}

// ValidateMigrationDestructiveness 检查给定迁移路径（相对仓库根）：命中破坏性关键字却未标
// `-- auriga:destructive` 则失败。paths 为空时扫描全树（仅用于夹具测试；正式 CI 应只传变更文件）。
func ValidateMigrationDestructiveness(repoRoot string, paths []string) error {
	var hints []DestructiveHint
	if len(paths) == 0 {
		_, unmarked, err := ScanDestructiveMigrations(repoRoot)
		if err != nil {
			return err
		}
		hints = unmarked
	} else {
		for _, rel := range paths {
			p := rel
			if !filepath.IsAbs(p) {
				p = filepath.Join(repoRoot, rel)
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			slash := filepath.ToSlash(rel)
			if filepath.IsAbs(rel) {
				if r, err := filepath.Rel(repoRoot, rel); err == nil {
					slash = filepath.ToSlash(r)
				}
			}
			if HasDestructiveMarker(string(raw)) {
				continue
			}
			if h := DestructiveHints(string(raw)); len(h) > 0 {
				hints = append(hints, DestructiveHint{Path: slash, Hints: h})
			}
		}
	}
	if len(hints) == 0 {
		return nil
	}
	var bad []string
	for _, h := range hints {
		bad = append(bad, fmt.Sprintf("%s（%s）", h.Path, strings.Join(h.Hints, "、")))
	}
	return fmt.Errorf("以下迁移疑似破坏性变更但未标 -- auriga:destructive：\n  - %s", strings.Join(bad, "\n  - "))
}

func checkFormatMaxKeys(label string, m map[string]string) error {
	if m == nil {
		return fmt.Errorf("%s 缺少 format 映射", label)
	}
	for _, k := range requiredFormatKeys {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("%s 缺少键 %s", label, k)
		}
	}
	return nil
}

// ScanDestructiveMigrations 扫描仓库内 Flyway SQL，返回已标记路径与未标记但命中关键字的提示。
func ScanDestructiveMigrations(repoRoot string) (marked []string, unmarked []DestructiveHint, err error) {
	paths, err := listMigrationSQL(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		markedFile := HasDestructiveMarker(string(raw))
		hints := DestructiveHints(string(raw))
		if markedFile {
			marked = append(marked, rel)
			continue
		}
		if len(hints) > 0 {
			unmarked = append(unmarked, DestructiveHint{Path: rel, Hints: hints})
		}
	}
	return marked, unmarked, nil
}

// HasDestructiveMarker 判断 SQL 是否含 `-- auriga:destructive` 头注释。
func HasDestructiveMarker(content string) bool {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if destructiveMarkerRe.MatchString(line) {
			return true
		}
		// 只认文件开头连续注释/空行区；遇到非注释即停。
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			return false
		}
	}
	return false
}

// DestructiveHints 对单份 SQL 做关键字辅助检查（不含标记与否）。
func DestructiveHints(content string) []string {
	stripped := stripSQLLineComments(content)
	var hints []string
	if hintDropTableRe.MatchString(stripped) {
		hints = append(hints, "DROP TABLE/COLUMN/TYPE/SCHEMA")
	}
	if hintRenameRe.MatchString(stripped) {
		hints = append(hints, "RENAME")
	}
	if hintAddUniqueRe.MatchString(stripped) {
		hints = append(hints, "ADD UNIQUE")
	}
	if hintAddCheckRe.MatchString(stripped) {
		hints = append(hints, "ADD CHECK")
	}
	if hintRLSTightenRe.MatchString(stripped) {
		hints = append(hints, "RLS/POLICY")
	}
	if hintNotNullNoDefRe.MatchString(stripped) {
		// 粗检：命中 ADD COLUMN … NOT NULL 后再看同语句是否有 DEFAULT。
		for _, stmt := range splitSQLStatements(stripped) {
			if !hintNotNullNoDefRe.MatchString(stmt) {
				continue
			}
			if !hintHasDefaultRe.MatchString(stmt) {
				hints = append(hints, "ADD COLUMN NOT NULL 无 DEFAULT")
				break
			}
		}
	}
	return hints
}

func stripSQLLineComments(s string) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func splitSQLStatements(s string) []string {
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func listMigrationSQL(repoRoot string) ([]string, error) {
	var out []string
	services := filepath.Join(repoRoot, "services")
	if st, err := os.Stat(services); err == nil && st.IsDir() {
		err := filepath.WalkDir(services, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".sql") {
				return nil
			}
			slash := filepath.ToSlash(path)
			if strings.Contains(slash, "/resources/db/") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	common := filepath.Join(repoRoot, filepath.FromSlash("services/common/src/main/resources/auriga/db/common"))
	if entries, err := os.ReadDir(common); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
				continue
			}
			p := filepath.Join(common, e.Name())
			// Walk 可能已收录（若路径也含 resources/db）；去重。
			dup := false
			for _, existing := range out {
				if existing == p {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, p)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// CommitHasDestructiveMigrations 判断给定路径列表（相对或绝对）是否含破坏性标记。
func CommitHasDestructiveMigrations(repoRoot string, relPaths []string) (bool, error) {
	for _, rel := range relPaths {
		p := rel
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, rel)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return false, err
		}
		if HasDestructiveMarker(string(raw)) {
			return true, nil
		}
	}
	return false, nil
}

// WriteFormatVersion 合并写入数据目录的 format-versions.json（原子写）。
// version 为业务当前格式号（字符串形式的整数即可）；不会写入源码 formatMax。
func WriteFormatVersion(dataDir, key, version string) error {
	if key == "" {
		return errors.New("format-versions 键不能为空")
	}
	if err := noSymlinks(dataDir); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "format-versions.json")
	if err := noSymlinks(path); err != nil {
		return err
	}
	m := map[string]string{}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("format-versions.json 无效：%w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	m[key] = version
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'), 0600)
}

// formatVersionAtMost 判断当前格式值是否不超过上限（十进制整数；允许 JSON 紧凑数字或带引号）。
func formatVersionAtMost(current, max string) bool {
	a, errA := parseFormatInt(current)
	b, errB := parseFormatInt(max)
	if errA != nil || errB != nil {
		return false
	}
	return a <= b
}

func parseFormatInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	return strconv.Atoi(s)
}

// cloneFormatMax 复制格式上限表。
func cloneFormatMax(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// floorPtr 把 floor 写成 releaseManifest 用的指针（空字符串也保留，表示设计合入起点）。
func floorPtr(s string) *string { return &s }

// applySchemaCompatToRelease 把源码声明填进 bundle 的 release。
func applySchemaCompatToRelease(repoRoot string, r *release) error {
	c, err := LoadSchemaCompat(repoRoot)
	if err != nil {
		return err
	}
	r.SchemaCompatFloor = c.SchemaCompatFloor
	r.FormatMax = cloneFormatMax(c.FormatMax)
	return nil
}

// packageCompatFields 从签名包 offline-release.json 读取 floor 与格式上限（旧包无字段则空）。
func packageCompatFields(root string) (floor string, formatMax map[string]string) {
	var r release
	if err := readJSON(filepath.Join(root, "offline-release.json"), &r); err != nil {
		return "", nil
	}
	return r.SchemaCompatFloor, cloneFormatMax(r.FormatMax)
}

// loadCompatForManifest 读源码声明，供 deploy 写 releaseManifest / deployRecord。
func loadCompatForManifest(repoRoot string) (floor string, formatMax map[string]string, err error) {
	c, err := LoadSchemaCompat(repoRoot)
	if err != nil {
		return "", nil, err
	}
	return c.SchemaCompatFloor, cloneFormatMax(c.FormatMax), nil
}
