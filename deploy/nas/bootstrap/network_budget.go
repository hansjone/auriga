package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Derive an unsigned runtime projection after the sealed sources were verified.
// Never write the source receipt, keys, identities or approval material.
func projectNetworkBudget(raw, broker []byte, ms int) ([]byte, []byte, error) {
	if ms < 10000 || ms > 120000 {
		return nil, nil, fmt.Errorf("network operation budget must be in [10000,120000] ms")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value == nil {
		return nil, nil, fmt.Errorf("invalid network source")
	}
	for _, key := range []string{"redeemTimeoutMillis", "requestTimeoutMillis", "activeIntervalMillis"} {
		if _, ok := value[key]; !ok {
			return nil, nil, fmt.Errorf("missing network timing source")
		}
	}
	value["operationTimeoutMillis"] = ms
	value["redeemTimeoutMillis"] = ms/3 - 2000
	value["requestTimeoutMillis"] = ms / 6
	value["activeIntervalMillis"] = 1000
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	authorization := regexp.MustCompile(`(?m)^authorization \{timeout: [0-9]+`)
	tls := regexp.MustCompile(`(?m)^(tls \{[^\r\n]*timeout: )[0-9]+`)
	if len(authorization.FindAll(broker, -1)) != 1 || len(tls.FindAll(broker, -1)) != 1 {
		return nil, nil, fmt.Errorf("unrecognized network broker timing source")
	}
	authSeconds := strconv.Itoa(ms / 3000)
	broker = authorization.ReplaceAll(broker, []byte("authorization {timeout: "+authSeconds))
	broker = tls.ReplaceAll(broker, []byte("${1}"+authSeconds))
	return append(out, '\n'), broker, nil
}

func readBudgetSource(path string) ([]byte, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, fmt.Errorf("invalid network budget source")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(st, actual) {
		return nil, fmt.Errorf("network source changed")
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

func writeBudgetProjection(path string, raw []byte, uid int) error {
	parent := filepath.Dir(path)
	real, e := filepath.EvalSymlinks(parent)
	if e != nil || real != parent {
		return fmt.Errorf("invalid network budget output parent")
	}
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("invalid network budget output")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(parent, ".network-budget-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Chown(uid, uid)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	d, e := os.Open(parent)
	if e != nil {
		return e
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func networkBudgetCommand(args []string) error {
	f := flag.NewFlagSet("network-budget", flag.ContinueOnError)
	runtime := f.String("runtime", "", "verified runtime projection source")
	broker := f.String("broker", "", "verified broker source")
	outRuntime := f.String("out-runtime", "", "new budget runtime projection")
	outBroker := f.String("out-broker", "", "new budget broker projection")
	ms := f.Int("operation-ms", 30000, "one WAN operation budget")
	if e := f.Parse(args); e != nil {
		return e
	}
	for _, path := range []*string{runtime, broker, outRuntime, outBroker} {
		absolute, e := filepath.Abs(*path)
		if e != nil {
			return e
		}
		*path = absolute
	}
	if *runtime == *outBroker || *broker == *outRuntime || *outRuntime == *outBroker {
		return fmt.Errorf("separate network budget projections required")
	}
	if f.NArg() != 0 || *runtime == *outRuntime || *broker == *outBroker || *outRuntime == "" || *outBroker == "" {
		return fmt.Errorf("separate network budget projections required")
	}
	r, e := readBudgetSource(*runtime)
	if e != nil {
		return e
	}
	b, e := readBudgetSource(*broker)
	if e != nil {
		return e
	}
	defer clear(b)
	r, b, e = projectNetworkBudget(r, b, *ms)
	if e != nil {
		return e
	}
	defer clear(b)
	if e = writeBudgetProjection(*outRuntime, r, 10001); e != nil {
		return e
	}
	return writeBudgetProjection(*outBroker, b, 10001)
}
