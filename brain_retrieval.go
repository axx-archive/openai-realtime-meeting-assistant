package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrBrainRetrievalInvalid     = errors.New("brain retrieval request or dependency is invalid")
	ErrBrainRetrievalUnavailable = errors.New("brain retrieval dependency is unavailable")
	ErrBrainRetrievalRetry       = errors.New("brain retrieval source snapshot changed; retry required")
)

// BrainSourceMetadata is deliberately body-free. Inventory implementations may
// inspect canonical metadata, but the resolver authorizes both metadata and the
// exact content revision before asking BrainSourceBodyReader for any body.
type BrainSourceMetadata struct {
	Evidence        BrainEvidenceRef             `json:"evidence"`
	CaptureSequence uint64                       `json:"captureSequence,omitempty"`
	CapturedAt      time.Time                    `json:"capturedAt"`
	Segments        []BrainSourceSegmentMetadata `json:"segments,omitempty"`
	temporalClipped bool
	lateArrival     bool
}

// BrainSourceSegmentMetadata is authoritative, body-free inventory metadata
// binding a timed segment to exact bytes in the revision identified by
// Evidence.ContentDigest. A clipped source must provide a full partition.
type BrainSourceSegmentMetadata struct {
	OccurredStart time.Time `json:"occurredStart"`
	OccurredEnd   time.Time `json:"occurredEnd"`
	ByteStart     int       `json:"byteStart"`
	ByteEnd       int       `json:"byteEnd"`
}

// BrainSourceInventoryPage is one page from a stable inventory snapshot.
// InventoryID, high-waters, bounds, and SnapshotAt must remain identical on
// every page. NextCursor is opaque and an empty value terminates the scan.
type BrainSourceInventoryPage struct {
	InventoryID            string                `json:"inventoryId"`
	InventoryManifest      string                `json:"inventoryManifest"`
	ExpectedSourceCount    uint64                `json:"expectedSourceCount"`
	Sources                []BrainSourceMetadata `json:"sources"`
	NextCursor             string                `json:"nextCursor,omitempty"`
	Terminal               bool                  `json:"terminal"`
	SourceHighWater        uint64                `json:"sourceHighWater"`
	CaptureCompleteThrough uint64                `json:"captureCompleteThrough"`
	ProjectionHighWater    uint64                `json:"projectionHighWater"`
	ResolvedStartUTC       time.Time             `json:"resolvedStartUtc"`
	ResolvedEndUTC         time.Time             `json:"resolvedEndUtc"`
	SnapshotAt             time.Time             `json:"snapshotAt"`
}

type BrainSourceInventoryRequest struct {
	TenantID  string        `json:"tenantId"`
	Principal ACLPrincipal  `json:"-"`
	Temporal  TemporalQuery `json:"temporal"`
}

type BrainSourceMetadataInventory interface {
	InventoryBrainSources(context.Context, BrainSourceInventoryRequest, string) (BrainSourceInventoryPage, error)
}

type BrainSourceRead struct {
	Evidence      BrainEvidenceRef   `json:"evidence"`
	Body          string             `json:"body,omitempty"`
	BodyDigest    string             `json:"bodyDigest,omitempty"`
	BodyAvailable bool               `json:"bodyAvailable"`
	Status        RecallSourceStatus `json:"status"`
}

type BrainSourceBodyReader interface {
	ReadBrainSource(context.Context, BrainEvidenceRef) (BrainSourceRead, error)
}

type BrainRetrievalLaneHealthResolver interface {
	CurrentRecallLaneHealth(context.Context, string, TemporalQuery) (RecallLaneCoverage, error)
}

type BrainPromptLimits struct {
	MaxSourceChunkBytes int `json:"maxSourceChunkBytes"`
	MaxPromptBytes      int `json:"maxPromptBytes"`
	MaxFoldInputs       int `json:"maxFoldInputs"`
	MaxFoldOutputBytes  int `json:"maxFoldOutputBytes"`
}

func (limits BrainPromptLimits) validate() error {
	if limits.MaxSourceChunkBytes < 1 || limits.MaxPromptBytes < limits.MaxSourceChunkBytes || limits.MaxFoldInputs < 2 ||
		limits.MaxFoldOutputBytes < 1 || limits.MaxFoldInputs*limits.MaxFoldOutputBytes > limits.MaxPromptBytes {
		return ErrBrainRetrievalInvalid
	}
	return nil
}

