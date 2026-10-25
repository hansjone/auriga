//go:build windows

package admin

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Match the restricted key-file policy used by agent/runtimebundle.
func checkKeyFile(f *os.File) error {
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
	allowed := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(user.User.Sid) || sid.Equals(system) || sid.Equals(admins))
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || !allowed(owner) {
		return errors.New("untrusted AI master key owner")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return errors.New("AI master key requires a restricted DACL")
	}
	for i := uint16(0); i < acl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, uint32(i), &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !allowed((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return errors.New("AI master key DACL grants untrusted access")
		}
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return errors.New("AI master key must have exactly one hard link")
	}
	return nil
}
