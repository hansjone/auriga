package main

// Dockerfile.assembly 的静态检查（P4 与纵深防御）。
//
// deploy 用 docker build --network=none --pull=false 装配镜像，但这两个参数挡不住：
//   - 解析器指令 syntax（BuildKit 会去拉外部前端镜像）。BuildKit 的识别比较宽：解析前去掉
//     UTF-8 BOM 和首行 shebang；指令前缀可以是 # 也可以是 //；空白按 \s 认；还认整个文件是
//     JSON 对象 {"syntax": ...} 的形式。这里一律从严：任意一行（不只是开头的解析器指令区）
//     去掉任意 Unicode 空白后是 # 或 // 加 syntax=（不区分大小写）都拒绝；escape 指令会改变
//     续行符、让下面的指令检查失真，同样拒绝；文件以 { 开头或首行是 #! 也拒绝。
//   - FROM 引用未固定的镜像、COPY --from / RUN --mount from= 引用外部镜像、ADD <URL>：都会绕过
//     “基础镜像固定 digest、断网装配”。FROM 只允许：前面声明过的阶段、固定 digest 的镜像，或
//     ${<SVC>_BASE}（且只能出现在名为 <svc> 的阶段里——CLI 只给目标阶段传这一个构建参数，
//     其它阶段的 ARG 默认值是未固定的标签）。用 ${<SVC>_BASE} 的阶段不能被其它阶段引用。

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	dockerStageName = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
	dockerBaseArg   = regexp.MustCompile(`^\$(?:\{([A-Z]+)_BASE\}|([A-Z]+)_BASE)$`)
	dockerHeredoc   = regexp.MustCompile(`<<-?["']?[A-Za-z0-9_]`)
)

// checkDockerfile 拒绝解析器指令 syntax/escape 及其变体，再做指令检查。
func checkDockerfile(raw []byte) error {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return fmt.Errorf("Dockerfile.assembly 必须是不含 NUL 的 UTF-8 文本")
	}
	text := strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	if strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "{") {
		return fmt.Errorf("Dockerfile.assembly 以 { 开头：BuildKit 会把 JSON 形式的 {\"syntax\": …} 当作前端指令，deploy 拒绝")
	}
	lines := splitDockerLines(text)
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
		return fmt.Errorf("Dockerfile.assembly 首行是 #!：BuildKit 会跳过它再识别解析器指令，deploy 拒绝")
	}
	for i, line := range lines {
		key, ok := dockerDirectiveKey(line)
		if !ok {
			continue
		}
		switch key {
		case "syntax":
			return fmt.Errorf("Dockerfile.assembly 第 %d 行含 syntax= 解析器指令，deploy 拒绝拉取外部 BuildKit 前端", i+1)
		case "escape":
			return fmt.Errorf("Dockerfile.assembly 第 %d 行含 escape= 解析器指令，deploy 拒绝（会改变续行符）", i+1)
		}
	}
	return checkDockerInstructions(lines)
}

// splitDockerLines 按 \n、\r\n 与单独的 \r 分行（单独的 \r 也当换行，宁可多拒）。
func splitDockerLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

// dockerDirectiveKey：一行（去掉任意 Unicode 空白后）形如 #key= 或 //key= 时返回小写的 key。
func dockerDirectiveKey(line string) (string, bool) {
	t := strings.TrimLeftFunc(line, unicode.IsSpace)
	switch {
	case strings.HasPrefix(t, "#"):
		t = t[1:]
	case strings.HasPrefix(t, "//"):
		t = t[2:]
	default:
		return "", false
	}
	t = strings.TrimLeftFunc(t, unicode.IsSpace)
	end := 0
	for end < len(t) && (t[end] >= 'a' && t[end] <= 'z' || t[end] >= 'A' && t[end] <= 'Z' || t[end] >= '0' && t[end] <= '9') {
		end++
	}
	if end == 0 || !strings.HasPrefix(strings.TrimLeftFunc(t[end:], unicode.IsSpace), "=") {
		return "", false
	}
	return strings.ToLower(t[:end]), true
}

type dockerInstruction struct {
	line int
	text string
}

