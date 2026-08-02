package main

import (
	"strings"
)

const meetingSpecialistAssignmentRole = "meeting_specialist"

type meetingSpecialistEligibilityMaterial struct {
	AgentID                 string          `json:"agentId"`
	RoomID                  string          `json:"roomId"`
	ProductAgentRevision    int64           `json:"productAgentRevision"`
	WorkforceRevisionDigest string          `json:"workforceRevisionDigest"`
	Assignment              STRIDEReference `json:"assignment"`
	Profile                 STRIDEReference `json:"profile"`
	Capability              STRIDEReference `json:"capability"`
}

// meetingSpecialistCandidateForScope joins the two canonical authorities
// without letting either one widen the other:
//   - Product owns the human-configured room membership and assignment revision.
//   - Workforce owns the current lifecycle, profile, and capability state.
//
// Hiring alone therefore never grants meeting access. Generic memberships such
// as organization, team, or meeting are intentionally ignored.
func meetingSpecialistCandidateForScope(agent STRIDEProductTeamAgent, seat STRIDEWorkforceSeat, roomID string) (MeetingSpecialistCandidate, bool) {
	roomID = normalizeRoomID(roomID)
	if !strideIdentifier(roomID) || agent.ID != seat.ID || agent.Status != "hired_fenced" || agent.AccessRevoked || !agent.ProviderExecutionFenced ||
		seat.Status != "active" || seat.AccessRevoked || seat.Owner != agent.OwnerID || seat.DirectThread != agent.DirectThreadID ||
		seat.Overlay == nil || seat.Overlay.Validate() != nil || seat.Capability.Validate() != nil || seat.Capability.ContractType != STRIDEContractAgentCapabilityManifest ||
		!meetingSpecialistExactMembership(agent.Config.Memberships, roomID) {
		return MeetingSpecialistCandidate{}, false
	}

	assignment, found := meetingSpecialistExactAssignment(agent.Assignments, roomID)
	if !found {
		return MeetingSpecialistCandidate{}, false
	}
	assignmentDigest, err := STRIDEContractDigest(struct {
		AgentID       string                       `json:"agentId"`
		AgentRevision int64                        `json:"agentRevision"`
		Assignment    STRIDEProductAgentAssignment `json:"assignment"`
	}{AgentID: agent.ID, AgentRevision: agent.Revision, Assignment: assignment})
	if err != nil {
		return MeetingSpecialistCandidate{}, false
	}
	assignmentRef := STRIDEReference{ContractType: STRIDEContractAgentAssignment, ID: assignment.ID, Revision: agent.Revision, Digest: assignmentDigest}
	if assignmentRef.Validate() != nil {
		return MeetingSpecialistCandidate{}, false
	}
	workforceRevisionDigest, err := STRIDEContractDigest(seat)
	if err != nil || !isHexDigest(workforceRevisionDigest) {
		return MeetingSpecialistCandidate{}, false
	}
	material := meetingSpecialistEligibilityMaterial{
		AgentID: agent.ID, RoomID: roomID, ProductAgentRevision: agent.Revision, WorkforceRevisionDigest: workforceRevisionDigest,
		Assignment: assignmentRef, Profile: *seat.Overlay, Capability: seat.Capability,
	}
	eligibilityDigest, err := STRIDEContractDigest(material)
	if err != nil {
		return MeetingSpecialistCandidate{}, false
	}
	eligibilityRef := STRIDEReference{
		ContractType: STRIDEContractAgentAssignment,
		ID:           "meeting_eligibility_" + temporalDigest(agent.ID + "\x00" + roomID)[:20],
		Revision:     agent.Revision,
		Digest:       eligibilityDigest,
	}
	candidate := MeetingSpecialistCandidate{
		AgentID: agent.ID, DisplayName: strings.TrimSpace(agent.DisplayName), Profile: *seat.Overlay, Capability: seat.Capability,
		Assignment: &assignmentRef, Eligibility: &eligibilityRef, RoomID: roomID, ProductAgentRevision: agent.Revision, WorkforceRevisionDigest: workforceRevisionDigest,
	}
	return candidate, validMeetingSpecialistCandidateForRoom(candidate, roomID)
}

