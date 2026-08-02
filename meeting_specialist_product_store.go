package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	meetingSpecialistProductSnapshotFormat    = 1
	meetingSpecialistProductSnapshotDomain    = "meeting_specialist_product"
	meetingSpecialistProductGenerationDomain  = "meeting_specialist_product_generation"
	defaultMeetingSpecialistProductSnapshot   = "stride/meeting-specialists.snapshot.json"
	defaultMeetingSpecialistProductGeneration = "stride/meeting-specialists.generation.json"
)

type MeetingSpecialistProductPersistence struct {
	SnapshotPath      string
	GenerationPath    string
	Authority         STRIDESnapshotMACAuthority
	MinimumGeneration uint64
	BootstrapEmpty    bool
}

type meetingSpecialistDurableScope struct {
	TenantID              string                                 `json:"tenantId"`
	RoomID                string                                 `json:"roomId"`
	SittingID             string                                 `json:"sittingId"`
	MediaGeneration       uint64                                 `json:"mediaGeneration"`
	RequesterPrincipal    string                                 `json:"requesterPrincipal"`
	Audience              STRIDEAudience                         `json:"audience"`
	ConsentPolicyRevision STRIDEReference                        `json:"consentPolicyRevision"`
	ConsentFences         []meetingSpecialistDurableConsentFence `json:"consentFences"`
}

// meetingSpecialistDurableConsentFence is the signed, server-only restart
// representation of a ConsentFence. The browser/native contract never sees
// these fields. Rehydrating the exact fences is important: an approval that
// survived a process restart must still be invalidated by a later withdrawal,
// policy revision, room sitting, or admission-generation change.
type meetingSpecialistDurableConsentFence struct {
	Binding      consentContributorBinding `json:"binding"`
	Lane         ConsentLane               `json:"lane"`
	Policy       string                    `json:"policy"`
	Generation   uint64                    `json:"generation"`
	RecordDigest string                    `json:"recordDigest"`
	IssuedAt     time.Time                 `json:"issuedAt"`
}

type meetingSpecialistDurableRecord struct {
	Invitation       MeetingAgentInvitation             `json:"invitation"`
	Agent            MeetingSpecialistCandidate         `json:"agent"`
	PurposeSummary   string                             `json:"purposeSummary,omitempty"`
	Limits           *MeetingSpecialistApprovalLimits   `json:"limits,omitempty"`
	Status           string                             `json:"status"`
	UpdatedAt        time.Time                          `json:"updatedAt"`
	TerminalEvidence *MeetingSpecialistTerminalEvidence `json:"terminalEvidence,omitempty"`
	Scope            meetingSpecialistDurableScope      `json:"scope"`
}

type meetingSpecialistSnapshotPayload struct {
	Format     int                              `json:"format"`
	TenantID   string                           `json:"tenantId"`
	Generation uint64                           `json:"generation"`
	KeyID      string                           `json:"keyId"`
	CreatedAt  time.Time                        `json:"createdAt"`
	Records    []meetingSpecialistDurableRecord `json:"records"`
}

type meetingSpecialistSnapshotEnvelope struct {
	Payload   meetingSpecialistSnapshotPayload `json:"payload"`
	Digest    string                           `json:"digest"`
	Signature string                           `json:"signature"`
}

type meetingSpecialistGenerationPayload struct {
	Format         int    `json:"format"`
	TenantID       string `json:"tenantId"`
	Generation     uint64 `json:"generation"`
	KeyID          string `json:"keyId"`
	SnapshotDigest string `json:"snapshotDigest"`
}

type meetingSpecialistGenerationEnvelope struct {
	Payload   meetingSpecialistGenerationPayload `json:"payload"`
	Digest    string                             `json:"digest"`
	Signature string                             `json:"signature"`
}

