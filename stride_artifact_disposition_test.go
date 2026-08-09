package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type toggleArtifactDispositionAuthorizer struct {
	mu         sync.Mutex
	denyDelete bool
}

func (authorizer *toggleArtifactDispositionAuthorizer) AuthorizeArtifactHeader(_ context.Context, _ *userAccount, action ACLAction, _ ArtifactAuthorizationHeader) bool {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	return !(authorizer.denyDelete && action == ACLDelete)
}

func (authorizer *toggleArtifactDispositionAuthorizer) setDenyDelete(deny bool) {
	authorizer.mu.Lock()
	authorizer.denyDelete = deny
	authorizer.mu.Unlock()
}

func artifactDispositionTestDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func artifactDispositionTestRef(id string, revision int64, audience string) ArtifactDispositionRef {
	return ArtifactDispositionRef{
		TenantID: "bonfire", ArtifactID: id, ContentRevision: revision,
		ContentDigest: artifactDispositionTestDigest(fmt.Sprintf("%s:%d", id, revision)), ACLVersion: 1,
		AudienceDigest: artifactDispositionTestDigest(audience),
	}
}

const artifactDispositionTestActor = "user:0123456789abcdef01234567"

type artifactDispositionFakeEffects struct {
	mu          sync.Mutex
	opens       int
	saves       int
	discards    int
	preserve    []bool
	retracted   int
	saveCreated time.Time
}

type artifactDispositionRetryEffects struct {
	mu            sync.Mutex
	calls         int
	deleteCommits int
	deleted       bool
	failures      int
	conflict      bool
	retracted     int
}

func (effects *artifactDispositionRetryEffects) Open(context.Context, ArtifactDispositionRef) error {
	return nil
}

func (effects *artifactDispositionRetryEffects) Save(_ context.Context, ref ArtifactDispositionRef, actor, folder, fileName string) (ArtifactDriveReference, error) {
	return ArtifactDriveReference{ID: ref.ArtifactID, Name: fileName, Artifact: ref, CreatedAt: time.Now().UTC(), CreatedBy: actor, FolderID: folder, SourceArtifactID: ref.ArtifactID}, nil
}

func (effects *artifactDispositionRetryEffects) Discard(context.Context, ArtifactDispositionRef, bool) (int, error) {
	effects.mu.Lock()
	defer effects.mu.Unlock()
	effects.calls++
	if effects.conflict {
		return 0, ErrArtifactDispositionConflict
	}
	if effects.failures > 0 {
		effects.failures--
		return 0, errors.New("injected disposition effect failure")
	}
	if !effects.deleted {
		effects.deleted = true
		effects.deleteCommits++
	}
	return effects.retracted, nil
}

func (effects *artifactDispositionFakeEffects) Open(context.Context, ArtifactDispositionRef) error {
	effects.mu.Lock()
	effects.opens++
	effects.mu.Unlock()
	return nil
}

func (effects *artifactDispositionFakeEffects) Save(_ context.Context, ref ArtifactDispositionRef, actor, folderID, fileName string) (ArtifactDriveReference, error) {
	effects.mu.Lock()
	defer effects.mu.Unlock()
	effects.saves++
	created := effects.saveCreated
	if created.IsZero() {
		created = time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	}
	return ArtifactDriveReference{ID: ref.ArtifactID, Name: fileName, Artifact: ref, CreatedAt: created, CreatedBy: actor, FolderID: folderID, SourceArtifactID: ref.ArtifactID}, nil
}

func (effects *artifactDispositionFakeEffects) Discard(_ context.Context, _ ArtifactDispositionRef, preserve bool) (int, error) {
	effects.mu.Lock()
	defer effects.mu.Unlock()
	effects.discards++
	effects.preserve = append(effects.preserve, preserve)
	return effects.retracted, nil
}

