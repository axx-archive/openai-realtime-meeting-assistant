package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type openAIToolScriptStep struct {
	response openAIResponsesToolResponse
	err      error
}

type openAIToolScriptProvider struct {
	mu       sync.Mutex
	steps    []openAIToolScriptStep
	calls    int
	held     *atomic.Int32
	requests []openAIResponsesToolRequest
	inspect  func(int, openAIResponsesToolRequest) error
}

func (provider *openAIToolScriptProvider) RespondWithOpenAITools(_ context.Context, request openAIResponsesToolRequest) (openAIResponsesToolResponse, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.held != nil && provider.held.Load() == 0 {
		return openAIResponsesToolResponse{}, errors.New("test provider called outside authority lease")
	}
	if request.ManifestDigest != openAIToolManifestV1SHA256 || request.Model != openAIToolRunnerModel || request.Reasoning["effort"] != openAIToolRunnerReasoningEffort || request.Store || request.ParallelToolCalls || len(request.Tools) != 4 {
		return openAIResponsesToolResponse{}, errors.New("test provider saw widened route")
	}
	request.Input = cloneOpenAIToolHistory(request.Input)
	provider.requests = append(provider.requests, request)
	if provider.inspect != nil {
		if err := provider.inspect(provider.calls, request); err != nil {
			return openAIResponsesToolResponse{}, err
		}
	}
	if provider.calls >= len(provider.steps) {
		return openAIResponsesToolResponse{}, errors.New("unexpected test provider call")
	}
	step := provider.steps[provider.calls]
	provider.calls++
	return step.response, step.err
}

type openAIToolTestAuthority struct {
	held           atomic.Int32
	reject         atomic.Bool
	preimage       string
	currentSource  string
	afterAuthorize func()
}

func (authority *openAIToolTestAuthority) WithCurrentOpenAIToolAuthority(ctx context.Context, expectation openAIToolAuthorityExpectation, use func(context.Context, openAIToolCurrentAuthority) error) error {
	if authority.reject.Load() {
		return errors.New("test authority revoked")
	}
	authority.held.Add(1)
	defer authority.held.Add(-1)
	return use(ctx, authority)
}

func (authority *openAIToolTestAuthority) AuthorizeOpenAITool(_ context.Context, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, arguments map[string]any) (string, error) {
	if authority.held.Load() == 0 || authority.reject.Load() {
		return "", errors.New("test authority is not current")
	}
	if expectation.ToolName != entry.Name || expectation.SchemaDigest != entry.SchemaSHA256 || expectation.PolicyRevision != entry.PolicyRevision || len(arguments) == 0 {
		return "", errors.New("test operation expectation drift")
	}
	if authority.currentSource != "" && expectation.SourceWindowDigest != authority.currentSource {
		return "", errors.New("test source window is no longer current")
	}
	if authority.afterAuthorize != nil {
		authority.afterAuthorize()
	}
	return authority.preimage, nil
}

type openAIToolTestExecutor struct {
	mu                sync.Mutex
	authority         *openAIToolTestAuthority
	counts            map[string]int
	commits           map[string]openAIToolEffectCommit
	afterExecute      func()
	afterReconcile    func()
	reconcileOverride *openAIToolReconciliation
	goalProgress      int
}

func (executor *openAIToolTestExecutor) ReconcileOpenAITool(_ context.Context, _ openAIToolCurrentAuthority, operationID string, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, _ map[string]any, _ string) (openAIToolReconciliation, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.authority.held.Load() == 0 || executor.authority.reject.Load() || expectation.ToolName != entry.Name {
		return openAIToolReconciliation{}, errors.New("test reconciliation escaped authority")
	}
	if executor.reconcileOverride != nil {
		return *executor.reconcileOverride, nil
	}
	if commit, ok := executor.commits[operationID]; ok {
		return openAIToolReconciliation{Status: openAIToolReconciliationCommitted, Commit: commit}, nil
	}
	if executor.afterReconcile != nil {
		executor.afterReconcile()
	}
	return openAIToolReconciliation{Status: openAIToolReconciliationNotApplied}, nil
}

func (executor *openAIToolTestExecutor) ExecuteOpenAITool(_ context.Context, _ openAIToolCurrentAuthority, operationID string, expectation openAIToolAuthorityExpectation, entry openAIToolManifestEntry, arguments map[string]any, preimageDigest string) (openAIToolEffectCommit, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.authority.held.Load() == 0 || executor.authority.reject.Load() || expectation.ToolName != entry.Name || strings.TrimSpace(preimageDigest) == "" {
		return openAIToolEffectCommit{}, errors.New("test effect escaped authority")
	}
	var output json.RawMessage
	switch entry.Name {
	case controlToolReportGoalState:
		if progress, ok := asOptionalInt(arguments["progress_percent"]); ok {
			if progress < executor.goalProgress {
				return openAIToolEffectCommit{}, errors.New("test goal backend rejected non-monotonic progress")
			}
			executor.goalProgress = progress
		}
		output = json.RawMessage(fmt.Sprintf(`{"goal_status":"running","stage":"execute","receipt":%q}`, "goal:"+operationID))
	case "answer_memory_question":
		output = json.RawMessage(fmt.Sprintf(`{"answer":"The decision was approved.","sources":[%q]}`, "memory:"+operationID))
	case "create_artifact":
		output = json.RawMessage(`{"artifact_id":"artifact-1","title":"Decision","type":"document","status":"created"}`)
	case "update_artifact":
		output = json.RawMessage(`{"artifact_id":"artifact-1","revision":"4","status":"updated"}`)
	default:
		return openAIToolEffectCommit{}, errors.New("test backend rejects unknown tool")
	}
	executor.counts[operationID]++
	commit := openAIToolEffectCommit{FunctionOutput: output, PostimageDigest: "postimage:" + operationID, ReconciliationDigest: "reconcile:" + operationID}
	if executor.commits == nil {
		executor.commits = map[string]openAIToolEffectCommit{}
	}
	executor.commits[operationID] = commit
	if executor.afterExecute != nil {
		executor.afterExecute()
	}
	return commit, nil
}

func (executor *openAIToolTestExecutor) ReconcileGoalState(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	return executor.ReconcileOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) ApplyGoalState(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	return executor.ExecuteOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) ReconcileMemoryAnswer(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	return executor.ReconcileOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) ReadMemoryAnswer(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	return executor.ExecuteOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) ReconcileArtifactCreate(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	return executor.ReconcileOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) CreatePrivateArtifact(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	return executor.ExecuteOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) ReconcileArtifactUpdate(ctx context.Context, request openAIToolEffectRequest) (openAIToolReconciliation, error) {
	return executor.ReconcileOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}
func (executor *openAIToolTestExecutor) UpdateAuthorizedArtifact(ctx context.Context, request openAIToolEffectRequest) (openAIToolEffectCommit, error) {
	return executor.ExecuteOpenAITool(ctx, request.Current, request.OperationID, request.Expectation, request.Entry, request.Arguments, request.PreimageDigest)
}

