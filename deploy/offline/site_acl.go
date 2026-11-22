package main

// Windows 站点配置 / 依赖缓存 / 套件的 ACL 校验（设计 D9）。
//
// 判定逻辑与平台无关（便于在 Linux CI 上用构造数据单测），真实读取安全描述符见
// site_platform_windows.go（只用标准库 syscall，不引入外部模块）。
//
// 规则（对照 Linux trustedParent）：
//   - 被校验对象本身及其直接父目录（严格）：所有者必须是 Administrators 或 SYSTEM；
//     DACL 不能为空（空 DACL = 对所有人完全开放）；除 Administrators、SYSTEM、
//     TrustedInstaller 外任何主体（Users、Authenticated Users、Everyone、INTERACTIVE、
//     CREATOR OWNER、具体用户等）的允许 ACE 都不能含写类权限。
//   - 更上层祖先（宽松，相当于 Linux 带粘滞位的公共目录）：所有者可以是上述三者；
//     允许普通主体“新建文件/子目录、写扩展属性/属性”（C:\、C:\ProgramData 的默认
//     ACL 即如此），但不得有删除/改名子项（FILE_DELETE_CHILD）、删除自身（DELETE，
//     卷根目录除外）、改 DACL/所有者、GENERIC_ALL 等能替换路径的权限。
//   - 仅继承（INHERIT_ONLY）的 ACE 不作用于对象本身，跳过；拒绝 ACE 只会收紧权限，跳过。
//   - 其他允许类 ACE（对象 ACE、回调 ACE 等）授予非受信主体时一律拒绝。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sidLocalSystem     = "S-1-5-18"
	sidAdministrators  = "S-1-5-32-544"
	sidTrustedInstall  = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	aceTypeAllowed     = 0x0
	aceTypeDenied      = 0x1
	aceTypeAllowedObj  = 0x5
	aceTypeDeniedObj   = 0x6
	aceTypeAllowedCb   = 0x9
	aceTypeDeniedCb    = 0xA
	aceTypeAllowedCbOb = 0xB
	aceTypeDeniedCbOb  = 0xC
	aceFlagInheritOnly = 0x08

	accFileWriteData   = 0x00000002 // 目录上即“新建文件”
	accFileAppendData  = 0x00000004 // 目录上即“新建子目录”
	accFileWriteEA     = 0x00000010
	accFileDeleteChild = 0x00000040
	accFileWriteAttrs  = 0x00000100
	accDelete          = 0x00010000
	accWriteDAC        = 0x00040000
	accWriteOwner      = 0x00080000
	accMaximumAllowed  = 0x02000000
	accGenericAll      = 0x10000000
	accGenericWrite    = 0x40000000

	// 严格模式下任何一位都算“写”。
	aclStrictWrite = accFileWriteData | accFileAppendData | accFileWriteEA | accFileDeleteChild |
		accFileWriteAttrs | accDelete | accWriteDAC | accWriteOwner | accMaximumAllowed |
		accGenericAll | accGenericWrite
	// 祖先模式下只拒绝能删除/替换路径分量或改权限的位。
	aclAncestorWrite = accFileDeleteChild | accDelete | accWriteDAC | accWriteOwner |
		accMaximumAllowed | accGenericAll
)

type windowsACE struct {
	Type  byte
	Flags byte
	Mask  uint32
	SID   string // 无法解析主体的 ACE 类型为空
}

// windowsSecurity 是一次读取到的所有者与 DACL；NullDACL 表示空 DACL（含未设置 DACL）。
type windowsSecurity struct {
	Owner    string
	NullDACL bool
	ACEs     []windowsACE
}

type windowsACLMode int

const (
	aclStrict windowsACLMode = iota
	aclAncestor
	aclVolumeRoot
)

// 可注入点：测试用构造数据替换真实读取。
var (
	windowsSecurityOf = inspectWindowsSecurity
	windowsLstat      = os.Lstat
)

// trustedWindowsSID：ACE 主体为 SYSTEM、Administrators 或 TrustedInstaller 时其写权限不计。
func trustedWindowsSID(sid string) bool {
	return sid == sidLocalSystem || sid == sidAdministrators || sid == sidTrustedInstall
}

func windowsSIDName(sid string) string {
	switch sid {
	case "S-1-1-0":
		return "Everyone"
	case "S-1-5-11":
		return "Authenticated Users"
	case "S-1-5-32-545":
		return "Users"
	case "S-1-5-4":
		return "INTERACTIVE"
	case "S-1-3-0":
		return "CREATOR OWNER"
	case "S-1-3-4":
		return "OWNER RIGHTS"
	case "":
		return "未知主体"
	}
	return sid
}

