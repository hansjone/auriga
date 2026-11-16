package main

import (
	"strings"
	"testing"
)

func TestSiteDesktopInstallFlags(t *testing.T) {
	base := []string{"--address", "192.0.2.10", "--data", "/srv/auriga-test"}
	if _, err := parseInstall(append(base, "--desktop-public-key", "public.pem")); err == nil {
		t.Fatal("unpaired desktop key accepted")
	}
	args := append(base, "--desktop-public-key", "public.pem", "--desktop-publisher-tenant", "11111111-1111-4111-8111-111111111111")
	if _, err := parseInstall(args); err == nil {
		t.Fatal("desktop key without fingerprint confirmation accepted")
	}
	if _, err := parseInstall(append(base, "--desktop-confirm-fingerprint", strings.Repeat("a", 64))); err == nil {
		t.Fatal("standalone desktop fingerprint accepted")
	}
	if _, err := parseInstall(append(args, "--desktop-confirm-fingerprint", "invalid")); err == nil {
		t.Fatal("malformed desktop fingerprint accepted")
	}
	args = append(args, "--desktop-confirm-fingerprint", strings.Repeat("a", 64))
	o, err := parseInstall(args)
	if err != nil || o.DesktopPublicKey != "public.pem" || o.DesktopPublisherTenant == "" || o.DesktopConfirmFingerprint != strings.Repeat("a", 64) {
		t.Fatalf("desktop install flags lost: %v", err)
	}
}
