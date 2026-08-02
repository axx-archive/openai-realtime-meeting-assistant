package e9readiness

import (
	"fmt"
	"sort"
)

type ReadinessReport struct {
	ManifestID      string   `json:"manifestId"`
	ContractValid   bool     `json:"contractValid"`
	ActivationReady bool     `json:"activationReady"`
	State           string   `json:"state"`
	Blockers        []string `json:"blockers"`
	ClaimsExcluded  []string `json:"claimsExcluded"`
}

type WorkerIsolationReport struct {
	PolicyID              string   `json:"policyId"`
	ContractValid         bool     `json:"contractValid"`
	DeploymentClosed      bool     `json:"deploymentClosed"`
	ActivationReady       bool     `json:"activationReady"`
	State                 string   `json:"state"`
	ComposeEgressEnforced bool     `json:"composeEgressEnforced"`
	Blockers              []string `json:"blockers"`
	ClaimsExcluded        []string `json:"claimsExcluded"`
}

// EvaluateReadiness reports a valid repository plan as externally pending.
// There is deliberately no code path in E9 that changes ActivationReady to
// true; only the separately authorized E10 evidence gate may do that.
func EvaluateReadiness(manifest ReadinessManifest) ReadinessReport {
	report := ReadinessReport{
		ManifestID: manifest.ManifestID,
		State:      "invalid",
		Blockers:   []string{"manifest validation failed"},
		ClaimsExcluded: []string{
			"managed PostgreSQL HA provisioned",
			"immutable offsite backup current",
			"independent KMS custody assigned",
			"separate restore host qualified",
			"live failover or traffic shift complete",
		},
	}
	if err := ValidateReadinessManifest(manifest); err != nil {
		report.Blockers = []string{err.Error()}
		return report
	}
	report.ContractValid = true
	report.State = "external_pending"
	report.Blockers = append([]string(nil), manifest.ExternalEvidenceRequired...)
	return report
}

// EvaluateWorkerIsolation proves only that this candidate has removed the
// reusable Compose executor and that the target contract remains fail-closed.
// It cannot prove an external orchestrator, egress gateway, credential broker,
// container runtime, quota enforcer, or callback replay store.
func EvaluateWorkerIsolation(policy WorkerIsolationPolicy, candidate WorkerCandidateSources) WorkerIsolationReport {
	report := WorkerIsolationReport{
		PolicyID: policy.PolicyID,
		State:    "invalid",
		Blockers: []string{"worker isolation validation failed"},
		ClaimsExcluded: []string{
			"ephemeral per-run worker installed",
			"read-only root and bounded mounts enforced",
			"default-deny egress enforced",
			"short-lived run-bound credentials issued",
			"resource and network quotas enforced",
			"signed nonce-bound callback replay fence installed",
		},
	}
	if err := ValidateWorkerIsolation(policy); err != nil {
		report.Blockers = []string{err.Error()}
		return report
	}
	report.ContractValid = true
	if err := AuditWorkerCandidate(policy, candidate); err != nil {
		report.Blockers = []string{err.Error()}
		return report
	}
	report.DeploymentClosed = true
	report.State = "external_pending"
	report.Blockers = append([]string(nil), policy.ExternalEvidenceRequired...)
	return report
}

// ContractDrillReceipt is evidence only of a manifest/state-machine contract
// exercise. It must never be interpreted as product integration evidence.
type ContractDrillReceipt struct {
	DrillID                string   `json:"drillId"`
	EvidenceClass          string   `json:"evidenceClass"`
	State                  string   `json:"state"`
	Synthetic              bool     `json:"synthetic"`
	ProviderCalls          bool     `json:"providerCalls"`
	ProductionMutation     bool     `json:"productionMutation"`
	ProductionReady        bool     `json:"productionReady"`
	VirtualHours           int      `json:"virtualHours"`
	Sittings               int      `json:"sittings"`
	ConsultationRuns       int      `json:"consultationRuns"`
	DeclaredCoverage       []string `json:"declaredCoverage"`
	ExecutedSystems        []string `json:"executedSystems"`
	ClaimsExcluded         []string `json:"claimsExcluded"`
	PassedScenarios        []string `json:"passedScenarios"`
	PassedWorkforceSteps   []string `json:"passedWorkforceSteps"`
	ExternalPending        []string `json:"externalPending"`
	HumanCallsContinued    bool     `json:"humanCallsContinued"`
	RoomIsolationPreserved bool     `json:"roomIsolationPreserved"`
	NewWorkerAccessRevoked bool     `json:"newWorkerAccessRevoked"`
	HistoryAttributable    bool     `json:"historyAttributable"`
}

