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
// GC (Wave 5 D10): sweepBlobStore computes the referenced set from every
// live or trashed file row, artifact asset, version body, chat attachment,
// pending upload, and live file share link, then deletes the rest. Two
// callers: the admin action POST /assistant/admin/blobs/sweep (dry-run or
// immediate, a human triggers it) and runScheduledBlobSweep, the weekly job
// that runs behind the Drive trash purge and deletes only blobs unreferenced
// across two consecutive weekly runs (dry-run first, delete on the second
// sighting).
//
// KEYLESS: pure disk, no model calls, no sidecar — nothing here degrades.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// artifactAssetKinds is the closed vocabulary for artifactAsset.Kind.
// page_image is deliberately distinct from editable/generated imagery: it is
// a flattened render used only for export QA and slide juries.
var artifactAssetKinds = map[string]bool{
	"pdf":        true,
	"image":      true,
	"page_image": true,
	"export":     true,
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
	Ref                   string `json:"ref"`
	Mime                  string `json:"mime,omitempty"`
	Name                  string `json:"name,omitempty"`
	Kind                  string `json:"kind,omitempty"` // pdf | image | page_image | export
	SourceArtifactVersion int    `json:"sourceArtifactVersion,omitempty"`
	SourceSceneRef        string `json:"sourceSceneRef,omitempty"`
}

