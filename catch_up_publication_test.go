package main

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type catchUpPublicationFixture struct {
	ctx          context.Context
	app          *kanbanBoardApp
	canonical    *PostgresCanonicalStore
	resolver     *productionCatchUpResolver
	principal    ACLPrincipal
	snapshot     RetrievalSnapshot
	notification string
	result       map[string]any
}

func newCatchUpPublicationFixture(t *testing.T) catchUpPublicationFixture {
	t.Helper()
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "tenant-publication")
	ctx, canonical, registry := migratedPostgresCanonicalStore(t)
	app := newW2ATestApp(t)
	t.Cleanup(func() { _ = app.Close() })
	stamp := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	entry := meetingMemoryEntry{ID: "publication-source", Kind: meetingMemoryKindTranscript, Text: "private catch-up evidence", CreatedAt: stamp,
		Metadata: map[string]string{"roomId": officeRoomID, "meetingId": "publication-sitting", "captureSequence": "1", "capturedAt": stamp.Format(time.RFC3339Nano), "source": transcriptSourceRoomChat}}
	persistAdapterEntries(t, app.memory, []meetingMemoryEntry{entry})
	key := BrainProjectionCheckpointKey{TenantID: "tenant-publication", ProjectorVersion: brainProjectionProjectorVersion, RoomID: officeRoomID, SittingID: "publication-sitting", SourceFamily: "memory"}
	appendProjectionCanonicalEvent(t, ctx, canonical, registry, key, entry.ID, 1, entry.Text)
	principal := ACLPrincipal{TenantID: key.TenantID, ID: "aj@shareability.com", Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: key.RoomID, SittingID: key.SittingID}
	for _, action := range []ACLAction{ACLReadMetadata, ACLReadContent} {
		if _, err := canonical.pool.Exec(ctx, `INSERT INTO object_grants (
			grant_id,tenant_id,object_type,object_id,acl_version,subject_type,subject_id,action,granted_by_type,granted_by_id
		) VALUES ($1,$2,$3,$4,1,$5,$6,$7,'service','publication-test')`, uuid.New(), key.TenantID, key.SourceFamily, entry.ID,
			string(ACLPrincipalUser), principal.ID, string(action)); err != nil {
			t.Fatal(err)
		}
	}
	purge := &PostgresPurgeGenerationResolver{pool: canonical.pool}
	adapter := &MeetingMemoryBrainAdapter{Memory: app.memory, Objects: aclBrainCurrentObjectResolver{Store: canonical}, Kernel: AuthorizationKernel{Store: canonical}, Purge: purge, Consent: selectiveBrainConsent{}, Now: func() time.Time { return stamp.Add(time.Minute) }}
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, stamp.Add(-time.Minute), stamp.Add(time.Minute), "UTC", key.RoomID, key.SittingID, "publication test")
	if err != nil {
		t.Fatal(err)
	}
	planner := BrainRetrievalPlanner{Inventory: adapter, Bodies: adapter, Kernel: AuthorizationKernel{Store: canonical}, Purge: purge,
		PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 8 << 10, MaxPromptBytes: 64 << 10, MaxFoldInputs: 8, MaxFoldOutputBytes: 4 << 10}}
	retrieval, err := planner.Resolve(ctx, BrainRetrievalRequest{Principal: principal, Query: "catch me up", Temporal: temporal})
	if err != nil {
		t.Fatal(err)
	}
	return catchUpPublicationFixture{ctx: ctx, app: app, canonical: canonical,
		resolver: &productionCatchUpResolver{Planner: planner, Sources: adapter, Postgres: canonical, TenantID: key.TenantID}, principal: principal, snapshot: retrieval.Snapshot,
		notification: "Private headline\n\nPrivate catch-up", result: map[string]any{"ok": true, "recap": "Private catch-up", "audience": notificationAudienceMe}}
}

func (fixture catchUpPublicationFixture) publish() (map[string]any, error) {
	return fixture.resolver.CommitAndDeliverCatchUpPublication(fixture.ctx, fixture.app, fixture.principal, fixture.snapshot, fixture.notification, fixture.result)
}

