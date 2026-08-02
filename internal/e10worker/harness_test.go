package e10worker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

var harnessNow = time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

func repositoryPolicy(t *testing.T) e9readiness.WorkerIsolationPolicy {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "deploy", "e9", "worker-isolation-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	policy, err := e9readiness.DecodeStrict[e9readiness.WorkerIsolationPolicy](file)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testWorkflow() WorkflowPolicy {
	return WorkflowPolicy{
		ID:               "insights_opportunities_v1",
		AllowedScopes:    []string{"source:read", "artifact:write", "callback:complete"},
		AllowedHosts:     []string{"source.test.invalid", "artifact.test.invalid"},
		MaximumCalls:     4,
		MaximumBytes:     4096,
		CredentialTTL:    5 * time.Minute,
		CallbackAudience: "stride-control.test.invalid",
	}
}

func testSandbox() SandboxSpec {
	return SandboxSpec{
		ReadOnlyRoot: true, WritableMounts: []string{"/tmp", "/workspace/run"},
		MountedPaths: []string{"/workspace/run"}, EnvKeys: []string{"STRIDE_RUN_ID"},
		NetworkMode: "synthetic_gateway_only", Ephemeral: true,
	}
}

func newTestRun(t *testing.T, workflow WorkflowPolicy) (*Controller, RunLease) {
	t.Helper()
	controller, err := NewController(repositoryPolicy(t), []WorkflowPolicy{workflow}, func() time.Time { return harnessNow })
	if err != nil {
		t.Fatal(err)
	}
	lease, err := controller.Admit(RunRequest{RunID: "run-1", WorkflowID: workflow.ID, Sandbox: testSandbox()})
	if err != nil {
		t.Fatal(err)
	}
	return controller, lease
}

func TestExecuteHarnessProducesClosedReceipt(t *testing.T) {
	receipt, err := ExecuteHarness(repositoryPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ProductionReady || receipt.ProviderCalls || receipt.NetworkConnections != 0 || receipt.ActivationDefault != "off" {
		t.Fatalf("receipt crossed boundary: %+v", receipt)
	}
	for _, forbidden := range []string{"synthetic token-free result", "source.test.invalid", "harness-run-1"} {
		if strings.Contains(receipt.ReceiptDigest+strings.Join(receipt.PassedControls, "\n")+strings.Join(receipt.ClaimsExcluded, "\n"), forbidden) {
			t.Fatalf("receipt leaked body or endpoint %q", forbidden)
		}
	}

	tampered := receipt
	tampered.ProviderCalls = true
	if err := ValidateReceipt(tampered); err == nil {
		t.Fatal("provider-call receipt tamper was accepted")
	}
	tampered = receipt
	tampered.ClaimsExcluded = tampered.ClaimsExcluded[:1]
	if err := ValidateReceipt(tampered); err == nil {
		t.Fatal("incomplete evidence boundary was accepted")
	}
	tampered = receipt
	tampered.PolicyDigest = strings.Repeat("0", 64)
	if err := ValidateReceipt(tampered); err == nil {
		t.Fatal("receipt digest tamper was accepted")
	}
}

func TestSandboxAdmissionRejectsProductionAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SandboxSpec)
	}{
		{"writable root", func(v *SandboxSpec) { v.ReadOnlyRoot = false }},
		{"not ephemeral", func(v *SandboxSpec) { v.Ephemeral = false }},
		{"direct network", func(v *SandboxSpec) { v.NetworkMode = "host" }},
		{"extra writable mount", func(v *SandboxSpec) { v.WritableMounts = append(v.WritableMounts, "/workspace/repo") }},
		{"extra read-only mount", func(v *SandboxSpec) { v.MountedPaths = append(v.MountedPaths, "/workspace/repo") }},
		{"production data mount", func(v *SandboxSpec) { v.MountedPaths = append(v.MountedPaths, "/app/data/meeting-memory.jsonl") }},
		{"docker mount", func(v *SandboxSpec) { v.MountedPaths = append(v.MountedPaths, "/var/lib/docker/volumes") }},
		{"provider secret", func(v *SandboxSpec) { v.EnvKeys = append(v.EnvKeys, "OPENAI_API_KEY") }},
		{"database secret", func(v *SandboxSpec) { v.EnvKeys = append(v.EnvKeys, "DATABASE_URL") }},
		{"DR signing secret", func(v *SandboxSpec) { v.EnvKeys = append(v.EnvKeys, "BONFIRE_DR_AUTHORITY_PRIVATE_KEY") }},
		{"unreviewed environment", func(v *SandboxSpec) { v.EnvKeys = append(v.EnvKeys, "HOME") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller, err := NewController(repositoryPolicy(t), []WorkflowPolicy{testWorkflow()}, func() time.Time { return harnessNow })
			if err != nil {
				t.Fatal(err)
			}
			sandbox := testSandbox()
			test.mutate(&sandbox)
			if _, err := controller.Admit(RunRequest{RunID: "unsafe", WorkflowID: testWorkflow().ID, Sandbox: sandbox}); !errors.Is(err, ErrDenied) {
				t.Fatalf("unsafe sandbox error=%v", err)
			}
		})
	}
}

