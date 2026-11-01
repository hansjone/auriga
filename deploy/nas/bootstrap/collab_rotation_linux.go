package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Local installation records, never a network protocol or a supplied PG proof.
type nasRotationBinding struct {
	Base   string `json:"baseReceiptSha256"`
	Source string `json:"activationReceiptSha256"`
	Image  string `json:"platformImage"`
	Jar    string `json:"operatorJarSha256"`
	Data   string `json:"dataRoot"`
	Recipe string `json:"recipeRoot"`
}
type nasRotationInput struct {
	Base         string            `json:"baseReceiptSha256"`
	Source       string            `json:"activationReceiptSha256"`
	Image        string            `json:"platformImage"`
	Jar          string            `json:"operatorJarSha256"`
	Mode         string            `json:"mode"`
	Signed       string            `json:"signedFile"`
	Announcement string            `json:"announcementFile"`
	Private      map[string]string `json:"privateFiles"`
	Approval     string            `json:"-"`
}
type nasRotationKey struct {
	Kid     string `json:"kid"`
	Alg     string `json:"alg"`
	Purpose string `json:"purpose"`
	Public  string `json:"public_key_spki"`
	Before  string `json:"not_before"`
	After   string `json:"not_after"`
	State   string `json:"state"`
}
type nasRotationPayload struct {
	Schema  int              `json:"schema_version"`
	Realm   string           `json:"realm"`
	Version string           `json:"keyset_version"`
	Issued  string           `json:"issued_at"`
	Until   string           `json:"valid_until"`
	Keys    []nasRotationKey `json:"keys"`
}
type nasRotationRelease struct {
	Binding  nasRotationBinding `json:"binding"`
	Version  string             `json:"version"`
	Previous string             `json:"previousReceiptSha256"`
	Files    map[string]string  `json:"files"`
	Approval string             `json:"approval"`
	Mode     string             `json:"mode"`
}
type nasRotationInstall struct {
	Binding       nasRotationBinding `json:"binding"`
	Version       string             `json:"version"`
	Publication   string             `json:"publicationReceiptSha256"`
	State         string             `json:"stateSha256"`
	PreviousState string             `json:"previousStateSha256"`
	Files         map[string]string  `json:"files"`
}
type nasRotationCurrent struct {
	Version string `json:"version"`
	Receipt string `json:"receiptSha256"`
}
type nasRotationPlan struct {
	Config       string            `json:"configFile"`
	Root         string            `json:"rootFile"`
	Expected     string            `json:"expectedSignedFile"`
	ExpectedAnn  string            `json:"expectedAnnouncementFile"`
	Signed       string            `json:"signedFile"`
	Announcement string            `json:"announcementFile"`
	State        string            `json:"stateFile"`
	Private      map[string]string `json:"privateFiles"`
	History      string            `json:"expectedHistoryFile"`
	StateHash    string            `json:"expectedStateSha256"`
}
type nasRotationStatus struct {
	Version string            `json:"version"`
	Time    string            `json:"databaseTime"`
	Pending bool              `json:"pendingPush"`
	Digest  string            `json:"manifestDigest"`
	First   map[string]string `json:"firstPublished"`
}
type nasRotation struct {
	root, data, recipe, source, state, inputDir string
	binding                                     nasRotationBinding
	engine                                      *nasRotationEngine
	stopOnly                                    bool
	recovery                                    bool
	operator                                    func(string, string, nasRotationPlan) ([]byte, error)
}

var nasRotationDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var nasRotationKid = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func nasRotationHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func nasRotationVersion(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == s
}

