package team

import (
	"context"
	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Opt-in integration with the current desktop's bundled client, never a downloaded substitute.
func TestInstalledDSHStreamableHTTPClient(t *testing.T) {
	executable := os.Getenv("AURIGA_TEST_DSH_EXE")
	module := os.Getenv("AURIGA_TEST_DSH_MCP_MODULE")
	if executable == "" || module == "" || runtime.GOOS != "windows" {
		t.Skip("requires explicit current Windows DSH executable and bundled MCP module")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	backend := &wireCollab{}
	rpc := grpc.NewServer()
	collab.RegisterCollabCommunicationServiceServer(rpc, backend)
	go func() { _ = rpc.Serve(listener) }()
	defer rpc.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	contract, err := os.ReadFile("../../../../contracts/plugin/team-mcp.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(collab.NewCollabCommunicationServiceClient(conn), func(context.Context, string) (*collab.Actor, error) {
		return &collab.Actor{TenantId: "tenant", AccountId: "account-from-iam", CredentialKind: "DEVICE", CredentialSubject: "device"}, nil
	}, contract)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Deadline(handler))
	defer server.Close()
	temp := t.TempDir()
	script := filepath.Join(temp, "client.cjs")
	out := filepath.Join(temp, "out.log")
	stderr := filepath.Join(temp, "err.log")
	program := `
(async()=>{
 const assert=require('node:assert/strict');const {Client,StreamableHTTPClientTransport}=require(process.env.AURIGA_TEST_DSH_MCP_MODULE);
 const client=new Client({name:'auriga-installed-dsh-test',version:'1'},{capabilities:{}});
 const transport=new StreamableHTTPClientTransport(new URL(process.env.AURIGA_TEST_MCP_URL),{requestInit:{headers:{Authorization:'Bearer test-device','Auriga-Endpoint-Id':'endpoint','Auriga-Binding-Id':'binding'}}});
 try{await client.connect(transport);const tools=await client.listTools();assert.equal(tools.tools.length,4);
 const result=await client.callTool({name:'auriga_open_thread',arguments:{targetEndpointId:'018f6f20-8f00-7000-8000-000000000001',text:'installed client hello',idempotencyKey:'installed-key'},_meta:{'auriga/sessionId':'real-session','auriga/toolCallId':'call','auriga/rootCallId':'root'}});
 assert.equal(result.structuredContent.message.text,'installed client hello');assert.equal(JSON.parse(result.content[0].text).message.text,'installed client hello');
 console.log(JSON.stringify({passed:3,node:process.versions.node,clientPackage:require(process.env.AURIGA_TEST_DSH_MCP_MODULE+'/package.json').version,tools:tools.tools.map(t=>t.name),remoteCall:true}));
 }finally{await client.close()}
})().catch(e=>{console.error(e.message);process.exitCode=1});`
	if err = os.WriteFile(script, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `$env:ELECTRON_RUN_AS_NODE='1'; $p=Start-Process -FilePath $env:AURIGA_TEST_DSH_EXE -ArgumentList '--expose-internals',$env:AURIGA_TEST_SCRIPT -WindowStyle Hidden -Wait -PassThru -RedirectStandardOutput $env:AURIGA_TEST_OUT -RedirectStandardError $env:AURIGA_TEST_ERR; exit $p.ExitCode`)
	command.Env = append(os.Environ(), "AURIGA_TEST_MCP_URL="+server.URL+"/ai/mcp", "AURIGA_TEST_SCRIPT="+script, "AURIGA_TEST_OUT="+out, "AURIGA_TEST_ERR="+stderr)
	commandOut, err := command.CombinedOutput()
	result, _ := os.ReadFile(out)
	failures, _ := os.ReadFile(stderr)
	if err != nil || !strings.Contains(string(result), `"passed":3`) {
		t.Fatalf("client failed %v; %s %s %s", err, commandOut, result, failures)
	}
	t.Log(string(result))
	if backend.calls.Load() != 1 {
		t.Fatal("actual collab call absent")
	}
}