// artifactAssetIsPageImage identifies flattened render output, including the
// narrow legacy shape emitted before page_image became its own kind. Rendered
// pages are review evidence — never editable source imagery.
func artifactAssetIsPageImage(asset artifactAsset) bool {
	kind := strings.ToLower(strings.TrimSpace(asset.Kind))
	if kind == "page_image" {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(asset.Name))
	return kind == "image" && strings.HasPrefix(name, "page-") &&
		(strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg"))
}

func artifactAssetIsEditableImage(asset artifactAsset) bool {
	if artifactAssetIsPageImage(asset) {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(asset.Kind))
	mime := strings.ToLower(strings.TrimSpace(asset.Mime))
	return (kind == "" || kind == "image") && strings.HasPrefix(mime, "image/")
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
	return putBlobWithCap(data, mime, blobMaxBytes)
}

// putBlobWithCap is putBlob with a caller-supplied size ceiling for lanes
// whose spec cap exceeds the 64MB default (Wave 7 meeting recordings,
// MEETING_RECORDING_MAX_BYTES). A non-positive cap falls back to
// blobMaxBytes; the cap only ever bounds THIS write — the store's
// content-addressed contract is unchanged.
func putBlobWithCap(data []byte, mime string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = blobMaxBytes
	}
	if len(data) == 0 {
		return "", fmt.Errorf("blob is empty")
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("blob exceeds the %dMB cap", maxBytes>>20)
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
	if existing, readErr := os.ReadFile(dataPath); readErr == nil {
		existingDigest := sha256.Sum256(existing)
		if hex.EncodeToString(existingDigest[:]) != ref || len(existing) != len(data) {
			// The caller supplied the bytes that hash to ref, so repairing a
			// corrupted content-addressed slot is deterministic.
			if err := writeFileAtomicallyForCanonicalMode(dataPath, data, 0o644); err != nil {
				return "", fmt.Errorf("repair corrupt blob: %w", err)
			}
		}
		if rawMeta, metaErr := os.ReadFile(metaPath); metaErr == nil {
			var pinned blobMeta
			if json.Unmarshal(rawMeta, &pinned) == nil && strings.TrimSpace(pinned.Mime) != "" && pinned.Size == int64(len(data)) {
				return ref, nil
			}
		}
		// Missing/malformed/inconsistent sidecars are repaired below. Until that
		// durable write lands, getBlob refuses to serve the untracked data.
	} else if !os.IsNotExist(readErr) {
		return "", fmt.Errorf("inspect existing blob: %w", readErr)
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		return "", fmt.Errorf("create blob shard directory: %w", err)
	}

	// Data first, sidecar last: importers enumerate sidecars, so a crash between
	// the two leaves only an unreferenced content-addressed data file (ignored by
	// canonical import and removable by the sweep), never a phantom canonical
	// blob whose bytes do not exist. Reads already tolerate a missing sidecar.
	metaJSON, err := json.Marshal(blobMeta{
		Mime:      mime,
		Size:      int64(len(data)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", fmt.Errorf("encode blob meta: %w", err)
	}
	if err := writeFileAtomicallyForCanonicalMode(dataPath, data, 0o644); err != nil {
		return "", fmt.Errorf("finalize blob: %w", err)
	}
	if err := writeFileAtomicallyForCanonicalMode(metaPath, metaJSON, 0o644); err != nil {
		return "", fmt.Errorf("write blob meta: %w", err)
	}

	return ref, nil
}

// getBlob returns the bytes and sidecar meta for a ref. The digest is
// re-verified on every read — the content-addressed contract means a
// corrupted file is an error, never silently-wrong bytes. The sidecar is the
// publication marker: missing or malformed metadata means the data is an
// incomplete/untracked put and is not retrievable until putBlob repairs it.
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

	rawMeta, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, blobMeta{}, fmt.Errorf("blob is not published: %w", err)
	}
	var meta blobMeta
	if err := json.Unmarshal(rawMeta, &meta); err != nil || strings.TrimSpace(meta.Mime) == "" || meta.Size != int64(len(data)) {
		return nil, blobMeta{}, fmt.Errorf("blob publication metadata is invalid")
	}

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
	asset.SourceSceneRef = strings.TrimSpace(asset.SourceSceneRef)
	if asset.Kind != "" && !artifactAssetKinds[asset.Kind] {
		return meetingMemoryEntry{}, fmt.Errorf("asset kind must be pdf, image, page_image, or export")
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

// replaceArtifactAssetsOfKind swaps ALL of an artifact's assets of one kind
// for the given fresh set in a SINGLE metadata write. This is the re-export
// seam: a fresh render's page images replace the previous export's pages
// (content-addressed refs mean an edited page would otherwise linger beside
// its replacement), and one write instead of one-per-asset keeps a long deck
// from rewriting the growing assets JSON quadratically. Assets of other kinds
// (the PDF, exports) are preserved untouched.
func (app *kanbanBoardApp) replaceArtifactAssetsOfKind(artifactID string, kind string, fresh []artifactAsset) (meetingMemoryEntry, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, fmt.Errorf("artifact memory is unavailable")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !artifactAssetKinds[kind] {
		return meetingMemoryEntry{}, fmt.Errorf("asset kind must be pdf, image, page_image, or export")
	}
	normalized := make([]artifactAsset, 0, len(fresh))
	for _, asset := range fresh {
		asset.Ref = strings.TrimSpace(asset.Ref)
		if !validBlobRef(asset.Ref) {
			return meetingMemoryEntry{}, fmt.Errorf("invalid blob ref")
		}
		asset.Mime = strings.TrimSpace(asset.Mime)
		asset.Name = strings.TrimSpace(asset.Name)
		asset.Kind = strings.ToLower(strings.TrimSpace(asset.Kind))
		asset.SourceSceneRef = strings.TrimSpace(asset.SourceSceneRef)
		if asset.Kind != kind {
			return meetingMemoryEntry{}, fmt.Errorf("asset kind %q does not match the replaced kind %q", asset.Kind, kind)
		}
		normalized = append(normalized, asset)
	}

	artifact, found := app.osArtifactByID(artifactID)
	if !found {
		return meetingMemoryEntry{}, fmt.Errorf("artifact not found")
	}
	existing := artifactAssets(artifact)
	assets := make([]artifactAsset, 0, len(existing)+len(normalized))
	for _, asset := range existing {
		if (kind == "page_image" && !artifactAssetIsPageImage(asset)) || (kind != "page_image" && asset.Kind != kind) {
			assets = append(assets, asset)
		}
	}
	assets = append(assets, normalized...)

	encoded, err := json.Marshal(assets)
	if err != nil {
		return meetingMemoryEntry{}, fmt.Errorf("encode artifact assets: %w", err)
	}
	entry, _, err := app.memory.updateOSArtifactMetadata(artifact.ID, map[string]string{
		artifactAssetsMetadataKey: string(encoded),
	})
	return entry, err
}

// blobReferenceWalkers are additional "these refs are still in use" sources
// registered by other files (registerBlobReferenceWalker from an init), so a
// new blob-storing lane keeps its bytes alive without editing this file.
var (
	blobReferenceWalkersMu sync.Mutex
	blobReferenceWalkers   []func(app *kanbanBoardApp) []string
)

// registerBlobReferenceWalker adds a reference source to every future sweep.
// The walker must tolerate a nil app and return only refs it can vouch for.
func registerBlobReferenceWalker(walker func(app *kanbanBoardApp) []string) {
	if walker == nil {
		return
	}
	blobReferenceWalkersMu.Lock()
	blobReferenceWalkers = append(blobReferenceWalkers, walker)
	blobReferenceWalkersMu.Unlock()
}

// blobReferencedRefs collects every blob ref the workspace still points at.
// It refuses to run without a live store: sweeping blind would treat every
// blob as an orphan. Trashed Drive rows count as references until the trash
// purge removes the row itself — the sweep never races the restore window.
func blobReferencedRefs(app *kanbanBoardApp) (map[string]struct{}, error) {
	return blobReferencedRefsWithChat(app, true)
}

// blobReferencedRefsWithChat is the walk with the chat lane switchable: the
// chat media retention pass (chat_media_retention.go) asks for everything
// EXCEPT chat attachments to learn which bodies are permanent. An attachment
// already marked expired never counts as a reference in either mode, so a
// body whose direct delete failed is still reclaimed by the weekly GC.
func blobReferencedRefsWithChat(app *kanbanBoardApp, includeChat bool) (map[string]struct{}, error) {
	if app == nil || app.memory == nil {
		return nil, fmt.Errorf("artifact memory is unavailable")
	}

	referenced := map[string]struct{}{}
	keep := func(ref string) {
		if ref = strings.TrimSpace(ref); validBlobRef(ref) {
			referenced[ref] = struct{}{}
		}
	}
	// Raw entriesOfKind, not osArtifactsSnapshot: a quarantined/expired
	// artifact is hidden from recall but can still be restored, so its bytes
	// must outlive the hide. rowsClaimingRefs feeds the fail-safe below: it
	// counts only rows that actually CLAIM bytes — a non-empty value under a
	// ref-bearing field — not every artifact/file row. Counting every row made
	// the fail-safe fire on any workspace whose blob bytes all live in chat
	// (the chat-excluded walk chatMediaPermanentRefs asks for produces an
	// empty set there by design), which refused the sweep every day and
	// disabled chat media retention outright. Claim-counting catches the
	// decode-regression case the artifact lane can hit — assets/versions
	// metadata present but no longer decoding — while the Drive lane below
	// counts key-agnostically so a renamed metadata key still trips it.
	rowsClaimingRefs := 0
	for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		for _, key := range []string{artifactAssetsMetadataKey, artifactVersionsMetadataKey, deckSceneRefMetadataKey, renderSourceSceneRefMetadataKey, renderPDFAssetRefMetadataKey} {
			if strings.TrimSpace(artifact.Metadata[key]) != "" {
				rowsClaimingRefs++
				break
			}
		}
		for _, asset := range artifactAssets(artifact) {
			keep(asset.Ref)
			keep(asset.SourceSceneRef)
		}
		// Metadata-held refs: the Deck Studio scene (deckSceneRef) is the ONLY
		// copy of a native deck's editable scene — loadDeckDocument 409s
		// without it — and the render lane pins the scene/PDF it rendered
		// from. None of these are artifact assets, so they are walked here.
		for _, key := range []string{deckSceneRefMetadataKey, renderSourceSceneRefMetadataKey, renderPDFAssetRefMetadataKey} {
			keep(artifact.Metadata[key])
		}
		// Version-lineage body snapshots (memory.go's artifactVersions journal)
		// and each superseded revision's scene are referenced too — the sweep
		// must never orphan an edit history.
		for _, version := range artifactVersionHistory(artifact) {
			keep(version.BodyBlobRef)
			keep(version.SceneRef)
		}
	}
	// Files-surface direct uploads (kind=file entries, card 095) reference
	// their bytes via metadata blobRef — the shared drive must survive a sweep.
	// Every row counts, live or trashed: a purge deletes the row first, and
	// only then does the next weekly pass see the blob as unreferenced.
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		// Claim detection here is KEY-AGNOSTIC: any metadata value shaped like
		// a blob ref counts, not only the value under "blobRef". A Drive row's
		// metadata schema is small and known (files.go: name/mime/size/emails/
		// origin/visibility, plus prefixed "sha256:" revisions that are not
		// blob refs), so a bare sha in it means this row claims bytes. That is
		// what keeps the fail-safe below able to catch a renamed metadata key:
		// counting only entry.Metadata["blobRef"] reads the very key the walk
		// reads, so a rename without a data migration would zero BOTH sides
		// and let the sweep call every blob in the store an orphan. Claims are
		// counted, never kept — turning an unknown key into a reference would
		// destroy the walker-gap detection the mention audit exists for.
		for _, value := range entry.Metadata {
			if validBlobRef(strings.TrimSpace(value)) {
				rowsClaimingRefs++
				break
			}
		}
		keep(entry.Metadata["blobRef"])
	}
	// Chat-attachment refs (scoutChatFileAttachment.Ref, card 085) are live
	// references too: a thread's inline images and PDFs must survive a sweep
	// for as long as the thread record renders them.
	// The guard sits ABOVE the snapshot: snapshot(0) clones every entry body in
	// the store (years of chat), and the chat-EXCLUDED walk — one per daily
	// retention pass — used to materialise that whole copy only to break on the
	// first entry.
	chatEntries := []meetingMemoryEntry(nil)
	if includeChat {
		chatEntries = app.memory.snapshot(0)
	}
	for _, entry := range chatEntries {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			continue
		}
		for _, message := range thread.Messages {
			for _, file := range message.Files {
				if file.Expired {
					continue
				}
				if ref := strings.TrimSpace(file.Ref); validBlobRef(ref) {
					referenced[ref] = struct{}{}
				}
			}
			if message.Image != nil && !message.Image.Expired {
				if ref := strings.TrimSpace(message.Image.Ref); validBlobRef(ref) {
					referenced[ref] = struct{}{}
				}
			}
		}
	}
	// A newly uploaded composer source is intentionally not yet present in a
	// chat message. Keep pending/reserved bytes alive until commit, expiry, or
	// explicit revocation; a manual sweep must not race the authorization lane.
	app.pendingAttachmentUploadsMu.Lock()
	for _, source := range app.pendingAttachmentUploads {
		if (source.State == attachmentSourcePending || source.State == attachmentSourceReserved) && source.ExpiresAt.After(time.Now().UTC()) && validBlobRef(source.Ref) {
			referenced[source.Ref] = struct{}{}
		}
	}
	app.pendingAttachmentUploadsMu.Unlock()
	// Other lanes that store bytes here (Wave 7 meeting recordings) register
	// their reference walkers at init; a lane that has not registered yet
	// simply contributes nothing, so the sweep never depends on link order.
	blobReferenceWalkersMu.Lock()
	walkers := append([]func(*kanbanBoardApp) []string(nil), blobReferenceWalkers...)
	blobReferenceWalkersMu.Unlock()
	for _, walker := range walkers {
		for _, ref := range walker(app) {
			if ref = strings.TrimSpace(ref); validBlobRef(ref) {
				referenced[ref] = struct{}{}
			}
		}
	}
	// A live file share link (share_links.go, Wave 5 D3) binds its capability
	// to the exact blob it streams. The row behind it is already referenced
	// above; this keeps the bytes for the link's window even if that row
	// changes underneath it.
	if links, err := loadShareLinks(); err == nil {
		now := time.Now().UTC()
		for _, link := range links {
			if link.ObjectType == shareLinkObjectTypeFile && shareLinkLive(link, now) && validBlobRef(link.ContentDigest) {
				referenced[link.ContentDigest] = struct{}{}
			}
		}
	} else {
		return nil, fmt.Errorf("read share links for blob sweep: %w", err)
	}
	// Fail-safe: rows that CLAIM blob bytes exist but the walk produced no refs
	// at all. That is a broken walk (a Drive metadata key renamed without a
	// migration — the key-agnostic Drive count above still sees the sha the
	// walk's "blobRef" lookup no longer finds — or an artifact decoder
	// regression), not an empty workspace: refuse, so sweepBlobStore never
	// treats every blob as an orphan. An error here skips deletion on both the
	// admin and weekly paths. A workspace whose only bytes are chat media
	// claims nothing here, so the chat-excluded walk returns an honest empty
	// "nothing outside chat is permanent" set instead of refusing.
	if rowsClaimingRefs > 0 && len(referenced) == 0 {
		return nil, fmt.Errorf("blob reference walk found %d artifact/file row(s) claiming blob refs but collected none; refusing to sweep", rowsClaimingRefs)
	}
	return referenced, nil
}

