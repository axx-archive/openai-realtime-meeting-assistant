package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrCatchUpUnavailable  = errors.New("exact catch-up is unavailable")
	ErrCatchUpUnauthorized = errors.New("exact catch-up requires an admitted organization member")
	ErrCatchUpStale        = errors.New("exact catch-up room or sitting binding is stale")
)

const catchUpSettleDelay = 8 * time.Second

// CatchUpRecapResolver is the production seam into W2 full-range retrieval.
// Implementations must enforce canonical authorization before fetching bodies;
// the returned snapshot and coverage are revalidated again here before any
// recap text is exposed.
type CatchUpRecapResolver interface {
	ResolveCatchUp(context.Context, BrainRetrievalRequest) (BrainRetrievalResult, error)
	CommitCatchUpPublication(context.Context, ACLPrincipal, RetrievalSnapshot, func() error) error
}

type catchUpRecapResolverFunc func(context.Context, BrainRetrievalRequest) (BrainRetrievalResult, error)

func (fn catchUpRecapResolverFunc) ResolveCatchUp(ctx context.Context, request BrainRetrievalRequest) (BrainRetrievalResult, error) {
	return fn(ctx, request)
}

func (fn catchUpRecapResolverFunc) CommitCatchUpPublication(_ context.Context, principal ACLPrincipal, snapshot RetrievalSnapshot, publish func() error) error {
	// Function adapters are test-only. They still fence exact principal and
	// snapshot identity; production adapters must re-run canonical ACL + purge
	// authorization for every source revision around the publication callback.
	if snapshot.PrincipalKind != principal.Kind || snapshot.PrincipalID != principal.ID || snapshot.TenantID != principal.TenantID {
		return ErrCatchUpUnauthorized
	}
	if publish == nil {
		return ErrCatchUpUnavailable
	}
	return publish()
}

type CatchUpRecapEvidence struct {
	EvidenceID string           `json:"evidenceId"`
	Evidence   BrainEvidenceRef `json:"evidence"`
}

type CatchUpRecapResponse struct {
	OK        bool                   `json:"ok"`
	RoomID    string                 `json:"roomId"`
	SittingID string                 `json:"sittingId"`
	Headline  string                 `json:"headline"`
	Recap     string                 `json:"recap"`
	Temporal  TemporalQuery          `json:"temporal"`
	Snapshot  RetrievalSnapshot      `json:"snapshot"`
	Coverage  RecallCoverage         `json:"coverage"`
	Evidence  []CatchUpRecapEvidence `json:"evidence"`
}

func (app *kanbanBoardApp) exactCatchUpRecap(ctx context.Context, requesterEmail, roomID, focus string) (CatchUpRecapResponse, error) {
	return app.exactCatchUpRecapWithComposer(ctx, requesterEmail, roomID, focus, app.configuredCatchUpComposer())
}

func (app *kanbanBoardApp) exactCatchUpRecapWithComposer(ctx context.Context, requesterEmail, roomID, focus string, composer catchUpComposerProvider) (CatchUpRecapResponse, error) {
	var response CatchUpRecapResponse
	if app == nil || app.memory == nil || app.meetings == nil || app.admissionAnchors == nil || app.catchUpRecapResolver == nil {
		return response, ErrCatchUpUnavailable
	}
	requesterEmail = normalizeAccountEmail(requesterEmail)
	if requesterEmail == "" || accountStore().findUser(requesterEmail) == nil {
		return response, ErrCatchUpUnauthorized
	}
	roomID = normalizeRoomID(roomID)
	sittingID := strings.TrimSpace(app.memory.currentMeetingID(roomID))
	if sittingID == "" {
		return response, ErrCatchUpStale
	}
	record, ok := app.meetings.recordByID(sittingID)
	if !ok || strings.TrimSpace(record.EndedAt) != "" || meetingRoomID(record) != roomID {
		return response, ErrCatchUpStale
	}
	startedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(record.StartedAt))
	if err != nil {
		return response, ErrCatchUpStale
	}
	anchor, found, err := app.admissionAnchors.Lookup(ctx, canonicalTenantID(), roomID, sittingID, memberAdmissionPrincipal(requesterEmail))
	if err != nil {
		return response, fmt.Errorf("%w: admission anchor lookup", ErrCatchUpUnavailable)
	}
	if !found {
		return response, ErrCatchUpUnauthorized
	}
	if !startedAt.Before(anchor.AdmittedAt) {
		return response, fmt.Errorf("%w: no material predates first admission", ErrCatchUpUnavailable)
	}
	temporal, err := NewBeforeAdmissionTemporalQuery(anchor, startedAt, meetingTimeLocation().String(), catchUpSettleDelay,
		"captured meeting material before this member's first durable admission")
	if err != nil {
		return response, fmt.Errorf("%w: temporal boundary", ErrCatchUpUnavailable)
	}
	query := "Catch me up on the captured material before I first joined this sitting."
	if focus = strings.TrimSpace(focus); focus != "" {
		query += " Focus on: " + focus
	}
	principal := ACLPrincipal{
		TenantID: canonicalTenantID(), ID: requesterEmail, Kind: ACLPrincipalUser,
		TeamIDs: []string{"organization"}, RoomID: roomID, SittingID: sittingID,
	}
	if wait := time.Until(temporal.SettleUntil); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return response, fmt.Errorf("%w: settle window canceled", ErrCatchUpUnavailable)
		case <-timer.C:
		}
	}
	retrieval, err := app.catchUpRecapResolver.ResolveCatchUp(ctx, BrainRetrievalRequest{Principal: principal, Query: query, Temporal: temporal})
	if err != nil {
		return response, fmt.Errorf("%w: %v", ErrCatchUpUnavailable, err)
	}
	if err := validateCatchUpRetrieval(retrieval, principal, temporal); err != nil {
		return response, err
	}

	recap, headline, evidence, err := composeCatchUpWithOptionalProvider(ctx, retrieval, composer)
	if err != nil {
		return response, err
	}
	if err := ctx.Err(); err != nil {
		return response, fmt.Errorf("%w: composition canceled", ErrCatchUpUnavailable)
	}
	// Response construction is a conditional publication seam, separate from
	// prompt construction. Production keeps exact consent, canonical, purge,
	// and authoritative-body fences held until the response is committed.
	if err := app.catchUpRecapResolver.CommitCatchUpPublication(ctx, principal, retrieval.Snapshot, func() error {
		response = CatchUpRecapResponse{
			OK: true, RoomID: roomID, SittingID: sittingID, Headline: headline, Recap: recap,
			Temporal: temporal, Snapshot: retrieval.Snapshot, Coverage: retrieval.Coverage, Evidence: evidence,
		}
		return nil
	}); err != nil {
		return response, fmt.Errorf("%w: publication reauthorization", ErrCatchUpUnavailable)
	}
	return response, nil
}

