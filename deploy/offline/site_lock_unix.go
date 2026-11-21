//go:build !windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
)

// acquireSiteLock 用 flock(2) 锁住 <数据目录>.oneclick-lock（设计 D11）。所有改动站点的
// 命令共用这一把锁；进程被杀时内核自动释放。拿不到锁时直接失败，并读出锁文件里记录的
// 正在进行的操作。锁文件本身保留在磁盘上（删除已加锁的文件会让后来者锁到另一个 inode）。
func acquireSiteLock(path, op string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		if info, statErr := os.Lstat(path); statErr == nil && info.IsDir() {
			return nil, fmt.Errorf("站点锁 %s 是旧版本留下的目录锁：确认没有 auriga 进程在运行后删除该目录再重试", path)
		}
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := io.ReadAll(io.LimitReader(f, 4096))
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			who := strings.TrimSpace(string(holder))
			if who == "" {
				who = "未知操作"
			}
			return nil, fmt.Errorf("另一个站点操作正在进行（%s），本次 %s 未执行；请等它结束后重试", who, op)
		}
		return nil, err
	}
	info := fmt.Sprintf("%s，PID %d，开始于 %s", op, os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(info+"\n"), 0)
		_ = f.Sync()
	}
	return func() {
		_ = f.Truncate(0)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// openNoFollow 打开文件时拒绝最后一级是符号链接（O_NOFOLLOW）。
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}
