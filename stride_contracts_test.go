package main

import (
	"strings"
	"testing"
	"time"
)

func strideTestDigest(seed string) string { return strings.Repeat(seed, 64)[:64] }

func strideTestHeader(kind STRIDEContractType, id string) STRIDEContractHeader {
	return STRIDEContractHeader{TenantID: "bonfire", ID: id, Revision: 1, SchemaVersion: STRIDEContractSchemaVersion, ContractType: kind, ContentDigest: strideTestDigest("a"), CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
}

func strideTestRef(kind STRIDEContractType, id string) STRIDEReference {
	return STRIDEReference{ContractType: kind, ID: id, Revision: 1, Digest: strideTestDigest("b")}
}

func strideTestAudience() STRIDEAudience {
	return STRIDEAudience{Visibility: "organization", Principals: []string{"member_aj"}}
}

func TestConversationEventRejectsOperationalBodiesAndUnknownEnum(t *testing.T) {
	event := ConversationEvent{
		Header: strideTestHeader(STRIDEContractConversationEvent, "event_1"), SourceType: "chat", SourceID: "thread_team", ThreadID: "thread_team",
		AuthorPrincipal: "member_aj", AuthorName: "AJ", OccurredAt: time.Now().UTC(), IngestedAt: time.Now().UTC(), EventType: "message",
		ContentRevision: 1, ContentDigest: strideTestDigest("c"), Audience: strideTestAudience(), ACLVersion: 1, RetentionPolicy: "company_default", PurgeGeneration: 0, Provenance: "client",
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	event.EventType = "provider_prompt"
	if err := event.Validate(); err == nil {
		t.Fatal("unknown event type was accepted")
	}
}

func TestCollaborationPreferenceRejectsSensitiveInference(t *testing.T) {
	now := time.Now().UTC()
	pref := CollaborationPreference{
		Header: strideTestHeader(STRIDEContractCollaborationPreference, "preference_1"), SubjectPrincipal: "member_aj", Scope: "team", PreferenceType: "health_status",
		ValueDigest: strideTestDigest("d"), Origin: "inferred", Evidence: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "event_1")}, Confidence: .7,
		FirstObserved: now, LastObserved: now, Audience: strideTestAudience(), Status: "active",
	}
	if err := pref.Validate(); err == nil {
		t.Fatal("sensitive preference type was accepted")
	}
}

func TestPackageManifestRejectsUntrustedProvenance(t *testing.T) {
	manifest := AgentPackageManifest{
		Header: strideTestHeader(STRIDEContractAgentPackageManifest, "package_1"), PackageID: "package_1", PublisherID: "publisher_stride", PublisherAttestationDigest: strideTestDigest("e"),
		Version: "v1", Provenance: "third_party", PersonaSeedDigest: strideTestDigest("f"), AssetRefs: []STRIDEReference{strideTestRef(STRIDEContractRichMessagePart, "asset_1")},
		RequestedCapabilities: []string{"research"}, RuntimeClasses: []string{"server"}, ModelClasses: []string{"text"}, VoiceClasses: []string{"none"}, DataClassifications: []string{"internal"},
		EvalBundleRefs: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "eval_1")}, DependencySBOMRefs: []STRIDEReference{strideTestRef(STRIDEContractOutcome, "sbom_1")}, LicenseID: "internal", UpdatePolicy: "manual", MigrationCompatibility: "v1", Status: "draft",
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("third-party package was accepted")
	}
	manifest.Provenance = "stride_authored"
	if err := manifest.Validate(); err != nil {
		t.Fatalf("closed first-party package rejected: %v", err)
	}
}

func TestSortedSTRIDEReferencesAndDigestAreDeterministic(t *testing.T) {
	refs := []STRIDEReference{strideTestRef(STRIDEContractWorkRun, "run_b"), strideTestRef(STRIDEContractWorkRun, "run_a")}
	sorted := SortedSTRIDEReferences(refs)
	if sorted[0].ID != "run_a" || refs[0].ID != "run_b" {
		t.Fatalf("sort was not stable/non-mutating: %#v", refs)
	}
	first, err := STRIDEContractDigest(sorted)
	if err != nil {
		t.Fatal(err)
	}
	second, err := STRIDEContractDigest(SortedSTRIDEReferences(refs))
	if err != nil || first != second {
		t.Fatalf("digest drift: %q %q %v", first, second, err)
	}
}

func TestAgentContextEnvelopeAllowsNoRelationshipMemoryOrActiveWork(t *testing.T) {
	envelope := AgentContextEnvelope{
		Header:            strideTestHeader(STRIDEContractAgentContextEnvelope, "context_1"),
		AgentProfile:      strideTestRef(STRIDEContractAgentCoreProfile, "scout_profile"),
		Capability:        strideTestRef(STRIDEContractAgentCapabilityManifest, "scout_capability"),
		ChannelPolicy:     strideTestRef(STRIDEContractChannelNormProfile, "team_norms"),
		InvocationSurface: "private", InvocationReason: "direct_message", Requester: "member_aj", Recipients: []string{"member_aj"},
		CurrentTurn: strideTestRef(STRIDEContractConversationEvent, "turn_current"), RecentTurns: []STRIDEReference{strideTestRef(STRIDEContractConversationEvent, "turn_recent")},
		Evidence:      []STRIDEReference{strideTestRef(STRIDEContractKnowledgeAssertion, "evidence_1")},
		ResponseModes: []string{"text"}, PermittedTools: []string{"memory_read"}, Audience: strideTestAudience(),
		CoverageDigest: strideTestDigest("7"), ContextDigest: strideTestDigest("8"),
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("empty optional relationship/work context rejected: %v", err)
	}
}
