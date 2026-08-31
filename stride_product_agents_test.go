package main

import (
	"strings"
	"testing"
	"time"
)

func TestSTRIDEProductColtonAndMarvinAreDistinctInspectableResearchCoworkers(t *testing.T) {
	state := NewSTRIDEProductState()
	colton, coltonFound := state.candidate("colton-research")
	marvin, marvinFound := state.candidate("marvin-research")
	if !coltonFound || !marvinFound {
		t.Fatalf("research coworkers missing: colton=%v marvin=%v", coltonFound, marvinFound)
	}
	for _, candidate := range []STRIDEProductMarketplaceCandidate{colton, marvin} {
		if candidate.Category != "research" || candidate.RoleTitle == "" || candidate.VoiceSummary == "" || candidate.WorkingStyle == "" ||
			len(candidate.PersonalityTraits) < 3 || len(candidate.CoreMemories) < 2 || candidate.DefaultPersonalityNotes == "" || candidate.DefaultProactivity != "quiet" ||
			!containsSTRIDEID(candidate.Capabilities, "research_brief") || !containsSTRIDEID(candidate.RequiredAccess, "acl_bound_files") ||
			candidate.LiveAvailable || !candidate.ProviderExecutionFenced || candidate.ReceiptStatus["providerQuality"] || candidate.ReceiptStatus["humanAdmission"] {
			t.Fatalf("research coworker is not richly described and safely fenced: %#v", candidate)
		}
	}
	if colton.RoleTitle == marvin.RoleTitle || colton.PersonalitySummary == marvin.PersonalitySummary || colton.WorkingStyle == marvin.WorkingStyle ||
		colton.UsageGuidance == "" ||
		strings.Join(colton.PersonalityTraits, "\x00") == strings.Join(marvin.PersonalityTraits, "\x00") ||
		strings.Join(colton.Capabilities, "\x00") == strings.Join(marvin.Capabilities, "\x00") {
		t.Fatalf("Colton and Marvin collapsed to the same identity: colton=%#v marvin=%#v", colton, marvin)
	}
}

func TestSTRIDEProductScoutProfileIsInspectableIncludedAndNotHireable(t *testing.T) {
	state := NewSTRIDEProductState()
	scout, found := state.candidate("scout")
	if !found || scout.DisplayName != "Scout" || scout.RoleTitle != "Chief of Staff" || scout.UsageGuidance == "" || len(scout.PersonalityTraits) < 3 || len(scout.CoreMemories) < 2 ||
		!strings.Contains(scout.VoiceSummary, "first-person") || !strings.Contains(scout.MemoryPolicy, "human correction") || !containsSTRIDEID(scout.Capabilities, "agent_delegation") {
		t.Fatalf("Scout marketplace identity is incomplete: %#v found=%v", scout, found)
	}
	if _, err := state.beginTrial("scout", "member_aj", time.Now().UTC()); err != ErrSTRIDEProductDenied {
		t.Fatalf("included Scout must not create a duplicate hire seat: %v", err)
	}
}

func TestSTRIDEProductMarketplaceLeadsWithCurrentTeamIdentities(t *testing.T) {
	catalog := NewSTRIDEProductState().candidateCatalog()
	if len(catalog) < 3 || catalog[0].ID != "scout" || catalog[1].ID != "colton-research" || catalog[2].ID != "marvin-research" {
		t.Fatalf("marketplace priority=%v, want Scout, Colton, Marvin", func() []string {
			ids := make([]string, 0, len(catalog))
			for _, candidate := range catalog {
				ids = append(ids, candidate.ID)
			}
			return ids
		}())
	}
}

