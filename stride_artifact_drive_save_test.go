package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupArtifactDriveSaveSlice(t *testing.T) ([]*http.Cookie, meetingMemoryEntry) {
	t.Helper()
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Drive save", "evidence body", "AJ", map[string]string{
		"source": "scout_thread", "status": "complete", "visibility": "private", "requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return loginAs(t, "aj@shareability.com", "B0NFIRE!"), artifact
}

func artifactDriveSaveBody(t *testing.T, operationID string, ref ArtifactDispositionRef, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"operationId": operationID, "artifact": ref, "fileName": "Evidence brief"}
	for key, value := range extra {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestArtifactDriveSaveCapabilityIsDefaultOffAndDoesNotAdmitDispositionActions(t *testing.T) {
	cookies, artifact := setupArtifactDriveSaveSlice(t)
	previous := artifactDriveSaveStoreForRequest
	artifactDriveSaveStoreForRequest = func() (*ArtifactDispositionStore, error) { return nil, ErrArtifactDispositionDisabled }
	t.Cleanup(func() { artifactDriveSaveStoreForRequest = previous })

	capability := artifactAuthorizationRequest(t, http.MethodGet, artifactDriveSavePath, "", cookies, artifactDriveSaveHandler)
	if capability.Code != http.StatusOK || !strings.Contains(capability.Body.String(), `"available":false`) || !strings.Contains(capability.Body.String(), `"action":"save"`) {
		t.Fatalf("capability status=%d body=%s", capability.Code, capability.Body.String())
	}
	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	disabled := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, artifactDriveSaveBody(t, "drive-save-disabled", ref, nil), cookies, artifactDriveSaveHandler)
	if disabled.Code != http.StatusServiceUnavailable || !strings.Contains(disabled.Body.String(), "Save to Drive is unavailable") {
		t.Fatalf("disabled status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	forbiddenAction := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, artifactDriveSaveBody(t, "drive-save-discard", ref, map[string]any{"action": "discard", "confirmationId": "confirmation-one"}), cookies, artifactDriveSaveHandler)
	if forbiddenAction.Code != http.StatusServiceUnavailable {
		// Availability is checked before body parsing so an off capability reveals
		// no request-shape oracle. The enabled-schema negative below proves the
		// same fields can never become a discard command.
		t.Fatalf("off forbidden action status=%d body=%s", forbiddenAction.Code, forbiddenAction.Body.String())
	}
}

func TestArtifactDriveSaveActivationIsIndependentFromBroadDispositionMutation(t *testing.T) {
	setupAuthTestEnv(t)
	digest := strings.Repeat("a", 64)
	t.Setenv("BONFIRE_ARTIFACT_DISPOSITION_ENABLED", "true")
	t.Setenv("BONFIRE_ARTIFACT_DISPOSITION_ACTIVATION_RECEIPT", digest)
	t.Setenv("BONFIRE_ARTIFACT_DRIVE_SAVE_ENABLED", "false")
	t.Setenv("BONFIRE_ARTIFACT_DRIVE_SAVE_ACTIVATION_RECEIPT", "")
	if _, err := productionArtifactDriveSaveStore(); !errors.Is(err, ErrArtifactDispositionDisabled) {
		t.Fatalf("broad disposition enabled save-only capability: %v", err)
	}

	t.Setenv("BONFIRE_ARTIFACT_DISPOSITION_ENABLED", "false")
	t.Setenv("BONFIRE_ARTIFACT_DISPOSITION_ACTIVATION_RECEIPT", "")
	t.Setenv("BONFIRE_ARTIFACT_DRIVE_SAVE_ENABLED", "true")
	t.Setenv("BONFIRE_ARTIFACT_DRIVE_SAVE_ACTIVATION_RECEIPT", digest)
	if _, err := productionArtifactDispositionStore(); !errors.Is(err, ErrArtifactDispositionDisabled) {
		t.Fatalf("save-only capability enabled broad disposition mutation: %v", err)
	}
	artifactDriveSaveRuntime.Lock()
	artifactDriveSaveRuntime.path = ""
	artifactDriveSaveRuntime.store = nil
	artifactDriveSaveRuntime.err = nil
	artifactDriveSaveRuntime.Unlock()
	store, err := productionArtifactDriveSaveStore()
	if err != nil || store == nil {
		t.Fatalf("save-only capability unavailable with exact gate: store=%v err=%v", store, err)
	}
	if _, err := (artifactDriveSaveEffects{}).Discard(context.Background(), artifactDispositionTestRef("never-discard", 1, "private"), false); !errors.Is(err, ErrArtifactDispositionDenied) {
		t.Fatalf("save-only effect admitted discard: %v", err)
	}
}

func TestArtifactDriveSaveIsExactAuthorizedReceiptBackedAndIdempotent(t *testing.T) {
	cookies, artifact := setupArtifactDriveSaveSlice(t)
	store, err := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "drive-saves.json"), true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	previous := artifactDriveSaveStoreForRequest
	artifactDriveSaveStoreForRequest = func() (*ArtifactDispositionStore, error) { return store, nil }
	t.Cleanup(func() { artifactDriveSaveStoreForRequest = previous })

	capability := artifactAuthorizationRequest(t, http.MethodGet, artifactDriveSavePath, "", cookies, artifactDriveSaveHandler)
	if capability.Code != http.StatusOK || !strings.Contains(capability.Body.String(), `"available":true`) || !strings.Contains(capability.Body.String(), `"receiptBacked":true`) {
		t.Fatalf("capability status=%d body=%s", capability.Code, capability.Body.String())
	}
	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	body := artifactDriveSaveBody(t, "drive-save-once", ref, nil)
	first := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, body, cookies, artifactDriveSaveHandler)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"outcome":"saved"`) || !strings.Contains(first.Body.String(), `"action":"save"`) {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	replay := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, body, cookies, artifactDriveSaveHandler)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay status=%d first=%s replay=%s", replay.Code, first.Body.String(), replay.Body.String())
	}
	if len(store.receipts) != 1 {
		t.Fatalf("receipts=%d, want exactly one", len(store.receipts))
	}
	current, found := kanbanApp.osArtifactByID(artifact.ID)
	if !found {
		t.Fatal("saved source artifact disappeared")
	}
	row, saved := fileDeliverableRecord(current)
	if !saved || row.ID != artifact.ID || row.Name != "Evidence brief" {
		t.Fatalf("saved=%v row=%+v", saved, row)
	}

	unknownAuthority := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, artifactDriveSaveBody(t, "drive-save-unknown", ref, map[string]any{"action": "discard"}), cookies, artifactDriveSaveHandler)
	if unknownAuthority.Code != http.StatusBadRequest {
		t.Fatalf("unknown action status=%d body=%s", unknownAuthority.Code, unknownAuthority.Body.String())
	}
	stale := ref
	stale.ContentRevision++
	staleRequest := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, artifactDriveSaveBody(t, "drive-save-stale", stale, nil), cookies, artifactDriveSaveHandler)
	if staleRequest.Code != http.StatusConflict {
		t.Fatalf("stale status=%d body=%s", staleRequest.Code, staleRequest.Body.String())
	}
	otherCookies := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	crossPrincipal := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, artifactDriveSaveBody(t, "drive-save-other", ref, nil), otherCookies, artifactDriveSaveHandler)
	if crossPrincipal.Code != http.StatusNotFound {
		t.Fatalf("cross-principal status=%d body=%s", crossPrincipal.Code, crossPrincipal.Body.String())
	}
	changedName := artifactAuthorizationRequest(t, http.MethodPost, artifactDriveSavePath, artifactDriveSaveBody(t, "drive-save-once", ref, map[string]any{"fileName": "Different name"}), cookies, artifactDriveSaveHandler)
	if changedName.Code != http.StatusConflict {
		t.Fatalf("idempotency collision status=%d body=%s", changedName.Code, changedName.Body.String())
	}
}

func TestArtifactDriveSaveHoldsCurrentSessionAuthorityThroughReceiptAndFinalEffect(t *testing.T) {
	cookies, artifact := setupArtifactDriveSaveSlice(t)
	store, err := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "held-drive-saves.json"), true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := artifactDriveSaveStoreForRequest
	artifactDriveSaveStoreForRequest = func() (*ArtifactDispositionStore, error) { return store, nil }
	previousAuthorizer := artifactObjectAuthorizer
	canonicalAuthorizer := &strideE10TenantTestArtifactAuthorizer{}
	artifactObjectAuthorizer = canonicalAuthorizer
	t.Cleanup(func() {
		artifactDriveSaveStoreForRequest = previousStore
		artifactObjectAuthorizer = previousAuthorizer
	})

	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	request := httptest.NewRequest(http.MethodPost, artifactDriveSavePath, strings.NewReader(artifactDriveSaveBody(t, "drive-save-held", ref, nil)))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	sessionHash := strideE10SessionHashFromRequest(request)
	now := time.Now().UTC()
	converter, gate, resolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	gate.enabled.Store(true)
	snapshot := strideE10TenantTestSnapshot(now)
	snapshot.SessionHash = sessionHash
	snapshot.ActiveSession.SessionSubjectDigest = sessionHash
	snapshot.Session.ActiveOrganizationID = ref.TenantID
	snapshot.Organization.Header.ID = ref.TenantID
	snapshot.Membership.Header.TenantID = ref.TenantID
	snapshot.Membership.OrganizationID = ref.TenantID
	snapshot.ActiveSession.OrganizationID = ref.TenantID
	snapshot.Legacy.TenantID = ref.TenantID
	resolver.set(snapshot, nil)
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	defer restoreConverter()

	entered := make(chan struct{})
	release := make(chan struct{})
	previousProbe := fileSaveAfterArtifactStampProbe
	fileSaveAfterArtifactStampProbe = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { fileSaveAfterArtifactStampProbe = previousProbe })
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		artifactDriveSaveHandler(recorder, request)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("save never reached final effect")
	}
	switched := make(chan struct{})
	go func() {
		next := snapshot
		next.Generation++
		resolver.set(next, nil)
		close(switched)
	}()
	select {
	case <-switched:
		t.Fatal("session switch interleaved before the receipt and final effect completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("save handler did not finish")
	}
	if recorder.Code != http.StatusOK || canonicalAuthorizer.seen.PersonID != snapshot.Person.Header.ID {
		t.Fatalf("status=%d body=%s principal=%+v", recorder.Code, recorder.Body.String(), canonicalAuthorizer.seen)
	}
	select {
	case <-switched:
	case <-time.After(time.Second):
		t.Fatal("session switch remained blocked after response")
	}
}
