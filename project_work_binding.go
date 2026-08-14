package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	projectWorkBindingVersion     = 1
	projectWorkBindingMetadataKey = "projectWorkBinding"
)

// projectWorkBinding is the body-free, server-owned bridge from one confirmed
// Project-linked chat turn to the private Research artifact it starts. The
// chat association remains canonical truth; the artifact carries this
// immutable snapshot so every provider/read/edit/save/follow-up seam can prove
// that exact association is still authorized-current.
type projectWorkBinding struct {
	Version             int    `json:"version"`
	OrganizationID      string `json:"organizationId"`
	PersonID            string `json:"personId"`
	ThreadID            string `json:"threadId"`
	MessageID           string `json:"messageId"`
	ContextRevision     int64  `json:"contextRevision"`
	ProjectID           string `json:"projectId"`
	ProjectRevision     int64  `json:"projectRevision"`
	ProjectDigest       string `json:"projectDigest"`
	ProjectTitle        string `json:"projectTitle"`
	AssociationID       string `json:"associationId"`
	AssociationRevision int64  `json:"associationRevision"`
	AssociationDigest   string `json:"associationDigest"`
	SourceEventID       string `json:"sourceEventId"`
	SourceEventRevision int64  `json:"sourceEventRevision"`
	SourceDigest        string `json:"sourceDigest"`
	SourceACLRevision   int64  `json:"sourceAclRevision"`
	SourceACLDigest     string `json:"sourceAclDigest"`
	PurgeGeneration     int64  `json:"purgeGeneration"`
}

func (binding projectWorkBinding) validate() error {
	if binding.Version != projectWorkBindingVersion ||
		!strideIdentifier(binding.OrganizationID) || !strideIdentifier(binding.PersonID) ||
		!strideIdentifier(binding.ThreadID) || !strideIdentifier(binding.MessageID) || binding.ContextRevision < 1 ||
		!strideIdentifier(binding.ProjectID) || binding.ProjectRevision < 1 || !isHexDigest(binding.ProjectDigest) ||
		!stridePlainText(binding.ProjectTitle, 120, true) || !strideIdentifier(binding.AssociationID) ||
		binding.AssociationRevision < 1 || !isHexDigest(binding.AssociationDigest) ||
		!strideIdentifier(binding.SourceEventID) || binding.SourceEventRevision < 1 || !isHexDigest(binding.SourceDigest) ||
		binding.SourceACLRevision < 1 || !isHexDigest(binding.SourceACLDigest) || binding.PurgeGeneration < 1 {
		return fmt.Errorf("Project-bound work authority is invalid")
	}
	return nil
}

