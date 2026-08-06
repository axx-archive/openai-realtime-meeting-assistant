package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

type productionAmbientReplayAuthority struct {
	app       *kanbanBoardApp
	runtime   *CanonicalRuntime
	inventory *MeetingMemoryBrainAdapter
	fences    AmbientReplayFenceAuthority
}

// AmbientReplayFenceAuthority resolves externally issued, digest-addressed
// approval and retained-rollback receipts. Implementations must authenticate
// both receipts and bind them to the exact source/release snapshot; merely
// accepting a syntactically valid caller string is forbidden.
type AmbientReplayFenceAuthority interface {
	ResolveAmbientReplayFences(context.Context, AmbientReplayPlanRequest, AmbientReplayAuthoritySnapshot) (approvalReference, rollbackFloor string, err error)
}

func newProductionAmbientReplayAuthority(app *kanbanBoardApp, runtime *CanonicalRuntime) *productionAmbientReplayAuthority {
	if app == nil || app.memory == nil || runtime == nil || runtime.postgres == nil {
		return nil
	}
	purge := &PostgresPurgeGenerationResolver{pool: runtime.postgres.pool}
	return &productionAmbientReplayAuthority{app: app, runtime: runtime, fences: currentAmbientReplayFenceAuthority(), inventory: &MeetingMemoryBrainAdapter{
		Memory: app.memory, Objects: aclBrainCurrentObjectResolver{Store: runtime.postgres}, Kernel: AuthorizationKernel{Store: runtime.postgres},
		Purge: purge, Consent: appBrainSourceConsentVerifier{App: app}, Now: func() time.Time { return time.Now().UTC() },
	}}
}

func (authority *productionAmbientReplayAuthority) Plan(ctx context.Context, request AmbientReplayPlanRequest) (AmbientReplayAuthoritySnapshot, error) {
	var snapshot AmbientReplayAuthoritySnapshot
	if authority == nil || authority.inventory == nil || authority.runtime == nil {
		return snapshot, ErrAmbientReplayUnavailable
	}
	release := currentReleaseIdentity()
	if !release.ProcessQualified || release.ReleaseCommit == "" || !isHexDigest(release.GitTreeDigest) {
		return snapshot, ErrAmbientReplayUnavailable
	}
	temporal, err := NewBoundedTemporalQuery(TemporalExplicitRange, time.Unix(0, 0).UTC(), time.Now().UTC().Add(24*time.Hour), "UTC", request.RoomID, request.SittingID, "governed ambient replay inventory")
	if err != nil {
		return snapshot, err
	}
	principal := ACLPrincipal{TenantID: request.TenantID, ID: request.AuthorizedBy, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: request.RoomID, SittingID: request.SittingID}
	page, err := authority.inventory.InventoryBrainSources(ctx, BrainSourceInventoryRequest{TenantID: request.TenantID, Principal: principal, Temporal: temporal}, "")
	if err != nil {
		return snapshot, err
	}
	selectedSitting := request.SittingID
	if selectedSitting == "" {
		selectedSitting = authority.oldestStaleSitting(page.Sources)
	}
	if selectedSitting == "" {
		return snapshot, ErrAmbientReplayInvalid
	}
	all := make([]BrainSourceMetadata, 0)
	for _, source := range page.Sources {
		if source.Evidence.SittingID == selectedSitting {
			all = append(all, source)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].CaptureSequence != all[j].CaptureSequence {
			return all[i].CaptureSequence < all[j].CaptureSequence
		}
		return all[i].Evidence.ObjectID < all[j].Evidence.ObjectID
	})
	if len(all) == 0 {
		return snapshot, ErrAmbientReplayInvalid
	}
	startAfter := request.StartAfter
	filtered := make([]BrainSourceMetadata, 0, ambientReplayMaxSources)
	for _, source := range all {
		if source.CaptureSequence <= startAfter {
			continue
		}
		if request.EndAt > 0 && source.CaptureSequence > request.EndAt {
			break
		}
		if len(filtered) < ambientReplayMaxSources {
			filtered = append(filtered, source)
		}
	}
	if len(filtered) == 0 {
		return snapshot, ErrAmbientReplayInvalid
	}
	for _, source := range filtered {
		entry, found := authority.app.memory.entryByID(source.Evidence.ObjectID)
		if !found || entry.Kind != meetingMemoryKindTranscript || digestBrainString(entry.Text) != source.Evidence.ContentDigest {
			return snapshot, ErrAmbientReplayDrift
		}
		fences, err := (appBrainSourceConsentVerifier{App: authority.app}).AuthorizeBrainSourceConsent(ctx, entry)
		if err != nil {
			return snapshot, err
		}
		consentDigest, err := ambientReplayConsentFenceDigest(fences, entry)
		if err != nil {
			return snapshot, err
		}
		snapshot.Sources = append(snapshot.Sources, AmbientReplaySource{ObjectID: source.Evidence.ObjectID, CaptureSequence: source.CaptureSequence,
			ContentRevision: source.Evidence.ContentRevision, ContentDigest: source.Evidence.ContentDigest, ACLVersion: source.Evidence.ACLVersion,
			PurgeGeneration: source.Evidence.PurgeGeneration, OccurredStart: source.Evidence.OccurredStart, OccurredEnd: source.Evidence.OccurredEnd,
			ConsentFenceDigest: consentDigest, RoomID: source.Evidence.RoomID, SittingID: source.Evidence.SittingID})
	}
	selected := make(map[string]bool, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		selected[source.ObjectID] = true
	}
	for _, source := range all {
		if !selected[source.Evidence.ObjectID] {
			snapshot.ExcludedSources = append(snapshot.ExcludedSources, source.Evidence.ObjectID)
		}
	}
	snapshot.CursorDigests = authority.cursorDigests(request.RoomID)
	snapshot.PurgeGeneration = snapshot.Sources[0].PurgeGeneration
	snapshot.ReleaseCommit, snapshot.ReleaseTreeDigest = release.ReleaseCommit, release.GitTreeDigest
	snapshot.ReleaseReceiptDigest, err = digestAmbientReplayValue(release)
	if err != nil {
		return snapshot, err
	}
	if authority.fences == nil {
		return snapshot, ErrAmbientReplayUnavailable
	}
	snapshot.ApprovalReference, snapshot.RollbackFloor, err = authority.fences.ResolveAmbientReplayFences(ctx, request, snapshot)
	if err != nil || !isHexDigest(snapshot.ApprovalReference) || !isHexDigest(snapshot.RollbackFloor) ||
		snapshot.ApprovalReference != request.ApprovalReference || snapshot.RollbackFloor != request.RollbackFloor {
		return AmbientReplayAuthoritySnapshot{}, ErrAmbientReplayUnauthorized
	}
	return snapshot, nil
}

