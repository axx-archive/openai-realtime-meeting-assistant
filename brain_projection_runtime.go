package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	brainProjectionRuntimeModeEnv   = "BONFIRE_BRAIN_PROJECTION_MODE"
	brainProjectionRuntimeOff       = "off"
	brainProjectionRuntimeShadow    = "shadow"
	brainProjectionProjectorVersion = "company-brain/v2"
	brainProjectionManifestFormat   = 1
	brainProjectionEnvelopeFormat   = 1
	brainProjectionMaxFileBytes     = 32 << 20
)

// BrainProjectionSourceManifest is a body-free, deterministic description of
// the exact canonical facts visible to one projector key. Payloads, object
// state, revision bodies, and destruction evidence never enter this artifact.
// Their authoritative digests are sufficient to detect drift and replay.
type BrainProjectionSourceManifest struct {
	Format      int                             `json:"format"`
	Key         BrainProjectionCheckpointKey    `json:"key"`
	HighWater   int64                           `json:"highWater"`
	Events      []BrainProjectionManifestEvent  `json:"events"`
	Objects     []BrainProjectionManifestObject `json:"objects"`
	Purges      []BrainProjectionManifestPurge  `json:"purges"`
	ManifestSHA string                          `json:"manifestSha256,omitempty"`
}

type BrainProjectionManifestEvent struct {
	Sequence         int64  `json:"sequence"`
	EventID          string `json:"eventId"`
	AggregateID      string `json:"aggregateId"`
	AggregateVersion int64  `json:"aggregateVersion"`
	EventType        string `json:"eventType"`
	SchemaVersion    int    `json:"schemaVersion"`
	OccurredAt       string `json:"occurredAt"`
	RecordedAt       string `json:"recordedAt"`
	Classification   string `json:"classification"`
	ACLVersion       int64  `json:"aclVersion"`
	PayloadSHA256    string `json:"payloadSha256"`
}

type BrainProjectionManifestObject struct {
	ObjectID          string `json:"objectId"`
	StateRevision     int64  `json:"stateRevision"`
	ContentRevision   int64  `json:"contentRevision"`
	ContentSHA256     string `json:"contentSha256,omitempty"`
	Classification    string `json:"classification"`
	ACLVersion        int64  `json:"aclVersion"`
	LastEventSequence int64  `json:"lastEventSequence"`
	DeletedAt         string `json:"deletedAt,omitempty"`
	RetainUntil       string `json:"retainUntil,omitempty"`
}

type BrainProjectionManifestPurge struct {
	SourceFamily              string `json:"sourceFamily"`
	ObjectID                  string `json:"objectId"`
	RevisionID                string `json:"revisionId"`
	ContentSHA256             string `json:"contentSha256"`
	PolicyID                  string `json:"policyId"`
	PurgedAt                  string `json:"purgedAt"`
	RecordedAt                string `json:"recordedAt"`
	DestructionEvidenceSHA256 string `json:"destructionEvidenceSha256"`
}

type postgresBrainProjectionSourceResolver struct{}

func NewPostgresBrainProjectionSourceResolver() SourceHighWaterResolver {
	return &postgresBrainProjectionSourceResolver{}
}

func (resolver *postgresBrainProjectionSourceResolver) ResolveBrainProjectionSourceState(ctx context.Context, tx pgx.Tx, key BrainProjectionCheckpointKey) (BrainProjectionSourceState, error) {
	manifest, state, err := resolver.ResolveBrainProjectionSourceManifest(ctx, tx, key)
	_ = manifest
	return state, err
}

// ResolveBrainProjectionSourceManifest performs one SQL statement so events,
// current objects, and purge evidence share one PostgreSQL statement snapshot
// inside the checkpoint transaction/advisory-lock fence.
func (resolver *postgresBrainProjectionSourceResolver) ResolveBrainProjectionSourceManifest(ctx context.Context, tx pgx.Tx, key BrainProjectionCheckpointKey) (BrainProjectionSourceManifest, BrainProjectionSourceState, error) {
	manifest := BrainProjectionSourceManifest{Format: brainProjectionManifestFormat, Key: key, Events: []BrainProjectionManifestEvent{}, Objects: []BrainProjectionManifestObject{}, Purges: []BrainProjectionManifestPurge{}}
	if resolver == nil || tx == nil || key.Validate() != nil {
		return manifest, BrainProjectionSourceState{}, ErrBrainProjectionCheckpointInvalid
	}
	var highWater int64
	var rawEvents, rawObjects, rawPurges []byte
	err := tx.QueryRow(ctx, `WITH scoped_events AS MATERIALIZED (
		SELECT sequence,event_id::text,aggregate_id,aggregate_version,event_type,schema_version,
			occurred_at,recorded_at,classification,acl_version,encode(payload_sha256,'hex') AS payload_sha256
		FROM canonical_events
		WHERE tenant_id=$1 AND aggregate_type=$2 AND room_id=$3 AND meeting_id=$4
	), scoped_objects AS MATERIALIZED (
		SELECT object_type,object_id,state_revision,content_revision,encode(content_sha256,'hex') AS content_sha256,
			classification,acl_version,last_event_sequence,deleted_at,retain_until
		FROM objects
		WHERE tenant_id=$1 AND object_type=$2 AND room_id=$3 AND meeting_id=$4
	), scoped_purges AS MATERIALIZED (
		SELECT p.object_type,p.object_id,p.revision_id,encode(p.content_sha256,'hex') AS content_sha256,p.policy_id,
			p.purged_at,p.recorded_at,p.destruction_evidence
		FROM purge_ledger p
		JOIN scoped_objects o ON o.object_id=p.object_id AND o.object_type=p.object_type
		WHERE p.tenant_id=$1
	)
	SELECT COALESCE((SELECT max(sequence) FROM scoped_events),0),
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'sequence',sequence,'eventId',event_id,'aggregateId',aggregate_id,'aggregateVersion',aggregate_version,
			'eventType',event_type,'schemaVersion',schema_version,'occurredAt',occurred_at,'recordedAt',recorded_at,
			'classification',classification,'aclVersion',acl_version,'payloadSha256',payload_sha256
		) ORDER BY sequence) FROM scoped_events),'[]'::jsonb),
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'objectId',object_id,'stateRevision',state_revision,'contentRevision',content_revision,
			'contentSha256',COALESCE(content_sha256,''),'classification',classification,'aclVersion',acl_version,
			'lastEventSequence',last_event_sequence,'deletedAt',deleted_at,'retainUntil',retain_until
		) ORDER BY object_id) FROM scoped_objects),'[]'::jsonb),
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'sourceFamily',object_type,'objectId',object_id,'revisionId',revision_id,'contentSha256',content_sha256,'policyId',policy_id,
			'purgedAt',purged_at,'recordedAt',recorded_at,'destructionEvidence',destruction_evidence
		) ORDER BY object_type,object_id,revision_id) FROM scoped_purges),'[]'::jsonb)`,
		key.TenantID, key.SourceFamily, key.RoomID, key.SittingID).Scan(&highWater, &rawEvents, &rawObjects, &rawPurges)
	if err != nil {
		return manifest, BrainProjectionSourceState{}, fmt.Errorf("resolve canonical projection source: %w", err)
	}
	if err := decodeProjectionManifestRows(rawEvents, &manifest.Events); err != nil {
		return manifest, BrainProjectionSourceState{}, err
	}
	if err := decodeProjectionManifestRows(rawObjects, &manifest.Objects); err != nil {
		return manifest, BrainProjectionSourceState{}, err
	}
	var purgeRows []struct {
		SourceFamily        string          `json:"sourceFamily"`
		ObjectID            string          `json:"objectId"`
		RevisionID          string          `json:"revisionId"`
		ContentSHA256       string          `json:"contentSha256"`
		PolicyID            string          `json:"policyId"`
		PurgedAt            time.Time       `json:"purgedAt"`
		RecordedAt          time.Time       `json:"recordedAt"`
		DestructionEvidence json.RawMessage `json:"destructionEvidence"`
	}
	if err := decodeProjectionManifestRows(rawPurges, &purgeRows); err != nil {
		return manifest, BrainProjectionSourceState{}, err
	}
	manifest.HighWater = highWater
	for _, row := range purgeRows {
		evidence, err := canonicalJSON(json.RawMessage(row.DestructionEvidence))
		if err != nil {
			return manifest, BrainProjectionSourceState{}, fmt.Errorf("canonicalize purge evidence: %w", err)
		}
		evidenceDigest := sha256.Sum256(evidence)
		manifest.Purges = append(manifest.Purges, BrainProjectionManifestPurge{
			SourceFamily: row.SourceFamily, ObjectID: row.ObjectID, RevisionID: row.RevisionID, ContentSHA256: row.ContentSHA256, PolicyID: row.PolicyID,
			PurgedAt: projectionManifestTime(row.PurgedAt), RecordedAt: projectionManifestTime(row.RecordedAt),
			DestructionEvidenceSHA256: hex.EncodeToString(evidenceDigest[:]),
		})
	}
	// PostgreSQL's jsonb timestamptz rendering is decoded into strings above.
	// Normalize it once so session timezone and fractional precision cannot
	// influence the manifest identity.
	for index := range manifest.Events {
		manifest.Events[index].OccurredAt = normalizeProjectionManifestTime(manifest.Events[index].OccurredAt)
		manifest.Events[index].RecordedAt = normalizeProjectionManifestTime(manifest.Events[index].RecordedAt)
	}
	for index := range manifest.Objects {
		manifest.Objects[index].DeletedAt = normalizeProjectionManifestTime(manifest.Objects[index].DeletedAt)
		manifest.Objects[index].RetainUntil = normalizeProjectionManifestTime(manifest.Objects[index].RetainUntil)
	}
	digestible := manifest
	digestible.ManifestSHA = ""
	raw, err := canonicalJSON(digestible)
	if err != nil {
		return manifest, BrainProjectionSourceState{}, err
	}
	digest := sha256.Sum256(raw)
	manifest.ManifestSHA = hex.EncodeToString(digest[:])
	return manifest, BrainProjectionSourceState{HighWater: highWater, ManifestSHA256: digest}, nil
}