func (executor *openAIToolTestExecutor) total() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	total := 0
	for _, count := range executor.counts {
		total += count
	}
	return total
}

type openAIToolTestFinalizer struct {
	mu                sync.Mutex
	authority         *openAIToolTestAuthority
	seen              map[string]bool
	commits           map[string]openAIToolFinalizationCommit
	count             int
	calls             int
	beforeFinalize    func()
	afterFinalize     func()
	reconcileOverride *openAIToolFinalizationReconciliation
}

func (finalizer *openAIToolTestFinalizer) ReconcileOpenAIToolRunFinalization(_ context.Context, _ openAIToolCurrentAuthority, expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (openAIToolFinalizationReconciliation, error) {
	finalizer.mu.Lock()
	defer finalizer.mu.Unlock()
	if finalizer.authority.held.Load() == 0 || finalizer.authority.reject.Load() || text == "" {
		return openAIToolFinalizationReconciliation{}, errors.New("test final use reconciliation escaped authority")
	}
	if finalizer.reconcileOverride != nil {
		return *finalizer.reconcileOverride, nil
	}
	runDigest, err := openAIToolFinalizationRunDigest(expectation, runID, text, operationIDs)
	if err != nil {
		return openAIToolFinalizationReconciliation{}, err
	}
	if commit, ok := finalizer.commits[runDigest]; ok {
		return openAIToolFinalizationReconciliation{Status: openAIToolReconciliationCommitted, Commit: commit}, nil
	}
	return openAIToolFinalizationReconciliation{Status: openAIToolReconciliationNotApplied}, nil
}

func (finalizer *openAIToolTestFinalizer) FinalizeOpenAIToolRun(_ context.Context, _ openAIToolCurrentAuthority, expectation openAIToolAuthorityExpectation, runID, text string, operationIDs []string) (openAIToolFinalizationCommit, error) {
	finalizer.mu.Lock()
	defer finalizer.mu.Unlock()
	if finalizer.authority.held.Load() == 0 || finalizer.authority.reject.Load() || text == "" {
		return openAIToolFinalizationCommit{}, errors.New("test final use escaped authority")
	}
	if finalizer.beforeFinalize != nil {
		finalizer.beforeFinalize()
	}
	runDigest, err := openAIToolFinalizationRunDigest(expectation, runID, text, operationIDs)
	if err != nil {
		return openAIToolFinalizationCommit{}, err
	}
	key := runDigest
	finalizer.calls++
	if !finalizer.seen[key] {
		finalizer.seen[key] = true
		finalizer.count++
	}
	commit := openAIToolFinalizationCommit{RunDigest: runDigest, OperationIDs: append([]string(nil), operationIDs...), FinalUseDigest: "final-use:" + key, FanOutReceiptDigest: "fan-out:" + key}
	if finalizer.commits == nil {
		finalizer.commits = map[string]openAIToolFinalizationCommit{}
	}
	finalizer.commits[runDigest] = commit
	if finalizer.afterFinalize != nil {
		finalizer.afterFinalize()
	}
	return commit, nil
}

func openAIToolFunctionResponse(t *testing.T, responseID, callID, name string, arguments json.RawMessage) openAIResponsesToolResponse {
	t.Helper()
	if !json.Valid(arguments) {
		t.Fatalf("invalid test arguments: %s", arguments)
	}
	callItem, err := json.Marshal(map[string]any{"type": "function_call", "status": "completed", "name": name, "call_id": callID, "arguments": string(arguments)})
	if err != nil {
		t.Fatal(err)
	}
	return openAIResponsesToolResponse{
		ResponseID: responseID, Model: openAIToolRunnerModel,
		FunctionCalls:    []openAIResponsesFunctionCall{{Name: name, CallID: callID, Arguments: append(json.RawMessage(nil), arguments...)}},
		ExactOutputItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","status":"completed","id":"reasoning-1","summary":[]}`), callItem},
	}
}

func openAIToolTerminalResponse(responseID, text string) openAIResponsesToolResponse {
	item, _ := json.Marshal(map[string]any{"type": "message", "status": "completed", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": text}}})
	return openAIResponsesToolResponse{ResponseID: responseID, Model: openAIToolRunnerModel, Text: text, ExactOutputItems: []json.RawMessage{item}}
}

func openAIToolWireArguments(tool string) json.RawMessage {
	switch tool {
	case controlToolReportGoalState:
		return json.RawMessage(`{"goal_status":"running","review_gate":"pending","stage":"execute","progress_percent":20,"note":null}`)
	case "answer_memory_question":
		return json.RawMessage(`{"query":"What was decided?"}`)
	case "create_artifact":
		return json.RawMessage(`{"mode":"artifacts","query":"Save the decision","content":"Approved"}`)
	case "update_artifact":
		return json.RawMessage(`{"artifact_id":"artifact-1","title":null,"content":"Revised"}`)
	default:
		panic(tool)
	}
}

func openAIToolTestCarrier(t *testing.T, journal *openAIToolJournal, provider openAIResponsesToolProvider, authority *openAIToolTestAuthority, executor *openAIToolTestExecutor, finalizer *openAIToolTestFinalizer) *openAIToolLoopCarrier {
	t.Helper()
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	return &openAIToolLoopCarrier{Enabled: true, Manifest: manifest, Journal: journal, Authority: authority, Provider: provider, Executor: openAIToolEffectAdapter{Backend: executor}, Finalizer: finalizer}
}

func runOpenAIToolNamedSuite(t *testing.T, tool string) {
	t.Helper()
	ctx := context.Background()
	t.Run("normal", func(t *testing.T) {
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resp-1", "call-1", tool, openAIToolWireArguments(tool))},
			{response: openAIToolTerminalResponse("resp-2", "Completed")},
		}}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		result, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()})
		if err != nil || result.Text != "Completed" || len(result.OperationIDs) != 1 || executor.total() != 1 || finalizer.count != 1 || authority.held.Load() != 0 {
			t.Fatalf("normal result=%+v effects=%d final=%d held=%d err=%v", result, executor.total(), finalizer.count, authority.held.Load(), err)
		}
		record, envelope, err := journal.Record(ctx, result.OperationIDs[0])
		if err != nil || record.State != openAIToolStateCompleted || record.AttemptCount != 1 || record.PostimageDigest == "" || record.ResultDigest == "" || record.FinalizationRunDigest == "" || record.FinalUseDigest == "" || record.FanOutReceiptDigest == "" || len(record.CorrelationDigests) != 3 || len(envelope.ExactOutputItems) != 2 || len(envelope.FinalOutputItems) != 1 {
			t.Fatalf("terminal receipt record=%+v envelope=%+v err=%v", record, envelope, err)
		}
	})

	t.Run("race", func(t *testing.T) {
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		var wait sync.WaitGroup
		errorsSeen := make(chan error, 2)
		for index := 0; index < 2; index++ {
			index := index
			wait.Add(1)
			go func() {
				defer wait.Done()
				provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
					{response: openAIToolFunctionResponse(t, fmt.Sprintf("resp-call-%d", index), fmt.Sprintf("call-%d", index), tool, openAIToolWireArguments(tool))},
					{response: openAIToolTerminalResponse(fmt.Sprintf("resp-terminal-%d", index), "Completed")},
				}}
				_, runErr := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()})
				errorsSeen <- runErr
			}()
		}
		wait.Wait()
		close(errorsSeen)
		for runErr := range errorsSeen {
			if runErr != nil {
				t.Fatal(runErr)
			}
		}
		if executor.total() != 1 || finalizer.count != 1 {
			t.Fatalf("concurrent semantic effect duplicated: effects=%d finalizations=%d", executor.total(), finalizer.count)
		}
	})

	t.Run("restart_after_lost_provider_response", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resp-1", "call-1", tool, openAIToolWireArguments(tool))},
			{err: errors.New("lost provider response")},
		}}
		if _, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()}); err == nil {
			t.Fatal("lost response unexpectedly completed")
		}
		if executor.total() != 1 {
			t.Fatalf("first effect count=%d", executor.total())
		}
		if len(firstProvider.requests) != 2 {
			t.Fatalf("lost-response run requests=%d", len(firstProvider.requests))
		}
		wantReplay, err := canonicalJSON(firstProvider.requests[1].Input)
		if err != nil {
			t.Fatal(err)
		}
		_ = journal.Close()
		reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		secondProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resp-3", "call-3", tool, openAIToolWireArguments(tool))},
			{response: openAIToolTerminalResponse("resp-4", "Completed")},
		}}
		secondProvider.inspect = func(index int, request openAIResponsesToolRequest) error {
			if index != 0 {
				return nil
			}
			gotReplay, err := canonicalJSON(request.Input)
			if err != nil {
				return err
			}
			if !bytes.Equal(gotReplay, wantReplay) {
				return errors.New("restart did not send the exact stored manual continuation")
			}
			return nil
		}
		result, err := openAIToolTestCarrier(t, reopened, secondProvider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()})
		if err != nil || result.Text != "Completed" || executor.total() != 1 || finalizer.count != 1 {
			t.Fatalf("restart result=%+v effects=%d finals=%d err=%v", result, executor.total(), finalizer.count, err)
		}
	})

	t.Run("restart_after_effect_before_journal_commit", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		executor.afterExecute = func() {
			highWater.mu.Lock()
			highWater.rejectOnce = true
			highWater.mu.Unlock()
			executor.afterExecute = nil
		}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resp-1", "call-1", tool, openAIToolWireArguments(tool))},
		}}
		if _, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()}); err == nil {
			t.Fatal("effect/journal crash boundary unexpectedly completed")
		}
		if executor.total() != 1 {
			t.Fatalf("first effect count=%d", executor.total())
		}
		_ = journal.Close()
		reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		secondProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolTerminalResponse("resp-2", "Completed")},
		}}
		result, err := openAIToolTestCarrier(t, reopened, secondProvider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()})
		if err != nil || result.Text != "Completed" || executor.total() != 1 || finalizer.count != 1 {
			t.Fatalf("reconciled restart result=%+v effects=%d finals=%d err=%v", result, executor.total(), finalizer.count, err)
		}
	})

	t.Run("restart_after_terminal_before_final_use_receipt", func(t *testing.T) {
		directory := openAIToolSecureTestDirectory(t)
		highWater := &openAIToolTestHighWater{}
		keyring := newOpenAIToolTestKeyring()
		journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		finalizer.afterFinalize = func() {
			highWater.mu.Lock()
			highWater.rejectOnce = true
			highWater.mu.Unlock()
			finalizer.afterFinalize = nil
		}
		firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resp-1", "call-1", tool, openAIToolWireArguments(tool))},
			{response: openAIToolTerminalResponse("resp-2", "Completed")},
		}}
		if _, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()}); err == nil {
			t.Fatal("terminal/final-use crash boundary unexpectedly completed")
		}
		if executor.total() != 1 || finalizer.count != 1 {
			t.Fatalf("first boundary effects=%d finalizations=%d", executor.total(), finalizer.count)
		}
		_ = journal.Close()
		reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		secondProvider := &openAIToolScriptProvider{held: &authority.held}
		result, err := openAIToolTestCarrier(t, reopened, secondProvider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "do it", Expectation: openAIToolTestExpectation()})
		if err != nil || result.Text != "Completed" || executor.total() != 1 || finalizer.count != 1 || finalizer.calls != 1 || secondProvider.calls != 0 {
			t.Fatalf("terminal restart result=%+v effects=%d finals=%d final_calls=%d provider_calls=%d err=%v", result, executor.total(), finalizer.count, finalizer.calls, secondProvider.calls, err)
		}
	})
}

