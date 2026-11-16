package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func renamePortable(source, target string) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		err = os.Rename(source, target)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.Errno(32)) && !errors.Is(err, syscall.Errno(33)) && !os.IsPermission(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return fmt.Errorf("%w；目标文件被占用或权限不足，关闭编辑器/等待Defender扫描后重试；不要关闭安全防护", err)
}
