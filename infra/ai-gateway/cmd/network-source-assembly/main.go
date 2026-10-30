// Command network-source-assembly is a test-only T3b adapter. Java owns IAM,
// collab, Org, identity, signing and PG; this process owns Broker and gateway.
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/network"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type bridge struct {
	Tenant          string                        `json:"tenant"`
	GatewayToken    string                        `json:"gatewayToken"`
	CollabToken     string                        `json:"collabToken"`
	ServerAudience  string                        `json:"serverAudience"`
	ProtocolSigning network.ProtocolSigningConfig `json:"protocolSigning"`
}
type input struct {
	Config string `json:"config"`
	IAM    string `json:"iam"`
	Collab string `json:"collab"`
	PGPort uint16 `json:"pgPort"`
}
type command struct {
	ID            string                          `json:"id"`
	Op            string                          `json:"op"`
	Method        string                          `json:"method"`
	Mode          string                          `json:"mode"`
	Connection    *iam.NetworkAuthorityConnection `json:"connection"`
	Message       json.RawMessage                 `json:"message"`
	SourceContext json.RawMessage                 `json:"sourceContext"`
	Original      json.RawMessage                 `json:"original"`
	Binding       string                          `json:"binding"`
	TimeoutMillis int                             `json:"timeoutMillis"`
	Call          string                          `json:"call"`
	RequireLive   bool                            `json:"requireLive"`
}

var outputMu sync.Mutex

func emit(v any) error {
	outputMu.Lock()
	defer outputMu.Unlock()
	return json.NewEncoder(os.Stdout).Encode(v)
}

func main() {
	if err := run(); err != nil {
		// Never print upstream errors, credentials, config content or CONNECT bytes.
		_, _ = fmt.Fprintln(os.Stderr, "source assembly adapter failed:", err)
		os.Exit(1)
	}
}

