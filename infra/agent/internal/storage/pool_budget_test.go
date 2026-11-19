package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExt4PoolAcceptsExistingTwentyTwoShareBudget(t *testing.T) {
	for _, sample := range []struct {
		name string
		size int64
		want bool
	}{
		{"exact-22", 22 * MemberBytes, true},
		{"larger-existing-image", 24 * MemberBytes, true},
		{"one-block-short", 22*MemberBytes - 4096, false},
		{"only-21", 21 * MemberBytes, false},
		{"unaligned", 22*MemberBytes + 1, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			root := t.TempDir()
			mounted := false
			images := &Ext4Images{Root: root, ImageBytes: sample.size,
				IDs: &ProjectIDs{File: filepath.Join(root, "project-ids"), First: 1, Last: 22},
				mount: func(_ context.Context, image, target string, size int64) (string, error) {
					mounted = true
					if size != sample.size || image != filepath.Join(root, "pool", "control", "members-ext4") || target != filepath.Join(root, "pool", "members") {
						t.Fatal("existing storage target or size changed")
					}
					return "/dev/loop7", nil
				},
			}
			actual, err := images.MountPool(context.Background(), "pool")
			if sample.want {
				if err != nil || !mounted || actual.Device != "/dev/loop7" {
					t.Fatal("22-share minimum rejected", err)
				}
			} else if err == nil || mounted {
				t.Fatal("invalid storage provisioning reached the mount boundary")
			} else if _, statErr := os.Stat(filepath.Join(root, "pool")); !os.IsNotExist(statErr) {
				t.Fatal("invalid storage provisioning changed the pool directory", statErr)
			}
		})
	}
}
