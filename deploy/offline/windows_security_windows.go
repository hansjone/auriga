package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

func protectTransferRoot(root string) error {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return errors.New("无法定位Windows系统工具目录，拒绝转运签名材料")
	}
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return err
	}
	advapi, err := syscall.LoadDLL(filepath.Join(systemRoot, "System32", "advapi32.dll"))
	if err != nil {
		return err
	}
	defer func() { _ = advapi.Release() }()
	convert, err := advapi.FindProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	if err != nil {
		return err
	}
	getDACL, err := advapi.FindProc("GetSecurityDescriptorDacl")
	if err != nil {
		return err
	}
	setSecurity, err := advapi.FindProc("SetNamedSecurityInfoW")
	if err != nil {
		return err
	}
	// Replace the entire DACL: removing inheritance alone retains explicit ACEs.
	sddl, err := syscall.UTF16PtrFromString("D:P(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	var descriptor syscall.Handle
	ok, _, callErr := convert.Call(uintptr(unsafe.Pointer(sddl)), 1, uintptr(unsafe.Pointer(&descriptor)), 0)
	if ok == 0 {
		return fmt.Errorf("生成Windows私有DACL失败：%w", callErr)
	}
	defer func() { _, _ = syscall.LocalFree(descriptor) }()
	var present, defaulted int32
	var dacl uintptr
	ok, _, callErr = getDACL.Call(uintptr(descriptor), uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted)))
	if ok == 0 {
		return fmt.Errorf("读取Windows私有DACL失败：%w", callErr)
	}
	if present == 0 || dacl == 0 {
		return errors.New("缺少Windows私有DACL，拒绝转运签名材料")
	}
	name, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return err
	}
	const (
		fileObject    = 1
		ownerSecurity = 0x00000001
		daclSecurity  = 0x00000004
		protectedDACL = 0x80000000
	)
	result, _, _ := setSecurity.Call(uintptr(unsafe.Pointer(name)), fileObject, ownerSecurity|daclSecurity|protectedDACL, uintptr(unsafe.Pointer(user.User.Sid)), 0, dacl, 0)
	runtime.KeepAlive(user)
	if result != 0 {
		return fmt.Errorf("设置Windows私有目录属主/DACL失败：%w", syscall.Errno(result))
	}
	return nil
}
