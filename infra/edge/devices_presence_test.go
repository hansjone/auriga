package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestDeviceHeartbeatFrames(t *testing.T) {
	now := time.Now().UTC()
	var got time.Time
	observer := deviceHeartbeatFrames{beat: func() { got = now }}
	// Client-masked NATS PING, fragmented across TCP writes.
	frame := []byte{0x82, 0x86, 1, 2, 3, 4, 'P' ^ 1, 'I' ^ 2, 'N' ^ 3, 'G' ^ 4, '\r' ^ 1, '\n' ^ 2}
	observer.feed(frame[:5])
	observer.feed(frame[5:])
	if !got.Equal(now) {
		t.Fatal("actual PING was not observed")
	}
	got = time.Time{}
	observer.feed([]byte{0x82, 3, 'P', 'U', 'B'})
	if !got.IsZero() {
		t.Fatal("application traffic became heartbeat")
	}
	masked := func(payload string) []byte {
		result := []byte{0x82, 0x80 | byte(len(payload)), 1, 2, 3, 4}
		for i, b := range []byte(payload) {
			result = append(result, b^byte(i%4+1))
		}
		return result
	}
	observer = deviceHeartbeatFrames{beat: func() { got = now }}
	observer.feed(masked("PUB subject 6\r\nPING\r\n\r\n"))
	if !got.IsZero() {
		t.Fatal("PING inside an application payload became a heartbeat")
	}
	observer.feed(masked("PING\r\n"))
	if !got.Equal(now) {
		t.Fatal("heartbeat after application payload was lost")
	}
}

func TestDevicePresenceOwnershipAndTruncation(t *testing.T) {
	b := deviceBinding{Device: "device", Server: "N" + strings.Repeat("A", 55), Client: "7", Account: "CLIENTS", Ingress: strings.Repeat("a", 26)}
	for _, tc := range []struct {
		name    string
		account string
		user    string
		total   int
	}{
		{"other-account", "OTHER", b.Ingress, 1}, {"other-ingress", b.Account, strings.Repeat("b", 26), 1}, {"truncated", b.Account, b.Ingress, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kicks := 0
			p := &devicePresence{request: func(_ context.Context, subject string, _ []byte) ([]byte, error) {
				if strings.HasSuffix(subject, ".KICK") {
					kicks++
				}
				return json.Marshal(map[string]any{"data": map[string]any{"server_id": b.Server, "total": tc.total, "connections": []any{map[string]any{"cid": 7, "account": tc.account, "authorized_user": tc.user}}}})
			}}
			if err := p.disconnect(context.Background(), []deviceBinding{b}); err == nil || kicks != 0 {
				t.Fatalf("err=%v kicks=%d", err, kicks)
			}
		})
	}
	p := &devicePresence{request: func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("offline") }}
	if _, err := p.snapshot(context.Background(), []deviceBinding{b}); err == nil {
		t.Fatal("outage became offline snapshot")
	}
}

