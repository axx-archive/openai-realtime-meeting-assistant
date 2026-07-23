package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const catchUpPublicationRetention = 24 * time.Hour
const catchUpNotificationRetention = catchUpPublicationRetention - 5*time.Minute

const (
	catchUpPublicationPending   = "pending"
	catchUpPublicationDelivered = "delivered"
	catchUpPublicationCancelled = "cancelled"
)

var errCatchUpPublicationCancelled = errors.New("catch-up publication was cancelled")

type catchUpPublicationPayload struct {
	RecipientEmail   string         `json:"recipientEmail"`
	NotificationText string         `json:"notificationText"`
	Result           map[string]any `json:"result"`
}

// catchUpPublicationAuthority is the minimum durable material needed to rerun
// the exact publication authorization after a process restart. It deliberately
// stores source identities and revisions, never source bodies.
type catchUpPublicationAuthority struct {
	TenantID      string            `json:"tenantId"`
	PrincipalID   string            `json:"principalId"`
	PrincipalKind ACLPrincipalKind  `json:"principalKind"`
	TeamIDs       []string          `json:"teamIds,omitempty"`
	RoomID        string            `json:"roomId"`
	SittingID     string            `json:"sittingId"`
	Snapshot      RetrievalSnapshot `json:"snapshot"`
}

func (authority catchUpPublicationAuthority) principal() ACLPrincipal {
	return ACLPrincipal{TenantID: authority.TenantID, ID: authority.PrincipalID, Kind: authority.PrincipalKind,
		TeamIDs: append([]string(nil), authority.TeamIDs...), RoomID: authority.RoomID, SittingID: authority.SittingID}
}

func (authority catchUpPublicationAuthority) validate() error {
	principal := authority.principal()
	if authority.Snapshot.Validate() != nil || strings.TrimSpace(authority.TenantID) == "" ||
		normalizeAccountEmail(authority.PrincipalID) != authority.PrincipalID || authority.PrincipalKind != ACLPrincipalUser ||
		authority.RoomID == "" || authority.SittingID == "" || authority.Snapshot.TenantID != principal.TenantID ||
		authority.Snapshot.PrincipalID != principal.ID || authority.Snapshot.PrincipalKind != principal.Kind ||
		authority.Snapshot.Temporal.RoomID != principal.RoomID || authority.Snapshot.Temporal.SittingID != principal.SittingID {
		return ErrCatchUpUnauthorized
	}
	if !sort.StringsAreSorted(authority.TeamIDs) {
		return ErrCatchUpUnauthorized
	}
	for index, teamID := range authority.TeamIDs {
		if strings.TrimSpace(teamID) == "" || (index > 0 && teamID == authority.TeamIDs[index-1]) {
			return ErrCatchUpUnauthorized
		}
	}
	return nil
}

type catchUpPublicationIntent struct {
	PublicationID   string
	TenantID        string
	RecipientEmail  string
	RoomID          string
	SittingID       string
	SnapshotID      string
	SourcesSHA256   [sha256.Size]byte
	Authority       catchUpPublicationAuthority
	AuthorityJSON   []byte
	AuthoritySHA256 [sha256.Size]byte
	Payload         catchUpPublicationPayload
	PayloadJSON     []byte
	PayloadSHA256   [sha256.Size]byte
	NotificationID  string
}

type catchUpCommittedPublication struct {
	Intent                  catchUpPublicationIntent
	Status                  string
	CommittedAt             time.Time
	RetainUntil             time.Time
	NotificationPersistedAt *time.Time
	PushDispatchedAt        *time.Time
	DeliveredAt             *time.Time
	CancelledAt             *time.Time
	CancellationReason      string
	RedactedAt              *time.Time
}

type catchUpTransactionalPublisher interface {
	CommitAndDeliverCatchUpPublication(context.Context, *kanbanBoardApp, ACLPrincipal, RetrievalSnapshot, string, map[string]any) (map[string]any, error)
}

