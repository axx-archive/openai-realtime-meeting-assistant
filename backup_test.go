package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// sampleDataDir builds a temp data dir populated with the real state-file names
// (plus an archives/ and blobs/ subdir) so snapshot tests exercise the actual
// walk/exclude logic.
func sampleDataDir(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"meeting-memory.jsonl": `{"kind":"transcript","text":"hello"}` + "\n",
		"kanban-board.json":    `{"columns":[]}`,
		"meetings.json":        `[]`,
		"notifications.json":   `[]`,
		"users.json":           `{"users":[]}`,
		"sessions.json":        `{}`,
		"rooms.json":           `{"rooms":[]}`,
		"file-folders.json":    `{"folders":[]}`,
		"archive-secret":       "supersecret-hmac-seed",
		"vapid-keys.json":      `{"public":"x","private":"y"}`,
		"archives/a1.json":     `{"archived":true}`,
		"blobs/deadbeef":       "BINARYBLOBDATA",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return dir, files
}

// readTarGz returns a map of tar path -> content from a gzip'd tar stream.
func readTarGz(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(body)
	}
	return out
}

func TestBackupSnapshotUnencryptedRoundTrip(t *testing.T) {
	dir, files := sampleDataDir(t)
	cfg := backupConfig{dataDir: dir, ringKeep: 7, includeBlobs: false, keyConfigured: false}
	out, err := createBackupSnapshot(cfg, time.Date(2026, 7, 10, 18, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("createBackupSnapshot: %v", err)
	}
	if out.encrypted {
		t.Fatalf("expected unencrypted snapshot")
	}
	if !strings.HasSuffix(out.path, ".tgz") || strings.HasSuffix(out.path, ".tgz.enc") {
		t.Fatalf("unexpected snapshot path %s", out.path)
	}
	if !strings.Contains(filepath.Base(out.path), "bonfire-data-20260710T183000Z") {
		t.Fatalf("timestamp not embedded in %s", out.path)
	}
	raw, err := os.ReadFile(out.path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	got := readTarGz(t, raw)

	// Every non-blob file must be present with identical content; blobs excluded.
	for rel, content := range files {
		slashRel := filepath.ToSlash(rel)
		if strings.HasPrefix(slashRel, "blobs/") {
			if _, ok := got[slashRel]; ok {
				t.Errorf("blobs excluded by default, but %s is in the archive", slashRel)
			}
			continue
		}
		if got[slashRel] != content {
			t.Errorf("content mismatch for %s: got %q want %q", slashRel, got[slashRel], content)
		}
	}
	// The archives/ subdir file must survive.
	if _, ok := got["archives/a1.json"]; !ok {
		t.Errorf("archives/a1.json missing from archive")
	}
}

func TestBackupSnapshotEncryptedRoundTrip(t *testing.T) {
	dir, files := sampleDataDir(t)
	key := bytes.Repeat([]byte{0x2b}, 32)
	cfg := backupConfig{dataDir: dir, ringKeep: 7, includeBlobs: true, keyConfigured: true, encryptionKey: key}
	out, err := createBackupSnapshot(cfg, time.Now().UTC())
	if err != nil {
		t.Fatalf("createBackupSnapshot: %v", err)
	}
	if !out.encrypted || !strings.HasSuffix(out.path, ".tgz.enc") {
		t.Fatalf("expected encrypted .tgz.enc, got %s", out.path)
	}
	blob, err := os.ReadFile(out.path)
	if err != nil {
		t.Fatalf("read enc snapshot: %v", err)
	}
	// The magic marker must be the file prefix.
	if !bytes.HasPrefix(blob, []byte(backupEncMagic)) {
		t.Fatalf("encrypted snapshot missing magic")
	}
	plain, err := decryptBackupBlob(key, blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	got := readTarGz(t, plain)
	for rel, content := range files {
		slashRel := filepath.ToSlash(rel)
		if got[slashRel] != content {
			t.Errorf("content mismatch for %s after decrypt: got %q want %q", slashRel, got[slashRel], content)
		}
	}
	// includeBlobs=true means the blob is present this time.
	if got["blobs/deadbeef"] != "BINARYBLOBDATA" {
		t.Errorf("blob missing though BACKUP_INCLUDE_BLOBS was set")
	}
}

func TestBackupNeverIncludesPriorSnapshots(t *testing.T) {
	dir, _ := sampleDataDir(t)
	cfg := backupConfig{dataDir: dir, ringKeep: 7}
	if _, err := createBackupSnapshot(cfg, time.Unix(1000, 0)); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	// A second snapshot must not contain the first (no backups/ recursion → the
	// ring can never grow without bound).
	out, err := createBackupSnapshot(cfg, time.Unix(2000, 0))
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	raw, err := os.ReadFile(out.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for name := range readTarGz(t, raw) {
		if strings.HasPrefix(name, backupDirName+"/") {
			t.Fatalf("snapshot recursively included the backups dir: %s", name)
		}
	}
}

func TestBackupEncryptionRoundTripAndTamper(t *testing.T) {
	key := sha256.Sum256([]byte("passphrase-one"))
	plaintext := []byte("the entire company brain, compressed")
	blob, err := encryptBackupBlob(key[:], plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := decryptBackupBlob(key[:], blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch")
	}
	// Wrong key fails authentication.
	wrong := sha256.Sum256([]byte("passphrase-two"))
	if _, err := decryptBackupBlob(wrong[:], blob); err == nil {
		t.Fatalf("expected auth failure with wrong key")
	}
	// A flipped ciphertext byte fails authentication.
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := decryptBackupBlob(key[:], tampered); err == nil {
		t.Fatalf("expected auth failure on tampered ciphertext")
	}
	// Missing/foreign magic fails clearly.
	if _, err := decryptBackupBlob(key[:], []byte("not-a-backup-file-at-all-xxxxxxxx")); err == nil {
		t.Fatalf("expected magic rejection")
	}
}

func TestDeriveBackupKey(t *testing.T) {
	// Empty => no key.
	if k, err := deriveBackupKey("  "); err != nil || k != nil {
		t.Fatalf("empty should yield nil key, got %v err=%v", k, err)
	}
	// 32-byte hex => raw key.
	hexKey := strings.Repeat("ab", 32) // 64 hex chars => 32 bytes
	if k, err := deriveBackupKey(hexKey); err != nil || len(k) != 32 {
		t.Fatalf("hex key: len=%d err=%v", len(k), err)
	}
	// 32-byte base64 => raw key.
	rawKey := bytes.Repeat([]byte{0x11}, 32)
	b64 := base64.StdEncoding.EncodeToString(rawKey)
	k, err := deriveBackupKey(b64)
	if err != nil || !bytes.Equal(k, rawKey) {
		t.Fatalf("base64 key mismatch: err=%v", err)
	}
	// Arbitrary passphrase => SHA-256 of the passphrase.
	pass := "correct horse battery staple"
	want := sha256.Sum256([]byte(pass))
	if k, err := deriveBackupKey(pass); err != nil || !bytes.Equal(k, want[:]) {
		t.Fatalf("passphrase derivation mismatch: err=%v", err)
	}
}

func TestBackupRingRotation(t *testing.T) {
	dir := t.TempDir()
	// Create 10 snapshots with sortable timestamps + one unrelated file.
	var made []string
	for i := 0; i < 10; i++ {
		name := backupFilePrefix + time.Date(2026, 7, 1, 0, 0, i, 0, time.UTC).Format(backupTimestampLayout) + ".tgz.enc"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		made = append(made, name)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := rotateBackupRing(dir, 3)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if kept != 3 {
		t.Fatalf("kept=%d want 3", kept)
	}
	remaining := listSnapshotNames(t, dir)
	sort.Strings(made)
	wantNewest := made[len(made)-3:]
	if strings.Join(remaining, ",") != strings.Join(wantNewest, ",") {
		t.Fatalf("ring kept wrong files: got %v want %v", remaining, wantNewest)
	}
	// The unrelated file must be untouched.
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Fatalf("rotation deleted a non-snapshot file: %v", err)
	}
}

func listSnapshotNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupFilePrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestBackupOffsiteDormantWithoutS3(t *testing.T) {
	dir, _ := sampleDataDir(t)
	cfg := backupConfig{dataDir: dir, ringKeep: 7, keyConfigured: true, encryptionKey: bytes.Repeat([]byte{7}, 32)}
	out, err := createBackupSnapshot(cfg, time.Now())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if out.offsite != "dormant" {
		t.Fatalf("offsite=%q want dormant (no S3 configured)", out.offsite)
	}
}

func TestBackupOffsiteFailsClosedWithoutKey(t *testing.T) {
	dir, _ := sampleDataDir(t)
	// A live-looking S3 target that MUST NOT be contacted because there is no key.
	var contacted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	cfg := backupConfig{
		dataDir:       dir,
		ringKeep:      7,
		keyConfigured: false, // no key
		s3: &backupS3Config{
			endpoint: strings.TrimPrefix(srv.URL, "http://"), bucket: "b",
			region: "us-east-1", accessKey: "AK", secretKey: "SK", pathStyle: true, scheme: "http",
		},
	}
	out, err := createBackupSnapshot(cfg, time.Now())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if out.offsite != "skip_no_key" {
		t.Fatalf("offsite=%q want skip_no_key", out.offsite)
	}
	if contacted {
		t.Fatalf("fail-closed violated: the S3 endpoint was contacted without an encryption key")
	}
	// A local (unencrypted) snapshot must still exist.
	if !strings.HasSuffix(out.path, ".tgz") || strings.HasSuffix(out.path, ".enc") {
		t.Fatalf("expected a local unencrypted .tgz, got %s", out.path)
	}
}

func TestBackupOffsiteUploadAndSigV4(t *testing.T) {
	dir, _ := sampleDataDir(t)
	const secret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	var gotBody []byte
	var verifyErr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			verifyErr = "method not PUT: " + r.Method
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		if err := verifyS3PutSignature(r, body, secret); err != "" {
			verifyErr = err
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	key := bytes.Repeat([]byte{0x5a}, 32)
	cfg := backupConfig{
		dataDir: dir, ringKeep: 7, keyConfigured: true, encryptionKey: key,
		s3: &backupS3Config{
			endpoint: strings.TrimPrefix(srv.URL, "http://"), bucket: "bonfire-backups",
			region: "nyc3", accessKey: "DO00ACCESSKEY", secretKey: secret,
			prefix: "brain/", pathStyle: true, scheme: "http",
		},
	}
	out, err := createBackupSnapshot(cfg, time.Date(2026, 7, 10, 18, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if verifyErr != "" {
		t.Fatalf("server rejected the signed request: %s", verifyErr)
	}
	if out.offsite != "ok" {
		t.Fatalf("offsite=%q want ok", out.offsite)
	}
	// The bytes uploaded must be exactly the encrypted local snapshot.
	local, err := os.ReadFile(out.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBody, local) {
		t.Fatalf("uploaded bytes (%d) differ from local snapshot (%d)", len(gotBody), len(local))
	}
}

// verifyS3PutSignature independently re-derives the SigV4 signature for a received
// request and compares it to the Authorization header, exactly as S3 would. A
// match proves signAndBuildS3PutRequest produced a valid signature. Returns ""
// on success or a human-readable reason on failure.
func verifyS3PutSignature(r *http.Request, body []byte, secret string) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return "bad Authorization prefix: " + auth
	}
	// Payload hash header must match the body S3 actually received.
	wantHash := hex.EncodeToString(sha256Sum(body))
	if got := r.Header.Get("X-Amz-Content-Sha256"); got != wantHash {
		return "content-sha256 mismatch: " + got + " != " + wantHash
	}
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			fields[kv[0]] = kv[1]
		}
	}
	cred := fields["Credential"]
	signedHeaders := fields["SignedHeaders"]
	gotSig := fields["Signature"]
	if cred == "" || signedHeaders == "" || gotSig == "" {
		return "missing Authorization fields"
	}
	credParts := strings.Split(cred, "/")
	if len(credParts) != 5 || credParts[4] != "aws4_request" {
		return "bad credential scope: " + cred
	}
	dateStamp, region, service := credParts[1], credParts[2], credParts[3]
	amzDate := r.Header.Get("X-Amz-Date")

	canonicalHeaders := "host:" + r.Host + "\n" +
		"x-amz-content-sha256:" + wantHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		r.URL.EscapedPath(),
		"",
		canonicalHeaders,
		signedHeaders,
		wantHash,
	}, "\n")
	scope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(sha256Sum([]byte(canonicalRequest))),
	}, "\n")
	signingKey := s3SigningKey(secret, dateStamp, region)
	wantSig := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	if wantSig != gotSig {
		return "signature mismatch: got " + gotSig + " want " + wantSig
	}
	return ""
}

