package main

import "testing"

func TestSiteDesktopInstallFlags(t *testing.T) {
	base := []string{"--address", "192.0.2.10", "--data", "/srv/auriga-test"}
	if _, err := parseInstall(append(base, "--desktop-public-key", "public.pem")); err == nil {
		t.Fatal("unpaired desktop key accepted")
	}
	args := append(base, "--desktop-public-key", "public.pem", "--desktop-publisher-tenant", "11111111-1111-4111-8111-111111111111")
	o, err := parseInstall(args)
	if err != nil || o.DesktopPublicKey != "public.pem" || o.DesktopPublisherTenant == "" {
		t.Fatalf("desktop install flags lost: %v", err)
	}
}
