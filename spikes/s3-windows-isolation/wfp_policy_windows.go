//go:build windows && amd64

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

var listen4 = guid("88bb5dad-76d7-4227-9c71-df0a3ed7be7e")
var listen6 = guid("7ac9de24-17dd-4814-b4bd-a9fbc95a321b")
var localAddressCondition = guid("d9ee00de-c1ef-4617-bfe3-ffd8f5a08957")
var localPortCondition = guid("0c1ba1af-5765-453f-af22-a8f791ac775b")

func policyFlags(allow, persistent bool) uint32 {
	var flags uint32
	if persistent {
		flags |= 1
	}
	if !allow {
		flags |= 8
	}
	return flags
}

type wfpProvider struct {
	Key         windows.GUID
	Display     displayData
	Flags       uint32
	Data        byteBlob
	ServiceName *uint16
}
type wfpSubLayer struct {
	Key      windows.GUID
	Display  displayData
	Flags    uint32
	Provider *windows.GUID
	Data     byteBlob
	Weight   uint16
}

func preparePolicyIDs(f *fixture) error {
	if f.Provider != "" {
		return nil
	}
	p, e := windows.GenerateGUID()
	if e != nil {
		return e
	}
	s, e := windows.GenerateGUID()
	if e != nil {
		return e
	}
	f.Provider = p.String()
	f.SubLayer = s.String()
	return f.save()
}
func ensurePolicy(f *fixture, h windows.Handle) error {
	p, e := windows.GUIDFromString(f.Provider)
	if e != nil {
		return e
	}
	s, e := windows.GUIDFromString(f.SubLayer)
	if e != nil {
		return e
	}
	provider := wfpProvider{Key: p, Display: displayData{Name: u16("Auriga S3 " + f.Name)}, Flags: 1}
	if e = statusCall(fw.NewProc("FwpmProviderAdd0"), uintptr(h), uintptr(unsafe.Pointer(&provider)), 0); e != nil {
		return e
	}
	sub := wfpSubLayer{Key: s, Display: displayData{Name: u16("Auriga S3 " + f.Name)}, Flags: 1, Provider: &p, Weight: 0xffff}
	return statusCall(fw.NewProc("FwpmSubLayerAdd0"), uintptr(h), uintptr(unsafe.Pointer(&sub)), 0)
}
func policyFilterKeys(h windows.Handle, provider windows.GUID) ([]string, error) {
	var enum windows.Handle
	if e := statusCall(fw.NewProc("FwpmFilterCreateEnumHandle0"), uintptr(h), 0, uintptr(unsafe.Pointer(&enum))); e != nil {
		return nil, e
	}
	defer fw.NewProc("FwpmFilterDestroyEnumHandle0").Call(uintptr(h), uintptr(enum))
	var keys []string
	for {
		var entries **wfpFilter
		var count uint32
		if e := statusCall(fw.NewProc("FwpmFilterEnum0"), uintptr(h), uintptr(enum), 100, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count))); e != nil {
			return nil, e
		}
		for _, filter := range unsafe.Slice(entries, int(count)) {
			if filter.Provider != nil && *filter.Provider == provider {
				keys = append(keys, filter.Key.String())
			}
		}
		fw.NewProc("FwpmFreeMemory0").Call(uintptr(unsafe.Pointer(&entries)))
		if count < 100 {
			return keys, nil
		}
	}
}
func policyObjectPresent(h windows.Handle, kind, key string) (bool, error) {
	g, e := windows.GUIDFromString(key)
	if e != nil {
		return false, e
	}
	var value *byte
	v, _, _ := fw.NewProc("Fwpm"+kind+"GetByKey0").Call(uintptr(h), uintptr(unsafe.Pointer(&g)), uintptr(unsafe.Pointer(&value)))
	if status32(v) == 0 {
		defer fw.NewProc("FwpmFreeMemory0").Call(uintptr(unsafe.Pointer(&value)))
		return true, nil
	}
	missing := uint32(0x80320005)
	if kind == "SubLayer" {
		missing = 0x80320007
	}
	return wfpPresence(v, missing, "Fwpm"+kind+"GetByKey0")
}
func removePolicy(j journal) error {
	if j.Provider == "" {
		return removeFilters(j.Filters)
	}
	p, e := windows.GUIDFromString(j.Provider)
	if e != nil {
		return e
	}
	h, e := engine(false)
	if e != nil {
		return e
	}
	defer closeEngine(h)
	keys, e := policyFilterKeys(h, p)
	if e != nil {
		return e
	}
	keys = append(keys, j.Filters...)
	if e = statusCall(fw.NewProc("FwpmTransactionBegin0"), uintptr(h), 0); e != nil {
		return e
	}
	committed := false
	defer func() {
		if !committed {
			_ = statusCall(fw.NewProc("FwpmTransactionAbort0"), uintptr(h))
		}
	}()
	for _, key := range keys {
		g, e := windows.GUIDFromString(key)
		if e != nil {
			return e
		}
		v, _, _ := fw.NewProc("FwpmFilterDeleteByKey0").Call(uintptr(h), uintptr(unsafe.Pointer(&g)))
		if _, e = wfpPresence(v, 0x80320003, "FwpmFilterDeleteByKey0"); e != nil {
			return e
		}
	}
	for _, object := range []struct {
		kind, key string
		missing   uint32
	}{{"SubLayer", j.SubLayer, 0x80320007}, {"Provider", j.Provider, 0x80320005}} {
		g, e := windows.GUIDFromString(object.key)
		if e != nil {
			return e
		}
		v, _, _ := fw.NewProc("Fwpm"+object.kind+"DeleteByKey0").Call(uintptr(h), uintptr(unsafe.Pointer(&g)))
		if _, e = wfpPresence(v, object.missing, "delete "+object.kind); e != nil {
			return e
		}
	}
	if e = statusCall(fw.NewProc("FwpmTransactionCommit0"), uintptr(h)); e != nil {
		return e
	}
	committed = true
	left, e := policyFilterKeys(h, p)
	if e != nil {
		return e
	}
	if len(left) != 0 {
		return fmt.Errorf("provider has %d orphan filters", len(left))
	}
	for _, object := range []struct{ kind, key string }{{"SubLayer", j.SubLayer}, {"Provider", j.Provider}} {
		present, e := policyObjectPresent(h, object.kind, object.key)
		if e != nil {
			return e
		}
		if present {
			return fmt.Errorf("WFP %s still present", object.kind)
		}
	}
	return nil
}
