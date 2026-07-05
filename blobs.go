package main

// Content-addressed blob store (packaging OS §4 data model, Wave 3 item 13) —
// the storage floor under first-class artifacts. Binary payloads (flattened
// PDF decks, page rasters, exports) do NOT belong in data/meeting-memory.jsonl
// (one line per entry, whole file rewritten per metadata update, 256KB PATCH
// cap); they live here as immutable files keyed by their own sha256, and
// artifacts reference them by ref through a small `assets` metadata JSON.
// Killing the inline-body ceiling is the point: JSONL stores refs only.
//
// LAYOUT: data/blobs/<sha256[0:2]>/<sha256> beside a `<sha256>.meta` sidecar
// JSON {mime, size, createdAt}. Sharding by the first two hex chars keeps any
// single directory small; the store root rides the meeting-memory directory
// (the users.json/sessions.json/codex-runner-jobs precedent) so isolated
// tests and the VPS deploy both place it automatically.
//
// IMMUTABILITY CONTRACT: a ref's bytes can never change (the ref IS the
// digest), so putBlob dedupes by existence, the FIRST write pins the mime,
// and the serving route may set ETag=ref + cache forever. getBlob re-verifies
// the digest on read: a corrupted file is an error, never wrong bytes.
//
// GC is exposed as sweepUnreferencedBlobs for a FUTURE admin action only —
// deliberately NOT wired to any timer or ambient agent (spec: blobs
// referenced by no artifact are eligible, and a human triggers it).
//
// KEYLESS: pure disk, no model calls, no sidecar — nothing here degrades.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// blobMaxBytes caps a single blob at 64MB. Flattened PDF decks land ~5MB
	// (spec item 14b), so the cap is generous headroom while still bounding a
	// runaway export or hostile payload.
	blobMaxBytes = 64 << 20

	blobMetaSuffix = ".meta"

	// blobDefaultMime is served when a sidecar is missing or unreadable —
	// the octet-stream fallback never lets the browser sniff (the route sets
	// nosniff and an attachment disposition for non-inline-safe types).
	blobDefaultMime = "application/octet-stream"

	// blobCacheControl leans on content addressing: the bytes for a ref can
	// never change, so the session-gated response caches privately forever.
	blobCacheControl = "private, max-age=31536000, immutable"

	// artifactAssetsMetadataKey is the artifact metadata key holding the
	// assets JSON array ([{ref, mime, name, kind}]). The broader artifact
	// metadata schema is owned by the artifact model (memory_query.go
	// conventions: flat string map, trimmed keys/values); this key follows
	// the workflowStages precedent of structured JSON inside one value.
	artifactAssetsMetadataKey = "assets"
)

// artifactAssetKinds is the closed vocabulary for artifactAsset.Kind
// (spec §4: pdf | image | export).
var artifactAssetKinds = map[string]bool{
	"pdf":    true,
	"image":  true,
	"export": true,
}

// blobMeta is the sidecar JSON written beside each blob file.
type blobMeta struct {
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// artifactAsset is one content-addressed attachment on an os_artifact:
// the blob ref plus the display facts the viewer needs without a disk read.
type artifactAsset struct {
	Ref  string `json:"ref"`
	Mime string `json:"mime,omitempty"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"` // pdf | image | export
}

func blobStoreDir() string {
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "blobs")
}

// init wires the artifact-version body seam memory.go declares: every body
// edit journals the SUPERSEDED body here so the version lineage carries
// recoverable content, not just counters. Plain local disk writes only —
// bumpArtifactVersionLocked calls this while holding store.mu, and putBlob is
// lock-free. An empty prior body (putBlob rejects empty payloads) journals
// without a ref, exactly like the pre-wiring degraded path.
func init() {
	artifactVersionBlobStore = func(artifactID string, version int, body string) (string, error) {
		if len(body) == 0 {
			return "", nil
		}
		return putBlob([]byte(body), "text/markdown; charset=utf-8")
	}
}