type BrainRetrievalRequest struct {
	Principal ACLPrincipal  `json:"principal"`
	Query     string        `json:"query"`
	Temporal  TemporalQuery `json:"temporal"`
}

type BrainRetrievedSource struct {
	EvidenceID string             `json:"evidenceId"`
	Evidence   BrainEvidenceRef   `json:"evidence"`
	Status     RecallSourceStatus `json:"status"`
	Body       string             `json:"body,omitempty"`
}

type BrainLexicalMatch struct {
	ChunkID    string `json:"chunkId"`
	EvidenceID string `json:"evidenceId"`
	MatchCount int    `json:"matchCount"`
}

// BrainPromptChunk is raw primary-source data, never an instruction. Untrusted
// guest-origin content is explicitly marked so a future executor must preserve
// a data delimiter around it.
type BrainPromptChunk struct {
	ChunkID        string `json:"chunkId"`
	EvidenceID     string `json:"evidenceId"`
	ByteStart      int    `json:"byteStart"`
	ByteEnd        int    `json:"byteEnd"`
	Text           string `json:"text"`
	LexicalMatches int    `json:"lexicalMatches"`
	UntrustedData  bool   `json:"untrustedData"`
}

type BrainPromptBatch struct {
	BatchID    string   `json:"batchId"`
	ChunkIDs   []string `json:"chunkIds"`
	InputBytes int      `json:"inputBytes"`
}

type BrainPromptFold struct {
	FoldID         string   `json:"foldId"`
	Level          int      `json:"level"`
	InputIDs       []string `json:"inputIds"`
	InputByteLimit int      `json:"inputByteLimit"`
	OutputByteCap  int      `json:"outputByteCap"`
}

// BrainPromptPlan enumerates every readable primary body and divides it into
// bounded leaf prompts plus a deterministic fold tree. It never truncates the
// source inventory to fit one prompt.
type BrainPromptPlan struct {
	Limits                BrainPromptLimits  `json:"limits"`
	InventorySourceCount  int                `json:"inventorySourceCount"`
	ReadableSourceCount   int                `json:"readableSourceCount"`
	ClassifiedWithoutBody int                `json:"classifiedWithoutBody"`
	Chunks                []BrainPromptChunk `json:"chunks"`
	Batches               []BrainPromptBatch `json:"batches"`
	Folds                 []BrainPromptFold  `json:"folds"`
	RootID                string             `json:"rootId,omitempty"`
}

type BrainRetrievalResult struct {
	Snapshot       RetrievalSnapshot      `json:"snapshot"`
	Coverage       RecallCoverage         `json:"coverage"`
	Sources        []BrainRetrievedSource `json:"sources"`
	LexicalMatches []BrainLexicalMatch    `json:"lexicalMatches"`
	PromptPlan     BrainPromptPlan        `json:"promptPlan"`
}

type BrainRetrievalPlanner struct {
	Inventory    BrainSourceMetadataInventory
	Bodies       BrainSourceBodyReader
	Kernel       AuthorizationKernel
	Purge        BrainPurgeGenerationResolver
	LaneHealth   BrainRetrievalLaneHealthResolver
	PromptLimits BrainPromptLimits
}

