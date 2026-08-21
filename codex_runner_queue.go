package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	codexJobAuthorityReadOnly       = "read_only"
	codexJobAuthorityWorkspaceWrite = "workspace_write"
	codexJobAuthorityExternalWrite  = "external_write"

	codexJobStatusQueued           = "queued"
	codexJobStatusRunning          = "running"
	codexJobStatusComplete         = "complete"
	codexJobStatusFailed           = "failed"
	codexJobStatusApprovalRequired = "approval_required"
	codexJobStatusCancelled        = "cancelled"

	defaultCodexRunnerPollInterval = 2 * time.Second
	defaultCodexRunnerStaleAfter   = 2 * time.Minute
	defaultCodexRunnerLease        = 30 * time.Second
)

var errCodexRunnerClaimLost = errors.New("Codex runner claim ownership was lost")
var writeCodexRunnerClaimAtomically = writeJSONFileAtomically

type codexRunnerJob struct {
	ID         string `json:"id"`
	ArtifactID string `json:"artifact_id"`
	ThreadID   string `json:"thread_id"`
	Mode       string `json:"mode"`
	Query      string `json:"query"`
	Prompt     string `json:"prompt"`
	// ThreadMetadata is populated for source-bound jobs. Their provider prompt
	// is deliberately NOT serialized at enqueue because it contains File body
	// context; the sidecar reauthorizes these refs and builds the prompt only
	// after claiming the job. Legacy/source-free jobs keep Prompt for backwards
	// compatibility.
	ThreadMetadata  map[string]string                 `json:"thread_metadata,omitempty"`
	Authority       string                            `json:"authority"`
	Status          string                            `json:"status"`
	CreatedAt       time.Time                         `json:"created_at"`
	StartedAt       time.Time                         `json:"started_at,omitempty"`
	CompletedAt     time.Time                         `json:"completed_at,omitempty"`
	Attempts        int                               `json:"attempts"`
	RunnerID        string                            `json:"runner_id,omitempty"`
	ClaimGeneration uint64                            `json:"claim_generation,omitempty"`
	FencingToken    string                            `json:"fencing_token,omitempty"`
	LeaseExpiresAt  time.Time                         `json:"lease_expires_at,omitempty"`
	HeartbeatAt     time.Time                         `json:"heartbeat_at,omitempty"`
	Error           string                            `json:"error,omitempty"`
	RunnerEvidence  string                            `json:"runner_evidence,omitempty"`
	ResultText      string                            `json:"result_text,omitempty"`
	Metadata        map[string]string                 `json:"metadata,omitempty"`
	TenantAuthority *StrideE10TenantAuthorityEnvelope `json:"tenant_authority,omitempty"`
}

type codexRunnerJobStore struct {
	dir string
}