// TestBackupIncludeBlobsDefaultsOn pins the roadmap-0.1 fix: blobs are in the
// snapshot by DEFAULT (a default-posture restore must not silently lose every
// uploaded file body), with an explicit falsy opt-out.
func TestBackupIncludeBlobsDefaultsOn(t *testing.T) {
	t.Setenv("BACKUP_INCLUDE_BLOBS", "")
	if !backupIncludeBlobs() {
		t.Fatal("blobs must be included by default (roadmap-0.1: tar the data dir + blobs)")
	}
	for _, off := range []string{"0", "off", "false", "no", "disabled"} {
		t.Setenv("BACKUP_INCLUDE_BLOBS", off)
		if backupIncludeBlobs() {
			t.Errorf("BACKUP_INCLUDE_BLOBS=%q should opt OUT of blobs", off)
		}
	}
	for _, on := range []string{"1", "true", "yes", "on"} {
		t.Setenv("BACKUP_INCLUDE_BLOBS", on)
		if !backupIncludeBlobs() {
			t.Errorf("BACKUP_INCLUDE_BLOBS=%q should include blobs", on)
		}
	}
	// The resolved config carries the default through.
	t.Setenv("BACKUP_INCLUDE_BLOBS", "")
	if !loadBackupConfig().includeBlobs {
		t.Fatal("loadBackupConfig must default includeBlobs to true")
	}
}

