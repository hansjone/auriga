//go:build windows && amd64

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var fw = windows.NewLazySystemDLL("fwpuclnt.dll")

func guid(s string) windows.GUID {
	g, e := windows.GUIDFromString("{" + s + "}")
	if e != nil {
		panic(e)
	}
	return g
}

var connect4 = guid("c38d57d1-05a7-4c33-904f-7fbceee60e82")
var connect6 = guid("4a72393b-319f-44bc-84c3-ba54dcb3b6b4")
var userCondition = guid("af043a0a-b34d-4f86-979c-c90371af6e66")
var addressCondition = guid("b235ae9a-1d64-49b8-a44c-5ff3d9095045")
var portCondition = guid("c35a604d-d22b-4e1a-91b4-68f674ee674b")

// x64 SDK ABI. Union storage is pointer-sized and aligned to eight bytes.
type fwpValue struct {
	Type  uint32
	_     uint32
	Value uintptr
}
type byteBlob struct {
	Size uint32
	Data *byte
}
type displayData struct{ Name, Description *uint16 }
type filterCondition struct {
	Key   windows.GUID
	Match uint32
	Value fwpValue
}
type filterAction struct {
	Type uint32
	Key  windows.GUID
}
type wfpFilter struct {
	Key             windows.GUID
	Display         displayData
	Flags           uint32
	Provider        *windows.GUID
	Data            byteBlob
	Layer, SubLayer windows.GUID
	Weight          fwpValue
	Count           uint32
	Conditions      *filterCondition
	Action          filterAction
	Context         [2]uint64
	Reserved        *windows.GUID
	ID              uint64
	EffectiveWeight fwpValue
}
type wfpSession struct {
	Key                 windows.GUID
	Display             displayData
	Flags, Timeout, PID uint32
	SID                 *windows.SID
	Username            *uint16
	KernelMode          int32
}

