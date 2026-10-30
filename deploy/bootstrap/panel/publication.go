package panel

import (
	"errors"
	"fmt"
	"os"
	"time"
)

var ErrPublicationBusy = errors.New("panel publication already in progress")

func withOutputLock(out string, action func() error) error {
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(out)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid public panel directory")
	}
	unlock, err := lockOutput(out)
	if err != nil {
		return err
	}
	defer unlock()
	return action()
}

// PublishChecked records success/failure while still holding the publication
// lock. Losing contenders never write or clear another publication's status.
func PublishChecked(keyFile, dist, out string, now time.Time) error {
	return withOutputLock(out, func() error {
		err := publish(keyFile, dist, out, "", now)
		return errors.Join(err, recordCheck(out, err))
	})
}