// TestDeriveBackupKeyAcceptsRawBase64 pins the server contract the restore doc
// must mirror: a 32-byte key supplied as UNPADDED base64 is used as raw key
// material, not SHA-256'd as a passphrase.
func TestDeriveBackupKeyAcceptsRawBase64(t *testing.T) {
	raw := bytes.Repeat([]byte{0x3c}, 32)
	unpadded := base64.RawStdEncoding.EncodeToString(raw) // 43 chars, no '='
	if strings.Contains(unpadded, "=") {
		t.Fatalf("expected unpadded base64, got %q", unpadded)
	}
	k, err := deriveBackupKey(unpadded)
	if err != nil || !bytes.Equal(k, raw) {
		t.Fatalf("unpadded base64 key not used as raw 32 bytes: err=%v k=%x", err, k)
	}
}

// TestBackupTarGzToleratesVanishingFile pins the writeDataDirTarGz fix: a file
// that vanishes between os.ReadFile and d.Info() (a memory-store compaction temp
// renamed away mid-walk) is skipped, not fatal — one racing temp must never fail
// the whole nightly snapshot.
func TestBackupTarGzToleratesVanishingFile(t *testing.T) {
	// Premise: a WalkDir entry's Info() lazily lstats, so a file removed after it
	// is listed yields an os.IsNotExist error — exactly the race the skip handles.
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "gone.jsonl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var infoErr error
	_ = filepath.WalkDir(probe, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		os.Remove(path) // vanish between listing and Info()
		_, infoErr = d.Info()
		return nil
	})
	if !os.IsNotExist(infoErr) {
		t.Fatalf("expected IsNotExist from Info() after remove, got %v", infoErr)
	}

	// End-to-end: churn a compaction-shaped temp (.meeting-memory-*.jsonl, NOT
	// matched by the .tmp- skip) in the data dir while snapshots run; with the
	// vanished-file skip every snapshot must succeed.
	dir, _ := sampleDataDir(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tmp := filepath.Join(dir, fmt.Sprintf(".meeting-memory-%d.jsonl", i))
			if err := os.WriteFile(tmp, bytes.Repeat([]byte("data\n"), 4096), 0o600); err != nil {
				continue
			}
			os.Rename(tmp, filepath.Join(dir, "meeting-memory.jsonl")) // temp name disappears
		}
	}()
	for i := 0; i < 200; i++ {
		var buf bytes.Buffer
		if err := writeDataDirTarGz(&buf, dir, true); err != nil {
			close(stop)
			<-done
			t.Fatalf("snapshot %d failed on a vanishing-file race: %v", i, err)
		}
	}
	close(stop)
	<-done
}

