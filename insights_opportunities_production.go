package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const insightsAuthorityJournalVersion = 1

type insightsAuthorityJournalEvent struct {
	Version  int                                       `json:"version"`
	Sequence uint64                                    `json:"sequence"`
	Type     string                                    `json:"type"`
	At       time.Time                                 `json:"at"`
	Digest   string                                    `json:"digest"`
	Target   InsightsOpportunitiesAuthorizationTarget  `json:"target"`
	Approval *InsightsOpportunitiesApprovalConsumption `json:"approval,omitempty"`
}

func (event insightsAuthorityJournalEvent) canonicalDigest() (string, error) {
	event.Digest = ""
	return CanonicalStateDigest(event)
}

// productionInsightsAuthorizationVerifier is the durable, server-owned
// authority plane for the dedicated workflow. The authenticated HTTP request
// is the direct approval gesture; its exact target is compare-and-consumed once
// before any model call, and both approval and critic transitions survive a
// process crash independently of the executor journal.
type productionInsightsAuthorizationVerifier struct {
	mu                      sync.Mutex
	path                    string
	next                    uint64
	approvals               map[string]InsightsOpportunitiesApprovalConsumption
	targets                 map[string]InsightsOpportunitiesAuthorizationTarget
	runs                    map[string]InsightsOpportunitiesAuthorizationTarget
	secret                  [32]byte
	publicationCapabilities map[string]productionInsightsPublicationCapability
}

type productionInsightsPublicationCapability struct {
	PrincipalID  string
	TenantID     string
	TargetDigest string
	Key          string
}

