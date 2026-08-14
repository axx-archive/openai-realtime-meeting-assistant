package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	workstreamAffinityCorrectionVersion            = 1
	workstreamAffinityCorrectionMetadataKey        = "workstreamAffinityCorrection"
	workstreamAffinityCorrectionHistoryMetadataKey = "workstreamAffinityCorrectionHistory"
	workstreamAffinityCorrectionTokenVersion       = 1
	workstreamAffinityCorrectionTokenTTL           = 15 * time.Minute
)

var errWorkstreamAffinityConflict = errors.New("workstream affinity changed elsewhere")

type workstreamAffinityCorrectionToken struct {
	Version                  int                         `json:"version"`
	Purpose                  string                      `json:"purpose"`
	ArtifactID               string                      `json:"artifactId"`
	ArtifactContentRevision  int64                       `json:"artifactContentRevision"`
	ArtifactContentDigest    string                      `json:"artifactContentDigest"`
	ExpectedAffinityDigest   string                      `json:"expectedAffinityDigest"`
	ExpectedCorrectionDigest string                      `json:"expectedCorrectionDigest"`
	PersonID                 string                      `json:"personId"`
	OrganizationID           string                      `json:"organizationId"`
	MembershipID             string                      `json:"membershipId"`
	MembershipRevision       int64                       `json:"membershipRevision"`
	SessionSubjectDigest     string                      `json:"sessionSubjectDigest"`
	SessionRevision          int64                       `json:"sessionRevision"`
	AuthorityGeneration      uint64                      `json:"authorityGeneration"`
	Target                   projectChatCorrectionTarget `json:"target"`
	IssuedAt                 time.Time                   `json:"issuedAt"`
	ExpiresAt                time.Time                   `json:"expiresAt"`
	KeyID                    string                      `json:"keyId"`
	KeyVersion               uint64                      `json:"keyVersion"`
}

type workstreamAffinityCorrectionChoice struct {
	Title string `json:"title"`
	Token string `json:"token"`
}

type workstreamAffinityCorrectionCurrent struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

type workstreamAffinityCorrectionPreview struct {
	Available bool                                 `json:"available"`
	ScopeKey  string                               `json:"scopeKey,omitempty"`
	Current   workstreamAffinityCorrectionCurrent  `json:"current"`
	Choices   []workstreamAffinityCorrectionChoice `json:"choices,omitempty"`
	Remove    *workstreamAffinityCorrectionChoice  `json:"remove,omitempty"`
}

func workstreamAffinityCorrectionTokenMAC(key StrideE10TenantAuthorityEnvelopeKey, raw []byte) []byte {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte("workstream-affinity-correction/v1\x00"))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func mintWorkstreamAffinityCorrectionToken(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, artifact meetingMemoryEntry, header ArtifactAuthorizationHeader, target projectChatCorrectionTarget) (string, error) {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if runtime == nil || runtime.keys == nil {
		return "", errHomeProjectUnavailable
	}
	key, err := runtime.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(ctx)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) {
		return "", errHomeProjectUnavailable
	}
	now := time.Now().UTC()
	token := workstreamAffinityCorrectionToken{
		Version: workstreamAffinityCorrectionTokenVersion, Purpose: "work_result_project_correction",
		ArtifactID: artifact.ID, ArtifactContentRevision: header.ContentRevision, ArtifactContentDigest: header.ContentDigest,
		ExpectedAffinityDigest:   sha256Hex([]byte(strings.TrimSpace(artifact.Metadata[workstreamAffinityMetadataKey]))),
		ExpectedCorrectionDigest: sha256Hex([]byte(strings.TrimSpace(artifact.Metadata[workstreamAffinityCorrectionMetadataKey]))),
		PersonID:                 snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID,
		MembershipID: snapshot.Membership.Header.ID, MembershipRevision: snapshot.Membership.Header.Revision,
		SessionSubjectDigest: snapshot.SessionHash, SessionRevision: snapshot.ActiveSession.SessionRevision,
		AuthorityGeneration: snapshot.Generation, Target: target, IssuedAt: now, ExpiresAt: now.Add(workstreamAffinityCorrectionTokenTTL),
		KeyID: key.ID, KeyVersion: key.Version,
	}
	raw, _ := json.Marshal(token)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(workstreamAffinityCorrectionTokenMAC(key, raw)), nil
}

