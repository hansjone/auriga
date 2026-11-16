package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
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
	return command(nil, io.Discard, nil, filepath.Join(systemRoot, "System32", "icacls.exe"), root, "/inheritance:r", "/grant:r", "*"+sid+":(OI)(CI)F")
}
