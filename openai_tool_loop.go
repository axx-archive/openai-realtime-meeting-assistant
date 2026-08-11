package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	openAIToolRunnerModel           = "gpt-5.6-terra"
	openAIToolRunnerReasoningEffort = "high"
	openAIToolRunnerMaxTurns        = 16
)

// Test-only race seam after exact operation replay and before the provider
// admission generation is compared under the held source-store fence.
var openAIToolBeforeProviderAdmissionProbe func()

var errOpenAIToolCarrierUnavailable = errors.New("OpenAI function tools are unavailable")

// openAIResponsesToolInputItem is the complete manual replay unit sent to the
// Responses API. The carrier never sends a previous_response_id and never asks
// the provider to retain state.
type openAIResponsesToolInputItem struct {
	Raw       json.RawMessage `json:"-"`
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
}

func (item openAIResponsesToolInputItem) MarshalJSON() ([]byte, error) {
	if len(item.Raw) != 0 {
		if !json.Valid(item.Raw) {
			return nil, errors.New("OpenAI Responses replay item is invalid JSON")
		}
		return append([]byte(nil), item.Raw...), nil
	}
	type wire openAIResponsesToolInputItem
	return json.Marshal(wire(item))
}

func (item *openAIResponsesToolInputItem) UnmarshalJSON(raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("OpenAI Responses replay item is invalid JSON")
	}
	item.Raw = append(json.RawMessage(nil), raw...)
	return nil
}

type openAIResponsesToolRequest struct {
	Model             string                         `json:"model"`
	Reasoning         map[string]string              `json:"reasoning"`
	Instructions      string                         `json:"instructions"`
	Input             []openAIResponsesToolInputItem `json:"input"`
	Tools             []map[string]any               `json:"tools"`
	ToolChoice        string                         `json:"tool_choice"`
	ParallelToolCalls bool                           `json:"parallel_tool_calls"`
	Store             bool                           `json:"store"`
	ManifestDigest    string                         `json:"-"`
}

type openAIResponsesFunctionCall struct {
	Name      string
	CallID    string
	Arguments json.RawMessage
}

type openAIResponsesToolResponse struct {
	Text             string
	FunctionCalls    []openAIResponsesFunctionCall
	Model            string
	ResponseID       string
	ExactOutputItems []json.RawMessage
	Incomplete       bool
	Refusal          string
}

type openAIResponsesToolProvider interface {
	RespondWithOpenAITools(context.Context, openAIResponsesToolRequest) (openAIResponsesToolResponse, error)
}

type openAIToolCurrentAuthority interface {
	AuthorizeOpenAITool(context.Context, openAIToolAuthorityExpectation, openAIToolManifestEntry, map[string]any) (string, error)
}

// openAIToolProviderAdmissionGuard is an optional stronger source lease used
// by the concrete product authority. Snapshot tokens bracket operation replay;
// With... compares the exact token again and holds the underlying source store
// read fence through the provider call. Generic test authorities may omit it.
type openAIToolProviderAdmissionGuard interface {
	SnapshotOpenAIToolProviderAdmission(context.Context) (string, error)
	WithOpenAIToolProviderAdmission(context.Context, string, func(context.Context) error) error
}

// openAIToolAuthorityLease must hold current authority until use returns. A
// snapshot-only implementation violates the contract even if its initial
// values happen to match the expectation.
type openAIToolAuthorityLease interface {
	WithCurrentOpenAIToolAuthority(context.Context, openAIToolAuthorityExpectation, func(context.Context, openAIToolCurrentAuthority) error) error
}

type openAIToolExecutor interface {
	ReconcileOpenAITool(context.Context, openAIToolCurrentAuthority, string, openAIToolAuthorityExpectation, openAIToolManifestEntry, map[string]any, string) (openAIToolReconciliation, error)
	ExecuteOpenAITool(context.Context, openAIToolCurrentAuthority, string, openAIToolAuthorityExpectation, openAIToolManifestEntry, map[string]any, string) (openAIToolEffectCommit, error)
}

type openAIToolReconciliationStatus string

const (
	openAIToolReconciliationNotApplied openAIToolReconciliationStatus = "not_applied"
	openAIToolReconciliationCommitted  openAIToolReconciliationStatus = "exact_commit_found"
	openAIToolReconciliationAmbiguous  openAIToolReconciliationStatus = "ambiguous"
)

type openAIToolReconciliation struct {
	Status openAIToolReconciliationStatus
	Commit openAIToolEffectCommit
}

