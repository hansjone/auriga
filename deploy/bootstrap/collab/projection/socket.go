package projection

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func readFrame(c net.Conn) ([]byte, error) {
	var h [4]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n < 1 || n > 16384 {
		return nil, Fault("invalid_chain")
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(c, b); e != nil {
		return nil, e
	}
	return b, nil
}
func writeFrame(c net.Conn, v any) error {
	b, e := Bytes(v)
	if e != nil {
		return e
	}
	if len(b) < 1 || len(b) > 16384 {
		return Fault("invalid_chain")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	for _, part := range [][]byte{h[:], b} {
		for len(part) > 0 {
			n, e := c.Write(part)
			if e != nil {
				return e
			}
			part = part[n:]
		}
	}
	return nil
}
func parseResponse(b []byte) (api.LocalProjectionSelector, string, string, error) {
	var m map[string]json.RawMessage
	var s api.LocalProjectionSelector
	if e := Decode(b, &m, true); e != nil {
		return s, "", "", e
	}
	if len(m) != 3 {
		return s, "", "", Fault("invalid_chain")
	}
	if e := Decode(m["selector"], &s, true); e != nil {
		return s, "", "", e
	}
	if e := CheckSelector(s); e != nil {
		return s, "", "", e
	}
	var phase, code string
	if json.Unmarshal(m["phase"], &phase) != nil || json.Unmarshal(m["failure_code"], &code) != nil {
		return s, "", "", Fault("invalid_chain")
	}
	if (phase == "loaded" || phase == "handshake_verified") && code == "none" {
		return s, phase, code, nil
	}
	if phase == "failed" {
		switch code {
		case "invalid_chain", "projection_pending", "same_version_conflict", "version_rollback":
			return s, phase, code, nil
		}
	}
	return s, "", "", Fault("invalid_chain")
}
func Exchange(ctx context.Context, control, component string, q api.LocalProjectionRequest) (api.LocalProjectionResponse, error) {
	var zero api.LocalProjectionResponse
	d := net.Dialer{Timeout: 5 * time.Second}
	c, e := d.DialContext(ctx, "unix", filepath.Join(control, "reload.sock"))
	if e != nil {
		return zero, e
	}
	defer func() { _ = c.Close() }()
	if e = checkPeer(c, readerUID(component)); e != nil {
		return zero, e
	}
	if e = c.SetDeadline(time.Now().Add(5 * time.Second)); e != nil {
		return zero, e
	}
	if e = writeFrame(c, q); e != nil {
		return zero, e
	}
	u, ok := c.(*net.UnixConn)
	if !ok {
		return zero, Fault("invalid_chain")
	}
	if e = u.CloseWrite(); e != nil {
		return zero, e
	}
	b, e := readFrame(c)
	if e != nil {
		return zero, e
	}
	var extra [1]byte
	if n, e := c.Read(extra[:]); n != 0 || e != io.EOF {
		return zero, Fault("invalid_chain")
	}
	s, p, f, e := parseResponse(b)
	if e != nil || !Equal(s, q.Selector) {
		return zero, Fault("invalid_chain")
	}
	return response(s, p, f), nil
}
func readerUID(component string) uint32 {
	if component == "edge" {
		return 65532
	}
	return 10001
}
func (r *Reader) Serve(ctx context.Context, component string) error {
	if component != "network" && component != "broker" && component != "edge" {
		return Fault("invalid_chain")
	}
	if e := checkListenerUID(readerUID(component)); e != nil {
		return e
	}
	path := filepath.Join(r.control, "reload.sock")
	release, e := lockListener(r.control)
	if e != nil {
		return e
	}
	defer release()
	if e = clearStaleSocket(path, readerUID(component)); e != nil {
		return e
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	defer func() { _ = l.Close() }()
	if e = os.Chmod(path, 0660); e != nil {
		return e
	}
	if e = setSocketGroup(path); e != nil {
		return e
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-done:
		}
	}()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		func() {
			defer func() { _ = c.Close() }()
			if checkPeer(c, 10001) != nil {
				return
			}
			if c.SetDeadline(time.Now().Add(5*time.Second)) != nil {
				return
			}
			b, e := readFrame(c)
			if e != nil {
				return
			}
			var q api.LocalProjectionRequest
			if Decode(b, &q, true) != nil {
				return
			}
			var extra [1]byte
			if n, e := c.Read(extra[:]); n != 0 || !errors.Is(e, io.EOF) {
				return
			}
			if q.Selector.Component != component {
				return
			}
			_ = writeFrame(c, r.Handle(q))
		}()
	}
}