type codexRunnerCallbackPayload struct {
	JobID           string            `json:"job_id"`
	ArtifactID      string            `json:"artifact_id"`
	ThreadID        string            `json:"thread_id,omitempty"`
	Status          string            `json:"status"`
	Text            string            `json:"text,omitempty"`
	Error           string            `json:"error,omitempty"`
	RunnerEvidence  string            `json:"runner_evidence,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Capability      string            `json:"capability"`
	ClaimGeneration uint64            `json:"claim_generation,omitempty"`
	FencingToken    string            `json:"fencing_token,omitempty"`
}

type strideE10CodexRunnerCallbackAuthorityContextKey struct{}

func codexRunnerQueuePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_QUEUE_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "codex-runner-jobs")
}

func codexRunnerHeartbeatPath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_HEARTBEAT_PATH")); path != "" {
		return filepath.Clean(path)
	}
	return filepath.Join(filepath.Dir(codexRunnerQueuePath()), "codex-runner-heartbeat.json")
}

func codexRunnerPollInterval() time.Duration {
	return durationEnv("BONFIRE_CODEX_RUNNER_POLL_INTERVAL", defaultCodexRunnerPollInterval, 250*time.Millisecond)
}

func codexRunnerLeaseDuration() time.Duration {
	return durationEnv("BONFIRE_CODEX_RUNNER_LEASE_DURATION", defaultCodexRunnerLease, time.Second)
}

func newCodexRunnerJobStore(dir string) *codexRunnerJobStore {
	return &codexRunnerJobStore{dir: filepath.Clean(strings.TrimSpace(dir))}
}

func (store *codexRunnerJobStore) enqueue(job codexRunnerJob) (codexRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return codexRunnerJob{}, fmt.Errorf("Codex runner queue path is not configured")
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = newCodexRunnerJobID()
	}
	job.ID = strings.TrimSpace(job.ID)
	if job.ID == "." || job.ID == ".." || filepath.Base(job.ID) != job.ID || strings.ContainsAny(job.ID, `/\\`) {
		return codexRunnerJob{}, fmt.Errorf("invalid Codex runner job id %q", job.ID)
	}
	if strings.TrimSpace(job.Status) == "" {
		job.Status = codexJobStatusQueued
	}
	if !validCodexJobStatus(job.Status) || job.Status != codexJobStatusQueued {
		return codexRunnerJob{}, fmt.Errorf("new Codex runner job must have queued status")
	}
	if strings.TrimSpace(job.ArtifactID) == "" || strings.TrimSpace(job.ThreadID) == "" {
		return codexRunnerJob{}, fmt.Errorf("Codex runner artifact_id and thread_id are required")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.Authority = normalizeCodexJobAuthority(job.Authority)
	if job.Metadata == nil {
		job.Metadata = map[string]string{}
	}
	if job.TenantAuthority != nil && !strideE10TenantCutoverEnabled() {
		return codexRunnerJob{}, ErrStrideE10TenantAuthorityStale
	}
	if strideE10TenantCutoverEnabled() {
		if job.TenantAuthority == nil || validateStrideE10TenantAuthorityEnvelope(context.Background(), *job.TenantAuthority, time.Now().UTC()) != nil {
			return codexRunnerJob{}, ErrStrideE10TenantAuthorityStale
		}
		if job.TenantAuthority.Purpose != StrideE10TenantAuthorityPurposeForCodexJob(job.ArtifactID, job.ThreadID, job.Mode, job.Query, job.Authority) {
			return codexRunnerJob{}, ErrStrideE10TenantAuthorityStale
		}
		if raw, err := json.Marshal(job); err != nil || strideE10TenantEnvelopeContainsPrivateAuthority(raw) || strideE10CodexJobContainsLegacyAuthority(job) {
			return codexRunnerJob{}, ErrStrideE10TenantAuthorityInvalid
		}
	}

	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return codexRunnerJob{}, fmt.Errorf("create Codex runner queue: %w", err)
	}
	var result codexRunnerJob
	err := store.withQueueLock(func() error {
		// An explicitly reserved job ID is a durable outbox key. Retrying the same
		// binding returns the existing job in-place (including running/terminal
		// status) and must never overwrite it back to queued. Reusing an ID for a
		// different artifact/thread/action fails closed.
		if existing, readErr := store.read(filepath.Base(store.jobPath(job.ID))); readErr == nil {
			if !sameCodexRunnerJobBinding(*existing, job) {
				return fmt.Errorf("Codex runner job id %q is already bound to another action", job.ID)
			}
			result = *existing
			return nil
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if writeErr := writeJSONFileAtomically(store.jobPath(job.ID), "Codex runner job", job); writeErr != nil {
			return writeErr
		}
		result = job
		return nil
	})
	if err != nil {
		return codexRunnerJob{}, err
	}
	return result, nil
}

func strideE10CodexJobContainsLegacyAuthority(job codexRunnerJob) bool {
	for key := range job.ThreadMetadata {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "requestedby", "createdby", "owneremail", "useremail", "sessiontoken", "authorization":
			return true
		}
	}
	return false
}

func (store *codexRunnerJobStore) withQueueLock(fn func() error) (resultErr error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return fmt.Errorf("Codex runner queue path is not configured")
	}
	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return fmt.Errorf("create Codex runner queue: %w", err)
	}
	// Keep the durable lock adjacent to the queue rather than inside it. Queue
	// tooling and legacy tests correctly treat every directory entry as a job;
	// an out-of-band lock preserves that contract while still naming one lock
	// per absolute queue path.
	lock, err := os.OpenFile(store.dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open Codex runner queue lock: %w", err)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil && resultErr == nil {
			resultErr = fmt.Errorf("close Codex runner queue lock after mutation: %w", closeErr)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock Codex runner queue: %w", err)
	}
	defer func() {
		if unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); unlockErr != nil && resultErr == nil {
			// The caller must treat an unlock failure as ambiguous and never begin
			// work from a claim returned alongside it. A persisted lease will expire
			// and recover safely even if this process cannot prove lock release.
			resultErr = fmt.Errorf("Codex runner queue mutation is ambiguous: unlock failed: %w", unlockErr)
		}
	}()
	return fn()
}

func sameCodexRunnerJobBinding(existing codexRunnerJob, reserved codexRunnerJob) bool {
	return strings.TrimSpace(existing.ID) == strings.TrimSpace(reserved.ID) &&
		sameCodexRunnerActionBinding(existing, reserved)
}

func sameCodexRunnerActionBinding(existing codexRunnerJob, reserved codexRunnerJob) bool {
	return strings.TrimSpace(existing.ArtifactID) == strings.TrimSpace(reserved.ArtifactID) &&
		strings.TrimSpace(existing.ThreadID) == strings.TrimSpace(reserved.ThreadID) &&
		strings.TrimSpace(existing.Mode) == strings.TrimSpace(reserved.Mode) &&
		strings.TrimSpace(existing.Query) == strings.TrimSpace(reserved.Query) &&
		normalizeCodexJobAuthority(existing.Authority) == normalizeCodexJobAuthority(reserved.Authority) &&
		strideE10TenantEnvelopeBindingEqual(existing.TenantAuthority, reserved.TenantAuthority)
}

// findByActionBinding discovers a legacy random-ID job from the immutable
// action tuple that was durable in the queue even when the old code crashed
// before stamping runnerJobId back onto its child artifact.
func (store *codexRunnerJobStore) findByActionBinding(binding codexRunnerJob) ([]codexRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return nil, fmt.Errorf("Codex runner queue path is not configured")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Codex runner queue: %w", err)
	}
	matches := make([]codexRunnerJob, 0, 1)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := store.read(entry.Name())
		if err != nil {
			return nil, err
		}
		if sameCodexRunnerActionBinding(*job, binding) {
			matches = append(matches, *job)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	return matches, nil
}

func (store *codexRunnerJobStore) claimNext(runnerID string) (*codexRunnerJob, error) {
	return store.claimNextAt(runnerID, time.Now().UTC(), codexRunnerLeaseDuration())
}

func (store *codexRunnerJobStore) claimNextAt(runnerID string, now time.Time, leaseDuration time.Duration) (*codexRunnerJob, error) {
	if store == nil || strings.TrimSpace(store.dir) == "" {
		return nil, fmt.Errorf("Codex runner queue path is not configured")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil, fmt.Errorf("Codex runner id is required")
	}
	if leaseDuration <= 0 {
		return nil, fmt.Errorf("Codex runner lease duration must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	var claimed *codexRunnerJob
	err := store.withQueueLock(func() error {
		entries, readDirErr := os.ReadDir(store.dir)
		if readDirErr != nil {
			if os.IsNotExist(readDirErr) {
				return nil
			}
			return fmt.Errorf("read Codex runner queue: %w", readDirErr)
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			job, readErr := store.read(entry.Name())
			if readErr != nil {
				// A malformed or unreadable queue entry makes ordering ambiguous. Stop
				// instead of skipping it and executing later work out of order.
				return readErr
			}
			recovered := false
			switch job.Status {
			case codexJobStatusQueued:
			case codexJobStatusRunning:
				if !codexRunnerLeaseExpired(*job, now, leaseDuration) {
					continue
				}
				recovered = true
			default:
				continue
			}
			if job.TenantAuthority != nil && !strideE10TenantCutoverEnabled() {
				job.Status, job.CompletedAt, job.Error = codexJobStatusFailed, now, ErrStrideE10TenantAuthorityStale.Error()
				job.Metadata = mergeStringMaps(job.Metadata, map[string]string{"status": "error", "threadStatus": "error", "goalStatus": "needs_attention", "currentStage": "tenant_authority_quarantine", "progressPercent": "0", "reviewGate": "blocked", "completedAt": now.Format(time.RFC3339Nano), "error": ErrStrideE10TenantAuthorityStale.Error()})
				return writeCodexRunnerClaimAtomically(store.jobPath(job.ID), "Codex runner quarantined job", *job)
			}
			persistClaim := func() error {
				fencingToken, tokenErr := newCodexRunnerFencingToken()
				if tokenErr != nil {
					return tokenErr
				}
				job.Status = codexJobStatusRunning
				job.StartedAt = now
				job.Attempts++
				job.RunnerID = runnerID
				job.ClaimGeneration++
				job.FencingToken = fencingToken
				job.HeartbeatAt = now
				job.LeaseExpiresAt = now.Add(leaseDuration)
				job.CompletedAt = time.Time{}
				job.Error = ""
				job.RunnerEvidence = ""
				if job.Metadata == nil {
					job.Metadata = map[string]string{}
				}
				job.Metadata["claimedAt"] = now.Format(time.RFC3339Nano)
				job.Metadata["heartbeatAt"] = now.Format(time.RFC3339Nano)
				job.Metadata["leaseExpiresAt"] = job.LeaseExpiresAt.Format(time.RFC3339Nano)
				job.Metadata["runnerId"] = runnerID
				job.Metadata["claimGeneration"] = strconv.FormatUint(job.ClaimGeneration, 10)
				if recovered {
					job.Metadata["recoveredExpiredClaimAt"] = now.Format(time.RFC3339Nano)
				}
				if writeErr := writeCodexRunnerClaimAtomically(store.jobPath(job.ID), "Codex runner job", *job); writeErr != nil {
					return writeErr
				}
				copy := *job
				claimed = &copy
				return nil
			}
			if strideE10TenantCutoverEnabled() {
				authErr := withStrideE10TenantEnvelopeAuthority(context.Background(), job.TenantAuthority, StrideE10TenantSurfaceWorkQueue, now, func(StrideE10TenantPrincipal) error { return persistClaim() })
				if authErr == nil {
					return nil
				}
				job.Status, job.CompletedAt, job.Error = codexJobStatusFailed, now, ErrStrideE10TenantAuthorityStale.Error()
				job.RunnerID, job.FencingToken, job.LeaseExpiresAt = "", "", time.Time{}
				job.Metadata = mergeStringMaps(job.Metadata, map[string]string{"status": "error", "threadStatus": "error", "goalStatus": "needs_attention", "currentStage": "tenant_authority_quarantine", "progressPercent": "0", "reviewGate": "blocked", "completedAt": now.Format(time.RFC3339Nano), "error": ErrStrideE10TenantAuthorityStale.Error()})
				return writeCodexRunnerClaimAtomically(store.jobPath(job.ID), "Codex runner quarantined job", *job)
			}
			return persistClaim()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (store *codexRunnerJobStore) update(job codexRunnerJob) error {
	return store.updateAt(job, time.Now().UTC())
}

func (store *codexRunnerJobStore) updateAt(job codexRunnerJob, now time.Time) error {
	if strings.TrimSpace(job.ID) == "" {
		return fmt.Errorf("Codex runner job id is required")
	}
	if !validCodexJobStatus(job.Status) {
		return fmt.Errorf("invalid Codex runner job status %q", job.Status)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return store.withQueueLock(func() error {
		current, err := store.read(filepath.Base(store.jobPath(job.ID)))
		if err != nil {
			return err
		}
		if !codexJobStatusTransitionAllowed(current.Status, job.Status) {
			return fmt.Errorf("Codex runner job status cannot transition from %s to %s", current.Status, job.Status)
		}
		if current.Status == codexJobStatusRunning || job.Status == codexJobStatusRunning || codexJobStatusTerminal(job.Status) {
			if err := validateCodexRunnerClaimOwnership(*current, job, now.UTC(), current.Status == codexJobStatusRunning); err != nil {
				return err
			}
		}
		return writeJSONFileAtomically(store.jobPath(job.ID), "Codex runner job", job)
	})
}

func (store *codexRunnerJobStore) renewClaim(job codexRunnerJob) (*codexRunnerJob, error) {
	return store.renewClaimAt(job, time.Now().UTC(), codexRunnerLeaseDuration())
}

func (store *codexRunnerJobStore) renewClaimAt(job codexRunnerJob, now time.Time, leaseDuration time.Duration) (*codexRunnerJob, error) {
	if leaseDuration <= 0 {
		return nil, fmt.Errorf("Codex runner lease duration must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	var renewed *codexRunnerJob
	err := store.withQueueLock(func() error {
		current, readErr := store.read(filepath.Base(store.jobPath(job.ID)))
		if readErr != nil {
			return readErr
		}
		if current.Status != codexJobStatusRunning {
			return fmt.Errorf("%w: job %s is %s", errCodexRunnerClaimLost, job.ID, current.Status)
		}
		if ownershipErr := validateCodexRunnerClaimOwnership(*current, job, now, true); ownershipErr != nil {
			return ownershipErr
		}
		current.HeartbeatAt = now
		current.LeaseExpiresAt = now.Add(leaseDuration)
		if current.Metadata == nil {
			current.Metadata = map[string]string{}
		}
		current.Metadata["heartbeatAt"] = now.Format(time.RFC3339Nano)
		current.Metadata["leaseExpiresAt"] = current.LeaseExpiresAt.Format(time.RFC3339Nano)
		if writeErr := writeJSONFileAtomically(store.jobPath(current.ID), "Codex runner job", *current); writeErr != nil {
			return writeErr
		}
		copy := *current
		renewed = &copy
		return nil
	})
	if err != nil {
		return nil, err
	}
	return renewed, nil
}

func validateCodexRunnerClaimOwnership(current codexRunnerJob, proposed codexRunnerJob, now time.Time, requireUnexpired bool) error {
	if strings.TrimSpace(current.RunnerID) == "" || current.ClaimGeneration == 0 || strings.TrimSpace(current.FencingToken) == "" {
		return fmt.Errorf("%w: persisted job has no complete lease identity", errCodexRunnerClaimLost)
	}
	if strings.TrimSpace(proposed.RunnerID) != strings.TrimSpace(current.RunnerID) ||
		proposed.ClaimGeneration != current.ClaimGeneration ||
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(proposed.FencingToken)), []byte(strings.TrimSpace(current.FencingToken))) != 1 {
		return fmt.Errorf("%w: stale runner, generation, or fencing token for job %s", errCodexRunnerClaimLost, current.ID)
	}
	if requireUnexpired && (current.LeaseExpiresAt.IsZero() || !now.Before(current.LeaseExpiresAt)) {
		return fmt.Errorf("%w: lease expired for job %s", errCodexRunnerClaimLost, current.ID)
	}
	return nil
}

func codexRunnerLeaseExpired(job codexRunnerJob, now time.Time, fallbackLease time.Duration) bool {
	if !job.LeaseExpiresAt.IsZero() {
		return !now.Before(job.LeaseExpiresAt)
	}
	// Legacy running records predate explicit leases. Recover them from their
	// last durable activity instead of leaving them stuck forever.
	base := job.HeartbeatAt
	if base.IsZero() {
		base = job.StartedAt
	}
	if base.IsZero() {
		base = job.CreatedAt
	}
	return base.IsZero() || !now.Before(base.Add(fallbackLease))
}

func newCodexRunnerFencingToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("create Codex runner fencing token: %w", err)
	}
	return fmt.Sprintf("v1.%x", raw), nil
}

func validCodexJobStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case codexJobStatusQueued, codexJobStatusRunning, codexJobStatusComplete, codexJobStatusFailed, codexJobStatusApprovalRequired, codexJobStatusCancelled:
		return true
	default:
		return false
	}
}

func codexJobStatusTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case codexJobStatusComplete, codexJobStatusFailed, codexJobStatusApprovalRequired, codexJobStatusCancelled:
		return true
	default:
		return false
	}
}

func codexJobStatusTransitionAllowed(from, to string) bool {
	from, to = strings.ToLower(strings.TrimSpace(from)), strings.ToLower(strings.TrimSpace(to))
	if from == to {
		return true
	}
	if codexJobStatusTerminal(from) {
		return false
	}
	switch from {
	case codexJobStatusQueued:
		return to == codexJobStatusRunning || codexJobStatusTerminal(to)
	case codexJobStatusRunning:
		return codexJobStatusTerminal(to)
	default:
		return false
	}
}

func (store *codexRunnerJobStore) read(filename string) (*codexRunnerJob, error) {
	path := filepath.Join(store.dir, filepath.Base(filename))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Codex runner job: %w", err)
	}
	var job codexRunnerJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("decode Codex runner job %s: %w", filepath.Base(filename), err)
	}
	return &job, nil
}

func (store *codexRunnerJobStore) jobPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = newCodexRunnerJobID()
	}
	return filepath.Join(store.dir, id+".json")
}

func newCodexRunnerJobID() string {
	return fmt.Sprintf("codex-job-%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}

func normalizeCodexJobAuthority(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read-only", "readonly", "read_only":
		return codexJobAuthorityReadOnly
	case "external", "external-write", "external_write":
		return codexJobAuthorityExternalWrite
	default:
		return codexJobAuthorityWorkspaceWrite
	}
}

func codexJobAuthorityForThread(thread scoutAgentThread) string {
	mode := normalizeAgentThreadMode(thread.Mode)
	lower := strings.ToLower(strings.Join(strings.Fields(thread.Query), " "))
	if hasAssistantPhrase(lower, "commit", "push", "deploy", "ssh", "rsync", "docker compose", "send email", "email this", "call the api", "production mutation", "mutate production", "ship this live", "ship it live", "make this live", "release to production", "restart production", "run the migration", "run migration", "apply migration") {
		return codexJobAuthorityExternalWrite
	}
	if mode == "research" && !hasAssistantPhrase(lower, "edit", "implement", "write code", "change files", "test the app", "build the app") {
		return codexJobAuthorityReadOnly
	}
	if hasAssistantPhrase(lower, "audit", "research", "investigate", "report", "plan") && !hasAssistantPhrase(lower, "implement", "fix", "change", "write", "build") {
		return codexJobAuthorityReadOnly
	}
	return codexJobAuthorityWorkspaceWrite
}

func codexExecConfigForAuthority(cfg codexExecConfig, authority string, mode string) codexExecConfig {
	authority = normalizeCodexJobAuthority(authority)
	switch authority {
	case codexJobAuthorityReadOnly:
		cfg.Sandbox = "read-only"
	case codexJobAuthorityWorkspaceWrite:
		if cfg.Sandbox == "" || cfg.Sandbox == "read-only" {
			cfg.Sandbox = "workspace-write"
		}
	}
	if normalizeAgentThreadMode(mode) == "research" {
		cfg.Search = true
	}
	return cfg
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadArtifact(ctx context.Context, thread scoutAgentThread) (agentThreadWorkerResult, error) {
	if app == nil {
		return agentThreadWorkerResult{}, fmt.Errorf("assistant is unavailable")
	}
	providerContext, err := app.agentThreadProviderContext(ctx, thread)
	if err != nil {
		return agentThreadWorkerResult{}, err
	}
	job := app.newAgentJob(thread)
	job.Context = providerContext
	return app.enqueueCodexAgentThreadArtifactForJob(ctx, job)
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadArtifactForJob(_ context.Context, job AgentJob) (agentThreadWorkerResult, error) {
	thread := job.thread

	authority := codexJobAuthorityForThread(thread)
	// Wave-6 handoff: a /goal subtask child (goalParentId present) already had
	// its authority clamped by goalChildAuthority — never above workspace_write,
	// never out-privileging its parent. Re-deriving from the title text here
	// would ignore that clamp and, worse, could spuriously trip the approval
	// gate on a child whose title merely mentions "deploy". Honor the stamped,
	// already-clamped authority so the sidecar respects the engine's decision.
	if strings.TrimSpace(thread.Artifact.Metadata["goalParentId"]) != "" {
		if stamped := normalizeCodexJobAuthority(thread.Artifact.Metadata["authority"]); stamped == codexJobAuthorityReadOnly || stamped == codexJobAuthorityWorkspaceWrite {
			authority = stamped
		}
	}
	if authority == codexJobAuthorityExternalWrite {
		return codexApprovalRequiredResult(thread, authority), nil
	}

	reservedJobID := ""
	if operationKey := publicConversationProviderOperationKey(thread); operationKey != "" {
		reservedJobID = "codex-job-" + sha256Hex([]byte(operationKey))[:32]
	}
	return app.enqueueCodexAgentThreadJobWithContext(job, authority, reservedJobID)
}

func codexApprovalRequiredResult(thread scoutAgentThread, authority string) agentThreadWorkerResult {
	metadata := codexRunnerQueuedMetadata(thread, authority)
	metadata["workerBoundary"] = "codex_external_write_gate"
	metadata["status"] = codexJobStatusApprovalRequired
	metadata["threadStatus"] = codexJobStatusApprovalRequired
	metadata["goalStatus"] = "approval_required"
	metadata["currentStage"] = "gate_before_shipping"
	metadata["progressPercent"] = "68"
	metadata["reviewGate"] = "approval_required"
	metadata["codexRunner"] = "approval_required"
	// Card 069: a run that parks at the external-write gate IS heavy-lane work
	// regardless of its launch-time stamp — consumers read the artifact's
	// current stamp, never the launch-time one.
	metadata["approvalLane"] = approvalLaneHeavy
	return agentThreadWorkerResult{
		Text:     buildCodexApprovalRequiredArtifact(thread, authority),
		Metadata: metadata,
		Terminal: false,
	}
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadJob(thread scoutAgentThread, authority string) (agentThreadWorkerResult, error) {
	return app.enqueueCodexAgentThreadJobWithID(thread, authority, "")
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadJobWithID(thread scoutAgentThread, authority string, reservedJobID string) (agentThreadWorkerResult, error) {
	providerContext, err := app.agentThreadProviderContext(context.Background(), thread)
	if err != nil {
		return agentThreadWorkerResult{}, err
	}
	job := app.newAgentJob(thread)
	job.Context = providerContext
	return app.enqueueCodexAgentThreadJobWithContext(job, authority, reservedJobID)
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadJobWithContext(admittedJob AgentJob, authority string, reservedJobID string) (agentThreadWorkerResult, error) {
	if strideE10TenantCutoverEnabled() {
		threadEnvelope, err := strideE10ScoutThreadEnvelope(admittedJob.thread)
		if err != nil {
			return agentThreadWorkerResult{}, ErrStrideE10TenantAuthorityStale
		}
		purpose := StrideE10TenantAuthorityPurposeForCodexJob(admittedJob.thread.Artifact.ID, admittedJob.thread.ID, admittedJob.thread.Mode, admittedJob.thread.Query, authority)
		queueEnvelope, err := MintStrideE10TenantAuthorityEnvelope(context.Background(), currentStrideE10TenantRuntimeConverter(), threadEnvelope.SessionSubjectDigest, purpose, threadEnvelope.ExpiresAt)
		if err != nil {
			return agentThreadWorkerResult{}, ErrStrideE10TenantAuthorityStale
		}
		return app.enqueueCodexAgentThreadJobWithContextAndTenantAuthority(admittedJob, authority, reservedJobID, &queueEnvelope)
	}
	return app.enqueueCodexAgentThreadJobWithContextAndTenantAuthority(admittedJob, authority, reservedJobID, nil)
}

func (app *kanbanBoardApp) enqueueCodexAgentThreadJobWithContextAndTenantAuthority(admittedJob AgentJob, authority string, reservedJobID string, envelope *StrideE10TenantAuthorityEnvelope) (agentThreadWorkerResult, error) {
	if strideE10TenantCutoverEnabled() && envelope == nil {
		return agentThreadWorkerResult{}, ErrStrideE10TenantAuthorityStale
	}
	thread := admittedJob.thread
	authority = normalizeCodexJobAuthority(authority)
	metadata := codexRunnerQueuedMetadata(thread, authority)
	store := newCodexRunnerJobStore(codexRunnerQueuePath())
	prompt := app.buildCodexAgentJobPrompt(admittedJob, time.Now(), authority)
	var threadMetadata map[string]string
	if len(decodeAssistantContextRefs(thread.Artifact.Metadata["contextRefs"])) > 0 {
		// A queued prompt can outlive the ACL/seat decisions that created it.
		// Persist the thread's audit metadata (refs, requester, profile snapshot)
		// but not the File body; the claimed sidecar refreshes the profile,
		// resolves current content, and constructs the actual provider prompt.
		prompt = ""
		threadMetadata = cloneCodexThreadMetadata(thread.Artifact.Metadata)
	}
	queuedJob, err := store.enqueue(codexRunnerJob{
		ID:              strings.TrimSpace(reservedJobID),
		ArtifactID:      thread.Artifact.ID,
		ThreadID:        thread.ID,
		Mode:            thread.Mode,
		Query:           thread.Query,
		Prompt:          prompt,
		ThreadMetadata:  threadMetadata,
		Authority:       authority,
		TenantAuthority: envelope,
		Metadata: map[string]string{
			"toolRegistry":   codexToolRegistrySummary(),
			"requestedTools": codexRequestedToolsForMode(thread.Mode),
			"worker":         agentThreadWorkerCodexExec,
			"workerBoundary": "codex_sidecar_queue",
			// Carry the raw-document contract so the runner's result handler
			// keeps the worker-evidence footer OFF a deck (a markdown section
			// after </html> renders as a trailing junk page in the export).
			"outputContract": strings.TrimSpace(thread.Artifact.Metadata["outputContract"]),
		},
	})
	if err != nil {
		return agentThreadWorkerResult{Metadata: metadata}, err
	}

	metadata["runnerJobId"] = queuedJob.ID
	metadata["threadId"] = queuedJob.ThreadID
	metadata["runnerQueuePath"] = store.dir
	metadata["createdAt"] = queuedJob.CreatedAt.Format(time.RFC3339Nano)
	if codexJobStatusTerminal(queuedJob.Status) {
		return app.replayTerminalCodexRunnerJob(thread, queuedJob)
	}

	return agentThreadWorkerResult{
		Text:     buildCodexQueuedArtifact(thread, queuedJob),
		Metadata: metadata,
		Terminal: false,
	}, nil
}

func (app *kanbanBoardApp) replayTerminalCodexRunnerJob(thread scoutAgentThread, job codexRunnerJob) (agentThreadWorkerResult, error) {
	if !codexJobStatusTerminal(job.Status) || job.ClaimGeneration == 0 || strings.TrimSpace(job.FencingToken) == "" {
		return agentThreadWorkerResult{}, fmt.Errorf("terminal Codex job is missing its fenced claim")
	}
	existing, ok := app.osArtifactByID(thread.Artifact.ID)
	if !ok || strings.TrimSpace(existing.Metadata["runnerJobId"]) != job.ID || strings.TrimSpace(existing.Metadata["threadId"]) != job.ThreadID {
		return agentThreadWorkerResult{}, fmt.Errorf("terminal Codex job does not match its durable artifact")
	}
	currentStatus := strings.ToLower(strings.TrimSpace(existing.Metadata["threadStatus"]))
	if currentStatus == "error" {
		currentStatus = codexJobStatusFailed
	}
	if codexJobStatusTerminal(currentStatus) {
		if currentStatus != strings.ToLower(strings.TrimSpace(job.Status)) {
			return agentThreadWorkerResult{}, fmt.Errorf("terminal Codex artifact conflicts with its queue result")
		}
		return agentThreadWorkerResult{Text: existing.Text, Metadata: map[string]string{
			openAIToolFinalizedMetadataKey: "true", "runnerJobId": job.ID, "codexRunner": "terminal_replay",
		}, Terminal: true}, nil
	}
	if !codexJobStatusTransitionAllowed(currentStatus, job.Status) {
		return agentThreadWorkerResult{}, fmt.Errorf("terminal Codex job cannot finalize artifact from %s", currentStatus)
	}
	payload := codexRunnerCallbackPayload{
		JobID: job.ID, ArtifactID: job.ArtifactID, ThreadID: job.ThreadID, Status: job.Status,
		Text: job.ResultText, Error: job.Error, RunnerEvidence: job.RunnerEvidence, Metadata: cloneCodexThreadMetadata(job.Metadata),
		ClaimGeneration: job.ClaimGeneration, FencingToken: job.FencingToken,
	}
	if strings.TrimSpace(payload.Text) == "" && oneOf(job.Status, codexJobStatusComplete, codexJobStatusFailed) {
		return agentThreadWorkerResult{}, fmt.Errorf("terminal Codex job result payload is unavailable")
	}
	artifact, _, err := app.finalizeCodexRunnerResult(existing, payload)
	if err != nil {
		return agentThreadWorkerResult{}, err
	}
	return agentThreadWorkerResult{Text: artifact.Text, Metadata: map[string]string{
		openAIToolFinalizedMetadataKey: "true", "runnerJobId": job.ID, "codexRunner": "terminal_replay",
	}, Terminal: true}, nil
}

func cloneCodexThreadMetadata(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func codexRunnerQueuedMetadata(thread scoutAgentThread, authority string) map[string]string {
	worker := agentThreadWorkerCodexExec
	return map[string]string{
		"worker":          worker,
		"workerBoundary":  "codex_sidecar_queue",
		"codexRunnerMode": configuredCodexRunnerMode(),
		"codexRunner":     "queued",
		"authority":       normalizeCodexJobAuthority(authority),
		"requestedTools":  codexRequestedToolsForMode(thread.Mode),
		"status":          codexJobStatusQueued,
		"threadStatus":    codexJobStatusQueued,
		"goalStatus":      codexJobStatusQueued,
		"currentStage":    "queued_for_codex_runner",
		"progressPercent": "12",
		"workflowStages":  goalWorkflowStageMetadata,
		"reviewGate":      "pending",
		"queuedAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"published":       "false",
	}
}

func buildCodexQueuedArtifact(thread scoutAgentThread, job codexRunnerJob) string {
	contextLine := "Codex runner job " + job.ID + " queued with " + normalizeCodexJobAuthority(job.Authority) + " authority."
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(thread.Query),
		"Status: queued",
		"Thread mode: " + assistantToolLabel(thread.Mode),
		"Runner: Codex sidecar queue",
		"Authority: " + normalizeCodexJobAuthority(job.Authority),
		"",
		"Execution log",
		"- Realtime 2 created the artifact and kept the voice/UI loop free.",
		"- The app enqueued a Codex job for the sidecar runner.",
		"- The runner will claim one job at a time, execute with explicit sandbox and approval settings, then call back with evidence.",
	}
	return strings.Join(appendGoalWorkflow(lines, thread.Mode, thread.Query, contextLine, agentThreadDeliverable(thread.Mode), "waiting for Codex runner claim"), "\n")
}

func buildCodexApprovalRequiredArtifact(thread scoutAgentThread, authority string) string {
	contextLine := "The request requires " + normalizeCodexJobAuthority(authority) + " authority before Codex can run external side effects."
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(thread.Query),
		"Status: approval required",
		"Thread mode: " + assistantToolLabel(thread.Mode),
		"Runner: Codex sidecar queue",
		"Authority: " + normalizeCodexJobAuthority(authority),
		"",
		"Execution log",
		"- Realtime 2 created the artifact.",
		"- The requested action appears to involve commit, push, deploy, SSH, external APIs, email, or production mutation.",
		"- Codex did not run that side effect. Approve the exact side effect before resuming.",
	}
	return strings.Join(appendGoalWorkflow(lines, thread.Mode, thread.Query, contextLine, agentThreadDeliverable(thread.Mode), "approval required before external write"), "\n")
}

func codexToolRegistrySummary() string {
	return "research:read_only/report,design:workspace_write/artifact,grill:read_only/scorecard,workflow:workspace_write/goal_loop"
}

func codexRequestedToolsForMode(mode string) string {
	switch normalizeAgentThreadMode(mode) {
	case "research":
		return "research"
	case "design":
		return "design"
	case "grill":
		return "grill"
	case "workflow":
		return "workflow,research,grill"
	default:
		return "workflow"
	}
}

func runCodexRunnerLoop(ctx context.Context) error {
	store := newCodexRunnerJobStore(codexRunnerQueuePath())
	runnerID := codexRunnerID()
	pollInterval := codexRunnerPollInterval()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	log.Infof("Codex runner started id=%s queue=%s poll=%s", runnerID, store.dir, pollInterval)
	for {
		if err := writeCodexRunnerHeartbeat(runnerID); err != nil {
			log.Errorf("Codex runner heartbeat failed: %v", err)
		}
		job, err := store.claimNext(runnerID)
		if err != nil {
			log.Errorf("Codex runner queue claim failed: %v", err)
		} else if job != nil {
			processCodexRunnerJob(ctx, store, *job)
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// codexRunnerPromptAtProviderAdmission is the delayed sidecar equivalent of
// produceAgentThreadArtifactWithWorker's in-process admission fence. New
// source-bound queue records intentionally carry no prompt body: after claim,
// the sidecar resolves every ref under the original requester and only then
// assembles the provider prompt. This closes the enqueue-to-claim revocation
// window without invalidating legacy/source-free queue records.
func codexRunnerPromptAtProviderAdmission(ctx context.Context, job codexRunnerJob) (string, map[string]string, error) {
	if len(job.ThreadMetadata) == 0 {
		prompt := strings.TrimSpace(job.Prompt)
		if prompt == "" {
			return "", nil, fmt.Errorf("Codex runner prompt is unavailable")
		}
		return prompt, nil, nil
	}
	if kanbanApp == nil {
		return "", nil, fmt.Errorf("%w: Files authority is unavailable at sidecar claim", ErrAgentThreadSourceChanged)
	}
	thread := scoutAgentThread{
		ID:    job.ThreadID,
		Mode:  job.Mode,
		Query: job.Query,
		Artifact: meetingMemoryEntry{
			ID:       job.ArtifactID,
			Metadata: cloneCodexThreadMetadata(job.ThreadMetadata),
		},
	}
	// The hired-seat profile and File refs are both launch-time snapshots. A
	// delayed sidecar claim must reauthorize them together so pause/offboard and
	// human-corrected learning take effect before Codex sees the job.
	var err error
	thread, err = kanbanApp.reauthorizeAgentThreadProfile(thread)
	if err != nil {
		return "", nil, err
	}
	providerContext, err := kanbanApp.agentThreadProviderContext(ctx, thread)
	if err != nil {
		return "", nil, err
	}
	agentJob := kanbanApp.newAgentJob(thread)
	agentJob.Context = providerContext
	return kanbanApp.buildCodexAgentJobPrompt(agentJob, time.Now(), job.Authority), cloneCodexThreadMetadata(thread.Artifact.Metadata), nil
}

func failCodexRunnerProviderAdmission(ctx context.Context, store *codexRunnerJobStore, job codexRunnerJob, err error) {
	completedAt := time.Now().UTC()
	job.Status = codexJobStatusFailed
	job.CompletedAt = completedAt
	job.Error = err.Error()
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status":          "error",
		"threadStatus":    "error",
		"goalStatus":      "needs_attention",
		"currentStage":    "gate_before_shipping",
		"progressPercent": "72",
		"reviewGate":      "blocked",
		"completedAt":     completedAt.Format(time.RFC3339Nano),
		"error":           err.Error(),
		"sourceChanged":   strconv.FormatBool(errors.Is(err, ErrAgentThreadSourceChanged)),
	})
	if updateErr := store.update(job); updateErr != nil {
		log.Errorf("Codex runner could not persist provider-admission failure for job %s: %v", job.ID, updateErr)
		return
	}
	_ = sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
		JobID: job.ID, ArtifactID: job.ArtifactID, ThreadID: job.ThreadID,
		Status: job.Status, Text: buildCodexRunnerErrorArtifact(job, err), Error: job.Error, Metadata: job.Metadata,
		ClaimGeneration: job.ClaimGeneration, FencingToken: job.FencingToken,
	})
}

func quarantineCodexRunnerTenantAuthority(store *codexRunnerJobStore, job codexRunnerJob) {
	completedAt := time.Now().UTC()
	job.Status = codexJobStatusFailed
	job.CompletedAt = completedAt
	job.Error = ErrStrideE10TenantAuthorityStale.Error()
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status": "error", "threadStatus": "error", "goalStatus": "needs_attention",
		"currentStage": "tenant_authority_quarantine", "progressPercent": "0",
		"reviewGate": "blocked", "completedAt": completedAt.Format(time.RFC3339Nano),
		"error": ErrStrideE10TenantAuthorityStale.Error(),
	})
	if updateErr := store.update(job); updateErr != nil {
		log.Errorf("Codex runner could not quarantine stale-authority job %s: %v", job.ID, updateErr)
	}
}

func processCodexRunnerJob(ctx context.Context, store *codexRunnerJobStore, job codexRunnerJob) {
	if !strideE10TenantCutoverEnabled() {
		if job.TenantAuthority != nil {
			quarantineCodexRunnerTenantAuthority(store, job)
			return
		}
		processCodexRunnerJobAuthorized(ctx, store, job)
		return
	}
	err := withStrideE10TenantEnvelopeAuthority(ctx, job.TenantAuthority, StrideE10TenantSurfaceWorker, time.Now().UTC(), func(StrideE10TenantPrincipal) error {
		processCodexRunnerJobAuthorized(ctx, store, job)
		return nil
	})
	if err != nil {
		quarantineCodexRunnerTenantAuthority(store, job)
	}
}

// processCodexRunnerJobAuthorized runs only inside the worker resolver callback
// in cutover, holding current session/membership authority across the provider,
// terminal queue write, and application callback. Off and shadow call it
// directly, preserving the legacy execution path.
func processCodexRunnerJobAuthorized(ctx context.Context, store *codexRunnerJobStore, job codexRunnerJob) {
	authority := normalizeCodexJobAuthority(job.Authority)
	cfg := codexExecConfigForAuthority(codexExecConfigFromEnv(), authority, job.Mode)
	cfg.ScratchDir = filepath.Join(getenvDefault("BONFIRE_CODEX_SCRATCH_ROOT", "/runner-data/jobs"), filepath.Base(job.ID))
	defer os.RemoveAll(cfg.ScratchDir)
	now := time.Now().UTC()
	prompt, currentThreadMetadata, admissionErr := codexRunnerPromptAtProviderAdmission(ctx, job)
	if admissionErr != nil {
		failCodexRunnerProviderAdmission(ctx, store, job, admissionErr)
		return
	}
	runningMetadata := map[string]string{
		"status":              codexJobStatusRunning,
		"threadStatus":        codexJobStatusRunning,
		"goalStatus":          codexJobStatusRunning,
		"currentStage":        "codex_runner_executing",
		"progressPercent":     "35",
		"reviewGate":          "pending",
		"runnerJobId":         job.ID,
		"runnerId":            job.RunnerID,
		"worker":              agentThreadWorkerCodexExec,
		"workerBoundary":      "codex_sidecar_queue",
		"authority":           authority,
		"codexCommand":        cfg.Command,
		"codexCwd":            cfg.CWD,
		"codexSandbox":        cfg.Sandbox,
		"codexApprovalPolicy": cfg.ApprovalPolicy,
		"codexReasoning":      cfg.Reasoning,
		"codexSearch":         strconv.FormatBool(cfg.Search),
		"startedAt":           firstNonEmptyString(job.StartedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)),
	}
	if len(currentThreadMetadata) > 0 {
		job.ThreadMetadata = currentThreadMetadata
		for _, key := range append(append([]string(nil), agentThreadProfileMetadataKeys...), "agentReauthorizedAt") {
			if value := strings.TrimSpace(currentThreadMetadata[key]); value != "" {
				runningMetadata[key] = value
			}
		}
	}
	job.Status = codexJobStatusRunning
	job.Metadata = mergeStringMaps(job.Metadata, runningMetadata)
	if err := store.update(job); err != nil {
		log.Errorf("Codex runner could not persist running job %s: %v", job.ID, err)
		return
	}
	_ = sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
		JobID:           job.ID,
		ArtifactID:      job.ArtifactID,
		ThreadID:        job.ThreadID,
		Status:          codexJobStatusRunning,
		Metadata:        runningMetadata,
		ClaimGeneration: job.ClaimGeneration,
		FencingToken:    job.FencingToken,
	})
	if authority == codexJobAuthorityExternalWrite && !boolEnv("BONFIRE_CODEX_EXTERNAL_WRITE_ENABLED") {
		err := fmt.Errorf("external runner execution is disabled; set BONFIRE_CODEX_EXTERNAL_WRITE_ENABLED only for an approved shipping window")
		job.Status = codexJobStatusApprovalRequired
		job.CompletedAt = time.Now().UTC()
		job.Error = err.Error()
		job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
			"status": codexJobStatusApprovalRequired, "threadStatus": codexJobStatusApprovalRequired,
			"goalStatus": "approval_required", "reviewGate": "approval_required", "error": err.Error(),
		})
		if updateErr := store.update(job); updateErr != nil {
			log.Errorf("Codex runner could not persist approval-required job %s: %v", job.ID, updateErr)
			return
		}
		_ = sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
			JobID: job.ID, ArtifactID: job.ArtifactID, ThreadID: job.ThreadID,
			Status: job.Status, Error: job.Error, Metadata: job.Metadata,
			ClaimGeneration: job.ClaimGeneration, FencingToken: job.FencingToken,
		})
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	stopHeartbeat, heartbeatResult := startCodexRunnerClaimHeartbeat(runCtx, store, job, cancel)
	result, err := runCodexExecCommand(runCtx, cfg, prompt)
	stopHeartbeat()
	if heartbeatErr := <-heartbeatResult; heartbeatErr != nil {
		// Once renewal is ambiguous or ownership is lost, this generation has no
		// authority to persist a terminal state or notify the application. The
		// winner (or a later lease recovery) is the sole valid completion source.
		log.Errorf("Codex runner abandoned stale claim for job %s: %v", job.ID, heartbeatErr)
		return
	}
	completedAt := time.Now().UTC()
	if err != nil {
		job.Status = codexJobStatusFailed
		job.CompletedAt = completedAt
		job.Error = err.Error()
		job.RunnerEvidence = codexRunnerCommandEvidence(result, cfg)
		job.ResultText = buildCodexRunnerErrorArtifact(job, err)
		job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
			"status":          "error",
			"threadStatus":    "error",
			"goalStatus":      "needs_attention",
			"currentStage":    "gate_before_shipping",
			"progressPercent": "72",
			"reviewGate":      "blocked",
			"completedAt":     completedAt.Format(time.RFC3339Nano),
			"error":           err.Error(),
		})
		if updateErr := store.update(job); updateErr != nil {
			log.Errorf("Codex runner could not persist failed job %s: %v", job.ID, updateErr)
			return
		}
		_ = sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
			JobID:           job.ID,
			ArtifactID:      job.ArtifactID,
			ThreadID:        job.ThreadID,
			Status:          codexJobStatusFailed,
			Text:            job.ResultText,
			Error:           err.Error(),
			RunnerEvidence:  job.RunnerEvidence,
			Metadata:        job.Metadata,
			ClaimGeneration: job.ClaimGeneration,
			FencingToken:    job.FencingToken,
		})
		return
	}

	output := strings.TrimSpace(result.FinalMessage)
	if output == "" {
		output = strings.TrimSpace(result.Stdout)
	}
	status := codexJobStatusComplete
	reviewGate := "passed"
	goalStatus := "verified"
	progress := "100"
	if codexOutputRequiresExternalApproval(output) {
		status = codexJobStatusApprovalRequired
		reviewGate = "approval_required"
		goalStatus = "approval_required"
		progress = "82"
	}
	text := appendCodexWorkerEvidenceForContract(output, cfg, job.Metadata["outputContract"])
	job.Status = status
	job.CompletedAt = completedAt
	job.RunnerEvidence = codexRunnerCommandEvidence(result, cfg)
	job.ResultText = text
	job.Metadata = mergeStringMaps(job.Metadata, map[string]string{
		"status":          status,
		"threadStatus":    status,
		"goalStatus":      goalStatus,
		"currentStage":    "verify_goal_completed",
		"progressPercent": progress,
		"reviewGate":      reviewGate,
		"completedAt":     completedAt.Format(time.RFC3339Nano),
		"codexFinalBytes": strconv.Itoa(len(output)),
	})
	if status == codexJobStatusApprovalRequired {
		job.Metadata["currentStage"] = "gate_before_shipping"
	}
	if err := store.update(job); err != nil {
		log.Errorf("Codex runner could not persist completed job %s: %v", job.ID, err)
		return
	}

	if err := sendCodexRunnerCallback(ctx, codexRunnerCallbackPayload{
		JobID:           job.ID,
		ArtifactID:      job.ArtifactID,
		ThreadID:        job.ThreadID,
		Status:          status,
		Text:            text,
		RunnerEvidence:  job.RunnerEvidence,
		Metadata:        job.Metadata,
		ClaimGeneration: job.ClaimGeneration,
		FencingToken:    job.FencingToken,
	}); err != nil {
		log.Errorf("Codex runner callback failed for job %s: %v", job.ID, err)
	}
}

func startCodexRunnerClaimHeartbeat(ctx context.Context, store *codexRunnerJobStore, job codexRunnerJob, cancelRun context.CancelFunc) (func(), <-chan error) {
	leaseDuration := codexRunnerLeaseDuration()
	interval := leaseDuration / 3
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	heartbeatCtx, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				result <- nil
				return
			case <-ticker.C:
				renewed, err := store.renewClaim(job)
				if err != nil {
					cancelRun()
					result <- err
					return
				}
				job = *renewed
			}
		}
	}()
	return stop, result
}

func buildCodexRunnerErrorArtifact(job codexRunnerJob, err error) string {
	nextAction := "inspect runner logs, credentials, queue health, or sandbox access, then rerun the thread."
	if errors.Is(err, ErrAgentThreadSourceChanged) {
		nextAction = "the source changed while this job was queued. Reselect or reattach every referenced File, confirm you can still open it, then retry. The sidecar stopped before sending stale or revoked source content to Codex."
	}
	lines := []string{
		"Scout work thread",
		"",
		"Vision: " + compactAssistantLine(job.Query),
		"Status: needs attention",
		"Thread mode: " + assistantToolLabel(job.Mode),
		"",
		"Execution log",
		"- The sidecar Codex runner claimed the job.",
		"- Worker error: " + strings.TrimSpace(err.Error()),
		"",
		"Next action: " + nextAction,
	}
	return strings.Join(appendGoalWorkflow(lines, job.Mode, job.Query, err.Error(), agentThreadDeliverable(job.Mode), "worker error recorded on artifact"), "\n")
}

func codexRunnerCommandEvidence(result codexExecResult, cfg codexExecConfig) string {
	parts := []string{
		"command=" + cfg.Command,
		"cwd=" + cfg.CWD,
		"sandbox=" + cfg.Sandbox,
		"approval=" + cfg.ApprovalPolicy,
		"reasoning=" + cfg.Reasoning,
		"search=" + strconv.FormatBool(cfg.Search),
		"stdout_bytes=" + strconv.Itoa(len(result.Stdout)),
		"stderr_bytes=" + strconv.Itoa(len(result.Stderr)),
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, "stderr="+compactAssistantLine(result.Stderr))
	}
	return strings.Join(parts, "\n")
}

func codexOutputRequiresExternalApproval(output string) bool {
	const marker = "EXTERNAL_WRITE_APPROVAL_REQUIRED"
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimLeft(trimmed, "-*>` \t")
		if !strings.HasPrefix(strings.ToUpper(trimmed), marker) {
			continue
		}
		remainder := strings.TrimSpace(trimmed[len(marker):])
		remainder = strings.TrimLeft(remainder, "*` \t")
		if remainder == "" || strings.HasPrefix(remainder, ":") || strings.HasPrefix(remainder, "-") {
			return true
		}
	}
	return false
}

