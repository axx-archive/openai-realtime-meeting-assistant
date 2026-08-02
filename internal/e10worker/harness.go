// Package e10worker implements a token-free executable model of the external
// worker trust boundary. It deliberately has no HTTP client, process launcher,
// provider adapter, production queue integration, or environment lookup. The
// package can prove its contracts locally; it cannot activate a worker.
package e10worker

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

const (
	ReceiptSchema           = "stride.e10.worker-harness-receipt/v1"
	EvidenceClass           = "local_token_free_boundary_harness"
	maxCallbackNoncesPerRun = 64
)

var (
	ErrDenied           = errors.New("worker boundary denied")
	ErrKilled           = errors.New("worker kill switch is active")
	ErrQuota            = errors.New("worker quota exceeded")
	ErrReplay           = errors.New("callback nonce replayed")
	ErrStaleFence       = errors.New("callback fence is stale")
	ErrIdempotency      = errors.New("callback idempotency binding changed")
	ErrInvalidSignature = errors.New("invalid boundary signature")
)

var forbiddenMountPrefixes = []string{
	"/app/data",
	"/app/codex-queue",
	"/var/lib/docker",
	"/run/bonfire-dr",
	"/var/lib/postgresql",
}

var forbiddenEnvironment = map[string]struct{}{
	"DATABASE_URL":                     {},
	"BONFIRE_CANONICAL_DATABASE_URL":   {},
	"OPENAI_API_KEY":                   {},
	"ANTHROPIC_API_KEY":                {},
	"BONFIRE_DR_ENVELOPE_KEY":          {},
	"BONFIRE_DR_AUTHORITY_PRIVATE_KEY": {},
	"BONFIRE_DR_MANIFEST_PRIVATE_KEY":  {},
}

var allowedHarnessEnvironment = map[string]struct{}{
	"STRIDE_RUN_ID":      {},
	"STRIDE_WORKFLOW_ID": {},
}

var requiredPassedControls = []string{
	"ephemeral_read_only_sandbox_admission",
	"production_mount_and_secret_denylist",
	"synthetic_default_deny_gateway",
	"run_bound_short_lived_scoped_credential",
	"per_workflow_call_and_byte_quota",
	"signed_run_audience_nonce_callback",
	"callback_fencing_and_idempotency",
	"run_workflow_global_kill_switch_revocation",
	"body_and_secret_free_audit_receipt",
}

var requiredClaimsExcluded = []string{
	"external orchestrator installed",
	"container runtime isolation enforced",
	"real network egress controlled",
	"real credential broker installed",
	"infrastructure resource quotas enforced",
	"production callback store installed",
	"provider qualified",
	"production worker enabled",
}

var requiredExternalPending = []string{
	"external_per_run_orchestrator_identity",
	"ephemeral_container_lifecycle_receipt",
	"read_only_root_and_mount_receipt",
	"default_deny_egress_gateway_receipt",
	"short_lived_run_bound_credential_receipt",
	"resource_and_network_quota_receipt",
	"callback_nonce_replay_receipt",
	"no_production_mount_receipt",
}

// SandboxSpec is the only worker environment the harness will admit. EnvKeys
// are names, never values, so an audit receipt cannot capture a secret.
type SandboxSpec struct {
	ReadOnlyRoot   bool     `json:"readOnlyRoot"`
	WritableMounts []string `json:"writableMounts"`
	MountedPaths   []string `json:"mountedPaths"`
	EnvKeys        []string `json:"envKeys"`
	NetworkMode    string   `json:"networkMode"`
	Ephemeral      bool     `json:"ephemeral"`
}

// WorkflowPolicy defines one allowed workflow and its logical gateway budget.
// Infrastructure CPU/memory/PID enforcement remains an external gate.
type WorkflowPolicy struct {
	ID               string
	AllowedScopes    []string
	AllowedHosts     []string
	MaximumCalls     uint64
	MaximumBytes     uint64
	CredentialTTL    time.Duration
	CallbackAudience string
}

type RunRequest struct {
	RunID      string
	WorkflowID string
	Sandbox    SandboxSpec
}

type RunLease struct {
	RunID        string
	WorkflowID   string
	Generation   uint64
	FencingToken string
	IssuedAt     time.Time
}

