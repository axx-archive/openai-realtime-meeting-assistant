package main

// Nightly data-dir snapshot engine (memory-architecture study 2026-07-10, §5
// gap #1 / §6 Phase 0.1). The entire company brain lives in one Docker volume on
// one droplet disk with zero backup: one disk failure or fat-finger is total
// permanent loss — the maximal violation of the brain's "always recall" mandate.
//
// This is an in-process, boot-once background worker (the slop-classifier /
// liveness-sweeper idiom, not an ambientAgentConfig): it makes no model call and
// touches no app lock. Once a night it tar.gz's the data dir, optionally
// AES-256-GCM encrypts it, keeps a rotating local ring inside the volume, and —
// when S3/Spaces credentials are configured — PUTs the encrypted snapshot
// offsite via a pure-stdlib SigV4 signer (no new module deps).
//
// Consistency model: the target files are append-mostly JSON/JSONL. Each file is
// read once, whole (os.ReadFile), and its byte count becomes the tar header size,
// so a concurrent append never produces a size-mismatched (corrupt) tar entry.
// No app lock is held. Worst case for a file mid-append is that the snapshot
// captures the file as of its read instant and misses that same tick's tail —
// i.e. the theoretical maximum loss on restore is the current day's tail, versus
// today's actual exposure of losing *everything* on a disk failure. This is the
// deliberate "consistent-enough for an append-mostly store" tradeoff from the
// study (§6 Phase 0.1).
//
// The local ring lives INSIDE the same volume it snapshots, so it survives
// `docker compose up -d --build` rebuilds but is NOT offsite — a disk loss takes
// the ring with it. Offsite (S3/Spaces) is the actual disaster-recovery copy and
// requires an encryption key: the engine fails closed and refuses to upload
// unencrypted (see runBackupOnce).

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBackupIntervalHours = 24
	defaultBackupRingKeep      = 7
	// defaultBackupFirstRunDelay stages the first snapshot a few minutes after
	// boot so a fresh deploy proves the whole path (tar → encrypt → ring →
	// offsite) without waiting a full interval. It doubles as a unit-test guard:
	// main() is never invoked under `go test`, but even if the ticker started, no
	// snapshot fires inside a short test window.
	defaultBackupFirstRunDelay = 3 * time.Minute

	backupDirName         = "backups"
	backupFilePrefix      = "bonfire-data-"
	backupTimestampLayout = "20060102T150405Z" // UTC, colon-free, lexically sortable
	defaultBackupS3Region = "us-east-1"

	// backupEncMagic is an unencrypted, GCM-authenticated file-type marker. It is
	// bound as the GCM additional-authenticated-data so a decrypt with the wrong
	// key or a tampered/foreign file fails the auth check rather than emitting
	// garbage. On-disk layout of a .tgz.enc: magic(8) | nonce(12) | ciphertext+tag.
	backupEncMagic = "BFBKUP01"
)

// backupS3Config is the offsite target. All five of endpoint/bucket/accessKey/
// secretKey (region defaults) must be present for uploads to be attempted;
// anything short leaves offsite dormant (local ring only).
type backupS3Config struct {
	endpoint  string // host only, no scheme, e.g. "nyc3.digitaloceanspaces.com"
	bucket    string
	region    string
	accessKey string
	secretKey string
	prefix    string // optional object-key prefix, e.g. "bonfire/"
	pathStyle bool   // true => https://host/bucket/key ; false => https://bucket.host/key
	scheme    string // "https" in prod; overridden to "http" only by tests
}

// backupConfig is the resolved env surface for one run. Read fresh each pass so
// ops changes take effect on the next tick without a restart (parity with the
// workflow ticker's env getters).
type backupConfig struct {
	dataDir       string
	intervalHours int
	ringKeep      int
	includeBlobs  bool
	firstRunDelay time.Duration

	// encryptionKey is 32 bytes when a key is configured, nil otherwise.
	// keyConfigured distinguishes "no key set" (local plaintext, offsite dormant)
	// from a present-but-derived key.
	encryptionKey []byte
	keyConfigured bool

	s3 *backupS3Config // nil unless all required S3 env is present
}

