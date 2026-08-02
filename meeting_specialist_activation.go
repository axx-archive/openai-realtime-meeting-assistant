package main

// The in-meeting specialist control plane is deliberately separate from the
// provider/audio plane. A short-lived signed receipt can activate roster,
// request, approval, disclosure, and dismissal surfaces after a reviewed test
// ceremony. It cannot activate token minting, a model provider, tools, or audio.

import (
	"os"
	"sort"
	"strings"
	"time"
)

const (
	meetingSpecialistControlActivationDomain = "meeting_specialist_control_activation"
	meetingSpecialistControlActivationFormat = 1
	meetingSpecialistControlActivationEnv    = "STRIDE_MEETING_SPECIALIST_CONTROL_ACTIVATION_PATH"
	meetingSpecialistControlActivationMaxTTL = 24 * time.Hour
)

var meetingSpecialistControlFeatures = []STRIDEFeature{
	STRIDEFeatureMeetingInvitation,
	STRIDEFeatureSpecialistContext,
	STRIDEFeatureVisibleSpecialist,
}

type meetingSpecialistControlActivationPayload struct {
	Format         int             `json:"format"`
	TenantID       string          `json:"tenantId"`
	Generation     uint64          `json:"generation"`
	KeyID          string          `json:"keyId"`
	Features       []STRIDEFeature `json:"features"`
	EvidenceDigest string          `json:"evidenceDigest"`
	// StateMinimumGeneration is the externally reviewed high-water floor for
	// the specialist ledger. It is independent of the STRIDE runtime's own
	// snapshot generation and makes rollback fencing explicit in the receipt.
	StateMinimumGeneration uint64    `json:"stateMinimumGeneration"`
	BootstrapEmpty         bool      `json:"bootstrapEmpty"`
	IssuedAt               time.Time `json:"issuedAt"`
	ExpiresAt              time.Time `json:"expiresAt"`
}

type meetingSpecialistControlActivationEnvelope struct {
	Payload   meetingSpecialistControlActivationPayload `json:"payload"`
	Digest    string                                    `json:"digest"`
	Signature string                                    `json:"signature"`
}

func meetingSpecialistControlActivationEnabled(runtime *STRIDERuntime, now time.Time) bool {
	_, ok := loadMeetingSpecialistControlActivation(runtime, now)
	return ok
}

func loadMeetingSpecialistControlActivation(runtime *STRIDERuntime, now time.Time) (meetingSpecialistControlActivationPayload, bool) {
	if runtime == nil || !runtime.config.Authority.valid() {
		return meetingSpecialistControlActivationPayload{}, false
	}
	health := runtime.Health()
	if health.State != STRIDERuntimeStandby {
		return meetingSpecialistControlActivationPayload{}, false
	}
	path := strings.TrimSpace(os.Getenv(meetingSpecialistControlActivationEnv))
	if path == "" {
		return meetingSpecialistControlActivationPayload{}, false
	}
	var envelope meetingSpecialistControlActivationEnvelope
	if err := readSTRIDERuntimeJSON(path, &envelope); err != nil {
		return meetingSpecialistControlActivationPayload{}, false
	}
	payload := envelope.Payload
	digest, err := STRIDEContractDigest(payload)
	if err != nil || digest != envelope.Digest || payload.Format != meetingSpecialistControlActivationFormat || payload.TenantID != canonicalTenantID() || payload.KeyID != runtime.config.Authority.KeyID || payload.Generation < runtime.config.MinimumGeneration || payload.Generation < health.Generation || payload.Generation == 0 || payload.StateMinimumGeneration == 0 || !isHexDigest(payload.EvidenceDigest) {
		return meetingSpecialistControlActivationPayload{}, false
	}
	issuedAt, expiresAt := payload.IssuedAt.UTC(), payload.ExpiresAt.UTC()
	now = now.UTC()
	if issuedAt.IsZero() || expiresAt.IsZero() || issuedAt.After(now) || !expiresAt.After(now) || !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > meetingSpecialistControlActivationMaxTTL {
		return meetingSpecialistControlActivationPayload{}, false
	}
	features := append([]STRIDEFeature(nil), payload.Features...)
	sort.Slice(features, func(i, j int) bool { return features[i] < features[j] })
	want := append([]STRIDEFeature(nil), meetingSpecialistControlFeatures...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(features) != len(want) {
		return meetingSpecialistControlActivationPayload{}, false
	}
	for index := range want {
		if features[index] != want[index] {
			return meetingSpecialistControlActivationPayload{}, false
		}
	}
	if !verifySTRIDESnapshotMAC(
		STRIDESnapshotRestorePolicy{Authority: runtime.config.Authority, MinimumGeneration: runtime.config.MinimumGeneration},
		meetingSpecialistControlActivationDomain, payload.KeyID, payload.Generation, envelope.Digest, envelope.Signature,
	) {
		return meetingSpecialistControlActivationPayload{}, false
	}
	return payload, true
}
