package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
)

// load must return N's root/scope/ledger-verified immutable snapshot, including
// the component's leaf and bundle CA set. Reading files without that verifier
// cannot authorize new trust. Production assembly must supply this port before
// enabling v2 reload; the ordinary static hop remains in use until then.
func configureMsgnetSubmitTLS(tr *http.Transport, load func() (*tls.Config, error)) error {
	if load == nil {
		return fmt.Errorf("verified msgnet TLS snapshot source required")
	}
	initial, e := load()
	if e != nil {
		return e
	}
	if !scopedMsgnetTLS(initial) {
		return fmt.Errorf("invalid verified msgnet TLS snapshot")
	}
	initial = initial.Clone()
	tr.TLSClientConfig = initial.Clone()
	tr.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		current, e := load()
		if e != nil || !scopedMsgnetTLS(current) || !currentTLSLeaf(current.Certificates[0]) {
			// A verification failure may be a newly installed root revocation.
			// Only N may return a still-authorized committed snapshot; this hop
			// must never choose an older trust set on the verifier's behalf.
			return nil, fmt.Errorf("submit TLS projection pending")
		}
		dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: tr.TLSHandshakeTimeout}, Config: current.Clone()}
		return dialer.DialContext(ctx, network, address)
	}
	return nil
}

func scopedMsgnetTLS(config *tls.Config) bool {
	return config != nil && !config.InsecureSkipVerify && config.RootCAs != nil && config.ServerName != "" && config.MinVersion >= tls.VersionTLS12 && len(config.Certificates) == 1
}