// blobSweepReport is the outcome of one sweep pass — the admin action's wire
// shape and the weekly job's log line.
type blobSweepReport struct {
	DryRun       bool  `json:"dryRun"`
	Scanned      int   `json:"scanned"`
	Unreferenced int   `json:"unreferenced"`
	Deleted      int   `json:"deleted"`
	DeletedBytes int64 `json:"deletedBytes"`
	// UnreferencedRefs / DeletedRefs are the per-ref detail behind the counts
	// (never on the wire — a ref list is not something an admin needs to see,
	// and the weekly job persists its own candidate set).
	UnreferencedRefs []string `json:"-"`
	DeletedRefs      []string `json:"-"`
}

// sweepBlobStore walks the store once. Every blob absent from the referenced
// set is reported as unreferenced; with dryRun=false those are deleted —
// restricted to the eligible set when one is supplied (the weekly job's
// "unreferenced twice in a row" rule). Every deletion journals to the
// canonical lifecycle log first, exactly as before.
func sweepBlobStore(app *kanbanBoardApp, dryRun bool, eligible map[string]struct{}) (blobSweepReport, error) {
	report := blobSweepReport{DryRun: dryRun}
	referenced, err := blobReferencedRefs(app)
	if err != nil {
		return report, err
	}

	shards, err := os.ReadDir(blobStoreDir())
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, fmt.Errorf("read blob store: %w", err)
	}

	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		shardDir := filepath.Join(blobStoreDir(), shard.Name())
		files, err := os.ReadDir(shardDir)
		if err != nil {
			return report, fmt.Errorf("read blob shard %s: %w", shard.Name(), err)
		}
		for _, file := range files {
			ref := file.Name()
			if strings.HasSuffix(ref, blobMetaSuffix) || !validBlobRef(ref) {
				continue
			}
			report.Scanned++
			if _, ok := referenced[ref]; ok {
				continue
			}
			report.Unreferenced++
			report.UnreferencedRefs = append(report.UnreferencedRefs, ref)
			if dryRun {
				continue
			}
			if eligible != nil {
				if _, ok := eligible[ref]; !ok {
					continue
				}
			}
			var size int64
			if info, infoErr := file.Info(); infoErr == nil {
				size = info.Size()
			}
			journalRecord := CanonicalLifecycleJournalRecord{
				Family: "blob", ObjectID: ref, StateDigest: ref, At: time.Now().UTC(), Reason: "unreferenced_blob_sweep",
			}
			journalPath := filepath.Join(filepath.Dir(meetingMemoryPath()), "evicted-objects.jsonl")
			if err := ensureCanonicalLifecycleJournal(journalPath, journalRecord); err != nil {
				return report, fmt.Errorf("journal blob eviction %s: %w", ref, err)
			}
			metaPath := filepath.Join(shardDir, ref+blobMetaSuffix)
			if err := canonicalFenceRemoveMutation(metaPath, func() error { return os.Remove(metaPath) }); err != nil && !os.IsNotExist(err) {
				return report, fmt.Errorf("delete blob metadata %s: %w", ref, err)
			}
			dataPath := filepath.Join(shardDir, ref)
			if err := canonicalFenceRemoveMutation(dataPath, func() error { return os.Remove(dataPath) }); err != nil {
				return report, fmt.Errorf("delete blob %s: %w", ref, err)
			}
			report.Deleted++
			report.DeletedBytes += size
			report.DeletedRefs = append(report.DeletedRefs, ref)
		}
	}

	return report, nil
}