func decodeProjectionManifestRows(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode canonical projection manifest rows: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("canonical projection manifest rows contain trailing data")
	}
	return nil
}

func projectionManifestTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func normalizeProjectionManifestTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return projectionManifestTime(parsed)
}

type BrainProjectionReplayClaim struct {
	ClaimID        string `json:"claimId"`
	SourceObjectID string `json:"sourceObjectId"`
	SourceRevision int64  `json:"sourceRevision"`
	ContentSHA256  string `json:"contentSha256"`
}

type BrainProjectionSanitizedReplay struct {
	Format               int                          `json:"format"`
	SourceHighWater      int64                        `json:"sourceHighWater"`
	SourceManifestSHA256 string                       `json:"sourceManifestSha256"`
	Claims               []BrainProjectionReplayClaim `json:"claims"`
}

// BuildBrainProjectionSanitizedReplay is the production-shaped deterministic
// replay helper. It consumes only manifest metadata and never canonical bodies.
func BuildBrainProjectionSanitizedReplay(manifest BrainProjectionSourceManifest) (BrainProjectionSanitizedReplay, []byte, int64, error) {
	purged := make(map[string]bool, len(manifest.Purges))
	for _, entry := range manifest.Purges {
		// RevisionID is intentionally opaque across source families. The purge
		// ledger's object+content binding is the portable identity that prevents
		// a purged current revision from reappearing in rebuilt claims.
		purged[strings.Join([]string{entry.SourceFamily, entry.ObjectID, entry.ContentSHA256}, "\x00")] = true
	}
	replay := BrainProjectionSanitizedReplay{Format: 1, SourceHighWater: manifest.HighWater, SourceManifestSHA256: manifest.ManifestSHA, Claims: []BrainProjectionReplayClaim{}}
	for _, object := range manifest.Objects {
		purgeIdentity := strings.Join([]string{manifest.Key.SourceFamily, object.ObjectID, object.ContentSHA256}, "\x00")
		if object.DeletedAt != "" || object.ContentRevision < 1 || object.ContentSHA256 == "" || purged[purgeIdentity] {
			continue
		}
		identity := strings.Join([]string{manifest.Key.SourceFamily, object.ObjectID, fmt.Sprint(object.ContentRevision), object.ContentSHA256}, "\x00")
		replay.Claims = append(replay.Claims, BrainProjectionReplayClaim{
			ClaimID: "projection-claim-" + digestBrainString(identity)[:24], SourceObjectID: object.ObjectID,
			SourceRevision: object.ContentRevision, ContentSHA256: object.ContentSHA256,
		})
	}
	sort.Slice(replay.Claims, func(i, j int) bool { return replay.Claims[i].ClaimID < replay.Claims[j].ClaimID })
	raw, err := canonicalJSON(replay)
	if err != nil {
		return replay, nil, 0, err
	}
	// Purges may change the effective claim set without appending a canonical
	// event, so they participate in this monotonic derived watermark.
	derivedHighWater := manifest.HighWater + int64(len(manifest.Purges))
	return replay, raw, derivedHighWater, nil
}

type fileBrainProjectionDerivedEnvelope struct {
	Format               int                          `json:"format"`
	Key                  BrainProjectionCheckpointKey `json:"key"`
	SourceHighWater      int64                        `json:"sourceHighWater"`
	SourceManifestSHA256 string                       `json:"sourceManifestSha256"`
	DerivedHighWater     int64                        `json:"derivedHighWater"`
	RebuildGeneration    int64                        `json:"rebuildGeneration"`
	RebuildFenceToken    string                       `json:"rebuildFenceToken"`
	DerivedID            string                       `json:"derivedId"`
	DerivedSHA256        string                       `json:"derivedSha256"`
	Body                 []byte                       `json:"body"`
	DurableAt            time.Time                    `json:"durableAt"`
}

type FileBrainProjectionDerivedSink struct {
	mu   sync.Mutex
	root string
	now  func() time.Time
}

func NewFileBrainProjectionDerivedSink(root string) (*FileBrainProjectionDerivedSink, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrBrainProjectionDerivedNotDurable
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create projection derived store: %w", err)
	}
	return &FileBrainProjectionDerivedSink{root: filepath.Clean(root), now: func() time.Time { return time.Now().UTC() }}, nil
}

func (sink *FileBrainProjectionDerivedSink) outputPath(output BrainProjectionDerivedOutput) (string, error) {
	if sink == nil || sink.root == "" || output.validate() != nil || !strings.HasPrefix(output.DerivedID, "brain-projection-") {
		return "", ErrBrainProjectionCheckpointInvalid
	}
	raw, err := canonicalJSON(output.Key)
	if err != nil {
		return "", err
	}
	scopeDigest := sha256.Sum256(raw)
	scope := hex.EncodeToString(scopeDigest[:])
	return filepath.Join(sink.root, scope, output.DerivedID+".json"), nil
}