func encodeProjectWorkBinding(binding projectWorkBinding) (string, error) {
	if err := binding.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeProjectWorkBinding(metadata map[string]string) (projectWorkBinding, bool) {
	var binding projectWorkBinding
	raw := strings.TrimSpace(metadata[projectWorkBindingMetadataKey])
	if raw == "" {
		return binding, false
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || ensureJSONEOF(decoder) != nil || binding.validate() != nil {
		return projectWorkBinding{}, false
	}
	return binding, true
}

func (app *kanbanBoardApp) projectWorkBindingForLaunch(ctx context.Context, thread scoutChatThreadRecord, message scoutChatMessageRecord) (projectWorkBinding, error) {
	project := message.Project
	fence := strideE10HeldTenantAuthorityFromContext(ctx)
	store := currentHomeProjectStore()
	if app == nil || project == nil || project.Status != "confirmed" || store == nil || fence == nil ||
		fence.snapshot.Person.Header.ID == "" || fence.snapshot.Organization.Header.ID == "" {
		return projectWorkBinding{}, fmt.Errorf("Project-bound Research is unavailable; retry from the current Project conversation")
	}
	truth, err := store.projectChatCorrectionTruth(ctx, fence.snapshot, thread.ID, message.ID, project)
	if err != nil || truth.ProjectID != project.ProjectID || truth.ProjectRevision != project.ProjectRevision ||
		truth.ProjectTitle != project.Title || truth.AssociationID != project.AssociationID || truth.AssociationRevision != project.AssociationRevision {
		return projectWorkBinding{}, fmt.Errorf("the Project source changed; review the conversation and retry")
	}
	binding := projectWorkBinding{
		Version: projectWorkBindingVersion, OrganizationID: fence.snapshot.Organization.Header.ID, PersonID: fence.snapshot.Person.Header.ID,
		ThreadID: thread.ID, MessageID: message.ID, ContextRevision: projectChatCorrectionContextRevision(project),
		ProjectID: truth.ProjectID, ProjectRevision: truth.ProjectRevision, ProjectDigest: truth.ProjectDigest, ProjectTitle: truth.ProjectTitle,
		AssociationID: truth.AssociationID, AssociationRevision: truth.AssociationRevision, AssociationDigest: truth.AssociationDigest,
		SourceEventID: truth.SourceEventID, SourceEventRevision: truth.SourceEventRevision, SourceDigest: truth.SourceDigest,
		SourceACLRevision: truth.SourceACLRevision, SourceACLDigest: truth.SourceACLDigest, PurgeGeneration: truth.PurgeGeneration,
	}
	return binding, binding.validate()
}

// projectBoundArtifactCurrent is independent of the retired launch session.
// Current artifact ACL/session checks remain the caller's job; this proves the
// exact Project and source turn which admitted the work are still canonical.
func (app *kanbanBoardApp) projectBoundArtifactCurrent(ctx context.Context, artifact meetingMemoryEntry) bool {
	raw := strings.TrimSpace(artifact.Metadata[projectWorkBindingMetadataKey])
	if raw == "" && strings.TrimSpace(artifact.Metadata[workstreamAffinityMetadataKey]) != "" {
		return app.workstreamAffinityCurrent(ctx, artifact)
	}
	if raw == "" {
		return true
	}
	binding, ok := decodeProjectWorkBinding(artifact.Metadata)
	store := currentHomeProjectStore()
	if !ok || app == nil || store == nil {
		return false
	}

	requester := normalizeAccountEmail(artifact.Metadata["requestedBy"])
	if requester == "" || strings.TrimSpace(artifact.Metadata["originKind"]) != agentThreadOriginPrivateThread ||
		strings.TrimSpace(artifact.Metadata["originId"]) != binding.ThreadID || strings.TrimSpace(artifact.Metadata["sourceMessageId"]) != binding.MessageID {
		return false
	}
	lock := app.scoutChatThreadLock(binding.ThreadID)
	lock.Lock()
	thread, _, threadErr := app.scoutChatThreadByID(requester, binding.ThreadID)
	if threadErr != nil || !projectWorkBindingMatchesThread(binding, thread) {
		lock.Unlock()
		return false
	}
	project := thread.Messages[scoutChatMessageIndex(thread, binding.MessageID)].Project
	lock.Unlock()
	return projectWorkBindingCanonicalCurrent(ctx, store, binding, project)
}

func projectWorkBindingMatchesThread(binding projectWorkBinding, thread scoutChatThreadRecord) bool {
	messageIndex := scoutChatMessageIndex(thread, binding.MessageID)
	if thread.ID != binding.ThreadID || thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || messageIndex < 0 {
		return false
	}
	project := thread.Messages[messageIndex].Project
	return project != nil && project.Status == "confirmed" &&
		projectChatCorrectionContextRevision(project) == binding.ContextRevision && project.ProjectID == binding.ProjectID &&
		project.ProjectRevision == binding.ProjectRevision && project.Title == binding.ProjectTitle &&
		project.AssociationID == binding.AssociationID && project.AssociationRevision == binding.AssociationRevision
}

func projectWorkBindingCanonicalCurrent(ctx context.Context, store *PostgresCanonicalStore, binding projectWorkBinding, project *scoutChatProjectContext) bool {
	if store == nil || project == nil {
		return false
	}
	snapshot := StrideE10TenantAuthoritySnapshot{}
	snapshot.Organization.Header.ID = binding.OrganizationID
	snapshot.Person.Header.ID = binding.PersonID
	truth, err := store.projectChatCorrectionTruth(ctx, snapshot, binding.ThreadID, binding.MessageID, project)
	return err == nil && truth.ProjectID == binding.ProjectID && truth.ProjectRevision == binding.ProjectRevision &&
		truth.ProjectDigest == binding.ProjectDigest && truth.ProjectTitle == binding.ProjectTitle &&
		truth.AssociationID == binding.AssociationID && truth.AssociationRevision == binding.AssociationRevision &&
		truth.AssociationDigest == binding.AssociationDigest && truth.SourceEventID == binding.SourceEventID &&
		truth.SourceEventRevision == binding.SourceEventRevision && truth.SourceDigest == binding.SourceDigest &&
		truth.SourceACLRevision == binding.SourceACLRevision && truth.SourceACLDigest == binding.SourceACLDigest &&
		truth.PurgeGeneration == binding.PurgeGeneration
}
