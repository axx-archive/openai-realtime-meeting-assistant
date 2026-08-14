package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	workstreamAffinityVersion           = 2
	workstreamAffinityMetadataKey       = "workstreamAffinity"
	workstreamAffinityResolver          = "exact_authorized_project_thread"
	workstreamAffinityExactBasis        = "exact_project_title"
	workstreamAffinitySourceBasis       = "source_project_thread"
	workstreamAffinityThreadBasis       = "current_thread_continuity"
	workstreamAffinityMeetingClaimBasis = "meeting_claim_work_project"
	workstreamAffinityCorrectionBasis   = "human_work_result_correction"
	workstreamAffinityMaxDepth          = 8
)

func validWorkstreamAffinityBasis(basis string, confidence float64) bool {
	switch basis {
	case workstreamAffinityExactBasis:
		return confidence == .96
	case workstreamAffinitySourceBasis:
		return confidence == .99
	case workstreamAffinityThreadBasis:
		return confidence == .92
	case workstreamAffinityMeetingClaimBasis:
		return confidence == .94
	case workstreamAffinityCorrectionBasis:
		return confidence == 1
	default:
		return false
	}
}

// workstreamAffinityBinding is a body-free, server-owned explanation of why a
// resulting Work item belongs with one existing project thread. It is
// minted only after the person asks STRIDE to start work; chat composition
// never selects or sends this authority. An ambiguous or absent match produces
// no binding rather than a guess.
type workstreamAffinityBinding struct {
	Version                     int       `json:"version"`
	TenantID                    string    `json:"tenantId"`
	RequestedBy                 string    `json:"requestedBy"`
	SourceThreadID              string    `json:"sourceThreadId"`
	SourceMessageID             string    `json:"sourceMessageId"`
	SourceMessageDigest         string    `json:"sourceMessageDigest"`
	SourceWindowDigest          string    `json:"sourceWindowDigest"`
	SourceMeetingID             string    `json:"sourceMeetingId,omitempty"`
	SourceMeetingRevision       string    `json:"sourceMeetingRevision,omitempty"`
	SourceClaimSegmentID        string    `json:"sourceClaimSegmentId,omitempty"`
	SourceClaimRevision         string    `json:"sourceClaimRevision,omitempty"`
	ProjectThreadID             string    `json:"projectThreadId"`
	ProjectTitle                string    `json:"projectTitle"`
	ProjectACLVersion           int64     `json:"projectAclVersion"`
	ProjectAudienceDigest       string    `json:"projectAudienceDigest"`
	SupportArtifactID           string    `json:"supportArtifactId,omitempty"`
	SupportAffinityDigest       string    `json:"supportAffinityDigest,omitempty"`
	SupportArtifactHeaderDigest string    `json:"supportArtifactHeaderDigest,omitempty"`
	CorrectionReceiptDigest     string    `json:"correctionReceiptDigest,omitempty"`
	Basis                       string    `json:"basis"`
	Confidence                  float64   `json:"confidence"`
	Resolver                    string    `json:"resolver"`
	EvaluatedAt                 time.Time `json:"evaluatedAt"`
	Digest                      string    `json:"digest"`
}

