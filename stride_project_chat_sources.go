package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const projectChatSourceManifestVersion = 2

type projectChatAttachmentHandle struct {
	SourceID       string `json:"sourceId"`
	SourceRevision string `json:"sourceRevision"`
}

type projectChatManifestAttachment struct {
	Ordinal             int
	SourceID            string
	SourceRevision      string
	BlobRef             string
	BlobDigest          string
	Mime                string
	Size                int64
	DestinationRevision string
	OriginFileID        string
	OriginRevision      string
}

type projectChatManifestReply struct {
	MessageID       string
	EventID         string
	SourceRevision  int64
	SourceDigest    string
	LegacyDigest    string
	AuthorEmail     string
	AuthorPersonID  string
	AudienceDigest  string
	ACLRevision     int64
	PurgeGeneration int64
}

type projectChatSourceManifest struct {
	Version     int
	Destination homeProjectDestination
	TextDigest  string
	Attachments []projectChatManifestAttachment
	Reply       *projectChatManifestReply
	Digest      string
}

func projectChatMessageSourceDigest(message scoutChatMessageRecord) string {
	files := make([]string, 0, len(message.Files))
	for _, file := range message.Files {
		files = append(files, strings.Join([]string{strings.TrimSpace(file.SourceID), strings.TrimSpace(file.SourceRevision), strings.TrimSpace(file.Ref)}, "\x1f"))
	}
	return sha256Hex([]byte(strings.Join([]string{
		"project-chat-parent/v1", strings.TrimSpace(message.ID), strings.TrimSpace(message.Text),
		strings.TrimSpace(message.CreatedAt), strings.TrimSpace(message.EditedAt), strings.Join(files, "\x1e"),
	}, "\x00")))
}

func projectChatManifestDigest(manifest projectChatSourceManifest) string {
	parts := []string{"project-chat-source-manifest/v2", manifest.Destination.Route, manifest.Destination.ThreadID, manifest.TextDigest}
	attachmentParts := make([]string, 0, len(manifest.Attachments))
	for _, file := range manifest.Attachments {
		attachmentParts = append(attachmentParts, fmt.Sprintf("attachment\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%d\x1f%s\x1f%s\x1f%s",
			file.Ordinal+1, file.SourceID, file.SourceRevision, file.BlobRef, file.BlobDigest, file.Mime, file.Size,
			file.DestinationRevision, file.OriginFileID, file.OriginRevision))
	}
	parts = append(parts, strings.Join(attachmentParts, "\x1e"))
	replyPart := ""
	if manifest.Reply != nil {
		replyPart = fmt.Sprintf("reply\x1f%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%s\x1f%d\x1f%d",
			manifest.Reply.EventID, manifest.Reply.SourceRevision, manifest.Reply.SourceDigest,
			manifest.Reply.AuthorPersonID, manifest.Reply.LegacyDigest, manifest.Reply.AudienceDigest, manifest.Reply.ACLRevision, manifest.Reply.PurgeGeneration)
	}
	parts = append(parts, replyPart)
	return sha256Hex([]byte(strings.Join(parts, "\x1e")))
}

func projectChatBlobDigest(ref string, meta blobMeta) string {
	ref = strings.TrimSpace(ref)
	if !validBlobRef(ref) {
		return ""
	}
	return ref
}