func (effects *artifactDispositionFakeEffects) counts() (int, int, int) {
	effects.mu.Lock()
	defer effects.mu.Unlock()
	return effects.opens, effects.saves, effects.discards
}

func TestArtifactDispositionFeatureIsDefaultOff(t *testing.T) {
	store, err := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "disabled.json"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ref := artifactDispositionTestRef("artifact-disabled", 1, "organization")
	_, err = store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "op-disabled", Action: ArtifactDispositionOpen, ActorPrincipal: artifactDispositionTestActor, Artifact: ref}, ref, &artifactDispositionFakeEffects{})
	if !errors.Is(err, ErrArtifactDispositionDisabled) {
		t.Fatalf("err=%v, want disabled", err)
	}
	registry := NewSTRIDERegistry()
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range snapshot.Features {
		if state.Feature == STRIDEFeatureArtifactDisposition {
			if state.Enabled {
				t.Fatal("artifact disposition feature unexpectedly enabled")
			}
			return
		}
	}
	t.Fatal("artifact disposition feature absent from registry")
}

func TestArtifactDispositionOpenSaveAndIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispositions.json")
	store, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ref := artifactDispositionTestRef("artifact-save", 3, "private-aj")
	effects := &artifactDispositionFakeEffects{}
	open := ArtifactDispositionRequest{OperationID: "open-artifact-save", Action: ArtifactDispositionOpen, ActorPrincipal: artifactDispositionTestActor, Artifact: ref}
	receipt, err := store.Apply(context.Background(), open, ref, effects)
	if err != nil || receipt.Outcome != "opened" || receipt.Validate() != nil {
		t.Fatalf("open receipt=%+v err=%v", receipt, err)
	}
	if repeated, err := store.Apply(context.Background(), open, ref, effects); err != nil || repeated.Header.ContentDigest != receipt.Header.ContentDigest {
		t.Fatalf("idempotent open=%+v err=%v", repeated, err)
	}
	save := ArtifactDispositionRequest{OperationID: "save-artifact-save", Action: ArtifactDispositionSave, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, FolderID: "folder-decks"}
	saved, err := store.Apply(context.Background(), save, ref, effects)
	if err != nil || saved.Outcome != "saved" || saved.Drive == nil || saved.Drive.ID != ref.ArtifactID || saved.Validate() != nil {
		t.Fatalf("save receipt=%+v err=%v", saved, err)
	}
	tampered := saved
	tampered.RetractedReferences++
	if tampered.Validate() == nil {
		t.Fatal("receipt digest accepted tampered retraction count")
	}
	if _, err := store.Apply(context.Background(), save, ref, effects); err != nil {
		t.Fatal(err)
	}
	opens, saves, _ := effects.counts()
	if opens != 1 || saves != 1 {
		t.Fatalf("effects open=%d save=%d, want exactly once", opens, saves)
	}
	if _, err := store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: save.OperationID, Action: ArtifactDispositionOpen, ActorPrincipal: save.ActorPrincipal, Artifact: ref}, ref, effects); !errors.Is(err, ErrArtifactDispositionConflict) {
		t.Fatalf("operation reuse err=%v, want conflict", err)
	}
}

