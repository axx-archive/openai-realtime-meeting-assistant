package main

// The artifact-disposition boundary owns the three completion actions exposed
// by compact work results: Open, Save to Drive, and Discard. It deliberately
// persists only body-free authority and receipts. Artifact bodies remain in
// their existing ACL-governed store and are fetched only after an exact
// revision/audience reauthorization.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	artifactDispositionStoreFormat = 1
	artifactDispositionMaxBytes    = 8 << 20
	artifactDiscardDefaultTTL      = 2 * time.Minute
)

var (
	ErrArtifactDispositionDisabled = errors.New("artifact disposition is disabled")
	ErrArtifactDispositionInvalid  = errors.New("invalid artifact disposition request")
	ErrArtifactDispositionConflict = errors.New("artifact revision or audience changed")
	ErrArtifactDispositionDenied   = errors.New("artifact disposition denied")
	ErrArtifactDispositionConfirm  = errors.New("a second explicit confirmation is required")
	ErrArtifactDispositionExpired  = errors.New("discard confirmation expired")
)

type ArtifactDispositionAction string

const (
	ArtifactDispositionOpen    ArtifactDispositionAction = "open"
	ArtifactDispositionSave    ArtifactDispositionAction = "save"
	ArtifactDispositionDiscard ArtifactDispositionAction = "discard"
)

type ArtifactDispositionRef struct {
	TenantID        string `json:"tenantId"`
	ArtifactID      string `json:"artifactId"`
	ContentRevision int64  `json:"contentRevision"`
	ContentDigest   string `json:"contentDigest"`
	ACLVersion      int64  `json:"aclVersion"`
	AudienceDigest  string `json:"audienceDigest"`
}

func (ref ArtifactDispositionRef) Validate() error {
	if !strideIdentifier(ref.TenantID) || !strideIdentifier(ref.ArtifactID) || ref.ContentRevision < 1 ||
		!isHexDigest(ref.ContentDigest) || ref.ACLVersion < 1 || !isHexDigest(ref.AudienceDigest) {
		return ErrArtifactDispositionInvalid
	}
	return nil
}

func (ref ArtifactDispositionRef) Equal(other ArtifactDispositionRef) bool {
	return ref == other
}

type ArtifactDriveReference struct {
	ID               string                 `json:"id"`
	Artifact         ArtifactDispositionRef `json:"artifact"`
	CreatedAt        time.Time              `json:"createdAt"`
	CreatedBy        string                 `json:"createdBy"`
	FolderID         string                 `json:"folderId,omitempty"`
	SourceArtifactID string                 `json:"sourceArtifactId"`
}

func (ref ArtifactDriveReference) Validate() error {
	if !strideIdentifier(ref.ID) || ref.Artifact.Validate() != nil || ref.CreatedAt.IsZero() ||
		!strideIdentifier(ref.CreatedBy) || ref.SourceArtifactID != ref.Artifact.ArtifactID ||
		(ref.FolderID != "" && !strideIdentifier(ref.FolderID)) {
		return ErrArtifactDispositionInvalid
	}
	return nil
}

type ArtifactDispositionReceipt struct {
	Header              STRIDEContractHeader      `json:"header"`
	OperationID         string                    `json:"operationId"`
	Action              ArtifactDispositionAction `json:"action"`
	ActorPrincipal      string                    `json:"actorPrincipal"`
	Artifact            ArtifactDispositionRef    `json:"artifact"`
	Outcome             string                    `json:"outcome"`
	Drive               *ArtifactDriveReference   `json:"drive,omitempty"`
	ConfirmationID      string                    `json:"confirmationId,omitempty"`
	PriorConfirmationID string                    `json:"priorConfirmationId,omitempty"`
	ConfirmationExpires *time.Time                `json:"confirmationExpiresAt,omitempty"`
	RetractedReferences int                       `json:"retractedReferences,omitempty"`
	OccurredAt          time.Time                 `json:"occurredAt"`
}

func (receipt ArtifactDispositionReceipt) Validate() error {
	if receipt.Header.Validate(STRIDEContractArtifactDisposition) != nil || receipt.OperationID != receipt.Header.ID ||
		!oneOf(string(receipt.Action), string(ArtifactDispositionOpen), string(ArtifactDispositionSave), string(ArtifactDispositionDiscard)) ||
		!strideIdentifier(receipt.ActorPrincipal) || receipt.Artifact.Validate() != nil || receipt.OccurredAt.IsZero() ||
		!oneOf(receipt.Outcome, "opened", "save_pending", "saved", "save_conflicted", "confirmation_required", "discard_pending", "discarded", "discard_conflicted", "chat_retracted_drive_preserved") ||
		(receipt.Drive != nil && receipt.Drive.Validate() != nil) || (receipt.ConfirmationID != "" && !strideIdentifier(receipt.ConfirmationID)) ||
		(receipt.PriorConfirmationID != "" && !strideIdentifier(receipt.PriorConfirmationID)) || receipt.RetractedReferences < 0 {
		return ErrArtifactDispositionInvalid
	}
	if receipt.Action == ArtifactDispositionDiscard && receipt.ConfirmationID == "" {
		return ErrArtifactDispositionInvalid
	}
	if receipt.Outcome == "confirmation_required" && receipt.ConfirmationExpires == nil {
		return ErrArtifactDispositionInvalid
	}
	if receipt.Outcome != "confirmation_required" && receipt.ConfirmationExpires != nil {
		return ErrArtifactDispositionInvalid
	}
	if receipt.Outcome == "chat_retracted_drive_preserved" && receipt.Drive == nil {
		return ErrArtifactDispositionInvalid
	}
	digest, err := artifactDispositionReceiptDigest(receipt)
	if err != nil || digest != receipt.Header.ContentDigest {
		return ErrArtifactDispositionInvalid
	}
	return nil
}

