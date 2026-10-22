package main

import (
	"encoding/json"
	"fmt"

	"github.com/hansjone/auriga/infra/gen/deployment"
	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

// These ports belong to the disposable public M2 stack, not product defaults.
const (
	composeTenant  = "00000000-0000-0000-0000-000000000001"
	portalHost     = "portal.example.com"
	adminHost      = "admin.example.com"
	instanceParent = "i.example.com"
	controlAlias   = "auriga-instance.internal"
	portalPort     = 8080
	adminPort      = 8084
	observerPort   = 8082
	controlPort    = 8083
	iamPort        = 8081
	iamServerName  = "platform"
	edgeHopDNSName = "edge.auriga.internal"
)

func composeAccess(edgeContainer string) deployment.Access {
	portal := deployment.Origin{Scheme: "http", Host: portalHost, Port: portalPort}
	return deployment.Access{
		SchemaVersion: 1, Mode: "domain", TenantId: composeTenant,
		PortalOrigin: portal,
		AdminOrigin:  deployment.Origin{Scheme: "http", Host: adminHost, Port: adminPort},
		IssuerOrigin: portal, InsecureHttpAcknowledged: true,
		Listeners: deployment.AccessListeners{
			Portal:          deployment.Listener{Address: "0.0.0.0", Port: portalPort, Tls: false},
			Admin:           deployment.Listener{Address: "0.0.0.0", Port: adminPort, Tls: false},
			InstanceControl: deployment.Listener{Address: "0.0.0.0", Port: controlPort, Tls: false},
		},
		Ingress: deployment.AccessIngressDirectAsAccessIngress(&deployment.AccessIngressDirect{Kind: "direct"}),
		Instances: deployment.AccessInstancesSubdomainAsAccessInstances(&deployment.AccessInstancesSubdomain{
			Mode: "subdomain", ParentDomain: instanceParent, Scheme: "http", Port: portalPort,
		}),
		InstanceControl: deployment.AccessInstanceControl{
			DnsAlias:      controlAlias,
			Origin:        deployment.Origin{Scheme: "http", Host: controlAlias, Port: controlPort},
			EdgeContainer: edgeContainer,
		},
	}
}

// Validate the exact serialized generated model through the production loader.
func marshalAccess(access deployment.Access) ([]byte, deploymentconfig.Config, error) {
	raw, err := json.MarshalIndent(access, "", "  ")
	if err != nil {
		return nil, deploymentconfig.Config{}, err
	}
	cfg, err := deploymentconfig.Load(raw)
	if err != nil {
		return nil, deploymentconfig.Config{}, fmt.Errorf("compose access: %w", err)
	}
	return append(raw, '\n'), cfg, nil
}

func iamHealthcheck() string {
	// The same generated script runs in the platform container and the CI checkout.
	return fmt.Sprintf(`#!/bin/sh
set -eu
material_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec curl --fail --silent --show-error --noproxy '*' \
  --cacert "$material_dir/pki/ca.crt" \
  --cert "$material_dir/pki/edge-iam.crt" \
  --key "$material_dir/pki/edge-iam.key" \
  --resolve '%s:%d:127.0.0.1' 'https://%s:%d/actuator/health'
`, iamServerName, iamPort, iamServerName, iamPort)
}