func projectionEnvelope(output BrainProjectionDerivedOutput, durableAt time.Time) fileBrainProjectionDerivedEnvelope {
	return fileBrainProjectionDerivedEnvelope{
		Format: brainProjectionEnvelopeFormat, Key: output.Key, SourceHighWater: output.SourceHighWater,
		SourceManifestSHA256: hex.EncodeToString(output.SourceManifestSHA256[:]), DerivedHighWater: output.DerivedHighWater,
		RebuildGeneration: output.RebuildGeneration, RebuildFenceToken: hex.EncodeToString(output.RebuildFenceToken.value[:]),
		DerivedID: output.DerivedID, DerivedSHA256: hex.EncodeToString(output.DerivedSHA256[:]), Body: append([]byte(nil), output.Body...), DurableAt: durableAt.UTC(),
	}
}

func receiptFromProjectionEnvelope(envelope fileBrainProjectionDerivedEnvelope) (BrainProjectionDurableReceipt, error) {
	derived, err := hex.DecodeString(envelope.DerivedSHA256)
	if err != nil || len(derived) != sha256.Size {
		return BrainProjectionDurableReceipt{}, ErrBrainProjectionDerivedNotDurable
	}
	manifest, err := hex.DecodeString(envelope.SourceManifestSHA256)
	if err != nil || len(manifest) != sha256.Size {
		return BrainProjectionDurableReceipt{}, ErrBrainProjectionDerivedNotDurable
	}
	tokenRaw, err := hex.DecodeString(envelope.RebuildFenceToken)
	if err != nil || len(tokenRaw) != sha256.Size {
		return BrainProjectionDurableReceipt{}, ErrBrainProjectionDerivedNotDurable
	}
	token, err := tokenFromBytes(tokenRaw)
	if err != nil {
		return BrainProjectionDurableReceipt{}, err
	}
	receipt := BrainProjectionDurableReceipt{DerivedID: envelope.DerivedID, DerivedHighWater: envelope.DerivedHighWater, RebuildFenceToken: token, DurableAt: envelope.DurableAt.UTC()}
	copy(receipt.DerivedSHA256[:], derived)
	copy(receipt.SourceManifestSHA256[:], manifest)
	return receipt, nil
}

