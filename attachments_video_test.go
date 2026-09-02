package main

// Hotfix gen 249 — photos and videos as chat attachments. The upload
// allowlist admits the phone/desktop video containers (mp4 / quicktime /
// webm) behind a container-header check, stamps image dimensions on the
// upload response so the feed can reserve the card's aspect box, keeps every
// video OUT of both model lanes, prints a video's duration on the model line,
// and serves video blobs with byte-range support so Safari's <video> plays.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tinyMP4(brand string) []byte {
	// A bare ISO BMFF `ftyp` box (size 20, major brand, minor version, one
	// compatible brand) is enough for the container check; no media follows.
	box := []byte{0, 0, 0, 20}
	box = append(box, "ftyp"...)
	box = append(box, brand...)
	box = append(box, 0, 0, 0, 0)
	box = append(box, brand...)
	return append(box, bytes.Repeat([]byte{0}, 64)...)
}

func tinyWebM() []byte {
	return append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01}, bytes.Repeat([]byte{0}, 64)...)
}

func TestAttachmentVideoAllowlistAndContainerValidation(t *testing.T) {
	for _, mime := range []string{"video/mp4", "video/quicktime", "video/webm"} {
		if !attachmentUploadSafeMimes[mime] {
			t.Fatalf("%s must be upload-safe", mime)
		}
		if attachmentModelSafeMimes[mime] || openAIAttachmentModelSafeMimes[mime] {
			t.Fatalf("%s must never be model-safe", mime)
		}
		if !blobInlineSafeMimes[mime] {
			t.Fatalf("%s must serve inline for <video>", mime)
		}
	}
	if err := validateAttachmentBytes("video/mp4", tinyMP4("isom")); err != nil {
		t.Fatalf("isom mp4 rejected: %v", err)
	}
	if err := validateAttachmentBytes("video/quicktime", tinyMP4("qt  ")); err != nil {
		t.Fatalf("qt mov rejected: %v", err)
	}
	if err := validateAttachmentBytes("video/webm", tinyWebM()); err != nil {
		t.Fatalf("webm rejected: %v", err)
	}
	if err := validateAttachmentBytes("video/mp4", []byte("<!doctype html><script>alert(1)</script>")); err == nil {
		t.Fatal("html bytes declared as mp4 must be rejected")
	}
	if err := validateAttachmentBytes("video/webm", tinyMP4("isom")); err == nil {
		t.Fatal("mp4 bytes declared as webm must be rejected")
	}
	// Native pickers often declare octet-stream; the container sniff decides.
	if got, err := resolveAttachmentUploadMime("application/octet-stream", tinyMP4("qt  ")); err != nil || got != "video/quicktime" {
		t.Fatalf("octet-stream quicktime resolved to %q (%v), want video/quicktime", got, err)
	}
	if got, err := resolveAttachmentUploadMime("", tinyWebM()); err != nil || got != "video/webm" {
		t.Fatalf("undeclared webm resolved to %q (%v), want video/webm", got, err)
	}
	if got, err := resolveAttachmentUploadMime("video/x-m4v", tinyMP4("M4V ")); err != nil || got != "video/mp4" {
		t.Fatalf("x-m4v alias resolved to %q (%v), want video/mp4", got, err)
	}
	// Unsupported media (audio, svg, html) stay out.
	for _, declared := range []string{"audio/mpeg", "image/svg+xml", "text/html", "video/x-flv"} {
		if _, err := resolveAttachmentUploadMime(declared, tinyMP4("isom")); err == nil {
			t.Fatalf("%s must stay unsupported", declared)
		}
	}
}

func TestAttachmentImageDimensionsRideTheUploadResponse(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	owner := accountStore().findUser("aj@shareability.com")
	destination, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Video attachment test", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	upload := func(mime string, body []byte) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/assistant/attachments?threadId="+destination.ID, bytes.NewReader(body))
		req.Header.Set("Content-Type", mime)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		assistantAttachmentUploadHandler(recorder, req)
		payload := map[string]any{}
		_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
		return recorder.Code, payload
	}

	if code, payload := upload("image/png", tinyPNG(t)); code != http.StatusOK || payload["width"] == nil || payload["height"] == nil {
		t.Fatalf("png upload status=%d payload=%v, want 200 with width/height", code, payload)
	} else if w, h := payload["width"].(float64), payload["height"].(float64); w < 1 || h < 1 {
		t.Fatalf("png dimensions %vx%v, want positive", w, h)
	}
	if code, payload := upload("video/mp4", tinyMP4("isom")); code != http.StatusOK || payload["mime"] != "video/mp4" {
		t.Fatalf("mp4 upload status=%d payload=%v, want 200 video/mp4", code, payload)
	}
	if code, payload := upload("video/quicktime", tinyMP4("qt  ")); code != http.StatusOK || payload["mime"] != "video/quicktime" {
		t.Fatalf("mov upload status=%d payload=%v, want 200 video/quicktime", code, payload)
	}
	if code, _ := upload("video/mp4", []byte("not a container at all, just prose")); code != http.StatusUnsupportedMediaType {
		t.Fatalf("prose-as-mp4 status=%d, want 415", code)
	}
}