func (product *MeetingSpecialistProduct) initializePersistence(config *MeetingSpecialistProductPersistence) {
	if product == nil || config == nil {
		return
	}
	product.persistence = config
	if !product.enabled {
		return
	}
	if strings.TrimSpace(config.SnapshotPath) == "" || strings.TrimSpace(config.GenerationPath) == "" || config.SnapshotPath == config.GenerationPath || !config.Authority.valid() || config.MinimumGeneration == 0 {
		product.failClosedLocked(ErrMeetingSpecialistProductRestore)
		return
	}
	snapshotExists, snapshotErr := strideRuntimeFileExists(config.SnapshotPath)
	generationExists, generationErr := strideRuntimeFileExists(config.GenerationPath)
	if snapshotErr != nil || generationErr != nil || snapshotExists != generationExists {
		product.failClosedLocked(ErrMeetingSpecialistProductRestore)
		return
	}
	if !snapshotExists {
		if !config.BootstrapEmpty {
			product.failClosedLocked(ErrMeetingSpecialistProductRestore)
			return
		}
		product.generation = config.MinimumGeneration - 1
		return
	}
	if err := product.restoreLocked(); err != nil {
		product.failClosedLocked(err)
	}
}

func (product *MeetingSpecialistProduct) restoreLocked() error {
	config := product.persistence
	if config == nil {
		return nil
	}
	var snapshot meetingSpecialistSnapshotEnvelope
	if err := readSTRIDERuntimeJSON(config.SnapshotPath, &snapshot); err != nil {
		return fmt.Errorf("%w: snapshot read", ErrMeetingSpecialistProductRestore)
	}
	var generation meetingSpecialistGenerationEnvelope
	if err := readSTRIDERuntimeJSON(config.GenerationPath, &generation); err != nil {
		return fmt.Errorf("%w: generation read", ErrMeetingSpecialistProductRestore)
	}
	payloadDigest, err := STRIDEContractDigest(snapshot.Payload)
	if err != nil || payloadDigest != snapshot.Digest || snapshot.Payload.Format != meetingSpecialistProductSnapshotFormat || snapshot.Payload.TenantID != product.tenantID || snapshot.Payload.Generation != generation.Payload.Generation || snapshot.Payload.KeyID != config.Authority.KeyID || !verifySTRIDESnapshotMAC(STRIDESnapshotRestorePolicy{Authority: config.Authority, MinimumGeneration: config.MinimumGeneration}, meetingSpecialistProductSnapshotDomain, snapshot.Payload.KeyID, snapshot.Payload.Generation, snapshot.Digest, snapshot.Signature) {
		return ErrMeetingSpecialistProductRestore
	}
	generationDigest, err := STRIDEContractDigest(generation.Payload)
	if err != nil || generationDigest != generation.Digest || generation.Payload.Format != meetingSpecialistProductSnapshotFormat || generation.Payload.TenantID != product.tenantID || generation.Payload.KeyID != config.Authority.KeyID || generation.Payload.SnapshotDigest != snapshot.Digest || !verifySTRIDESnapshotMAC(STRIDESnapshotRestorePolicy{Authority: config.Authority, MinimumGeneration: config.MinimumGeneration}, meetingSpecialistProductGenerationDomain, generation.Payload.KeyID, generation.Payload.Generation, generation.Digest, generation.Signature) {
		return ErrMeetingSpecialistProductRestore
	}
	restored := make(map[string]meetingSpecialistProductRecord, len(snapshot.Payload.Records))
	idempotency := map[string]struct{}{}
	activeSeats := map[string]string{}
	now := product.now().UTC()
	expiredOnRestore := false
	for _, durable := range snapshot.Payload.Records {
		if err := validateMeetingSpecialistDurableRecord(product.tenantID, durable); err != nil {
			return err
		}
		id := durable.Invitation.Header.ID
		if _, duplicate := restored[id]; duplicate {
			return ErrMeetingSpecialistProductRestore
		}
		if _, duplicate := idempotency[durable.Invitation.IdempotencyKeyDigest]; duplicate {
			return ErrMeetingSpecialistProductRestore
		}
		idempotency[durable.Invitation.IdempotencyKeyDigest] = struct{}{}
		status := durable.Status
		updatedAt := durable.UpdatedAt
		if meetingSpecialistInvitationIsActive(meetingSpecialistProductRecord{Invitation: durable.Invitation, Status: status}) && !now.Before(durable.Invitation.ExpiresAt) {
			expired, err := expireMeetingSpecialistRecord(meetingSpecialistProductRecord{Invitation: durable.Invitation, Status: status, UpdatedAt: updatedAt}, now)
			if err != nil {
				return ErrMeetingSpecialistProductRestore
			}
			durable.Invitation, status, updatedAt = expired.Invitation, expired.Status, expired.UpdatedAt
			expiredOnRestore = true
		} else if legacyMeetingSpecialistCandidate(durable.Agent) && durable.Invitation.Eligibility == nil && meetingSpecialistInvitationRequiresEligibility(meetingSpecialistProductRecord{Invitation: durable.Invitation, Status: status}) {
			status = "eligibility_revoked"
		} else if durable.Invitation.Decision == "approved" && (status == "approved_waiting_for_provider_qualification" || status == "joined_test_session") {
			status = "approved_reauthorization_required"
		}
		if meetingSpecialistInvitationIsActive(meetingSpecialistProductRecord{Invitation: durable.Invitation, Status: status}) {
			seat := durable.Invitation.RoomID + "\x00" + durable.Invitation.SittingID
			if _, occupied := activeSeats[seat]; occupied {
				return ErrMeetingSpecialistProductRestore
			}
			activeSeats[seat] = id
		}
		purpose := strings.TrimSpace(durable.PurposeSummary)
		if purpose == "" {
			purpose = "Specialist contribution requested"
		}
		limits := defaultMeetingSpecialistApprovalLimits()
		if durable.Limits != nil {
			limits = *durable.Limits
		}
		restored[id] = meetingSpecialistProductRecord{Invitation: durable.Invitation, Agent: durable.Agent, PurposeSummary: purpose, Limits: limits, Status: status, UpdatedAt: updatedAt, TerminalEvidence: cloneMeetingSpecialistTerminalEvidence(durable.TerminalEvidence), Scope: productScopeFromDurable(durable.Scope)}
	}
	product.invitations = restored
	product.generation = snapshot.Payload.Generation
	if expiredOnRestore {
		return product.persistLocked()
	}
	return nil
}

