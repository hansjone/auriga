package main

import (
	"path/filepath"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

func writeBifrost(out string) {
	if err := materials.EnsureBifrostSecrets(filepath.Join(out, "bifrost")); err != nil {
		fatalf("%v", err)
	}
	rpc := map[string]string{"Target": "host.docker.internal:9091", "ServiceTokenFile": "/run/auriga-network-token/gateway/service.jwt", "CertFile": "/etc/auriga/pki/gateway.crt", "KeyFile": "/etc/auriga/pki/gateway.key", "CAFile": "/etc/auriga/pki/ca.crt", "ServerName": "platform"}
	writeJSON(filepath.Join(out, "bifrost-runtime.json"), map[string]any{"listen": "0.0.0.0:18445", "tlsCertFile": "/etc/auriga/pki/gateway.crt", "tlsKeyFile": "/etc/auriga/pki/gateway.key", "origin": "http://bifrost:8080", "adminPasswordFile": "/run/secrets/bifrost_admin_password", "memberPolicy": map[string]any{"monthlyBudgetMicros": 100000000, "requestsPerMinute": 60}, "iam": rpc, "org": rpc}, 0644)
}