func openProductionInsightsAuthorizationVerifier(path string) (*productionInsightsAuthorizationVerifier, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("insights authority journal path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	verifier := &productionInsightsAuthorizationVerifier{path: path, next: 1, approvals: map[string]InsightsOpportunitiesApprovalConsumption{}, targets: map[string]InsightsOpportunitiesAuthorizationTarget{}, runs: map[string]InsightsOpportunitiesAuthorizationTarget{}, publicationCapabilities: map[string]productionInsightsPublicationCapability{}}
	if _, err := rand.Read(verifier.secret[:]); err != nil {
		return nil, fmt.Errorf("initialize insights publication authority: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := recoverInsightsJournalTail(file, path); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), insightsExecutorJournalMaxEventBytes)
	for scanner.Scan() {
		var event insightsAuthorityJournalEvent
		if err := decodeInsightsStrict(scanner.Bytes(), &event, "insights authority journal event"); err != nil {
			return nil, err
		}
		want, digestErr := event.canonicalDigest()
		if digestErr != nil || event.Version != insightsAuthorityJournalVersion || event.Sequence != verifier.next || event.At.IsZero() || event.Digest != want {
			return nil, errors.New("insights authority journal continuity or digest failure")
		}
		if err := verifier.fold(event); err != nil {
			return nil, err
		}
		verifier.next++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return verifier, nil
}

func (verifier *productionInsightsAuthorizationVerifier) fold(event insightsAuthorityJournalEvent) error {
	switch event.Type {
	case "approval_consumed":
		if event.Approval == nil || verifier.approvals[event.Target.ApprovalID].Consumed || event.Approval.RunID != event.Target.RunID {
			return ErrInsightsOpportunitiesConflict
		}
		verifier.approvals[event.Target.ApprovalID] = *event.Approval
		verifier.targets[event.Target.ApprovalID] = event.Target
	case "run_advanced":
		prior, found := verifier.runs[event.Target.RunID]
		if found && prior == event.Target {
			return nil
		}
		if (!found && event.Target.ReportRevision != 1) || (found && (event.Target.ReportRevision != prior.ReportRevision+1 || event.Target.ParentReportDigest != prior.ReportDigest)) {
			return ErrInsightsOpportunitiesConflict
		}
		verifier.runs[event.Target.RunID] = event.Target
	default:
		return ErrInsightsOpportunitiesConflict
	}
	return nil
}

func (verifier *productionInsightsAuthorizationVerifier) append(event insightsAuthorityJournalEvent) error {
	event.Version, event.Sequence, event.At = insightsAuthorityJournalVersion, verifier.next, time.Now().UTC()
	digest, err := event.canonicalDigest()
	if err != nil {
		return err
	}
	event.Digest = digest
	raw, err := json.Marshal(event)
	if err != nil || len(raw) >= insightsExecutorJournalMaxEventBytes {
		if err == nil {
			err = ErrInsightsOpportunitiesEventTooLarge
		}
		return err
	}
	if err := appendFileDurably(verifier.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := verifier.fold(event); err != nil {
		return err
	}
	verifier.next++
	return nil
}

func productionInsightsPrincipalAllowed(principal ACLPrincipal, tenantID string) bool {
	return principal.Kind == ACLPrincipalUser && principal.TenantID == tenantID && tenantID == canonicalTenantID() && normalizeAccountEmail(principal.ID) == principal.ID && accountStore().findUser(principal.ID) != nil
}

func productionInsightsDecision(principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) ACLDecision {
	if !productionInsightsPrincipalAllowed(principal, target.TenantID) || target.ActorID != "" && normalizeAccountEmail(target.ActorID) != principal.ID {
		return ACLDecision{DenialCode: ACLDenialNotFound}
	}
	return ACLDecision{Allowed: true, MatchedGrantID: "insights-active-member", ACLVersion: 1, Obligations: []string{"audit", "workspace-only"}}
}

func (verifier *productionInsightsAuthorizationVerifier) AuthorizeInsightsOpportunities(_ context.Context, principal ACLPrincipal, action ACLAction, target InsightsOpportunitiesAuthorizationTarget) ACLDecision {
	if action != ACLReadContent && action != ACLWrite && action != ACLApprove {
		return ACLDecision{DenialCode: ACLDenialNotFound}
	}
	return productionInsightsDecision(principal, target)
}

func (verifier *productionInsightsAuthorizationVerifier) VerifyInsightsOpportunitiesRequirement(_ context.Context, principal ACLPrincipal, requirement string, target InsightsOpportunitiesAuthorizationTarget) ACLDecision {
	decision := productionInsightsDecision(principal, target)
	if !decision.Allowed {
		return decision
	}
	switch requirement {
	case insightsRequirementActiveOrganizationMember:
		return decision
	case insightsRequirementPilotReviewerRole:
		if isArtifactApprovalAdmin(accountStore().findUser(principal.ID)) {
			return decision
		}
	}
	return ACLDecision{DenialCode: ACLDenialNotFound}
}

func (verifier *productionInsightsAuthorizationVerifier) ConsumeInsightsOpportunitiesApproval(_ context.Context, principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) InsightsOpportunitiesApprovalConsumption {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	if !productionInsightsDecision(principal, target).Allowed || target.ApprovalID == "" || target.ApprovalKind != insightsApprovalDirectOnce || target.ActorID != principal.ID || verifier.approvals[target.ApprovalID].Consumed {
		return InsightsOpportunitiesApprovalConsumption{}
	}
	binding, err := insightsApprovalTargetDigest(target)
	if err != nil {
		return InsightsOpportunitiesApprovalConsumption{}
	}
	receipt := InsightsOpportunitiesApprovalConsumption{
		Consumed: true, ConsumptionID: "insights-consume-" + digestBrainString(target.ApprovalID + binding)[:24],
		CheckpointID: "insights-approval-" + digestBrainString(target.RunID + binding)[:24], RunID: target.RunID, BindingDigest: binding,
		Decision: productionInsightsDecision(principal, target),
	}
	if err := verifier.append(insightsAuthorityJournalEvent{Type: "approval_consumed", Target: target, Approval: &receipt}); err != nil {
		return InsightsOpportunitiesApprovalConsumption{}
	}
	return receipt
}

func (verifier *productionInsightsAuthorizationVerifier) RecoverInsightsOpportunitiesApproval(_ context.Context, principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) (InsightsOpportunitiesApprovalConsumption, error) {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	receipt, ok := verifier.approvals[target.ApprovalID]
	if !ok || verifier.targets[target.ApprovalID] != target || !productionInsightsDecision(principal, target).Allowed || !validInsightsApprovalConsumption(receipt, target) {
		return InsightsOpportunitiesApprovalConsumption{}, errors.New("durable insights approval receipt not found")
	}
	return receipt, nil
}

func (verifier *productionInsightsAuthorizationVerifier) ResumeInsightsOpportunitiesApproval(_ context.Context, principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget, receipt InsightsOpportunitiesApprovalConsumption) ACLDecision {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	stored, ok := verifier.approvals[target.ApprovalID]
	if !ok || verifier.targets[target.ApprovalID] != target || !reflect.DeepEqual(stored, receipt) || !validInsightsApprovalConsumption(receipt, target) {
		return ACLDecision{DenialCode: ACLDenialNotFound}
	}
	return productionInsightsDecision(principal, target)
}

func (verifier *productionInsightsAuthorizationVerifier) AdvanceInsightsOpportunitiesRun(_ context.Context, principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget) InsightsOpportunitiesRunTransition {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	decision := productionInsightsDecision(principal, target)
	if !decision.Allowed || target.RunID == "" || target.ReportRevision < 1 || target.ReportRevision > 2 {
		return InsightsOpportunitiesRunTransition{}
	}
	checkpoint := "insights-run-" + digestBrainString(target.RunID + target.ReportDigest)[:24]
	if prior, found := verifier.runs[target.RunID]; found && prior == target {
		return InsightsOpportunitiesRunTransition{Resumed: true, CheckpointID: checkpoint, Decision: decision}
	}
	if prior, found := verifier.runs[target.RunID]; (!found && target.ReportRevision != 1) || (found && (target.ReportRevision != prior.ReportRevision+1 || target.ParentReportDigest != prior.ReportDigest)) {
		return InsightsOpportunitiesRunTransition{}
	}
	if err := verifier.append(insightsAuthorityJournalEvent{Type: "run_advanced", Target: target}); err != nil {
		return InsightsOpportunitiesRunTransition{}
	}
	return InsightsOpportunitiesRunTransition{Advanced: true, CheckpointID: checkpoint, Decision: decision}
}

func (verifier *productionInsightsAuthorizationVerifier) AuthorizeInsightsOpportunitiesPublication(_ context.Context, principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget, key string) (InsightsOpportunitiesWorkspaceWriteAuthority, error) {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	decision := productionInsightsDecision(principal, target)
	if !decision.Allowed || target.Action != toolAuthorityWorkspaceWrite || target.CriticOutcome != insightsCriticAccept || !target.Terminal || !strings.HasPrefix(target.ArtifactDestination, "workspace:") {
		return InsightsOpportunitiesWorkspaceWriteAuthority{}, errors.New("workspace publication authority denied")
	}
	digest, err := CanonicalStateDigest(target)
	if err != nil {
		return InsightsOpportunitiesWorkspaceWriteAuthority{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return InsightsOpportunitiesWorkspaceWriteAuthority{}, err
	}
	mac := hmac.New(sha256.New, verifier.secret[:])
	_, _ = mac.Write([]byte(principal.TenantID + "\x00" + principal.ID + "\x00" + key + "\x00" + digest + "\x00" + hex.EncodeToString(nonce[:])))
	authorityID := "insights-write-" + hex.EncodeToString(nonce[:]) + "-" + hex.EncodeToString(mac.Sum(nil))
	if verifier.publicationCapabilities == nil {
		verifier.publicationCapabilities = map[string]productionInsightsPublicationCapability{}
	}
	verifier.publicationCapabilities[authorityID] = productionInsightsPublicationCapability{PrincipalID: principal.ID, TenantID: principal.TenantID, TargetDigest: digest, Key: key}
	return InsightsOpportunitiesWorkspaceWriteAuthority{AuthorityID: authorityID, TargetDigest: digest, IdempotencyKey: key, Decision: decision}, nil
}

func (verifier *productionInsightsAuthorizationVerifier) VerifyInsightsOpportunitiesPublication(_ context.Context, principal ACLPrincipal, target InsightsOpportunitiesAuthorizationTarget, key string, authority InsightsOpportunitiesWorkspaceWriteAuthority) error {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	decision := productionInsightsDecision(principal, target)
	if !decision.Allowed || target.Action != toolAuthorityWorkspaceWrite || target.CriticOutcome != insightsCriticAccept || !target.Terminal {
		return errors.New("workspace publication authority is no longer current")
	}
	digest, err := CanonicalStateDigest(target)
	if err != nil || authority.TargetDigest != digest || authority.IdempotencyKey != key {
		return errors.New("workspace publication authority binding mismatch")
	}
	capability, found := verifier.publicationCapabilities[authority.AuthorityID]
	if !found || capability != (productionInsightsPublicationCapability{PrincipalID: principal.ID, TenantID: principal.TenantID, TargetDigest: digest, Key: key}) {
		return errors.New("workspace publication capability is invalid")
	}
	delete(verifier.publicationCapabilities, authority.AuthorityID)
	return nil
}

type anthropicInsightsOpportunitiesProvider struct{ Sources *MeetingMemoryBrainAdapter }

type insightsGeneratedPayload struct {
	ReportID string `json:"reportId"`
	Claims   []struct {
		ClaimID     string   `json:"claimId"`
		Text        string   `json:"text"`
		EvidenceIDs []string `json:"evidenceIds"`
	} `json:"claims"`
	Opportunities []InsightsOpportunity `json:"opportunities"`
}

type insightsReviewPayload struct {
	VerdictID string                               `json:"verdictId"`
	Outcome   string                               `json:"outcome"`
	Findings  []InsightsOpportunitiesCriticFinding `json:"findings"`
}

func (provider *anthropicInsightsOpportunitiesProvider) evidencePrompt(ctx context.Context, request InsightsOpportunitiesRequest) ([]map[string]any, error) {
	if provider == nil || provider.Sources == nil {
		return nil, ErrInsightsOpportunitiesUnavailable
	}
	principal := ACLPrincipal{TenantID: request.TenantID, ID: request.PrincipalID, Kind: request.PrincipalKind, TeamIDs: []string{"organization"}}
	if err := provider.Sources.ReauthorizeEvidence(ctx, principal, request.EvidenceSnapshot.Sources); err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(request.EvidenceSnapshot.Sources))
	for _, source := range request.EvidenceSnapshot.Sources {
		read, err := provider.Sources.ReadBrainSource(ctx, source.Evidence)
		if err != nil || !read.BodyAvailable || read.Status != RecallSourceFresh {
			return nil, ErrRetrievalSnapshotStale
		}
		result = append(result, map[string]any{"evidenceId": source.EvidenceID, "body": read.Body, "occurredStart": source.Evidence.OccurredStart, "occurredEnd": source.Evidence.OccurredEnd})
	}
	return result, nil
}

func callAnthropicInsightsJSON(ctx context.Context, seat InsightsOpportunitiesRouteSeat, usageSeat, system string, input any, output any) (InsightsOpportunitiesProviderExecution, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return InsightsOpportunitiesProviderExecution{}, err
	}
	response, err := createAnthropicMessagesResponse(ctx, currentAnthropicAPIKey(), anthropicMessagesRequest{
		Model: seat.Model, System: system, Messages: []anthropicMessage{{Role: "user", Content: []json.RawMessage{anthropicTextBlock(string(raw))}}},
		MaxTokens: 8192, Effort: seat.Effort, Seat: usageSeat, ThreadID: "insights-opportunities",
	})
	if err != nil {
		return InsightsOpportunitiesProviderExecution{}, err
	}
	if response.Model != seat.Model || response.StopReason != "end_turn" || strings.TrimSpace(response.ID) == "" {
		return InsightsOpportunitiesProviderExecution{}, errors.New("Anthropic response did not complete on the pinned model")
	}
	text := anthropicResponseText(response)
	if err := decodeInsightsStrict([]byte(text), output, "Anthropic insights JSON"); err != nil {
		return InsightsOpportunitiesProviderExecution{}, err
	}
	return InsightsOpportunitiesProviderExecution{
		Provider: "anthropic", Model: response.Model, Effort: seat.Effort, Request: response.ID,
		Usage:    InsightsOpportunitiesUsage{InputTokens: response.Usage.InputTokens + response.Usage.CacheCreationInputTokens, CachedInputTokens: response.Usage.CacheReadInputTokens, OutputTokens: response.Usage.OutputTokens},
		Metadata: map[string]string{"cacheCreationInputTokens": fmt.Sprint(response.Usage.CacheCreationInputTokens)},
	}, nil
}

func (provider *anthropicInsightsOpportunitiesProvider) Orchestrate(ctx context.Context, request InsightsOpportunitiesRequest, prior *InsightsOpportunitiesReport, verdict *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesOrchestrationPlan, InsightsOpportunitiesProviderExecution, error) {
	evidence, err := provider.evidencePrompt(ctx, request)
	if err != nil {
		return InsightsOpportunitiesOrchestrationPlan{}, InsightsOpportunitiesProviderExecution{}, err
	}
	var plan InsightsOpportunitiesOrchestrationPlan
	execution, err := callAnthropicInsightsJSON(ctx, insightsOpportunitiesStaticRoute().Orchestration, seatOrchestrator,
		"Return only strict JSON with focus[] and constraints[]. Plan an evidence-grounded Insights & Opportunities report. Never introduce evidence IDs absent from the input.",
		map[string]any{"request": request.EvidenceSnapshot.Query, "coverage": request.RecallCoverage, "evidence": evidence, "priorReport": prior, "priorCritic": verdict}, &plan)
	return plan, execution, err
}

func (provider *anthropicInsightsOpportunitiesProvider) Generate(ctx context.Context, request InsightsOpportunitiesRequest, plan InsightsOpportunitiesOrchestrationPlan, revision int, prior *InsightsOpportunitiesReport, verdict *InsightsOpportunitiesCriticVerdict) (InsightsOpportunitiesReport, InsightsOpportunitiesProviderExecution, error) {
	evidence, err := provider.evidencePrompt(ctx, request)
	if err != nil {
		return InsightsOpportunitiesReport{}, InsightsOpportunitiesProviderExecution{}, err
	}
	var payload insightsGeneratedPayload
	execution, err := callAnthropicInsightsJSON(ctx, insightsOpportunitiesStaticRoute().Generation, seatDeliverable,
		"Return only strict JSON: reportId, claims[{claimId,text,evidenceIds}], opportunities[{opportunityId,claimIds,evidenceIds,confidence,counterevidenceIds,expectedImpact,recommendedNextAction,proposedOwner,decisionStatus}]. Cite only supplied evidence IDs. decisionStatus must be proposed.",
		map[string]any{"revision": revision, "plan": plan, "evidence": evidence, "priorReport": prior, "priorCritic": verdict}, &payload)
	if err != nil {
		return InsightsOpportunitiesReport{}, InsightsOpportunitiesProviderExecution{}, err
	}
	report := InsightsOpportunitiesReport{ReportID: payload.ReportID, Opportunities: payload.Opportunities}
	for _, claim := range payload.Claims {
		assertion, digestErr := CanonicalStateDigest(struct {
			Text string `json:"text"`
		}{claim.Text})
		if digestErr != nil {
			return InsightsOpportunitiesReport{}, InsightsOpportunitiesProviderExecution{}, digestErr
		}
		report.Claims = append(report.Claims, InsightsOpportunitiesClaim{ClaimID: claim.ClaimID, State: BrainClaimAsserted, Text: claim.Text, AssertionDigest: assertion, EvidenceIDs: claim.EvidenceIDs})
	}
	return report, execution, nil
}

func (provider *anthropicInsightsOpportunitiesProvider) Review(ctx context.Context, request InsightsOpportunitiesRequest, report InsightsOpportunitiesReport) (InsightsOpportunitiesCriticVerdict, InsightsOpportunitiesProviderExecution, error) {
	evidence, err := provider.evidencePrompt(ctx, request)
	if err != nil {
		return InsightsOpportunitiesCriticVerdict{}, InsightsOpportunitiesProviderExecution{}, err
	}
	var payload insightsReviewPayload
	execution, err := callAnthropicInsightsJSON(ctx, insightsOpportunitiesStaticRoute().Review, seatReview,
		"Return only strict JSON: verdictId, outcome, findings[]. Supply exactly one finding for every claim and opportunity. Outcome is accept, revise, or reject; unsupported or conflicted assertions cannot be accepted.",
		map[string]any{"report": report, "evidence": evidence}, &payload)
	return InsightsOpportunitiesCriticVerdict{VerdictID: payload.VerdictID, Outcome: payload.Outcome, Findings: payload.Findings}, execution, err
}

func configureProductionInsightsOpportunitiesExecutor(app *kanbanBoardApp) error {
	if !insightsOpportunitiesRequested() {
		return nil
	}
	runtime := currentCanonicalRuntime()
	if app == nil || app.memory == nil || runtime == nil || runtime.postgres == nil || currentAnthropicAPIKey() == "" {
		return ErrInsightsOpportunitiesUnavailable
	}
	runStore, err := OpenInsightsOpportunitiesRunStore(filepath.Join(runtime.dataDir, "insights-opportunities-runs.jsonl"))
	if err != nil {
		return err
	}
	verifier, err := openProductionInsightsAuthorizationVerifier(filepath.Join(runtime.dataDir, "insights-opportunities-authority.jsonl"))
	if err != nil {
		return err
	}
	purge := &PostgresPurgeGenerationResolver{pool: runtime.postgres.pool}
	kernel := AuthorizationKernel{Store: runtime.postgres}
	sources := &MeetingMemoryBrainAdapter{Memory: app.memory, Objects: aclBrainCurrentObjectResolver{Store: runtime.postgres}, Kernel: kernel, Purge: purge, Consent: appBrainSourceConsentVerifier{App: app}, Now: func() time.Time { return time.Now().UTC() }}
	provider := &anthropicInsightsOpportunitiesProvider{Sources: sources}
	installInsightsOpportunitiesExecutor(&InsightsOpportunitiesExecutor{Store: runStore, Kernel: kernel, Purge: purge, Evidence: sources, Verifier: verifier, Generation: provider, Review: provider, Writer: appInsightsOpportunitiesWorkspaceWriter{app: app, sources: sources, postgres: runtime.postgres}})
	return nil
}
