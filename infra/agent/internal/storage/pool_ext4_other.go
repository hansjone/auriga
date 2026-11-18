//go:build !linux

package storage

import (
	"context"
	"fmt"
)

func mountExt4Image(context.Context, string, string, int64, string, string) (string, error) {
	return "", fmt.Errorf("ext4 pool images require Linux")
}

func setExt4ProjectQuota(string, string, uint32, int64, int64) error {
	return fmt.Errorf("ext4 project quotas require Linux")
}
