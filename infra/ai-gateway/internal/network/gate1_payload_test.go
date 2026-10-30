package network

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

const payloadViolationLine = "-ERR 'Maximum Payload Violation'\r\n"

// An EOF/reset alone does not prove a payload violation. Even when the error
// line arrives, require the same connection's closed server record.
func payloadClosureRejected(line string, err error, cid uint64, port int, closed *server.ConnInfo) bool {
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return false
	}
	// Winsock's WSAECONNRESET (10054) differs from Go's portable ECONNRESET.
	reset := errors.Is(err, syscall.ECONNRESET) || (runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(10054)))
	return (line == "" || line == payloadViolationLine) && (errors.Is(err, io.EOF) || reset) &&
		cid != 0 && port != 0 && closed != nil && closed.Cid == cid && closed.Port == port &&
		closed.Account == clientAccount && closed.Stop != nil && closed.Reason == server.MaxPayloadExceeded.String()
}

func TestGate1_PayloadClosureEvidence(t *testing.T) {
	now := time.Now()
	record := server.ConnInfo{Cid: 7, Port: 4242, Account: clientAccount, Stop: &now, Reason: server.MaxPayloadExceeded.String()}
	for _, tc := range []struct {
		name, line string
		err        error
		change     func(*server.ConnInfo)
		missing    bool
		want       bool
	}{
		{name: "error-line-eof", line: payloadViolationLine, err: io.EOF, want: true},
		{name: "reset", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, want: true},
		{name: "error-line-reset", line: payloadViolationLine, err: syscall.ECONNRESET, want: true},
		{name: "winsock-reset", err: &os.SyscallError{Syscall: "wsarecv", Err: syscall.Errno(10054)}, want: runtime.GOOS == "windows"},
		{name: "eof", err: io.EOF, want: true},
		{name: "normal-close", err: io.EOF, change: func(c *server.ConnInfo) { c.Reason = server.ClientClosed.String() }},
		{name: "parse-error", err: syscall.ECONNRESET, change: func(c *server.ConnInfo) { c.Reason = server.ParseError.String() }},
		{name: "missing-record", err: io.EOF, missing: true},
		{name: "wrong-cid", err: io.EOF, change: func(c *server.ConnInfo) { c.Cid++ }},
		{name: "wrong-port", err: io.EOF, change: func(c *server.ConnInfo) { c.Port++ }},
		{name: "wrong-account", err: io.EOF, change: func(c *server.ConnInfo) { c.Account = "SVC" }},
		{name: "missing-reason", err: io.EOF, change: func(c *server.ConnInfo) { c.Reason = "" }},
		{name: "not-stopped", err: io.EOF, change: func(c *server.ConnInfo) { c.Stop = nil }},
		{name: "timeout", err: os.ErrDeadlineExceeded},
		{name: "other-error", err: io.ErrUnexpectedEOF},
		{name: "partial-line", line: "-ERR 'Maximum Payload", err: io.EOF},
		{name: "abnormal-line", line: "-ERR 'Parser Error'\r\n", err: syscall.ECONNRESET},
		{name: "still-open", line: payloadViolationLine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := record
			if tc.change != nil {
				tc.change(&c)
			}
			closed := &c
			if tc.missing {
				closed = nil
			}
			if got := payloadClosureRejected(tc.line, tc.err, 7, 4242, closed); got != tc.want {
				t.Fatalf("closure accepted=%v want=%v", got, tc.want)
			}
		})
	}
}

type payloadPeer struct {
	conn net.Conn
	r    *bufio.Reader
	cid  uint64
	port int
	up   string
}

func (p *payloadPeer) write(t *testing.T, wire string) {
	t.Helper()
	if n, err := io.WriteString(p.conn, wire); err != nil || n != len(wire) {
		t.Fatalf("raw write bytes=%d/%d err=%v", n, len(wire), err)
	}
}