func (sink *FileBrainProjectionDerivedSink) readEnvelope(path string) (fileBrainProjectionDerivedEnvelope, error) {
	var envelope fileBrainProjectionDerivedEnvelope
	file, err := os.Open(path)
	if err != nil {
		return envelope, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return envelope, err
	}
	// LimitReader alone is insufficient: a valid JSON envelope followed by
	// enough whitespace to cross the limit appears to end cleanly at the
	// artificial EOF. Bind acceptance to the actual on-disk file size, then
	// retain a max+1 read fence in case an external writer grows the file after
	// stat (the sink mutex coordinates only cooperating in-process writers).
	if info.Size() <= 0 || info.Size() > brainProjectionMaxFileBytes {
		return envelope, ErrBrainProjectionDerivedNotDurable
	}
	raw, err := io.ReadAll(io.LimitReader(file, brainProjectionMaxFileBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > brainProjectionMaxFileBytes {
		return envelope, ErrBrainProjectionDerivedNotDurable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return envelope, ErrBrainProjectionDerivedNotDurable
	}
	if envelope.Format != brainProjectionEnvelopeFormat || envelope.DurableAt.IsZero() {
		return envelope, ErrBrainProjectionDerivedNotDurable
	}
	return envelope, nil
}

func (sink *FileBrainProjectionDerivedSink) PutBrainProjectionDerived(_ context.Context, output BrainProjectionDerivedOutput) (BrainProjectionDurableReceipt, error) {
	path, err := sink.outputPath(output)
	if err != nil {
		return BrainProjectionDurableReceipt{}, err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if envelope, readErr := sink.readEnvelope(path); readErr == nil {
		receipt, receiptErr := receiptFromProjectionEnvelope(envelope)
		if receiptErr != nil || verifyProjectionEnvelope(output, envelope, receipt) != nil {
			return BrainProjectionDurableReceipt{}, ErrBrainProjectionConflict
		}
		return receipt, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return BrainProjectionDurableReceipt{}, fmt.Errorf("%w: read existing derived output: %v", ErrBrainProjectionDerivedNotDurable, readErr)
	}
	envelope := projectionEnvelope(output, sink.now().UTC())
	raw, err := canonicalJSON(envelope)
	if err != nil {
		return BrainProjectionDurableReceipt{}, err
	}
	if len(raw) > brainProjectionMaxFileBytes {
		return BrainProjectionDurableReceipt{}, ErrBrainProjectionDerivedNotDurable
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return BrainProjectionDurableReceipt{}, err
	}
	if err := writeFileAtomicallyDurable(path, raw, 0o600); err != nil {
		// A parent fsync failure is ambiguous; reread and verify the visible file
		// before deciding whether the receipt is safe to return.
		persisted, readErr := sink.readEnvelope(path)
		if readErr != nil {
			return BrainProjectionDurableReceipt{}, err
		}
		receipt, receiptErr := receiptFromProjectionEnvelope(persisted)
		if receiptErr != nil || verifyProjectionEnvelope(output, persisted, receipt) != nil {
			return BrainProjectionDurableReceipt{}, err
		}
		return receipt, nil
	}
	persisted, err := sink.readEnvelope(path)
	if err != nil {
		return BrainProjectionDurableReceipt{}, err
	}
	receipt, err := receiptFromProjectionEnvelope(persisted)
	if err != nil || verifyProjectionEnvelope(output, persisted, receipt) != nil {
		return BrainProjectionDurableReceipt{}, ErrBrainProjectionDerivedNotDurable
	}
	return receipt, nil
}

func (sink *FileBrainProjectionDerivedSink) VerifyBrainProjectionDerived(_ context.Context, output BrainProjectionDerivedOutput, receipt BrainProjectionDurableReceipt) error {
	path, err := sink.outputPath(output)
	if err != nil {
		return err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	envelope, err := sink.readEnvelope(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBrainProjectionDerivedNotDurable, err)
	}
	return verifyProjectionEnvelope(output, envelope, receipt)
}

func verifyProjectionEnvelope(output BrainProjectionDerivedOutput, envelope fileBrainProjectionDerivedEnvelope, receipt BrainProjectionDurableReceipt) error {
	storedReceipt, err := receiptFromProjectionEnvelope(envelope)
	if err != nil || output.validate() != nil || !reflect.DeepEqual(envelope, projectionEnvelope(output, envelope.DurableAt)) || storedReceipt != receipt {
		return ErrBrainProjectionDerivedNotDurable
	}
	return nil
}

type BrainProjectionRuntimeStatus struct {
	Mode              string `json:"mode"`
	Enabled           bool   `json:"enabled"`
	Ready             bool   `json:"ready"`
	Database          bool   `json:"database"`
	DurableSink       bool   `json:"durableSink"`
	WorkerRunning     bool   `json:"workerRunning"`
	QueueKnown        bool   `json:"queueKnown"`
	CaughtUp          bool   `json:"caughtUp"`
	PendingScopes     int    `json:"pendingScopes"`
	FailedScopes      int    `json:"failedScopes"`
	BackoffScopes     int    `json:"backoffScopes"`
	OldestPendingSecs int64  `json:"oldestPendingSeconds"`
	PublishedScopes   uint64 `json:"publishedScopes"`
	LastPublishedAt   string `json:"lastPublishedAt,omitempty"`
	AutomaticBaseline bool   `json:"automaticBaseline"`
	Error             string `json:"error,omitempty"`
}

// BrainProjectionHistoricalBackfillAuthorization is an exact, short-lived
// grant produced by the authenticated operator plane. The runtime API is not
// exposed through HTTP and refuses a grant whose scope, generation, or source
// interval differs from the requested rebuild.
type BrainProjectionHistoricalBackfillAuthorization struct {
	RequestID            string
	Key                  BrainProjectionCheckpointKey
	ExpectedGeneration   int64
	StartSourceHighWater int64
	EndSourceHighWater   int64
	AuthorizedBy         string
	ApprovalReference    string
	ExpiresAt            time.Time
}

type BrainProjectionHistoricalBackfillRequest struct {
	Rebuild       BrainProjectionRebuildRequest
	Authorization BrainProjectionHistoricalBackfillAuthorization
}

type BrainProjectionHistoricalBackfillResult struct {
	Fence     BrainProjectionRebuildFence
	Existing  bool
	Scheduled bool
	Completed bool
}

var (
	ErrBrainProjectionBackfillUnauthorized = errors.New("brain projection historical backfill is not authorized")
	ErrBrainProjectionHistorySkip          = errors.New("brain projection historical backfill would skip source history")
	ErrBrainProjectionLeaseLost            = errors.New("brain projection work lease lost")
)

func (request BrainProjectionHistoricalBackfillRequest) validate(_ time.Time) error {
	if err := request.Rebuild.Validate(); err != nil {
		return err
	}
	authorization := request.Authorization
	if authorization.Key != request.Rebuild.Key || authorization.ExpectedGeneration != request.Rebuild.ExpectedGeneration ||
		authorization.StartSourceHighWater != request.Rebuild.StartSourceHighWater || authorization.EndSourceHighWater != request.Rebuild.EndSourceHighWater {
		return ErrBrainProjectionBackfillUnauthorized
	}
	if request.Rebuild.EndSourceHighWater <= request.Rebuild.StartSourceHighWater ||
		!validProjectionOperatorField(authorization.RequestID, 128) || !validProjectionOperatorField(authorization.AuthorizedBy, 256) ||
		!validProjectionOperatorField(authorization.ApprovalReference, 256) || authorization.ExpiresAt.IsZero() {
		return ErrBrainProjectionBackfillUnauthorized
	}
	return nil
}

func validProjectionOperatorField(value string, max int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= max
}

type brainProjectionBackfillAudit struct {
	RequestID            string
	Key                  BrainProjectionCheckpointKey
	ExpectedGeneration   int64
	StartSourceHighWater int64
	EndSourceHighWater   int64
	AuthorizedBy         string
	ApprovalReference    string
	AuthorizationExpiry  time.Time
	FenceGeneration      int64
	FenceToken           BrainProjectionRebuildFenceToken
	RebuildStartedAt     time.Time
	HasFence             bool
	SourceManifest       BrainProjectionSourceManifest
}

type productionBrainProjectionRuntime struct {
	mu            sync.Mutex
	status        BrainProjectionRuntimeStatus
	canonicalPool *pgxpool.Pool
	resolver      *postgresBrainProjectionSourceResolver
	sink          *FileBrainProjectionDerivedSink
	checkpoints   *PostgresBrainProjectionCheckpointStore
	publisher     *brainProjectionCheckpointPublisher
	ctx           context.Context
	cancel        context.CancelFunc
	wake          chan struct{}
	wg            sync.WaitGroup
}

var brainProjectionRuntimeState struct {
	sync.RWMutex
	runtime *productionBrainProjectionRuntime
}

func configureProductionBrainProjectionRuntime(canonical *CanonicalRuntime) BrainProjectionRuntimeStatus {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(brainProjectionRuntimeModeEnv)))
	if mode == "" {
		mode = brainProjectionRuntimeOff
	}
	runtime := &productionBrainProjectionRuntime{status: BrainProjectionRuntimeStatus{Mode: mode, AutomaticBaseline: false}}
	if mode == brainProjectionRuntimeOff {
		runtime.status.Ready, runtime.status.CaughtUp = true, true
	} else if mode != brainProjectionRuntimeShadow {
		runtime.status.Error = "invalid brain projection mode; want off or shadow"
	} else if canonical == nil || canonical.postgres == nil || strings.TrimSpace(canonical.dataDir) == "" {
		runtime.status.Enabled = true
		runtime.status.Error = "canonical PostgreSQL and data directory are required"
	} else {
		runtime.status.Enabled, runtime.status.Database = true, true
		runtime.resolver = &postgresBrainProjectionSourceResolver{}
		sink, err := NewFileBrainProjectionDerivedSink(filepath.Join(canonical.dataDir, "brain-projections", "derived"))
		if err != nil {
			runtime.status.Error = err.Error()
		} else {
			runtime.sink = sink
			runtime.status.DurableSink = true
			runtime.canonicalPool = canonical.postgres.pool
			runtime.checkpoints = NewBrainProjectionCheckpointStore(canonical.postgres, runtime.resolver)
			runtime.publisher = newBrainProjectionCheckpointPublisher(runtime.checkpoints, runtime.sink)
			runtime.ctx, runtime.cancel = context.WithCancel(context.Background())
			runtime.wake = make(chan struct{}, 1)
			runtime.status.WorkerRunning = true
			if err := runtime.recoverExplicitRebuildWork(runtime.ctx); err != nil {
				runtime.setQueueUnavailable(err)
			} else {
				runtime.refreshDurableStatus(runtime.ctx)
			}
		}
	}
	brainProjectionRuntimeState.Lock()
	prior := brainProjectionRuntimeState.runtime
	brainProjectionRuntimeState.runtime = runtime
	brainProjectionRuntimeState.Unlock()
	if prior != nil {
		prior.stop()
	}
	if runtime.status.WorkerRunning {
		runtime.wg.Add(1)
		go runtime.run()
		runtime.signal()
	}
	return runtime.snapshot()
}

func brainProjectionRuntimeStatus() BrainProjectionRuntimeStatus {
	brainProjectionRuntimeState.RLock()
	runtime := brainProjectionRuntimeState.runtime
	brainProjectionRuntimeState.RUnlock()
	if runtime == nil {
		return BrainProjectionRuntimeStatus{Mode: "uninitialized", AutomaticBaseline: false, Error: "runtime is not configured"}
	}
	if runtime.canonicalPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		runtime.refreshDurableStatus(ctx)
		cancel()
	}
	return runtime.snapshot()
}

func currentProductionBrainProjectionRuntime() *productionBrainProjectionRuntime {
	brainProjectionRuntimeState.RLock()
	defer brainProjectionRuntimeState.RUnlock()
	return brainProjectionRuntimeState.runtime
}

func stopProductionBrainProjectionRuntime() {
	brainProjectionRuntimeState.Lock()
	runtime := brainProjectionRuntimeState.runtime
	brainProjectionRuntimeState.runtime = nil
	brainProjectionRuntimeState.Unlock()
	if runtime != nil {
		runtime.stop()
	}
}

func brainProjectionKeyForCanonicalEvent(event CanonicalEvent) BrainProjectionCheckpointKey {
	return BrainProjectionCheckpointKey{
		TenantID: event.TenantID, ProjectorVersion: brainProjectionProjectorVersion,
		RoomID: NormalizeCanonicalRoomID(event.RoomID), SittingID: strings.TrimSpace(event.MeetingID),
		SourceFamily: strings.TrimSpace(event.AggregateType),
	}
}

func notifyProductionBrainProjectionCanonicalEvent(store *PostgresCanonicalStore, event CanonicalEvent) {
	if store == nil {
		return
	}
	notifyProductionBrainProjectionScope(store.pool, brainProjectionKeyForCanonicalEvent(event))
}

func notifyProductionBrainProjectionScope(pool *pgxpool.Pool, key BrainProjectionCheckpointKey) {
	runtime := currentProductionBrainProjectionRuntime()
	if runtime == nil || pool == nil || runtime.canonicalPool != pool || key.Validate() != nil {
		return
	}
	runtime.signal()
}

