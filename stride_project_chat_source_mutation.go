package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type projectChatMutationAuthorityBinding struct {
	OrganizationID string
	ActorPersonID  string
}

func currentProjectChatMutationAuthorityBinding(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, message scoutChatMessageRecord) (projectChatMutationAuthorityBinding, error) {
	var binding projectChatMutationAuthorityBinding
	if user == nil || !projectChatMessageAuthorOnly(thread, message, user) {
		return binding, ErrProjectAuthorityDenied
	}
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil {
		return binding, errHomeProjectUnavailable
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok || resolver == nil {
		return binding, errHomeProjectUnavailable
	}
	sessionHash := strideE10TenantSessionHashFromContext(ctx)
	if !validStrideE10SessionHash(sessionHash) {
		return binding, ErrStrideE10TenantAuthorityStale
	}
	err := resolver.WithCurrentTenantAuthority(ctx, StrideE10TenantSurfaceHTTP, sessionHash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		if normalizeAccountEmail(snapshot.Session.Email) != normalizeAccountEmail(user.Email) {
			return ErrProjectAuthorityDenied
		}
		binding = projectChatMutationAuthorityBinding{OrganizationID: snapshot.Organization.Header.ID, ActorPersonID: snapshot.Person.Header.ID}
		return nil
	})
	return binding, err
}

func projectSourceMutationRequestDigest(kind, threadID, messageID string, textPresent bool, text string) string {
	return sha256Hex([]byte(strings.Join([]string{"project-chat-source-mutation/v1", kind, threadID, messageID, fmt.Sprint(textPresent), strings.TrimSpace(text)}, "\x00")))
}

func projectSourceMutationOperationID(kind, threadID, messageID, requestDigest string) string {
	return projectChatID("project_source_mutation_journal", kind, threadID, messageID, requestDigest)
}

func scoutProjectSourceMutationIndex(thread scoutChatThreadRecord, operationID string) int {
	for index := range thread.ProjectSourceMutationOperations {
		if thread.ProjectSourceMutationOperations[index].OperationID == operationID {
			return index
		}
	}
	return -1
}

func hasPendingProjectSourceMutation(thread scoutChatThreadRecord) bool {
	for _, operation := range thread.ProjectSourceMutationOperations {
		if operation.State == "pending" {
			return true
		}
	}
	return false
}

