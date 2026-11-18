package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExt4PoolPreparesBeforePublicationAndRetainsQuotaReservation(t *testing.T) {
	root := t.TempDir()
	var projects []uint32
	fail := true
	images := &Ext4Images{Root: root, ImageBytes: 128 << 30,
		IDs: &ProjectIDs{File: filepath.Join(t.TempDir(), "projects.db"), First: 100, Last: 102},
		mount: func(_ context.Context, image, target string, size int64) (string, error) {
			if filepath.Base(image) != "members-ext4" || size != 128<<30 || target != filepath.Join(root, "pool", "members") {
				t.Fatal("uncontrolled storage target")
			}
			return "/dev/loop7", nil
		},
		quota: func(device, path string, project uint32, bytes, inodes int64) error {
			if device != "/dev/loop7" || bytes != MemberBytes || inodes != MemberInodes {
				t.Fatal("wrong kernel quota")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
			projects = append(projects, project)
			if fail {
				return errors.New("kernel readback failed")
			}
			return nil
		},
	}
	layout := &PoolLayout{Root: root, Quota: images}
	if _, err := layout.Prepare(context.Background(), "pool", "tenant", "member", 1); err == nil {
		t.Fatal("unconfirmed quota published")
	}
	fail = false
	path, err := layout.Prepare(context.Background(), "pool", "tenant", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0] != projects[1] {
		t.Fatal("retry lost durable project ownership")
	}
	if path != filepath.Join(root, "pool", "members", "tenant", "member", "1", "workspace") {
		t.Fatal("member path changed")
	}
	if _, err := layout.Prepare(context.Background(), "pool", "tenant", "other", 1); err != nil {
		t.Fatal(err)
	}
	if projects[2] == projects[1] {
		t.Fatal("two members share a quota project")
	}
	if err := images.Ensure(context.Background(), filepath.Join(root, "pool", "control"), MemberBytes, MemberInodes); err == nil {
		t.Fatal("private control accepted as member filesystem")
	}
}

func TestExt4PoolMountFailureAndUnknownBackingDoNotPrepareMember(t *testing.T) {
	root := t.TempDir()
	images := &Ext4Images{Root: root, ImageBytes: 128 << 30,
		IDs: &ProjectIDs{File: filepath.Join(t.TempDir(), "projects.db"), First: 1, Last: 10},
		mount: func(context.Context, string, string, int64) (string, error) {
			return "", errors.New("kernel does not support project quotas")
		},
	}
	layout := &PoolLayout{Root: root, Quota: images}
	if _, err := layout.Prepare(context.Background(), "pool", "tenant", "member", 1); err == nil {
		t.Fatal("missing kernel storage accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "pool", "members", "tenant")); !os.IsNotExist(err) {
		t.Fatal("member directory created on backing NAS filesystem")
	}
	images.ImageBytes = MemberBytes
	if _, err := images.MountPool(context.Background(), "pool"); err == nil {
		t.Fatal("undersized pool filesystem accepted")
	}
}
