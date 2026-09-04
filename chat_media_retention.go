package main

// Chat media retention (Wave 11 D18).
//
// Founder: "be logical about how that feed is analyzed and items eventually
// deleted automatically (unless something explicitly was clicked and saved to
// drive) … or we'll have intense server bloat."
//
// What stays forever: every message's TEXT, every brain-derived row (the
// transcript rows chat files, digests, notes), and every attachment's
// server-derived Text/caption (ingestion already read the media where it
// does; that reading lives on the message record, never in the blob).
//
// What expires: media BODIES shared in chat — uploaded images, GIF imports,
// video/photo/file attachments and Scout-generated chat images — once older
// than CHAT_MEDIA_RETENTION_DAYS (default 90). Videos additionally expire at
// 30 days while the data volume is under disk pressure (>80% used, read with
// syscall.Statfs on the blob dir).
//
// What is PERMANENT regardless of age: a body referenced by a Drive row (the
// user clicked Save to Drive), by a deliverable/artifact asset or version, by
// a Meeting Record recording, by a live share link, or carrying an explicit
// keep flag on the attachment.
//
// Discipline: the pass runs inside the daily Drive trash sweeper
// (sweepFileTrashOnce) with the same two-sighting rule the weekly blob GC
// uses — the first sighting past the threshold only stamps expiresAt (7 days
// out, visible to clients); a later sighting at/after that stamp marks the
// attachment expired=true and deletes the body if nothing else still points
// at it. The message stays intact; the client renders "expired · not saved to
// Drive". Keyless, pure disk.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	chatMediaRetentionDaysEnv      = "CHAT_MEDIA_RETENTION_DAYS"
	chatMediaRetentionDefaultDays  = 90
	chatMediaVideoPressureDays     = 30
	chatMediaDiskPressureThreshold = 0.80
	chatMediaExpiryGrace           = 7 * 24 * time.Hour
	chatMediaExpiredReason         = "chat_media_retention"
)

// chatMediaRetentionSettings is the founder-visible policy the storage report
// echoes and the sweep applies.
type chatMediaRetentionSettings struct {
	RetentionDays         int     `json:"retentionDays"`
	VideoPressureDays     int     `json:"videoPressureDays"`
	DiskPressureThreshold float64 `json:"diskPressureThreshold"`
	GraceDays             int     `json:"graceDays"`
}

func chatMediaRetentionDays() int {
	if raw := strings.TrimSpace(os.Getenv(chatMediaRetentionDaysEnv)); raw != "" {
		if days, err := strconv.Atoi(raw); err == nil && days > 0 {
			return days
		}
	}
	return chatMediaRetentionDefaultDays
}

func currentChatMediaRetentionSettings() chatMediaRetentionSettings {
	return chatMediaRetentionSettings{
		RetentionDays: chatMediaRetentionDays(), VideoPressureDays: chatMediaVideoPressureDays,
		DiskPressureThreshold: chatMediaDiskPressureThreshold, GraceDays: int(chatMediaExpiryGrace / (24 * time.Hour)),
	}
}

// chatMediaDiskUsage is the data-volume usage fraction (0..1) read from the
// filesystem holding the blob store. Tests override it; a read failure
// reports zero (no pressure) rather than inventing pressure.
var chatMediaDiskUsage = func() (float64, error) {
	dir := blobStoreDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	// All three numbers, because the policy is stated in df's terms and df's
	// Use% is neither blocks-minus-bfree nor blocks-minus-bavail over blocks.
	return chatMediaUsedFraction(uint64(stat.Blocks), uint64(stat.Bfree), uint64(stat.Bavail)), nil
}