// sweepUnreferencedBlobs deletes every stored blob whose ref appears in no
// live reference, returning the deleted refs — the immediate (non-dry-run)
// form a human triggers through the admin action.
func sweepUnreferencedBlobs(app *kanbanBoardApp) ([]string, error) {
	report, err := sweepBlobStore(app, false, nil)
	return report.DeletedRefs, err
}

/* ---------- Wave 5 D10: admin action + weekly two-sighting sweep ---------- */

// blobSweepInterval is the weekly cadence of the scheduled sweep. Combined
// with the two-consecutive-sightings rule, a blob is deleted no sooner than a
// week after it first became unreferenced.
const blobSweepInterval = 7 * 24 * time.Hour

// blobSweepState is the scheduled job's durable memory beside the memory
// store: when it last ran and which refs it saw unreferenced then. A ref is
// deleted only when it is unreferenced on two consecutive weekly runs.
type blobSweepState struct {
	LastRunAt        string   `json:"lastRunAt"`
	PendingRefs      []string `json:"pendingRefs"`
	LastScanned      int      `json:"lastScanned"`
	LastUnreferenced int      `json:"lastUnreferenced"`
	LastDeleted      int      `json:"lastDeleted"`
	LastDeletedBytes int64    `json:"lastDeletedBytes"`
}

// blobSweepMu serializes the admin action and the scheduled job: two
// concurrent walks could otherwise both journal and race the same removal.
var blobSweepMu sync.Mutex

func blobSweepStatePath() string {
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "blob-sweep-state.json")
}

func loadBlobSweepState() (blobSweepState, error) {
	raw, err := os.ReadFile(blobSweepStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return blobSweepState{}, nil
		}
		return blobSweepState{}, fmt.Errorf("read blob sweep state: %w", err)
	}
	var state blobSweepState
	if err := json.Unmarshal(raw, &state); err != nil {
		return blobSweepState{}, fmt.Errorf("decode blob sweep state: %w", err)
	}
	return state, nil
}

// blobSweepDue reports whether the weekly cadence has elapsed. An unreadable
// or missing timestamp counts as due (the first run is always a dry run, so
// a lost state file can only delay deletion, never cause one).
func blobSweepDue(state blobSweepState, now time.Time) bool {
	last, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(state.LastRunAt))
	if err != nil {
		return true
	}
	return !now.Before(last.Add(blobSweepInterval))
}

