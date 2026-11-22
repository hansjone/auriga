package main

// Python（operator）requirements 哈希钉定（设计 D5 / 统一现场部署第 8 步）。
//
// 要求 deploy/nas/test/admin-bootstrap-requirements.txt 中每个包规范行都钉版本，
// 并带至少一个 --hash=sha256:<64hex>（可跨行续写）。全部满足后才允许自动补。

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const pythonRequirementsRel = "deploy/nas/test/admin-bootstrap-requirements.txt"

var (
	pipHashFlag = regexp.MustCompile(`(?i)--hash=sha256:([0-9a-f]{64})\b`)
	pipReqStart = regexp.MustCompile(`(?i)^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*==\s*([^\s\\]+)`)
)

// pythonRequirementsHashed 判定 requirements 是否全部钉版本并带 sha256 哈希。
func pythonRequirementsHashed(source string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(pythonRequirementsRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return requirementsFullyHashed(raw)
}

// requirementsFullyHashed 解析 pip requirements 文本：每个包行须 == 钉版本且带哈希。
func requirementsFullyHashed(raw []byte) (bool, error) {
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 64<<10), 1<<20)
	lineNo := 0
	var cur strings.Builder
	var curStart int
	flush := func() error {
		text := strings.TrimSpace(cur.String())
		cur.Reset()
		if text == "" || strings.HasPrefix(text, "#") {
			return nil
		}
		// 选项行（仅 --hash / -r 等）在续行合并后应挂在包规范上；孤立选项拒绝。
		if strings.HasPrefix(text, "-") {
			return fmt.Errorf("requirements 第 %d 行起：孤立选项，须写在包规范行上", curStart)
		}
		m := pipReqStart.FindStringSubmatch(text)
		if m == nil {
			return fmt.Errorf("requirements 第 %d 行起：须为 name==version（已钉版本）", curStart)
		}
		if !pipHashFlag.MatchString(text) {
			return fmt.Errorf("requirements 第 %d 行起：%s==%s 缺少 --hash=sha256:…", curStart, m[1], m[2])
		}
		// 校验每个 --hash 的摘要长度（正则已保证 64 hex）。
		for _, hm := range pipHashFlag.FindAllStringSubmatch(text, -1) {
			if !hex64.MatchString(hm[1]) {
				return fmt.Errorf("requirements 第 %d 行起：哈希不是 64 位十六进制", curStart)
			}
		}
		return nil
	}
	pkgs := 0
	flushPkg := func() error {
		before := cur.Len()
		if err := flush(); err != nil {
			return err
		}
		if before > 0 {
			pkgs++
		}
		return nil
	}
	for s.Scan() {
		lineNo++
		line := s.Text()
		trimmed := strings.TrimSpace(line)
		if cur.Len() == 0 {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			curStart = lineNo
		}
		if strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
			cur.WriteString(strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(line, " \t"), "\\")))
			cur.WriteByte(' ')
			continue
		}
		cur.WriteString(trimmed)
		if err := flushPkg(); err != nil {
			return false, err
		}
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	if cur.Len() > 0 {
		if err := flushPkg(); err != nil {
			return false, err
		}
	}
	if pkgs == 0 {
		return false, nil
	}
	return true, nil
}

// listPythonWheels 枚举 packages/ 下的 .whl 为依赖条目（名称=文件名，版本留空）。
func listPythonWheels(ctx context.Context, dir string) (map[string]depEntry, error) {
	out := map[string]depEntry{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if cancelled := ctx.Err(); cancelled != nil {
			return nil, cancelled
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".whl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		ent := depEntry{Ecosystem: "python", Name: e.Name(), path: p}
		out[depKey(ent)] = ent
	}
	return out, nil
}