func (app *kanbanBoardApp) resolveProjectChatSourceManifest(ctx context.Context, user *userAccount, snapshot StrideE10TenantAuthoritySnapshot, text string, destination homeProjectDestination, handles []projectChatAttachmentHandle, replyToMessageID string) (projectChatSourceManifest, error) {
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion, Destination: destination, TextDigest: sha256Hex([]byte(strings.TrimSpace(text)))}
	if app == nil || user == nil || len(handles) > scoutChatMaxFilesPerMessage {
		return manifest, ErrProjectAuthorityInvalid
	}
	destination, err := destination.normalized()
	if err != nil {
		return manifest, err
	}
	manifest.Destination = destination
	if destination.Route == "new-private" && (len(handles) != 0 || strings.TrimSpace(replyToMessageID) != "") {
		return manifest, ErrProjectAuthorityInvalid
	}
	var thread scoutChatThreadRecord
	if destination.Route == "thread" {
		thread, _, err = app.scoutChatThreadByID(user.Email, destination.ThreadID)
		if err != nil || thread.ArchivedAt != "" {
			return manifest, errHomeProjectStale
		}
		if _, err = currentHomeProjectStore().projectChatSourceAuthorityForThread(ctx, snapshot, thread); err != nil {
			return manifest, err
		}
	}
	seen := map[string]bool{}
	for ordinal, handle := range handles {
		handle.SourceID, handle.SourceRevision = strings.TrimSpace(handle.SourceID), strings.TrimSpace(handle.SourceRevision)
		if handle.SourceID == "" || handle.SourceRevision == "" || seen[handle.SourceID] {
			return manifest, ErrProjectAuthorityInvalid
		}
		seen[handle.SourceID] = true
		app.pendingAttachmentUploadsMu.Lock()
		grant, ok := app.pendingAttachmentUploads[handle.SourceID]
		storeHealthy := app.attachmentSourceStoreErr == nil
		app.pendingAttachmentUploadsMu.Unlock()
		if !ok || !storeHealthy || grant.State != attachmentSourcePending || !grant.ExpiresAt.After(time.Now().UTC()) ||
			grant.OwnerEmail != normalizeAccountEmail(user.Email) || grant.SourceRevision != handle.SourceRevision ||
			grant.DestinationID != thread.ID || grant.DestinationRevision != scoutChatAttachmentDestinationRevision(thread) {
			return manifest, errHomeProjectStale
		}
		meta, statErr := blobStatForRef(grant.Ref)
		if statErr != nil || meta.Size != grant.Size || strings.ToLower(strings.TrimSpace(meta.Mime)) != grant.Mime || attachmentSourceRevision(grant.Ref, meta) != grant.SourceRevision {
			return manifest, errHomeProjectStale
		}
		if grant.OriginFileID != "" {
			current, currentOK := app.assistantFileSourceRevisionForDestination(ctx, user, grant.OriginFileID, thread)
			if !currentOK || current != grant.OriginRevision {
				return manifest, errHomeProjectStale
			}
		}
		manifest.Attachments = append(manifest.Attachments, projectChatManifestAttachment{
			Ordinal: ordinal, SourceID: grant.SourceID, SourceRevision: grant.SourceRevision, BlobRef: grant.Ref,
			BlobDigest: projectChatBlobDigest(grant.Ref, meta), Mime: grant.Mime, Size: int64(meta.Size),
			DestinationRevision: grant.DestinationRevision, OriginFileID: grant.OriginFileID, OriginRevision: grant.OriginRevision,
		})
	}
	if replyID := strings.TrimSpace(replyToMessageID); replyID != "" {
		index := scoutChatMessageIndex(thread, replyID)
		if index < 0 {
			return manifest, errHomeProjectStale
		}
		parent := thread.Messages[index]
		personID, mapErr := currentHomeProjectStore().projectChatPersonForEmail(ctx, snapshot.Organization.Header.ID, parent.AuthorEmail)
		if mapErr != nil {
			return manifest, mapErr
		}
		authority, authorityErr := currentHomeProjectStore().projectChatSourceAuthorityForThread(ctx, snapshot, thread)
		if authorityErr != nil {
			return manifest, authorityErr
		}
		audience, _ := json.Marshal(STRIDEAudience{Visibility: authority.Visibility, Principals: authority.Principals})
		parentEventID, parentRevision, parentDigest, parentErr := currentHomeProjectStore().projectChatParentEventPreview(ctx, snapshot.Organization.Header.ID, thread.ID, parent)
		if parentErr != nil {
			return manifest, parentErr
		}
		manifest.Reply = &projectChatManifestReply{MessageID: replyID, EventID: parentEventID, SourceRevision: parentRevision,
			SourceDigest: parentDigest, LegacyDigest: projectChatMessageSourceDigest(parent), AuthorEmail: normalizeAccountEmail(parent.AuthorEmail), AuthorPersonID: personID,
			AudienceDigest: sha256Hex(audience), ACLRevision: authority.ACLRevision, PurgeGeneration: 1}
	}
	manifest.Digest = projectChatManifestDigest(manifest)
	return manifest, nil
}