func (authority *productionAmbientReplayAuthority) oldestStaleSitting(sources []BrainSourceMetadata) string {
	if authority == nil || authority.app == nil || authority.app.memory == nil {
		return ""
	}
	type boundary struct {
		id          string
		first, last time.Time
	}
	bySitting := map[string]boundary{}
	for _, source := range sources {
		id := strings.TrimSpace(source.Evidence.SittingID)
		if id == "" {
			continue
		}
		row := bySitting[id]
		row.id = id
		if row.first.IsZero() || source.Evidence.OccurredStart.Before(row.first) {
			row.first = source.Evidence.OccurredStart
		}
		if source.Evidence.OccurredEnd.After(row.last) {
			row.last = source.Evidence.OccurredEnd
		}
		bySitting[id] = row
	}
	rows := make([]boundary, 0, len(bySitting))
	for _, row := range bySitting {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].first.Before(rows[j].first) })
	digests := authority.app.memory.latestDigestPerMeeting()
	for _, row := range rows {
		digest, ok := digests[row.id]
		if !ok {
			return row.id
		}
		end, err := time.Parse(time.RFC3339, strings.TrimSpace(digest.Metadata[digestSpanEndMetadataKey]))
		if err != nil || end.Before(row.last) {
			return row.id
		}
	}
	return ""
}

func (authority *productionAmbientReplayAuthority) cursorDigests(roomID string) map[string]string {
	result := map[string]string{}
	for _, pair := range []struct {
		stage string
		agent ambientAgentConfig
	}{
		{"brain", meetingBrainAgent()}, {"decision", decisionLedgerAgent()}, {"mission", missionIntelligenceAgent()},
		{"narrative", narrativeMaintainerAgent()}, {"meeting_digest", meetingDigestAgent()}, {"day_fold", dayDigestAgent()},
		{"entity_ledger", entityLedgerAgent()}, {"company_digest", companyDigestAgent()},
	} {
		checkpoint, ok, err := authority.app.ambientScopeCheckpoint(ambientAgentScopeKey(pair.agent, roomID))
		if err != nil {
			result[pair.stage] = digestBrainString("checkpoint-unavailable")
			continue
		}
		if !ok {
			checkpoint = ambientHeldWindow{Agent: pair.agent.name, RoomID: pair.agent.scopeRoomID(roomID), InputKind: pair.agent.inputKind, ArtifactKind: pair.agent.artifactKind, CursorMetadataKey: pair.agent.cursorMetadataKey}
		}
		digest, _ := digestAmbientReplayValue(checkpoint)
		result[pair.stage] = digest
	}
	return result
}