func (product *MeetingSpecialistProduct) persistLocked() error {
	return product.persistStateLocked(false)
}

func (product *MeetingSpecialistProduct) persistTerminalLocked() error {
	return product.persistStateLocked(true)
}

func (product *MeetingSpecialistProduct) persistStateLocked(allowDisabled bool) error {
	config := product.persistence
	if config == nil {
		return nil
	}
	if product.healthErr != nil || !product.enabled && !allowDisabled {
		return ErrMeetingSpecialistProductRestore
	}
	next := product.generation + 1
	if next < config.MinimumGeneration || next == 0 {
		return ErrMeetingSpecialistProductRestore
	}
	records := make([]meetingSpecialistDurableRecord, 0, len(product.invitations))
	for _, record := range product.invitations {
		limits := record.Limits
		records = append(records, meetingSpecialistDurableRecord{Invitation: record.Invitation, Agent: record.Agent, PurposeSummary: record.PurposeSummary, Limits: &limits, Status: record.Status, UpdatedAt: record.UpdatedAt, TerminalEvidence: cloneMeetingSpecialistTerminalEvidence(record.TerminalEvidence), Scope: durableMeetingSpecialistScope(record.Scope)})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Invitation.Header.ID < records[j].Invitation.Header.ID })
	payload := meetingSpecialistSnapshotPayload{Format: meetingSpecialistProductSnapshotFormat, TenantID: product.tenantID, Generation: next, KeyID: config.Authority.KeyID, CreatedAt: product.now().UTC(), Records: records}
	digest, err := STRIDEContractDigest(payload)
	if err != nil {
		return err
	}
	signature, err := strideSnapshotMAC(config.Authority, meetingSpecialistProductSnapshotDomain, next, digest)
	if err != nil {
		return err
	}
	snapshot := meetingSpecialistSnapshotEnvelope{Payload: payload, Digest: digest, Signature: signature}
	generationPayload := meetingSpecialistGenerationPayload{Format: meetingSpecialistProductSnapshotFormat, TenantID: product.tenantID, Generation: next, KeyID: config.Authority.KeyID, SnapshotDigest: digest}
	generationDigest, err := STRIDEContractDigest(generationPayload)
	if err != nil {
		return err
	}
	generationSignature, err := strideSnapshotMAC(config.Authority, meetingSpecialistProductGenerationDomain, next, generationDigest)
	if err != nil {
		return err
	}
	if err := writeJSONFileAtomically(config.SnapshotPath, "meeting specialist snapshot", snapshot); err != nil {
		return err
	}
	if err := writeJSONFileAtomically(config.GenerationPath, "meeting specialist generation", meetingSpecialistGenerationEnvelope{Payload: generationPayload, Digest: generationDigest, Signature: generationSignature}); err != nil {
		return err
	}
	product.generation = next
	return nil
}

