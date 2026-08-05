package main

// Scout may recommend a specialist, but that recommendation is never meeting
// authority. A human still creates and revision-approves the invitation through
// the existing product endpoints before any qualified provider can join.

import (
	"context"
	"strings"
)

type MeetingSpecialistRecommendation struct {
	AgentID                string `json:"agentId"`
	DisplayName            string `json:"displayName"`
	PurposeSummary         string `json:"purposeSummary"`
	Reason                 string `json:"reason"`
	RequiresHumanApproval  bool   `json:"requiresHumanApproval"`
	ProviderReady          bool   `json:"providerReady"`
	ProviderReadinessState string `json:"providerReadinessState"`
}

// RecommendColtonForResearch is the shared direct/Scout-assisted seam. It is
// deliberately read-only and deterministic: it can surface Colton only when
// the current human can already see an exact, room-assigned Colton candidate.
func (product *MeetingSpecialistProduct) RecommendColtonForResearch(ctx context.Context, user *userAccount, roomID, purpose string) (MeetingSpecialistRecommendation, error) {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || !meetingSpecialistResearchIntent(purpose) {
		return MeetingSpecialistRecommendation{}, ErrMeetingSpecialistProductDecision
	}
	status := product.Status(ctx, user, roomID)
	if !status.CanInvite {
		return MeetingSpecialistRecommendation{}, ErrMeetingSpecialistProductAgent
	}
	for _, candidate := range status.Candidates {
		if candidate.AgentID != coltonMeetingSpecialistAgentID {
			continue
		}
		readiness := status.Reason
		if status.Available {
			readiness = "qualified"
		}
		return MeetingSpecialistRecommendation{
			AgentID: candidate.AgentID, DisplayName: candidate.DisplayName,
			PurposeSummary: trimForStorage(purpose, 240), Reason: "research_fit",
			RequiresHumanApproval: true, ProviderReady: status.Available,
			ProviderReadinessState: readiness,
		}, nil
	}
	return MeetingSpecialistRecommendation{}, ErrMeetingSpecialistProductAgent
}

func meetingSpecialistResearchIntent(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(lower, "colton") {
		return true
	}
	for _, signal := range []string{"research", "investigate", "source", "evidence", "competitive", "competitor", "market landscape", "fact-check", "fact check", "verify", "brief"} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}
