//go:build windows && amd64

package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Copy only system/profile variables from a non-inheriting native environment; never
// pass the runner's arbitrary environment (which can contain Actions credentials).
func nativeEnvironment(token windows.Token) (map[string]string, error) {
	var block *uint16
	if e := boolCall(userenv.NewProc("CreateEnvironmentBlock"), uintptr(unsafe.Pointer(&block)), uintptr(token), 0); e != nil {
		return nil, e
	}
	defer userenv.NewProc("DestroyEnvironmentBlock").Call(uintptr(unsafe.Pointer(block)))
	var units []uint16
	previousZero := false
	for offset := 0; offset < 1<<20; offset++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(block), offset*2))
		units = append(units, v)
		if v == 0 && previousZero {
			return decodeEnvironment(units)
		}
		previousZero = v == 0
	}
	return nil, fmt.Errorf("native environment exceeds size bound")
}
func decodeEnvironment(block []uint16) (map[string]string, error) {
	if len(block) < 2 || block[len(block)-1] != 0 || block[len(block)-2] != 0 {
		return nil, fmt.Errorf("environment must end with two UTF-16 NULs")
	}
	result := map[string]string{}
	start := 0
	for i, v := range block {
		if v != 0 {
			continue
		}
		if i == start {
			break
		}
		entry := windows.UTF16ToString(block[start:i])
		start = i + 1
		if strings.HasPrefix(entry, "=") {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("malformed environment entry")
		}
		result[strings.ToUpper(key)] = value
	}
	return result, nil
}
func encodeEnvironment(values map[string]string) ([]uint16, error) {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid environment entry")
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToUpper(keys[i]) < strings.ToUpper(keys[j]) })
	var block []uint16
	for _, key := range keys {
		block = append(block, windows.StringToUTF16(key+"="+values[key])...)
	}
	block = append(block, 0)
	if len(keys) == 0 {
		block = append(block, 0)
	}
	return block, nil
}
func appEnvironment(token windows.Token, dir string) ([]uint16, map[string]any, error) {
	target, e := nativeEnvironment(token)
	if e != nil {
		return nil, nil, e
	}
	var ownerToken windows.Token
	if e = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &ownerToken); e != nil {
		return nil, nil, e
	}
	defer ownerToken.Close()
	owner, e := nativeEnvironment(ownerToken)
	if e != nil {
		return nil, nil, e
	}
	values, e := appEnvironmentValues(target, owner, dir)
	if e != nil {
		return nil, nil, e
	}
	block, e := encodeEnvironment(values)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return block, map[string]any{"keys": keys, "unicode": true, "doubleNul": len(block) >= 2 && block[len(block)-1] == 0 && block[len(block)-2] == 0, "inheritRunnerEnvironment": false, "profileOwner": "SYSTEM (CreateAppContainerProfile caller)", "profileBase": "native LOCALAPPDATA; not inherited secrets", "nodePathValidation": "absolute existing executable"}, e
}
func appEnvironmentValues(target, owner map[string]string, dir string) (map[string]string, error) {
	values := map[string]string{}
	for _, key := range []string{"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "COMMONPROGRAMFILES", "USERPROFILE", "USERNAME", "USERDOMAIN", "APPDATA", "LOCALAPPDATA"} {
		value := target[key]
		if value == "" {
			value = owner[key]
		}
		if value != "" {
			values[key] = value
		}
	}
	// This profile was registered by SYSTEM. Windows resolves its AppContainer profile
	// from LOCALAPPDATA before rewriting LOCALAPPDATA/TEMP for the LowBox process.
	if owner["LOCALAPPDATA"] != "" {
		values["LOCALAPPDATA"] = owner["LOCALAPPDATA"]
	}
	for _, key := range []string{"SYSTEMROOT", "USERPROFILE", "LOCALAPPDATA"} {
		if values[key] == "" {
			return nil, fmt.Errorf("AppContainer native environment missing %s", key)
		}
	}
	values["PATH"] = filepath.Join(values["SYSTEMROOT"], "System32")
	values["TEMP"] = dir
	values["TMP"] = dir
	return values, nil
}