func TestToolGoalStateNormalRaceRestart(t *testing.T) {
	runOpenAIToolNamedSuite(t, controlToolReportGoalState)
}
func TestToolMemoryReadNormalRaceRestart(t *testing.T) {
	runOpenAIToolNamedSuite(t, "answer_memory_question")
}
func TestToolCreateArtifactNormalRaceRestart(t *testing.T) {
	runOpenAIToolNamedSuite(t, "create_artifact")
}
func TestToolUpdateArtifactNormalRaceRestart(t *testing.T) {
	runOpenAIToolNamedSuite(t, "update_artifact")
}

func TestOpenAIToolMultiOperationRunRestartPreservesFinalizationSetAndOrder(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := buildOpenAIToolManifest()
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-two-operation-restart"
	baseExpectation := openAIToolTestExpectation()
	runRequestDigest, err := openAIToolCanonicalDigestOnly(map[string]any{
		"domain": "stride-openai-tool-run-request-v1", "instructions": "server-owned",
		"user_turn": "recall the decision and save it", "expectation": openAIToolRunBaseExpectation(baseExpectation),
	})
	if err != nil {
		t.Fatal(err)
	}
	history := []openAIResponsesToolInputItem{{Role: "user", Content: "recall the decision and save it"}}
	type stagedOperation struct {
		tool   string
		callID string
		args   json.RawMessage
		output json.RawMessage
	}
	staged := []stagedOperation{
		{tool: "answer_memory_question", callID: "call-memory", args: openAIToolWireArguments("answer_memory_question"), output: json.RawMessage(`{"answer":"The decision was approved.","sources":["memory:decision"]}`)},
		{tool: "create_artifact", callID: "call-create", args: openAIToolWireArguments("create_artifact"), output: json.RawMessage(`{"artifact_id":"artifact-1","title":"Decision","type":"document","status":"created"}`)},
	}
	operationIDs := make([]string, 0, len(staged))
	commits := make(map[string]openAIToolEffectCommit, len(staged))
	for sequence, operation := range staged {
		entry, admitted := manifest.admitted(operation.tool)
		if !admitted {
			t.Fatalf("tool %q not admitted", operation.tool)
		}
		decoded, err := decodeOpenAIToolArguments(operation.args)
		if err != nil {
			t.Fatal(err)
		}
		arguments, err := normalizeOpenAIToolArguments(entry, decoded)
		if err != nil {
			t.Fatal(err)
		}
		expectation := baseExpectation
		expectation.ToolName = operation.tool
		expectation.ManifestDigest = manifest.DigestSHA256
		expectation.SchemaDigest = entry.SchemaSHA256
		expectation.PolicyRevision = entry.PolicyRevision
		expectation.ArgumentsDigest, _, err = openAIToolCanonicalDigest(arguments)
		if err != nil {
			t.Fatal(err)
		}
		response := openAIToolFunctionResponse(t, "response-"+operation.callID, operation.callID, operation.tool, operation.args)
		record, _, replayed, err := journal.Reserve(ctx, entry, arguments, expectation, openAIToolJournalProposal{
			ProviderResponseID: response.ResponseID, ProviderCallID: operation.callID,
			ExactOutputItems: response.ExactOutputItems, PreimageDigest: "preimage", ManifestDigest: manifest.DigestSHA256,
			RunID: runID, RunSequence: uint64(sequence), RunRequestDigest: runRequestDigest,
		}, history)
		if err != nil || replayed {
			t.Fatalf("stage operation %d replayed=%v err=%v", sequence, replayed, err)
		}
		if err := journal.BeginAttempt(ctx, record.OperationID, "preimage"); err != nil {
			t.Fatal(err)
		}
		commit := openAIToolEffectCommit{FunctionOutput: operation.output, PostimageDigest: "postimage:" + record.OperationID, ReconciliationDigest: "reconcile:" + record.OperationID}
		if err := journal.CommitEffect(ctx, record.OperationID, commit); err != nil {
			t.Fatal(err)
		}
		for _, exactItem := range response.ExactOutputItems {
			history = append(history, openAIResponsesToolInputItem{Raw: append(json.RawMessage(nil), exactItem...)})
		}
		history = append(history, openAIResponsesToolInputItem{Type: "function_call_output", CallID: operation.callID, Output: string(operation.output)})
		if err := journal.MarkContinuationSent(ctx, record.OperationID, history); err != nil {
			t.Fatal(err)
		}
		operationIDs = append(operationIDs, record.OperationID)
		commits[record.OperationID] = commit
	}
	terminal := openAIToolTerminalResponse("response-terminal", "Completed both steps")
	if err := journal.RecordRunTerminalResponse(ctx, operationIDs, terminal.ResponseID, terminal.Text, terminal.ExactOutputItems); err != nil {
		t.Fatal(err)
	}
	runDigest, err := openAIToolFinalizationRunDigest(baseExpectation, runID, terminal.Text, operationIDs)
	if err != nil {
		t.Fatal(err)
	}
	finalCommit := openAIToolFinalizationCommit{
		RunDigest: runDigest, OperationIDs: append([]string(nil), operationIDs...),
		FinalUseDigest: "final-use:" + runDigest, FanOutReceiptDigest: "fan-out:" + runDigest,
	}
	for _, operationID := range operationIDs {
		if err := journal.CommitFinalUse(ctx, operationID, finalCommit); err != nil {
			t.Fatal(err)
		}
	}
	// Model a crash after the first member is complete but before the second
	// completion transition. The final external use/fan-out commit already
	// exists and must only be reconciled, never repeated.
	if err := journal.Complete(ctx, operationIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending, err := reopened.PendingForExpectation(ctx, baseExpectation)
	if err != nil || len(pending) != 2 || pending[0].Record.OperationID != operationIDs[0] || pending[1].Record.OperationID != operationIDs[1] || pending[0].Record.State != openAIToolStateCompleted || pending[1].Record.State != openAIToolStateContinuationSent {
		t.Fatalf("durable run reconstruction pending=%+v err=%v", pending, err)
	}
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}, commits: commits}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{runDigest: true}, commits: map[string]openAIToolFinalizationCommit{runDigest: finalCommit}, count: 1}
	provider := &openAIToolScriptProvider{held: &authority.held}
	result, err := openAIToolTestCarrier(t, reopened, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "recall the decision and save it", Expectation: baseExpectation})
	if err != nil || result.Text != terminal.Text || !equalOpenAIToolStrings(result.OperationIDs, operationIDs) || provider.calls != 0 || executor.total() != 0 || finalizer.calls != 0 || finalizer.count != 1 {
		t.Fatalf("restart result=%+v provider=%d effects=%d final_calls=%d final_count=%d err=%v", result, provider.calls, executor.total(), finalizer.calls, finalizer.count, err)
	}
	for _, operationID := range operationIDs {
		record, _, err := reopened.Record(ctx, operationID)
		if err != nil || record.State != openAIToolStateCompleted || record.RunID != runID || !equalOpenAIToolStrings(record.FinalizationOperationIDs, operationIDs) || record.FinalizationRunDigest != runDigest {
			t.Fatalf("final member record=%+v err=%v", record, err)
		}
	}
}

