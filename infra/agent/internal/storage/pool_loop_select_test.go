package storage

import (
	"encoding/json"
	"testing"
)

func TestSelectPoolLoopUsesBackingInodeNotNamespacePath(t *testing.T) {
	rows := func(text string) []loopAttachment {
		var v struct {
			Devices []loopAttachment `json:"loopdevices"`
		}
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatal(err)
		}
		return v.Devices
	}
	// A restarted Agent reads a detached backing path; identity is inode+device.
	got, err := selectPoolLoop(rows(`{"loopdevices":[{"name":"/dev/loop3","back-ino":1234,"back-maj:min":"253:3"}]}`), 1234, 253, 3)
	if err != nil || got != "/dev/loop3" {
		t.Fatalf("numeric inode: %q %v", got, err)
	}
	got, err = selectPoolLoop(rows(`{"loopdevices":[{"name":"/dev/loop3","back-ino":"1234","back-maj:min":"253:3"}]}`), 1234, 253, 3)
	if err != nil || got != "/dev/loop3" {
		t.Fatalf("string inode: %q %v", got, err)
	}
	if got, err = selectPoolLoop(nil, 1234, 253, 3); err != nil || got != "" {
		t.Fatal("no attachment must allocate")
	}
	for name, text := range map[string]string{
		"two loops":      `{"loopdevices":[{"name":"/dev/loop3","back-ino":1234,"back-maj:min":"253:3"},{"name":"/dev/loop4","back-ino":1234,"back-maj:min":"253:3"}]}`,
		"other inode":    `{"loopdevices":[{"name":"/dev/loop3","back-ino":99,"back-maj:min":"253:3"}]}`,
		"other device":   `{"loopdevices":[{"name":"/dev/loop3","back-ino":1234,"back-maj:min":"8:1"}]}`,
		"missing device": `{"loopdevices":[{"name":"/dev/loop3","back-ino":1234}]}`,
	} {
		if _, err := selectPoolLoop(rows(text), 1234, 253, 3); err == nil {
			t.Fatal(name)
		}
	}
}
