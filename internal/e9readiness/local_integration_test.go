package e9readiness

import (
	"strings"
	"testing"
)

func validLocalIntegrationReceipt() LocalIntegrationReceipt {
	return LocalIntegrationReceipt{
		SchemaVersion: LocalIntegrationReceiptSchema, EvidenceClass: "local_deterministic_integration", State: "passed",
		TempResourcesOnly: true, NetworkScope: "loopback_only",
		ExecutedSystems: append([]string(nil), requiredLocalIntegrationSystems...),
		Routing: LocalRoutingObservation{
			InitialReplica: "app-a", FailoverReplica: "app-b", RestoredControlReplica: "app-b", PersistenceReplica: "app-c",
			InitialStatus: 200, FailoverStatus: 200, RestoredControlStatus: 200, PersistenceStatus: 200,
			DeadPrimaryObserved: true, SelectedRoutePersisted: true,
		},
		MediaIsolation: LocalMediaIsolationObservation{
			EvidenceClass: "room_scope_control_probe", RoomA: "room-a", RoomB: "room-b",
			SameRoomStatus: 200, CrossRoomStatus: 403, DuringAppLossStatus: 200, RoomLaneDistinct: true,
		},
		Persistence: LocalPersistenceObservation{
			InitialGeneration: 91, FailoverGeneration: 92, PurgedGeneration: 93, RestoredGeneration: 94,
			InitialDecisionCount: 1, FailoverDecisionCount: 1, PurgedDecisionCount: 0, RestoredDecisionCount: 0,
			PurgeGeneration: 1, PurgePersisted: true, StaleRollbackRefused: true, CurrentRestoreAccepted: true,
		},
		Timings: []MeasuredTiming{
			{Operation: "app_failover", ElapsedNanoseconds: 1},
			{Operation: "media_during_app_loss", ElapsedNanoseconds: 2},
			{Operation: "control_restore", ElapsedNanoseconds: 3},
			{Operation: "purge_persist", ElapsedNanoseconds: 4},
			{Operation: "signed_state_restore", ElapsedNanoseconds: 5},
			{Operation: "stale_rollback_refusal", ElapsedNanoseconds: 6},
		},
		ClaimsExcluded: append([]string(nil), requiredLocalIntegrationExclusions...),
	}
}

func TestValidateLocalIntegrationReceiptAcceptsNarrowObservedEvidence(t *testing.T) {
	receipt := validLocalIntegrationReceipt()
	if err := ValidateLocalIntegrationReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	SortLocalIntegrationReceipt(&receipt)
	if receipt.ExecutedSystems[0] == "" || receipt.Timings[0].Operation != "app_failover" {
		t.Fatalf("receipt sorting failed: %+v", receipt)
	}
}

func TestValidateLocalIntegrationReceiptFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LocalIntegrationReceipt)
		want   string
	}{
		{"provider call", func(value *LocalIntegrationReceipt) { value.ProviderCalls = true }, "resource or mutation"},
		{"production mutation", func(value *LocalIntegrationReceipt) { value.ProductionMutation = true }, "resource or mutation"},
		{"public network", func(value *LocalIntegrationReceipt) { value.NetworkScope = "internet" }, "resource or mutation"},
		{"no dead primary", func(value *LocalIntegrationReceipt) { value.Routing.DeadPrimaryObserved = false }, "routing"},
		{"cross room leaked", func(value *LocalIntegrationReceipt) { value.MediaIsolation.CrossRoomStatus = 200 }, "media-scope"},
		{"purge missing", func(value *LocalIntegrationReceipt) { value.Persistence.PurgePersisted = false }, "persistence"},
		{"rollback accepted", func(value *LocalIntegrationReceipt) { value.Persistence.StaleRollbackRefused = false }, "persistence"},
		{"fixed timing", func(value *LocalIntegrationReceipt) { value.Timings[0].ElapsedNanoseconds = 0 }, "timing"},
		{"missing exclusion", func(value *LocalIntegrationReceipt) { value.ClaimsExcluded = value.ClaimsExcluded[:2] }, "claim exclusion"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := validLocalIntegrationReceipt()
			test.mutate(&receipt)
			if err := ValidateLocalIntegrationReceipt(receipt); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