func nasRotationDecode(raw []byte, value any) error {
	// encoding/json otherwise accepts duplicate keys and case-folded struct names.
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, e := d.Token()
		if e != nil {
			return e
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				s := fmt.Sprint(k)
				if seen[s] {
					return fmt.Errorf("duplicate rotation field")
				}
				seen[s] = true
				if e = walk(); e != nil {
					return e
				}
			}
			_, e = d.Token()
			return e
		case json.Delim('['):
			for d.More() {
				if e = walk(); e != nil {
					return e
				}
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := walk(); e != nil {
		return fmt.Errorf("rotation JSON refused")
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("rotation trailing JSON refused")
	}
	var exact func([]byte, reflect.Type) error
	exact = func(b []byte, t reflect.Type) error {
		if t.Kind() == reflect.Pointer {
			return exact(b, t.Elem())
		}
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct {
			var items []json.RawMessage
			if json.Unmarshal(b, &items) != nil {
				return fmt.Errorf("rotation array required")
			}
			for _, item := range items {
				if err := exact(item, t.Elem()); err != nil {
					return err
				}
			}
			return nil
		}
		if t.Kind() != reflect.Struct {
			return nil
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(b, &fields) != nil {
			return fmt.Errorf("rotation object required")
		}
		allowed := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			allowed[name] = f.Type
		}
		for k, v := range fields {
			kind, ok := allowed[k]
			if !ok {
				return fmt.Errorf("rotation unknown field")
			}
			if e := exact(v, kind); e != nil {
				return e
			}
		}
		return nil
	}
	if e := exact(raw, reflect.TypeOf(value)); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(value)
}
func nasRotationRead(path string, uid uint32) ([]byte, error) {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e != nil || s.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("rotation symlink or missing path")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	s, e := os.Lstat(path)
	if e != nil || !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || s.Size() > 1<<20 {
		return nil, fmt.Errorf("rotation file metadata refused")
	}
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || st.Uid != uid || st.Nlink != 1 {
		return nil, fmt.Errorf("rotation file owner refused")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(s, opened) {
		return nil, fmt.Errorf("rotation file changed")
	}
	b, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	after, se := os.Lstat(path)
	if e != nil || se != nil || !os.SameFile(s, after) || !s.ModTime().Equal(after.ModTime()) || int64(len(b)) != s.Size() {
		clear(b)
		return nil, fmt.Errorf("rotation file changed")
	}
	return b, nil
}
func nasRotationDirectory(path string, uid uint32) error {
	s, e := os.Lstat(path)
	var st unix.Stat_t
	if e != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 || s.Mode().Perm() != 0700 || unix.Lstat(path, &st) != nil || st.Uid != uid || st.Gid != uid {
		return fmt.Errorf("rotation private directory refused")
	}
	return nil
}
func nasRotationWrite(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func nasRotationWriteJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return nasRotationWrite(path, b)
}
func nasRotationSync(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func (r *nasRotation) releaseDir(v string) string { return filepath.Join(r.root, "releases", v) }

func (r *nasRotation) releases() ([]nasRotationRelease, error) {
	entries, e := os.ReadDir(filepath.Join(r.root, "releases"))
	if e != nil {
		return nil, e
	}
	sort.Slice(entries, func(i, j int) bool {
		a, _ := strconv.ParseInt(entries[i].Name(), 10, 64)
		b, _ := strconv.ParseInt(entries[j].Name(), 10, 64)
		return a < b
	})
	var result []nasRotationRelease
	previous := ""
	for _, entry := range entries {
		v := entry.Name()
		if !nasRotationVersion(v) {
			return nil, fmt.Errorf("rotation invalid archived version")
		}
		dir := r.releaseDir(v)
		if e = nasRotationDirectory(dir, 0); e != nil {
			return nil, e
		}
		raw, e := nasRotationRead(filepath.Join(dir, "publication.receipt"), 0)
		if e != nil {
			return nil, e
		}
		var row nasRotationRelease
		if nasRotationDecode(raw, &row) != nil || row.Binding != r.binding || row.Version != v || row.Previous != previous || len(row.Files) != 3 || (row.Mode != "regular" && row.Mode != "expired_recovery") {
			return nil, fmt.Errorf("rotation publication chain refused")
		}
		for _, name := range []string{"signed.json", "announcement.json", "pg-status.json"} {
			b, e := nasRotationRead(filepath.Join(dir, name), 0)
			if e != nil || !nasRotationDigest.MatchString(row.Files[name]) || nasRotationHash(b) != row.Files[name] {
				return nil, fmt.Errorf("rotation archive changed")
			}
		}
		leaves, e := os.ReadDir(dir)
		if e != nil || len(leaves) != 4 {
			return nil, fmt.Errorf("rotation archive contains unknown files")
		}
		previous = nasRotationHash(raw)
		result = append(result, row)
	}
	return result, nil
}
func (r *nasRotation) latest(rows []nasRotationRelease) (string, string, string) {
	dir := filepath.Join(r.source, "collab-installation")
	history := ""
	if len(rows) > 0 {
		dir = r.releaseDir(rows[len(rows)-1].Version)
		history = filepath.Join(dir, "pg-status.json")
	}
	return filepath.Join(dir, "signed.json"), filepath.Join(dir, "announcement.json"), history
}
func nasRotationParsePayload(raw []byte) (nasRotationPayload, error) {
	var signed struct {
		Keyset nasRotationPayload `json:"keyset"`
		Auth   json.RawMessage    `json:"root_auth"`
	}
	if e := nasRotationDecode(raw, &signed); e != nil {
		return signed.Keyset, e
	}
	p := signed.Keyset
	if !nasRotationVersion(p.Version) || len(p.Keys) < 3 || len(p.Keys) > 256 {
		return p, fmt.Errorf("rotation payload refused")
	}
	return p, nil
}

func (r *nasRotation) installed(rows []nasRotationRelease, latest bool) (string, string, error) {
	raw, e := nasRotationRead(filepath.Join(r.root, "current.json"), 0)
	if os.IsNotExist(e) {
		return "", "", e
	}
	if e != nil {
		return "", "", e
	}
	var current nasRotationCurrent
	if nasRotationDecode(raw, &current) != nil || !nasRotationVersion(current.Version) || !nasRotationDigest.MatchString(current.Receipt) {
		return "", "", fmt.Errorf("rotation selector refused")
	}
	var publication *nasRotationRelease
	for i := range rows {
		if rows[i].Version == current.Version {
			publication = &rows[i]
		}
	}
	if publication == nil || latest && (len(rows) == 0 || rows[len(rows)-1].Version != current.Version) {
		return "", "", fmt.Errorf("rotation publication awaiting installation")
	}
	dir := filepath.Join(r.root, "installs", current.Version)
	if e = nasRotationDirectory(dir, 0); e != nil {
		return "", "", e
	}
	raw, e = nasRotationRead(filepath.Join(dir, "install.receipt"), 0)
	var receipt nasRotationInstall
	if e != nil || nasRotationHash(raw) != current.Receipt || nasRotationDecode(raw, &receipt) != nil || receipt.Binding != r.binding || receipt.Version != current.Version {
		return "", "", fmt.Errorf("rotation installed receipt refused")
	}
	pub, e := nasRotationRead(filepath.Join(r.releaseDir(current.Version), "publication.receipt"), 0)
	if e != nil || nasRotationHash(pub) != receipt.Publication {
		return "", "", fmt.Errorf("rotation installed publication changed")
	}
	for _, sub := range []string{"config", "config/keys", "network"} {
		uid := uint32(10002)
		if sub == "network" {
			uid = 10001
		}
		if e = nasRotationDirectory(filepath.Join(dir, sub), uid); e != nil {
			return "", "", e
		}
	}
	seen := 0
	e = filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			if rel != "." && rel != "config" && rel != filepath.Join("config", "keys") && rel != "network" {
				return fmt.Errorf("rotation installed unknown directory")
			}
			return nil
		}
		if path == filepath.Join(dir, "install.receipt") {
			return nil
		}
		if path == filepath.Join(dir, "previous-state.json") {
			b, e := nasRotationRead(path, 0)
			if e != nil || !nasRotationDigest.MatchString(receipt.PreviousState) || nasRotationHash(b) != receipt.PreviousState {
				return fmt.Errorf("rotation previous state snapshot changed")
			}
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		uid := uint32(10002)
		if strings.HasPrefix(rel, "network/") {
			uid = 10001
		} else if !strings.HasPrefix(rel, "config/") {
			return fmt.Errorf("rotation installed unknown path")
		}
		b, e := nasRotationRead(path, uid)
		defer clear(b)
		if e != nil || !nasRotationDigest.MatchString(receipt.Files[rel]) || nasRotationHash(b) != receipt.Files[rel] {
			return fmt.Errorf("rotation installed files changed")
		}
		seen++
		return nil
	})
	if e != nil || seen != len(receipt.Files) {
		return "", "", fmt.Errorf("rotation installed file set changed")
	}
	state, e := nasRotationRead(r.state, 10002)
	if e != nil || nasRotationHash(state) != receipt.State {
		return "", "", fmt.Errorf("rotation original state changed")
	}
	return dir, receipt.State, nil
}
func (r *nasRotation) stateCheckpoint(rows []nasRotationRelease) (string, error) {
	if err := nasRotationDirectory(filepath.Dir(r.state), 10002); err != nil {
		return "", err
	}
	var st unix.Stat_t
	if unix.Lstat(r.state+".lock", &st) != nil || st.Uid != 10002 || st.Gid != 10002 || st.Nlink != 1 || st.Mode&07777 != 0600 || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return "", fmt.Errorf("rotation original state lock refused")
	}
	if _, e := os.Lstat(filepath.Join(r.root, "current.json")); e == nil {
		_, hash, e := r.installed(rows, false)
		return hash, e
	} else if !os.IsNotExist(e) {
		return "", e
	}
	initial, e := nasRotationRead(filepath.Join(r.source, "collab-installation/state.json"), 0)
	if e != nil {
		return "", e
	}
	state, e := nasRotationRead(r.state, 10002)
	if e != nil || !bytes.Equal(initial, state) {
		return "", fmt.Errorf("rotation initial state changed")
	}
	return nasRotationHash(state), nil
}
func (r *nasRotation) plan(rows []nasRotationRelease, signed, ann, state string, private map[string]string) (nasRotationPlan, error) {
	expected, expectedAnn, history := r.latest(rows)
	hash, e := r.stateCheckpoint(rows)
	if e != nil {
		return nasRotationPlan{}, e
	}
	return nasRotationPlan{Config: filepath.Join(r.source, "collab-installation/collab.properties"), Root: filepath.Join(r.source, "collab-installation/root.der"), Expected: expected, ExpectedAnn: expectedAnn, Signed: signed, Announcement: ann, State: state, Private: private, History: history, StateHash: hash}, nil
}
func (r *nasRotation) loadInput(inputName string) (nasRotationInput, []byte, []byte, nasRotationPayload, error) {
	var in nasRotationInput
	var p nasRotationPayload
	if nasRotationDirectory(r.inputDir, 0) != nil {
		return in, nil, nil, p, fmt.Errorf("rotation approval directory refused")
	}
	raw, e := nasRotationRead(filepath.Join(r.inputDir, inputName), 0)
	if e != nil {
		return in, nil, nil, p, e
	}
	if nasRotationDecode(raw, &in) != nil || in.Base != r.binding.Base || in.Source != r.binding.Source || in.Image != r.binding.Image || in.Jar != r.binding.Jar || (in.Mode != "regular" && in.Mode != "expired_recovery") {
		return in, nil, nil, p, fmt.Errorf("rotation input binding refused")
	}
	for _, name := range append([]string{in.Signed, in.Announcement}, mapValues(in.Private)...) {
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\r\n") || name == "input.json" {
			return in, nil, nil, p, fmt.Errorf("rotation material path refused")
		}
	}
	signed, e := nasRotationRead(filepath.Join(r.inputDir, in.Signed), 0)
	if e != nil {
		return in, nil, nil, p, e
	}
	ann, e := nasRotationRead(filepath.Join(r.inputDir, in.Announcement), 0)
	if e != nil {
		return in, nil, nil, p, e
	}
	p, e = nasRotationParsePayload(signed)
	if e != nil {
		return in, nil, nil, p, e
	}
	var approval struct {
		ApprovedBy   string `json:"approvedBy"`
		Reason       string `json:"reason"`
		Signed       string `json:"signedSha256"`
		Announcement string `json:"announcementSha256"`
		Payload      string `json:"payloadSha256"`
		Refresh      *bool  `json:"requireClientRefresh"`
	}
	audit, e := nasRotationRead(filepath.Join(r.inputDir, "approval.json"), 0)
	if e != nil || nasRotationDecode(audit, &approval) != nil || strings.TrimSpace(approval.ApprovedBy) == "" || strings.TrimSpace(approval.Reason) == "" || len(approval.ApprovedBy)+len(approval.Reason) > 4096 || approval.Signed != nasRotationHash(signed) || approval.Announcement != nasRotationHash(ann) {
		return in, nil, nil, p, fmt.Errorf("rotation explicit approval audit required")
	}
	payload, e := nasRotationRead(filepath.Join(r.inputDir, "payload.json"), 0)
	var request nasRotationPayload
	if e != nil || approval.Payload != nasRotationHash(payload) || nasRotationDecode(payload, &request) != nil || !reflect.DeepEqual(request, p) {
		return in, nil, nil, p, fmt.Errorf("rotation approval payload binding changed")
	}
	in.Approval = approval.ApprovedBy + ": " + approval.Reason + "; audit sha256:" + nasRotationHash(audit)
	if in.Mode == "expired_recovery" && (approval.Refresh == nil || !*approval.Refresh) {
		return in, nil, nil, p, fmt.Errorf("expired recovery requires explicit client refresh acknowledgement")
	}
	return in, signed, ann, p, e
}
func mapValues(m map[string]string) []string {
	v := make([]string, 0, len(m))
	for _, s := range m {
		v = append(v, s)
	}
	return v
}

