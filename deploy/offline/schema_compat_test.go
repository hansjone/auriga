package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeSchemaCompatFloor(t *testing.T) {
	parent := strings.Repeat("a", 40)
	headParent := strings.Repeat("b", 40)
	if got := ComputeSchemaCompatFloor("", parent, headParent, false); got != parent {
		t.Fatalf("无破坏性应继承：%s", got)
	}
	if got := ComputeSchemaCompatFloor("", parent, headParent, true); got != headParent {
		t.Fatalf("有破坏性应前移到父提交：%s", got)
	}
}

func TestValidateSchemaCompatAtRepoRoot(t *testing.T) {
	// 测试 cwd 为 deploy/offline，仓库根是上两级。
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchemaCompat(root); err != nil {
		t.Fatalf("仓库声明应自洽：%v", err)
	}
}

func TestDestructiveMarkerAndHints(t *testing.T) {
	if !HasDestructiveMarker("-- auriga:destructive 说明\nALTER TABLE t DROP COLUMN c;\n") {
		t.Fatal("应识别头注释标记")
	}
	if HasDestructiveMarker("ALTER TABLE t DROP COLUMN c;\n-- auriga:destructive\n") {
		t.Fatal("标记不在文件头时不应算")
	}
	hints := DestructiveHints("ALTER TABLE t DROP COLUMN c;")
	if len(hints) == 0 || !strings.Contains(hints[0], "DROP") {
		t.Fatalf("应提示 DROP：%v", hints)
	}
	dir := t.TempDir()
	sql := filepath.Join(dir, "V2__drop.sql")
	if err := os.WriteFile(sql, []byte("ALTER TABLE t DROP COLUMN c;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMigrationDestructiveness(dir, []string{sql}); err == nil {
		t.Fatal("未标记的 DROP 应失败")
	}
	if err := os.WriteFile(sql, []byte("-- auriga:destructive\nALTER TABLE t DROP COLUMN c;\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMigrationDestructiveness(dir, []string{sql}); err != nil {
		t.Fatalf("已标记应通过：%v", err)
	}
}

// TestCIN1Gate 是 CI 门禁入口（.github/scripts/n1-check-migrations.sh 设置环境变量后运行）：
// 声明自洽、floor 与迁移内容一致（相对真实父提交）、变更迁移的破坏性关键字辅助检查。
func TestCIN1Gate(t *testing.T) {
	root := os.Getenv("AURIGA_N1_REPO")
	parent := os.Getenv("AURIGA_N1_PARENT")
	if root == "" || parent == "" {
		t.Skip("仅 CI 设置 AURIGA_N1_REPO / AURIGA_N1_PARENT 时运行")
	}
	if !commitPattern.MatchString(parent) {
		t.Fatalf("AURIGA_N1_PARENT 须为 40 位提交号：%q", parent)
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		return string(out), err
	}
	if err := ValidateSchemaCompat(root); err != nil {
		t.Fatal(err)
	}
	head, err := LoadSchemaCompat(root)
	if err != nil {
		t.Fatal(err)
	}
	parentRaw, err := git("show", parent+":"+siteCompatRel)
	if err != nil {
		parentRaw = "" // 父提交没有该文件：本设计之前，floor 视为空
	}
	parentCompat, err := ParseSchemaCompat([]byte(parentRaw))
	if err != nil {
		t.Fatalf("父提交 %s：%v", parent, err)
	}
	list, err := git("diff", "--name-only", "--diff-filter=AMR", "-z", parent, "HEAD", "--",
		":(glob)services/**/src/main/resources/db/**/*.sql", ":(glob)services/common/src/main/resources/auriga/db/common/*.sql")
	if err != nil {
		t.Fatalf("列出变更迁移失败：%v", err)
	}
	var changed []string
	for _, p := range strings.Split(list, "\x00") {
		if p != "" {
			changed = append(changed, p)
		}
	}
	t.Logf("父提交 %s；变更迁移 %d 个：%v", parent, len(changed), changed)
	destructive, err := CommitHasDestructiveMigrations(root, changed)
	if err != nil {
		t.Fatal(err)
	}
	isAncestor := func(a, b string) (bool, error) {
		if _, err := git("cat-file", "-e", a+"^{commit}"); err != nil {
			return false, fmt.Errorf("提交 %s 不在仓库里", a)
		}
		_, err := git("merge-base", "--is-ancestor", a, b)
		return err == nil, nil
	}
	if err := ValidateFloorAgainstParent(head.SchemaCompatFloor, parentCompat.SchemaCompatFloor, parent, destructive, isAncestor); err != nil {
		t.Fatal(err)
	}
	if len(changed) > 0 {
		if err := ValidateMigrationDestructiveness(root, changed); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateFloorAgainstParent(t *testing.T) {
	a, b, c, d := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	// 线性历史 a → b → c（c 为父提交），d 不相关。
	order := map[string]int{a: 1, b: 2, c: 3}
	isAncestor := func(x, y string) (bool, error) {
		ox, okx := order[x]
		oy, oky := order[y]
		if !okx || !oky {
			return false, fmt.Errorf("未知提交")
		}
		return ox <= oy, nil
	}
	cases := []struct {
		name               string
		head, parentFloor  string
		destructive, valid bool
	}{
		{"无破坏性继承空 floor", "", "", false, true},
		{"无破坏性继承非空 floor", a, a, false, true},
		{"无破坏性却改了 floor", c, a, false, false},
		{"无破坏性却清空 floor", "", a, false, false},
		{"破坏性前移到父提交", c, "", true, true},
		{"破坏性前移到父提交（父有 floor）", c, a, true, true},
		{"破坏性前移到父提交之前（main 已前进）", b, a, true, true},
		{"破坏性未前移", a, a, true, false},
		{"破坏性 floor 为空", "", "", true, false},
		{"破坏性 floor 晚于父提交/不相关", d, a, true, false},
		{"破坏性 floor 倒退", a, b, true, false},
	}
	for _, tc := range cases {
		err := ValidateFloorAgainstParent(tc.head, tc.parentFloor, c, tc.destructive, isAncestor)
		if (err == nil) != tc.valid {
			t.Errorf("%s：valid=%v err=%v", tc.name, tc.valid, err)
		}
	}
}

func TestParseSchemaCompat(t *testing.T) {
	if c, err := ParseSchemaCompat(nil); err != nil || c.SchemaCompatFloor != "" {
		t.Fatalf("父提交无文件应视为空 floor：%v", err)
	}
	if _, err := ParseSchemaCompat([]byte(`{"schemaCompatFloor":"x"}`)); err == nil {
		t.Fatal("非法 floor 应拒绝")
	}
	if _, err := ParseSchemaCompat([]byte(`{`)); err == nil {
		t.Fatal("非法 JSON 应拒绝")
	}
}

// 提升任一 formatMax 之前，必须先把对应流程接上数据目录格式版本写入（见 docs/dev/schema-compat.zh.md
// “数据目录当前格式”）。目前这些流程都不写 format-versions.json，缺键时回滚检查无从比较，
// 所以上限只能停在 1；接线完成后再改本测试。
func TestFormatMaxStaysAtOneUntilWritersWired(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadFormatMax(root)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range m {
		if n, err := parseFormatInt(v); err != nil || n != 1 {
			t.Fatalf("formatMax %s=%s：提升前先接线该流程的格式版本写入", k, v)
		}
	}
}

func TestWriteFormatVersion(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFormatVersion(dir, "materials", "2"); err != nil {
		t.Fatal(err)
	}
	if err := WriteFormatVersion(dir, "relay", "1"); err != nil {
		t.Fatal(err)
	}
	m, err := readFormats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m["materials"] != `"2"` && m["materials"] != "2" {
		// readFormats compact 后数字无引号
		if m["materials"] != "2" {
			t.Fatalf("materials=%q", m["materials"])
		}
	}
	if m["relay"] != "1" && m["relay"] != `"1"` {
		if m["relay"] != "1" {
			t.Fatalf("relay=%q", m["relay"])
		}
	}
}
