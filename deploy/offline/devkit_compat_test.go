package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func devkitReachableFields(roots []any) map[string]reflect.StructField {
	fields := map[string]reflect.StructField{}
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() == reflect.Interface || t == reflect.TypeOf(json.RawMessage{}) {
			fields["!无法检查的类型 "+t.String()] = reflect.StructField{}
			return
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		if t.Name() == "" {
			fields["!匿名结构体 "+t.String()] = reflect.StructField{}
			return
		}
		seen[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := jsonFieldName(f)
			if name == "" {
				continue
			}
			fields[t.Name()+"."+name] = f
			walk(f.Type)
		}
	}
	for _, root := range roots {
		walk(reflect.TypeOf(root))
	}
	return fields
}

// devkitFieldProblems 返回门禁问题：漏登记、过期登记，以及非基线字段写法不合规。
// 非基线字段（说明性或能力）必须带 omitempty 且不能是值类型结构体，
// 保证零值时整个键不出现在清单里，与“零值不写 requires”一致。
func devkitFieldProblems(roots []any, table map[string]string) (missing, stale, badShape []string) {
	reachable := devkitReachableFields(roots)
	for name, f := range reachable {
		class, ok := table[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if class == devkitBaseline {
			continue
		}
		_, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		omit := false
		for _, o := range strings.Split(opts, ",") {
			omit = omit || o == "omitempty"
		}
		if !omit || f.Type.Kind() == reflect.Struct || f.Type.Kind() == reflect.Array {
			badShape = append(badShape, name)
		}
	}
	for name := range table {
		if _, ok := reachable[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(badShape)
	return
}

// 门禁：套件清单可达的每个字段都必须在 devkitFieldFeature 登记，表里也不能有过期项，
// 非基线字段必须零值不输出。规则见 devkit_compat.go 文件头。
func TestDevkitFieldsClassified(t *testing.T) {
	missing, stale, badShape := devkitFieldProblems(devkitRoots, devkitFieldFeature)
	if len(missing) > 0 {
		t.Errorf("套件清单新字段未在 devkitFieldFeature 登记（见 devkit_compat.go 文件头规则）：\n%s", strings.Join(missing, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("devkitFieldFeature 有过期登记（套件格式只增不删，删字段须换新基线）：\n%s", strings.Join(stale, "\n"))
	}
	if len(badShape) > 0 {
		t.Errorf("非基线字段必须带 omitempty 且不能是值类型结构体/数组（改用指针），否则零值也会写进清单：\n%s", strings.Join(badShape, "\n"))
	}
}

type gateProbeInner struct {
	X int `json:"x"`
}

type gateProbe struct {
	Base      string          `json:"base"`
	NoOmit    string          `json:"noOmit"`
	ValStruct gateProbeInner  `json:"valStruct,omitempty"`
	Good      *gateProbeInner `json:"good,omitempty"`
	Anon      struct {
		Y int `json:"y"`
	} `json:"anon"`
}

// 门禁本身要能拦住这些写法。
func TestDevkitFieldGateCatchesBadShapes(t *testing.T) {
	table := map[string]string{
		"gateProbe.base":      devkitBaseline,
		"gateProbe.noOmit":    devkitInfo,
		"gateProbe.valStruct": "probeFeature",
		"gateProbe.good":      devkitInfo,
		"gateProbe.anon":      devkitBaseline,
		"gateProbeInner.x":    devkitBaseline,
	}
	missing, _, badShape := devkitFieldProblems([]any{gateProbe{}}, table)
	if !reflect.DeepEqual(badShape, []string{"gateProbe.noOmit", "gateProbe.valStruct"}) {
		t.Fatalf("应拦下无 omitempty 与值类型结构体: %v", badShape)
	}
	if len(missing) != 1 || !strings.HasPrefix(missing[0], "!匿名结构体") {
		t.Fatalf("应拦下匿名结构体: %v", missing)
	}
}

func writeTestFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadDevkitJSONCompat(t *testing.T) {
	// 新格式 + 不认识的说明性字段：忽略。
	var d devkitDelta
	path := writeTestFile(t, "delta.json", `{"schemaVersion":1,"requires":[],"baseline":"0.2.0","futureNote":{"x":1},"added":["a"]}`)
	if err := readDevkitJSON(path, &d); err != nil || d.Baseline != "0.2.0" || len(d.Added) != 1 {
		t.Fatalf("新格式说明性字段应被忽略: %v %+v", err, d)
	}
	// 新格式 + 不认识的能力：拒绝并提示换新版 auriga。
	path = writeTestFile(t, "devkit.json", `{"schemaVersion":1,"requires":["futureBuildInputs"],"futureBuildInputs":{}}`)
	err := readDevkitJSON(path, &devkitManifest{})
	if err == nil || !strings.Contains(err.Error(), "futureBuildInputs") || !strings.Contains(err.Error(), "发行页附带的 auriga") {
		t.Fatalf("未知能力应明确拒绝: %v", err)
	}
	// 旧格式（无 requires）+ 未知字段：仍严格拒绝，但报错能看懂。
	path = writeTestFile(t, "devkit-catalog.json", `{"schemaVersion":1,"baseline":"0.2.0","runtimeTooling":{}}`)
	err = readDevkitJSON(path, &devkitCatalog{})
	if err == nil || !strings.Contains(err.Error(), "套件比这个 auriga 新") {
		t.Fatalf("旧格式未知字段应严格拒绝并给出提示: %v", err)
	}
	// 旧格式已知字段：照常读取。
	path = writeTestFile(t, "delta.json", `{"schemaVersion":1,"baseline":"0.2.0","added":[]}`)
	if err := readDevkitJSON(path, &devkitDelta{}); err != nil {
		t.Fatal(err)
	}
	// requires 类型错误、尾部多余数据：拒绝。
	for _, body := range []string{`{"requires":"x"}`, `{"requires":[]} {}`} {
		if err := readDevkitJSON(writeTestFile(t, "bad.json", body), &devkitDelta{}); err == nil {
			t.Fatalf("应拒绝 %s", body)
		}
	}
}

func TestDevkitRequiresWrittenAndDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devkit.json")
	// 只用到基线字段：不写 requires，与旧格式一致，旧 auriga 照常读取。
	if err := writeDevkitManifest(path, devkitManifest{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "requires") {
		t.Fatalf("只有基线字段时不应写 requires:\n%s", raw)
	}
	var strict devkitManifest
	if err := readJSON(path, &strict); err != nil {
		t.Fatalf("旧版严格读取应能读新写出的基线清单: %v", err)
	}

	// 说明性新字段有值：写空 requires；能力字段有值：写能力名（含 versions 内嵌的）。
	defer func(old map[string]string) { devkitFieldFeature = old }(devkitFieldFeature)
	devkitFieldFeature = map[string]string{}
	for k, v := range defaultDevkitFields() {
		devkitFieldFeature[k] = v
	}
	devkitFieldFeature["devkitCatalog.resetReason"] = devkitInfo
	devkitFieldFeature["devkitVersion.commit"] = "testFeature"
	cat := devkitCatalog{Schema: 1, Versions: map[string]devkitVersion{"0.2.0": {}}}
	if needs, got := devkitFormatNeeds(cat); needs || len(got) != 0 {
		t.Fatalf("空值不应要求新格式: %v %v", needs, got)
	}
	cat.ResetReason = "x"
	if needs, got := devkitFormatNeeds(cat); !needs || len(got) != 0 {
		t.Fatalf("说明性新字段应要求新格式且无能力: %v %v", needs, got)
	}
	cat.Versions["0.2.0"] = devkitVersion{Commit: "x"}
	if got := devkitRequires(cat); !reflect.DeepEqual(got, []string{"testFeature"}) {
		t.Fatalf("嵌套能力未检出: %v", got)
	}
	catPath := filepath.Join(dir, "devkit-catalog.json")
	if err := writeDevkitCatalog(catPath, cat); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(catPath)
	if strings.Count(string(raw), `"requires"`) != 1 || !strings.Contains(string(raw), `"testFeature"`) {
		t.Fatalf("能力应只汇总在 catalog 顶层:\n%s", raw)
	}
	if err := readDevkitJSON(catPath, &devkitCatalog{}); err != nil {
		t.Fatalf("认识的能力应能读取: %v", err)
	}
}

func defaultDevkitFields() map[string]string {
	out := map[string]string{}
	for k, v := range devkitFieldFeature {
		out[k] = v
	}
	return out
}

// 旧 auriga 生成的条目（内嵌 kit 无 requires）与新生成的同版本条目必须判为相同。
func TestSameDevkitVersionIgnoresNestedRequires(t *testing.T) {
	empty := []string{}
	a := devkitVersion{Commit: "c", Kit: devkitManifest{Schema: 1}}
	b := devkitVersion{Commit: "c", Kit: devkitManifest{Schema: 1, Requires: &empty}}
	if !sameDevkitVersion(a, b) {
		t.Fatal("内嵌 requires 不应影响同版本比较")
	}
	b.Commit = "d"
	if sameDevkitVersion(a, b) {
		t.Fatal("内容不同必须判为不同")
	}
}

func TestReadDevkitJSONRejectsOddRequiresAndOversize(t *testing.T) {
	for _, body := range []string{`{"Requires":["futureX"]}`, `{"requires":[],"REQUIRES":["futureX"]}`} {
		if err := readDevkitJSON(writeTestFile(t, "devkit.json", body), &devkitManifest{}); err == nil {
			t.Fatalf("大小写变体 requires 应拒绝: %s", body)
		}
	}
	big := `{"schemaVersion":1,"x":"` + strings.Repeat("a", devkitJSONLimit) + `"}`
	err := readDevkitJSON(writeTestFile(t, "devkit.json", big), &devkitManifest{})
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超限应明确报错: %v", err)
	}
}

// 0.2.0 预发布套件已带 runtimeTooling/runtimeDependencies 但没有 requires：
// 新 CLI 必须按旧格式严格读出；重写时只多出顶层 requires，其余字节不变。
func TestDevkitLegacyRuntimeFieldsReadAndRewrite(t *testing.T) {
	sum := strings.Repeat("a", 64)
	kit := devkitManifest{Schema: 1, Source: &provenance{}, Release: release{Schema: 1, Version: "0.2.0", Build: &buildSpec{
		RuntimeTooling: &artifact{URL: "runtime-tooling.tgz", SHA256: sum},
		RuntimeDependencies: &runtimeDependencyInputs{
			Manifest: artifact{URL: "runtime-dependencies/runtime-dependencies.json", SHA256: sum},
			Corepack: artifact{URL: "runtime-dependencies/corepack.tgz", SHA256: sum},
			WebCache: artifact{URL: "runtime-dependencies/web-cache.tar.gz", SHA256: sum},
			NpmCache: artifact{URL: "runtime-dependencies/npm-cache.tar.gz", SHA256: sum},
		},
	}}}
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy", "devkit.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := jsonFile(legacy, kit, 0644); err != nil { // 不经过 stamp，等同旧版写出
		t.Fatal(err)
	}
	var read devkitManifest
	if err := readDevkitJSON(legacy, &read); err != nil {
		t.Fatalf("无 requires 的预发布清单应按旧格式读通: %v", err)
	}
	if got := devkitRequires(read); !reflect.DeepEqual(got, []string{"runtime-dependencies", "runtime-tooling"}) {
		t.Fatalf("能力检出不对: %v", got)
	}
	rewritten := filepath.Join(dir, "devkit.json")
	if err := writeDevkitManifest(rewritten, read); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(legacy)
	after, _ := os.ReadFile(rewritten)
	block := "  \"requires\": [\n    \"runtime-dependencies\",\n    \"runtime-tooling\"\n  ],\n"
	if !strings.Contains(string(after), block) || strings.Replace(string(after), block, "", 1) != string(before) {
		t.Fatalf("重写应只多出顶层 requires:\n%s", after)
	}
	// 重写结果本 CLI 仍应能读通（requires 里的能力都已登记）。
	if err := readDevkitJSON(rewritten, &devkitManifest{}); err != nil {
		t.Fatalf("新 CLI 应能读重写结果: %v", err)
	}
}
