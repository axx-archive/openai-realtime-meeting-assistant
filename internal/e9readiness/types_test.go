package e9readiness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture[T any](t *testing.T, name string) T {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "deploy", "e9", name))
	if err != nil {
		t.Fatal(err)
	}
	value, err := DecodeBytes[T](content)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRepositoryManifestsValidateAndRemainClosed(t *testing.T) {
	readiness := fixture[ReadinessManifest](t, "readiness-plan.json")
	if err := ValidateReadinessManifest(readiness); err != nil {
		t.Fatal(err)
	}
	report := EvaluateReadiness(readiness)
	if !report.ContractValid || report.ActivationReady || report.State != "external_pending" {
		t.Fatalf("unexpected readiness report: %+v", report)
	}
	if len(report.Blockers) < 5 {
		t.Fatalf("external blockers disappeared: %+v", report.Blockers)
	}
	runbook, err := os.ReadFile(filepath.Join("..", "..", "docs", "e9-operations-runbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range readiness.Paging {
		heading := "### " + strings.ToUpper(page.Capability[:1]) + page.Capability[1:]
		if !bytes.Contains(runbook, []byte(heading)) {
			t.Fatalf("paging runbook anchor for %s has no matching heading %q", page.Capability, heading)
		}
	}

	worker := fixture[WorkerIsolationPolicy](t, "worker-isolation-policy.json")
	if err := ValidateWorkerIsolation(worker); err != nil {
		t.Fatal(err)
	}
	workerReport := EvaluateWorkerIsolation(worker, repositoryWorkerCandidate(t))
	if !workerReport.ContractValid || !workerReport.DeploymentClosed || workerReport.ActivationReady || workerReport.State != "external_pending" || workerReport.ComposeEgressEnforced {
		t.Fatalf("worker report crossed its authority boundary: %+v", workerReport)
	}
	if len(workerReport.Blockers) < 8 {
		t.Fatalf("worker external blockers disappeared: %+v", workerReport.Blockers)
	}

	drill := fixture[ContractDrillManifest](t, "contract-drill.json")
	receipt, err := ExecuteContractDrill(drill)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ProductionReady || !receipt.Synthetic || receipt.ProviderCalls || receipt.ProductionMutation {
		t.Fatalf("contract receipt crossed its authority boundary: %+v", receipt)
	}
	if receipt.State != "contract_only" || len(receipt.ExecutedSystems) != 2 || len(receipt.ClaimsExcluded) < 5 {
		t.Fatalf("contract drill overstated or omitted its evidence boundary: %+v", receipt)
	}
	if !receipt.HumanCallsContinued || !receipt.RoomIsolationPreserved || !receipt.NewWorkerAccessRevoked || !receipt.HistoryAttributable {
		t.Fatalf("synthetic safety invariants missing: %+v", receipt)
	}
}

func TestReadinessFailsClosedOnUnknownOrUnsafeConfiguration(t *testing.T) {
	base := fixture[ReadinessManifest](t, "readiness-plan.json")
	tests := []struct {
		name   string
		mutate func(*ReadinessManifest)
		want   string
	}{
		{"traffic shift enabled", func(value *ReadinessManifest) { value.Availability.ActivationEnabled = true }, "activation must remain disabled"},
		{"single app replica", func(value *ReadinessManifest) { value.Availability.AppReplicas = value.Availability.AppReplicas[:1] }, "at least two app replicas"},
		{"aggregate paging", func(value *ReadinessManifest) { value.Paging[0].AggregateOnly = true }, "aggregate-only"},
		{"missing protected root", func(value *ReadinessManifest) {
			value.OffsiteRecovery.ProtectedRoots = value.OffsiteRecovery.ProtectedRoots[:3]
		}, "exactly the four protected roots"},
		{"restore host private keys", func(value *ReadinessManifest) { value.OffsiteRecovery.RestoreHostPublicOnlyKeys = false }, "public verification keys only"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.Paging = append([]CapabilityPage(nil), base.Paging...)
			value.Availability.AppReplicas = append([]ReplicaPlan(nil), base.Availability.AppReplicas...)
			value.OffsiteRecovery.ProtectedRoots = append([]string(nil), base.OffsiteRecovery.ProtectedRoots...)
			test.mutate(&value)
			err := ValidateReadinessManifest(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
			if EvaluateReadiness(value).ActivationReady {
				t.Fatal("invalid manifest became activation-ready")
			}
		})
	}

	unknown := bytes.ReplaceAll(mustRead(t, "readiness-plan.json"), []byte(`"manifestId"`), []byte(`"unknownSafetyField": true, "manifestId"`))
	if _, err := DecodeBytes[ReadinessManifest](unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field was accepted: %v", err)
	}
}

func TestWorkerIsolationFailsClosed(t *testing.T) {
	base := fixture[WorkerIsolationPolicy](t, "worker-isolation-policy.json")
	tests := []struct {
		name   string
		mutate func(*WorkerIsolationPolicy)
		want   string
	}{
		{"activation enabled", func(value *WorkerIsolationPolicy) { value.ActivationDefault = "on" }, "activationDefault"},
		{"executor installed", func(value *WorkerIsolationPolicy) { value.CurrentDeployment.ComposeExecutorInstalled = true }, "must not be installed"},
		{"binary runner mode", func(value *WorkerIsolationPolicy) { value.CurrentDeployment.BinaryRunnerMode = "present" }, "binary runner mode"},
		{"runner image target", func(value *WorkerIsolationPolicy) { value.CurrentDeployment.RunnerImageTarget = "present" }, "runner image target"},
		{"in-process activation", func(value *WorkerIsolationPolicy) { value.CurrentDeployment.InProcessSelection = "enabled" }, "compile_time_disabled"},
		{"provider credential injection", func(value *WorkerIsolationPolicy) { value.CurrentDeployment.ProviderCredentialInjection = true }, "must not inject"},
		{"unreviewed Compose service", func(value *WorkerIsolationPolicy) {
			value.CurrentDeployment.AllowedComposeServices = append(value.CurrentDeployment.AllowedComposeServices, "worker")
		}, "reviewed Compose services"},
		{"production volume", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.NoProductionVolume = false }, "production-volume"},
		{"brain mount", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.NoCompanyBrainMount = false }, "company-brain"},
		{"pretend Compose egress", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.Egress.ComposeEnforced = true }, "must not be represented"},
		{"open egress", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.Egress.DefaultDenyRequired = false }, "default deny"},
		{"premature allowlist", func(value *WorkerIsolationPolicy) {
			value.RequiredBoundary.Egress.AllowedHosts = []string{"api.github.com"}
		}, "allowlist empty"},
		{"credential issuance", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.Credentials.Issuance = "enabled" }, "issuance must remain disabled"},
		{"long credential", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.Credentials.MaximumTTLSecond = 901 }, "credential TTL"},
		{"unfenced callback", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.Callback.ReplayCache = false }, "replay fenced"},
		{"unbounded memory", func(value *WorkerIsolationPolicy) { value.RequiredBoundary.Resources.MemoryMiB = 8192 }, "memory quota"},
		{"missing external evidence", func(value *WorkerIsolationPolicy) {
			value.ExternalEvidenceRequired = value.ExternalEvidenceRequired[:1]
		}, "external evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.RequiredBoundary.WritableMounts = append([]string(nil), base.RequiredBoundary.WritableMounts...)
			value.RequiredBoundary.DeniedMounts = append([]string(nil), base.RequiredBoundary.DeniedMounts...)
			value.RequiredBoundary.Egress.AllowedHosts = append([]string(nil), base.RequiredBoundary.Egress.AllowedHosts...)
			value.RequiredBoundary.Credentials.AllowedScopes = append([]string(nil), base.RequiredBoundary.Credentials.AllowedScopes...)
			value.RequiredBoundary.EnvironmentDenylist = append([]string(nil), base.RequiredBoundary.EnvironmentDenylist...)
			value.ExternalEvidenceRequired = append([]string(nil), base.ExternalEvidenceRequired...)
			value.CurrentDeployment.AllowedComposeServices = append([]string(nil), base.CurrentDeployment.AllowedComposeServices...)
			test.mutate(&value)
			if err := ValidateWorkerIsolation(value); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unsafe worker policy error=%v, want %q", err, test.want)
			}
			if EvaluateWorkerIsolation(value, workerCandidateForCompose(workerDisabledCompose(""))).ActivationReady {
				t.Fatal("unsafe worker policy became activation-ready")
			}
		})
	}

	unknown := bytes.ReplaceAll(mustRead(t, "worker-isolation-policy.json"), []byte(`"policyId"`), []byte(`"unknownEnforcementClaim": true, "policyId"`))
	if _, err := DecodeBytes[WorkerIsolationPolicy](unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown worker field was accepted: %v", err)
	}
	legacy := bytes.ReplaceAll(mustRead(t, "worker-isolation-policy.json"), []byte(WorkerSchema), []byte("stride.e9.worker-isolation/v1"))
	if value, err := DecodeBytes[WorkerIsolationPolicy](legacy); err != nil {
		t.Fatal(err)
	} else if err := ValidateWorkerIsolation(value); err == nil || !strings.Contains(err.Error(), WorkerSchema) {
		t.Fatalf("legacy worker schema was accepted: %v", err)
	}
}

