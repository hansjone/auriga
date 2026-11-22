//go:build windows

package main

// Windows 平台实现：只用标准库 syscall 动态调用 advapi32/kernel32（均为 KnownDLLs，
// 固定从 System32 加载），不引入 golang.org/x/sys，保持 deploy/offline 零外部依赖，
// 离线 / --network none 构建无需取模块。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modAdvapi32                  = syscall.NewLazyDLL("advapi32.dll")
	modKernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetNamedSecurityInfoW    = modAdvapi32.NewProc("GetNamedSecurityInfoW")
	procGetSecurityInfo          = modAdvapi32.NewProc("GetSecurityInfo")
	procGetAce                   = modAdvapi32.NewProc("GetAce")
	procLockFileEx               = modKernel32.NewProc("LockFileEx")
	procUnlockFileEx             = modKernel32.NewProc("UnlockFileEx")
	errWindowsSecurityUnreadable = errors.New("无法读取所有者")
)

const (
	seFileObject              = 1
	ownerSecurityInformation  = 0x1
	daclSecurityInformation   = 0x4
	lockfileFailImmediately   = 0x1
	lockfileExclusiveLock     = 0x2
	errorLockViolation        = syscall.Errno(33)
	fileFlagOpenReparsePoint  = 0x00200000
	fileFlagBackupSemantics   = 0x02000000
	fileAttributeReparsePoint = 0x400
)

type winACL struct {
	AclRevision byte
	Sbz1        byte
	AclSize     uint16
	AceCount    uint16
	Sbz2        uint16
}

type winACEHeader struct {
	AceType  byte
	AceFlags byte
	AceSize  uint16
}

// winAccessACE 对应 ACCESS_ALLOWED_ACE / ACCESS_DENIED_ACE（及 *_CALLBACK_ACE 前缀）。
type winAccessACE struct {
	Header   winACEHeader
	Mask     uint32
	SidStart uint32
}

func fileOwnerUID(os.FileInfo) (uint32, error) {
	return 0, errors.New("Windows 不使用 POSIX uid；请走 ACL 校验")
}

// openSiteFile 以 FILE_FLAG_OPEN_REPARSE_POINT 打开（不跟随重解析点），只共享读，
// 打开期间文件不能被改写、删除或改名；随后的 ACL 校验基于同一句柄。
func openSiteFile(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil,
		syscall.OPEN_EXISTING, fileFlagOpenReparsePoint, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		_ = syscall.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&fileAttributeReparsePoint != 0 {
		_ = syscall.CloseHandle(h)
		return nil, fmt.Errorf("拒绝重解析点路径 %s", path)
	}
	return os.NewFile(uintptr(h), path), nil
}

// siteFileSecurity 读取已打开文件句柄上的所有者与 DACL。
func siteFileSecurity(f *os.File) (windowsSecurity, error) {
	var owner *syscall.SID
	var dacl *winACL
	var sd uintptr
	r, _, _ := procGetSecurityInfo.Call(f.Fd(), seFileObject,
		ownerSecurityInformation|daclSecurityInformation,
		uintptr(unsafe.Pointer(&owner)), 0, uintptr(unsafe.Pointer(&dacl)), 0,
		uintptr(unsafe.Pointer(&sd)))
	if r != 0 {
		return windowsSecurity{}, syscall.Errno(r)
	}
	defer func() { _, _ = syscall.LocalFree(syscall.Handle(sd)) }()
	return parseWindowsSecurity(owner, dacl)
}

// inspectWindowsSecurity 按路径读取所有者与 DACL（目录、套件、缓存等）。
func inspectWindowsSecurity(path string) (windowsSecurity, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return windowsSecurity{}, err
	}
	var owner *syscall.SID
	var dacl *winACL
	var sd uintptr
	r, _, _ := procGetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(name)), seFileObject,
		ownerSecurityInformation|daclSecurityInformation,
		uintptr(unsafe.Pointer(&owner)), 0, uintptr(unsafe.Pointer(&dacl)), 0,
		uintptr(unsafe.Pointer(&sd)))
	if r != 0 {
		return windowsSecurity{}, syscall.Errno(r)
	}
	defer func() { _, _ = syscall.LocalFree(syscall.Handle(sd)) }()
	return parseWindowsSecurity(owner, dacl)
}

// parseWindowsSecurity 把所有者 SID 与 DACL（均指向仍有效的安全描述符内部）转成
// 与平台无关的结构；dacl 为 nil 表示空 DACL。
func parseWindowsSecurity(owner *syscall.SID, dacl *winACL) (windowsSecurity, error) {
	if owner == nil {
		return windowsSecurity{}, errWindowsSecurityUnreadable
	}
	ownerSID, err := owner.String()
	if err != nil {
		return windowsSecurity{}, fmt.Errorf("无法解析所有者 SID：%w", err)
	}
	sec := windowsSecurity{Owner: ownerSID}
	if dacl == nil {
		sec.NullDACL = true
		return sec, nil
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *winAccessACE
		if r, _, e := procGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace))); r == 0 || ace == nil {
			return windowsSecurity{}, fmt.Errorf("读取 ACE 失败：%w", e)
		}
		if ace.Header.AceSize < 8 {
			return windowsSecurity{}, errors.New("ACE 长度无效")
		}
		// 所有允许/拒绝类 ACE 的 Mask 都紧随 4 字节头。
		row := windowsACE{Type: ace.Header.AceType, Flags: ace.Header.AceFlags, Mask: ace.Mask}
		switch ace.Header.AceType {
		case aceTypeAllowed, aceTypeDenied, aceTypeAllowedCb, aceTypeDeniedCb:
			if ace.Header.AceSize < 16 {
				return windowsSecurity{}, errors.New("ACE 长度无效")
			}
			sid, err := (*syscall.SID)(unsafe.Pointer(&ace.SidStart)).String()
			if err != nil {
				return windowsSecurity{}, fmt.Errorf("无法解析 ACE 主体：%w", err)
			}
			row.SID = sid
		}
		sec.ACEs = append(sec.ACEs, row)
	}
	return sec, nil
}

// lockDepsCache 用 LockFileEx 排他锁，防止多个 deps 命令同时写缓存；进程退出时系统释放。
func lockDepsCache(cache string) (func(), error) {
	path := filepath.Join(cache, ".deps.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var ol syscall.Overlapped
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		_ = f.Close()
		if errors.Is(e, errorLockViolation) || errors.Is(e, syscall.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("另一个 deps 命令正在写缓存 %s，请等它结束后重试", cache)
		}
		return nil, fmt.Errorf("锁定依赖缓存失败 %s：%w", cache, e)
	}
	return func() {
		var uol syscall.Overlapped
		_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&uol)))
		_ = f.Close()
	}, nil
}
