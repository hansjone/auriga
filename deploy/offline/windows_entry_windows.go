package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func windowsDispatch(args []string) error {
	b, rest, err := parseWindowsBackend(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return errors.New("缺少命令")
	}
	var result error
	switch rest[0] {
	case "verify":
		result = verifyCommand(rest[1:])
	case "unpack":
		result = unpackCommand(rest[1:])
	case "changes":
		result = changesCommand(rest[1:])
	case "bundle":
		result = b.build(rest[1:])
	case "devkit":
		if len(rest) < 2 || rest[1] != "verify" && rest[1] != "merge" {
			return errors.New("当前Windows支持devkit verify/merge；发行打包由Linux维护机执行")
		}
		result = devkit(rest[1:])
		if result == nil && rest[1] == "verify" {
			source, err := os.Getwd()
			if err != nil {
				return err
			}
			result = b.doctor(source, buildBudget{4096, 2})
		}
	case "doctor":
		if len(rest) > 2 || len(rest) == 2 && rest[1] != "--build" {
			return errors.New("当前Windows doctor检查本机构建环境；部署诊断请在Linux服务器运行")
		}
		source, err := os.Getwd()
		if err != nil {
			return err
		}
		result = b.doctor(source, buildBudget{4096, 2})
	default:
		return errors.New("当前Windows仅提供离线开发命令；install/upgrade/backup/status请在Linux服务器执行")
	}
	if result != nil && (os.IsPermission(result) || strings.Contains(strings.ToLower(result.Error()), "being used") || strings.Contains(result.Error(), "进程")) {
		return fmt.Errorf("%w；若文件被Defender/杀软或编辑器占用，先结束占用后重试，不关闭安全防护", result)
	}
	return result
}