func newPayloadPeer(t *testing.T, h *harness) *payloadPeer {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(h.tcpURL(), "nats://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	p := &payloadPeer{conn: c, r: bufio.NewReader(c), port: c.LocalAddr().(*net.TCPAddr).Port}
	readInfo := func() server.Info {
		line, err := p.r.ReadString('\n')
		var info server.Info
		if err != nil || !strings.HasPrefix(line, "INFO ") || json.Unmarshal([]byte(strings.TrimPrefix(line, "INFO ")), &info) != nil || info.CID == 0 || info.ID != h.srv.ID() {
			t.Fatalf("invalid INFO: err=%v", err)
		}
		return info
	}
	p.cid = readInfo().CID
	p.write(t, "CONNECT {\"auth_token\":\"nt_raw\",\"verbose\":false,\"protocol\":1}\r\nPING\r\n")
	// CONNECT can send PONG before the account-specific INFO.
	for pong, accountInfo := false, false; !pong || !accountInfo; {
		first, err := p.r.Peek(1)
		if err != nil {
			t.Fatalf("CONNECT phase: %v", err)
		}
		if first[0] == 'I' {
			info := readInfo()
			if info.CID != p.cid || !info.ConnectInfo || info.MaxPayload != 4096 {
				t.Fatalf("account INFO cid=%d max_payload=%d connect_info=%v", info.CID, info.MaxPayload, info.ConnectInfo)
			}
			accountInfo = true
		} else {
			line, err := p.r.ReadString('\n')
			if err != nil || line != "PONG\r\n" || pong {
				t.Fatalf("CONNECT/PONG phase: line=%q err=%v", line, err)
			}
			pong = true
		}
	}
	z, err := h.srv.Connz(&server.ConnzOptions{CID: p.cid, State: server.ConnOpen, Username: true})
	if err != nil || len(z.Conns) != 1 {
		t.Fatalf("live Connz: %v", err)
	}
	ci := z.Conns[0]
	if ci.Cid != p.cid || ci.Port != p.port || ci.Account != clientAccount || !ValidIngressID(ci.AuthorizedUser) {
		t.Fatalf("live Connz cid=%d port=%d account=%s", ci.Cid, ci.Port, ci.Account)
	}
	p.up = UpSubject(ci.AuthorizedUser)
	return p
}

func payloadClosedInfo(t *testing.T, h *harness, cid uint64) *server.ConnInfo {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		z, err := h.srv.Connz(&server.ConnzOptions{CID: cid, State: server.ConnClosed, Username: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(z.Conns) == 1 {
			c := z.Conns[0]
			t.Logf("closed Connz cid=%d port=%d account=%s reason=%q stop=%v in_msgs=%d in_bytes=%d", c.Cid, c.Port, c.Account, c.Reason, c.Stop, c.InMsgs, c.InBytes)
			return c
		}
		select {
		case <-tick.C:
		case <-timer.C:
			t.Fatalf("no closed Connz record for cid=%d", cid)
		}
	}
}

func runPayloadGate(t *testing.T, h *harness, mode string) {
	t.Helper()
	p := newPayloadPeer(t, h)
	nc := h.internalConn(h.adptUser, h.adptPass)
	sub, err := nc.SubscribeSync(p.up)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err = nc.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "oversize":
		body := strings.Repeat("a", 4096)
		p.write(t, fmt.Sprintf("PUB %s 4096\r\n%s\r\nPING\r\n", p.up, body))
		if line, err := p.r.ReadString('\n'); err != nil || line != "PONG\r\n" {
			t.Fatalf("boundary PONG: line=%q err=%v", line, err)
		}
		if msg, err := sub.NextMsg(time.Second); err != nil || string(msg.Data) != body {
			t.Fatalf("4096 boundary delivery: %v", err)
		}
		p.write(t, fmt.Sprintf("PUB %s 4097\r\n%s\r\n", p.up, strings.Repeat("a", 4097)))
	case "normal-close":
		if err = p.conn.(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatal(err)
		}
	case "parse-error":
		p.write(t, "-ERR 'fixture protocol error'\r\n")
	default:
		p.write(t, "BOGUS\r\n")
	}
	line, readErr := p.r.ReadString('\n')
	if readErr == nil {
		tail, err := p.r.ReadString('\n')
		line, readErr = line+tail, err
	}
	t.Logf("socket cid=%d port=%d line=%q err=%v", p.cid, p.port, line, readErr)
	closed := payloadClosedInfo(t, h, p.cid)
	wantReason := map[string]server.ClosedState{"oversize": server.MaxPayloadExceeded, "normal-close": server.ClientClosed, "parse-error": server.ParseError, "protocol-violation": server.ProtocolViolation}[mode]
	if closed.Reason != wantReason.String() || payloadClosureRejected(line, readErr, p.cid, p.port, closed) != (mode == "oversize") {
		t.Fatalf("incorrect payload closure classification for %s", mode)
	}
	if mode == "oversize" && (closed.InMsgs != 1 || closed.InBytes != 4096) {
		t.Fatal("oversized body was processed")
	}
	if _, err = sub.NextMsg(100 * time.Millisecond); !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("unexpected delivery after %s: %v", mode, err)
	}
}