type openAIToolFinalizer interface {
	ReconcileOpenAIToolRunFinalization(context.Context, openAIToolCurrentAuthority, openAIToolAuthorityExpectation, string, string, []string) (openAIToolFinalizationReconciliation, error)
	FinalizeOpenAIToolRun(context.Context, openAIToolCurrentAuthority, openAIToolAuthorityExpectation, string, string, []string) (openAIToolFinalizationCommit, error)
}

type openAIToolFinalizationCommit struct {
	RunDigest           string
	OperationIDs        []string
	FinalUseDigest      string
	FanOutReceiptDigest string
}

type openAIToolFinalizationReconciliation struct {
	Status openAIToolReconciliationStatus
	Commit openAIToolFinalizationCommit
}

type openAIToolLoopRequest struct {
	Instructions string
	UserTurn     string
	Expectation  openAIToolAuthorityExpectation
}

type openAIToolLoopResult struct {
	Text         string
	OperationIDs []string
	Model        string
	Reasoning    string
}

// openAIToolLoopCarrier is intentionally inert unless every dependency is
// installed and Enabled is true. No package init, env alias, credential, or
// legacy persisted assignment can turn it on.
type openAIToolLoopCarrier struct {
	Enabled   bool
	Manifest  openAIToolManifest
	Journal   *openAIToolJournal
	Authority openAIToolAuthorityLease
	Provider  openAIResponsesToolProvider
	Executor  openAIToolExecutor
	Finalizer openAIToolFinalizer
}

