package sharednode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"time"
)

func (r *Relay) enterpriseOutbound(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		Capability              json.RawMessage
		URL, Method, BodyBase64 string
		Headers                 map[string]string
		TimeoutMs               int32
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Capability) == 0 || in.Capability[0] != '{' || in.TimeoutMs < 1 || in.TimeoutMs > 60000 || len(in.BodyBase64) > base64.StdEncoding.EncodedLen(1024*1024) {
		return nil, errors.New("common.validation_failed")
	}
	body, err := base64.StdEncoding.Strict().DecodeString(in.BodyBase64)
	if err != nil || len(body) > 1024*1024 || base64.StdEncoding.EncodeToString(body) != in.BodyBase64 {
		return nil, errors.New("common.validation_failed")
	}
	token, err := r.localCapability(in.Capability)
	if err != nil {
		return nil, err
	}
	cap, err := r.capability(ctx, token)
	if err != nil {
		return nil, err
	}
	if cap.Audience != "auriga.shared.enterprise" || !has(cap.Scopes, "outbound") {
		return nil, errors.New("common.forbidden")
	}
	out := new(iamv1.ExecuteSharedOutboundResponse)
	request := &iamv1.ExecuteSharedOutboundRequest{Capability: cap, Url: in.URL, Method: in.Method, Headers: in.Headers, Body: body, TimeoutMs: in.TimeoutMs}
	if err = r.Client.callTimeout(ctx, true, "ExecuteSharedOutbound", request, out, time.Duration(in.TimeoutMs)*time.Millisecond+5*time.Second); err != nil {
		return nil, err
	}
	if _, err = r.capability(ctx, token); err != nil {
		return nil, err
	}
	return wire(out)
}

// Host authorize/read/outbound supply full typed K claims; streaming calls use
// the signed token. Either carrier must name exactly one locally issued token.
func (r *Relay) localCapability(raw json.RawMessage) (string, error) {
	var token string
	if json.Unmarshal(raw, &token) == nil && token != "" {
		return token, nil
	}
	declared := new(nodev1.SharedDshCapability)
	if decode(raw, declared) != nil {
		return "", errors.New("shared.binding_mismatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for value, cap := range r.tokens {
		if proto.Equal(cap, declared) {
			return value, nil
		}
	}
	return "", errors.New("shared.binding_mismatch")
}
