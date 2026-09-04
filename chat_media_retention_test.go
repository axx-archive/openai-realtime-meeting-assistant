package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func setupChatMediaRetentionTest(t *testing.T) *kanbanBoardApp {
	t.Helper()
	setupAuthTestEnv(t)
	t.Setenv(chatMediaRetentionDaysEnv, "")
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousApp := kanbanApp
	app := newIsolatedKanbanBoardApp(t)
	kanbanApp = app
	t.Cleanup(func() { kanbanApp = previousApp })
	previousUsage := chatMediaDiskUsage
	chatMediaDiskUsage = func() (float64, error) { return 0.5, nil }
	t.Cleanup(func() { chatMediaDiskUsage = previousUsage })
	return app
}

// seedChatMedia posts one human message carrying a ref'd attachment whose
// body lives in the blob store, dated ageDays ago. Returns the ref.
func seedChatMedia(t *testing.T, app *kanbanBoardApp, threadID string, messageID string, name string, mime string, body []byte, ageDays int) string {
	t.Helper()
	thread, _, err := app.ensureScoutChatThread(threadID, "aj@shareability.com", "AJ", "packaging", scoutChatVisibilityPublic, nil)
	if err != nil {
		t.Fatalf("ensure thread: %v", err)
	}
	ref, err := putBlob(body, mime)
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	message := scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", AuthorEmail: "aj@shareability.com", AuthorName: "AJ",
		Text:      "sharing " + name,
		CreatedAt: time.Now().UTC().Add(-time.Duration(ageDays) * 24 * time.Hour).Format(time.RFC3339Nano),
		Files:     []scoutChatFileAttachment{{Name: name, Mime: mime, Ref: ref, Size: int64(len(body)), Text: "ingestion caption for " + name}},
	}
	// Committing a ref'd attachment demands a composer grant; the retention
	// pass reads persisted records, so seed the message directly.
	current, _, err := app.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	current.Messages = append(current.Messages, message)
	if err := app.saveScoutChatThread(current); err != nil {
		t.Fatalf("save seeded message: %v", err)
	}
	return ref
}