func artifactDispositionReceiptDigest(receipt ArtifactDispositionReceipt) (string, error) {
	return STRIDEContractDigest(struct {
		OperationID         string                    `json:"operationId"`
		Action              ArtifactDispositionAction `json:"action"`
		ActorPrincipal      string                    `json:"actorPrincipal"`
		Artifact            ArtifactDispositionRef    `json:"artifact"`
		Outcome             string                    `json:"outcome"`
		Drive               *ArtifactDriveReference   `json:"drive,omitempty"`
		ConfirmationID      string                    `json:"confirmationId,omitempty"`
		PriorConfirmationID string                    `json:"priorConfirmationId,omitempty"`
		ConfirmationExpires *time.Time                `json:"confirmationExpiresAt,omitempty"`
		RetractedReferences int                       `json:"retractedReferences,omitempty"`
		OccurredAt          time.Time                 `json:"occurredAt"`
	}{receipt.OperationID, receipt.Action, receipt.ActorPrincipal, receipt.Artifact, receipt.Outcome, receipt.Drive,
		receipt.ConfirmationID, receipt.PriorConfirmationID, receipt.ConfirmationExpires, receipt.RetractedReferences, receipt.OccurredAt})
}

type ArtifactDispositionRequest struct {
	OperationID    string                    `json:"operationId"`
	Action         ArtifactDispositionAction `json:"action"`
	ActorPrincipal string                    `json:"actorPrincipal"`
	Artifact       ArtifactDispositionRef    `json:"artifact"`
	FolderID       string                    `json:"folderId,omitempty"`
	ConfirmationID string                    `json:"confirmationId,omitempty"`
	At             time.Time                 `json:"-"`
}

func (request ArtifactDispositionRequest) Validate() error {
	if !strideIdentifier(request.OperationID) || !oneOf(string(request.Action), string(ArtifactDispositionOpen), string(ArtifactDispositionSave), string(ArtifactDispositionDiscard)) ||
		!strideIdentifier(request.ActorPrincipal) || request.Artifact.Validate() != nil ||
		(request.FolderID != "" && !strideIdentifier(request.FolderID)) ||
		(request.Action == ArtifactDispositionDiscard) != (request.ConfirmationID != "") ||
		(request.ConfirmationID != "" && !strideIdentifier(request.ConfirmationID)) {
		return ErrArtifactDispositionInvalid
	}
	return nil
}

type ArtifactDispositionEffects interface {
	Open(context.Context, ArtifactDispositionRef) error
	Save(context.Context, ArtifactDispositionRef, string, string) (ArtifactDriveReference, error)
	Discard(context.Context, ArtifactDispositionRef, bool) (int, error)
}

type artifactDiscardConfirmation struct {
	ID        string                 `json:"id"`
	Operation string                 `json:"operationId"`
	Actor     string                 `json:"actor"`
	Artifact  ArtifactDispositionRef `json:"artifact"`
	CreatedAt time.Time              `json:"createdAt"`
	ExpiresAt time.Time              `json:"expiresAt"`
}

type artifactDispositionState struct {
	Artifact           ArtifactDispositionRef       `json:"artifact"`
	Drive              *ArtifactDriveReference      `json:"drive,omitempty"`
	Confirmation       *artifactDiscardConfirmation `json:"confirmation,omitempty"`
	PendingOperationID string                       `json:"pendingOperationId,omitempty"`
	TombstonedAt       *time.Time                   `json:"tombstonedAt,omitempty"`
}

type artifactDispositionDiskState struct {
	Format   int                                   `json:"format"`
	States   map[string]artifactDispositionState   `json:"states"`
	Receipts map[string]ArtifactDispositionReceipt `json:"receipts"`
}

type ArtifactDispositionStore struct {
	mu       sync.Mutex
	path     string
	enabled  bool
	ttl      time.Duration
	now      func() time.Time
	states   map[string]artifactDispositionState
	receipts map[string]ArtifactDispositionReceipt
	write    func(string, []byte) error
}

