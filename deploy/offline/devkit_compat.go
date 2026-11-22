package main

// 套件清单格式兼容（devkit.json、devkit-catalog.json、delta.json）。
//
// 套件要覆盖一段时间内的源码，所以会出现“旧 auriga 读新套件”。规则：
//   - 每个字段在 devkitFieldFeature 里登记，三类：
//     devkitBaseline：本机制之前就有的字段，所有 auriga 都认识；
//     devkitInfo：之后新增的说明性字段，不认识的 auriga 可以忽略；
//     其他值：能力名。字段有值就会改变校验或构建行为，不认识的 auriga 必须拒绝。
//     能力字段的零值必须等于旧行为（判断“有没有用到”靠零值）。
//     非基线字段必须带 omitempty、结构体用指针，零值时整个键不出现（门禁检查）。
//   - 只有清单里真的出现了 devkitInfo 或能力字段，写出时才带顶层 "requires"
//     （只有说明性字段时为空数组）。否则清单与旧格式逐字节相同，旧 auriga 照常读取。
//   - 读到带 "requires" 的清单：列表里有不认识的能力就拒绝，并提示改用套件发行页
//     附带的 auriga；否则宽松解码，忽略不认识的说明性字段。
//   - 没有 "requires" 的清单按原来的严格模式读取，只把报错改得能看懂。
//   - 本机制之前的 auriga 读带 "requires" 的清单会报 unknown field "requires"，
//     这类旧版本只能改用套件发行页附带的 auriga。
//   - 套件格式只增不删：删字段或改字段含义必须换新基线、出整套新套件。
//   - 只有 versions 内嵌的 kit 不单独带 requires，能力统一汇总到 catalog 顶层。
// devkit_compat_test.go 会检查每个字段都已登记，新增字段漏登记时测试失败。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const (
	devkitBaseline = ""
	devkitInfo     = "info"
)

// devkitFieldFeature 以“Go 类型名.json 字段名”为键，值见文件头三类。
var devkitFieldFeature = map[string]string{
	"artifact.sha256":               "",
	"artifact.url":                  "",
	"buildSpec.bases":               "",
	"buildSpec.inputs":              "",
	"buildSpec.recipes":             "",
	"buildSpec.runtimeDependencies": "runtime-dependencies", // 固定 DSH 冷构建输入，影响构建，旧 CLI 必须拒绝
	"buildSpec.runtimeTooling":      "runtime-tooling",      // 固定 pnpm 原始 TGZ 输入，影响构建，旧 CLI 必须拒绝
	"buildSpec.tools":               "",
	"devkitCatalog.baseline":        "",
	"devkitCatalog.blobs":           "",
	"devkitCatalog.latest":          "",
	"devkitCatalog.requires":        "",
	"devkitCatalog.resetReason":     "",
	"devkitCatalog.schemaVersion":   "",
	"devkitCatalog.versions":        "",
	"devkitDelta.added":             "",
	"devkitDelta.baseSha256":        "",
	"devkitDelta.baseline":          "",
	"devkitDelta.requires":          "",
	"devkitDelta.reset":             "",
	"devkitDelta.schemaVersion":     "",
	"devkitDelta.targetSha256":      "",
	"devkitManifest.images":         "",
	"devkitManifest.locks":          "",
	"devkitManifest.release":        "",
	"devkitManifest.requires":       "",
	"devkitManifest.schemaVersion":  "",
	"devkitManifest.source":         "",
	"devkitVersion.commit":          "",
	"devkitVersion.files":           "",
	"devkitVersion.kit":             "",
	"image.archive":                 "",
	"image.ref":                     "",
	"image.tag":                     "",
	"kitImage.file":                 "",
	"kitImage.ref":                  "",
	"kitImage.role":                 "",
	"kitImage.tag":                  "",
	"lockInventory.digest":          "",
	"lockInventory.files":           "",
	"provenance.branch":             "",
	"provenance.builtAt":            "",
	"provenance.commit":             "",
	"provenance.dirty":              "",
	"provenance.siteChanges":        "",
	"provenance.snapshotSha256":     "",
	"provenance.upstreamVersion":    "",
	"provenance.version":            "",
	// runtimeDependencyInputs 的四项是 buildSpec.runtimeDependencies 的子字段，只在父键出现时才有；
	// 能力已由父字段承载（旧 CLI 见 runtime-dependencies 即拒绝），子字段登记为基线（它们是值结构体，也无法带 omitempty）。
	"runtimeDependencyInputs.corepack": "",
	"runtimeDependencyInputs.manifest": "",
	"runtimeDependencyInputs.npmCache": "",
	"runtimeDependencyInputs.webCache": "",
	"release.build":                    "",
	"release.formatMax":                devkitInfo, // N-1 兼容声明（2b），只影响站点回滚判定，旧 CLI 可忽略
	"release.desktop":                  "",
	"release.images":                   "",
	"release.nas":                      "",
	"release.schemaCompatFloor":        devkitInfo, // 同上
	"release.schemaVersion":            "",
	"release.seeds":                    "",
	"release.source":                   "",
	"release.version":                  "",
}

// devkitRoots 是三种清单文件的顶层类型，测试从这里遍历全部可达字段。
var devkitRoots = []any{devkitManifest{}, devkitCatalog{}, devkitDelta{}}