func TestOpenAIToolMultiOperationTerminalPersistenceIsAtomicAcrossChangedRetry(t *testing.T) {
	ctx := context.Background()
	highWater := &openAIToolTestHighWater{}
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", highWater, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "response-memory", "call-memory", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
		{response: openAIToolFunctionResponse(t, "response-create", "call-create", "create_artifact", openAIToolWireArguments("create_artifact"))},
		{response: openAIToolTerminalResponse("response-terminal-first", "First terminal bytes")},
	}}
	firstProvider.inspect = func(index int, _ openAIResponsesToolRequest) error {
		if index == 2 {
			highWater.mu.Lock()
			highWater.rejectOnce = true
			highWater.mu.Unlock()
		}
		return nil
	}
	request := openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "recall and save", Expectation: openAIToolTestExpectation()}
	if _, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, request); err == nil {
		t.Fatal("rejected atomic terminal journal commit unexpectedly completed")
	}
	if executor.total() != 2 || finalizer.count != 0 {
		t.Fatalf("first boundary effects=%d finalizations=%d", executor.total(), finalizer.count)
	}
	journal.mu.Lock()
	records := make([]openAIToolJournalRecord, 0, len(journal.state.Records))
	for _, record := range journal.state.Records {
		records = append(records, record)
	}
	journal.mu.Unlock()
	sort.Slice(records, func(i, k int) bool { return records[i].RunSequence < records[k].RunSequence })
	if len(records) != 2 {
		t.Fatalf("atomic rollback retained %d run members", len(records))
	}
	for _, record := range records {
		_, envelope, err := journal.Record(ctx, record.OperationID)
		if err != nil || envelope.FinalOutput != "" || len(envelope.FinalOutputItems) != 0 {
			t.Fatalf("partial terminal became visible record=%+v envelope=%+v err=%v", record, envelope, err)
		}
	}
	secondProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolTerminalResponse("response-terminal-retry", "Changed retry bytes")},
	}}
	result, err := openAIToolTestCarrier(t, journal, secondProvider, authority, executor, finalizer).Run(ctx, request)
	if err != nil || result.Text != "Changed retry bytes" || len(result.OperationIDs) != 2 || executor.total() != 2 || finalizer.count != 1 || secondProvider.calls != 1 {
		t.Fatalf("atomic retry result=%+v effects=%d finals=%d provider=%d err=%v", result, executor.total(), finalizer.count, secondProvider.calls, err)
	}
}