func (binding workstreamAffinityBinding) material() any {
	return struct {
		Version                     int       `json:"version"`
		TenantID                    string    `json:"tenantId"`
		RequestedBy                 string    `json:"requestedBy"`
		SourceThreadID              string    `json:"sourceThreadId"`
		SourceMessageID             string    `json:"sourceMessageId"`
		SourceMessageDigest         string    `json:"sourceMessageDigest"`
		SourceWindowDigest          string    `json:"sourceWindowDigest"`
		SourceMeetingID             string    `json:"sourceMeetingId,omitempty"`
		SourceMeetingRevision       string    `json:"sourceMeetingRevision,omitempty"`
		SourceClaimSegmentID        string    `json:"sourceClaimSegmentId,omitempty"`
		SourceClaimRevision         string    `json:"sourceClaimRevision,omitempty"`
		ProjectThreadID             string    `json:"projectThreadId"`
		ProjectTitle                string    `json:"projectTitle"`
		ProjectACLVersion           int64     `json:"projectAclVersion"`
		ProjectAudienceDigest       string    `json:"projectAudienceDigest"`
		SupportArtifactID           string    `json:"supportArtifactId,omitempty"`
		SupportAffinityDigest       string    `json:"supportAffinityDigest,omitempty"`
		SupportArtifactHeaderDigest string    `json:"supportArtifactHeaderDigest,omitempty"`
		CorrectionReceiptDigest     string    `json:"correctionReceiptDigest,omitempty"`
		Basis                       string    `json:"basis"`
		Confidence                  float64   `json:"confidence"`
		Resolver                    string    `json:"resolver"`
		EvaluatedAt                 time.Time `json:"evaluatedAt"`
	}{
		binding.Version, binding.TenantID, binding.RequestedBy, binding.SourceThreadID,
		binding.SourceMessageID, binding.SourceMessageDigest, binding.SourceWindowDigest,
		binding.SourceMeetingID, binding.SourceMeetingRevision,
		binding.SourceClaimSegmentID, binding.SourceClaimRevision,
		binding.ProjectThreadID, binding.ProjectTitle, binding.ProjectACLVersion,
		binding.ProjectAudienceDigest, binding.SupportArtifactID, binding.SupportAffinityDigest, binding.SupportArtifactHeaderDigest, binding.CorrectionReceiptDigest,
		binding.Basis, binding.Confidence, binding.Resolver,
		binding.EvaluatedAt,
	}
}

func (binding workstreamAffinityBinding) validate() error {
	if binding.Version != workstreamAffinityVersion || binding.TenantID != canonicalTenantID() ||
		normalizeAccountEmail(binding.RequestedBy) == "" || !strideIdentifier(binding.SourceThreadID) ||
		!strideIdentifier(binding.SourceMessageID) || !isHexDigest(binding.SourceMessageDigest) ||
		!isHexDigest(binding.SourceWindowDigest) || !strideIdentifier(binding.ProjectThreadID) ||
		!stridePlainText(binding.ProjectTitle, 120, true) || binding.ProjectACLVersion < 1 ||
		!isHexDigest(binding.ProjectAudienceDigest) || !validWorkstreamAffinityBasis(binding.Basis, binding.Confidence) ||
		binding.Resolver != workstreamAffinityResolver ||
		binding.EvaluatedAt.IsZero() || !isHexDigest(binding.Digest) {
		return fmt.Errorf("workstream affinity is invalid")
	}
	if binding.Basis == workstreamAffinityThreadBasis {
		if !strideIdentifier(binding.SupportArtifactID) || !isHexDigest(binding.SupportAffinityDigest) {
			return fmt.Errorf("workstream affinity support is invalid")
		}
		if binding.SupportArtifactHeaderDigest != "" || binding.SourceClaimSegmentID != "" || binding.SourceClaimRevision != "" || binding.CorrectionReceiptDigest != "" {
			return fmt.Errorf("workstream affinity has unexpected support")
		}
	} else if binding.Basis == workstreamAffinityMeetingClaimBasis {
		if !strideIdentifier(binding.SourceMeetingID) || !isHexDigest(binding.SourceMeetingRevision) ||
			!strideIdentifier(binding.SourceClaimSegmentID) || !isHexDigest(binding.SourceClaimRevision) ||
			!strideIdentifier(binding.SupportArtifactID) || binding.SupportAffinityDigest != "" || !isHexDigest(binding.SupportArtifactHeaderDigest) {
			return fmt.Errorf("workstream affinity Meeting claim support is invalid")
		}
		if binding.CorrectionReceiptDigest != "" {
			return fmt.Errorf("workstream affinity has unexpected correction")
		}
	} else if binding.Basis == workstreamAffinityCorrectionBasis {
		if !isHexDigest(binding.CorrectionReceiptDigest) || binding.SupportArtifactID != "" || binding.SupportAffinityDigest != "" ||
			binding.SupportArtifactHeaderDigest != "" || binding.SourceClaimSegmentID != "" || binding.SourceClaimRevision != "" {
			return fmt.Errorf("workstream affinity correction is invalid")
		}
	} else if binding.SupportArtifactID != "" || binding.SupportAffinityDigest != "" || binding.SupportArtifactHeaderDigest != "" ||
		binding.SourceClaimSegmentID != "" || binding.SourceClaimRevision != "" || binding.CorrectionReceiptDigest != "" {
		return fmt.Errorf("workstream affinity has unexpected support")
	}
	if (binding.SourceMeetingID == "") != (binding.SourceMeetingRevision == "") ||
		(binding.SourceMeetingID != "" && (!strideIdentifier(binding.SourceMeetingID) || !isHexDigest(binding.SourceMeetingRevision))) {
		return fmt.Errorf("workstream affinity Meeting source is invalid")
	}
	digest, err := STRIDEContractDigest(binding.material())
	if err != nil || digest != binding.Digest {
		return fmt.Errorf("workstream affinity digest is invalid")
	}
	return nil
}

