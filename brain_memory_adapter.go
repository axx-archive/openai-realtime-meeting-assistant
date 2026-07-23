package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBrainSourceConsentAbsent = errors.New("brain source has no current organization-memory consent")

// BrainCurrentObjectResolver resolves the canonical identity of the current
// source revision without requiring a caller to guess its ACL version.
type BrainCurrentObjectResolver interface {
	CurrentBrainObject(context.Context, string, string, string) (ACLObject, error)
}

type aclBrainCurrentObjectResolver struct{ Store ACLStore }

func (resolver aclBrainCurrentObjectResolver) CurrentBrainObject(ctx context.Context, tenantID, family, objectID string) (ACLObject, error) {
	if resolver.Store == nil {
		return ACLObject{}, ErrCanonicalStoreUnhealthy
	}
	// ResolveACLObject implementations resolve by the stable identity tuple and
	// return the authoritative ACL version; ACLVersion is intentionally not a
	// lookup predicate.
	return resolver.Store.ResolveACLObject(ctx, ACLObjectRef{TenantID: tenantID, Type: family, ID: objectID, ACLVersion: 1})
}

// BrainSourceConsentVerifier is evaluated before an inventory row exists and
// again before its body is read or published. A denied source therefore cannot
// influence counts, ranking, prompts, or source drill-down.
type BrainSourceConsentVerifier interface {
	VerifyBrainSourceConsent(context.Context, meetingMemoryEntry) error
}

type BrainSourceConsentFenceAuthorizer interface {
	AuthorizeBrainSourceConsent(context.Context, meetingMemoryEntry) ([]ConsentFence, error)
}

type appBrainSourceConsentVerifier struct{ App *kanbanBoardApp }

func (verifier appBrainSourceConsentVerifier) VerifyBrainSourceConsent(ctx context.Context, entry meetingMemoryEntry) error {
	_, err := verifier.AuthorizeBrainSourceConsent(ctx, entry)
	return err
}

