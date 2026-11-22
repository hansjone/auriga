package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	sidUsers         = "S-1-5-32-545"
	sidAuthenticated = "S-1-5-11"
	sidEveryone      = "S-1-1-0"
	sidInteractive   = "S-1-5-4"
	sidCreatorOwner  = "S-1-3-0"
	sidSomeUser      = "S-1-5-21-1-2-3-1001"
	maskFullControl  = 0x001F01FF
	maskReadExecute  = 0x001200A9
	maskModify       = 0x001301BF
	maskAddSubdir    = accFileAppendData
	maskIcaclsWrite  = 0x00100116 // icacls (W)：WD|AD|WEA|WA|SYNCHRONIZE
)

func allow(sid string, mask uint32) windowsACE {
	return windowsACE{Type: aceTypeAllowed, Mask: mask, SID: sid}
}

func adminOnly(extra ...windowsACE) windowsSecurity {
	aces := []windowsACE{allow(sidLocalSystem, maskFullControl), allow(sidAdministrators, maskFullControl), allow(sidUsers, maskReadExecute)}
	return windowsSecurity{Owner: sidAdministrators, ACEs: append(aces, extra...)}
}

func TestEvaluateWindowsACLStrict(t *testing.T) {
	ok := []struct {
		name string
		sec  windowsSecurity
	}{
		{"管理员所有、普通用户只读", adminOnly()},
		{"SYSTEM 所有", windowsSecurity{Owner: sidLocalSystem, ACEs: adminOnly().ACEs}},
		{"仅继承的 CREATOR OWNER 不作用于对象", adminOnly(windowsACE{Type: aceTypeAllowed, Flags: aceFlagInheritOnly | 0x3, Mask: maskFullControl, SID: sidCreatorOwner})},
		{"拒绝 ACE 只收紧", adminOnly(windowsACE{Type: aceTypeDenied, Mask: maskFullControl, SID: sidEveryone})},
		{"TrustedInstaller 可写", adminOnly(allow(sidTrustedInstall, maskFullControl))},
	}
	for _, c := range ok {
		if err := evaluateWindowsACL("站点配置", `C:\x`, c.sec, aclStrict); err != nil {
			t.Errorf("%s：应通过，得到 %v", c.name, err)
		}
	}
	bad := []struct {
		name string
		sec  windowsSecurity
		want string
	}{
		{"普通用户所有", windowsSecurity{Owner: sidSomeUser, ACEs: adminOnly().ACEs}, "所有者"},
		{"TrustedInstaller 所有（严格模式不接受）", windowsSecurity{Owner: sidTrustedInstall, ACEs: adminOnly().ACEs}, "所有者"},
		{"空 DACL", windowsSecurity{Owner: sidAdministrators, NullDACL: true}, "DACL"},
		{"Users 写数据", adminOnly(allow(sidUsers, accFileWriteData)), "Users"},
		{"Users 新建子目录（ProgramData 继承项）", adminOnly(allow(sidUsers, maskIcaclsWrite)), "Users"},
		{"Authenticated Users 修改", adminOnly(allow(sidAuthenticated, maskModify)), "Authenticated Users"},
		{"Everyone GENERIC_WRITE", adminOnly(allow(sidEveryone, accGenericWrite)), "Everyone"},
		{"INTERACTIVE GENERIC_ALL", adminOnly(allow(sidInteractive, accGenericAll)), "INTERACTIVE"},
		{"具体用户 WRITE_DAC", adminOnly(allow(sidSomeUser, accWriteDAC)), "写类权限"},
		{"具体用户 WRITE_OWNER", adminOnly(allow(sidSomeUser, accWriteOwner)), "写类权限"},
		{"具体用户 DELETE", adminOnly(allow(sidSomeUser, accDelete)), "写类权限"},
		{"具体用户删除子项", adminOnly(allow(sidSomeUser, accFileDeleteChild)), "写类权限"},
		{"具体用户写属性", adminOnly(allow(sidSomeUser, accFileWriteAttrs)), "写类权限"},
		{"具体用户写扩展属性", adminOnly(allow(sidSomeUser, accFileWriteEA)), "写类权限"},
		{"非仅继承的 CREATOR OWNER 写", adminOnly(windowsACE{Type: aceTypeAllowed, Flags: 0x3, Mask: maskFullControl, SID: sidCreatorOwner}), "CREATOR OWNER"},
		{"对象允许 ACE", adminOnly(windowsACE{Type: aceTypeAllowedObj, Mask: accFileWriteData}), "不支持的 ACE"},
		{"回调允许 ACE 给普通用户", adminOnly(windowsACE{Type: aceTypeAllowedCb, Mask: accFileWriteData, SID: sidUsers}), "不支持的 ACE"},
	}
	for _, c := range bad {
		err := evaluateWindowsACL("站点配置", `C:\x`, c.sec, aclStrict)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：期望含 %q，得到 %v", c.name, c.want, err)
		}
	}
}

