package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrBrainProjectionCheckpointInvalid     = errors.New("invalid brain projection checkpoint")
	ErrBrainProjectionCheckpointUnavailable = errors.New("brain projection checkpoint store unavailable")
	ErrBrainProjectionCheckpointCorrupt     = errors.New("brain projection checkpoint is corrupt")
	ErrBrainProjectionRegression            = errors.New("brain projection checkpoint would regress")
	ErrBrainProjectionConflict              = errors.New("brain projection checkpoint conflicts with published output")
	ErrBrainProjectionFenceLost             = errors.New("brain projection rebuild fence lost")
	ErrBrainProjectionRebuildIncomplete     = errors.New("brain projection rebuild has not reached its end watermark")
	ErrBrainProjectionDerivedNotDurable     = errors.New("brain projection derived output is not durably stored")
	ErrBrainProjectionSourceMoved           = errors.New("brain projection source high-water or manifest changed")
)

const (
	BrainProjectionStaleCheckpointAbsent      = "checkpoint_absent"
	BrainProjectionStaleCheckpointUnavailable = "checkpoint_store_unavailable"
	BrainProjectionStaleCheckpointCorrupt     = "checkpoint_corrupt"
	BrainProjectionStaleRebuildInProgress     = "rebuild_in_progress"
	BrainProjectionStaleSourceAhead           = "source_ahead_of_checkpoint"
	BrainProjectionStaleSourceBehind          = "source_behind_checkpoint"
	BrainProjectionStaleSourceManifestChanged = "source_manifest_changed"
)

type BrainProjectionCheckpointKey struct {
	TenantID         string
	ProjectorVersion string
	RoomID           string
	SittingID        string
	SourceFamily     string
}

func (key BrainProjectionCheckpointKey) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"tenant_id", key.TenantID}, {"projector_version", key.ProjectorVersion},
		{"room_id", key.RoomID}, {"sitting_id", key.SittingID}, {"source_family", key.SourceFamily},
	} {
		if field.value == "" || field.value != strings.TrimSpace(field.value) {
			return fmt.Errorf("%w: %s is required and must be trimmed", ErrBrainProjectionCheckpointInvalid, field.name)
		}
	}
	return nil
}

func (key BrainProjectionCheckpointKey) advisoryLockID() int64 {
	hash := sha256.New()
	for _, value := range []string{key.TenantID, key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]))
}