func (carrier *openAIToolLoopCarrier) Run(ctx context.Context, request openAIToolLoopRequest) (openAIToolLoopResult, error) {
	if carrier == nil || !carrier.Enabled || carrier.Journal == nil || carrier.Authority == nil || carrier.Provider == nil || carrier.Executor == nil || carrier.Finalizer == nil {
		return openAIToolLoopResult{}, errOpenAIToolCarrierUnavailable
	}
	if request.Instructions = strings.TrimSpace(request.Instructions); request.Instructions == "" {
		return openAIToolLoopResult{}, errors.New("OpenAI tool instructions are required")
	}
	if request.UserTurn = strings.TrimSpace(request.UserTurn); request.UserTurn == "" {
		return openAIToolLoopResult{}, errors.New("OpenAI tool user turn is required")
	}
	if err := request.Expectation.validate(); err != nil {
		return openAIToolLoopResult{}, err
	}
	authoritativeManifest, err := buildOpenAIToolManifest()
	if err != nil {
		return openAIToolLoopResult{}, errOpenAIToolCarrierUnavailable
	}
	providedManifest, providedErr := canonicalJSON(carrier.Manifest)
	authoritativeRaw, authoritativeErr := canonicalJSON(authoritativeManifest)
	if providedErr != nil || authoritativeErr != nil || !bytes.Equal(providedManifest, authoritativeRaw) || carrier.Manifest.DigestSHA256 != openAIToolManifestV1SHA256 {
		return openAIToolLoopResult{}, errors.New("OpenAI tool manifest is not the exact frozen server manifest")
	}
	runRequestDigest, err := openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-run-request-v1", "instructions": request.Instructions,
		"user_turn": request.UserTurn, "expectation": openAIToolRunBaseExpectation(request.Expectation),
	})
	if err != nil {
		return openAIToolLoopResult{}, err
	}
	var result openAIToolLoopResult
	err = carrier.Authority.WithCurrentOpenAIToolAuthority(ctx, request.Expectation, func(leaseContext context.Context, current openAIToolCurrentAuthority) error {
		if current == nil {
			return errors.New("OpenAI tool current authority is unavailable")
		}
		manualHistory := []openAIResponsesToolInputItem{{Role: "user", Content: request.UserTurn}}
		operationIDs := make([]string, 0, 2)
		providerAdmissionToken, tokenErr := snapshotOpenAIToolProviderAdmission(leaseContext, current)
		if tokenErr != nil {
			return tokenErr
		}
		durableRun, recovered, runErr := carrier.Journal.ActiveOperationRunForExpectation(leaseContext, request.Expectation)
		if runErr != nil {
			return runErr
		}
		if !recovered {
			durableRun, runErr = carrier.Journal.BeginOrResumeRun(leaseContext, request.Expectation, runRequestDigest, uuid.NewString())
			if runErr != nil {
				return runErr
			}
		}
		runID := durableRun.RunID
		nextRunSequence := uint64(0)
		pending, pendingErr := carrier.Journal.RunOperations(leaseContext, runID)
		if pendingErr != nil {
			return pendingErr
		}
		nextRunSequence = uint64(len(pending))
		for _, operation := range pending {
			if operation.Record.RunID != runID || operation.Record.RunSequence != uint64(len(operationIDs)) {
				return errors.New("OpenAI tool pending run membership or order changed")
			}
			if !containsOpenAIToolOperation(operationIDs, operation.Record.OperationID) {
				operationIDs = append(operationIDs, operation.Record.OperationID)
			}
			resumedHistory, stableToken, resumeErr := carrier.resumeOpenAIToolOperationForProvider(leaseContext, current, authoritativeManifest, operation.Record.OperationID)
			if resumeErr != nil {
				return resumeErr
			}
			manualHistory = resumedHistory
			providerAdmissionToken = stableToken
		}
		if len(pending) > 0 {
			allTerminal, terminalText := true, ""
			for _, operationID := range operationIDs {
				_, envelope, recordErr := carrier.Journal.Record(leaseContext, operationID)
				if recordErr != nil {
					return recordErr
				}
				if strings.TrimSpace(envelope.FinalOutput) == "" || len(envelope.FinalOutputItems) == 0 || terminalText != "" && terminalText != envelope.FinalOutput {
					allTerminal = false
					break
				}
				terminalText = envelope.FinalOutput
			}
			if allTerminal {
				finalization, finalizeErr := carrier.reconcileOrFinalizeOpenAIToolRun(leaseContext, current, request.Expectation, runID, terminalText, operationIDs)
				if finalizeErr != nil {
					return fmt.Errorf("resume OpenAI function-tool finalization: %w", finalizeErr)
				}
				if strings.TrimSpace(finalization.FinalUseDigest) == "" || strings.TrimSpace(finalization.FanOutReceiptDigest) == "" {
					return errors.New("OpenAI function-tool resumed finalization omitted durable proof")
				}
				for _, operationID := range operationIDs {
					unlockOperation := carrier.Journal.lockOperation(operationID)
					if err := carrier.Journal.CommitFinalUse(leaseContext, operationID, finalization); err != nil {
						unlockOperation()
						return err
					}
					if err := carrier.Journal.Complete(leaseContext, operationID); err != nil {
						unlockOperation()
						return err
					}
					unlockOperation()
				}
				if err := carrier.Journal.CompleteRun(leaseContext, runID, terminalText); err != nil {
					return err
				}
				result = openAIToolLoopResult{Text: terminalText, OperationIDs: append([]string(nil), operationIDs...), Model: openAIToolRunnerModel, Reasoning: openAIToolRunnerReasoningEffort}
				return nil
			}
		}
		for {
			if err := leaseContext.Err(); err != nil {
				return err
			}
			if openAIToolBeforeProviderAdmissionProbe != nil {
				openAIToolBeforeProviderAdmissionProbe()
			}
			var providerResponse openAIResponsesToolResponse
			err := withOpenAIToolProviderAdmission(leaseContext, current, providerAdmissionToken, func(admissionContext context.Context) error {
				if _, err := carrier.Journal.BeginProviderTurn(admissionContext, runID); err != nil {
					return fmt.Errorf("admit OpenAI function-tool provider turn: %w", err)
				}
				var providerErr error
				providerResponse, providerErr = carrier.Provider.RespondWithOpenAITools(admissionContext, openAIResponsesToolRequest{
					Model:          openAIToolRunnerModel,
					Reasoning:      map[string]string{"effort": openAIToolRunnerReasoningEffort},
					Instructions:   request.Instructions,
					Input:          cloneOpenAIToolHistory(manualHistory),
					Tools:          authoritativeManifest.responsesTools(),
					ManifestDigest: authoritativeManifest.DigestSHA256,
					ToolChoice:     "auto", ParallelToolCalls: false, Store: false,
				})
				return providerErr
			})
			if err != nil {
				return fmt.Errorf("OpenAI function-tool response: %w", err)
			}
			if strings.TrimSpace(providerResponse.ResponseID) == "" || len(providerResponse.ExactOutputItems) == 0 {
				return errors.New("OpenAI function-tool response lacks exact response identity or output items")
			}
			if providerResponse.Incomplete || strings.TrimSpace(providerResponse.Refusal) != "" {
				return errors.New("OpenAI function-tool response is incomplete or refused")
			}
			for _, item := range providerResponse.ExactOutputItems {
				if !json.Valid(item) {
					return errors.New("OpenAI function-tool response contains invalid exact output JSON")
				}
			}
			if err := validateOpenAIToolResponseProjection(providerResponse); err != nil {
				return err
			}
			if providerResponse.Model != "" && providerResponse.Model != openAIToolRunnerModel {
				return fmt.Errorf("OpenAI function-tool provider returned unapproved model %q", providerResponse.Model)
			}
			if len(providerResponse.FunctionCalls) > 1 {
				return errors.New("OpenAI function-tool response attempted parallel or multiple calls")
			}
			if len(providerResponse.FunctionCalls) == 0 {
				finalText := strings.TrimSpace(providerResponse.Text)
				if finalText == "" {
					return errors.New("OpenAI function-tool response has no terminal text")
				}
				for _, operationID := range operationIDs {
					if _, err := carrier.resumeOpenAIToolOperation(leaseContext, current, authoritativeManifest, operationID); err != nil {
						return fmt.Errorf("revalidate OpenAI function-tool terminal effect: %w", err)
					}
				}
				for _, operationID := range operationIDs {
					unlockOperation := carrier.Journal.lockOperation(operationID)
					record, envelope, recordErr := carrier.Journal.Record(leaseContext, operationID)
					if recordErr != nil {
						unlockOperation()
						return recordErr
					}
					if record.State == openAIToolStateCompleted {
						if envelope.FinalOutput != finalText {
							unlockOperation()
							return errors.New("OpenAI terminal replay changed after completion")
						}
					} else if record.State != openAIToolStateContinuationSent {
						unlockOperation()
						return fmt.Errorf("OpenAI tool operation %s reached terminal output from state %s", operationID, record.State)
					}
					unlockOperation()
				}
				if err := carrier.Journal.RecordRunTerminalResponse(leaseContext, operationIDs, providerResponse.ResponseID, finalText, providerResponse.ExactOutputItems); err != nil {
					return err
				}
				finalization, err := carrier.reconcileOrFinalizeOpenAIToolRun(leaseContext, current, request.Expectation, runID, finalText, operationIDs)
				if err != nil {
					return fmt.Errorf("finalize OpenAI function-tool run: %w", err)
				}
				if len(operationIDs) > 0 && (strings.TrimSpace(finalization.FinalUseDigest) == "" || strings.TrimSpace(finalization.FanOutReceiptDigest) == "") {
					return errors.New("OpenAI function-tool finalization omitted durable use or fan-out proof")
				}
				for _, operationID := range operationIDs {
					unlockOperation := carrier.Journal.lockOperation(operationID)
					record, _, recordErr := carrier.Journal.Record(leaseContext, operationID)
					if recordErr != nil {
						unlockOperation()
						return recordErr
					}
					if record.State == openAIToolStateCompleted {
						unlockOperation()
						continue
					}
					if err := carrier.Journal.CommitFinalUse(leaseContext, operationID, finalization); err != nil {
						unlockOperation()
						return err
					}
					if err := carrier.Journal.Complete(leaseContext, operationID); err != nil {
						unlockOperation()
						return err
					}
					unlockOperation()
				}
				if err := carrier.Journal.CompleteRun(leaseContext, runID, finalText); err != nil {
					return err
				}
				result = openAIToolLoopResult{Text: finalText, OperationIDs: append([]string(nil), operationIDs...), Model: openAIToolRunnerModel, Reasoning: openAIToolRunnerReasoningEffort}
				return nil
			}

			call := providerResponse.FunctionCalls[0]
			call.Name, call.CallID = strings.TrimSpace(call.Name), strings.TrimSpace(call.CallID)
			if call.Name == "" || call.CallID == "" || len(call.Arguments) == 0 {
				return errors.New("OpenAI function call name, call_id, and arguments are required")
			}
			entry, admitted := authoritativeManifest.admitted(call.Name)
			if !admitted {
				return fmt.Errorf("OpenAI function %q is explicitly unavailable", call.Name)
			}
			decoded, err := decodeOpenAIToolArguments(call.Arguments)
			if err != nil {
				return fmt.Errorf("decode OpenAI function %q arguments: %w", call.Name, err)
			}
			arguments, err := normalizeOpenAIToolArguments(entry, decoded)
			if err != nil {
				return err
			}
			if call.Name == controlToolReportGoalState {
				if percent, ok := asOptionalInt(arguments["progress_percent"]); ok && (percent < 0 || percent > 100) {
					return errors.New("OpenAI report_goal_state progress must be between 0 and 100")
				}
			} else {
				requiredAuthority := strings.SplitN(entry.Authority, ":", 2)[0]
				if !codexAuthorityAllows(request.Expectation.JobAuthority, requiredAuthority) {
					return fmt.Errorf("OpenAI function %q exceeds held job authority", call.Name)
				}
			}
			if call.Name == "create_artifact" && strings.TrimSpace(asString(arguments["content"])) == "" {
				return errors.New("OpenAI create_artifact without explicit content is unavailable; nested provider generation is forbidden")
			}
			if call.Name == "update_artifact" && strings.TrimSpace(asString(arguments["title"])) == "" && strings.TrimSpace(asString(arguments["content"])) == "" {
				return errors.New("OpenAI update_artifact requires an explicit title or content change")
			}
			if call.Name == "update_artifact" && strings.TrimSpace(asString(arguments["artifact_id"])) != request.Expectation.ArtifactID {
				return errors.New("OpenAI update_artifact target does not match immutable artifact authority")
			}
			argumentsDigest, _, err := openAIToolCanonicalDigest(arguments)
			if err != nil {
				return err
			}
			operationExpectation := request.Expectation
			operationExpectation.ToolName, operationExpectation.ManifestDigest, operationExpectation.SchemaDigest, operationExpectation.ArgumentsDigest = call.Name, authoritativeManifest.DigestSHA256, entry.SchemaSHA256, argumentsDigest
			operationExpectation.PolicyRevision = entry.PolicyRevision
			preimageDigest, err := current.AuthorizeOpenAITool(leaseContext, operationExpectation, entry, arguments)
			if err != nil {
				return fmt.Errorf("authorize OpenAI function %q: %w", call.Name, err)
			}
			call.Arguments, err = json.Marshal(arguments)
			if err != nil {
				return err
			}
			record, _, replayed, err := carrier.Journal.Reserve(leaseContext, entry, arguments, operationExpectation, openAIToolJournalProposal{
				ProviderResponseID: providerResponse.ResponseID, ProviderCallID: call.CallID,
				ExactOutputItems: providerResponse.ExactOutputItems, PreimageDigest: preimageDigest, ManifestDigest: authoritativeManifest.DigestSHA256,
				RunID: runID, RunSequence: nextRunSequence, RunRequestDigest: runRequestDigest,
			}, manualHistory)
			if err != nil {
				return err
			}
			if record.RunID != runID {
				if len(operationIDs) != 0 {
					return errors.New("OpenAI tool semantic replay crossed durable run membership")
				}
				if err := carrier.Journal.SupersedeRun(leaseContext, runID, record.RunID); err != nil {
					return err
				}
				runID = record.RunID
				members, memberErr := carrier.Journal.RunOperations(leaseContext, runID)
				if memberErr != nil {
					return memberErr
				}
				operationIDs = operationIDs[:0]
				for _, member := range members {
					operationIDs = append(operationIDs, member.Record.OperationID)
					manualHistory, providerAdmissionToken, memberErr = carrier.resumeOpenAIToolOperationForProvider(leaseContext, current, authoritativeManifest, member.Record.OperationID)
					if memberErr != nil {
						return memberErr
					}
				}
				nextRunSequence = uint64(len(members))
				allCompleted, terminalText := true, ""
				for _, member := range members {
					if member.Record.State != openAIToolStateCompleted || strings.TrimSpace(member.Envelope.FinalOutput) == "" || len(member.Envelope.FinalOutputItems) == 0 || terminalText != "" && terminalText != member.Envelope.FinalOutput {
						allCompleted = false
						break
					}
					terminalText = member.Envelope.FinalOutput
				}
				if allCompleted {
					if _, finalizeErr := carrier.reconcileOrFinalizeOpenAIToolRun(leaseContext, current, request.Expectation, runID, terminalText, operationIDs); finalizeErr != nil {
						return fmt.Errorf("reconcile completed OpenAI function-tool run: %w", finalizeErr)
					}
					if err := carrier.Journal.CompleteRun(leaseContext, runID, terminalText); err != nil {
						return err
					}
					result = openAIToolLoopResult{Text: terminalText, OperationIDs: append([]string(nil), operationIDs...), Model: openAIToolRunnerModel, Reasoning: openAIToolRunnerReasoningEffort}
					return nil
				}
				continue
			}
			if !replayed {
				if record.RunSequence != nextRunSequence {
					return errors.New("OpenAI tool run sequence changed during reservation")
				}
				nextRunSequence++
			}
			if !containsOpenAIToolOperation(operationIDs, record.OperationID) {
				operationIDs = append(operationIDs, record.OperationID)
			}
			manualHistory, providerAdmissionToken, err = carrier.resumeOpenAIToolOperationForProvider(leaseContext, current, authoritativeManifest, record.OperationID)
			if err != nil {
				return err
			}
		}
	})
	if err != nil {
		return openAIToolLoopResult{}, err
	}
	return result, nil
}