func TestCatchUpTransactionalPublicationCommitFailureLeaksNothing(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	fixture.resolver.commitPublication = func(context.Context, pgx.Tx) error { return errors.New("commit rejected") }
	result, err := fixture.publish()
	if err == nil || result != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 0 {
		t.Fatalf("notifications=%+v", got)
	}
	var count int
	if err := fixture.canonical.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM catch_up_publications`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("publications=%d err=%v", count, err)
	}
}

func TestCatchUpTransactionalPublicationReconcilesAmbiguousCommitAndDuplicateRetry(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	fixture.resolver.commitPublication = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("connection lost after commit")
	}
	result, err := fixture.publish()
	if err != nil || result["recap"] != "Private catch-up" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	fixture.resolver.commitPublication = nil
	if _, err := fixture.publish(); err != nil {
		t.Fatal(err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 1 {
		t.Fatalf("duplicate notifications=%+v", got)
	}
}

func TestCatchUpTransactionalPublicationCrashRecoveryAndMarkFailureAreIdempotent(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	fixture.resolver.publicationFailpoint = func(point string) error {
		if point == "after_commit_before_delivery" {
			return errors.New("crash")
		}
		return nil
	}
	if result, err := fixture.publish(); err == nil || result != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 0 {
		t.Fatalf("pre-recovery notifications=%+v", got)
	}
	fixture.resolver.publicationFailpoint = nil
	if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
		t.Fatal(err)
	}
	if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
		t.Fatal(err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 1 {
		t.Fatalf("recovered notifications=%+v", got)
	}

	second := newCatchUpPublicationFixture(t)
	second.resolver.markPublicationDelivered = func(context.Context, string) error { return errors.New("mark unavailable") }
	result, err := second.publish()
	if err != nil || result == nil || len(second.app.notificationsForTenantUser(second.principal.TenantID, second.principal.ID, 10)) != 1 {
		t.Fatalf("mark failure result=%+v err=%v", result, err)
	}
	second.resolver.markPublicationDelivered = nil
	if err := second.resolver.RecoverCatchUpPublications(second.ctx, second.app); err != nil {
		t.Fatal(err)
	}
	if got := second.app.notificationsForTenantUser(second.principal.TenantID, second.principal.ID, 10); len(got) != 1 {
		t.Fatalf("mark retry duplicated=%+v", got)
	}
}

func TestCatchUpTransactionalPublicationRevocationBeforeCommitLeaksNothing(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	fixture.resolver.beforeCommit = func() {
		if _, err := fixture.canonical.pool.Exec(fixture.ctx, `UPDATE object_grants SET revoked_at=now() WHERE tenant_id=$1 AND subject_id=$2 AND action=$3`,
			fixture.principal.TenantID, fixture.principal.ID, string(ACLReadContent)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := fixture.publish()
	if err == nil || result != nil || len(fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10)) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func commitCatchUpForRecoveryTest(t *testing.T, fixture catchUpPublicationFixture) {
	t.Helper()
	fixture.resolver.publicationFailpoint = func(point string) error {
		if point == "after_commit_before_delivery" {
			return errors.New("simulated process stop")
		}
		return nil
	}
	if result, err := fixture.publish(); err == nil || result != nil {
		t.Fatalf("commit-only result=%+v err=%v", result, err)
	}
	fixture.resolver.publicationFailpoint = nil
}

func assertCatchUpPublicationRedacted(t *testing.T, fixture catchUpPublicationFixture, wantStatus, wantReason string) {
	t.Helper()
	var status, reason string
	var payloadNil, authorityNil, redacted bool
	if err := fixture.canonical.pool.QueryRow(fixture.ctx, `SELECT status,COALESCE(cancellation_reason,''),
		payload IS NULL,authority IS NULL,redacted_at IS NOT NULL FROM catch_up_publications
		WHERE tenant_id=$1`, fixture.principal.TenantID).Scan(&status, &reason, &payloadNil, &authorityNil, &redacted); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || reason != wantReason || !payloadNil || !authorityNil || !redacted {
		t.Fatalf("terminal row status=%q reason=%q payloadNil=%t authorityNil=%t redacted=%t", status, reason, payloadNil, authorityNil, redacted)
	}
}

func TestCatchUpRecoveryReauthorizesAndCancelsStalePrivatePayload(t *testing.T) {
	for _, mutation := range []string{"ACL revoke", "purge", "body mutation", "consent withdrawal"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newCatchUpPublicationFixture(t)
			commitCatchUpForRecoveryTest(t, fixture)
			source := fixture.snapshot.Sources[0].Evidence
			switch mutation {
			case "ACL revoke":
				if _, err := fixture.canonical.pool.Exec(fixture.ctx, `UPDATE object_grants SET revoked_at=now()
					WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND action=$4`, source.TenantID, source.SourceFamily,
					source.ObjectID, string(ACLReadContent)); err != nil {
					t.Fatal(err)
				}
			case "purge":
				digest, err := hex.DecodeString(source.ContentDigest)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.canonical.pool.Exec(fixture.ctx, `INSERT INTO purge_ledger (
					tenant_id,object_type,object_id,revision_id,content_sha256,policy_id,purged_at,destruction_evidence
				) VALUES ($1,$2,$3,'1',$4,'catch-up-recovery-test',now(),'{"proof":"destroyed"}'::jsonb)`,
					source.TenantID, source.SourceFamily, source.ObjectID, digest); err != nil {
					t.Fatal(err)
				}
			case "body mutation":
				entries, err := fixture.resolver.Sources.authoritativeMemorySnapshot()
				if err != nil || len(entries) != 1 {
					t.Fatalf("entries=%+v err=%v", entries, err)
				}
				entries[0].Text = "mutated after canonical commit"
				persistAdapterEntries(t, fixture.app.memory, entries)
			case "consent withdrawal":
				fixture.resolver.Sources.Consent = selectiveBrainConsent{denied: map[string]bool{source.ObjectID: true}}
			}

			if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
				t.Fatal(err)
			}
			if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 0 {
				t.Fatalf("stale recovery disclosed notifications=%+v", got)
			}
			assertCatchUpPublicationRedacted(t, fixture, catchUpPublicationCancelled, "source_authority_stale")
		})
	}
}

func TestCatchUpRecoveryIsTenantBound(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	commitCatchUpForRecoveryTest(t, fixture)
	other := *fixture.resolver
	other.TenantID = "tenant-other"
	if err := other.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
		t.Fatal(err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 0 {
		t.Fatalf("other tenant recovered notification=%+v", got)
	}
	var status string
	if err := fixture.canonical.pool.QueryRow(fixture.ctx, `SELECT status FROM catch_up_publications WHERE tenant_id=$1`, fixture.principal.TenantID).Scan(&status); err != nil || status != catchUpPublicationPending {
		t.Fatalf("tenant-A status=%q err=%v", status, err)
	}
	if _, found, err := other.lookupCatchUpPublication(fixture.ctx, catchUpPublicationIDForFixture(t, fixture)); err != nil || found {
		t.Fatalf("cross-tenant lookup found=%t err=%v", found, err)
	}
	if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
		t.Fatal(err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 1 {
		t.Fatalf("tenant-A recovery notifications=%+v", got)
	}
	assertCatchUpPublicationRedacted(t, fixture, catchUpPublicationDelivered, "")
}

func catchUpPublicationIDForFixture(t *testing.T, fixture catchUpPublicationFixture) string {
	t.Helper()
	intent, err := newCatchUpPublicationIntent(fixture.principal, fixture.snapshot, fixture.notification, fixture.result)
	if err != nil {
		t.Fatal(err)
	}
	return intent.PublicationID
}

func TestCatchUpRetentionExpiryCancelsAndScrubsWithoutDelivery(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	commitCatchUpForRecoveryTest(t, fixture)
	if _, err := fixture.canonical.pool.Exec(fixture.ctx, `UPDATE catch_up_publications SET retain_until=now()-interval '1 second'
		WHERE tenant_id=$1`, fixture.principal.TenantID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
		t.Fatal(err)
	}
	if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 0 {
		t.Fatalf("expired publication disclosed=%+v", got)
	}
	assertCatchUpPublicationRedacted(t, fixture, catchUpPublicationCancelled, "retention_expired")
}

func TestCatchUpDurablePushOutboxClosesPersistenceAndMarkerCrashWindows(t *testing.T) {
	for _, point := range []string{"after_notification_persist_before_push", "after_push_before_marker"} {
		t.Run(point, func(t *testing.T) {
			fixture := newCatchUpPublicationFixture(t)
			var dispatched []string
			fixture.resolver.dispatchNotification = func(_ context.Context, record notificationRecord) error {
				dispatched = append(dispatched, record.ID)
				return nil
			}
			fixture.resolver.publicationFailpoint = func(candidate string) error {
				if candidate == point {
					return errors.New("simulated crash at " + point)
				}
				return nil
			}
			result, err := fixture.publish()
			if point == "after_notification_persist_before_push" {
				if err == nil || result != nil || len(dispatched) != 0 {
					t.Fatalf("pre-push result=%+v err=%v dispatched=%v", result, err, dispatched)
				}
			} else if err != nil || result == nil || len(dispatched) != 1 {
				t.Fatalf("post-push result=%+v err=%v dispatched=%v", result, err, dispatched)
			}
			if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 1 {
				t.Fatalf("durable notification=%+v", got)
			}
			fixture.resolver.publicationFailpoint = nil
			if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
				t.Fatal(err)
			}
			wantDispatches := 1
			if point == "after_push_before_marker" {
				wantDispatches = 2
			}
			if len(dispatched) != wantDispatches {
				t.Fatalf("dispatch attempts=%v want=%d", dispatched, wantDispatches)
			}
			for _, id := range dispatched {
				if id != dispatched[0] || !strings.HasPrefix(id, "catch-up-notification-") {
					t.Fatalf("non-idempotent dispatch ids=%v", dispatched)
				}
			}
			if got := fixture.app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10); len(got) != 1 {
				t.Fatalf("duplicate durable notifications=%+v", got)
			}
			assertCatchUpPublicationRedacted(t, fixture, catchUpPublicationDelivered, "")
		})
	}
}

func TestCatchUpRecoveryStartsUnreadyUntilFirstDurableScan(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fixture.resolver.publicationFailpoint = func(point string) error {
		if point == "before_recovery_scan" {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
		return nil
	}
	startCatchUpPublicationRecovery(fixture.resolver, fixture.app)
	t.Cleanup(stopCatchUpPublicationRecovery)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("initial recovery scan did not start")
	}
	if status := catchUpPublicationRuntimeStatus(); status.Ready || !status.WorkerRunning || status.Error != "initial recovery scan pending" {
		t.Fatalf("pre-scan readiness=%+v", status)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if status := catchUpPublicationRuntimeStatus(); status.Ready && status.Error == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery never became ready: %+v", catchUpPublicationRuntimeStatus())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCatchUpNotificationSameEmailTenantIsolationSurvivesRestart(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	if _, err := fixture.publish(); err != nil {
		t.Fatal(err)
	}
	generic, err := fixture.app.createNotification(fixture.principal.ID, notificationKindInfo, "generic notification remains visible", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant := "tenant-other"
	assertIsolation := func(t *testing.T, app *kanbanBoardApp) {
		t.Helper()
		own := app.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10)
		if len(own) != 2 {
			t.Fatalf("own-tenant list=%+v, want catch-up plus generic", own)
		}
		other := app.notificationsForTenantUser(otherTenant, fixture.principal.ID, 10)
		if len(other) != 1 || other[0]["id"] != generic.ID || strings.Contains(asString(other[0]["text"]), "Private catch-up") {
			t.Fatalf("same-email cross-tenant list=%+v, want generic only", other)
		}
		unread := app.unreadNotificationsForTenant(otherTenant, fixture.principal.ID, 10)
		if len(unread) != 1 || unread[0]["id"] != generic.ID {
			t.Fatalf("same-email cross-tenant backlog=%+v", unread)
		}
		publicationID := catchUpPublicationIDForFixture(t, fixture)
		notificationID := strings.Replace(publicationID, "catch-up-publication-", "catch-up-notification-", 1)
		if marked, err := app.markNotificationsReadForTenant(otherTenant, fixture.principal.ID, []string{notificationID}); err != nil || marked != 0 {
			t.Fatalf("cross-tenant read marked=%d err=%v", marked, err)
		}
		if cleared, err := app.clearNotificationsForTenant(otherTenant, fixture.principal.ID, []string{notificationID}); err != nil || cleared != 0 {
			t.Fatalf("cross-tenant clear=%d err=%v", cleared, err)
		}
	}
	assertIsolation(t, fixture.app)
	reloaded := newKanbanBoardApp()
	t.Cleanup(func() { _ = reloaded.Close() })
	assertIsolation(t, reloaded)
}

func TestCatchUpNotificationNeverPersistsPrivateBodyBeforeOrAfterRestart(t *testing.T) {
	fixture := newCatchUpPublicationFixture(t)
	if _, err := fixture.publish(); err != nil {
		t.Fatal(err)
	}
	generic, err := fixture.app.createNotification(fixture.principal.ID, notificationKindInfo, "GENERIC-BODY-MUST-REMAIN", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	assertSafeBytes := func(stage string) {
		t.Helper()
		raw, err := os.ReadFile(notificationsPath())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), fixture.notification) || strings.Contains(string(raw), "Private catch-up") {
			t.Fatalf("%s notifications.json contains private recap bytes: %s", stage, raw)
		}
		if !strings.Contains(string(raw), catchUpNotificationAuditText) || !strings.Contains(string(raw), "GENERIC-BODY-MUST-REMAIN") {
			t.Fatalf("%s notifications.json lost audit or generic notification: %s", stage, raw)
		}
	}
	assertSafeBytes("before restart")

	reloaded := newKanbanBoardApp()
	t.Cleanup(func() { _ = reloaded.Close() })
	assertSafeBytes("after restart")
	list := reloaded.notificationsForTenantUser(fixture.principal.TenantID, fixture.principal.ID, 10)
	if len(list) != 2 {
		t.Fatalf("reloaded notifications=%+v", list)
	}
	var sawAudit, sawGeneric bool
	for _, item := range list {
		switch item["id"] {
		case generic.ID:
			sawGeneric = asString(item["text"]) == "GENERIC-BODY-MUST-REMAIN"
		default:
			if strings.HasPrefix(asString(item["id"]), "catch-up-notification-") {
				sawAudit = asString(item["text"]) == catchUpNotificationAuditText
			}
		}
	}
	if !sawAudit || !sawGeneric {
		t.Fatalf("body-free audit=%t generic=%t list=%+v", sawAudit, sawGeneric, list)
	}
}

func TestCatchUpExpiryDuringEveryOutboundPhaseStopsLaterChannelsAndRetriesStableID(t *testing.T) {
	for _, expiringPhase := range []string{"websocket", "os", "web_push"} {
		t.Run(expiringPhase, func(t *testing.T) {
			fixture := newCatchUpPublicationFixture(t)
			clock := time.Now().UTC()
			fixture.resolver.publicationNow = func() time.Time { return clock }
			calls := map[string][]string{}
			fixture.resolver.dispatchWebsocket = func(record notificationRecord) error {
				calls["websocket"] = append(calls["websocket"], record.ID)
				return nil
			}
			fixture.resolver.dispatchOS = func(record notificationRecord) error {
				calls["os"] = append(calls["os"], record.ID)
				return nil
			}
			fixture.resolver.dispatchWebPush = func(ctx context.Context, record notificationRecord) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("private web push lacks a context deadline")
				}
				calls["web_push"] = append(calls["web_push"], record.ID)
				return nil
			}
			expired := false
			fixture.resolver.publicationFailpoint = func(point string) error {
				if point == "after_"+expiringPhase+"_dispatch" {
					clock = clock.Add(48 * time.Hour)
					expired = true
				}
				return nil
			}

			result, err := fixture.publish()
			if err == nil || result != nil || !expired {
				t.Fatalf("expiry phase=%s result=%+v err=%v expired=%t", expiringPhase, result, err, expired)
			}
			wantFirst := map[string]int{"websocket": 1, "os": 0, "web_push": 0}
			if expiringPhase == "os" {
				wantFirst = map[string]int{"websocket": 1, "os": 1, "web_push": 0}
			} else if expiringPhase == "web_push" {
				wantFirst = map[string]int{"websocket": 1, "os": 1, "web_push": 1}
			}
			for phase, want := range wantFirst {
				if got := len(calls[phase]); got != want {
					t.Fatalf("expiry phase=%s calls[%s]=%v want=%d", expiringPhase, phase, calls[phase], want)
				}
			}
			raw, readErr := os.ReadFile(notificationsPath())
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(raw), fixture.notification) || strings.Contains(string(raw), "Private catch-up") ||
				!strings.Contains(string(raw), catchUpNotificationAuditText) {
				t.Fatalf("failed dispatch persisted private bytes: %s", raw)
			}

			clock = time.Now().UTC()
			fixture.resolver.publicationFailpoint = nil
			if err := fixture.resolver.RecoverCatchUpPublications(fixture.ctx, fixture.app); err != nil {
				t.Fatal(err)
			}
			var stableID string
			for _, ids := range calls {
				for _, id := range ids {
					if stableID == "" {
						stableID = id
					}
					if id != stableID || !strings.HasPrefix(id, "catch-up-notification-") {
						t.Fatalf("non-idempotent retry ids=%+v", calls)
					}
				}
			}
			if len(calls["websocket"]) != wantFirst["websocket"]+1 || len(calls["os"]) != wantFirst["os"]+1 || len(calls["web_push"]) != wantFirst["web_push"]+1 {
				t.Fatalf("recovery did not complete all channels: %+v", calls)
			}
			assertCatchUpPublicationRedacted(t, fixture, catchUpPublicationDelivered, "")
		})
	}
}
