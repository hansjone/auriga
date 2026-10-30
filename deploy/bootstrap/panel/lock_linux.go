package panel

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lockOutput(out string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(out, ".panel-lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