func TestOpenAIToolConcurrentChangedTerminalBytesFinalizeExactlyOnce(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	terminalReady := &sync.WaitGroup{}
	terminalReady.Add(2)
	finalizationEntered := make(chan struct{})
	releaseFinalization := make(chan struct{})
	var enteredOnce sync.Once
	finalizer.beforeFinalize = func() {
		enteredOnce.Do(func() { close(finalizationEntered) })
		<-releaseFinalization
	}
	provider := func(index int, terminalText string) *openAIToolScriptProvider {
		candidate := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, fmt.Sprintf("response-call-%d", index), fmt.Sprintf("call-%d", index), "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
			{response: openAIToolTerminalResponse(fmt.Sprintf("response-terminal-%d", index), terminalText)},
		}}
		candidate.inspect = func(step int, _ openAIResponsesToolRequest) error {
			if step == 1 {
				terminalReady.Done()
				terminalReady.Wait()
			}
			return nil
		}
		return candidate
	}
	type outcome struct {
		result openAIToolLoopResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	request := openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "what was decided?", Expectation: openAIToolTestExpectation()}
	for index, terminalText := range []string{"Terminal A", "Terminal B"} {
		candidate := provider(index, terminalText)
		go func() {
			result, runErr := openAIToolTestCarrier(t, journal, candidate, authority, executor, finalizer).Run(ctx, request)
			outcomes <- outcome{result: result, err: runErr}
		}()
	}
	select {
	case <-finalizationEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("no terminal candidate reached finalization")
	}
	var rejected outcome
	select {
	case rejected = <-outcomes:
	case <-time.After(5 * time.Second):
		t.Fatal("changed terminal candidate did not fail while the first finalization was held")
	}
	if rejected.err == nil || !strings.Contains(rejected.err.Error(), "terminal response changed") {
		t.Fatalf("changed terminal candidate was not rejected: result=%+v err=%v", rejected.result, rejected.err)
	}
	close(releaseFinalization)
	accepted := <-outcomes
	if accepted.err != nil || accepted.result.Text != "Terminal A" && accepted.result.Text != "Terminal B" || executor.total() != 1 || finalizer.count != 1 || finalizer.calls != 1 {
		t.Fatalf("accepted terminal result=%+v effects=%d final_count=%d final_calls=%d err=%v", accepted.result, executor.total(), finalizer.count, finalizer.calls, accepted.err)
	}
}

func TestOpenAIToolExternalHighWaterAdvancePreventsProviderAdmission(t *testing.T) {
	ctx := context.Background()
	highWater := &openAIToolTestHighWater{}
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", highWater, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	highWater.mu.Lock()
	highWater.anchor = openAIToolJournalAnchor{Generation: journal.anchor.Generation + 1, Digest: "external-generation"}
	highWater.mu.Unlock()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("response", "must not run")}}}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	_, err = openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "turn", Expectation: openAIToolTestExpectation()})
	if err == nil || !errors.Is(err, errOpenAIToolJournalRollback) || provider.calls != 0 || executor.total() != 0 || finalizer.count != 0 {
		t.Fatalf("stale journal admitted work provider=%d effects=%d finals=%d err=%v", provider.calls, executor.total(), finalizer.count, err)
	}
}

func TestOpenAIToolCompletedSemanticReplayReturnsStoredRunWithoutRefinalizing(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "response-first", "call-first", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
		{response: openAIToolTerminalResponse("response-terminal", "Completed")},
	}}
	request := openAIToolLoopRequest{Instructions: "server-owned", UserTurn: "what was decided?", Expectation: openAIToolTestExpectation()}
	first, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	secondProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "response-retry", "call-retry", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
	}}
	second, err := openAIToolTestCarrier(t, journal, secondProvider, authority, executor, finalizer).Run(ctx, request)
	if err != nil || second.Text != first.Text || !equalOpenAIToolStrings(second.OperationIDs, first.OperationIDs) || secondProvider.calls != 0 || executor.total() != 1 || finalizer.calls != 1 || finalizer.count != 1 {
		t.Fatalf("completed replay first=%+v second=%+v provider=%d effects=%d final_calls=%d final_count=%d err=%v", first, second, secondProvider.calls, executor.total(), finalizer.calls, finalizer.count, err)
	}
}

func TestOpenAIToolSemanticReplayCannotDropDifferentUserRequest(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	firstProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "first-response", "first-call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
		{response: openAIToolTerminalResponse("first-terminal", "First request completed")},
	}}
	firstRequest := openAIToolLoopRequest{Instructions: "server", UserTurn: "first user request", Expectation: openAIToolTestExpectation()}
	first, err := openAIToolTestCarrier(t, journal, firstProvider, authority, executor, finalizer).Run(ctx, firstRequest)
	if err != nil || first.Text != "First request completed" || len(first.OperationIDs) != 1 || executor.total() != 1 || finalizer.count != 1 {
		t.Fatalf("first request did not establish exact semantic effect: result=%+v effects=%d final=%d err=%v", first, executor.total(), finalizer.count, err)
	}

	secondProvider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "second-response", "second-call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
	}}
	secondRequest := openAIToolLoopRequest{Instructions: "server", UserTurn: "different user request with the same tool arguments", Expectation: openAIToolTestExpectation()}
	result, err := openAIToolTestCarrier(t, journal, secondProvider, authority, executor, finalizer).Run(ctx, secondRequest)
	if err == nil || result.Text != "" || len(result.OperationIDs) != 0 || secondProvider.calls != 1 || executor.total() != 1 || finalizer.count != 1 {
		t.Fatalf("different request adopted old terminal response: result=%+v provider=%d effects=%d final=%d err=%v", result, secondProvider.calls, executor.total(), finalizer.count, err)
	}
	if !strings.Contains(err.Error(), "different durable request identity") || len(journal.state.Records) != 1 {
		t.Fatalf("different request did not fail closed at one immutable operation: records=%d err=%v", len(journal.state.Records), err)
	}
}

