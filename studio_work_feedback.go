package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Human review is durable work state, not an implicit taste signal, a machine
// quality gate, publication authority, or organizational knowledge.
const meetingMemoryKindWorkReview = "work_review"

type studioWorkResultIdentity struct {
	ArtifactID string `json:"artifactId"`
	Version    int    `json:"version"`
	Digest     string `json:"digest"`
}

type studioWorkFeedbackRequest struct {
	Type             string                   `json:"type"`
	Verdict          string                   `json:"verdict"`
	Note             string                   `json:"note"`
	IdempotencyKey   string                   `json:"idempotencyKey"`
	Result           studioWorkResultIdentity `json:"result"`
	AcceptedReviewID string                   `json:"acceptedReviewId,omitempty"`
}

type studioWorkFeedbackEvent struct {
	ID               string                   `json:"id"`
	RootID           string                   `json:"rootId"`
	Type             string                   `json:"type"`
	Verdict          string                   `json:"verdict"`
	Note             string                   `json:"note,omitempty"`
	Result           studioWorkResultIdentity `json:"result"`
	ActorID          string                   `json:"actorId"`
	ActorName        string                   `json:"actorName"`
	At               time.Time                `json:"at"`
	AcceptedReviewID string                   `json:"acceptedReviewId,omitempty"`
	RequestDigest    string                   `json:"-"`
}

type studioWorkFeedbackView struct {
	ReviewState       string                    `json:"reviewState"`
	CurrentReview     *studioWorkFeedbackEvent  `json:"currentReview,omitempty"`
	CurrentOutcome    *studioWorkFeedbackEvent  `json:"currentOutcome,omitempty"`
	History           []studioWorkFeedbackEvent `json:"history"`
	HistoryTruncated  bool                      `json:"historyTruncated"`
	CanReview         bool                      `json:"canReview"`
	CanObserveOutcome bool                      `json:"canObserveOutcome"`
}

func studioWorkCurrentResult(project studioProjectView) studioWorkResultIdentity {
	if project.Result == nil {
		return studioWorkResultIdentity{}
	}
	return studioWorkResultIdentity{ArtifactID: project.Result.ArtifactID, Version: project.Result.Version, Digest: project.Result.Digest}
}

func studioWorkResultIdentityCurrent(entry meetingMemoryEntry, result studioWorkResultIdentity) bool {
	return entry.ID == result.ArtifactID && artifactVersion(entry) == result.Version && artifactCapabilityDigest(entry) == result.Digest
}

func studioWorkResultIdentityKnown(entry meetingMemoryEntry, result studioWorkResultIdentity) bool {
	if studioWorkResultIdentityCurrent(entry, result) {
		return true
	}
	if entry.ID != result.ArtifactID {
		return false
	}
	for _, prior := range artifactVersionHistory(entry) {
		if prior.V == result.Version && prior.ContentDigest == result.Digest {
			return true
		}
	}
	return false
}

func studioWorkProducedResult(entry meetingMemoryEntry) bool {
	return oneOf(artifactStatus(entry), artifactStatusComplete, artifactStatusApproved, artifactStatusPublished, artifactStatusGated)
}

func decodeStudioWorkFeedback(entry meetingMemoryEntry) (studioWorkFeedbackEvent, bool) {
	var event studioWorkFeedbackEvent
	if entry.Kind != meetingMemoryKindWorkReview || json.Unmarshal([]byte(entry.Text), &event) != nil || event.ID != entry.ID || event.RootID != entry.Metadata["workRootId"] {
		return event, false
	}
	event.RequestDigest = entry.Metadata["requestDigest"]
	return event, event.ID != "" && event.Result.ArtifactID != "" && event.Result.Version > 0 && isHexDigest(event.Result.Digest)
}