func devkitSupported(feature string) bool {
	if feature == devkitBaseline || feature == devkitInfo {
		return false
	}
	for _, f := range devkitFieldFeature {
		if f == feature {
			return true
		}
	}
	return false
}

// devkitFormatNeeds 返回清单是否用到了本机制之后新增的字段，以及用到的能力名（排序、去重）。
func devkitFormatNeeds(v any) (bool, []string) {
	seen := map[string]bool{}
	needs := walkDevkitValue(reflect.ValueOf(v), seen)
	out := []string{}
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return needs || len(out) > 0, out
}

// devkitRequires 只返回能力名（空时为空切片）。
func devkitRequires(v any) []string {
	_, out := devkitFormatNeeds(v)
	return out
}

func walkDevkitValue(v reflect.Value, seen map[string]bool) bool {
	needs := false
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			needs = walkDevkitValue(v.Elem(), seen) || needs
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			needs = walkDevkitValue(v.Index(i), seen) || needs
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			needs = walkDevkitValue(v.MapIndex(k), seen) || needs
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := jsonFieldName(field)
			if name == "" || name == "requires" {
				continue
			}
			value := v.Field(i)
			if !value.IsZero() {
				switch class := devkitFieldFeature[t.Name()+"."+name]; class {
				case devkitBaseline:
				case devkitInfo:
					needs = true
				default:
					seen[class] = true
				}
			}
			needs = walkDevkitValue(value, seen) || needs
		}
	}
	return needs
}

func jsonFieldName(f reflect.StructField) string {
	if !f.IsExported() {
		return ""
	}
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	return name
}

func stampDevkitRequires(target **[]string, v any) {
	*target = nil
	if needs, req := devkitFormatNeeds(v); needs {
		*target = &req
	}
}

// writeDevkitManifest 写 devkit.json（需要时顶层带 requires）。
func writeDevkitManifest(path string, kit devkitManifest) error {
	stampDevkitRequires(&kit.Requires, kit)
	return jsonFile(path, kit, 0644)
}

func writeDevkitCatalog(path string, cat devkitCatalog) error {
	versions := make(map[string]devkitVersion, len(cat.Versions))
	for v, entry := range cat.Versions {
		versions[v] = normalizeDevkitVersion(entry)
	}
	cat.Versions = versions
	stampDevkitRequires(&cat.Requires, cat)
	return jsonFile(path, cat, 0644)
}

func writeDeltaManifest(path string, delta devkitDelta, cat devkitCatalog) error {
	// 增量本身没有能力字段；合并时还要读目标清单，所以沿用目标清单的格式要求。
	stampDevkitRequires(&delta.Requires, cat)
	return jsonFile(path, delta, 0644)
}

// normalizeDevkitVersion 去掉内嵌 kit 的 requires（能力统一汇总在 catalog 顶层），
// 让新旧 auriga 生成的同一版本条目可以直接比较。
func normalizeDevkitVersion(entry devkitVersion) devkitVersion {
	entry.Kit.Requires = nil
	return entry
}

func sameDevkitVersion(a, b devkitVersion) bool {
	return reflect.DeepEqual(normalizeDevkitVersion(a), normalizeDevkitVersion(b))
}

const devkitNewerHint = "请改用该套件发行页附带的 auriga 引导程序，或把源码更新到套件对应版本后再运行"

const devkitJSONLimit = 4 << 20

// readDevkitJSON 读三种套件清单文件，规则见文件头注释。
func readDevkitJSON(path string, output any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, devkitJSONLimit+1))
	_ = f.Close()
	if err != nil {
		return err
	}
	return decodeDevkitJSON(filepath.Base(path), raw, output)
}

// decodeDevkitJSON 按 readDevkitJSON 的规则解析已读入内存的清单字节（调用方可先核对
// 这份字节的摘要，再用同一份字节解析）。
func decodeDevkitJSON(name string, raw []byte, output any) error {
	if len(raw) > devkitJSONLimit {
		return fmt.Errorf("%s 超过 %d 字节上限", name, devkitJSONLimit)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("%s 不是有效 JSON: %w", name, err)
	}
	var reqRaw json.RawMessage
	newFormat := false
	for key, value := range top {
		if strings.EqualFold(key, "requires") {
			if key != "requires" || newFormat {
				return fmt.Errorf("%s 的 requires 键无效", name)
			}
			reqRaw, newFormat = value, true
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if !newFormat {
		d.DisallowUnknownFields()
		if err := d.Decode(output); err != nil {
			if strings.Contains(err.Error(), "unknown field") {
				return fmt.Errorf("%s 含当前 auriga 不认识的字段（%v），说明套件比这个 auriga 新。%s", name, err, devkitNewerHint)
			}
			return err
		}
	} else {
		var features []string
		if err := json.Unmarshal(reqRaw, &features); err != nil {
			return fmt.Errorf("%s 的 requires 无效: %w", name, err)
		}
		var missing []string
		for _, feature := range features {
			if !devkitSupported(feature) {
				missing = append(missing, feature)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s 需要更新的 auriga（当前版本不支持：%s）。%s", name, strings.Join(missing, "、"), devkitNewerHint)
		}
		if err := d.Decode(output); err != nil {
			return err
		}
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return errors.New("JSON 尾部有多余数据")
	}
	return nil
}
