package main

import (
	"golang.org/x/sys/windows"
	"time"
)

func creationToken(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return time.Unix(0, created.Nanoseconds()).UTC().Format(time.RFC3339Nano), nil
}
