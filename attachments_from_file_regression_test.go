package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type attachmentFileEmailAuthorizer struct {
	email string
}

func (authorizer attachmentFileEmailAuthorizer) AuthorizeArtifactHeader(_ context.Context, user *userAccount, _ ACLAction, _ ArtifactAuthorizationHeader) bool {
	return user != nil && normalizeAccountEmail(user.Email) == normalizeAccountEmail(authorizer.email)
}

func postAttachmentFromFileForTest(t *testing.T, cookies []*http.Cookie, threadID, fileID string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"threadId": threadID, "fileId": fileID})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/assistant/attachments/from-file", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	assistantAttachmentFromFileHandler(recorder, request)
	return recorder
}

func decodeAttachmentFromFileForTest(t *testing.T, recorder *httptest.ResponseRecorder) scoutChatFileAttachment {
	t.Helper()
	var payload struct {
		OK         bool                    `json:"ok"`
		Attachment scoutChatFileAttachment `json:"attachment"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode from-file response: %v body=%s", err, recorder.Body.String())
	}
	if !payload.OK || payload.Attachment.SourceID == "" || payload.Attachment.SourceRevision == "" {
		t.Fatalf("from-file response=%+v", payload)
	}
	return payload.Attachment
}

func TestAssistantAttachmentFromFileAuthACLBindingAndRevisionFence(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = attachmentFileEmailAuthorizer{email: "aj@shareability.com"}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})

	owner := accountStore().findUser("aj@shareability.com")
	public, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Shared destination", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	other, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Other destination", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Source brief", "# Source brief\n\nExact source body.", owner.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "savedToFiles": "true", "visibility": "organization", "requestedBy": owner.Email,
	})
	if err != nil {
		t.Fatal(err)
	}

	if recorder := postAttachmentFromFileForTest(t, nil, public.ID, artifact.ID); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d body=%s, want 401", recorder.Code, recorder.Body.String())
	}
	timCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	if recorder := postAttachmentFromFileForTest(t, timCookies, public.ID, artifact.ID); recorder.Code != http.StatusNotFound {
		t.Fatalf("ACL-denied status=%d body=%s, want 404", recorder.Code, recorder.Body.String())
	}

	ajCookies := loginAs(t, owner.Email, "B0NFIRE!")
	recorder := postAttachmentFromFileForTest(t, ajCookies, public.ID, artifact.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	bound := decodeAttachmentFromFileForTest(t, recorder)
	meta, err := blobStatForRef(bound.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := kanbanApp.reservePendingAttachmentUpload(owner, other, bound, meta, "wrong-destination-reservation"); err == nil || !strings.Contains(err.Error(), "does not match this destination") {
		t.Fatalf("cross-destination reserve err=%v, want destination binding failure", err)
	}

	recorder = postAttachmentFromFileForTest(t, ajCookies, public.ID, artifact.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second authorized status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stale := decodeAttachmentFromFileForTest(t, recorder)
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "Source brief revised", artifact.Text+"\nChanged after grant.", owner.Name, nil); err != nil {
		t.Fatal(err)
	}
	meta, err = blobStatForRef(stale.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := kanbanApp.reservePendingAttachmentUpload(owner, public, stale, meta, "stale-source-reservation"); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("stale source reserve err=%v, want revision revalidation failure", err)
	}
}

func TestAssistantAttachmentFromFileNeverWidensPrivateAudienceAndRevalidatesAfterCommit(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{TenantID: canonicalArtifactTenantID()}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})

	owner := accountStore().findUser("aj@shareability.com")
	public, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Public destination", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	private, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Private destination", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Private source", "private source body", owner.Name, map[string]string{
		"source": "scout_thread", "status": "complete", "savedToFiles": "true", "visibility": scoutChatVisibilityPrivate, "requestedBy": owner.Email,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies := loginAs(t, owner.Email, "B0NFIRE!")
	if recorder := postAttachmentFromFileForTest(t, cookies, public.ID, artifact.ID); recorder.Code != http.StatusNotFound {
		t.Fatalf("private-to-public status=%d body=%s, want opaque 404", recorder.Code, recorder.Body.String())
	}

	recorder := postAttachmentFromFileForTest(t, cookies, private.ID, artifact.ID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("private-to-private status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	file := decodeAttachmentFromFileForTest(t, recorder)
	const reservationID = "private-drive-source-reservation"
	cleaned, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), owner, private, []scoutChatFileAttachment{file}, reservationID)
	if err != nil || len(cleaned) != 1 {
		t.Fatalf("sanitize private source: files=%+v err=%v", cleaned, err)
	}
	message := scoutChatMessageRecord{
		ID: "private-drive-source-message", Kind: "message", Role: "user", Text: "review this",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: owner.Name, AuthorEmail: owner.Email,
		Files: cleaned, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(private), attachmentReservationID: reservationID,
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(owner.Email, private.ID, message); err != nil {
		t.Fatalf("commit private attachment: %v", err)
	}
	stored, _, err := kanbanApp.scoutChatThreadByID(owner.Email, private.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projected := kanbanApp.projectScoutChatThreadForViewer(owner.Email, stored); len(projected.Messages) != 1 || len(projected.Messages[0].Files) != 1 {
		t.Fatalf("healthy private projection=%+v", projected)
	}
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "Private source revised", artifact.Text+"\nrevoked revision", owner.Name, nil); err != nil {
		t.Fatal(err)
	}
	if projected := kanbanApp.projectScoutChatThreadForViewer(owner.Email, stored); len(projected.Messages) != 1 || len(projected.Messages[0].Files) != 0 {
		t.Fatalf("stale committed source remained visible: %+v", projected)
	}
}

func TestAssistantAttachmentFromRestrictedProjectRequiresDestinationAudienceSubset(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	owner := accountStore().findUser("aj@shareability.com")
	if owner == nil {
		t.Fatal("seed owner missing")
	}
	source, _, err := kanbanApp.ensureScoutChatThread("project-source", owner.Email, owner.Name, "Project source", scoutChatVisibilityPublic, []string{owner.Email, "tim@shareability.com"})
	if err != nil {
		t.Fatal(err)
	}
	narrow, _, err := kanbanApp.ensureScoutChatThread("project-narrow", owner.Email, owner.Name, "Project narrow", scoutChatVisibilityPublic, []string{owner.Email})
	if err != nil {
		t.Fatal(err)
	}
	broad, err := kanbanApp.createScoutChatThread(owner.Email, owner.Name, "Company broad", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := putBlob(tinyPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	const sourceReservation = "restricted-source-upload"
	original := reserveTestAttachment(t, kanbanApp, owner, source, scoutChatFileAttachment{Name: "project.png", Kind: "png", Ref: ref}, sourceReservation)
	sourceMessage := scoutChatMessageRecord{
		ID: "restricted-source-message", Kind: "message", Role: "user", Text: "restricted source",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: owner.Name, AuthorEmail: owner.Email,
		Files: []scoutChatFileAttachment{original}, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(source), attachmentReservationID: sourceReservation,
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(owner.Email, source.ID, sourceMessage); err != nil {
		t.Fatal(err)
	}
	fileID := fmt.Sprintf("%s:%s:0", source.ID, sourceMessage.ID)
	cookies := loginAs(t, owner.Email, "B0NFIRE!")
	if recorder := postAttachmentFromFileForTest(t, cookies, broad.ID, fileID); recorder.Code != http.StatusNotFound {
		t.Fatalf("restricted-to-company status=%d body=%s, want opaque 404", recorder.Code, recorder.Body.String())
	}
	recorder := postAttachmentFromFileForTest(t, cookies, narrow.ID, fileID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restricted-to-subset status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	copyFile := decodeAttachmentFromFileForTest(t, recorder)
	const copyReservation = "restricted-copy-reservation"
	cleaned, err := kanbanApp.sanitizeScoutChatFiles(context.Background(), owner, narrow, []scoutChatFileAttachment{copyFile}, copyReservation)
	if err != nil {
		t.Fatal(err)
	}
	copyMessage := scoutChatMessageRecord{
		ID: "restricted-copy-message", Kind: "message", Role: "user", Text: "narrow copy",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: owner.Name, AuthorEmail: owner.Email,
		Files: cleaned, attachmentDestinationRevision: scoutChatAttachmentDestinationRevision(narrow), attachmentReservationID: copyReservation,
	}
	if _, err := kanbanApp.commitScoutChatThreadMessages(owner.Email, narrow.ID, copyMessage); err != nil {
		t.Fatal(err)
	}
	chainedFileID := fmt.Sprintf("%s:%s:0", narrow.ID, copyMessage.ID)
	if recorder := postAttachmentFromFileForTest(t, cookies, broad.ID, chainedFileID); recorder.Code != http.StatusNotFound {
		t.Fatalf("chained restricted-to-company status=%d body=%s, want opaque 404", recorder.Code, recorder.Body.String())
	}
}
