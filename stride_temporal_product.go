package main

// This file is the read-only product boundary over the E3 temporal reducer.
// It never calls a model. Room, sitting, media generation, audience and
// consent are derived from live server state; callers may choose only the
// bounded interval kind.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSTRIDETemporalProductDisabled = errors.New("STRIDE temporal recall is disabled")
	ErrSTRIDETemporalProductScope    = errors.New("STRIDE temporal recall scope is unauthorized")
	ErrSTRIDETemporalProductAudience = errors.New("STRIDE temporal recall audience is not safe to publish")
)

type STRIDETemporalRecallResult struct {
	Window              TemporalMeetingQueryKind `json:"window"`
	Text                string                   `json:"text"`
	RoomID              string                   `json:"roomId"`
	SittingID           string                   `json:"sittingId"`
	TranscriptHighWater uint64                   `json:"transcriptHighWater"`
	AnalysisHighWater   uint64                   `json:"analysisHighWater"`
	AnalysisFresh       bool                     `json:"analysisFresh"`
	Coverage            TemporalAnswerCoverage   `json:"coverage"`
	Evidence            []STRIDEReference        `json:"evidence"`
	EvidenceDigest      string                   `json:"evidenceDigest"`
}

func (app *kanbanBoardApp) requireSTRIDETemporalProduct() error {
	if app == nil || app.strideRuntime == nil {
		return ErrSTRIDETemporalProductDisabled
	}
	// Product reachability is admitted only through the same short-lived,
	// tenant+generation-bound MAC receipt as the other deterministic E6-E9
	// surfaces. The callback intentionally captures no provider handle.
	err := app.strideRuntime.WithProductContext(canonicalTenantID(), STRIDEProductScopeTemporal, func(ctx STRIDEProductContext) error {
		if ctx.Receipt.Mode != "deterministic_local" || ctx.Receipt.Scope != STRIDEProductScopeTemporal {
			return ErrSTRIDETemporalProductDisabled
		}
		return nil
	})
	if errors.Is(err, ErrSTRIDEProductDisabled) || errors.Is(err, ErrSTRIDERuntimeDisabled) || errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		return ErrSTRIDETemporalProductDisabled
	}
	return err
}

func parseSTRIDETemporalWindow(value string) (TemporalMeetingQueryKind, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "last_5_minutes", "5", "5m", "five_minutes":
		return TemporalQueryLastFiveMinutes, nil
	case "last_30_minutes", "30", "30m", "thirty_minutes":
		return TemporalQueryLastThirtyMinutes, nil
	default:
		return "", ErrTemporalBrainInvalid
	}
}

func (app *kanbanBoardApp) answerSTRIDETemporalForMember(ctx context.Context, user *userAccount, roomID string, kind TemporalMeetingQueryKind) (STRIDETemporalRecallResult, error) {
	if err := app.requireSTRIDETemporalProduct(); err != nil {
		return STRIDETemporalRecallResult{}, err
	}
	if user == nil {
		return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductScope
	}
	if strings.TrimSpace(roomID) == "" {
		var ok bool
		roomID, ok = app.activeMemberConsentRoom(user.Email)
		if !ok {
			return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductScope
		}
	}
	authority := &appMeetingSpecialistProductAuthority{app: app, runtime: app.strideRuntime}
	scope, err := authority.ResolveScope(ctx, user, roomID)
	if err != nil {
		return STRIDETemporalRecallResult{}, err
	}
	meeting, active := app.meetings.activeRecord(scope.RoomID)
	if !active || meeting.ID != scope.SittingID {
		return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductScope
	}
	startedAt, err := time.Parse(time.RFC3339Nano, meeting.StartedAt)
	if err != nil {
		return STRIDETemporalRecallResult{}, ErrTemporalBrainInvalid
	}
	config := TemporalMeetingBrainConfig{TenantID: scope.TenantID, RoomID: scope.RoomID, SittingID: scope.SittingID, SittingStart: startedAt.UTC()}
	requestedAt := time.Now().UTC()
	var answer TemporalMeetingAnswer
	err = app.strideRuntime.ReadTemporalMeetingBrain(scope.TenantID, scope.RoomID, scope.SittingID, func(brain *TemporalMeetingBrain) error {
		if brain.CurrentState().Config != config {
			return ErrTemporalBrainInvalid
		}
		intervals, resolveErr := brain.ResolveQuery(TemporalMeetingQuery{Kind: kind, AsOf: requestedAt, Timezone: "UTC", RequestedAt: requestedAt})
		if resolveErr != nil {
			return resolveErr
		}
		principal := ACLPrincipal{TenantID: scope.TenantID, ID: strideRuntimePrincipalForEmail(user.Email), Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}, RoomID: scope.RoomID, SittingID: scope.SittingID}
		answer, resolveErr = brain.Answer(principal, []string{"org_memory", "transcription", "model_analysis"}, intervals, requestedAt)
		return resolveErr
	})
	if err != nil {
		return STRIDETemporalRecallResult{}, err
	}
	if err := authority.ScopeCurrent(ctx, scope); err != nil {
		return STRIDETemporalRecallResult{}, err
	}
	return formatSTRIDETemporalRecall(kind, scope.RoomID, scope.SittingID, answer), nil
}