// chatMediaUsedFraction is the pure arithmetic behind chatMediaDiskUsage, and
// it is exactly df's Use%: used / (used + available), where used counts the
// filesystem's reserved blocks as consumed (blocks - bfree) but the
// DENOMINATOR excludes them. df does not merely ignore the reserve — it drops
// it from both halves — so neither single-number reading matches the figure an
// operator reads off df when checking the documented ">80% used" rule:
//
//	100GB volume, 76GB used, ext4's default 5% root reserve
//	  bfree=24  bavail=19  →  df says 76/(76+19) = 80%
//	  blocks-minus-bavail over blocks = 81%  (tripped the rule four points
//	    early, expiring chat video 60 days ahead of policy — the regression
//	    this formula's predecessor was written to fix)
//	  blocks-minus-bfree over blocks  = 76%  (four points LATE: df already
//	    reads 80% while the pressure rule stays off)
func chatMediaUsedFraction(totalBlocks uint64, freeBlocks uint64, availBlocks uint64) float64 {
	if totalBlocks == 0 {
		return 0
	}
	if freeBlocks > totalBlocks {
		return 0
	}
	usedBlocks := totalBlocks - freeBlocks
	if availBlocks > totalBlocks {
		availBlocks = totalBlocks
	}
	denominator := usedBlocks + availBlocks
	if denominator == 0 {
		// Nothing used and nothing available to a normal writer: the volume is
		// entirely reserve. df prints "-"; the honest reading here is full.
		return 1
	}
	used := float64(usedBlocks) / float64(denominator)
	if used < 0 {
		used = 0
	}
	if used > 1 {
		used = 1
	}
	return used
}

func chatMediaUnderDiskPressure() (float64, bool) {
	used, err := chatMediaDiskUsage()
	if err != nil {
		log.Warnf("chat media retention: disk usage unavailable: %v", err)
		return 0, false
	}
	return used, used > chatMediaDiskPressureThreshold
}

// chatMediaPermanentRefs is the set of blob refs the retention pass must
// never touch: everything the workspace references outside chat. It is the
// blob GC's reference walk minus the chat lane itself.
func chatMediaPermanentRefs(app *kanbanBoardApp) (map[string]struct{}, error) {
	return blobReferencedRefsWithChat(app, false)
}

// chatMediaAttachmentAge is the age of one attachment: its message's
// timestamp (the body was shared then; the blob's own mtime is a dedupe
// artefact and would let a re-share reset the clock).
func chatMediaAttachmentAge(message scoutChatMessageRecord, now time.Time) (time.Duration, bool) {
	created := parseRFC3339OrZero(message.CreatedAt)
	if created.IsZero() {
		return 0, false
	}
	return now.Sub(created), true
}

func chatMediaIsVideo(mime string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mime)), "video/")
}

// chatMediaBodyPresent reports whether a ref's bytes are still on disk. Only
// the heal path consults it (an expired record whose ref turned permanent), so
// it costs one stat for a case that is rare by construction.
func chatMediaBodyPresent(ref string) bool {
	dataPath, _, err := blobPaths(strings.TrimSpace(ref))
	if err != nil {
		return false
	}
	_, statErr := os.Stat(dataPath)
	return statErr == nil
}

// chatMediaRetentionReport is one pass's outcome (logged, and the wire shape
// of the storage report's last-pass block).
type chatMediaRetentionReport struct {
	Threads      int     `json:"threads"`
	Marked       int     `json:"marked"`
	Expired      int     `json:"expired"`
	Deleted      int     `json:"deleted"`
	DeletedBytes int64   `json:"deletedBytes"`
	DiskUsage    float64 `json:"diskUsage"`
	DiskPressure bool    `json:"diskPressure"`
	RanAt        string  `json:"ranAt"`
}

var lastChatMediaRetentionReport struct {
	mu     sync.Mutex
	report chatMediaRetentionReport
	ok     bool
}

// chatMediaRetentionSaveThread is the pass's thread-save seam. Tests replace
// it to prove a failed save never lets the delete loop reclaim a body the
// persisted record still claims as live.
var chatMediaRetentionSaveThread = func(app *kanbanBoardApp, thread scoutChatThreadRecord) error {
	return app.saveScoutChatThread(thread)
}

// chatMediaRetentionAfterWalkProbe runs between the thread walk and the
// permanent-reference recheck. Tests use it to land a Save to Drive exactly
// where a real one races the pass.
var chatMediaRetentionAfterWalkProbe func()