func resolveWorkstreamAffinityCorrectionToken(ctx context.Context, encoded string, snapshot StrideE10TenantAuthoritySnapshot) (workstreamAffinityCorrectionToken, error) {
	var token workstreamAffinityCorrectionToken
	parts := strings.Split(strings.TrimSpace(encoded), ".")
	if len(parts) != 2 {
		return token, errHomeProjectStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &token) != nil {
		return token, errHomeProjectStale
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if err != nil || runtime == nil || runtime.keys == nil {
		return token, errHomeProjectStale
	}
	key, err := runtime.keys.ResolveStrideE10TenantAuthorityEnvelopeKey(ctx, token.KeyID, token.KeyVersion)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) || !hmac.Equal(signature, workstreamAffinityCorrectionTokenMAC(key, raw)) {
		return token, errHomeProjectStale
	}
	validTarget := token.Target.Kind == "remove" && token.Target.ProjectID == "" && token.Target.ProjectRevision == 0 && token.Target.ProjectDigest == "" && token.Target.ProjectTitle == ""
	if token.Target.Kind == "project" {
		validTarget = strideIdentifier(token.Target.ProjectID) && token.Target.ProjectRevision > 0 && isHexDigest(token.Target.ProjectDigest) && stridePlainText(token.Target.ProjectTitle, 120, true)
	}
	if token.Version != workstreamAffinityCorrectionTokenVersion || token.Purpose != "work_result_project_correction" ||
		!strideIdentifier(token.ArtifactID) || token.ArtifactContentRevision < 1 || !isHexDigest(token.ArtifactContentDigest) ||
		!isHexDigest(token.ExpectedAffinityDigest) || !isHexDigest(token.ExpectedCorrectionDigest) ||
		token.PersonID != snapshot.Person.Header.ID || token.OrganizationID != snapshot.Organization.Header.ID ||
		token.MembershipID != snapshot.Membership.Header.ID || token.MembershipRevision != snapshot.Membership.Header.Revision ||
		token.SessionSubjectDigest != snapshot.SessionHash || token.SessionRevision != snapshot.ActiveSession.SessionRevision ||
		token.AuthorityGeneration != snapshot.Generation || token.IssuedAt.IsZero() || token.ExpiresAt.IsZero() ||
		!token.ExpiresAt.After(token.IssuedAt) || !time.Now().UTC().Before(token.ExpiresAt) || !validTarget {
		return token, errHomeProjectStale
	}
	return token, nil
}

// workstreamAffinityCorrectionReceipt is the append-only, body-free result-side
// review record. It never edits the source conversation and never grants
// Project authority by itself: currentness rechecks the exact source window and
// target Project before the corrected Work may seed another turn.
type workstreamAffinityCorrectionReceipt struct {
	Version                int       `json:"version"`
	TenantID               string    `json:"tenantId"`
	ArtifactID             string    `json:"artifactId"`
	RequestedBy            string    `json:"requestedBy"`
	SourceThreadID         string    `json:"sourceThreadId"`
	SourceMessageID        string    `json:"sourceMessageId"`
	SourceMessageDigest    string    `json:"sourceMessageDigest"`
	SourceWindowDigest     string    `json:"sourceWindowDigest"`
	Revision               int64     `json:"revision"`
	PreviousDigest         string    `json:"previousDigest,omitempty"`
	PreviousAffinityDigest string    `json:"previousAffinityDigest,omitempty"`
	TargetKind             string    `json:"targetKind"`
	ProjectThreadID        string    `json:"projectThreadId,omitempty"`
	ProjectTitle           string    `json:"projectTitle,omitempty"`
	ProjectACLVersion      int64     `json:"projectAclVersion,omitempty"`
	ProjectAudienceDigest  string    `json:"projectAudienceDigest,omitempty"`
	CorrectedBy            string    `json:"correctedBy"`
	OperationID            string    `json:"operationId"`
	OperationBodyDigest    string    `json:"operationBodyDigest"`
	CorrectedAt            time.Time `json:"correctedAt"`
	Digest                 string    `json:"digest"`
}