// runScheduledBlobSweep is the weekly job. Safe to call on any cadence (the
// daily trash-purge tick calls it after purged rows are gone): it returns
// ran=false without touching disk when the weekly interval has not elapsed.
// Each due run is a dry-run walk first (logged), then deletes ONLY the refs
// that were already unreferenced on the previous weekly run — a blob must be
// sighted as an orphan twice, a week apart, before its bytes go. The fresh
// reference walk inside the delete pass means a ref re-referenced in between
// (a re-upload of the same bytes dedupes to the same ref) survives.
func (app *kanbanBoardApp) runScheduledBlobSweep(now time.Time) (blobSweepReport, bool, error) {
	if app == nil || app.memory == nil {
		return blobSweepReport{}, false, fmt.Errorf("artifact memory is unavailable")
	}
	now = now.UTC()
	blobSweepMu.Lock()
	defer blobSweepMu.Unlock()

	state, err := loadBlobSweepState()
	if err != nil {
		return blobSweepReport{}, false, err
	}
	if !blobSweepDue(state, now) {
		return blobSweepReport{}, false, nil
	}

	dry, err := sweepBlobStore(app, true, nil)
	if err != nil {
		return dry, true, err
	}
	log.Infof("Weekly blob sweep dry run: scanned=%d unreferenced=%d pendingFromLastRun=%d", dry.Scanned, dry.Unreferenced, len(state.PendingRefs))

	// Mention audit (2026-09-02): a ref the reference walk did not collect but
	// whose sha still appears in ANY row text or metadata value is the exact
	// class of bug the deck-scene walker miss was — a producer storing bytes
	// under a key the walker does not know. Every such ref is reported and,
	// whatever the walker says, never deleted by the weekly job; extending the
	// walker is the fix, and until then the report is the evidence.
	mentioned, blocking := blobRefMentions(app, blobRefSet(dry.UnreferencedRefs))
	for _, ref := range sortedBlobRefs(mentioned) {
		if _, veto := blocking[ref]; !veto {
			log.Infof("Weekly blob sweep report: %s is mentioned only as provenance by %s; reclaimable", ref, describeBlobRefMentions(mentioned[ref]))
			continue
		}
		log.Warnf("Weekly blob sweep report: %s is not collected by the reference walk but is mentioned by %s; protected from deletion — extend the walker", ref, describeBlobRefMentions(mentioned[ref]))
	}
	if blobSweepReportOnly() {
		log.Infof("Weekly blob sweep report-only mode (%s): scanned=%d unreferenced=%d mentionedButUncollected=%d protected=%d; nothing deleted and the sweep state is untouched", blobSweepReportOnlyEnv, dry.Scanned, dry.Unreferenced, len(mentioned), len(blocking))
		return dry, true, nil
	}

	previouslyPending := make(map[string]struct{}, len(state.PendingRefs))
	for _, ref := range state.PendingRefs {
		if validBlobRef(ref) {
			previouslyPending[ref] = struct{}{}
		}
	}
	eligible := map[string]struct{}{}
	for _, ref := range dry.UnreferencedRefs {
		if _, seenBefore := previouslyPending[ref]; !seenBefore {
			continue
		}
		if _, protected := blocking[ref]; protected {
			continue
		}
		eligible[ref] = struct{}{}
	}
	report := dry
	if len(eligible) > 0 {
		report, err = sweepBlobStore(app, false, eligible)
		if err != nil {
			return report, true, err
		}
		log.Infof("Weekly blob sweep deleted %d blob(s) (%d bytes) unreferenced on two consecutive runs", report.Deleted, report.DeletedBytes)
	}

	deleted := make(map[string]struct{}, len(report.DeletedRefs))
	for _, ref := range report.DeletedRefs {
		deleted[ref] = struct{}{}
	}
	pending := make([]string, 0, len(report.UnreferencedRefs))
	for _, ref := range report.UnreferencedRefs {
		if _, gone := deleted[ref]; !gone {
			pending = append(pending, ref)
		}
	}
	state = blobSweepState{
		LastRunAt: now.Format(time.RFC3339Nano), PendingRefs: pending,
		LastScanned: report.Scanned, LastUnreferenced: report.Unreferenced, LastDeleted: report.Deleted, LastDeletedBytes: report.DeletedBytes,
	}
	if err := writeJSONFileAtomically(blobSweepStatePath(), "blob sweep state", state); err != nil {
		return report, true, err
	}
	return report, true, nil
}

// adminBlobSweepHandler serves POST /assistant/admin/blobs/sweep {dryRun}
// for the artifact approval admin — the human-triggered form of the GC.
// dryRun=true reports the counts and deletes nothing; dryRun=false deletes
// every currently unreferenced blob immediately (no two-sighting wait: a
// human asked). Response: {ok, dryRun, scanned, unreferenced, deleted,
// deletedBytes}.
func adminBlobSweepHandler(w http.ResponseWriter, r *http.Request) {
	// The cross-origin check guards the ROUTE, above the method dispatch: a
	// sub-handler reached before it (the GET report, which takes blobSweepMu
	// and can run a whole-store scan) would otherwise be drivable from any
	// origin on the founder's session cookie.
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if r.Method == http.MethodGet {
		adminBlobSweepReportHandler(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if !isArtifactApprovalAdmin(user) {
		writeAuthError(w, http.StatusForbidden, "blob sweep is admin-only")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "blob sweep is unavailable")
		return
	}
	payload := struct {
		DryRun bool `json:"dryRun"`
		// Force deletes even the refs the mention audit protects; it requires a
		// reason, which is logged with the actor. The only override there is.
		Force  bool   `json:"force"`
		Reason string `json:"reason"`
	}{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read blob sweep request")
			return
		}
	}
	if payload.Force && strings.TrimSpace(payload.Reason) == "" {
		writeAuthError(w, http.StatusBadRequest, "force requires a reason")
		return
	}

	blobSweepMu.Lock()
	report, protected, err := adminSweepBlobStore(kanbanApp, payload.DryRun, payload.Force, payload.Reason, user.Email)
	blobSweepMu.Unlock()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"dryRun":       report.DryRun,
		"scanned":      report.Scanned,
		"unreferenced": report.Unreferenced,
		"deleted":      report.Deleted,
		"deletedBytes": report.DeletedBytes,
		"protected":    protected,
		"forced":       payload.Force,
	})
}

// adminSweepBlobStore is the human-triggered sweep behind the same mention
// audit as the weekly job: a ref the reference walk did not collect but whose
// sha is still written somewhere is never deleted by "sweep now" either —
// that is the walker-gap class the audit exists for. force=true with a logged
// reason is the only override. Returns the protected count alongside the
// report.
func adminSweepBlobStore(app *kanbanBoardApp, dryRun bool, force bool, reason string, actor string) (blobSweepReport, int, error) {
	dry, err := sweepBlobStore(app, true, nil)
	if err != nil {
		return dry, 0, err
	}
	// Only non-provenance mentions veto: a transcript row stamping the blob it
	// was read from, an import naming its source file, a chat row still
	// carrying an already-expired attachment's ref, a spent composer grant or
	// a dead share link cannot serve bytes, and holding on them made those
	// blobs unreclaimable forever.
	mentioned, blocking := blobRefMentions(app, blobRefSet(dry.UnreferencedRefs))
	for _, ref := range sortedBlobRefs(mentioned) {
		if _, veto := blocking[ref]; !veto {
			log.Infof("Admin blob sweep: %s is mentioned only as provenance by %s; reclaimable", ref, describeBlobRefMentions(mentioned[ref]))
			continue
		}
		log.Warnf("Admin blob sweep: %s is not collected by the reference walk but is mentioned by %s — extend the walker", ref, describeBlobRefMentions(mentioned[ref]))
	}
	if dryRun {
		return dry, len(blocking), nil
	}
	eligible := make(map[string]struct{}, len(dry.UnreferencedRefs))
	for _, ref := range dry.UnreferencedRefs {
		if _, protected := blocking[ref]; protected && !force {
			continue
		}
		eligible[ref] = struct{}{}
	}
	if force && len(blocking) > 0 {
		log.Warnf("Admin blob sweep FORCED by %s (reason: %q): deleting %d mentioned-but-uncollected ref(s)", actor, strings.TrimSpace(reason), len(blocking))
	}
	report, err := sweepBlobStore(app, false, eligible)
	if err != nil {
		return report, len(blocking), err
	}
	if !force {
		return report, len(blocking), nil
	}
	return report, 0, nil
}

