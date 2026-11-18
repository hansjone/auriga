//go:build linux

package storage

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

var loopDevice = regexp.MustCompile(`^/dev/loop[0-9]+$`)

func storageCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if !filepath.IsAbs(binary) {
		return nil, fmt.Errorf("absolute storage tool path required")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pool storage tool %s failed: %w", filepath.Base(binary), err)
	}
	return output, nil
}

func mountExt4Image(ctx context.Context, image, root string, size int64, mkfs, losetup string) (string, error) {
	info, err := os.Lstat(image)
	if os.IsNotExist(err) {
		entries, readErr := os.ReadDir(root)
		if readErr != nil || len(entries) != 0 {
			return "", fmt.Errorf("existing pool files require explicit storage migration")
		}
		file, err := os.CreateTemp(filepath.Dir(image), ".members-ext4-")
		if err != nil {
			return "", err
		}
		tmp := file.Name()
		defer os.Remove(tmp)
		if err = file.Chmod(0600); err == nil {
			err = unix.Fallocate(int(file.Fd()), 0, 0, size)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if _, err = storageCommand(ctx, mkfs, "-q", "-F", "-t", "ext4", "-b", "4096", "-m", "0",
			"-O", "quota,project,^orphan_file,^metadata_csum_seed", "-Q", "prjquota",
			"-E", "lazy_itable_init=0,lazy_journal_init=0", tmp); err != nil {
			return "", err
		}
		if err = verifyExt4Image(tmp, size); err != nil {
			return "", err
		}
		formatted, err := os.OpenFile(tmp, os.O_RDWR, 0)
		if err != nil {
			return "", err
		}
		err = formatted.Sync()
		_ = formatted.Close()
		if err != nil {
			return "", err
		}
		if err = os.Rename(tmp, image); err != nil {
			return "", err
		}
		dir, err := os.Open(filepath.Dir(image))
		if err != nil {
			return "", err
		}
		err = dir.Sync()
		_ = dir.Close()
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if !info.Mode().IsRegular() || info.Size() != size {
		return "", fmt.Errorf("pool backing image identity or size mismatch")
	}
	if err := verifyExt4Image(image, size); err != nil {
		return "", err
	}
	output, err := storageCommand(ctx, losetup, "--json", "--list", "--associated", image, "--output", "NAME,BACK-FILE")
	if err != nil {
		return "", err
	}
	var loops struct {
		Devices []struct {
			Name     string `json:"name"`
			BackFile string `json:"back-file"`
		} `json:"loopdevices"`
	}
	if err = json.Unmarshal(output, &loops); err != nil {
		return "", err
	}
	device := ""
	if len(loops.Devices) == 0 {
		output, err = storageCommand(ctx, losetup, "--find", "--show", "--nooverlap", image)
		if err != nil {
			return "", err
		}
		device = strings.TrimSpace(string(output))
	} else if len(loops.Devices) == 1 && filepath.Clean(loops.Devices[0].BackFile) == image {
		device = loops.Devices[0].Name
	}
	if !loopDevice.MatchString(device) {
		return "", fmt.Errorf("ambiguous pool loop device")
	}
	// Read back the actual kernel attachment after both allocation and recovery.
	output, err = storageCommand(ctx, losetup, "--json", "--list", device, "--output", "NAME,BACK-FILE")
	if err != nil {
		return "", err
	}
	loops.Devices = nil
	if err = json.Unmarshal(output, &loops); err != nil || len(loops.Devices) != 1 || loops.Devices[0].Name != device || filepath.Clean(loops.Devices[0].BackFile) != image {
		return "", fmt.Errorf("pool loop backing file readback mismatch")
	}
	var source, target unix.Stat_t
	if err = unix.Stat(device, &source); err != nil {
		return "", err
	}
	if source.Mode&unix.S_IFMT != unix.S_IFBLK {
		return "", fmt.Errorf("pool loop is not a block device")
	}
	if err = unix.Stat(root, &target); err != nil {
		return "", err
	}
	if target.Dev != source.Rdev {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			return "", fmt.Errorf("pool mount target is occupied")
		}
		if err = unix.Mount(device, root, "ext4", unix.MS_NODEV|unix.MS_NOSUID, "prjquota"); err != nil {
			return "", err
		}
	}
	if err = verifyProjectMount(device, root); err != nil {
		return "", err
	}
	return device, nil
}

func verifyExt4Image(path string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var super [1024]byte
	if _, err = file.ReadAt(super[:], 1024); err != nil {
		return err
	}
	read := binary.LittleEndian.Uint32
	blocks := uint64(read(super[4:])) | uint64(read(super[0x150:]))<<32
	if binary.LittleEndian.Uint16(super[0x38:]) != 0xef53 || read(super[0x18:]) != 2 || blocks*4096 != uint64(size) || read(super[0x64:])&0x2100 != 0x2100 {
		return fmt.Errorf("pool image lacks the exact ext4 size and project-quota features")
	}
	return nil
}

func verifyProjectMount(device, root string) error {
	var block, dir unix.Stat_t
	var stat unix.Statfs_t
	if err := unix.Stat(device, &block); err != nil {
		return err
	}
	if err := unix.Stat(root, &dir); err != nil {
		return err
	}
	if err := unix.Statfs(root, &stat); err != nil {
		return err
	}
	if dir.Dev != block.Rdev || stat.Type != unix.EXT4_SUPER_MAGIC {
		return fmt.Errorf("pool filesystem device mismatch")
	}
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	for _, row := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(row)
		if len(fields) < 10 || fields[4] != root {
			continue
		}
		for i := 6; i+3 < len(fields); i++ {
			if fields[i] == "-" && fields[i+1] == "ext4" && strings.Contains(","+fields[i+3]+",", ",prjquota,") {
				return nil
			}
		}
	}
	return fmt.Errorf("ext4 project quota enforcement is unavailable")
}