func chatMediaAttachment(t *testing.T, app *kanbanBoardApp, threadID, messageID string) (scoutChatMessageRecord, scoutChatFileAttachment) {
	t.Helper()
	thread, _, err := app.scoutChatThreadByID("aj@shareability.com", threadID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	index := scoutChatMessageIndex(thread, messageID)
	if index < 0 || len(thread.Messages[index].Files) != 1 {
		t.Fatalf("message %s missing or attachment count wrong", messageID)
	}
	return thread.Messages[index], thread.Messages[index].Files[0]
}

func blobBodyExists(ref string) bool {
	dataPath, _, err := blobPaths(ref)
	if err != nil {
		return false
	}
	_, statErr := os.Stat(dataPath)
	return statErr == nil
}

func TestChatMediaRetentionMarksThenDeletesKeepingTextAndCaption(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	ref := seedChatMedia(t, app, "media-expiry", "media-expiry-msg", "photo.png", "image/png", []byte("png bytes old"), 100)
	fresh := seedChatMedia(t, app, "media-expiry", "media-fresh-msg", "recent.png", "image/png", []byte("png bytes fresh"), 10)
	now := time.Now().UTC()

	first, err := app.sweepChatMediaRetentionOnce(now)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.Marked != 1 || first.Expired != 0 || first.Deleted != 0 {
		t.Fatalf("first pass=%+v, want exactly one mark and no deletion", first)
	}
	_, file := chatMediaAttachment(t, app, "media-expiry", "media-expiry-msg")
	if file.ExpiresAt == "" || file.Expired || !blobBodyExists(ref) {
		t.Fatalf("first sighting attachment=%+v bodyExists=%v, want expiresAt stamped and the body kept", file, blobBodyExists(ref))
	}
	stamped := parseRFC3339OrZero(file.ExpiresAt)
	if stamped.Sub(now) < chatMediaExpiryGrace-time.Minute {
		t.Fatalf("expiresAt=%s, want ~7 days out", file.ExpiresAt)
	}
	if _, freshFile := chatMediaAttachment(t, app, "media-expiry", "media-fresh-msg"); freshFile.ExpiresAt != "" {
		t.Fatalf("fresh attachment marked: %+v", freshFile)
	}

	// Inside the grace window: nothing changes.
	second, _ := app.sweepChatMediaRetentionOnce(now.Add(2 * 24 * time.Hour))
	if second.Marked != 0 || second.Expired != 0 || second.Deleted != 0 || !blobBodyExists(ref) {
		t.Fatalf("grace-window pass=%+v", second)
	}

	// Second sighting past the stamp: expired + body deleted; text intact.
	third, err := app.sweepChatMediaRetentionOnce(now.Add(8 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if third.Expired != 1 || third.Deleted != 1 || third.DeletedBytes != int64(len("png bytes old")) {
		t.Fatalf("third pass=%+v, want one expiry and one deleted body", third)
	}
	message, file := chatMediaAttachment(t, app, "media-expiry", "media-expiry-msg")
	if !file.Expired || file.Ref != ref || file.Text != "ingestion caption for photo.png" || message.Text != "sharing photo.png" {
		t.Fatalf("expired attachment=%+v message=%q, want the record and caption intact", file, message.Text)
	}
	if blobBodyExists(ref) {
		t.Fatal("expired body still on disk")
	}
	if !blobBodyExists(fresh) {
		t.Fatal("fresh body deleted")
	}
	referenced, err := blobReferencedRefs(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, still := referenced[ref]; still {
		t.Fatal("expired attachment still counts as a blob reference")
	}
	if _, live := referenced[fresh]; !live {
		t.Fatal("live attachment dropped from the blob reference walk")
	}
	// The daily sweeper drives the pass.
	if _, err := app.sweepChatMediaRetentionOnce(now.Add(9 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	app.sweepFileTrashOnce(now.Add(9 * 24 * time.Hour))
	if _, file := chatMediaAttachment(t, app, "media-expiry", "media-expiry-msg"); !file.Expired {
		t.Fatal("sweeper pass lost the expired state")
	}
}

func TestChatMediaRetentionSavedToDriveOrKeptNeverExpires(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	saved := seedChatMedia(t, app, "media-keep", "media-saved-msg", "saved.png", "image/png", []byte("saved to drive body"), 200)
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-saved-copy", "saved.png", map[string]string{
		"blobRef": saved, "size": fmt.Sprint(len("saved to drive body")), "uploadedBy": "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	kept := seedChatMedia(t, app, "media-keep", "media-kept-msg", "kept.png", "image/png", []byte("kept body"), 200)
	thread, _, _ := app.scoutChatThreadByID("aj@shareability.com", "media-keep")
	thread.Messages[scoutChatMessageIndex(thread, "media-kept-msg")].Files[0].Keep = true
	if err := app.saveScoutChatThread(thread); err != nil {
		t.Fatal(err)
	}
	for day := 0; day < 30; day += 10 {
		report, err := app.sweepChatMediaRetentionOnce(time.Now().UTC().Add(time.Duration(day) * 24 * time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if report.Marked != 0 || report.Expired != 0 || report.Deleted != 0 {
			t.Fatalf("day %d report=%+v, want nothing touched", day, report)
		}
	}
	for _, id := range []string{"media-saved-msg", "media-kept-msg"} {
		if _, file := chatMediaAttachment(t, app, "media-keep", id); file.ExpiresAt != "" || file.Expired {
			t.Fatalf("%s attachment=%+v, want permanent", id, file)
		}
	}
	if !blobBodyExists(saved) || !blobBodyExists(kept) {
		t.Fatal("permanent body deleted")
	}
	// Marked, then saved to Drive before the second sighting: the mark clears.
	late := seedChatMedia(t, app, "media-keep", "media-late-msg", "late.png", "image/png", []byte("late save body"), 200)
	if report, _ := app.sweepChatMediaRetentionOnce(time.Now().UTC()); report.Marked != 1 {
		t.Fatalf("late report=%+v", report)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-late-copy", "late.png", map[string]string{"blobRef": late, "size": "14"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.sweepChatMediaRetentionOnce(time.Now().UTC().Add(10 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, file := chatMediaAttachment(t, app, "media-keep", "media-late-msg"); file.ExpiresAt != "" || file.Expired || !blobBodyExists(late) {
		t.Fatalf("late-saved attachment=%+v bodyExists=%v, want the mark cleared and the body kept", file, blobBodyExists(late))
	}
}

func TestChatMediaRetentionVideosExpireEarlyUnderDiskPressure(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	video := seedChatMedia(t, app, "media-pressure", "media-video-msg", "clip.mp4", "video/mp4", []byte("video bytes"), 40)
	image := seedChatMedia(t, app, "media-pressure", "media-image-msg", "still.png", "image/png", []byte("image bytes"), 40)
	report, err := app.sweepChatMediaRetentionOnce(time.Now().UTC())
	if err != nil || report.DiskPressure || report.Marked != 0 {
		t.Fatalf("no-pressure report=%+v err=%v, want nothing marked at 40 days", report, err)
	}
	chatMediaDiskUsage = func() (float64, error) { return 0.9, nil }
	report, err = app.sweepChatMediaRetentionOnce(time.Now().UTC())
	if err != nil || !report.DiskPressure || report.Marked != 1 {
		t.Fatalf("pressure report=%+v err=%v, want only the 40-day video marked", report, err)
	}
	if _, file := chatMediaAttachment(t, app, "media-pressure", "media-video-msg"); file.ExpiresAt == "" {
		t.Fatalf("video attachment=%+v, want expiresAt under pressure", file)
	}
	if _, file := chatMediaAttachment(t, app, "media-pressure", "media-image-msg"); file.ExpiresAt != "" {
		t.Fatalf("image attachment=%+v, want untouched (90-day rule)", file)
	}
	// Pressure relieved before the second sighting: the mark clears.
	chatMediaDiskUsage = func() (float64, error) { return 0.4, nil }
	if _, err := app.sweepChatMediaRetentionOnce(time.Now().UTC().Add(8 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, file := chatMediaAttachment(t, app, "media-pressure", "media-video-msg"); file.ExpiresAt != "" || file.Expired || !blobBodyExists(video) {
		t.Fatalf("video after relief=%+v, want the mark cleared", file)
	}
	_ = image
	settings := currentChatMediaRetentionSettings()
	if settings.RetentionDays != 90 || settings.VideoPressureDays != 30 || settings.DiskPressureThreshold != 0.8 || settings.GraceDays != 7 {
		t.Fatalf("settings=%+v", settings)
	}
	t.Setenv(chatMediaRetentionDaysEnv, "45")
	if chatMediaRetentionDays() != 45 {
		t.Fatalf("retention days env not honoured: %d", chatMediaRetentionDays())
	}
}

func TestChatMediaExpiredPlaceholderPinnedInAttachmentRenderer(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start := strings.Index(source, "function scoutChatFilesNode(files)")
	if start < 0 {
		t.Fatal("attachment renderer missing")
	}
	renderer := source[start:min(len(source), start+4000)]
	for _, want := range []string{"if (file?.expired)", "scout-chat-file--expired", "'expired · not saved to Drive'"} {
		if !strings.Contains(renderer, want) {
			t.Fatalf("attachment renderer missing %q", want)
		}
	}
	if strings.Index(renderer, "if (file?.expired)") > strings.Index(renderer, "const blobHref = ref") {
		t.Fatal("expired placeholder must short-circuit before the blob href is built")
	}
	// The generated-image lane expires the same way (message.image.expired,
	// scout_chat_images.go) and must not render a broken <img> beside a live
	// save/regenerate control. Slicing only scoutChatFilesNode let that half
	// of the retention contract go unimplemented.
	imageStart := strings.Index(source, "function scoutChatImageNode(message)")
	if imageStart < 0 {
		t.Fatal("image renderer missing")
	}
	image := source[imageStart:min(len(source), imageStart+4000)]
	for _, want := range []string{"if (image.expired)", "scout-chat-file--expired", "'expired · not saved to Drive'"} {
		if !strings.Contains(image, want) {
			t.Fatalf("image renderer missing %q", want)
		}
	}
	for _, live := range []string{"img.src = artifactBlobUrl(", "scoutChatImageSaveControl(image)"} {
		at := strings.Index(image, live)
		if at < 0 {
			t.Fatalf("image renderer no longer builds %q", live)
		}
		if strings.Index(image, "if (image.expired)") > at {
			t.Fatalf("expired placeholder must short-circuit before %q", live)
		}
	}
}

func TestAdminStorageReportIsFounderOnlyAndClassifiesBytes(t *testing.T) {
	app := setupChatMediaRetentionTest(t)
	chat := seedChatMedia(t, app, "media-report", "media-report-msg", "shared.png", "image/png", []byte("chat only body"), 1)
	upload, err := putBlob([]byte("drive upload body!"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.memory.appendEntry(meetingMemoryKindFile, "file-report-upload", "upload.txt", map[string]string{"blobRef": upload, "size": "18"}); err != nil {
		t.Fatal(err)
	}
	founder := loginAs(t, founderOwnerEmail, "B0NFIRE!")
	member := loginAs(t, "joel@shareability.com", "B0NFIRE!")
	call := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/assistant/admin/storage", nil)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		adminStorageHandler(recorder, request)
		return recorder
	}
	if response := call(member); response.Code != http.StatusForbidden {
		t.Fatalf("member status=%d, want 403", response.Code)
	}
	if response := call(nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d, want 401", response.Code)
	}
	response := call(founder)
	if response.Code != http.StatusOK {
		t.Fatalf("founder status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{`"uploads":{"bytes":18,"count":1}`, `"chatMedia":{"bytes":14,"count":1}`, `"recordings"`, `"deliverables"`, `"pendingExpiry"`, `"retentionDays":90`, `"videoPressureDays":30`, `"diskPressureThreshold":0.8`, `"disk":{`} {
		if !strings.Contains(body, want) {
			t.Fatalf("storage report missing %s: %s", want, body)
		}
	}
	_ = chat
}