func (authority *productionAmbientReplayAuthority) Revalidate(ctx context.Context, manifest AmbientReplayManifest) error {
	if authority == nil || manifest.Schema != ambientReplaySchema || manifest.Digest == "" {
		return ErrAmbientReplayInvalid
	}
	fresh, err := authority.Plan(ctx, AmbientReplayPlanRequest{IdempotencyKey: manifest.IdempotencyKey, TenantID: manifest.TenantID, RoomID: manifest.RoomID,
		SittingID: manifest.SittingID, StartAfter: manifest.StartAfter, EndAt: manifest.EndAt, AuthorizedBy: manifest.AuthorizedBy,
		ApprovalReference: manifest.ApprovalReference, RollbackFloor: manifest.RollbackFloor})
	if err != nil {
		return ErrAmbientReplayDrift
	}
	freshSources, freshErr := digestAmbientReplayValue(fresh.Sources)
	frozenSources, frozenErr := digestAmbientReplayValue(manifest.Sources)
	if freshErr != nil || frozenErr != nil || freshSources != frozenSources ||
		strings.Join(uniqueSortedStrings(fresh.ExcludedSources), "\x00") != strings.Join(uniqueSortedStrings(manifest.ExcludedSources), "\x00") ||
		fresh.PurgeGeneration != manifest.PurgeGeneration || fresh.ApprovalReference != manifest.ApprovalReference ||
		fresh.RollbackFloor != manifest.RollbackFloor || !equalStringMaps(fresh.CursorDigests, manifest.CursorDigests) ||
		fresh.ReleaseCommit != manifest.ReleaseCommit || fresh.ReleaseTreeDigest != manifest.ReleaseTreeDigest ||
		fresh.ReleaseReceiptDigest != manifest.ReleaseReceiptDigest {
		return ErrAmbientReplayDrift
	}
	release := currentReleaseIdentity()
	receiptDigest, err := digestAmbientReplayValue(release)
	if err != nil || !release.ProcessQualified || release.ReleaseCommit != manifest.ReleaseCommit || release.GitTreeDigest != manifest.ReleaseTreeDigest || receiptDigest != manifest.ReleaseReceiptDigest {
		return ErrAmbientReplayDrift
	}
	purge := &PostgresPurgeGenerationResolver{pool: authority.runtime.postgres.pool}
	generation, err := purge.CurrentPurgeGeneration(ctx, manifest.TenantID)
	if err != nil || generation != manifest.PurgeGeneration {
		return ErrAmbientReplayDrift
	}
	if cursor := authority.cursorDigests(manifest.RoomID); !equalStringMaps(cursor, manifest.CursorDigests) {
		return ErrAmbientReplayDrift
	}
	for _, source := range manifest.Sources {
		entry, found := authority.app.memory.entryByID(source.ObjectID)
		if !found || entry.Kind != meetingMemoryKindTranscript || normalizeRoomID(entry.Metadata["roomId"]) != manifest.RoomID || strings.TrimSpace(entry.Metadata["meetingId"]) != manifest.SittingID || digestBrainString(entry.Text) != source.ContentDigest {
			return ErrAmbientReplayDrift
		}
		object, err := authority.runtime.postgres.ResolveACLObject(ctx, ACLObjectRef{TenantID: manifest.TenantID, Type: "memory", ID: source.ObjectID, ACLVersion: source.ACLVersion})
		if err != nil || object.Deleted || object.CurrentContentRevision != source.ContentRevision || object.CurrentContentDigest != source.ContentDigest || object.Ref.ACLVersion != source.ACLVersion {
			return ErrAmbientReplayDrift
		}
		decision := (AuthorizationKernel{Store: authority.runtime.postgres}).Authorize(ctx, ACLPrincipal{TenantID: manifest.TenantID, ID: manifest.AuthorizedBy, Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}}, ACLReadContent, object.Ref, ACLRevisionRef{ContentRevision: source.ContentRevision, ContentDigest: source.ContentDigest})
		if !decision.Allowed {
			return ErrAmbientReplayDrift
		}
		fences, err := (appBrainSourceConsentVerifier{App: authority.app}).AuthorizeBrainSourceConsent(ctx, entry)
		if err != nil {
			return ErrAmbientReplayDrift
		}
		digest, err := ambientReplayConsentFenceDigest(fences, entry)
		if err != nil || digest != source.ConsentFenceDigest {
			return ErrAmbientReplayDrift
		}
	}
	return nil
}

func ambientReplayConsentFenceDigest(fences []ConsentFence, entry meetingMemoryEntry) (string, error) {
	rows := make([]map[string]any, 0, len(fences)+1)
	for _, fence := range fences {
		rows = append(rows, map[string]any{"binding": consentBindingKey(fence.binding), "lane": fence.lane, "policy": fence.policy, "generation": fence.generation, "records": fence.recordDigest})
	}
	if len(rows) == 0 {
		rows = append(rows, map[string]any{"typedSource": strings.TrimSpace(entry.Metadata["source"]), "objectId": entry.ID})
	}
	sort.Slice(rows, func(i, j int) bool {
		return asString(rows[i]["binding"])+asString(rows[i]["objectId"]) < asString(rows[j]["binding"])+asString(rows[j]["objectId"])
	})
	return digestAmbientReplayValue(rows)
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