func TestArtifactDispositionTwoConfirmationsSurviveRestartAndExpire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispositions.json")
	base := time.Date(2026, 8, 6, 21, 0, 0, 0, time.UTC)
	store, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return base }
	ref := artifactDispositionTestRef("artifact-discard", 2, "organization")
	effects := &artifactDispositionFakeEffects{retracted: 3}
	first := ArtifactDispositionRequest{OperationID: "discard-artifact-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "confirmation-first"}
	pending, err := store.Apply(context.Background(), first, ref, effects)
	if !errors.Is(err, ErrArtifactDispositionConfirm) || pending.Outcome != "confirmation_required" || pending.ConfirmationExpires == nil {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if _, _, discards := effects.counts(); discards != 0 {
		t.Fatal("first confirmation executed discard")
	}
	restarted, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return base.Add(30 * time.Second) }
	second := ArtifactDispositionRequest{OperationID: "discard-artifact-second", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "confirmation-second"}
	completed, err := restarted.Apply(context.Background(), second, ref, effects)
	if err != nil || completed.Outcome != "discarded" || completed.PriorConfirmationID != first.ConfirmationID || completed.RetractedReferences != 3 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if _, err := restarted.Apply(context.Background(), second, ref, effects); err != nil {
		t.Fatalf("idempotent second confirmation: %v", err)
	}
	if _, _, discards := effects.counts(); discards != 1 {
		t.Fatalf("discards=%d, want one", discards)
	}

	expiringPath := filepath.Join(t.TempDir(), "expiring.json")
	expiring, _ := OpenArtifactDispositionStore(expiringPath, true, time.Second)
	expiring.now = func() time.Time { return base }
	expiringRef := artifactDispositionTestRef("artifact-expiring", 1, "organization")
	_, _ = expiring.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "expire-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: expiringRef, ConfirmationID: "expire-confirmation-first"}, expiringRef, effects)
	expiring.now = func() time.Time { return base.Add(2 * time.Second) }
	_, err = expiring.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "expire-second", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: expiringRef, ConfirmationID: "expire-confirmation-second"}, expiringRef, effects)
	if !errors.Is(err, ErrArtifactDispositionExpired) {
		t.Fatalf("expired confirmation err=%v", err)
	}
}

func TestArtifactDispositionRevisionAndAudienceReauthorization(t *testing.T) {
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	ref := artifactDispositionTestRef("artifact-revision", 1, "private-aj")
	effects := &artifactDispositionFakeEffects{}
	first := ArtifactDispositionRequest{OperationID: "revision-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "revision-confirmation-first"}
	_, _ = store.Apply(context.Background(), first, ref, effects)
	changedRevision := artifactDispositionTestRef(ref.ArtifactID, 2, "private-aj")
	second := ArtifactDispositionRequest{OperationID: "revision-second", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "revision-confirmation-second"}
	if _, err := store.Apply(context.Background(), second, changedRevision, effects); !errors.Is(err, ErrArtifactDispositionConflict) {
		t.Fatalf("revision drift err=%v", err)
	}
	changedAudience := artifactDispositionTestRef(ref.ArtifactID, 1, "organization")
	if _, err := store.Apply(context.Background(), second, changedAudience, effects); !errors.Is(err, ErrArtifactDispositionConflict) {
		t.Fatalf("audience drift err=%v", err)
	}
	if _, _, discards := effects.counts(); discards != 0 {
		t.Fatal("stale authority executed discard")
	}
}

func TestArtifactDispositionSavedDriveSurvivesDiscard(t *testing.T) {
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	ref := artifactDispositionTestRef("artifact-preserved", 1, "organization")
	effects := &artifactDispositionFakeEffects{retracted: 2}
	_, err := store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "preserve-save", Action: ArtifactDispositionSave, ActorPrincipal: artifactDispositionTestActor, Artifact: ref}, ref, effects)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "preserve-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "preserve-confirmation-first"}, ref, effects)
	receipt, err := store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "preserve-second", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "preserve-confirmation-second"}, ref, effects)
	if err != nil || receipt.Outcome != "chat_retracted_drive_preserved" || receipt.Drive == nil || receipt.Drive.ID != ref.ArtifactID {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	effects.mu.Lock()
	defer effects.mu.Unlock()
	if effects.discards != 1 || len(effects.preserve) != 1 || !effects.preserve[0] {
		t.Fatalf("discard preserve calls=%v", effects.preserve)
	}
}

