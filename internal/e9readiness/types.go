// Package e9readiness defines the closed, token-free E9 launch-readiness
// contracts. It validates plans and manifest-only contract exercises; it cannot
// provision infrastructure, shift traffic, or turn synthetic receipts into
// production evidence.
package e9readiness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	ReadinessSchema = "stride.e9.readiness/v1"
	WorkerSchema    = "stride.e9.worker-isolation/v2"
	// ContractDrillSchema names a manifest-only exercise. It is intentionally
	// not an integration, media, provider, or runtime verification schema.
	ContractDrillSchema = "stride.e9.contract-drill/v1"
)

var requiredCapabilities = []string{
	"canonical", "consent", "transcript", "analysis", "brain", "embeddings",
	"scout", "workflow", "queue", "backup", "cost",
}

type ReadinessManifest struct {
	SchemaVersion            string              `json:"schemaVersion"`
	ManifestID               string              `json:"manifestId"`
	Mode                     string              `json:"mode"`
	ActivationDefault        string              `json:"activationDefault"`
	ProductionMutation       bool                `json:"productionMutation"`
	ManagedPostgres          ManagedPostgresPlan `json:"managedPostgres"`
	OffsiteRecovery          OffsiteRecoveryPlan `json:"offsiteRecovery"`
	Availability             AvailabilityPlan    `json:"availability"`
	Paging                   []CapabilityPage    `json:"paging"`
	ExternalEvidenceRequired []string            `json:"externalEvidenceRequired"`
}

type ManagedPostgresPlan struct {
	State                 string   `json:"state"`
	ActivationEnabled     bool     `json:"activationEnabled"`
	MinimumNodes          int      `json:"minimumNodes"`
	MultiZone             bool     `json:"multiZone"`
	AutomaticFailover     bool     `json:"automaticFailover"`
	PurgeAuthorityOutside bool     `json:"purgeAuthorityOutsideCluster"`
	RequiredEvidence      []string `json:"requiredEvidence"`
}

type OffsiteRecoveryPlan struct {
	State                     string   `json:"state"`
	ActivationEnabled         bool     `json:"activationEnabled"`
	Encrypted                 bool     `json:"encrypted"`
	ImmutableObjectLock       bool     `json:"immutableObjectLock"`
	CrossRegion               bool     `json:"crossRegion"`
	SeparateKMSCustody        bool     `json:"separateKmsCustody"`
	SeparateRestoreHost       bool     `json:"separateRestoreHost"`
	RestoreHostPublicOnlyKeys bool     `json:"restoreHostPublicOnlyKeys"`
	ProtectedRoots            []string `json:"protectedRoots"`
	RequiredEvidence          []string `json:"requiredEvidence"`
}

type AvailabilityPlan struct {
	State                   string        `json:"state"`
	ActivationEnabled       bool          `json:"activationEnabled"`
	TrafficShiftDefault     string        `json:"trafficShiftDefault"`
	AppReplicas             []ReplicaPlan `json:"appReplicas"`
	TURNReplicas            []ReplicaPlan `json:"turnReplicas"`
	HealthBasedRouting      bool          `json:"healthBasedRouting"`
	SessionDrainRequired    bool          `json:"sessionDrainRequired"`
	RetainPreviousRelease   bool          `json:"retainPreviousRelease"`
	MediaIndependentFromApp bool          `json:"mediaIndependentFromApp"`
	RollbackCommandRequired bool          `json:"rollbackCommandRequired"`
}

type ReplicaPlan struct {
	ID     string `json:"id"`
	Region string `json:"region"`
	State  string `json:"state"`
}

type CapabilityPage struct {
	Capability    string   `json:"capability"`
	Owner         string   `json:"owner"`
	Runbook       string   `json:"runbook"`
	PageOn        []string `json:"pageOn"`
	FailMode      string   `json:"failMode"`
	AggregateOnly bool     `json:"aggregateOnly"`
}

type WorkerIsolationPolicy struct {
	SchemaVersion            string                     `json:"schemaVersion"`
	PolicyID                 string                     `json:"policyId"`
	Mode                     string                     `json:"mode"`
	ActivationDefault        string                     `json:"activationDefault"`
	ProductionMutation       bool                       `json:"productionMutation"`
	CurrentDeployment        WorkerDeploymentState      `json:"currentDeployment"`
	RequiredBoundary         WorkerBoundaryRequirements `json:"requiredBoundary"`
	ExternalEvidenceRequired []string                   `json:"externalEvidenceRequired"`
}

