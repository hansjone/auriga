//go:build windows

package ingress

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func OpenBeneath(root, rel string, flags int, mode os.FileMode) (*os.File, error) {
	return openWindowsBeneath(root, rel, flags, false)
}

func createBeneath(root, rel string) (*os.File, error) {
	return openWindowsBeneath(root, rel, os.O_CREATE|os.O_WRONLY, true)
}

// Pin each parent handle. FILE_OPEN_REPARSE_POINT opens the link itself so it
// can be rejected before reading, creating children or truncating a file.
func openWindowsBeneath(root, rel string, flags int, parents bool) (*os.File, error) {
	clean, err := ValidateRelPath(rel)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	parent, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() {
		if parent != nil {
			_ = parent.Close()
		}
	}()
	if clean == "." {
		f := parent
		parent = nil
		return f, nil
	}
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		if strings.ContainsAny(part, `:\`) || strings.TrimRight(part, " .") != part && part != "." {
			return nil, fmt.Errorf("%s: invalid Windows component", CodePathInvalid)
		}
		last := i == len(parts)-1
		name, err := windows.NewNTUnicodeString(part)
		if err != nil {
			return nil, err
		}
		oa := &windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: name, Attributes: windows.OBJ_CASE_INSENSITIVE}
		oa.Length = uint32(unsafe.Sizeof(*oa))
		access := uint32(windows.FILE_GENERIC_READ)
		disposition := uint32(windows.FILE_OPEN)
		options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
		if !last {
			options |= windows.FILE_DIRECTORY_FILE
		}
		if !last && parents {
			disposition = windows.FILE_OPEN_IF
		}
		if last && flags&(os.O_WRONLY|os.O_RDWR) != 0 {
			access |= windows.FILE_GENERIC_WRITE
		}
		if last && flags&os.O_CREATE != 0 {
			disposition = windows.FILE_OPEN_IF
		}
		var handle windows.Handle
		err = windows.NtCreateFile(&handle, access, oa, &windows.IO_STATUS_BLOCK{}, nil, windows.FILE_ATTRIBUTE_NORMAL,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, disposition, options, 0, 0)
		if err != nil {
			if status, ok := err.(windows.NTStatus); ok {
				err = status.Errno()
			}
			return nil, err
		}
		var info windows.ByHandleFileInformation
		if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
			_ = windows.CloseHandle(handle)
			return nil, err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("%s: reparse point", CodePathEscape)
		}
		f := os.NewFile(uintptr(handle), part)
		if last {
			if flags&os.O_TRUNC != 0 {
				if err := f.Truncate(0); err != nil {
					_ = f.Close()
					return nil, err
				}
			}
			return f, nil
		}
		_ = parent.Close()
		parent = f
	}
	return nil, fmt.Errorf("%s: empty path", CodePathInvalid)
}