func TestArtifactDispositionConcurrentSecondConfirmationExecutesOnce(t *testing.T) {
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	ref := artifactDispositionTestRef("artifact-race", 1, "organization")
	effects := &artifactDispositionFakeEffects{}
	_, _ = store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "race-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "race-confirmation-first"}, ref, effects)
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: fmt.Sprintf("race-second-%d", index), Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: fmt.Sprintf("race-confirmation-second-%d", index)}, ref, effects)
			if err == nil {
				successes.Add(1)
			} else if errors.Is(err, ErrArtifactDispositionConflict) {
				conflicts.Add(1)
			} else {
				t.Errorf("unexpected race error: %v", err)
			}
		}(index)
	}
	wait.Wait()
	if successes.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes.Load(), conflicts.Load())
	}
	if _, _, discards := effects.counts(); discards != 1 {
		t.Fatalf("discard effects=%d, want one", discards)
	}
}

func TestArtifactDispositionHandlerReauthorizesRevisionAudienceAndRevocation(t *testing.T) {
	cookies, artifact, _ := setupArtifactAuthorizationSlice(t)
	path := filepath.Join(t.TempDir(), "dispositions.json")
	store, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := artifactDispositionStoreForRequest
	artifactDispositionStoreForRequest = func() (*ArtifactDispositionStore, error) { return store, nil }
	previousAuthorizer := artifactObjectAuthorizer
	authorizer := &toggleArtifactDispositionAuthorizer{}
	artifactObjectAuthorizer = authorizer
	previousDeleteAuthorizer := artifactDispositionDeleteAuthorized
	artifactDispositionDeleteAuthorized = func(_ context.Context, _ *userAccount, _ ArtifactAuthorizationHeader) bool {
		authorizer.mu.Lock()
		defer authorizer.mu.Unlock()
		return !authorizer.denyDelete
	}
	t.Cleanup(func() {
		artifactDispositionStoreForRequest = previousStore
		artifactObjectAuthorizer = previousAuthorizer
		artifactDispositionDeleteAuthorized = previousDeleteAuthorizer
	})
	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	requestBody := func(operation string, action ArtifactDispositionAction, ref ArtifactDispositionRef, confirmation string) string {
		raw, err := json.Marshal(map[string]any{"operationId": operation, "action": action, "artifact": ref, "confirmationId": confirmation})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	open := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", requestBody("handler-open", ArtifactDispositionOpen, ref, ""), cookies, artifactDispositionHandler)
	if open.Code != http.StatusOK {
		t.Fatalf("open status=%d body=%s", open.Code, open.Body.String())
	}

	updated, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "updated", artifact.Text+" changed", "AJ", nil)
	if err != nil {
		t.Fatal(err)
	}
	stale := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", requestBody("handler-stale", ArtifactDispositionOpen, ref, ""), cookies, artifactDispositionHandler)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body.String())
	}
	current := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(updated)))
	first := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", requestBody("handler-discard-first", ArtifactDispositionDiscard, current, "handler-confirmation-first"), cookies, artifactDispositionHandler)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first discard status=%d body=%s", first.Code, first.Body.String())
	}
	authorizer.setDenyDelete(true)
	second := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", requestBody("handler-discard-second", ArtifactDispositionDiscard, current, "handler-confirmation-second"), cookies, artifactDispositionHandler)
	if second.Code != http.StatusNotFound {
		t.Fatalf("revoked discard status=%d body=%s", second.Code, second.Body.String())
	}
	if _, found := kanbanApp.osArtifactByID(artifact.ID); !found {
		t.Fatal("revoked discard removed artifact")
	}
}