// sourceAdvisoryLockID excludes projector version: canonical mutations and
// every projector publication for the same tenant/family/room/sitting must
// share one PostgreSQL transaction boundary.
func (key BrainProjectionCheckpointKey) sourceAdvisoryLockID() int64 {
	hash := sha256.New()
	_, _ = hash.Write([]byte("bonfire-brain-projection-source-v1"))
	for _, value := range []string{key.TenantID, key.SourceFamily, key.RoomID, key.SittingID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	return int64(binary.BigEndian.Uint64(hash.Sum(nil)[:8]))
}

type BrainProjectionSourceState struct {
	HighWater      int64
	ManifestSHA256 [sha256.Size]byte
}

func (state BrainProjectionSourceState) valid() bool {
	return state.HighWater >= 0 && state.ManifestSHA256 != [sha256.Size]byte{}
}

// SourceHighWaterResolver reads canonical source state using
// the same PostgreSQL transaction that owns the checkpoint advisory lock.
// Implementations must resolve the complete key's source family, room, and
// sitting rather than a projector-wide cursor.
type SourceHighWaterResolver interface {
	ResolveBrainProjectionSourceState(context.Context, pgx.Tx, BrainProjectionCheckpointKey) (BrainProjectionSourceState, error)
}

// BrainProjectionRebuildFenceToken is intentionally opaque. Callers can carry
// a token returned by BeginRebuild into derived output, but cannot mint or
// inspect one through the public API.
type BrainProjectionRebuildFenceToken struct{ value [sha256.Size]byte }

func (token BrainProjectionRebuildFenceToken) isZero() bool {
	return token == BrainProjectionRebuildFenceToken{}
}

func tokenFromBytes(raw []byte) (BrainProjectionRebuildFenceToken, error) {
	if len(raw) != sha256.Size {
		return BrainProjectionRebuildFenceToken{}, ErrBrainProjectionCheckpointCorrupt
	}
	var token BrainProjectionRebuildFenceToken
	copy(token.value[:], raw)
	return token, nil
}

func newBrainProjectionRebuildFenceToken() (BrainProjectionRebuildFenceToken, error) {
	var token BrainProjectionRebuildFenceToken
	if _, err := rand.Read(token.value[:]); err != nil {
		return token, fmt.Errorf("mint rebuild fence: %w", err)
	}
	if token.isZero() {
		return newBrainProjectionRebuildFenceToken()
	}
	return token, nil
}

type BrainProjectionCheckpoint struct {
	Key                         BrainProjectionCheckpointKey
	HasPublication              bool
	SourceHighWater             int64
	SourceManifestSHA256        [sha256.Size]byte
	DerivedHighWater            int64
	DerivedID                   string
	DerivedSHA256               [sha256.Size]byte
	PublishedGeneration         int64
	PublishedRebuildFenceToken  BrainProjectionRebuildFenceToken
	RebuildGeneration           int64
	RebuildActive               bool
	RebuildStartHighWater       int64
	RebuildEndHighWater         int64
	RebuildSourceManifestSHA256 [sha256.Size]byte
	RebuildFenceToken           BrainProjectionRebuildFenceToken
	RebuildStartedAt            time.Time
	PublishedAt                 time.Time
}

type BrainProjectionCheckpointStatus struct {
	Checkpoint BrainProjectionCheckpoint
	Stale      bool
	Reason     string
}

type BrainProjectionDurableReceipt struct {
	DerivedID            string
	DerivedHighWater     int64
	DerivedSHA256        [sha256.Size]byte
	SourceManifestSHA256 [sha256.Size]byte
	RebuildFenceToken    BrainProjectionRebuildFenceToken
	DurableAt            time.Time
}

type BrainProjectionPublishResult struct {
	Checkpoint BrainProjectionCheckpoint
	DerivedID  string
	Existing   bool
	Status     BrainProjectionCheckpointStatus
}

type BrainProjectionRebuildRequest struct {
	Key                  BrainProjectionCheckpointKey
	ExpectedGeneration   int64
	StartSourceHighWater int64
	EndSourceHighWater   int64
}

func (request BrainProjectionRebuildRequest) Validate() error {
	if err := request.Key.Validate(); err != nil {
		return err
	}
	if request.ExpectedGeneration < 0 || request.ExpectedGeneration == math.MaxInt64 || request.StartSourceHighWater < 0 || request.EndSourceHighWater < request.StartSourceHighWater {
		return fmt.Errorf("%w: rebuild requires a valid generation and explicit ordered start/end watermarks", ErrBrainProjectionCheckpointInvalid)
	}
	return nil
}

type BrainProjectionRebuildFence struct {
	Key                  BrainProjectionCheckpointKey
	Generation           int64
	StartSourceHighWater int64
	EndSourceHighWater   int64
	SourceManifestSHA256 [sha256.Size]byte
	Token                BrainProjectionRebuildFenceToken
	StartedAt            time.Time
	Existing             bool
}

func BrainProjectionDerivedID(key BrainProjectionCheckpointKey, generation, sourceHighWater, derivedHighWater int64, derivedDigest, sourceManifest [sha256.Size]byte, fence BrainProjectionRebuildFenceToken) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("bonfire-brain-projection-v2"))
	for _, value := range []string{key.TenantID, key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	for _, value := range []int64{generation, sourceHighWater, derivedHighWater} {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		_, _ = hash.Write(encoded[:])
	}
	_, _ = hash.Write(derivedDigest[:])
	_, _ = hash.Write(sourceManifest[:])
	_, _ = hash.Write(fence.value[:])
	return "brain-projection-" + hex.EncodeToString(hash.Sum(nil))
}

type BrainProjectionDerivedOutput struct {
	Key                  BrainProjectionCheckpointKey
	SourceHighWater      int64
	SourceManifestSHA256 [sha256.Size]byte
	DerivedHighWater     int64
	RebuildGeneration    int64
	RebuildFenceToken    BrainProjectionRebuildFenceToken
	DerivedID            string
	DerivedSHA256        [sha256.Size]byte
	Body                 []byte
}