func TestCredentialAndGatewayFailClosed(t *testing.T) {
	workflow := testWorkflow()
	controller, lease := newTestRun(t, workflow)
	credential, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"})
	if err != nil {
		t.Fatal(err)
	}
	if credential.ExpiresAt.Sub(credential.IssuedAt) != workflow.CredentialTTL || credential.ExpiresAt.Sub(credential.IssuedAt) > 10*time.Minute {
		t.Fatalf("credential TTL=%v", credential.ExpiresAt.Sub(credential.IssuedAt))
	}
	request := GatewayRequest{RunID: lease.RunID, WorkflowID: workflow.ID, Credential: credential, Scope: "source:read", Target: "https://source.test.invalid/fixture", Bytes: 1024}
	if err := controller.AuthorizeGateway(request); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*GatewayRequest)
		want   error
	}{
		{"signature", func(v *GatewayRequest) { v.Credential.Signature = strings.Repeat("0", 64) }, ErrInvalidSignature},
		{"run", func(v *GatewayRequest) { v.Credential.RunID = "other" }, ErrInvalidSignature},
		{"scope", func(v *GatewayRequest) { v.Scope = "provider:invoke" }, ErrDenied},
		{"audience", func(v *GatewayRequest) { v.Credential.Audience = "artifact.test.invalid" }, ErrInvalidSignature},
		{"unknown host", func(v *GatewayRequest) { v.Target = "https://other.test.invalid/x" }, ErrDenied},
		{"literal IP", func(v *GatewayRequest) { v.Target = "https://127.0.0.1/x" }, ErrDenied},
		{"metadata IP", func(v *GatewayRequest) { v.Target = "https://169.254.169.254/latest" }, ErrDenied},
		{"localhost", func(v *GatewayRequest) { v.Target = "https://localhost/x" }, ErrDenied},
		{"redirect", func(v *GatewayRequest) { v.RedirectHop = true }, ErrDenied},
		{"query", func(v *GatewayRequest) { v.Target += "?secret=x" }, ErrDenied},
		{"over quota", func(v *GatewayRequest) { v.Bytes = workflow.MaximumBytes }, ErrQuota},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := request
			candidate.Credential.Scopes = append([]string(nil), request.Credential.Scopes...)
			test.mutate(&candidate)
			if err := controller.AuthorizeGateway(candidate); !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
	audienceMismatch := request
	audienceMismatch.Target = "https://artifact.test.invalid/x"
	if err := controller.AuthorizeGateway(audienceMismatch); !errors.Is(err, ErrDenied) {
		t.Fatalf("gateway audience mismatch error=%v", err)
	}

	controller.KillRun(lease.RunID)
	if err := controller.AuthorizeGateway(request); !errors.Is(err, ErrKilled) {
		t.Fatalf("gateway after kill error=%v", err)
	}
	if _, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"}); !errors.Is(err, ErrKilled) {
		t.Fatalf("credential after kill error=%v", err)
	}
}