func TestArtifactDispositionHandlerKeepsAuthorizedOpenAvailableWhileMutationsAreDisabled(t *testing.T) {
	cookies, artifact, _ := setupArtifactAuthorizationSlice(t)
	previousStore := artifactDispositionStoreForRequest
	artifactDispositionStoreForRequest = func() (*ArtifactDispositionStore, error) {
		return nil, ErrArtifactDispositionDisabled
	}
	t.Cleanup(func() { artifactDispositionStoreForRequest = previousStore })
	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	body := func(operation string, action ArtifactDispositionAction, target ArtifactDispositionRef) string {
		raw, err := json.Marshal(map[string]any{"operationId": operation, "action": action, "artifact": target})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	opened := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", body("read-only-open", ArtifactDispositionOpen, ref), cookies, artifactDispositionHandler)
	if opened.Code != http.StatusOK || !strings.Contains(opened.Body.String(), `"outcome":"opened"`) {
		t.Fatalf("open status=%d body=%s", opened.Code, opened.Body.String())
	}
	stale := ref
	stale.ContentRevision++
	staleOpen := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", body("read-only-stale", ArtifactDispositionOpen, stale), cookies, artifactDispositionHandler)
	if staleOpen.Code != http.StatusConflict {
		t.Fatalf("stale open status=%d body=%s", staleOpen.Code, staleOpen.Body.String())
	}
	save := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", body("disabled-save", ArtifactDispositionSave, ref), cookies, artifactDispositionHandler)
	if save.Code != http.StatusServiceUnavailable || !strings.Contains(save.Body.String(), ErrArtifactDispositionDisabled.Error()) {
		t.Fatalf("save status=%d body=%s", save.Code, save.Body.String())
	}
}

func TestArtifactDispositionUnsavedDiscardRetractsChatAndRecall(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	previousAuthorizer := artifactObjectAuthorizer
	kanbanApp = newIsolatedKanbanBoardApp(t)
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}
	t.Cleanup(func() { kanbanApp = previousApp; artifactObjectAuthorizer = previousAuthorizer })
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Disposition", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Disposition result", "needle-disposition-body", "AJ", map[string]string{
		"status": "complete", "visibility": "private", "requestedBy": "aj@shareability.com", "originSurface": "chat:" + thread.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.commitScoutChatThreadArtifactRef("aj@shareability.com", thread.ID, kanbanApp.scoutChatArtifactRefMessage(artifact)); err != nil {
		t.Fatal(err)
	}
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	ref := artifactDispositionRefFromHeader(header)
	effects := appArtifactDispositionEffects{app: kanbanApp, user: &userAccount{Email: "aj@shareability.com", Name: "AJ"}, artifact: artifact}
	if _, err := effects.Discard(context.Background(), ref, false); err != nil {
		t.Fatal(err)
	}
	if _, found := kanbanApp.osArtifactByID(artifact.ID); found {
		t.Fatal("unsaved artifact body survived discard")
	}
	if matches := kanbanApp.memory.search("needle-disposition-body", 10); len(matches) != 0 {
		t.Fatalf("discarded artifact remained in recall: %+v", matches)
	}
	storedThread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scoutChatThreadHasArtifactRef(storedThread, artifact.ID) {
		t.Fatal("discarded artifact chat projection survived")
	}
}

func TestArtifactDispositionPersistsPendingBeforeDestructiveEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispositions.json")
	store, _ := OpenArtifactDispositionStore(path, true, time.Minute)
	ref := artifactDispositionTestRef("artifact-persist-first", 1, "organization")
	effects := &artifactDispositionRetryEffects{}
	first := ArtifactDispositionRequest{OperationID: "persist-first-confirmation", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "persist-confirmation-one"}
	_, _ = store.Apply(context.Background(), first, ref, effects)
	second := ArtifactDispositionRequest{OperationID: "persist-second-confirmation", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "persist-confirmation-two"}
	originalWrite := store.write
	store.write = func(path string, raw []byte) error {
		if strings.Contains(string(raw), `"outcome":"discard_pending"`) {
			return errors.New("injected pending persistence failure")
		}
		return originalWrite(path, raw)
	}
	if _, err := store.Apply(context.Background(), second, ref, effects); err == nil {
		t.Fatal("pending persistence failure was ignored")
	}
	if effects.calls != 0 || effects.deleteCommits != 0 {
		t.Fatalf("destructive effect ran before pending receipt: calls=%d deletes=%d", effects.calls, effects.deleteCommits)
	}
	restarted, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.HasPendingDiscard(second) {
		t.Fatal("failed pending write appeared durable")
	}
}