func studioWorkFeedbackForViewer(ctx context.Context, viewer *userAccount, root meetingMemoryEntry, project studioProjectView) *studioWorkFeedbackView {
	view := &studioWorkFeedbackView{ReviewState: "unreviewed", History: []studioWorkFeedbackEvent{}}
	if kanbanApp == nil || kanbanApp.memory == nil || viewer == nil || !kanbanApp.projectBoundArtifactCurrent(ctx, root) {
		return view
	}
	if _, ok := authorizedArtifactByID(ctx, viewer, ACLReadContent, root.ID); !ok {
		return view
	}
	current := studioWorkCurrentResult(project)
	currentArtifact, currentReadable := authorizedArtifactByID(ctx, viewer, ACLReadContent, current.ArtifactID)
	view.CanReview = currentReadable && studioWorkProducedResult(currentArtifact) && studioWorkResultIdentityCurrent(currentArtifact, current) && kanbanApp.artifactAuthorized(ctx, viewer, ACLWrite, root)
	rows := kanbanApp.memory.entriesOfKindByMetadata(meetingMemoryKindWorkReview, "workRootId", root.ID)
	// Authorize each distinct result once; the same artifact may have many
	// immutable review revisions. Neither root access nor an old review grants
	// access to a result whose own ACL/source has since changed.
	readable := map[string]meetingMemoryEntry{}
	checked := map[string]bool{}
	for _, row := range rows {
		event, ok := decodeStudioWorkFeedback(row)
		if !ok {
			continue
		}
		// Intersect today's Work access with the captured audience. Sharing or
		// reassigning a result later never publishes a previously private note.
		captured := artifactAuthorizationHeaderFromEntry(root)
		captured.TenantID = row.Metadata["tenantId"]
		captured.Visibility, captured.OwnerEmail, captured.OriginSurface = row.Metadata["visibility"], row.Metadata["ownerEmail"], row.Metadata["originSurface"]
		captured.ACLVersion, _ = strconv.ParseInt(row.Metadata["rootACLVersion"], 10, 64)
		if captured.ACLVersion < 1 || !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, captured) {
			continue
		}
		if !checked[event.Result.ArtifactID] {
			checked[event.Result.ArtifactID] = true
			artifact, found := authorizedArtifactByID(ctx, viewer, ACLReadContent, event.Result.ArtifactID)
			if found && kanbanApp.projectBoundArtifactCurrent(ctx, artifact) {
				readable[event.Result.ArtifactID] = artifact
			}
		}
		artifact, ok := readable[event.Result.ArtifactID]
		if !ok || !studioWorkResultIdentityKnown(artifact, event.Result) {
			continue
		}
		capturedResult := artifactAuthorizationHeaderFromEntry(artifact)
		capturedResult.TenantID = row.Metadata["resultTenantId"]
		capturedResult.Visibility, capturedResult.OwnerEmail, capturedResult.OriginSurface = row.Metadata["resultVisibility"], row.Metadata["resultOwnerEmail"], row.Metadata["resultOriginSurface"]
		capturedResult.ACLVersion, _ = strconv.ParseInt(row.Metadata["resultACLVersion"], 10, 64)
		if capturedResult.ACLVersion < 1 || !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, capturedResult) {
			continue
		}
		view.History = append(view.History, event)
		if event.Result == current {
			copy := event
			if event.Type == "review" {
				view.CurrentReview = &copy
				view.CurrentOutcome = nil
			}
			if event.Type == "outcome" && view.CurrentReview != nil && event.AcceptedReviewID == view.CurrentReview.ID {
				view.CurrentOutcome = &copy
			}
		}
	}
	if view.CurrentReview != nil {
		view.ReviewState = view.CurrentReview.Verdict
	}
	view.CanObserveOutcome = view.CanReview && view.ReviewState == "accepted"
	if len(view.History) > 100 {
		view.HistoryTruncated = true
		view.History = view.History[len(view.History)-100:]
	}
	return view
}

func studioWorkFeedbackActor(ctx context.Context, viewer *userAccount) (string, string) {
	if principal, ok := strideE10TenantPrincipalFromContext(ctx); ok {
		return principal.PersonID, principal.TenantID
	}
	return normalizeAccountEmail(viewer.Email), ""
}