// AuthorizeBrainSourceConsent returns the exact current org-memory fences for
// every microphone contributor. Callers that invoke a model keep these fences
// through provider execution and use CommitWithFences for the final artifact;
// ordinary readers use VerifyBrainSourceConsent and discard them.
func (verifier appBrainSourceConsentVerifier) AuthorizeBrainSourceConsent(ctx context.Context, entry meetingMemoryEntry) ([]ConsentFence, error) {
	if verifier.App == nil || entry.Kind != meetingMemoryKindTranscript {
		return nil, ErrBrainSourceConsentAbsent
	}
	// These are deliberate typed submissions, not passive microphone capture.
	// Their authorization is enforced by the existing room/intake visibility
	// gates; the microphone-consent API must not misrepresent itself as a gate
	// on text the person explicitly sent.
	switch strings.TrimSpace(entry.Metadata["source"]) {
	case transcriptSourceRoomChat, "brain_intake":
		return nil, nil
	}
	roomID := normalizeRoomID(entry.Metadata["roomId"])
	sittingID := strings.TrimSpace(entry.Metadata["meetingId"])
	if rawBindings := strings.TrimSpace(entry.Metadata[consentContributorBindingsMetadataKey]); rawBindings != "" {
		bindings, err := decodeConsentContributorBindings(rawBindings)
		if err != nil || len(bindings) == 0 {
			return nil, ErrBrainSourceConsentAbsent
		}
		authority := currentConsentLaneAuthority()
		fences := make([]ConsentFence, 0, len(bindings))
		for _, binding := range bindings {
			if normalizeRoomID(binding.RoomID) != roomID || strings.TrimSpace(binding.SittingID) != sittingID || binding.TenantID != canonicalTenantID() {
				return nil, ErrBrainSourceConsentAbsent
			}
			principal := CanonicalPrincipalRef{Kind: string(binding.PrincipalKind), ID: binding.PrincipalID}
			canonicalBinding, canonicalErr := verifier.App.consentBindingForPrincipal(ctx, principal, roomID, sittingID)
			if canonicalErr != nil || consentBindingKey(canonicalBinding) != consentBindingKey(binding) || canonicalBinding.GuestPolicyListenOnly != binding.GuestPolicyListenOnly {
				if canonicalErr != nil && !errors.Is(canonicalErr, ErrConsentAdmissionInvalid) {
					return nil, canonicalErr
				}
				return nil, ErrBrainSourceConsentAbsent
			}
			decision, authorizeErr := authority.Authorize(ctx, canonicalBinding, ConsentLaneOrgMemory)
			if authorizeErr != nil {
				return nil, authorizeErr
			}
			if !decision.Allowed {
				return nil, ErrBrainSourceConsentAbsent
			}
			if validateErr := authority.ValidateFence(ctx, decision.Fence); validateErr != nil {
				if errors.Is(validateErr, ErrConsentFenceStale) {
					return nil, ErrBrainSourceConsentAbsent
				}
				return nil, validateErr
			}
			fences = append(fences, decision.Fence)
		}
		return fences, nil
	}
	principal := CanonicalPrincipalRef{
		Kind: strings.TrimSpace(entry.Metadata["consentPrincipalKind"]),
		ID:   strings.TrimSpace(entry.Metadata["consentPrincipalId"]),
	}
	if principal.Kind == "" && principal.ID == "" {
		if email := normalizeAccountEmail(participantEmail(entry.Metadata["speaker"])); email != "" && accountStore().findUser(email) != nil {
			principal = memberAdmissionPrincipal(email)
		}
	}
	if principal.Kind == "user" {
		principal.ID = normalizeAccountEmail(principal.ID)
	}
	if (principal.Kind != "user" && principal.Kind != "guest") || principal.ID == "" || (principal.Kind == "guest" && !isHexDigest(principal.ID)) {
		// Legacy/unattributed content has no durable recall. A display name is
		// never converted back into authority.
		return nil, ErrBrainSourceConsentAbsent
	}
	decision, err := verifier.App.effectiveConsentLane(ctx, principal, roomID, sittingID, ConsentLaneOrgMemory)
	if err != nil {
		if errors.Is(err, ErrConsentAdmissionInvalid) {
			return nil, ErrBrainSourceConsentAbsent
		}
		return nil, err
	}
	if !decision.Allowed {
		return nil, ErrBrainSourceConsentAbsent
	}
	if err := currentConsentLaneAuthority().ValidateFence(ctx, decision.Fence); err != nil {
		if errors.Is(err, ErrConsentFenceStale) {
			return nil, ErrBrainSourceConsentAbsent
		}
		return nil, err
	}
	return []ConsentFence{decision.Fence}, nil
}

// MeetingMemoryBrainAdapter is the W2 shadow reader over the authoritative
// JSONL transcript corpus. PostgreSQL supplies identity/ACL revisions while
// bodies remain local and are hash-verified immediately before use.
type MeetingMemoryBrainAdapter struct {
	Memory  *meetingMemoryStore
	Objects BrainCurrentObjectResolver
	Kernel  AuthorizationKernel
	Purge   BrainPurgeGenerationResolver
	Consent BrainSourceConsentVerifier
	Now     func() time.Time
}