func productionBrainProjectionAcceptsPool(pool *pgxpool.Pool) bool {
	runtime := currentProductionBrainProjectionRuntime()
	if runtime == nil || pool == nil || runtime.canonicalPool != pool {
		return false
	}
	status := runtime.snapshot()
	return status.Enabled && status.WorkerRunning
}

func registerBrainProjectionScopeDurably(ctx context.Context, tx pgx.Tx, pool *pgxpool.Pool, key BrainProjectionCheckpointKey) error {
	if key.Validate() != nil || !productionBrainProjectionAcceptsPool(pool) {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO brain_projection_work (
		tenant_id,projector_version,room_id,sitting_id,source_family,requested_at,available_at,last_error
	) VALUES ($1,$2,$3,$4,$5,now(),now(),'')
	ON CONFLICT (tenant_id,projector_version,room_id,sitting_id,source_family) DO UPDATE SET
		requested_at=EXCLUDED.requested_at,available_at=EXCLUDED.available_at,
		request_generation=nextval('brain_projection_work_request_generation_seq'),lease_token=NULL,lease_expires_at=NULL`,
		key.TenantID, key.ProjectorVersion, key.RoomID, key.SittingID, key.SourceFamily)
	return err
}

func (runtime *productionBrainProjectionRuntime) snapshot() BrainProjectionRuntimeStatus {
	if runtime == nil {
		return BrainProjectionRuntimeStatus{Mode: "uninitialized", Error: "runtime is not configured"}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	status := runtime.status
	return status
}

func (runtime *productionBrainProjectionRuntime) stop() {
	if runtime == nil || runtime.cancel == nil {
		return
	}
	runtime.cancel()
	runtime.wg.Wait()
	runtime.mu.Lock()
	runtime.status.WorkerRunning = false
	runtime.status.Ready = false
	runtime.mu.Unlock()
}

func (runtime *productionBrainProjectionRuntime) signal() {
	if runtime == nil || runtime.wake == nil {
		return
	}
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
}

func (runtime *productionBrainProjectionRuntime) run() {
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
		if err := runtime.recoverExplicitRebuildWork(runtime.ctx); err != nil {
			runtime.setQueueUnavailable(err)
			continue
		}
		for {
			work, ok := runtime.takePending(runtime.ctx)
			if !ok {
				runtime.refreshDurableStatus(runtime.ctx)
				break
			}
			if err := runtime.projectScope(runtime.ctx, work.key); err != nil {
				if recordErr := runtime.recordDurableFailure(runtime.ctx, work, err); recordErr != nil {
					if errors.Is(recordErr, ErrBrainProjectionLeaseLost) {
						runtime.refreshDurableStatus(runtime.ctx)
						continue
					}
					runtime.setQueueUnavailable(fmt.Errorf("record projection failure: %w", recordErr))
					continue
				}
				if errors.Is(err, ErrBrainProjectionSourceMoved) && runtime.ctx.Err() == nil {
					if retryErr := runtime.retryDurableScopeNow(runtime.ctx, work); retryErr != nil {
						runtime.setQueueUnavailable(fmt.Errorf("retry moved projection source: %w", retryErr))
						continue
					}
					runtime.signal()
				}
				runtime.refreshDurableStatus(runtime.ctx)
				continue
			}
			if err := runtime.completeDurableScope(runtime.ctx, work); err != nil {
				if errors.Is(err, ErrBrainProjectionLeaseLost) {
					runtime.refreshDurableStatus(runtime.ctx)
					continue
				}
				runtime.setQueueUnavailable(fmt.Errorf("acknowledge projection work: %w", err))
				continue
			}
			runtime.refreshDurableStatus(runtime.ctx)
		}
	}
}

type brainProjectionWorkItem struct {
	key        BrainProjectionCheckpointKey
	generation int64
	leaseToken [sha256.Size]byte
}

func (runtime *productionBrainProjectionRuntime) takePending(ctx context.Context) (brainProjectionWorkItem, bool) {
	var work brainProjectionWorkItem
	if _, err := rand.Read(work.leaseToken[:]); err != nil {
		runtime.setQueueUnavailable(fmt.Errorf("mint projection work lease: %w", err))
		return brainProjectionWorkItem{}, false
	}
	if work.leaseToken == [sha256.Size]byte{} {
		runtime.setQueueUnavailable(errors.New("mint projection work lease: zero token"))
		return brainProjectionWorkItem{}, false
	}
	err := runtime.canonicalPool.QueryRow(ctx, `WITH candidate AS (
		SELECT tenant_id,projector_version,room_id,sitting_id,source_family,request_generation
		FROM brain_projection_work WHERE available_at <= now() AND (lease_expires_at IS NULL OR lease_expires_at <= now())
		ORDER BY available_at,requested_at FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE brain_projection_work work SET attempts=work.attempts+1,available_at=now()+interval '30 seconds',
		lease_token=$1,lease_expires_at=now()+interval '30 seconds'
	FROM candidate WHERE work.tenant_id=candidate.tenant_id AND work.projector_version=candidate.projector_version
		AND work.room_id=candidate.room_id AND work.sitting_id=candidate.sitting_id AND work.source_family=candidate.source_family
	RETURNING work.tenant_id,work.projector_version,work.room_id,work.sitting_id,work.source_family,work.request_generation`, work.leaseToken[:]).Scan(
		&work.key.TenantID, &work.key.ProjectorVersion, &work.key.RoomID, &work.key.SittingID, &work.key.SourceFamily, &work.generation)
	if errors.Is(err, pgx.ErrNoRows) || ctx.Err() != nil {
		return brainProjectionWorkItem{}, false
	}
	if err != nil {
		runtime.setQueueUnavailable(err)
		return brainProjectionWorkItem{}, false
	}
	return work, true
}

func (runtime *productionBrainProjectionRuntime) completeDurableScope(ctx context.Context, work brainProjectionWorkItem) error {
	tag, err := runtime.canonicalPool.Exec(ctx, `DELETE FROM brain_projection_work
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5
			AND request_generation=$6 AND lease_token=$7`, work.key.TenantID, work.key.ProjectorVersion, work.key.RoomID,
		work.key.SittingID, work.key.SourceFamily, work.generation, work.leaseToken[:])
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrBrainProjectionLeaseLost
	}
	return nil
}

func (runtime *productionBrainProjectionRuntime) recordDurableFailure(ctx context.Context, work brainProjectionWorkItem, cause error) error {
	message := "projection failed"
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	tag, err := runtime.canonicalPool.Exec(ctx, `UPDATE brain_projection_work SET last_error=$8,
		failure_since=COALESCE(failure_since,now()),available_at=now()+interval '30 seconds',lease_token=NULL,lease_expires_at=NULL
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5
			AND request_generation=$6 AND lease_token=$7`, work.key.TenantID, work.key.ProjectorVersion, work.key.RoomID,
		work.key.SittingID, work.key.SourceFamily, work.generation, work.leaseToken[:], message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrBrainProjectionLeaseLost
	}
	return nil
}

func (runtime *productionBrainProjectionRuntime) retryDurableScopeNow(ctx context.Context, work brainProjectionWorkItem) error {
	_, err := runtime.canonicalPool.Exec(ctx, `UPDATE brain_projection_work SET available_at=now()
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5 AND request_generation=$6`,
		work.key.TenantID, work.key.ProjectorVersion, work.key.RoomID, work.key.SittingID, work.key.SourceFamily, work.generation)
	return err
}

func (runtime *productionBrainProjectionRuntime) setQueueUnavailable(err error) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.status.QueueKnown = false
	runtime.status.CaughtUp = false
	runtime.status.Ready = false
	if err != nil {
		runtime.status.Error = err.Error()
	}
	runtime.mu.Unlock()
}

func (runtime *productionBrainProjectionRuntime) refreshDurableStatus(ctx context.Context) {
	if runtime == nil || runtime.canonicalPool == nil {
		return
	}
	var pending, failed, backoff int
	var oldestSeconds int64
	var oldestError string
	err := runtime.canonicalPool.QueryRow(ctx, `SELECT count(*)::int,
		count(*) FILTER (WHERE last_error <> '')::int,
		count(*) FILTER (WHERE last_error <> '' AND available_at > now())::int,
		COALESCE(GREATEST(0,extract(epoch FROM (now()-min(first_requested_at)))::bigint),0),
		COALESCE((SELECT last_error FROM brain_projection_work WHERE last_error <> '' ORDER BY failure_since,first_requested_at LIMIT 1),'')
		FROM brain_projection_work`).Scan(&pending, &failed, &backoff, &oldestSeconds, &oldestError)
	if err != nil {
		runtime.setQueueUnavailable(fmt.Errorf("read durable projection queue: %w", err))
		return
	}
	runtime.mu.Lock()
	runtime.status.QueueKnown = true
	runtime.status.PendingScopes = pending
	runtime.status.FailedScopes = failed
	runtime.status.BackoffScopes = backoff
	runtime.status.OldestPendingSecs = oldestSeconds
	runtime.status.CaughtUp = pending == 0
	runtime.status.Ready = runtime.status.Enabled && runtime.status.WorkerRunning && runtime.status.CaughtUp
	runtime.status.Error = oldestError
	runtime.mu.Unlock()
}

// recoverExplicitRebuildWork recovers only scopes with an already durable,
// operator-created rebuild fence. It deliberately never scans canonical
// history for uncheckpointed scopes, preserving the no-auto-baseline rule.
func (runtime *productionBrainProjectionRuntime) recoverExplicitRebuildWork(ctx context.Context) error {
	if runtime == nil || runtime.canonicalPool == nil {
		return ErrBrainProjectionCheckpointUnavailable
	}
	_, err := runtime.canonicalPool.Exec(ctx, `INSERT INTO brain_projection_work (
		tenant_id,projector_version,room_id,sitting_id,source_family,requested_at,available_at,last_error
	) SELECT c.tenant_id,c.projector_version,c.room_id,c.sitting_id,c.source_family,now(),now(),''
	FROM brain_projection_checkpoints c
	JOIN brain_projection_backfill_requests b ON b.tenant_id=c.tenant_id AND b.projector_version=c.projector_version
		AND b.room_id=c.room_id AND b.sitting_id=c.sitting_id AND b.source_family=c.source_family
		AND b.fence_generation=c.rebuild_generation AND b.fence_token=c.rebuild_fence_token
		AND b.start_source_high_water=c.rebuild_start_high_water AND b.end_source_high_water=c.rebuild_end_high_water
		AND b.rebuild_started_at=c.rebuild_started_at AND b.source_manifest IS NOT NULL
	WHERE c.rebuild_started_at IS NOT NULL
	ON CONFLICT (tenant_id,projector_version,room_id,sitting_id,source_family) DO NOTHING`)
	return err
}

// ScheduleHistoricalBackfill is the bounded operator/runtime entry point for
// pre-enable history. Authority is exact (one key and one source interval),
// durably audited, and idempotent by request ID. It never discovers or
// baselines unrelated canonical scopes.
func (runtime *productionBrainProjectionRuntime) ScheduleHistoricalBackfill(ctx context.Context, request BrainProjectionHistoricalBackfillRequest) (BrainProjectionHistoricalBackfillResult, error) {
	result := BrainProjectionHistoricalBackfillResult{}
	now := time.Now().UTC()
	if runtime == nil || runtime.canonicalPool == nil || runtime.checkpoints == nil || !runtime.snapshot().Enabled {
		return result, ErrBrainProjectionCheckpointUnavailable
	}
	if err := request.validate(now); err != nil {
		return result, err
	}
	if runtime.resolver == nil {
		return result, ErrBrainProjectionCheckpointUnavailable
	}
	audit, existingAuthorization, err := runtime.loadBackfillAudit(ctx, request.Authorization.RequestID)
	if err != nil {
		return result, err
	}
	if existingAuthorization && !backfillAuditMatchesAuthorization(audit, request.Authorization) {
		return result, ErrBrainProjectionBackfillUnauthorized
	}
	if (!existingAuthorization || !audit.HasFence) && (!request.Authorization.ExpiresAt.After(now) || request.Authorization.ExpiresAt.After(now.Add(24*time.Hour))) {
		return result, ErrBrainProjectionBackfillUnauthorized
	}
	status, err := runtime.checkpoints.Status(ctx, request.Rebuild.Key)
	if err != nil {
		return result, err
	}
	if audit.HasFence {
		checkpoint := status.Checkpoint
		if checkpoint.RebuildActive && checkpoint.RebuildGeneration == audit.FenceGeneration && checkpoint.RebuildFenceToken == audit.FenceToken &&
			checkpoint.RebuildStartHighWater == audit.StartSourceHighWater && checkpoint.RebuildEndHighWater == audit.EndSourceHighWater {
			result.Fence = BrainProjectionRebuildFence{Key: audit.Key, Generation: audit.FenceGeneration, StartSourceHighWater: audit.StartSourceHighWater,
				EndSourceHighWater: audit.EndSourceHighWater, SourceManifestSHA256: checkpoint.RebuildSourceManifestSHA256,
				Token: audit.FenceToken, StartedAt: audit.RebuildStartedAt, Existing: true}
			if err := runtime.scheduleExplicitRebuildWork(ctx, audit.Key); err != nil {
				return result, err
			}
			result.Existing, result.Scheduled = true, true
			runtime.refreshDurableStatus(ctx)
			runtime.signal()
			return result, nil
		}
		if checkpoint.HasPublication && !checkpoint.RebuildActive && checkpoint.PublishedGeneration == audit.FenceGeneration &&
			checkpoint.PublishedRebuildFenceToken == audit.FenceToken && checkpoint.SourceHighWater >= audit.EndSourceHighWater {
			result.Fence = BrainProjectionRebuildFence{Key: audit.Key, Generation: audit.FenceGeneration, StartSourceHighWater: audit.StartSourceHighWater,
				EndSourceHighWater: audit.EndSourceHighWater, SourceManifestSHA256: checkpoint.SourceManifestSHA256,
				Token: audit.FenceToken, StartedAt: audit.RebuildStartedAt, Existing: true}
			result.Existing, result.Completed = true, true
			return result, nil
		}
		return result, ErrBrainProjectionFenceLost
	}
	checkpoint := status.Checkpoint
	if checkpoint.RebuildActive {
		// A checkpoint fence without the exact durable audit must not be adopted
		// after the fact. Legitimate concurrent/idempotent callers observe both
		// records together because they commit in one transaction below.
		return result, ErrBrainProjectionFenceLost
	}
	expectedStart := int64(0)
	if checkpoint.HasPublication {
		expectedStart = checkpoint.SourceHighWater
	}
	if request.Rebuild.StartSourceHighWater != expectedStart {
		return result, ErrBrainProjectionHistorySkip
	}
	fence, err := runtime.checkpoints.beginRebuild(ctx, request.Rebuild, func(ctx context.Context, tx pgx.Tx, fence BrainProjectionRebuildFence) error {
		manifest, source, resolveErr := runtime.resolver.ResolveBrainProjectionSourceManifest(ctx, tx, request.Rebuild.Key)
		if resolveErr != nil {
			return resolveErr
		}
		if source.HighWater != fence.EndSourceHighWater || source.ManifestSHA256 != fence.SourceManifestSHA256 {
			return ErrBrainProjectionSourceMoved
		}
		return bindBackfillAuthorizationAndFenceTx(ctx, tx, request.Authorization, fence, manifest)
	})
	if err != nil {
		return result, err
	}
	if err := runtime.scheduleExplicitRebuildWork(ctx, request.Rebuild.Key); err != nil {
		return result, err
	}
	result.Fence, result.Existing, result.Scheduled = fence, existingAuthorization || fence.Existing, true
	runtime.refreshDurableStatus(ctx)
	runtime.signal()
	return result, nil
}

// brainProjectionHistoricalBackfillHandler is the sole production operator
// door for historical projection. It derives the authorizing actor from the
// live member session, requires the existing admin gate, and binds one exact
// scope/range/approval to the durable audit before any work is recoverable.
func brainProjectionHistoricalBackfillHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if !isArtifactApprovalAdmin(user) {
		writeAuthError(w, http.StatusForbidden, "projection backfill is admin-only")
		return
	}
	payload := struct {
		RequestID            string    `json:"requestId"`
		TenantID             string    `json:"tenantId"`
		ProjectorVersion     string    `json:"projectorVersion"`
		RoomID               string    `json:"roomId"`
		SittingID            string    `json:"sittingId"`
		SourceFamily         string    `json:"sourceFamily"`
		ExpectedGeneration   int64     `json:"expectedGeneration"`
		StartSourceHighWater int64     `json:"startSourceHighWater"`
		EndSourceHighWater   int64     `json:"endSourceHighWater"`
		ApprovalReference    string    `json:"approvalReference"`
		AuthorizationExpires time.Time `json:"authorizationExpiresAt"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read exact projection backfill request")
		return
	}
	if payload.ProjectorVersion != brainProjectionProjectorVersion {
		writeAuthError(w, http.StatusBadRequest, "unsupported projector version")
		return
	}
	key := BrainProjectionCheckpointKey{TenantID: payload.TenantID, ProjectorVersion: payload.ProjectorVersion,
		RoomID: payload.RoomID, SittingID: payload.SittingID, SourceFamily: payload.SourceFamily}
	authorization := BrainProjectionHistoricalBackfillAuthorization{RequestID: payload.RequestID, Key: key,
		ExpectedGeneration: payload.ExpectedGeneration, StartSourceHighWater: payload.StartSourceHighWater,
		EndSourceHighWater: payload.EndSourceHighWater, AuthorizedBy: normalizeAccountEmail(user.Email),
		ApprovalReference: payload.ApprovalReference, ExpiresAt: payload.AuthorizationExpires.UTC()}
	request := BrainProjectionHistoricalBackfillRequest{Rebuild: BrainProjectionRebuildRequest{Key: key,
		ExpectedGeneration: payload.ExpectedGeneration, StartSourceHighWater: payload.StartSourceHighWater,
		EndSourceHighWater: payload.EndSourceHighWater}, Authorization: authorization}
	runtime := currentProductionBrainProjectionRuntime()
	if runtime == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "projection runtime is unavailable")
		return
	}
	result, err := runtime.ScheduleHistoricalBackfill(r.Context(), request)
	if err != nil {
		status := http.StatusConflict
		switch {
		case errors.Is(err, ErrBrainProjectionBackfillUnauthorized):
			status = http.StatusForbidden
		case errors.Is(err, ErrBrainProjectionCheckpointInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, ErrBrainProjectionCheckpointUnavailable):
			status = http.StatusServiceUnavailable
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusAccepted, map[string]any{"ok": true, "requestId": payload.RequestID,
		"generation": result.Fence.Generation, "existing": result.Existing, "scheduled": result.Scheduled, "completed": result.Completed})
}