func userSDValue(blob *byteBlob) fwpValue {
	return fwpValue{Type: 14, Value: uintptr(unsafe.Pointer(blob))}
}
func endpointConditions(port uint16) []filterCondition {
	return []filterCondition{{Key: addressCondition, Value: fwpValue{Type: 3, Value: uintptr(binary.BigEndian.Uint32(net.ParseIP("127.0.0.1").To4()))}}, {Key: portCondition, Value: fwpValue{Type: 2, Value: uintptr(port)}}}
}
func engine(dynamic bool) (windows.Handle, error) {
	var h windows.Handle
	s := wfpSession{}
	if dynamic {
		s.Flags = 1
	}
	e := statusCall(fw.NewProc("FwpmEngineOpen0"), 0, 10, 0, uintptr(unsafe.Pointer(&s)), uintptr(unsafe.Pointer(&h)))
	return h, e
}
func closeEngine(h windows.Handle) { _ = statusCall(fw.NewProc("FwpmEngineClose0"), uintptr(h)) }
func filterPresent(h windows.Handle, g windows.GUID) (bool, error) {
	var p *wfpFilter
	v, _, _ := fw.NewProc("FwpmFilterGetByKey0").Call(uintptr(h), uintptr(unsafe.Pointer(&g)), uintptr(unsafe.Pointer(&p)))
	if v == 0 {
		defer fw.NewProc("FwpmFreeMemory0").Call(uintptr(unsafe.Pointer(&p)))
		return true, nil
	}
	if v == 0x80320003 {
		return false, nil
	}
	return false, fmt.Errorf("FwpmFilterGetByKey0: 0x%x", v)
}
func removeFilters(keys []string) error {
	h, e := engine(false)
	if e != nil {
		return e
	}
	defer closeEngine(h)
	var err error
	for _, k := range keys {
		g, e := windows.GUIDFromString(k)
		if e != nil {
			err = join(err, e)
			continue
		}
		v, _, _ := fw.NewProc("FwpmFilterDeleteByKey0").Call(uintptr(h), uintptr(unsafe.Pointer(&g)))
		if v != 0 && v != 0x80320003 {
			err = join(err, fmt.Errorf("delete WFP filter %s: 0x%x", k, v))
		}
	}
	return err
}
func addUserFilter(f *fixture, h windows.Handle, layer windows.GUID, allow bool, port uint16, persistent bool) (windows.GUID, error) {
	key, e := windows.GenerateGUID()
	if e != nil {
		return key, e
	}
	f.Filters = append(f.Filters, key.String())
	if e = f.save(); e != nil {
		return key, e
	}
	// ALE_USER_ID expects an SD byte blob, not FWP_SID. FWP_ACTRL_MATCH_FILTER == 1.
	sd, e := windows.SecurityDescriptorFromString("O:SYG:SYD:(A;;CC;;;" + f.sid.String() + ")")
	if e != nil {
		return key, e
	}
	blob := byteBlob{sd.Length(), (*byte)(unsafe.Pointer(sd))}
	// FWP_VALUE0 stores this pointer in an untyped union. Pin the blob so stack growth/GC
	// cannot invalidate a uintptr that the Go collector cannot discover.
	var pin runtime.Pinner
	pin.Pin(&blob)
	defer pin.Unpin()
	conditions := []filterCondition{{Key: userCondition, Value: userSDValue(&blob)}}
	action := uint32(0x1001)
	weight := uintptr(1)
	if allow {
		action = 0x1002
		weight = 15
		conditions = append(conditions, endpointConditions(port)...)
	}
	filter := wfpFilter{Key: key, Display: displayData{Name: u16("Auriga S3 " + f.Name)}, Layer: layer, Weight: fwpValue{Type: 1, Value: weight}, Count: uint32(len(conditions)), Conditions: &conditions[0], Action: filterAction{Type: action}}
	if persistent {
		filter.Flags = 1
	}
	// Default sublayer keeps allow/deny arbitration in one sublayer. No global block or policy reset.
	var id uint64
	e = statusCall(fw.NewProc("FwpmFilterAdd0"), uintptr(h), uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&id)))
	runtime.KeepAlive(sd)
	runtime.KeepAlive(blob)
	runtime.KeepAlive(conditions)
	return key, e
}
func wfpObserver(args []string) {
	h, e := engine(false)
	if e != nil {
		emit(map[string]any{"error": e.Error()})
		os.Exit(1)
	}
	defer closeEngine(h)
	result := map[string]any{}
	for _, k := range args {
		g, e := windows.GUIDFromString(k)
		if e != nil {
			os.Exit(2)
		}
		present, e := filterPresent(h, g)
		result[k] = map[string]any{"present": present, "error": errorString(e)}
	}
	emit(result)
}
func observer(keys []string) (map[string]any, error) {
	c := exec.Command(os.Args[0], append([]string{"_wfp-observer"}, keys...)...)
	b, e := c.Output()
	if e != nil {
		return nil, e
	}
	var data map[string]any
	e = json.Unmarshal(b, &data)
	return data, e
}

type installRequest struct {
	Options options
	Port    uint16
	Dynamic bool
}