func codexRunnerID() string {
	if value := strings.TrimSpace(os.Getenv("BONFIRE_CODEX_RUNNER_ID")); value != "" {
		return value
	}
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "codex-runner"
	}
	return hostname + "-" + strconv.Itoa(os.Getpid())
}

func writeCodexRunnerHeartbeat(runnerID string) error {
	cfg := codexExecConfigFromEnv()
	payload := map[string]any{
		"ok":           true,
		"runnerId":     runnerID,
		"queuePath":    codexRunnerQueuePath(),
		"codexCwd":     cfg.CWD,
		"workspaceGit": codexWorkspaceHasGit(cfg.CWD),
		"time":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	return writeJSONFileAtomically(codexRunnerHeartbeatPath(), "Codex runner heartbeat", payload)
}

func readinessCodexRunnerSnapshot() map[string]any {
	worker := configuredAgentThreadWorkerMode()
	snapshot := map[string]any{
		"worker":          worker,
		"runnerMode":      configuredCodexRunnerMode(),
		"queuePath":       codexRunnerQueuePath(),
		"heartbeatPath":   codexRunnerHeartbeatPath(),
		"callbackSecured": strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN")) != "",
	}
	if worker != agentThreadWorkerCodexExec {
		snapshot["enabled"] = false
		return snapshot
	}
	snapshot["enabled"] = true
	raw, err := os.ReadFile(codexRunnerHeartbeatPath())
	if err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "missing"
		return snapshot
	}
	var heartbeat struct {
		RunnerID     string `json:"runnerId"`
		CodexCWD     string `json:"codexCwd"`
		WorkspaceGit bool   `json:"workspaceGit"`
		Time         string `json:"time"`
	}
	if err := json.Unmarshal(raw, &heartbeat); err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid"
		return snapshot
	}
	parsed, err := time.Parse(time.RFC3339Nano, heartbeat.Time)
	if err != nil {
		snapshot["heartbeatOK"] = false
		snapshot["heartbeatError"] = "invalid_time"
		return snapshot
	}
	age := time.Since(parsed)
	snapshot["heartbeatOK"] = age <= defaultCodexRunnerStaleAfter
	snapshot["heartbeatAgeSeconds"] = int(age.Seconds())
	snapshot["runnerId"] = heartbeat.RunnerID
	snapshot["codexCwd"] = heartbeat.CodexCWD
	snapshot["workspaceGit"] = heartbeat.WorkspaceGit
	return snapshot
}