func (runtime *productionBrainProjectionRuntime) loadBackfillAudit(ctx context.Context, requestID string) (brainProjectionBackfillAudit, bool, error) {
	var audit brainProjectionBackfillAudit
	var fenceGeneration int64
	var fenceBytes []byte
	var rebuildStarted time.Time
	var rawManifest []byte
	err := runtime.canonicalPool.QueryRow(ctx, `SELECT request_id,tenant_id,projector_version,room_id,sitting_id,source_family,
		expected_generation,start_source_high_water,end_source_high_water,authorized_by,approval_reference,authorization_expires_at,
		COALESCE(fence_generation,0),COALESCE(fence_token,''::bytea),COALESCE(rebuild_started_at,'epoch'::timestamptz),
		COALESCE(source_manifest,'null'::jsonb)
		FROM brain_projection_backfill_requests WHERE request_id=$1`, requestID).Scan(&audit.RequestID, &audit.Key.TenantID,
		&audit.Key.ProjectorVersion, &audit.Key.RoomID, &audit.Key.SittingID, &audit.Key.SourceFamily, &audit.ExpectedGeneration,
		&audit.StartSourceHighWater, &audit.EndSourceHighWater, &audit.AuthorizedBy, &audit.ApprovalReference,
		&audit.AuthorizationExpiry, &fenceGeneration, &fenceBytes, &rebuildStarted, &rawManifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return audit, false, nil
	}
	if err != nil {
		return audit, false, fmt.Errorf("read projection backfill authorization: %w", err)
	}
	if fenceGeneration > 0 {
		token, tokenErr := tokenFromBytes(fenceBytes)
		if tokenErr != nil || rebuildStarted.Equal(time.Unix(0, 0).UTC()) || bytes.Equal(rawManifest, []byte("null")) ||
			json.Unmarshal(rawManifest, &audit.SourceManifest) != nil || validateBackfillManifestSnapshot(audit) != nil {
			return brainProjectionBackfillAudit{}, false, ErrBrainProjectionCheckpointCorrupt
		}
		audit.FenceGeneration, audit.FenceToken, audit.RebuildStartedAt, audit.HasFence = fenceGeneration, token, rebuildStarted.UTC(), true
	} else if len(fenceBytes) != 0 || !rebuildStarted.Equal(time.Unix(0, 0).UTC()) || !bytes.Equal(rawManifest, []byte("null")) {
		return brainProjectionBackfillAudit{}, false, ErrBrainProjectionCheckpointCorrupt
	}
	return audit, true, nil
}