func (store *PostgresCanonicalStore) projectChatPersonForEmail(ctx context.Context, organizationID, email string) (string, error) {
	if store == nil || store.pool == nil || normalizeAccountEmail(email) == "" {
		return "", ErrProjectAuthorityDenied
	}
	accountDigest := sha256Hex([]byte(normalizeAccountEmail(email)))
	var personID string
	var count int
	err := store.pool.QueryRow(ctx, `SELECT min(mapping.person_id),count(DISTINCT mapping.person_id)
FROM stride_account_person_mappings_current mapping
JOIN stride_organization_memberships_current membership ON membership.person_id=mapping.person_id
WHERE mapping.account_subject_digest=decode($1,'hex') AND mapping.status='active'
  AND membership.organization_id=$2 AND membership.status='active'`, accountDigest, organizationID).Scan(&personID, &count)
	if errors.Is(err, pgx.ErrNoRows) || count != 1 || !strideIdentifier(personID) {
		return "", ErrProjectAuthorityDenied
	}
	return personID, err
}

// Parent preflight never guesses a canonical revision from legacy timestamps.
// The exact existing canonical event must match the current legacy snapshot;
// admission of a previously body-only parent belongs inside the group tx.
func (store *PostgresCanonicalStore) projectChatParentEventPreview(ctx context.Context, organizationID, threadID string, message scoutChatMessageRecord) (string, int64, string, error) {
	if store == nil || store.pool == nil {
		return "", 0, "", ErrProjectAuthorityDenied
	}
	eventID := projectChatID("conversation_event", organizationID, threadID, message.ID)
	var revision int64
	var digest string
	err := store.pool.QueryRow(ctx, `SELECT content_revision,encode(content_digest,'hex') FROM stride_conversation_events
WHERE tenant_id=$1 AND event_id=$2 AND thread_id=$3 AND source_id=$4 AND invalidated_at IS NULL`, organizationID, eventID, threadID, message.ID).Scan(&revision, &digest)
	wantDigest := sha256Hex([]byte(strings.TrimSpace(message.Text)))
	if errors.Is(err, pgx.ErrNoRows) {
		return eventID, 1, wantDigest, nil
	}
	if err != nil || digest != wantDigest {
		return "", 0, "", errHomeProjectStale
	}
	return eventID, revision, digest, nil
}

func projectChatManifestMatchesFiles(manifest projectChatSourceManifest, files []scoutChatFileAttachment, replyToMessageID string) bool {
	if len(manifest.Attachments) != len(files) || strings.TrimSpace(replyToMessageID) != func() string {
		if manifest.Reply == nil {
			return ""
		}
		return manifest.Reply.MessageID
	}() {
		return false
	}
	for index := range files {
		if manifest.Attachments[index].Ordinal != index || manifest.Attachments[index].SourceID != strings.TrimSpace(files[index].SourceID) ||
			manifest.Attachments[index].SourceRevision != strings.TrimSpace(files[index].SourceRevision) || manifest.Attachments[index].BlobRef != strings.TrimSpace(files[index].Ref) {
			return false
		}
	}
	return true
}

