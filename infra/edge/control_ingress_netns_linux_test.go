//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

// Opt in only for an explicitly authorized root integration run. Normal unit
// tests neither sudo nor change the caller's network namespace.
func TestControlIngressNetworkNamespaces(t *testing.T) {
	if os.Getenv("AURIGA_CONTROL_NETNS_TEST") != "1" {
		t.Skip("explicit owned-netns integration run required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("explicit netns integration run requires root")
	}
	if os.Getenv("AURIGA_CONTROL_NETNS_CHILD") == "1" {
		if err := controlNetnsChildContext(); err != nil {
			t.Fatal(err)
		}
		testControlIngressNamespaces(t)
		return
	}
	for _, tool := range []string{"unshare", "nsenter", "ip", "sh", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("explicit integration tool missing: %s", tool)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hostNet, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	hostMount, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "-m", "-n", "--fork", "--mount-proc", "sh", "-c", controlNetnsScript, "control-netns", executable, hostNet, hostMount)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatalf("owned namespace integration failed: %v", err)
	}
}

const controlNetnsScript = `set -eu
binary=$1
host_net=$2
host_mount=$3
current_net=$(readlink /proc/self/ns/net)
current_mount=$(readlink /proc/self/ns/mnt)
printf 'host_netns=%s test_netns=%s host_mountns=%s test_mountns=%s\n' "$host_net" "$current_net" "$host_mount" "$current_mount"
[ "$host_net" != "$current_net" ] && [ "$host_mount" != "$current_mount" ] || exit 90
lan_child= private_child=
cleanup() {
 for pid in "$lan_child" "$private_child"; do [ -z "$pid" ] || kill "$pid" 2>/dev/null || true; done
 for pid in "$lan_child" "$private_child"; do [ -z "$pid" ] || wait "$pid" 2>/dev/null || true; done
 echo 'owned client children collected; private namespaces exit'
}
trap cleanup EXIT HUP INT TERM
unshare -n sleep 120 & lan_child=$!
unshare -n sleep 120 & private_child=$!
for pid in "$lan_child" "$private_child"; do
 i=0
 while [ "$(readlink /proc/$pid/ns/net)" = "$current_net" ]; do i=$((i+1)); [ "$i" -lt 50 ] || exit 91; sleep .02; done
 [ "$(readlink /proc/$pid/ns/net)" != "$host_net" ] || exit 92
done
ip link set lo up
ip link add lan0 type veth peer name lanpeer
ip link set lanpeer netns "$lan_child"
ip addr add 192.0.2.10/26 dev lan0
ip link set lan0 up
nsenter -t "$lan_child" -n ip addr add 192.0.2.20/26 dev lanpeer
nsenter -t "$lan_child" -n ip addr add 198.51.100.20/32 dev lanpeer
nsenter -t "$lan_child" -n ip link set lanpeer up
nsenter -t "$lan_child" -n ip link set lo up
nsenter -t "$lan_child" -n ip route add 192.0.2.128/26 via 192.0.2.10
nsenter -t "$lan_child" -n ip route add 203.0.113.0/26 via 192.0.2.10
ip route add 198.51.100.0/24 via 192.0.2.20 dev lan0
ip link add private0 type veth peer name privatepeer
ip link set privatepeer netns "$private_child"
ip addr add 192.0.2.138/26 dev private0
ip link set private0 up
nsenter -t "$private_child" -n ip addr add 192.0.2.148/26 dev privatepeer
nsenter -t "$private_child" -n ip link set privatepeer up
nsenter -t "$private_child" -n ip link set lo up
ip -br addr
ip route
AURIGA_CONTROL_ORIGINAL_NETNS="$host_net" AURIGA_CONTROL_ORIGINAL_MOUNTNS="$host_mount" AURIGA_CONTROL_NETNS_CHILD=1 REVIEW_LAN_PID=$lan_child REVIEW_PRIVATE_PID=$private_child "$binary" -test.run '^TestControlIngressNetworkNamespaces$' -test.v -test.timeout 60s
`

