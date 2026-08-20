package main

import (
	"errors"
	"sort"
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

func TestRestoreSTRIDERegistryAcceptsOlderDefaultOffFeatureSet(t *testing.T) {
	registry := NewSTRIDERegistry()
	entry := strideTestRegistryEntry()
	if err := registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	omitted := STRIDEFeatureProjectRecordProjection
	features := snapshot.Features[:0]
	for _, feature := range snapshot.Features {
		if feature.Feature != omitted {
			features = append(features, feature)
		}
	}
	snapshot.Features = features
	snapshot.Digest, err = STRIDEContractDigest(struct {
		Entries  []STRIDERegistryEntry `json:"entries"`
		Features []STRIDEFeatureState  `json:"features"`
	}{snapshot.Entries, snapshot.Features})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoreSTRIDERegistry(snapshot)
	if err != nil {
		t.Fatalf("restore older valid snapshot: %v", err)
	}
	upgraded, err := restored.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, feature := range upgraded.Features {
		if feature.Feature == omitted {
			seen = true
			if feature.Enabled {
				t.Fatal("newly introduced feature restored enabled")
			}
		}
	}
	if !seen || len(upgraded.Features) != len(allSTRIDEFeatures) {
		t.Fatalf("restored snapshot did not seed all current default-off features: %+v", upgraded.Features)
	}
}

func TestRestoreSTRIDERegistryRejectsInvalidLegacySnapshots(t *testing.T) {
	base, err := NewSTRIDERegistry().Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	redigest := func(snapshot *STRIDERegistrySnapshot) {
		snapshot.Digest, err = STRIDEContractDigest(struct {
			Entries  []STRIDERegistryEntry `json:"entries"`
			Features []STRIDEFeatureState  `json:"features"`
		}{snapshot.Entries, snapshot.Features})
		if err != nil {
			t.Fatal(err)
		}
	}
	tests := map[string]func(*STRIDERegistrySnapshot){
		"enabled": func(snapshot *STRIDERegistrySnapshot) { snapshot.Features[0].Enabled = true },
		"unknown": func(snapshot *STRIDERegistrySnapshot) {
			snapshot.Features[0].Feature = STRIDEFeature("unknown_feature")
			sort.Slice(snapshot.Features, func(i, j int) bool { return snapshot.Features[i].Feature < snapshot.Features[j].Feature })
		},
		"duplicate": func(snapshot *STRIDERegistrySnapshot) { snapshot.Features[1] = snapshot.Features[0] },
		"entry feature omitted": func(snapshot *STRIDERegistrySnapshot) {
			entry := strideTestRegistryEntry()
			snapshot.Entries = []STRIDERegistryEntry{entry}
			features := snapshot.Features[:0]
			for _, feature := range snapshot.Features {
				if feature.Feature != entry.Feature {
					features = append(features, feature)
				}
			}
			snapshot.Features = features
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := STRIDERegistrySnapshot{Entries: append([]STRIDERegistryEntry(nil), base.Entries...), Features: append([]STRIDEFeatureState(nil), base.Features...), Digest: base.Digest}
			mutate(&snapshot)
			redigest(&snapshot)
			if _, restoreErr := restoreSTRIDERegistry(snapshot); !errors.Is(restoreErr, ErrSTRIDERegistryInvalid) {
				t.Fatalf("restore error=%v, want invalid", restoreErr)
			}
		})
	}
	tampered := base
	tampered.Features = append([]STRIDEFeatureState(nil), base.Features...)
	tampered.Features[0].Feature = STRIDEFeatureProjectRecordProjection
	if _, restoreErr := restoreSTRIDERegistry(tampered); !errors.Is(restoreErr, ErrSTRIDERegistryInvalid) {
		t.Fatalf("tampered digest restore error=%v, want invalid", restoreErr)
	}
}