// WorkerDeploymentState records only the executable repository state. The
// E9 policy is intentionally not allowed to turn target controls into claims
// about a worker that does not exist.
type WorkerDeploymentState struct {
	State                       string   `json:"state"`
	ComposeRunnerService        string   `json:"composeRunnerService"`
	ComposeExecutorInstalled    bool     `json:"composeExecutorInstalled"`
	BinaryRunnerMode            string   `json:"binaryRunnerMode"`
	RunnerImageTarget           string   `json:"runnerImageTarget"`
	InProcessSelection          string   `json:"inProcessSelection"`
	ProviderCredentialInjection bool     `json:"providerCredentialInjection"`
	QueueMountedIntoWorker      bool     `json:"queueMountedIntoWorker"`
	WorkspaceMountedIntoWorker  bool     `json:"workspaceMountedIntoWorker"`
	AllowedComposeServices      []string `json:"allowedComposeServices"`
}

// WorkerBoundaryRequirements is the E10 installation target, not evidence of
// current enforcement. Every field remains externally pending while the
// repository deployment has no worker executor.
type WorkerBoundaryRequirements struct {
	State               string           `json:"state"`
	Executor            string           `json:"executor"`
	EphemeralPerRun     bool             `json:"ephemeralPerRun"`
	ReadOnlyRoot        bool             `json:"readOnlyRoot"`
	WritableMounts      []string         `json:"writableMounts"`
	DeniedMounts        []string         `json:"deniedMounts"`
	Egress              EgressPolicy     `json:"egress"`
	Credentials         CredentialPolicy `json:"credentials"`
	Resources           ResourcePolicy   `json:"resources"`
	Callback            CallbackPolicy   `json:"callback"`
	NoCompanyBrainMount bool             `json:"noCompanyBrainMount"`
	NoProductionVolume  bool             `json:"noProductionVolume"`
	EnvironmentDenylist []string         `json:"environmentDenylist"`
}

type EgressPolicy struct {
	State               string   `json:"state"`
	Enforcement         string   `json:"enforcement"`
	ComposeEnforced     bool     `json:"composeEnforced"`
	DefaultDenyRequired bool     `json:"defaultDenyRequired"`
	AllowedHosts        []string `json:"allowedHosts"`
	IPLiteralDeny       bool     `json:"ipLiteralDeny"`
	PrivateNetDeny      bool     `json:"privateNetworkDeny"`
}

type CredentialPolicy struct {
	State            string   `json:"state"`
	Issuance         string   `json:"issuance"`
	ShortLived       bool     `json:"shortLived"`
	MaximumTTLSecond int      `json:"maximumTtlSeconds"`
	RunBound         bool     `json:"runBound"`
	AllowedScopes    []string `json:"allowedScopes"`
}

type ResourcePolicy struct {
	CPU             float64 `json:"cpu"`
	MemoryMiB       int     `json:"memoryMiB"`
	PIDs            int     `json:"pids"`
	WallTimeSeconds int     `json:"wallTimeSeconds"`
	NetworkBytes    int64   `json:"networkBytes"`
}

type CallbackPolicy struct {
	Signed             bool `json:"signed"`
	RunIDBound         bool `json:"runIdBound"`
	AudienceBound      bool `json:"audienceBound"`
	NonceRequired      bool `json:"nonceRequired"`
	MaximumSkewSeconds int  `json:"maximumSkewSeconds"`
	ReplayCache        bool `json:"replayCache"`
}

type WorkerCandidateSources struct {
	Compose          []byte
	Dockerfile       []byte
	Main             []byte
	CodexRunner      []byte
	AgentRunnerIface []byte
}

// ContractDrillManifest records assertions that the E9 contract state machine
// can evaluate. DeclaredCoverage is a checklist of future product/runtime
// evidence, not evidence that those systems ran in this process.
type ContractDrillManifest struct {
	SchemaVersion      string           `json:"schemaVersion"`
	DrillID            string           `json:"drillId"`
	EvidenceClass      string           `json:"evidenceClass"`
	Clock              string           `json:"clock"`
	ProviderCalls      bool             `json:"providerCalls"`
	ProductionMutation bool             `json:"productionMutation"`
	TrafficShift       string           `json:"trafficShift"`
	FrozenRouteMap     bool             `json:"frozenRouteMap"`
	Rooms              []DrillRoom      `json:"rooms"`
	Actors             []DrillActor     `json:"actors"`
	DeclaredCoverage   []string         `json:"declaredCoverage"`
	Consultations      ConsultationPlan `json:"consultations"`
	Soak               SoakPlan         `json:"soak"`
	Scenarios          []DrillScenario  `json:"scenarios"`
	WorkforceSteps     []string         `json:"workforceSteps"`
	ExternalPending    []string         `json:"externalPending"`
}