func TestArtifactDispositionPersistsSaveIntentBeforeDriveEffect(t *testing.T) {
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	ref := artifactDispositionTestRef("artifact-save-intent", 1, "organization")
	effects := &artifactDispositionFakeEffects{}
	originalWrite := store.write
	store.write = func(path string, raw []byte) error {
		if strings.Contains(string(raw), `"outcome":"save_pending"`) {
			return errors.New("injected save intent persistence failure")
		}
		return originalWrite(path, raw)
	}
	_, err := store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "save-intent-operation", Action: ArtifactDispositionSave, ActorPrincipal: artifactDispositionTestActor, Artifact: ref}, ref, effects)
	if err == nil {
		t.Fatal("save intent persistence failure was ignored")
	}
	_, saves, _ := effects.counts()
	if saves != 0 {
		t.Fatalf("Drive effect ran before durable save intent: %d", saves)
	}
}

func TestArtifactDispositionFinalPersistenceFailureResumesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispositions.json")
	store, _ := OpenArtifactDispositionStore(path, true, time.Minute)
	ref := artifactDispositionTestRef("artifact-final-retry", 1, "organization")
	effects := &artifactDispositionRetryEffects{retracted: 2}
	first := ArtifactDispositionRequest{OperationID: "final-first-confirmation", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "final-confirmation-one"}
	_, _ = store.Apply(context.Background(), first, ref, effects)
	second := ArtifactDispositionRequest{OperationID: "final-second-confirmation", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "final-confirmation-two"}
	originalWrite := store.write
	failedFinal := false
	store.write = func(path string, raw []byte) error {
		if !failedFinal && strings.Contains(string(raw), `"outcome":"discarded"`) {
			failedFinal = true
			return errors.New("injected final persistence failure")
		}
		return originalWrite(path, raw)
	}
	pending, err := store.Apply(context.Background(), second, ref, effects)
	if err == nil || pending.Outcome != "discard_pending" || effects.deleteCommits != 1 {
		t.Fatalf("pending=%+v err=%v deletes=%d", pending, err, effects.deleteCommits)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), `"outcome":"discard_pending"`) {
		t.Fatalf("durable pending receipt missing: err=%v raw=%s", err, raw)
	}
	restarted, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := restarted.ResumePendingDiscard(context.Background(), second, effects)
	if err != nil || completed.Outcome != "discarded" || completed.Header.Revision != 2 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if effects.deleteCommits != 1 || effects.calls != 2 {
		t.Fatalf("idempotent retry calls=%d delete commits=%d", effects.calls, effects.deleteCommits)
	}
	verified, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil || verified.HasPendingDiscard(second) {
		t.Fatalf("final receipt did not settle after restart: err=%v", err)
	}
}

