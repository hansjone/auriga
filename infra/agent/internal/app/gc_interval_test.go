package app

import (
	"testing"
	"time"
)

func TestArtifactGCInterval(t *testing.T) {
	t.Setenv("AURIGA_AGENT_ARTIFACT_GC_INTERVAL", "")
	if d, err := artifactGCInterval(); err != nil || d != defaultArtifactGCInterval {
		t.Fatalf("default: %v %v", d, err)
	}
	t.Setenv("AURIGA_AGENT_ARTIFACT_GC_INTERVAL", "30m")
	if d, err := artifactGCInterval(); err != nil || d != 30*time.Minute {
		t.Fatalf("30m: %v %v", d, err)
	}
	t.Setenv("AURIGA_AGENT_ARTIFACT_GC_INTERVAL", "0")
	if d, err := artifactGCInterval(); err != nil || d != 0 {
		t.Fatalf("0: %v %v", d, err)
	}
	for _, bad := range []string{"soon", "-1h"} {
		t.Setenv("AURIGA_AGENT_ARTIFACT_GC_INTERVAL", bad)
		if _, err := artifactGCInterval(); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
}