// answerSTRIDETemporalForRoom computes the answer independently for every
// current organization member and publishes only when the evidence set is
// identical for all of them. Guests or audience differences fail closed; a
// room answer must never widen one member's private recall authority.
func (app *kanbanBoardApp) answerSTRIDETemporalForRoom(ctx context.Context, scope RoomScoutScope, kind TemporalMeetingQueryKind) (STRIDETemporalRecallResult, error) {
	if err := app.requireSTRIDETemporalProduct(); err != nil {
		return STRIDETemporalRecallResult{}, err
	}
	if !scope.valid() {
		return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductScope
	}
	app.mu.Lock()
	live, found := app.roomLive[normalizeRoomID(scope.RoomID)]
	current := found && live.mediaActor != nil && live.mediaGen == scope.MediaGeneration && live.mediaSittingID == scope.SittingID
	participants := []string(nil)
	if current {
		participants = app.participantSnapshotLocked(live)
	}
	app.mu.Unlock()
	if !current || len(participants) == 0 {
		return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductScope
	}
	users := make([]*userAccount, 0, len(participants))
	seen := map[string]bool{}
	for _, participant := range participants {
		principal, ok := app.consentPrincipalForTranscriptSpeaker(scope.RoomID, participant)
		if !ok || principal.Kind != "user" {
			return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductAudience
		}
		email := normalizeAccountEmail(principal.ID)
		user := accountStore().findUser(email)
		if user == nil || seen[email] {
			if user == nil {
				return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductAudience
			}
			continue
		}
		seen[email] = true
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool {
		return normalizeAccountEmail(users[i].Email) < normalizeAccountEmail(users[j].Email)
	})
	var shared STRIDETemporalRecallResult
	for index, user := range users {
		answer, err := app.answerSTRIDETemporalForMember(ctx, user, scope.RoomID, kind)
		if err != nil {
			return STRIDETemporalRecallResult{}, err
		}
		if index == 0 {
			shared = answer
			continue
		}
		if answer.EvidenceDigest != shared.EvidenceDigest || answer.TranscriptHighWater != shared.TranscriptHighWater || answer.AnalysisHighWater != shared.AnalysisHighWater || strings.Join(answer.Coverage.Gaps, "\x00") != strings.Join(shared.Coverage.Gaps, "\x00") {
			return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductAudience
		}
	}
	if len(users) == 0 {
		return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductAudience
	}
	metadata := map[string]string{
		"strideTemporalWindow": string(kind), "evidenceDigest": shared.EvidenceDigest,
		"transcriptHighWater": strconv.FormatUint(shared.TranscriptHighWater, 10), "analysisHighWater": strconv.FormatUint(shared.AnalysisHighWater, 10),
	}
	ids := make([]string, 0, len(shared.Evidence))
	for _, reference := range shared.Evidence {
		ids = append(ids, reference.ID+"@"+strconv.FormatInt(reference.Revision, 10))
	}
	metadata["evidenceRefs"] = strings.Join(ids, ",")
	if _, ok := app.recordRoomChatMessageForScope(scope, scoutParticipantName, shared.Text, metadata); !ok {
		return STRIDETemporalRecallResult{}, ErrSTRIDETemporalProductScope
	}
	return shared, nil
}