func TestArtifactDispositionHTTPResumesPendingAfterArtifactAlreadyDeleted(t *testing.T) {
	cookies, artifact, _ := setupArtifactAuthorizationSlice(t)
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	previousStore := artifactDispositionStoreForRequest
	previousDelete := artifactDispositionDeleteAuthorized
	artifactDispositionStoreForRequest = func() (*ArtifactDispositionStore, error) { return store, nil }
	artifactDispositionDeleteAuthorized = func(context.Context, *userAccount, ArtifactAuthorizationHeader) bool { return true }
	t.Cleanup(func() {
		artifactDispositionStoreForRequest = previousStore
		artifactDispositionDeleteAuthorized = previousDelete
	})
	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	body := func(operation, confirmation string) string {
		raw, _ := json.Marshal(map[string]any{"operationId": operation, "action": ArtifactDispositionDiscard, "artifact": ref, "confirmationId": confirmation})
		return string(raw)
	}
	first := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", body("http-resume-first", "http-resume-confirmation-one"), cookies, artifactDispositionHandler)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	originalWrite := store.write
	failedFinal := false
	store.write = func(path string, raw []byte) error {
		if !failedFinal && strings.Contains(string(raw), `"outcome":"discarded"`) {
			failedFinal = true
			return errors.New("injected HTTP final receipt failure")
		}
		return originalWrite(path, raw)
	}
	secondBody := body("http-resume-second", "http-resume-confirmation-two")
	second := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", secondBody, cookies, artifactDispositionHandler)
	if second.Code != http.StatusInternalServerError {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if _, found := kanbanApp.osArtifactByID(artifact.ID); found {
		t.Fatal("injected final receipt failure did not occur after exact deletion")
	}
	resumed := artifactAuthorizationRequest(t, http.MethodPost, "/api/artifact-dispositions/v1", secondBody, cookies, artifactDispositionHandler)
	if resumed.Code != http.StatusOK || !strings.Contains(resumed.Body.String(), `"outcome":"discarded"`) {
		t.Fatalf("resume status=%d body=%s", resumed.Code, resumed.Body.String())
	}
}

func TestArtifactDispositionEffectFailureRetainsResumablePendingReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispositions.json")
	store, _ := OpenArtifactDispositionStore(path, true, time.Minute)
	ref := artifactDispositionTestRef("artifact-effect-retry", 1, "organization")
	effects := &artifactDispositionRetryEffects{failures: 1}
	_, _ = store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "effect-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "effect-confirmation-one"}, ref, effects)
	second := ArtifactDispositionRequest{OperationID: "effect-second", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "effect-confirmation-two"}
	pending, err := store.Apply(context.Background(), second, ref, effects)
	if err == nil || pending.Outcome != "discard_pending" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	restarted, err := OpenArtifactDispositionStore(path, true, time.Minute)
	if err != nil || !restarted.HasPendingDiscard(second) {
		t.Fatalf("pending receipt unavailable after restart: %v", err)
	}
	completed, err := restarted.ResumePendingDiscard(context.Background(), second, effects)
	if err != nil || completed.Outcome != "discarded" || effects.deleteCommits != 1 {
		t.Fatalf("completed=%+v err=%v deletes=%d", completed, err, effects.deleteCommits)
	}
}