func (receipt workstreamAffinityCorrectionReceipt) material() any {
	return struct {
		Version                int       `json:"version"`
		TenantID               string    `json:"tenantId"`
		ArtifactID             string    `json:"artifactId"`
		RequestedBy            string    `json:"requestedBy"`
		SourceThreadID         string    `json:"sourceThreadId"`
		SourceMessageID        string    `json:"sourceMessageId"`
		SourceMessageDigest    string    `json:"sourceMessageDigest"`
		SourceWindowDigest     string    `json:"sourceWindowDigest"`
		Revision               int64     `json:"revision"`
		PreviousDigest         string    `json:"previousDigest,omitempty"`
		PreviousAffinityDigest string    `json:"previousAffinityDigest,omitempty"`
		TargetKind             string    `json:"targetKind"`
		ProjectThreadID        string    `json:"projectThreadId,omitempty"`
		ProjectTitle           string    `json:"projectTitle,omitempty"`
		ProjectACLVersion      int64     `json:"projectAclVersion,omitempty"`
		ProjectAudienceDigest  string    `json:"projectAudienceDigest,omitempty"`
		CorrectedBy            string    `json:"correctedBy"`
		OperationID            string    `json:"operationId"`
		OperationBodyDigest    string    `json:"operationBodyDigest"`
		CorrectedAt            time.Time `json:"correctedAt"`
	}{
		receipt.Version, receipt.TenantID, receipt.ArtifactID, receipt.RequestedBy,
		receipt.SourceThreadID, receipt.SourceMessageID, receipt.SourceMessageDigest, receipt.SourceWindowDigest,
		receipt.Revision, receipt.PreviousDigest, receipt.PreviousAffinityDigest, receipt.TargetKind,
		receipt.ProjectThreadID, receipt.ProjectTitle, receipt.ProjectACLVersion, receipt.ProjectAudienceDigest,
		receipt.CorrectedBy, receipt.OperationID, receipt.OperationBodyDigest, receipt.CorrectedAt,
	}
}

func (receipt workstreamAffinityCorrectionReceipt) validate() error {
	if receipt.Version != workstreamAffinityCorrectionVersion || receipt.TenantID != canonicalTenantID() ||
		!strideIdentifier(receipt.ArtifactID) || normalizeAccountEmail(receipt.RequestedBy) == "" ||
		!strideIdentifier(receipt.SourceThreadID) || !strideIdentifier(receipt.SourceMessageID) ||
		!isHexDigest(receipt.SourceMessageDigest) || !isHexDigest(receipt.SourceWindowDigest) || receipt.Revision < 1 ||
		(receipt.Revision == 1 && receipt.PreviousDigest != "") || (receipt.Revision > 1 && !isHexDigest(receipt.PreviousDigest)) ||
		(receipt.PreviousAffinityDigest != "" && !isHexDigest(receipt.PreviousAffinityDigest)) ||
		normalizeAccountEmail(receipt.CorrectedBy) != normalizeAccountEmail(receipt.RequestedBy) ||
		!strideIdentifier(receipt.OperationID) || !isHexDigest(receipt.OperationBodyDigest) || receipt.CorrectedAt.IsZero() || !isHexDigest(receipt.Digest) {
		return fmt.Errorf("workstream affinity correction is invalid")
	}
	validTarget := receipt.TargetKind == "none" && receipt.ProjectThreadID == "" && receipt.ProjectTitle == "" && receipt.ProjectACLVersion == 0 && receipt.ProjectAudienceDigest == ""
	if receipt.TargetKind == "project" {
		validTarget = strideIdentifier(receipt.ProjectThreadID) && stridePlainText(receipt.ProjectTitle, 120, true) && receipt.ProjectACLVersion > 0 && isHexDigest(receipt.ProjectAudienceDigest)
	}
	if !validTarget {
		return fmt.Errorf("workstream affinity correction target is invalid")
	}
	digest, err := STRIDEContractDigest(receipt.material())
	if err != nil || digest != receipt.Digest {
		return fmt.Errorf("workstream affinity correction digest is invalid")
	}
	return nil
}

func decodeWorkstreamAffinityCorrection(metadata map[string]string) (workstreamAffinityCorrectionReceipt, bool) {
	var receipt workstreamAffinityCorrectionReceipt
	raw := strings.TrimSpace(metadata[workstreamAffinityCorrectionMetadataKey])
	if raw == "" {
		return receipt, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || ensureJSONEOF(decoder) != nil || receipt.validate() != nil {
		return workstreamAffinityCorrectionReceipt{}, false
	}
	return receipt, true
}

func decodeWorkstreamAffinityCorrectionHistory(metadata map[string]string) ([]workstreamAffinityCorrectionReceipt, bool) {
	raw := strings.TrimSpace(metadata[workstreamAffinityCorrectionHistoryMetadataKey])
	if raw == "" {
		return []workstreamAffinityCorrectionReceipt{}, true
	}
	var receipts []workstreamAffinityCorrectionReceipt
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipts) != nil || ensureJSONEOF(decoder) != nil {
		return nil, false
	}
	previous := ""
	for index, receipt := range receipts {
		if receipt.validate() != nil || receipt.Revision != int64(index+1) || receipt.PreviousDigest != previous {
			return nil, false
		}
		previous = receipt.Digest
	}
	return receipts, true
}