func TestWorkerComposeAuditRejectsReusableOrObfuscatedCodexExecutor(t *testing.T) {
	policy := fixture[WorkerIsolationPolicy](t, "worker-isolation-policy.json")
	tests := []struct {
		name    string
		compose string
		service string
	}{
		{
			name:    "legacy named service",
			compose: string(workerDisabledCompose("  codex-runner:\n    image: runner\n")),
			service: "codex-runner",
		},
		{
			name:    "renamed build target",
			compose: string(workerDisabledCompose("  worker-17:\n    build:\n      target: codex-runner\n")),
			service: "worker-17",
		},
		{
			name:    "renamed provider workspace executor",
			compose: string(workerDisabledCompose("  worker-18:\n    environment:\n      CODEX_API_KEY: ${CODEX_API_KEY}\n    volumes:\n      - ../..:/workspace/meetingassist\n")),
			service: "worker-18",
		},
		{
			name:    "opaque extra worker",
			compose: string(workerDisabledCompose("  worker-19:\n    image: registry.example/opaque:latest\n")),
			service: "worker-19",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := AuditWorkerCompose(policy, []byte(test.compose))
			if err == nil || !strings.Contains(err.Error(), test.service) {
				t.Fatalf("unsafe Compose audit err=%v, want service %q", err, test.service)
			}
			report := EvaluateWorkerIsolation(policy, workerCandidateForCompose([]byte(test.compose)))
			if !report.ContractValid || report.DeploymentClosed || report.ActivationReady || report.State != "invalid" {
				t.Fatalf("unsafe Compose report=%+v", report)
			}
		})
	}
}

