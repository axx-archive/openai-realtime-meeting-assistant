package main

import (
	"errors"
	"testing"
	"time"
)

type strideContextAuthorizationStub struct {
	denied map[string]bool
}

func (stub strideContextAuthorizationStub) AuthorizeAgentContext(reference STRIDEReference, _ string, _ STRIDEAudience) bool {
	return !stub.denied[reference.ID]
}

type strideRelationshipConsentStub struct {
	denied map[string]bool
}

func (stub strideRelationshipConsentStub) AuthorizeRelationshipMemory(reference STRIDEReference, _ string, _ STRIDEAudience) bool {
	return !stub.denied[reference.ID]
}

func strideAgentContextRequestForTest() STRIDEAgentContextRequest {
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	return STRIDEAgentContextRequest{
		TenantID: "bonfire", ContextID: "context_team_1", Revision: 1, CreatedAt: now,
		Surface: STRIDEContextTeam, Invocation: "explicit_mention", Requester: "member_aj", Recipients: []string{"member_aj"},
		CoreProfile: strideTestRef(STRIDEContractAgentCoreProfile, "scout_core"),
		Overlay: func() *STRIDEReference {
			reference := strideTestRef(STRIDEContractAgentProfileOverlay, "team_scout_overlay")
			return &reference
		}(),
		Capability: strideTestRef(STRIDEContractAgentCapabilityManifest, "scout_capability"), ChannelPolicy: strideTestRef(STRIDEContractChannelNormProfile, "team_policy"),
		CurrentTurn: STRIDEContextTurn{Event: strideTestRef(STRIDEContractConversationEvent, "event_2"), AuthorPrincipal: "member_erick", AuthorName: "Erick", ReplyTo: func() *STRIDEReference {
			reference := strideTestRef(STRIDEContractConversationEvent, "event_1")
			return &reference
		}()},
		RecentTurns: []STRIDEContextTurn{
			{Event: strideTestRef(STRIDEContractConversationEvent, "event_1"), AuthorPrincipal: "member_aj", AuthorName: "AJ"},
			{Event: strideTestRef(STRIDEContractConversationEvent, "event_2"), AuthorPrincipal: "member_erick", AuthorName: "Erick", ReplyTo: func() *STRIDEReference {
				reference := strideTestRef(STRIDEContractConversationEvent, "event_1")
				return &reference
			}(), ReactionActors: []string{"member_aj"}},
		},
		Evidence:      []STRIDEReference{strideTestRef(STRIDEContractTranscriptSegment, "segment_1"), strideTestRef(STRIDEContractKnowledgeAssertion, "assertion_1")},
		Relationships: []STRIDEReference{strideTestRef(STRIDEContractAgentRelationshipMemory, "memory_erick")},
		ActiveWork:    []STRIDEContextWorkRef{{Reference: strideTestRef(STRIDEContractWorkProposal, "proposal_1"), Status: "approved"}},
		AllowedTools:  []string{"search_company_artifacts", "post_thread_reply"}, ResponseModes: []string{"text", "artifact_card"}, Audience: strideTestAudience(),
		ACLVersion: 3, PurgeGeneration: 2, TranscriptHighWater: 14, AnalysisHighWater: 9, BrainHighWater: 8, FreshnessDigest: strideTestDigest("c"), GapsDigest: strideTestDigest("d"),
	}
}

func TestSTRIDEAgentContextAssemblyIsDeterministicAndBodyFree(t *testing.T) {
	request := strideAgentContextRequestForTest()
	assembler := STRIDEAgentContextAssembler{Authorizer: strideContextAuthorizationStub{}, Consent: strideRelationshipConsentStub{}}
	first, err := assembler.Assemble(request)
	if err != nil {
		t.Fatalf("assemble first: %v", err)
	}
	second, err := assembler.Assemble(request)
	if err != nil {
		t.Fatalf("assemble second: %v", err)
	}
	if first.Digest != second.Digest || first.Envelope.ContextDigest != first.Digest {
		t.Fatalf("context digest is not deterministic: %q %q", first.Digest, second.Digest)
	}
	if first.Envelope.AgentProfile.ID != "team_scout_overlay" || first.CoreProfile.ID != "scout_core" || first.Turns[1].ReplyTo == nil || first.Turns[1].ReplyTo.ID != "event_1" {
		t.Fatalf("profile or reply ancestry was not preserved: %#v", first)
	}
	if len(first.Envelope.Evidence) != 2 || first.ACLVersion != 3 || first.PurgeGeneration != 2 || first.TranscriptHighWater != 14 || first.GapsDigest != request.GapsDigest {
		t.Fatalf("coverage boundary missing from assembled context: %#v", first)
	}
	if err := first.Envelope.Validate(); err != nil {
		t.Fatalf("relationship context should satisfy envelope validation: %v", err)
	}
}

func TestSTRIDEAgentContextReauthorizesEveryReferenceAndRelationshipConsent(t *testing.T) {
	request := strideAgentContextRequestForTest()
	assembler := STRIDEAgentContextAssembler{Authorizer: strideContextAuthorizationStub{denied: map[string]bool{"segment_1": true}}, Consent: strideRelationshipConsentStub{}}
	if _, err := assembler.Assemble(request); !errors.Is(err, ErrSTRIDEAgentContextDenied) {
		t.Fatalf("unauthorized evidence error=%v, want context denial", err)
	}
	assembler = STRIDEAgentContextAssembler{Authorizer: strideContextAuthorizationStub{}, Consent: strideRelationshipConsentStub{denied: map[string]bool{"memory_erick": true}}}
	if _, err := assembler.Assemble(request); !errors.Is(err, ErrSTRIDEAgentContextDenied) {
		t.Fatalf("unconsented relationship error=%v, want context denial", err)
	}
}

