//go:build windows && amd64

package main

import (
	"encoding/binary"
	"errors"
	"golang.org/x/sys/windows"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

func junctionBuffer(target string) []byte {
	sub := windows.StringToUTF16(`\??\` + target)
	print := windows.StringToUTF16(target)
	b := make([]byte, 16+2*(len(sub)+len(print)))
	binary.LittleEndian.PutUint32(b, 0xa0000003)
	binary.LittleEndian.PutUint16(b[4:], uint16(len(b)-8))
	binary.LittleEndian.PutUint16(b[10:], uint16((len(sub)-1)*2))
	binary.LittleEndian.PutUint16(b[12:], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(b[14:], uint16((len(print)-1)*2))
	for i, v := range append(sub, print...) {
		binary.LittleEndian.PutUint16(b[16+2*i:], v)
	}
	return b
}
func setJunction(path, target string) error {
	h, e := windows.CreateFile(u16(path), windows.GENERIC_WRITE, 7, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	buffer := junctionBuffer(target)
	var returned uint32
	return windows.DeviceIoControl(h, 0x900a4, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
}
func errorCode(e error) uint32 {
	if e == nil {
		return 0
	}
	var code syscall.Errno
	if errors.As(e, &code) {
		return uint32(code)
	}
	return ^uint32(0)
}
func win32Evidence(e error) map[string]any {
	return map[string]any{"win32Error": errorCode(e), "error": errorString(e)}
}
func win32Code(v any) uint32 {
	m, ok := v.(map[string]any)
	if !ok {
		return ^uint32(0)
	}
	v2, ok := m["win32Error"].(float64)
	if !ok {
		return ^uint32(0)
	}
	return uint32(v2)
}
func securityWorker(mode string, a []string, result map[string]any) bool {
	switch mode {
	case "broker":
		if len(a) != 3 {
			os.Exit(2)
		}
		for k, v := range brokerWorker(a[0], a[1], a[2]) {
			result[k] = v
		}
	case "dns":
		for _, name := range a {
			result[name] = map[string]any{"win32Error": nativeDNS(name), "bypassCache": true}
		}
	case "job-security":
		if len(a) != 2 {
			os.Exit(2)
		}
		h, e := openJob(a[0])
		if e == nil {
			_ = windows.CloseHandle(h)
		}
		result["jobOpen"] = win32Evidence(e)
		v, _, je := kernel.NewProc("OpenJobObjectW").Call(windows.WRITE_DAC, 0, uintptr(unsafe.Pointer(u16(a[0]))))
		if v != 0 {
			_ = windows.CloseHandle(windows.Handle(v))
			je = nil
		}
		result["jobWriteDACL"] = win32Evidence(je)
		pid, _ := strconv.ParseUint(a[1], 10, 32)
		h, pe := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_DUP_HANDLE|windows.PROCESS_VM_WRITE, false, uint32(pid))
		if pe == nil {
			_ = windows.CloseHandle(h)
		}
		result["shimOpen"] = win32Evidence(pe)
		own, ce := windows.CreateJobObject(nil, nil)
		if ce == nil {
			_ = windows.CloseHandle(own)
		}
		result["ownJobControl"] = win32Evidence(ce)
	case "breakaway":
		if len(a) != 1 {
			os.Exit(2)
		}
		c := exec.Command(os.Args[0], "_work", a[0], "sleep")
		c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_NO_WINDOW}
		e := c.Start()
		result["create"] = win32Evidence(e)
		if e == nil {
			result["pid"] = c.Process.Pid
			_ = c.Process.Kill()
			_ = c.Wait()
		}
	case "root-security":
		if len(a) != 1 {
			os.Exit(2)
		}
		for name, access := range map[string]uint32{"rootWriteDACL": windows.WRITE_DAC, "rootDelete": windows.DELETE} {
			h, e := windows.CreateFile(u16(a[0]), access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if e == nil {
				_ = windows.CloseHandle(h)
			}
			result[name] = win32Evidence(e)
		}
		work := filepath.Join(a[0], "work")
		target := filepath.Join(work, "junction-target")
		canary := filepath.Join(work, "junction-canary")
		e := os.Mkdir(target, 0700)
		if e == nil {
			e = os.Mkdir(canary, 0700)
		}
		if e == nil {
			e = setJunction(canary, target)
		}
		result["junctionControl"] = win32Evidence(e)
		if e == nil {
			h, he := windows.CreateFile(u16(canary), windows.GENERIC_WRITE, 7, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if he == nil {
				buffer := make([]byte, 8)
				binary.LittleEndian.PutUint32(buffer, 0xa0000003)
				var returned uint32
				he = windows.DeviceIoControl(h, 0x900ac, &buffer[0], 8, nil, 0, &returned, nil)
				_ = windows.CloseHandle(h)
			}
			result["junctionControlCleanup"] = win32Evidence(he)
			if he == nil {
				_ = os.Remove(canary)
			}
		}
		result["junctionWrite"] = win32Evidence(setJunction(a[0], target))
	case "write-probes":
		if len(a) < 2 {
			os.Exit(2)
		}
		for _, base := range a[1:] {
			path := filepath.Join(base, a[0]+"-write-canary")
			file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e == nil {
				_ = file.Close()
				_ = os.Remove(path)
			}
			result[base] = win32Evidence(e)
		}
	case "listen":
		for _, addr := range a {
			l, e := net.Listen("tcp", addr)
			result[addr] = win32Evidence(e)
			if e == nil {
				_ = l.Close()
			}
		}
	case "udp":
		for _, addr := range a {
			c, e := net.DialTimeout("udp", addr, time.Second)
			if e == nil {
				_ = c.SetDeadline(time.Now().Add(time.Second))
				_, e = c.Write([]byte("auriga-s3"))
				if e == nil {
					b := make([]byte, 32)
					_, e = c.Read(b)
				}
				_ = c.Close()
			}
			result[addr] = win32Evidence(e)
		}
	case "unc":
		for _, path := range a {
			_, e := os.Stat(path)
			result[path] = win32Evidence(e)
		}
	default:
		return false
	}
	return true
}