func OpenArtifactDispositionStore(path string, enabled bool, ttl time.Duration) (*ArtifactDispositionStore, error) {
	if ttl <= 0 {
		ttl = artifactDiscardDefaultTTL
	}
	store := &ArtifactDispositionStore{
		path: path, enabled: enabled, ttl: ttl, now: func() time.Time { return time.Now().UTC() },
		states: map[string]artifactDispositionState{}, receipts: map[string]ArtifactDispositionReceipt{},
		write: func(path string, raw []byte) error { return writeFileAtomicallyDurable(path, raw, 0o600) },
	}
	if !enabled {
		return store, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > artifactDispositionMaxBytes {
		return nil, ErrArtifactDispositionInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, artifactDispositionMaxBytes+1))
	if err != nil || len(raw) > artifactDispositionMaxBytes {
		return nil, ErrArtifactDispositionInvalid
	}
	var disk artifactDispositionDiskState
	if err := strictJSONBytes(raw, &disk); err != nil || disk.Format != artifactDispositionStoreFormat || disk.States == nil || disk.Receipts == nil {
		return nil, ErrArtifactDispositionInvalid
	}
	for key, state := range disk.States {
		if key != artifactDispositionStateKey(state.Artifact) || state.Artifact.Validate() != nil || (state.Drive != nil && state.Drive.Validate() != nil) ||
			(state.Confirmation != nil && validateArtifactDiscardConfirmation(*state.Confirmation) != nil) ||
			(state.PendingOperationID != "" && !strideIdentifier(state.PendingOperationID)) {
			return nil, ErrArtifactDispositionInvalid
		}
		if state.PendingOperationID != "" {
			receipt, ok := disk.Receipts[artifactDispositionReceiptKey(state.Artifact.TenantID, state.PendingOperationID)]
			if !ok || !oneOf(receipt.Outcome, "save_pending", "discard_pending") || !receipt.Artifact.Equal(state.Artifact) {
				return nil, ErrArtifactDispositionInvalid
			}
		}
	}
	for key, receipt := range disk.Receipts {
		if key != artifactDispositionReceiptKey(receipt.Artifact.TenantID, receipt.OperationID) || receipt.Validate() != nil {
			return nil, ErrArtifactDispositionInvalid
		}
	}
	store.states, store.receipts = disk.States, disk.Receipts
	return store, nil
}

func artifactDispositionStateKey(ref ArtifactDispositionRef) string {
	return ref.TenantID + "\x00" + ref.ArtifactID
}

func artifactDispositionReceiptKey(tenantID, operationID string) string {
	return tenantID + "\x00" + operationID
}

func validateArtifactDiscardConfirmation(confirmation artifactDiscardConfirmation) error {
	if !strideIdentifier(confirmation.ID) || !strideIdentifier(confirmation.Operation) || !strideIdentifier(confirmation.Actor) ||
		confirmation.Artifact.Validate() != nil || confirmation.CreatedAt.IsZero() || !confirmation.ExpiresAt.After(confirmation.CreatedAt) {
		return ErrArtifactDispositionInvalid
	}
	return nil
}

func (store *ArtifactDispositionStore) persistLocked() error {
	if store.path == "" || store.write == nil {
		return ErrArtifactDispositionDenied
	}
	raw, err := json.Marshal(artifactDispositionDiskState{Format: artifactDispositionStoreFormat, States: store.states, Receipts: store.receipts})
	if err != nil {
		return err
	}
	return store.write(store.path, append(raw, '\n'))
}