func workstreamCorrectionOperationBodyDigest(artifactID, targetKind, projectID string) string {
	body, _ := canonicalJSON(map[string]string{
		"version": "workstream-affinity-correction/v1", "artifactId": strings.TrimSpace(artifactID),
		"targetKind": strings.TrimSpace(targetKind), "projectId": strings.TrimSpace(projectID),
	})
	return sha256Hex(body)
}

func (app *kanbanBoardApp) workstreamAffinityCorrectionCurrent(ctx context.Context, artifact meetingMemoryEntry, receipt workstreamAffinityCorrectionReceipt) bool {
	if app == nil || app.memory == nil || receipt.validate() != nil || artifact.ID != receipt.ArtifactID ||
		normalizeAccountEmail(artifact.Metadata["requestedBy"]) != receipt.RequestedBy ||
		strings.TrimSpace(artifact.Metadata["originId"]) != receipt.SourceThreadID ||
		strings.TrimSpace(artifact.Metadata["sourceMessageId"]) != receipt.SourceMessageID ||
		strings.TrimSpace(artifact.Metadata["sourceMessageDigest"]) != receipt.SourceMessageDigest ||
		strings.TrimSpace(artifact.Metadata["sourceWindowDigest"]) != receipt.SourceWindowDigest {
		return false
	}
	user := accountStore().findUser(receipt.RequestedBy)
	if user == nil {
		return false
	}
	thread, _, err := app.scoutChatThreadByID(user.Email, receipt.SourceThreadID)
	if err != nil || thread.ArchivedAt != "" || !scoutChatThreadAllowsViewer(thread, user.Email) ||
		(thread.MeetingRecord != nil && !app.meetingRecordConversationBindingCurrent(user.Email, thread)) {
		return false
	}
	_, source, err := scoutChatSourceWindow(thread, receipt.SourceMessageID)
	if err != nil || source.MessageDigest != receipt.SourceMessageDigest || source.WindowDigest != receipt.SourceWindowDigest {
		return false
	}
	if receipt.TargetKind == "none" {
		return true
	}
	project, _, err := app.scoutChatThreadByID(user.Email, receipt.ProjectThreadID)
	if err != nil || project.ArchivedAt != "" || !strideProductProjectDestinationEligible(project) ||
		!scoutChatThreadAllowsViewer(project, user.Email) || strings.TrimSpace(project.Title) != receipt.ProjectTitle {
		return false
	}
	audience, aclVersion, err := strideProductProjectDestinationAuthority(project)
	if err != nil || aclVersion != receipt.ProjectACLVersion {
		return false
	}
	digest, err := STRIDEContractDigest(audience)
	return err == nil && digest == receipt.ProjectAudienceDigest
}