func (adapter *MeetingMemoryBrainAdapter) InventoryBrainSources(ctx context.Context, request BrainSourceInventoryRequest, cursor string) (BrainSourceInventoryPage, error) {
	var page BrainSourceInventoryPage
	if adapter == nil || adapter.Memory == nil || adapter.Objects == nil || adapter.Kernel.Store == nil || adapter.Purge == nil || adapter.Consent == nil ||
		strings.TrimSpace(request.TenantID) == "" || request.Principal.TenantID != request.TenantID || strings.TrimSpace(request.Principal.ID) == "" ||
		request.Temporal.Validate() != nil || cursor != "" {
		return page, ErrBrainRetrievalInvalid
	}
	purgeGeneration, err := adapter.Purge.CurrentPurgeGeneration(ctx, request.TenantID)
	if err != nil || purgeGeneration < 0 {
		return page, ErrBrainRetrievalUnavailable
	}
	entries, err := adapter.authoritativeMemorySnapshot()
	if err != nil {
		return page, fmt.Errorf("%w: authoritative meeting memory", ErrBrainRetrievalUnavailable)
	}
	sources := make([]BrainSourceMetadata, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != meetingMemoryKindTranscript || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		roomID := normalizeRoomID(entry.Metadata["roomId"])
		sittingID := strings.TrimSpace(entry.Metadata["meetingId"])
		if request.Temporal.RoomID != "" && roomID != normalizeRoomID(request.Temporal.RoomID) {
			continue
		}
		if request.Temporal.SittingID != "" && sittingID != strings.TrimSpace(request.Temporal.SittingID) {
			continue
		}
		occurredStart, occurredEnd, capturedAt := brainMemoryEntryTimes(entry)
		if !occurredStart.Before(request.Temporal.EndUTC) || !request.Temporal.StartUTC.Before(occurredEnd) {
			continue
		}
		object, resolveErr := adapter.Objects.CurrentBrainObject(ctx, request.TenantID, "memory", entry.ID)
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrACLObjectNotFound) {
				continue
			}
			return page, fmt.Errorf("%w: canonical source identity", ErrBrainRetrievalUnavailable)
		}
		metadataDecision := adapter.Kernel.Authorize(ctx, request.Principal, ACLReadMetadata, object.Ref, ACLRevisionRef{})
		contentDecision := adapter.Kernel.Authorize(ctx, request.Principal, ACLReadContent, object.Ref, ACLRevisionRef{
			ContentRevision: object.CurrentContentRevision, ContentDigest: object.CurrentContentDigest,
		})
		if metadataDecision.DenialCode == ACLDenialUnavailable || contentDecision.DenialCode == ACLDenialUnavailable {
			return page, fmt.Errorf("%w: source authorization", ErrBrainRetrievalUnavailable)
		}
		if !metadataDecision.Allowed || !contentDecision.Allowed {
			continue
		}
		if consentErr := adapter.Consent.VerifyBrainSourceConsent(ctx, entry); consentErr != nil {
			if errors.Is(consentErr, ErrBrainSourceConsentAbsent) {
				continue
			}
			return page, fmt.Errorf("%w: consent authority", ErrBrainRetrievalUnavailable)
		}
		digest := digestBrainString(entry.Text)
		if object.Deleted || object.Ref.TenantID != request.TenantID || object.Ref.Type != "memory" || object.Ref.ID != entry.ID ||
			object.Ref.ACLVersion < 1 || object.CurrentContentRevision < 1 || object.CurrentContentDigest != digest {
			return page, fmt.Errorf("%w: canonical source drift", ErrBrainRetrievalRetry)
		}
		sequence, _ := entryCaptureSequence(entry)
		sources = append(sources, BrainSourceMetadata{
			Evidence: BrainEvidenceRef{
				TenantID: request.TenantID, SourceFamily: "memory", ObjectID: entry.ID,
				ContentRevision: object.CurrentContentRevision, ACLVersion: object.Ref.ACLVersion, ContentDigest: digest,
				RoomID: roomID, SittingID: sittingID, OccurredStart: occurredStart, OccurredEnd: occurredEnd,
				PurgeGeneration: purgeGeneration, Trust: BrainEvidenceTrusted,
			},
			CaptureSequence: sequence, CapturedAt: capturedAt,
			Segments: []BrainSourceSegmentMetadata{{OccurredStart: occurredStart, OccurredEnd: occurredEnd, ByteStart: 0, ByteEnd: len(entry.Text)}},
		})
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if !sources[i].Evidence.OccurredStart.Equal(sources[j].Evidence.OccurredStart) {
			return sources[i].Evidence.OccurredStart.Before(sources[j].Evidence.OccurredStart)
		}
		return sources[i].Evidence.ObjectID < sources[j].Evidence.ObjectID
	})
	manifest, err := brainInventoryManifestDigest(sources)
	if err != nil {
		return page, err
	}
	snapshotAt := time.Now().UTC()
	if adapter.Now != nil {
		snapshotAt = adapter.Now().UTC()
	}
	// Both progress markers are derived exclusively from authorized inventory
	// rows. Even the existence or sequence position of a denied source is
	// private metadata and must not change caller-visible completeness.
	sourceHighWater := maxBrainSourceCaptureSequence(sources)
	captureCompleteThrough := contiguousAuthorizedCaptureSequenceThrough(sources)
	idMaterial := struct {
		TenantID        string        `json:"tenantId"`
		Temporal        TemporalQuery `json:"temporal"`
		Manifest        string        `json:"manifest"`
		SourceHighWater uint64        `json:"sourceHighWater"`
		CapturedThrough uint64        `json:"capturedThrough"`
		PurgeGeneration int64         `json:"purgeGeneration"`
		SnapshotAt      time.Time     `json:"snapshotAt"`
	}{request.TenantID, request.Temporal, manifest, sourceHighWater, captureCompleteThrough, purgeGeneration, snapshotAt}
	rawID, err := canonicalJSON(idMaterial)
	if err != nil {
		return page, err
	}
	page = BrainSourceInventoryPage{
		InventoryID: digestBrainString(string(rawID)), InventoryManifest: manifest, ExpectedSourceCount: uint64(len(sources)), Sources: sources, Terminal: true,
		SourceHighWater: sourceHighWater, CaptureCompleteThrough: captureCompleteThrough, ProjectionHighWater: 0,
		ResolvedStartUTC: request.Temporal.StartUTC, ResolvedEndUTC: request.Temporal.EndUTC, SnapshotAt: snapshotAt,
	}
	return page, nil
}