func (store *ArtifactDispositionStore) Apply(ctx context.Context, request ArtifactDispositionRequest, current ArtifactDispositionRef, effects ArtifactDispositionEffects) (ArtifactDispositionReceipt, error) {
	if store == nil || !store.enabled {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionDisabled
	}
	if request.Validate() != nil || current.Validate() != nil || effects == nil {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionInvalid
	}
	if !request.Artifact.Equal(current) {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	receiptKey := artifactDispositionReceiptKey(request.Artifact.TenantID, request.OperationID)
	stateKey := artifactDispositionStateKey(current)
	if prior, ok := store.receipts[receiptKey]; ok {
		if !artifactDispositionReceiptMatchesRequest(prior, request) {
			return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
		}
		if prior.Outcome == "discard_pending" {
			state := store.states[stateKey]
			return store.completePendingDiscardLocked(ctx, request, stateKey, state, prior, effects)
		}
		if prior.Outcome == "save_pending" {
			state := store.states[stateKey]
			return store.completePendingSaveLocked(ctx, request, stateKey, state, prior, effects)
		}
		return prior, nil
	}
	now := request.At.UTC()
	if now.IsZero() {
		now = store.now().UTC()
	}
	state := store.states[stateKey]
	if state.TombstonedAt != nil {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
	}
	if state.PendingOperationID != "" {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
	}
	if state.Artifact.ArtifactID != "" && !state.Artifact.Equal(current) {
		// A first confirmation is authority for exactly one revision/audience.
		// Saved Drive identity survives revisions, but pending delete authority
		// never does.
		state.Confirmation = nil
	}
	state.Artifact = current

	var receipt ArtifactDispositionReceipt
	switch request.Action {
	case ArtifactDispositionOpen:
		if err := effects.Open(ctx, current); err != nil {
			return ArtifactDispositionReceipt{}, err
		}
		receipt = newArtifactDispositionReceipt(request, "opened", nil, "", nil, 0, now)
	case ArtifactDispositionSave:
		drive := ArtifactDriveReference{ID: current.ArtifactID, Artifact: current, CreatedAt: now, CreatedBy: request.ActorPrincipal, FolderID: request.FolderID, SourceArtifactID: current.ArtifactID}
		if state.Drive != nil {
			// Re-saving (including to a different folder) reuses the one Drive
			// identity and its original creation custody instead of manufacturing
			// a second logical copy.
			drive.CreatedAt = state.Drive.CreatedAt
			drive.CreatedBy = state.Drive.CreatedBy
		}
		state.Confirmation = nil
		state.PendingOperationID = request.OperationID
		receipt = newArtifactDispositionReceipt(request, "save_pending", &drive, "", nil, 0, now)
		oldState, hadState := store.states[stateKey]
		store.states[stateKey] = state
		store.receipts[receiptKey] = receipt
		if err := store.persistLocked(); err != nil {
			delete(store.receipts, receiptKey)
			if hadState {
				store.states[stateKey] = oldState
			} else {
				delete(store.states, stateKey)
			}
			return ArtifactDispositionReceipt{}, err
		}
		return store.completePendingSaveLocked(ctx, request, stateKey, state, receipt, effects)
	case ArtifactDispositionDiscard:
		prior := state.Confirmation
		if prior == nil || !prior.Artifact.Equal(current) {
			expires := now.Add(store.ttl)
			state.Confirmation = &artifactDiscardConfirmation{ID: request.ConfirmationID, Operation: request.OperationID, Actor: request.ActorPrincipal, Artifact: current, CreatedAt: now, ExpiresAt: expires}
			receipt = newArtifactDispositionReceipt(request, "confirmation_required", state.Drive, "", &expires, 0, now)
			oldState, hadState := store.states[stateKey]
			store.states[stateKey] = state
			store.receipts[receiptKey] = receipt
			if err := store.persistLocked(); err != nil {
				delete(store.receipts, receiptKey)
				if hadState {
					store.states[stateKey] = oldState
				} else {
					delete(store.states, stateKey)
				}
				return ArtifactDispositionReceipt{}, err
			}
			return receipt, ErrArtifactDispositionConfirm
		}
		if !now.Before(prior.ExpiresAt) {
			oldState := state
			state.Confirmation = nil
			store.states[stateKey] = state
			if err := store.persistLocked(); err != nil {
				store.states[stateKey] = oldState
				return ArtifactDispositionReceipt{}, err
			}
			return ArtifactDispositionReceipt{}, ErrArtifactDispositionExpired
		}
		if prior.ID == request.ConfirmationID || prior.Operation == request.OperationID || prior.Actor != request.ActorPrincipal {
			return ArtifactDispositionReceipt{}, ErrArtifactDispositionInvalid
		}
		preserveDrive := state.Drive != nil
		state.Confirmation = nil
		state.PendingOperationID = request.OperationID
		if !preserveDrive {
			state.TombstonedAt = &now
		}
		receipt = newArtifactDispositionReceipt(request, "discard_pending", state.Drive, prior.ID, nil, 0, now)
		oldState := store.states[stateKey]
		store.states[stateKey] = state
		store.receipts[receiptKey] = receipt
		if err := store.persistLocked(); err != nil {
			store.states[stateKey] = oldState
			delete(store.receipts, receiptKey)
			return ArtifactDispositionReceipt{}, err
		}
		return store.completePendingDiscardLocked(ctx, request, stateKey, state, receipt, effects)
	}
	oldState, hadState := store.states[stateKey]
	store.states[stateKey] = state
	store.receipts[receiptKey] = receipt
	if err := store.persistLocked(); err != nil {
		delete(store.receipts, receiptKey)
		if hadState {
			store.states[stateKey] = oldState
		} else {
			delete(store.states, stateKey)
		}
		return ArtifactDispositionReceipt{}, err
	}
	return receipt, nil
}

func artifactDispositionReceiptMatchesRequest(receipt ArtifactDispositionReceipt, request ArtifactDispositionRequest) bool {
	return receipt.Action == request.Action && receipt.ActorPrincipal == request.ActorPrincipal && receipt.Artifact.Equal(request.Artifact) &&
		receipt.ConfirmationID == request.ConfirmationID
}

func (store *ArtifactDispositionStore) completePendingSaveLocked(ctx context.Context, request ArtifactDispositionRequest, stateKey string, state artifactDispositionState, pending ArtifactDispositionReceipt, effects ArtifactDispositionEffects) (ArtifactDispositionReceipt, error) {
	if pending.Outcome != "save_pending" || pending.Drive == nil || state.PendingOperationID != request.OperationID || !pending.Artifact.Equal(state.Artifact) {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
	}
	drive, effectErr := effects.Save(ctx, pending.Artifact, request.ActorPrincipal, request.FolderID)
	if effectErr != nil && !errors.Is(effectErr, ErrArtifactDispositionConflict) {
		return pending, effectErr
	}
	if effectErr == nil && (drive.Validate() != nil || drive.ID != pending.Drive.ID || !drive.Artifact.Equal(pending.Artifact)) {
		effectErr = ErrArtifactDispositionConflict
	}
	if errors.Is(effectErr, ErrArtifactDispositionConflict) {
		state.PendingOperationID = ""
		completed := newArtifactDispositionReceiptRevision(request, "save_conflicted", state.Drive, "", nil, 0, store.now().UTC(), pending.Header.Revision+1)
		receiptKey := artifactDispositionReceiptKey(request.Artifact.TenantID, request.OperationID)
		store.states[stateKey] = state
		store.receipts[receiptKey] = completed
		if err := store.persistLocked(); err != nil {
			state.PendingOperationID = request.OperationID
			store.states[stateKey] = state
			store.receipts[receiptKey] = pending
			return pending, err
		}
		return completed, effectErr
	}
	// The durable intent minted the stable identity and custody timestamp.
	// The idempotent Drive effect may report a later retry timestamp, which is
	// never allowed to manufacture a second logical copy.
	drive.CreatedAt = pending.Drive.CreatedAt
	drive.CreatedBy = pending.Drive.CreatedBy
	priorDrive := state.Drive
	state.Drive = &drive
	state.PendingOperationID = ""
	completed := newArtifactDispositionReceiptRevision(request, "saved", &drive, "", nil, 0, store.now().UTC(), pending.Header.Revision+1)
	receiptKey := artifactDispositionReceiptKey(request.Artifact.TenantID, request.OperationID)
	store.states[stateKey] = state
	store.receipts[receiptKey] = completed
	if err := store.persistLocked(); err != nil {
		state.Drive = priorDrive
		state.PendingOperationID = request.OperationID
		store.states[stateKey] = state
		store.receipts[receiptKey] = pending
		return pending, err
	}
	return completed, nil
}

func (store *ArtifactDispositionStore) completePendingDiscardLocked(ctx context.Context, request ArtifactDispositionRequest, stateKey string, state artifactDispositionState, pending ArtifactDispositionReceipt, effects ArtifactDispositionEffects) (ArtifactDispositionReceipt, error) {
	if pending.Outcome != "discard_pending" || state.PendingOperationID != request.OperationID || !pending.Artifact.Equal(state.Artifact) {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
	}
	preserveDrive := state.Drive != nil
	retracted, effectErr := effects.Discard(ctx, pending.Artifact, preserveDrive)
	if effectErr != nil && !errors.Is(effectErr, ErrArtifactDispositionConflict) {
		// The pending receipt is already durable. A retry or restart resumes the
		// same idempotent effects; no destructive action can exist without this
		// inspectable intent.
		return pending, effectErr
	}
	outcome := "discarded"
	if errors.Is(effectErr, ErrArtifactDispositionConflict) {
		outcome = "discard_conflicted"
		state.TombstonedAt = nil
	} else if preserveDrive {
		outcome = "chat_retracted_drive_preserved"
	}
	state.PendingOperationID = ""
	completed := newArtifactDispositionReceiptRevision(request, outcome, state.Drive, pending.PriorConfirmationID, nil, retracted, store.now().UTC(), pending.Header.Revision+1)
	receiptKey := artifactDispositionReceiptKey(request.Artifact.TenantID, request.OperationID)
	store.states[stateKey] = state
	store.receipts[receiptKey] = completed
	if err := store.persistLocked(); err != nil {
		// Restore the in-memory pending state to match the last durable image.
		state.PendingOperationID = request.OperationID
		if !preserveDrive {
			when := pending.OccurredAt
			state.TombstonedAt = &when
		}
		store.states[stateKey] = state
		store.receipts[receiptKey] = pending
		return pending, err
	}
	if effectErr != nil {
		return completed, effectErr
	}
	return completed, nil
}

func (store *ArtifactDispositionStore) HasPendingDiscard(request ArtifactDispositionRequest) bool {
	if store == nil || request.Validate() != nil || request.Action != ArtifactDispositionDiscard {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	receipt, ok := store.receipts[artifactDispositionReceiptKey(request.Artifact.TenantID, request.OperationID)]
	return ok && receipt.Outcome == "discard_pending" && artifactDispositionReceiptMatchesRequest(receipt, request)
}

func (store *ArtifactDispositionStore) ResumePendingDiscard(ctx context.Context, request ArtifactDispositionRequest, effects ArtifactDispositionEffects) (ArtifactDispositionReceipt, error) {
	if store == nil || !store.enabled || request.Validate() != nil || request.Action != ArtifactDispositionDiscard || effects == nil {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	receipt, ok := store.receipts[artifactDispositionReceiptKey(request.Artifact.TenantID, request.OperationID)]
	if !ok || receipt.Outcome != "discard_pending" || !artifactDispositionReceiptMatchesRequest(receipt, request) {
		return ArtifactDispositionReceipt{}, ErrArtifactDispositionConflict
	}
	stateKey := artifactDispositionStateKey(request.Artifact)
	return store.completePendingDiscardLocked(ctx, request, stateKey, store.states[stateKey], receipt, effects)
}

func newArtifactDispositionReceipt(request ArtifactDispositionRequest, outcome string, drive *ArtifactDriveReference, prior string, expires *time.Time, retracted int, now time.Time) ArtifactDispositionReceipt {
	return newArtifactDispositionReceiptRevision(request, outcome, drive, prior, expires, retracted, now, 1)
}

func newArtifactDispositionReceiptRevision(request ArtifactDispositionRequest, outcome string, drive *ArtifactDriveReference, prior string, expires *time.Time, retracted int, now time.Time, revision int64) ArtifactDispositionReceipt {
	receipt := ArtifactDispositionReceipt{
		Header:      STRIDEContractHeader{TenantID: request.Artifact.TenantID, ID: request.OperationID, Revision: revision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractArtifactDisposition, CreatedAt: now},
		OperationID: request.OperationID, Action: request.Action, ActorPrincipal: request.ActorPrincipal, Artifact: request.Artifact,
		Outcome: outcome, Drive: drive, ConfirmationID: request.ConfirmationID, PriorConfirmationID: prior,
		ConfirmationExpires: expires, RetractedReferences: retracted, OccurredAt: now,
	}
	receipt.Header.ContentDigest, _ = artifactDispositionReceiptDigest(receipt)
	return receipt
}

func artifactDispositionRefFromHeader(header ArtifactAuthorizationHeader) ArtifactDispositionRef {
	audience, _ := STRIDEContractDigest(struct {
		Visibility string `json:"visibility"`
		Owner      string `json:"owner,omitempty"`
		ACLVersion int64  `json:"aclVersion"`
	}{strings.ToLower(strings.TrimSpace(header.Visibility)), normalizeAccountEmail(header.OwnerEmail), header.ACLVersion})
	return ArtifactDispositionRef{
		TenantID: header.TenantID, ArtifactID: header.ObjectID, ContentRevision: header.ContentRevision,
		ContentDigest: header.ContentDigest, ACLVersion: header.ACLVersion, AudienceDigest: audience,
	}
}

type appArtifactDispositionEffects struct {
	app      *kanbanBoardApp
	user     *userAccount
	artifact meetingMemoryEntry
}

func (effects appArtifactDispositionEffects) Open(_ context.Context, ref ArtifactDispositionRef) error {
	if effects.app == nil || effects.app.memory == nil {
		return ErrArtifactDispositionDenied
	}
	var header ArtifactAuthorizationHeader
	if effects.artifact.ID != "" {
		header = resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(effects.artifact))
	} else {
		var found bool
		header, found = effects.app.memory.artifactAuthorizationHeaderByID(ref.ArtifactID)
		if !found {
			return ErrArtifactDispositionConflict
		}
	}
	if !artifactDispositionRefFromHeader(header).Equal(ref) {
		return ErrArtifactDispositionConflict
	}
	return nil
}

func (effects appArtifactDispositionEffects) Save(_ context.Context, ref ArtifactDispositionRef, actor, folderID string) (ArtifactDriveReference, error) {
	if err := effects.Open(context.Background(), ref); err != nil {
		return ArtifactDriveReference{}, err
	}
	row, err := effects.app.saveDeliverableSnapshotToFiles(effects.artifact, folderID, actor)
	if err != nil {
		return ArtifactDriveReference{}, err
	}
	return ArtifactDriveReference{ID: row.ID, Artifact: ref, CreatedAt: time.Now().UTC(), CreatedBy: actor, FolderID: folderID, SourceArtifactID: ref.ArtifactID}, nil
}

func (effects appArtifactDispositionEffects) Discard(ctx context.Context, ref ArtifactDispositionRef, preserveDrive bool) (int, error) {
	if preserveDrive {
		if err := effects.Open(ctx, ref); err != nil {
			return 0, err
		}
		return effects.app.retractArtifactChatReferences(ref.ArtifactID)
	}
	// Delete the exact revision first. Once the body is absent, chat/search
	// projection cleanup is idempotent and restart-safe; the durable pending
	// disposition receipt already exists before this method is called.
	_, _, deleted, err := effects.app.deleteOSArtifactAndEmitIfDispositionRefMatches(ref)
	if err != nil && !deleted {
		return 0, err
	}
	if err != nil {
		log.Errorf("artifact disposition delete event delivery failed after durable removal: %v", err)
	}
	return effects.app.retractArtifactChatReferences(ref.ArtifactID)
}

func (app *kanbanBoardApp) retractArtifactChatReferences(artifactID string) (int, error) {
	if app == nil || app.memory == nil || !strideIdentifier(artifactID) {
		return 0, ErrArtifactDispositionInvalid
	}
	entries := app.memory.entriesOfKind(meetingMemoryKindScoutChat, 0)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	retracted := 0
	for _, entry := range entries {
		lock := app.scoutChatThreadLock(entry.ID)
		lock.Lock()
		current, found := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, entry.ID)
		if !found {
			lock.Unlock()
			continue
		}
		thread, ok := decodeScoutChatThreadEntry(current)
		if !ok {
			lock.Unlock()
			return retracted, fmt.Errorf("decode chat projection %s", entry.ID)
		}
		next := make([]scoutChatMessageRecord, 0, len(thread.Messages))
		changed := false
		for _, message := range thread.Messages {
			if message.Thread != nil && strings.TrimSpace(message.Thread.ArtifactID) == artifactID {
				retracted++
				changed = true
				continue
			}
			if message.Image != nil && strings.TrimSpace(message.Image.ArtifactID) == artifactID {
				clone := message
				image := *message.Image
				image.ArtifactID = ""
				clone.Image = &image
				message = clone
				retracted++
				changed = true
			}
			next = append(next, message)
		}
		if changed {
			thread.Messages = next
			thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := app.saveScoutChatThread(thread); err != nil {
				lock.Unlock()
				return retracted, err
			}
		}
		lock.Unlock()
	}
	return retracted, nil
}

func (app *kanbanBoardApp) deleteOSArtifactAndEmitIfDispositionRefMatches(expected ArtifactDispositionRef) (meetingMemoryEntry, []scopedRoomDeliveryAcknowledgement, bool, error) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, nil, false, ErrArtifactDispositionDenied
	}
	removed, projection, deleted, err := app.memory.deleteOSArtifactWithProjectionIfDispositionRefMatches(expected)
	if err != nil || !deleted {
		return removed, nil, deleted, err
	}
	acks, err := emitOSArtifactDeletionEvent(app, projection)
	return removed, acks, true, err
}