// sweepChatMediaRetentionOnce is the daily pass. It walks every thread once,
// stamps/expires attachments per the policy above, saves each changed thread
// under its lock, then deletes the bodies of newly expired attachments that
// nothing permanent or still-live in chat references.
func (app *kanbanBoardApp) sweepChatMediaRetentionOnce(now time.Time) (chatMediaRetentionReport, error) {
	report := chatMediaRetentionReport{RanAt: now.UTC().Format(time.RFC3339Nano)}
	if app == nil || app.memory == nil {
		return report, fmt.Errorf("chat memory is unavailable")
	}
	now = now.UTC()
	permanent, err := chatMediaPermanentRefs(app)
	if err != nil {
		return report, err
	}
	usage, pressure := chatMediaUnderDiskPressure()
	report.DiskUsage, report.DiskPressure = usage, pressure
	retention := time.Duration(chatMediaRetentionDays()) * 24 * time.Hour
	videoRetention := retention
	if pressure && time.Duration(chatMediaVideoPressureDays)*24*time.Hour < retention {
		videoRetention = time.Duration(chatMediaVideoPressureDays) * 24 * time.Hour
	}

	liveChatRefs := map[string]struct{}{}
	expiredRefs := map[string]struct{}{}
	// decide applies the policy to one media ref and returns the new
	// (expiresAt, expired) state plus whether anything changed.
	decide := func(ref, mime, expiresAt string, expired, keep bool, message scoutChatMessageRecord) (string, bool, bool) {
		if !validBlobRef(ref) {
			return expiresAt, expired, false
		}
		if _, forever := permanent[ref]; forever || keep {
			if expiresAt == "" && !expired {
				return expiresAt, false, false
			}
			// A body saved to Drive (or kept) after being marked: clear the
			// mark. If the flag already flipped — a Save to Drive that raced
			// the pass that expired it, whose body the pre-delete permanence
			// recheck then spared — heal the record too, but only while the
			// bytes are genuinely still there to serve.
			if expired && !chatMediaBodyPresent(ref) {
				return expiresAt, expired, false
			}
			return "", false, true
		}
		if expired {
			return expiresAt, expired, false
		}
		age, ok := chatMediaAttachmentAge(message, now)
		if !ok {
			return expiresAt, false, false
		}
		limit := retention
		if chatMediaIsVideo(mime) {
			limit = videoRetention
		}
		if age < limit {
			if expiresAt != "" {
				return "", false, true
			}
			return expiresAt, false, false
		}
		if expiresAt == "" {
			return now.Add(chatMediaExpiryGrace).Format(time.RFC3339Nano), false, true
		}
		if stamped := parseRFC3339OrZero(expiresAt); !stamped.IsZero() && !now.Before(stamped) {
			return expiresAt, true, true
		}
		return expiresAt, false, false
	}

	type threadChange struct {
		thread   scoutChatThreadRecord
		messages []scoutChatMessageRecord
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			continue
		}
		report.Threads++
		lock := app.scoutChatThreadLock(thread.ID)
		lock.Lock()
		// Re-read under the lock so a concurrent commit is never overwritten.
		current, _, readErr := app.scoutChatThreadByID(thread.OwnerEmail, thread.ID)
		if readErr != nil {
			lock.Unlock()
			continue
		}
		change := threadChange{thread: current}
		// Everything this thread's walk learned stays LOCAL until the save
		// succeeds. A newly expired ref recorded before persistence would have
		// its body deleted even when saveScoutChatThread failed — the exact
		// disk-full / EIO condition this pass exists to relieve — leaving a
		// persisted record that still claims a live attachment whose bytes are
		// gone, which the reference walk then protects forever.
		threadLive := []string{}
		threadAlreadyExpired := []string{}
		threadNewlyExpired := []string{}
		threadMarked, threadExpired := 0, 0
		for index := range current.Messages {
			message := &current.Messages[index]
			touched := false
			for fileIndex := range message.Files {
				file := &message.Files[fileIndex]
				if !validBlobRef(file.Ref) {
					continue
				}
				wasExpired := file.Expired
				expiresAt, expired, changed := decide(file.Ref, file.Mime, file.ExpiresAt, file.Expired, file.Keep, *message)
				if changed {
					if expiresAt != "" && file.ExpiresAt == "" {
						threadMarked++
					}
					if expired && !file.Expired {
						threadExpired++
					}
					file.ExpiresAt, file.Expired = expiresAt, expired
					touched = true
				}
				switch {
				case file.Expired && wasExpired:
					threadAlreadyExpired = append(threadAlreadyExpired, file.Ref)
				case file.Expired:
					threadNewlyExpired = append(threadNewlyExpired, file.Ref)
				default:
					threadLive = append(threadLive, file.Ref)
				}
			}
			if message.Image != nil && validBlobRef(message.Image.Ref) {
				image := message.Image
				wasExpired := image.Expired
				expiresAt, expired, changed := decide(image.Ref, firstNonEmptyString(image.Mime, "image/png"), image.ExpiresAt, image.Expired, image.SavedToFiles, *message)
				if changed {
					if expiresAt != "" && image.ExpiresAt == "" {
						threadMarked++
					}
					if expired && !image.Expired {
						threadExpired++
					}
					image.ExpiresAt, image.Expired = expiresAt, expired
					touched = true
				}
				switch {
				case image.Expired && wasExpired:
					threadAlreadyExpired = append(threadAlreadyExpired, image.Ref)
				case image.Expired:
					threadNewlyExpired = append(threadNewlyExpired, image.Ref)
				default:
					threadLive = append(threadLive, image.Ref)
				}
			}
			if touched {
				change.messages = append(change.messages, *message)
			}
		}
		// A ref already persisted as expired is safe to reclaim whatever this
		// pass's save does; the refs this pass flipped are not.
		for _, ref := range threadAlreadyExpired {
			expiredRefs[ref] = struct{}{}
		}
		for _, ref := range threadLive {
			liveChatRefs[ref] = struct{}{}
		}
		saved := len(change.messages) == 0
		if len(change.messages) > 0 {
			if saveErr := chatMediaRetentionSaveThread(app, current); saveErr != nil {
				log.Errorf("chat media retention: save thread %s: %v", current.ID, saveErr)
			} else {
				saved = true
			}
		}
		if saved {
			report.Marked += threadMarked
			report.Expired += threadExpired
			for _, ref := range threadNewlyExpired {
				expiredRefs[ref] = struct{}{}
			}
		} else {
			// The persisted record still says live, so the body must stay.
			for _, ref := range threadNewlyExpired {
				liveChatRefs[ref] = struct{}{}
			}
		}
		if saved && len(change.messages) > 0 {
			change.thread = current
			lock.Unlock()
			for _, message := range change.messages {
				deliverScoutChatThreadUpdate(change.thread, message)
			}
			continue
		}
		lock.Unlock()
	}

	if chatMediaRetentionAfterWalkProbe != nil {
		chatMediaRetentionAfterWalkProbe()
	}
	// Recompute the reference set immediately before deleting, and use the
	// chat-INCLUSIVE walk this time. Two races land here, both because blobs
	// are content-addressed (putBlobWithCap), so a re-share of identical bytes
	// resolves to the SAME ref this pass is about to delete:
	//   - Save to Drive: the visible expiresAt warning is precisely the prompt
	//     for it, and the save appends a Drive row carrying that same ref
	//     (files.go writes "blobRef": source.Ref, never a copy).
	//   - A new chat message: a fresh reserve+commit of the same GIF/logo in
	//     any thread. It is not in liveChatRefs (built from the walk above,
	//     never recomputed) and its grant is already COMMITTED, so the
	//     pending-upload lane does not cover it either — the chat-excluded
	//     walk was structurally blind to it and deleted the body out from
	//     under a message posted seconds ago.
	// The chat-inclusive walk skips file.Expired refs, so a body whose record
	// is expired everywhere is still reclaimed; any live copy, old or born
	// mid-pass, spares it. It subsumes the Drive half of the race.
	//
	// It lands in its OWN variable: `permanent` is what decide() reads to mean
	// "saved outside chat, never expire it", and feeding the chat-inclusive
	// walk back into it would make every live attachment permanent and stop
	// the policy from ever marking anything again.
	stillReferenced := permanent
	if fresh, refreshErr := blobReferencedRefs(app); refreshErr != nil {
		log.Errorf("chat media retention: reference recheck failed, deleting nothing this pass: %v", refreshErr)
		expiredRefs = nil
	} else {
		stillReferenced = fresh
	}

	// Delete the bodies of expired attachments nothing else still points at.
	doomed := make([]string, 0, len(expiredRefs))
	for ref := range expiredRefs {
		if _, referenced := stillReferenced[ref]; referenced {
			continue
		}
		if _, live := liveChatRefs[ref]; live {
			continue
		}
		doomed = append(doomed, ref)
	}
	sort.Strings(doomed)
	// Last gate: the SAME mention audit the weekly GC and the admin sweep run
	// (blobs.go). The two reference walks above only know the keys the walker
	// knows; the audit knows none of them and vetoes any ref whose sha is still
	// written somewhere that could serve the bytes — a producer storing under a
	// key blobReferencedRefsWithChat does not collect (the deck-scene walker
	// gap this audit was written for). Because blobs are content-addressed, one
	// such producer sharing bytes a user also posted in chat would lose them
	// permanently here at 90 days, with no second sighting to catch it. This
	// pass was the only deletion path opting out of that guard.
	//
	// Normal expiry is untouched: by the time we get here the thread record is
	// persisted expired, so chatMediaExpiredOnlyRefs classifies the chat row's
	// own text mention (and the committed composer grant behind it) as
	// provenance, not as a lane that can still serve bytes.
	if _, blocking := blobRefMentions(app, blobRefSet(doomed)); len(blocking) > 0 {
		kept := doomed[:0]
		for _, ref := range doomed {
			if _, veto := blocking[ref]; veto {
				log.Warnf("chat media retention: %s is mentioned by a producer the reference walk does not know; keeping the body — extend the walker", ref)
				continue
			}
			kept = append(kept, ref)
		}
		doomed = kept
	}
	for _, ref := range doomed {
		size, deleted, deleteErr := deleteChatMediaBlob(ref)
		if deleteErr != nil {
			log.Errorf("chat media retention: delete %s: %v", ref, deleteErr)
			continue
		}
		if deleted {
			report.Deleted++
			report.DeletedBytes += size
		}
	}
	if report.Marked > 0 || report.Expired > 0 || report.Deleted > 0 {
		log.Infof("Chat media retention: marked=%d expired=%d deleted=%d (%d bytes) diskUsage=%.0f%% pressure=%t", report.Marked, report.Expired, report.Deleted, report.DeletedBytes, usage*100, pressure)
	}
	lastChatMediaRetentionReport.mu.Lock()
	lastChatMediaRetentionReport.report, lastChatMediaRetentionReport.ok = report, true
	lastChatMediaRetentionReport.mu.Unlock()
	return report, nil
}