func (adapter *MeetingMemoryBrainAdapter) ReadBrainSource(ctx context.Context, expected BrainEvidenceRef) (BrainSourceRead, error) {
	if adapter == nil || adapter.Memory == nil || adapter.Objects == nil || adapter.Purge == nil || adapter.Consent == nil || expected.Validate() != nil || expected.SourceFamily != "memory" {
		return BrainSourceRead{}, ErrBrainRetrievalInvalid
	}
	entry, found, authorityErr := adapter.authoritativeMemoryEntry(expected.ObjectID)
	if authorityErr != nil {
		return BrainSourceRead{}, ErrBrainRetrievalRetry
	}
	if !found || entry.Kind != meetingMemoryKindTranscript || memoryEntryHiddenFromRecall(entry) {
		return BrainSourceRead{Evidence: expected, Status: RecallSourceMissing}, nil
	}
	if err := adapter.Consent.VerifyBrainSourceConsent(ctx, entry); err != nil {
		if errors.Is(err, ErrBrainSourceConsentAbsent) {
			return BrainSourceRead{Evidence: expected, Status: RecallSourceOmitted}, nil
		}
		return BrainSourceRead{}, fmt.Errorf("%w: consent authority", ErrBrainRetrievalUnavailable)
	}
	object, err := adapter.Objects.CurrentBrainObject(ctx, expected.TenantID, expected.SourceFamily, expected.ObjectID)
	if err != nil {
		return BrainSourceRead{}, err
	}
	purgeGeneration, err := adapter.Purge.CurrentPurgeGeneration(ctx, expected.TenantID)
	if err != nil || purgeGeneration != expected.PurgeGeneration {
		return BrainSourceRead{}, ErrBrainRetrievalRetry
	}
	digest := digestBrainString(entry.Text)
	start, end, _ := brainMemoryEntryTimes(entry)
	if object.Deleted || object.Ref.ACLVersion != expected.ACLVersion || object.CurrentContentRevision != expected.ContentRevision ||
		object.CurrentContentDigest != expected.ContentDigest || digest != expected.ContentDigest || normalizeRoomID(entry.Metadata["roomId"]) != expected.RoomID ||
		strings.TrimSpace(entry.Metadata["meetingId"]) != expected.SittingID || start.After(expected.OccurredStart) || end.Before(expected.OccurredEnd) {
		return BrainSourceRead{}, ErrBrainRetrievalRetry
	}
	return BrainSourceRead{Evidence: expected, Body: entry.Text, BodyDigest: digest, BodyAvailable: true, Status: RecallSourceFresh}, nil
}