func (app *kanbanBoardApp) applyWorkstreamAffinityCorrection(ctx context.Context, user *userAccount, artifactID, targetKind, projectID, operationID string, expectedHeader ArtifactAuthorizationHeader, expectedAffinityDigest, expectedCorrectionDigest string, now time.Time) (meetingMemoryEntry, bool, error) {
	if app == nil || app.memory == nil || user == nil || !strideIdentifier(artifactID) || !oneOf(targetKind, "project", "none") ||
		(targetKind == "project" && !strideIdentifier(projectID)) || (targetKind == "none" && strings.TrimSpace(projectID) != "") ||
		!strideIdentifier(operationID) || now.IsZero() {
		return meetingMemoryEntry{}, false, fmt.Errorf("workstream affinity correction is invalid")
	}
	currentHeader, found := app.memory.artifactAuthorizationHeaderByID(artifactID)
	if !found || !artifactAuthorizationHeaderEqual(currentHeader, expectedHeader) || !artifactHeaderAuthorized(ctx, user, ACLWrite, currentHeader) {
		return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
	}
	artifact, exact := app.memory.artifactSnapshotIfHeaderMatches(artifactID, currentHeader)
	if !exact || normalizeAccountEmail(artifact.Metadata["requestedBy"]) != normalizeAccountEmail(user.Email) {
		return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
	}
	bodyDigest := workstreamCorrectionOperationBodyDigest(artifactID, targetKind, projectID)
	if current, ok := decodeWorkstreamAffinityCorrection(artifact.Metadata); ok && current.OperationID == operationID {
		if current.OperationBodyDigest != bodyDigest {
			return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
		}
		return artifact, true, nil
	}
	rawAffinity := strings.TrimSpace(artifact.Metadata[workstreamAffinityMetadataKey])
	rawCorrection := strings.TrimSpace(artifact.Metadata[workstreamAffinityCorrectionMetadataKey])
	if sha256Hex([]byte(rawAffinity)) != expectedAffinityDigest || sha256Hex([]byte(rawCorrection)) != expectedCorrectionDigest {
		return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
	}
	history, historyOK := decodeWorkstreamAffinityCorrectionHistory(artifact.Metadata)
	if !historyOK {
		return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
	}
	for _, prior := range history {
		if prior.OperationID == operationID {
			return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
		}
	}
	threadID := strings.TrimSpace(artifact.Metadata["originId"])
	messageID := strings.TrimSpace(artifact.Metadata["sourceMessageId"])
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil || thread.ArchivedAt != "" || !scoutChatThreadAllowsViewer(thread, user.Email) {
		return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
	}
	_, source, err := scoutChatSourceWindow(thread, messageID)
	if err != nil || source.MessageDigest != strings.TrimSpace(artifact.Metadata["sourceMessageDigest"]) || source.WindowDigest != strings.TrimSpace(artifact.Metadata["sourceWindowDigest"]) {
		return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
	}
	receipt := workstreamAffinityCorrectionReceipt{
		Version: workstreamAffinityCorrectionVersion, TenantID: canonicalTenantID(), ArtifactID: artifactID,
		RequestedBy: normalizeAccountEmail(user.Email), SourceThreadID: threadID, SourceMessageID: messageID,
		SourceMessageDigest: source.MessageDigest, SourceWindowDigest: source.WindowDigest,
		Revision: int64(len(history) + 1), PreviousAffinityDigest: "", TargetKind: targetKind,
		CorrectedBy: normalizeAccountEmail(user.Email), OperationID: operationID, OperationBodyDigest: bodyDigest, CorrectedAt: now.UTC(),
	}
	if len(history) > 0 {
		receipt.PreviousDigest = history[len(history)-1].Digest
	}
	if prior, ok := decodeWorkstreamAffinity(artifact.Metadata); ok {
		receipt.PreviousAffinityDigest = prior.Digest
	}
	var project scoutChatThreadRecord
	if targetKind == "project" {
		project, _, err = app.scoutChatThreadByID(user.Email, projectID)
		if err != nil || project.ArchivedAt != "" || !strideProductProjectDestinationEligible(project) || !scoutChatThreadAllowsViewer(project, user.Email) {
			return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
		}
		audience, aclVersion, authorityErr := strideProductProjectDestinationAuthority(project)
		if authorityErr != nil {
			return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
		}
		audienceDigest, digestErr := STRIDEContractDigest(audience)
		if digestErr != nil {
			return meetingMemoryEntry{}, false, errWorkstreamAffinityConflict
		}
		receipt.ProjectThreadID, receipt.ProjectTitle = project.ID, strings.TrimSpace(project.Title)
		receipt.ProjectACLVersion, receipt.ProjectAudienceDigest = aclVersion, audienceDigest
	}
	receipt.Digest, err = STRIDEContractDigest(receipt.material())
	if err != nil || receipt.validate() != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("workstream affinity correction could not be signed")
	}
	receiptRaw, _ := json.Marshal(receipt)
	history = append(history, receipt)
	historyRaw, _ := json.Marshal(history)
	updates := map[string]string{
		workstreamAffinityCorrectionMetadataKey: string(receiptRaw), workstreamAffinityCorrectionHistoryMetadataKey: string(historyRaw),
	}
	if targetKind == "none" {
		updates[workstreamAffinityMetadataKey], updates["projectWorkId"], updates["projectWorkTitle"] = "", "", ""
	} else {
		sourceMeetingID, sourceMeetingRevision := "", ""
		if thread.MeetingRecord != nil {
			sourceMeetingID, sourceMeetingRevision = thread.MeetingRecord.MeetingID, thread.MeetingRecord.RecordRevision
		}
		binding := workstreamAffinityBinding{
			Version: workstreamAffinityVersion, TenantID: canonicalTenantID(), RequestedBy: normalizeAccountEmail(user.Email),
			SourceThreadID: threadID, SourceMessageID: messageID, SourceMessageDigest: source.MessageDigest, SourceWindowDigest: source.WindowDigest,
			SourceMeetingID: sourceMeetingID, SourceMeetingRevision: sourceMeetingRevision,
			ProjectThreadID: project.ID, ProjectTitle: strings.TrimSpace(project.Title), ProjectACLVersion: receipt.ProjectACLVersion,
			ProjectAudienceDigest: receipt.ProjectAudienceDigest, CorrectionReceiptDigest: receipt.Digest,
			Basis: workstreamAffinityCorrectionBasis, Confidence: 1, Resolver: workstreamAffinityResolver, EvaluatedAt: now.UTC(),
		}
		binding.Digest, err = STRIDEContractDigest(binding.material())
		if err != nil || binding.validate() != nil {
			return meetingMemoryEntry{}, false, fmt.Errorf("corrected workstream affinity could not be signed")
		}
		encoded, _ := encodeWorkstreamAffinity(binding)
		updates[workstreamAffinityMetadataKey], updates["projectWorkId"], updates["projectWorkTitle"] = encoded, project.ID, project.Title
	}
	updated, changed, err := app.memory.updateOSArtifactMetadataIfHeaderAndMetadataMatch(currentHeader, map[string]string{
		workstreamAffinityMetadataKey: rawAffinity, workstreamAffinityCorrectionMetadataKey: rawCorrection,
	}, artifactID, updates)
	if err != nil || !changed {
		if err == nil {
			err = errWorkstreamAffinityConflict
		}
		return meetingMemoryEntry{}, false, err
	}
	return updated, false, nil
}