func newCatchUpPublicationIntent(principal ACLPrincipal, snapshot RetrievalSnapshot, notificationText string, result map[string]any) (catchUpPublicationIntent, error) {
	var intent catchUpPublicationIntent
	recipient := normalizeAccountEmail(principal.ID)
	if snapshot.Validate() != nil || recipient == "" || principal.Kind != ACLPrincipalUser || snapshot.TenantID != principal.TenantID ||
		snapshot.PrincipalID != recipient || snapshot.PrincipalKind != principal.Kind || snapshot.Temporal.RoomID == "" ||
		snapshot.Temporal.SittingID == "" || result == nil || strings.TrimSpace(notificationText) == "" {
		return intent, ErrCatchUpUnauthorized
	}
	sourcesJSON, err := canonicalJSON(snapshot.Sources)
	if err != nil {
		return intent, err
	}
	teams := uniqueSortedStrings(principal.TeamIDs)
	authority := catchUpPublicationAuthority{TenantID: principal.TenantID, PrincipalID: recipient, PrincipalKind: principal.Kind,
		TeamIDs: teams, RoomID: snapshot.Temporal.RoomID, SittingID: snapshot.Temporal.SittingID, Snapshot: snapshot}
	if err := authority.validate(); err != nil {
		return intent, err
	}
	authorityJSON, err := canonicalJSON(authority)
	if err != nil {
		return intent, err
	}
	payload := catchUpPublicationPayload{RecipientEmail: recipient, NotificationText: notificationText, Result: result}
	payloadJSON, err := canonicalJSON(payload)
	if err != nil {
		return intent, err
	}
	intent = catchUpPublicationIntent{
		TenantID: principal.TenantID, RecipientEmail: recipient, RoomID: snapshot.Temporal.RoomID, SittingID: snapshot.Temporal.SittingID,
		SnapshotID: snapshot.SnapshotID, SourcesSHA256: sha256.Sum256(sourcesJSON), Authority: authority, AuthorityJSON: authorityJSON,
		AuthoritySHA256: sha256.Sum256(authorityJSON), Payload: payload, PayloadJSON: payloadJSON, PayloadSHA256: sha256.Sum256(payloadJSON),
	}
	identity, _ := canonicalJSON(struct {
		TenantID, RecipientEmail, RoomID, SittingID, SnapshotID string
		SourcesSHA256, AuthoritySHA256, PayloadSHA256           string
	}{intent.TenantID, intent.RecipientEmail, intent.RoomID, intent.SittingID, intent.SnapshotID,
		hex.EncodeToString(intent.SourcesSHA256[:]), hex.EncodeToString(intent.AuthoritySHA256[:]), hex.EncodeToString(intent.PayloadSHA256[:])})
	digest := sha256.Sum256(identity)
	intent.PublicationID = "catch-up-publication-" + hex.EncodeToString(digest[:])
	intent.NotificationID = "catch-up-notification-" + hex.EncodeToString(digest[:])
	return intent, nil
}

func (resolver *productionCatchUpResolver) publicationTenantID() string {
	if resolver == nil {
		return ""
	}
	return strings.TrimSpace(resolver.TenantID)
}

func (resolver *productionCatchUpResolver) CommitAndDeliverCatchUpPublication(ctx context.Context, app *kanbanBoardApp, principal ACLPrincipal, snapshot RetrievalSnapshot, notificationText string, result map[string]any) (map[string]any, error) {
	intent, err := newCatchUpPublicationIntent(principal, snapshot, notificationText, result)
	if err != nil || app == nil || resolver.publicationTenantID() == "" || intent.TenantID != resolver.publicationTenantID() || resolver.Sources == nil {
		return nil, ErrCatchUpUnavailable
	}
	fences, err := resolver.Sources.ReauthorizeEvidenceWithConsentFences(ctx, principal, snapshot.Sources)
	if err != nil {
		return nil, err
	}
	if resolver.beforeCommit != nil {
		resolver.beforeCommit()
	}
	commit := func() error {
		return resolver.Sources.commitCatchUpPublicationLocked(snapshot, func() error {
			return resolver.withCanonicalCatchUpSourceFenceCommit(ctx, principal, snapshot, func(tx pgx.Tx) error {
				return resolver.insertCatchUpPublication(ctx, tx, intent)
			}, resolver.commitPublication)
		})
	}
	if len(fences) == 0 {
		err = commit()
	} else {
		err = currentConsentLaneAuthority().CommitWithFences(ctx, fences, commit)
	}
	committed, found, reconcileErr := resolver.lookupCatchUpPublication(context.Background(), intent.PublicationID)
	if err != nil {
		if reconcileErr != nil || !found || verifyCatchUpPublication(intent, committed) != nil {
			return nil, fmt.Errorf("%w: publication commit unconfirmed", ErrCatchUpUnavailable)
		}
	} else if reconcileErr != nil || !found || verifyCatchUpPublication(intent, committed) != nil {
		return nil, fmt.Errorf("%w: publication commit missing", ErrCatchUpUnavailable)
	}
	if committed.Status == catchUpPublicationDelivered {
		return cloneAnyMap(intent.Payload.Result), nil
	}
	if committed.Status == catchUpPublicationCancelled {
		return nil, ErrCatchUpUnavailable
	}
	if resolver.publicationFailpoint != nil {
		if failErr := resolver.publicationFailpoint("after_commit_before_delivery"); failErr != nil {
			return nil, failErr
		}
	}
	return resolver.deliverCatchUpPublication(ctx, app, committed)
}