// beginScoutProjectSourceMutationLocked writes the recovery journal before the
// PostgreSQL invalidation. The caller owns the per-thread lock. It also makes
// the visible Project projection fail closed while the two stores converge.
func (app *kanbanBoardApp) beginScoutProjectSourceMutationLocked(user *userAccount, thread scoutChatThreadRecord, messageIndex int, kind string, textPresent bool, text string, bindings ...projectChatMutationAuthorityBinding) (scoutChatThreadRecord, scoutChatProjectSourceMutationOperation, bool, error) {
	if app == nil || user == nil || messageIndex < 0 || messageIndex >= len(thread.Messages) || !oneOf(kind, "edit", "delete") {
		return thread, scoutChatProjectSourceMutationOperation{}, false, ErrProjectAuthorityInvalid
	}
	message := thread.Messages[messageIndex]
	requestDigest := projectSourceMutationRequestDigest(kind, thread.ID, message.ID, textPresent, text)
	operationID := projectSourceMutationOperationID(kind, thread.ID, message.ID, requestDigest)
	actor := normalizeAccountEmail(user.Email)
	for _, existing := range thread.ProjectSourceMutationOperations {
		if existing.MessageID != message.ID || oneOf(existing.State, "confirmed", "failed") && existing.OperationID != operationID {
			continue
		}
		if existing.OperationID != operationID || existing.RequestDigest != requestDigest || existing.Kind != kind || existing.ActorEmail != actor || existing.TextPresent != textPresent || existing.Text != strings.TrimSpace(text) {
			return thread, existing, false, ErrProjectAuthorityConflict
		}
		if existing.State == "failed" {
			return thread, existing, false, ErrProjectAuthorityConflict
		}
		return thread, existing, false, nil
	}
	if message.Project == nil || message.Project.Status != "confirmed" || !strideIdentifier(message.Project.AssociationID) || message.Project.AssociationRevision < 1 {
		return thread, scoutChatProjectSourceMutationOperation{}, false, ErrProjectAuthorityConflict
	}
	originalProject := *message.Project
	binding := projectChatMutationAuthorityBinding{}
	if len(bindings) > 0 {
		binding = bindings[0]
	}
	operation := scoutChatProjectSourceMutationOperation{
		OperationID: operationID, RequestDigest: requestDigest, Kind: kind, MessageID: message.ID, ActorEmail: actor,
		OrganizationID: binding.OrganizationID, ActorPersonID: binding.ActorPersonID,
		ExpectedProject: originalProject, State: "pending", TextPresent: textPresent, Text: strings.TrimSpace(text),
		ResultContextRevision: projectChatCorrectionContextRevision(&originalProject) + 1,
	}
	thread.ProjectSourceMutationOperations = append(thread.ProjectSourceMutationOperations, operation)
	thread.Messages[messageIndex].Project = &scoutChatProjectContext{
		Status: "unavailable", ContextRevision: operation.ResultContextRevision, Title: originalProject.Title,
		ProjectID: originalProject.ProjectID, ProjectRevision: originalProject.ProjectRevision,
		AssociationID: originalProject.AssociationID, AssociationRevision: originalProject.AssociationRevision,
	}
	if kind == "delete" {
		previewThread := thread
		previewThread.Messages = make([]scoutChatMessageRecord, 0, len(thread.Messages))
		for _, candidate := range thread.Messages {
			if candidate.ID == message.ID || candidate.CausedByMessageID == message.ID {
				continue
			}
			previewThread.Messages = append(previewThread.Messages, candidate)
		}
		thread.Preview = scoutChatThreadPreview(previewThread)
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := app.saveScoutChatThread(thread); err != nil {
		return thread, operation, false, err
	}
	if kind == "delete" {
		deliverScoutChatThreadDeletion(thread, message.ID)
		for _, candidate := range thread.Messages {
			if candidate.CausedByMessageID == message.ID {
				deliverScoutChatThreadDeletion(thread, candidate.ID)
			}
		}
	} else {
		deliverScoutChatThreadUpdate(thread, thread.Messages[messageIndex])
	}
	return thread, operation, true, nil
}

func (app *kanbanBoardApp) failScoutProjectSourceMutationLocked(thread scoutChatThreadRecord, operationID string, receiptAbsent ...bool) error {
	index := scoutProjectSourceMutationIndex(thread, operationID)
	if index < 0 {
		return ErrProjectAuthorityNotFound
	}
	operation := &thread.ProjectSourceMutationOperations[index]
	if operation.State == "confirmed" {
		return ErrProjectAuthorityConflict
	}
	operation.State = "failed"
	if len(receiptAbsent) > 0 && receiptAbsent[0] {
		messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
		if messageIndex >= 0 {
			current := thread.Messages[messageIndex].Project
			if current != nil && current.Status == "unavailable" && projectChatCorrectionContextRevision(current) == operation.ResultContextRevision {
				project := operation.ExpectedProject
				thread.Messages[messageIndex].Project = &project
			}
		}
	}
	thread.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return app.saveScoutChatThread(thread)
}

func projectSourceMutationOriginalMessage(thread scoutChatThreadRecord, operation scoutChatProjectSourceMutationOperation) (scoutChatMessageRecord, error) {
	index := scoutChatMessageIndex(thread, operation.MessageID)
	if index < 0 {
		return scoutChatMessageRecord{}, ErrProjectAuthorityConflict
	}
	message := thread.Messages[index]
	project := operation.ExpectedProject
	message.Project = &project
	return message, nil
}

func markProjectSourceMutationConfirmed(thread *scoutChatThreadRecord, operationID string, sourceRevision int64, editedAt string) error {
	index := scoutProjectSourceMutationIndex(*thread, operationID)
	if index < 0 {
		return ErrProjectAuthorityConflict
	}
	operation := &thread.ProjectSourceMutationOperations[index]
	if operation.State == "confirmed" {
		if operation.ResultSourceRevision != sourceRevision || operation.ResultEditedAt != editedAt {
			return ErrProjectAuthorityConflict
		}
		return nil
	}
	operation.State = "confirmed"
	operation.ResultSourceRevision = sourceRevision
	operation.ResultEditedAt = editedAt
	return nil
}

// applyProjectSourceDeleteToThread mutates only the supplied in-memory record.
// Its caller saves the journal and deletion as one legacy-store replacement.
func applyProjectSourceDeleteToThread(thread *scoutChatThreadRecord, messageID string) ([]string, []scoutChatMessageRecord, error) {
	index := scoutChatMessageIndex(*thread, messageID)
	if index < 0 {
		return nil, nil, ErrProjectAuthorityConflict
	}
	message := thread.Messages[index]
	deletedIDs := []string{messageID}
	deletedMessages := []scoutChatMessageRecord{message}
	if thread.OpeningOperation != nil && thread.OpeningOperation.UserMessageID == messageID {
		replyID := thread.OpeningOperation.ReplyMessageID
		filtered := make([]scoutChatMessageRecord, 0, len(thread.Messages)-1)
		for _, candidate := range thread.Messages {
			if candidate.ID == messageID || candidate.ID == replyID {
				if candidate.ID == replyID {
					deletedIDs = append(deletedIDs, replyID)
					deletedMessages = append(deletedMessages, candidate)
				}
				continue
			}
			filtered = append(filtered, candidate)
		}
		thread.Messages = filtered
		thread.OpeningOperation = nil
	} else {
		filtered := make([]scoutChatMessageRecord, 0, len(thread.Messages)-1)
		for _, candidate := range thread.Messages {
			ordinaryGeneratedAnswer := candidate.ID != messageID && strings.TrimSpace(candidate.CausedByMessageID) == messageID &&
				(strings.EqualFold(candidate.Role, "scout") || strings.EqualFold(candidate.Role, "assistant")) &&
				candidate.Kind == "message" && candidate.Thread == nil && candidate.Proposal == nil && candidate.Image == nil && candidate.Manifest == nil
			if candidate.ID == messageID || ordinaryGeneratedAnswer {
				if ordinaryGeneratedAnswer {
					deletedIDs = append(deletedIDs, candidate.ID)
					deletedMessages = append(deletedMessages, candidate)
				}
				continue
			}
			filtered = append(filtered, candidate)
		}
		thread.Messages = filtered
	}
	return deletedIDs, deletedMessages, nil
}

func (app *kanbanBoardApp) resumePendingProjectSourceMutations(ctx context.Context, user *userAccount, threadID string) (scoutChatThreadRecord, error) {
	lock := app.scoutChatThreadLock(threadID)
	lock.Lock()
	defer lock.Unlock()
	thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
	if err != nil {
		return thread, err
	}
	for operationIndex := range thread.ProjectSourceMutationOperations {
		operation := thread.ProjectSourceMutationOperations[operationIndex]
		if operation.State != "pending" {
			continue
		}
		message, err := projectSourceMutationOriginalMessage(thread, operation)
		if err != nil {
			return thread, err
		}
		var sourceRevision int64
		var found bool
		store := currentHomeProjectStore()
		if store != nil && strideIdentifier(operation.OrganizationID) && strideIdentifier(operation.ActorPersonID) {
			sourceRevision, found, err = store.committedProjectChatSourceMutation(ctx, operation.OrganizationID, operation.ActorPersonID, threadID, operation)
			if err != nil {
				return thread, err
			}
		}
		if !found {
			// Only the original actor's current session may retry an operation
			// that has not committed. Other viewers still receive the honest
			// unavailable projection and can never restore the old association.
			if user == nil || operation.ActorEmail != normalizeAccountEmail(user.Email) {
				continue
			}
			sourceRevision, err = invalidateProjectChatSourceForMutation(ctx, user, thread, message, operation.OperationID, operation.RequestDigest, operation.Kind)
			if err != nil {
				return thread, err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		var deletedIDs []string
		var deletedMessages []scoutChatMessageRecord
		var canceledReply scoutChatMessageRecord
		var canceledOpeningReply bool
		if operation.Kind == "edit" {
			messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
			if messageIndex < 0 {
				return thread, ErrProjectAuthorityConflict
			}
			thread.Messages[messageIndex].Text = operation.Text
			thread.Messages[messageIndex].EditedAt = now
			if thread.OpeningOperation != nil && thread.OpeningOperation.UserMessageID == operation.MessageID {
				canceledReply, canceledOpeningReply = cancelScoutOpeningReplyInThread(&thread, scoutReplyCanceledAfterEditText, time.Now().UTC())
			}
		} else {
			deletedIDs, deletedMessages, err = applyProjectSourceDeleteToThread(&thread, operation.MessageID)
			if err != nil {
				return thread, err
			}
		}
		if err := markProjectSourceMutationConfirmed(&thread, operation.OperationID, sourceRevision, map[bool]string{true: now}[operation.Kind == "edit"]); err != nil {
			return thread, err
		}
		thread.UpdatedAt = now
		thread.Preview = scoutChatThreadPreview(thread)
		if err := app.saveScoutChatThread(thread); err != nil {
			return thread, err
		}
		if operation.Kind == "edit" {
			messageIndex := scoutChatMessageIndex(thread, operation.MessageID)
			message := thread.Messages[messageIndex]
			app.observeSTRIDETeamChatMessage(thread, message, "edit", operation.ActorEmail)
			app.rebuildPrivateConversationContinuity(thread, "edit")
			deliverScoutChatThreadUpdate(thread, message)
			if canceledOpeningReply {
				deliverScoutChatThreadUpdate(thread, canceledReply)
			}
		} else {
			for _, deletedMessage := range deletedMessages {
				app.observeSTRIDETeamChatMessage(thread, deletedMessage, "delete", operation.ActorEmail)
			}
			app.rebuildPrivateConversationContinuity(thread, "delete")
			for _, deletedID := range deletedIDs {
				deliverScoutChatThreadDeletion(thread, deletedID)
			}
		}
	}
	return thread, nil
}
