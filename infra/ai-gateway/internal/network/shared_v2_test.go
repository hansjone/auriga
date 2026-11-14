package network

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

// W/P directory double for authoritative host-session/typed-local registration.
type sharedV2DirectoryDouble struct {
	from, to SharedV2Endpoint
	denied   bool
}

func (d *sharedV2DirectoryDouble) Cloud(context.Context, sharedauth.Binding) (SharedV2Endpoint, error) {
	return d.from, nil
}
func (d *sharedV2DirectoryDouble) Target(context.Context, SharedTarget) (SharedV2Endpoint, error) {
	return d.to, nil
}
func (d *sharedV2DirectoryDouble) Desktop(context.Context, NodeIdentity, string) (SharedV2Endpoint, error) {
	return d.from, nil
}
func (d *sharedV2DirectoryDouble) CheckAccess(context.Context, SharedV2Endpoint, SharedV2Endpoint) error {
	if d.denied {
		return sharedauth.ErrRevoked
	}
	return nil
}

func TestSharedV2UsesLiveConnectionAndOriginalProtocol(t *testing.T) {
	fake := &fakeMailboxRPC{response: []byte(`{ "status":"platform_stored", "opaque":"keep original bytes" }`)}
	conn, err := dialMailboxRPC(startMailboxRPC(t, fake))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client, err := NewMailboxClient(conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	binding := sharedauth.Binding{TenantID: testTenant, MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: uuid.NewString(), Mode: "shared", Epoch: 2, Revision: 3, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 4}
	cloudIngress, desktopIngress := ingressForTicket(uuid.NewString()), ingressForTicket(uuid.NewString())
	cloud := liveBinding{Binding: Binding{Ingress: cloudIngress, ServerID: "server", ClientID: 17, Account: clientAccount}, Tenant: testTenant, Realm: "2", Node: "64", Epoch: 9, NotAfter: time.Now().Add(time.Minute), Incarnation: "server-incarnation"}
	desktop := cloud
	desktop.Ingress = desktopIngress
	desktop.Node = "65"
	desktop.ClientID = 18
	directory := &sharedV2DirectoryDouble{
		from: SharedV2Endpoint{Binding: binding, TenantID: testTenant, Ingress: cloudIngress, Realm: "2", Node: "64", Local: "1", BootID: "boot", ConnectionEpoch: 9, SourceBinding: "registered-source", ProducerEpoch: "7"},
		to:   SharedV2Endpoint{TenantID: testTenant, Ingress: desktopIngress, Realm: "2", Node: "65", Local: "2", BootID: "desktop-boot", ConnectionEpoch: 9},
	}
	runtime := &Runtime{redeemer: &IAMRedeemer{bindings: map[string]liveBinding{cloudIngress: cloud, desktopIngress: desktop}}, mailbox: &mailboxRouter{client: client, sourceBoot: map[string]string{cloudIngress: "boot", desktopIngress: "desktop-boot"}}}
	adapter, err := runtime.SharedV2(directory)
	if err != nil {
		t.Fatal(err)
	}
	delivery := SharedDelivery{Sender: binding, Target: SharedTarget{TenantID: testTenant, DesktopNode: "65", DesktopLocal: "2"}, MessageID: uuid.NewString(), Body: []byte(`{"subtype":"text","text":"hello"}`)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := adapter.Submit(ctx, delivery)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != string(fake.response) {
		t.Fatal("result was rewritten")
	}
	fake.mu.Lock()
	count := len(fake.calls)
	raw := append([]byte(nil), fake.frames[0]...)
	identity := fake.identities[0]
	fake.mu.Unlock()
	if count != 1 || identity.Ingress != cloudIngress || identity.Node != "64" || identity.ClientId != "17" {
		t.Fatal("wrong authenticated SOURCE connection", identity)
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(raw, &request) != nil {
		t.Fatal("invalid submit")
	}
	var source map[string]string
	json.Unmarshal(request["source"], &source)
	var idempotency map[string]string
	json.Unmarshal(request["idempotency"], &idempotency)
	var target map[string]any
	json.Unmarshal(request["to"], &target)
	if source["source_binding"] != "registered-source" || idempotency["producer_epoch"] != "7" || idempotency["key"] != delivery.MessageID || target["node"] != "65" || target["local"] != "2" {
		t.Fatal("source/target/idempotency changed", string(raw))
	}
	directory.denied = true
	if _, err := adapter.Submit(ctx, delivery); err == nil {
		t.Fatal("grant bypass")
	}
	directory.denied = false
	directory.from.Binding.Epoch++
	if _, err := adapter.Submit(ctx, delivery); err == nil {
		t.Fatal("old binding accepted")
	}
	directory.from.Binding = binding
	runtime.redeemer.mu.Lock()
	changed := cloud
	changed.Epoch++
	runtime.redeemer.bindings[cloudIngress] = changed
	runtime.redeemer.mu.Unlock()
	if _, err := adapter.Submit(ctx, delivery); err == nil {
		t.Fatal("old connection accepted")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.calls) != 1 {
		t.Fatal("denied operation reached collab")
	}
}