func (resolver *productionCatchUpResolver) insertCatchUpPublication(ctx context.Context, tx pgx.Tx, intent catchUpPublicationIntent) error {
	_, err := tx.Exec(ctx, `INSERT INTO catch_up_publications (
		publication_id,tenant_id,recipient_email,room_id,sitting_id,snapshot_id,sources_sha256,authority,authority_sha256,payload,payload_sha256,notification_id,retain_until
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10::jsonb,$11,$12,now()+$13::interval) ON CONFLICT (publication_id) DO NOTHING`,
		intent.PublicationID, intent.TenantID, intent.RecipientEmail, intent.RoomID, intent.SittingID, intent.SnapshotID,
		intent.SourcesSHA256[:], intent.AuthorityJSON, intent.AuthoritySHA256[:], intent.PayloadJSON, intent.PayloadSHA256[:], intent.NotificationID,
		catchUpPublicationRetention.String())
	if err != nil {
		return err
	}
	var payloadDigest, sourceDigest, authorityDigest []byte
	if err := tx.QueryRow(ctx, `SELECT sources_sha256,authority_sha256,payload_sha256 FROM catch_up_publications
		WHERE tenant_id=$1 AND publication_id=$2 FOR SHARE`, resolver.publicationTenantID(), intent.PublicationID).
		Scan(&sourceDigest, &authorityDigest, &payloadDigest); err != nil {
		return err
	}
	if !equalBytes(sourceDigest, intent.SourcesSHA256[:]) || !equalBytes(authorityDigest, intent.AuthoritySHA256[:]) || !equalBytes(payloadDigest, intent.PayloadSHA256[:]) {
		return ErrCatchUpUnavailable
	}
	return nil
}