func studioWorkFeedbackHandler(w http.ResponseWriter, r *http.Request, viewer *userAccount, rootID string, expectedRevision int, payload studioWorkFeedbackRequest) {
	payload.Note = strings.TrimSpace(payload.Note)
	payload.IdempotencyKey = strings.TrimSpace(payload.IdempotencyKey)
	if rootID == "" || expectedRevision < 1 || len(payload.IdempotencyKey) < 8 || len(payload.IdempotencyKey) > 128 || utf8.RuneCountInString(payload.Note) > 2000 ||
		payload.Result.ArtifactID == "" || payload.Result.Version < 1 || !isHexDigest(payload.Result.Digest) ||
		(payload.Type != "review" && payload.Type != "outcome") ||
		(payload.Type == "review" && (!oneOf(payload.Verdict, "accepted", "revision_requested") || payload.AcceptedReviewID != "" || (payload.Verdict == "revision_requested" && payload.Note == ""))) ||
		(payload.Type == "outcome" && (!oneOf(payload.Verdict, "helped", "did_not_help", "inconclusive") || payload.AcceptedReviewID == "")) {
		writeAuthError(w, http.StatusBadRequest, "provide an exact result, idempotency key, valid review or outcome, and a reason when requesting revision")
		return
	}
	root, found := authorizedArtifactByID(r.Context(), viewer, ACLWrite, rootID)
	if !found || !kanbanApp.projectBoundArtifactCurrent(r.Context(), root) {
		writeAuthError(w, http.StatusNotFound, "work not found")
		return
	}
	candidates, index := studioProjectProjectionDirectory()
	var project studioProjectView
	projectFound := false
	for _, candidate := range authorizedStudioProjectCandidates(r.Context(), viewer, candidates) {
		if candidate.Entry.ID == rootID {
			project, projectFound = studioProjectViewForCandidate(r.Context(), viewer, candidate, index)
			break
		}
	}
	if !projectFound {
		writeAuthError(w, http.StatusNotFound, "work not found")
		return
	}
	result, found := authorizedArtifactByID(r.Context(), viewer, ACLReadContent, payload.Result.ArtifactID)
	if !found || !kanbanApp.projectBoundArtifactCurrent(r.Context(), result) || !studioWorkResultIdentityKnown(result, payload.Result) {
		writeAuthError(w, http.StatusConflict, "the exact result changed or is no longer available")
		return
	}
	actor, tenant := studioWorkFeedbackActor(r.Context(), viewer)
	requestBytes, _ := json.Marshal(struct {
		RootID   string
		Revision int
		Actor    string
		Payload  studioWorkFeedbackRequest
	}{rootID, expectedRevision, actor, payload})
	event := studioWorkFeedbackEvent{ID: "work-review-" + sha256Hex([]byte(tenant + "\x00" + rootID + "\x00" + actor + "\x00" + payload.IdempotencyKey))[:32], RootID: rootID,
		Type: payload.Type, Verdict: payload.Verdict, Note: payload.Note, Result: payload.Result, ActorID: actor, ActorName: viewer.Name, At: time.Now().UTC(),
		AcceptedReviewID: payload.AcceptedReviewID, RequestDigest: sha256Hex(requestBytes)}
	rootHeader, rootOK := kanbanApp.memory.artifactAuthorizationHeaderByID(root.ID)
	resultHeader, resultOK := kanbanApp.memory.artifactAuthorizationHeaderByID(result.ID)
	if !rootOK || !resultOK || rootHeader.ContentRevision != int64(artifactVersion(root)) || resultHeader.ContentRevision != int64(artifactVersion(result)) ||
		rootHeader.ContentDigest != artifactCapabilityDigest(root) || resultHeader.ContentDigest != artifactCapabilityDigest(result) ||
		!artifactHeaderAuthorized(r.Context(), viewer, ACLWrite, rootHeader) || !artifactHeaderAuthorized(r.Context(), viewer, ACLReadContent, resultHeader) {
		writeAuthError(w, http.StatusConflict, "work changed; reload and try again")
		return
	}
	var saved studioWorkFeedbackEvent
	var replayed bool
	err := kanbanApp.withCurrentAgentThreadSource(scoutAgentThread{Artifact: root}, func() error {
		var appendErr error
		saved, replayed, appendErr = kanbanApp.memory.appendStudioWorkFeedback(event, tenant, root, result, rootHeader, resultHeader, expectedRevision, project)
		return appendErr
	})
	if err != nil {
		writeAuthError(w, http.StatusConflict, err.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "replayed": replayed, "event": saved, "feedback": studioWorkFeedbackForViewer(r.Context(), viewer, root, project), "rerunStarted": false})
}

