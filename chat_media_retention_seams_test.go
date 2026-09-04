package main

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// webpVP8XHeader builds the 30-byte RIFF/WEBP/VP8X prefix carrying a canvas
// size, which the format stores MINUS ONE as 24-bit little-endian.
func webpVP8XHeader(width int, height int) []byte {
	data := make([]byte, 30)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], 22)
	copy(data[8:12], "WEBP")
	copy(data[12:16], "VP8X")
	binary.LittleEndian.PutUint32(data[16:20], 10)
	putUint24 := func(at int, value int) {
		data[at] = byte(value & 0xFF)
		data[at+1] = byte((value >> 8) & 0xFF)
		data[at+2] = byte((value >> 16) & 0xFF)
	}
	putUint24(24, width-1)
	putUint24(27, height-1)
	return data
}

// The VP8X canvas parse must survive a low byte of 0xFF. `1 + a | b<<8` groups
// as `(1+a) | b<<8` in Go (both operators share precedence), which silently
// halved every size of the form 256·k — the feed then reserved a 1:2 box for a
// square image, the exact collapsed-card symptom the dimension hints were
// added to fix.
func TestWebPVP8XCanvasSizeCarriesTheLowByte(t *testing.T) {
	for _, want := range []struct{ width, height int }{
		{512, 512}, {768, 1024}, {1024, 768}, {256, 256}, {1, 1}, {12000, 9000}, {255, 255}, {513, 257},
	} {
		width, height := webpDimensions(webpVP8XHeader(want.width, want.height))
		if width != want.width || height != want.height {
			t.Fatalf("VP8X %dx%d decoded as %dx%d", want.width, want.height, width, height)
		}
		if gotWidth, gotHeight := attachmentImageDimensions("image/webp", webpVP8XHeader(want.width, want.height)); gotWidth != want.width || gotHeight != want.height {
			t.Fatalf("attachmentImageDimensions %dx%d reported %dx%d", want.width, want.height, gotWidth, gotHeight)
		}
	}
	// The other two layouts stay intact.
	vp8l := make([]byte, 30)
	copy(vp8l[0:4], "RIFF")
	copy(vp8l[8:12], "WEBP")
	copy(vp8l[12:16], "VP8L")
	binary.LittleEndian.PutUint32(vp8l[21:25], uint32(511)|uint32(383)<<14)
	if width, height := webpDimensions(vp8l); width != 512 || height != 384 {
		t.Fatalf("VP8L decoded as %dx%d, want 512x384", width, height)
	}
	vp8 := make([]byte, 30)
	copy(vp8[0:4], "RIFF")
	copy(vp8[8:12], "WEBP")
	copy(vp8[12:16], "VP8 ")
	vp8[26], vp8[27] = byte(640&0xFF), byte(640>>8)
	vp8[28], vp8[29] = byte(480&0xFF), byte(480>>8)
	if width, height := webpDimensions(vp8); width != 640 || height != 480 {
		t.Fatalf("VP8 decoded as %dx%d, want 640x480", width, height)
	}
	if width, height := webpDimensions([]byte("not a webp file at all......")); width != 0 || height != 0 {
		t.Fatalf("garbage decoded as %dx%d, want 0x0", width, height)
	}
}

// pendingAttachmentGrantStates reports the state of every composer grant still
// on file for one ref — the store the mention audit reads, and the one
// initializeAttachmentSourceStore deliberately never prunes.
func pendingAttachmentGrantStates(app *kanbanBoardApp, ref string) []string {
	app.pendingAttachmentUploadsMu.Lock()
	defer app.pendingAttachmentUploadsMu.Unlock()
	states := []string{}
	for _, grant := range app.pendingAttachmentUploads {
		if grant.Ref == strings.TrimSpace(ref) {
			states = append(states, grant.State)
		}
	}
	return states
}