func run() (result error) {
	var ready input
	data, err := os.ReadFile(os.Getenv("AURIGA_ASSEMBLY_READY"))
	if err != nil {
		return errors.New("ready file unavailable")
	}
	if json.Unmarshal(data, &ready) != nil {
		return errors.New("invalid ready metadata")
	}
	var b bridge
	data, err = os.ReadFile(ready.Config)
	if err != nil || json.Unmarshal(data, &b) != nil || b.Tenant == "" || b.ProtocolSigning.PrivateKeyFile == "" {
		return errors.New("bridge config unavailable")
	}
	if !loopback(ready.IAM) || !loopback(ready.Collab) || ready.PGPort == 0 {
		return errors.New("bridge endpoints must be numeric loopback")
	}
	binary := os.Getenv("AURIGA_NATS_SERVER")
	if err = verifyBroker(binary); err != nil {
		return err
	}
	dir := filepath.Dir(ready.Config) // The already-private, per-run T3a root.
	cert, key, ca, err := tlsFiles(dir)
	if err != nil {
		return errors.New("broker TLS preparation failed")
	}
	submit, err := submitClientFiles(dir)
	if err != nil {
		return errors.New("submit client TLS preparation failed")
	}
	issuer, err := nkeys.CreateAccount()
	if err != nil {
		return err
	}
	defer issuer.Wipe()
	xkey, err := nkeys.CreateCurveKeys()
	if err != nil {
		return err
	}
	defer xkey.Wipe()
	write := func(name string, data []byte) (string, error) {
		path := filepath.Join(dir, name)
		return path, os.WriteFile(path, data, 0600)
	}
	issuerSeed, err := issuer.Seed()
	if err != nil {
		return err
	}
	defer clear(issuerSeed)
	xSeed, err := xkey.Seed()
	if err != nil {
		return err
	}
	defer clear(xSeed)
	issuerFile, err := write("callout.seed", issuerSeed)
	if err != nil {
		return err
	}
	xFile, err := write("callout-x.seed", xSeed)
	if err != nil {
		return err
	}
	pub, err := issuer.PublicKey()
	if err != nil {
		return err
	}
	xp, err := xkey.PublicKey()
	if err != nil {
		return err
	}
	tcpPort, err := freePort()
	if err != nil {
		return err
	}
	wsPort, err := freePort()
	if err != nil {
		return err
	}
	httpsPort, err := freePort()
	if err != nil {
		return err
	}
	passwords := make([]string, 3)
	configs := make([]network.NATSConfig, 3)
	for i, user := range []string{"auth", "ops", "data"} {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		passwords[i] = hex.EncodeToString(raw)
		clear(raw)
		file, e := write(user+".password", []byte(passwords[i]))
		if e != nil {
			return e
		}
		configs[i] = network.NATSConfig{URL: fmt.Sprintf("nats://127.0.0.1:%d", tcpPort), Username: user, PasswordFile: file}
	}
	cfgPath, err := write("broker.conf", []byte(brokerConfig(tcpPort, wsPort, cert, key, pub, xp, passwords)))
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(dir, "broker.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := log.Close(); e != nil && result == nil {
			result = e
		}
	}()
	child := exec.Command(binary, "-c", cfgPath)
	child.Stdout, child.Stderr = log, log
	started := time.Now().UTC()
	if err = child.Start(); err != nil {
		return errors.New("broker start failed")
	}
	done := make(chan struct{})
	go func() { _ = child.Wait(); close(done) }()
	brokerIdentity, identityError := creationToken(child.Process.Pid)
	defer func() {
		select {
		case <-done:
		default:
			current, e := creationToken(child.Process.Pid)
			if identityError == nil && (e != nil || current != brokerIdentity) {
				result = errors.New("broker creation identity changed; refusing PID cleanup")
				return
			}
			_ = child.Process.Kill()
			<-done
		}
		for _, port := range []int{tcpPort, wsPort, httpsPort} {
			c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if e == nil {
				_ = c.Close()
				result = errors.New("own Broker port survived shutdown")
			}
		}
	}()
	if identityError != nil {
		return errors.New("cannot establish own Broker creation identity")
	}
	if err = waitBroker(configs[0], passwords[0], done); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay, err := startRelay(ctx, ready.IAM, ready.Collab)
	if err != nil {
		return err
	}
	defer relay.close()
	cfg := network.RuntimeConfig{
		SubmitHTTP:       submit.listenerConfig(fmt.Sprintf("127.0.0.1:%d", httpsPort), cert, key),
		BrokerConfigFile: cfgPath, ServerAudience: b.ServerAudience, ClientAccount: "CLIENTS",
		IssuerSeedFile: issuerFile, XKeySeedFile: xFile,
		IAM:    team.RPCConfig{Target: relay.iamAddress, ServiceTokenFile: b.GatewayToken},
		Collab: team.RPCConfig{Target: relay.collabAddress, ServiceTokenFile: b.GatewayToken},
		Auth:   configs[0], Ops: configs[1], Data: configs[2], ProtocolSigning: b.ProtocolSigning,
		RedeemTimeoutMillis: 1000, ActiveIntervalMillis: 250, RequestTimeoutMillis: 250, MaxKickAttempts: 3, MaxConnections: 100,
	}
	gateway, err := network.StartRuntime(ctx, cfg)
	if err != nil {
		return errors.New("production gateway startup failed")
	}
	defer gateway.Close()
	authority, err := team.DialRPC(team.RPCConfig{Target: ready.IAM, ServiceTokenFile: b.CollabToken})
	if err != nil {
		return err
	}
	defer func() { _ = authority.Close() }()
	sourceCalls := newSourceCalls(ctx, authority, b.Tenant)
	defer sourceCalls.close()
	if err = emit(map[string]any{"stage": "gateway-ready", "https": fmt.Sprintf("https://127.0.0.1:%d", httpsPort), "wss": fmt.Sprintf("wss://127.0.0.1:%d", wsPort), "ca": ca, "submitServerName": "localhost", "submitClientCertFile": submit.ClientCertFile, "submitClientKeyFile": submit.ClientKeyFile, "brokerVersion": "2.15.1", "brokerPid": child.Process.Pid, "brokerCreationToken": brokerIdentity, "brokerLaunchWallClock": started.Format(time.RFC3339Nano), "gatewayPid": os.Getpid()}); err != nil {
		return err
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 131072)
	for scan.Scan() {
		var cmd command
		if json.Unmarshal(scan.Bytes(), &cmd) != nil || cmd.ID == "" {
			return errors.New("invalid control command")
		}
		response := map[string]any{"stage": "reply", "id": cmd.ID}
		switch cmd.Op {
		case "source-start":
			if e := sourceCalls.start(cmd); e != nil {
				return e
			}
			response["call"] = cmd.ID
		case "source-cancel":
			response["cancelled"] = sourceCalls.cancel(cmd.Call)
		case "source-result":
			response["result"] = sourceCalls.result(cmd.Call)
		case "trace":
			response["records"] = relay.trace()
		case "snapshot":
			tables, e := snapshot(ctx, ready.PGPort, b.Tenant)
			if e != nil {
				return fmt.Errorf("runtime-role DB snapshot failed: %w", e)
			}
			response["tables"] = tables
		case "fault":
			if e := relay.fault(cmd.Method, cmd.Mode); e != nil {
				return e
			}
		case "delivery":
			rpcCtx, stop := context.WithTimeout(team.WithActor(ctx, relay.actor(b.Tenant)), 5*time.Second)
			reply, e := iam.NewNetworkAuthorityServiceClient(authority).AuthorizeNetworkDelivery(rpcCtx, &iam.AuthorizeNetworkDeliveryRequest{Connection: cmd.Connection, MessageJson: cmd.Message, SourceContextJson: cmd.SourceContext})
			stop()
			response["code"] = status.Code(e).String()
			response["allowed"] = reply.GetAllowed()
			if e != nil && status.Convert(e).Message() == "common.forbidden" {
				response["reason"] = "common.forbidden"
			}
			if e != nil && status.Code(e) != codes.PermissionDenied {
				response["unexpected"] = true
			}
		case "stop":
			response["stopping"] = true
			if err = emit(response); err != nil {
				return err
			}
			return nil
		default:
			return errors.New("unknown control command")
		}
		if err = emit(response); err != nil {
			return err
		}
	}
	return scan.Err()
}

func loopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func verifyBroker(path string) error {
	expected := map[string]string{
		"windows/amd64": "308b0ac72d7079a19f7221bf2326a3c8abfe861be0034fd73d9da444ca3fe055",
		"linux/amd64":   "baab2eecb5a8ebdabf6edd3e22756f89805e3184468653ba67185adf5a3b57eb",
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if expected == "" {
		return errors.New("assembly supports pinned Windows/Linux amd64 only")
	}
	f, err := os.Open(path)
	if err != nil {
		return errors.New("broker binary unavailable")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, 64*1024*1024+1))
	_ = f.Close()
	if e != nil || n > 64*1024*1024 || hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("broker must match locked 2.15.1 binary SHA256")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-v").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "nats-server: v2.15.1" {
		return errors.New("broker version mismatch")
	}
	return nil
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}

func waitBroker(cfg network.NATSConfig, password string, done <-chan struct{}) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		nc, err := nats.Connect(cfg.URL, nats.UserInfo(cfg.Username, password), nats.NoReconnect(), nats.Timeout(100*time.Millisecond))
		if err == nil {
			nc.Close()
			return nil
		}
		select {
		case <-done:
			return errors.New("broker exited before ready")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return errors.New("broker readiness timeout")
}

func brokerConfig(tcp, ws int, cert, key, issuer, xkey string, passwords []string) string {
	return fmt.Sprintf(`server_name: "auriga-source-assembly"
host: "127.0.0.1"
port: %d
system_account: SYS
websocket { host: "127.0.0.1", port: %d, tls { cert_file: %q, key_file: %q } }
accounts {
 AUTH { users: [{user: "auth", password: %q}] }
 CLIENTS {
  limits {max_payload: 65536, max_subscriptions: 8}
  exports: [{stream: "auriga.net.up.>", accounts: [SVC]}]
  imports: [{stream: {account: SVC, subject: "auriga.net.down.>"}}, {stream: {account: SVC, subject: "auriga.net.reply.>"}}]
 }
 SVC {
  users: [{user: "data", password: %q, permissions: {
   publish: {allow: ["auriga.net.down.*", "auriga.net.reply.>", "auriga.internal.>"]}
   subscribe: {allow: ["auriga.net.up.*.*", "auriga.internal.>", "_INBOX.>"]}
  }}]
  exports: [{stream: "auriga.net.down.>", accounts: [CLIENTS]}, {stream: "auriga.net.reply.>", accounts: [CLIENTS]}]
  imports: [{stream: {account: CLIENTS, subject: "auriga.net.up.>"}}]
 }
 SYS {users: [{user: "ops", password: %q, permissions: {publish: {allow: [%s]}, subscribe: {allow: ["_INBOX.>"]}}}]}
}
authorization { timeout: 2, auth_callout {issuer: %q, account: AUTH, auth_users: ["auth", "ops", "data"], xkey: %q} }
`, tcp, ws, cert, key, passwords[0], passwords[2], passwords[1], `"`+strings.Join(network.SysPublishAllow, `", "`)+`"`, issuer, xkey)
}

func tlsFiles(dir string) (certFile, keyFile, caFile string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return "", "", "", err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", "", err
	}
	defer clear(private)
	certFile, keyFile, caFile = filepath.Join(dir, "broker-cert.pem"), filepath.Join(dir, "broker-key.pem"), filepath.Join(dir, "broker-cert.pem")
	if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return "", "", "", err
	}
	err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), 0600)
	return certFile, keyFile, caFile, err
}
