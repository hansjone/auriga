//go:build linux

package projection

import (
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"net"
	"time"
)

func (r *Reader) serveConnection(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	raw, e := ReadFrame(conn)
	if e != nil {
		return
	}
	var request api.LocalProjectionRequest
	if e = strictModel(raw, &request); e != nil {
		return
	}
	selector, phase, code := r.Handle(request)
	var response api.LocalProjectionResponse
	if code == "none" {
		response = api.LocalProjectionResponseOneOfAsLocalProjectionResponse(&api.LocalProjectionResponseOneOf{Phase: phase, FailureCode: code, AdditionalProperties: map[string]interface{}{"selector": selector}})
	} else {
		response = api.LocalProjectionResponseOneOf1AsLocalProjectionResponse(&api.LocalProjectionResponseOneOf1{Phase: phase, FailureCode: code, AdditionalProperties: map[string]interface{}{"selector": selector}})
	}
	encoded, e := Bytes(response)
	if e != nil {
		return
	}
	_ = WriteFrame(conn, encoded)
}
