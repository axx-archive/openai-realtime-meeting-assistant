package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMeetingSpecialistColtonRecommendationIsReadOnlyHumanApprovedAndTruthful(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	colton := specialistCandidateFixture(coltonMeetingSpecialistAgentID, authority.scope.RoomID)
	colton.DisplayName = "Colton"
	authority.mu.Lock()
	authority.roster = []MeetingSpecialistCandidate{colton}
	authority.mu.Unlock()

	recommendation, err := product.RecommendColtonForResearch(context.Background(), user, authority.scope.RoomID, "We should research the competitive landscape and verify the strongest sources")
	if err != nil {
		t.Fatal(err)
	}
	if recommendation.AgentID != coltonMeetingSpecialistAgentID || recommendation.DisplayName != "Colton" || recommendation.Reason != "research_fit" ||
		!recommendation.RequiresHumanApproval || recommendation.ProviderReady || recommendation.ProviderReadinessState != "provider_qualification_pending" {
		t.Fatalf("recommendation=%+v", recommendation)
	}
	product.mu.Lock()
	invitationCount := len(product.invitations)
	product.mu.Unlock()
	if invitationCount != 0 {
		t.Fatalf("Scout recommendation created %d invitations without human action", invitationCount)
	}
}

func TestMeetingSpecialistColtonRecommendationRequiresResearchAndExactEligibleSeat(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	colton := specialistCandidateFixture(coltonMeetingSpecialistAgentID, authority.scope.RoomID)
	colton.DisplayName = "Colton"
	authority.mu.Lock()
	authority.roster = []MeetingSpecialistCandidate{colton}
	authority.mu.Unlock()
	if _, err := product.RecommendColtonForResearch(context.Background(), user, authority.scope.RoomID, "Please take notes"); err != ErrMeetingSpecialistProductDecision {
		t.Fatalf("non-research recommendation err=%v", err)
	}
	authority.mu.Lock()
	authority.roster = nil
	authority.mu.Unlock()
	if _, err := product.RecommendColtonForResearch(context.Background(), user, authority.scope.RoomID, "Ask Colton to research this"); err != ErrMeetingSpecialistProductAgent {
		t.Fatalf("unassigned Colton recommendation err=%v", err)
	}
}