// backupDataDir resolves the directory to snapshot from the SAME source every
// state file derives from (filepath.Dir(meetingMemoryPath())), so it tracks
// MEETING_MEMORY_PATH overrides without a second config knob.
func backupDataDir() string {
	dir := filepath.Dir(strings.TrimSpace(meetingMemoryPath()))
	if dir == "" {
		return "."
	}
	return dir
}

// backupIntervalHours parses BACKUP_INTERVAL_HOURS. Empty => 24h; the
// off-switches ("0"/"off"/"false"/"disabled") => 0 (disabled), matching the
// workflow ticker's interval vocabulary.
func backupIntervalHours() int {
	raw := strings.TrimSpace(os.Getenv("BACKUP_INTERVAL_HOURS"))
	if raw == "" {
		return defaultBackupIntervalHours
	}
	switch strings.ToLower(raw) {
	case "0", "off", "false", "disabled":
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return defaultBackupIntervalHours
	}
	return n
}

// backupDisabled is the boot-time short-circuit: an explicit BACKUP_DISABLED or a
// disabled interval keeps the ticker goroutine from ever starting.
func backupDisabled() bool {
	return boolEnv("BACKUP_DISABLED") || backupIntervalHours() <= 0
}

// backupIncludeBlobs resolves whether the blobs/ upload store is snapshotted. It
// defaults to TRUE: the roadmap-0.1 backup promise is "tar the data dir + blobs",
// and a default-posture restore that silently lost every uploaded file body would
// break it. Set BACKUP_INCLUDE_BLOBS to a falsy value (0/off/false/no/disabled)
// to opt out when snapshot size or encrypt-step RAM is a concern.
func backupIncludeBlobs() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BACKUP_INCLUDE_BLOBS"))) {
	case "0", "off", "false", "no", "disabled":
		return false
	default:
		return true
	}
}

// deriveBackupKey turns BACKUP_ENCRYPTION_KEY into a 32-byte AES-256 key. A value
// that hex- or base64-decodes to exactly 32 bytes is used as raw key material;
// anything else is treated as a passphrase and SHA-256'd to 32 bytes. Empty =>
// (nil, nil): no key configured.
func deriveBackupKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