func TestOpenAIToolGoalStateIsControlOnlyAndMonotonic(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "sticky-goal-revision-1"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	first := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "resp-1", "call-1", controlToolReportGoalState, json.RawMessage(`{"goal_status":"running","review_gate":"pending","stage":"execute","progress_percent":50,"note":null}`))},
		{response: openAIToolTerminalResponse("resp-2", "Progress recorded")},
	}}
	if _, err := openAIToolTestCarrier(t, journal, first, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "progress", Expectation: openAIToolTestExpectation()}); err != nil {
		t.Fatal(err)
	}
	second := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolFunctionResponse(t, "resp-3", "call-3", controlToolReportGoalState, json.RawMessage(`{"goal_status":"running","review_gate":"pending","stage":"execute","progress_percent":20,"note":null}`))}}}
	if _, err := openAIToolTestCarrier(t, journal, second, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "lower progress", Expectation: openAIToolTestExpectation()}); err == nil || executor.goalProgress != 50 || executor.total() != 1 {
		t.Fatalf("non-monotonic goal progress changed control state: progress=%d effects=%d err=%v", executor.goalProgress, executor.total(), err)
	}
	for _, commit := range executor.commits {
		if strings.Contains(string(commit.FunctionOutput), "artifact") {
			t.Fatalf("control-only goal result claimed an artifact effect: %s", commit.FunctionOutput)
		}
	}
}

func TestOpenAIToolMemoryReplayRejectsChangedSourceHighWaterBeforeProvider(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	authority := &openAIToolTestAuthority{preimage: "memory-high-water-11", currentSource: "source-window-11"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	first := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "resp-1", "call-1", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
		{err: errors.New("lost provider response")},
	}}
	request := openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}
	if _, err := openAIToolTestCarrier(t, journal, first, authority, executor, finalizer).Run(ctx, request); err == nil || executor.total() != 1 {
		t.Fatalf("memory setup did not reach one bounded read: effects=%d err=%v", executor.total(), err)
	}
	_ = journal.Close()
	authority.currentSource = "source-window-12"
	reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	provider := &openAIToolScriptProvider{held: &authority.held}
	if _, err := openAIToolTestCarrier(t, reopened, provider, authority, executor, finalizer).Run(ctx, request); err == nil || provider.calls != 0 || executor.total() != 1 {
		t.Fatalf("stale memory replay reached provider/effect: provider=%d effects=%d err=%v", provider.calls, executor.total(), err)
	}
}

func TestOpenAIToolCarrierDefaultOffAndRevokedAuthorityMakeNoCalls(t *testing.T) {
	provider := &openAIToolScriptProvider{}
	carrier := &openAIToolLoopCarrier{Provider: provider}
	if _, err := carrier.Run(context.Background(), openAIToolLoopRequest{}); !errors.Is(err, errOpenAIToolCarrierUnavailable) || provider.calls != 0 {
		t.Fatalf("default-off carrier called provider: calls=%d err=%v", provider.calls, err)
	}
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	authority.reject.Store(true)
	journal, err := openOpenAIToolJournal(context.Background(), openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	provider = &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("resp", "no")}}}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(context.Background(), openAIToolLoopRequest{Instructions: "server", UserTurn: "turn", Expectation: openAIToolTestExpectation()}); err == nil || provider.calls != 0 || executor.total() != 0 {
		t.Fatalf("revoked authority leaked provider/effect: calls=%d effects=%d err=%v", provider.calls, executor.total(), err)
	}
}

func TestOpenAIToolCarrierRejectsFrozenManifestSubstitutionBeforeProvider(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	provider := &openAIToolScriptProvider{steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("resp", "no")}}}
	carrier := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer)
	for index := range carrier.Manifest.Tools {
		if carrier.Manifest.Tools[index].Admitted {
			carrier.Manifest.Tools[index].Description = "substituted"
			break
		}
	}
	if _, err := carrier.Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "turn", Expectation: openAIToolTestExpectation()}); err == nil || provider.calls != 0 || executor.total() != 0 {
		t.Fatalf("substituted manifest reached provider/effect: provider=%d effects=%d err=%v", provider.calls, executor.total(), err)
	}
}

func TestOpenAIToolAuthorityRevocationInterleavingsFailClosed(t *testing.T) {
	ctx := context.Background()
	newFixture := func(t *testing.T) (*openAIToolJournal, *openAIToolTestAuthority, *openAIToolTestExecutor, *openAIToolTestFinalizer) {
		t.Helper()
		journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		return journal, authority, executor, finalizer
	}
	request := openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}

	t.Run("after provider before authorization", func(t *testing.T) {
		journal, authority, executor, finalizer := newFixture(t)
		defer journal.Close()
		provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolFunctionResponse(t, "resp", "call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))}}}
		provider.inspect = func(_ int, _ openAIResponsesToolRequest) error { authority.reject.Store(true); return nil }
		if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, request); err == nil || executor.total() != 0 || len(journal.state.Records) != 0 {
			t.Fatalf("revoked pre-authorization reached journal/effect: records=%d effects=%d err=%v", len(journal.state.Records), executor.total(), err)
		}
	})

	t.Run("after reservation before reconciliation", func(t *testing.T) {
		journal, authority, executor, finalizer := newFixture(t)
		defer journal.Close()
		authority.afterAuthorize = func() { authority.afterAuthorize = nil; authority.reject.Store(true) }
		provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolFunctionResponse(t, "resp", "call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))}}}
		if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, request); err == nil || executor.total() != 0 || len(journal.state.Records) != 1 {
			t.Fatalf("revoked reservation reached effect or disappeared: records=%d effects=%d err=%v", len(journal.state.Records), executor.total(), err)
		}
	})

	t.Run("after reconciliation before effect", func(t *testing.T) {
		journal, authority, executor, finalizer := newFixture(t)
		defer journal.Close()
		executor.afterReconcile = func() { executor.afterReconcile = nil; authority.reject.Store(true) }
		provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolFunctionResponse(t, "resp", "call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))}}}
		if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, request); err == nil || executor.total() != 0 {
			t.Fatalf("revoked post-reconciliation reached effect: effects=%d err=%v", executor.total(), err)
		}
	})

	t.Run("before final use and fan-out", func(t *testing.T) {
		journal, authority, executor, finalizer := newFixture(t)
		defer journal.Close()
		provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
			{response: openAIToolFunctionResponse(t, "resp-1", "call-1", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
			{response: openAIToolTerminalResponse("resp-2", "Completed")},
		}}
		provider.inspect = func(index int, _ openAIResponsesToolRequest) error {
			if index == 1 {
				authority.reject.Store(true)
			}
			return nil
		}
		if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, request); err == nil || executor.total() != 1 || finalizer.count != 0 {
			t.Fatalf("revoked final use escaped: effects=%d finalizations=%d err=%v", executor.total(), finalizer.count, err)
		}
	})
}

func TestOpenAIToolAmbiguousReconciliationQuarantinesWithoutEffect(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	ambiguous := openAIToolReconciliation{Status: openAIToolReconciliationAmbiguous}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}, reconcileOverride: &ambiguous}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolFunctionResponse(t, "resp", "call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))}}}
	if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}); err == nil || executor.total() != 0 || len(journal.state.Records) != 1 {
		t.Fatalf("ambiguous reconciliation did not fail closed: records=%d effects=%d err=%v", len(journal.state.Records), executor.total(), err)
	}
	for _, record := range journal.state.Records {
		if record.State != openAIToolStateQuarantined || record.QuarantineReason == "" {
			t.Fatalf("ambiguous operation was not durably quarantined: %+v", record)
		}
	}
}