func (adapter *MeetingMemoryBrainAdapter) ReauthorizeEvidence(ctx context.Context, principal ACLPrincipal, sources []RetrievalSnapshotSource) error {
	_, err := adapter.ReauthorizeEvidenceWithConsentFences(ctx, principal, sources)
	return err
}

// ReauthorizeEvidenceWithConsentFences is the publication form of source
// reauthorization. It returns the exact current org-memory fences minted only
// after the authoritative body and canonical revision have been reread.
func (adapter *MeetingMemoryBrainAdapter) ReauthorizeEvidenceWithConsentFences(ctx context.Context, principal ACLPrincipal, sources []RetrievalSnapshotSource) ([]ConsentFence, error) {
	fences := make([]ConsentFence, 0, len(sources))
	for _, source := range sources {
		objectRef, revisionRef := source.Evidence.ACLRefs()
		metadata := adapter.Kernel.Authorize(ctx, principal, ACLReadMetadata, objectRef, ACLRevisionRef{})
		content := adapter.Kernel.Authorize(ctx, principal, ACLReadContent, objectRef, revisionRef)
		if !metadata.Allowed || !content.Allowed || metadata.ACLVersion != source.Evidence.ACLVersion || content.ACLVersion != source.Evidence.ACLVersion {
			return nil, ErrRetrievalSnapshotStale
		}
		read, err := adapter.ReadBrainSource(ctx, source.Evidence)
		if errors.Is(err, ErrBrainRetrievalUnavailable) {
			return nil, err
		}
		if err != nil || read.Status != RecallSourceFresh || !read.BodyAvailable || !sameBrainEvidenceRef(read.Evidence, source.Evidence) ||
			read.BodyDigest != source.Evidence.ContentDigest || digestBrainString(read.Body) != source.Evidence.ContentDigest {
			return nil, ErrRetrievalSnapshotStale
		}
		entry, found, authorityErr := adapter.authoritativeMemoryEntry(source.Evidence.ObjectID)
		if authorityErr != nil || !found {
			return nil, ErrRetrievalSnapshotStale
		}
		if authorizer, ok := adapter.Consent.(BrainSourceConsentFenceAuthorizer); ok {
			sourceFences, consentErr := authorizer.AuthorizeBrainSourceConsent(ctx, entry)
			if consentErr != nil {
				return nil, consentErr
			}
			fences = append(fences, sourceFences...)
		} else if consentErr := adapter.Consent.VerifyBrainSourceConsent(ctx, entry); consentErr != nil {
			return nil, consentErr
		}
	}
	return fences, nil
}

// authoritativeMemorySnapshot rereads the JSONL source of truth while holding
// the same lock used by appenders. The in-memory index is a convenience for
// legacy readers, never proof of the bytes that a W2 recall will expose.
func (adapter *MeetingMemoryBrainAdapter) authoritativeMemorySnapshot() ([]meetingMemoryEntry, error) {
	if adapter == nil || adapter.Memory == nil || strings.TrimSpace(adapter.Memory.path) == "" {
		return nil, ErrBrainRetrievalUnavailable
	}
	adapter.Memory.mu.Lock()
	defer adapter.Memory.mu.Unlock()
	return adapter.authoritativeMemorySnapshotLocked()
}

