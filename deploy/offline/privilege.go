package main

// 经 sudo 调用的提权边界（设计 D12，遗留 P3）。
//
// 判定：
//   - 没有 SUDO_UID：root 直接运行，不受限（--operator 无作用）。
//   - 有 SUDO_UID 且调用者（SUDO_UID）是 site.json 登记的运行器账户 server.runner：一律受限，
//     --operator 被拒绝，无法绕过；而且只能运行 deploy、rollback、status、history。
//   - 有 SUDO_UID、调用者不是运行器（例如 sudo -i 的管理员）：
//     deploy、rollback 默认受限（D12：sudo 调用即走白名单），显式 --operator 才跳过白名单；
//     --operator 要求 site.json 已登记 server.runner 且能解析为本机账户（否则无法证明调用者
//     不是运行器，拒绝）。history 与其余旧命令（install、upgrade、trust、deps 等）保持原有
//     行为，只有运行器调用时受限；status 在 site.json 存在时对任何经 sudo 的调用都受限（不收
//     --data，防止漏登记运行器时伪造 state 让 root 执行 status.sh，见 restrictedStatus）。
//
// 白名单由 CLI 逐个参数执行（只认精确写法，不接受单横线、重复与 -- 终止符）：
//   - deploy：恰好一个 40 位小写十六进制提交号，可选 --full、--services <a,b>（或
//     --services=<a,b>）、--dry-run、--json、--backup-db；
//   - rollback：--to <序号>（或 --to=<序号>）、--json；
//   - history：--json；
//   - status：不接受参数（数据目录取 site.json 的 server.data）。

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

const operatorFlag = "--operator"

// lookupRunnerUID 把运行器账户名解析为 uid（可注入）。
var lookupRunnerUID = func(name string) (string, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return "", err
	}
	return u.Uid, nil
}

// loadSudoSiteConfig 读取固定路径的 site.json，用来判定经 sudo 的调用者是否为运行器；
// 文件不存在时返回 (nil, nil)（未登记运行器）。可注入。
var loadSudoSiteConfig = func(inv siteInvocation) (*siteConfig, error) {
	path := defaultSiteConfigPath()
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	}
	cfg, _, err := loadSiteConfig("", inv)
	return cfg, err
}

// splitOperator 去掉参数里的 --operator（只认这一种写法），返回其余参数与是否出现过。
func splitOperator(args []string) ([]string, bool, error) {
	var rest []string
	seen := false
	for _, a := range args {
		if a == operatorFlag {
			if seen {
				return nil, false, errors.New("--operator 重复")
			}
			seen = true
			continue
		}
		if strings.HasPrefix(a, operatorFlag+"=") || a == "-operator" || strings.HasPrefix(a, "-operator=") {
			return nil, false, fmt.Errorf("--operator 不接受取值或单横线写法（当前 %q）", a)
		}
		rest = append(rest, a)
	}
	return rest, seen, nil
}

// callerIsRunner：经 sudo 时调用者是否为登记的运行器。registered 表示 site.json 登记了
// server.runner；解析账户失败时返回错误（调用方按失败关闭处理）。
func callerIsRunner(inv siteInvocation, cfg *siteConfig) (isRunner, registered bool, err error) {
	if cfg == nil || cfg.Server == nil || cfg.Server.Runner == nil {
		return false, false, nil
	}
	uid, err := lookupRunnerUID(*cfg.Server.Runner)
	if err != nil {
		return false, true, fmt.Errorf("site.json 的 server.runner %q 无法解析为本机账户：%w", *cfg.Server.Runner, err)
	}
	return inv.Sudo && inv.SudoUID == uid, true, nil
}

// resolvePrivilege 判定 deploy、rollback 是否受限（见文件头）。
func resolvePrivilege(inv siteInvocation, cfg *siteConfig, operator bool) (bool, error) {
	if !inv.Sudo {
		return false, nil
	}
	isRunner, registered, err := callerIsRunner(inv, cfg)
	switch {
	case err != nil:
		if operator {
			return true, fmt.Errorf("%v；无法确认调用者不是运行器，--operator 不可用", err)
		}
		return true, nil
	case isRunner:
		if operator {
			return true, fmt.Errorf("调用者是 site.json 登记的运行器账户 %s：运行器发起的调用一律受限，不能用 --operator 跳过参数白名单", *cfg.Server.Runner)
		}
		return true, nil
	case !registered:
		if operator {
			return true, errors.New("site.json 没有登记运行器账户 server.runner，无法确认调用者不是运行器，--operator 不可用；请先登记 server.runner，或以 root 登录直接运行（不经 sudo）")
		}
		return true, nil
	}
	return !operator, nil
}