func meetingSpecialistExactMembership(memberships []string, roomID string) bool {
	for _, membership := range memberships {
		if strings.TrimSpace(membership) == roomID {
			return true
		}
	}
	return false
}

func meetingSpecialistExactAssignment(assignments []STRIDEProductAgentAssignment, roomID string) (STRIDEProductAgentAssignment, bool) {
	var selected STRIDEProductAgentAssignment
	found := false
	for _, assignment := range assignments {
		if assignment.Status != "active_fenced" || assignment.ProjectOrChannel != roomID || assignment.Destination != roomID || assignment.Role != meetingSpecialistAssignmentRole {
			continue
		}
		if !found || assignment.CreatedAt.After(selected.CreatedAt) || assignment.CreatedAt.Equal(selected.CreatedAt) && assignment.ID > selected.ID {
			selected, found = assignment, true
		}
	}
	return selected, found
}

func validMeetingSpecialistCandidateForRoom(candidate MeetingSpecialistCandidate, roomID string) bool {
	roomID = normalizeRoomID(roomID)
	if !strideIdentifier(candidate.AgentID) || strings.TrimSpace(candidate.DisplayName) == "" || candidate.RoomID != roomID || candidate.Profile.Validate() != nil ||
		candidate.Capability.Validate() != nil || candidate.Capability.ContractType != STRIDEContractAgentCapabilityManifest || candidate.Assignment == nil ||
		candidate.Assignment.Validate() != nil || candidate.Assignment.ContractType != STRIDEContractAgentAssignment || candidate.Eligibility == nil ||
		candidate.Eligibility.Validate() != nil || candidate.Eligibility.ContractType != STRIDEContractAgentAssignment || candidate.ProductAgentRevision <= 0 ||
		candidate.Assignment.Revision != candidate.ProductAgentRevision || candidate.Eligibility.Revision != candidate.ProductAgentRevision ||
		candidate.Eligibility.ID != "meeting_eligibility_"+temporalDigest(candidate.AgentID + "\x00" + roomID)[:20] || !isHexDigest(candidate.WorkforceRevisionDigest) {
		return false
	}
	material := meetingSpecialistEligibilityMaterial{
		AgentID: candidate.AgentID, RoomID: roomID, ProductAgentRevision: candidate.ProductAgentRevision, WorkforceRevisionDigest: candidate.WorkforceRevisionDigest,
		Assignment: *candidate.Assignment, Profile: candidate.Profile, Capability: candidate.Capability,
	}
	digest, err := STRIDEContractDigest(material)
	return err == nil && candidate.Eligibility.Digest == digest
}

func legacyMeetingSpecialistCandidate(candidate MeetingSpecialistCandidate) bool {
	return candidate.Assignment == nil && candidate.Eligibility == nil && candidate.RoomID == "" && candidate.ProductAgentRevision == 0 && candidate.WorkforceRevisionDigest == ""
}

func sameMeetingSpecialistCandidate(left, right MeetingSpecialistCandidate) bool {
	if left.AgentID != right.AgentID || left.DisplayName != right.DisplayName || left.Profile != right.Profile || left.Capability != right.Capability ||
		left.RoomID != right.RoomID || left.ProductAgentRevision != right.ProductAgentRevision || left.WorkforceRevisionDigest != right.WorkforceRevisionDigest || (left.Assignment == nil) != (right.Assignment == nil) || (left.Eligibility == nil) != (right.Eligibility == nil) {
		return false
	}
	return (left.Assignment == nil || *left.Assignment == *right.Assignment) && (left.Eligibility == nil || *left.Eligibility == *right.Eligibility)
}

func currentMeetingSpecialistCandidate(candidates []MeetingSpecialistCandidate, prior MeetingSpecialistCandidate, invitation MeetingAgentInvitation) bool {
	if invitation.Eligibility == nil || prior.Eligibility == nil || *invitation.Eligibility != *prior.Eligibility {
		return false
	}
	for _, candidate := range candidates {
		if sameMeetingSpecialistCandidate(candidate, prior) && candidate.Eligibility != nil && *candidate.Eligibility == *invitation.Eligibility {
			return true
		}
	}
	return false
}