func (resolver *productionCatchUpResolver) lookupCatchUpPublication(ctx context.Context, publicationID string) (catchUpCommittedPublication, bool, error) {
	var committed catchUpCommittedPublication
	tenantID := resolver.publicationTenantID()
	if resolver == nil || resolver.Postgres == nil || resolver.Postgres.pool == nil || tenantID == "" || strings.TrimSpace(publicationID) == "" {
		return committed, false, ErrCatchUpUnavailable
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var sourcesDigest, authorityDigest, payloadDigest, authorityJSON, payloadJSON []byte
	err := resolver.Postgres.pool.QueryRow(lookupCtx, `SELECT publication_id,tenant_id,recipient_email,room_id,sitting_id,snapshot_id,
		sources_sha256,authority,authority_sha256,payload,payload_sha256,notification_id,status,committed_at,retain_until,
		notification_persisted_at,push_dispatched_at,delivered_at,cancelled_at,COALESCE(cancellation_reason,''),redacted_at
		FROM catch_up_publications WHERE tenant_id=$1 AND publication_id=$2`, tenantID, publicationID).Scan(
		&committed.Intent.PublicationID, &committed.Intent.TenantID, &committed.Intent.RecipientEmail, &committed.Intent.RoomID,
		&committed.Intent.SittingID, &committed.Intent.SnapshotID, &sourcesDigest, &authorityJSON, &authorityDigest, &payloadJSON, &payloadDigest,
		&committed.Intent.NotificationID, &committed.Status, &committed.CommittedAt, &committed.RetainUntil,
		&committed.NotificationPersistedAt, &committed.PushDispatchedAt, &committed.DeliveredAt, &committed.CancelledAt,
		&committed.CancellationReason, &committed.RedactedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return committed, false, nil
	}
	if err != nil || len(sourcesDigest) != sha256.Size || len(authorityDigest) != sha256.Size || len(payloadDigest) != sha256.Size {
		return committed, false, err
	}
	copy(committed.Intent.SourcesSHA256[:], sourcesDigest)
	copy(committed.Intent.AuthoritySHA256[:], authorityDigest)
	copy(committed.Intent.PayloadSHA256[:], payloadDigest)
	if len(authorityJSON) > 0 {
		if err := json.Unmarshal(authorityJSON, &committed.Intent.Authority); err != nil {
			return catchUpCommittedPublication{}, false, err
		}
		canonicalAuthority, err := canonicalJSON(committed.Intent.Authority)
		if err != nil {
			return catchUpCommittedPublication{}, false, err
		}
		committed.Intent.AuthorityJSON = canonicalAuthority
	}
	if len(payloadJSON) > 0 {
		if err := json.Unmarshal(payloadJSON, &committed.Intent.Payload); err != nil {
			return catchUpCommittedPublication{}, false, err
		}
		canonicalPayload, err := canonicalJSON(committed.Intent.Payload)
		if err != nil {
			return catchUpCommittedPublication{}, false, err
		}
		committed.Intent.PayloadJSON = canonicalPayload
	}
	if err := validateCatchUpCommittedPublication(committed); err != nil {
		return catchUpCommittedPublication{}, false, err
	}
	return committed, true, nil
}

func validateCatchUpCommittedPublication(committed catchUpCommittedPublication) error {
	intent := committed.Intent
	if intent.PublicationID == "" || intent.TenantID == "" || normalizeAccountEmail(intent.RecipientEmail) != intent.RecipientEmail ||
		intent.RoomID == "" || intent.SittingID == "" || intent.SnapshotID == "" || intent.NotificationID == "" ||
		committed.CommittedAt.IsZero() || committed.RetainUntil.IsZero() {
		return ErrCatchUpUnavailable
	}
	if committed.Status == catchUpPublicationPending {
		if intent.Authority.validate() != nil || intent.Authority.TenantID != intent.TenantID || intent.Authority.PrincipalID != intent.RecipientEmail ||
			intent.Authority.Snapshot.SnapshotID != intent.SnapshotID || intent.Payload.RecipientEmail != intent.RecipientEmail || intent.Payload.Result == nil ||
			sha256.Sum256(intent.AuthorityJSON) != intent.AuthoritySHA256 || sha256.Sum256(intent.PayloadJSON) != intent.PayloadSHA256 || committed.RedactedAt != nil {
			return ErrCatchUpUnavailable
		}
	} else if committed.Status == catchUpPublicationDelivered {
		if committed.DeliveredAt == nil || committed.RedactedAt == nil || len(intent.PayloadJSON) != 0 || len(intent.AuthorityJSON) != 0 {
			return ErrCatchUpUnavailable
		}
	} else if committed.Status == catchUpPublicationCancelled {
		if committed.CancelledAt == nil || committed.RedactedAt == nil || committed.CancellationReason == "" || len(intent.PayloadJSON) != 0 || len(intent.AuthorityJSON) != 0 {
			return ErrCatchUpUnavailable
		}
	} else {
		return ErrCatchUpUnavailable
	}
	identity, err := canonicalJSON(struct {
		TenantID, RecipientEmail, RoomID, SittingID, SnapshotID string
		SourcesSHA256, AuthoritySHA256, PayloadSHA256           string
	}{intent.TenantID, intent.RecipientEmail, intent.RoomID, intent.SittingID, intent.SnapshotID,
		hex.EncodeToString(intent.SourcesSHA256[:]), hex.EncodeToString(intent.AuthoritySHA256[:]), hex.EncodeToString(intent.PayloadSHA256[:])})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(identity)
	wantSuffix := hex.EncodeToString(digest[:])
	if intent.PublicationID != "catch-up-publication-"+wantSuffix || intent.NotificationID != "catch-up-notification-"+wantSuffix {
		return ErrCatchUpUnavailable
	}
	return nil
}

func verifyCatchUpPublication(expected catchUpPublicationIntent, actual catchUpCommittedPublication) error {
	if actual.Intent.PublicationID != expected.PublicationID || actual.Intent.TenantID != expected.TenantID ||
		actual.Intent.RecipientEmail != expected.RecipientEmail || actual.Intent.RoomID != expected.RoomID || actual.Intent.SittingID != expected.SittingID ||
		actual.Intent.SnapshotID != expected.SnapshotID || actual.Intent.SourcesSHA256 != expected.SourcesSHA256 ||
		actual.Intent.AuthoritySHA256 != expected.AuthoritySHA256 || actual.Intent.PayloadSHA256 != expected.PayloadSHA256 ||
		actual.Intent.NotificationID != expected.NotificationID {
		return ErrCatchUpUnavailable
	}
	if actual.Status == catchUpPublicationPending &&
		(sha256.Sum256(actual.Intent.AuthorityJSON) != expected.AuthoritySHA256 || sha256.Sum256(actual.Intent.PayloadJSON) != expected.PayloadSHA256 ||
			actual.Intent.Payload.RecipientEmail != expected.RecipientEmail) {
		return ErrCatchUpUnavailable
	}
	return nil
}

func catchUpTerminalAuthorityDenial(err error) bool {
	return errors.Is(err, ErrRetrievalSnapshotStale) || errors.Is(err, ErrBrainSourceConsentAbsent) ||
		errors.Is(err, ErrCatchUpUnauthorized) || errors.Is(err, ErrBrainEvidenceInvalid)
}

func (resolver *productionCatchUpResolver) cancelCatchUpPublication(ctx context.Context, publicationID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "authority_revoked"
	}
	_, err := resolver.Postgres.pool.Exec(ctx, `UPDATE catch_up_publications SET status='cancelled',cancelled_at=now(),
		cancellation_reason=$3,authority=NULL,payload=NULL,redacted_at=now()
		WHERE tenant_id=$1 AND publication_id=$2 AND status='pending'`, resolver.publicationTenantID(), publicationID, reason)
	return err
}