/* ---------- 2026-09-02: pending-ref report + mention audit ---------- */

const (
	// blobSweepReportOnlyEnv turns the weekly job into a read-only report: the
	// dry run and the mention audit run and log, nothing is deleted and the
	// two-sighting state is not written (so the report repeats on every tick
	// until the flag is cleared).
	blobSweepReportOnlyEnv = "BLOB_SWEEP_REPORT_ONLY"
	// blobMentionCap bounds the mentions REPORTED per ref. It is a display
	// bound only: blobRefMentions records the deletion veto before applying it.
	blobMentionCap = 8
	// blobMentionKindShareLink / blobMentionKindPendingUpload are the two
	// mention sources that are not memory rows, named so the classifier and
	// the producer guess cannot drift apart from the emitter.
	blobMentionKindShareLink     = "share_link"
	blobMentionKindPendingUpload = "pending_upload"
)

func blobSweepReportOnly() bool {
	return boolEnv(blobSweepReportOnlyEnv)
}

// blobRefMention is one place a blob's sha appears in the workspace outside
// the reference walk: the row kind + id and the metadata key ("text" for the
// row body; "share_link" / "pending_upload" for the non-row stores).
type blobRefMention struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Key  string `json:"key"`
	// Provenance marks a mention that records where bytes CAME FROM rather
	// than a lane that could still serve them: a brain-intake transcript row
	// stamping its source blob, a Document Studio import naming the Drive file
	// it converted, or a chat thread's JSON body still carrying the ref of an
	// attachment already marked expired. Those are reported but never veto
	// deletion — otherwise the audit is a permanent, unreclaimable hold on
	// bytes nothing can render (and chat media retention could never finish).
	Provenance bool `json:"provenance,omitempty"`
}

// blobPendingRef is one row of the founder report.
type blobPendingRef struct {
	Ref       string           `json:"ref"`
	Origin    string           `json:"origin"` // pending | unreferenced
	Size      int64            `json:"size"`
	Mime      string           `json:"mime,omitempty"`
	CreatedAt string           `json:"createdAt,omitempty"`
	Missing   bool             `json:"missing,omitempty"`
	Producer  string           `json:"producer"`
	Mentions  []blobRefMention `json:"mentions"`
	Mentioned bool             `json:"mentioned"`
	// Blocking is the half of Mentioned that actually vetoes deletion: a ref
	// mentioned only as provenance is reported but still reclaimable.
	Blocking bool `json:"blocking"`
}

func blobRefSet(refs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref = strings.TrimSpace(ref); validBlobRef(ref) {
			set[ref] = struct{}{}
		}
	}
	return set
}