func TestWorkerComposeAuditAllowsQueueBrokerWithoutExecutor(t *testing.T) {
	policy := fixture[WorkerIsolationPolicy](t, "worker-isolation-policy.json")
	compose := workerDisabledCompose("")
	if err := AuditWorkerCompose(policy, compose); err != nil {
		t.Fatalf("control-plane queue broker was mistaken for an executor: %v", err)
	}
}

func TestWorkerCandidateAuditRejectsEveryLegacyLaunchSurface(t *testing.T) {
	policy := fixture[WorkerIsolationPolicy](t, "worker-isolation-policy.json")
	tests := []struct {
		name   string
		mutate func(*WorkerCandidateSources)
		want   string
	}{
		{"runner image target", func(value *WorkerCandidateSources) {
			value.Dockerfile = append(value.Dockerfile, []byte("FROM scratch AS codex-runner\n")...)
		}, "Dockerfile"},
		{"Codex CLI image", func(value *WorkerCandidateSources) {
			value.Dockerfile = append(value.Dockerfile, []byte("RUN npm install -g @openai/codex\n")...)
		}, "Dockerfile"},
		{"binary runner flag", func(value *WorkerCandidateSources) {
			value.Main = append(value.Main, []byte(`var worker = flag.Bool("codex-runner", false, "unsafe")`)...)
		}, "main binary"},
		{"runner loop", func(value *WorkerCandidateSources) {
			value.Main = append(value.Main, []byte("\nrunCodexRunnerLoop(ctx)\n")...)
		}, "main binary"},
		{"production gate enabled", func(value *WorkerCandidateSources) {
			value.CodexRunner = bytes.ReplaceAll(value.CodexRunner, []byte("const codexExecutionProductionEnabled = false"), []byte("const codexExecutionProductionEnabled = true"))
		}, "compile-time disabled"},
		{"environment activation", func(value *WorkerCandidateSources) {
			value.CodexRunner = append(value.CodexRunner, []byte("\nconst x = \"BONFIRE_CODEX_EXECUTION_ENABLED\"\n")...)
		}, "environment activation"},
		{"missing final choke point", func(value *WorkerCandidateSources) { value.AgentRunnerIface = []byte("package main\n") }, "choke point"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := workerCandidateForCompose(workerDisabledCompose(""))
			test.mutate(&candidate)
			if err := AuditWorkerCandidate(policy, candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("legacy launch surface error=%v, want %q", err, test.want)
			}
			if report := EvaluateWorkerIsolation(policy, candidate); report.DeploymentClosed || report.ActivationReady {
				t.Fatalf("legacy launch surface passed: %+v", report)
			}
		})
	}
}