func TestArtifactDispositionRevisionRaceConflictsBeforeChatRetraction(t *testing.T) {
	setupAuthTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	thread, err := kanbanApp.createScoutChatThread("aj@shareability.com", "AJ", "Revision race", scoutChatVisibilityPrivate)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := kanbanApp.createOSArtifactWithMetadata("research", "Race result", "race body", "AJ", map[string]string{"status": "complete", "visibility": "private", "requestedBy": "aj@shareability.com", "originSurface": "chat:" + thread.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kanbanApp.commitScoutChatThreadArtifactRef("aj@shareability.com", thread.ID, kanbanApp.scoutChatArtifactRefMessage(artifact)); err != nil {
		t.Fatal(err)
	}
	ref := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	_, _ = store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "revision-race-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "revision-race-confirmation-one"}, ref, &artifactDispositionFakeEffects{})
	if _, _, err := kanbanApp.updateOSArtifactWithMetadata(artifact.ID, "Race result revised", artifact.Text+" revised", "AJ", nil); err != nil {
		t.Fatal(err)
	}
	second := ArtifactDispositionRequest{OperationID: "revision-race-second", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: ref, ConfirmationID: "revision-race-confirmation-two"}
	receipt, err := store.Apply(context.Background(), second, ref, appArtifactDispositionEffects{app: kanbanApp, artifact: artifact})
	if !errors.Is(err, ErrArtifactDispositionConflict) || receipt.Outcome != "discard_conflicted" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	storedThread, _, err := kanbanApp.scoutChatThreadByID("aj@shareability.com", thread.ID)
	if err != nil || !scoutChatThreadHasArtifactRef(storedThread, artifact.ID) {
		t.Fatalf("revision race retracted chat before conflict: err=%v thread=%+v", err, storedThread)
	}
	if _, found := kanbanApp.osArtifactByID(artifact.ID); !found {
		t.Fatal("revision race deleted newer artifact")
	}
}

func TestArtifactDispositionTenantAndActorBindingsDoNotCollide(t *testing.T) {
	store, _ := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "dispositions.json"), true, time.Minute)
	refA := artifactDispositionTestRef("shared-artifact", 1, "organization")
	refB := refA
	refB.TenantID = "second-tenant"
	effects := &artifactDispositionFakeEffects{}
	for _, ref := range []ArtifactDispositionRef{refA, refB} {
		_, err := store.Apply(context.Background(), ArtifactDispositionRequest{OperationID: "shared-operation", Action: ArtifactDispositionOpen, ActorPrincipal: artifactDispositionTestActor, Artifact: ref}, ref, effects)
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ArtifactDispositionRequest{OperationID: "actor-first", Action: ArtifactDispositionDiscard, ActorPrincipal: artifactDispositionTestActor, Artifact: refA, ConfirmationID: "actor-confirmation-one"}
	_, _ = store.Apply(context.Background(), first, refA, effects)
	otherActor := ArtifactDispositionRequest{OperationID: "actor-second", Action: ArtifactDispositionDiscard, ActorPrincipal: "user:fedcba9876543210fedcba98", Artifact: refA, ConfirmationID: "actor-confirmation-two"}
	if _, err := store.Apply(context.Background(), otherActor, refA, effects); !errors.Is(err, ErrArtifactDispositionInvalid) {
		t.Fatalf("different actor completed confirmation: %v", err)
	}
}

func TestPostgresArtifactDispositionMigrationEnforcesAuthorityBindings(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	digest := strings.Repeat("a", 64)
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_artifact_disposition_states (
		tenant_id,artifact_id,content_revision,content_digest,acl_version,audience_digest,updated_at
	) VALUES ('bonfire','artifact_sql',1,decode($1,'hex'),1,decode($1,'hex'),now())`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_artifact_discard_confirmations (
		tenant_id,artifact_id,confirmation_id,operation_id,actor_principal,content_revision,content_digest,acl_version,audience_digest,created_at,expires_at
	) VALUES ('bonfire','artifact_sql','confirm_one','operation_one','user:0123456789abcdef01234567',1,decode($1,'hex'),1,decode($1,'hex'),now(),now()+interval '2 minutes')`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_artifact_discard_confirmations (
		tenant_id,artifact_id,confirmation_id,operation_id,actor_principal,content_revision,content_digest,acl_version,audience_digest,created_at,expires_at
	) VALUES ('bonfire','artifact_sql','confirm_two','operation_two','user:0123456789abcdef01234567',1,decode($1,'hex'),1,decode($1,'hex'),now(),now()+interval '2 minutes')`, digest); err == nil {
		t.Fatal("migration accepted two live discard confirmations for one artifact")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_artifact_disposition_receipts (
		tenant_id,operation_id,artifact_id,action,actor_principal,content_revision,content_digest,acl_version,audience_digest,outcome,confirmation_id,retracted_references,receipt_digest,occurred_at,receipt
	) VALUES ('bonfire','operation_one','artifact_sql','discard','user:0123456789abcdef01234567',1,decode($1,'hex'),1,decode($1,'hex'),'confirmation_required','confirm_one',0,decode($1,'hex'),now(),'{}'::jsonb)`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_artifact_disposition_states SET drive_reference_id='artifact_sql' WHERE tenant_id='bonfire' AND artifact_id='artifact_sql'`); err == nil {
		t.Fatal("migration accepted a partial unbound Drive reference")
	}
}