func (planner BrainRetrievalPlanner) Resolve(ctx context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
	var result BrainRetrievalResult
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" || request.Temporal.Validate() != nil || planner.Inventory == nil || planner.Bodies == nil || planner.Purge == nil ||
		planner.Kernel.Store == nil || planner.PromptLimits.validate() != nil || request.Principal.TenantID == "" || request.Principal.ID == "" {
		return result, ErrBrainRetrievalInvalid
	}
	if request.Principal.Kind == ACLPrincipalGuest || request.Principal.Kind == ACLPrincipalCapability {
		return result, fmt.Errorf("%w: guest and capability principals have no durable recall", ErrBrainRetrievalInvalid)
	}
	if request.Principal.Kind != ACLPrincipalUser && request.Principal.Kind != ACLPrincipalService {
		return result, ErrBrainRetrievalInvalid
	}

	purgeGeneration, err := planner.Purge.CurrentPurgeGeneration(ctx, request.Principal.TenantID)
	if err != nil || purgeGeneration < 0 {
		return result, fmt.Errorf("%w: purge generation", ErrBrainRetrievalUnavailable)
	}
	pages, err := planner.inventoryAll(ctx, BrainSourceInventoryRequest{TenantID: request.Principal.TenantID, Principal: request.Principal, Temporal: request.Temporal})
	if err != nil {
		return result, err
	}

	metadata, err := normalizeBrainInventorySources(pages.sources, request.Temporal, request.Principal.TenantID, purgeGeneration)
	if err != nil {
		return result, err
	}
	lanes := planner.resolveLaneHealth(ctx, request.Principal.TenantID, request.Temporal)
	retrieved := make([]BrainRetrievedSource, 0, len(metadata))
	snapshotSources := make([]RetrievalSnapshotSource, 0, len(metadata))
	coverageSources := make([]RecallSourceCoverage, 0, len(metadata))

	for _, source := range metadata {
		ref := source.Evidence
		objectRef, revisionRef := ref.ACLRefs()
		metadataDecision := planner.Kernel.Authorize(ctx, request.Principal, ACLReadMetadata, objectRef, ACLRevisionRef{})
		if !metadataDecision.Allowed {
			if metadataDecision.DenialCode == ACLDenialUnavailable || metadataDecision.DenialCode == ACLDenialUnauthenticated {
				return result, fmt.Errorf("%w: metadata authorization", ErrBrainRetrievalUnavailable)
			}
			continue
		}
		contentDecision := planner.Kernel.Authorize(ctx, request.Principal, ACLReadContent, objectRef, revisionRef)
		if !contentDecision.Allowed {
			if contentDecision.DenialCode == ACLDenialUnavailable || contentDecision.DenialCode == ACLDenialUnauthenticated {
				return result, fmt.Errorf("%w: content authorization", ErrBrainRetrievalUnavailable)
			}
			if contentDecision.ACLVersion != ref.ACLVersion || strings.Contains(contentDecision.PolicyReason, "revision is stale") || strings.Contains(contentDecision.PolicyReason, "identity or ACL version is stale") {
				return result, fmt.Errorf("%w: %s/%s changed before body read", ErrBrainRetrievalRetry, ref.SourceFamily, ref.ObjectID)
			}
			continue
		}
		if source.lateArrival {
			pages.lateArrivalSources++
		}

		evidenceID, idErr := brainRetrievalEvidenceID(ref)
		if idErr != nil {
			return result, idErr
		}
		read, readErr := planner.Bodies.ReadBrainSource(ctx, ref)
		status := read.Status
		body := ""
		if readErr != nil {
			status = RecallSourceFailed
		} else {
			if status == "" {
				status = RecallSourceFresh
			}
			if !validRecallSourceStatus(status) || !sameBrainEvidenceRef(read.Evidence, ref) {
				return result, fmt.Errorf("%w: %s/%s changed during body read", ErrBrainRetrievalRetry, ref.SourceFamily, ref.ObjectID)
			}
			if status == RecallSourceFresh || status == RecallSourcePartial {
				if !read.BodyAvailable {
					status = RecallSourceMissing
				} else if actualDigest := digestBrainString(read.Body); read.BodyDigest != ref.ContentDigest || actualDigest != ref.ContentDigest {
					return result, fmt.Errorf("%w: %s/%s body digest changed during read", ErrBrainRetrievalRetry, ref.SourceFamily, ref.ObjectID)
				} else {
					body = read.Body
					if source.temporalClipped {
						var clipped bool
						body, clipped, err = clipBrainSourceBody(read.Body, source.Segments, request.Temporal)
						if err != nil {
							return result, err
						}
						if body == "" {
							status = RecallSourceOmitted
						} else if clipped {
							status = RecallSourcePartial
						}
					}
				}
			} else if read.BodyAvailable {
				return result, fmt.Errorf("%w: non-readable status carried a body", ErrBrainRetrievalInvalid)
			}
		}
		retrieved = append(retrieved, BrainRetrievedSource{EvidenceID: evidenceID, Evidence: ref, Status: status, Body: body})
		snapshotSources = append(snapshotSources, RetrievalSnapshotSource{EvidenceID: evidenceID, Evidence: ref})
		coverageSources = append(coverageSources, RecallSourceCoverage{SourceFamily: ref.SourceFamily, ObjectID: ref.ObjectID, ContentDigest: ref.ContentDigest, Status: status})
	}

	snapshot := RetrievalSnapshot{
		TenantID: request.Principal.TenantID, PrincipalKind: request.Principal.Kind, PrincipalID: request.Principal.ID,
		Query: request.Query, QueryDigest: digestBrainString(request.Query), Temporal: request.Temporal,
		SourceHighWater: pages.sourceHighWater, ProjectionHighWater: pages.projectionHighWater,
		PurgeGeneration: purgeGeneration, Sources: snapshotSources, CreatedAt: pages.snapshotAt,
	}
	snapshot.SnapshotID, err = snapshot.CanonicalID()
	if err != nil || snapshot.Validate() != nil {
		return result, fmt.Errorf("%w: canonical snapshot", ErrBrainRetrievalInvalid)
	}
	if err := ReauthorizeRetrievalSnapshot(ctx, planner.Kernel, planner.Purge, request.Principal, snapshot); err != nil {
		return result, fmt.Errorf("%w: final source reauthorization: %v", ErrBrainRetrievalRetry, err)
	}

	lanes = observedBrainRetrievalLanes(lanes, retrieved)
	coverage := buildBrainRecallCoverage(snapshot, pages.resolvedStart, pages.resolvedEnd, coverageSources, lanes,
		pages.captureCompleteThrough, pages.lateArrivalSources, request.Temporal.Settled(pages.snapshotAt))
	if coverage.Validate() != nil {
		return result, fmt.Errorf("%w: canonical coverage", ErrBrainRetrievalInvalid)
	}
	plan, lexical, err := buildBrainPromptPlan(retrieved, request.Query, planner.PromptLimits)
	if err != nil {
		return result, err
	}
	result = BrainRetrievalResult{Snapshot: snapshot, Coverage: coverage, Sources: retrieved, LexicalMatches: lexical, PromptPlan: plan}
	return result, nil
}