func (store *meetingMemoryStore) deleteOSArtifactWithProjectionIfDispositionRefMatches(expected ArtifactDispositionRef) (meetingMemoryEntry, artifactDeletionProjection, bool, error) {
	if store == nil {
		return meetingMemoryEntry{}, artifactDeletionProjection{}, false, ErrArtifactDispositionDenied
	}
	id := expected.ArtifactID
	store.mu.Lock()
	defer store.mu.Unlock()
	index := -1
	for candidate := len(store.entries) - 1; candidate >= 0; candidate-- {
		if store.entries[candidate].ID == id && store.entries[candidate].Kind == meetingMemoryKindOSArtifact {
			index = candidate
			break
		}
	}
	if index < 0 {
		// A durable pending disposition may be resuming after the exact artifact
		// deletion committed but before projection cleanup/final receipt rewrite.
		return meetingMemoryEntry{}, artifactDeletionProjection{}, false, nil
	}
	removed := cloneMemoryEntry(store.entries[index])
	current := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: removed.ID, Kind: removed.Kind, Metadata: removed.Metadata}))
	if !artifactDispositionRefFromHeader(current).Equal(expected) {
		return meetingMemoryEntry{}, artifactDeletionProjection{}, false, ErrArtifactDispositionConflict
	}
	projection := mintArtifactDeletionProjection(current, osEvent{Kind: osEventArtifactDeleted, Ref: removed.ID,
		Title:         firstNonEmptyString(strings.TrimSpace(removed.Metadata["title"]), "Artifact removed"),
		OriginSurface: firstNonEmptyString(strings.TrimSpace(removed.Metadata["originSurface"]), strings.TrimSpace(removed.Metadata["originKind"]), "artifacts"), Actor: "system"})
	store.entries = append(store.entries[:index], store.entries[index+1:]...)
	if err := store.rewriteLocked(false); err != nil {
		store.entries = append(store.entries[:index], append([]meetingMemoryEntry{removed}, store.entries[index:]...)...)
		revokeArtifactDeletionProjection(projection)
		return meetingMemoryEntry{}, artifactDeletionProjection{}, false, err
	}
	delete(store.seen, id)
	return removed, projection, true, nil
}