func TestAttachmentVideoNeverReachesAModelAndPrintsDuration(t *testing.T) {
	video := scoutChatFileAttachment{Name: "walkthrough.mov", Kind: "quicktime", Size: 4096, Ref: strings.Repeat("ab", 32), Mime: "video/quicktime", Duration: 62.4}
	read := func(scoutChatFileAttachment) ([]byte, blobMeta, bool) {
		return tinyMP4("qt  "), blobMeta{Mime: "video/quicktime", Size: 4096}, true
	}
	if blocks := attachmentContentBlocksWithReader([]scoutChatFileAttachment{video}, read); len(blocks) != 0 {
		t.Fatalf("anthropic lane forwarded a video: %d blocks", len(blocks))
	}
	if content := openAIAttachmentContentWithReader([]scoutChatFileAttachment{video}, read); len(content) != 0 {
		t.Fatalf("openai lane forwarded a video: %d parts", len(content))
	}
	text := scoutChatMessageModelText(scoutChatMessageRecord{Text: "watch this", Files: []scoutChatFileAttachment{video}})
	if !strings.Contains(text, "Attached file: walkthrough.mov (quicktime, 4096 bytes, video 1:02).") {
		t.Fatalf("model line missing the name/size/duration summary: %q", text)
	}

	// Hints are bounded and cleared for non-media files.
	if w, h, d := sanitizeScoutChatFileMediaHints("video/mp4", scoutChatFileAttachment{Width: 1920, Height: 1080, Duration: 12.34}); w != 1920 || h != 1080 || d != 12.3 {
		t.Fatalf("video hints = %d x %d %v", w, h, d)
	}
	if w, h, d := sanitizeScoutChatFileMediaHints("image/gif", scoutChatFileAttachment{Width: 480, Height: 270, Duration: 5}); w != 480 || h != 270 || d != 0 {
		t.Fatalf("gif hints = %d x %d %v (an image carries no duration)", w, h, d)
	}
	if w, h, d := sanitizeScoutChatFileMediaHints("application/pdf", scoutChatFileAttachment{Width: 480, Height: 270, Duration: 5}); w != 0 || h != 0 || d != 0 {
		t.Fatalf("pdf hints = %d x %d %v, want none", w, h, d)
	}
	if w, h, _ := sanitizeScoutChatFileMediaHints("image/png", scoutChatFileAttachment{Width: 50_000, Height: 10}); w != 0 || h != 0 {
		t.Fatalf("absurd dimensions survived: %d x %d", w, h)
	}
	if label := scoutChatFileDurationLabel(3725); label != "1:02:05" {
		t.Fatalf("duration label = %q", label)
	}
}

func TestArtifactBlobRouteServesVideoByteRanges(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })

	videoBytes := tinyMP4("isom")
	ref, err := putBlob(videoBytes, "video/mp4")
	if err != nil {
		t.Fatalf("putBlob video: %v", err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("design", "video blob fixture", "Authorized assets", "AJ", map[string]string{"visibility": "organization"})
	if err != nil {
		t.Fatalf("createOSArtifactWithMetadata: %v", err)
	}
	if _, err := kanbanApp.appendArtifactAsset(artifact.ID, artifactAsset{Ref: ref, Mime: "video/mp4", Kind: "export"}); err != nil {
		t.Fatalf("append video asset: %v", err)
	}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	get := func(headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/artifacts/blob?ref="+ref+"&name=clip.mp4", nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		recorder := httptest.NewRecorder()
		artifactBlobHandler(recorder, req)
		return recorder
	}

	full := get(nil)
	if full.Code != http.StatusOK || full.Body.Len() != len(videoBytes) {
		t.Fatalf("full GET status=%d len=%d, want 200/%d", full.Code, full.Body.Len(), len(videoBytes))
	}
	if got := full.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges=%q, want bytes (Safari refuses <video> without it)", got)
	}
	if got := full.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type=%q, want video/mp4", got)
	}
	if !strings.HasPrefix(full.Header().Get("Content-Disposition"), "inline;") {
		t.Fatalf("Content-Disposition=%q, want inline", full.Header().Get("Content-Disposition"))
	}
	if got := full.Header().Get("Cache-Control"); got != blobCacheControl {
		t.Fatalf("Cache-Control=%q, want %q", got, blobCacheControl)
	}

	partial := get(map[string]string{"Range": "bytes=4-7"})
	if partial.Code != http.StatusPartialContent || partial.Body.String() != "ftyp" {
		t.Fatalf("range GET status=%d body=%q, want 206 %q", partial.Code, partial.Body.String(), "ftyp")
	}
	if got := partial.Header().Get("Content-Range"); got == "" {
		t.Fatal("range GET missing Content-Range")
	}

	cached := get(map[string]string{"If-None-Match": `"` + ref + `"`})
	if cached.Code != http.StatusNotModified {
		t.Fatalf("conditional GET status=%d, want 304", cached.Code)
	}
}
