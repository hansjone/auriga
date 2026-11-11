package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type listeningLog struct{ address chan string }

func (l listeningLog) Write(raw []byte) (int, error) {
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return 0, err
	}
	if record["msg"] == "egress listening" {
		l.address <- record["address"].(string)
	}
	return len(raw), nil
}

func TestLatePlatformDoesNotBlockHealthAndKeepsLastPolicy(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "policy.json")
	report := filepath.Join(dir, "report.json")
	t.Setenv("AURIGA_EGRESS_DENY_HOSTS", "portal.example.com,admin.example.com")
	t.Setenv("AURIGA_EGRESS_POLICY_FILE", source)
	t.Setenv("AURIGA_EGRESS_STATE_FILE", filepath.Join(dir, "state.json"))
	t.Setenv("AURIGA_EGRESS_REPORT_FILE", report)
	t.Setenv("AURIGA_EGRESS_NODE_ID", "egress-primary")
	t.Setenv("AURIGA_EGRESS_TENANT_ID", "")
	ctx, cancel := context.WithCancel(context.Background())
	address := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- runContext(ctx, []string{"--listen", "127.0.0.1:0"}, io.Discard, listeningLog{address})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("exit did not stop")
		}
	})
	var endpoint string
	select {
	case endpoint = <-address:
	case <-time.After(5 * time.Second):
		t.Fatal("listener waited for platform")
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(client.CloseIdleConnections)
	check := func() {
		t.Helper()
		response, err := client.Get("http://" + endpoint + "/health")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusNoContent {
			t.Fatal(response.StatusCode)
		}
	}
	denied := func() {
		t.Helper()
		conn, err := net.DialTimeout("tcp", endpoint, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = fmt.Fprint(conn, "CONNECT api.example.com:443 HTTP/1.1\r\nHost: api.example.com:443\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusForbidden {
			t.Fatal("cached allowlist no longer enforced", response.StatusCode)
		}
	}
	check()
	snapshot := []byte(`{"schemaVersion":1,"tenantId":"00000000-0000-0000-0000-000000000001","version":1,"value":{"mode":"allowlist","categories":[],"allowDomains":[],"denyDomains":[]}}`)
	if err := os.WriteFile(source, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		raw, err := os.ReadFile(report)
		if err == nil && bytes.Contains(raw, []byte(`"result":"applied"`)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late policy not applied")
		}
		time.Sleep(20 * time.Millisecond)
	}
	check()
	denied()
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	check()
	denied()
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"mode":"allowlist"`)) {
		t.Fatal("last policy was lost")
	}
}
