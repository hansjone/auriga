//go:build windows

package main

import (
	"errors"
	"os"
)

// Windows 的站点配置 ACL 校验（所有者 Administrators 或 SYSTEM、普通用户无写权限）
// 属于第 9 步“客户端统一”，这里只保留接口并给出清楚的错误。
var errWindowsSiteUnsupported = errors.New("Windows 下站点配置的 ACL 校验与依赖缓存暂不支持（统一现场部署第 9 步实现）")

func fileOwnerUID(os.FileInfo) (uint32, error) { return 0, errWindowsSiteUnsupported }

func openSiteFile(string) (*os.File, error) { return nil, errWindowsSiteUnsupported }

func lockDepsCache(string) (func(), error) { return nil, errWindowsSiteUnsupported }
