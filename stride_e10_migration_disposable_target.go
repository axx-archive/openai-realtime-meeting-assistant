package main

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

type strideE10DisposableTargetApply struct {
	Request    StrideE10MigrationWriteRequest     `json:"request"`
	Receipt    StrideE10MigrationWriteReceipt     `json:"receipt"`
	BeforeRows []StrideE10CanonicalTargetRow      `json:"beforeRows"`
	RolledBack bool                               `json:"rolledBack"`
	Rollback   *StrideE10MigrationRollbackReceipt `json:"rollback,omitempty"`
}

type strideE10DisposableTargetState struct {
	Version   int                                       `json:"version"`
	HighWater uint64                                    `json:"highWater"`
	Rows      []StrideE10CanonicalTargetRow             `json:"rows"`
	Applies   map[string]strideE10DisposableTargetApply `json:"applies"`
}

// StrideE10DisposableMigrationTarget is an offline, signed canonical target
// used only by the rehearsal. Each method reloads its file so readback is
// independent of the Apply call's in-memory observation.
type StrideE10DisposableMigrationTarget struct {
	mu               sync.Mutex
	path             string
	initialHighWater uint64
	keys             StrideE10MigrationKeyring
	persistOverride  func(context.Context, strideE10DisposableTargetState) error
}

func NewStrideE10DisposableMigrationTarget(path string, initialHighWater uint64, keys StrideE10MigrationKeyring) *StrideE10DisposableMigrationTarget {
	return &StrideE10DisposableMigrationTarget{path: path, initialHighWater: initialHighWater, keys: keys}
}

func (t *StrideE10DisposableMigrationTarget) strideE10DisposableTargetPath() string {
	if t == nil {
		return ""
	}
	return t.path
}

func (t *StrideE10DisposableMigrationTarget) load(ctx context.Context) (strideE10DisposableTargetState, error) {
	if strings.TrimSpace(t.path) == "" || t.initialHighWater == 0 || t.keys == nil {
		return strideE10DisposableTargetState{}, errors.New("complete disposable target authority is required")
	}
	raw, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return strideE10DisposableTargetState{Version: strideE10MigrationStateVersion, HighWater: t.initialHighWater, Applies: map[string]strideE10DisposableTargetApply{}}, nil
	}
	if err != nil {
		return strideE10DisposableTargetState{}, err
	}
	var state strideE10DisposableTargetState
	if err := strideE10UnmarshalSigned(ctx, t.keys, raw, &state); err != nil {
		return strideE10DisposableTargetState{}, err
	}
	if state.Version != strideE10MigrationStateVersion || state.HighWater == 0 || state.Applies == nil {
		return strideE10DisposableTargetState{}, errors.New("invalid disposable target state")
	}
	var envelope strideE10MigrationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return strideE10DisposableTargetState{}, err
	}
	current, err := t.keys.CurrentStrideE10MigrationKey(ctx)
	if err != nil {
		return strideE10DisposableTargetState{}, err
	}
	if envelope.KeyID != current.ID || envelope.KeyVersion != current.Version {
		if err := strideE10WriteSigned(ctx, t.path, t.keys, state); err != nil {
			return strideE10DisposableTargetState{}, fmt.Errorf("reseal disposable target with current managed key: %w", err)
		}
	}
	return state, nil
}

func (t *StrideE10DisposableMigrationTarget) persist(ctx context.Context, state strideE10DisposableTargetState) error {
	if t.persistOverride != nil {
		return t.persistOverride(ctx, state)
	}
	return strideE10WriteSigned(ctx, t.path, t.keys, state)
}

func strideE10SameWriteRequest(left, right StrideE10MigrationWriteRequest) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return hmac.Equal(a, b)
}

