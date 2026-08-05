package main

// Scout can manage the specialist roster conversationally, but never owns the
// human's meeting authority. This seam accepts only a server-attributed human
// and an invocation-gated Scout turn. A research discussion can yield a
// recommendation; an explicit human invitation request can create the same
// visible, revision-bound pending invitation as the direct product endpoint.
// Neither path approves an invitation or starts a provider session.

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrMeetingSpecialistScoutInvocation = errors.New("meeting specialist Scout turn was not invocation gated")

type MeetingSpecialistScoutResearchTurn struct {
	RoomID         string
	Utterance      string
	Purpose        string
	Addressed      bool
	IdempotencyKey string
	InvitationTTL  time.Duration
}

type MeetingSpecialistScoutResearchResult struct {
	Action                 string
	Recommendation         *MeetingSpecialistRecommendation
	Invitation             *meetingSpecialistInvitationView
	RequiresHumanApproval  bool
	InvitationCreated      bool
	ProviderSessionStarted bool
}

// HandleScoutManagedResearchTurn is intentionally narrower than a general
// natural-language router. The caller must prove that Scout's existing
// invocation gate admitted this attributed human turn. Merely mentioning
// research in ambient room speech therefore cannot create a recommendation or
// invitation.
func (product *MeetingSpecialistProduct) HandleScoutManagedResearchTurn(ctx context.Context, user *userAccount, turn MeetingSpecialistScoutResearchTurn) (MeetingSpecialistScoutResearchResult, error) {
	if !turn.Addressed {
		return MeetingSpecialistScoutResearchResult{}, ErrMeetingSpecialistScoutInvocation
	}
	utterance := strings.TrimSpace(turn.Utterance)
	purpose := strings.TrimSpace(turn.Purpose)
	if purpose == "" {
		purpose = utterance
	}
	recommendation, err := product.RecommendColtonForResearch(ctx, user, turn.RoomID, purpose)
	if err != nil {
		return MeetingSpecialistScoutResearchResult{}, err
	}
	result := MeetingSpecialistScoutResearchResult{
		Action: "recommend_colton", Recommendation: &recommendation,
		RequiresHumanApproval: true,
	}
	if !meetingSpecialistExplicitColtonInvite(utterance) {
		return result, nil
	}
	if strings.TrimSpace(turn.IdempotencyKey) == "" || turn.InvitationTTL <= 0 {
		return MeetingSpecialistScoutResearchResult{}, ErrMeetingSpecialistProductDecision
	}
	invitation, err := product.Request(ctx, user, turn.RoomID, coltonMeetingSpecialistAgentID, purpose, turn.IdempotencyKey, turn.InvitationTTL)
	if err != nil {
		return MeetingSpecialistScoutResearchResult{}, err
	}
	result.Action = "colton_invitation_pending"
	result.Invitation = &invitation
	result.InvitationCreated = true
	// Request always stops at the separate, revision-bound human approval
	// decision. Assert the invariant here so future product changes fail closed.
	if invitation.Status != "awaiting_approval" || invitation.ProviderSessionStarted {
		return MeetingSpecialistScoutResearchResult{}, ErrMeetingSpecialistProductDecision
	}
	return result, nil
}

func meetingSpecialistExplicitColtonInvite(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if !strings.Contains(lower, "colton") {
		return false
	}
	for _, signal := range []string{"invite", "bring", "add", "join"} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}