func TestWorkflowPolicyRejectsUnsafeCallbackAudienceAndQuota(t *testing.T) {
	workflow := testWorkflow()
	workflow.CallbackAudience = "metadata.google.internal"
	if _, err := NewController(repositoryPolicy(t), []WorkflowPolicy{workflow}, func() time.Time { return harnessNow }); err == nil {
		t.Fatal("unsafe callback audience was accepted")
	}
	workflow = testWorkflow()
	workflow.MaximumBytes = uint64(repositoryPolicy(t).RequiredBoundary.Resources.NetworkBytes) + 1
	if _, err := NewController(repositoryPolicy(t), []WorkflowPolicy{workflow}, func() time.Time { return harnessNow }); err == nil {
		t.Fatal("workflow quota above boundary policy was accepted")
	}
	workflow = testWorkflow()
	workflow.CallbackAudience = workflow.AllowedHosts[0]
	if _, err := NewController(repositoryPolicy(t), []WorkflowPolicy{workflow}, func() time.Time { return harnessNow }); err == nil {
		t.Fatal("callback audience overlapping a gateway host was accepted")
	}
}

func TestGlobalKillSwitchStopsAdmissionAndActiveRun(t *testing.T) {
	controller, lease := newTestRun(t, testWorkflow())
	controller.KillAll()
	if _, err := controller.Admit(RunRequest{RunID: "run-2", WorkflowID: lease.WorkflowID, Sandbox: testSandbox()}); !errors.Is(err, ErrKilled) {
		t.Fatalf("global-killed admission error=%v", err)
	}
	if _, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"}); !errors.Is(err, ErrKilled) {
		t.Fatalf("global-killed credential error=%v", err)
	}
}