func containsOpenAIToolOperation(operationIDs []string, target string) bool {
	for _, operationID := range operationIDs {
		if operationID == target {
			return true
		}
	}
	return false
}

func snapshotOpenAIToolProviderAdmission(ctx context.Context, current openAIToolCurrentAuthority) (string, error) {
	guard, ok := current.(openAIToolProviderAdmissionGuard)
	if !ok {
		return "", nil
	}
	token, err := guard.SnapshotOpenAIToolProviderAdmission(ctx)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", errors.New("OpenAI tool provider source snapshot is unavailable")
	}
	return token, nil
}

func withOpenAIToolProviderAdmission(ctx context.Context, current openAIToolCurrentAuthority, token string, use func(context.Context) error) error {
	guard, ok := current.(openAIToolProviderAdmissionGuard)
	if !ok {
		return use(ctx)
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("OpenAI tool provider source snapshot is missing")
	}
	return guard.WithOpenAIToolProviderAdmission(ctx, token, use)
}

// resumeOpenAIToolOperationForProvider brackets replay/reconciliation with a
// stable product-source generation. One pass may legitimately commit the
// operation or its pending projection; a second stable pass then proves the
// exact manual output against that successor before the provider lease starts.
func (carrier *openAIToolLoopCarrier) resumeOpenAIToolOperationForProvider(ctx context.Context, current openAIToolCurrentAuthority, manifest openAIToolManifest, operationID string) ([]openAIResponsesToolInputItem, string, error) {
	if _, guarded := current.(openAIToolProviderAdmissionGuard); !guarded {
		history, err := carrier.resumeOpenAIToolOperation(ctx, current, manifest, operationID)
		return history, "", err
	}
	for attempt := 0; attempt < 3; attempt++ {
		before, err := snapshotOpenAIToolProviderAdmission(ctx, current)
		if err != nil {
			return nil, "", err
		}
		history, err := carrier.resumeOpenAIToolOperation(ctx, current, manifest, operationID)
		if err != nil {
			return nil, "", err
		}
		after, err := snapshotOpenAIToolProviderAdmission(ctx, current)
		if err != nil {
			return nil, "", err
		}
		if before == after {
			return history, after, nil
		}
	}
	return nil, "", errors.New("OpenAI tool provider source changed during operation replay")
}

