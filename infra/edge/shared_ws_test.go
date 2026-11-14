package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSharedWebSocketRevocation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b := newIPBrowser(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := sharedVerify(r.Header.Get("Auriga-Identity"), map[string]ed25519.PublicKey{"test": pub}, true, time.Now().Unix()); err != nil {
			t.Error(err)
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		rw.Flush()
		io.Copy(io.Discard, rw)
	}))
	now := time.Now()
	c := sharedTestClaims(false, now.Unix())
	c["binding"].(map[string]any)["tenantId"] = b.g.Cfg.Access.TenantID()
	raw, _ := json.Marshal(c)
	c, _ = sharedDecode(raw)
	a := &sharedTestAuthority{binding: c["binding"].(map[string]any), active: true}
	b.g.Shared = &SharedEntry{Authority: a, Store: &sharedTestStore{used: map[string]bool{}, cookies: map[string]SharedBrowserBinding{}}, TicketKeys: map[string]ed25519.PublicKey{"test": pub}, IdentityKey: priv, IdentityKid: "test"}
	resp, err := b.client.Post(b.root+instanceMount(sharedTestID)+"_auriga/placement-ticket", "application/json", strings.NewReader(`{"ticket":"`+sharedTestSign(c, priv, false)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	readIPResponse(t, resp, 302, "")
	u, _ := url.Parse(b.root + instanceMount(sharedTestID) + "api/ws")
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	req, _ := http.NewRequest("GET", u.String(), nil)
	for _, cookie := range b.client.Jar.Cookies(u) {
		req.AddCookie(cookie)
	}
	req.Header.Set("Origin", strings.TrimSuffix(b.root, "/"))
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")))
	if err = req.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err = http.ReadResponse(reader, req)
	if err != nil || resp.StatusCode != 101 {
		t.Fatal("upgrade", err, resp)
	}
	a.mu.Lock()
	a.active = false
	for _, cancel := range a.cancels {
		cancel()
	}
	a.mu.Unlock()
	if _, err = reader.ReadByte(); err == nil {
		t.Fatal("revoked websocket remained open")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("revocation timed out")
	}
	resp = b.get(t, instanceMount(sharedTestID)+"api/ws")
	readIPResponse(t, resp, 403, "shared.revoked")
}
