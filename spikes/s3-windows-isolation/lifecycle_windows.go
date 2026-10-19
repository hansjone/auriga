//go:build windows && amd64

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func processesForSID(sid string, terminate bool) ([]uint32, error) {
	snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(snap)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var ids []uint32
	var failures []error
	for e = windows.Process32First(snap, &entry); e == nil; e = windows.Process32Next(snap, &entry) {
		if entry.ProcessID == uint32(os.Getpid()) {
			continue
		}
		h, pe := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, entry.ProcessID)
		if pe != nil {
			continue
		}
		var token windows.Token
		pe = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token)
		if pe == nil {
			user, ue := token.GetTokenUser()
			_ = token.Close()
			if ue == nil && user.User.Sid.String() == sid {
				ids = append(ids, entry.ProcessID)
				if terminate {
					kill, ke := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, entry.ProcessID)
					if ke == nil {
						ke = windows.TerminateProcess(kill, 91)
						if ke == nil {
							state, we := windows.WaitForSingleObject(kill, 5000)
							if we != nil {
								ke = we
							} else if state != windows.WAIT_OBJECT_0 {
								ke = fmt.Errorf("SID process did not terminate")
							}
						}
						_ = windows.CloseHandle(kill)
					}
					if ke != nil {
						failures = append(failures, ke)
					}
				}
			}
		}
		_ = windows.CloseHandle(h)
	}
	if e != nil && !errors.Is(e, windows.ERROR_NO_MORE_FILES) {
		failures = append(failures, e)
	}
	return ids, errors.Join(failures...)
}
func cleanupAccountTasks(j journal) error {
	if j.SID == "" {
		return nil
	}
	script := `$ErrorActionPreference='Stop'; $owned=@(Get-ScheduledTask | Where-Object { $_.Principal.UserId -eq '` + j.SID + `' -or ($_.Principal.UserId -split '\\')[-1] -eq '` + j.Name + `' }); foreach($task in $owned){Stop-ScheduledTask -InputObject $task -ErrorAction SilentlyContinue; Unregister-ScheduledTask -InputObject $task -Confirm:$false}; $left=@(Get-ScheduledTask | Where-Object { $_.Principal.UserId -eq '` + j.SID + `' -or ($_.Principal.UserId -split '\\')[-1] -eq '` + j.Name + `' }); if($left.Count){exit 9}`
	out, e := command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if e != nil {
		return fmt.Errorf("account task cleanup: %w: %s", e, out)
	}
	return nil
}
func deleteAccountProfile(sid string) error {
	if sid == "" {
		return nil
	}
	if !strings.HasPrefix(sid, "S-1-5-21-") {
		return fmt.Errorf("refuse profile removal for non-account SID")
	}
	e := boolCall(userenv.NewProc("DeleteProfileW"), uintptr(unsafe.Pointer(u16(sid))), 0, 0)
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND) || errors.Is(e, windows.ERROR_NOT_FOUND) {
		return nil
	}
	return e
}
func profileAbsent(sid string) error {
	key, e := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+sid, registry.QUERY_VALUE)
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if e != nil {
		return e
	}
	_ = key.Close()
	return fmt.Errorf("user profile registry entry remains")
}
func servicesAbsent(names []string) error {
	if len(names) == 0 {
		return nil
	}
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	for _, name := range names {
		s, e := m.OpenService(name)
		if errors.Is(e, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			continue
		}
		if e != nil {
			return e
		}
		_ = s.Close()
		return fmt.Errorf("service still registered: %s", name)
	}
	return nil
}
func externalCanaryPaths(j journal) []string {
	return []string{filepath.Join(os.Getenv("ProgramData"), j.Name+"-write-canary"), filepath.Join(os.Getenv("PUBLIC"), j.Name+"-write-canary"), filepath.Join(os.Getenv("SystemRoot"), "Temp", j.Name+"-write-canary"), filepath.Join(filepath.Dir(j.Dir), "tools", j.Name+"-write-canary")}
}
func removeCanaries(j journal) error {
	for _, path := range externalCanaryPaths(j) {
		if _, e := os.Lstat(path); os.IsNotExist(e) {
			continue
		}
		if e := safeRemoveTree(path); e != nil {
			return e
		}
	}
	return nil
}
