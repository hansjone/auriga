package identifiers

import "testing"

func TestValidUUID(t *testing.T) {
	for _, value := range []string{
		"00000000-0000-0000-0000-000000000001",
		"019a0000-0000-7000-8000-000000000001",
		"11111111-2222-4333-b444-555555555555",
		"11111111-2222-3333-f444-555555555555",
	} {
		if !ValidUUID(value) {
			t.Errorf("canonical non-nil UUID rejected: %q", value)
		}
	}
	for _, value := range []string{
		"", "00000000-0000-0000-0000-000000000000", "not-a-uuid",
		"019A0000-0000-7000-8000-000000000001",
		"019a0000000070008000000000000001",
		"{019a0000-0000-7000-8000-000000000001}",
		"urn:uuid:019a0000-0000-7000-8000-000000000001",
		"019a0000-0000-7000-8000-000000000001 ",
		"019a0000-0000-7000-8000-000000000001\n",
		"019a0000-0000-7000-8000-000000000001/other",
	} {
		if ValidUUID(value) {
			t.Errorf("noncanonical/nil UUID accepted: %q", value)
		}
	}
}