type ConsultationPlan struct {
	ScoutID       string `json:"scoutId"`
	SpecialistID  string `json:"specialistId"`
	RoundsPerRoom int    `json:"roundsPerRoom"`
	Concurrent    bool   `json:"concurrent"`
}

type DrillRoom struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
}

type DrillActor struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Capability string `json:"capability"`
}

type SoakPlan struct {
	VirtualHours int `json:"virtualHours"`
	Sittings     int `json:"sittings"`
}

type DrillScenario struct {
	ID         string   `json:"id"`
	RoomID     string   `json:"roomId,omitempty"`
	Fault      string   `json:"fault"`
	Assertions []string `json:"assertions"`
}

// DecodeStrict rejects trailing JSON and unknown fields so a misspelled safety
// control can never be silently ignored.
func DecodeStrict[T any](reader io.Reader) (T, error) {
	var value T
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, errors.New("trailing JSON value is not allowed")
		}
		return value, err
	}
	return value, nil
}

func DecodeBytes[T any](input []byte) (T, error) {
	return DecodeStrict[T](bytes.NewReader(input))
}

func ValidateReadinessManifest(manifest ReadinessManifest) error {
	var problems []string
	require := func(ok bool, message string) {
		if !ok {
			problems = append(problems, message)
		}
	}
	require(manifest.SchemaVersion == ReadinessSchema, "schemaVersion must be "+ReadinessSchema)
	require(validID(manifest.ManifestID), "manifestId is invalid")
	require(manifest.Mode == "plan_only", "mode must be plan_only before E10")
	require(manifest.ActivationDefault == "off", "activationDefault must be off")
	require(!manifest.ProductionMutation, "productionMutation must be false")
	requirePlanState := func(name, state string, activation bool) {
		require(state == "external_pending", name+" state must be external_pending")
		require(!activation, name+" activation must remain disabled")
	}
	requirePlanState("managedPostgres", manifest.ManagedPostgres.State, manifest.ManagedPostgres.ActivationEnabled)
	require(manifest.ManagedPostgres.MinimumNodes >= 2, "managedPostgres requires at least two planned nodes")
	require(manifest.ManagedPostgres.MultiZone, "managedPostgres must plan multi-zone placement")
	require(manifest.ManagedPostgres.AutomaticFailover, "managedPostgres must plan automatic failover")
	require(manifest.ManagedPostgres.PurgeAuthorityOutside, "purge authority must remain outside PostgreSQL HA")
	require(len(manifest.ManagedPostgres.RequiredEvidence) >= 3, "managedPostgres required evidence is incomplete")
	requirePlanState("offsiteRecovery", manifest.OffsiteRecovery.State, manifest.OffsiteRecovery.ActivationEnabled)
	require(manifest.OffsiteRecovery.Encrypted, "offsite recovery must be encrypted")
	require(manifest.OffsiteRecovery.ImmutableObjectLock, "offsite recovery must use immutable object lock")
	require(manifest.OffsiteRecovery.CrossRegion, "offsite recovery must be cross-region")
	require(manifest.OffsiteRecovery.SeparateKMSCustody, "KMS custody must be separate")
	require(manifest.OffsiteRecovery.SeparateRestoreHost, "restore host must be separate")
	require(manifest.OffsiteRecovery.RestoreHostPublicOnlyKeys, "restore host may receive public verification keys only")
	require(exactSet(manifest.OffsiteRecovery.ProtectedRoots, []string{"canonical_postgres", "meeting_data", "codex_queue", "usage_ledger"}), "offsite recovery must name exactly the four protected roots")
	require(len(manifest.OffsiteRecovery.RequiredEvidence) >= 4, "offsite recovery required evidence is incomplete")
	requirePlanState("availability", manifest.Availability.State, manifest.Availability.ActivationEnabled)
	require(manifest.Availability.TrafficShiftDefault == "off", "traffic shift must default off")
	require(len(manifest.Availability.AppReplicas) >= 2, "at least two app replicas must be planned")
	require(len(manifest.Availability.TURNReplicas) >= 2, "at least two TURN replicas must be planned")
	require(uniqueReplicaRegions(manifest.Availability.AppReplicas), "app replicas must have unique ids and at least two regions")
	require(uniqueReplicaRegions(manifest.Availability.TURNReplicas), "TURN replicas must have unique ids and at least two regions")
	require(manifest.Availability.HealthBasedRouting, "health-based routing is required")
	require(manifest.Availability.SessionDrainRequired, "session drain is required")
	require(manifest.Availability.RetainPreviousRelease, "previous release retention is required")
	require(manifest.Availability.MediaIndependentFromApp, "media routing must be independent from app routing")
	require(manifest.Availability.RollbackCommandRequired, "a rollback command is required before activation")
	if err := validatePaging(manifest.Paging); err != nil {
		problems = append(problems, err.Error())
	}
	require(len(manifest.ExternalEvidenceRequired) >= 5, "external evidence requirements are incomplete")
	return joinedProblems(problems)
}

