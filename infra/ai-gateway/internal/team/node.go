package team

import (
	"context"
	"errors"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/protobuf/proto"
	"math"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validate(value any, schema map[string]any) error {
	bad := errors.New("invalid arguments")
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return bad
		}
		props, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, found := object[key.(string)]; !found {
					return bad
				}
			}
		}
		for key, v := range object {
			property, ok := props[key].(map[string]any)
			if !ok {
				return bad
			}
			if err := validate(v, property); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok || !utf8.ValidString(text) {
			return bad
		}
		size := float64(utf8.RuneCountInString(text))
		if min, ok := schema["minLength"].(float64); ok && size < min {
			return bad
		}
		if max, ok := schema["maxLength"].(float64); ok && size > max {
			return bad
		}
		if schema["format"] == "uuid" && !uuidPattern.MatchString(text) {
			return bad
		}
		if schema["pattern"] == `\S` && strings.TrimSpace(text) == "" {
			return bad
		}
		if schema["maxLength"] == float64(16384) && len(text) > 16384 {
			return bad
		}
	case "integer":
		n, ok := value.(float64)
		if !ok || math.Trunc(n) != n {
			return bad
		}
		if min, ok := schema["minimum"].(float64); ok && n < min {
			return bad
		}
		if max, ok := schema["maximum"].(float64); ok && n > max {
			return bad
		}
	}
	return nil
}
func (s *Server) node(w http.ResponseWriter, r *http.Request, a *collab.Actor, body []byte) {
	var message proto.Message
	var err error
	switch strings.TrimPrefix(r.URL.Path, "/ai/team/node/") {
	case "register":
		var p struct {
			Label          string `json:"label"`
			ReceiveEnabled *bool  `json:"receiveEnabled"`
		}
		if strict(body, &p) != nil || p.ReceiveEnabled == nil {
			http.Error(w, "common.validation_failed", http.StatusBadRequest)
			return
		}
		message, err = s.client.RegisterEndpoint(r.Context(), &collab.RegisterEndpointRequest{Actor: a, Label: p.Label, ReceiveEnabled: *p.ReceiveEnabled})
	case "bind":
		var p struct {
			EndpointID    string `json:"endpointId"`
			HostSessionID string `json:"hostSessionId"`
			ThreadID      string `json:"threadId"`
		}
		if strict(body, &p) != nil {
			http.Error(w, "common.validation_failed", http.StatusBadRequest)
			return
		}
		a.EndpointId = p.EndpointID
		a.HostSessionId = p.HostSessionID
		message, err = s.client.BindSession(r.Context(), &collab.BindSessionRequest{Actor: a, ThreadId: p.ThreadID})
	case "pull":
		var p struct {
			EndpointID string `json:"endpointId"`
			Limit      uint32 `json:"limit"`
		}
		if strict(body, &p) != nil {
			http.Error(w, "common.validation_failed", http.StatusBadRequest)
			return
		}
		a.EndpointId = p.EndpointID
		message, err = s.client.PullDeliveries(r.Context(), &collab.PullDeliveriesRequest{Actor: a, Limit: p.Limit})
	case "ack":
		var p struct {
			EndpointID    string `json:"endpointId"`
			BindingID     string `json:"bindingId"`
			HostSessionID string `json:"hostSessionId"`
			DeliveryID    string `json:"deliveryId"`
			LeaseToken    string `json:"leaseToken"`
			HostRequestID string `json:"hostRequestId"`
		}
		if strict(body, &p) != nil {
			http.Error(w, "common.validation_failed", http.StatusBadRequest)
			return
		}
		a.EndpointId = p.EndpointID
		a.BindingId = p.BindingID
		a.HostSessionId = p.HostSessionID
		message, err = s.client.AckDelivery(r.Context(), &collab.AckDeliveryRequest{Actor: a, DeliveryId: p.DeliveryID, LeaseToken: p.LeaseToken, HostRequestId: p.HostRequestID})
	case "revoke":
		var p struct {
			EndpointID string `json:"endpointId"`
		}
		if strict(body, &p) != nil {
			http.Error(w, "common.validation_failed", http.StatusBadRequest)
			return
		}
		a.EndpointId = p.EndpointID
		message, err = s.client.RevokeEndpoint(r.Context(), &collab.RevokeEndpointRequest{Actor: a})
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, protoValue(message))
}

// A per-request deadline bounds all downstream control-plane and collab calls.
func Deadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
