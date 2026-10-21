//go:build windows

package renderer

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func restrictSecretFile(path string) (retErr error) {
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("system sid: %w", err)
	}
	adminsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("admins sid: %w", err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer func() {
		if cerr := token.Close(); cerr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close process token: %w", cerr))
		}
	}()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	userSID := tokenUser.User.Sid

	entries := []windows.EXPLICIT_ACCESS{
		explicitAccess(systemSID),
		explicitAccess(adminsSID),
		explicitAccess(userSID),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, acl, nil)
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
