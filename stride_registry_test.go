package main

import (
	"errors"
	"testing"
	"time"
)

func strideTestRegistryEntry() STRIDERegistryEntry {
	return STRIDERegistryEntry{
		TenantID: "bonfire", Kind: STRIDERegistryWorkflow, Key: "insights_opportunities_v1", Revision: 1,
		Contract: strideTestRef(STRIDEContractWorkRun, "workflow_contract"), Feature: STRIDEFeatureTeamAgentAssignment, Status: STRIDERegistryDraft,
		SchemaDigest: strideTestDigest("c"), CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		Capability: STRIDERuntimeCapability{StrictSchema: true, SideEffectClasses: []string{"read_only"}, MinimumReasoningEffort: "low", MaximumReasoningEffort: "high"},
	}
}

func TestSTRIDERegistryIsDefaultOffAndActivationFenced(t *testing.T) {
	registry := NewSTRIDERegistry()
	entry := strideTestRegistryEntry()
	if err := registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(entry.TenantID, entry.Kind, entry.Key); !errors.Is(err, ErrSTRIDEFeatureDisabled) {
		t.Fatalf("resolve error=%v, want disabled", err)
	}
	if err := registry.SetFeatureEnabled(entry.Feature, true); !errors.Is(err, ErrSTRIDEActivationFenced) {
		t.Fatalf("enable error=%v, want activation fence", err)
	}
}

func TestSTRIDERegistryHasIndependentRollbackSeams(t *testing.T) {
	registry := NewSTRIDERegistry()
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[STRIDEFeature]bool, len(snapshot.Features))
	for _, feature := range snapshot.Features {
		if feature.Enabled {
			t.Fatalf("feature %s must default off", feature.Feature)
		}
		seen[feature.Feature] = true
	}
	for _, want := range []STRIDEFeature{
		STRIDEFeaturePublicProjection,
		STRIDEFeatureCrossSurfaceRetrieval,
		STRIDEFeatureScoutFileSearch,
		STRIDEFeatureScoutFileActions,
		STRIDEFeatureAgentGIFActions,
		STRIDEFeatureSuggestedWorkDetection,
		STRIDEFeatureSuggestedWorkExecution,
		STRIDEFeatureInsightsWorkflow,
		STRIDEFeatureMarketplaceDiscovery,
		STRIDEFeatureMarketplaceTrial,
		STRIDEFeatureTeamAgentHire,
		STRIDEFeatureMarketplaceUpdate,
		STRIDEFeatureRoomVoiceInvocation,
		STRIDEFeatureEnrichedScoutRouting,
		STRIDEFeatureRelationshipMemory,
		STRIDEFeatureRichResponseModes,
		STRIDEFeatureModelRouteCanary,
	} {
		if !seen[want] {
			t.Errorf("missing independent rollback seam %s", want)
		}
	}
}

func TestSTRIDERegistryHasExactDefaultOffW1Seams(t *testing.T) {
	registry := NewSTRIDERegistry()
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[STRIDEFeature]bool, len(snapshot.Features))
	for _, state := range snapshot.Features {
		if state.Enabled {
			t.Fatalf("feature %s unexpectedly enabled", state.Feature)
		}
		seen[state.Feature] = true
	}
	for _, feature := range []STRIDEFeature{
		STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureActiveOrganizationSession,
		STRIDEFeatureContributionCandidateDetection, STRIDEFeatureContributionReview, STRIDEFeatureWorkRecordPrivate,
		STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch,
		STRIDEFeatureNetworkContact, STRIDEFeatureNetworkQueryParserProvider, STRIDEFeatureNetworkSemanticReranker,
		STRIDEFeaturePersonMyMindContext,
	} {
		if !seen[feature] {
			t.Errorf("missing W1 feature seam %s", feature)
		}
		if err := registry.SetFeatureEnabled(feature, true); !errors.Is(err, ErrSTRIDEActivationFenced) {
			t.Errorf("feature %s escaped activation fence: %v", feature, err)
		}
	}
}

func TestSTRIDERegistryQuarantineAndSnapshotDigest(t *testing.T) {
	registry := NewSTRIDERegistry()
	entry := strideTestRegistryEntry()
	if err := registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	before, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	again, err := registry.Snapshot()
	if err != nil || before.Digest != again.Digest {
		t.Fatalf("snapshot digest drift: %q %q %v", before.Digest, again.Digest, err)
	}
	if err := registry.Quarantine(entry.TenantID, entry.Kind, entry.Key, 2, "schema_mismatch"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(entry.TenantID, entry.Kind, entry.Key); !errors.Is(err, ErrSTRIDERegistryQuarantined) {
		t.Fatalf("resolve error=%v, want quarantined", err)
	}
	after, err := registry.Snapshot()
	if err != nil || before.Digest == after.Digest {
		t.Fatalf("quarantine snapshot did not change: %q %q %v", before.Digest, after.Digest, err)
	}
}
