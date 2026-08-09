package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type strideE10TenantTestArtifactAuthorizer struct{ seen StrideE10TenantPrincipal }

func (a *strideE10TenantTestArtifactAuthorizer) AuthorizeArtifactHeader(context.Context, *userAccount, ACLAction, ArtifactAuthorizationHeader) bool {
	return false
}

func (a *strideE10TenantTestArtifactAuthorizer) AuthorizeArtifactHeaderForStridePrincipal(_ context.Context, principal StrideE10TenantPrincipal, _ ACLAction, _ ArtifactAuthorizationHeader) bool {
	a.seen = principal
	return true
}

func strideE10TenantHookRequestAndConverter(t *testing.T, mode StrideE10TenantConversionMode, enabled bool) (*http.Request, *StrideE10TenantConverter, *strideE10TenantTestResolver) {
	t.Helper()
	now := time.Now().UTC()
	converter, gate, resolver, _ := strideE10TenantTestConverter(now, enabled, mode)
	gate.enabled.Store(enabled)
	token := "server-held-tenant-hook-token"
	hash := hashResetToken(token)
	snapshot := strideE10TenantTestSnapshot(now)
	snapshot.SessionHash = hash
	snapshot.ActiveSession.SessionSubjectDigest = hash
	resolver.set(snapshot, nil)
	request := httptest.NewRequest(http.MethodGet, "/tenant-hook", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request, converter, resolver
}

func TestStrideE10TenantRuntimeHookCoverageAndServerHash(t *testing.T) {
	coverage := StrideE10TenantRuntimeHookCoverage()
	if len(coverage) < 4 {
		t.Fatalf("coverage=%v", coverage)
	}
	for _, item := range coverage {
		want := StrideE10TenantHookActive
		if item.Surface == StrideE10TenantSurfaceWebSocket || item.Surface == StrideE10TenantSurfaceChat || item.Surface == StrideE10TenantSurfaceRoomAdmission || item.Surface == StrideE10TenantSurfaceBoard || item.Surface == StrideE10TenantSurfaceScout || item.Surface == StrideE10TenantSurfaceBrain || item.Surface == StrideE10TenantSurfaceProductContext || item.Surface == StrideE10TenantSurfaceMarketplace || item.Surface == StrideE10TenantSurfaceWorkQueue || item.Surface == StrideE10TenantSurfaceCache || item.Surface == StrideE10TenantSurfaceWorker {
			want = StrideE10TenantHookPending
		}
		if item.HookStatus != want {
			t.Fatalf("runtime hook honesty drift: got=%+v want=%q", item, want)
		}
	}
	request, _, _ := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	raw := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	hash := strideE10SessionHashFromRequest(request)
	if hash == raw || len(hash) != 64 || !isHexDigest(hash) || strings.Contains(hash, raw) {
		t.Fatalf("session hash was not an opaque server digest: %q", hash)
	}
}

func TestStrideE10TenantRequestHookOffAndShadowPreserveLegacyBytes(t *testing.T) {
	want := []byte(`{"ok":true,"legacy":"unchanged"}`)
	for _, test := range []struct {
		name    string
		mode    StrideE10TenantConversionMode
		enabled bool
	}{
		{name: "off", mode: StrideE10TenantConversionCutover, enabled: false},
		{name: "shadow", mode: StrideE10TenantConversionShadow, enabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, converter, _ := strideE10TenantHookRequestAndConverter(t, test.mode, test.enabled)
			restore := InstallStrideE10TenantRuntimeConverter(converter)
			defer restore()
			var got bytes.Buffer
			if err := withStrideE10TenantRequestUse(request, StrideE10TenantSurfaceDrive, func(ctx context.Context, principal *StrideE10TenantPrincipal) error {
				if principal != nil || !strideE10TenantSurfaceUseBound(ctx, StrideE10TenantSurfaceDrive) {
					return errors.New("legacy callback received canonical authority")
				}
				_, _ = got.Write(want)
				return nil
			}); err != nil || !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("legacy bytes changed: got=%q err=%v", got.Bytes(), err)
			}
		})
	}
}