func codexWorkspaceHasGit(cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(cwd, ".git"))
	return err == nil && info != nil
}

var sendCodexRunnerCallback = sendCodexRunnerCallbackContext

func sendCodexRunnerCallbackContext(ctx context.Context, payload codexRunnerCallbackPayload) error {
	callbackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	callbackURL := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_CALLBACK_URL"))
	if callbackURL == "" {
		callbackURL = "http://meetingassist:3000/internal/codex/jobs/result"
	}
	token := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN"))
	if token == "" {
		return fmt.Errorf("BONFIRE_RUNNER_TOKEN is required for Codex runner callbacks")
	}
	if payload.ClaimGeneration > 0 || strings.TrimSpace(payload.FencingToken) != "" {
		payload.Capability = codexRunnerCallbackCapabilityV2(token, payload.JobID, payload.ArtifactID, payload.ThreadID, payload.ClaimGeneration, payload.FencingToken)
	} else {
		payload.Capability = codexRunnerCallbackCapability(token, payload.JobID, payload.ArtifactID, payload.ThreadID)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Codex runner callback: %w", err)
	}
	req, err := http.NewRequestWithContext(callbackCtx, http.MethodPost, callbackURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("create Codex runner callback request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send Codex runner callback: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Codex runner callback returned %s", resp.Status)
	}
	return nil
}