func (app *kanbanBoardApp) buildWorkstreamAffinityCorrectionPreview(ctx context.Context, user *userAccount, artifact meetingMemoryEntry, header ArtifactAuthorizationHeader, snapshot StrideE10TenantAuthoritySnapshot) (workstreamAffinityCorrectionPreview, error) {
	preview := workstreamAffinityCorrectionPreview{Current: workstreamAffinityCorrectionCurrent{Title: "No project", Status: "none"}}
	if app == nil || user == nil || artifact.ID == "" || normalizeAccountEmail(artifact.Metadata["requestedBy"]) != normalizeAccountEmail(user.Email) ||
		!artifactHeaderAuthorized(ctx, user, ACLWrite, header) {
		return preview, ErrProjectAuthorityDenied
	}
	currentProjectID := ""
	if receipt, corrected := decodeWorkstreamAffinityCorrection(artifact.Metadata); corrected {
		preview.Current.Revision = receipt.Revision
		if receipt.TargetKind == "project" {
			preview.Current.Title, currentProjectID = receipt.ProjectTitle, receipt.ProjectThreadID
			if app.workstreamAffinityCorrectionCurrent(ctx, artifact, receipt) {
				preview.Current.Status = "current"
			} else {
				preview.Current.Status = "unavailable"
			}
		}
	} else if affinity, inferred := decodeWorkstreamAffinity(artifact.Metadata); inferred {
		preview.Current.Title, currentProjectID = affinity.ProjectTitle, affinity.ProjectThreadID
		if app.workstreamAffinityCurrent(ctx, artifact) {
			preview.Current.Status = "current"
		} else {
			preview.Current.Status = "unavailable"
		}
	}
	projects, err := visibleHomeProjects(ctx, snapshot)
	if err != nil {
		return preview, err
	}
	preview.Available, preview.ScopeKey = true, homeProjectScopeKey(snapshot)
	for _, project := range projects {
		if project.ID == currentProjectID {
			continue
		}
		thread, _, threadErr := app.scoutChatThreadByID(user.Email, project.ID)
		if threadErr != nil || !strideProductProjectDestinationEligible(thread) || !scoutChatThreadAllowsViewer(thread, user.Email) || strings.TrimSpace(thread.Title) != project.Title {
			continue
		}
		target := projectChatCorrectionTarget{Kind: "project", ProjectID: project.ID, ProjectRevision: project.Revision, ProjectDigest: project.Digest, ProjectTitle: project.Title}
		token, mintErr := mintWorkstreamAffinityCorrectionToken(ctx, snapshot, artifact, header, target)
		if mintErr != nil {
			return preview, mintErr
		}
		preview.Choices = append(preview.Choices, workstreamAffinityCorrectionChoice{Title: project.Title, Token: token})
	}
	if currentProjectID != "" {
		token, mintErr := mintWorkstreamAffinityCorrectionToken(ctx, snapshot, artifact, header, projectChatCorrectionTarget{Kind: "remove"})
		if mintErr != nil {
			return preview, mintErr
		}
		preview.Remove = &workstreamAffinityCorrectionChoice{Title: "No project", Token: token}
	}
	return preview, nil
}