var requiredScenarioAssertions = map[string][]string{
	"app_replica_loss":       {"route_failover_expected", "media_continuity_expected", "traffic_shift_remains_simulated"},
	"consent_withdrawal":     {"specialist_observation_revoked", "no_new_context", "human_call_continues"},
	"control_failover":       {"route_failover_expected", "no_unauthorized_work", "traffic_shift_remains_simulated"},
	"participant_churn":      {"rooms_isolated", "human_call_continues"},
	"quota_exhaustion":       {"specialist_unavailable", "human_call_continues", "no_unbounded_retry"},
	"realtime_disconnect":    {"specialist_unavailable", "human_call_continues", "no_cross_room_context"},
	"restore_tamper":         {"restore_refused", "purge_authority_preserved", "no_service_start"},
	"specialist_kill_switch": {"specialist_unavailable", "human_call_continues", "no_new_context"},
}

// ExecuteContractDrill evaluates only declared state transitions and safety
// assertions. It invokes no product runtime, WebRTC stack, persistence,
// provider, filesystem, network, or live traffic. It therefore produces a
// contract-only receipt, never deterministic integration verification.
func ExecuteContractDrill(manifest ContractDrillManifest) (ContractDrillReceipt, error) {
	if err := ValidateContractDrillManifest(manifest); err != nil {
		return ContractDrillReceipt{}, err
	}
	passed := make([]string, 0, len(manifest.Scenarios))
	humanCallsContinued := true
	roomIsolationPreserved := true
	for _, scenario := range manifest.Scenarios {
		if !containsAll(scenario.Assertions, requiredScenarioAssertions[scenario.Fault]) {
			return ContractDrillReceipt{}, fmt.Errorf("scenario %s omits required safety assertions for %s", scenario.ID, scenario.Fault)
		}
		observed, state, err := simulateScenario(manifest.Rooms, scenario)
		if err != nil {
			return ContractDrillReceipt{}, err
		}
		if !containsAll(observed, scenario.Assertions) {
			return ContractDrillReceipt{}, fmt.Errorf("scenario %s declares assertions not produced by the contract reducer", scenario.ID)
		}
		humanCallsContinued = humanCallsContinued && state.allHumanCallsActive
		roomIsolationPreserved = roomIsolationPreserved && !state.crossRoomContext
		passed = append(passed, scenario.ID)
	}

	workforce, err := executeWorkforceLifecycle(manifest.WorkforceSteps)
	if err != nil {
		return ContractDrillReceipt{}, err
	}
	sort.Strings(passed)
	return ContractDrillReceipt{
		DrillID:            manifest.DrillID,
		EvidenceClass:      "synthetic_only",
		State:              "contract_only",
		Synthetic:          true,
		ProviderCalls:      false,
		ProductionMutation: false,
		ProductionReady:    false,
		VirtualHours:       manifest.Soak.VirtualHours,
		Sittings:           manifest.Soak.Sittings,
		ConsultationRuns:   manifest.Consultations.RoundsPerRoom * len(manifest.Rooms),
		DeclaredCoverage:   append([]string(nil), manifest.DeclaredCoverage...),
		ExecutedSystems:    []string{"e9readiness.strict_manifest_validator", "e9readiness.contract_state_machine"},
		ClaimsExcluded: []string{
			"product/runtime integration verified",
			"media or WebRTC behavior verified",
			"persistence, restore, or failover verified",
			"tenant isolation verified outside the contract reducer",
			"provider, native-device, or live-traffic qualification",
		},
		PassedScenarios:        passed,
		PassedWorkforceSteps:   append([]string(nil), manifest.WorkforceSteps...),
		ExternalPending:        append([]string(nil), manifest.ExternalPending...),
		HumanCallsContinued:    humanCallsContinued,
		RoomIsolationPreserved: roomIsolationPreserved,
		NewWorkerAccessRevoked: workforce.accessRevoked,
		HistoryAttributable:    workforce.historyAttributable,
	}, nil
}

type simulatedFaultState struct {
	allHumanCallsActive bool
	crossRoomContext    bool
}