type brainInventoryAggregate struct {
	sources                []BrainSourceMetadata
	sourceHighWater        uint64
	captureCompleteThrough uint64
	projectionHighWater    uint64
	resolvedStart          time.Time
	resolvedEnd            time.Time
	snapshotAt             time.Time
	lateArrivalSources     int
}

func (planner BrainRetrievalPlanner) inventoryAll(ctx context.Context, request BrainSourceInventoryRequest) (brainInventoryAggregate, error) {
	var aggregate brainInventoryAggregate
	cursor := ""
	seenCursors := map[string]bool{}
	var inventoryID string
	var inventoryManifest string
	var expectedSourceCount uint64
	for {
		page, err := planner.Inventory.InventoryBrainSources(ctx, request, cursor)
		if err != nil {
			return aggregate, fmt.Errorf("%w: source inventory", ErrBrainRetrievalUnavailable)
		}
		if !isHexDigest(page.InventoryID) || !isHexDigest(page.InventoryManifest) || page.SnapshotAt.IsZero() || page.SnapshotAt.Location() != time.UTC || page.ResolvedStartUTC.IsZero() || page.ResolvedEndUTC.IsZero() ||
			!page.ResolvedStartUTC.Before(page.ResolvedEndUTC) || page.ResolvedStartUTC.Before(request.Temporal.StartUTC) || page.ResolvedEndUTC.After(request.Temporal.EndUTC) {
			return aggregate, fmt.Errorf("%w: malformed inventory page", ErrBrainRetrievalInvalid)
		}
		if inventoryID == "" {
			inventoryID = page.InventoryID
			inventoryManifest, expectedSourceCount = page.InventoryManifest, page.ExpectedSourceCount
			aggregate.sourceHighWater, aggregate.projectionHighWater = page.SourceHighWater, page.ProjectionHighWater
			aggregate.captureCompleteThrough = page.CaptureCompleteThrough
			aggregate.resolvedStart, aggregate.resolvedEnd, aggregate.snapshotAt = page.ResolvedStartUTC, page.ResolvedEndUTC, page.SnapshotAt
		} else if page.InventoryID != inventoryID || page.InventoryManifest != inventoryManifest || page.ExpectedSourceCount != expectedSourceCount ||
			page.SourceHighWater != aggregate.sourceHighWater || page.CaptureCompleteThrough != aggregate.captureCompleteThrough || page.ProjectionHighWater != aggregate.projectionHighWater ||
			!page.ResolvedStartUTC.Equal(aggregate.resolvedStart) || !page.ResolvedEndUTC.Equal(aggregate.resolvedEnd) || !page.SnapshotAt.Equal(aggregate.snapshotAt) {
			return aggregate, fmt.Errorf("%w: inventory page snapshot drift", ErrBrainRetrievalRetry)
		}
		aggregate.sources = append(aggregate.sources, page.Sources...)
		if page.NextCursor == "" {
			if !page.Terminal {
				return aggregate, fmt.Errorf("%w: inventory ended without terminal proof", ErrBrainRetrievalInvalid)
			}
			break
		}
		if page.Terminal {
			return aggregate, fmt.Errorf("%w: terminal inventory page carried a continuation", ErrBrainRetrievalInvalid)
		}
		if page.NextCursor == cursor || seenCursors[page.NextCursor] {
			return aggregate, fmt.Errorf("%w: inventory cursor cycle", ErrBrainRetrievalInvalid)
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
	if uint64(len(aggregate.sources)) != expectedSourceCount {
		return aggregate, fmt.Errorf("%w: inventory count proof mismatch", ErrBrainRetrievalRetry)
	}
	wantManifest, err := brainInventoryManifestDigest(aggregate.sources)
	if err != nil || wantManifest != inventoryManifest {
		return aggregate, fmt.Errorf("%w: inventory manifest proof mismatch", ErrBrainRetrievalRetry)
	}
	return aggregate, nil
}

func normalizeBrainInventorySources(sources []BrainSourceMetadata, temporal TemporalQuery, tenantID string, purgeGeneration int64) ([]BrainSourceMetadata, error) {
	normalized := make([]BrainSourceMetadata, 0, len(sources))
	for _, source := range sources {
		ref := source.Evidence
		if ref.Validate() != nil || ref.TenantID != tenantID || ref.PurgeGeneration != purgeGeneration || ref.OccurredStart.IsZero() || ref.OccurredEnd.IsZero() || source.CapturedAt.IsZero() {
			if ref.PurgeGeneration != purgeGeneration {
				return nil, fmt.Errorf("%w: inventory purge generation drift", ErrBrainRetrievalRetry)
			}
			return nil, fmt.Errorf("%w: invalid inventory source", ErrBrainRetrievalInvalid)
		}
		for _, segment := range source.Segments {
			if segment.OccurredStart.IsZero() || segment.OccurredEnd.IsZero() || segment.OccurredStart.Location() != time.UTC || segment.OccurredEnd.Location() != time.UTC ||
				!segment.OccurredStart.Before(segment.OccurredEnd) || segment.OccurredStart.Before(ref.OccurredStart) || segment.OccurredEnd.After(ref.OccurredEnd) {
				return nil, fmt.Errorf("%w: invalid source segment metadata", ErrBrainRetrievalInvalid)
			}
		}
		decision := temporal.DecideSegment(CapturedTemporalSegment{OccurredStart: ref.OccurredStart, OccurredEnd: ref.OccurredEnd, CaptureSequence: source.CaptureSequence, CapturedAt: source.CapturedAt})
		if !decision.Include {
			continue
		}
		ref.OccurredStart, ref.OccurredEnd = decision.ClippedStart, decision.ClippedEnd
		source.Evidence = ref
		source.temporalClipped = decision.Clipped
		source.lateArrival = decision.LateArrival
		normalized = append(normalized, source)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		a, b := normalized[i].Evidence, normalized[j].Evidence
		if a.SourceFamily != b.SourceFamily {
			return a.SourceFamily < b.SourceFamily
		}
		if a.ObjectID != b.ObjectID {
			return a.ObjectID < b.ObjectID
		}
		if a.ContentRevision != b.ContentRevision {
			return a.ContentRevision < b.ContentRevision
		}
		if !a.OccurredStart.Equal(b.OccurredStart) {
			return a.OccurredStart.Before(b.OccurredStart)
		}
		if a.SpanStart != b.SpanStart {
			return a.SpanStart < b.SpanStart
		}
		return a.ContentDigest < b.ContentDigest
	})
	seen := map[string]bool{}
	for _, source := range normalized {
		ref := source.Evidence
		key := ref.SourceFamily + "\x00" + ref.ObjectID + "\x00" + fmt.Sprint(ref.ContentRevision)
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate source revision", ErrBrainRetrievalInvalid)
		}
		seen[key] = true
	}
	return normalized, nil
}