func workstreamArtifactHeaderDigest(header ArtifactAuthorizationHeader) (string, bool) {
	digest, err := STRIDEContractDigest(header)
	return digest, err == nil && isHexDigest(digest)
}

type meetingClaimWorkstreamCandidate struct {
	project             scoutChatThreadRecord
	segmentID           string
	segmentRevision     string
	supportArtifactID   string
	supportHeaderDigest string
}

// currentMeetingClaimWorkstreamCandidate is the one-time migration bridge
// from a legacy Meeting claim/card link into the successor Work authority. A
// card may locate a candidate, but the returned receipt contains no card id and
// currentness never consults Board state. The bridge accepts only a current
// grounded commitment, a delivered and currently authorized artifact whose
// immutable chat origin is the candidate Project, and exactly one Project.
func (app *kanbanBoardApp) currentMeetingClaimWorkstreamCandidate(ctx context.Context, user *userAccount, source scoutChatThreadRecord) (meetingClaimWorkstreamCandidate, bool) {
	if app == nil || app.memory == nil || user == nil || source.MeetingRecord == nil ||
		scoutChatThreadVisibility(source) != scoutChatVisibilityPrivate {
		return meetingClaimWorkstreamCandidate{}, false
	}
	projection, found := app.meetingRecordProjectionForPrincipal(ctx, recallPrincipalForUser(user), source.MeetingRecord.MeetingID)
	if !found || projection.index.RecordRevision != source.MeetingRecord.RecordRevision || projection.legacyDetail == nil {
		return meetingClaimWorkstreamCandidate{}, false
	}
	actionAnchors := map[string]struct{}{}
	for _, action := range projection.payload.ActionItems {
		if _, grounded := projection.groundedSource(action.Anchor); grounded && strings.TrimSpace(action.A) != "" {
			actionAnchors[strings.TrimSpace(action.Anchor)] = struct{}{}
		}
	}
	if len(actionAnchors) == 0 {
		return meetingClaimWorkstreamCandidate{}, false
	}
	rows := map[string]boardCardViewerProjection{}
	for _, row := range app.boardProjectionForViewer(ctx, user).Cards {
		rows[row.CardID] = row
	}
	byProject := map[string]meetingClaimWorkstreamCandidate{}
	for segmentID, cardIDs := range projection.legacyDetail.ClaimCardIDs {
		segmentID = strings.TrimSpace(segmentID)
		segment, segmentFound := projection.segmentByID[segmentID]
		if _, action := actionAnchors[segmentID]; !action || !segmentFound || segment.Revision == "" {
			continue
		}
		for _, cardID := range cardIDs {
			row, rowFound := rows[strings.TrimSpace(cardID)]
			if !rowFound || row.ProjectResolution != "linked" || row.ProjectID == "needs-project" || row.ArtifactID == "" {
				continue
			}
			header, headerFound := app.memory.artifactAuthorizationHeaderByID(row.ArtifactID)
			if !headerFound || !artifactHeaderAuthorized(ctx, user, ACLReadContent, header) {
				continue
			}
			artifact, exact := app.memory.artifactSnapshotIfHeaderMatches(row.ArtifactID, header)
			if !exact || !boardArtifactDelivered(artifact) || strings.TrimSpace(artifact.Metadata["boardCardId"]) != strings.TrimSpace(cardID) ||
				strings.TrimSpace(artifact.Metadata["originKind"]) != agentThreadOriginChannel || strings.TrimSpace(artifact.Metadata["originId"]) != row.ProjectID ||
				strings.TrimSpace(header.OriginSurface) != "chat:"+row.ProjectID {
				continue
			}
			project, _, projectErr := app.scoutChatThreadByID(user.Email, row.ProjectID)
			if projectErr != nil || project.ArchivedAt != "" || !strideProductProjectDestinationEligible(project) ||
				!scoutChatThreadAllowsViewer(project, user.Email) || strings.TrimSpace(project.Title) != strings.TrimSpace(row.ProjectTitle) {
				continue
			}
			headerDigest, digestOK := workstreamArtifactHeaderDigest(header)
			if !digestOK {
				continue
			}
			candidate := meetingClaimWorkstreamCandidate{project: project, segmentID: segmentID, segmentRevision: segment.Revision,
				supportArtifactID: artifact.ID, supportHeaderDigest: headerDigest}
			prior, exists := byProject[project.ID]
			if !exists || candidate.segmentID+"\x00"+candidate.supportArtifactID < prior.segmentID+"\x00"+prior.supportArtifactID {
				byProject[project.ID] = candidate
			}
		}
	}
	if len(byProject) != 1 {
		return meetingClaimWorkstreamCandidate{}, false
	}
	for _, candidate := range byProject {
		return candidate, true
	}
	return meetingClaimWorkstreamCandidate{}, false
}