func validateMeetingSpecialistDurableRecord(tenantID string, record meetingSpecialistDurableRecord) error {
	computed, err := meetingSpecialistInvitationDigest(record.Invitation)
	if err != nil || record.Invitation.Validate() != nil || computed != record.Invitation.Header.ContentDigest || record.Invitation.Header.TenantID != tenantID || record.Agent.AgentID == "" || record.Agent.Profile.Validate() != nil || record.Agent.Capability.Validate() != nil || record.UpdatedAt.IsZero() || !validMeetingSpecialistStatusDecision(record.Status, record.Invitation.Decision) || !validMeetingSpecialistTerminalEvidence(record.TerminalEvidence) || record.Status == "failed" && (record.TerminalEvidence == nil || record.TerminalEvidence.TerminalReason != "failed") {
		return ErrMeetingSpecialistProductRestore
	}
	legacyCandidate := legacyMeetingSpecialistCandidate(record.Agent) && record.Invitation.Eligibility == nil
	currentCandidate := validMeetingSpecialistCandidateForRoom(record.Agent, record.Invitation.RoomID) && record.Invitation.Eligibility != nil && record.Agent.Eligibility != nil && *record.Invitation.Eligibility == *record.Agent.Eligibility
	if !legacyCandidate && !currentCandidate {
		return ErrMeetingSpecialistProductRestore
	}
	if strings.TrimSpace(record.PurposeSummary) != "" && strings.TrimSpace(record.PurposeSummary) != record.PurposeSummary || record.Limits != nil && record.Limits.validate(record.Invitation) != nil {
		return ErrMeetingSpecialistProductRestore
	}
	limits := defaultMeetingSpecialistApprovalLimits()
	if record.Limits != nil {
		limits = *record.Limits
	}
	if !validMeetingSpecialistTerminalEvidenceForLimits(record.TerminalEvidence, limits) {
		return ErrMeetingSpecialistProductRestore
	}
	scope := productScopeFromDurable(record.Scope)
	if !validMeetingSpecialistDurableScope(scope) || scope.RoomID != record.Invitation.RoomID || scope.SittingID != record.Invitation.SittingID || scope.RequesterPrincipal != record.Invitation.Requester || scope.Audience.Visibility != record.Invitation.Audience.Visibility || len(scope.Audience.Principals) != len(record.Invitation.Audience.Principals) {
		return ErrMeetingSpecialistProductRestore
	}
	for index := range scope.Audience.Principals {
		if scope.Audience.Principals[index] != record.Invitation.Audience.Principals[index] {
			return ErrMeetingSpecialistProductRestore
		}
	}
	return nil
}