func TestEvaluateWindowsACLAncestor(t *testing.T) {
	// 真实默认 ACL：C:\ProgramData 与 C:\（Win11）。
	programData := windowsSecurity{Owner: sidLocalSystem, ACEs: []windowsACE{
		allow(sidLocalSystem, maskFullControl), allow(sidAdministrators, maskFullControl),
		{Type: aceTypeAllowed, Flags: aceFlagInheritOnly | 0x3, Mask: accGenericAll, SID: sidCreatorOwner},
		allow(sidUsers, maskReadExecute), allow(sidUsers, maskIcaclsWrite),
	}}
	driveC := windowsSecurity{Owner: sidTrustedInstall, ACEs: []windowsACE{
		allow(sidAdministrators, maskFullControl), allow(sidLocalSystem, maskFullControl), allow(sidUsers, maskReadExecute),
		{Type: aceTypeAllowed, Flags: aceFlagInheritOnly | 0x3, Mask: maskModify, SID: sidAuthenticated},
		allow(sidAuthenticated, maskAddSubdir),
	}}
	driveD := windowsSecurity{Owner: sidLocalSystem, ACEs: []windowsACE{
		allow(sidAdministrators, maskFullControl), allow(sidLocalSystem, maskFullControl),
		allow(sidAuthenticated, maskModify), allow(sidUsers, maskReadExecute),
	}}
	if err := evaluateWindowsACL("祖先目录", `C:\ProgramData`, programData, aclAncestor); err != nil {
		t.Errorf("默认 ProgramData 作为上层祖先应通过：%v", err)
	}
	if err := evaluateWindowsACL("祖先目录", `C:\`, driveC, aclVolumeRoot); err != nil {
		t.Errorf("默认 C:\\ 作为卷根应通过：%v", err)
	}
	if err := evaluateWindowsACL("祖先目录", `D:\`, driveD, aclVolumeRoot); err != nil {
		t.Errorf("Authenticated Users 修改（含 DELETE）的卷根应通过：%v", err)
	}
	if err := evaluateWindowsACL("祖先目录", `D:\x`, driveD, aclAncestor); err == nil {
		t.Error("非卷根祖先给普通用户 DELETE 应拒绝（可改名替换路径）")
	}
	if err := evaluateWindowsACL("祖先目录", `C:\ProgramData`, programData, aclStrict); err == nil {
		t.Error("ProgramData 默认 ACL 作为直接父目录应拒绝（普通用户可新建文件）")
	}
	for _, mask := range []uint32{accFileDeleteChild, accWriteDAC, accWriteOwner, accGenericAll} {
		sec := windowsSecurity{Owner: sidLocalSystem, ACEs: []windowsACE{allow(sidUsers, mask)}}
		if err := evaluateWindowsACL("祖先目录", `C:\a`, sec, aclAncestor); err == nil {
			t.Errorf("祖先给普通用户 0x%x 应拒绝", mask)
		}
	}
	if err := evaluateWindowsACL("祖先目录", `C:\a`, windowsSecurity{Owner: sidSomeUser}, aclAncestor); err == nil {
		t.Error("普通用户所有的祖先应拒绝")
	}
	if err := evaluateWindowsACL("祖先目录", `C:\a`, windowsSecurity{Owner: sidLocalSystem, NullDACL: true}, aclAncestor); err == nil {
		t.Error("空 DACL 祖先应拒绝")
	}
}

type fakeDirInfo struct {
	name string
	mode fs.FileMode
}

func (f fakeDirInfo) Name() string       { return f.name }
func (f fakeDirInfo) Size() int64        { return 0 }
func (f fakeDirInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDirInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeDirInfo) Sys() any           { return nil }

func TestWindowsTrustedParentModes(t *testing.T) {
	oldSec, oldStat := windowsSecurityOf, windowsLstat
	t.Cleanup(func() { windowsSecurityOf, windowsLstat = oldSec, oldStat })
	// 用本平台的路径分隔规则构造三层：root/mid/parent。
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	mid := filepath.Join(root, "mid")
	parent := filepath.Join(mid, "parent")
	secs := map[string]windowsSecurity{}
	modes := map[string]fs.FileMode{root: fs.ModeDir, mid: fs.ModeDir, parent: fs.ModeDir}
	windowsLstat = func(p string) (os.FileInfo, error) {
		m, ok := modes[p]
		if !ok {
			return nil, errors.New("不存在 " + p)
		}
		return fakeDirInfo{name: p, mode: m}, nil
	}
	windowsSecurityOf = func(p string) (windowsSecurity, error) { return secs[p], nil }
	shared := windowsSecurity{Owner: sidLocalSystem, ACEs: []windowsACE{allow(sidUsers, maskIcaclsWrite)}}
	secs[root] = windowsSecurity{Owner: sidTrustedInstall, ACEs: []windowsACE{allow(sidAuthenticated, maskModify)}}
	secs[mid] = shared
	secs[parent] = adminOnly()
	if err := windowsTrustedParent(parent); err != nil {
		t.Fatalf("直接父目录严格、祖先可新建子项、卷根可 DELETE：应通过，得到 %v", err)
	}
	secs[parent] = shared
	if err := windowsTrustedParent(parent); err == nil {
		t.Fatal("直接父目录普通用户可新建文件应拒绝")
	}
	secs[parent] = adminOnly()
	secs[mid] = windowsSecurity{Owner: sidLocalSystem, ACEs: []windowsACE{allow(sidUsers, accFileDeleteChild)}}
	if err := windowsTrustedParent(parent); err == nil {
		t.Fatal("上层祖先普通用户可删除子项应拒绝")
	}
	secs[mid] = shared
	modes[mid] = fs.ModeDir | fs.ModeIrregular
	if err := windowsTrustedParent(parent); err == nil || !strings.Contains(err.Error(), "重解析点") {
		t.Fatalf("祖先为 junction（ModeIrregular）应拒绝：%v", err)
	}
}

func TestWindowsSiteSecurityChecksParent(t *testing.T) {
	old := siteParentCheck
	t.Cleanup(func() { siteParentCheck = old })
	var checked string
	siteParentCheck = func(p string) error { checked = p; return nil }
	path := filepath.Join(os.TempDir(), "Auriga", "site.json")
	if err := checkWindowsSiteSecurity(path, adminOnly()); err != nil {
		t.Fatal(err)
	}
	if checked != filepath.Dir(path) {
		t.Fatalf("站点配置应校验所在目录，实际 %q", checked)
	}
	siteParentCheck = func(string) error { return errors.New("祖先可写") }
	if err := checkWindowsSiteSecurity(path, adminOnly()); err == nil || !strings.Contains(err.Error(), "祖先可写") {
		t.Fatalf("所在目录不受信应拒绝：%v", err)
	}
	if err := checkWindowsSiteSecurity(path, adminOnly(allow(sidUsers, accFileWriteData))); err == nil {
		t.Fatal("普通用户可写的站点配置应拒绝")
	}
}

func TestConfigPathWindowsSyntaxOnlyOnWindows(t *testing.T) {
	old := siteGOOS
	t.Cleanup(func() { siteGOOS = old })
	siteGOOS = "linux"
	if err := checkConfigPath("deps.cache", `D:\auriga\cache`); err == nil {
		t.Fatal("Linux 上 Windows 路径会被当作相对路径，必须拒绝")
	}
	if err := checkConfigPath("deps.cache", "/srv/auriga/cache"); err != nil {
		t.Fatal(err)
	}
	siteGOOS = "windows"
	if err := checkConfigPath("deps.cache", `D:\auriga\cache`); err != nil {
		t.Fatal(err)
	}
	cfg := &siteConfig{Deps: &siteDeps{Cache: "/srv/auriga/cache"}}
	if err := checkClientPaths(cfg); err == nil {
		t.Fatal("Windows 客户端命令的 deps.cache 必须是 Windows 绝对路径")
	}
	cfg.Deps.Cache = `D:\auriga\cache`
	cfg.Deps.Kits = []siteKit{{Path: "/srv/kits/a", TrustFingerprint: "SHA256:" + strings.Repeat("a", 64)}}
	if err := checkClientPaths(cfg); err == nil {
		t.Fatal("Windows 客户端命令的 deps.kits 必须是 Windows 绝对路径")
	}
}