func workstreamAffinityWindowContainsArtifact(window []scoutChatMessageRecord, artifactID string) bool {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return false
	}
	for _, message := range window {
		if message.Thread != nil && strings.TrimSpace(message.Thread.ArtifactID) == artifactID {
			return true
		}
	}
	return false
}

func (app *kanbanBoardApp) currentWorkstreamSupport(ctx context.Context, user *userAccount, artifact meetingMemoryEntry, depth int) (string, string, string, bool) {
	if app == nil || user == nil || depth > workstreamAffinityMaxDepth || normalizeAccountEmail(artifact.Metadata["requestedBy"]) != normalizeAccountEmail(user.Email) {
		return "", "", "", false
	}
	if affinity, ok := decodeWorkstreamAffinity(artifact.Metadata); ok {
		if !app.workstreamAffinityCurrentAtDepth(ctx, artifact, depth+1) {
			return "", "", "", false
		}
		return affinity.ProjectThreadID, affinity.ProjectTitle, affinity.Digest, true
	}
	if binding, ok := decodeProjectWorkBinding(artifact.Metadata); ok && app.projectBoundArtifactCurrent(ctx, artifact) {
		digest := sha256Hex([]byte(strings.TrimSpace(artifact.Metadata[projectWorkBindingMetadataKey])))
		return binding.ProjectID, binding.ProjectTitle, digest, true
	}
	return "", "", "", false
}