func (t *StrideE10DisposableMigrationTarget) ApplyStrideE10Migration(ctx context.Context, request StrideE10MigrationWriteRequest) (StrideE10MigrationWriteObservation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if request.ExpectedDelta != StrideE10MigrationExpectedTargetDelta || request.OperationID == "" || validateStrideE10CanonicalTargetManifest(request.Manifest) != nil {
		return StrideE10MigrationWriteObservation{}, errors.New("invalid disposable target write request")
	}
	state, err := t.load(ctx)
	if err != nil {
		return StrideE10MigrationWriteObservation{}, err
	}
	if prior, ok := state.Applies[request.OperationID]; ok && !prior.RolledBack {
		if !strideE10SameWriteRequest(prior.Request, request) {
			return StrideE10MigrationWriteObservation{}, errors.New("durable operation request drift")
		}
		return StrideE10MigrationWriteObservation{Receipt: prior.Receipt}, nil
	}
	beforeRows := append([]StrideE10CanonicalTargetRow(nil), state.Rows...)
	beforeHighWater := state.HighWater
	state.Rows = append([]StrideE10CanonicalTargetRow(nil), request.Manifest.Rows...)
	state.HighWater = beforeHighWater + request.ExpectedDelta
	receipt := StrideE10MigrationWriteReceipt{
		OperationID: request.OperationID, MigrationDigest: request.MigrationDigest, SourceDigest: request.SourceDigest,
		BackupIdentityDigest: request.BackupIdentityDigest, BeforeHighWater: beforeHighWater, AfterHighWater: state.HighWater,
		ExpectedDelta: request.ExpectedDelta, ManifestDigest: request.Manifest.Digest,
		BeforeSnapshotDigest: strideE10TargetSnapshotDigest(beforeHighWater, beforeRows),
		AfterSnapshotDigest:  strideE10TargetSnapshotDigest(state.HighWater, state.Rows),
	}
	receipt.ReceiptDigest = strideE10MigrationWriteReceiptDigest(receipt)
	state.Applies[request.OperationID] = strideE10DisposableTargetApply{Request: request, Receipt: receipt, BeforeRows: beforeRows}
	if err := t.persist(ctx, state); err != nil {
		return StrideE10MigrationWriteObservation{}, err
	}
	return StrideE10MigrationWriteObservation{Receipt: receipt}, nil
}

func (t *StrideE10DisposableMigrationTarget) ReadStrideE10MigrationTarget(ctx context.Context, _ string) (StrideE10MigrationTargetReadback, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, err := t.load(ctx)
	if err != nil {
		return StrideE10MigrationTargetReadback{}, err
	}
	rows := append([]StrideE10CanonicalTargetRow(nil), state.Rows...)
	return StrideE10MigrationTargetReadback{HighWater: state.HighWater, Rows: rows, SnapshotDigest: strideE10TargetSnapshotDigest(state.HighWater, rows)}, nil
}

func strideE10MigrationRollbackReceiptDigest(receipt StrideE10MigrationRollbackReceipt) string {
	receipt.ReceiptDigest = ""
	raw, _ := json.Marshal(receipt)
	return strideE10Digest("target-rollback-receipt", raw)
}

func (t *StrideE10DisposableMigrationTarget) RollbackStrideE10Migration(ctx context.Context, request StrideE10MigrationRollbackRequest) (StrideE10MigrationRollbackReceipt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, err := t.load(ctx)
	if err != nil {
		return StrideE10MigrationRollbackReceipt{}, err
	}
	apply, ok := state.Applies[request.OperationID]
	if !ok || (request.WriterReceiptDigest != "" && apply.Receipt.ReceiptDigest != request.WriterReceiptDigest) || apply.Request.MigrationDigest != request.MigrationDigest || apply.Request.SourceDigest != request.SourceDigest || apply.Request.BackupIdentityDigest != request.BackupIdentityDigest || apply.Request.Manifest.Digest != request.ManifestDigest || request.ExpectedBeforeHighWater != apply.Receipt.BeforeHighWater || request.ExpectedBeforeSnapshotDigest != apply.Receipt.BeforeSnapshotDigest {
		return StrideE10MigrationRollbackReceipt{}, errors.New("rollback authority binding drift")
	}
	if apply.RolledBack && apply.Rollback != nil {
		return *apply.Rollback, nil
	}
	if state.HighWater != apply.Receipt.AfterHighWater || strideE10TargetSnapshotDigest(state.HighWater, state.Rows) != apply.Receipt.AfterSnapshotDigest {
		return StrideE10MigrationRollbackReceipt{}, errors.New("target changed after migration apply")
	}
	state.Rows = append([]StrideE10CanonicalTargetRow(nil), apply.BeforeRows...)
	state.HighWater = apply.Receipt.BeforeHighWater
	receipt := StrideE10MigrationRollbackReceipt{OperationID: request.OperationID, BeforeHighWater: apply.Receipt.BeforeHighWater, RestoredHighWater: state.HighWater, BeforeSnapshotDigest: apply.Receipt.BeforeSnapshotDigest, RestoredDigest: strideE10TargetSnapshotDigest(state.HighWater, state.Rows)}
	receipt.ReceiptDigest = strideE10MigrationRollbackReceiptDigest(receipt)
	apply.RolledBack, apply.Rollback = true, &receipt
	state.Applies[request.OperationID] = apply
	if err := t.persist(ctx, state); err != nil {
		return StrideE10MigrationRollbackReceipt{}, err
	}
	return receipt, nil
}