// The child marker alone never authorizes namespace mutations. Both namespace
// identities must differ from the recorded original, and the two clients must
// be sibling children of our setup shell in distinct, non-original namespaces.
func controlNetnsChildContext() error {
	originalNet, originalMount := os.Getenv("AURIGA_CONTROL_ORIGINAL_NETNS"), os.Getenv("AURIGA_CONTROL_ORIGINAL_MOUNTNS")
	currentNet, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	currentMount, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		return err
	}
	if originalNet == "" || originalMount == "" || originalNet == currentNet || originalMount == currentMount {
		return fmt.Errorf("refuse missing or original namespace context")
	}
	previous := ""
	for _, key := range []string{"REVIEW_LAN_PID", "REVIEW_PRIVATE_PID"} {
		text := os.Getenv(key)
		pid, err := strconv.Atoi(text)
		if err != nil || pid <= 1 || pid == os.Getpid() {
			return fmt.Errorf("invalid owned client PID")
		}
		namespace, err := os.Readlink("/proc/" + text + "/ns/net")
		if err != nil || namespace == originalNet || namespace == currentNet || namespace == previous {
			return fmt.Errorf("client must own a distinct private namespace")
		}
		status, err := os.ReadFile("/proc/" + text + "/status")
		if err != nil {
			return err
		}
		owned := false
		for _, line := range strings.Split(string(status), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "PPid:" && fields[1] == strconv.Itoa(os.Getppid()) {
				owned = true
			}
		}
		if !owned {
			return fmt.Errorf("client is not an owned setup-shell child")
		}
		previous = namespace
	}
	return nil
}

func TestControlIngressNetnsChildContext(t *testing.T) {
	t.Setenv("AURIGA_CONTROL_NETNS_CHILD", "1")
	t.Setenv("AURIGA_CONTROL_ORIGINAL_NETNS", "")
	t.Setenv("AURIGA_CONTROL_ORIGINAL_MOUNTNS", "")
	if controlNetnsChildContext() == nil {
		t.Fatal("child marker alone authorized namespace mutations")
	}
	netns, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	mountns, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURIGA_CONTROL_ORIGINAL_NETNS", netns)
	t.Setenv("AURIGA_CONTROL_ORIGINAL_MOUNTNS", mountns)
	if controlNetnsChildContext() == nil {
		t.Fatal("original namespace authorized mutations")
	}
}