func validMeetingSpecialistStatusDecision(status, decision string) bool {
	switch status {
	case "awaiting_approval":
		return decision == "requested"
	case "approved_waiting_for_provider_qualification", "approved_reauthorization_required", "approved_test_session_failed", "joined_test_session", "failed":
		return decision == "approved"
	case "eligibility_revoked", "closed":
		return decision == "requested" || decision == "approved"
	case "declined", "dismissed", "expired":
		return decision == status
	default:
		return false
	}
}

func validMeetingSpecialistTerminalEvidence(evidence *MeetingSpecialistTerminalEvidence) bool {
	if evidence == nil {
		return true
	}
	cause := strings.TrimSpace(evidence.Cause)
	return allowedMeetingAgentTerminalReason(evidence.TerminalReason) && cause != "" && cause == evidence.Cause && len(cause) <= 128 && !evidence.EndedAt.IsZero() && isHexDigest(evidence.TeardownReceiptDigest) && validMeetingSpecialistProviderReceipt(evidence.ProviderReceipt)
}

func validMeetingSpecialistProviderReceipt(receipt MeetingSpecialistProviderReceipt) bool {
	if receipt == (MeetingSpecialistProviderReceipt{}) {
		return true
	}
	if !isHexDigest(receipt.BindingDigest) || !isHexDigest(receipt.RequestDigest) || !isHexDigest(receipt.EventDigest) || !isHexDigest(receipt.ContractDigest) ||
		!validOptionalMeetingSpecialistDigest(receipt.SessionIDHash) || !validOptionalMeetingSpecialistDigest(receipt.UsageDigest) ||
		!validOptionalMeetingSpecialistDigest(receipt.TerminalEventHash) || !validOptionalMeetingSpecialistDigest(receipt.SessionFailureHash) ||
		strings.TrimSpace(receipt.Model) == "" || strings.TrimSpace(receipt.Model) != receipt.Model || strings.TrimSpace(receipt.ReasoningEffort) == "" || strings.TrimSpace(receipt.ReasoningEffort) != receipt.ReasoningEffort ||
		strings.TrimSpace(receipt.ProtocolSource) == "" || strings.TrimSpace(receipt.ProtocolSource) != receipt.ProtocolSource || strings.TrimSpace(receipt.ModelSource) == "" || strings.TrimSpace(receipt.ModelSource) != receipt.ModelSource ||
		receipt.EventCount < 0 || receipt.InputTokens < 0 || receipt.OutputTokens < 0 || receipt.OutputAudioTokens < 0 || receipt.OutputAudioTokens > receipt.OutputTokens || receipt.ReconciledCostCent < 0 ||
		!oneOf(receipt.UsageStatus, "", "reconciled", "usage_unreconciled") || !oneOf(receipt.TerminalStatus, "", "completed", "cancelled", "incomplete", "failed") ||
		receipt.UsageStatus == "reconciled" && receipt.UsageDigest == "" || receipt.TerminalStatus != "" && receipt.TerminalEventHash == "" {
		return false
	}
	return true
}

func validMeetingSpecialistTerminalEvidenceForLimits(evidence *MeetingSpecialistTerminalEvidence, limits MeetingSpecialistApprovalLimits) bool {
	if !validMeetingSpecialistTerminalEvidence(evidence) || evidence == nil {
		return evidence == nil || validMeetingSpecialistTerminalEvidence(evidence)
	}
	receipt := evidence.ProviderReceipt
	return receipt.InputTokens <= limits.TokenBudget && receipt.OutputTokens <= limits.TokenBudget-receipt.InputTokens && receipt.ReconciledCostCent <= limits.CostBudgetCents
}

func validOptionalMeetingSpecialistDigest(value string) bool {
	return value == "" || isHexDigest(value)
}