func ValidateWorkerIsolation(policy WorkerIsolationPolicy) error {
	var problems []string
	require := func(ok bool, message string) {
		if !ok {
			problems = append(problems, message)
		}
	}
	require(policy.SchemaVersion == WorkerSchema, "schemaVersion must be "+WorkerSchema)
	require(validID(policy.PolicyID), "policyId is invalid")
	require(policy.Mode == "plan_only", "mode must be plan_only before E10")
	require(policy.ActivationDefault == "off", "activationDefault must be off")
	require(!policy.ProductionMutation, "productionMutation must be false")

	current := policy.CurrentDeployment
	require(current.State == "disabled", "current deployment state must be disabled")
	require(current.ComposeRunnerService == "absent", "current Compose runner service must be absent")
	require(!current.ComposeExecutorInstalled, "current Compose executor must not be installed")
	require(current.BinaryRunnerMode == "absent", "current binary runner mode must be absent")
	require(current.RunnerImageTarget == "absent", "current runner image target must be absent")
	require(current.InProcessSelection == "compile_time_disabled", "current in-process Codex selection must be compile_time_disabled")
	require(!current.ProviderCredentialInjection, "current deployment must not inject provider credentials into a worker")
	require(!current.QueueMountedIntoWorker, "current deployment must not mount the queue into a worker")
	require(!current.WorkspaceMountedIntoWorker, "current deployment must not mount a workspace into a worker")
	require(exactSet(current.AllowedComposeServices, []string{"meetingassist", "canonical-postgres", "render-queue-init", "render-runner", "coturn", "caddy"}), "current deployment must allow exactly the reviewed Compose services")

	required := policy.RequiredBoundary
	require(required.State == "external_pending", "required boundary state must be external_pending")
	require(required.Executor == "external_ephemeral_container_worktree", "required executor must be external_ephemeral_container_worktree")
	require(required.EphemeralPerRun, "required worker must be ephemeral per run")
	require(required.ReadOnlyRoot, "required worker root filesystem must be read-only")
	require(exactSet(required.WritableMounts, []string{"/tmp", "/workspace/run"}), "writable mounts must be exactly /tmp and /workspace/run")
	require(containsAll(required.DeniedMounts, []string{"/app/data", "/app/codex-queue", "/var/lib/docker", "/run/bonfire-dr", "/var/lib/postgresql"}), "denied mounts must cover production data, queue, Docker, DR evidence, and PostgreSQL")
	require(required.Egress.State == "external_pending", "egress state must be external_pending")
	require(required.Egress.Enforcement == "external_gateway_required", "egress enforcement must require an external gateway")
	require(!required.Egress.ComposeEnforced, "Compose must not be represented as enforcing default-deny egress")
	require(required.Egress.DefaultDenyRequired, "egress must require default deny")
	require(len(required.Egress.AllowedHosts) == 0, "token-free E9 must keep the egress allowlist empty")
	require(required.Egress.IPLiteralDeny && required.Egress.PrivateNetDeny, "egress must deny IP literals and private networks")
	require(required.Credentials.State == "external_pending", "credential state must be external_pending")
	require(required.Credentials.Issuance == "disabled", "credential issuance must remain disabled")
	require(required.Credentials.ShortLived && required.Credentials.RunBound, "credentials must be short-lived and run-bound when installed")
	require(required.Credentials.MaximumTTLSecond > 0 && required.Credentials.MaximumTTLSecond <= 900, "credential TTL must be 1..900 seconds")
	require(len(required.Credentials.AllowedScopes) > 0, "credential scopes must be explicit")
	require(required.Resources.CPU > 0 && required.Resources.CPU <= 4, "CPU quota must be in (0,4]")
	require(required.Resources.MemoryMiB >= 128 && required.Resources.MemoryMiB <= 4096, "memory quota must be 128..4096 MiB")
	require(required.Resources.PIDs >= 16 && required.Resources.PIDs <= 512, "PID quota must be 16..512")
	require(required.Resources.WallTimeSeconds > 0 && required.Resources.WallTimeSeconds <= 3600, "wall-time quota must be 1..3600 seconds")
	require(required.Resources.NetworkBytes > 0, "network byte quota must be positive")
	require(required.Callback.Signed && required.Callback.RunIDBound && required.Callback.AudienceBound && required.Callback.NonceRequired && required.Callback.ReplayCache, "callbacks must be signed, run/audience/nonce bound, and replay fenced")
	require(required.Callback.MaximumSkewSeconds > 0 && required.Callback.MaximumSkewSeconds <= 300, "callback skew must be 1..300 seconds")
	require(required.NoCompanyBrainMount, "company-brain mounts must be forbidden")
	require(required.NoProductionVolume, "production-volume mounts must be forbidden")
	require(containsAll(required.EnvironmentDenylist, []string{"DATABASE_URL", "BONFIRE_CANONICAL_DATABASE_URL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "BONFIRE_DR_ENVELOPE_KEY", "BONFIRE_DR_AUTHORITY_PRIVATE_KEY", "BONFIRE_DR_MANIFEST_PRIVATE_KEY"}), "environment denylist must include database, provider, and DR credentials")
	require(containsAll(policy.ExternalEvidenceRequired, requiredWorkerExternalEvidence), "worker external evidence requirements are incomplete")
	return joinedProblems(problems)
}

var requiredWorkerExternalEvidence = []string{
	"external_per_run_orchestrator_identity",
	"ephemeral_container_lifecycle_receipt",
	"read_only_root_and_mount_receipt",
	"default_deny_egress_gateway_receipt",
	"short_lived_run_bound_credential_receipt",
	"resource_and_network_quota_receipt",
	"callback_nonce_replay_receipt",
	"no_production_mount_receipt",
}

// AuditWorkerCompose closes the only executable repository path covered by the
// E9 policy: the production-style Compose file must not contain a reusable
// Codex executor. Compose is deliberately not treated as an egress allowlist;
// that control requires a separately installed and evidenced gateway.
func AuditWorkerCompose(policy WorkerIsolationPolicy, compose []byte) error {
	if err := ValidateWorkerIsolation(policy); err != nil {
		return err
	}
	services, err := composeServiceBlocks(compose)
	if err != nil {
		return err
	}
	if !exactSet(sortedKeys(services), policy.CurrentDeployment.AllowedComposeServices) {
		return fmt.Errorf("Compose service set differs from the reviewed worker-disabled candidate: got %v", sortedKeys(services))
	}
	var unsafe []string
	for name, body := range services {
		if looksLikeCodexExecutor(name, body) {
			unsafe = append(unsafe, name)
		}
	}
	if len(unsafe) > 0 {
		sort.Strings(unsafe)
		return fmt.Errorf("launchable Compose Codex executor is forbidden while worker isolation is external_pending: %s", strings.Join(unsafe, ", "))
	}
	return nil
}

// AuditWorkerCandidate binds the closed manifest to every repository launch
// surface that previously made the shared runner executable. These are source
// checks, not runtime sandbox receipts; their only positive claim is that the
// legacy executor cannot be launched from this candidate.
func AuditWorkerCandidate(policy WorkerIsolationPolicy, candidate WorkerCandidateSources) error {
	if err := AuditWorkerCompose(policy, candidate.Compose); err != nil {
		return err
	}
	if len(candidate.Dockerfile) == 0 || len(candidate.Main) == 0 || len(candidate.CodexRunner) == 0 || len(candidate.AgentRunnerIface) == 0 {
		return errors.New("worker candidate source set is incomplete")
	}
	for _, marker := range []string{" AS codex-runner", "@openai/codex", `"-codex-runner"`} {
		if bytes.Contains(candidate.Dockerfile, []byte(marker)) {
			return fmt.Errorf("Dockerfile retains forbidden Codex runner launch marker %q", marker)
		}
	}
	for _, marker := range []string{"codexRunnerWorker", `flag.Bool("codex-runner"`, "runCodexRunnerLoop("} {
		if bytes.Contains(candidate.Main, []byte(marker)) {
			return fmt.Errorf("main binary retains forbidden Codex runner launch marker %q", marker)
		}
	}
	if !bytes.Contains(candidate.CodexRunner, []byte("const codexExecutionProductionEnabled = false")) {
		return errors.New("Codex in-process execution is not compile-time disabled")
	}
	if bytes.Contains(candidate.CodexRunner, []byte("BONFIRE_CODEX_EXECUTION_ENABLED")) {
		return errors.New("Codex in-process execution has an environment activation path")
	}
	if !bytes.Contains(candidate.AgentRunnerIface, []byte("name = admittedAgentRunnerName(name)")) {
		return errors.New("agent runner selection lacks the final Codex admission choke point")
	}
	return nil
}

func composeServiceBlocks(compose []byte) (map[string]string, error) {
	services := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(compose))
	inServices := false
	current := ""
	var body strings.Builder
	flush := func() {
		if current != "" {
			services[current] = body.String()
		}
		current = ""
		body.Reset()
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\t ")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !inServices {
			if line == "services:" {
				inServices = true
			}
			continue
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			flush()
			break
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			separator := strings.IndexByte(trimmed, ':')
			if separator > 0 {
				candidate := strings.Trim(strings.TrimSpace(trimmed[:separator]), "'\"")
				if !validComposeServiceName(candidate) {
					return nil, fmt.Errorf("unsupported Compose service key %q", trimmed[:separator])
				}
				flush()
				current = candidate
				if inline := strings.TrimSpace(trimmed[separator+1:]); inline != "" {
					body.WriteString(inline)
					body.WriteByte('\n')
				}
				continue
			}
		}
		if current != "" {
			body.WriteString(trimmed)
			body.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Compose file: %w", err)
	}
	flush()
	if !inServices || len(services) == 0 {
		return nil, errors.New("Compose services section is missing or empty")
	}
	return services, nil
}

func looksLikeCodexExecutor(name, body string) bool {
	if strings.Contains(strings.ToLower(name), "codex") {
		return true
	}
	for _, marker := range []string{
		"target: codex-runner",
		"-codex-runner",
		"CODEX_API_KEY:",
		"BONFIRE_CODEX_CWD:",
		"BONFIRE_CODEX_MODEL:",
		"BONFIRE_CODEX_SANDBOX:",
		"/workspace/meetingassist",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func validComposeServiceName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ValidateContractDrillManifest(manifest ContractDrillManifest) error {
	var problems []string
	require := func(ok bool, message string) {
		if !ok {
			problems = append(problems, message)
		}
	}
	require(manifest.SchemaVersion == ContractDrillSchema, "schemaVersion must be "+ContractDrillSchema)
	require(validID(manifest.DrillID), "drillId is invalid")
	require(manifest.EvidenceClass == "synthetic_only", "evidenceClass must be synthetic_only")
	require(manifest.Clock == "virtual", "clock must be virtual")
	require(!manifest.ProviderCalls, "providerCalls must be false")
	require(!manifest.ProductionMutation, "productionMutation must be false")
	require(manifest.TrafficShift == "simulated_default_off", "trafficShift must be simulated_default_off")
	require(manifest.FrozenRouteMap, "route map must be frozen")
	require(len(manifest.Rooms) == 2 && uniqueRooms(manifest.Rooms), "exactly two distinct synthetic rooms are required")
	require(hasActor(manifest.Actors, "scout", "coordinator"), "Scout coordinator actor is required")
	require(hasActor(manifest.Actors, "mary", "specialist"), "Mary specialist actor is required")
	require(exactSet(manifest.DeclaredCoverage, []string{"media", "recall", "team", "scout", "suggested_work", "insights_opportunities", "agent_marketplace", "workforce"}), "declared product/runtime coverage checklist is incomplete")
	require(manifest.Consultations.ScoutID == "scout" && manifest.Consultations.SpecialistID == "mary", "consultations must bind Scout and Mary")
	require(manifest.Consultations.RoundsPerRoom >= 2, "consultations must repeat in each room")
	require(manifest.Consultations.Concurrent, "consultations must exercise concurrent rooms")
	require(manifest.Soak.VirtualHours == 24 && manifest.Soak.Sittings == 10, "soak plan must declare 24 virtual hours and ten sittings")
	require(exactSet(scenarioFaults(manifest.Scenarios), requiredScenarioFaults), "failure scenarios are incomplete or contain unknown faults")
	for _, scenario := range manifest.Scenarios {
		require(validID(scenario.ID), "scenario id is invalid")
		require(len(scenario.Assertions) > 0, "scenario "+scenario.ID+" has no assertions")
		if scenario.RoomID != "" {
			require(roomExists(manifest.Rooms, scenario.RoomID), "scenario "+scenario.ID+" references an unknown room")
		}
	}
	require(exactSet(manifest.WorkforceSteps, requiredWorkforceSteps), "workforce lifecycle is incomplete or reordered/extended incorrectly")
	require(containsAll(manifest.ExternalPending, []string{"paid_provider_qualification", "physical_device_acceptance", "production_restore", "live_traffic_shift", "live_24h_soak"}), "external pending list is incomplete")
	return joinedProblems(problems)
}

var requiredScenarioFaults = []string{
	"app_replica_loss", "consent_withdrawal", "control_failover", "participant_churn",
	"quota_exhaustion", "realtime_disconnect", "restore_tamper", "specialist_kill_switch",
}

var requiredWorkforceSteps = []string{
	"discover_mary", "inspect_mary", "trial_mary", "hire_bounded", "direct_message",
	"add_to_dog_perfect", "scout_select", "scout_introduce", "invite_to_meeting",
	"approve_one_workrun", "inspect_learning", "correct_learning", "preview_update",
	"rollback_update", "pause", "offboard", "verify_history_attributable", "verify_new_access_revoked",
}

func validatePaging(pages []CapabilityPage) error {
	seen := map[string]bool{}
	for _, page := range pages {
		if seen[page.Capability] {
			return fmt.Errorf("duplicate paging capability %q", page.Capability)
		}
		seen[page.Capability] = true
		if strings.TrimSpace(page.Owner) == "" || !strings.HasPrefix(page.Runbook, "docs/e9-operations-runbook.md#") || len(page.PageOn) == 0 || page.FailMode == "" || page.AggregateOnly {
			return fmt.Errorf("paging capability %q lacks owner, runbook, signal, fail mode, or uses aggregate-only health", page.Capability)
		}
	}
	if !exactSet(mapKeys(seen), requiredCapabilities) {
		return fmt.Errorf("paging matrix must cover exactly %v", requiredCapabilities)
	}
	return nil
}

func uniqueReplicaRegions(replicas []ReplicaPlan) bool {
	ids, regions := map[string]bool{}, map[string]bool{}
	for _, replica := range replicas {
		if !validID(replica.ID) || strings.TrimSpace(replica.Region) == "" || replica.State != "planned" || ids[replica.ID] {
			return false
		}
		ids[replica.ID], regions[replica.Region] = true, true
	}
	return len(regions) >= 2
}

func uniqueRooms(rooms []DrillRoom) bool {
	seen := map[string]bool{}
	for _, room := range rooms {
		if !validID(room.ID) || !validID(room.TenantID) || seen[room.ID] {
			return false
		}
		seen[room.ID] = true
	}
	return true
}

func roomExists(rooms []DrillRoom, id string) bool {
	for _, room := range rooms {
		if room.ID == id {
			return true
		}
	}
	return false
}

func hasActor(actors []DrillActor, id, kind string) bool {
	for _, actor := range actors {
		if actor.ID == id && actor.Kind == kind && actor.Capability != "" {
			return true
		}
	}
	return false
}

func scenarioFaults(scenarios []DrillScenario) []string {
	result := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		result = append(result, scenario.Fault)
	}
	return result
}

func validID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func exactSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	a, e := append([]string(nil), actual...), append([]string(nil), expected...)
	sort.Strings(a)
	sort.Strings(e)
	for index := range a {
		if a[index] != e[index] || (index > 0 && a[index] == a[index-1]) {
			return false
		}
	}
	return true
}

func containsAll(actual, expected []string) bool {
	seen := map[string]bool{}
	for _, value := range actual {
		seen[value] = true
	}
	for _, value := range expected {
		if !seen[value] {
			return false
		}
	}
	return true
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func joinedProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}