// loadBackupS3Config returns a fully-populated target or nil when any required
// field is missing (offsite stays dormant, local-only).
func loadBackupS3Config() *backupS3Config {
	endpoint := normalizeS3Host(os.Getenv("BACKUP_S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("BACKUP_S3_BUCKET"))
	access := strings.TrimSpace(os.Getenv("BACKUP_S3_ACCESS_KEY"))
	secret := strings.TrimSpace(os.Getenv("BACKUP_S3_SECRET_KEY"))
	if endpoint == "" || bucket == "" || access == "" || secret == "" {
		return nil
	}
	region := strings.TrimSpace(os.Getenv("BACKUP_S3_REGION"))
	if region == "" {
		region = defaultBackupS3Region
	}
	return &backupS3Config{
		endpoint:  endpoint,
		bucket:    bucket,
		region:    region,
		accessKey: access,
		secretKey: secret,
		prefix:    strings.TrimSpace(os.Getenv("BACKUP_S3_PREFIX")),
		pathStyle: boolEnv("BACKUP_S3_PATH_STYLE"),
		scheme:    "https",
	}
}

// normalizeS3Host strips a scheme and any trailing slash so the value is a bare
// host suitable for both the Host header and SigV4 canonicalization.
func normalizeS3Host(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return strings.TrimRight(host, "/")
}

func loadBackupConfig() backupConfig {
	key, _ := deriveBackupKey(os.Getenv("BACKUP_ENCRYPTION_KEY"))
	firstRun := defaultBackupFirstRunDelay
	if raw := strings.TrimSpace(os.Getenv("BACKUP_FIRST_RUN_DELAY")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			firstRun = d
		}
	}
	return backupConfig{
		dataDir:       backupDataDir(),
		intervalHours: backupIntervalHours(),
		ringKeep:      positiveIntEnv("BACKUP_RING_KEEP", defaultBackupRingKeep),
		includeBlobs:  backupIncludeBlobs(),
		firstRunDelay: firstRun,
		encryptionKey: key,
		keyConfigured: len(key) == 32,
		s3:            loadBackupS3Config(),
	}
}

// ---- ticker lifecycle -------------------------------------------------------

// startBackupTicker is the single call registered from main.go. It logs the
// resolved posture once at boot (so a deploy's logs state plainly whether offsite
// is armed) and, unless disabled, spawns a process-lifetime goroutine.
func startBackupTicker() {
	cfg := loadBackupConfig()
	if backupDisabled() {
		log.Infof("backup: disabled (BACKUP_DISABLED or BACKUP_INTERVAL_HOURS=0); data dir %s has NO snapshot", cfg.dataDir)
		return
	}

	switch {
	case cfg.s3 != nil && !cfg.keyConfigured:
		// Fail-closed posture is announced up front, not just at upload time.
		log.Warnf("backup: offsite S3 is configured but BACKUP_ENCRYPTION_KEY is unset — uploads will be SKIPPED (never ship the brain offsite unencrypted); set BACKUP_ENCRYPTION_KEY to arm offsite")
	case cfg.s3 != nil:
		style := "virtual-host"
		if cfg.s3.pathStyle {
			style = "path"
		}
		log.Infof("backup: offsite armed (encrypted) → bucket %q on %s (%s style), every %dh, ring keep %d", cfg.s3.bucket, cfg.s3.endpoint, style, cfg.intervalHours, cfg.ringKeep)
	default:
		enc := "unencrypted"
		if cfg.keyConfigured {
			enc = "encrypted"
		}
		log.Infof("backup: offsite dormant (no BACKUP_S3_* set); local %s ring only under %s, every %dh, ring keep %d", enc, filepath.Join(cfg.dataDir, backupDirName), cfg.intervalHours, cfg.ringKeep)
	}

	go runBackupLoop()
}

// runBackupLoop stages the first snapshot after the boot delay, then ticks on the
// interval. Env is re-read each pass (loadBackupConfig inside runBackupOnce), so
// the interval used here is a snapshot; changing BACKUP_INTERVAL_HOURS takes
// effect after a restart, which is acceptable for a nightly job.
func runBackupLoop() {
	cfg := loadBackupConfig()
	timer := time.NewTimer(cfg.firstRunDelay)
	<-timer.C
	runBackupOnce(time.Now())

	interval := time.Duration(cfg.intervalHours) * time.Hour
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		runBackupOnce(time.Now())
	}
}

// ---- one snapshot -----------------------------------------------------------

// backupOutcome is the recorded result of one pass (also feeds the readiness
// snapshot and tests).
type backupOutcome struct {
	path       string
	sizeBytes  int64
	encrypted  bool
	ringKept   int
	offsite    string // "ok" | "skip_no_key" | "dormant" | "error"
	offsiteErr string
	duration   time.Duration
}

// runBackupOnce performs a full snapshot pass and records/logs the outcome. It
// never panics the process on failure — a backup error is logged loudly and the
// next tick retries.
func runBackupOnce(now time.Time) (backupOutcome, error) {
	start := time.Now()
	cfg := loadBackupConfig()
	out, err := createBackupSnapshot(cfg, now)
	out.duration = time.Since(start)
	if err != nil {
		recordBackupOutcome(now, out, err)
		log.Errorf("backup: snapshot FAILED after %s: %v", out.duration.Round(time.Millisecond), err)
		return out, err
	}
	recordBackupOutcome(now, out, nil)
	log.Infof("backup: wrote %s (%s, %s, %s) in %s; ring keeps %d; offsite %s",
		filepath.Base(out.path), humanBytes(out.sizeBytes), encLabel(out.encrypted), blobsLabel(cfg.includeBlobs), out.duration.Round(time.Millisecond), out.ringKept, out.offsite)
	return out, nil
}