// commitTestChatAttachment posts one real committed attachment (grant, ref and
// destination revision all genuine) and back-dates the message so the
// retention pass can act on it.
func commitTestChatAttachment(t *testing.T, app *kanbanBoardApp, owner *userAccount, thread scoutChatThreadRecord, messageID string, name string, body []byte, ageDays int) (string, scoutChatThreadRecord) {
	t.Helper()
	ref, err := putBlob(body, "image/png")
	if err != nil {
		t.Fatalf("put source: %v", err)
	}
	reservationID := messageID + "-reservation"
	file := reserveTestAttachment(t, app, owner, thread, scoutChatFileAttachment{Name: name, Kind: "png", Ref: ref}, reservationID)
	file.Text = "ingestion caption for " + name
	message := scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: "sharing " + name,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: owner.Name, AuthorEmail: owner.Email,
		Files:                         []scoutChatFileAttachment{file},
		attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(thread),
		attachmentReservationID:       reservationID,
	}
	if _, err := app.commitScoutChatThreadMessages(owner.Email, thread.ID, message); err != nil {
		t.Fatalf("commit attachment: %v", err)
	}
	current, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	index := scoutChatMessageIndex(current, messageID)
	if index < 0 {
		t.Fatalf("committed message %s missing", messageID)
	}
	current.Messages[index].CreatedAt = time.Now().UTC().Add(-time.Duration(ageDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(current); err != nil {
		t.Fatalf("back-date message: %v", err)
	}
	return ref, current
}

// An attachment whose body retention deleted must still reach the reader as an
// expired placeholder. Before this fix the vanished body failed the projection's
// blob stat, the whole file record was stripped, and the message rendered with
// no chip at all — the "expired · not saved to Drive" branch could never fire
// and the permanently-retained caption became unreachable.
func TestExpiredChatAttachmentProjectsPlaceholderNotSilence(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	owner := accountStore().findUser("aj@shareability.com")
	viewer := accountStore().findUser("tim@shareability.com")
	if owner == nil || viewer == nil {
		t.Fatal("seed users missing")
	}
	thread, err := app.createScoutChatThread(owner.Email, owner.Name, "expiry placeholder", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	ref, _ := commitTestChatAttachment(t, app, owner, thread, "expiry-placeholder-msg", "photo.png", tinyPNG(t), 100)

	loaded, _, err := app.scoutChatThreadByID(viewer.Email, thread.ID)
	if err != nil {
		t.Fatalf("load thread: %v", err)
	}
	before := app.projectScoutChatThreadForViewer(viewer.Email, loaded)
	if len(before.Messages) != 1 || len(before.Messages[0].Files) != 1 || before.Messages[0].Files[0].Ref != ref {
		t.Fatalf("pre-expiry projection=%+v, want the live attachment visible", before.Messages)
	}

	now := time.Now().UTC()
	if report, err := app.sweepChatMediaRetentionOnce(now); err != nil || report.Marked != 1 {
		t.Fatalf("first pass report=%+v err=%v, want one mark", report, err)
	}
	if report, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour)); err != nil || report.Expired != 1 || report.Deleted != 1 {
		t.Fatalf("second pass report=%+v err=%v, want one expiry and one deleted body", report, err)
	}
	if blobBodyExists(ref) {
		t.Fatal("expired body still on disk")
	}

	loaded, _, err = app.scoutChatThreadByID(viewer.Email, thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	projected := app.projectScoutChatThreadForViewer(viewer.Email, loaded)
	if len(projected.Messages) != 1 || len(projected.Messages[0].Files) != 1 {
		t.Fatalf("expired projection=%+v, want exactly one placeholder file", projected.Messages)
	}
	placeholder := projected.Messages[0].Files[0]
	if !placeholder.Expired {
		t.Fatalf("placeholder=%+v, want expired=true so the client renders 'expired · not saved to Drive'", placeholder)
	}
	if placeholder.Name != "photo.png" || placeholder.Text != "ingestion caption for photo.png" {
		t.Fatalf("placeholder=%+v, want the name and the permanently-retained caption kept", placeholder)
	}
	if placeholder.Ref != "" || placeholder.Mime != "" || placeholder.SourceID != "" || placeholder.SourceRevision != "" {
		t.Fatalf("placeholder=%+v, want every byte-pointing field stripped", placeholder)
	}
	if projected.Messages[0].Text != "sharing photo.png" {
		t.Fatalf("message text=%q, want the message intact", projected.Messages[0].Text)
	}

	// The placeholder is still gated: revoking the grant removes the record
	// entirely, exactly as it does for a live attachment.
	persisted, _, err := app.scoutChatThreadByID(owner.Email, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := persisted.Messages[scoutChatMessageIndex(persisted, "expiry-placeholder-msg")].Files[0].SourceID
	if err := app.revokeAttachmentSource(sourceID); err != nil {
		t.Fatalf("revoke source: %v", err)
	}
	revoked := app.projectScoutChatThreadForViewer(viewer.Email, loaded)
	if len(revoked.Messages) != 1 || len(revoked.Messages[0].Files) != 0 {
		t.Fatalf("revoked projection=%+v, want the placeholder gone too", revoked.Messages)
	}
}

// A failed thread save must never let the delete loop reclaim the body: the
// persisted record still says the attachment is live, the reference walk still
// counts it, and the viewer would get a chip pointing at a 404.
func TestChatMediaRetentionKeepsBodyWhenThreadSaveFails(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	ref := seedChatMedia(t, app, "media-save-fail", "media-save-fail-msg", "doomed.png", "image/png", []byte("save failure body"), 100)
	now := time.Now().UTC()
	if report, err := app.sweepChatMediaRetentionOnce(now); err != nil || report.Marked != 1 {
		t.Fatalf("first pass report=%+v err=%v", report, err)
	}

	previousSave := chatMediaRetentionSaveThread
	chatMediaRetentionSaveThread = func(*kanbanBoardApp, scoutChatThreadRecord) error {
		return fmt.Errorf("no space left on device")
	}
	t.Cleanup(func() { chatMediaRetentionSaveThread = previousSave })

	report, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("expiry pass: %v", err)
	}
	if report.Deleted != 0 || report.DeletedBytes != 0 {
		t.Fatalf("report=%+v, want no body deleted when the record could not be persisted", report)
	}
	if report.Expired != 0 {
		t.Fatalf("report=%+v, want the expiry counter to follow the save, not the in-memory flip", report)
	}
	if !blobBodyExists(ref) {
		t.Fatal("body deleted although the expired record was never persisted")
	}
	if _, file := chatMediaAttachment(t, app, "media-save-fail", "media-save-fail-msg"); file.Expired {
		t.Fatalf("attachment=%+v, want the persisted record still live", file)
	}
	referenced, err := blobReferencedRefs(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, live := referenced[ref]; !live {
		t.Fatal("the still-live record stopped counting as a blob reference")
	}

	// With the save working again, the next pass completes normally.
	chatMediaRetentionSaveThread = previousSave
	if report, err := app.sweepChatMediaRetentionOnce(now.Add(9 * 24 * time.Hour)); err != nil || report.Expired != 1 || report.Deleted != 1 {
		t.Fatalf("recovery pass report=%+v err=%v", report, err)
	}
	if blobBodyExists(ref) {
		t.Fatal("recovery pass left the body behind")
	}
}

// The visible expiresAt warning is the prompt for a user to click Save to
// Drive, so a save that lands DURING the pass is the expected race. The
// permanent set must be re-read before the delete loop or the brand-new Drive
// row is born pointing at deleted bytes.
func TestChatMediaRetentionSkipsRefSavedToDriveDuringThePass(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	ref := seedChatMedia(t, app, "media-race", "media-race-msg", "raced.png", "image/png", []byte("raced body"), 100)
	now := time.Now().UTC()
	if report, err := app.sweepChatMediaRetentionOnce(now); err != nil || report.Marked != 1 {
		t.Fatalf("first pass report=%+v err=%v", report, err)
	}

	previousProbe := chatMediaRetentionAfterWalkProbe
	chatMediaRetentionAfterWalkProbe = func() {
		// Save to Drive lands after the pass snapshotted `permanent`.
		if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-raced-copy", "raced.png", map[string]string{
			"blobRef": ref, "name": "raced.png", "size": "10",
		}); err != nil {
			t.Errorf("append Drive row mid-pass: %v", err)
		}
	}
	t.Cleanup(func() { chatMediaRetentionAfterWalkProbe = previousProbe })

	report, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("expiry pass: %v", err)
	}
	if report.Deleted != 0 {
		t.Fatalf("report=%+v, want the raced Drive row's bytes kept", report)
	}
	if !blobBodyExists(ref) {
		t.Fatal("body of a ref saved to Drive during the pass was deleted")
	}
	chatMediaRetentionAfterWalkProbe = previousProbe
	// The Drive row now makes it permanent, so the mark clears on the next pass.
	if _, err := app.sweepChatMediaRetentionOnce(now.Add(9 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, file := chatMediaAttachment(t, app, "media-race", "media-race-msg"); file.Expired || !blobBodyExists(ref) {
		t.Fatalf("attachment=%+v bodyExists=%v, want the saved body permanent", file, blobBodyExists(ref))
	}
}

// A workspace whose blob bytes all live in chat (Scout artifacts with no
// assets, no Drive upload, no recording, no share link) is exactly the shape
// D18 was written for. The reference walk's "rows but no refs" fail-safe used
// to fire there — the chat-excluded walk legitimately collects nothing — so
// the retention pass errored out daily and nothing ever expired.
func TestChatMediaRetentionRunsWhenEveryBlobIsChatMedia(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	for index := 0; index < 3; index++ {
		if _, appended, err := app.memory.appendOSArtifact(fmt.Sprintf("artifact-assetless-%d", index), "# Answer\n\nBody only.", map[string]string{
			"title": "Answer", "mode": "answer",
		}); err != nil || !appended {
			t.Fatalf("append assetless artifact: appended=%v err=%v", appended, err)
		}
	}
	ref := seedChatMedia(t, app, "media-chat-only", "media-chat-only-msg", "only.png", "image/png", []byte("chat only bytes"), 100)

	permanent, err := chatMediaPermanentRefs(app)
	if err != nil {
		t.Fatalf("permanent-ref walk refused a chat-only workspace: %v", err)
	}
	if len(permanent) != 0 {
		t.Fatalf("permanent=%v, want an honest empty set (nothing outside chat holds bytes)", permanent)
	}

	now := time.Now().UTC()
	if report, err := app.sweepChatMediaRetentionOnce(now); err != nil || report.Marked != 1 {
		t.Fatalf("first pass report=%+v err=%v, want the pass to run and mark", report, err)
	}
	if report, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour)); err != nil || report.Expired != 1 || report.Deleted != 1 {
		t.Fatalf("second pass report=%+v err=%v", report, err)
	}
	if blobBodyExists(ref) {
		t.Fatal("chat-only workspace never reclaimed its expired body")
	}

	// The fail-safe still catches a genuinely broken walk: an artifact whose
	// assets metadata is present but no longer decodes claims bytes the walk
	// cannot collect.
	if _, appended, err := app.memory.appendOSArtifact("artifact-broken-assets", "# Deck\n\nBody.", map[string]string{
		"title": "Deck", "mode": "design", artifactAssetsMetadataKey: "{not json at all",
	}); err != nil || !appended {
		t.Fatalf("append broken-assets artifact: appended=%v err=%v", appended, err)
	}
	if _, err := chatMediaPermanentRefs(app); err == nil {
		t.Fatal("a row claiming blob refs the walk cannot collect must refuse the sweep")
	}
}

