package modelgate

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"testing"
	"time"
)

func TestManagedIAMClockSkewBound(t *testing.T) {
	s, a, _ := controlFixture(t)
	s.raw = "amc." + tenant + "." + account + "." + account + "." + device + "." + strings.Repeat("a", 43)
	s.verify.ExpiresAt = timestamppb.New(s.now.Add(303 * time.Second))
	if _, err := a.Check(t.Context(), s.raw); err != nil {
		t.Fatal("IAM three seconds ahead must remain valid", err)
	}
	s.verify.ExpiresAt = timestamppb.New(s.now.Add(306 * time.Second))
	_, err := a.Check(t.Context(), s.raw)
	wantCode(t, err, "common.unauthenticated")
	s.verify.ExpiresAt = timestamppb.New(s.now)
	_, err = a.Check(t.Context(), s.raw)
	wantCode(t, err, "common.unauthenticated")
}

func TestMalformedManagedKeyNeverCallsIAM(t *testing.T) {
	good := []string{"amc", tenant, account, account, device, strings.Repeat("a", 43)}
	tests := map[string]string{"missing": "amc." + tenant, "extra": strings.Join(good, ".") + ".extra"}
	for name, index := range map[string]int{"platform-tenant": 1, "uppercase-member": 2, "uppercase-instance": 3, "uppercase-device": 4, "short-secret": 5, "long-secret": 5} {
		parts := append([]string(nil), good...)
		switch name {
		case "platform-tenant":
			parts[index] = platformTenant
		case "short-secret":
			parts[index] = strings.Repeat("a", 42)
		case "long-secret":
			parts[index] = strings.Repeat("a", 44)
		default:
			parts[index] = "01900000-0000-7000-8000-00000000000A"
		}
		tests[name] = strings.Join(parts, ".")
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			s, a, _ := controlFixture(t)
			_, err := a.Check(t.Context(), raw)
			wantCode(t, err, "common.unauthenticated")
			if len(s.calls) != 0 {
				t.Fatal("malformed key reached IAM")
			}
		})
	}
}