var artifactDispositionRuntime = struct {
	sync.Mutex
	path  string
	store *ArtifactDispositionStore
	err   error
}{}

func productionArtifactDispositionStore() (*ArtifactDispositionStore, error) {
	enabled, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("BONFIRE_ARTIFACT_DISPOSITION_ENABLED")))
	// An environment toggle is not activation authority. A reviewed release
	// must also supply the digest of its disposition activation receipt.
	if !enabled || !isHexDigest(strings.TrimSpace(os.Getenv("BONFIRE_ARTIFACT_DISPOSITION_ACTIVATION_RECEIPT"))) {
		return nil, ErrArtifactDispositionDisabled
	}
	path := filepath.Join(filepath.Dir(meetingMemoryPath()), "artifact-dispositions.json")
	artifactDispositionRuntime.Lock()
	defer artifactDispositionRuntime.Unlock()
	if artifactDispositionRuntime.path != path || artifactDispositionRuntime.store == nil && artifactDispositionRuntime.err == nil {
		artifactDispositionRuntime.path = path
		artifactDispositionRuntime.store, artifactDispositionRuntime.err = OpenArtifactDispositionStore(path, true, artifactDiscardDefaultTTL)
	}
	return artifactDispositionRuntime.store, artifactDispositionRuntime.err
}

