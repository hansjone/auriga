package app

import "testing"

func TestInstanceSurfaceFromEnv(t *testing.T) {
	t.Setenv("AURIGA_AGENT_SERVER", "https://192.0.2.10:7443")
	t.Setenv("AURIGA_AGENT_INSTANCE_PLATFORM_URL", "")
	t.Setenv("AURIGA_AGENT_INSTANCE_PARENT_DOMAIN", "")
	got, err := instanceSurfaceFromEnv()
	if err != nil || got.PlatformURL != "" || got.ParentDomain != "" {
		t.Fatalf("unset must stay empty (never the gRPC server): %+v %v", got, err)
	}

	t.Setenv("AURIGA_AGENT_INSTANCE_PLATFORM_URL", "http://192.0.2.10:8080/")
	t.Setenv("AURIGA_AGENT_INSTANCE_PARENT_DOMAIN", "i.example.com")
	got, err = instanceSurfaceFromEnv()
	if err != nil || got.PlatformURL != "http://192.0.2.10:8080" || got.ParentDomain != "i.example.com" {
		t.Fatalf("valid surface: %+v %v", got, err)
	}

	for _, bad := range []string{
		"grpc://192.0.2.10:7443",
		"https://edge.example.com/api",
		"https://user@edge.example.com",
		"https://edge.example.com?x=1",
		"edge.example.com",
	} {
		t.Setenv("AURIGA_AGENT_INSTANCE_PLATFORM_URL", bad)
		if _, err := instanceSurfaceFromEnv(); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
	t.Setenv("AURIGA_AGENT_INSTANCE_PLATFORM_URL", "")
	t.Setenv("AURIGA_AGENT_INSTANCE_PARENT_DOMAIN", "not a domain")
	if _, err := instanceSurfaceFromEnv(); err == nil {
		t.Fatal("bad parent domain should fail")
	}
}