func TestDevicePresenceRealWSSHeartbeatAndKick(t *testing.T) {
	ingress := strings.Repeat("a", 26)
	clients := server.NewAccount("CLIENTS")
	sys := server.NewAccount("SYS")
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, SystemAccount: "SYS", Accounts: []*server.Account{clients, sys}, Users: []*server.User{
		{Username: ingress, Password: "fixture", Account: clients},
		{Username: strings.Repeat("b", 26), Password: "fixture", Account: clients},
		{Username: "ops", Password: "fixture", Account: sys, Permissions: &server.Permissions{Publish: &server.SubjectPermission{Allow: []string{"$SYS.REQ.SERVER.*.CONNZ", "$SYS.REQ.SERVER.*.KICK"}}, Subscribe: &server.SubjectPermission{Allow: []string{"_INBOX.>"}}}},
	}, Websocket: server.WebsocketOpts{Host: "127.0.0.1", Port: -1, NoTLS: true}})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("broker startup")
	}
	wss, err := newNetworkWSS(NetworkWSSConfig{Enabled: true, UpstreamURL: srv.WebsocketURL(), AllowedOrigins: []string{"https://portal.example.com"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(wss)
	t.Cleanup(edge.Close)
	victim, err := nats.Connect("ws"+strings.TrimPrefix(edge.URL, "http"), nats.UserInfo(ingress, "fixture"), nats.NoReconnect(), nats.WebSocketConnectionHeaders(http.Header{"Origin": []string{"https://portal.example.com"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(victim.Close)
	bystander, err := nats.Connect(srv.ClientURL(), nats.UserInfo(strings.Repeat("b", 26), "fixture"), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bystander.Close)
	if err = victim.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	cid, err := victim.GetClientID()
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "password")
	if err = os.WriteFile(secret, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	presence, err := openDevicePresence(DevicePresenceConfig{URL: srv.ClientURL(), Username: "ops", PasswordFile: secret}, &wss.heartbeats)
	if err != nil {
		t.Fatal(err)
	}
	defer presence.close()
	binding := deviceBinding{Device: "device", Server: srv.ID(), Client: strconv.FormatUint(cid, 10), Account: "CLIENTS", Ingress: ingress}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	states, err := presence.snapshot(ctx, []deviceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	if states["device"].Online != "online" || states["device"].Heartbeat.IsZero() {
		t.Fatalf("state=%+v", states)
	}
	var bindingsReads atomic.Int64
	iam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == deviceBindingsPath {
			bindingsReads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []deviceBinding{binding}})
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Has("online") {
			t.Error("edge passed state filter to placeholder IAM state")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "device", "online": "unknown"}}})
	}))
	t.Cleanup(iam.Close)
	gateway := &Gateway{Cfg: Config{DevicePresence: DevicePresenceConfig{URL: srv.ClientURL(), Username: "ops", PasswordFile: secret}}, networkWSS: wss}
	transport := gateway.devicesTransport(iam.Client().Transport)
	request := httptest.NewRequest(http.MethodGet, iam.URL+deviceAdminPath+"?online=online", nil)
	request.Header.Set("Authorization", "Bearer fixture")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(page.Items) != 1 || page.Items[0]["online"] != "online" || page.Items[0]["lastHeartbeatAt"] == nil {
		t.Fatalf("page=%+v", page)
	}
	denied := request.Clone(ctx)
	denied.Header.Del("Authorization")
	before := bindingsReads.Load()
	response, err = transport.RoundTrip(denied)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 || bindingsReads.Load() != before {
		t.Fatal("denial queried device bindings")
	}
	private := request.Clone(ctx)
	private.URL.Path = deviceBindingsPath
	response, err = transport.RoundTrip(private)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 404 || bindingsReads.Load() != before {
		t.Fatal("private bindings exposed")
	}
	revoke := request.Clone(ctx)
	revoke.Method = http.MethodDelete
	revoke.URL.Path = deviceAdminPath + "/device"
	revoke.URL.RawQuery = ""
	response, err = transport.RoundTrip(revoke)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("revoke status=%d", response.StatusCode)
	}
	states, err = presence.snapshot(ctx, []deviceBinding{binding})
	if err != nil || states["device"].Online != "offline" {
		t.Fatalf("readback=%+v err=%v", states, err)
	}
	if states["device"].Heartbeat.IsZero() {
		t.Fatal("lost the actual last heartbeat on disconnect")
	}
	if err = bystander.FlushTimeout(time.Second); err != nil {
		t.Fatalf("bystander was disconnected: %v", err)
	}
}

func TestDeviceAdminOutageRemainsUnknownAndRevokeUnconfirmed(t *testing.T) {
	iam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == deviceBindingsPath {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []deviceBinding{}})
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "device", "online": "online", "lastHeartbeatAt": "stale"}}})
	}))
	t.Cleanup(iam.Close)
	transport := (&Gateway{}).devicesTransport(iam.Client().Transport)
	for _, tc := range []struct {
		filter string
		want   int
	}{{"unknown", 1}, {"online", 0}, {"offline", 0}} {
		request := httptest.NewRequest(http.MethodGet, iam.URL+deviceAdminPath+"?online="+tc.filter, nil)
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Items []map[string]any `json:"items"`
		}
		err = json.NewDecoder(response.Body).Decode(&body)
		_ = response.Body.Close()
		if err != nil || len(body.Items) != tc.want {
			t.Fatalf("filter %s: %+v %v", tc.filter, body, err)
		}
		if len(body.Items) > 0 && (body.Items[0]["online"] != "unknown" || body.Items[0]["lastHeartbeatAt"] != nil) {
			t.Fatalf("outage leaked stale state: %+v", body)
		}
	}
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodDelete, iam.URL+deviceAdminPath+"/device", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("unverified closure was reported successful")
	}
}