func (adapter *MeetingMemoryBrainAdapter) authoritativeMemorySnapshotLocked() ([]meetingMemoryEntry, error) {
	if adapter == nil || adapter.Memory == nil || strings.TrimSpace(adapter.Memory.path) == "" {
		return nil, ErrBrainRetrievalUnavailable
	}
	file, err := os.Open(adapter.Memory.path)
	if err != nil {
		if os.IsNotExist(err) && len(adapter.Memory.entries) == 0 {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1024*1024)
	entries := make([]meetingMemoryEntry, 0, len(adapter.Memory.entries))
	for {
		line, readErr := reader.ReadString('\n')
		if readErr == io.EOF && len(line) > 0 {
			return nil, fmt.Errorf("authoritative meeting memory has a torn final record")
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			var entry meetingMemoryEntry
			if decodeErr := json.Unmarshal([]byte(trimmed), &entry); decodeErr != nil || strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Text) == "" {
				return nil, fmt.Errorf("authoritative meeting memory contains an invalid record")
			}
			entry.Metadata = cloneBrainMemoryMetadata(entry.Metadata)
			entries = append(entries, entry)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}
	return entries, nil
}

func (adapter *MeetingMemoryBrainAdapter) authoritativeMemoryEntry(id string) (meetingMemoryEntry, bool, error) {
	entries, err := adapter.authoritativeMemorySnapshot()
	if err != nil {
		return meetingMemoryEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true, nil
		}
	}
	return meetingMemoryEntry{}, false, nil
}

func brainMemoryEntryTimes(entry meetingMemoryEntry) (time.Time, time.Time, time.Time) {
	capturedAt := entry.CreatedAt.UTC()
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["capturedAt"])); err == nil {
		capturedAt = parsed.UTC()
	}
	start := capturedAt
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["occurredStart"])); err == nil {
		start = parsed.UTC()
	}
	end := start.Add(time.Nanosecond)
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Metadata["occurredEnd"])); err == nil && parsed.After(start) {
		end = parsed.UTC()
	}
	return start, end, capturedAt
}