func (r *nasRotation) mutate(action string, body func(string) error) (result error) {
	work := filepath.Join(r.root, ".pending")
	if e := os.Mkdir(work, 0700); e != nil {
		return fmt.Errorf("rotation pending exists; preserve evidence")
	}
	original, e := os.Lstat(work)
	if e != nil {
		return e
	}
	defer func() {
		current, e := os.Lstat(work)
		if e != nil || !os.SameFile(original, current) {
			if result == nil {
				result = fmt.Errorf("rotation pending instance changed")
			}
			return
		}
		if result != nil {
			_ = nasRotationWriteJSON(filepath.Join(work, "first-failure.json"), map[string]string{"action": action, "status": "incomplete"})
			return
		}
		result = os.RemoveAll(work)
		if result == nil {
			result = nasRotationSync(r.root)
		}
	}()
	return body(work)
}
func (r *nasRotation) publish() error {
	in, signed, ann, p, e := r.loadInput("input.json")
	if e != nil {
		return e
	}
	defer clear(signed)
	if (in.Mode == "expired_recovery") != r.recovery || r.recovery && r.binding.Jar == "" {
		return fmt.Errorf("explicit expired-recovery action and approved operator jar required")
	}
	if len(in.Private) != 0 {
		return fmt.Errorf("publish accepts public pair only; install uses a separate input")
	}
	rows, e := r.releases()
	if e != nil {
		return e
	}
	if len(rows) > 0 && rows[len(rows)-1].Version == p.Version {
		last := rows[len(rows)-1]
		if last.Files["signed.json"] != nasRotationHash(signed) || last.Files["announcement.json"] != nasRotationHash(ann) || last.Approval != in.Approval || last.Mode != in.Mode {
			return fmt.Errorf("same rotation version changed")
		}
		_, e = r.status(rows)
		return e
	}
	old, _, _ := r.latest(rows)
	b, e := nasRotationRead(old, 0)
	if e != nil {
		return e
	}
	previous, e := nasRotationParsePayload(b)
	if e != nil {
		return e
	}
	a, _ := strconv.ParseInt(previous.Version, 10, 64)
	n, _ := strconv.ParseInt(p.Version, 10, 64)
	if n <= a {
		return fmt.Errorf("rotation version rollback refused")
	}
	action := "publish"
	if r.recovery {
		action = "recover-expired"
	}
	return r.mutate(action, func(work string) error {
		stage := filepath.Join(work, "release")
		if e := os.Mkdir(stage, 0700); e != nil {
			return e
		}
		for name, b := range map[string][]byte{"signed.json": signed, "announcement.json": ann} {
			if e := nasRotationWrite(filepath.Join(stage, name), b); e != nil {
				return e
			}
		}
		plan, e := r.plan(rows, filepath.Join(stage, "signed.json"), filepath.Join(stage, "announcement.json"), r.state, map[string]string{})
		if e != nil {
			return e
		}
		if _, e = r.operator("verify", work, plan); e != nil {
			return e
		}
		preflight, publish := "preflight", "publish"
		if r.recovery {
			preflight, publish = "preflight-recovery", "recover-expired"
		}
		if _, e = r.operator(preflight, work, plan); e != nil {
			return e
		}
		if e = r.engine.stop(r); e != nil {
			return e
		}
		status, e := r.operator(publish, work, plan)
		if e != nil {
			return e
		}
		var proof nasRotationStatus
		if nasRotationDecode(status, &proof) != nil || proof.Version != p.Version || len(proof.First) == 0 {
			return fmt.Errorf("rotation PG acknowledgement refused")
		}
		if e = nasRotationWrite(filepath.Join(stage, "pg-status.json"), status); e != nil {
			return e
		}
		prev := ""
		if len(rows) > 0 {
			b, e := nasRotationRead(filepath.Join(r.releaseDir(rows[len(rows)-1].Version), "publication.receipt"), 0)
			if e != nil {
				return e
			}
			prev = nasRotationHash(b)
		}
		receipt := nasRotationRelease{Binding: r.binding, Version: p.Version, Previous: prev, Approval: in.Approval, Mode: in.Mode, Files: map[string]string{"signed.json": nasRotationHash(signed), "announcement.json": nasRotationHash(ann), "pg-status.json": nasRotationHash(status)}}
		if e = nasRotationWriteJSON(filepath.Join(stage, "publication.receipt"), receipt); e != nil {
			return e
		}
		if e = nasRotationSync(stage); e != nil {
			return e
		}
		if e = os.Rename(stage, r.releaseDir(p.Version)); e != nil {
			return e
		}
		return nasRotationSync(filepath.Join(r.root, "releases"))
	})
}
func (r *nasRotation) status(rows []nasRotationRelease) (nasRotationStatus, error) {
	var result nasRotationStatus
	work, e := os.MkdirTemp(r.root, ".status-")
	if e != nil {
		return result, e
	}
	defer func() { _ = os.RemoveAll(work) }()
	signed, ann, _ := r.latest(rows)
	plan, e := r.plan(rows, signed, ann, r.state, map[string]string{})
	if e != nil {
		return result, e
	}
	b, e := r.operator("status", work, plan)
	if e != nil {
		return result, e
	}
	if nasRotationDecode(b, &result) != nil || !nasRotationVersion(result.Version) || len(result.First) == 0 {
		return result, fmt.Errorf("rotation status refused")
	}
	return result, nil
}
func nasRotationActive(p nasRotationPayload, now time.Time) (map[string]nasRotationKey, error) {
	active := map[string]nasRotationKey{}
	for _, k := range p.Keys {
		if !nasRotationKid.MatchString(k.Kid) {
			return nil, fmt.Errorf("rotation invalid kid")
		}
		if k.State != "active" {
			continue
		}
		b, e := time.Parse(time.RFC3339, k.Before)
		a, e2 := time.Parse(time.RFC3339, k.After)
		if e != nil || e2 != nil || now.Before(b) || !now.Before(a) || active[k.Purpose].Kid != "" {
			return nil, fmt.Errorf("rotation active key unavailable")
		}
		active[k.Purpose] = k
	}
	for _, p := range []string{"business_delivery", "protocol_response", "management_state"} {
		if active[p].Kid == "" {
			return nil, fmt.Errorf("rotation requires three valid active purposes; wait for real publication maturity")
		}
	}
	return active, nil
}
func nasRotationGateway(raw []byte, version string, key nasRotationKey) ([]byte, error) {
	if key.Purpose != "protocol_response" || key.State != "active" || !nasRotationKid.MatchString(key.Kid) {
		return nil, fmt.Errorf("rotation active protocol required")
	}
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil || original == nil {
		return nil, fmt.Errorf("rotation gateway refused")
	}
	p, e := json.Marshal(map[string]string{"keyId": key.Kid, "keysetVersion": version, "notBefore": key.Before, "notAfter": key.After, "privateKeyFile": "/etc/auriga/network/protocol.key"})
	if e != nil {
		return nil, e
	}
	original["protocolSigning"] = p
	return json.Marshal(original)
}