// validBlobRef accepts exactly a lowercase hex sha256 — 64 chars of
// [0-9a-f]. Everything else (including path traversal attempts) is rejected
// before any filesystem path is built from it.
func validBlobRef(ref string) bool {
	if len(ref) != sha256.Size*2 {
		return false
	}
	for _, char := range ref {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// blobPaths maps a validated ref to its data + meta file paths inside the
// two-hex-char shard directory.
func blobPaths(ref string) (string, string, error) {
	if !validBlobRef(ref) {
		return "", "", fmt.Errorf("invalid blob ref")
	}
	dir := filepath.Join(blobStoreDir(), ref[:2])
	return filepath.Join(dir, ref), filepath.Join(dir, ref+blobMetaSuffix), nil
}

// putBlob stores data under its sha256 and returns the ref. Same bytes always
// yield the same ref (dedupe by existence — an already-stored blob is NOT
// rewritten, and its sidecar keeps the mime the first write pinned). The data
// file lands via temp-file + rename so a crash can never leave half-written
// bytes addressable by their ref.
func putBlob(data []byte, mime string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("blob is empty")
	}
	if len(data) > blobMaxBytes {
		return "", fmt.Errorf("blob exceeds the %dMB cap", blobMaxBytes>>20)
	}
	if mime = strings.TrimSpace(mime); mime == "" {
		mime = blobDefaultMime
	}

	digest := sha256.Sum256(data)
	ref := hex.EncodeToString(digest[:])
	dataPath, metaPath, err := blobPaths(ref)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dataPath); err == nil {
		return ref, nil
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		return "", fmt.Errorf("create blob shard directory: %w", err)
	}

	// Sidecar first, data rename last: an addressable blob always has its
	// meta; a crash in between leaves only an orphan sidecar that the next
	// put of the same bytes overwrites.
	metaJSON, err := json.Marshal(blobMeta{
		Mime:      mime,
		Size:      int64(len(data)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", fmt.Errorf("encode blob meta: %w", err)
	}
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		return "", fmt.Errorf("write blob meta: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(dataPath), "."+ref[:8]+".put-*")
	if err != nil {
		return "", fmt.Errorf("create blob temp file: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return "", fmt.Errorf("write blob: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("close blob temp file: %w", err)
	}
	if err := os.Rename(tempPath, dataPath); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("finalize blob: %w", err)
	}

	return ref, nil
}

// getBlob returns the bytes and sidecar meta for a ref. The digest is
// re-verified on every read — the content-addressed contract means a
// corrupted file is an error, never silently-wrong bytes. A missing or
// unreadable sidecar degrades to the octet-stream default instead of failing
// the read.
func getBlob(ref string) ([]byte, blobMeta, error) {
	ref = strings.TrimSpace(ref)
	dataPath, metaPath, err := blobPaths(ref)
	if err != nil {
		return nil, blobMeta{}, err
	}

	data, err := os.ReadFile(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, blobMeta{}, fmt.Errorf("blob not found")
		}
		return nil, blobMeta{}, fmt.Errorf("read blob: %w", err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ref {
		return nil, blobMeta{}, fmt.Errorf("blob %s failed content verification", ref)
	}

	meta := blobMeta{Mime: blobDefaultMime}
	if rawMeta, err := os.ReadFile(metaPath); err == nil {
		var parsed blobMeta
		if err := json.Unmarshal(rawMeta, &parsed); err == nil && strings.TrimSpace(parsed.Mime) != "" {
			meta = parsed
		}
	}
	meta.Size = int64(len(data))

	return data, meta, nil
}

// artifactAssets decodes the artifact's assets metadata JSON. Malformed JSON
// degrades to no assets (log-and-continue, the store's malformed-entry
// posture); entries without a valid ref are dropped so downstream code never
// builds a path from garbage.
func artifactAssets(entry meetingMemoryEntry) []artifactAsset {
	raw := strings.TrimSpace(entry.Metadata[artifactAssetsMetadataKey])
	if raw == "" {
		return nil
	}
	var decoded []artifactAsset
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		log.Warnf("Skipping malformed assets metadata on artifact %s: %v", entry.ID, err)
		return nil
	}
	assets := decoded[:0]
	for _, asset := range decoded {
		asset.Ref = strings.TrimSpace(asset.Ref)
		if !validBlobRef(asset.Ref) {
			continue
		}
		assets = append(assets, asset)
	}
	if len(assets) == 0 {
		return nil
	}
	return assets
}

// appendArtifactAsset attaches one blob ref to an artifact's assets metadata.
// Re-attaching an existing ref updates that entry in place (an idempotent
// re-export never stacks duplicates). The write goes through the
// metadata-only stamp path (updateOSArtifactMetadata, the openedAt
// precedent) so a concurrent body update is never clobbered and no artifact
// event fans out for pure bookkeeping.
func (app *kanbanBoardApp) appendArtifactAsset(artifactID string, asset artifactAsset) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, fmt.Errorf("artifact memory is unavailable")
	}
	asset.Ref = strings.TrimSpace(asset.Ref)
	if !validBlobRef(asset.Ref) {
		return meetingMemoryEntry{}, fmt.Errorf("invalid blob ref")
	}
	asset.Mime = strings.TrimSpace(asset.Mime)
	asset.Name = strings.TrimSpace(asset.Name)
	asset.Kind = strings.ToLower(strings.TrimSpace(asset.Kind))
	if asset.Kind != "" && !artifactAssetKinds[asset.Kind] {
		return meetingMemoryEntry{}, fmt.Errorf("asset kind must be pdf, image, or export")
	}

	artifact, found := app.osArtifactByID(artifactID)
	if !found {
		return meetingMemoryEntry{}, fmt.Errorf("artifact not found")
	}

	assets := artifactAssets(artifact)
	replaced := false
	for index, existing := range assets {
		if existing.Ref == asset.Ref {
			assets[index] = asset
			replaced = true
			break
		}
	}
	if !replaced {
		assets = append(assets, asset)
	}

	encoded, err := json.Marshal(assets)
	if err != nil {
		return meetingMemoryEntry{}, fmt.Errorf("encode artifact assets: %w", err)
	}
	entry, _, err := app.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{
		artifactAssetsMetadataKey: string(encoded),
	})
	return entry, err
}

