package runtime

import (
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
)

func unifiedLocalFS(root string) error {
	volume := filepath.VolumeName(root)
	if volume == "" || strings.HasPrefix(volume, `\\`) {
		return fmt.Errorf("shared state requires local NTFS")
	}
	name, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return err
	}
	if windows.GetDriveType(name) != windows.DRIVE_FIXED {
		return fmt.Errorf("shared state requires a fixed local drive")
	}
	var fs [64]uint16
	if err := windows.GetVolumeInformation(name, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return err
	}
	if windows.UTF16ToString(fs[:]) != "NTFS" {
		return fmt.Errorf("shared state requires NTFS")
	}
	return nil
}

func unifiedReplace(source, target string) error {
	a, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// Directory changes are performed by MoveFileEx with WRITE_THROUGH on Windows.
func unifiedSyncDirectory(string) error { return nil }
