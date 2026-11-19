package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

type sharedReplaySourceReceipt struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	Public  string `json:"public"`
}

type sharedReplayBucketReceipt struct {
	Version int       `json:"version"`
	Source  string    `json:"source"`
	Created time.Time `json:"created"`
}

type sharedReplayActivation struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	Final   string `json:"final"`
	Open    bool   `json:"open"`
}

// All paths and the network peer are fixed. The network operations are run only
// by the dedicated short-lived helper on the fixed internal Docker network.
func nasSharedReplay(args []string, stdout io.Writer) error {
	f := flag.NewFlagSet("shared-replay-prepare", flag.ContinueOnError)
	base := f.String("base", "", "original sealed NAS materials")
	action := f.String("action", "check", "stage, prepare-path, final-path, provision, verify-final, close or check")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || os.Geteuid() != 0 || !sharedCanonicalPath(*base) {
		return fmt.Errorf("shared replay installation requires root and canonical fixed base")
	}
	switch *action {
	case "stage", "prepare-path", "final-path", "provision", "verify-final", "close", "check":
	default:
		return fmt.Errorf("unsupported shared replay installation operation")
	}
	var checked bytes.Buffer
	if err := nasSharedRuntime([]string{"-base", *base, "-out", filepath.Join(filepath.Dir(*base), "shared-source"), "-action", "path"}, &checked); err != nil {
		return err
	}
	sourcePath := checked.String()
	baseSHA, err := verifyNASCollabBase(*base)
	if err != nil {
		return err
	}
	_, source, err := readSharedInstallation(*base, sourcePath, baseSHA)
	if err != nil {
		return err
	}
	defer sharedClear(source)
	installation, err := runtimeReadLeaf(filepath.Join(sourcePath, "installation.receipt"), 0600, 65536)
	if err != nil {
		return err
	}
	sourceSHA := sharedSHA(installation)
	root := filepath.Join(filepath.Dir(*base), "shared-replay-source")
	if _, err := os.Lstat(root); os.IsNotExist(err) && *action == "stage" {
		if err := os.Mkdir(root, 0700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := materialsPrivateDirectory(root); err != nil {
		return err
	}
	unlock, err := sharedInstallationLock(root)
	if err != nil {
		return err
	}
	defer unlock()
	if *action == "stage" {
		if err := stageSharedReplaySource(root, sourceSHA, string(source["public/replay.nkey"])); err != nil {
			return err
		}
	}
	receipt, err := readSharedReplaySource(root, sourceSHA, string(source["public/replay.nkey"]))
	if err != nil {
		return err
	}
	original, err := sharedRoleReadLeaf(filepath.Join(filepath.Dir(*base), "nats/nats.conf"), 65532, 0600, 256<<10)
	if err != nil {
		return err
	}
	defer clear(original)
	final, err := sharedNATSProjection(original, string(source["public/replay.nkey"]))
	if err != nil {
		return err
	}
	defer clear(final)
	state := sharedReplayActivation{Version: 1, Source: sourceSHA, Final: sharedSHA(final)}
	if *action == "stage" || *action == "close" {
		if err := sharedReplayWriteState(root, state); err != nil {
			return err
		}
	}
	if *action == "stage" || *action == "final-path" {
		path, err := sharedReplayConfigProjection(filepath.Dir(*base), final, *action == "stage")
		if err != nil {
			return err
		}
		if *action == "final-path" {
			_, err = fmt.Fprint(stdout, path)
			return err
		}
	}
	if *action == "stage" || *action == "prepare-path" {
		prepared, err := sharedReplayPrepareNATS(original, string(source["public/replay.nkey"]), receipt.Public)
		if err != nil {
			return err
		}
		defer clear(prepared)
		path, err := sharedReplayConfigProjection(filepath.Dir(*base), prepared, *action == "stage")
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(stdout, path)
		return err
	}
	if *action == "close" {
		return nil
	}
	previous, err := readSharedReplayBucketReceipt(root, sourceSHA)
	if err != nil {
		return err
	}
	if *action == "check" {
		actual, err := runtimeReadLeaf(filepath.Join(root, "activation.receipt"), 0600, 65536)
		if err != nil {
			return fmt.Errorf("shared replay activation unavailable; run shared-replay-prepare")
		}
		state.Open = true
		expected, _ := json.Marshal(state)
		if !bytes.Equal(actual, expected) || previous.IsZero() {
			return fmt.Errorf("shared replay preparation or final revocation incomplete")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if *action == "provision" {
		connection, err := sharedReplayConnect(filepath.Join(root, "source/installer.seed"), true)
		if err != nil {
			return fmt.Errorf("temporary replay installer could not authenticate")
		}
		defer connection.Close()
		js, err := jetstream.New(connection)
		if err != nil {
			return err
		}
		created, err := prepareSharedReplayBucket(ctx, js, previous)
		if err != nil {
			return err
		}
		if previous.IsZero() {
			raw, _ := json.Marshal(sharedReplayBucketReceipt{Version: 1, Source: sourceSHA, Created: created})
			if err := materialsCreate(filepath.Join(root, "bucket.receipt"), raw); err != nil {
				return err
			}
			return runtimeSyncDir(root)
		}
		return nil
	}
	if previous.IsZero() {
		return fmt.Errorf("replay bucket installation receipt absent")
	}
	// Refuse network failures/timeouts as proof of revocation. The same server
	// must explicitly reject the installer, then authenticate the resident Edge.
	if connection, err := sharedReplayConnect(filepath.Join(root, "source/installer.seed"), true); connection != nil || !errors.Is(err, nats.ErrAuthorization) {
		if connection != nil {
			connection.Close()
		}
		return fmt.Errorf("temporary installer revocation has not been confirmed")
	}
	connection, err := sharedReplayConnect(filepath.Join(sourcePath, "edge/replay.seed"), false)
	if err != nil {
		return fmt.Errorf("resident replay identity could not authenticate after revocation")
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return err
	}
	kv, err := js.KeyValue(ctx, sharedReplayBucket)
	if err != nil || kv == nil {
		return fmt.Errorf("installed replay bucket unavailable after revocation")
	}
	info, err := sharedReplayStreamInfo(ctx, kv)
	if err != nil {
		return err
	}
	if !info.Created.Equal(previous) {
		return fmt.Errorf("replay ledger changed during installer revocation")
	}
	state.Open = true
	return sharedReplayWriteState(root, state)
}

func stageSharedReplaySource(root, sourceSHA, edgePublic string) error {
	if _, err := os.Lstat(filepath.Join(root, "source")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	stage := filepath.Join(root, ".pending")
	if err := os.Mkdir(stage, 0700); err != nil {
		return fmt.Errorf("incomplete replay source preserved; inspect before retry")
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		return err
	}
	defer user.Wipe()
	seed, err := user.Seed()
	if err != nil {
		return err
	}
	defer clear(seed)
	public, err := user.PublicKey()
	if err != nil || public == edgePublic {
		return fmt.Errorf("separate replay installer identity required")
	}
	raw, _ := json.Marshal(sharedReplaySourceReceipt{Version: 1, Source: sourceSHA, Public: public})
	for name, body := range map[string][]byte{"installer.seed": seed, "installer.nkey": []byte(public), "source.receipt": raw} {
		if err := materialsCreate(filepath.Join(stage, name), body); err != nil {
			return err
		}
	}
	if err := runtimeSyncTree(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, filepath.Join(root, "source")); err != nil {
		return err
	}
	return runtimeSyncDir(root)
}

func readSharedReplaySource(root, sourceSHA, edgePublic string) (*sharedReplaySourceReceipt, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case ".lock", "source", "bucket.receipt", "activation.receipt":
		default:
			return nil, fmt.Errorf("unknown or pending replay installation material")
		}
	}
	dir := filepath.Join(root, "source")
	if err := materialsPrivateDirectory(dir); err != nil {
		return nil, err
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		return nil, fmt.Errorf("unexpected replay installer source leaves")
	}
	for _, entry := range entries {
		if entry.Name() != "installer.seed" && entry.Name() != "installer.nkey" && entry.Name() != "source.receipt" {
			return nil, fmt.Errorf("unexpected replay installer source leaf")
		}
	}
	raw, err := runtimeReadLeaf(filepath.Join(dir, "source.receipt"), 0600, 65536)
	if err != nil {
		return nil, err
	}
	var receipt sharedReplaySourceReceipt
	if sharedDecodeJSON(raw, &receipt) != nil || receipt.Version != 1 || receipt.Source != sourceSHA || !nkeys.IsValidPublicUserKey(receipt.Public) || receipt.Public == edgePublic {
		return nil, fmt.Errorf("replay installer source binding differs")
	}
	public, err := runtimeReadLeaf(filepath.Join(dir, "installer.nkey"), 0600, 65536)
	if err != nil || string(public) != receipt.Public {
		return nil, fmt.Errorf("replay installer public key differs")
	}
	seed, err := runtimeReadLeaf(filepath.Join(dir, "installer.seed"), 0600, 65536)
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	user, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, fmt.Errorf("replay installer seed invalid")
	}
	defer user.Wipe()
	derived, err := user.PublicKey()
	if err != nil || derived != receipt.Public {
		return nil, fmt.Errorf("replay installer seed/public pair differs")
	}
	return &receipt, nil
}

func readSharedReplayBucketReceipt(root, sourceSHA string) (time.Time, error) {
	raw, err := runtimeReadLeaf(filepath.Join(root, "bucket.receipt"), 0600, 65536)
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var receipt sharedReplayBucketReceipt
	if sharedDecodeJSON(raw, &receipt) != nil || receipt.Version != 1 || receipt.Source != sourceSHA || receipt.Created.IsZero() {
		return time.Time{}, fmt.Errorf("replay bucket installation receipt differs")
	}
	return receipt.Created, nil
}

func sharedReplayWriteState(root string, state sharedReplayActivation) error {
	path := filepath.Join(root, "activation.receipt")
	if _, err := os.Lstat(path); err == nil {
		if _, err := runtimeReadLeaf(path, 0600, 65536); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	raw, _ := json.Marshal(state)
	tmp := filepath.Join(root, ".activation.pending")
	if err := materialsCreate(tmp, raw); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return runtimeSyncDir(root)
}

func sharedReplayConfigProjection(config string, body []byte, create bool) (string, error) {
	root := filepath.Join(config, "shared-replay-runtime")
	if _, err := os.Lstat(root); os.IsNotExist(err) && create {
		if err := os.Mkdir(root, 0755); err != nil {
			return "", err
		}
		if err := os.Chmod(root, 0755); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := sharedRoleDirectory(root, 0, 0755); err != nil {
		return "", err
	}
	dir := filepath.Join(root, "g-"+sharedSHA(body))
	if _, err := os.Lstat(dir); os.IsNotExist(err) && create {
		if err := os.Mkdir(dir, 0700); err != nil {
			return "", err
		}
		if err := os.Chown(dir, 65532, 65532); err != nil {
			return "", err
		}
		if err := sharedRoleCreate(filepath.Join(dir, "nats.conf"), sharedProjectedFile{Body: body, UID: 65532, Mode: 0600}); err != nil {
			return "", err
		}
		if err := runtimeSyncDir(dir); err != nil {
			return "", err
		}
		if err := runtimeSyncDir(root); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := sharedRoleDirectory(dir, 65532, 0700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "nats.conf" {
		return "", fmt.Errorf("unexpected replay prepare configuration")
	}
	actual, err := sharedRoleReadLeaf(filepath.Join(dir, "nats.conf"), 65532, 0600, 256<<10)
	if err != nil {
		return "", err
	}
	defer clear(actual)
	if !bytes.Equal(actual, body) {
		return "", fmt.Errorf("replay prepare configuration changed")
	}
	return dir, nil
}

func sharedReplayConnect(seed string, installer bool) (*nats.Conn, error) {
	// Source metadata was checked above; the callback is always the pinned
	// library reading that actual immutable private file, never an inline secret.
	option, err := nats.NkeyOptionFromSeed(seed)
	if err != nil {
		return nil, err
	}
	inbox := "_INBOX_auriga_shared_entry"
	if installer {
		inbox = "_INBOX_auriga_shared_install"
	}
	return nats.Connect("nats://nats:4222", option, nats.NoReconnect(), nats.Timeout(5*time.Second), nats.CustomInboxPrefix(inbox))
}