func validateOpenAIToolResponseProjection(response openAIResponsesToolResponse) error {
	body, err := json.Marshal(struct {
		ID                string            `json:"id"`
		Model             string            `json:"model"`
		Status            string            `json:"status"`
		IncompleteDetails any               `json:"incomplete_details"`
		Output            []json.RawMessage `json:"output"`
	}{
		ID: response.ResponseID, Model: response.Model, Status: "completed",
		IncompleteDetails: nil, Output: cloneOpenAIToolRawItems(response.ExactOutputItems),
	})
	if err != nil {
		return err
	}
	projected, err := parseOpenAIToolResponsesBody(body)
	if err != nil {
		return fmt.Errorf("OpenAI function-tool exact output projection is invalid: %w", err)
	}
	if projected.Text != response.Text || projected.Refusal != response.Refusal || len(projected.FunctionCalls) != len(response.FunctionCalls) {
		return errors.New("OpenAI function-tool decoded response does not match its exact output items")
	}
	for index := range projected.FunctionCalls {
		exact, decoded := projected.FunctionCalls[index], response.FunctionCalls[index]
		if exact.Name != decoded.Name || exact.CallID != decoded.CallID || !bytes.Equal(bytes.TrimSpace(exact.Arguments), bytes.TrimSpace(decoded.Arguments)) {
			return errors.New("OpenAI function-tool decoded call does not match its exact replay item")
		}
	}
	return nil
}