// createBackupSnapshot is the pure(ish) core: tar.gz the data dir into the ring,
// optionally encrypt, upload offsite (fail-closed without a key), rotate. Broken
// out from runBackupOnce so tests can drive it against a temp data dir.
func createBackupSnapshot(cfg backupConfig, now time.Time) (backupOutcome, error) {
	out := backupOutcome{offsite: "dormant"}

	backupsDir := filepath.Join(cfg.dataDir, backupDirName)
	if err := os.MkdirAll(backupsDir, 0o700); err != nil {
		return out, fmt.Errorf("create backups dir: %w", err)
	}

	// 1. Stream tar.gz to a temp file (low memory even with blobs on).
	tmpTar, err := os.CreateTemp(backupsDir, ".tmp-tar-*")
	if err != nil {
		return out, fmt.Errorf("temp tar: %w", err)
	}
	tmpTarPath := tmpTar.Name()
	defer os.Remove(tmpTarPath) // no-op after a successful rename
	if writeErr := writeDataDirTarGz(tmpTar, cfg.dataDir, cfg.includeBlobs); writeErr != nil {
		tmpTar.Close()
		return out, fmt.Errorf("write tar: %w", writeErr)
	}
	if closeErr := tmpTar.Close(); closeErr != nil {
		return out, fmt.Errorf("close tar: %w", closeErr)
	}

	stamp := now.UTC().Format(backupTimestampLayout)
	finalName := backupFilePrefix + stamp + ".tgz"
	out.encrypted = cfg.keyConfigured

	// 2. Encrypt (or not) into the final ring file via a temp+rename so a partial
	// write is never observed as a valid snapshot.
	if cfg.keyConfigured {
		finalName += ".enc"
		plain, readErr := os.ReadFile(tmpTarPath)
		if readErr != nil {
			return out, fmt.Errorf("read tar for encrypt: %w", readErr)
		}
		enc, encErr := encryptBackupBlob(cfg.encryptionKey, plain)
		if encErr != nil {
			return out, fmt.Errorf("encrypt: %w", encErr)
		}
		if err := writeFileAtomic(filepath.Join(backupsDir, finalName), enc, backupsDir); err != nil {
			return out, fmt.Errorf("write encrypted snapshot: %w", err)
		}
	} else {
		if err := os.Rename(tmpTarPath, filepath.Join(backupsDir, finalName)); err != nil {
			return out, fmt.Errorf("finalize snapshot: %w", err)
		}
	}

	finalPath := filepath.Join(backupsDir, finalName)
	out.path = finalPath
	if info, statErr := os.Stat(finalPath); statErr == nil {
		out.sizeBytes = info.Size()
	}

	// 3. Offsite. Fail closed: with S3 configured but no key, never upload the
	// unencrypted brain — take the local snapshot and loudly skip.
	if cfg.s3 != nil {
		if !cfg.keyConfigured {
			out.offsite = "skip_no_key"
			log.Warnf("backup: offsite upload SKIPPED — S3 is configured but BACKUP_ENCRYPTION_KEY is unset; refusing to ship %s unencrypted", finalName)
		} else {
			body, readErr := os.ReadFile(finalPath)
			if readErr != nil {
				out.offsite = "error"
				out.offsiteErr = readErr.Error()
				log.Errorf("backup: offsite read %s failed: %v", finalName, readErr)
			} else if upErr := uploadBackupToS3(cfg.s3, finalName, body, now); upErr != nil {
				out.offsite = "error"
				out.offsiteErr = upErr.Error()
				log.Errorf("backup: offsite upload of %s failed: %v", finalName, upErr)
			} else {
				out.offsite = "ok"
			}
		}
	}

	// 4. Rotate the local ring (best-effort: a rotation failure must not fail an
	// otherwise-good snapshot).
	kept, rotErr := rotateBackupRing(backupsDir, cfg.ringKeep)
	if rotErr != nil {
		log.Warnf("backup: ring rotation error: %v", rotErr)
	}
	out.ringKept = kept

	return out, nil
}