func catchUpCancellationReason(err error) string {
	switch {
	case errors.Is(err, ErrBrainSourceConsentAbsent):
		return "consent_withdrawn"
	case errors.Is(err, ErrCatchUpUnauthorized):
		return "recipient_revoked"
	default:
		return "source_authority_stale"
	}
}

func (resolver *productionCatchUpResolver) deliverCatchUpPublication(ctx context.Context, app *kanbanBoardApp, committed catchUpCommittedPublication) (map[string]any, error) {
	if app == nil || resolver.Sources == nil || committed.Status != catchUpPublicationPending ||
		committed.Intent.TenantID != resolver.publicationTenantID() || committed.Intent.Payload.RecipientEmail != committed.Intent.RecipientEmail ||
		committed.Intent.Payload.Result == nil || committed.Intent.Authority.validate() != nil {
		return nil, ErrCatchUpUnavailable
	}
	result := cloneAnyMap(committed.Intent.Payload.Result)
	principal := committed.Intent.Authority.principal()
	snapshot := committed.Intent.Authority.Snapshot
	if accountStore().findUser(principal.ID) == nil {
		if err := resolver.cancelCatchUpPublication(context.Background(), committed.Intent.PublicationID, "recipient_revoked"); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: recipient revoked", errCatchUpPublicationCancelled)
	}
	fences, err := resolver.Sources.ReauthorizeEvidenceWithConsentFences(ctx, principal, snapshot.Sources)
	if err != nil {
		if catchUpTerminalAuthorityDenial(err) {
			if cancelErr := resolver.cancelCatchUpPublication(context.Background(), committed.Intent.PublicationID, catchUpCancellationReason(err)); cancelErr != nil {
				return nil, cancelErr
			}
			return nil, fmt.Errorf("%w: %v", errCatchUpPublicationCancelled, err)
		}
		return nil, err
	}
	notificationExpiresAt := committed.CommittedAt.UTC().Add(catchUpNotificationRetention)
	if committed.RetainUntil.Before(notificationExpiresAt) {
		notificationExpiresAt = committed.RetainUntil.UTC()
	}
	record := notificationRecord{ID: committed.Intent.NotificationID, TenantID: committed.Intent.TenantID,
		UserEmail: committed.Intent.RecipientEmail, Kind: notificationKindInfo, Text: committed.Intent.Payload.NotificationText,
		Tool: "catch_up_recap", CreatedAt: committed.CommittedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: notificationExpiresAt.Format(time.RFC3339Nano)}
	var dispatched bool
	var alreadyDelivered bool
	var cancelledDuringDelivery bool
	deliver := func() error {
		return resolver.Sources.commitCatchUpPublicationLocked(snapshot, func() error {
			return resolver.withCanonicalCatchUpSourceFenceCommit(ctx, principal, snapshot, func(tx pgx.Tx) error {
				var status string
				var retainUntil time.Time
				var notificationPersistedAt, pushDispatchedAt *time.Time
				var payloadDigest []byte
				if err := tx.QueryRow(ctx, `SELECT status,retain_until,notification_persisted_at,push_dispatched_at,payload_sha256
					FROM catch_up_publications WHERE tenant_id=$1 AND publication_id=$2 FOR UPDATE`, resolver.publicationTenantID(), committed.Intent.PublicationID).
					Scan(&status, &retainUntil, &notificationPersistedAt, &pushDispatchedAt, &payloadDigest); err != nil {
					return err
				}
				if status == catchUpPublicationDelivered {
					alreadyDelivered = true
					return nil
				}
				if status == catchUpPublicationCancelled {
					cancelledDuringDelivery = true
					return nil
				}
				if status != catchUpPublicationPending || !equalBytes(payloadDigest, committed.Intent.PayloadSHA256[:]) {
					return ErrCatchUpUnavailable
				}
				if !time.Now().UTC().Before(retainUntil) {
					_, updateErr := tx.Exec(ctx, `UPDATE catch_up_publications SET status='cancelled',cancelled_at=now(),
						cancellation_reason='retention_expired',authority=NULL,payload=NULL,redacted_at=now()
						WHERE tenant_id=$1 AND publication_id=$2 AND status='pending'`, resolver.publicationTenantID(), committed.Intent.PublicationID)
					if updateErr != nil {
						return updateErr
					}
					cancelledDuringDelivery = true
					return nil
				}
				if _, err := app.persistCommittedCatchUpNotification(record); err != nil {
					return err
				}
				if notificationPersistedAt == nil {
					if _, err := tx.Exec(ctx, `UPDATE catch_up_publications SET notification_persisted_at=now()
						WHERE tenant_id=$1 AND publication_id=$2`, resolver.publicationTenantID(), committed.Intent.PublicationID); err != nil {
						return err
					}
				}
				if resolver.publicationFailpoint != nil {
					if failErr := resolver.publicationFailpoint("after_notification_persist_before_push"); failErr != nil {
						return failErr
					}
				}
				if pushDispatchedAt == nil {
					var dispatchErr error
					if resolver.dispatchNotification != nil {
						dispatchErr = resolver.dispatchNotification(ctx, record)
					} else {
						dispatchErr = dispatchCommittedCatchUpNotification(ctx, resolver.publicationTenantID(), record, catchUpDispatchHooks{
							now: resolver.publicationNow, failpoint: resolver.publicationFailpoint,
							websocket: resolver.dispatchWebsocket, os: resolver.dispatchOS, webPush: resolver.dispatchWebPush,
						})
					}
					if dispatchErr != nil {
						return dispatchErr
					}
					dispatched = true
					if resolver.publicationFailpoint != nil {
						if failErr := resolver.publicationFailpoint("after_push_before_marker"); failErr != nil {
							return failErr
						}
					}
					if _, err := tx.Exec(ctx, `UPDATE catch_up_publications SET push_dispatched_at=now()
						WHERE tenant_id=$1 AND publication_id=$2`, resolver.publicationTenantID(), committed.Intent.PublicationID); err != nil {
						return err
					}
				}
				if resolver.markPublicationDelivered != nil {
					if err := resolver.markPublicationDelivered(ctx, committed.Intent.PublicationID); err != nil {
						return err
					}
				}
				_, err := tx.Exec(ctx, `UPDATE catch_up_publications SET status='delivered',delivered_at=now(),
					authority=NULL,payload=NULL,redacted_at=now() WHERE tenant_id=$1 AND publication_id=$2 AND status='pending'`,
					resolver.publicationTenantID(), committed.Intent.PublicationID)
				return err
			}, nil)
		})
	}
	if len(fences) == 0 {
		err = deliver()
	} else {
		err = currentConsentLaneAuthority().CommitWithFences(ctx, fences, deliver)
	}
	if err != nil {
		if errors.Is(err, errCatchUpPublicationCancelled) {
			return nil, err
		}
		if catchUpTerminalAuthorityDenial(err) {
			if cancelErr := resolver.cancelCatchUpPublication(context.Background(), committed.Intent.PublicationID, catchUpCancellationReason(err)); cancelErr != nil {
				return nil, cancelErr
			}
			return nil, fmt.Errorf("%w: %v", errCatchUpPublicationCancelled, err)
		}
		markCatchUpPublicationRecoveryDegraded(err)
		signalCatchUpPublicationRecovery()
		if dispatched {
			return result, nil
		}
		return nil, err
	}
	if cancelledDuringDelivery {
		return nil, errCatchUpPublicationCancelled
	}
	if alreadyDelivered {
		return result, nil
	}
	return result, nil
}