type Credential struct {
	ID         string    `json:"id"`
	RunID      string    `json:"runId"`
	WorkflowID string    `json:"workflowId"`
	Audience   string    `json:"audience"`
	Scopes     []string  `json:"scopes"`
	Generation uint64    `json:"generation"`
	IssuedAt   time.Time `json:"issuedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Signature  string    `json:"signature"`
}

type GatewayRequest struct {
	RunID       string
	WorkflowID  string
	Credential  Credential
	Scope       string
	Target      string
	Bytes       uint64
	RedirectHop bool
}

type Callback struct {
	RunID          string    `json:"runId"`
	WorkflowID     string    `json:"workflowId"`
	Audience       string    `json:"audience"`
	Nonce          string    `json:"nonce"`
	IdempotencyKey string    `json:"idempotencyKey"`
	Generation     uint64    `json:"generation"`
	FencingToken   string    `json:"fencingToken"`
	Status         string    `json:"status"`
	ResultDigest   string    `json:"resultDigest"`
	OccurredAt     time.Time `json:"occurredAt"`
	Signature      string    `json:"signature"`
}

type CommitResult struct {
	Applied   bool `json:"applied"`
	Duplicate bool `json:"duplicate"`
}

// Receipt is deliberately body-, token-, host-address-, and credential-free.
type Receipt struct {
	SchemaVersion      string   `json:"schemaVersion"`
	HarnessID          string   `json:"harnessId"`
	EvidenceClass      string   `json:"evidenceClass"`
	State              string   `json:"state"`
	Synthetic          bool     `json:"synthetic"`
	ProviderCalls      bool     `json:"providerCalls"`
	NetworkConnections int      `json:"networkConnections"`
	ProductionMutation bool     `json:"productionMutation"`
	ProductionReady    bool     `json:"productionReady"`
	ActivationDefault  string   `json:"activationDefault"`
	PolicyDigest       string   `json:"policyDigest"`
	RunBindingDigest   string   `json:"runBindingDigest"`
	ReceiptDigest      string   `json:"receiptDigest"`
	PassedControls     []string `json:"passedControls"`
	ClaimsExcluded     []string `json:"claimsExcluded"`
	ExternalPending    []string `json:"externalPending"`
}

type runState struct {
	lease           RunLease
	policy          WorkflowPolicy
	calls           uint64
	bytes           uint64
	killed          bool
	nonces          map[string]struct{}
	terminalKey     string
	terminalBinding string
}

// Controller is an in-memory contract implementation. Its mutex makes quota,
// kill, replay, and idempotency decisions one authority boundary.
type Controller struct {
	mu        sync.Mutex
	key       []byte
	policy    e9readiness.WorkerIsolationPolicy
	workflows map[string]WorkflowPolicy
	runs      map[string]*runState
	disabled  map[string]bool
	killAll   bool
	now       func() time.Time
}

func NewController(policy e9readiness.WorkerIsolationPolicy, workflows []WorkflowPolicy, now func() time.Time) (*Controller, error) {
	if err := e9readiness.ValidateWorkerIsolation(policy); err != nil {
		return nil, fmt.Errorf("worker isolation policy: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("create ephemeral harness key: %w", err)
	}
	c := &Controller{key: key, policy: policy, workflows: map[string]WorkflowPolicy{}, runs: map[string]*runState{}, disabled: map[string]bool{}, now: now}
	for _, workflow := range workflows {
		workflow.ID = strings.TrimSpace(workflow.ID)
		workflow.CallbackAudience = strings.TrimSpace(workflow.CallbackAudience)
		if !safeID(workflow.ID) || workflow.CallbackAudience == "" || workflow.MaximumCalls == 0 || workflow.MaximumBytes == 0 {
			return nil, fmt.Errorf("invalid workflow policy %q", workflow.ID)
		}
		if err := validateSyntheticHost(workflow.CallbackAudience); err != nil {
			return nil, fmt.Errorf("workflow %s callback audience: %w", workflow.ID, err)
		}
		if workflow.MaximumBytes > uint64(policy.RequiredBoundary.Resources.NetworkBytes) {
			return nil, fmt.Errorf("workflow %s byte quota exceeds boundary policy", workflow.ID)
		}
		if workflow.CredentialTTL <= 0 || workflow.CredentialTTL > time.Duration(policy.RequiredBoundary.Credentials.MaximumTTLSecond)*time.Second {
			return nil, fmt.Errorf("workflow %s credential TTL exceeds policy", workflow.ID)
		}
		if !subset(workflow.AllowedScopes, policy.RequiredBoundary.Credentials.AllowedScopes) || len(workflow.AllowedScopes) == 0 {
			return nil, fmt.Errorf("workflow %s scopes exceed policy", workflow.ID)
		}
		if len(workflow.AllowedHosts) == 0 {
			return nil, fmt.Errorf("workflow %s must have a synthetic gateway allowlist", workflow.ID)
		}
		for _, host := range workflow.AllowedHosts {
			if err := validateSyntheticHost(host); err != nil {
				return nil, fmt.Errorf("workflow %s host: %w", workflow.ID, err)
			}
		}
		if contains(workflow.AllowedHosts, workflow.CallbackAudience) {
			return nil, fmt.Errorf("workflow %s callback audience must be separate from gateway hosts", workflow.ID)
		}
		if _, exists := c.workflows[workflow.ID]; exists {
			return nil, fmt.Errorf("duplicate workflow %q", workflow.ID)
		}
		workflow.AllowedScopes = canonicalStrings(workflow.AllowedScopes)
		workflow.AllowedHosts = canonicalStrings(workflow.AllowedHosts)
		c.workflows[workflow.ID] = workflow
	}
	if len(c.workflows) == 0 {
		return nil, errors.New("at least one workflow policy is required")
	}
	return c, nil
}

func (c *Controller) Admit(request RunRequest) (RunLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killAll {
		return RunLease{}, ErrKilled
	}
	if !safeID(request.RunID) || !safeID(request.WorkflowID) {
		return RunLease{}, fmt.Errorf("%w: invalid run binding", ErrDenied)
	}
	workflow, ok := c.workflows[request.WorkflowID]
	if !ok {
		return RunLease{}, fmt.Errorf("%w: workflow is not allowed", ErrDenied)
	}
	if c.disabled[request.WorkflowID] {
		return RunLease{}, ErrKilled
	}
	if err := validateSandbox(c.policy, request.Sandbox); err != nil {
		return RunLease{}, err
	}
	if _, exists := c.runs[request.RunID]; exists {
		return RunLease{}, fmt.Errorf("%w: run id already exists", ErrDenied)
	}
	lease := RunLease{
		RunID: request.RunID, WorkflowID: request.WorkflowID, Generation: 1,
		FencingToken: c.sign(joinFields("fence", request.RunID, request.WorkflowID, "1")), IssuedAt: c.now().UTC(),
	}
	c.runs[request.RunID] = &runState{lease: lease, policy: workflow, nonces: map[string]struct{}{}}
	return lease, nil
}

func validateSandbox(policy e9readiness.WorkerIsolationPolicy, sandbox SandboxSpec) error {
	if !sandbox.Ephemeral || !sandbox.ReadOnlyRoot || sandbox.NetworkMode != "synthetic_gateway_only" {
		return fmt.Errorf("%w: sandbox must be ephemeral, read-only-root, and synthetic-gateway-only", ErrDenied)
	}
	if !equalSet(sandbox.WritableMounts, policy.RequiredBoundary.WritableMounts) {
		return fmt.Errorf("%w: writable mount set differs from policy", ErrDenied)
	}
	for _, path := range append(append([]string{}, sandbox.MountedPaths...), sandbox.WritableMounts...) {
		clean := filepath.Clean(strings.TrimSpace(path))
		for _, forbidden := range forbiddenMountPrefixes {
			if clean == forbidden || strings.HasPrefix(clean, forbidden+string(filepath.Separator)) {
				return fmt.Errorf("%w: production mount is forbidden", ErrDenied)
			}
		}
		if !contains(policy.RequiredBoundary.WritableMounts, clean) {
			return fmt.Errorf("%w: unreviewed mount is forbidden", ErrDenied)
		}
	}
	for _, key := range sandbox.EnvKeys {
		key = strings.TrimSpace(key)
		if _, denied := forbiddenEnvironment[key]; denied {
			return fmt.Errorf("%w: secret environment key is forbidden", ErrDenied)
		}
		if _, allowed := allowedHarnessEnvironment[key]; !allowed {
			return fmt.Errorf("%w: unreviewed environment key is forbidden", ErrDenied)
		}
	}
	return nil
}

func (c *Controller) IssueCredential(lease RunLease, audience string, scopes []string) (Credential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.authorizeLeaseLocked(lease)
	if err != nil {
		return Credential{}, err
	}
	audience = strings.TrimSpace(audience)
	if (!contains(state.policy.AllowedHosts, audience) && audience != state.policy.CallbackAudience) || !subset(scopes, state.policy.AllowedScopes) || len(scopes) == 0 {
		return Credential{}, fmt.Errorf("%w: credential audience or scope", ErrDenied)
	}
	now := c.now().UTC()
	credential := Credential{
		ID: digestString(c.sign(joinFields("credential", lease.RunID, audience, strings.Join(canonicalStrings(scopes), ","), now.Format(time.RFC3339Nano))))[:32], RunID: lease.RunID, WorkflowID: lease.WorkflowID,
		Audience: audience, Scopes: canonicalStrings(scopes), Generation: lease.Generation,
		IssuedAt: now, ExpiresAt: now.Add(state.policy.CredentialTTL),
	}
	credential.Signature = c.sign(credentialSigningInput(credential))
	return credential, nil
}

func (c *Controller) AuthorizeGateway(request GatewayRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.runs[strings.TrimSpace(request.RunID)]
	if !ok || state.lease.WorkflowID != strings.TrimSpace(request.WorkflowID) {
		return fmt.Errorf("%w: unknown run", ErrDenied)
	}
	if c.killAll || state.killed {
		return ErrKilled
	}
	if request.RedirectHop {
		return fmt.Errorf("%w: redirects require a fresh gateway decision", ErrDenied)
	}
	host, err := gatewayHost(request.Target)
	if err != nil || !contains(state.policy.AllowedHosts, host) {
		return fmt.Errorf("%w: gateway target is not allowed", ErrDenied)
	}
	if err := c.verifyCredentialLocked(state, request.Credential, request.Scope); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(request.Credential.Audience), []byte(host)) != 1 {
		return fmt.Errorf("%w: credential audience does not match gateway target", ErrDenied)
	}
	if state.calls >= state.policy.MaximumCalls || request.Bytes > state.policy.MaximumBytes-state.bytes {
		return ErrQuota
	}
	state.calls++
	state.bytes += request.Bytes
	return nil
}

func (c *Controller) verifyCredentialLocked(state *runState, credential Credential, scope string) error {
	if subtle.ConstantTimeCompare([]byte(credential.Signature), []byte(c.sign(credentialSigningInput(credential)))) != 1 {
		return ErrInvalidSignature
	}
	if credential.RunID != state.lease.RunID || credential.WorkflowID != state.lease.WorkflowID || credential.Generation != state.lease.Generation {
		return fmt.Errorf("%w: credential binding", ErrDenied)
	}
	now := c.now().UTC()
	if now.Before(credential.IssuedAt) || !now.Before(credential.ExpiresAt) || credential.ExpiresAt.Sub(credential.IssuedAt) > state.policy.CredentialTTL {
		return fmt.Errorf("%w: credential expired or invalid", ErrDenied)
	}
	if !contains(credential.Scopes, strings.TrimSpace(scope)) || !contains(state.policy.AllowedScopes, strings.TrimSpace(scope)) {
		return fmt.Errorf("%w: scope", ErrDenied)
	}
	return nil
}

func (c *Controller) NewCallback(lease RunLease, credential Credential, nonce, idempotencyKey, status, resultDigest string, occurredAt time.Time) (Callback, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.authorizeLeaseLocked(lease)
	if err != nil {
		return Callback{}, err
	}
	if err := c.verifyCredentialLocked(state, credential, "callback:complete"); err != nil {
		return Callback{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential.Audience), []byte(state.policy.CallbackAudience)) != 1 {
		return Callback{}, fmt.Errorf("%w: callback credential audience", ErrDenied)
	}
	if !safeID(nonce) || !safeID(idempotencyKey) || !validDigest(resultDigest) || !validStatus(status) {
		return Callback{}, fmt.Errorf("%w: invalid callback fields", ErrDenied)
	}
	callback := Callback{
		RunID: lease.RunID, WorkflowID: lease.WorkflowID, Audience: state.policy.CallbackAudience,
		Nonce: nonce, IdempotencyKey: idempotencyKey, Generation: lease.Generation,
		FencingToken: lease.FencingToken, Status: status, ResultDigest: resultDigest, OccurredAt: occurredAt.UTC(),
	}
	callback.Signature = c.sign(callbackSigningInput(callback))
	return callback, nil
}

func (c *Controller) CommitCallback(callback Callback) (CommitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.runs[strings.TrimSpace(callback.RunID)]
	if !ok || state.lease.WorkflowID != callback.WorkflowID {
		return CommitResult{}, fmt.Errorf("%w: callback run", ErrDenied)
	}
	if c.killAll || state.killed {
		return CommitResult{}, ErrKilled
	}
	if callback.Generation != state.lease.Generation || subtle.ConstantTimeCompare([]byte(callback.FencingToken), []byte(state.lease.FencingToken)) != 1 {
		return CommitResult{}, ErrStaleFence
	}
	if callback.Audience != state.policy.CallbackAudience {
		return CommitResult{}, fmt.Errorf("%w: callback audience", ErrDenied)
	}
	if subtle.ConstantTimeCompare([]byte(callback.Signature), []byte(c.sign(callbackSigningInput(callback)))) != 1 {
		return CommitResult{}, ErrInvalidSignature
	}
	now := c.now().UTC()
	maxSkew := time.Duration(c.policy.RequiredBoundary.Callback.MaximumSkewSeconds) * time.Second
	if callback.OccurredAt.Before(now.Add(-maxSkew)) || callback.OccurredAt.After(now.Add(maxSkew)) {
		return CommitResult{}, fmt.Errorf("%w: callback timestamp", ErrDenied)
	}
	if _, used := state.nonces[callback.Nonce]; used {
		return CommitResult{}, ErrReplay
	}
	binding := callbackBindingDigest(callback)
	if state.terminalBinding != "" {
		if state.terminalKey != callback.IdempotencyKey || subtle.ConstantTimeCompare([]byte(state.terminalBinding), []byte(binding)) != 1 {
			return CommitResult{}, ErrIdempotency
		}
		if len(state.nonces) >= maxCallbackNoncesPerRun {
			return CommitResult{}, ErrQuota
		}
		state.nonces[callback.Nonce] = struct{}{}
		return CommitResult{Duplicate: true}, nil
	}
	if len(state.nonces) >= maxCallbackNoncesPerRun {
		return CommitResult{}, ErrQuota
	}
	state.nonces[callback.Nonce] = struct{}{}
	state.terminalKey = callback.IdempotencyKey
	state.terminalBinding = binding
	return CommitResult{Applied: true}, nil
}

func (c *Controller) KillRun(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state := c.runs[strings.TrimSpace(runID)]; state != nil {
		state.killed = true
		state.lease.Generation++
		state.lease.FencingToken = c.sign(joinFields("fence", state.lease.RunID, state.lease.WorkflowID, fmt.Sprint(state.lease.Generation)))
	}
}

func (c *Controller) KillWorkflow(workflowID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	workflowID = strings.TrimSpace(workflowID)
	c.disabled[workflowID] = true
	for _, state := range c.runs {
		if state.lease.WorkflowID == workflowID {
			state.killed = true
			state.lease.Generation++
			state.lease.FencingToken = c.sign(joinFields("fence", state.lease.RunID, state.lease.WorkflowID, fmt.Sprint(state.lease.Generation)))
		}
	}
}

func (c *Controller) KillAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killAll = true
}

func (c *Controller) authorizeLeaseLocked(lease RunLease) (*runState, error) {
	state, ok := c.runs[strings.TrimSpace(lease.RunID)]
	if !ok || state.lease.WorkflowID != lease.WorkflowID {
		return nil, fmt.Errorf("%w: unknown run", ErrDenied)
	}
	if c.killAll || state.killed {
		return nil, ErrKilled
	}
	if state.lease.Generation != lease.Generation || subtle.ConstantTimeCompare([]byte(state.lease.FencingToken), []byte(lease.FencingToken)) != 1 {
		return nil, ErrStaleFence
	}
	return state, nil
}

// ExecuteHarness runs one synthetic workflow through every local contract.
func ExecuteHarness(policy e9readiness.WorkerIsolationPolicy) (Receipt, error) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	workflow := WorkflowPolicy{
		ID: "insights_opportunities_v1", AllowedScopes: []string{"source:read", "artifact:write", "callback:complete"},
		AllowedHosts: []string{"source.test.invalid", "artifact.test.invalid"}, MaximumCalls: 4, MaximumBytes: 8192,
		CredentialTTL: 5 * time.Minute, CallbackAudience: "stride-control.test.invalid",
	}
	controller, err := NewController(policy, []WorkflowPolicy{workflow}, func() time.Time { return now })
	if err != nil {
		return Receipt{}, err
	}
	sandbox := SandboxSpec{
		ReadOnlyRoot: true, WritableMounts: []string{"/tmp", "/workspace/run"}, MountedPaths: []string{"/workspace/run"},
		EnvKeys: []string{"STRIDE_RUN_ID", "STRIDE_WORKFLOW_ID"}, NetworkMode: "synthetic_gateway_only", Ephemeral: true,
	}
	lease, err := controller.Admit(RunRequest{RunID: "harness-run-1", WorkflowID: workflow.ID, Sandbox: sandbox})
	if err != nil {
		return Receipt{}, err
	}
	credential, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"})
	if err != nil {
		return Receipt{}, err
	}
	if err := controller.AuthorizeGateway(GatewayRequest{RunID: lease.RunID, WorkflowID: workflow.ID, Credential: credential, Scope: "source:read", Target: "https://source.test.invalid/fixture/input", Bytes: 2048}); err != nil {
		return Receipt{}, err
	}
	if err := controller.AuthorizeGateway(GatewayRequest{RunID: lease.RunID, WorkflowID: workflow.ID, Credential: credential, Scope: "source:read", Target: "http://169.254.169.254/latest/meta-data", Bytes: 1}); !errors.Is(err, ErrDenied) {
		return Receipt{}, errors.New("metadata target did not fail closed")
	}
	resultDigest := digestString("synthetic token-free result")
	callbackCredential, err := controller.IssueCredential(lease, workflow.CallbackAudience, []string{"callback:complete"})
	if err != nil {
		return Receipt{}, err
	}
	callback, err := controller.NewCallback(lease, callbackCredential, "nonce-1", "complete-1", "complete", resultDigest, now)
	if err != nil {
		return Receipt{}, err
	}
	committed, err := controller.CommitCallback(callback)
	if err != nil || !committed.Applied {
		return Receipt{}, fmt.Errorf("commit callback: %w", err)
	}
	retry, err := controller.NewCallback(lease, callbackCredential, "nonce-2", "complete-1", "complete", resultDigest, now)
	if err != nil {
		return Receipt{}, err
	}
	duplicate, err := controller.CommitCallback(retry)
	if err != nil || !duplicate.Duplicate {
		return Receipt{}, fmt.Errorf("idempotent callback retry: %w", err)
	}
	controller.KillRun(lease.RunID)
	if _, err := controller.IssueCredential(lease, "source.test.invalid", []string{"source:read"}); !errors.Is(err, ErrKilled) {
		return Receipt{}, errors.New("kill switch did not revoke credential issuance")
	}
	policyBytes, _ := json.Marshal(policy)
	receipt := Receipt{
		SchemaVersion: ReceiptSchema, HarnessID: "e10-worker-boundary-harness-v1", EvidenceClass: EvidenceClass,
		State: "local_contract_passed", Synthetic: true, ProviderCalls: false, NetworkConnections: 0,
		ProductionMutation: false, ProductionReady: false, ActivationDefault: "off",
		PolicyDigest: digestBytes(policyBytes), RunBindingDigest: digestString(lease.RunID + "\x00" + lease.WorkflowID + "\x00" + fmt.Sprint(lease.Generation)),
		PassedControls:  append([]string(nil), requiredPassedControls...),
		ClaimsExcluded:  append([]string(nil), requiredClaimsExcluded...),
		ExternalPending: append([]string(nil), policy.ExternalEvidenceRequired...),
	}
	sort.Strings(receipt.PassedControls)
	receipt.ReceiptDigest = receiptDigest(receipt)
	if err := ValidateReceipt(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt) error {
	if receipt.SchemaVersion != ReceiptSchema || receipt.EvidenceClass != EvidenceClass || receipt.State != "local_contract_passed" {
		return errors.New("worker harness receipt identity is invalid")
	}
	if !receipt.Synthetic || receipt.ProviderCalls || receipt.NetworkConnections != 0 || receipt.ProductionMutation || receipt.ProductionReady || receipt.ActivationDefault != "off" {
		return errors.New("worker harness receipt crossed its evidence boundary")
	}
	if !validDigest(receipt.PolicyDigest) || !validDigest(receipt.RunBindingDigest) ||
		!equalSet(receipt.PassedControls, requiredPassedControls) ||
		!equalSet(receipt.ClaimsExcluded, requiredClaimsExcluded) ||
		!equalSet(receipt.ExternalPending, requiredExternalPending) {
		return errors.New("worker harness receipt is incomplete")
	}
	if subtle.ConstantTimeCompare([]byte(receipt.ReceiptDigest), []byte(receiptDigest(receipt))) != 1 {
		return errors.New("worker harness receipt digest mismatch")
	}
	return nil
}

func receiptDigest(receipt Receipt) string {
	receipt.ReceiptDigest = ""
	raw, _ := json.Marshal(receipt)
	return digestBytes(raw)
}

func credentialSigningInput(credential Credential) string {
	return joinFields(credential.ID, credential.RunID, credential.WorkflowID, credential.Audience, strings.Join(canonicalStrings(credential.Scopes), ","), fmt.Sprint(credential.Generation), credential.IssuedAt.UTC().Format(time.RFC3339Nano), credential.ExpiresAt.UTC().Format(time.RFC3339Nano))
}

func callbackSigningInput(callback Callback) string {
	return joinFields(callback.RunID, callback.WorkflowID, callback.Audience, callback.Nonce, callback.IdempotencyKey, fmt.Sprint(callback.Generation), callback.FencingToken, callback.Status, callback.ResultDigest, callback.OccurredAt.UTC().Format(time.RFC3339Nano))
}

func callbackBindingDigest(callback Callback) string {
	return digestString(joinFields(callback.RunID, callback.WorkflowID, callback.Audience, callback.IdempotencyKey, fmt.Sprint(callback.Generation), callback.FencingToken, callback.Status, callback.ResultDigest))
}

func (c *Controller) sign(value string) string {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func gatewayHost(target string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return "", errors.New("invalid gateway target")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" || net.ParseIP(host) != nil || isPrivateOrMetadataName(host) {
		return "", errors.New("unsafe gateway target")
	}
	if err := validateSyntheticHost(host); err != nil {
		return "", err
	}
	return host, nil
}

func validateSyntheticHost(host string) error {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if net.ParseIP(host) != nil || !strings.HasSuffix(host, ".test.invalid") || isPrivateOrMetadataName(host) {
		return errors.New("harness hosts must be non-address synthetic .test.invalid names")
	}
	return nil
}

func isPrivateOrMetadataName(host string) bool {
	host = strings.ToLower(host)
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local")
}

func safeID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && !strings.ContainsRune("._-", char) {
			return false
		}
	}
	return true
}

func validStatus(status string) bool {
	return status == "complete" || status == "failed" || status == "cancelled"
}
func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func digestBytes(value []byte) string  { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func digestString(value string) string { return digestBytes([]byte(value)) }
func joinFields(values ...string) string {
	var b strings.Builder
	for _, value := range values {
		fmt.Fprintf(&b, "%d:%s", len(value), value)
	}
	return b.String()
}
func canonicalStrings(values []string) []string {
	result := append([]string(nil), values...)
	for i := range result {
		result[i] = strings.TrimSpace(result[i])
	}
	sort.Strings(result)
	return result
}
func subset(values, allowed []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !contains(allowed, strings.TrimSpace(value)) {
			return false
		}
	}
	return true
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
func equalSet(left, right []string) bool {
	left = canonicalStrings(left)
	right = canonicalStrings(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