// writeDataDirTarGz walks dataDir and writes a gzip-compressed tar of every
// regular file to w, with paths relative to dataDir. The backups/ subdir is
// always excluded (a snapshot never contains prior snapshots — that would make
// the ring grow without bound); blobs/ is excluded unless includeBlobs. Each
// file is read whole so its declared tar size always matches its bytes, which
// keeps a concurrent append from corrupting the archive.
func writeDataDirTarGz(w io.Writer, dataDir string, includeBlobs bool) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	walkErr := filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// A file that vanished between listing and visiting (e.g. a temp being
			// renamed) is skipped, not fatal.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, relErr := filepath.Rel(dataDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		top := rel
		if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
			top = rel[:i]
		}
		if d.IsDir() {
			if top == backupDirName {
				return filepath.SkipDir
			}
			if top == "blobs" && !includeBlobs {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip our own in-flight temp files and anything that isn't a regular file
		// (sockets, devices, dangling symlinks).
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return nil
			}
			return readErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			// Same vanished-file race as the ReadFile above: d.Info() lazily lstats,
			// so a compaction temp (e.g. .meeting-memory-*.jsonl) renamed away between
			// the read and here yields IsNotExist. info feeds only cosmetic Mode/ModTime
			// header fields for bytes already read, so skip the file rather than failing
			// the whole nightly snapshot.
			if os.IsNotExist(infoErr) {
				return nil
			}
			return infoErr
		}
		hdr := &tar.Header{
			Name:    filepath.ToSlash(rel),
			Mode:    int64(info.Mode().Perm()),
			Size:    int64(len(data)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		return nil
	})
	if walkErr != nil {
		tw.Close()
		gz.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}

// ---- encryption -------------------------------------------------------------

// encryptBackupBlob seals plaintext with AES-256-GCM. Layout: magic(8) |
// nonce(12) | ciphertext+tag. The magic is bound as GCM AAD so a wrong-key or
// foreign file fails authentication.
func encryptBackupBlob(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	magic := []byte(backupEncMagic)
	out := make([]byte, 0, len(magic)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, magic...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, magic)
	return out, nil
}

// decryptBackupBlob reverses encryptBackupBlob. Exported semantics: used by the
// round-trip test and documented (as a Go snippet) in the restore procedure.
func decryptBackupBlob(key, blob []byte) ([]byte, error) {
	magic := []byte(backupEncMagic)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < len(magic)+ns {
		return nil, fmt.Errorf("backup blob too short")
	}
	if !bytes.Equal(blob[:len(magic)], magic) {
		return nil, fmt.Errorf("backup blob has wrong magic (not a %s file)", backupEncMagic)
	}
	nonce := blob[len(magic) : len(magic)+ns]
	ct := blob[len(magic)+ns:]
	return gcm.Open(nil, nonce, ct, magic)
}

// ---- local ring -------------------------------------------------------------