func cloneBrainMemoryMetadata(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func contiguousAuthorizedCaptureSequenceThrough(sources []BrainSourceMetadata) uint64 {
	seen := make(map[uint64]struct{}, len(sources))
	for _, source := range sources {
		if source.CaptureSequence > 0 {
			seen[source.CaptureSequence] = struct{}{}
		}
	}
	var through uint64
	for {
		next := through + 1
		if _, ok := seen[next]; !ok {
			return through
		}
		through = next
	}
}

// PostgresPurgeGenerationResolver treats the append-only purge ledger count as
// a durable generation. A concurrent purge changes the generation and forces
// source-snapshot retry across process restart.
type PostgresPurgeGenerationResolver struct{ pool *pgxpool.Pool }

func (resolver *PostgresPurgeGenerationResolver) CurrentPurgeGeneration(ctx context.Context, tenantID string) (int64, error) {
	if resolver == nil || resolver.pool == nil || strings.TrimSpace(tenantID) == "" {
		return -1, ErrRetentionRestoreGate
	}
	var generation int64
	if err := resolver.pool.QueryRow(ctx, `SELECT count(*)::bigint FROM purge_ledger WHERE tenant_id=$1`, tenantID).Scan(&generation); err != nil {
		return -1, err
	}
	return generation, nil
}

type productionCatchUpResolver struct {
	Planner  BrainRetrievalPlanner
	Sources  *MeetingMemoryBrainAdapter
	Postgres *PostgresCanonicalStore
	TenantID string
	// Test-only barrier after preflight succeeds and before conditional commit.
	// Production leaves it nil.
	beforeCommit func()
	// Test-only commit/failure seams for the durable publication protocol.
	commitPublication        func(context.Context, pgx.Tx) error
	publicationFailpoint     func(string) error
	publicationNow           func() time.Time
	markPublicationDelivered func(context.Context, string) error
	dispatchNotification     func(context.Context, notificationRecord) error
	dispatchWebsocket        func(notificationRecord) error
	dispatchOS               func(notificationRecord) error
	dispatchWebPush          func(context.Context, notificationRecord) error
}

func (resolver *productionCatchUpResolver) ResolveCatchUp(ctx context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
	return resolver.Planner.Resolve(ctx, request)
}

func (resolver *productionCatchUpResolver) CommitCatchUpPublication(ctx context.Context, principal ACLPrincipal, snapshot RetrievalSnapshot, publish func() error) error {
	if resolver == nil || resolver.Sources == nil || resolver.Postgres == nil || publish == nil {
		return ErrCatchUpUnavailable
	}
	// Mint exact contributor fences only after re-reading every authoritative
	// body and canonical revision. They remain held through the canonical/body
	// conditional commit below, so a withdrawal cannot land in the publication
	// gap.
	fences, err := resolver.Sources.ReauthorizeEvidenceWithConsentFences(ctx, principal, snapshot.Sources)
	if err != nil {
		return err
	}
	if resolver.beforeCommit != nil {
		resolver.beforeCommit()
	}
	commit := func() error {
		return resolver.Sources.commitCatchUpPublicationLocked(snapshot, func() error {
			return resolver.withCanonicalCatchUpSourceFence(ctx, principal, snapshot, publish)
		})
	}
	if len(fences) == 0 {
		return commit()
	}
	return currentConsentLaneAuthority().CommitWithFences(ctx, fences, commit)
}

// withCanonicalCatchUpSourceFence holds exact source objects and their grant
// rows against ACL/source mutation, and prevents a purge insert, until the
// local body+notification/result publication callback completes. The final
// authorization read occurs after those locks are held.
func (resolver *productionCatchUpResolver) withCanonicalCatchUpSourceFence(ctx context.Context, principal ACLPrincipal, snapshot RetrievalSnapshot, publish func() error) error {
	return resolver.withCanonicalCatchUpSourceFenceCommit(ctx, principal, snapshot, func(pgx.Tx) error { return publish() }, nil)
}

func (resolver *productionCatchUpResolver) withCanonicalCatchUpSourceFenceCommit(ctx context.Context, principal ACLPrincipal, snapshot RetrievalSnapshot, publish func(pgx.Tx) error, commitFn func(context.Context, pgx.Tx) error) error {
	if resolver == nil || resolver.Postgres == nil || resolver.Postgres.pool == nil || publish == nil {
		return ErrCatchUpUnavailable
	}
	tx, err := resolver.Postgres.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// INSERT/UPDATE/DELETE take ROW EXCLUSIVE, which conflicts with SHARE. This
	// makes the tenant purge-generation check stable through local disclosure;
	// supported purge paths also update the exact object row locked below.
	if _, err := tx.Exec(ctx, `LOCK TABLE purge_ledger IN SHARE MODE`); err != nil {
		return ErrRetrievalSnapshotStale
	}
	sources := append([]RetrievalSnapshotSource(nil), snapshot.Sources...)
	sort.Slice(sources, func(i, j int) bool {
		left, right := sources[i].Evidence, sources[j].Evidence
		if left.SourceFamily != right.SourceFamily {
			return left.SourceFamily < right.SourceFamily
		}
		return left.ObjectID < right.ObjectID
	})
	for _, source := range sources {
		expected := source.Evidence
		var contentRevision, aclVersion int64
		var contentDigest []byte
		var deleted bool
		err := tx.QueryRow(ctx, `SELECT content_revision,content_sha256,acl_version,(deleted_at IS NOT NULL)
			FROM objects WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND room_id=$4
			AND COALESCE(meeting_id,'')=$5 FOR SHARE`,
			expected.TenantID, expected.SourceFamily, expected.ObjectID, NormalizeCanonicalRoomID(expected.RoomID), expected.SittingID).
			Scan(&contentRevision, &contentDigest, &aclVersion, &deleted)
		if err != nil || deleted || contentRevision != expected.ContentRevision || aclVersion != expected.ACLVersion || hex.EncodeToString(contentDigest) != expected.ContentDigest {
			return ErrRetrievalSnapshotStale
		}
		grantRows, grantErr := tx.Query(ctx, `SELECT grant_id FROM object_grants
			WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND acl_version=$4 FOR SHARE`,
			expected.TenantID, expected.SourceFamily, expected.ObjectID, expected.ACLVersion)
		if grantErr != nil {
			return ErrRetrievalSnapshotStale
		}
		grantRows.Close()
	}
	// Re-run the complete principal, purge-generation, and exact revision
	// authorization only after conflicting source/grant/purge writers are held.
	if err := ReauthorizeRetrievalSnapshot(ctx, resolver.Planner.Kernel, resolver.Planner.Purge, principal, snapshot); err != nil {
		return err
	}
	if err := publish(tx); err != nil {
		return err
	}
	if commitFn == nil {
		commitFn = func(commitCtx context.Context, commitTx pgx.Tx) error { return commitTx.Commit(commitCtx) }
	}
	if err := commitFn(ctx, tx); err != nil {
		return fmt.Errorf("commit catch-up source authority fence: %w", err)
	}
	return nil
}

// commitCatchUpPublicationLocked keeps the authoritative JSONL body identity
// stable through the local notification append and result construction.
func (adapter *MeetingMemoryBrainAdapter) commitCatchUpPublicationLocked(snapshot RetrievalSnapshot, publish func() error) error {
	if adapter == nil || adapter.Memory == nil || publish == nil {
		return ErrCatchUpUnavailable
	}
	adapter.Memory.mu.Lock()
	defer adapter.Memory.mu.Unlock()
	entries, err := adapter.authoritativeMemorySnapshotLocked()
	if err != nil {
		return ErrRetrievalSnapshotStale
	}
	byID := make(map[string]meetingMemoryEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, source := range snapshot.Sources {
		entry, found := byID[source.Evidence.ObjectID]
		if !found || entry.Kind != meetingMemoryKindTranscript || memoryEntryHiddenFromRecall(entry) ||
			digestBrainString(entry.Text) != source.Evidence.ContentDigest ||
			normalizeRoomID(entry.Metadata["roomId"]) != normalizeRoomID(source.Evidence.RoomID) ||
			strings.TrimSpace(entry.Metadata["meetingId"]) != source.Evidence.SittingID {
			return ErrRetrievalSnapshotStale
		}
	}
	return publish()
}

func configureProductionCatchUpResolver(app *kanbanBoardApp) {
	runtime := currentCanonicalRuntime()
	if app == nil || app.memory == nil || runtime == nil || runtime.postgres == nil {
		return
	}
	purge := &PostgresPurgeGenerationResolver{pool: runtime.postgres.pool}
	sources := &MeetingMemoryBrainAdapter{
		Memory: app.memory, Objects: aclBrainCurrentObjectResolver{Store: runtime.postgres}, Kernel: AuthorizationKernel{Store: runtime.postgres}, Purge: purge,
		Consent: appBrainSourceConsentVerifier{App: app}, Now: func() time.Time { return time.Now().UTC() },
	}
	resolver := &productionCatchUpResolver{
		Sources: sources, Postgres: runtime.postgres, TenantID: runtime.tenantID,
		Planner: BrainRetrievalPlanner{
			Inventory: sources, Bodies: sources, Kernel: AuthorizationKernel{Store: runtime.postgres}, Purge: purge,
			PromptLimits: BrainPromptLimits{MaxSourceChunkBytes: 8 << 10, MaxPromptBytes: 64 << 10, MaxFoldInputs: 8, MaxFoldOutputBytes: 4 << 10},
		},
	}
	app.catchUpRecapResolver = resolver
	startCatchUpPublicationRecovery(resolver, app)
}

func maxBrainSourceCaptureSequence(sources []BrainSourceMetadata) uint64 {
	var highWater uint64
	for _, source := range sources {
		if source.CaptureSequence > highWater {
			highWater = source.CaptureSequence
		}
	}
	return highWater
}