func testControlIngressNamespaces(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ait_control_netns_test" {
			t.Error("invalid test AIT")
			w.WriteHeader(401)
			return
		}
		calls.Add(1)
		t.Logf("UPSTREAM %s %s calls=%d", r.Method, r.URL.Path, calls.Load())
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	cfg.Upstreams = map[string]string{"iam": upstream.URL, "org": upstream.URL, "workspace": upstream.URL}
	cfg.IAMHop.AllowLoopbackHTTP = true
	cfg.InstanceControlIngress = &InstanceControlIngressConfig{DenyLocalIPv4: []string{"192.0.2.10"}}
	raw, err := os.ReadFile("../../contracts/deployment/fixtures/ip-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	sockets := map[string]net.Listener{}
	ports := map[string]int{}
	for _, role := range []string{"portal", "admin", "instanceControl"} {
		ln, err := net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		sockets[role] = ln
		ports[role] = ln.Addr().(*net.TCPAddr).Port
		doc["listeners"].(map[string]any)[role] = map[string]any{"address": "0.0.0.0", "port": ports[role], "tls": false}
	}
	doc["portalOrigin"] = map[string]any{"scheme": "http", "host": "192.0.2.10", "port": ports["portal"]}
	doc["issuerOrigin"] = doc["portalOrigin"]
	doc["adminOrigin"] = map[string]any{"scheme": "http", "host": "192.0.2.10", "port": ports["admin"]}
	doc["instanceControl"].(map[string]any)["origin"].(map[string]any)["port"] = ports["instanceControl"]
	raw, _ = json.Marshal(doc)
	cfg.Access, err = deploymentconfig.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PortalHost = originAuthority(cfg.Access.PortalOrigin())
	cfg.AdminHost = originAuthority(cfg.Access.AdminOrigin())
	gateway := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	server := &Server{Cfg: cfg, Handler: gateway.Handler(), Listen: func(_, address string) (net.Listener, error) {
		for role, port := range ports {
			if address == net.JoinHostPort("0.0.0.0", strconv.Itoa(port)) {
				return &controlNetnsLogListener{Listener: sockets[role], t: t}, nil
			}
		}
		return nil, fmt.Errorf("unknown listener")
	}}
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server shutdown timeout")
		}
	}()
	lan, private := os.Getenv("REVIEW_LAN_PID"), os.Getenv("REVIEW_PRIVATE_PID")
	if lan == "" || private == "" {
		t.Fatal("owned client namespace PIDs required")
	}
	run := func(args ...string) {
		b, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned netns operation %v: %v %s", args, err, b)
		}
	}
	probe := func(pid, target, source, path, method string, wantHTTP bool, wantCalls int64) {
		py := `import http.client,sys,json
c=http.client.HTTPConnection(sys.argv[1],int(sys.argv[2]),timeout=3,source_address=(sys.argv[6],0) if sys.argv[6] else None)
try:
 c.request(sys.argv[5],sys.argv[3],headers={"Host":sys.argv[4],"Authorization":"Bearer ait_control_netns_test","X-Forwarded-For":"192.0.2.148"})
 r=c.getresponse(); print(json.dumps({"target":sys.argv[1],"source":sys.argv[6],"status":r.status})); r.read()
except Exception as e: print(json.dumps({"target":sys.argv[1],"source":sys.argv[6],"error":type(e).__name__}))
finally: c.close()
`
		b, err := exec.Command("nsenter", "-t", pid, "-n", "python3", "-c", py, target, strconv.Itoa(ports["instanceControl"]), path, originAuthority(cfg.Access.InstanceControl().Origin), method, source).CombinedOutput()
		if err != nil {
			t.Fatalf("client helper failed: %v %s", err, b)
		}
		var result struct {
			Status int    `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(b, &result); err != nil {
			t.Fatalf("client result: %s", b)
		}
		t.Logf("PROBE %s upstream=%d", b, calls.Load())
		if (wantHTTP && result.Status != 204) || (!wantHTTP && result.Error == "") || calls.Load() != wantCalls {
			t.Fatalf("incorrect route isolation: %s calls=%d want=%d", b, calls.Load(), wantCalls)
		}
	}
	self, heartbeat := "/api/workspace/v1/instance/self", "/api/workspace/v1/instance/heartbeat"
	probe(private, "192.0.2.138", "", self, "GET", true, 1)
	probe(lan, "192.0.2.10", "192.0.2.20", self, "GET", false, 1)
	probe(lan, "192.0.2.138", "192.0.2.20", self, "GET", false, 1)
	probe(lan, "192.0.2.138", "192.0.2.20", heartbeat, "POST", false, 1)
	probe(lan, "192.0.2.138", "198.51.100.20", self, "GET", false, 1)
	probe(lan, "192.0.2.138", "198.51.100.20", heartbeat, "POST", false, 1)
	// Add a real interface after Server.Run; the policy has no startup-only cache.
	run("ip", "link", "add", "dyn0", "type", "veth", "peer", "name", "dynpeer")
	defer run("ip", "link", "del", "dyn0")
	run("ip", "link", "set", "dynpeer", "netns", private)
	run("ip", "addr", "add", "203.0.113.10/26", "dev", "dyn0")
	run("ip", "link", "set", "dyn0", "up")
	run("nsenter", "-t", private, "-n", "ip", "addr", "add", "203.0.113.20/26", "dev", "dynpeer")
	run("nsenter", "-t", private, "-n", "ip", "link", "set", "dynpeer", "up")
	probe(private, "203.0.113.10", "203.0.113.20", self, "GET", true, 2)
	probe(lan, "203.0.113.10", "192.0.2.20", heartbeat, "POST", false, 2)
	probe(lan, "203.0.113.10", "198.51.100.20", self, "GET", false, 2)
	probe(private, "203.0.113.10", "192.0.2.148", self, "GET", false, 2)
	run("ip", "addr", "del", "203.0.113.10/26", "dev", "dyn0")
	snapshot, err := controlInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := cfg.parseControlIngress()
	if err != nil {
		t.Fatal(err)
	}
	if policy.allowsConnection(&net.TCPAddr{IP: net.ParseIP("203.0.113.10"), Port: ports["instanceControl"]}, &net.TCPAddr{IP: net.ParseIP("203.0.113.20"), Port: 40000}, snapshot) {
		t.Fatal("removed actual interface address still authorized")
	}
	t.Log("removed interface address rejected using fresh kernel snapshot")
	run("ip", "addr", "add", "192.0.2.150/26", "dev", "dyn0")
	probe(private, "192.0.2.138", "192.0.2.148", self, "GET", false, 2)
	run("ip", "addr", "del", "192.0.2.150/26", "dev", "dyn0")
	probe(private, "192.0.2.138", "192.0.2.148", self, "GET", true, 3)
	run("ip", "addr", "del", "192.0.2.10/26", "dev", "lan0")
	probe(private, "192.0.2.138", "192.0.2.148", self, "GET", false, 3)
	run("ip", "addr", "add", "192.0.2.10/26", "dev", "lan0")
	probe(private, "192.0.2.138", "192.0.2.148", self, "GET", true, 4)
	t.Log("PASS actual routed external sources rejected before HTTP; dynamic interface add/remove, overlap and missing LAN fail closed")
}

type controlNetnsLogListener struct {
	net.Listener
	t *testing.T
}

func (l *controlNetnsLogListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err == nil {
		l.t.Logf("KERNEL TCP local=%s remote=%s", connection.LocalAddr(), connection.RemoteAddr())
	}
	return connection, err
}
