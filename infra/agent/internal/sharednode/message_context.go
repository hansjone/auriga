package sharednode

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Cross-check the final N/collab association without rewriting its signed
// envelopes. Host independently validates those envelopes with installed roots
// and its durable inbox/trust ledgers; Node never initializes or resets them.
func checkMessageContext(raw []byte, binding *nodev1.SharedDshSessionBinding, task, boot, witness, native, revision string, requestUntil time.Time) (string, error) {
	var out struct {
		Binding                          json.RawMessage
		TaskId, ContextId, ProducerEpoch string
		Connection                       struct{ Transport, Realm, Node, Incarnation, Epoch, BootId, ChannelNonce, RequestId, ValidUntil string }
		Registration                     struct {
			Realm, Node, Local string
			ID                 string `json:"registration_id"`
			Operation          string `json:"allocation_op_id"`
			Revision           string `json:"registration_revision"`
			Type               string `json:"address_type"`
		}
		Mailbox struct {
			ReceiveEnabled                           *bool
			ReceiveVersion, HeadSeq, CommittedCursor string
		}
		MessageTrust struct{ Control, Publication, Announcement json.RawMessage }
		SourceGet    struct {
			Type    string `json:"type"`
			Request string `json:"request_id"`
			Witness string `json:"witness_id"`
			Source  struct {
				State  string `json:"state"`
				Native struct {
					ID       string `json:"binding_id"`
					Revision string `json:"binding_revision"`
				} `json:"native_binding"`
			} `json:"source"`
			Auth struct {
				Request string `json:"request_id"`
			} `json:"server_auth"`
		}
	}
	fail := errors.New("shared.binding_mismatch")
	if json.Unmarshal(raw, &out) != nil {
		return "", fail
	}
	b := new(nodev1.SharedDshSessionBinding)
	if protojson.Unmarshal(out.Binding, b) != nil {
		b.Reset()
		if decode(out.Binding, b) != nil {
			return "", fail
		}
	}
	c, r, m, s := out.Connection, out.Registration, out.Mailbox, out.SourceGet
	end, err := time.Parse(time.RFC3339Nano, c.ValidUntil)
	if !proto.Equal(b, binding) || out.TaskId != task || !uuid.MatchString(out.ContextId) || !canonicalUint(out.ProducerEpoch, true) ||
		c.Transport != "shared_node_http" || !canonicalUint(c.Realm, true) || !canonicalUint(c.Node, true) || !uuid.MatchString(c.Incarnation) || !canonicalUint(c.Epoch, true) ||
		c.BootId != boot || !uuid.MatchString(c.ChannelNonce) || c.RequestId != witness || err != nil || !end.After(time.Now()) || end.After(requestUntil) ||
		r.Realm != c.Realm || r.Node != c.Node || !canonicalUint(r.Local, true) || !uuid.MatchString(r.ID) || !uuid.MatchString(r.Operation) || !canonicalUint(r.Revision, true) || r.Type != "session" ||
		m.ReceiveEnabled == nil || !canonicalUint(m.ReceiveVersion, true) || !canonicalUint(m.HeadSeq, false) || !canonicalUint(m.CommittedCursor, false) ||
		s.Type != "source_get_result" || s.Request != witness || s.Witness != witness || s.Auth.Request != witness || s.Source.State != "active" || s.Source.Native.ID != native || s.Source.Native.Revision != revision {
		return "", fail
	}
	head, _ := strconv.ParseUint(m.HeadSeq, 10, 64)
	cursor, _ := strconv.ParseUint(m.CommittedCursor, 10, 64)
	if cursor > head {
		return "", fail
	}
	for _, document := range []json.RawMessage{out.MessageTrust.Control, out.MessageTrust.Publication, out.MessageTrust.Announcement} {
		var object map[string]json.RawMessage
		if json.Unmarshal(document, &object) != nil || object == nil {
			return "", fail
		}
	}
	return out.ContextId, nil
}