// evaluateWindowsACL 按模式判定一份安全描述符；what/path 只用于报错。
func evaluateWindowsACL(what, path string, sec windowsSecurity, mode windowsACLMode) error {
	ownerOK := sec.Owner == sidAdministrators || sec.Owner == sidLocalSystem ||
		(mode != aclStrict && sec.Owner == sidTrustedInstall)
	if !ownerOK {
		return fmt.Errorf("%s所有者必须是 Administrators 或 SYSTEM %s（当前 %s）", what, path, windowsSIDName(sec.Owner))
	}
	if sec.NullDACL {
		return fmt.Errorf("%s没有 DACL（等于对所有人开放写权限）%s", what, path)
	}
	bad := uint32(aclStrictWrite)
	switch mode {
	case aclAncestor:
		bad = aclAncestorWrite
	case aclVolumeRoot:
		// 卷根目录不能被删除或改名，DELETE 不构成替换风险。
		bad = aclAncestorWrite &^ accDelete
	}
	for _, ace := range sec.ACEs {
		if ace.Flags&aceFlagInheritOnly != 0 {
			continue
		}
		switch ace.Type {
		case aceTypeDenied, aceTypeDeniedObj, aceTypeDeniedCb, aceTypeDeniedCbOb:
			continue
		case aceTypeAllowed:
			if trustedWindowsSID(ace.SID) {
				continue
			}
			if ace.Mask&bad != 0 {
				return fmt.Errorf("%s不能授予普通用户写权限 %s（%s 有写类权限 0x%08x）", what, path, windowsSIDName(ace.SID), ace.Mask&bad)
			}
		default:
			if ace.SID != "" && trustedWindowsSID(ace.SID) {
				continue
			}
			return fmt.Errorf("%s含不支持的 ACE 类型 0x%02x %s", what, ace.Type, path)
		}
	}
	return nil
}

// checkWindowsTrustedPath 严格校验对象本身（文件或目录）。
func checkWindowsTrustedPath(what, path string) error {
	if err := rejectWindowsReparse(path); err != nil {
		return err
	}
	sec, err := windowsSecurityOf(path)
	if err != nil {
		return fmt.Errorf("%sACL 校验失败 %s：%w", what, path, err)
	}
	return evaluateWindowsACL(what, path, sec, aclStrict)
}

// windowsTrustedParent：直接父目录严格校验，更上层祖先按祖先模式校验，直到卷根。
func windowsTrustedParent(parent string) error {
	for p := parent; ; p = filepath.Dir(p) {
		info, err := windowsLstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("拒绝符号链接或重解析点路径 %s", p)
		}
		if !info.IsDir() {
			return fmt.Errorf("祖先路径不是目录 %s", p)
		}
		mode := aclAncestor
		root := p == filepath.Dir(p)
		switch {
		case p == parent:
			mode = aclStrict
		case root:
			mode = aclVolumeRoot
		}
		sec, err := windowsSecurityOf(p)
		if err != nil {
			return fmt.Errorf("读取祖先目录 ACL 失败 %s：%w", p, err)
		}
		if err := evaluateWindowsACL("祖先目录", p, sec, mode); err != nil {
			return fmt.Errorf("数据目录祖先必须由 Administrators/SYSTEM 管理且普通用户不能替换：%w", err)
		}
		if root {
			return nil
		}
	}
}

func rejectWindowsReparse(path string) error {
	info, err := windowsLstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return fmt.Errorf("拒绝符号链接或重解析点路径 %s", path)
	}
	return nil
}

// checkWindowsSiteSecurity 校验已打开的站点配置（安全描述符取自同一句柄）及其祖先。
func checkWindowsSiteSecurity(path string, sec windowsSecurity) error {
	if err := evaluateWindowsACL("站点配置", path, sec, aclStrict); err != nil {
		return err
	}
	if err := siteParentCheck(filepath.Dir(path)); err != nil {
		return fmt.Errorf("站点配置所在目录不受信：%w", err)
	}
	return nil
}

// windowsACLHint 给出把目录收紧为“管理员可写、普通用户只读”的 icacls 命令。
func windowsACLHint(path string) string {
	p := strings.TrimRight(path, `\`)
	return fmt.Sprintf("；可用管理员 PowerShell 执行：icacls \"%s\" /setowner *S-1-5-32-544 /T; icacls \"%s\" /inheritance:r /grant:r *S-1-5-18:(OI)(CI)F *S-1-5-32-544:(OI)(CI)F *S-1-5-32-545:(OI)(CI)RX /T", p, p)
}

var errWindowsOnly = errors.New("仅 Windows 支持 ACL 校验")
