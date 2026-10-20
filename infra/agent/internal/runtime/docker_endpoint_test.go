package runtime

import "testing"

func TestInstanceEndpointPrefersPublishedPortThenNetworkAddress(t *testing.T) {
	published := map[string]any{"NetworkSettings": map[string]any{
		"Ports": map[string]any{"7860/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "49153"}}},
	}}
	if got, err := instanceEndpoint(published, "auriga-net-x", 7860); err != nil || got != "http://127.0.0.1:49153" {
		t.Fatalf("published: %q %v", got, err)
	}
	// Internal-only network: Docker leaves the port unpublished.
	internal := map[string]any{"NetworkSettings": map[string]any{
		"Ports":    map[string]any{"7860/tcp": nil},
		"Networks": map[string]any{"auriga-net-x": map[string]any{"IPAddress": "192.0.2.7"}},
	}}
	if got, err := instanceEndpoint(internal, "auriga-net-x", 7860); err != nil || got != "http://192.0.2.7:7860" {
		t.Fatalf("internal: %q %v", got, err)
	}
	if _, err := instanceEndpoint(map[string]any{}, "auriga-net-x", 7860); err == nil {
		t.Fatal("expected error without port or address")
	}
}
