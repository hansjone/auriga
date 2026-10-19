//go:build windows && amd64

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func diskpart(script string) (string, error) {
	c := exec.Command("diskpart.exe")
	c.Stdin = strings.NewReader(script + "\r\nexit\r\n")
	b, e := c.CombinedOutput()
	return string(b), e
}
func detachVHD(path string) error {
	if _, e := os.Stat(path); os.IsNotExist(e) {
		return nil
	}
	out, e := diskpart(fmt.Sprintf("select vdisk file=\"%s\"\r\ndetach vdisk", path))
	if e != nil {
		return fmt.Errorf("detach VHD: %w: %s", e, out)
	}
	// diskpart sometimes exits zero after command failure. Query the storage subsystem independently.
	out, e = command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$d=Get-DiskImage -ImagePath '`+strings.ReplaceAll(path, "'", "''")+`' -ErrorAction Stop; if($d.Attached){exit 9}`)
	if e != nil {
		return fmt.Errorf("verify detached VHD: %w: %s", e, out)
	}
	return nil
}
func quotaExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "q")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	f.VHD = filepath.Join(f.Dir, "quota.vhd")
	if e = f.save(); e != nil {
		return e
	}
	mount := filepath.Join(f.Dir, "volume")
	if e = os.Mkdir(mount, 0700); e != nil {
		return e
	}
	out, e := diskpart(fmt.Sprintf("create vdisk file=\"%s\" maximum=128 type=expandable\r\nselect vdisk file=\"%s\"\r\nattach vdisk\r\ncreate partition primary\r\nformat fs=ntfs quick label=AurigaS3\r\nassign mount=\"%s\"", f.VHD, f.VHD, mount))
	defer func() { err = join(err, detachVHD(f.VHD)) }()
	var fs [64]uint16
	var serial uint32
	ve := windows.GetVolumeInformation(u16(mount+`\`), nil, 0, &serial, nil, nil, &fs[0], uint32(len(fs)))
	if e != nil || ve != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		r.unsupported("temporary_NTFS_volume", map[string]any{"diskpart": out, "error": errorString(join(e, ve))})
		return nil
	}
	// Never apply quota to an ordinary directory on the host volume, even when diskpart exits zero.
	tempGUID, ge := volumeGUID(mount + `\`)
	var parentRoot [windows.MAX_PATH]uint16
	pe := windows.GetVolumePathName(u16(f.Dir), &parentRoot[0], uint32(len(parentRoot)))
	parentGUID, pge := volumeGUID(windows.UTF16ToString(parentRoot[:]))
	systemGUID, sge := volumeGUID(filepath.VolumeName(os.Getenv("SystemRoot")) + `\`)
	if ge != nil || pe != nil || pge != nil || sge != nil || strings.EqualFold(tempGUID, parentGUID) || strings.EqualFold(tempGUID, systemGUID) {
		return fmt.Errorf("refuse quota: temporary volume not independently identified (temporary=%q parent=%q system=%q): %w", tempGUID, parentGUID, systemGUID, join(join(ge, pe), join(pge, sge)))
	}
	r.check("temporary_NTFS_volume", true, map[string]any{"fileSystem": "NTFS", "serial": serial, "temporaryVolume": tempGUID, "parentVolume": parentGUID, "systemVolume": systemGUID})
	// SetNamedSecurityInfo on the mount-point name can update the host reparse-point ACL.
	// Address the mounted volume itself by GUID, then explicitly protect its data directory.
	if e = dacl(tempGUID, instanceACL(f.sid.String())); e != nil {
		return e
	}
	dataDir := tempGUID + "probe-data"
	if e = os.MkdirAll(dataDir, 0700); e != nil {
		return e
	}
	if e = dacl(dataDir, instanceACL(f.sid.String())); e != nil {
		return e
	}
	sd, se := windows.GetNamedSecurityInfo(dataDir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if se != nil {
		return se
	}
	r.observe("quota_data_ACL", map[string]any{"sddl": sd.String(), "instanceSID": f.sid.String(), "volumeRoot": tempGUID})
	target := filepath.Join(mount, "probe-data", "large.bin")
	baseline, e := runWork(f, "write", target)
	if e != nil {
		return e
	}
	baselineOK := baseline["denied"] == false && baseline["bytesWritten"] == float64(32<<20)
	r.check("write_baseline_before_quota", baselineOK, baseline)
	if !baselineOK {
		return fmt.Errorf("quota baseline write failed; quota was not enabled: %v", baseline)
	}
	if e = removeExistingFile(target); e != nil {
		return e
	}
	for _, args := range [][]string{{"quota", "enforce", mount + `\`}, {"quota", "modify", mount + `\`, "4194304", "4194304", f.Name}} {
		text, e := command("fsutil.exe", args...)
		if e != nil {
			r.unsupported("NTFS_quota_configuration", map[string]any{"output": text, "error": e.Error()})
			return nil
		}
		r.observe("fsutil_"+args[1], text)
	}
	// Wait for the quota index after deleting the baseline allocation.
	time.Sleep(time.Second)
	data, e := runWork(f, "write", target)
	if e != nil {
		return e
	}
	bytes, _ := data["bytesWritten"].(float64)
	code, _ := data["win32Error"].(float64)
	r.check("quota_4MiB_rejects_32MiB", data["denied"] == true && bytes < 32<<20 && (code == 112 || code == 1295 || code == 1816), data)
	out, e = command("fsutil.exe", "quota", "query", mount+`\`)
	r.observe("quota_query", map[string]any{"output": out, "error": errorString(e)})
	return nil
}
func removeExistingFile(path string) error {
	e := os.Remove(path)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func volumeGUID(path string) (string, error) {
	var name [windows.MAX_PATH]uint16
	e := windows.GetVolumeNameForVolumeMountPoint(u16(path), &name[0], uint32(len(name)))
	if e != nil {
		return "", e
	}
	return windows.UTF16ToString(name[:]), nil
}