func formatSTRIDETemporalRecall(kind TemporalMeetingQueryKind, roomID, sittingID string, answer TemporalMeetingAnswer) STRIDETemporalRecallResult {
	label := "Last 5 minutes"
	if kind == TemporalQueryLastThirtyMinutes {
		label = "Last 30 minutes"
	}
	lines := []string{label + ":"}
	if answer.AnalysisFresh && len(answer.Facts) > 0 {
		for _, fact := range answer.Facts {
			lines = append(lines, "• "+strings.TrimSpace(fact.Statement))
			if len(lines) == 7 {
				break
			}
		}
	} else {
		for _, source := range answer.Sources {
			if source.BodyOmitted || strings.TrimSpace(source.Text) == "" {
				continue
			}
			lines = append(lines, "• "+trimForStorage(strings.TrimSpace(source.Text), 700))
			if len(lines) == 7 {
				break
			}
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "I don't have an authorized transcript for that interval yet.")
	}
	if !answer.AnalysisFresh && len(answer.Sources) > 0 {
		lines = append(lines, "Analysis is still catching up, so this is transcript-backed.")
	}
	if len(answer.Coverage.Gaps) > 0 {
		lines = append(lines, "Coverage: "+strings.Join(answer.Coverage.Gaps, ", ")+".")
	}
	evidence := make([]STRIDEReference, 0, len(answer.Sources))
	seen := map[string]bool{}
	for _, source := range answer.Sources {
		key := referenceKey(source.Evidence)
		if source.Evidence.Validate() == nil && !seen[key] {
			seen[key] = true
			evidence = append(evidence, source.Evidence)
		}
	}
	evidence = SortedSTRIDEReferences(evidence)
	return STRIDETemporalRecallResult{
		Window: kind, Text: strings.Join(lines, "\n"), RoomID: roomID, SittingID: sittingID,
		TranscriptHighWater: answer.TranscriptHighWater, AnalysisHighWater: answer.AnalysisHighWater,
		AnalysisFresh: answer.AnalysisFresh, Coverage: answer.Coverage, Evidence: evidence, EvidenceDigest: answer.EvidenceDigest,
	}
}

func (result STRIDETemporalRecallResult) toolResult() map[string]any {
	refs := make([]map[string]any, 0, len(result.Evidence))
	for _, ref := range result.Evidence {
		refs = append(refs, map[string]any{"type": ref.ContractType, "id": ref.ID, "revision": ref.Revision, "digest": ref.Digest})
	}
	return map[string]any{
		"ok": result.TranscriptHighWater > 0, "text": result.Text, "window": result.Window,
		"roomId": result.RoomID, "sittingId": result.SittingID, "analysisFresh": result.AnalysisFresh,
		"transcriptHighWater": result.TranscriptHighWater, "analysisHighWater": result.AnalysisHighWater,
		"coverage": result.Coverage, "evidence": refs, "evidenceDigest": result.EvidenceDigest,
	}
}

func strideTemporalRecallToolDefinition() map[string]any {
	return map[string]any{
		"type": "function", "name": "meeting_interval_recall",
		"description": "Answer what happened in exactly the last 5 or 30 minutes of the current meeting from authorized transcript and fresh analysis evidence. Use this instead of a whole-meeting recap when the person names one of those intervals.",
		"parameters": map[string]any{
			"type": "object", "properties": map[string]any{
				"window": map[string]any{"type": "string", "enum": []string{"last_5_minutes", "last_30_minutes"}},
			}, "required": []string{"window"}, "additionalProperties": false,
		},
	}
}

func (app *kanbanBoardApp) currentSTRIDERoomScope(roomID string) (RoomScoutScope, error) {
	roomID = normalizeRoomID(roomID)
	meeting, active := app.meetings.activeRecord(roomID)
	if !active {
		return RoomScoutScope{}, ErrSTRIDETemporalProductScope
	}
	app.mu.Lock()
	live, found := app.roomLive[roomID]
	scope := RoomScoutScope{}
	if found && live.mediaActor != nil && live.mediaGen > 0 && live.mediaSittingID == meeting.ID {
		scope = RoomScoutScope{RoomID: roomID, SittingID: meeting.ID, MediaGeneration: live.mediaGen}
	}
	app.mu.Unlock()
	if !scope.valid() {
		return RoomScoutScope{}, ErrSTRIDETemporalProductScope
	}
	return scope, nil
}

func temporalProductErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrSTRIDETemporalProductDisabled):
		return "Exact meeting recall is not enabled yet."
	case errors.Is(err, ErrBrainRetrievalUnavailable):
		return "The authorized meeting transcript is not available yet."
	case errors.Is(err, ErrSTRIDETemporalProductAudience):
		return "I can't safely share that interval with the whole room."
	default:
		return fmt.Sprintf("Exact meeting recall is unavailable: %s", "room or consent changed")
	}
}