func validateCatchUpRetrieval(result BrainRetrievalResult, principal ACLPrincipal, temporal TemporalQuery) error {
	if result.Snapshot.Validate() != nil || result.Coverage.Validate() != nil || result.Coverage.SnapshotID != result.Snapshot.SnapshotID ||
		result.Snapshot.PrincipalKind != ACLPrincipalUser || result.Snapshot.PrincipalID != principal.ID || result.Snapshot.TenantID != principal.TenantID {
		return fmt.Errorf("%w: invalid retrieval proof", ErrCatchUpUnavailable)
	}
	left, leftErr := canonicalJSON(result.Snapshot.Temporal)
	right, rightErr := canonicalJSON(temporal)
	if leftErr != nil || rightErr != nil || string(left) != string(right) || !result.Coverage.AdmissionRelative ||
		result.Coverage.CaptureSequenceCutoff != temporal.CaptureSequenceCutoff {
		return fmt.Errorf("%w: retrieval crossed the admission boundary", ErrCatchUpUnavailable)
	}
	return nil
}

// composeEvidenceLinkedCatchUp is deliberately extractive: every visible
// bullet is copied from one authorized primary body and carries its evidence
// id. A future model composer may improve prose only if it returns the same
// claim/evidence structure and passes the same publication reauthorization.
func composeEvidenceLinkedCatchUp(result BrainRetrievalResult) (string, string, []CatchUpRecapEvidence, error) {
	snapshotEvidence := make(map[string]BrainEvidenceRef, len(result.Snapshot.Sources))
	for _, source := range result.Snapshot.Sources {
		snapshotEvidence[source.EvidenceID] = source.Evidence
	}
	type line struct {
		id, text string
		occurred time.Time
	}
	lines := make([]line, 0, len(result.Sources))
	for _, source := range result.Sources {
		ref, ok := snapshotEvidence[source.EvidenceID]
		if !ok || !sameBrainEvidenceRef(ref, source.Evidence) ||
			(source.Status != RecallSourceFresh && source.Status != RecallSourcePartial) || strings.TrimSpace(source.Body) == "" {
			continue
		}
		text := strings.Join(strings.Fields(source.Body), " ")
		lines = append(lines, line{id: source.EvidenceID, text: text, occurred: source.Evidence.OccurredStart})
	}
	if len(lines) == 0 {
		return "", "", nil, fmt.Errorf("%w: no authorized captured material", ErrCatchUpUnavailable)
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if !lines[i].occurred.Equal(lines[j].occurred) {
			return lines[i].occurred.Before(lines[j].occurred)
		}
		return lines[i].id < lines[j].id
	})
	evidence := make([]CatchUpRecapEvidence, 0, len(lines))
	seen := map[string]bool{}
	var recap strings.Builder
	statusLine := "Coverage: " + string(result.Coverage.Status)
	if reason := strings.TrimSpace(result.Coverage.Reason); reason != "" {
		statusLine += " — " + reason
	}
	recap.WriteString(statusLine)
	for _, item := range lines {
		recap.WriteString("\n- ")
		recap.WriteString(item.text)
		recap.WriteString(" [evidence:")
		recap.WriteString(item.id)
		recap.WriteString("]")
		if !seen[item.id] {
			seen[item.id] = true
			evidence = append(evidence, CatchUpRecapEvidence{EvidenceID: item.id, Evidence: snapshotEvidence[item.id]})
		}
	}
	headline := fmt.Sprintf("I found %d authorized pre-join source%s; coverage is %s.", len(lines), catchUpPluralSuffix(len(lines)), result.Coverage.Status)
	return recap.String(), headline, evidence, nil
}

func catchUpPluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (response CatchUpRecapResponse) toolResult() map[string]any {
	return map[string]any{
		"ok": response.OK, "roomId": response.RoomID, "sittingId": response.SittingID,
		"headline": response.Headline, "recap": response.Recap, "temporal": response.Temporal,
		"snapshot": response.Snapshot, "coverage": response.Coverage, "evidence": response.Evidence,
		"audience": notificationAudienceMe,
	}
}