// runnerOnly 判定 status、history 是否受限：只有运行器调用时受限；读不到配置或解析不了
// 运行器账户时失败关闭（受限）。
func runnerOnly(inv siteInvocation, cfg *siteConfig) bool {
	if !inv.Sudo {
		return false
	}
	isRunner, _, err := callerIsRunner(inv, cfg)
	return err != nil || isRunner
}

// guardSudoCommand 在分派前执行：经 sudo 的运行器只能运行 deploy、rollback、status、history，
// 且不能带帮助参数。site.json 不存在时视为未登记运行器（旧的签名包站点经 sudo 运维照旧）；
// 存在但读不出来时失败关闭。
func guardSudoCommand(args []string, inv siteInvocation) error {
	if !inv.Sudo || len(args) == 0 {
		return nil
	}
	cfg, err := loadSudoSiteConfig(inv)
	if err != nil {
		return fmt.Errorf("经 sudo 调用时需要读取 %s 判定调用者是否为运行器账户，读取失败：%w", defaultSiteConfigPath(), err)
	}
	isRunner, _, err := callerIsRunner(inv, cfg)
	if err != nil {
		return fmt.Errorf("%v；经 sudo 的调用无法确认调用者身份，拒绝", err)
	}
	if !isRunner {
		return nil
	}
	switch args[0] {
	case "deploy", "rollback", "status", "history":
	default:
		return fmt.Errorf("运行器账户经 sudo 只能运行 deploy、rollback、status、history（当前 %q）", args[0])
	}
	for _, a := range args[1:] {
		if a == "-h" || a == "--help" || a == "-help" || a == "help" {
			return errors.New("运行器账户经 sudo 调用时不接受帮助参数；请不经 sudo 运行 auriga " + args[0] + " --help")
		}
	}
	return nil
}

func restrictedHint(cmd string) string {
	switch cmd {
	case "deploy":
		return "受限调用的 deploy 只接受一个 40 位小写十六进制提交号与 --full、--services <a,b>、--dry-run、--json、--backup-db"
	case "rollback":
		return "受限调用的 rollback 只接受 --to <序号> 与 --json"
	case "history":
		return "受限调用的 history 只接受 --json"
	}
	return "受限调用的 status 不接受参数（数据目录取 site.json 的 server.data）"
}

// restrictedArgs 按白名单逐个检查受限调用的参数。
func restrictedArgs(cmd string, args []string) error {
	reject := func(a string) error {
		return fmt.Errorf("受限调用（经 sudo 的运行器，或未加 --operator 的 sudo 调用）拒绝参数 %q；%s", a, restrictedHint(cmd))
	}
	bools := map[string]bool{}
	values := map[string]bool{}
	switch cmd {
	case "deploy":
		bools = map[string]bool{"--full": true, "--dry-run": true, "--json": true, "--backup-db": true}
		values = map[string]bool{"--services": true}
	case "rollback":
		bools = map[string]bool{"--json": true}
		values = map[string]bool{"--to": true}
	case "history":
		bools = map[string]bool{"--json": true}
	case "status":
	default:
		return fmt.Errorf("受限调用不允许子命令 %q", cmd)
	}
	seen := map[string]bool{}
	commits := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		switch {
		case bools[a]:
		case values[name] && hasValue:
			if value == "" {
				return reject(a)
			}
		case values[a]:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || args[i+1] == "" {
				return fmt.Errorf("受限调用的 %s 需要取值；%s", a, restrictedHint(cmd))
			}
			value = args[i+1]
			i++
		case cmd == "deploy" && commitPattern.MatchString(a):
			// parseDeploy 只认第一个或最后一个位置的提交号（flag 包遇到非 flag 参数即停止），
			// 白名单与之保持一致，避免放行后解析失败或语义不一。
			if i != 0 && i != len(args)-1 {
				return fmt.Errorf("受限调用的 deploy 提交号必须是第一个或最后一个参数（当前第 %d 个）；%s", i+1, restrictedHint(cmd))
			}
			commits++
			continue
		default:
			return reject(a)
		}
		if seen[name] {
			return fmt.Errorf("受限调用不接受重复参数 %s", name)
		}
		seen[name] = true
		if name == "--to" {
			if n, err := strconv.Atoi(value); err != nil || n < 1 || strconv.Itoa(n) != value {
				return fmt.Errorf("--to 需要正整数序号（当前 %q）", value)
			}
		}
	}
	if cmd == "deploy" && commits != 1 {
		return errors.New("受限调用的 deploy 必须给出恰好一个 40 位小写十六进制提交号（由 root 进程自己从配置的受信远端获取）")
	}
	return nil
}