func NewBrainProjectionDerivedOutput(key BrainProjectionCheckpointKey, generation int64, fence BrainProjectionRebuildFenceToken, source BrainProjectionSourceState, derivedHighWater int64, body []byte) (BrainProjectionDerivedOutput, error) {
	if err := key.Validate(); err != nil {
		return BrainProjectionDerivedOutput{}, err
	}
	if generation < 0 || !source.valid() || derivedHighWater < 0 || (generation == 0 && !fence.isZero()) || (generation > 0 && fence.isZero()) {
		return BrainProjectionDerivedOutput{}, fmt.Errorf("%w: output generation, fence, or high-water is invalid", ErrBrainProjectionCheckpointInvalid)
	}
	output := BrainProjectionDerivedOutput{
		Key: key, RebuildGeneration: generation, RebuildFenceToken: fence,
		SourceHighWater: source.HighWater, SourceManifestSHA256: source.ManifestSHA256,
		DerivedHighWater: derivedHighWater, Body: append([]byte(nil), body...), DerivedSHA256: sha256.Sum256(body),
	}
	output.DerivedID = BrainProjectionDerivedID(key, generation, source.HighWater, derivedHighWater, output.DerivedSHA256, source.ManifestSHA256, fence)
	return output, nil
}

func (output BrainProjectionDerivedOutput) validate() error {
	want, err := NewBrainProjectionDerivedOutput(output.Key, output.RebuildGeneration, output.RebuildFenceToken,
		BrainProjectionSourceState{HighWater: output.SourceHighWater, ManifestSHA256: output.SourceManifestSHA256}, output.DerivedHighWater, output.Body)
	if err != nil {
		return err
	}
	if output.DerivedID != want.DerivedID || output.DerivedSHA256 != want.DerivedSHA256 {
		return fmt.Errorf("%w: output identity does not match its bytes and fences", ErrBrainProjectionCheckpointInvalid)
	}
	return nil
}

type brainProjectionDerivedSink interface {
	PutBrainProjectionDerived(context.Context, BrainProjectionDerivedOutput) (BrainProjectionDurableReceipt, error)
	VerifyBrainProjectionDerived(context.Context, BrainProjectionDerivedOutput, BrainProjectionDurableReceipt) error
}

type PostgresBrainProjectionCheckpointStore struct {
	pool      *pgxpool.Pool
	sources   SourceHighWaterResolver
	failpoint func(string) error
}

func NewPostgresBrainProjectionCheckpointStore(pool *pgxpool.Pool, sources SourceHighWaterResolver) *PostgresBrainProjectionCheckpointStore {
	return &PostgresBrainProjectionCheckpointStore{pool: pool, sources: sources}
}

func NewBrainProjectionCheckpointStore(canonical *PostgresCanonicalStore, sources SourceHighWaterResolver) *PostgresBrainProjectionCheckpointStore {
	if canonical == nil {
		return NewPostgresBrainProjectionCheckpointStore(nil, sources)
	}
	return NewPostgresBrainProjectionCheckpointStore(canonical.pool, sources)
}

func unavailableBrainProjectionCheckpoint(op string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrBrainProjectionCheckpointUnavailable, op)
	}
	return fmt.Errorf("%w: %s: %w", ErrBrainProjectionCheckpointUnavailable, op, err)
}

