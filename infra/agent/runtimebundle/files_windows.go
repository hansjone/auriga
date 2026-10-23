package runtimebundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func checkWindowsPath(path string) error {
	// Restrict to ordinary drive paths. Device names, UNC, ADS and reparse points
	// are outside the supported import/private-key boundary.
	if len(path) < 3 || path[1] != ':' || path[2] != '\\' || strings.Contains(path[2:], ":") {
		return fmt.Errorf("ordinary local drive path required")
	}
	for p := path; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		data, ok := st.Sys().(*syscall.Win32FileAttributeData)
		if st.Mode()&os.ModeSymlink != 0 || (ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0) {
			return fmt.Errorf("reparse points are forbidden")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func openedWindowsFile(f *os.File, path string, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return fmt.Errorf("ordinary file/directory required")
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
	if err != nil || n >= uint32(len(buf)) {
		return fmt.Errorf("cannot verify resolved file path")
	}
	resolved := strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`)
	if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
		return fmt.Errorf("file path changed or traversed a reparse point")
	}
	return checkWindowsPath(path)
}

func openOrdinary(path string, directory bool) (*os.File, error) {
	if err := checkWindowsPath(path); err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	if directory {
		access = windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	if err := openedWindowsFile(f, path, directory); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func openMember(r *os.Root, name string, directory bool) (*os.File, error) {
	path := filepath.Join(r.Name(), filepath.FromSlash(name))
	if err := checkWindowsPath(path); err != nil {
		return nil, err
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	if err := openedWindowsFile(f, path, directory); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func singleLink(f *os.File) error {
	var st windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &st); err != nil {
		return err
	}
	if st.NumberOfLinks != 1 {
		return fmt.Errorf("input hard links are forbidden")
	}
	return nil
}

func privatePermissions(f *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return fmt.Errorf("private key must be owned by the current user")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("private key requires an explicit restricted DACL")
	}
	for i := uint16(0); i < acl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, uint32(i), &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("private key DACL contains unsupported ACE types")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) && !sid.Equals(system) && !sid.Equals(admins) {
			return fmt.Errorf("private key DACL grants access beyond current user, SYSTEM and Administrators")
		}
	}
	return singleLink(f)
}