// deleteChatMediaBlob removes one blob's data + sidecar behind a lifecycle
// journal line, the same fence the GC uses. Returns the bytes freed and
// whether a body was actually present.
func deleteChatMediaBlob(ref string) (int64, bool, error) {
	dataPath, metaPath, err := blobPaths(ref)
	if err != nil {
		return 0, false, err
	}
	info, statErr := os.Stat(dataPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return 0, false, nil
		}
		return 0, false, statErr
	}
	journalRecord := CanonicalLifecycleJournalRecord{
		Family: "blob", ObjectID: ref, StateDigest: ref, At: time.Now().UTC(), Reason: chatMediaExpiredReason,
	}
	journalPath := filepath.Join(filepath.Dir(meetingMemoryPath()), "evicted-objects.jsonl")
	if err := ensureCanonicalLifecycleJournal(journalPath, journalRecord); err != nil {
		return 0, false, fmt.Errorf("journal chat media eviction %s: %w", ref, err)
	}
	if err := canonicalFenceRemoveMutation(metaPath, func() error { return os.Remove(metaPath) }); err != nil && !os.IsNotExist(err) {
		return 0, false, fmt.Errorf("delete blob metadata %s: %w", ref, err)
	}
	if err := canonicalFenceRemoveMutation(dataPath, func() error { return os.Remove(dataPath) }); err != nil {
		return 0, false, fmt.Errorf("delete blob %s: %w", ref, err)
	}
	return info.Size(), true, nil
}