func brainInventoryManifestDigest(sources []BrainSourceMetadata) (string, error) {
	copy := make([]BrainSourceMetadata, len(sources))
	for index := range sources {
		copy[index] = sources[index]
		copy[index].temporalClipped = false
		copy[index].lateArrival = false
	}
	raw, err := canonicalJSON(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func clipBrainSourceBody(body string, segments []BrainSourceSegmentMetadata, temporal TemporalQuery) (string, bool, error) {
	if len(segments) == 0 {
		return "", false, fmt.Errorf("%w: clipped source lacks byte-addressable segment metadata", ErrBrainRetrievalInvalid)
	}
	var selected strings.Builder
	expectedStart := 0
	omitted := false
	for _, segment := range segments {
		if segment.ByteStart != expectedStart || segment.ByteEnd <= segment.ByteStart || segment.ByteEnd > len(body) {
			return "", false, fmt.Errorf("%w: source segment partition is invalid", ErrBrainRetrievalInvalid)
		}
		expectedStart = segment.ByteEnd
		inside := !segment.OccurredStart.Before(temporal.StartUTC) && !segment.OccurredEnd.After(temporal.EndUTC)
		overlaps := segment.OccurredStart.Before(temporal.EndUTC) && temporal.StartUTC.Before(segment.OccurredEnd)
		if inside {
			selected.WriteString(body[segment.ByteStart:segment.ByteEnd])
		} else {
			omitted = true
			// A segment that straddles the requested boundary is deliberately
			// excluded: byte-level timing inside that segment is unknowable.
			_ = overlaps
		}
	}
	if expectedStart != len(body) {
		return "", false, fmt.Errorf("%w: source segment partition does not cover body", ErrBrainRetrievalInvalid)
	}
	return selected.String(), omitted, nil
}

func brainRetrievalEvidenceID(ref BrainEvidenceRef) (string, error) {
	raw, err := canonicalJSON(ref)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func sameBrainEvidenceRef(left, right BrainEvidenceRef) bool {
	leftRaw, leftErr := canonicalJSON(left)
	rightRaw, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func (planner BrainRetrievalPlanner) resolveLaneHealth(ctx context.Context, tenantID string, temporal TemporalQuery) RecallLaneCoverage {
	defaults := RecallLaneCoverage{Lexical: RecallLaneActive, Semantic: RecallLaneNotRequired, Digest: RecallLaneNotRequired, Raw: RecallLaneActive}
	if planner.LaneHealth == nil {
		return defaults
	}
	lanes, err := planner.LaneHealth.CurrentRecallLaneHealth(ctx, tenantID, temporal)
	if err != nil || !validRecallLaneState(lanes.Lexical) || !validRecallLaneState(lanes.Semantic) || !validRecallLaneState(lanes.Digest) || !validRecallLaneState(lanes.Raw) {
		return RecallLaneCoverage{Lexical: RecallLaneActive, Semantic: RecallLaneUnavailable, Digest: RecallLaneUnavailable, Raw: RecallLaneActive}
	}
	return lanes
}

func observedBrainRetrievalLanes(lanes RecallLaneCoverage, sources []BrainRetrievedSource) RecallLaneCoverage {
	if len(sources) == 0 {
		lanes.Raw, lanes.Lexical, lanes.Digest = RecallLaneUnavailable, RecallLaneUnavailable, RecallLaneUnavailable
		return lanes
	}
	readable := 0
	incomplete := false
	for _, source := range sources {
		if source.Status == RecallSourceFresh || source.Status == RecallSourcePartial {
			readable++
		}
		if source.Status != RecallSourceFresh {
			incomplete = true
		}
	}
	if readable == 0 {
		lanes.Raw, lanes.Lexical = RecallLaneUnavailable, RecallLaneUnavailable
	} else if incomplete {
		lanes.Raw = degradeBrainLane(lanes.Raw)
		lanes.Lexical = degradeBrainLane(lanes.Lexical)
	}
	return lanes
}

func degradeBrainLane(state RecallLaneState) RecallLaneState {
	if state == RecallLaneActive || state == RecallLaneNotRequired {
		return RecallLaneDegraded
	}
	return state
}

func buildBrainRecallCoverage(snapshot RetrievalSnapshot, resolvedStart, resolvedEnd time.Time, sources []RecallSourceCoverage, lanes RecallLaneCoverage,
	captureCompleteThrough uint64, lateArrivalSources int, settled bool) RecallCoverage {
	coverage := RecallCoverage{
		SnapshotID: snapshot.SnapshotID, RequestedStartUTC: snapshot.Temporal.StartUTC, RequestedEndUTC: snapshot.Temporal.EndUTC,
		ResolvedStartUTC: resolvedStart, ResolvedEndUTC: resolvedEnd, Timezone: snapshot.Temporal.Timezone,
		SourceHighWater: snapshot.SourceHighWater, ProjectionHighWater: snapshot.ProjectionHighWater,
		AdmissionRelative:     snapshot.Temporal.Interpretation == TemporalBeforeAdmission,
		CaptureSequenceCutoff: snapshot.Temporal.CaptureSequenceCutoff, CaptureCompleteThrough: captureCompleteThrough,
		Settled: settled, LateArrivalSources: lateArrivalSources,
		Sources: sources, AuthorizedSources: len(sources), Lanes: lanes, AsOf: snapshot.CreatedAt,
	}
	for _, source := range sources {
		switch source.Status {
		case RecallSourceFresh:
			coverage.FreshSources++
		case RecallSourcePartial:
			coverage.PartialSources++
		case RecallSourceStale:
			coverage.StaleSources++
		case RecallSourceMissing:
			coverage.MissingSources++
		case RecallSourceFailed:
			coverage.FailedSources++
		case RecallSourceOmitted:
			coverage.OmittedSources++
		}
	}
	coverage.Status = deriveRecallCoverageStatus(coverage)
	if coverage.Status != RecallCoverageComplete {
		coverage.Reason = brainCoverageReason(coverage)
	}
	coverage.Digest, _ = coverage.CanonicalDigest()
	return coverage
}

func brainCoverageReason(coverage RecallCoverage) string {
	parts := make([]string, 0, 8)
	if coverage.AuthorizedSources == 0 {
		parts = append(parts, "no authorized sources were available")
	}
	if !coverage.ResolvedStartUTC.Equal(coverage.RequestedStartUTC) || !coverage.ResolvedEndUTC.Equal(coverage.RequestedEndUTC) {
		parts = append(parts, "the requested time range contains an inventory gap")
	}
	if coverage.ProjectionHighWater < coverage.SourceHighWater {
		parts = append(parts, "projection is behind the source high-water")
	}
	if coverage.AdmissionRelative && !coverage.Settled {
		parts = append(parts, "late source capture is still settling")
	}
	if coverage.AdmissionRelative && coverage.CaptureCompleteThrough < coverage.CaptureSequenceCutoff {
		parts = append(parts, "capture sequence continuity has not reached the admission cutoff")
	}
	if coverage.LateArrivalSources > 0 {
		parts = append(parts, fmt.Sprintf("%d late-arriving source(s) require explicit review", coverage.LateArrivalSources))
	}
	if coverage.PartialSources > 0 {
		parts = append(parts, fmt.Sprintf("%d source(s) were partial", coverage.PartialSources))
	}
	if coverage.StaleSources > 0 {
		parts = append(parts, fmt.Sprintf("%d source(s) were stale", coverage.StaleSources))
	}
	if coverage.MissingSources > 0 {
		parts = append(parts, fmt.Sprintf("%d source(s) were missing", coverage.MissingSources))
	}
	if coverage.FailedSources > 0 {
		parts = append(parts, fmt.Sprintf("%d source(s) failed", coverage.FailedSources))
	}
	if coverage.OmittedSources > 0 {
		parts = append(parts, fmt.Sprintf("%d source(s) were deliberately omitted", coverage.OmittedSources))
	}
	if coverage.Lanes.Semantic == RecallLaneUnavailable {
		parts = append(parts, "semantic retrieval was unavailable; raw and lexical retrieval remained independent")
	}
	if coverage.Lanes.Semantic == RecallLaneDegraded {
		parts = append(parts, "semantic retrieval was degraded")
	}
	if coverage.Lanes.Raw == RecallLaneUnavailable && coverage.Lanes.Lexical == RecallLaneUnavailable {
		parts = append(parts, "raw and lexical retrieval were unavailable")
	}
	if len(parts) == 0 {
		parts = append(parts, "one or more retrieval lanes were degraded")
	}
	return strings.Join(parts, "; ")
}

func buildBrainPromptPlan(sources []BrainRetrievedSource, query string, limits BrainPromptLimits) (BrainPromptPlan, []BrainLexicalMatch, error) {
	plan := BrainPromptPlan{Limits: limits, InventorySourceCount: len(sources)}
	if limits.validate() != nil {
		return plan, nil, ErrBrainRetrievalInvalid
	}
	queryTokens := brainLexicalTokens(query)
	for _, source := range sources {
		if source.Status != RecallSourceFresh && source.Status != RecallSourcePartial {
			plan.ClassifiedWithoutBody++
			continue
		}
		plan.ReadableSourceCount++
		parts := splitBrainPromptBody(source.Body, limits.MaxSourceChunkBytes)
		for _, part := range parts {
			matches := countBrainLexicalMatches(part.text, queryTokens)
			material := struct {
				EvidenceID string `json:"evidenceId"`
				Start      int    `json:"start"`
				End        int    `json:"end"`
				TextDigest string `json:"textDigest"`
			}{
				EvidenceID: source.EvidenceID, Start: part.start, End: part.end, TextDigest: digestBrainString(part.text),
			}
			raw, err := canonicalJSON(material)
			if err != nil {
				return plan, nil, err
			}
			sum := sha256.Sum256(raw)
			plan.Chunks = append(plan.Chunks, BrainPromptChunk{ChunkID: hex.EncodeToString(sum[:]), EvidenceID: source.EvidenceID,
				ByteStart: part.start, ByteEnd: part.end, Text: part.text, LexicalMatches: matches, UntrustedData: source.Evidence.Trust == BrainEvidenceUntrustedGuest})
		}
	}
	for index := 0; index < len(plan.Chunks); {
		batch := BrainPromptBatch{}
		for index < len(plan.Chunks) {
			chunk := plan.Chunks[index]
			addition := len(chunk.Text)
			if len(batch.ChunkIDs) > 0 {
				addition++
			}
			if len(batch.ChunkIDs) > 0 && batch.InputBytes+addition > limits.MaxPromptBytes {
				break
			}
			batch.ChunkIDs = append(batch.ChunkIDs, chunk.ChunkID)
			batch.InputBytes += addition
			index++
		}
		batch.BatchID = digestBrainID("batch", batch.ChunkIDs)
		plan.Batches = append(plan.Batches, batch)
	}
	levelInputs := make([]string, len(plan.Batches))
	for index := range plan.Batches {
		levelInputs[index] = plan.Batches[index].BatchID
	}
	level := 1
	for len(levelInputs) > 1 {
		next := make([]string, 0, (len(levelInputs)+limits.MaxFoldInputs-1)/limits.MaxFoldInputs)
		for start := 0; start < len(levelInputs); start += limits.MaxFoldInputs {
			end := start + limits.MaxFoldInputs
			if end > len(levelInputs) {
				end = len(levelInputs)
			}
			inputs := append([]string(nil), levelInputs[start:end]...)
			foldID := digestBrainID(fmt.Sprintf("fold-%d", level), inputs)
			plan.Folds = append(plan.Folds, BrainPromptFold{FoldID: foldID, Level: level, InputIDs: inputs,
				InputByteLimit: len(inputs) * limits.MaxFoldOutputBytes, OutputByteCap: limits.MaxFoldOutputBytes})
			next = append(next, foldID)
		}
		levelInputs, level = next, level+1
	}
	if len(levelInputs) == 1 {
		plan.RootID = levelInputs[0]
	}
	matches := make([]BrainLexicalMatch, 0)
	for _, chunk := range plan.Chunks {
		if chunk.LexicalMatches > 0 {
			matches = append(matches, BrainLexicalMatch{ChunkID: chunk.ChunkID, EvidenceID: chunk.EvidenceID, MatchCount: chunk.LexicalMatches})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].MatchCount != matches[j].MatchCount {
			return matches[i].MatchCount > matches[j].MatchCount
		}
		return matches[i].ChunkID < matches[j].ChunkID
	})
	return plan, matches, nil
}

type brainPromptPart struct {
	start, end int
	text       string
}

func splitBrainPromptBody(body string, limit int) []brainPromptPart {
	if body == "" {
		return []brainPromptPart{{}}
	}
	parts := make([]brainPromptPart, 0, (len(body)+limit-1)/limit)
	for start := 0; start < len(body); {
		end := start + limit
		if end > len(body) {
			end = len(body)
		}
		for end < len(body) && !utf8.RuneStart(body[end]) {
			end--
		}
		if end == start {
			_, size := utf8.DecodeRuneInString(body[start:])
			end = start + size
		}
		parts = append(parts, brainPromptPart{start: start, end: end, text: body[start:end]})
		start = end
	}
	return parts
}

func brainLexicalTokens(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return uniqueSortedStrings(fields)
}

func countBrainLexicalMatches(value string, tokens []string) int {
	if len(tokens) == 0 {
		return 0
	}
	words := brainLexicalTokens(value)
	counts := map[string]int{}
	for _, word := range words {
		counts[word]++
	}
	total := 0
	for _, token := range tokens {
		total += counts[token]
	}
	return total
}

func digestBrainID(namespace string, ids []string) string {
	raw, _ := canonicalJSON(struct {
		Namespace string   `json:"namespace"`
		IDs       []string `json:"ids"`
	}{namespace, ids})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
