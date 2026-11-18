package sharednode

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedArtifactTransportUsesRealMTLSAndFullLeaseBinding(t *testing.T) {
	stamp := time.Now().UTC().Truncate(time.Second)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || r.URL.Path != "/internal/shared/resource/artifact" || r.Method != "POST" || len(r.Header.Values("Authorization")) != 1 || r.Header.Get("Authorization") != "Bearer synthetic-service-token" || r.Header.Get("Auriga-Tenant-Id") != testTenant {
			t.Error("resource transport authority missing")
		}
		var request struct {
			Binding    map[string]any
			LeaseId    string
			ArtifactId *string
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.LeaseId != testTask || request.ArtifactId != nil || request.Binding["physicalInstanceId"] != testPool || request.Binding["mode"] != "shared" || request.Binding["epoch"] != float64(1) {
			t.Error("artifact binding altered")
		}
		w.Header().Set("Date", stamp.Format(http.TimeFormat))
		_, _ = w.Write([]byte("synthetic-manifest"))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "resource-ca.crt")
	token := filepath.Join(dir, "service.jwt")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0444); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(token, []byte("synthetic-service-token"), 0600); e != nil {
		t.Fatal(e)
	}
	endpoint := Endpoint{Address: server.URL, CAFile: ca}
	client, e := Open(Settings{TenantID: testTenant, Workspace: endpoint, IAM: endpoint, ServiceTokenFile: token, SharedResources: &endpoint}, server.TLS.Certificates[0])
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	binding := &nodev1.SharedDshBinding{TenantId: testTenant, PhysicalInstanceId: testPool, MemberId: testSession, PlacementId: testSession, Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1}
	raw, trusted, e := client.Artifact(context.Background(), binding, testTask, "", 256*1024)
	if e != nil || string(raw) != "synthetic-manifest" || !trusted.Equal(stamp) {
		t.Fatal("actual signed-reference download transport failed", e)
	}
	if _, _, e = client.Artifact(context.Background(), binding, testTask, "", 4); e == nil {
		t.Fatal("artifact byte limit ignored")
	}
}