func (app *kanbanBoardApp) persistCommittedCatchUpNotification(record notificationRecord) (bool, error) {
	if app == nil || !canonicalCatchUpNotification(record) || strings.TrimSpace(record.TenantID) == "" || record.ID == "" ||
		normalizeAccountEmail(record.UserEmail) == "" || strings.TrimSpace(record.Text) == "" {
		return false, ErrCatchUpUnavailable
	}
	// The caller's record contains the transient private body used by outbound
	// channels. Project only a fixed body-free audit placeholder into the bell
	// store so notifications.json never holds recap text, even briefly.
	record.Text = catchUpNotificationAuditText
	record.RedactedAt = ""
	app.mu.Lock()
	for _, existing := range app.notifications {
		if existing.ID != record.ID {
			continue
		}
		if existing.TenantID != record.TenantID || existing.UserEmail != record.UserEmail || existing.Kind != record.Kind || existing.Text != record.Text ||
			existing.Tool != record.Tool || existing.CreatedAt != record.CreatedAt || existing.ExpiresAt != record.ExpiresAt || existing.RedactedAt != record.RedactedAt {
			app.mu.Unlock()
			return false, ErrCatchUpUnavailable
		}
		app.mu.Unlock()
		return false, nil
	}
	prior := append([]notificationRecord(nil), app.notifications...)
	app.notifications = append(app.notifications, record)
	if len(app.notifications) > notificationStoreCap {
		app.notifications = app.notifications[len(app.notifications)-notificationStoreCap:]
	}
	if err := app.persistNotificationsLocked(); err != nil {
		app.notifications = prior
		app.mu.Unlock()
		return false, err
	}
	app.mu.Unlock()
	return true, nil
}