// Deliberately leave the engine handle open: process exit, rather than an explicit close,
// is the event whose effect is measured by the observer process.
func wfpInstaller() {
	var q installRequest
	if e := json.NewDecoder(os.Stdin).Decode(&q); e != nil {
		os.Exit(2)
	}
	if e := validateOptions(q.Options); e != nil {
		os.Exit(2)
	}
	f := &fixture{o: q.Options, journalPath: filepath.Join(q.Options.Root, "w.state.json")}
	e := readJSON(f.journalPath, &f.journal)
	if e == nil {
		e = validateJournal(q.Options, f.journal)
	}
	if e == nil {
		f.sid, _, _, e = windows.LookupSID("", f.Name)
	}
	var h windows.Handle
	if e == nil {
		h, e = engine(q.Dynamic)
	}
	if e == nil {
		if q.Dynamic {
			_, e = addUserFilter(f, h, connect4, false, 0, false)
		} else {
			for _, layer := range []windows.GUID{connect4, connect6} {
				if _, e = addUserFilter(f, h, layer, false, 0, true); e != nil {
					break
				}
			}
			if e == nil {
				_, e = addUserFilter(f, h, connect4, true, q.Port, true)
			}
		}
	}
	emit(map[string]any{"error": errorString(e)})
	if e != nil {
		os.Exit(1)
	}
}
func installInProcess(f *fixture, port uint16, dynamic bool) error {
	b, e := json.Marshal(installRequest{f.o, port, dynamic})
	if e != nil {
		return e
	}
	c := exec.Command(os.Args[0], "_wfp-install")
	c.Stdin = bytes.NewReader(b)
	out, e := c.CombinedOutput()
	// Always reload the write-ahead journal, including partially installed rules on failure.
	re := readJSON(f.journalPath, &f.journal)
	if e != nil {
		return join(fmt.Errorf("WFP installer: %w: %s", e, out), re)
	}
	return re
}
func listening(network, address string) (net.Listener, error) {
	l, e := net.Listen(network, address)
	if e == nil {
		go func() {
			for {
				c, e := l.Accept()
				if e != nil {
					return
				}
				_ = c.Close()
			}
		}()
	}
	return l, e
}
func wfpExperiment(o options, r *Result) (err error) {
	f, e := newFixture(o, "w")
	if e != nil {
		return e
	}
	defer func() { err = join(err, f.close()) }()
	allowed, e := listening("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer allowed.Close()
	denied, e := listening("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer denied.Close()
	// Positive baselines ensure a refusal/listener failure cannot be reported as firewall success.
	addresses := []string{allowed.Addr().String(), denied.Addr().String()}
	otherIP, oe := listening("tcp4", fmt.Sprintf("127.0.0.2:%d", allowed.Addr().(*net.TCPAddr).Port))
	if oe == nil {
		defer otherIP.Close()
		addresses = append(addresses, otherIP.Addr().String())
	} else {
		r.unsupported("second_loopback_IP", oe.Error())
	}
	v6, e6 := listening("tcp6", "[::1]:0")
	if e6 == nil {
		defer v6.Close()
		addresses = append(addresses, v6.Addr().String())
	} else {
		r.unsupported("IPv6_listener", e6.Error())
	}
	baseline, e := runWork(f, append([]string{"network"}, addresses...)...)
	if e != nil {
		return e
	}
	for _, a := range addresses {
		r.check("baseline_connect_"+a, connected(baseline, a), baseline[a])
	}
	if e = installInProcess(f, uint16(allowed.Addr().(*net.TCPAddr).Port), false); e != nil {
		return e
	}
	keys := append([]string(nil), f.Filters...)
	seen, e := observer(keys)
	if e != nil {
		return e
	}
	for _, k := range keys {
		v, _ := seen[k].(map[string]any)
		r.check("persistent_filter_after_agent_exit_"+k, v["present"] == true && v["error"] == "", v)
	}
	for pass := 0; pass < 2; pass++ {
		data, e := runWork(f, append([]string{"network"}, addresses...)...)
		if e != nil {
			return e
		}
		r.check(fmt.Sprintf("allow_exact_IP_port_restart_%d", pass), connected(data, addresses[0]), data[addresses[0]])
		for _, a := range addresses[1:] {
			r.check(fmt.Sprintf("deny_other_loopback_restart_%d_%s", pass, a), !connected(data, a), data[a])
		}
	}
	// A dynamic filter disappears when its management process exits; an independent observer checks it.
	if e = installInProcess(f, 0, true); e != nil {
		return e
	}
	key := f.Filters[len(f.Filters)-1]
	seen, e = observer([]string{key})
	if e != nil {
		return e
	}
	v, _ := seen[key].(map[string]any)
	r.check("dynamic_session_filter_removed", v["present"] == false && v["error"] == "", v)
	r.observe("scope", map[string]any{"layers": []string{"ALE_AUTH_CONNECT_V4", "ALE_AUTH_CONNECT_V6"}, "transport": "TCP loopback controlled listeners", "BFE_reboot": "not performed", "remote_network": "not inferred from loopback"})
	return nil
}
func connected(data map[string]any, address string) bool {
	m, ok := data[address].(map[string]any)
	return ok && m["connected"] == true
}