func TestStrideE10TenantRequestHookLinearizesFinalEffectAndFencesSwitch(t *testing.T) {
	request, converter, resolver := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withStrideE10TenantRequestUse(request, StrideE10TenantSurfaceNotification, func(ctx context.Context, principal *StrideE10TenantPrincipal) error {
			if principal == nil || principal.TenantID != "org-one" {
				return errors.New("missing canonical principal")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	switched := make(chan struct{})
	go func() {
		next := strideE10TenantTestSnapshot(time.Now().UTC())
		next.SessionHash = strideE10SessionHashFromRequest(request)
		next.ActiveSession.SessionSubjectDigest = next.SessionHash
		next.Generation++
		resolver.set(next, nil)
		close(switched)
	}()
	select {
	case <-switched:
		t.Fatal("session switch interleaved before final effect completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-switched:
	case <-time.After(time.Second):
		t.Fatal("session switch remained blocked")
	}
	resolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("revoked"))
	if err := withStrideE10TenantRequestUse(request, StrideE10TenantSurfaceNotification, func(context.Context, *StrideE10TenantPrincipal) error {
		t.Fatal("revoked session reached final use")
		return nil
	}); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("revoked session err=%v", err)
	}
}

func TestStrideE10TenantCutoverRejectsSingletonEmailFallbackAndHiddenRows(t *testing.T) {
	request, converter, _ := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	principal := StrideE10TenantPrincipal{TenantID: "org-one", PersonID: "person-one"}
	ctx := context.WithValue(request.Context(), strideE10TenantPrincipalContextKey{}, principal)
	user := &userAccount{Email: "owner@example.com"}
	header := ArtifactAuthorizationHeader{TenantID: canonicalArtifactTenantID(), ObjectID: "artifact-private", ACLVersion: 1, ContentRevision: 1, ContentDigest: strings.Repeat("a", 64), Visibility: "private", OwnerEmail: user.Email}
	if header.TenantID == principal.TenantID {
		header.TenantID = "org-other"
	}
	if artifactHeaderAuthorized(ctx, user, ACLReadContent, header) {
		t.Fatal("owner email/singleton tenant fallback authorized cross-tenant artifact")
	}
	if artifactHeaderAuthorized(context.Background(), user, ACLReadContent, header) {
		t.Fatal("cutover without capability authorized owner email fallback")
	}
	priorAuthorizer := artifactObjectAuthorizer
	canonicalAuthorizer := &strideE10TenantTestArtifactAuthorizer{}
	artifactObjectAuthorizer = canonicalAuthorizer
	defer func() { artifactObjectAuthorizer = priorAuthorizer }()
	header.TenantID = principal.TenantID
	if !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) || canonicalAuthorizer.seen.PersonID != principal.PersonID {
		t.Fatal("person-principal artifact authorizer was not used")
	}

	app := &kanbanBoardApp{notifications: []notificationRecord{
		{ID: "legacy-private", Text: "legacy private body", Kind: notificationKindChat, UserEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ID: "email-only-current-tenant", TenantID: "org-one", Text: "email authority body", Kind: notificationKindChat, UserEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ID: "cross-private", TenantID: "org-other", Text: "cross tenant body", Kind: notificationKindChat, UserEmail: user.Email, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		{ID: "current-safe", TenantID: "org-one", PersonID: "person-one", Text: "safe", Kind: notificationKindInfo, UserEmail: "hidden@example.com", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
	}}
	rows := app.notificationsForExactTenantPerson("org-one", "person-one", 100)
	if len(rows) != 1 || rows[0]["id"] != "current-safe" || rows[0]["userEmail"] != nil {
		t.Fatalf("exact tenant projection leaked hidden rows: %+v", rows)
	}
	marked, err := app.markNotificationsReadForExactTenantPerson("org-one", "person-one", []string{"current-safe", "email-only-current-tenant"})
	if err != nil || marked != 1 || len(app.notifications[3].ReadBy) != 0 || len(app.notifications[3].ReadByPersonIDs) != 1 || app.notifications[3].ReadByPersonIDs[0] != "person-one" {
		t.Fatalf("canonical read used email authority marked=%d records=%+v err=%v", marked, app.notifications, err)
	}
}

func TestStrideE10TenantPublicShareFailsClosedOnlyInCutover(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/a/missing", nil)
	off, _, _, _ := strideE10TenantTestConverter(time.Now().UTC(), false, StrideE10TenantConversionCutover)
	restore := InstallStrideE10TenantRuntimeConverter(off)
	offRecorder := httptest.NewRecorder()
	shareLinkPublicHandler(offRecorder, request.Clone(request.Context()))
	restore()

	cutover, _, _, _ := strideE10TenantTestConverter(time.Now().UTC(), true, StrideE10TenantConversionCutover)
	restore = InstallStrideE10TenantRuntimeConverter(cutover)
	defer restore()
	cutoverRecorder := httptest.NewRecorder()
	shareLinkPublicHandler(cutoverRecorder, request.Clone(request.Context()))
	if offRecorder.Code != http.StatusNotFound || cutoverRecorder.Code != http.StatusNotFound || offRecorder.Body.String() != cutoverRecorder.Body.String() {
		t.Fatalf("public share honesty drift off=%d %q cutover=%d %q", offRecorder.Code, offRecorder.Body.String(), cutoverRecorder.Code, cutoverRecorder.Body.String())
	}
}

func TestStrideE10TenantWebPushRevalidatesThroughSenderAndRejectsCrossTenant(t *testing.T) {
	request, converter, resolver := strideE10TenantHookRequestAndConverter(t, StrideE10TenantConversionCutover, true)
	restore := InstallStrideE10TenantRuntimeConverter(converter)
	defer restore()
	t.Setenv("PUSH_SUBSCRIPTIONS_PATH", t.TempDir()+"/push.json")
	t.Setenv("WEB_PUSH_VAPID_PUBLIC_KEY", "test-public")
	t.Setenv("WEB_PUSH_VAPID_PRIVATE_KEY", "test-private")
	sessionHash := strideE10SessionHashFromRequest(request)
	subscription := pushSubscriptionRecord{
		UserEmail: "legacy@example.com", TenantID: "org-one", PersonID: "person-one", SessionHash: sessionHash,
		Endpoint: "https://push.example.test/current", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	subscription.Keys.P256dh = "p256dh"
	subscription.Keys.Auth = "auth"
	if err := upsertPushSubscription(subscription); err != nil {
		t.Fatal(err)
	}
	record := notificationRecord{ID: "notification-current", TenantID: "org-one", PersonID: "person-one", UserEmail: "legacy@example.com", Kind: notificationKindInfo, Text: "safe", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	sends := 0
	sender := func(context.Context, []byte, *webpush.Subscription, *webpush.Options) (*http.Response, error) {
		sends++
		return nil, nil
	}
	if err := deliverWebPushForRecordWithSender(context.Background(), record, sender); err != nil || sends != 1 {
		t.Fatalf("current canonical push sends=%d err=%v", sends, err)
	}
	cross := record
	cross.TenantID = "org-other"
	if err := deliverWebPushForRecordWithSender(context.Background(), cross, sender); sends != 1 {
		t.Fatalf("cross-tenant push reached sender sends=%d err=%v", sends, err)
	}
	resolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("revoked"))
	if err := deliverWebPushForRecordWithSender(context.Background(), record, sender); err == nil || sends != 1 {
		t.Fatalf("revoked push reached sender sends=%d err=%v", sends, err)
	}
	raw, err := os.ReadFile(pushSubscriptionsPath())
	if err != nil || bytes.Contains(raw, []byte("server-held-tenant-hook-token")) || !bytes.Contains(raw, []byte(sessionHash)) {
		t.Fatalf("push session binding persistence raw=%q err=%v", raw, err)
	}
}

func TestStrideE10TenantDriveFoldersRestartAndCrossTenantIsolation(t *testing.T) {
	path := t.TempDir() + "/folders.json"
	t.Setenv("BONFIRE_FILE_FOLDERS_PATH", path)
	store := sharedFileFolderStore()
	personA := StrideE10TenantPrincipal{TenantID: "org_one", PersonID: "person_one"}
	personB := StrideE10TenantPrincipal{TenantID: "org_two", PersonID: "person_two"}
	folderA, err := store.createInParentForPrincipal("Evidence", "", personA)
	if err != nil {
		t.Fatal(err)
	}
	folderB, err := store.createInParentForPrincipal("Recruiting", "", personB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.createInParentForPrincipal("Cross child", folderA.ID, personB); !errors.Is(err, errFileFolderNotFound) {
		t.Fatalf("cross-tenant parent accepted: %v", err)
	}
	if err := store.assign("file_one", folderA.ID); err != nil {
		t.Fatal(err)
	}
	rows := []assistantFileRecord{{ID: "file_one"}}
	folders := decorateAssistantFileFoldersForTenant(rows, personA.TenantID)
	if len(folders) != 1 || folders[0].ID != folderA.ID || rows[0].FolderID != folderA.ID || fileFolderManagedByPrincipal(folderB.ID, personA) {
		t.Fatalf("tenant folder projection leaked folders=%+v rows=%+v", folders, rows)
	}
	restarted := newFileFolderStore(path)
	restored, assignments := restarted.snapshot()
	if len(restored) != 2 || assignments["file_one"] != folderA.ID || restored[0].TenantID == "" || restored[0].CreatedByPersonID == "" {
		t.Fatalf("folder restart lost authority restored=%+v assignments=%+v", restored, assignments)
	}
}

func TestStrideE10TenantDriveChatAttachmentUsesPersonProjectionAndCommittedGrant(t *testing.T) {
	principal := StrideE10TenantPrincipal{TenantID: "org_one", PersonID: "person_one"}
	metadata := map[string]string{"tenantId": "org_one", "visibility": scoutChatVisibilityPrivate, "ownerPersonId": "person_one", "ownerEmail": "attacker@example.com"}
	if !scoutChatThreadMetadataAllowsPrincipal(metadata, principal) {
		t.Fatal("current owner person denied")
	}
	metadata["ownerPersonId"] = "person_other"
	if scoutChatThreadMetadataAllowsPrincipal(metadata, principal) {
		t.Fatal("owner email escalated over canonical person")
	}
	metadata["ownerPersonId"] = "person_one"
	app := newIsolatedKanbanBoardApp(t)
	data := []byte("canonical attachment")
	ref, err := putBlob(data, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := blobStatForRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	thread := scoutChatThreadRecord{ID: "thread_one", Visibility: scoutChatVisibilityPrivate, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	file := scoutChatFileAttachment{Name: "evidence.txt", Ref: ref, Mime: meta.Mime, Size: meta.Size, SourceID: "source_one", SourceRevision: attachmentSourceRevision(ref, meta)}
	app.pendingAttachmentUploadsMu.Lock()
	app.pendingAttachmentUploads[file.SourceID] = pendingAttachmentUploadGrant{
		SourceID: file.SourceID, SourceRevision: file.SourceRevision, Ref: ref, Mime: meta.Mime, Size: meta.Size,
		DestinationID: thread.ID, DestinationRevision: scoutChatAttachmentDestinationRevision(thread), State: attachmentSourceCommitted, CommittedMessageID: "message_one",
	}
	app.pendingAttachmentUploadsMu.Unlock()
	if !app.committedChatAttachmentAuthorizedForPrincipal(context.Background(), principal, thread, "message_one", file) {
		t.Fatal("exact committed attachment grant denied")
	}
	app.pendingAttachmentUploadsMu.Lock()
	grant := app.pendingAttachmentUploads[file.SourceID]
	grant.State = attachmentSourceRevoked
	app.pendingAttachmentUploads[file.SourceID] = grant
	app.pendingAttachmentUploadsMu.Unlock()
	if app.committedChatAttachmentAuthorizedForPrincipal(context.Background(), principal, thread, "message_one", file) {
		t.Fatal("revoked attachment grant authorized")
	}
}