func TestSTRIDEProductCustomerRosterIsFixedToThreeAddressableRoles(t *testing.T) {
	state := NewSTRIDEProductState()
	wantIDs := []string{"scout", "researcher", "presenter"}
	if catalog := state.candidateCatalogForViewer(false); len(catalog) != 3 || catalog[0].ID != wantIDs[0] || catalog[1].ID != wantIDs[1] || catalog[2].ID != wantIDs[2] {
		t.Fatalf("customer roster before compatibility hire=%v, want %v", catalog, wantIDs)
	}
	if adminCatalog := state.candidateCatalogForViewer(true); len(adminCatalog) != 3 {
		t.Fatalf("administrator customer roster must not become a marketplace: %v", adminCatalog)
	}
	for _, id := range []string{"researcher", "presenter"} {
		if _, err := state.beginTrial(id, "member_aj", time.Now().UTC()); err != ErrSTRIDEProductDenied {
			t.Fatalf("fixed role %q entered legacy hire funnel: %v", id, err)
		}
	}

	now := time.Date(2026, 8, 5, 20, 0, 0, 0, time.UTC)
	trial, err := state.beginTrial("colton-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	if catalog := state.candidateCatalogForViewer(false); len(catalog) != 3 {
		t.Fatalf("legacy trial changed fixed customer roster: %v", catalog)
	}
	_, err = state.mutateAgent(trial.ID, trial.Revision, func(agent *STRIDEProductTeamAgent) error {
		agent.Status = "hired_fenced"
		agent.DirectThreadID = "stride_agent_direct_colton_directory"
		agent.AccessRevoked = false
		return nil
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	catalog := state.candidateCatalogForViewer(false)
	if len(catalog) != 3 || catalog[0].ID != "scout" || catalog[1].ID != "researcher" || catalog[2].ID != "presenter" {
		t.Fatalf("legacy hire changed fixed customer roster: %v", catalog)
	}
}

func TestSTRIDEProductAddressableSpecialistsHaveDistinctLearningAndOutputContracts(t *testing.T) {
	state := NewSTRIDEProductState()
	researcher, researchOK := state.addressableAgentContextProfile("agent_researcher")
	presenter, presentationOK := state.addressableAgentContextProfile("agent_presenter")
	if !researchOK || !presentationOK {
		t.Fatalf("fixed specialists unavailable: researcher=%v presenter=%v", researchOK, presentationOK)
	}
	if !containsSTRIDEID(researcher.Capabilities, "deep_research") || containsSTRIDEID(presenter.Capabilities, "deep_research") ||
		!containsSTRIDEID(presenter.Capabilities, "presentation_deck") || researcher.MemoryPolicy == "" || presenter.MemoryPolicy == "" ||
		researcher.AgentID == presenter.AgentID || researcher.Digest == presenter.Digest {
		t.Fatalf("specialist contracts are not distinct and governed: researcher=%#v presenter=%#v", researcher, presenter)
	}
}

func TestScoutAnswerContractIsFirstPersonAndMemoryBound(t *testing.T) {
	instructions := assistantQueryInstructionsForCoreAvailability(true)
	for _, want := range []string{"persistent teammate", "Speak in first person", "human-reviewed relationship and company memory", "never invent a preference"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("Scout identity contract missing %q", want)
		}
	}
}

func TestSTRIDEProductResearchCoworkerIdentityAndLearningSurviveSnapshot(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 8, 4, 21, 0, 0, 0, time.UTC)
	agent, err := state.beginTrial("colton-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	if agent.ID != "agent_colton-research" || agent.RoleTitle != "Research Partner" || agent.Config.Proactivity != "quiet" ||
		!strings.Contains(agent.Config.PersonalityNotes, "primary sources") || len(agent.CoreMemories) != 2 || !containsSTRIDEID(agent.Capabilities, "deep_research") ||
		!agent.ProviderExecutionFenced || !agent.AccessRevoked {
		t.Fatalf("trial did not retain Colton's identity and safety posture: %#v", agent)
	}
	agent, err = state.recordAgentLearning(agent.ID, agent.Revision, "delivery", "team", "Lead Country+Golf briefs with the recommendation, then show the evidence map.", now.Add(time.Minute))
	if err != nil || len(agent.Learning) != 1 {
		t.Fatalf("record learning agent=%#v err=%v", agent, err)
	}
	agent, err = state.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
		value.Status = "hired_fenced"
		value.DirectThreadID = "stride_agent_direct_colton_test"
		value.AccessRevoked = false
		value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
		return nil
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	profile, available := state.agentContextProfile(agent.ID)
	if !available || profile.AgentID != agent.ID || profile.RoleTitle != "Research Partner" || len(profile.ActiveLearning) != 1 ||
		profile.ActiveLearning[0].Summary != agent.Learning[0].Summary || !profile.ProviderExecutionFenced || !isHexDigest(profile.Digest) {
		t.Fatalf("context profile=%#v available=%v", profile, available)
	}
	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEProductState(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got, found := restored.agentRecord(agent.ID)
	if !found || got.RoleTitle != agent.RoleTitle || got.PersonalitySummary != agent.PersonalitySummary ||
		strings.Join(got.PersonalityTraits, "\x00") != strings.Join(agent.PersonalityTraits, "\x00") || len(got.CoreMemories) != 2 ||
		len(got.Learning) != 1 || got.Learning[0].Summary != agent.Learning[0].Summary || got.Learning[0].Status != "reviewed" {
		t.Fatalf("restored identity/learning=%#v found=%v, want %#v", got, found, agent)
	}
}

func TestSTRIDEDirectCoworkerIdentityProjectsIntoExistingChatThread(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 8, 4, 21, 30, 0, 0, time.UTC)
	agent, err := state.beginTrial("colton-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
		value.Status = "hired_fenced"
		value.DirectThreadID = "stride_agent_direct_colton_projection"
		value.AccessRevoked = false
		return nil
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{strideRuntime: &STRIDERuntime{domains: &strideRuntimeTenantState{product: state}}}
	projected := app.projectScoutChatThreadForViewer("aj@shareability.com", scoutChatThreadRecord{ID: agent.DirectThreadID, Title: "renamed research thread"})
	if projected.AgentID != agent.ID || projected.AgentName != "Colton" {
		t.Fatalf("direct coworker identity projection=%#v", projected)
	}
}

func TestSTRIDEProductRestoreReconcilesMissingAndOlderFirstPartyListings(t *testing.T) {
	state := NewSTRIDEProductState()
	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	filtered := snapshot.Candidates[:0]
	for _, candidate := range snapshot.Candidates {
		if candidate.ID == "colton-research" || candidate.ID == "marvin-research" {
			continue
		}
		if candidate.ID == "mary-marketing" {
			candidate.RoleTitle = ""
			candidate.VoiceSummary = ""
			candidate.WorkingStyle = ""
			candidate.PersonalityTraits = nil
			candidate.CoreMemories = nil
			candidate.Version = "1.0.0-preview"
			candidate.PackageDigest = temporalDigest(candidate.PackageID + "\x00" + candidate.Version + "\x00" + candidate.Provenance)
		}
		filtered = append(filtered, candidate)
	}
	snapshot.Candidates = filtered
	snapshot.Digest, err = STRIDEContractDigest(struct {
		Version    int
		Work       []STRIDEProductWorkRecord
		Insights   []STRIDEProductInsightsState `json:"Insights,omitempty"`
		Candidates []STRIDEProductMarketplaceCandidate
		Agents     []STRIDEProductTeamAgent
	}{snapshot.Version, snapshot.Work, snapshot.Insights, snapshot.Candidates, snapshot.Agents})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEProductState(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"colton-research", "marvin-research"} {
		if _, found := restored.candidate(id); !found {
			t.Fatalf("restore did not add %s", id)
		}
	}
	mary, found := restored.candidate("mary-marketing")
	if !found || mary.Version != "1.1.0-preview" || mary.RoleTitle != "Marketing Partner" || len(mary.CoreMemories) != 2 {
		t.Fatalf("restore did not enrich older Mary listing: %#v found=%v", mary, found)
	}
}

func TestSTRIDEProductRestoreUpgradesFirstPartySeatIdentityWithoutRewritingTeamState(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Date(2026, 8, 4, 22, 0, 0, 0, time.UTC)
	agent, err := state.beginTrial("colton-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.recordAgentLearning(agent.ID, agent.Revision, "delivery", "team", "Lead with the decision and preserve the source trail.", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	agent, err = state.mutateAgent(agent.ID, agent.Revision, func(value *STRIDEProductTeamAgent) error {
		value.Status = "hired_fenced"
		value.DirectThreadID = "stride_agent_direct_colton_upgrade"
		value.AccessRevoked = false
		value.Config.PersonalityNotes = "Keep the team's approved concise delivery style."
		value.Config.Memberships = []string{"country_golf", "team"}
		value.Lifecycle = append(value.Lifecycle, "human_approved_hire", "provider_runtime_remains_fenced")
		return nil
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for index := range snapshot.Agents {
		if snapshot.Agents[index].ID != agent.ID {
			continue
		}
		legacy := &snapshot.Agents[index]
		legacy.RoleTitle = ""
		legacy.OutcomeSummary = ""
		legacy.PersonalitySummary = ""
		legacy.VoiceSummary = ""
		legacy.WorkingStyle = ""
		legacy.PersonalityTraits = nil
		legacy.Capabilities = nil
		legacy.MemoryPolicy = ""
		legacy.CoreMemories = nil
	}
	snapshot.Digest, err = STRIDEContractDigest(struct {
		Version    int
		Work       []STRIDEProductWorkRecord
		Insights   []STRIDEProductInsightsState `json:"Insights,omitempty"`
		Candidates []STRIDEProductMarketplaceCandidate
		Agents     []STRIDEProductTeamAgent
	}{snapshot.Version, snapshot.Work, snapshot.Insights, snapshot.Candidates, snapshot.Agents})
	if err != nil {
		t.Fatal(err)
	}

	restored, err := RestoreSTRIDEProductState(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, found := restored.agentRecord(agent.ID)
	if !found || upgraded.RoleTitle != "Research Partner" || upgraded.VoiceSummary == "" || upgraded.WorkingStyle == "" ||
		len(upgraded.CoreMemories) != 2 || !containsSTRIDEID(upgraded.Capabilities, "deep_research") {
		t.Fatalf("restored seat did not inherit current first-party identity: %#v found=%v", upgraded, found)
	}
	if upgraded.Status != agent.Status || upgraded.DirectThreadID != agent.DirectThreadID || upgraded.AccessRevoked != agent.AccessRevoked ||
		upgraded.ProviderExecutionFenced != agent.ProviderExecutionFenced || upgraded.Revision != agent.Revision ||
		upgraded.Config.PersonalityNotes != agent.Config.PersonalityNotes || strings.Join(upgraded.Config.Memberships, "\x00") != strings.Join(agent.Config.Memberships, "\x00") ||
		len(upgraded.Learning) != len(agent.Learning) || upgraded.Learning[0].Summary != agent.Learning[0].Summary ||
		strings.Join(upgraded.Lifecycle, "\x00") != strings.Join(agent.Lifecycle, "\x00") {
		t.Fatalf("identity upgrade rewrote durable team state: upgraded=%#v original=%#v", upgraded, agent)
	}
}

func TestCompletedCoworkerWorkProposesProvenanceBoundLearningBeforeActivation(t *testing.T) {
	state := NewSTRIDEProductState()
	now := time.Now().UTC().Add(-time.Minute)
	trial, err := state.beginTrial("colton-research", "member_aj", now)
	if err != nil {
		t.Fatal(err)
	}
	hired, err := state.mutateAgent(trial.ID, trial.Revision, func(agent *STRIDEProductTeamAgent) error {
		agent.Status = "hired_fenced"
		agent.DirectThreadID = "stride_agent_direct_colton_learning"
		agent.AccessRevoked = false
		return nil
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	hired, created, err := state.proposeAgentLearningFromWork(
		hired.ID, "research_delivery", "country_golf", "Lead with the decision and keep the source map attached.",
		"agent-thread-research-learning", "artifact_research_learning", "country_golf", []string{"artifact:artifact_research_learning", "file|country_golf_brief"}, 0.6, nil, now.Add(2*time.Second),
	)
	if err != nil || !created || len(hired.Learning) != 1 {
		t.Fatalf("proposed learning=%#v created=%v err=%v", hired.Learning, created, err)
	}
	pending := hired.Learning[0]
	if pending.Status != "pending" || pending.Origin != "completed_work" || pending.RunID == "" || pending.ArtifactID == "" || pending.SourceThreadID != "country_golf" || len(pending.SourceRefs) != 2 || pending.Confidence != 0.6 {
		t.Fatalf("learning provenance=%#v", pending)
	}
	if profile, ok := state.agentContextProfile(hired.ID); !ok || len(profile.ActiveLearning) != 0 {
		t.Fatalf("pending learning entered active context: %#v ok=%v", profile.ActiveLearning, ok)
	}
	hired, err = state.resolveAgentLearning(hired.ID, hired.Revision, pending.ID, "approve", "", now.Add(3*time.Second))
	if err != nil || hired.Learning[0].Status != "reviewed" {
		t.Fatalf("approved learning=%#v err=%v", hired.Learning, err)
	}
	profile, ok := state.agentContextProfile(hired.ID)
	if !ok || len(profile.ActiveLearning) != 0 {
		t.Fatalf("human approval bypassed the separate completed-work admission gate: %#v ok=%v", profile.ActiveLearning, ok)
	}
	state.setCompletedWorkLearningAdmission(func(learning STRIDEProductAgentLearning) bool {
		return learning.ID == pending.ID && learning.ArtifactID == pending.ArtifactID && learning.RunID == pending.RunID
	})
	profile, ok = state.agentContextProfile(hired.ID)
	if !ok || len(profile.ActiveLearning) != 1 || profile.ActiveLearning[0].ArtifactID != pending.ArtifactID {
		t.Fatalf("provenance-admitted learning missing from active context: %#v ok=%v", profile.ActiveLearning, ok)
	}
	snapshot, err := state.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEProductState(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restoredAgent, found := restored.agentRecord(hired.ID)
	if !found || len(restoredAgent.Learning) != 1 || len(restoredAgent.Learning[0].SourceRefs) != 2 || restoredAgent.Learning[0].Origin != "completed_work" {
		t.Fatalf("restored provenance-bound learning=%#v found=%v", restoredAgent.Learning, found)
	}
}