func cloneAnyMap(input map[string]any) map[string]any {
	raw, _ := json.Marshal(input)
	var output map[string]any
	_ = json.Unmarshal(raw, &output)
	return output
}

func (resolver *productionCatchUpResolver) redactExpiredCatchUpPublications(ctx context.Context) error {
	_, err := resolver.Postgres.pool.Exec(ctx, `UPDATE catch_up_publications SET status='cancelled',cancelled_at=now(),
		cancellation_reason='retention_expired',authority=NULL,payload=NULL,redacted_at=now()
		WHERE tenant_id=$1 AND status='pending' AND retain_until<=now()`, resolver.publicationTenantID())
	return err
}

func (resolver *productionCatchUpResolver) RecoverCatchUpPublications(ctx context.Context, app *kanbanBoardApp) error {
	if resolver == nil || resolver.Postgres == nil || resolver.Postgres.pool == nil || resolver.publicationTenantID() == "" || app == nil {
		return ErrCatchUpUnavailable
	}
	if resolver.publicationFailpoint != nil {
		if err := resolver.publicationFailpoint("before_recovery_scan"); err != nil {
			return err
		}
	}
	if err := resolver.redactExpiredCatchUpPublications(ctx); err != nil {
		return err
	}
	rows, err := resolver.Postgres.pool.Query(ctx, `SELECT publication_id FROM catch_up_publications
		WHERE tenant_id=$1 AND status='pending' ORDER BY committed_at LIMIT 100`, resolver.publicationTenantID())
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		committed, found, err := resolver.lookupCatchUpPublication(ctx, id)
		if err != nil || !found {
			return ErrCatchUpUnavailable
		}
		if _, err := resolver.deliverCatchUpPublication(ctx, app, committed); err != nil && !errors.Is(err, errCatchUpPublicationCancelled) {
			return err
		}
	}
	return nil
}