func (carrier *openAIToolLoopCarrier) resumeOpenAIToolOperation(ctx context.Context, current openAIToolCurrentAuthority, manifest openAIToolManifest, operationID string) ([]openAIResponsesToolInputItem, error) {
	unlockOperation := carrier.Journal.lockOperation(operationID)
	defer unlockOperation()
	record, envelope, err := carrier.Journal.Record(ctx, operationID)
	if err != nil {
		return nil, err
	}
	entry, admitted := manifest.admitted(record.ToolName)
	if !admitted || record.ManifestSHA256 != manifest.DigestSHA256 || record.SchemaSHA256 != entry.SchemaSHA256 || record.PolicyRevision != entry.PolicyRevision {
		return nil, errors.New("OpenAI tool pending operation no longer matches the frozen manifest")
	}
	currentPreimage, err := current.AuthorizeOpenAITool(ctx, record.Expectation, entry, envelope.Arguments)
	if err != nil {
		return nil, fmt.Errorf("reauthorize OpenAI function %q: %w", record.ToolName, err)
	}
	if record.State == openAIToolStateQuarantined {
		return nil, fmt.Errorf("OpenAI tool operation %s is quarantined: %s", record.OperationID, record.QuarantineReason)
	}
	if err := carrier.Journal.VerifyCurrent(ctx); err != nil {
		return nil, fmt.Errorf("verify OpenAI function-tool journal before effect reconciliation: %w", err)
	}
	reconciliation, reconcileErr := carrier.Executor.ReconcileOpenAITool(ctx, current, record.OperationID, record.Expectation, entry, envelope.Arguments, record.PreimageDigest)
	if reconcileErr != nil {
		return nil, fmt.Errorf("reconcile OpenAI function %q: %w", record.ToolName, reconcileErr)
	}
	if record.State == openAIToolStateReserved {
		switch reconciliation.Status {
		case openAIToolReconciliationCommitted:
			if err := validateOpenAIToolEffectCommit(record.ToolName, reconciliation.Commit); err != nil {
				_ = carrier.Journal.Quarantine(ctx, record.OperationID, "reconciliation returned an invalid exact commit")
				return nil, err
			}
			if err := carrier.Journal.CommitEffect(ctx, record.OperationID, reconciliation.Commit); err != nil {
				return nil, err
			}
		case openAIToolReconciliationNotApplied:
			if strings.TrimSpace(currentPreimage) != record.PreimageDigest {
				_ = carrier.Journal.Quarantine(ctx, record.OperationID, "preimage changed while exact effect was absent")
				return nil, errors.New("OpenAI tool preimage changed before a non-applied effect could execute")
			}
			if err := carrier.Journal.BeginAttempt(ctx, record.OperationID, record.PreimageDigest); err != nil {
				return nil, err
			}
			if err := carrier.Journal.VerifyCurrent(ctx); err != nil {
				return nil, fmt.Errorf("verify OpenAI function-tool journal before effect execution: %w", err)
			}
			commit, executeErr := carrier.Executor.ExecuteOpenAITool(ctx, current, record.OperationID, record.Expectation, entry, envelope.Arguments, record.PreimageDigest)
			if executeErr != nil {
				return nil, fmt.Errorf("execute OpenAI function %q: %w", record.ToolName, executeErr)
			}
			if err := validateOpenAIToolEffectCommit(record.ToolName, commit); err != nil {
				return nil, err
			}
			if err := carrier.Journal.CommitEffect(ctx, record.OperationID, commit); err != nil {
				return nil, err
			}
		case openAIToolReconciliationAmbiguous:
			if err := carrier.Journal.Quarantine(ctx, record.OperationID, "effect reconciliation was ambiguous"); err != nil {
				return nil, err
			}
			return nil, errors.New("OpenAI tool effect reconciliation was ambiguous and the operation was quarantined")
		default:
			return nil, errors.New("OpenAI tool executor returned an invalid reconciliation status")
		}
	} else {
		if reconciliation.Status != openAIToolReconciliationCommitted || validateOpenAIToolEffectCommit(record.ToolName, reconciliation.Commit) != nil || !openAIToolEffectCommitMatchesRecord(reconciliation.Commit, record, envelope) {
			if err := carrier.Journal.Quarantine(ctx, record.OperationID, "committed effect postimage could not be revalidated exactly"); err != nil {
				return nil, err
			}
			return nil, errors.New("OpenAI tool committed effect could not be revalidated and was quarantined")
		}
	}
	record, envelope, err = carrier.Journal.Record(ctx, operationID)
	if err != nil {
		return nil, err
	}
	switch record.State {
	case openAIToolStateEffectCommitted:
		if !json.Valid(envelope.ToolOutput) || len(envelope.ExactOutputItems) == 0 || len(envelope.ProviderCallIDs) == 0 {
			return nil, errors.New("OpenAI tool journal is missing exact committed replay material")
		}
		history := cloneOpenAIToolHistory(envelope.ManualHistory)
		for _, exactItem := range envelope.ExactOutputItems {
			history = append(history, openAIResponsesToolInputItem{Raw: append(json.RawMessage(nil), exactItem...)})
		}
		history = append(history, openAIResponsesToolInputItem{Type: "function_call_output", CallID: envelope.ProviderCallIDs[0], Output: string(envelope.ToolOutput)})
		if err := carrier.Journal.MarkContinuationSent(ctx, operationID, history); err != nil {
			return nil, err
		}
		return history, nil
	case openAIToolStateContinuationSent, openAIToolStateCompleted:
		if len(envelope.ManualHistory) == 0 {
			return nil, errors.New("OpenAI tool journal is missing the exact continuation history")
		}
		return cloneOpenAIToolHistory(envelope.ManualHistory), nil
	case openAIToolStateQuarantined:
		return nil, fmt.Errorf("OpenAI tool operation %s is quarantined: %s", record.OperationID, record.QuarantineReason)
	default:
		return nil, fmt.Errorf("OpenAI tool operation cannot resume from state %s", record.State)
	}
}

