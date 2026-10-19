//go:build windows

package identity

import (
	"fmt"
	"os/user"

	"golang.org/x/sys/windows"
)

// restrictFile sets a DACL granting FULL access only to SYSTEM, Administrators, and the current user.
func restrictFile(path string) error {
	return setRestrictiveACL(path)
}

func restrictDir(path string) error {
	return setRestrictiveACL(path)
}

func setRestrictiveACL(path string) error {
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("system sid: %w", err)
	}
	adminsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("admins sid: %w", err)
	}
	userSID, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("current user sid: %w", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		explicitAccess(systemSID),
		explicitAccess(adminsSID),
		explicitAccess(userSID),
	}

	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build acl: %w", err)
	}

	var info windows.SECURITY_INFORMATION
	info = windows.DACL_SECURITY_INFORMATION
	info |= windows.PROTECTED_DACL_SECURITY_INFORMATION

	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		info,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("set named security info on %s: %w", path, err)
	}
	return nil
}

func explicitAccess(sid *windows.SID) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func currentUserSID() (*windows.SID, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	sid, err := windows.StringToSid(u.Uid)
	if err != nil {
		var token windows.Token
		if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
			return nil, err
		}
		defer token.Close()
		tu, err := token.GetTokenUser()
		if err != nil {
			return nil, err
		}
		return tu.User.Sid, nil
	}
	return sid, nil
}