func (app *kanbanBoardApp) currentThreadWorkstreamCandidate(ctx context.Context, user *userAccount, source scoutChatThreadRecord, sourceMessageID string, depth int) (scoutChatThreadRecord, string, string, bool, bool) {
	if app == nil || app.memory == nil || user == nil || scoutChatThreadVisibility(source) != scoutChatVisibilityPrivate || depth > workstreamAffinityMaxDepth {
		return scoutChatThreadRecord{}, "", "", false, false
	}
	window, _, err := scoutChatSourceWindow(source, sourceMessageID)
	if err != nil {
		return scoutChatThreadRecord{}, "", "", false, false
	}
	type candidate struct {
		project  scoutChatThreadRecord
		artifact string
		digest   string
	}
	byProject := map[string]candidate{}
	start := 0
	explicitNone := false
	// A result-side correction is the newest explicit interpretation in the
	// bounded conversation. It supersedes older inferred support but not Work
	// accepted after it. "No project" is therefore a barrier, not an empty old
	// artifact that lets earlier evidence leak back in.
	for index := len(window) - 1; index >= 0; index-- {
		message := window[index]
		if message.Thread == nil || strings.TrimSpace(message.Thread.ArtifactID) == "" {
			continue
		}
		artifact, found := app.memory.entryByID(message.Thread.ArtifactID)
		if !found {
			continue
		}
		correction, corrected := decodeWorkstreamAffinityCorrection(artifact.Metadata)
		if !corrected || !app.workstreamAffinityCorrectionCurrent(ctx, artifact, correction) {
			continue
		}
		start = index
		if correction.TargetKind == "none" {
			start = index + 1
			explicitNone = true
		}
		break
	}
	for _, message := range window[start:] {
		if message.Thread == nil || strings.TrimSpace(message.Thread.ArtifactID) == "" {
			continue
		}
		artifact, found := app.memory.entryByID(message.Thread.ArtifactID)
		if !found {
			continue
		}
		projectID, projectTitle, supportDigest, current := app.currentWorkstreamSupport(ctx, user, artifact, depth)
		if !current {
			continue
		}
		project, _, projectErr := app.scoutChatThreadByID(user.Email, projectID)
		if projectErr != nil || !strideProductProjectDestinationEligible(project) || !scoutChatThreadAllowsViewer(project, user.Email) || strings.TrimSpace(project.Title) != projectTitle {
			continue
		}
		prior, exists := byProject[project.ID]
		if !exists || artifact.ID < prior.artifact {
			byProject[project.ID] = candidate{project: project, artifact: artifact.ID, digest: supportDigest}
		}
	}
	if len(byProject) != 1 {
		return scoutChatThreadRecord{}, "", "", false, explicitNone && len(byProject) == 0
	}
	keys := make([]string, 0, 1)
	for key := range byProject {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	matched := byProject[keys[0]]
	return matched.project, matched.artifact, matched.digest, true, false
}

func encodeWorkstreamAffinity(binding workstreamAffinityBinding) (string, error) {
	if err := binding.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(binding)
	return string(raw), err
}

func decodeWorkstreamAffinity(metadata map[string]string) (workstreamAffinityBinding, bool) {
	var binding workstreamAffinityBinding
	raw := strings.TrimSpace(metadata[workstreamAffinityMetadataKey])
	if raw == "" {
		return binding, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || ensureJSONEOF(decoder) != nil || binding.validate() != nil {
		return workstreamAffinityBinding{}, false
	}
	return binding, true
}

// resolveWorkstreamAffinity deliberately uses a smaller contract than Work
// delivery routing. A private artifact may bind to one exact currently-readable
// project named by the objective because the binding remains visible only to
// its owner. Public work may bind only to its own exact channel: requester-only
// readability is not proof that every channel viewer may learn about a second
// Project. Ambiguous, absent, stale, or cross-audience matches abstain.
func (app *kanbanBoardApp) resolveWorkstreamAffinity(user *userAccount, source scoutChatThreadRecord, message scoutChatMessageRecord, objective string, now time.Time) (workstreamAffinityBinding, bool) {
	return app.resolveWorkstreamAffinityWithContext(context.Background(), user, source, message, objective, now)
}

func (app *kanbanBoardApp) resolveWorkstreamAffinityWithContext(ctx context.Context, user *userAccount, source scoutChatThreadRecord, message scoutChatMessageRecord, objective string, now time.Time) (workstreamAffinityBinding, bool) {
	if app == nil || user == nil || source.ID == "" || message.ID == "" || strings.TrimSpace(objective) == "" || now.IsZero() {
		return workstreamAffinityBinding{}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	currentSource, _, err := app.scoutChatThreadByID(user.Email, source.ID)
	if err != nil || currentSource.ArchivedAt != "" {
		return workstreamAffinityBinding{}, false
	}
	_, sourceBinding, err := scoutChatSourceWindow(currentSource, message.ID)
	if err != nil {
		return workstreamAffinityBinding{}, false
	}
	visibility := scoutChatThreadVisibility(currentSource)
	var matches []scoutChatThreadRecord
	basis, confidence := workstreamAffinityExactBasis, .96
	supportArtifactID, supportAffinityDigest := "", ""
	supportArtifactHeaderDigest, sourceClaimSegmentID, sourceClaimRevision := "", "", ""
	if visibility == scoutChatVisibilityPublic {
		// A public Project channel is already the exact workstream boundary. The
		// person should not have to repeat the channel title in every request, and
		// a different readable Project can never be inferred across audiences.
		if !strideProductProjectDestinationEligible(currentSource) {
			return workstreamAffinityBinding{}, false
		}
		matches = append(matches, currentSource)
		basis, confidence = workstreamAffinitySourceBasis, .99
	} else {
		for _, candidate := range app.scoutChatThreadsSnapshot(user.Email, false, 0) {
			if !strideProductProjectDestinationEligible(candidate) || candidate.ArchivedAt != "" ||
				!strideProductOutcomeNamesProject(objective, candidate.Title) {
				continue
			}
			matches = append(matches, candidate)
		}
		if len(matches) == 0 {
			if candidate, artifactID, supportDigest, found, explicitNone := app.currentThreadWorkstreamCandidate(ctx, user, currentSource, message.ID, 0); found {
				matches = append(matches, candidate)
				basis, confidence = workstreamAffinityThreadBasis, .92
				supportArtifactID, supportAffinityDigest = artifactID, supportDigest
			} else if !explicitNone {
				if claim, claimFound := app.currentMeetingClaimWorkstreamCandidate(ctx, user, currentSource); claimFound {
					matches = append(matches, claim.project)
					basis, confidence = workstreamAffinityMeetingClaimBasis, .94
					supportArtifactID, supportArtifactHeaderDigest = claim.supportArtifactID, claim.supportHeaderDigest
					sourceClaimSegmentID, sourceClaimRevision = claim.segmentID, claim.segmentRevision
				}
			}
		}
	}
	if len(matches) != 1 {
		return workstreamAffinityBinding{}, false
	}
	project := matches[0]
	audience, aclVersion, err := strideProductProjectDestinationAuthority(project)
	if err != nil || !scoutChatThreadAllowsViewer(project, user.Email) {
		return workstreamAffinityBinding{}, false
	}
	audienceDigest, err := STRIDEContractDigest(audience)
	if err != nil {
		return workstreamAffinityBinding{}, false
	}
	sourceMeetingID, sourceMeetingRevision := "", ""
	if currentSource.MeetingRecord != nil {
		sourceMeetingID = strings.TrimSpace(currentSource.MeetingRecord.MeetingID)
		sourceMeetingRevision = strings.TrimSpace(currentSource.MeetingRecord.RecordRevision)
	}
	binding := workstreamAffinityBinding{
		Version: workstreamAffinityVersion, TenantID: canonicalTenantID(), RequestedBy: normalizeAccountEmail(user.Email),
		SourceThreadID: currentSource.ID, SourceMessageID: message.ID, SourceMessageDigest: sourceBinding.MessageDigest,
		SourceWindowDigest: sourceBinding.WindowDigest, SourceMeetingID: sourceMeetingID, SourceMeetingRevision: sourceMeetingRevision,
		SourceClaimSegmentID: sourceClaimSegmentID, SourceClaimRevision: sourceClaimRevision,
		ProjectThreadID: project.ID, ProjectTitle: strings.TrimSpace(project.Title),
		ProjectACLVersion: aclVersion, ProjectAudienceDigest: audienceDigest,
		SupportArtifactID: supportArtifactID, SupportAffinityDigest: supportAffinityDigest, SupportArtifactHeaderDigest: supportArtifactHeaderDigest,
		Basis: basis, Confidence: confidence,
		Resolver: workstreamAffinityResolver, EvaluatedAt: now.UTC(),
	}
	binding.Digest, err = STRIDEContractDigest(binding.material())
	if err != nil || binding.validate() != nil {
		return workstreamAffinityBinding{}, false
	}
	return binding, true
}

func (app *kanbanBoardApp) workstreamAffinityCurrent(ctx context.Context, artifact meetingMemoryEntry) bool {
	return app.workstreamAffinityCurrentAtDepth(ctx, artifact, 0)
}

func (app *kanbanBoardApp) workstreamAffinityCurrentAtDepth(ctx context.Context, artifact meetingMemoryEntry, depth int) bool {
	binding, ok := decodeWorkstreamAffinity(artifact.Metadata)
	if !ok || app == nil || depth > workstreamAffinityMaxDepth || normalizeAccountEmail(artifact.Metadata["requestedBy"]) != binding.RequestedBy ||
		strings.TrimSpace(artifact.Metadata["originId"]) != binding.SourceThreadID ||
		strings.TrimSpace(artifact.Metadata["sourceMessageId"]) != binding.SourceMessageID ||
		strings.TrimSpace(artifact.Metadata["sourceMessageDigest"]) != binding.SourceMessageDigest ||
		strings.TrimSpace(artifact.Metadata["sourceWindowDigest"]) != binding.SourceWindowDigest ||
		strings.TrimSpace(artifact.Metadata["projectWorkId"]) != binding.ProjectThreadID ||
		strings.TrimSpace(artifact.Metadata["projectWorkTitle"]) != binding.ProjectTitle {
		return false
	}
	source, _, sourceErr := app.scoutChatThreadByID(binding.RequestedBy, binding.SourceThreadID)
	if sourceErr != nil || source.ArchivedAt != "" || !scoutChatThreadAllowsViewer(source, binding.RequestedBy) ||
		(scoutChatThreadVisibility(source) == scoutChatVisibilityPublic && source.ID != binding.ProjectThreadID) ||
		(source.MeetingRecord != nil && !app.meetingRecordConversationBindingCurrent(binding.RequestedBy, source)) {
		return false
	}
	if source.MeetingRecord == nil {
		if binding.SourceMeetingID != "" || binding.SourceMeetingRevision != "" {
			return false
		}
	} else if strings.TrimSpace(source.MeetingRecord.MeetingID) != binding.SourceMeetingID ||
		strings.TrimSpace(source.MeetingRecord.RecordRevision) != binding.SourceMeetingRevision {
		return false
	}
	window, currentSource, sourceErr := scoutChatSourceWindow(source, binding.SourceMessageID)
	if sourceErr != nil || currentSource.MessageDigest != binding.SourceMessageDigest || currentSource.WindowDigest != binding.SourceWindowDigest {
		return false
	}
	project, _, err := app.scoutChatThreadByID(binding.RequestedBy, binding.ProjectThreadID)
	if err != nil || project.ArchivedAt != "" || !strideProductProjectDestinationEligible(project) ||
		strings.TrimSpace(project.Title) != binding.ProjectTitle || !scoutChatThreadAllowsViewer(project, binding.RequestedBy) {
		return false
	}
	if binding.Basis == workstreamAffinitySourceBasis && (scoutChatThreadVisibility(source) != scoutChatVisibilityPublic || source.ID != project.ID) {
		return false
	}
	if binding.Basis == workstreamAffinityThreadBasis {
		if !workstreamAffinityWindowContainsArtifact(window, binding.SupportArtifactID) {
			return false
		}
		support, found := app.memory.entryByID(binding.SupportArtifactID)
		if !found {
			return false
		}
		projectID, projectTitle, supportDigest, current := app.currentWorkstreamSupport(ctx, accountStore().findUser(binding.RequestedBy), support, depth)
		if !current || projectID != binding.ProjectThreadID || projectTitle != binding.ProjectTitle || supportDigest != binding.SupportAffinityDigest {
			return false
		}
	}
	if binding.Basis == workstreamAffinityMeetingClaimBasis {
		if source.MeetingRecord == nil || source.MeetingRecord.MeetingID != binding.SourceMeetingID || source.MeetingRecord.RecordRevision != binding.SourceMeetingRevision {
			return false
		}
		requester := accountStore().findUser(binding.RequestedBy)
		if requester == nil {
			return false
		}
		projection, found := app.meetingRecordProjectionForPrincipal(ctx, recallPrincipalForUser(requester), binding.SourceMeetingID)
		if !found || projection.index.RecordRevision != binding.SourceMeetingRevision {
			return false
		}
		segment, found := projection.segmentByID[binding.SourceClaimSegmentID]
		if !found || segment.Revision != binding.SourceClaimRevision {
			return false
		}
		actionCurrent := false
		for _, action := range projection.payload.ActionItems {
			if strings.TrimSpace(action.Anchor) == binding.SourceClaimSegmentID && strings.TrimSpace(action.A) != "" {
				_, actionCurrent = projection.groundedSource(action.Anchor)
				break
			}
		}
		if !actionCurrent {
			return false
		}
		header, found := app.memory.artifactAuthorizationHeaderByID(binding.SupportArtifactID)
		if !found || !artifactHeaderAuthorized(ctx, requester, ACLReadContent, header) || strings.TrimSpace(header.OriginSurface) != "chat:"+binding.ProjectThreadID {
			return false
		}
		headerDigest, digestOK := workstreamArtifactHeaderDigest(header)
		if !digestOK || headerDigest != binding.SupportArtifactHeaderDigest {
			return false
		}
		support, exact := app.memory.artifactSnapshotIfHeaderMatches(binding.SupportArtifactID, header)
		if !exact || !boardArtifactDelivered(support) || strings.TrimSpace(support.Metadata["originKind"]) != agentThreadOriginChannel ||
			strings.TrimSpace(support.Metadata["originId"]) != binding.ProjectThreadID {
			return false
		}
	}
	if binding.Basis == workstreamAffinityCorrectionBasis {
		receipt, receiptOK := decodeWorkstreamAffinityCorrection(artifact.Metadata)
		if !receiptOK || receipt.Digest != binding.CorrectionReceiptDigest || receipt.ArtifactID != artifact.ID ||
			receipt.TargetKind != "project" || receipt.ProjectThreadID != binding.ProjectThreadID || receipt.ProjectTitle != binding.ProjectTitle ||
			receipt.RequestedBy != binding.RequestedBy || receipt.SourceThreadID != binding.SourceThreadID || receipt.SourceMessageID != binding.SourceMessageID ||
			receipt.SourceMessageDigest != binding.SourceMessageDigest || receipt.SourceWindowDigest != binding.SourceWindowDigest {
			return false
		}
	}
	audience, aclVersion, err := strideProductProjectDestinationAuthority(project)
	if err != nil || aclVersion != binding.ProjectACLVersion {
		return false
	}
	audienceDigest, err := STRIDEContractDigest(audience)
	return err == nil && audienceDigest == binding.ProjectAudienceDigest
}