func TestCredentialExpiryAndScopeBoundaries(t *testing.T) {
	clock := harnessNow
	workflow := testWorkflow()
	controller, err := NewController(repositoryPolicy(t), []WorkflowPolicy{workflow}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	lease, err := controller.Admit(RunRequest{RunID: "expiry-run", WorkflowID: workflow.ID, Sandbox: testSandbox()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read", "provider:invoke"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("over-scoped credential error=%v", err)
	}
	if _, err := controller.IssueCredential(lease, "other.test.invalid", []string{"source:read"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("unknown credential audience error=%v", err)
	}
	credential, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(workflow.CredentialTTL)
	request := GatewayRequest{RunID: lease.RunID, WorkflowID: workflow.ID, Credential: credential, Scope: "source:read", Target: "https://source.test.invalid/x", Bytes: 1}
	if err := controller.AuthorizeGateway(request); !errors.Is(err, ErrDenied) {
		t.Fatalf("expired credential error=%v", err)
	}
}

func TestCallbackSignatureReplayFenceAndIdempotency(t *testing.T) {
	newCallback := func(t *testing.T) (*Controller, RunLease, Credential, Callback) {
		t.Helper()
		controller, lease := newTestRun(t, testWorkflow())
		credential, err := controller.IssueCredential(lease, testWorkflow().CallbackAudience, []string{"callback:complete"})
		if err != nil {
			t.Fatal(err)
		}
		callback, err := controller.NewCallback(lease, credential, "nonce-1", "complete-1", "complete", digestString("result"), harnessNow)
		if err != nil {
			t.Fatal(err)
		}
		return controller, lease, credential, callback
	}
	credentialController, credentialLease := newTestRun(t, testWorkflow())
	sourceCredential, err := credentialController.IssueCredential(credentialLease, "source.test.invalid", []string{"source:read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credentialController.NewCallback(credentialLease, sourceCredential, "nonce-scope", "complete-scope", "complete", digestString("result"), harnessNow); !errors.Is(err, ErrDenied) {
		t.Fatalf("source credential signed callback error=%v", err)
	}
	callbackCredential, err := credentialController.IssueCredential(credentialLease, testWorkflow().CallbackAudience, []string{"callback:complete"})
	if err != nil {
		t.Fatal(err)
	}
	callbackCredential.Signature = strings.Repeat("0", 64)
	if _, err := credentialController.NewCallback(credentialLease, callbackCredential, "nonce-signature", "complete-signature", "complete", digestString("result"), harnessNow); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered callback credential error=%v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Callback)
		want   error
	}{
		{"signature", func(v *Callback) { v.Signature = strings.Repeat("0", 64) }, ErrInvalidSignature},
		{"audience", func(v *Callback) { v.Audience = "wrong.test.invalid" }, ErrDenied},
		{"generation", func(v *Callback) { v.Generation++ }, ErrStaleFence},
		{"fencing token", func(v *Callback) { v.FencingToken = "old" }, ErrStaleFence},
		{"timestamp", func(v *Callback) { v.OccurredAt = harnessNow.Add(-10 * time.Minute) }, ErrInvalidSignature},
		{"result digest", func(v *Callback) { v.ResultDigest = digestString("different") }, ErrInvalidSignature},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller, _, _, callback := newCallback(t)
			test.mutate(&callback)
			if _, err := controller.CommitCallback(callback); !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}

	controller, lease, callbackCredential, callback := newCallback(t)
	staleTimestamp := callback
	staleTimestamp.Nonce = "nonce-stale"
	staleTimestamp.OccurredAt = harnessNow.Add(-10 * time.Minute)
	staleTimestamp.Signature = controller.sign(callbackSigningInput(staleTimestamp))
	if _, err := controller.CommitCallback(staleTimestamp); !errors.Is(err, ErrDenied) {
		t.Fatalf("signed stale callback error=%v", err)
	}
	if result, err := controller.CommitCallback(callback); err != nil || !result.Applied {
		t.Fatalf("first commit result=%+v err=%v", result, err)
	}
	if _, err := controller.CommitCallback(callback); !errors.Is(err, ErrReplay) {
		t.Fatalf("nonce replay error=%v", err)
	}
	retry, err := controller.NewCallback(lease, callbackCredential, "nonce-2", callback.IdempotencyKey, callback.Status, callback.ResultDigest, harnessNow)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := controller.CommitCallback(retry); err != nil || !result.Duplicate {
		t.Fatalf("idempotent retry result=%+v err=%v", result, err)
	}
	changed, err := controller.NewCallback(lease, callbackCredential, "nonce-3", callback.IdempotencyKey, callback.Status, digestString("changed"), harnessNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CommitCallback(changed); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("changed idempotency binding error=%v", err)
	}
	distinct, err := controller.NewCallback(lease, callbackCredential, "nonce-4", "different-terminal", "failed", digestString("different-terminal"), harnessNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CommitCallback(distinct); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("distinct terminal after completion error=%v", err)
	}

	controller2, lease2, _, callback2 := newCallback(t)
	controller2.KillWorkflow(lease2.WorkflowID)
	if _, err := controller2.CommitCallback(callback2); !errors.Is(err, ErrKilled) {
		t.Fatalf("workflow-killed callback error=%v", err)
	}
	if _, err := controller2.Admit(RunRequest{RunID: "run-after-kill", WorkflowID: lease2.WorkflowID, Sandbox: testSandbox()}); !errors.Is(err, ErrKilled) {
		t.Fatalf("workflow-killed admission error=%v", err)
	}
}

func TestConcurrentGatewayQuotaIsAtomic(t *testing.T) {
	workflow := testWorkflow()
	workflow.MaximumCalls = 10
	workflow.MaximumBytes = 10
	controller, lease := newTestRun(t, workflow)
	credential, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"})
	if err != nil {
		t.Fatal(err)
	}
	var passed atomic.Int64
	var denied atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := controller.AuthorizeGateway(GatewayRequest{RunID: lease.RunID, WorkflowID: workflow.ID, Credential: credential, Scope: "source:read", Target: "https://source.test.invalid/x", Bytes: 1})
			switch {
			case err == nil:
				passed.Add(1)
			case errors.Is(err, ErrQuota):
				denied.Add(1)
			default:
				t.Errorf("unexpected gateway error: %v", err)
			}
		}()
	}
	wait.Wait()
	if passed.Load() != 10 || denied.Load() != 54 {
		t.Fatalf("passed=%d denied=%d", passed.Load(), denied.Load())
	}
}

