package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestMsgnetIsolationPinnedServiceAndRealBrokerKick(t *testing.T) {
	pki := createAccessPKI(t)
	clientTLS, e := iamTLS(pki.hop, pki.hop.ServerName)
	if e != nil {
		t.Fatal(e)
	}
	clients, sys := server.NewAccount("CLIENTS"), server.NewAccount("SYS")
	ingress := strings.Repeat("a", 26)
	srv, e := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TLSConfig:     &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientCAs: pki.roots, ClientAuth: tls.RequireAndVerifyClientCert},
		SystemAccount: "SYS", Accounts: []*server.Account{clients, sys}, Users: []*server.User{
			{Username: ingress, Password: "fixture", Account: clients},
			{Username: "edge-presence", Password: "fixture", Account: sys, Permissions: &server.Permissions{Publish: &server.SubjectPermission{Allow: []string{"$SYS.REQ.SERVER.*.CONNZ", "$SYS.REQ.SERVER.*.KICK"}}, Subscribe: &server.SubjectPermission{Allow: []string{"_INBOX.>"}}}},
		}})
	if e != nil {
		t.Fatal(e)
	}
	go srv.Start()
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("broker startup")
	}
	brokerURL := strings.Replace(srv.ClientURL(), "nats://", "tls://", 1)
	nc, e := nats.Connect(brokerURL, nats.Secure(clientTLS), nats.UserInfo(ingress, "fixture"), nats.NoReconnect())
	if e != nil {
		t.Fatal(e)
	}
	defer nc.Close()
	cid, e := nc.GetClientID()
	if e != nil {
		t.Fatal(e)
	}
	secret := filepath.Join(t.TempDir(), "presence-password")
	if e = os.WriteFile(secret, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	g, _, _ := accessFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	g.Cfg.DevicePresence = DevicePresenceConfig{URL: brokerURL, Username: "edge-presence", PasswordFile: secret, CAFile: pki.hop.CAFile, CertFile: pki.hop.CertFile, KeyFile: pki.hop.KeyFile, ServerName: pki.hop.ServerName}
	device := "01900000-0000-7000-8000-00000000abce"
	var reads atomic.Int32
	var wrongOwner atomic.Bool
	g.DevicesRPC = deviceBindingsFunc(func(context.Context, string) ([]deviceBinding, error) {
		reads.Add(1)
		account := "CLIENTS"
		if wrongOwner.Load() {
			account = "OTHER"
		}
		return []deviceBinding{{Device: device, Server: srv.ID(), Client: strconv.FormatUint(cid, 10), Account: account, Ingress: ingress}}, nil
	})
	schemas, e := trust.LoadSchemas("../../contracts/network/auto-renewal-v2.schema.json")
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := x509.ParseCertificate(clientTLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	x := &msgnetIsolation{gateway: g, schemas: schemas, caller: base64.RawURLEncoding.EncodeToString(leaf.RawSubjectPublicKeyInfo), slots: make(chan struct{}, 4), targets: map[string]map[string]bool{"1/node-target": {device: true}}}
	https := httptest.NewUnstartedServer(x)
	https.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pki.roots}
	https.StartTLS()
	defer https.Close()
	request := func(config *tls.Config, target string) int {
		t.Helper()
		tr := &http.Transport{TLSClientConfig: config, Proxy: nil}
		defer tr.CloseIdleConnections()
		client := &http.Client{Transport: tr, Timeout: 7 * time.Second}
		raw, err := trust.Bytes(api.IsolationRecord{TenantId: g.Cfg.Access.TenantID(), Realm: "1", TargetId: target, MinimumControlRevision: "2", ControlDigest: "sha256:" + strings.Repeat("0", 64), State: "quarantined", Challenge: "challenge"})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Post(https.URL+"/api/collab/v2/network/isolation", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	wrongService := clientTLS.Clone()
	wrongService.Certificates = []tls.Certificate{pki.proxy}
	if request(wrongService, "node-target") != 403 || reads.Load() != 0 {
		t.Fatal("unregistered service reached broker bindings")
	}
	if request(clientTLS, "unmapped-target") != 403 || reads.Load() != 0 {
		t.Fatal("wire target established an execution mapping")
	}
	wrongOwner.Store(true)
	if request(clientTLS, "node-target") != 503 || !nc.IsConnected() {
		t.Fatal("wrong broker ownership was confirmed")
	}
	wrongOwner.Store(false)
	if request(clientTLS, "node-target") != 200 {
		t.Fatal("actual disconnection not confirmed")
	}
	deadline := time.Now().Add(time.Second)
	for nc.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if nc.IsConnected() {
		t.Fatal("success left message connection alive")
	}
	if request(clientTLS, "node-target") != 200 {
		t.Fatal("confirmed offline retry failed")
	}
}