func workerDisabledCompose(extra string) []byte {
	return []byte("services:\n" +
		"  meetingassist:\n    environment:\n      BONFIRE_CODEX_QUEUE_PATH: /app/codex-queue/jobs\n    volumes:\n      - codex_queue:/app/codex-queue\n" +
		"  canonical-postgres:\n    image: postgres\n" +
		"  render-queue-init:\n    image: render\n" +
		"  render-runner:\n    image: render\n" +
		"  coturn:\n    image: coturn\n" +
		"  caddy:\n    image: caddy\n" + extra)
}

func workerCandidateForCompose(compose []byte) WorkerCandidateSources {
	return WorkerCandidateSources{
		Compose:          compose,
		Dockerfile:       []byte("FROM scratch AS meetingassist-runtime\n"),
		Main:             []byte("package main\n"),
		CodexRunner:      []byte("package main\nconst codexExecutionProductionEnabled = false\n"),
		AgentRunnerIface: []byte("package main\nfunc selectRunner() { name = admittedAgentRunnerName(name) }\n"),
	}
}

func repositoryWorkerCandidate(t *testing.T) WorkerCandidateSources {
	t.Helper()
	read := func(parts ...string) []byte {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(parts...))
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	return WorkerCandidateSources{
		Compose:          read("..", "..", "deploy", "digitalocean", "docker-compose.yml"),
		Dockerfile:       read("..", "..", "Dockerfile"),
		Main:             read("..", "..", "main.go"),
		CodexRunner:      read("..", "..", "codex_runner.go"),
		AgentRunnerIface: read("..", "..", "agent_runner_iface.go"),
	}
}

func TestSyntheticDrillRejectsMissingSafetyAssertionAndLifecycleReorder(t *testing.T) {
	base := fixture[ContractDrillManifest](t, "contract-drill.json")

	missing := base
	missing.Scenarios = append([]DrillScenario(nil), base.Scenarios...)
	missing.Scenarios[0].Assertions = []string{"route_failover_expected"}
	if _, err := ExecuteContractDrill(missing); err == nil || !strings.Contains(err.Error(), "omits required safety assertions") {
		t.Fatalf("missing safety assertions were accepted: %v", err)
	}

	reordered := base
	reordered.WorkforceSteps = append([]string(nil), base.WorkforceSteps...)
	reordered.WorkforceSteps[2], reordered.WorkforceSteps[3] = reordered.WorkforceSteps[3], reordered.WorkforceSteps[2]
	if _, err := ExecuteContractDrill(reordered); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("reordered workforce lifecycle was accepted: %v", err)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "deploy", "e9", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}
