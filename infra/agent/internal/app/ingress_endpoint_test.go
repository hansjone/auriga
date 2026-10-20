package app

import (
	"runtime"
	"testing"
)

func TestIngressEndpointActualPortAndAdvertisedHost(t *testing.T) {
	got, err := ingressEndpoint("0.0.0.0:0", "", "node.example.com", "0.0.0.0:43561")
	if err != nil || got != "https://node.example.com:43561" {
		t.Fatalf("endpoint %q %v", got, err)
	}
	if _, err := ingressEndpoint("0.0.0.0:0", "", "", "0.0.0.0:43561"); err == nil {
		t.Fatal("wildcard endpoint advertised")
	}
	got, err = ingressEndpoint("[::]:0", "", "2001:db8::1", "[::]:43561")
	if err != nil || got != "https://[2001:db8::1]:43561" {
		t.Fatalf("IPv6 %q %v", got, err)
	}
}

func TestIngressEndpointLocal(t *testing.T) {
	path := "/var/lib/auriga/agent/ingress.sock"
	want := "unix:///var/lib/auriga/agent/ingress.sock"
	if runtime.GOOS == "windows" {
		path = `\\.\pipe\auriga-agent-ingress`
		want = "npipe:////./pipe/auriga-agent-ingress"
	}
	got, err := ingressEndpoint("", path, "", "")
	if err != nil || got != want {
		t.Fatalf("local %q %v", got, err)
	}
}