func codexRunnerCallbackCapability(token, jobID, artifactID, threadID string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(token)))
	for _, value := range []string{jobID, artifactID, threadID} {
		value = strings.TrimSpace(value)
		_, _ = fmt.Fprintf(mac, "%d:", len(value))
		_, _ = mac.Write([]byte(value))
	}
	return fmt.Sprintf("v1.%x", mac.Sum(nil))
}

func codexRunnerCallbackCapabilityV2(token, jobID, artifactID, threadID string, generation uint64, fencingToken string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(token)))
	for _, value := range []string{jobID, artifactID, threadID, strconv.FormatUint(generation, 10), fencingToken} {
		value = strings.TrimSpace(value)
		_, _ = fmt.Fprintf(mac, "%d:", len(value))
		_, _ = mac.Write([]byte(value))
	}
	return fmt.Sprintf("v2.%x", mac.Sum(nil))
}

func internalCodexRunnerResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !runnerCallbackAuthorized(r) {
		writeSystemStatusJSON(w, r, http.StatusUnauthorized, map[string]any{
			"ok":    false,
			"error": "runner callback not authorized",
		})
		return
	}
	if kanbanApp == nil {
		writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"error": "assistant is unavailable",
		})
		return
	}

	var payload codexRunnerCallbackPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&payload); err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "could not read runner callback",
		})
		return
	}
	artifactID := strings.TrimSpace(payload.ArtifactID)
	if artifactID == "" {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "artifact_id is required",
		})
		return
	}
	callbackJobID := strings.TrimSpace(payload.JobID)
	callbackThreadID := strings.TrimSpace(payload.ThreadID)
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	if callbackJobID == "" || callbackThreadID == "" {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{"ok": false, "error": "job_id and thread_id are required"})
		return
	}
	if !validCodexJobStatus(status) || status == codexJobStatusQueued {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid runner status"})
		return
	}
	runnerToken := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN"))
	expectedCapability := codexRunnerCallbackCapability(runnerToken, callbackJobID, artifactID, callbackThreadID)
	queueStore := newCodexRunnerJobStore(codexRunnerQueuePath())
	queueJob, queueErr := queueStore.read(filepath.Base(queueStore.jobPath(callbackJobID)))
	if queueErr == nil && queueJob.ClaimGeneration > 0 {
		if strings.TrimSpace(queueJob.ArtifactID) != artifactID || strings.TrimSpace(queueJob.ThreadID) != callbackThreadID || strings.TrimSpace(queueJob.Status) != status {
			writeSystemStatusJSON(w, r, http.StatusConflict, map[string]any{"ok": false, "error": "runner callback does not match the durable claimed job state"})
			return
		}
		if payload.ClaimGeneration != queueJob.ClaimGeneration ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(payload.FencingToken)), []byte(strings.TrimSpace(queueJob.FencingToken))) != 1 {
			writeSystemStatusJSON(w, r, http.StatusConflict, map[string]any{"ok": false, "error": "runner callback carries a stale claim"})
			return
		}
		expectedCapability = codexRunnerCallbackCapabilityV2(runnerToken, callbackJobID, artifactID, callbackThreadID, payload.ClaimGeneration, payload.FencingToken)
	} else if queueErr != nil && !errors.Is(queueErr, os.ErrNotExist) {
		writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "runner claim could not be verified"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(payload.Capability)), []byte(expectedCapability)) != 1 {
		writeSystemStatusJSON(w, r, http.StatusUnauthorized, map[string]any{"ok": false, "error": "runner capability does not match job binding"})
		return
	}
	if strideE10TenantCutoverEnabled() {
		_, authorityHeld := r.Context().Value(strideE10CodexRunnerCallbackAuthorityContextKey{}).(bool)
		if !authorityHeld {
			if queueErr != nil || queueJob.TenantAuthority == nil || queueJob.TenantAuthority.Purpose != StrideE10TenantAuthorityPurposeForCodexJob(queueJob.ArtifactID, queueJob.ThreadID, queueJob.Mode, queueJob.Query, queueJob.Authority) {
				if queueErr == nil {
					quarantineCodexRunnerTenantAuthority(queueStore, *queueJob)
				}
				writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "tenant authority unavailable"})
				return
			}
			callbackBody, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "tenant authority unavailable"})
				return
			}
			authorityErr := withStrideE10TenantEnvelopeAuthority(r.Context(), queueJob.TenantAuthority, StrideE10TenantSurfaceWorker, time.Now().UTC(), func(StrideE10TenantPrincipal) error {
				bound := r.Clone(context.WithValue(r.Context(), strideE10CodexRunnerCallbackAuthorityContextKey{}, true))
				bound.Body = io.NopCloser(bytes.NewReader(callbackBody))
				internalCodexRunnerResultHandler(w, bound)
				return nil
			})
			if authorityErr != nil {
				quarantineCodexRunnerTenantAuthority(queueStore, *queueJob)
				writeSystemStatusJSON(w, r, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "tenant authority unavailable"})
			}
			return
		}
	}
	existing, exists := kanbanApp.osArtifactByID(artifactID)
	if !exists {
		writeSystemStatusJSON(w, r, http.StatusNotFound, map[string]any{
			"ok":    false,
			"error": "artifact not found",
		})
		return
	}
	expectedJobID := strings.TrimSpace(existing.Metadata["runnerJobId"])
	expectedThreadID := strings.TrimSpace(existing.Metadata["threadId"])
	if expectedJobID == "" || expectedThreadID == "" || callbackJobID != expectedJobID || callbackThreadID != expectedThreadID {
		writeSystemStatusJSON(w, r, http.StatusConflict, map[string]any{
			"ok":    false,
			"error": "runner job does not match artifact",
		})
		return
	}
	currentStatus := strings.ToLower(strings.TrimSpace(existing.Metadata["threadStatus"]))
	if currentStatus == "error" {
		currentStatus = codexJobStatusFailed
	}
	if currentStatus != "" && !codexJobStatusTransitionAllowed(currentStatus, status) {
		writeSystemStatusJSON(w, r, http.StatusConflict, map[string]any{"ok": false, "error": "runner status transition is not monotonic"})
		return
	}

	artifact, actions, err := kanbanApp.finalizeCodexRunnerResult(existing, payload)
	if err != nil {
		writeSystemStatusJSON(w, r, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	writeSystemStatusJSON(w, r, http.StatusOK, map[string]any{
		"ok":       true,
		"artifact": artifact,
		"actions":  actions,
	})
}

// finalizeCodexRunnerResult is the single post-fence terminal seam shared by
// the HTTP callback and durable terminal-job replay after a lost callback.
func (app *kanbanBoardApp) finalizeCodexRunnerResult(existing meetingMemoryEntry, payload codexRunnerCallbackPayload) (meetingMemoryEntry, []osAssistantAction, error) {
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	metadata := map[string]string{"runnerJobId": strings.TrimSpace(payload.JobID), "codexRunner": "callback", "status": status, "threadStatus": status}
	if payload.Error != "" {
		metadata["error"] = payload.Error
	}
	for key, value := range payload.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	// Descriptive callback metadata cannot rewrite the fenced binding/state.
	metadata["runnerJobId"] = strings.TrimSpace(payload.JobID)
	metadata["threadId"] = strings.TrimSpace(payload.ThreadID)
	metadata["status"] = status
	metadata["threadStatus"] = status
	if strings.TrimSpace(existing.Metadata[publicConversationWorkActivationState]) != "" {
		metadata[publicConversationWorkActivationState] = publicConversationWorkComplete
		if status == codexJobStatusFailed || status == codexJobStatusApprovalRequired {
			metadata[publicConversationWorkActivationState] = publicConversationWorkNeedsAttention
		}
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		text = existing.Text
	}
	title := existing.Metadata["title"]
	if status == codexJobStatusComplete && strings.TrimSpace(payload.Text) != "" {
		if runnerTitle := strings.TrimSpace(payload.Metadata["title"]); runnerTitle != "" {
			title = runnerTitle
		} else if derived := agentThreadDisplayTitle(text, title); derived != "" && derived != title {
			title = derived
			metadata["titleSource"] = "derived"
		}
		stampReadinessMetadata(existing, firstNonEmptyString(existing.Metadata["mode"], existing.Kind), text, metadata)
	}
	writer := agentThreadArtifactWriter(scoutAgentThread{Artifact: existing}, agentThreadWorkerResult{Metadata: metadata})
	artifact, changed, err := app.updateOSArtifactWithMetadata(existing.ID, title, text, writer, metadata)
	if err != nil {
		return meetingMemoryEntry{}, nil, err
	}
	switch status {
	case codexJobStatusComplete:
		app.appendAgentRunLogEntryForArtifact(artifact, "complete", text)
	case codexJobStatusFailed:
		app.appendAgentRunLogEntryForArtifact(artifact, "error", text)
	}
	if changed && oneOf(status, codexJobStatusComplete, codexJobStatusFailed) {
		usageEntry := llmUsageEntry{Provider: providerOpenAI, Model: defaultCodexExecModel, Seat: seatCodex,
			ThreadID: firstNonEmptyString(strings.TrimSpace(payload.ThreadID), strings.TrimSpace(existing.Metadata["threadId"])), Estimated: true}
		if startedAt, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(existing.Metadata["startedAt"])); parseErr == nil {
			usageEntry.DurationMS = time.Since(startedAt).Milliseconds()
		}
		usageEntry.Error = strings.TrimSpace(payload.Error)
		recordLLMUsage(usageEntry)
	}
	statusMessage := codexRunnerStatusMessage(payload.Status, artifact)
	if changed && oneOf(status, codexJobStatusComplete, codexJobStatusFailed, codexJobStatusApprovalRequired) {
		app.updateScoutChatThreadRefs(payload.ThreadID, status, artifact.ID)
		app.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText(statusMessage, artifact))
		if status == codexJobStatusComplete {
			app.deliverArtifactToOrigin(artifact, firstNonEmptyString(artifact.Metadata["latestThreadRun"], artifact.Metadata["threadId"]))
		}
		app.syncLinkedCardForArtifact(artifact, payload.Status)
		if parentID := strings.TrimSpace(artifact.Metadata["goalParentId"]); parentID != "" && oneOf(status, codexJobStatusComplete, codexJobStatusFailed) {
			if strideE10TenantCutoverEnabled() {
				app.foldGoalChildCompletion(parentID, artifact.Metadata["goalSubtaskId"], artifact, payload.Status)
			} else {
				go app.foldGoalChildCompletion(parentID, artifact.Metadata["goalSubtaskId"], artifact, payload.Status)
			}
		}
	}
	actions := app.osAssistantActions(firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"]), artifact.Metadata["mode"], artifact)
	broadcastSignedInKanbanEvent("memory", nil)
	broadcastAssistantEvent("action", statusMessage, agentThreadBroadcastMetadata("codex_runner", artifact.Metadata["threadId"], payload.Status, "listening"))
	return artifact, actions, nil
}

func artifactRunnerActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return
	}

	payload := struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Reason string `json:"reason"`
		// Choice is the human_checkpoint pick (index.html submitApproval): the
		// checkpoint's option label plus any appended notes. It MUST reach
		// resumeApprovedGoalWithChoice — dropping it turns every negative
		// option (hold, send back) into a silent proceed.
		Choice string `json:"choice"`
		// CheckpointID + CheckpointOptionID are the opaque server projection
		// carried by a public work card. When present they replace free-form
		// Choice: the persisted goal plan resolves the exact authored action.
		CheckpointID       string `json:"checkpointId"`
		CheckpointOptionID string `json:"checkpointOptionId"`
		// CheckpointNote is optional customer feedback for an authored revise
		// option. The server binds it to the selected opaque option; it never
		// changes the option's action or target.
		CheckpointNote string `json:"checkpointNote"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read artifact action")
		return
	}
	artifactID := strings.TrimSpace(payload.ID)
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	if artifactID == "" || action == "" {
		writeAuthError(w, http.StatusBadRequest, "artifact id and action are required")
		return
	}
	hasCheckpointBinding := strings.TrimSpace(payload.CheckpointID) != "" || strings.TrimSpace(payload.CheckpointOptionID) != ""
	if hasCheckpointBinding && (action != "approve" || strings.TrimSpace(payload.CheckpointID) == "" || strings.TrimSpace(payload.CheckpointOptionID) == "") {
		writeAuthError(w, http.StatusBadRequest, "checkpoint id and option id are required for a checkpoint choice")
		return
	}
	requiredActions := artifactRunnerRequiredACLActions(action)
	artifact, exists := authorizedArtifactForActions(r.Context(), user, artifactID, requiredActions...)
	if !exists {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if hasCheckpointBinding && goalCheckpointActionAfterAuthorizationProbe != nil {
		goalCheckpointActionAfterAuthorizationProbe()
	}

	switch action {
	case "approve":
		if hasCheckpointBinding && !isArtifactApprovalAdmin(user) {
			writeAuthError(w, http.StatusForbidden, "checkpoint choices are admin-only")
			return
		}
		// External-write approval stays admin-gated, now with the card-069
		// heavy-lane consensus door: the admin approves alone, and two distinct
		// non-admin members together carry the same weight. A non-admin approve
		// on a PARKED artifact records an endorsement (202, n/2); the
		// endorsement that completes the pair falls through and executes the
		// exact approve path the admin would. A non-admin approve on anything
		// not parked stays 403 — approve is not a general-purpose action.
		endorsedToExecution := false
		if !isArtifactApprovalAdmin(user) {
			if !artifactAwaitingApproval(artifact.Metadata) {
				writeAuthError(w, http.StatusForbidden, "external-write approval is admin-only")
				return
			}
			endorsements, reached, err := kanbanApp.recordApprovalEndorsement(artifactID, user.Email)
			if err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !reached {
				updated, _ := kanbanApp.osArtifactByID(artifactID)
				writeAuthJSON(w, http.StatusAccepted, map[string]any{
					"ok":       true,
					"artifact": updated,
					"endorsement": map[string]any{
						"count":    len(endorsements),
						"required": approvalConsensusRequired,
					},
					"message": fmt.Sprintf("endorsement recorded (%d/%d)", len(endorsements), approvalConsensusRequired),
				})
				return
			}
			endorsedToExecution = true
		}
		// A /goal artifact parked at its ship gate resumes through the goal
		// engine (commit_push), which ships exactly the command the gate
		// recorded — not a fresh codex job re-derived from the objective text.
		if artifact.Metadata["mode"] == "goal" {
			// The checkpoint choice rides through: a hold-action choice keeps
			// the goal parked and a revise-action choice re-queues its target —
			// resumeProcessCheckpoint's teeth are only real if the choice
			// survives the HTTP door.
			replayed := false
			receiptBound := hasCheckpointBinding
			var resumeErr error
			if hasCheckpointBinding {
				replayed, resumeErr = kanbanApp.resumeApprovedGoalWithCheckpointOptionAuthorizedNote(r.Context(), user, artifact, user.Name, payload.CheckpointID, payload.CheckpointOptionID, payload.CheckpointNote)
			} else {
				replayed, receiptBound, resumeErr = kanbanApp.resumeApprovedGoalWithChoiceAuthorized(r.Context(), user, artifact, user.Name, payload.Choice)
			}
			if resumeErr != nil {
				if endorsedToExecution {
					// The consensus was consumed but the execution failed:
					// un-consume it so a retry by either endorser can complete
					// the launch (resolveCodexProposal's revert discipline).
					kanbanApp.clearApprovalConsensusStamp(artifactID)
				}
				writeAuthError(w, http.StatusBadRequest, resumeErr.Error())
				return
			}
			updated, _ := kanbanApp.osArtifactByID(artifactID)
			// Only a proceed is a sign-off. A hold parked the goal; a revise
			// (send-back) asked for changes — including the disclosed
			// budget-spent fallback, where the founder asked for revision and
			// did NOT approve. Neither earns the durable approval stamp (it
			// unlocks sharing) or the "approved · sent" fan-out.
			if !receiptBound && !replayed {
				if plan, ok := decodeGoalPlan(updated.Metadata["goalPlan"]); !ok || plan.Checkpoint == nil ||
					(!plan.Checkpoint.Held && plan.Checkpoint.LastAction != processCheckpointActionRevise) {
					// Durable human-approval record (share_links.go): reviewGate/status
					// keep moving as the resumed work runs, so the share gate keys on
					// this stamp instead.
					kanbanApp.stampArtifactHumanApproval(artifactID, user.Name)
					// Round-trip loop: fan the approval to the push channel + the
					// requester so their origin surface flips to "approved · sent".
					kanbanApp.recordApprovalOutcome(artifact, "approve", "", user.Name)
					updated, _ = kanbanApp.osArtifactByID(artifactID)
				}
			}
			actions := kanbanApp.osAssistantActions(updated.Metadata["threadQuery"], updated.Metadata["mode"], updated)
			writeAuthJSON(w, http.StatusAccepted, map[string]any{
				"ok":       true,
				"artifact": updated,
				"actions":  actions,
				"replayed": replayed,
			})
			return
		}
		updated, actions, err := kanbanApp.approveCodexArtifactExternalWrite(artifact, user.Name)
		if err != nil {
			if endorsedToExecution {
				kanbanApp.clearApprovalConsensusStamp(artifactID)
			}
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Durable human-approval record (share_links.go): survives the queued
		// job's later reviewGate/status rewrites.
		kanbanApp.stampArtifactHumanApproval(artifact.ID, user.Name)
		kanbanApp.recordApprovalOutcome(artifact, "approve", "", user.Name)
		writeAuthJSON(w, http.StatusAccepted, map[string]any{
			"ok":       true,
			"artifact": updated,
			"actions":  actions,
		})
	case "resume":
		// The blocked-goal recovery door: requester or admin only, goal mode
		// only. Resets exhausted subtasks and re-drives from where it stopped.
		if artifact.Metadata["mode"] != "goal" {
			writeAuthError(w, http.StatusBadRequest, "resume applies to goal runs")
			return
		}
		requester := strings.TrimSpace(artifact.Metadata["requestedBy"])
		if !isArtifactApprovalAdmin(user) && !strings.EqualFold(requester, normalizeAccountEmail(user.Email)) && !strings.EqualFold(requester, user.Name) {
			writeAuthError(w, http.StatusForbidden, "only the requester or an admin resumes a blocked run")
			return
		}
		if err := kanbanApp.resumeBlockedGoal(artifactID, user.Name); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, _ := kanbanApp.osArtifactByID(artifactID)
		writeAuthJSON(w, http.StatusAccepted, map[string]any{"ok": true, "artifact": updated})
		return
	case "reject":
		if !isArtifactApprovalAdmin(user) {
			writeAuthError(w, http.StatusForbidden, "external-write approval is admin-only")
			return
		}
		updated, actions, err := kanbanApp.rejectCodexArtifactGate(artifact, user.Name)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Round-trip loop: the requester's card returns with the admin's reason.
		kanbanApp.recordApprovalOutcome(artifact, "reject", payload.Reason, user.Name)
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"artifact": updated,
			"actions":  actions,
		})
	case "rerun":
		// Rerun is the same capability as POST /assistant/threads, which is
		// open to every signed-in user.
		mode := rerunThreadMode(artifact)
		query := firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"], compactAssistantLine(artifact.Text))
		// A rerun inherits the prior artifact's origin ONLY when delivery there
		// is still safe for THIS user (GATE-FINDINGS G2); everything else drops
		// to originKind tool, which keeps the creator-notification behavior.
		origin := kanbanApp.rerunOriginForUser(artifact, user.Email)
		origin["requestedBy"] = normalizeAccountEmail(user.Email)
		thread, err := kanbanApp.launchAgentThreadWithOrigin(mode, query, user.Name, origin)
		if err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAuthJSON(w, http.StatusAccepted, map[string]any{
			"ok":       true,
			"thread":   thread,
			"artifact": thread.Artifact,
			"actions":  thread.Actions,
		})
	default:
		writeAuthError(w, http.StatusBadRequest, "unknown artifact action")
	}
}

func artifactRunnerRequiredACLActions(action string) []ACLAction {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "approve", "reject":
		return []ACLAction{ACLReadMetadata, ACLApprove, ACLWrite}
	case "resume":
		return []ACLAction{ACLReadContent, ACLExecute, ACLWrite}
	case "rerun":
		return []ACLAction{ACLReadContent, ACLExecute}
	default:
		return []ACLAction{ACLWrite}
	}
}

// rerunThreadMode resolves which thread mode a rerun relaunches with.
// metadata["mode"] carries the launch mode ("research", "grill", …);
// artifact.Kind is always "os_artifact", so reading Kind alone silently
// dropped every rerun to workflow mode and lost the research contract — the
// same firstNonEmptyString fallback the follow-up runner already uses
// (agent_thread_followup.go).
func rerunThreadMode(artifact meetingMemoryEntry) string {
	mode := normalizeAgentThreadMode(firstNonEmptyString(artifact.Metadata["mode"], artifact.Kind))
	if mode == "" {
		mode = "workflow"
	}
	return mode
}

// rerunOriginForUser decides which origin metadata a rerun may inherit from
// the stored artifact (GATE-FINDINGS G2 — conditional origin inheritance):
//   - channel origins survive only while the origin thread is still a public,
//     unarchived channel;
//   - private-thread origins survive only when the rerunning user OWNS the
//     origin thread (a non-owner rerun must never post into someone else's
//     private thread);
//   - room origins survive only while the origin meeting is still the active
//     meeting;
//   - everything else (tool, absent, unresolvable) drops to originKind tool,
//     which keeps the creator-notification-only completion behavior.
func (app *kanbanBoardApp) rerunOriginForUser(artifact meetingMemoryEntry, userEmail string) map[string]string {
	origin := map[string]string{"originKind": agentThreadOriginTool}
	if app == nil || app.memory == nil {
		return origin
	}
	originID := strings.TrimSpace(artifact.Metadata["originId"])
	switch strings.TrimSpace(artifact.Metadata["originKind"]) {
	case agentThreadOriginChannel, agentThreadOriginPrivateThread:
		if originID == "" {
			return origin
		}
		entry, ok := app.memory.entryByKindAndID(meetingMemoryKindScoutChat, originID)
		if !ok {
			return origin
		}
		thread, decoded := decodeScoutChatThreadEntry(entry)
		if !decoded || thread.ArchivedAt != "" {
			return origin
		}
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic && scoutChatThreadAllowsViewer(thread, userEmail) {
			origin["originKind"] = agentThreadOriginChannel
			origin["originId"] = originID
			return origin
		}
		if normalizeAccountEmail(thread.OwnerEmail) != normalizeAccountEmail(userEmail) {
			return origin
		}
		origin["originKind"] = agentThreadOriginPrivateThread
		origin["originId"] = originID
	case agentThreadOriginRoom:
		originMeetingID := strings.TrimSpace(artifact.Metadata["originMeetingId"])
		if originMeetingID == "" || originMeetingID != app.memory.currentMeetingID(officeRoomID) {
			return origin
		}
		origin["originKind"] = agentThreadOriginRoom
		origin["originMeetingId"] = originMeetingID
		if originID != "" {
			origin["originId"] = originID
		}
	}
	return origin
}

func (app *kanbanBoardApp) approveCodexArtifactExternalWrite(artifact meetingMemoryEntry, approvedBy string) (meetingMemoryEntry, []osAssistantAction, error) {
	// Serialize the approve EXECUTION and re-read the artifact's CURRENT state:
	// the caller's copy was fetched at handler entry, before any concurrent
	// approve (a racing admin tap, or the endorsement that completes the
	// 2-member consensus) flipped the gate. Guarding on that stale copy lets
	// both approves pass reviewGate==approval_required and enqueue the SAME
	// external_write job twice. Under the lock exactly one caller observes the
	// parked gate and flips it to approved; the loser re-reads reviewGate=approved
	// and returns the not-waiting error below.
	approvalExecuteMu.Lock()
	defer approvalExecuteMu.Unlock()
	if current, exists := app.osArtifactByID(artifact.ID); exists {
		artifact = current
	}
	if artifact.Metadata["reviewGate"] != "approval_required" && artifact.Metadata["threadStatus"] != codexJobStatusApprovalRequired {
		return meetingMemoryEntry{}, nil, fmt.Errorf("artifact is not waiting for external-write approval")
	}
	mode := normalizeAgentThreadMode(artifact.Kind)
	if mode == "" {
		mode = "workflow"
	}
	threadID := firstNonEmptyString(artifact.Metadata["threadId"], fmt.Sprintf("agent-thread-%s-%d", mode, time.Now().UTC().UnixNano()))
	thread := scoutAgentThread{
		ID:       threadID,
		Mode:     mode,
		Query:    firstNonEmptyString(artifact.Metadata["threadQuery"], artifact.Metadata["title"], compactAssistantLine(artifact.Text)),
		Status:   codexJobStatusQueued,
		Artifact: artifact,
	}
	result, err := app.enqueueCodexAgentThreadJob(thread, codexJobAuthorityExternalWrite)
	if err != nil {
		return meetingMemoryEntry{}, nil, err
	}
	result.Metadata["approvedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	result.Metadata["approvedBy"] = canonicalRoomActorName(approvedBy)
	result.Metadata["reviewGate"] = "approved"
	result.Metadata["approvalAuthority"] = codexJobAuthorityExternalWrite
	app.updateQueuedAgentThread(thread, result)
	updated, exists := app.osArtifactByID(artifact.ID)
	if !exists {
		return meetingMemoryEntry{}, nil, fmt.Errorf("approved artifact was not found after queue update")
	}
	actions := app.osAssistantActions(thread.Query, mode, updated)
	return updated, actions, nil
}

func (app *kanbanBoardApp) rejectCodexArtifactGate(artifact meetingMemoryEntry, rejectedBy string) (meetingMemoryEntry, []osAssistantAction, error) {
	// Idempotency guard, mirroring approveCodexArtifactExternalWrite: a second
	// reject on an artifact that is no longer at the gate must not rewrite it
	// again, or the handler would double-fire the requester's "Rejected"
	// notification + push event. A double-clicked/resubmitted reject is a no-op.
	if !artifactAwaitingApproval(artifact.Metadata) {
		return meetingMemoryEntry{}, nil, fmt.Errorf("artifact is not waiting for external-write approval")
	}
	metadata := map[string]string{
		"status":          "rejected",
		"threadStatus":    "rejected",
		"goalStatus":      "rejected",
		"currentStage":    "gate_before_shipping",
		"progressPercent": "68",
		"reviewGate":      "rejected",
		"rejectedAt":      time.Now().UTC().Format(time.RFC3339Nano),
		"rejectedBy":      canonicalRoomActorName(rejectedBy),
	}
	updated, _, err := app.updateOSArtifactWithMetadata(artifact.ID, artifact.Metadata["title"], artifact.Text, rejectedBy, metadata)
	if err != nil {
		return meetingMemoryEntry{}, nil, err
	}
	actions := app.osAssistantActions(updated.Metadata["title"], updated.Kind, updated)
	broadcastSignedInKanbanEvent("memory", nil)
	broadcastAssistantEvent("action", assistantToolLabel(updated.Kind)+" thread rejected", agentThreadBroadcastMetadata("codex_runner", updated.Metadata["threadId"], "rejected", "listening"))
	return updated, actions, nil
}

func runnerCallbackAuthorized(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("BONFIRE_RUNNER_TOKEN"))
	if expected == "" {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get("X-Bonfire-Runner-Token"))
	if provided == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			provided = strings.TrimSpace(auth[len("bearer "):])
		}
	}
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func codexRunnerStatusMessage(status string, artifact meetingMemoryEntry) string {
	label := assistantToolLabel(artifact.Kind)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case codexJobStatusRunning:
		return label + " thread running in Codex"
	case codexJobStatusFailed:
		return label + " thread needs attention"
	case codexJobStatusApprovalRequired:
		return label + " thread needs approval"
	default:
		return label + " thread complete"
	}
}

func mergeStringMaps(base map[string]string, overlay map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	for key, value := range overlay {
		if strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	return merged
}