type CatchUpPublicationRuntimeStatus struct {
	Ready         bool   `json:"ready"`
	WorkerRunning bool   `json:"workerRunning"`
	Pending       int64  `json:"pending"`
	Error         string `json:"error,omitempty"`
}

type catchUpPublicationRecoveryRuntime struct {
	mu       sync.Mutex
	status   CatchUpPublicationRuntimeStatus
	resolver *productionCatchUpResolver
	app      *kanbanBoardApp
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	wg       sync.WaitGroup
}

var catchUpPublicationRecoveryState struct {
	sync.RWMutex
	runtime *catchUpPublicationRecoveryRuntime
}

func startCatchUpPublicationRecovery(resolver *productionCatchUpResolver, app *kanbanBoardApp) {
	stopCatchUpPublicationRecovery()
	if resolver == nil || resolver.Postgres == nil || resolver.publicationTenantID() == "" || app == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &catchUpPublicationRecoveryRuntime{resolver: resolver, app: app, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		status: CatchUpPublicationRuntimeStatus{Ready: false, WorkerRunning: true, Error: "initial recovery scan pending"}}
	catchUpPublicationRecoveryState.Lock()
	catchUpPublicationRecoveryState.runtime = runtime
	catchUpPublicationRecoveryState.Unlock()
	runtime.wg.Add(1)
	go runtime.run()
	runtime.signal()
}

func (runtime *catchUpPublicationRecoveryRuntime) run() {
	defer runtime.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-runtime.wake:
		case <-ticker.C:
		}
		err := runtime.resolver.RecoverCatchUpPublications(runtime.ctx, runtime.app)
		var pending int64
		countErr := runtime.resolver.Postgres.pool.QueryRow(runtime.ctx, `SELECT count(*) FROM catch_up_publications
			WHERE tenant_id=$1 AND status='pending'`, runtime.resolver.publicationTenantID()).Scan(&pending)
		runtime.mu.Lock()
		runtime.status.Pending = pending
		if err != nil {
			runtime.status.Ready = false
			runtime.status.Error = err.Error()
		} else if countErr != nil {
			runtime.status.Ready = false
			runtime.status.Error = countErr.Error()
		} else if pending > 0 {
			runtime.status.Ready = false
			runtime.status.Error = "committed catch-up publications are pending delivery reconciliation"
		} else {
			runtime.status.Ready = true
			runtime.status.Error = ""
		}
		runtime.mu.Unlock()
	}
}

func (runtime *catchUpPublicationRecoveryRuntime) signal() {
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
}

func signalCatchUpPublicationRecovery() {
	catchUpPublicationRecoveryState.RLock()
	runtime := catchUpPublicationRecoveryState.runtime
	catchUpPublicationRecoveryState.RUnlock()
	if runtime != nil {
		runtime.signal()
	}
}

func markCatchUpPublicationRecoveryDegraded(err error) {
	catchUpPublicationRecoveryState.RLock()
	runtime := catchUpPublicationRecoveryState.runtime
	catchUpPublicationRecoveryState.RUnlock()
	if runtime == nil || err == nil {
		return
	}
	runtime.mu.Lock()
	runtime.status.Ready = false
	runtime.status.Error = err.Error()
	runtime.mu.Unlock()
}

func catchUpPublicationRuntimeStatus() CatchUpPublicationRuntimeStatus {
	catchUpPublicationRecoveryState.RLock()
	runtime := catchUpPublicationRecoveryState.runtime
	catchUpPublicationRecoveryState.RUnlock()
	if runtime == nil {
		return CatchUpPublicationRuntimeStatus{Ready: false, Error: "recovery worker is not configured"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.status
}

func stopCatchUpPublicationRecovery() {
	catchUpPublicationRecoveryState.Lock()
	runtime := catchUpPublicationRecoveryState.runtime
	catchUpPublicationRecoveryState.runtime = nil
	catchUpPublicationRecoveryState.Unlock()
	if runtime != nil {
		runtime.cancel()
		runtime.wg.Wait()
	}
}
