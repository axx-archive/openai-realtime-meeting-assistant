package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// StrideE10W7OperatorInput contains only read-only observations. It is not an
// acceptance receipt: Ready can become true only through the immutable-root
// signed W7 manifest validator.
type StrideE10W7OperatorInput struct {
	CandidateCommit         string
	CandidateBuild          int64
	ActiveReleaseLedgerJSON []byte
	HealthJSON              []byte
	ReadinessJSON           []byte
	EASBuildJSON            []byte
	AcceptanceManifest      *StrideE10W7AcceptanceManifest
}

type StrideE10W7OperatorGap struct {
	Gate     string `json:"gate"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
	Command  string `json:"command"`
	External bool   `json:"external"`
}

type StrideE10W7OperatorReport struct {
	Ready             bool                       `json:"ready"`
	CandidateCommit   string                     `json:"candidateCommit"`
	CandidateBuild    int64                      `json:"candidateBuild"`
	LiveReleaseCommit string                     `json:"liveReleaseCommit,omitempty"`
	NativeCommit      string                     `json:"nativeCommit,omitempty"`
	NativeBuild       string                     `json:"nativeBuild,omitempty"`
	Gaps              []StrideE10W7OperatorGap   `json:"gaps"`
	SignedResult      StrideE10W7PreflightResult `json:"signedResult"`
}

var strideE10W7OperatorCommands = map[string]string{
	"exact_native_build":              "cd mobile && npx eas-cli@21.4.0 build --platform ios --profile production --non-interactive --wait",
	"native_distribution":             "cd mobile && npx eas-cli@21.4.0 build:list --platform ios --limit 5 --json --non-interactive",
	"iphone_physical":                 "node scripts/native-apple-release-proofpack.mjs --run-id <exact-release-run> --full-gates",
	"ipad_physical":                   "node scripts/native-apple-release-proofpack.mjs --run-id <exact-release-run> --full-gates",
	"accessibility_privacy":           "node scripts/native-apple-release-readiness.mjs --apple-dir apple --evidence-file <signed-external-evidence.json> --strict",
	"restrictive_turn_webrtc":         "node scripts/native-apple-create-room-interop-observation.mjs --proofpack-dir <proofpack> <physical-room-confirmations>",
	"encrypted_offsite_restore":       "<custody-owner> run isolated four-root offsite restore and provide signed restore_execution receipt",
	"ha_failover":                     "<platform-owner> run PostgreSQL, application, TURN, and reversible-traffic failover drills and provide four signed subreceipts",
	"independent_release_attestation": "<off-host-independent-attestor> sign exact source/archive/image/binary/config/native-artifact evidence",
}

func BuildStrideE10W7OperatorReport(input StrideE10W7OperatorInput) StrideE10W7OperatorReport {
	report := StrideE10W7OperatorReport{CandidateCommit: strings.TrimSpace(input.CandidateCommit), CandidateBuild: input.CandidateBuild}
	if input.AcceptanceManifest != nil {
		report.SignedResult = ValidateStrideE10W7Acceptance(*input.AcceptanceManifest)
		if report.SignedResult.Ready && input.AcceptanceManifest.CandidateCommit == report.CandidateCommit && input.AcceptanceManifest.CandidateBuild == report.CandidateBuild {
			report.Ready = true
			return report
		}
	}

	var ledger struct {
		Active struct {
			ReleaseCommit string `json:"releaseCommit"`
		} `json:"active"`
	}
	if json.Unmarshal(input.ActiveReleaseLedgerJSON, &ledger) == nil {
		report.LiveReleaseCommit = strings.TrimSpace(ledger.Active.ReleaseCommit)
	}
	var health struct {
		Release struct {
			ReleaseCommit      string `json:"releaseCommit"`
			ProcessQualified   bool   `json:"processQualified"`
			ExternallyAttested bool   `json:"externallyAttested"`
		} `json:"release"`
	}
	healthOK := json.Unmarshal(input.HealthJSON, &health) == nil && health.Release.ReleaseCommit == report.CandidateCommit && health.Release.ProcessQualified
	if report.LiveReleaseCommit == "" {
		report.LiveReleaseCommit = health.Release.ReleaseCommit
	}
	if !strideE10W7Commit(report.CandidateCommit) || report.CandidateBuild < 1 {
		report.addGap("candidate", "invalid", "candidate commit/build is not frozen", "git rev-parse HEAD && rg -n 'buildNumber' mobile/app.config.ts", false)
	}
	if report.LiveReleaseCommit != report.CandidateCommit || !healthOK {
		report.addGap("exact_live_release", "mismatch", "active ledger and public health do not prove the candidate release", "curl -fsS https://thebonfire.xyz/healthz && ssh root@146.190.171.224 'cat /opt/meetingassist-releases/active-release.json'", false)
	}

	var eas struct {
		Status          string `json:"status"`
		AppBuildVersion string `json:"appBuildVersion"`
		GitCommitHash   string `json:"gitCommitHash"`
		Distribution    string `json:"distribution"`
		IsSimulator     bool   `json:"isForIosSimulator"`
	}
	if json.Unmarshal(input.EASBuildJSON, &eas) == nil {
		report.NativeCommit, report.NativeBuild = strings.TrimSpace(eas.GitCommitHash), strings.TrimSpace(eas.AppBuildVersion)
	}
	if eas.Status != "FINISHED" || eas.GitCommitHash != report.CandidateCommit || eas.AppBuildVersion != jsonNumber(report.CandidateBuild) || eas.Distribution != "STORE" || eas.IsSimulator {
		report.addGap("exact_native_build", "missing", "latest observed EAS store build is not the exact candidate commit/build", strideE10W7OperatorCommands["exact_native_build"], true)
	}

	var ready struct {
		Checks struct {
			Backup struct {
				Encrypted       bool   `json:"encrypted"`
				Offsite         string `json:"offsite"`
				RestoreVerified bool   `json:"restoreVerified"`
			} `json:"backup"`
		} `json:"checks"`
	}
	_ = json.Unmarshal(input.ReadinessJSON, &ready)
	if !ready.Checks.Backup.Encrypted || ready.Checks.Backup.Offsite != "active" || !ready.Checks.Backup.RestoreVerified {
		report.addGap("encrypted_offsite_restore", "external_waiting", "live readiness does not prove encrypted active offsite custody and an isolated restore", strideE10W7OperatorCommands["encrypted_offsite_restore"], true)
	}
	for _, gate := range []string{"native_distribution", "iphone_physical", "ipad_physical", "accessibility_privacy", "restrictive_turn_webrtc", "ha_failover"} {
		report.addGap(gate, "external_waiting", "no immutable-root signed W7 acceptance manifest proves this gate", strideE10W7OperatorCommands[gate], true)
	}
	if !health.Release.ExternallyAttested {
		report.addGap("independent_release_attestation", "external_waiting", "public health reports externallyAttested=false", strideE10W7OperatorCommands["independent_release_attestation"], true)
	}
	if input.AcceptanceManifest != nil {
		for _, reason := range report.SignedResult.Reasons {
			report.addGap("signed_manifest", "invalid", reason, "node scripts/e10-w7-readonly-preflight.mjs --policy <absolute-root-signed-policy.json> --result <absolute-signed-result.json>", true)
		}
	}
	sort.Slice(report.Gaps, func(i, j int) bool { return report.Gaps[i].Gate < report.Gaps[j].Gate })
	return report
}

func (r *StrideE10W7OperatorReport) addGap(gate, state, reason, command string, external bool) {
	for _, current := range r.Gaps {
		if current.Gate == gate && current.Reason == reason {
			return
		}
	}
	r.Gaps = append(r.Gaps, StrideE10W7OperatorGap{Gate: gate, State: state, Reason: reason, Command: command, External: external})
}

func jsonNumber(value int64) string {
	if value < 1 {
		return ""
	}
	return fmtInt64(value)
}

func fmtInt64(value int64) string {
	// Avoid accepting floating or exponent-form build numbers from external JSON.
	return strings.TrimSpace(string(mustJSON(value)))
}

func mustJSON(value any) []byte { body, _ := json.Marshal(value); return body }