func (r *nasRotation) install() error {
	in, signed, ann, p, e := r.loadInput("install-input.json")
	if e != nil {
		return e
	}
	rows, e := r.releases()
	if e != nil || len(rows) == 0 {
		return fmt.Errorf("rotation publication required before install")
	}
	last := rows[len(rows)-1]
	if last.Version != p.Version || last.Files["signed.json"] != nasRotationHash(signed) || last.Files["announcement.json"] != nasRotationHash(ann) || last.Approval != in.Approval || last.Mode != in.Mode {
		return fmt.Errorf("rotation installation differs from latest publication")
	}
	active, e := nasRotationActive(p, time.Now())
	if e != nil {
		return e
	}
	secrets := map[string][]byte{}
	defer func() {
		for _, b := range secrets {
			clear(b)
		}
	}()
	for _, k := range p.Keys {
		if k.State == "active" || k.State == "next" {
			name, ok := in.Private[k.Kid]
			if !ok {
				return fmt.Errorf("rotation every active/next private key required")
			}
			b, e := nasRotationRead(filepath.Join(r.inputDir, name), 0)
			if e != nil {
				return e
			}
			secrets[k.Kid] = b
		}
	}
	if len(secrets) != len(in.Private) {
		return fmt.Errorf("rotation extra private key refused")
	}
	if _, e := os.Lstat(filepath.Join(r.root, "installs", p.Version)); e == nil {
		dir, _, e := r.installed(rows, true)
		if e != nil {
			return e
		}
		for kid, b := range secrets {
			actual, e := nasRotationRead(filepath.Join(dir, "config/keys", kid+".der"), 10002)
			defer clear(actual)
			if e != nil || !bytes.Equal(b, actual) {
				return fmt.Errorf("rotation same-version private material changed")
			}
		}
		_, e = r.status(rows)
		return e
	}
	return r.mutate("install", func(work string) error {
		stage := filepath.Join(work, "runtime")
		for _, dir := range []string{stage, filepath.Join(stage, "config"), filepath.Join(stage, "config/keys"), filepath.Join(stage, "network")} {
			if e := os.Mkdir(dir, 0700); e != nil {
				return e
			}
		}
		config := filepath.Join(stage, "config")
		for _, name := range []string{"root.der", "browser.crt", "ca.crt", "server.crt", "server.key", "client.crt", "client.key"} {
			b, e := nasRotationRead(filepath.Join(r.source, "collab-installation", name), 0)
			if e != nil {
				return e
			}
			e = nasRotationWrite(filepath.Join(config, name), b)
			clear(b)
			if e != nil {
				return e
			}
		}
		private := map[string]string{}
		for kid, b := range secrets {
			path := filepath.Join(config, "keys", kid+".der")
			if e := nasRotationWrite(path, b); e != nil {
				return e
			}
			private[kid] = path
		}
		properties, e := nasRotationRead(filepath.Join(r.source, "collab-installation/collab.properties"), 0)
		if e != nil {
			return e
		}
		var lines []string
		for _, line := range strings.Split(string(properties), "\n") {
			if !strings.HasPrefix(line, "auriga.collab.network.key-operations.private-files[") {
				lines = append(lines, line)
			}
		}
		clear(properties)
		for _, k := range p.Keys {
			if private[k.Kid] != "" {
				lines = append(lines, "auriga.collab.network.key-operations.private-files["+k.Kid+"]=/etc/auriga/collab/keys/"+k.Kid+".der")
			}
		}
		for name, b := range map[string][]byte{"signed.json": signed, "announcement.json": ann, "collab.properties": []byte(strings.Join(lines, "\n") + "\n")} {
			if e = nasRotationWrite(filepath.Join(config, name), b); e != nil {
				return e
			}
		}
		gateway, e := nasRotationRead(filepath.Join(r.source, "network/runtime.json"), 0)
		if e != nil {
			return e
		}
		gateway, e = nasRotationGateway(gateway, p.Version, active["protocol_response"])
		if e != nil {
			return e
		}
		if e = nasRotationWrite(filepath.Join(stage, "network/runtime.json"), gateway); e != nil {
			return e
		}
		protocol := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: secrets[active["protocol_response"].Kid]})
		defer clear(protocol)
		if e = nasRotationWrite(filepath.Join(stage, "network/protocol.key"), protocol); e != nil {
			return e
		}
		plan, e := r.plan(rows, filepath.Join(config, "signed.json"), filepath.Join(config, "announcement.json"), filepath.Join(work, "checked-state.json"), private)
		if e != nil {
			return e
		}
		if _, e = r.operator("check-private", work, plan); e != nil {
			return e
		}
		if e = r.engine.stop(r); e != nil {
			return e
		}
		plan.State = r.state
		previousState, e := nasRotationRead(r.state, 10002)
		if e != nil {
			return e
		}
		if e = nasRotationWrite(filepath.Join(stage, "previous-state.json"), previousState); e != nil {
			return e
		}
		if _, e = r.operator("install", work, plan); e != nil {
			return e
		}
		if e = os.Chown(r.state, 10002, 10002); e != nil {
			return e
		}
		state, e := nasRotationRead(r.state, 10002)
		if e != nil {
			return e
		}
		files := map[string]string{}
		e = filepath.WalkDir(stage, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			uid := 10002
			rel, e := filepath.Rel(stage, path)
			if e != nil {
				return e
			}
			if rel == "." {
				return nil
			}
			if rel == "previous-state.json" {
				return nil
			}
			if rel == "network" || strings.HasPrefix(filepath.ToSlash(rel), "network/") {
				uid = 10001
			}
			if !d.IsDir() {
				b, e := nasRotationRead(path, 0)
				if e != nil {
					return e
				}
				files[filepath.ToSlash(rel)] = nasRotationHash(b)
				clear(b)
			}
			return os.Chown(path, uid, uid)
		})
		if e != nil {
			return e
		}
		pub, e := nasRotationRead(filepath.Join(r.releaseDir(p.Version), "publication.receipt"), 0)
		if e != nil {
			return e
		}
		receipt := nasRotationInstall{Binding: r.binding, Version: p.Version, Publication: nasRotationHash(pub), State: nasRotationHash(state), PreviousState: nasRotationHash(previousState), Files: files}
		if e = nasRotationWriteJSON(filepath.Join(stage, "install.receipt"), receipt); e != nil {
			return e
		}
		if e = nasRotationSync(stage); e != nil {
			return e
		}
		destination := filepath.Join(r.root, "installs", p.Version)
		if e = os.Rename(stage, destination); e != nil {
			return e
		}
		if e = nasRotationSync(filepath.Join(r.root, "installs")); e != nil {
			return e
		}
		b, e := nasRotationRead(filepath.Join(destination, "install.receipt"), 0)
		if e != nil {
			return e
		}
		if e = nasRotationWriteJSON(filepath.Join(work, "current.json"), nasRotationCurrent{Version: p.Version, Receipt: nasRotationHash(b)}); e != nil {
			return e
		}
		if e = os.Rename(filepath.Join(work, "current.json"), filepath.Join(r.root, "current.json")); e != nil {
			return e
		}
		return nasRotationSync(r.root)
	})
}