func workstreamAffinityTokenTargetCurrent(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, target projectChatCorrectionTarget) bool {
	if target.Kind == "remove" {
		return true
	}
	projects, err := visibleHomeProjects(ctx, snapshot)
	if err != nil {
		return false
	}
	for _, project := range projects {
		if project.ID == target.ProjectID && project.Revision == target.ProjectRevision && project.Digest == target.ProjectDigest && project.Title == target.ProjectTitle {
			return true
		}
	}
	return false
}

// artifactWorkstreamCorrectionHandler is the only client-facing Work Project
// correction seam. GET mints opaque current-session choices; PATCH accepts only
// one of those signed targets plus an idempotency key. Neither route changes the
// source conversation or revives composer-selected Project authority.
func artifactWorkstreamCorrectionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		w.Header().Set("Allow", "GET, PATCH")
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
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Work correction is unavailable")
		return
	}
	artifactID := strings.TrimSpace(r.URL.Query().Get("id"))
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(artifactID)
	if !found || !artifactHeaderAuthorized(r.Context(), user, ACLWrite, header) {
		writeAuthError(w, http.StatusNotFound, "Work not found")
		return
	}
	artifact, exact := kanbanApp.memory.artifactSnapshotIfHeaderMatches(artifactID, header)
	if !exact || normalizeAccountEmail(artifact.Metadata["requestedBy"]) != normalizeAccountEmail(user.Email) {
		writeAuthError(w, http.StatusNotFound, "Work not found")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodGet {
		var preview workstreamAffinityCorrectionPreview
		err := withCurrentHomeProjectAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
			var buildErr error
			preview, buildErr = kanbanApp.buildWorkstreamAffinityCorrectionPreview(r.Context(), user, artifact, header, snapshot)
			return buildErr
		})
		if err != nil {
			writeAuthError(w, http.StatusConflict, "Work correction is unavailable")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "workstreamCorrection": preview})
		return
	}
	payload := struct {
		OperationID     string `json:"operationId"`
		CorrectionToken string `json:"correctionToken"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read Work correction")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAuthError(w, http.StatusBadRequest, "Work correction must contain exactly one object")
		return
	}
	operationID, err := normalizeScoutIdempotencyKey(payload.OperationID)
	if err != nil || strings.TrimSpace(payload.CorrectionToken) == "" {
		writeAuthError(w, http.StatusBadRequest, "Work correction operation is invalid")
		return
	}
	var token workstreamAffinityCorrectionToken
	err = withCurrentHomeProjectAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		var tokenErr error
		token, tokenErr = resolveWorkstreamAffinityCorrectionToken(r.Context(), payload.CorrectionToken, snapshot)
		if tokenErr != nil || token.ArtifactID != artifactID || !workstreamAffinityTokenTargetCurrent(r.Context(), snapshot, token.Target) {
			return errHomeProjectStale
		}
		return nil
	})
	if err != nil || token.ArtifactContentRevision != header.ContentRevision || token.ArtifactContentDigest != header.ContentDigest {
		writeAuthJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Work changed elsewhere. Review and confirm again."})
		return
	}
	targetKind, projectID := "none", ""
	if token.Target.Kind == "project" {
		targetKind, projectID = "project", token.Target.ProjectID
	}
	updated, replayed, err := kanbanApp.applyWorkstreamAffinityCorrection(r.Context(), user, artifactID, targetKind, projectID, operationID,
		header, token.ExpectedAffinityDigest, token.ExpectedCorrectionDigest, time.Now().UTC())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWorkstreamAffinityConflict) {
			status = http.StatusConflict
		}
		writeAuthError(w, status, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "artifact": updated, "replayed": replayed})
}