func sortedBlobRefs(mentions map[string][]blobRefMention) []string {
	refs := make([]string, 0, len(mentions))
	for ref := range mentions {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func describeBlobRefMentions(mentions []blobRefMention) string {
	parts := make([]string, 0, len(mentions))
	for index, mention := range mentions {
		if index >= 3 {
			parts = append(parts, fmt.Sprintf("+%d more", len(mentions)-index))
			break
		}
		parts = append(parts, mention.Kind+"/"+mention.ID+"["+mention.Key+"]")
	}
	return strings.Join(parts, ", ")
}

// scanBlobRefTokens visits every maximal run of exactly 64 lowercase hex
// characters in text — the blob ref shape — without allocating per
// candidate. A longer hex run (two concatenated digests, a 128-char hash)
// is not a ref and is skipped.
func scanBlobRefTokens(text string, visit func(ref string)) {
	start := -1
	for index := 0; index <= len(text); index++ {
		isHex := false
		if index < len(text) {
			char := text[index]
			isHex = (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')
		}
		if isHex {
			if start < 0 {
				start = index
			}
			continue
		}
		if start >= 0 {
			if index-start == sha256.Size*2 {
				visit(text[start:index])
			}
			start = -1
		}
	}
}

// scanBlobRefMentions walks every row's text and metadata values once under
// the read lock and reports each wanted ref it finds.
func (store *meetingMemoryStore) scanBlobRefMentions(refs map[string]struct{}, add func(ref string, kind string, id string, key string)) {
	if store == nil || len(refs) == 0 {
		return
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, entry := range store.entries {
		kind, id := entry.Kind, entry.ID
		scanBlobRefTokens(entry.Text, func(ref string) {
			if _, wanted := refs[ref]; wanted {
				add(ref, kind, id, "text")
			}
		})
		for key, value := range entry.Metadata {
			if len(value) < sha256.Size*2 {
				continue
			}
			scanBlobRefTokens(value, func(ref string) {
				if _, wanted := refs[ref]; wanted {
					add(ref, kind, id, key)
				}
			})
		}
	}
}

// chatMediaExpiredOnlyRefs is the set of blob refs that appear in chat ONLY as
// attachments already marked expired. Their sha still sits inside the thread
// row's JSON body (the record survives the body on purpose — the client draws
// "expired · not saved to Drive"), so the mention scan finds them in that
// row's text. That is provenance, not a lane that can serve bytes: a ref live
// anywhere in chat is excluded, exactly as the reference walk does.
func chatMediaExpiredOnlyRefs(app *kanbanBoardApp) map[string]struct{} {
	expired := map[string]struct{}{}
	if app == nil || app.memory == nil {
		return expired
	}
	live := map[string]struct{}{}
	note := func(ref string, isExpired bool) {
		if ref = strings.TrimSpace(ref); !validBlobRef(ref) {
			return
		}
		if isExpired {
			expired[ref] = struct{}{}
			return
		}
		live[ref] = struct{}{}
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			continue
		}
		for _, message := range thread.Messages {
			for _, file := range message.Files {
				note(file.Ref, file.Expired)
			}
			if message.Image != nil {
				note(message.Image.Ref, message.Image.Expired)
			}
		}
	}
	for ref := range live {
		delete(expired, ref)
	}
	return expired
}

// blobMentionProvenanceIndex carries the per-pass facts the classifier needs
// about sources whose ability to serve bytes depends on record STATE rather
// than on the key alone: chat refs that appear only as already-expired
// attachments, and the refs a LIVE file share link still streams.
type blobMentionProvenanceIndex struct {
	expiredChatRefs map[string]struct{}
	liveShareRefs   map[string]struct{}
}

// blobRefMentionIsProvenance classifies one mention source. Provenance-only
// sources record where bytes came from and can never serve them again, so the
// audit reports them without vetoing the GC. Everything else — including any
// key this function has never heard of — still blocks deletion; that ignorance
// is the point of the audit.
func blobRefMentionIsProvenance(ref string, kind string, key string, index blobMentionProvenanceIndex) bool {
	switch kind {
	case meetingMemoryKindTranscript:
		// brain_intake.go stamps the source blob on the transcript row it
		// writes; the transcript row IS the retained copy of that reading.
		return key == "blobRef"
	case meetingMemoryKindOSArtifact:
		// document_import.go records the Drive file a document was converted
		// from; the markdown body is the artifact, not the original bytes.
		return key == "importedFromBlobRef"
	case meetingMemoryKindScoutChat:
		if key != "text" {
			return false
		}
		_, expiredOnly := index.expiredChatRefs[strings.TrimSpace(ref)]
		return expiredOnly
	case blobMentionKindPendingUpload:
		// A composer grant records that an upload was authorized; it holds a
		// ref string and nothing else, so it can never serve the bytes on its
		// own. A grant that could still become a message (pending/reserved and
		// unexpired) is already collected by blobReferencedRefs, so it never
		// reaches the audit as an unreferenced candidate. What lands here is a
		// COMMITTED grant whose message attachment has since expired, or an
		// abandoned/revoked one — and grants are never removed from the store
		// (initializeAttachmentSourceStore keeps committed and revoked records
		// deliberately), so blocking on them held those bodies forever.
		return true
	case blobMentionKindShareLink:
		// A live file share link streams these exact bytes on demand: a real
		// serving path, so it blocks (and blobReferencedRefs already collects
		// it, so it is not an orphan in the first place). Every other link —
		// revoked, expired, or bound to a non-file object — only records the
		// digest it was minted from, and pinned the blob forever.
		_, live := index.liveShareRefs[strings.TrimSpace(ref)]
		return !live
	}
	return false
}

// blobRefMentions finds, for each wanted ref, everywhere its sha is still
// written in the workspace: memory rows (text + every metadata value), share
// links and pending composer uploads. It deliberately knows NOTHING about
// which keys the reference walk collects — that ignorance is the point: a
// mention the walk did not turn into a reference is a walker gap. Each mention
// is classified provenance-or-not (see blobRefMentionIsProvenance) so a record
// of where bytes came from is reported without becoming an eternal hold.
//
// It returns the per-ref mention list AND the set of refs whose deletion the
// audit vetoes. The two are computed together on purpose: blobMentionCap
// truncates the reported list, so deriving the veto from that list afterwards
// silently discarded the 9th mention — the one that could still serve bytes —
// and turned a display bound into a deletion decision.
func blobRefMentions(app *kanbanBoardApp, refs map[string]struct{}) (map[string][]blobRefMention, map[string]struct{}) {
	mentions := map[string][]blobRefMention{}
	blocking := map[string]struct{}{}
	if app == nil || app.memory == nil || len(refs) == 0 {
		return mentions, blocking
	}
	index := blobMentionProvenanceIndex{
		expiredChatRefs: chatMediaExpiredOnlyRefs(app),
		liveShareRefs:   map[string]struct{}{},
	}
	links, linksErr := loadShareLinks()
	if linksErr == nil {
		now := time.Now().UTC()
		for _, link := range links {
			if link.ObjectType == shareLinkObjectTypeFile && shareLinkLive(link, now) {
				index.liveShareRefs[strings.TrimSpace(link.ContentDigest)] = struct{}{}
			}
		}
	}
	seen := map[string]struct{}{}
	add := func(ref string, kind string, id string, key string) {
		if _, wanted := refs[ref]; !wanted {
			return
		}
		dedupe := ref + "\x00" + kind + "\x00" + id + "\x00" + key
		if _, dup := seen[dedupe]; dup {
			return
		}
		seen[dedupe] = struct{}{}
		provenance := blobRefMentionIsProvenance(ref, kind, key, index)
		if !provenance {
			// Recorded BEFORE the cap: the veto must never depend on where a
			// mention happened to land in store-iteration order.
			blocking[ref] = struct{}{}
		}
		if len(mentions[ref]) >= blobMentionCap {
			return
		}
		mentions[ref] = append(mentions[ref], blobRefMention{
			Kind: kind, ID: id, Key: key, Provenance: provenance,
		})
	}
	app.memory.scanBlobRefMentions(refs, add)
	if linksErr == nil {
		for _, link := range links {
			add(strings.TrimSpace(link.ContentDigest), blobMentionKindShareLink, link.ID, "contentDigest")
		}
	}
	app.pendingAttachmentUploadsMu.Lock()
	for key, source := range app.pendingAttachmentUploads {
		add(strings.TrimSpace(source.Ref), blobMentionKindPendingUpload, key, "ref")
	}
	app.pendingAttachmentUploadsMu.Unlock()
	return mentions, blocking
}

// blobProducerGuess names the lane that most plausibly stored the bytes,
// from the first mention. Best-effort by design.
func blobProducerGuess(mentions []blobRefMention) string {
	if len(mentions) == 0 {
		return "no mention"
	}
	mention := mentions[0]
	switch mention.Kind {
	case meetingMemoryKindScoutChat:
		return "chat attachment/image (thread " + mention.ID + ")"
	case meetingMemoryKindFile:
		return "drive upload"
	case meetingMemoryKindOSArtifact:
		switch mention.Key {
		case artifactAssetsMetadataKey:
			return "artifact asset (pdf/image/page render)"
		case deckSceneRefMetadataKey, renderSourceSceneRefMetadataKey:
			return "deck scene"
		case renderPDFAssetRefMetadataKey:
			return "deck render pdf"
		case artifactVersionsMetadataKey:
			return "artifact version snapshot"
		case "text":
			return "artifact body (doc image / inline ref)"
		}
		return "artifact metadata " + mention.Key
	case blobMentionKindShareLink:
		return "share link preview"
	case blobMentionKindPendingUpload:
		return "pending composer upload"
	}
	if strings.Contains(strings.ToLower(mention.Kind), "recording") {
		return "meeting recording"
	}
	return mention.Kind + "." + mention.Key
}

// blobSidecarMeta reads a blob's publication sidecar (mime, size, created)
// without touching the data file; a missing sidecar degrades to the data
// file's size and mtime.
func blobSidecarMeta(ref string) (blobMeta, error) {
	dataPath, metaPath, err := blobPaths(ref)
	if err != nil {
		return blobMeta{}, err
	}
	if raw, err := os.ReadFile(metaPath); err == nil {
		var meta blobMeta
		if json.Unmarshal(raw, &meta) == nil && meta.Size > 0 {
			return meta, nil
		}
	}
	info, err := os.Stat(dataPath)
	if err != nil {
		return blobMeta{}, err
	}
	return blobMeta{Mime: blobDefaultMime, Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339Nano)}, nil
}

// blobSweepPendingReport is the founder report behind GET
// /assistant/admin/blobs/sweep?pending=1: the refs the weekly job is holding
// for deletion on its next sighting (plus, with includeScan, every ref a
// fresh read-only dry run finds unreferenced right now), each with size,
// created time, and a best-effort producer guess from wherever the sha is
// still mentioned. Read-only: no store mutation, no sweep-state write.
func (app *kanbanBoardApp) blobSweepPendingReport(includeScan bool) (map[string]any, error) {
	if app == nil || app.memory == nil {
		return nil, fmt.Errorf("artifact memory is unavailable")
	}
	state, err := loadBlobSweepState()
	if err != nil {
		return nil, err
	}
	origins := map[string]string{}
	for _, ref := range state.PendingRefs {
		if validBlobRef(ref) {
			origins[ref] = "pending"
		}
	}
	scanned := 0
	if includeScan {
		dry, err := sweepBlobStore(app, true, nil)
		if err != nil {
			return nil, err
		}
		scanned = dry.Scanned
		for _, ref := range dry.UnreferencedRefs {
			if _, known := origins[ref]; !known {
				origins[ref] = "unreferenced"
			}
		}
	}
	refs := make(map[string]struct{}, len(origins))
	for ref := range origins {
		refs[ref] = struct{}{}
	}
	mentions, blocking := blobRefMentions(app, refs)
	rows := make([]blobPendingRef, 0, len(origins))
	flagged := 0
	for ref, origin := range origins {
		row := blobPendingRef{Ref: ref, Origin: origin, Mentions: mentions[ref]}
		if row.Mentions == nil {
			row.Mentions = []blobRefMention{}
		}
		row.Mentioned = len(row.Mentions) > 0
		_, row.Blocking = blocking[ref]
		if row.Mentioned {
			flagged++
		}
		row.Producer = blobProducerGuess(row.Mentions)
		if meta, err := blobSidecarMeta(ref); err == nil {
			row.Size, row.Mime, row.CreatedAt = meta.Size, meta.Mime, meta.CreatedAt
		} else {
			row.Missing = true
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Blocking != rows[j].Blocking {
			return rows[i].Blocking
		}
		if rows[i].Mentioned != rows[j].Mentioned {
			return rows[i].Mentioned
		}
		return rows[i].Ref < rows[j].Ref
	})
	return map[string]any{
		"ok":                      true,
		"lastRunAt":               state.LastRunAt,
		"lastScanned":             state.LastScanned,
		"lastUnreferenced":        state.LastUnreferenced,
		"pendingCount":            len(state.PendingRefs),
		"scan":                    includeScan,
		"scanned":                 scanned,
		"reported":                len(rows),
		"mentionedButUncollected": flagged,
		"protectedFromDeletion":   len(blocking),
		"reportOnlyMode":          blobSweepReportOnly(),
		"refs":                    rows,
	}, nil
}

// adminBlobSweepReportHandler serves GET /assistant/admin/blobs/sweep?pending=1
// — founder-only (isFounderOwner), the sole principal who may see a ref list.
// ?scan=1 adds a fresh read-only dry run over the whole store. Nothing here
// mutates the store or the sweep state.
func adminBlobSweepReportHandler(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if !isFounderOwner(user) {
		writeAuthError(w, http.StatusForbidden, "blob sweep report is founder-only")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "blob sweep report is unavailable")
		return
	}
	query := r.URL.Query()
	truthy := func(value string) bool {
		value = strings.ToLower(strings.TrimSpace(value))
		return value == "1" || value == "true" || value == "yes"
	}
	if !truthy(query.Get("pending")) && !truthy(query.Get("scan")) {
		writeAuthError(w, http.StatusBadRequest, "pass ?pending=1 (held refs) and/or ?scan=1 (fresh dry run)")
		return
	}
	blobSweepMu.Lock()
	report, err := kanbanApp.blobSweepPendingReport(truthy(query.Get("scan")))
	blobSweepMu.Unlock()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, report)
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
	// Wave 7 Meeting Record playback: non-script-capable media containers.
	"video/webm": true,
	"audio/webm": true,
	// Hotfix gen 249: chat video attachments play inline (<video controls>).
	"video/mp4":       true,
	"video/quicktime": true,
}

