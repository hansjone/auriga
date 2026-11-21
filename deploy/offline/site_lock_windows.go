//go:build windows

package main

import (
	"errors"
	"os"
)

// 服务端站点命令只支持 Linux；Windows 上只为编译与跨平台单测保留接口。
func acquireSiteLock(string, string) (func(), error) {
	return nil, errors.New("站点锁只支持 Linux（flock）")
}

func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}