func projectChatManifestJournal(manifest projectChatSourceManifest) ([]scoutChatProjectAttachmentSource, *scoutChatProjectReplySource) {
	attachments := make([]scoutChatProjectAttachmentSource, 0, len(manifest.Attachments))
	for _, file := range manifest.Attachments {
		attachments = append(attachments, scoutChatProjectAttachmentSource{Ordinal: file.Ordinal, SourceID: file.SourceID,
			SourceRevision: file.SourceRevision, BlobRef: file.BlobRef, BlobDigest: file.BlobDigest, Mime: file.Mime, Size: file.Size,
			DestinationRevision: file.DestinationRevision, OriginFileID: file.OriginFileID, OriginRevision: file.OriginRevision})
	}
	var reply *scoutChatProjectReplySource
	if manifest.Reply != nil {
		reply = &scoutChatProjectReplySource{MessageID: manifest.Reply.MessageID, EventID: manifest.Reply.EventID,
			EventRevision: manifest.Reply.SourceRevision, EventDigest: manifest.Reply.SourceDigest, LegacyDigest: manifest.Reply.LegacyDigest,
			AuthorEmail: manifest.Reply.AuthorEmail, AuthorPersonID: manifest.Reply.AuthorPersonID, AudienceDigest: manifest.Reply.AudienceDigest,
			ACLRevision: manifest.Reply.ACLRevision, PurgeGeneration: manifest.Reply.PurgeGeneration}
	}
	return attachments, reply
}

func projectChatManifestFromJournal(thread scoutChatThreadRecord, operationID, text string, destination homeProjectDestination) (projectChatSourceManifest, bool) {
	for _, operation := range thread.ProjectLinkOperations {
		if operation.OperationID != operationID || !isHexDigest(operation.SourceManifestDigest) || !oneOf(operation.State, "pending", "confirmed", "drift_pending", "drifted") {
			continue
		}
		manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion, Destination: destination,
			TextDigest: sha256Hex([]byte(strings.TrimSpace(text)))}
		for _, file := range operation.AttachmentSources {
			manifest.Attachments = append(manifest.Attachments, projectChatManifestAttachment{Ordinal: file.Ordinal, SourceID: file.SourceID,
				SourceRevision: file.SourceRevision, BlobRef: file.BlobRef, BlobDigest: file.BlobDigest, Mime: file.Mime, Size: file.Size,
				DestinationRevision: file.DestinationRevision, OriginFileID: file.OriginFileID, OriginRevision: file.OriginRevision})
		}
		if reply := operation.ReplySource; reply != nil {
			manifest.Reply = &projectChatManifestReply{MessageID: reply.MessageID, EventID: reply.EventID, SourceRevision: reply.EventRevision,
				SourceDigest: reply.EventDigest, LegacyDigest: reply.LegacyDigest, AuthorEmail: reply.AuthorEmail, AuthorPersonID: reply.AuthorPersonID,
				AudienceDigest: reply.AudienceDigest, ACLRevision: reply.ACLRevision, PurgeGeneration: reply.PurgeGeneration}
		}
		manifest.Digest = projectChatManifestDigest(manifest)
		if manifest.Digest == operation.SourceManifestDigest {
			return manifest, true
		}
		return projectChatSourceManifest{}, false
	}
	return projectChatSourceManifest{}, false
}

func projectChatReplyJournalMatchesThread(reply *projectChatManifestReply, thread scoutChatThreadRecord) bool {
	if reply == nil {
		return true
	}
	index := scoutChatMessageIndex(thread, reply.MessageID)
	if index < 0 {
		return false
	}
	parent := thread.Messages[index]
	return normalizeAccountEmail(parent.AuthorEmail) == normalizeAccountEmail(reply.AuthorEmail) &&
		projectChatMessageSourceDigest(parent) == reply.LegacyDigest
}

func stableHomeProjectChoiceKey(key StrideE10TenantAuthorityEnvelopeKey, snapshot StrideE10TenantAuthoritySnapshot, kind string, project homeProjectRow) string {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte("home-project-choice/v1\x00"))
	_, _ = mac.Write([]byte(strings.Join([]string{homeProjectScopeKey(snapshot), strings.TrimSpace(kind), project.ID, fmt.Sprint(project.Revision), project.Digest, strings.ToLower(strings.TrimSpace(project.Title))}, "\x00")))
	return "project_choice_" + fmt.Sprintf("%x", mac.Sum(nil))[:32]
}

func sortedProjectChatPrincipals(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