// rotateBackupRing keeps the newest `keep` snapshot files under backupsDir and
// deletes the rest. Snapshot names embed a lexically-sortable UTC timestamp, so
// name order is chronological order. Returns the count retained.
func rotateBackupRing(backupsDir string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, backupFilePrefix) && (strings.HasSuffix(n, ".tgz") || strings.HasSuffix(n, ".tgz.enc")) {
			names = append(names, n)
		}
	}
	sort.Strings(names) // oldest → newest
	if len(names) <= keep {
		return len(names), nil
	}
	toDelete := names[:len(names)-keep]
	var firstErr error
	for _, n := range toDelete {
		if err := os.Remove(filepath.Join(backupsDir, n)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return keep, firstErr
}

// ---- offsite (S3 SigV4, pure stdlib) ---------------------------------------

// uploadBackupToS3 PUTs body to the configured bucket under prefix+name.
func uploadBackupToS3(cfg *backupS3Config, name string, body []byte, now time.Time) error {
	objectKey := s3ObjectKey(cfg, name)
	req, err := signAndBuildS3PutRequest(cfg, objectKey, body, now)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("s3 PUT %s: status %d: %s", objectKey, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

func s3ObjectKey(cfg *backupS3Config, name string) string {
	prefix := strings.Trim(cfg.prefix, "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// signAndBuildS3PutRequest constructs a fully SigV4-signed PUT request. Factored
// out (rather than inlined in uploadBackupToS3) so a test can assert the signed
// request against an httptest verifier without a network round-trip.
func signAndBuildS3PutRequest(cfg *backupS3Config, objectKey string, body []byte, now time.Time) (*http.Request, error) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := hex.EncodeToString(sha256Sum(body))

	scheme := cfg.scheme
	if scheme == "" {
		scheme = "https"
	}
	var host, canonicalURI string
	if cfg.pathStyle {
		host = cfg.endpoint
		canonicalURI = "/" + cfg.bucket + "/" + s3URIEncodePath(objectKey)
	} else {
		host = cfg.bucket + "." + cfg.endpoint
		canonicalURI = "/" + s3URIEncodePath(objectKey)
	}
	rawURL := scheme + "://" + host + canonicalURI

	req, err := http.NewRequest(http.MethodPut, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Host = host
	req.ContentLength = int64(len(body))
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Content-Type", "application/octet-stream")

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		canonicalURI,
		"", // no query
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + cfg.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(sha256Sum([]byte(canonicalRequest))),
	}, "\n")

	signingKey := s3SigningKey(cfg.secretKey, dateStamp, cfg.region)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 "+
		"Credential="+cfg.accessKey+"/"+scope+", "+
		"SignedHeaders="+signedHeaders+", "+
		"Signature="+signature)
	return req, nil
}

// s3SigningKey derives the SigV4 signing key: HMAC chain over
// ("AWS4"+secret) → date → region → "s3" → "aws4_request".
func s3SigningKey(secret, dateStamp, region string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	return hmacSHA256(kService, []byte("aws4_request"))
}

// s3URIEncodePath encodes each path segment per RFC 3986 (unreserved chars kept)
// while preserving '/' as the segment separator, as S3 canonicalization requires.
func s3URIEncodePath(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = s3URIEncodeSegment(s)
	}
	return strings.Join(segs, "/")
}

func s3URIEncodeSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// ---- atomic write, formatting, stats ---------------------------------------

// writeFileAtomic writes data to a temp file in dir then renames onto path, so a
// reader/backup process never sees a half-written snapshot.
func writeFileAtomic(path string, data []byte, dir string) error {
	tmp, err := os.CreateTemp(dir, ".tmp-enc-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func encLabel(encrypted bool) string {
	if encrypted {
		return "encrypted"
	}
	return "unencrypted"
}

func blobsLabel(includeBlobs bool) string {
	if includeBlobs {
		return "with-blobs"
	}
	return "no-blobs"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

var (
	backupStatMu      sync.Mutex
	backupLastRunAt   time.Time
	backupLastOK      bool
	backupLastSize    int64
	backupLastErr     string
	backupLastOffsite string
	backupOffsiteErr  string
	backupRingCount   int
	backupRestoreAt   time.Time
	backupRestoreErr  string
)

func recordBackupOutcome(now time.Time, out backupOutcome, err error) {
	backupStatMu.Lock()
	defer backupStatMu.Unlock()
	backupLastRunAt = now
	backupLastOK = err == nil
	backupLastSize = out.sizeBytes
	backupLastOffsite = out.offsite
	backupOffsiteErr = out.offsiteErr
	backupRingCount = out.ringKept
	if err != nil {
		backupLastErr = err.Error()
	} else {
		backupLastErr = ""
	}
}

// recordBackupRestoreVerification is the producer seam for a real restore
// drill. Snapshot creation never calls it: copying bytes is not proof that the
// archive can be restored.
func recordBackupRestoreVerification(at time.Time, err error) {
	backupStatMu.Lock()
	defer backupStatMu.Unlock()
	backupRestoreAt = at
	if err != nil {
		backupRestoreErr = err.Error()
	} else {
		backupRestoreErr = ""
	}
}

// readinessBackupSnapshot is ready to be surfaced on /healthz or /readyz. It is
// intentionally NOT wired into the payload here: doing so needs a second edit to
// main.go's readiness map, and this wave owns exactly one main.go add (the ticker
// registration). Adding `"backup": readinessBackupSnapshot()` to the readiness
// agents map in a follow-up is a one-liner. Until then, ops visibility comes from
// the boot-time posture line and the per-snapshot log line.
func readinessBackupSnapshot() map[string]any {
	backupStatMu.Lock()
	defer backupStatMu.Unlock()
	snap := map[string]any{
		"enabled":  !backupDisabled(),
		"interval": fmt.Sprintf("%dh", backupIntervalHours()),
	}
	if !backupLastRunAt.IsZero() {
		snap["lastBackupAt"] = backupLastRunAt.UTC().Format(time.RFC3339Nano)
		snap["lastOK"] = backupLastOK
		snap["lastSizeBytes"] = backupLastSize
		snap["offsite"] = backupLastOffsite
		snap["ringCount"] = backupRingCount
		if backupLastErr != "" {
			snap["lastError"] = backupLastErr
		}
	}
	return snap
}

// backupCapabilitySnapshot distinguishes a successful local ring write from
// actual disaster recovery. Local-only, dormant offsite, failed offsite, and an
// unproven restore are never reported as healthy recovery.
func backupCapabilitySnapshot(now time.Time) map[string]any {
	cfg := loadBackupConfig()
	backupStatMu.Lock()
	lastRunAt := backupLastRunAt
	lastOK := backupLastOK
	lastSize := backupLastSize
	lastErr := backupLastErr
	lastOffsite := backupLastOffsite
	offsiteErr := backupOffsiteErr
	ringCount := backupRingCount
	restoreAt := backupRestoreAt
	restoreErr := backupRestoreErr
	backupStatMu.Unlock()

	snap := map[string]any{
		"enabled":           !backupDisabled(),
		"interval":          fmt.Sprintf("%dh", cfg.intervalHours),
		"localConfigured":   true,
		"offsiteConfigured": cfg.s3 != nil,
		"encrypted":         cfg.keyConfigured,
		"offsite":           "dormant",
		"restoreVerified":   !restoreAt.IsZero() && restoreErr == "",
		"status":            "degraded",
	}
	if backupDisabled() {
		snap["status"] = "disabled"
		return snap
	}
	if !lastRunAt.IsZero() {
		age := now.Sub(lastRunAt)
		if age < 0 {
			age = 0
		}
		snap["lastBackupAt"] = lastRunAt.UTC().Format(time.RFC3339Nano)
		snap["lagSeconds"] = int64(age.Seconds())
		snap["stale"] = age > time.Duration(max(1, cfg.intervalHours))*2*time.Hour
		snap["localLastOK"] = lastOK
		snap["lastSizeBytes"] = lastSize
		snap["ringCount"] = ringCount
		if lastOffsite != "" {
			snap["offsite"] = lastOffsite
		}
		if offsiteErr != "" {
			snap["offsiteError"] = offsiteErr
		}
		if lastErr != "" {
			snap["lastError"] = lastErr
		}
	}
	if !restoreAt.IsZero() {
		snap["lastRestoreAt"] = restoreAt.UTC().Format(time.RFC3339Nano)
		if restoreErr != "" {
			snap["restoreError"] = restoreErr
		}
	}
	// Healthy DR requires all independent facts: current encrypted offsite
	// configuration, a fresh local snapshot, successful replication, and a
	// restore drill recorded through recordBackupRestoreVerification.
	if snap["offsiteConfigured"] == true && snap["encrypted"] == true && snap["localLastOK"] == true && snap["stale"] == false && snap["offsite"] == "ok" && snap["restoreVerified"] == true {
		snap["status"] = "healthy"
	}
	return snap
}
