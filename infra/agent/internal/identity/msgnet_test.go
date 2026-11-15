package identity

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMsgnetConfirmationKeyStableAndIndependent(t *testing.T) {
	dir := t.TempDir()
	native, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	keys := make(chan ed25519.PrivateKey, 8)
	for range 8 {
		wg.Go(func() {
			key, e := MsgnetConfirmationKey(dir)
			if e != nil {
				t.Error(e)
				return
			}
			keys <- key
		})
	}
	wg.Wait()
	close(keys)
	var first ed25519.PrivateKey
	for key := range keys {
		if first == nil {
			first = key
		}
		if !bytes.Equal(first, key) {
			t.Fatal("concurrent initialization rotated key")
		}
	}
	if len(first) != ed25519.PrivateKeySize || native.Curve == nil {
		t.Fatal("independent key roles missing")
	}
	if _, err = os.Stat(filepath.Join(dir, KeyFileName)); !os.IsNotExist(err) {
		t.Fatal("confirmation initialization touched native key")
	}
	if err = os.WriteFile(filepath.Join(dir, MsgnetConfirmationKeyFile), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = MsgnetConfirmationKey(dir); err == nil {
		t.Fatal("damaged key silently regenerated")
	}
}
