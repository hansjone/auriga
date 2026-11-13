package runtime

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func unifiedLocalFS(root string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(root, &stat); err != nil {
		return err
	}
	switch uint64(stat.Type) {
	case 0xef53, 0x58465342, 0x9123683e, 0x01021994, 0x794c7630, 0x2fc12fc1:
		return nil
	default:
		return fmt.Errorf("shared resource state requires a supported local filesystem")
	}
}