// All validation below uses already-authorized snapshots and direct locked
// fields. No authorizer or public store reader is called while holding mu.
func (store *meetingMemoryStore) appendStudioWorkFeedback(event studioWorkFeedbackEvent, tenant string, root, result meetingMemoryEntry, rootHeader, resultHeader ArtifactAuthorizationHeader, expectedRevision int, project studioProjectView) (studioWorkFeedbackEvent, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, expected := range []ArtifactAuthorizationHeader{rootHeader, resultHeader} {
		position, found := store.artifactEntryIndexByIDLocked(expected.ObjectID)
		if !found || memoryEntryHiddenFromRecall(store.entries[position]) {
			return event, false, errors.New("work or result no longer available")
		}
		current := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(store.entries[position]))
		if !artifactAuthorizationHeaderEqual(expected, current) {
			return event, false, errors.New("work or result changed; reload and try again")
		}
		if expected.ObjectID == root.ID && studioProjectRevision(store.entries[position]) != studioProjectRevision(root) {
			return event, false, errors.New("work changed; reload and try again")
		}
	}
	var latestReview *studioWorkFeedbackEvent
	for _, row := range store.entries {
		if row.Kind != meetingMemoryKindWorkReview || row.Metadata["workRootId"] != event.RootID {
			continue
		}
		prior, ok := decodeStudioWorkFeedback(row)
		if !ok {
			return event, false, errors.New("work review history is unavailable")
		}
		if prior.ID == event.ID {
			if prior.RequestDigest != event.RequestDigest {
				return event, false, errors.New("idempotency key was already used for different feedback")
			}
			return prior, true, nil
		}
		if prior.Type == "review" && prior.Result == event.Result {
			copy := prior
			latestReview = &copy
		}
	}
	if studioProjectRevision(root) != expectedRevision {
		return event, false, errors.New("work changed; reload and try again")
	}
	if event.Type == "review" && (!studioWorkProducedResult(result) || studioWorkCurrentResult(project) != event.Result || !studioWorkResultIdentityCurrent(result, event.Result)) {
		return event, false, errors.New("review requires the exact current produced result")
	}
	if event.Type == "outcome" && (latestReview == nil || latestReview.Verdict != "accepted" || latestReview.ID != event.AcceptedReviewID) {
		return event, false, errors.New("an outcome must reference the latest acceptance of this exact result")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return event, false, err
	}
	entry := meetingMemoryEntry{ID: event.ID, Kind: meetingMemoryKindWorkReview, Text: string(raw), CreatedAt: event.At,
		Metadata: map[string]string{"workRootId": root.ID, "artifactId": result.ID, "requestDigest": event.RequestDigest, "tenantId": rootHeader.TenantID, "rootACLVersion": strconv.FormatInt(rootHeader.ACLVersion, 10),
			"resultTenantId": resultHeader.TenantID, "resultVisibility": resultHeader.Visibility, "resultOwnerEmail": resultHeader.OwnerEmail, "resultOriginSurface": resultHeader.OriginSurface, "resultACLVersion": strconv.FormatInt(resultHeader.ACLVersion, 10),
			"visibility": rootHeader.Visibility, "ownerEmail": rootHeader.OwnerEmail, "originSurface": rootHeader.OriginSurface}}
	line, err := json.Marshal(entry)
	if err != nil {
		return event, false, err
	}
	// Human judgments are acknowledged only after durable append, including
	// when the legacy general memory writer uses best-effort persistence.
	if err := appendFileDurably(store.path, append(line, '\n'), 0600); err != nil {
		return event, false, errors.New("could not save work feedback")
	}
	store.entries = append(store.entries, entry)
	store.indexMeetingEntryLocked(len(store.entries)-1, entry)
	store.seen[entry.ID] = struct{}{}
	return event, false, nil
}