func backfillAuditMatchesAuthorization(audit brainProjectionBackfillAudit, authorization BrainProjectionHistoricalBackfillAuthorization) bool {
	return audit.RequestID == authorization.RequestID && audit.Key == authorization.Key && audit.ExpectedGeneration == authorization.ExpectedGeneration &&
		audit.StartSourceHighWater == authorization.StartSourceHighWater && audit.EndSourceHighWater == authorization.EndSourceHighWater &&
		audit.AuthorizedBy == authorization.AuthorizedBy && audit.ApprovalReference == authorization.ApprovalReference &&
		audit.AuthorizationExpiry.UnixMicro() == authorization.ExpiresAt.UTC().UnixMicro()
}

func bindBackfillAuthorizationAndFenceTx(ctx context.Context, tx pgx.Tx, authorization BrainProjectionHistoricalBackfillAuthorization,
	fence BrainProjectionRebuildFence, manifest BrainProjectionSourceManifest) error {
	rawManifest, err := canonicalJSON(manifest)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO brain_projection_backfill_requests (
		request_id,tenant_id,projector_version,room_id,sitting_id,source_family,expected_generation,
		start_source_high_water,end_source_high_water,authorized_by,approval_reference,authorization_expires_at,
		fence_generation,fence_token,rebuild_started_at,source_manifest
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb)
	ON CONFLICT (request_id) DO UPDATE SET fence_generation=EXCLUDED.fence_generation,fence_token=EXCLUDED.fence_token,
		rebuild_started_at=EXCLUDED.rebuild_started_at,source_manifest=EXCLUDED.source_manifest
	WHERE brain_projection_backfill_requests.tenant_id=EXCLUDED.tenant_id
		AND brain_projection_backfill_requests.projector_version=EXCLUDED.projector_version
		AND brain_projection_backfill_requests.room_id=EXCLUDED.room_id
		AND brain_projection_backfill_requests.sitting_id=EXCLUDED.sitting_id
		AND brain_projection_backfill_requests.source_family=EXCLUDED.source_family
		AND brain_projection_backfill_requests.expected_generation=EXCLUDED.expected_generation
		AND brain_projection_backfill_requests.start_source_high_water=EXCLUDED.start_source_high_water
		AND brain_projection_backfill_requests.end_source_high_water=EXCLUDED.end_source_high_water
		AND brain_projection_backfill_requests.authorized_by=EXCLUDED.authorized_by
		AND brain_projection_backfill_requests.approval_reference=EXCLUDED.approval_reference
		AND brain_projection_backfill_requests.authorization_expires_at=EXCLUDED.authorization_expires_at
		AND ((brain_projection_backfill_requests.fence_generation IS NULL
			AND brain_projection_backfill_requests.authorization_expires_at > now())
			OR (brain_projection_backfill_requests.fence_generation=EXCLUDED.fence_generation
				AND brain_projection_backfill_requests.fence_token=EXCLUDED.fence_token
				AND brain_projection_backfill_requests.rebuild_started_at=EXCLUDED.rebuild_started_at
				AND brain_projection_backfill_requests.source_manifest=EXCLUDED.source_manifest))`, authorization.RequestID,
		authorization.Key.TenantID, authorization.Key.ProjectorVersion, authorization.Key.RoomID, authorization.Key.SittingID,
		authorization.Key.SourceFamily, authorization.ExpectedGeneration, authorization.StartSourceHighWater,
		authorization.EndSourceHighWater, authorization.AuthorizedBy, authorization.ApprovalReference,
		authorization.ExpiresAt.UTC(), fence.Generation, fence.Token.value[:], fence.StartedAt.UTC(), rawManifest)
	if err != nil {
		return fmt.Errorf("bind projection backfill authorization and fence: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrBrainProjectionBackfillUnauthorized
	}
	return nil
}

func validateBackfillManifestSnapshot(audit brainProjectionBackfillAudit) error {
	manifest := audit.SourceManifest
	if manifest.Format != brainProjectionManifestFormat || manifest.Key != audit.Key || manifest.HighWater != audit.EndSourceHighWater {
		return ErrBrainProjectionCheckpointCorrupt
	}
	digestible := manifest
	digestible.ManifestSHA = ""
	raw, err := canonicalJSON(digestible)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if manifest.ManifestSHA != hex.EncodeToString(digest[:]) {
		return ErrBrainProjectionCheckpointCorrupt
	}
	return nil
}

func (runtime *productionBrainProjectionRuntime) scheduleExplicitRebuildWork(ctx context.Context, key BrainProjectionCheckpointKey) error {
	_, err := runtime.canonicalPool.Exec(ctx, `INSERT INTO brain_projection_work (
		tenant_id,projector_version,room_id,sitting_id,source_family,requested_at,available_at,last_error
	) VALUES ($1,$2,$3,$4,$5,now(),now(),'')
	ON CONFLICT (tenant_id,projector_version,room_id,sitting_id,source_family) DO UPDATE SET
		requested_at=EXCLUDED.requested_at,available_at=EXCLUDED.available_at,
		request_generation=nextval('brain_projection_work_request_generation_seq'),lease_token=NULL,lease_expires_at=NULL`, key.TenantID, key.ProjectorVersion,
		key.RoomID, key.SittingID, key.SourceFamily)
	return err
}

func (runtime *productionBrainProjectionRuntime) loadBackfillAuditForCheckpoint(ctx context.Context, checkpoint BrainProjectionCheckpoint) (brainProjectionBackfillAudit, error) {
	var requestID string
	err := runtime.canonicalPool.QueryRow(ctx, `SELECT request_id FROM brain_projection_backfill_requests
		WHERE tenant_id=$1 AND projector_version=$2 AND room_id=$3 AND sitting_id=$4 AND source_family=$5
			AND fence_generation=$6 AND fence_token=$7 AND start_source_high_water=$8 AND end_source_high_water=$9
			AND rebuild_started_at=$10 AND source_manifest IS NOT NULL`, checkpoint.Key.TenantID, checkpoint.Key.ProjectorVersion,
		checkpoint.Key.RoomID, checkpoint.Key.SittingID, checkpoint.Key.SourceFamily, checkpoint.RebuildGeneration,
		checkpoint.RebuildFenceToken.value[:], checkpoint.RebuildStartHighWater, checkpoint.RebuildEndHighWater,
		checkpoint.RebuildStartedAt.UTC()).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return brainProjectionBackfillAudit{}, ErrBrainProjectionFenceLost
	}
	if err != nil {
		return brainProjectionBackfillAudit{}, err
	}
	audit, found, err := runtime.loadBackfillAudit(ctx, requestID)
	if err != nil || !found || !audit.HasFence || audit.FenceGeneration != checkpoint.RebuildGeneration ||
		audit.FenceToken != checkpoint.RebuildFenceToken || audit.SourceManifest.ManifestSHA != hex.EncodeToString(checkpoint.RebuildSourceManifestSHA256[:]) {
		if err != nil {
			return brainProjectionBackfillAudit{}, err
		}
		return brainProjectionBackfillAudit{}, ErrBrainProjectionFenceLost
	}
	return audit, nil
}

func (runtime *productionBrainProjectionRuntime) projectScope(ctx context.Context, key BrainProjectionCheckpointKey) error {
	status, err := runtime.checkpoints.Status(ctx, key)
	if err != nil {
		return err
	}
	if err == nil && !status.Stale {
		return nil
	}
	var manifest BrainProjectionSourceManifest
	var source BrainProjectionSourceState
	if status.Checkpoint.RebuildActive {
		audit, auditErr := runtime.loadBackfillAuditForCheckpoint(ctx, status.Checkpoint)
		if auditErr != nil {
			return auditErr
		}
		manifest = audit.SourceManifest
		source = BrainProjectionSourceState{HighWater: manifest.HighWater, ManifestSHA256: status.Checkpoint.RebuildSourceManifestSHA256}
	} else {
		tx, txErr := runtime.canonicalPool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if txErr != nil {
			return txErr
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		manifest, source, err = runtime.resolver.ResolveBrainProjectionSourceManifest(ctx, tx, key)
		if err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	if len(manifest.Events) == 0 && len(manifest.Purges) == 0 {
		return nil
	}
	_, body, derivedHighWater, err := BuildBrainProjectionSanitizedReplay(manifest)
	if err != nil {
		return err
	}
	generation := status.Checkpoint.RebuildGeneration
	fence := status.Checkpoint.PublishedRebuildFenceToken
	if status.Checkpoint.RebuildActive {
		fence = status.Checkpoint.RebuildFenceToken
	} else if !status.Checkpoint.HasPublication {
		generation, fence = 0, BrainProjectionRebuildFenceToken{}
	}
	output, err := NewBrainProjectionDerivedOutput(key, generation, fence, source, derivedHighWater, body)
	if err != nil {
		return err
	}
	if _, err := runtime.publisher.Publish(ctx, output); err != nil {
		return err
	}
	runtime.mu.Lock()
	runtime.status.PublishedScopes++
	runtime.status.LastPublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	runtime.mu.Unlock()
	return nil
}
