//go:build windows && amd64

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func validateJournal(o options, j journal) error {
	tag := filepath.Base(j.Dir)
	if !strings.Contains("ijwsabqc", tag) || len(tag) != 1 {
		return errors.New("unexpected journal tag")
	}
	if j.Dir != filepath.Join(o.Root, tag) || j.Name != "s3"+o.ID+tag || j.Group != j.Name+"g" || j.Job != `Global\auriga-s3-`+o.ID+"-"+tag {
		return errors.New("journal resource outside run namespace")
	}
	if j.Profile != "" && j.Profile != "auriga.s3."+o.ID {
		return errors.New("unexpected AppContainer profile")
	}
	if j.VHD != "" && j.VHD != filepath.Join(j.Dir, "quota.vhd") {
		return errors.New("unexpected VHD path")
	}
	for _, name := range j.Jobs {
		if !strings.HasPrefix(name, j.Job+"-") {
			return errors.New("unexpected named job")
		}
	}
	if j.SID != "" {
		if _, e := windows.StringToSid(j.SID); e != nil {
			return e
		}
	}
	for _, key := range j.Filters {
		if _, e := windows.GUIDFromString(key); e != nil {
			return e
		}
	}
	return nil
}
func cleanupExperiment(o options, r *Result) error {
	paths, e := filepath.Glob(filepath.Join(o.Root, "*.state.json"))
	if e != nil {
		return e
	}
	if len(paths) == 0 {
		e = os.RemoveAll(o.Root)
		r.check("no_journals_and_run_root_removed", e == nil, errorString(e))
		return nil
	}
	var errs []error
	for _, path := range paths {
		var j journal
		if e = readJSON(path, &j); e != nil {
			errs = append(errs, e)
			continue
		}
		if e = validateJournal(o, j); e != nil {
			errs = append(errs, e)
			continue
		}
		checkStart := len(r.Checks)
		ce := cleanJournal(j)
		r.check("cleanup_"+j.Name, ce == nil, errorString(ce))
		if ce != nil {
			errs = append(errs, ce)
		}
		// The shim lease/stop marker releases the named job. Never count closing our own handle as deletion.
		absent := false
		until := time.Now().Add(8 * time.Second)
		for time.Now().Before(until) {
			h, je := openJob(j.Job)
			if errors.Is(je, windows.ERROR_FILE_NOT_FOUND) {
				absent = true
				break
			}
			if je != nil {
				break
			}
			_ = windows.CloseHandle(h)
			time.Sleep(150 * time.Millisecond)
		}
		r.check("job_absent_"+j.Name, absent, nil)
		for _, name := range j.Jobs {
			h, je := openJob(name)
			if je == nil {
				_ = windows.CloseHandle(h)
			}
			r.check("job_absent_"+name, errors.Is(je, windows.ERROR_FILE_NOT_FOUND), errorString(je))
		}
		if j.SID != "" {
			ae := rightsAbsent(j.SID)
			r.check("LSA_rights_absent_"+j.Name, ae == nil, errorString(ae))
		}
		var buf *byte
		v, _, _ := netapi.NewProc("NetUserGetInfo").Call(0, uintptr(unsafe.Pointer(u16(j.Name))), 0, uintptr(unsafe.Pointer(&buf)))
		if v == 0 {
			_, _, _ = netapi.NewProc("NetApiBufferFree").Call(uintptr(unsafe.Pointer(buf)))
		}
		r.check("account_absent_"+j.Name, v == 2221, fmt.Sprintf("NET_API_STATUS=%d", v))
		buf = nil
		v, _, _ = netapi.NewProc("NetLocalGroupGetInfo").Call(0, uintptr(unsafe.Pointer(u16(j.Group))), 0, uintptr(unsafe.Pointer(&buf)))
		if v == 0 {
			_, _, _ = netapi.NewProc("NetApiBufferFree").Call(uintptr(unsafe.Pointer(buf)))
		}
		r.check("group_absent_"+j.Name, v == 2220, fmt.Sprintf("NET_API_STATUS=%d", v))
		if len(j.Filters) > 0 {
			h, fe := engine(false)
			if fe != nil {
				errs = append(errs, fe)
			} else {
				for _, key := range j.Filters {
					g, _ := windows.GUIDFromString(key)
					present, fe := filterPresent(h, g)
					r.check("filter_absent_"+key, fe == nil && !present, errorString(fe))
				}
				closeEngine(h)
			}
		}
		if j.Profile != "" {
			present, pe := exemptionState(j.Profile)
			r.check("loopback_exemption_absent", pe == nil && !present, errorString(pe))
			profilePath := filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", j.Profile)
			_, pe = os.Stat(profilePath)
			r.check("profile_directory_absent", os.IsNotExist(pe), nil)
		}
		// Remove only validated exact fixture directories. Reparse mount points were detached above.
		if ce == nil && absent && aggregate(r.Checks[checkStart:], nil) == "pass" {
			re := os.RemoveAll(j.Dir)
			r.check("directory_ACL_and_VHD_removed_"+j.Name, re == nil, errorString(re))
			if re == nil {
				je := os.Remove(path)
				r.check("journal_removed_"+j.Name, je == nil, errorString(je))
			}
		}
	}
	if len(errs) == 0 && aggregate(r.Checks, nil) == "pass" {
		e = os.RemoveAll(o.Root)
		r.check("run_root_and_all_test_ACLs_removed", e == nil, errorString(e))
		if e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
func rightsAbsent(sidString string) error {
	sid, e := windows.StringToSid(sidString)
	if e != nil {
		return e
	}
	var h uintptr
	oa := lsaObject{}
	oa.Length = uint32(unsafe.Sizeof(oa))
	if e = statusCall(adv.NewProc("LsaOpenPolicy"), 0, uintptr(unsafe.Pointer(&oa)), 0x800, uintptr(unsafe.Pointer(&h))); e != nil {
		return e
	}
	defer adv.NewProc("LsaClose").Call(h)
	var rights *lsaString
	var count uint32
	v, _, _ := adv.NewProc("LsaEnumerateAccountRights").Call(h, uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&rights)), uintptr(unsafe.Pointer(&count)))
	if v == 0 {
		defer adv.NewProc("LsaFreeMemory").Call(uintptr(unsafe.Pointer(rights)))
		if count == 0 {
			return nil
		}
		return fmt.Errorf("remaining LSA rights: %d", count)
	}
	if v == 0xc0000034 {
		return nil
	}
	return fmt.Errorf("LsaEnumerateAccountRights: 0x%x", v)
}