var artifactDispositionStoreForRequest = productionArtifactDispositionStore

var artifactDispositionDeleteAuthorized = func(ctx context.Context, user *userAccount, header ArtifactAuthorizationHeader) bool {
	runtime := currentCanonicalRuntime()
	if runtime == nil || runtime.postgres == nil || user == nil {
		return false
	}
	decision := (AuthorizationKernel{Store: runtime.postgres}).Authorize(ctx,
		ACLPrincipal{TenantID: header.TenantID, ID: normalizeAccountEmail(user.Email), Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}},
		ACLDelete,
		ACLObjectRef{TenantID: header.TenantID, Type: "artifact", ID: header.ObjectID, ACLVersion: header.ACLVersion},
		ACLRevisionRef{ContentRevision: header.ContentRevision, ContentDigest: header.ContentDigest},
	)
	return decision.Allowed
}

// artifactDispositionHandler is registered by the HTTP assembly at
// /api/artifact-dispositions/v1. Keeping registration outside this authority
// file makes the feature genuinely default-off and keeps tests on the exact
// handler rather than a second policy path.
func artifactDispositionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	store, err := artifactDispositionStoreForRequest()
	if err != nil || store == nil {
		writeAuthError(w, http.StatusServiceUnavailable, ErrArtifactDispositionDisabled.Error())
		return
	}
	payload := struct {
		OperationID    string                    `json:"operationId"`
		Action         ArtifactDispositionAction `json:"action"`
		Artifact       ArtifactDispositionRef    `json:"artifact"`
		FolderID       string                    `json:"folderId,omitempty"`
		ConfirmationID string                    `json:"confirmationId,omitempty"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, ErrArtifactDispositionInvalid.Error())
		return
	}
	request := ArtifactDispositionRequest{OperationID: payload.OperationID, Action: payload.Action, ActorPrincipal: strideRuntimePrincipalForEmail(user.Email), Artifact: payload.Artifact, FolderID: payload.FolderID, ConfirmationID: payload.ConfirmationID}
	if request.Validate() != nil {
		writeAuthError(w, http.StatusBadRequest, ErrArtifactDispositionInvalid.Error())
		return
	}
	// Once the second confirmation's pending receipt is durable, it is the
	// commit authority. Resume must not depend on a body that may already have
	// been deleted by the first attempt.
	if store.HasPendingDiscard(request) {
		receipt, err := store.ResumePendingDiscard(r.Context(), request, appArtifactDispositionEffects{app: kanbanApp, user: user})
		if err != nil {
			if errors.Is(err, ErrArtifactDispositionConflict) {
				writeAuthError(w, http.StatusConflict, err.Error())
			} else {
				writeAuthError(w, http.StatusInternalServerError, "artifact disposition reconciliation failed")
			}
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "receipt": receipt})
		return
	}
	actions := []ACLAction{ACLReadContent}
	switch payload.Action {
	case ArtifactDispositionSave:
		actions = append(actions, ACLWrite)
	case ArtifactDispositionDiscard:
		actions = append(actions, ACLDelete)
	case ArtifactDispositionOpen:
	default:
		writeAuthError(w, http.StatusBadRequest, ErrArtifactDispositionInvalid.Error())
		return
	}
	var artifact meetingMemoryEntry
	var ok bool
	if payload.Action == ArtifactDispositionDiscard {
		header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(payload.Artifact.ArtifactID)
		if found && artifactHeaderAuthorized(r.Context(), user, ACLReadContent, header) && artifactDispositionDeleteAuthorized(r.Context(), user, header) {
			artifact, ok = kanbanApp.memory.artifactSnapshotIfHeaderMatches(payload.Artifact.ArtifactID, header)
		}
	} else {
		artifact, ok = authorizedArtifactForActions(r.Context(), user, payload.Artifact.ArtifactID, actions...)
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	current := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	receipt, err := store.Apply(r.Context(), request, current, appArtifactDispositionEffects{app: kanbanApp, user: user, artifact: artifact})
	status := http.StatusOK
	if err != nil {
		switch {
		case errors.Is(err, ErrArtifactDispositionConfirm):
			status = http.StatusAccepted
		case errors.Is(err, ErrArtifactDispositionConflict), errors.Is(err, ErrArtifactDispositionExpired):
			writeAuthError(w, http.StatusConflict, err.Error())
			return
		case errors.Is(err, ErrArtifactDispositionInvalid):
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		default:
			writeAuthError(w, http.StatusInternalServerError, "artifact disposition failed")
			return
		}
	}
	writeAuthJSON(w, status, map[string]any{"ok": status == http.StatusOK, "receipt": receipt})
}