// The 80% disk-pressure threshold is stated as "used", the number df reports —
// so the arithmetic has to BE df's Use%: used / (used + available). df drops
// the root reserve from both halves rather than ignoring it, so neither
// single-number reading matches it: bavail-as-consumed fired the rule four
// points early (expiring chat video 60 days ahead of policy), and bfree over
// blocks fires it four points late, with the operator staring at df saying 80%
// while the rule sleeps.
func TestChatMediaDiskUsageMatchesDfUsePercent(t *testing.T) {
	const total = 100_000
	const reserved = 5_000 // ext4's default 5% root reserve
	used := 76_000
	free := total - used
	available := free - reserved
	// df: 76 / (76 + 19) = 80%.
	if got := chatMediaUsedFraction(total, uint64(free), uint64(available)); got < 0.7999 || got > 0.8001 {
		t.Fatalf("used fraction=%v, want ~0.80 — exactly what df prints for this volume", got)
	}
	// The regression stays fixed: the old bavail-as-consumed reading was 0.81
	// and tripped the rule; df's 0.80 does not.
	if got := chatMediaUsedFraction(total, uint64(available), uint64(available)); got <= chatMediaDiskPressureThreshold {
		t.Fatalf("the bavail-as-consumed reading is %v — the regression this pins needs it above %v", got, chatMediaDiskPressureThreshold)
	}
	if chatMediaUsedFraction(total, uint64(free), uint64(available)) > chatMediaDiskPressureThreshold {
		t.Fatal("a volume df calls 80% must not trip the >80% video-pressure rule")
	}
	// One more gigabyte and df says 81% — the rule must engage with it.
	oneMore := used + 1_000
	if got := chatMediaUsedFraction(total, uint64(total-oneMore), uint64(total-oneMore-reserved)); got <= chatMediaDiskPressureThreshold {
		t.Fatalf("used fraction=%v at df's 81%%, want the video-pressure rule engaged", got)
	}
	if got := chatMediaUsedFraction(0, 0, 0); got != 0 {
		t.Fatalf("empty statfs=%v, want 0 (no invented pressure)", got)
	}
	if got := chatMediaUsedFraction(total, total+1, 0); got != 0 {
		t.Fatalf("nonsense statfs=%v, want 0", got)
	}
	if got := chatMediaUsedFraction(total, 0, 0); got != 1 {
		t.Fatalf("full volume=%v, want 1", got)
	}
	// A volume that is entirely reserve (nothing used, nothing available to a
	// normal writer) reads as full rather than dividing by zero.
	if got := chatMediaUsedFraction(total, total, 0); got != 1 {
		t.Fatalf("all-reserve volume=%v, want 1", got)
	}
}