// sweepUnreferencedBlobs deletes every stored blob whose ref appears in no
// artifact's assets metadata, returning the deleted refs. Exposed for a
// FUTURE admin action only — deliberately NOT wired to a timer or ambient
// agent. It refuses to run without a live artifact store: sweeping blind
// would treat every blob as an orphan.
func sweepUnreferencedBlobs(app *kanbanBoardApp) ([]string, error) {
	if app == nil || app.memory == nil {
		return nil, fmt.Errorf("artifact memory is unavailable")
	}

	referenced := map[string]struct{}{}
	for _, artifact := range app.osArtifactsSnapshot(0) {
		for _, asset := range artifactAssets(artifact) {
			referenced[asset.Ref] = struct{}{}
		}
		// Version-lineage body snapshots (memory.go's artifactVersions journal)
		// are referenced too — the sweep must never orphan an edit history.
		for _, version := range artifactVersionHistory(artifact) {
			if ref := strings.TrimSpace(version.BodyBlobRef); validBlobRef(ref) {
				referenced[ref] = struct{}{}
			}
		}
	}

	shards, err := os.ReadDir(blobStoreDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read blob store: %w", err)
	}

	var deleted []string
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		shardDir := filepath.Join(blobStoreDir(), shard.Name())
		files, err := os.ReadDir(shardDir)
		if err != nil {
			return deleted, fmt.Errorf("read blob shard %s: %w", shard.Name(), err)
		}
		for _, file := range files {
			ref := file.Name()
			if strings.HasSuffix(ref, blobMetaSuffix) || !validBlobRef(ref) {
				continue
			}
			if _, ok := referenced[ref]; ok {
				continue
			}
			if err := os.Remove(filepath.Join(shardDir, ref)); err != nil {
				return deleted, fmt.Errorf("delete blob %s: %w", ref, err)
			}
			// Best-effort sidecar cleanup: an orphan .meta blocks nothing.
			_ = os.Remove(filepath.Join(shardDir, ref+blobMetaSuffix))
			deleted = append(deleted, ref)
		}
	}

	return deleted, nil
}

// blobInlineSafeMimes are the types the blob route may serve with an inline
// disposition (browser-native PDF embed + plain images, spec §4 viewer item
// 2). Script-capable types (text/html, image/svg+xml) are deliberately
// EXCLUDED: this route carries session-cookie authority on the app origin, so
// anything that can execute must go through the sandboxed render route or
// download as an attachment.
var blobInlineSafeMimes = map[string]bool{
	"application/pdf": true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/webp":      true,
}

// blobDownloadFilename sanitizes the caller-supplied name down to a bare base
// name with no control characters; empty or degenerate names fall back to the
// ref itself.
func blobDownloadFilename(name string, ref string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f || char == '"' || char == '\\' {
			return -1
		}
		return char
	}, name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return ref
	}
	return name
}

// artifactBlobHandler serves GET /artifacts/blob?ref=...&name=... — the
// generic download/embed route for artifact assets (spec §4, Wave 3 item 13).
// Session-gated exactly like its /artifacts neighbors (origin check,
// signed-in user); the content-addressed contract makes the ETag the ref
// itself and the response immutable-cacheable, so a re-open of a 5MB deck
// export costs one conditional request.
func artifactBlobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if !validBlobRef(ref) {
		writeAuthError(w, http.StatusBadRequest, "invalid blob ref")
		return
	}

	etag := `"` + ref + `"`
	if requestETagMatches(r.Header.Get("If-None-Match"), etag) {
		// Immutable content: a matching validator answers before any disk I/O.
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", blobCacheControl)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	data, meta, err := getBlob(ref)
	if err != nil {
		writeAuthError(w, http.StatusNotFound, "blob not found")
		return
	}

	disposition := "attachment"
	if blobInlineSafeMimes[meta.Mime] {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", meta.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", blobCacheControl)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, blobDownloadFilename(r.URL.Query().Get("name"), ref)))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if _, err := w.Write(data); err != nil {
		log.Errorf("Failed to serve blob %s: %v", ref, err)
	}
}

// requestETagMatches reports whether an If-None-Match header names the etag,
// honoring comma-separated lists, weak validators, and the * wildcard.
func requestETagMatches(header string, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}