// Requests are unsigned until the offline operator explicitly approves them.
// PG firstPublished is consulted for scheduling only; production publish remains authority.
func nasRotationRequest(old nasRotationPayload, first map[string]string, now time.Time) (nasRotationPayload, map[string][]byte, error) {
	n, e := strconv.ParseInt(old.Version, 10, 64)
	if e != nil || n <= 0 || n == int64(^uint64(0)>>1) {
		return old, nil, fmt.Errorf("rotation version exhausted")
	}
	p := old
	p.Keys = append([]nasRotationKey{}, old.Keys...)
	p.Version = strconv.FormatInt(n+1, 10)
	now = now.UTC().Truncate(time.Second)
	p.Issued = now.Format(time.RFC3339)
	p.Until = now.Add(24 * time.Hour).Format(time.RFC3339)
	private := map[string][]byte{}
	for _, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		active := -1
		next := -1
		for i, k := range p.Keys {
			if k.Purpose == purpose {
				if k.State == "active" {
					active = i
				}
				if k.State == "next" {
					if next != -1 {
						return p, nil, fmt.Errorf("rotation multiple next keys require operator selection")
					}
					next = i
				}
			}
		}
		expired := active < 0
		var expires time.Time
		if active >= 0 {
			expires, e = time.Parse(time.RFC3339, p.Keys[active].After)
			if e != nil {
				return p, nil, e
			}
			expired = !now.Before(expires)
		}
		if next >= 0 {
			after, e := time.Parse(time.RFC3339, p.Keys[next].After)
			if e != nil || !now.Before(after) {
				return p, nil, fmt.Errorf("rotation next key expired; operator decision required")
			}
			published, e := time.Parse(time.RFC3339Nano, first[p.Keys[next].Kid])
			mature := e == nil && !now.Before(published.Add(24*time.Hour))
			if mature && (active < 0 || !expires.Before(now.Add(360*time.Second))) {
				p.Keys[next].State = "active"
				if active >= 0 {
					p.Keys[active].State = "retired"
				}
			} else if expired && active >= 0 {
				p.Keys[active].State = "retired"
			}
			continue
		}
		if !expired && expires.Sub(now) > 48*time.Hour {
			continue
		}
		if expired && active >= 0 {
			p.Keys[active].State = "retired"
		}
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return p, nil, e
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		clear(key)
		if e != nil {
			return p, nil, e
		}
		pubDER, e := x509.MarshalPKIXPublicKey(pub)
		if e != nil {
			return p, nil, e
		}
		id := make([]byte, 12)
		if _, e = rand.Read(id); e != nil {
			return p, nil, e
		}
		kid := "nas-" + strings.ReplaceAll(purpose, "_", "-") + "-" + hex.EncodeToString(id)
		private[kid] = der
		p.Keys = append(p.Keys, nasRotationKey{Kid: kid, Alg: "Ed25519", Purpose: purpose, Public: base64.RawURLEncoding.EncodeToString(pubDER), Before: p.Issued, After: now.Add(7 * 24 * time.Hour).Format(time.RFC3339), State: "next"})
	}
	return p, private, nil
}