type projectAttributes struct {
	Flags, Extsize, Nextents, Project, Cowextsize uint32
	Pad                                           [8]byte
}
type diskQuota struct {
	BlockHard, BlockSoft, Space, InodeHard, InodeSoft, Inodes, BlockTime, InodeTime uint64
	Valid                                                                           uint32
	Pad                                                                             uint32
}

func quotaCall(device string, operation uint32, project uint32, value *diskQuota) error {
	source, err := unix.BytePtrFromString(device)
	if err != nil {
		return err
	}
	_, _, errno := unix.Syscall6(unix.SYS_QUOTACTL, uintptr(operation<<8|2), uintptr(unsafe.Pointer(source)), uintptr(project), uintptr(unsafe.Pointer(value)), 0, 0)
	runtime.KeepAlive(source)
	runtime.KeepAlive(value)
	if errno != 0 {
		return errno
	}
	return nil
}

func setExt4ProjectQuota(device, path string, project uint32, bytes, inodes int64) error {
	if !loopDevice.MatchString(device) {
		return fmt.Errorf("invalid pool device")
	}
	var block, dir unix.Stat_t
	if err := unix.Stat(device, &block); err != nil {
		return err
	}
	if err := unix.Stat(path, &dir); err != nil {
		return err
	}
	if dir.Dev != block.Rdev {
		return fmt.Errorf("member is outside pool filesystem")
	}
	limits := diskQuota{BlockHard: uint64(bytes / 1024), InodeHard: uint64(inodes), Valid: 1 | 4}
	if err := quotaCall(device, 0x800008, project, &limits); err != nil {
		return err
	}
	err := filepath.WalkDir(path, func(entry string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fd, err := unix.Open(entry, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		var actual projectAttributes
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), 0x801c581f, uintptr(unsafe.Pointer(&actual))); e != 0 {
			return e
		}
		actual.Project = project
		if info.IsDir() {
			actual.Flags |= 0x200
		}
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), 0x401c5820, uintptr(unsafe.Pointer(&actual))); e != 0 {
			return e
		}
		var check projectAttributes
		if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), 0x801c581f, uintptr(unsafe.Pointer(&check))); e != 0 {
			return e
		}
		if check.Project != project || info.IsDir() && check.Flags&0x200 == 0 {
			return fmt.Errorf("project inheritance readback failed")
		}
		return nil
	})
	if err != nil {
		return err
	}
	var readback diskQuota
	if err := quotaCall(device, 0x800007, project, &readback); err != nil {
		return err
	}
	if readback.BlockHard != limits.BlockHard || readback.InodeHard != limits.InodeHard {
		return fmt.Errorf("member quota readback failed for project %s", strconv.FormatUint(uint64(project), 10))
	}
	return nil
}