func TestOpenAIToolAmbiguousFinalUseReconciliationQuarantinesWithoutFanOut(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	ambiguous := openAIToolFinalizationReconciliation{Status: openAIToolReconciliationAmbiguous}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}, reconcileOverride: &ambiguous}
	provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{
		{response: openAIToolFunctionResponse(t, "resp-1", "call-1", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))},
		{response: openAIToolTerminalResponse("resp-2", "Completed")},
	}}
	if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}); err == nil || executor.total() != 1 || finalizer.calls != 0 {
		t.Fatalf("ambiguous final use did not fail closed: effects=%d final_calls=%d err=%v", executor.total(), finalizer.calls, err)
	}
	for _, record := range journal.state.Records {
		if record.State != openAIToolStateQuarantined {
			t.Fatalf("ambiguous final-use operation was not quarantined: %+v", record)
		}
	}
}

func TestOpenAIToolMaxTurnsStopsWithoutDuplicateEffect(t *testing.T) {
	ctx := context.Background()
	journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	steps := make([]openAIToolScriptStep, openAIToolRunnerMaxTurns)
	for index := range steps {
		steps[index] = openAIToolScriptStep{response: openAIToolFunctionResponse(t, fmt.Sprintf("resp-%d", index), fmt.Sprintf("call-%d", index), "answer_memory_question", openAIToolWireArguments("answer_memory_question"))}
	}
	provider := &openAIToolScriptProvider{held: &authority.held, steps: steps}
	if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}); err == nil || !errors.Is(err, errOpenAIToolRunMaxTurns) || executor.total() != 1 || finalizer.calls != 0 {
		t.Fatalf("max-turn boundary result: provider=%d effects=%d final_calls=%d err=%v", provider.calls, executor.total(), finalizer.calls, err)
	}
}