func TestSTRIDEAgentContextRejectsUnsafeToolsAndWrongRelationshipClass(t *testing.T) {
	request := strideAgentContextRequestForTest()
	request.AllowedTools = []string{"provider_url_lookup"}
	if err := request.Validate(); !errors.Is(err, ErrSTRIDEAgentContextInvalid) {
		t.Fatalf("provider route error=%v, want invalid context", err)
	}
	request = strideAgentContextRequestForTest()
	request.Relationships = []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "event_not_memory")}
	if err := request.Validate(); !errors.Is(err, ErrSTRIDEAgentContextInvalid) {
		t.Fatalf("non-relationship reference error=%v, want invalid context", err)
	}
}

func TestSTRIDEAgentContextRejectsAudienceLeakage(t *testing.T) {
	request := strideAgentContextRequestForTest()
	request.Recipients = []string{"member_not_in_audience"}
	if err := request.Validate(); !errors.Is(err, ErrSTRIDEAgentContextInvalid) {
		t.Fatalf("recipient outside audience error=%v, want invalid context", err)
	}
	request = strideAgentContextRequestForTest()
	request.Requester = "member_not_in_audience"
	if err := request.Validate(); !errors.Is(err, ErrSTRIDEAgentContextInvalid) {
		t.Fatalf("requester outside audience error=%v, want invalid context", err)
	}
}

func TestSTRIDEAgentContextAllowsNoRelationshipMemory(t *testing.T) {
	request := strideAgentContextRequestForTest()
	request.Relationships = nil
	assembled, err := (STRIDEAgentContextAssembler{Authorizer: strideContextAuthorizationStub{}, Consent: strideRelationshipConsentStub{}}).Assemble(request)
	if err != nil {
		t.Fatalf("assemble without relationship memory: %v", err)
	}
	if len(assembled.Envelope.Preferences) != 0 {
		t.Fatalf("unexpected preference memory: %#v", assembled.Envelope.Preferences)
	}
}

func strideScoutCoreProfileForTest() AgentCoreProfile {
	return AgentCoreProfile{
		Header: strideTestHeader(STRIDEContractAgentCoreProfile, "scout_core_profile"), AgentID: "scout", DisplayName: "Scout", Pronunciation: "Scout", Role: "chief_of_staff",
		MissionDigest: strideTestDigest("e"), StyleDigest: strideTestDigest("f"), Traits: []string{"warm"}, HumorRange: "low", Values: []string{"helpful"}, Boundaries: []string{"consent"}, Prohibited: []string{"impersonation"}, EscalationPolicy: "human_owner", Owner: "member_aj", Status: "active",
	}
}

func TestSTRIDEScoutParticipantStateIsDefaultOffInspectableAndCorrectable(t *testing.T) {
	state, err := NewSTRIDEScoutParticipantState(strideScoutCoreProfileForTest())
	if err != nil || !state.Disabled() {
		t.Fatalf("new participant must default off: state=%#v err=%v", state, err)
	}
	if err := state.Enable(); !errors.Is(err, ErrSTRIDEActivationFenced) {
		t.Fatalf("unfenced enable error=%v", err)
	}
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	memory := AgentRelationshipMemory{Header: strideTestHeader(STRIDEContractAgentRelationshipMemory, "relationship_1"), AgentID: "scout", Subject: "member_erick", Scope: "team", ObservationDigest: strideTestDigest("a"), Evidence: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "event_1")}, Confidence: .6, FirstObserved: now, LastObserved: now, Audience: strideTestAudience(), Status: "present"}
	if err := state.CorrectRelationship(memory); err != nil {
		t.Fatalf("correct relationship: %v", err)
	}
	if got, err := state.InspectRelationship("relationship_1"); err != nil || got.Subject != "member_erick" {
		t.Fatalf("inspect correction: %#v %v", got, err)
	}
	if err := state.ForgetRelationship("relationship_1"); err != nil {
		t.Fatalf("forget relationship: %v", err)
	}
	if _, err := state.InspectRelationship("relationship_1"); !errors.Is(err, ErrSTRIDEConversationUnknown) {
		t.Fatalf("forgotten relationship error=%v", err)
	}
}

func TestSTRIDEScoutParticipantStateHasSignedLocalEnablePath(t *testing.T) {
	state, err := NewSTRIDEScoutParticipantState(strideScoutCoreProfileForTest())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.ProductPreviewEnabled = true
	config.RelationshipMemoryEnabled = true
	receipt, err := mintSTRIDEProductActivationReceipt(config, 7, STRIDEProductScopeCoworker, now)
	if err != nil {
		t.Fatal(err)
	}
	withoutRelationshipAuthority := config
	withoutRelationshipAuthority.RelationshipMemoryEnabled = false
	if err := state.EnableWithAuthority(withoutRelationshipAuthority, receipt, now); !errors.Is(err, ErrSTRIDEActivationFenced) {
		t.Fatalf("missing relationship authority error=%v", err)
	}
	if err := state.EnableWithAuthority(config, receipt, now.Add(3*time.Minute)); !errors.Is(err, ErrSTRIDEActivationFenced) {
		t.Fatalf("expired signed receipt error=%v", err)
	}
	if err := state.EnableWithAuthority(config, receipt, now); err != nil || state.Disabled() {
		t.Fatalf("signed deterministic-local enable disabled=%t err=%v", state.Disabled(), err)
	}
	state.Disable()
	if !state.Disabled() {
		t.Fatal("disable did not synchronously fence participant")
	}
}
