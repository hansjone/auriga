package main

import (
	"os"
	"strings"
	"testing"
)

func composeImageService(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), "\n  "+name+":\n")
	if !ok {
		t.Fatalf("missing %s service", name)
	}
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			return strings.Join(strings.Split(body, "\n")[:i], "\n")
		}
	}
	return body
}

func TestDeviceBrokerUsesUnpublishedInternalNetwork(t *testing.T) {
	broker := composeImageService(t, "network-broker")
	if strings.Contains(broker, "network_mode:") || strings.Contains(broker, "    ports:") || !strings.Contains(broker, "      network-control:") {
		t.Fatal("Broker must use its private network without host networking or published ports")
	}
	if !strings.Contains(composeImageService(t, "edge"), "      network-control: {}") {
		t.Fatal("edge cannot reach the private Broker listener")
	}
	platform := composeImageService(t, "platform")
	for _, setting := range []string{"AURIGA_OPS_ENABLED: 'true'", "AURIGA_OPS_DB_USER: auriga_ops_app", "AURIGA_OPS_NOTIFICATION_KEY: ${AURIGA_OPS_NOTIFICATION_KEY}"} {
		if !strings.Contains(platform, setting) {
			t.Fatalf("ops platform configuration lost while isolating Broker: %s", setting)
		}
	}
	raw, err := os.ReadFile("../docker-compose.yml")
	if err != nil || !strings.Contains(string(raw), "  network-control:\n    driver: bridge\n    internal: true\n") {
		t.Fatal("Broker network must prohibit external forwarding")
	}
}

func TestEgressConsumesSingleBuiltEdgeImage(t *testing.T) {
	edge, egress := composeImageService(t, "edge"), composeImageService(t, "egress")
	image := func(service string) string {
		for _, line := range strings.Split(service, "\n") {
			if strings.HasPrefix(line, "    image: ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "    image: "))
			}
		}
		return ""
	}
	if image(edge) == "" || image(edge) != image(egress) {
		t.Fatal("edge and egress must consume the same explicit image")
	}
	if !strings.Contains(image(edge), "${COMPOSE_PROJECT_NAME:-auriga-m2}") {
		t.Fatal("locally built image must remain isolated by the selected project")
	}
	if !strings.Contains(edge, "    build:\n") || !strings.Contains(edge, "dockerfile: deploy/compose/Dockerfile.edge") {
		t.Fatal("the shared image has no edge build source")
	}
	if strings.Contains(egress, "    build:") {
		t.Fatal("egress must not independently build a different image identity")
	}
	for _, service := range []string{edge, egress} {
		if !strings.Contains(service, "    pull_policy: never") {
			t.Fatal("a locally built service must not pull an unrelated registry tag")
		}
	}
}