func TestOpenAIToolDurableTurnLimitSurvivesRunAndProcessRestart(t *testing.T) {
	ctx := context.Background()
	directory := openAIToolSecureTestDirectory(t)
	highWater := &openAIToolTestHighWater{}
	keyring := newOpenAIToolTestKeyring()
	authority := &openAIToolTestAuthority{preimage: "preimage"}
	executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
	finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
	request := openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}

	journal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	firstSteps := make([]openAIToolScriptStep, 0, 9)
	for index := 0; index < 8; index++ {
		firstSteps = append(firstSteps, openAIToolScriptStep{response: openAIToolFunctionResponse(t, fmt.Sprintf("first-response-%d", index), fmt.Sprintf("first-call-%d", index), "answer_memory_question", openAIToolWireArguments("answer_memory_question"))})
	}
	firstSteps = append(firstSteps, openAIToolScriptStep{err: errors.New("simulated provider response loss")})
	first := &openAIToolScriptProvider{held: &authority.held, steps: firstSteps}
	if _, err := openAIToolTestCarrier(t, journal, first, authority, executor, finalizer).Run(ctx, request); err == nil || first.calls != 9 || executor.total() != 1 {
		t.Fatalf("first bounded process result: provider=%d effects=%d err=%v", first.calls, executor.total(), err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	secondSteps := make([]openAIToolScriptStep, 7)
	for index := range secondSteps {
		secondSteps[index] = openAIToolScriptStep{response: openAIToolFunctionResponse(t, fmt.Sprintf("second-response-%d", index), fmt.Sprintf("second-call-%d", index), "answer_memory_question", openAIToolWireArguments("answer_memory_question"))}
	}
	second := &openAIToolScriptProvider{held: &authority.held, steps: secondSteps}
	if _, err := openAIToolTestCarrier(t, reopened, second, authority, executor, finalizer).Run(ctx, request); err == nil || !errors.Is(err, errOpenAIToolRunMaxTurns) || second.calls != 7 || executor.total() != 1 || finalizer.calls != 0 {
		t.Fatalf("restart cap result: provider=%d effects=%d final=%d err=%v", second.calls, executor.total(), finalizer.calls, err)
	}
	var blocked openAIToolRunRecord
	for _, run := range reopened.state.Runs {
		if run.State == openAIToolRunBlocked {
			blocked = run
		}
	}
	if blocked.RunID == "" || blocked.ProviderTurnCount != openAIToolRunnerMaxTurns || blocked.BlockedReason != "max_provider_turns" {
		t.Fatalf("durable blocked run missing: %+v", blocked)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	thirdJournal, err := openOpenAIToolJournal(ctx, directory, "journal", highWater, keyring)
	if err != nil {
		t.Fatal(err)
	}
	defer thirdJournal.Close()
	third := &openAIToolScriptProvider{held: &authority.held}
	if _, err := openAIToolTestCarrier(t, thirdJournal, third, authority, executor, finalizer).Run(ctx, request); err == nil || !errors.Is(err, errOpenAIToolRunMaxTurns) || third.calls != 0 || executor.total() != 1 || finalizer.calls != 0 {
		t.Fatalf("post-reopen cap was not closed: provider=%d effects=%d final=%d err=%v", third.calls, executor.total(), finalizer.calls, err)
	}
}

func TestOpenAIToolAfterOpenJournalAndLockMutationPreventsProviderAndEffect(t *testing.T) {
	type mutation struct {
		apply  func(*testing.T, string)
		verify func(*testing.T, string)
	}
	var identicalReplacementInfo os.FileInfo
	mutations := map[string]mutation{
		"journal byte-identical inode replaced": {
			apply: func(t *testing.T, directory string) {
				t.Helper()
				journalPath := filepath.Join(directory, openAIToolJournalFileName)
				raw, err := os.ReadFile(journalPath)
				if err != nil {
					t.Fatal(err)
				}
				replacement := filepath.Join(directory, "identical-replacement")
				if err := os.WriteFile(replacement, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				identicalReplacementInfo, err = os.Lstat(replacement)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, journalPath); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				current, err := os.Lstat(filepath.Join(directory, openAIToolJournalFileName))
				if err != nil || identicalReplacementInfo == nil || !os.SameFile(identicalReplacementInfo, current) {
					t.Fatalf("byte-identical substituted journal was overwritten: current=%v err=%v", current, err)
				}
			},
		},
		"journal replaced": {
			apply: func(t *testing.T, directory string) {
				t.Helper()
				replacement := filepath.Join(directory, "replacement")
				if err := os.WriteFile(replacement, []byte("replacement-journal"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, filepath.Join(directory, openAIToolJournalFileName)); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				raw, err := os.ReadFile(filepath.Join(directory, openAIToolJournalFileName))
				if err != nil || string(raw) != "replacement-journal" {
					t.Fatalf("replacement journal was overwritten: %q err=%v", raw, err)
				}
			},
		},
		"journal deleted": {
			apply: func(t *testing.T, directory string) {
				if err := os.Remove(filepath.Join(directory, openAIToolJournalFileName)); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				if _, err := os.Lstat(filepath.Join(directory, openAIToolJournalFileName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deleted journal was recreated: %v", err)
				}
			},
		},
		"journal symlinked": {
			apply: func(t *testing.T, directory string) {
				target := filepath.Join(directory, "symlink-target")
				if err := os.WriteFile(target, []byte("do-not-overwrite"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(directory, openAIToolJournalFileName)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(directory, openAIToolJournalFileName)); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				raw, err := os.ReadFile(filepath.Join(directory, "symlink-target"))
				if err != nil || string(raw) != "do-not-overwrite" {
					t.Fatalf("symlink target was overwritten: %q err=%v", raw, err)
				}
			},
		},
		"journal hardlinked": {
			apply: func(t *testing.T, directory string) {
				if err := os.Link(filepath.Join(directory, openAIToolJournalFileName), filepath.Join(directory, "journal-hardlink")); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				info, err := os.Lstat(filepath.Join(directory, "journal-hardlink"))
				if err != nil || info.Mode().IsRegular() == false {
					t.Fatalf("hardlink disappeared or changed: %v", err)
				}
			},
		},
		"journal tampered": {
			apply: func(t *testing.T, directory string) {
				if err := os.WriteFile(filepath.Join(directory, openAIToolJournalFileName), []byte("tampered-in-place"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				raw, err := os.ReadFile(filepath.Join(directory, openAIToolJournalFileName))
				if err != nil || string(raw) != "tampered-in-place" {
					t.Fatalf("tampered journal was overwritten: %q err=%v", raw, err)
				}
			},
		},
		"lock replaced": {
			apply: func(t *testing.T, directory string) {
				replacement := filepath.Join(directory, "replacement-lock")
				if err := os.WriteFile(replacement, []byte("replacement-lock"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, filepath.Join(directory, openAIToolJournalLockName)); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, directory string) {
				raw, err := os.ReadFile(filepath.Join(directory, openAIToolJournalLockName))
				if err != nil || string(raw) != "replacement-lock" {
					t.Fatalf("replacement lock was overwritten: %q err=%v", raw, err)
				}
			},
		},
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			directory := openAIToolSecureTestDirectory(t)
			journal, err := openOpenAIToolJournal(ctx, directory, "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			authority := &openAIToolTestAuthority{preimage: "preimage"}
			executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
			finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
			provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("response", "should not run")}}}
			mutation.apply(t, directory)
			if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, openAIToolLoopRequest{Instructions: "server", UserTurn: "answer", Expectation: openAIToolTestExpectation()}); err == nil || provider.calls != 0 || executor.total() != 0 || finalizer.calls != 0 {
				t.Fatalf("mutated live target admitted work: provider=%d effects=%d final=%d err=%v", provider.calls, executor.total(), finalizer.calls, err)
			}
			mutation.verify(t, directory)
		})
	}
}

func TestOpenAIToolCarrierRejectsUnadmittedMalformedAndWrongAuthorityBeforeEffect(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range map[string]func(*openAIResponsesToolResponse, *openAIToolLoopRequest){
		"unadmitted": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", "publish_artifact", json.RawMessage(`{"artifact_id":"artifact-1","published":true}`))
		},
		"wrong artifact": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", "update_artifact", json.RawMessage(`{"artifact_id":"artifact-2","title":null,"content":"changed"}`))
		},
		"create nested generation": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", "create_artifact", json.RawMessage(`{"mode":"artifacts","query":"write it","content":null}`))
		},
		"invalid progress": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", controlToolReportGoalState, json.RawMessage(`{"goal_status":"running","review_gate":"pending","stage":"execute","progress_percent":101,"note":null}`))
		},
		"insufficient authority": func(response *openAIResponsesToolResponse, request *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", "create_artifact", openAIToolWireArguments("create_artifact"))
			request.Expectation.JobAuthority = codexJobAuthorityReadOnly
		},
		"multiple calls": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))
			response.FunctionCalls = append(response.FunctionCalls, response.FunctionCalls[0])
		},
		"refusal": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolTerminalResponse("resp", "no")
			response.Refusal = "refused"
		},
		"incomplete": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolTerminalResponse("resp", "partial")
			response.Incomplete = true
		},
		"wrong model": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolTerminalResponse("resp", "wrong")
			response.Model = "gpt-5.6-sol"
		},
		"malformed exact item": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolTerminalResponse("resp", "bad")
			response.ExactOutputItems = []json.RawMessage{json.RawMessage(`{"type":`)}
		},
		"decoded call differs from exact replay item": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolFunctionResponse(t, "resp", "call", "answer_memory_question", openAIToolWireArguments("answer_memory_question"))
			response.FunctionCalls[0].CallID = "substituted-call"
		},
		"decoded terminal text differs from exact replay item": func(response *openAIResponsesToolResponse, _ *openAIToolLoopRequest) {
			*response = openAIToolTerminalResponse("resp", "exact text")
			response.Text = "substituted text"
		},
	} {
		t.Run(name, func(t *testing.T) {
			journal, err := openOpenAIToolJournal(ctx, openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			authority := &openAIToolTestAuthority{preimage: "preimage"}
			executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
			finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
			response := openAIToolTerminalResponse("resp", "unused")
			request := openAIToolLoopRequest{Instructions: "server", UserTurn: "turn", Expectation: openAIToolTestExpectation()}
			mutate(&response, &request)
			provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: response}}}
			if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(ctx, request); err == nil {
				t.Fatal("unsafe function proposal was accepted")
			}
			if executor.total() != 0 || len(journal.state.Records) != 0 {
				t.Fatalf("unsafe proposal reached effect/journal: effects=%d records=%d", executor.total(), len(journal.state.Records))
			}
		})
	}

	t.Run("cancelled caller", func(t *testing.T) {
		journal, err := openOpenAIToolJournal(context.Background(), openAIToolSecureTestDirectory(t), "journal", &openAIToolTestHighWater{}, newOpenAIToolTestKeyring())
		if err != nil {
			t.Fatal(err)
		}
		defer journal.Close()
		authority := &openAIToolTestAuthority{preimage: "preimage"}
		provider := &openAIToolScriptProvider{held: &authority.held, steps: []openAIToolScriptStep{{response: openAIToolTerminalResponse("resp", "no")}}}
		executor := &openAIToolTestExecutor{authority: authority, counts: map[string]int{}}
		finalizer := &openAIToolTestFinalizer{authority: authority, seen: map[string]bool{}}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := openAIToolTestCarrier(t, journal, provider, authority, executor, finalizer).Run(cancelled, openAIToolLoopRequest{Instructions: "server", UserTurn: "turn", Expectation: openAIToolTestExpectation()}); err == nil || provider.calls != 0 || executor.total() != 0 {
			t.Fatalf("cancelled caller leaked provider/effect: calls=%d effects=%d err=%v", provider.calls, executor.total(), err)
		}
	})
}