func validMeetingSpecialistDurableScope(scope meetingSpecialistProductScope) bool {
	if !strideIdentifier(scope.TenantID) || !strideIdentifier(scope.RoomID) || !strideIdentifier(scope.SittingID) || scope.MediaGeneration == 0 || !strideIdentifier(scope.RequesterPrincipal) || scope.Audience.Validate() != nil || scope.ConsentPolicyRevision.Validate() != nil || len(scope.ConsentFences) == 0 {
		return false
	}
	for _, fence := range scope.ConsentFences {
		if fence.binding.Validate() != nil || !oneOf(string(fence.lane), string(ConsentLaneAudioCapture), string(ConsentLaneTranscription), string(ConsentLaneModelAnalysis)) || strings.TrimSpace(fence.policy) == "" || fence.generation == 0 || !isHexDigest(fence.recordDigest) || fence.issuedAt.IsZero() || fence.binding.TenantID != scope.TenantID || fence.binding.RoomID != scope.RoomID || fence.binding.SittingID != scope.SittingID {
			return false
		}
	}
	return true
}

func durableMeetingSpecialistScope(scope meetingSpecialistProductScope) meetingSpecialistDurableScope {
	fences := make([]meetingSpecialistDurableConsentFence, 0, len(scope.ConsentFences))
	for _, fence := range scope.ConsentFences {
		fences = append(fences, meetingSpecialistDurableConsentFence{
			Binding: consentContributorBinding{
				TenantID: fence.binding.TenantID, PrincipalKind: fence.binding.PrincipalKind, PrincipalID: fence.binding.PrincipalID,
				RoomID: fence.binding.RoomID, SittingID: fence.binding.SittingID, AnchorID: fence.binding.AnchorID,
				GuestPolicyListenOnly: fence.binding.GuestPolicyListenOnly,
			},
			Lane: fence.lane, Policy: fence.policy, Generation: fence.generation, RecordDigest: fence.recordDigest, IssuedAt: fence.issuedAt,
		})
	}
	return meetingSpecialistDurableScope{TenantID: scope.TenantID, RoomID: scope.RoomID, SittingID: scope.SittingID, MediaGeneration: scope.MediaGeneration, RequesterPrincipal: scope.RequesterPrincipal, Audience: scope.Audience, ConsentPolicyRevision: scope.ConsentPolicyRevision, ConsentFences: fences}
}

func productScopeFromDurable(scope meetingSpecialistDurableScope) meetingSpecialistProductScope {
	fences := make([]ConsentFence, 0, len(scope.ConsentFences))
	for _, fence := range scope.ConsentFences {
		fences = append(fences, ConsentFence{
			binding: ConsentAdmissionBinding{
				TenantID: fence.Binding.TenantID, PrincipalKind: fence.Binding.PrincipalKind, PrincipalID: fence.Binding.PrincipalID,
				RoomID: fence.Binding.RoomID, SittingID: fence.Binding.SittingID, AnchorID: fence.Binding.AnchorID,
				GuestPolicyListenOnly: fence.Binding.GuestPolicyListenOnly,
			},
			lane: fence.Lane, policy: fence.Policy, generation: fence.Generation, recordDigest: fence.RecordDigest, issuedAt: fence.IssuedAt,
		})
	}
	return meetingSpecialistProductScope{TenantID: scope.TenantID, RoomID: scope.RoomID, SittingID: scope.SittingID, MediaGeneration: scope.MediaGeneration, RequesterPrincipal: scope.RequesterPrincipal, Audience: scope.Audience, ConsentPolicyRevision: scope.ConsentPolicyRevision, ConsentFences: fences}
}

func (product *MeetingSpecialistProduct) failClosedLocked(err error) []meetingSpecialistRuntimeRevocation {
	if product == nil {
		return nil
	}
	product.enabled = false
	product.healthErr = err
	revocations := make([]meetingSpecialistRuntimeRevocation, 0)
	for id, record := range product.invitations {
		if record.Runtime != nil {
			revocations = append(revocations, newMeetingSpecialistRuntimeRevocation(record.Runtime, "kill_switch"))
			record.Runtime = nil
			product.invitations[id] = record
		}
	}
	return revocations
}

func removeMeetingSpecialistStoreForTest(config MeetingSpecialistProductPersistence) {
	_ = os.Remove(config.SnapshotPath)
	_ = os.Remove(config.GenerationPath)
}
