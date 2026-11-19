//go:build !linux

package storage

import (
	"context"
	"fmt"
)

func clearExt4ProjectQuota(string, uint32) error {
	return fmt.Errorf("ext4 project quotas require Linux")
}

func mountExt4Image(context.Context, string, string, int64, string, string) (string, error) {
	return "", fmt.Errorf("ext4 pool images require Linux")
}

func setExt4ProjectQuota(string, string, uint32, int64, int64) error {
	return fmt.Errorf("ext4 project quotas require Linux")
}
