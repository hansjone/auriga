package runtime

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// A scanner may temporarily deny DELETE sharing on the destination. Retrying
// the same fsynced replacement is safe; permission/I/O errors still fail closed.
func poolReplace(source, target string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = unifiedReplace(source, target)
		// MoveFileEx also reports ACCESS_DENIED for an open destination that
		// omitted FILE_SHARE_DELETE. Permanent ACL failures still return error
		// after the bounded retry; no permissions or ownership are changed.
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}