// dockerInstructions 合并续行（行尾 \），跳过空行与注释行（续行中间的注释行也跳过）。
func dockerInstructions(lines []string) []dockerInstruction {
	var out []dockerInstruction
	var cur strings.Builder
	start := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cur.Len() == 0 {
			start = i + 1
		}
		body := strings.TrimRightFunc(line, unicode.IsSpace)
		if strings.HasSuffix(body, "\\") {
			cur.WriteString(strings.TrimSuffix(body, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(body)
		out = append(out, dockerInstruction{line: start, text: cur.String()})
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, dockerInstruction{line: start, text: cur.String()})
	}
	return out
}

func checkDockerInstructions(lines []string) error {
	stages := map[string]bool{} // 阶段名 → 是否由 ${<SVC>_BASE} 而来
	known := map[string]bool{}
	for _, svc := range deployServices {
		known[svc] = true
	}
	stageRef := func(in dockerInstruction, name, what string) error {
		name = strings.ToLower(name)
		argBased, ok := stages[name]
		if !ok {
			return fmt.Errorf("Dockerfile.assembly 第 %d 行 %s 引用了未声明的阶段或外部镜像 %q，deploy 拒绝", in.line, what, name)
		}
		if argBased {
			return fmt.Errorf("Dockerfile.assembly 第 %d 行 %s 引用了用构建参数基础镜像的阶段 %q（只有目标阶段的构建参数由 CLI 固定），deploy 拒绝", in.line, what, name)
		}
		return nil
	}
	for _, in := range dockerInstructions(lines) {
		if dockerHeredoc.MatchString(in.text) {
			return fmt.Errorf("Dockerfile.assembly 第 %d 行使用 heredoc（<<），deploy 不支持", in.line)
		}
		fields := strings.Fields(in.text)
		args := fields[1:]
		switch strings.ToUpper(fields[0]) {
		case "ONBUILD":
			return fmt.Errorf("Dockerfile.assembly 第 %d 行使用 ONBUILD，deploy 拒绝", in.line)
		case "FROM":
			var rest []string
			for _, a := range args {
				if strings.HasPrefix(a, "--") {
					if strings.ToLower(a) != "--platform=linux/amd64" {
						return fmt.Errorf("Dockerfile.assembly 第 %d 行 FROM 只接受 --platform=linux/amd64（当前 %s）", in.line, a)
					}
					continue
				}
				rest = append(rest, a)
			}
			name := ""
			switch {
			case len(rest) == 1:
			case len(rest) == 3 && strings.EqualFold(rest[1], "AS"):
				name = strings.ToLower(rest[2])
				if !dockerStageName.MatchString(name) {
					return fmt.Errorf("Dockerfile.assembly 第 %d 行阶段名无效 %q", in.line, rest[2])
				}
				if _, dup := stages[name]; dup {
					return fmt.Errorf("Dockerfile.assembly 第 %d 行阶段名 %q 重复", in.line, name)
				}
			default:
				return fmt.Errorf("Dockerfile.assembly 第 %d 行 FROM 格式无法识别：%s", in.line, in.text)
			}
			image, argBased := rest[0], false
			if _, isStage := stages[strings.ToLower(image)]; isStage {
				if err := stageRef(in, image, "FROM"); err != nil {
					return err
				}
			} else if m := dockerBaseArg.FindStringSubmatch(image); m != nil {
				svc := strings.ToLower(m[1] + m[2])
				if !known[svc] || name != svc {
					return fmt.Errorf("Dockerfile.assembly 第 %d 行 FROM %s 只能用在名为 %s 的阶段（CLI 只为目标阶段固定 %s_BASE）", in.line, image, svc, strings.ToUpper(svc))
				}
				argBased = true
			} else if strings.Contains(image, "$") || !immutable.MatchString(image) || !strings.Contains(image, "@sha256:") {
				return fmt.Errorf("Dockerfile.assembly 第 %d 行 FROM %s 不是固定 digest 的镜像、配方的 ${<SVC>_BASE} 或已声明的阶段，deploy 拒绝", in.line, image)
			}
			if name != "" {
				stages[name] = argBased
			}
		case "COPY", "ADD":
			for _, a := range args {
				if !strings.HasPrefix(a, "--") {
					continue
				}
				if v, ok := cutFlag(a, "--from"); ok {
					if err := stageRef(in, v, strings.ToUpper(fields[0])+" --from"); err != nil {
						return err
					}
				}
			}
			if strings.EqualFold(fields[0], "ADD") {
				rest := strings.ToLower(strings.Join(args, " "))
				if strings.Contains(rest, "://") || strings.Contains(rest, "git@") {
					return fmt.Errorf("Dockerfile.assembly 第 %d 行 ADD 引用了远程地址（不受 --network=none 约束），deploy 拒绝", in.line)
				}
			}
		case "RUN":
			for _, a := range args {
				if !strings.HasPrefix(a, "--") {
					break
				}
				if v, ok := cutFlag(a, "--mount"); ok {
					for _, kv := range strings.Split(v, ",") {
						k, val, _ := strings.Cut(kv, "=")
						if strings.EqualFold(strings.TrimSpace(k), "from") {
							if err := stageRef(in, strings.TrimSpace(val), "RUN --mount from="); err != nil {
								return err
							}
						}
					}
				}
				if v, ok := cutFlag(a, "--network"); ok && v != "none" && v != "default" {
					return fmt.Errorf("Dockerfile.assembly 第 %d 行 RUN --network=%s，deploy 拒绝", in.line, v)
				}
				if v, ok := cutFlag(a, "--security"); ok && v != "sandbox" {
					return fmt.Errorf("Dockerfile.assembly 第 %d 行 RUN --security=%s，deploy 拒绝", in.line, v)
				}
			}
		}
	}
	return nil
}

// cutFlag 识别 --name=value（名字不区分大小写）。
func cutFlag(arg, name string) (string, bool) {
	k, v, ok := strings.Cut(arg, "=")
	if !ok || !strings.EqualFold(k, name) {
		return "", false
	}
	return v, true
}