// The mention audit is a permanent veto, so it must only fire on sources that
// could still serve bytes. Provenance — a brain-intake transcript stamping the
// blob it was read from, a document import naming its source file, or a chat
// row still carrying an already-expired attachment's ref — is reported but
// never blocks the GC, or those bytes become unreclaimable forever.
func TestBlobSweepReclaimsProvenanceOnlyMentionsAndStillVetoesUnknownKeys(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	// 1. Chat attachment whose body outlived its expired record (a delete that
	//    failed once). The thread row's JSON body still contains the sha, and —
	//    because this goes through the REAL composer path — so does the
	//    committed grant in the attachment source authority, which is never
	//    removed from the store. Seeding the message directly would hide that
	//    second mention and with it the eternal hold it used to create.
	owner := accountStore().findUser("aj@shareability.com")
	if owner == nil {
		t.Fatal("seed user missing")
	}
	provenanceThread, err := app.createScoutChatThread(owner.Email, owner.Name, "provenance chat", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create provenance thread: %v", err)
	}
	expiredRef, thread := commitTestChatAttachment(t, app, owner, provenanceThread, "provenance-chat-msg", "gone.png", []byte("expired chat body"), 200)
	thread.Messages[scoutChatMessageIndex(thread, "provenance-chat-msg")].Files[0].Expired = true
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	if grants := pendingAttachmentGrantStates(app, expiredRef); len(grants) != 1 || grants[0] != attachmentSourceCommitted {
		t.Fatalf("grant states for the committed attachment=%v, want one committed grant still on file", grants)
	}
	// 2. Brain-intake transcript provenance.
	intakeRef, err := putBlob([]byte("slides the brain already read"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindTranscript, "brain-intake-provenance-file-0", "slides.pdf\nTranscribed facts.", map[string]string{
		"blobRef": intakeRef, "attachmentName": "slides.pdf",
	}); err != nil {
		t.Fatal(err)
	}
	// 3. Document-import provenance.
	importedRef, err := putBlob([]byte("the drive file a document was imported from"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendOSArtifact("artifact-imported-doc", "# Imported\n\nBody.", map[string]string{
		"title": "Imported", "mode": "document", "importedFromBlobRef": importedRef,
	}); err != nil || !appended {
		t.Fatalf("append imported artifact: appended=%v err=%v", appended, err)
	}
	// 4. A ref stored under a key the walker does not know: still a veto.
	mysteryRef, err := putBlob([]byte("bytes under an unknown metadata key"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if _, appended, err := app.memory.appendOSArtifact("artifact-mystery-key", "# Deck\n\nBody.", map[string]string{
		"title": "Deck", "mode": "design", "futureSceneRef": mysteryRef,
	}); err != nil || !appended {
		t.Fatalf("append mystery artifact: appended=%v err=%v", appended, err)
	}

	mentions, blocking := blobRefMentions(app, blobRefSet([]string{expiredRef, intakeRef, importedRef, mysteryRef}))
	for _, ref := range []string{expiredRef, intakeRef, importedRef} {
		if len(mentions[ref]) == 0 {
			t.Fatalf("provenance ref %s reported no mention at all; the audit must still SEE it", ref)
		}
		for _, mention := range mentions[ref] {
			if !mention.Provenance {
				t.Fatalf("ref %s mention %+v classified as a live reference", ref, mention)
			}
		}
	}
	if len(mentions[mysteryRef]) != 1 || mentions[mysteryRef][0].Provenance {
		t.Fatalf("mystery mentions=%+v, want one blocking mention", mentions[mysteryRef])
	}
	if _, veto := blocking[mysteryRef]; !veto || len(blocking) != 1 {
		t.Fatalf("blocking=%v, want only the unknown-key ref", blocking)
	}

	// Two weekly sightings: provenance-only refs go, the unknown-key ref stays.
	start := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	if _, ran, err := app.runScheduledBlobSweep(start); err != nil || !ran {
		t.Fatalf("first weekly run ran=%v err=%v", ran, err)
	}
	report, ran, err := app.runScheduledBlobSweep(start.Add(blobSweepInterval))
	if err != nil || !ran {
		t.Fatalf("second weekly run ran=%v err=%v", ran, err)
	}
	deleted := map[string]bool{}
	for _, ref := range report.DeletedRefs {
		deleted[ref] = true
	}
	for _, ref := range []string{expiredRef, intakeRef, importedRef} {
		if !deleted[ref] {
			t.Fatalf("provenance-only ref %s was protected from deletion forever", ref)
		}
	}
	if deleted[mysteryRef] {
		t.Fatal("a ref mentioned under a key the walker does not know was deleted")
	}
	if _, _, err := getBlob(mysteryRef); err != nil {
		t.Fatalf("unknown-key blob deleted: %v", err)
	}
}

// Every other admin surface refuses a cross-origin request before doing any
// work. The GET report arm dispatched ahead of that check, so any page could
// drive a whole-store scan (under the global sweep mutex) on the founder's
// session cookie.
func TestAdminBlobSweepRejectsCrossOriginBeforeDispatch(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	founder := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	call := func(method string, target string, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader("{}"))
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		for _, cookie := range founder {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		adminBlobSweepHandler(recorder, request)
		return recorder
	}
	for _, target := range []string{"/assistant/admin/blobs/sweep?scan=1", "/assistant/admin/blobs/sweep?pending=1"} {
		if recorder := call(http.MethodGet, target, "https://evil.example"); recorder.Code != http.StatusForbidden {
			t.Fatalf("cross-origin GET %s status=%d, want 403", target, recorder.Code)
		}
	}
	if recorder := call(http.MethodPost, "/assistant/admin/blobs/sweep", "https://evil.example"); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST status=%d, want 403", recorder.Code)
	}
	// Same-origin (no Origin header) still works.
	if recorder := call(http.MethodGet, "/assistant/admin/blobs/sweep?pending=1", ""); recorder.Code != http.StatusOK {
		t.Fatalf("same-origin GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// A spent composer grant and a dead share link RECORD an upload; neither can
// serve the bytes. Grants are never pruned (initializeAttachmentSourceStore
// keeps committed and revoked records on purpose) and loadShareLinks returns
// revoked links forever, so treating either mention as a veto pinned those
// bodies for the life of the workspace — attach-then-delete-the-message and
// abandoned composer uploads leaked unbounded disk, the exact bloat D18
// exists to prevent. A LIVE file share link is the opposite: a real serving
// path, and it must still block.
func TestBlobSweepReclaimsSpentGrantAndDeadShareLinkMentions(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	owner := accountStore().findUser("aj@shareability.com")
	if owner == nil {
		t.Fatal("seed user missing")
	}

	// A referenced Drive row keeps the walk's "rows but no refs" fail-safe out
	// of the picture.
	keptRef, err := putBlob([]byte("a drive upload the sweep must keep"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-grant-audit-kept", "File kept.txt uploaded.", map[string]string{
		"name": "kept.txt", "blobRef": keptRef, "mime": "text/plain", "uploaderEmail": "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Abandoned composer upload: granted, then revoked without ever being
	//    sent. Nothing in the workspace can render it.
	abandonedRef, err := putBlob([]byte("composer upload the user never sent"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := app.createScoutChatThread(owner.Email, owner.Name, "abandoned upload", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}
	meta, err := blobStatForRef(abandonedRef)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := app.grantPendingAttachmentUpload(owner, destination, abandonedRef, meta)
	if err != nil {
		t.Fatalf("grant abandoned upload: %v", err)
	}
	if err := app.revokeAttachmentSource(grant.SourceID); err != nil {
		t.Fatalf("revoke abandoned upload: %v", err)
	}
	if states := pendingAttachmentGrantStates(app, abandonedRef); len(states) != 1 || states[0] != attachmentSourceRevoked {
		t.Fatalf("abandoned grant states=%v, want the revoked record still on file", states)
	}

	// 2. A revoked share link whose Drive row is long gone.
	deadLinkRef, err := putBlob([]byte("bytes behind a revoked share link"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	// 3. A LIVE file share link: still a serving path.
	liveLinkRef, err := putBlob([]byte("bytes behind a live share link"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	shareLink := func(id string, ref string, status string) shareLinkRecord {
		return shareLinkRecord{
			ID: id, FileID: "file-" + id, TenantID: canonicalArtifactTenantID(), ObjectType: shareLinkObjectTypeFile,
			ContentDigest: ref, Action: "read_content", Status: status, TokenHash: strings.Repeat("ab", 32),
			CreatedBy: "aj@shareability.com", CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
		}
	}
	if err := saveShareLinks([]shareLinkRecord{
		shareLink("dead-share", deadLinkRef, shareLinkStatusRevoked),
		shareLink("live-share", liveLinkRef, shareLinkStatusActive),
	}); err != nil {
		t.Fatal(err)
	}

	mentions, blocking := blobRefMentions(app, blobRefSet([]string{abandonedRef, deadLinkRef, liveLinkRef}))
	for _, ref := range []string{abandonedRef, deadLinkRef} {
		if len(mentions[ref]) == 0 {
			t.Fatalf("ref %s reported no mention at all; the audit must still SEE it", ref)
		}
		for _, mention := range mentions[ref] {
			if !mention.Provenance {
				t.Fatalf("ref %s mention %+v classified as a lane that can serve bytes", ref, mention)
			}
		}
		if _, veto := blocking[ref]; veto {
			t.Fatalf("ref %s still vetoes the GC forever", ref)
		}
	}
	if _, veto := blocking[liveLinkRef]; !veto {
		t.Fatalf("blocking=%v, want the LIVE file share link to keep vetoing deletion", blocking)
	}

	// The walk itself must agree: a live file link is a reference, a revoked
	// one is not, and neither is a revoked grant.
	referenced, err := blobReferencedRefs(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, live := referenced[liveLinkRef]; !live {
		t.Fatal("the live share link stopped counting as a blob reference")
	}
	for _, ref := range []string{abandonedRef, deadLinkRef} {
		if _, live := referenced[ref]; live {
			t.Fatalf("ref %s is still collected by the reference walk; the audit case is moot", ref)
		}
	}

	// Two weekly sightings: the spent records let their bytes go.
	start := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	if _, ran, err := app.runScheduledBlobSweep(start); err != nil || !ran {
		t.Fatalf("first weekly run ran=%v err=%v", ran, err)
	}
	report, ran, err := app.runScheduledBlobSweep(start.Add(blobSweepInterval))
	if err != nil || !ran {
		t.Fatalf("second weekly run ran=%v err=%v", ran, err)
	}
	deleted := map[string]bool{}
	for _, ref := range report.DeletedRefs {
		deleted[ref] = true
	}
	for _, ref := range []string{abandonedRef, deadLinkRef} {
		if !deleted[ref] {
			t.Fatalf("ref %s was protected from deletion forever (report=%+v)", ref, report)
		}
	}
	for _, ref := range []string{keptRef, liveLinkRef} {
		if deleted[ref] {
			t.Fatalf("ref %s was deleted although something can still serve it", ref)
		}
		if _, _, err := getBlob(ref); err != nil {
			t.Fatalf("still-servable blob %s deleted: %v", ref, err)
		}
	}
}

// blobMentionCap bounds what the audit REPORTS. Once the veto was computed
// from that truncated list, a ref with eight harmless provenance mentions and
// a ninth one under a key the walker does not know read as reclaimable — the
// deck-scene class of bug the audit exists to catch, deleted by the weekly
// job. The cap must never decide a deletion.
func TestBlobMentionCapNeverTruncatesTheDeletionVeto(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	body := []byte("one logo re-shared into every thread")
	var ref string
	for index := 0; index < blobMentionCap; index++ {
		threadID := fmt.Sprintf("cap-thread-%d", index)
		messageID := threadID + "-msg"
		ref = seedChatMedia(t, app, threadID, messageID, "logo.png", "image/png", body, 200)
		thread, _, err := app.scoutChatThreadByID("aj@shareability.com", threadID)
		if err != nil {
			t.Fatal(err)
		}
		thread.Messages[scoutChatMessageIndex(thread, messageID)].Files[0].Expired = true
		if err := app.saveScoutChatThread(thread); err != nil {
			t.Fatal(err)
		}
	}
	// Content addressing is the point: every re-share is the SAME ref.
	mentions, blocking := blobRefMentions(app, blobRefSet([]string{ref}))
	if len(mentions[ref]) != blobMentionCap {
		t.Fatalf("mentions=%d, want the report capped at %d", len(mentions[ref]), blobMentionCap)
	}
	if len(blocking) != 0 {
		t.Fatalf("blocking=%v, want expired-only chat provenance to reclaim", blocking)
	}

	// Now a producer the walker does not know points at the same bytes. Its
	// mention lands past the cap, so it is never reported — but it must still
	// veto.
	if _, appended, err := app.memory.appendOSArtifact("artifact-capped-scene", "# Deck\n\nBody.", map[string]string{
		"title": "Deck", "mode": "design", "futureSceneRef": ref,
	}); err != nil || !appended {
		t.Fatalf("append artifact: appended=%v err=%v", appended, err)
	}
	mentions, blocking = blobRefMentions(app, blobRefSet([]string{ref}))
	if len(mentions[ref]) != blobMentionCap {
		t.Fatalf("mentions=%d, want the report still capped at %d", len(mentions[ref]), blobMentionCap)
	}
	if _, veto := blocking[ref]; !veto {
		t.Fatalf("blocking=%v, want the past-the-cap unknown-key mention to veto deletion", blocking)
	}

	// End to end: the weekly job keeps the bytes the walker gap still claims.
	start := time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC)
	if _, ran, err := app.runScheduledBlobSweep(start); err != nil || !ran {
		t.Fatalf("first weekly run ran=%v err=%v", ran, err)
	}
	report, ran, err := app.runScheduledBlobSweep(start.Add(blobSweepInterval))
	if err != nil || !ran {
		t.Fatalf("second weekly run ran=%v err=%v", ran, err)
	}
	for _, deletedRef := range report.DeletedRefs {
		if deletedRef == ref {
			t.Fatalf("the weekly job deleted bytes an unknown-key producer still points at (report=%+v)", report)
		}
	}
	if !blobBodyExists(ref) {
		t.Fatal("body deleted despite a blocking mention past the report cap")
	}
}

// The Save-to-Drive race has a twin in chat: blobs are content-addressed, so
// re-sending the same GIF/logo/screenshot resolves to the ref this pass is
// about to delete. That new attachment is not in liveChatRefs (built from the
// walk, never recomputed) and its grant is already COMMITTED (so the
// pending-upload lane misses it too), and the pre-delete recheck used to ask
// the chat-EXCLUDED walk — structurally blind to it. The user's just-sent
// photo lost its body seconds after sending.
func TestChatMediaRetentionSkipsRefResharedInChatDuringThePass(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	owner := accountStore().findUser("aj@shareability.com")
	if owner == nil {
		t.Fatal("seed user missing")
	}
	body := []byte("the same screenshot, shared twice")
	oldRef := seedChatMedia(t, app, "media-reshare-old", "media-reshare-old-msg", "shot.png", "image/png", body, 100)
	freshThread, err := app.createScoutChatThread(owner.Email, owner.Name, "re-share destination", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	now := time.Now().UTC()
	if report, err := app.sweepChatMediaRetentionOnce(now); err != nil || report.Marked != 1 {
		t.Fatalf("first pass report=%+v err=%v", report, err)
	}

	previousProbe := chatMediaRetentionAfterWalkProbe
	chatMediaRetentionAfterWalkProbe = func() {
		// A genuine composer send of the SAME bytes, after the walk.
		newRef, _ := commitTestChatAttachment(t, app, owner, freshThread, "media-reshare-new-msg", "shot.png", body, 0)
		if newRef != oldRef {
			t.Errorf("re-share ref=%s, want the content-addressed dedupe to %s", newRef, oldRef)
		}
	}
	t.Cleanup(func() { chatMediaRetentionAfterWalkProbe = previousProbe })

	report, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour))
	chatMediaRetentionAfterWalkProbe = previousProbe
	if err != nil {
		t.Fatalf("expiry pass: %v", err)
	}
	if report.Expired != 1 {
		t.Fatalf("report=%+v, want the old record expired", report)
	}
	if report.Deleted != 0 || !blobBodyExists(oldRef) {
		t.Fatalf("report=%+v bodyExists=%v, want the freshly re-shared body kept", report, blobBodyExists(oldRef))
	}
	// The new message still renders its attachment: body present, record live,
	// and the projection keeps the file rather than dropping it.
	reloaded, _, err := app.scoutChatThreadByID(owner.Email, freshThread.ID)
	if err != nil {
		t.Fatal(err)
	}
	index := scoutChatMessageIndex(reloaded, "media-reshare-new-msg")
	if index < 0 || len(reloaded.Messages[index].Files) != 1 {
		t.Fatalf("re-shared message missing: %+v", reloaded.Messages)
	}
	if file := reloaded.Messages[index].Files[0]; file.Expired || file.Ref != oldRef {
		t.Fatalf("re-shared attachment=%+v, want a live record on the shared ref", file)
	}
	projected := app.projectScoutChatThreadForViewer(owner.Email, reloaded)
	if len(projected.Messages) != 1 || len(projected.Messages[0].Files) != 1 || projected.Messages[0].Files[0].Ref != oldRef {
		t.Fatalf("projection=%+v, want the just-sent photo still visible", projected.Messages)
	}
}

// The fail-safe's whole job is catching a walk that has gone blind. Counting a
// Drive row as "claims bytes" by reading the very key the walk reads made a
// renamed metadata key invisible to BOTH halves — zero claims, zero refs, no
// refusal, and sweepBlobStore would call every blob in the store an orphan.
func TestBlobReferenceWalkRefusesWhenTheDriveBlobKeyIsRenamed(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	previousApp := kanbanApp
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })

	ref, err := putBlob([]byte("a drive upload after a schema rename"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	// The rename: same row, same sha, a key the walk no longer looks under.
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-renamed-key", "File renamed.txt uploaded.", map[string]string{
		"name": "renamed.txt", "contentBlobRef": ref, "mime": "text/plain", "uploaderEmail": "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	if referenced, err := blobReferencedRefs(app); err == nil {
		t.Fatalf("walk returned %d ref(s) instead of refusing; a renamed Drive key must trip the fail-safe", len(referenced))
	}
	if report, err := sweepBlobStore(app, true, nil); err == nil {
		t.Fatalf("sweep ran on a blind walk: %+v", report)
	}

	// The same row under the key the walk knows is an ordinary reference.
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-known-key", "File known.txt uploaded.", map[string]string{
		"name": "known.txt", "blobRef": ref, "mime": "text/plain", "uploaderEmail": "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	referenced, err := blobReferencedRefs(app)
	if err != nil {
		t.Fatalf("walk refused a workspace it can read: %v", err)
	}
	if _, kept := referenced[ref]; !kept {
		t.Fatalf("referenced=%v, want the Drive row's ref collected", referenced)
	}
}

// The retention pass is the third blob-deletion path, and it was the only one
// that skipped the mention audit. Blobs are content-addressed, so bytes a user
// shared in chat and a producer stored under a metadata key the reference walk
// does not collect resolve to the SAME ref: the weekly GC and the admin sweep
// both refuse to delete it (an unknown key is a walker gap, not an orphan),
// while retention reclaimed it at 90 days with no warning and no second
// sighting left to catch the loss.
func TestChatMediaRetentionHonoursTheMentionAuditBeforeDeleting(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	shared := []byte("logo bytes shared in chat and stored by another lane")
	ref := seedChatMedia(t, app, "media-audit", "media-audit-msg", "logo.png", "image/png", shared, 100)
	control := seedChatMedia(t, app, "media-audit", "media-audit-control-msg", "plain.png", "image/png", []byte("chat-only bytes"), 100)
	// futureSceneRef is the deck-scene walker-gap shape: a real producer key
	// blobReferencedRefsWithChat has never heard of.
	if _, appended, err := app.memory.appendOSArtifact("artifact-walker-gap-scene", "# Deck\n\nBody.", map[string]string{
		"title": "Deck", "mode": "design", "futureSceneRef": ref,
	}); err != nil || !appended {
		t.Fatalf("append walker-gap artifact: appended=%v err=%v", appended, err)
	}

	now := time.Now().UTC()
	if _, err := app.sweepChatMediaRetentionOnce(now); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	report, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	// The guard the other two deletion paths share, read at the moment the
	// delete loop reads it: the walker-gap ref is vetoed, and the chat-only
	// ref is reclaimable provenance (its record is persisted expired, so its
	// own row text can no longer serve the bytes).
	_, blocking := blobRefMentions(app, blobRefSet([]string{ref, control}))
	if _, veto := blocking[ref]; !veto {
		t.Fatalf("blocking=%v, want the walker-gap ref vetoed by the audit", blocking)
	}
	if _, veto := blocking[control]; veto {
		t.Fatalf("blocking=%v, want the chat-only ref reclaimable", blocking)
	}
	if !blobBodyExists(ref) {
		t.Fatal("retention deleted a body the weekly GC and the admin sweep both refuse to delete: the mention audit is not consulted before the delete loop")
	}
	if report.Expired != 2 {
		t.Fatalf("report=%+v, want both records expired (the audit protects BYTES, it must not stall the policy)", report)
	}
	if report.Deleted != 1 || blobBodyExists(control) {
		t.Fatalf("report=%+v controlBodyExists=%v, want the chat-only body still reclaimed", report, blobBodyExists(control))
	}
	// The audit is a hold on the bytes, not a heal of the record: the client
	// still draws the expired placeholder, and a later pass must not thrash.
	if _, file := chatMediaAttachment(t, app, "media-audit", "media-audit-msg"); !file.Expired {
		t.Fatalf("attachment=%+v, want the record left expired", file)
	}
	third, err := app.sweepChatMediaRetentionOnce(now.Add(16 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if third.Deleted != 0 || !blobBodyExists(ref) {
		t.Fatalf("third pass=%+v bodyExists=%v, want the held body still held", third, blobBodyExists(ref))
	}
}