var artifactBlobAfterReadProbe func(string)

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
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if !validBlobRef(ref) {
		writeAuthError(w, http.StatusBadRequest, "invalid blob ref")
		return
	}
	// Resolve the hash through an authorized owning artifact/revision BEFORE
	// ETag handling or disk I/O. A learned content hash is never authority.
	if !blobAuthorized(r.Context(), user, ref) {
		writeAuthError(w, http.StatusNotFound, "blob not found")
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
	if artifactBlobAfterReadProbe != nil {
		artifactBlobAfterReadProbe(ref)
	}
	// Re-resolve authority after the byte read and before publishing any
	// headers or body. A concurrent message delete, archive, or ACL revocation
	// therefore turns the response into the same non-enumerating 404 instead
	// of leaking bytes authorized only by a stale pre-read snapshot.
	if !blobAuthorized(r.Context(), user, ref) {
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
	if strings.HasPrefix(meta.Mime, "video/") || strings.HasPrefix(meta.Mime, "audio/") {
		// Media elements (Safari above all) refuse to play a source whose
		// server ignores Range; ServeContent answers 206 byte ranges, keeps
		// the ETag/Cache-Control already set, and honors If-Range/If-None-Match.
		// The immutable ref carries no useful modtime, so none is passed.
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
		return
	}
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