func TestBackupIntervalAndDisabled(t *testing.T) {
	cases := []struct {
		env      string
		wantHrs  int
		disabled bool
	}{
		{"", 24, false},
		{"6", 6, false},
		{"0", 0, true},
		{"off", 0, true},
		{"disabled", 0, true},
		{"garbage", 24, false},
	}
	for _, c := range cases {
		t.Setenv("BACKUP_INTERVAL_HOURS", c.env)
		t.Setenv("BACKUP_DISABLED", "")
		if got := backupIntervalHours(); got != c.wantHrs {
			t.Errorf("BACKUP_INTERVAL_HOURS=%q => %d want %d", c.env, got, c.wantHrs)
		}
		if got := backupDisabled(); got != c.disabled {
			t.Errorf("BACKUP_INTERVAL_HOURS=%q disabled=%v want %v", c.env, got, c.disabled)
		}
	}
	// Explicit BACKUP_DISABLED wins even on a valid interval.
	t.Setenv("BACKUP_INTERVAL_HOURS", "24")
	t.Setenv("BACKUP_DISABLED", "true")
	if !backupDisabled() {
		t.Errorf("BACKUP_DISABLED=true should disable")
	}
}

func TestBackupS3ConfigRequiresAllFields(t *testing.T) {
	// Missing any one required field => nil (offsite dormant).
	for _, missing := range []string{"BACKUP_S3_ENDPOINT", "BACKUP_S3_BUCKET", "BACKUP_S3_ACCESS_KEY", "BACKUP_S3_SECRET_KEY"} {
		t.Setenv("BACKUP_S3_ENDPOINT", "nyc3.digitaloceanspaces.com")
		t.Setenv("BACKUP_S3_BUCKET", "b")
		t.Setenv("BACKUP_S3_ACCESS_KEY", "AK")
		t.Setenv("BACKUP_S3_SECRET_KEY", "SK")
		t.Setenv(missing, "")
		if cfg := loadBackupS3Config(); cfg != nil {
			t.Errorf("missing %s should yield nil S3 config", missing)
		}
	}
	// All present => config, with scheme defaulted and endpoint scheme-stripped.
	t.Setenv("BACKUP_S3_ENDPOINT", "https://nyc3.digitaloceanspaces.com/")
	t.Setenv("BACKUP_S3_BUCKET", "b")
	t.Setenv("BACKUP_S3_ACCESS_KEY", "AK")
	t.Setenv("BACKUP_S3_SECRET_KEY", "SK")
	t.Setenv("BACKUP_S3_REGION", "")
	cfg := loadBackupS3Config()
	if cfg == nil {
		t.Fatal("expected non-nil S3 config")
	}
	if cfg.endpoint != "nyc3.digitaloceanspaces.com" {
		t.Errorf("endpoint not normalized: %q", cfg.endpoint)
	}
	if cfg.region != defaultBackupS3Region {
		t.Errorf("region default: %q", cfg.region)
	}
	if cfg.scheme != "https" {
		t.Errorf("scheme default: %q", cfg.scheme)
	}
}