func TestConcurrentCallbackIdempotencyAppliesOnce(t *testing.T) {
	controller, lease := newTestRun(t, testWorkflow())
	callbackCredential, err := controller.IssueCredential(lease, testWorkflow().CallbackAudience, []string{"callback:complete"})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 32
	callbacks := make([]Callback, attempts)
	for index := range callbacks {
		callback, err := controller.NewCallback(lease, callbackCredential, fmt.Sprintf("nonce-%d", index), "complete-1", "complete", digestString("result"), harnessNow)
		if err != nil {
			t.Fatal(err)
		}
		callbacks[index] = callback
	}
	var applied atomic.Int64
	var duplicates atomic.Int64
	var wait sync.WaitGroup
	for _, callback := range callbacks {
		callback := callback
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := controller.CommitCallback(callback)
			if err != nil {
				t.Errorf("callback commit: %v", err)
				return
			}
			if result.Applied {
				applied.Add(1)
			}
			if result.Duplicate {
				duplicates.Add(1)
			}
		}()
	}
	wait.Wait()
	if applied.Load() != 1 || duplicates.Load() != attempts-1 {
		t.Fatalf("applied=%d duplicates=%d", applied.Load(), duplicates.Load())
	}
}

func TestConcurrentDistinctTerminalsApplyExactlyOne(t *testing.T) {
	controller, lease := newTestRun(t, testWorkflow())
	credential, err := controller.IssueCredential(lease, testWorkflow().CallbackAudience, []string{"callback:complete"})
	if err != nil {
		t.Fatal(err)
	}
	callbacks := make([]Callback, 32)
	for index := range callbacks {
		callback, err := controller.NewCallback(lease, credential, fmt.Sprintf("terminal-nonce-%d", index), fmt.Sprintf("terminal-%d", index), "complete", digestString(fmt.Sprintf("result-%d", index)), harnessNow)
		if err != nil {
			t.Fatal(err)
		}
		callbacks[index] = callback
	}
	var applied atomic.Int64
	var denied atomic.Int64
	var wait sync.WaitGroup
	for _, callback := range callbacks {
		callback := callback
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := controller.CommitCallback(callback)
			switch {
			case err == nil && result.Applied:
				applied.Add(1)
			case errors.Is(err, ErrIdempotency):
				denied.Add(1)
			default:
				t.Errorf("distinct terminal result=%+v err=%v", result, err)
			}
		}()
	}
	wait.Wait()
	if applied.Load() != 1 || denied.Load() != int64(len(callbacks)-1) {
		t.Fatalf("applied=%d denied=%d", applied.Load(), denied.Load())
	}
}

func TestCallbackNonceStateIsBoundedPerRun(t *testing.T) {
	controller, lease := newTestRun(t, testWorkflow())
	credential, err := controller.IssueCredential(lease, testWorkflow().CallbackAudience, []string{"callback:complete"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxCallbackNoncesPerRun; index++ {
		callback, err := controller.NewCallback(lease, credential, fmt.Sprintf("bounded-nonce-%d", index), "complete-1", "complete", digestString("result"), harnessNow)
		if err != nil {
			t.Fatal(err)
		}
		result, err := controller.CommitCallback(callback)
		if err != nil || index == 0 && !result.Applied || index > 0 && !result.Duplicate {
			t.Fatalf("callback %d result=%+v err=%v", index, result, err)
		}
	}
	overflow, err := controller.NewCallback(lease, credential, "bounded-overflow", "complete-1", "complete", digestString("result"), harnessNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.CommitCallback(overflow); !errors.Is(err, ErrQuota) {
		t.Fatalf("callback nonce overflow error=%v", err)
	}

	controller.mu.Lock()
	state := controller.runs[lease.RunID]
	gotNonces, gotTerminal := len(state.nonces), state.terminalKey
	controller.mu.Unlock()
	if gotNonces != maxCallbackNoncesPerRun || gotTerminal != "complete-1" {
		t.Fatalf("nonces=%d terminal=%q", gotNonces, gotTerminal)
	}
}