func openAIToolEffectCommitMatchesRecord(commit openAIToolEffectCommit, record openAIToolJournalRecord, envelope openAIToolReplayEnvelope) bool {
	return hmac.Equal(commit.FunctionOutput, envelope.ToolOutput) &&
		strings.TrimSpace(commit.PostimageDigest) == record.PostimageDigest &&
		strings.TrimSpace(commit.ReconciliationDigest) == record.ReconciliationDigest
}

func validateOpenAIToolEffectCommit(tool string, commit openAIToolEffectCommit) error {
	if !json.Valid(commit.FunctionOutput) || len(commit.FunctionOutput) > orchestratorToolResultBudgetChars {
		return fmt.Errorf("execute OpenAI function %q returned invalid or unbounded JSON", tool)
	}
	if err := validateOpenAIToolMinimizedResult(tool, commit.FunctionOutput); err != nil {
		return err
	}
	if strings.TrimSpace(commit.PostimageDigest) == "" || strings.TrimSpace(commit.ReconciliationDigest) == "" {
		return fmt.Errorf("execute OpenAI function %q omitted postimage or reconciliation proof", tool)
	}
	return nil
}

func (carrier *openAIToolLoopCarrier) reconcileOrFinalizeOpenAIToolRun(ctx context.Context, current openAIToolCurrentAuthority, expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (openAIToolFinalizationCommit, error) {
	operationIDs = append([]string(nil), operationIDs...)
	alreadyReceipted := false
	for _, operationID := range operationIDs {
		record, _, recordErr := carrier.Journal.Record(ctx, operationID)
		if recordErr != nil {
			return openAIToolFinalizationCommit{}, recordErr
		}
		if record.FinalizationRunDigest != "" {
			alreadyReceipted = true
		}
	}
	wantRunDigest, err := openAIToolFinalizationRunDigest(expectation, runID, text, operationIDs)
	if err != nil {
		return openAIToolFinalizationCommit{}, err
	}
	if err := carrier.Journal.VerifyCurrent(ctx); err != nil {
		return openAIToolFinalizationCommit{}, fmt.Errorf("verify OpenAI function-tool journal before final-use reconciliation: %w", err)
	}
	reconciliation, err := carrier.Finalizer.ReconcileOpenAIToolRunFinalization(ctx, current, expectation, runID, text, operationIDs)
	if err != nil {
		return openAIToolFinalizationCommit{}, err
	}
	var commit openAIToolFinalizationCommit
	switch reconciliation.Status {
	case openAIToolReconciliationCommitted:
		commit = reconciliation.Commit
	case openAIToolReconciliationNotApplied:
		if alreadyReceipted {
			for _, operationID := range operationIDs {
				_ = carrier.Journal.Quarantine(ctx, operationID, "durably receipted final use was absent during reconciliation")
			}
			return openAIToolFinalizationCommit{}, errors.New("OpenAI function-tool durably receipted final use could not be reconciled")
		}
		if err := carrier.Journal.VerifyCurrent(ctx); err != nil {
			return openAIToolFinalizationCommit{}, fmt.Errorf("verify OpenAI function-tool journal before final-use execution: %w", err)
		}
		commit, err = carrier.Finalizer.FinalizeOpenAIToolRun(ctx, current, expectation, runID, text, operationIDs)
		if err != nil {
			return openAIToolFinalizationCommit{}, err
		}
	case openAIToolReconciliationAmbiguous:
		for _, operationID := range operationIDs {
			_ = carrier.Journal.Quarantine(ctx, operationID, "final-use or fan-out reconciliation was ambiguous")
		}
		return openAIToolFinalizationCommit{}, errors.New("OpenAI function-tool final-use reconciliation was ambiguous")
	default:
		return openAIToolFinalizationCommit{}, errors.New("OpenAI function-tool finalizer returned an invalid reconciliation status")
	}
	if commit.RunDigest != wantRunDigest || !equalOpenAIToolStrings(commit.OperationIDs, operationIDs) || strings.TrimSpace(commit.FinalUseDigest) == "" || strings.TrimSpace(commit.FanOutReceiptDigest) == "" {
		return openAIToolFinalizationCommit{}, errors.New("OpenAI function-tool finalization receipt is incomplete or belongs to another run")
	}
	return commit, nil
}

func openAIToolFinalizationRunDigest(expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (string, error) {
	if strings.TrimSpace(runID) == "" {
		return "", errors.New("OpenAI function-tool finalization run ID is required")
	}
	return openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-final-use-v1", "manifest_digest": openAIToolManifestV1SHA256,
		"expectation": expectation, "run_id": runID, "terminal_text": strings.TrimSpace(text), "operation_ids": append([]string(nil), operationIDs...),
	})
}

func equalOpenAIToolStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func openAIToolCanonicalDigestOnly(value any) (string, error) {
	digest, _, err := openAIToolCanonicalDigest(value)
	return digest, err
}