func (store *PostgresBrainProjectionCheckpointStore) Status(ctx context.Context, key BrainProjectionCheckpointKey) (BrainProjectionCheckpointStatus, error) {
	status := BrainProjectionCheckpointStatus{Stale: true, Reason: BrainProjectionStaleCheckpointUnavailable}
	if err := key.Validate(); err != nil {
		return status, err
	}
	if store == nil || store.pool == nil || store.sources == nil {
		return status, unavailableBrainProjectionCheckpoint("read checkpoint", nil)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return status, unavailableBrainProjectionCheckpoint("begin checkpoint status", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	source, err := store.sources.ResolveBrainProjectionSourceState(ctx, tx, key)
	if err != nil || !source.valid() {
		return status, unavailableBrainProjectionCheckpoint("resolve checkpoint source status", err)
	}
	checkpoint, found, err := loadBrainProjectionCheckpoint(ctx, tx, key, false)
	if err != nil {
		if errors.Is(err, ErrBrainProjectionCheckpointCorrupt) {
			status.Reason = BrainProjectionStaleCheckpointCorrupt
			return status, err
		}
		return status, unavailableBrainProjectionCheckpoint("read checkpoint", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return status, unavailableBrainProjectionCheckpoint("commit checkpoint status", err)
	}
	status.Checkpoint = checkpoint
	if !found || !checkpoint.HasPublication {
		status.Reason = BrainProjectionStaleCheckpointAbsent
		if found && checkpoint.RebuildActive {
			status.Reason = BrainProjectionStaleRebuildInProgress
		}
		return status, nil
	}
	if checkpoint.RebuildActive {
		status.Reason = BrainProjectionStaleRebuildInProgress
		return status, nil
	}
	if checkpoint.SourceHighWater < source.HighWater {
		status.Reason = BrainProjectionStaleSourceAhead
		return status, nil
	}
	if checkpoint.SourceHighWater > source.HighWater {
		status.Reason = BrainProjectionStaleSourceBehind
		return status, nil
	}
	if checkpoint.SourceManifestSHA256 != source.ManifestSHA256 {
		status.Reason = BrainProjectionStaleSourceManifestChanged
		return status, nil
	}
	status.Stale = false
	status.Reason = ""
	return status, nil
}

type brainProjectionRebuildTransactionHook func(context.Context, pgx.Tx, BrainProjectionRebuildFence) error

// beginRebuild requires the caller to durably bind its authority in the same
// transaction as the checkpoint fence. There is deliberately no exported or
// hook-free rebuild API: an active fence without its authorization audit must
// never become recoverable projection work.
func (store *PostgresBrainProjectionCheckpointStore) beginRebuild(ctx context.Context, request BrainProjectionRebuildRequest, hook brainProjectionRebuildTransactionHook) (BrainProjectionRebuildFence, error) {
	fence := BrainProjectionRebuildFence{Key: request.Key, StartSourceHighWater: request.StartSourceHighWater, EndSourceHighWater: request.EndSourceHighWater}
	if err := request.Validate(); err != nil {
		return fence, err
	}
	if hook == nil {
		return fence, ErrBrainProjectionBackfillUnauthorized
	}
	if store == nil || store.pool == nil || store.sources == nil {
		return fence, unavailableBrainProjectionCheckpoint("begin rebuild", nil)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fence, unavailableBrainProjectionCheckpoint("begin rebuild transaction", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockBrainProjectionCheckpoint(ctx, tx, request.Key); err != nil {
		return fence, unavailableBrainProjectionCheckpoint("lock rebuild fence", err)
	}
	if err := lockBrainProjectionSource(ctx, tx, request.Key); err != nil {
		return fence, unavailableBrainProjectionCheckpoint("lock rebuild source", err)
	}
	source, err := store.sources.ResolveBrainProjectionSourceState(ctx, tx, request.Key)
	if err != nil {
		return fence, unavailableBrainProjectionCheckpoint("resolve rebuild source", err)
	}
	if !source.valid() || source.HighWater != request.EndSourceHighWater {
		return fence, ErrBrainProjectionSourceMoved
	}
	fence.SourceManifestSHA256 = source.ManifestSHA256
	current, found, err := loadBrainProjectionCheckpoint(ctx, tx, request.Key, true)
	if err != nil {
		return fence, err
	}
	if found && current.HasPublication && request.EndSourceHighWater < current.SourceHighWater {
		return fence, ErrBrainProjectionRegression
	}
	if found && current.RebuildActive {
		if current.RebuildGeneration == request.ExpectedGeneration+1 && current.RebuildStartHighWater == request.StartSourceHighWater &&
			current.RebuildEndHighWater == request.EndSourceHighWater && current.RebuildSourceManifestSHA256 == source.ManifestSHA256 {
			fence.Generation, fence.Token, fence.StartedAt, fence.Existing = current.RebuildGeneration, current.RebuildFenceToken, current.RebuildStartedAt, true
			if err := hook(ctx, tx, fence); err != nil {
				return fence, err
			}
			if err := tx.Commit(ctx); err != nil {
				return fence, unavailableBrainProjectionCheckpoint("commit idempotent rebuild fence", err)
			}
			return fence, nil
		}
		return fence, ErrBrainProjectionFenceLost
	}
	if (found && current.RebuildGeneration != request.ExpectedGeneration) || (!found && request.ExpectedGeneration != 0) {
		return fence, ErrBrainProjectionFenceLost
	}
	fence.Generation = request.ExpectedGeneration + 1
	fence.Token, err = newBrainProjectionRebuildFenceToken()
	if err != nil {
		return fence, err
	}
	if found {
		err = tx.QueryRow(ctx, `UPDATE brain_projection_checkpoints SET rebuild_generation=$6,
			rebuild_start_high_water=$7,rebuild_end_high_water=$8,rebuild_source_manifest_sha256=$9,
			rebuild_fence_token=$10,rebuild_started_at=now()
			WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5
			RETURNING rebuild_started_at`, request.Key.TenantID, request.Key.ProjectorVersion, request.Key.RoomID, request.Key.SittingID,
			request.Key.SourceFamily, fence.Generation, request.StartSourceHighWater, request.EndSourceHighWater,
			source.ManifestSHA256[:], fence.Token.value[:]).Scan(&fence.StartedAt)
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO brain_projection_checkpoints (
			tenant_id,projector_version,room_id,sitting_id,source_family,rebuild_generation,
			rebuild_start_high_water,rebuild_end_high_water,rebuild_source_manifest_sha256,rebuild_fence_token,rebuild_started_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now()) RETURNING rebuild_started_at`, request.Key.TenantID,
			request.Key.ProjectorVersion, request.Key.RoomID, request.Key.SittingID, request.Key.SourceFamily, fence.Generation,
			request.StartSourceHighWater, request.EndSourceHighWater, source.ManifestSHA256[:], fence.Token.value[:]).Scan(&fence.StartedAt)
	}
	if err != nil {
		return fence, unavailableBrainProjectionCheckpoint("persist rebuild fence", err)
	}
	if err := hook(ctx, tx, fence); err != nil {
		return fence, err
	}
	if err := tx.Commit(ctx); err != nil {
		return fence, unavailableBrainProjectionCheckpoint("commit rebuild fence", err)
	}
	return fence, nil
}

// The proof and verified publication are unexported and the store exposes no
// Publish method. Ordinary callers cannot turn a self-asserted receipt into a
// checkpoint mutation; only the package-private publisher can create the
// proof after the configured sink has verified durable bytes.
type brainProjectionDurabilityProof struct {
	checkpointStore *PostgresBrainProjectionCheckpointStore
	verify          func(context.Context) error
	verifiedAt      time.Time
}

type verifiedBrainProjectionPublication struct {
	output  BrainProjectionDerivedOutput
	receipt BrainProjectionDurableReceipt
	proof   *brainProjectionDurabilityProof
}

func (store *PostgresBrainProjectionCheckpointStore) publishVerified(ctx context.Context, publication verifiedBrainProjectionPublication) (BrainProjectionPublishResult, error) {
	result := BrainProjectionPublishResult{DerivedID: publication.output.DerivedID}
	if publication.proof == nil || publication.proof.checkpointStore != store || publication.proof.verify == nil || publication.receipt.DurableAt.IsZero() {
		return result, ErrBrainProjectionDerivedNotDurable
	}
	if store == nil || store.pool == nil || store.sources == nil {
		result.Status = BrainProjectionCheckpointStatus{Stale: true, Reason: BrainProjectionStaleCheckpointUnavailable}
		return result, unavailableBrainProjectionCheckpoint("publish checkpoint", nil)
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, unavailableBrainProjectionCheckpoint("begin checkpoint publication", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockBrainProjectionCheckpoint(ctx, tx, publication.output.Key); err != nil {
		return result, unavailableBrainProjectionCheckpoint("lock checkpoint publication", err)
	}
	if err := lockBrainProjectionSource(ctx, tx, publication.output.Key); err != nil {
		return result, unavailableBrainProjectionCheckpoint("lock publication source", err)
	}
	source, err := store.sources.ResolveBrainProjectionSourceState(ctx, tx, publication.output.Key)
	if err != nil {
		return result, unavailableBrainProjectionCheckpoint("resolve publication source", err)
	}
	current, found, err := loadBrainProjectionCheckpoint(ctx, tx, publication.output.Key, true)
	if err != nil {
		return result, err
	}
	output := publication.output
	if !found {
		if !source.valid() || source.HighWater != output.SourceHighWater || source.ManifestSHA256 != output.SourceManifestSHA256 {
			return result, ErrBrainProjectionSourceMoved
		}
		if output.RebuildGeneration != 0 || !output.RebuildFenceToken.isZero() {
			return result, ErrBrainProjectionFenceLost
		}
	} else {
		if output.RebuildGeneration != current.RebuildGeneration {
			return result, ErrBrainProjectionFenceLost
		}
		if current.RebuildActive {
			if output.RebuildFenceToken != current.RebuildFenceToken {
				return result, ErrBrainProjectionFenceLost
			}
			if output.SourceHighWater != current.RebuildEndHighWater || output.SourceManifestSHA256 != current.RebuildSourceManifestSHA256 {
				return result, ErrBrainProjectionRebuildIncomplete
			}
			// The rebuild publishes the exact source snapshot durably captured at
			// authorization time. Canonical appends or purges that landed after
			// that fence remain queued and make this checkpoint immediately stale;
			// they must not prevent the bounded generation from completing.
			if !source.valid() || source.HighWater < current.RebuildEndHighWater {
				return result, ErrBrainProjectionSourceMoved
			}
		} else if current.HasPublication {
			if !source.valid() || source.HighWater != output.SourceHighWater || source.ManifestSHA256 != output.SourceManifestSHA256 {
				return result, ErrBrainProjectionSourceMoved
			}
			if output.RebuildFenceToken != current.PublishedRebuildFenceToken {
				return result, ErrBrainProjectionFenceLost
			}
			if output.SourceHighWater < current.SourceHighWater || output.DerivedHighWater < current.DerivedHighWater {
				return result, ErrBrainProjectionRegression
			}
			if output.SourceHighWater == current.SourceHighWater && output.DerivedHighWater == current.DerivedHighWater {
				if output.DerivedID != current.DerivedID || output.DerivedSHA256 != current.DerivedSHA256 || output.SourceManifestSHA256 != current.SourceManifestSHA256 {
					return result, ErrBrainProjectionConflict
				}
				result.Checkpoint, result.Existing = current, true
				result.Status = BrainProjectionCheckpointStatus{Checkpoint: current}
				if err := tx.Commit(ctx); err != nil {
					return result, unavailableBrainProjectionCheckpoint("commit idempotent publication", err)
				}
				return result, nil
			}
		} else {
			return result, ErrBrainProjectionCheckpointCorrupt
		}
	}
	// Re-verify through the trusted sink after all source/fence checks and
	// immediately before the checkpoint write. A caller-provided receipt is
	// never itself authority to mutate the cursor.
	if err := publication.proof.verify(ctx); err != nil {
		return result, fmt.Errorf("%w: %v", ErrBrainProjectionDerivedNotDurable, err)
	}
	publication.proof.verifiedAt = time.Now().UTC()
	if current.RebuildActive && publication.receipt.DurableAt.Before(current.RebuildStartedAt) {
		return result, ErrBrainProjectionDerivedNotDurable
	}
	if store.failpoint != nil {
		if err := store.failpoint("after_checkpoint_lock_before_write"); err != nil {
			return result, err
		}
	}
	var publishedAt time.Time
	if !found {
		err = tx.QueryRow(ctx, `INSERT INTO brain_projection_checkpoints (
			tenant_id,projector_version,room_id,sitting_id,source_family,source_high_water,source_manifest_sha256,
			derived_high_water,derived_id,derived_sha256,published_generation,published_rebuild_fence_token,
			rebuild_generation,published_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$11,now()) RETURNING published_at`, output.Key.TenantID,
			output.Key.ProjectorVersion, output.Key.RoomID, output.Key.SittingID, output.Key.SourceFamily, output.SourceHighWater,
			output.SourceManifestSHA256[:], output.DerivedHighWater, output.DerivedID, output.DerivedSHA256[:],
			output.RebuildGeneration, output.RebuildFenceToken.value[:]).Scan(&publishedAt)
	} else {
		err = tx.QueryRow(ctx, `UPDATE brain_projection_checkpoints SET source_high_water=$6,source_manifest_sha256=$7,
			derived_high_water=$8,derived_id=$9,derived_sha256=$10,published_generation=$11,
			published_rebuild_fence_token=$12,published_at=now(),rebuild_start_high_water=NULL,
			rebuild_end_high_water=NULL,rebuild_source_manifest_sha256=NULL,rebuild_fence_token=NULL,rebuild_started_at=NULL
			WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5 RETURNING published_at`,
			output.Key.TenantID, output.Key.ProjectorVersion, output.Key.RoomID, output.Key.SittingID, output.Key.SourceFamily,
			output.SourceHighWater, output.SourceManifestSHA256[:], output.DerivedHighWater, output.DerivedID,
			output.DerivedSHA256[:], output.RebuildGeneration, output.RebuildFenceToken.value[:]).Scan(&publishedAt)
	}
	if err != nil {
		return result, unavailableBrainProjectionCheckpoint("write checkpoint publication", err)
	}
	result.Checkpoint = checkpointFromOutput(output, publishedAt)
	if store.failpoint != nil {
		if err := store.failpoint("after_checkpoint_write_before_commit"); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, unavailableBrainProjectionCheckpoint("commit checkpoint publication", err)
	}
	result.Status = BrainProjectionCheckpointStatus{Checkpoint: result.Checkpoint}
	if store.failpoint != nil {
		if err := store.failpoint("after_checkpoint_commit"); err != nil {
			return result, err
		}
	}
	return result, nil
}

func checkpointFromOutput(output BrainProjectionDerivedOutput, publishedAt time.Time) BrainProjectionCheckpoint {
	return BrainProjectionCheckpoint{
		Key: output.Key, HasPublication: true, SourceHighWater: output.SourceHighWater,
		SourceManifestSHA256: output.SourceManifestSHA256, DerivedHighWater: output.DerivedHighWater,
		DerivedID: output.DerivedID, DerivedSHA256: output.DerivedSHA256, PublishedGeneration: output.RebuildGeneration,
		PublishedRebuildFenceToken: output.RebuildFenceToken, RebuildGeneration: output.RebuildGeneration, PublishedAt: publishedAt.UTC(),
	}
}

type brainProjectionCheckpointPublisher struct {
	checkpoints *PostgresBrainProjectionCheckpointStore
	derived     brainProjectionDerivedSink
	failpoint   func(string) error
}

func newBrainProjectionCheckpointPublisher(checkpoints *PostgresBrainProjectionCheckpointStore, derived brainProjectionDerivedSink) *brainProjectionCheckpointPublisher {
	return &brainProjectionCheckpointPublisher{checkpoints: checkpoints, derived: derived}
}

func (publisher *brainProjectionCheckpointPublisher) Publish(ctx context.Context, output BrainProjectionDerivedOutput) (BrainProjectionPublishResult, error) {
	result := BrainProjectionPublishResult{DerivedID: output.DerivedID}
	if publisher == nil || publisher.derived == nil || publisher.checkpoints == nil {
		return result, ErrBrainProjectionDerivedNotDurable
	}
	if err := output.validate(); err != nil {
		return result, err
	}
	receipt, err := publisher.derived.PutBrainProjectionDerived(ctx, output)
	if err != nil {
		return result, err
	}
	if receipt.DerivedID != output.DerivedID || receipt.DerivedHighWater != output.DerivedHighWater || receipt.DerivedSHA256 != output.DerivedSHA256 ||
		receipt.SourceManifestSHA256 != output.SourceManifestSHA256 || receipt.RebuildFenceToken != output.RebuildFenceToken || receipt.DurableAt.IsZero() {
		return result, ErrBrainProjectionDerivedNotDurable
	}
	if publisher.failpoint != nil {
		if err := publisher.failpoint("after_derived_before_checkpoint"); err != nil {
			return result, err
		}
	}
	verified := verifiedBrainProjectionPublication{
		output: output, receipt: receipt,
		proof: &brainProjectionDurabilityProof{checkpointStore: publisher.checkpoints, verify: func(verifyCtx context.Context) error {
			return publisher.derived.VerifyBrainProjectionDerived(verifyCtx, output, receipt)
		}},
	}
	return publisher.checkpoints.publishVerified(ctx, verified)
}

type brainProjectionCheckpointQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadBrainProjectionCheckpoint(ctx context.Context, query brainProjectionCheckpointQuery, key BrainProjectionCheckpointKey, forUpdate bool) (BrainProjectionCheckpoint, bool, error) {
	statement := `SELECT source_high_water,source_manifest_sha256,derived_high_water,COALESCE(derived_id,''),derived_sha256,
		published_generation,published_rebuild_fence_token,rebuild_generation,rebuild_start_high_water,rebuild_end_high_water,
		rebuild_source_manifest_sha256,rebuild_fence_token,rebuild_started_at,published_at
		FROM brain_projection_checkpoints WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5`
	if forUpdate {
		statement += " FOR UPDATE"
	}
	checkpoint := BrainProjectionCheckpoint{Key: key}
	var sourceHW, derivedHW, publishedGeneration, rebuildStart, rebuildEnd pgtype.Int8
	var rebuildStarted, publishedAt pgtype.Timestamptz
	var sourceManifest, derivedDigest, publishedToken, rebuildManifest, rebuildToken []byte
	err := query.QueryRow(ctx, statement, key.TenantID, key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily).Scan(
		&sourceHW, &sourceManifest, &derivedHW, &checkpoint.DerivedID, &derivedDigest, &publishedGeneration, &publishedToken,
		&checkpoint.RebuildGeneration, &rebuildStart, &rebuildEnd, &rebuildManifest, &rebuildToken, &rebuildStarted, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, false, nil
	}
	if err != nil {
		return BrainProjectionCheckpoint{}, false, err
	}
	checkpoint.HasPublication = publishedAt.Valid
	if checkpoint.HasPublication {
		if !sourceHW.Valid || !derivedHW.Valid || !publishedGeneration.Valid || checkpoint.DerivedID == "" || len(sourceManifest) != sha256.Size || len(derivedDigest) != sha256.Size {
			return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
		}
		token, err := tokenFromBytes(publishedToken)
		if err != nil {
			return BrainProjectionCheckpoint{}, false, err
		}
		checkpoint.SourceHighWater, checkpoint.DerivedHighWater, checkpoint.PublishedGeneration = sourceHW.Int64, derivedHW.Int64, publishedGeneration.Int64
		copy(checkpoint.SourceManifestSHA256[:], sourceManifest)
		copy(checkpoint.DerivedSHA256[:], derivedDigest)
		checkpoint.PublishedRebuildFenceToken, checkpoint.PublishedAt = token, publishedAt.Time.UTC()
		if checkpoint.SourceManifestSHA256 == [sha256.Size]byte{} ||
			(checkpoint.PublishedGeneration == 0 && !token.isZero()) || (checkpoint.PublishedGeneration > 0 && token.isZero()) {
			return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
		}
		wantID := BrainProjectionDerivedID(key, checkpoint.PublishedGeneration, checkpoint.SourceHighWater, checkpoint.DerivedHighWater, checkpoint.DerivedSHA256, checkpoint.SourceManifestSHA256, token)
		if checkpoint.DerivedID != wantID {
			return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
		}
	} else if sourceHW.Valid || derivedHW.Valid || publishedGeneration.Valid || checkpoint.DerivedID != "" || len(sourceManifest)+len(derivedDigest)+len(publishedToken) != 0 {
		return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
	}
	checkpoint.RebuildActive = rebuildStarted.Valid
	if checkpoint.RebuildActive {
		if !rebuildStart.Valid || !rebuildEnd.Valid || len(rebuildManifest) != sha256.Size {
			return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
		}
		token, err := tokenFromBytes(rebuildToken)
		if err != nil || token.isZero() {
			return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
		}
		checkpoint.RebuildStartHighWater, checkpoint.RebuildEndHighWater = rebuildStart.Int64, rebuildEnd.Int64
		copy(checkpoint.RebuildSourceManifestSHA256[:], rebuildManifest)
		checkpoint.RebuildFenceToken, checkpoint.RebuildStartedAt = token, rebuildStarted.Time.UTC()
		if checkpoint.RebuildSourceManifestSHA256 == [sha256.Size]byte{} {
			return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
		}
	} else if rebuildStart.Valid || rebuildEnd.Valid || len(rebuildManifest)+len(rebuildToken) != 0 {
		return BrainProjectionCheckpoint{}, false, ErrBrainProjectionCheckpointCorrupt
	}
	return checkpoint, true, nil
}

func lockBrainProjectionCheckpoint(ctx context.Context, tx pgx.Tx, key BrainProjectionCheckpointKey) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key.advisoryLockID())
	return err
}

func lockBrainProjectionSource(ctx context.Context, tx pgx.Tx, key BrainProjectionCheckpointKey) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key.sourceAdvisoryLockID())
	return err
}