// ---------------------------------------------------------------------------
// Founder storage report: GET /assistant/admin/storage
// ---------------------------------------------------------------------------

type storageClassBytes struct {
	Bytes int64 `json:"bytes"`
	Count int   `json:"count"`
}

// storageReport is the founder's view: bytes by class (distinct blobs per
// class — a blob referenced by Drive AND chat counts once, under Drive),
// blobs pending expiry, the retention settings, disk usage and the last pass.
func (app *kanbanBoardApp) storageReport() (map[string]any, error) {
	if app == nil || app.memory == nil {
		return nil, fmt.Errorf("storage report is unavailable")
	}
	sizeOf := func(ref string) int64 {
		meta, err := blobSidecarMeta(ref)
		if err != nil {
			return 0
		}
		return meta.Size
	}
	classes := map[string]*storageClassBytes{
		"uploads": {}, "chatMedia": {}, "deliverables": {}, "recordings": {}, "pendingExpiry": {}, "expired": {},
	}
	claimed := map[string]string{}
	claim := func(class, ref string) {
		ref = strings.TrimSpace(ref)
		if !validBlobRef(ref) {
			return
		}
		if _, taken := claimed[ref]; taken {
			return
		}
		claimed[ref] = class
		classes[class].Count++
		classes[class].Bytes += sizeOf(ref)
	}
	for _, ref := range meetingRecordingBlobRefs(app) {
		claim("recordings", ref)
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindFile, 0) {
		claim("uploads", entry.Metadata["blobRef"])
	}
	for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		for _, asset := range artifactAssets(artifact) {
			claim("deliverables", asset.Ref)
			claim("deliverables", asset.SourceSceneRef)
		}
		for _, key := range []string{deckSceneRefMetadataKey, renderSourceSceneRefMetadataKey, renderPDFAssetRefMetadataKey} {
			claim("deliverables", artifact.Metadata[key])
		}
		for _, version := range artifactVersionHistory(artifact) {
			claim("deliverables", version.BodyBlobRef)
			claim("deliverables", version.SceneRef)
		}
	}
	pendingCount, expiredCount := 0, 0
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0) {
		thread, ok := decodeScoutChatThreadEntry(entry)
		if !ok {
			continue
		}
		for _, message := range thread.Messages {
			for _, file := range message.Files {
				switch {
				case file.Expired:
					expiredCount++
					claim("expired", file.Ref)
				case file.ExpiresAt != "":
					pendingCount++
					claim("pendingExpiry", file.Ref)
				default:
					claim("chatMedia", file.Ref)
				}
			}
			if message.Image != nil {
				switch {
				case message.Image.Expired:
					expiredCount++
					claim("expired", message.Image.Ref)
				case message.Image.ExpiresAt != "":
					pendingCount++
					claim("pendingExpiry", message.Image.Ref)
				default:
					claim("chatMedia", message.Image.Ref)
				}
			}
		}
	}
	// Whole-store truth: every blob on disk, so unclaimed bytes are visible.
	var storeBytes int64
	storeCount := 0
	if shards, err := os.ReadDir(blobStoreDir()); err == nil {
		for _, shard := range shards {
			if !shard.IsDir() {
				continue
			}
			files, readErr := os.ReadDir(filepath.Join(blobStoreDir(), shard.Name()))
			if readErr != nil {
				continue
			}
			for _, file := range files {
				if strings.HasSuffix(file.Name(), blobMetaSuffix) || !validBlobRef(file.Name()) {
					continue
				}
				if info, infoErr := file.Info(); infoErr == nil {
					storeBytes += info.Size()
					storeCount++
				}
			}
		}
	}
	var claimedBytes int64
	for _, class := range classes {
		claimedBytes += class.Bytes
	}
	usage, pressure := chatMediaUnderDiskPressure()
	report := map[string]any{
		"ok":                  true,
		"classes":             classes,
		"blobStore":           map[string]any{"bytes": storeBytes, "count": storeCount, "unclaimedBytes": maxInt64(storeBytes-claimedBytes, 0), "dir": blobStoreDir()},
		"pendingExpiry":       map[string]any{"attachments": pendingCount, "bytes": classes["pendingExpiry"].Bytes},
		"expiredPlaceholders": expiredCount,
		"disk":                map[string]any{"usage": usage, "pressure": pressure},
		"retention":           currentChatMediaRetentionSettings(),
	}
	lastChatMediaRetentionReport.mu.Lock()
	if lastChatMediaRetentionReport.ok {
		report["lastPass"] = lastChatMediaRetentionReport.report
	}
	lastChatMediaRetentionReport.mu.Unlock()
	return report, nil
}

// adminStorageHandler serves GET /assistant/admin/storage — founder-owner only.
func adminStorageHandler(w http.ResponseWriter, r *http.Request) {
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
	if !isFounderOwner(user) {
		writeAuthError(w, http.StatusForbidden, "the storage report is owner-only")
		return
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "storage report is unavailable")
		return
	}
	report, err := kanbanApp.storageReport()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, report)
}
