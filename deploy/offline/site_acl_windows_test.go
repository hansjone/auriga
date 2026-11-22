//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procConvertSDDL     = modAdvapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procGetSDOwner      = modAdvapi32.NewProc("GetSecurityDescriptorOwner")
	procGetSDDacl       = modAdvapi32.NewProc("GetSecurityDescriptorDacl")
	sddlRevision1       = uintptr(1)
	adminOnlyDACL       = "(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"
	programDataTemplate = "(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICIIO;GA;;;CO)(A;OICI;0x1200a9;;;BU)(A;CI;0x100116;;;BU)"
)

// securityFromSDDL 用系统 API 把 SDDL 转成真实安全描述符，再走与生产相同的解析。
func securityFromSDDL(t *testing.T, sddl string) windowsSecurity {
	t.Helper()
	text, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	var sd uintptr
	if r, _, e := procConvertSDDL.Call(uintptr(unsafe.Pointer(text)), sddlRevision1, uintptr(unsafe.Pointer(&sd)), 0); r == 0 {
		t.Fatalf("SDDL 无效 %q：%v", sddl, e)
	}
	defer func() { _, _ = syscall.LocalFree(syscall.Handle(sd)) }()
	var owner *syscall.SID
	var dacl *winACL
	var defaulted, present int32
	if r, _, e := procGetSDOwner.Call(sd, uintptr(unsafe.Pointer(&owner)), uintptr(unsafe.Pointer(&defaulted))); r == 0 {
		t.Fatal(e)
	}
	if r, _, e := procGetSDDacl.Call(sd, uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted))); r == 0 {
		t.Fatal(e)
	}
	if present == 0 {
		dacl = nil
	}
	sec, err := parseWindowsSecurity(owner, dacl)
	if err != nil {
		t.Fatal(err)
	}
	return sec
}

func TestWindowsACLFromRealDescriptors(t *testing.T) {
	cases := []struct {
		sddl string
		mode windowsACLMode
		ok   bool
		want string
	}{
		{"O:BAG:BAD:P" + adminOnlyDACL, aclStrict, true, ""},
		{"O:SYG:SYD:P" + adminOnlyDACL, aclStrict, true, ""},
		{"O:BAD:P" + adminOnlyDACL + "(A;OICIIO;GA;;;CO)", aclStrict, true, ""},
		{"O:BAD:P" + adminOnlyDACL + "(D;;FA;;;WD)", aclStrict, true, ""},
		{"O:BUD:P" + adminOnlyDACL, aclStrict, false, "所有者"},
		{"O:BAD:NO_ACCESS_CONTROL", aclStrict, false, "DACL"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;FW;;;BU)", aclStrict, false, "Users"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;0x4;;;AU)", aclStrict, false, "Authenticated Users"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;GW;;;WD)", aclStrict, false, "Everyone"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;GA;;;IU)", aclStrict, false, "INTERACTIVE"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;WD;;;BU)", aclStrict, false, "Users"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;SD;;;BU)", aclStrict, false, "Users"},
		{"O:BAD:P" + adminOnlyDACL + "(A;;0x40;;;BU)", aclStrict, false, "Users"},
		// 继承而来（ID 标志）的写 ACE 同样作用于对象。
		{"O:BAD:AI" + adminOnlyDACL + "(A;ID;0x1301bf;;;AU)", aclStrict, false, "Authenticated Users"},
		{"O:SYD:PAI" + programDataTemplate, aclAncestor, true, ""},
		{"O:SYD:PAI" + programDataTemplate, aclStrict, false, "Users"},
		{"O:SYD:PAI" + programDataTemplate + "(A;;0x40;;;BU)", aclAncestor, false, "Users"},
	}
	for _, c := range cases {
		sec := securityFromSDDL(t, c.sddl)
		err := evaluateWindowsACL("测试对象", `C:\x`, sec, c.mode)
		if c.ok && err != nil {
			t.Errorf("%s：应通过，得到 %v", c.sddl, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s：期望含 %q，得到 %v", c.sddl, c.want, err)
		}
	}
}

func TestWindowsACLRealFiles(t *testing.T) {
	// 普通用户在临时目录新建的文件：所有者是当前用户（未提权）或允许 Everyone 写，均须拒绝。
	dir := t.TempDir()
	file := filepath.Join(dir, "site.json")
	if err := os.WriteFile(file, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", file, "/grant", "*S-1-1-0:(W)").CombinedOutput(); err != nil {
		t.Fatalf("icacls：%v %s", err, out)
	}
	if err := checkWindowsTrustedPath("站点配置", file); err == nil {
		t.Fatal("普通用户所有或 Everyone 可写的文件应拒绝")
	}
	f, err := openSiteFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := siteFileSecurity(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := evaluateWindowsACL("站点配置", file, sec, aclStrict); err == nil {
		t.Fatal("句柄读取的安全描述符应同样拒绝")
	}
	// 真实系统目录：能解析出所有者与 ACE。
	sys, err := inspectWindowsSecurity(os.Getenv("SystemRoot"))
	if err != nil || sys.Owner == "" || len(sys.ACEs) == 0 {
		t.Fatalf("读取 %%SystemRoot%% 安全描述符失败：%+v %v", sys, err)
	}
	// 目录符号链接/junction 本身应拒绝。
	link := filepath.Join(dir, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, dir).CombinedOutput(); err == nil {
		if err := checkWindowsTrustedPath("缓存", link); err == nil || !strings.Contains(err.Error(), "重解析点") {
			t.Fatalf("junction 应拒绝：%v", err)
		}
	} else {
		t.Logf("无法创建 junction（%s），跳过该断言", strings.TrimSpace(string(out)))
	}
}

func TestLockDepsCacheExclusive(t *testing.T) {
	dir := t.TempDir()
	release, err := lockDepsCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDepsCache(dir); err == nil || !strings.Contains(err.Error(), "另一个 deps 命令") {
		t.Fatalf("第二把锁应失败：%v", err)
	}
	release()
	again, err := lockDepsCache(dir)
	if err != nil {
		t.Fatalf("释放后应能再次加锁：%v", err)
	}
	again()
}