func TestMeetingSpecialistScoutManagementRequiresAddressedTurnAndNeverAutoApproves(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	colton := specialistCandidateFixture(coltonMeetingSpecialistAgentID, authority.scope.RoomID)
	colton.DisplayName = "Colton"
	authority.mu.Lock()
	authority.roster = []MeetingSpecialistCandidate{colton}
	authority.mu.Unlock()

	ambient := MeetingSpecialistScoutResearchTurn{
		RoomID: authority.scope.RoomID, Utterance: "We need competitive research",
		Purpose: "Research the competitive landscape", Addressed: false,
	}
	if _, err := product.HandleScoutManagedResearchTurn(context.Background(), user, ambient); err != ErrMeetingSpecialistScoutInvocation {
		t.Fatalf("ambient research turn err=%v", err)
	}
	product.mu.Lock()
	if len(product.invitations) != 0 {
		product.mu.Unlock()
		t.Fatal("ambient research discussion created an invitation")
	}
	product.mu.Unlock()

	addressed := ambient
	addressed.Addressed = true
	addressed.Utterance = "Scout, what research support do you recommend?"
	result, err := product.HandleScoutManagedResearchTurn(context.Background(), user, addressed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "recommend_colton" || result.Recommendation == nil || result.Invitation != nil || result.InvitationCreated || result.ProviderSessionStarted || !result.RequiresHumanApproval {
		t.Fatalf("recommendation result=%+v", result)
	}

	confirmed := MeetingSpecialistScoutResearchTurn{
		RoomID: authority.scope.RoomID, Utterance: "Yes, invite Colton",
		Purpose: "Research the competitive landscape", Addressed: true,
		IdempotencyKey: "scout-colton-confirmed", InvitationTTL: time.Minute,
	}
	result, err = product.HandleScoutManagedResearchTurn(context.Background(), user, confirmed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "colton_invitation_pending" || result.Invitation == nil || result.Invitation.Status != "awaiting_approval" ||
		!result.InvitationCreated || result.ProviderSessionStarted || result.Invitation.ProviderSessionStarted || !result.RequiresHumanApproval {
		t.Fatalf("confirmed result=%+v", result)
	}
	product.mu.Lock()
	record := product.invitations[result.Invitation.ID]
	product.mu.Unlock()
	if record.Runtime != nil || record.Invitation.Decision != "requested" || record.Invitation.DecisionPrincipal != "" {
		t.Fatalf("Scout-owned provider or approval leaked into invitation=%+v", record)
	}
}

func TestMeetingSpecialistColtonCanBeInvitedDirectlyWithoutScout(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	colton := specialistCandidateFixture(coltonMeetingSpecialistAgentID, authority.scope.RoomID)
	colton.DisplayName = "Colton"
	authority.mu.Lock()
	authority.roster = []MeetingSpecialistCandidate{colton}
	authority.mu.Unlock()

	invitation, err := product.Request(context.Background(), user, authority.scope.RoomID, coltonMeetingSpecialistAgentID, "Research the category", "direct-colton", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if invitation.Status != "awaiting_approval" || invitation.ProviderSessionStarted {
		t.Fatalf("direct invitation=%+v", invitation)
	}
}

func TestMeetingSpecialistColtonIdentityUsesDurableProductProfile(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC)
	agent, err := state.beginTrial("colton-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.recordAgentLearning(agent.ID, agent.Revision, "delivery", "team", "Lead with the recommendation, then show the source map.", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
		value.Status = "hired_fenced"
		value.DirectThreadID = "stride_agent_direct_colton_room_identity"
		value.AccessRevoked = false
		value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
		return nil
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := state.agentContextProfile(agent.ID)
	if !ok {
		t.Fatal("Colton context profile missing")
	}
	revision := strideTestRef(STRIDEContractAgentProfileOverlay, "colton-room-overlay")
	identity, err := meetingSpecialistRealtimeIdentityFromProfile(profile, revision)
	if err != nil {
		t.Fatal(err)
	}
	if identity.AgentID != coltonMeetingSpecialistAgentID || identity.DisplayName != "Colton" || identity.RoleTitle != "Research Partner" ||
		len(identity.CoreMemories) != 2 || len(identity.ActiveLearning) != 1 || identity.ActiveLearning[0].Summary != "Lead with the recommendation, then show the source map." ||
		identity.validate(coltonMeetingSpecialistAgentID, revision) != nil {
		t.Fatalf("identity=%+v", identity)
	}
	identity.PersonalityNotes += " silently changed"
	if identity.validate(coltonMeetingSpecialistAgentID, revision) == nil {
		t.Fatal("identity digest accepted mutated personality")
	}
}

func TestMeetingSpecialistColtonHasDistinctQualifiedVoicePolicy(t *testing.T) {
	if meetingSpecialistVoiceMatchesIdentity(coltonMeetingSpecialistAgentID, defaultRealtimeVoice) {
		t.Fatal("Colton accepted Scout's voice")
	}
	if !meetingSpecialistVoiceMatchesIdentity(coltonMeetingSpecialistAgentID, coltonMeetingSpecialistVoice) {
		t.Fatal("Colton's dedicated voice was rejected")
	}
}

func TestMeetingSpecialistColtonBriefRequiresBoundIdentity(t *testing.T) {
	launch := specialistRuntimeLaunchFixture(time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC))
	launch.Scope.AgentID = coltonMeetingSpecialistAgentID
	purpose := "Research the category landscape"
	launch.Invitation.PurposeDigest = sha256Hex([]byte(purpose))
	evidence := []MeetingSpecialistRealtimeBriefEvidence{}
	for _, values := range [][]STRIDEReference{launch.Context.TranscriptRefs, launch.Context.AnalysisRefs, launch.Context.BrainRefs, launch.Context.WorkRefs} {
		for _, reference := range values {
			evidence = append(evidence, MeetingSpecialistRealtimeBriefEvidence{Reference: reference, Text: "Authorized evidence"})
		}
	}
	brief := MeetingSpecialistRealtimeBrief{Purpose: purpose, Evidence: evidence}
	if validateMeetingSpecialistRealtimeBrief(launch, brief) == nil {
		t.Fatal("first-party specialist brief accepted no durable identity")
	}

	state := NewSTRIDEProductState()
	agent, err := state.beginTrial("colton-research", "member_aj", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
		value.Status = "hired_fenced"
		value.DirectThreadID = "stride_agent_direct_colton_room_brief"
		value.AccessRevoked = false
		value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
		return nil
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := state.agentContextProfile(agent.ID)
	if !ok {
		t.Fatal("Colton profile missing")
	}
	identity, err := meetingSpecialistRealtimeIdentityFromProfile(profile, launch.Context.AgentProfile)
	if err != nil {
		t.Fatal(err)
	}
	brief.Identity = &identity
	if err := validateMeetingSpecialistRealtimeBrief(launch, brief); err != nil {
		t.Fatalf("bound Colton brief rejected: %v", err)
	}
}

func TestMeetingSpecialistColtonTranscriptContributionKeepsAgentAttribution(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	colton := specialistCandidateFixture(coltonMeetingSpecialistAgentID, authority.scope.RoomID)
	colton.DisplayName = "Colton"
	authority.mu.Lock()
	authority.roster = []MeetingSpecialistCandidate{colton}
	authority.mu.Unlock()
	now := product.now().UTC()
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixtureForQualification(t, now, now.Add(-time.Minute), meetingSpecialistQualificationFixtureForCandidate(authority.scope.TenantID, colton), provider, &factoryCalls)
	var contribution MeetingSpecialistTranscriptContribution
	joiner.publishTranscript = func(value MeetingSpecialistTranscriptContribution) error {
		contribution = value
		return nil
	}
	product.productionJoin = joiner

	requested, err := product.Request(context.Background(), user, authority.scope.RoomID, colton.AgentID, "Research the category", "colton-transcript", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := product.Resolve(context.Background(), user, authority.scope.RoomID, requested.ID, requested.Revision, "approved")
	if err != nil || !approved.ProviderSessionStarted {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	product.mu.Lock()
	runtime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	if runtime == nil {
		t.Fatal("joined Colton runtime missing")
	}
	runtime.mu.Lock()
	session := runtime.lease
	runtime.mu.Unlock()
	floor, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.PublishProviderTranscript(floor, "I found three primary sources that change the recommendation."); err != nil {
		t.Fatal(err)
	}
	if contribution.AgentID != coltonMeetingSpecialistAgentID || contribution.DisplayName != "Colton" || contribution.AudioTrackID != session.Scope.AudioTrackID ||
		contribution.Profile != colton.Profile || contribution.Transcript == "" || contribution.FloorGeneration != floor.Generation {
		t.Fatalf("contribution=%+v", contribution)
	}
}

func TestMeetingSpecialistColtonTranscriptIsFencedByHumanInterruption(t *testing.T) {
	product, authority, user := specialistProductFixture(t)
	colton := specialistCandidateFixture(coltonMeetingSpecialistAgentID, authority.scope.RoomID)
	colton.DisplayName = "Colton"
	authority.mu.Lock()
	authority.roster = []MeetingSpecialistCandidate{colton}
	authority.mu.Unlock()
	now := product.now().UTC()
	provider := &fakeMeetingSpecialistProvider{}
	var factoryCalls atomic.Int64
	joiner, _ := productionJoinFixtureForQualification(t, now, now.Add(-time.Minute), meetingSpecialistQualificationFixtureForCandidate(authority.scope.TenantID, colton), provider, &factoryCalls)
	published := 0
	joiner.publishTranscript = func(MeetingSpecialistTranscriptContribution) error {
		published++
		return nil
	}
	product.productionJoin = joiner

	requested, err := product.Request(context.Background(), user, authority.scope.RoomID, colton.AgentID, "Research the category", "colton-interruption", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = product.Resolve(context.Background(), user, authority.scope.RoomID, requested.ID, requested.Revision, "approved"); err != nil {
		t.Fatal(err)
	}
	product.mu.Lock()
	runtime := product.invitations[requested.ID].Runtime
	product.mu.Unlock()
	runtime.mu.Lock()
	session := runtime.lease
	runtime.mu.Unlock()
	floor, err := runtime.RequestTurn(session, "approved_scout_handoff", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if interruption, ok := runtime.HumanBargeIn(session.Scope.RoomID, session.Scope.SittingID, session.Scope.MediaGeneration); !ok || !interruption.CancelProvider {
		t.Fatalf("interruption=%+v ok=%v", interruption, ok)
	}
	if err = runtime.PublishProviderTranscript(floor, "This stale response must not appear."); err == nil {
		t.Fatal("interrupted Colton floor published a transcript")
	}
	if published != 0 {
		t.Fatalf("published transcripts=%d", published)
	}
}
