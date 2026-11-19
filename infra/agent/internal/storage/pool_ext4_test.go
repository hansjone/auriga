package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestExt4ArchiveResetStaysOnMemberFilesystemAndRecovers(t *testing.T) {
	root := t.TempDir()
	projects := map[string]uint32{}
	failArchive := true
	images := &Ext4Images{Root: root, ImageBytes: 128 << 30,
		IDs:        &ProjectIDs{File: filepath.Join(root, "projects.db"), First: 100, Last: 110},
		clearQuota: func(string, uint32) error { return nil },
		mount:      func(context.Context, string, string, int64) (string, error) { return "/dev/loop7", nil },
		quota: func(_ string, path string, project uint32, _, _ int64) error {
			projects[path] = project
			if filepath.Base(path) == "reset" && failArchive {
				return errors.New("quota readback interrupted")
			}
			return nil
		},
	}
	layout := &PoolLayout{Root: root, Quota: images}
	ctx := context.Background()
	p, err := layout.Prepare(ctx, "pool", "tenant", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p, "retained"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = layout.ArchiveReset(ctx, "pool", "tenant", "member", 1, "reset"); err == nil {
		t.Fatal("unverified archive published")
	}
	a := filepath.Join(root, "pool", "members", "node-archives", "reset")
	if raw, err := os.ReadFile(filepath.Join(a, "retained")); err != nil || string(raw) != "synthetic" {
		t.Fatalf("archive moved outside ext4 member mount or lost data: %v", err)
	}
	failArchive = false
	// A fresh authority recovers the durable operation, not an in-memory flag.
	recoveredImages := &Ext4Images{Root: root, ImageBytes: images.ImageBytes, IDs: &ProjectIDs{File: images.IDs.File, First: 100, Last: 110}, mount: images.mount, quota: images.quota, clearQuota: images.clearQuota}
	recovered := &PoolLayout{Root: root, Quota: recoveredImages}
	actual, err := recovered.ArchiveReset(ctx, "pool", "tenant", "member", 1, "reset")
	if err != nil || actual != a {
		t.Fatalf("resume: %s %v", actual, err)
	}
	if projects[a] == projects[p] {
		t.Fatal("archive and active workspace share quota")
	}
	if _, err = recovered.ArchiveReset(ctx, "pool", "tenant", "other", 1, "reset"); err == nil {
		t.Fatal("operation rebound to another member")
	}
	if entries, err := os.ReadDir(p); err != nil || len(entries) != 0 {
		t.Fatal("reset did not restore empty quota workspace", err)
	}
	if info, err := os.Stat(filepath.Dir(a)); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal("archive parent is not node-private", err)
	}
	recoveredImages.clearQuota = func(string, uint32) error { return errors.New("kernel reports open inode") }
	if err = recovered.DeleteArchive(ctx, "pool", "reset"); err == nil {
		t.Fatal("live quota usage was recycled")
	}
	if err = recoveredImages.IDs.Use(filepath.Join(root, "third"), func(id uint32) error {
		if id == projects[a] {
			t.Fatal("failed cleanup released reservation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recoveredImages.clearQuota = func(string, uint32) error { return nil }
	if err = recovered.DeleteArchive(ctx, "pool", "reset"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(a); !os.IsNotExist(err) {
		t.Fatal("archive retention cleanup missed ext4 path", err)
	}
	if err = recoveredImages.IDs.Use(filepath.Join(root, "recycled"), func(id uint32) error {
		if id != projects[a] {
			t.Fatal("confirmed cleanup did not recycle quota")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.ArchiveReset(ctx, "pool", "tenant", "member", 1, "reset"); err == nil {
		t.Fatal("deleted operation reexecuted")
	}
}