func simulateScenario(rooms []DrillRoom, scenario DrillScenario) ([]string, simulatedFaultState, error) {
	state := simulatedFaultState{allHumanCallsActive: true}
	if scenario.RoomID != "" && !roomExists(rooms, scenario.RoomID) {
		return nil, state, fmt.Errorf("scenario %s references unknown room %s", scenario.ID, scenario.RoomID)
	}
	var observed []string
	switch scenario.Fault {
	case "app_replica_loss":
		observed = append(observed, "route_failover_expected", "media_continuity_expected", "traffic_shift_remains_simulated")
	case "consent_withdrawal":
		observed = append(observed, "specialist_observation_revoked", "no_new_context", "human_call_continues")
	case "control_failover":
		observed = append(observed, "route_failover_expected", "no_unauthorized_work", "traffic_shift_remains_simulated")
	case "participant_churn":
		observed = append(observed, "rooms_isolated", "human_call_continues")
	case "quota_exhaustion":
		observed = append(observed, "specialist_unavailable", "human_call_continues", "no_unbounded_retry")
	case "realtime_disconnect":
		observed = append(observed, "specialist_unavailable", "human_call_continues", "no_cross_room_context")
	case "restore_tamper":
		observed = append(observed, "restore_refused", "purge_authority_preserved", "no_service_start")
	case "specialist_kill_switch":
		observed = append(observed, "specialist_unavailable", "human_call_continues", "no_new_context")
	default:
		return nil, state, fmt.Errorf("unknown synthetic fault %q", scenario.Fault)
	}
	return observed, state, nil
}

type workforceState struct {
	discovered, inspected, trialled, hired, messaged, assigned, selected, introduced bool
	invited, workApproved, learningInspected, learningCorrected, updatePreviewed     bool
	updateRolledBack, paused, offboarded, historyAttributable, accessRevoked         bool
}

func executeWorkforceLifecycle(steps []string) (workforceState, error) {
	var state workforceState
	for _, step := range steps {
		switch step {
		case "discover_mary":
			state.discovered = true
		case "inspect_mary":
			if !state.discovered {
				return state, transitionError(step)
			}
			state.inspected = true
		case "trial_mary":
			if !state.inspected {
				return state, transitionError(step)
			}
			state.trialled = true
		case "hire_bounded":
			if !state.trialled {
				return state, transitionError(step)
			}
			state.hired = true
		case "direct_message":
			if !state.hired {
				return state, transitionError(step)
			}
			state.messaged = true
		case "add_to_dog_perfect":
			if !state.hired {
				return state, transitionError(step)
			}
			state.assigned = true
		case "scout_select":
			if !state.assigned {
				return state, transitionError(step)
			}
			state.selected = true
		case "scout_introduce":
			if !state.selected {
				return state, transitionError(step)
			}
			state.introduced = true
		case "invite_to_meeting":
			if !state.introduced {
				return state, transitionError(step)
			}
			state.invited = true
		case "approve_one_workrun":
			if !state.invited {
				return state, transitionError(step)
			}
			state.workApproved = true
		case "inspect_learning":
			if !state.workApproved {
				return state, transitionError(step)
			}
			state.learningInspected = true
		case "correct_learning":
			if !state.learningInspected {
				return state, transitionError(step)
			}
			state.learningCorrected = true
		case "preview_update":
			if !state.learningCorrected {
				return state, transitionError(step)
			}
			state.updatePreviewed = true
		case "rollback_update":
			if !state.updatePreviewed {
				return state, transitionError(step)
			}
			state.updateRolledBack = true
		case "pause":
			if !state.updateRolledBack {
				return state, transitionError(step)
			}
			state.paused = true
		case "offboard":
			if !state.paused {
				return state, transitionError(step)
			}
			state.offboarded = true
		case "verify_history_attributable":
			if !state.offboarded {
				return state, transitionError(step)
			}
			state.historyAttributable = true
		case "verify_new_access_revoked":
			if !state.offboarded {
				return state, transitionError(step)
			}
			state.accessRevoked = true
		default:
			return state, fmt.Errorf("unknown workforce step %q", step)
		}
	}
	if !state.historyAttributable || !state.accessRevoked {
		return state, fmt.Errorf("workforce lifecycle ended without attribution and access-revocation proof")
	}
	return state, nil
}

func transitionError(step string) error {
	return fmt.Errorf("workforce step %s is out of order", step)
}
