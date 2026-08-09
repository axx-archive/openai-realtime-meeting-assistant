package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type strideE10TenantEnvelopeTestKeyring struct {
	mu      sync.RWMutex
	current StrideE10TenantAuthorityEnvelopeKey
	keys    map[string]StrideE10TenantAuthorityEnvelopeKey
}

func (k *strideE10TenantEnvelopeTestKeyring) CurrentStrideE10TenantAuthorityEnvelopeKey(context.Context) (StrideE10TenantAuthorityEnvelopeKey, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.current, nil
}

func (k *strideE10TenantEnvelopeTestKeyring) ResolveStrideE10TenantAuthorityEnvelopeKey(_ context.Context, id string, version uint64) (StrideE10TenantAuthorityEnvelopeKey, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[id]
	if !ok || key.Version != version {
		return StrideE10TenantAuthorityEnvelopeKey{}, errors.New("unknown envelope key")
	}
	return key, nil
}

func strideE10TenantEnvelopeTestSetup(t *testing.T) (*StrideE10TenantConverter, *strideE10TenantTestGate, *strideE10TenantTestResolver, *strideE10TenantEnvelopeTestKeyring, StrideE10TenantAuthorityEnvelope) {
	t.Helper()
	now := time.Now().UTC()
	converter, gate, resolver, _ := strideE10TenantTestConverter(now, true, StrideE10TenantConversionCutover)
	key := StrideE10TenantAuthorityEnvelopeKey{ID: "tenant-envelope-test", Version: 1, Secret: []byte(strings.Repeat("tenant-envelope-test/", 2))}
	keys := &strideE10TenantEnvelopeTestKeyring{current: key, keys: map[string]StrideE10TenantAuthorityEnvelopeKey{key.ID: key}}
	restoreKeys := InstallStrideE10TenantAuthorityEnvelopeRuntime(keys)
	restoreConverter := InstallStrideE10TenantRuntimeConverter(converter)
	t.Cleanup(restoreKeys)
	t.Cleanup(restoreConverter)
	purpose := StrideE10TenantAuthorityPurposeForCodexJob("artifact-one", "thread-one", "research", "summarize the bounded source", codexJobAuthorityReadOnly)
	envelope, err := MintStrideE10TenantAuthorityEnvelope(context.Background(), converter, strings.Repeat("a", 64), purpose, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("mint envelope: %v", err)
	}
	return converter, gate, resolver, keys, envelope
}

func strideE10TenantEnvelopeTestJob(envelope *StrideE10TenantAuthorityEnvelope) codexRunnerJob {
	return codexRunnerJob{
		ID: "tenant-job-one", ArtifactID: "artifact-one", ThreadID: "thread-one", Mode: "research",
		Query: "summarize the bounded source", Prompt: "bounded provider prompt", Authority: codexJobAuthorityReadOnly,
		Status: codexJobStatusQueued, CreatedAt: time.Now().UTC(), TenantAuthority: envelope,
	}
}

func TestStrideE10TenantEnvelopeMintPersistsBodyFreeAndRejectsLegacyOrTamper(t *testing.T) {
	_, _, _, _, envelope := strideE10TenantEnvelopeTestSetup(t)
	store := newCodexRunnerJobStore(t.TempDir())
	job := strideE10TenantEnvelopeTestJob(&envelope)
	queued, err := store.enqueue(job)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.jobPath(queued.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("@"), []byte("legacy@example.com"), []byte("sessionToken"), []byte("password")} {
		if bytes.Contains(bytes.ToLower(raw), bytes.ToLower(forbidden)) {
			t.Fatalf("durable job leaked raw authority %q: %s", forbidden, raw)
		}
	}
	if !bytes.Contains(raw, []byte(envelope.SessionSubjectDigest)) || !bytes.Contains(raw, []byte(`"tenant_authority"`)) {
		t.Fatalf("durable job omitted opaque authority envelope: %s", raw)
	}

	legacy := strideE10TenantEnvelopeTestJob(nil)
	legacy.ID = "legacy-job"
	if _, err := store.enqueue(legacy); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("cutover accepted legacy job: %v", err)
	}
	tampered := envelope
	tampered.PersonID = "person-attacker"
	tamperJob := strideE10TenantEnvelopeTestJob(&tampered)
	tamperJob.ID = "tampered-job"
	if _, err := store.enqueue(tamperJob); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("cutover accepted tampered envelope: %v", err)
	}
	transferred := strideE10TenantEnvelopeTestJob(&envelope)
	transferred.ID = "transferred-job"
	transferred.ThreadID = "thread-other"
	if _, err := store.enqueue(transferred); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("cutover accepted envelope copied to another action: %v", err)
	}
	legacyAuthority := strideE10TenantEnvelopeTestJob(&envelope)
	legacyAuthority.ID = "legacy-authority-job"
	legacyAuthority.ThreadMetadata = map[string]string{"requestedBy": "legacy@example.com"}
	if _, err := store.enqueue(legacyAuthority); !errors.Is(err, ErrStrideE10TenantAuthorityInvalid) {
		t.Fatalf("cutover persisted legacy email authority: %v", err)
	}
	var persisted codexRunnerJob
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	persisted.TenantAuthority.PersonID = "person-attacker"
	if err := writeJSONFileAtomically(store.jobPath(persisted.ID), "tampered tenant job", persisted); err != nil {
		t.Fatal(err)
	}
	later := strideE10TenantEnvelopeTestJob(&envelope)
	later.ID = "zz-later-authorized-job"
	if _, err := store.enqueue(later); err != nil {
		t.Fatal(err)
	}
	restarted := newCodexRunnerJobStore(store.dir)
	previousCallback := sendCodexRunnerCallback
	callbackCalls := 0
	sendCodexRunnerCallback = func(context.Context, codexRunnerCallbackPayload) error {
		callbackCalls++
		return nil
	}
	defer func() { sendCodexRunnerCallback = previousCallback }()
	if claimed, err := restarted.claimNextAt("runner", time.Now().UTC(), time.Minute); err != nil || claimed != nil {
		t.Fatalf("restart accepted persisted envelope tamper: job=%+v err=%v", claimed, err)
	}
	quarantined, err := restarted.read(filepath.Base(restarted.jobPath(persisted.ID)))
	if err != nil || quarantined.Status != codexJobStatusFailed || quarantined.Error != ErrStrideE10TenantAuthorityStale.Error() {
		t.Fatalf("tampered job was not durably quarantined: %+v %v", quarantined, err)
	}
	if callbackCalls != 0 {
		t.Fatalf("tampered queued authority emitted %d callbacks", callbackCalls)
	}
	claimed, err := restarted.claimNextAt("runner", time.Now().UTC(), time.Minute)
	if err != nil || claimed == nil || claimed.ID != later.ID {
		t.Fatalf("quarantined head-of-line job blocked later work: job=%+v err=%v", claimed, err)
	}
}

func TestStrideE10TenantEnvelopeClaimRestartExpiryRevisionAndSwitchFences(t *testing.T) {
	_, gate, resolver, _, envelope := strideE10TenantEnvelopeTestSetup(t)
	store := newCodexRunnerJobStore(t.TempDir())
	job := strideE10TenantEnvelopeTestJob(&envelope)
	if _, err := store.enqueue(job); err != nil {
		t.Fatal(err)
	}
	restarted := newCodexRunnerJobStore(store.dir)
	next := strideE10TenantTestSnapshot(time.Now().UTC())
	next.Membership.Header.Revision++
	next.Session.OrganizationMembershipRev++
	next.ActiveSession.MembershipRevision++
	resolver.set(next, nil)
	if claimed, err := restarted.claimNextAt("runner", time.Now().UTC(), time.Minute); err != nil || claimed != nil {
		t.Fatalf("stale membership revision claimed: job=%+v err=%v", claimed, err)
	}
	quarantined, err := restarted.read(filepath.Base(restarted.jobPath(job.ID)))
	if err != nil || quarantined.Status != codexJobStatusFailed {
		t.Fatalf("stale revision was not durably quarantined: %+v %v", quarantined, err)
	}
	resolver.set(strideE10TenantTestSnapshot(time.Now().UTC()), nil)
	expiring := strideE10TenantEnvelopeTestJob(&envelope)
	expiring.ID = "tenant-job-expiring"
	if _, err := restarted.enqueue(expiring); err != nil {
		t.Fatal(err)
	}
	if claimed, err := restarted.claimNextAt("runner", envelope.ExpiresAt.Add(time.Second), time.Minute); err != nil || claimed != nil {
		t.Fatalf("expired envelope claimed: job=%+v err=%v", claimed, err)
	}
	switching := strideE10TenantEnvelopeTestJob(&envelope)
	switching.ID = "tenant-job-switching"
	if _, err := restarted.enqueue(switching); err != nil {
		t.Fatal(err)
	}
	gate.enabled.Store(false)
	if claimed, err := restarted.claimNextAt("runner", time.Now().UTC(), time.Minute); err != nil || claimed != nil {
		t.Fatalf("cutover switch fell back to legacy claim: job=%+v err=%v", claimed, err)
	}
	quarantined, err = restarted.read(filepath.Base(restarted.jobPath(switching.ID)))
	if err != nil || quarantined.Status != codexJobStatusFailed {
		t.Fatalf("switch race was not durably quarantined: %+v %v", quarantined, err)
	}
}

func TestStrideE10TenantEnvelopeProductionEnqueueRequiresAuthorizedIngress(t *testing.T) {
	_, _, _, _, envelope := strideE10TenantEnvelopeTestSetup(t)
	queueDir := t.TempDir()
	t.Setenv("BONFIRE_CODEX_QUEUE_PATH", queueDir)
	app := newIsolatedKanbanBoardApp(t)
	thread := scoutAgentThread{ID: "thread-one", Mode: "research", Query: "summarize the bounded source", Artifact: meetingMemoryEntry{ID: "artifact-one", Metadata: map[string]string{}}}
	job := app.newAgentJob(thread)
	if _, err := app.enqueueCodexAgentThreadJobWithContext(job, codexJobAuthorityReadOnly, "authorized-ingress-job"); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy production enqueue did not fail before row creation: %v", err)
	}
	if entries, err := os.ReadDir(queueDir); err == nil && len(entries) != 0 {
		t.Fatalf("legacy cutover enqueue created rows: %v", entries)
	}
	result, err := app.enqueueCodexAgentThreadJobWithContextAndTenantAuthority(job, codexJobAuthorityReadOnly, "authorized-ingress-job", &envelope)
	if err != nil || result.Metadata["runnerJobId"] != "authorized-ingress-job" {
		t.Fatalf("authorized ingress enqueue: %+v %v", result, err)
	}
	stored, err := newCodexRunnerJobStore(queueDir).read("authorized-ingress-job.json")
	if err != nil || stored.TenantAuthority == nil || stored.TenantAuthority.MAC != envelope.MAC {
		t.Fatalf("authorized ingress omitted envelope: %+v %v", stored, err)
	}
}

func TestStrideE10TenantEnvelopeWorkerRevalidatesAndHoldsThroughFinalEffect(t *testing.T) {
	_, gate, resolver, _, envelope := strideE10TenantEnvelopeTestSetup(t)
	store := newCodexRunnerJobStore(t.TempDir())
	job := strideE10TenantEnvelopeTestJob(&envelope)
	if _, err := store.enqueue(job); err != nil {
		t.Fatal(err)
	}
	previousClaimWrite := writeCodexRunnerClaimAtomically
	claimWriteEntered := make(chan struct{})
	releaseClaimWrite := make(chan struct{})
	writeCodexRunnerClaimAtomically = func(path, label string, value any) error {
		close(claimWriteEntered)
		<-releaseClaimWrite
		return previousClaimWrite(path, label, value)
	}
	t.Cleanup(func() { writeCodexRunnerClaimAtomically = previousClaimWrite })
	claimResult := make(chan *codexRunnerJob, 1)
	claimError := make(chan error, 1)
	go func() {
		claimed, claimErr := store.claimNextAt("runner", time.Now().UTC(), time.Minute)
		claimResult <- claimed
		claimError <- claimErr
	}()
	<-claimWriteEntered
	claimSwitch := make(chan struct{})
	go func() {
		resolver.set(strideE10TenantTestSnapshot(time.Now().UTC()), nil)
		close(claimSwitch)
	}()
	select {
	case <-claimSwitch:
		t.Fatal("authority switch interleaved before durable claim write")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseClaimWrite)
	claimed, err := <-claimResult, <-claimError
	writeCodexRunnerClaimAtomically = previousClaimWrite
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	select {
	case <-claimSwitch:
	case <-time.After(time.Second):
		t.Fatal("authority switch remained blocked after durable claim")
	}
	previousRunner := runCodexExecCommand
	previousCallback := sendCodexRunnerCallback
	callbackCalls := 0
	sendCodexRunnerCallback = func(context.Context, codexRunnerCallbackPayload) error {
		callbackCalls++
		return nil
	}
	providerEntered := make(chan struct{})
	releaseProvider := make(chan struct{})
	runCodexExecCommand = func(context.Context, codexExecConfig, string) (codexExecResult, error) {
		close(providerEntered)
		<-releaseProvider
		return codexExecResult{FinalMessage: "authorized result"}, nil
	}
	t.Cleanup(func() {
		runCodexExecCommand = previousRunner
		sendCodexRunnerCallback = previousCallback
	})
	done := make(chan struct{})
	go func() {
		processCodexRunnerJob(context.Background(), store, *claimed)
		close(done)
	}()
	<-providerEntered
	switched := make(chan struct{})
	go func() {
		next := strideE10TenantTestSnapshot(time.Now().UTC())
		next.Generation++
		resolver.set(next, nil)
		close(switched)
	}()
	select {
	case <-switched:
		t.Fatal("authority switch interleaved while provider/final effect was active")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseProvider)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not complete")
	}
	select {
	case <-switched:
	case <-time.After(time.Second):
		t.Fatal("authority resolver remained locked after final effect")
	}
	completed, err := store.read(filepath.Base(store.jobPath(claimed.ID)))
	if err != nil || completed.Status != codexJobStatusComplete {
		t.Fatalf("held worker final effect: %+v %v", completed, err)
	}
	if callbackCalls != 2 { // running + terminal, both held by current authority.
		t.Fatalf("authorized callback count=%d", callbackCalls)
	}

	// A second job claimed while current must not reach the provider after the
	// cutover gate is revoked between claim and worker admission.
	resolver.set(strideE10TenantTestSnapshot(time.Now().UTC()), nil)
	secondEnvelope := envelope
	secondEnvelope.ExpiresAt = time.Now().UTC().Add(time.Hour)
	secondEnvelope.MAC = strideE10TenantEnvelopeMAC(strideE10CurrentTenantEnvelopeRuntime().keys.(*strideE10TenantEnvelopeTestKeyring).current, secondEnvelope)
	second := strideE10TenantEnvelopeTestJob(&secondEnvelope)
	second.ID = "tenant-job-two"
	if _, err := store.enqueue(second); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.claimNextAt("runner", time.Now().UTC(), time.Minute)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	providerCalls := 0
	runCodexExecCommand = func(context.Context, codexExecConfig, string) (codexExecResult, error) {
		providerCalls++
		return codexExecResult{FinalMessage: "must not run"}, nil
	}
	gate.enabled.Store(false)
	processCodexRunnerJob(context.Background(), store, *claimed)
	if providerCalls != 0 || callbackCalls != 2 {
		t.Fatalf("cutover switch reached provider/callback through legacy fallback: provider=%d callback=%d", providerCalls, callbackCalls)
	}

	gate.enabled.Store(true)
	third := strideE10TenantEnvelopeTestJob(&secondEnvelope)
	third.ID = "tenant-job-three"
	if _, err := store.enqueue(third); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.claimNextAt("runner", time.Now().UTC(), time.Minute)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	resolver.set(StrideE10TenantAuthoritySnapshot{}, errors.New("session revoked before provider"))
	processCodexRunnerJob(context.Background(), store, *claimed)
	if providerCalls != 0 || callbackCalls != 2 {
		t.Fatalf("revoked session reached provider/callback: provider=%d callback=%d", providerCalls, callbackCalls)
	}
}

func TestStrideE10TenantEnvelopeUnavailableWithoutManagedRuntime(t *testing.T) {
	restore := InstallStrideE10TenantAuthorityEnvelopeRuntime(nil)
	defer restore()
	if err := validateStrideE10TenantAuthorityEnvelope(context.Background(), StrideE10TenantAuthorityEnvelope{}, time.Now()); !errors.Is(err, ErrStrideE10TenantAuthorityInvalid) {
		t.Fatalf("missing managed keyring accepted: %v", err)
	}
	raw, _ := json.Marshal(StrideE10TenantAuthorityEnvelope{})
	if bytes.Contains(raw, []byte("@")) {
		t.Fatal("zero envelope leaked authority")
	}
}

func TestStrideE10BrainLegacyScheduledReplayAndProjectionRootsFailClosed(t *testing.T) {
	_, _, _, _, _ = strideE10TenantEnvelopeTestSetup(t)
	app := newIsolatedKanbanBoardApp(t)
	beforeMemory := len(app.memory.snapshot(0))
	beforeBoard := app.snapshotState()
	providerCalls := 0
	responder := func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "must not run", nil
	}
	if _, err := app.runMeetingBrainOnce(context.Background(), "key", responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy scheduled brain cutover err=%v", err)
	}
	if _, err := app.produceMeetingBrainWriteUp(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy direct brain cutover err=%v", err)
	}
	if _, err := app.runMeetingBoardOnce(context.Background(), "key", responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy scheduled board cutover err=%v", err)
	}
	if _, err := app.produceMeetingBoardUpdate(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy direct board cutover err=%v", err)
	}
	if _, err := app.runResearchSuggestionOnce(context.Background(), "key", responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy scheduled research suggestion cutover err=%v", err)
	}
	if _, err := app.produceResearchSuggestions(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy direct research suggestion cutover err=%v", err)
	}
	if _, err := app.produceMissionInsight(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy mission intelligence cutover err=%v", err)
	}
	if _, err := app.produceNarrativeUpdates(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy narrative cutover err=%v", err)
	}
	if got := app.detectDecisionReversals(context.Background(), "key", responder, []extractedDecision{{Statement: "cross-org private decision"}}, []meetingMemoryEntry{{ID: "other-org", Text: "private"}}, nil, nil); got != nil {
		t.Fatalf("legacy decision reversal produced cutover result: %+v", got)
	}
	if _, err := app.produceDecisionLedgerPass(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy decision ledger cutover err=%v", err)
	}
	if _, err := app.produceMeetingDigests(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy meeting digest cutover err=%v", err)
	}
	if _, err := app.produceDayDigestPass(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy day digest cutover err=%v", err)
	}
	if _, err := app.runDayDigestPass(context.Background(), "key", nil, responder, time.Now().UTC()); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy direct day digest cutover err=%v", err)
	}
	if _, _, err := app.maybeEmitDailyReflection(context.Background(), "key", responder, time.Now().UTC()); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy daily reflection cutover err=%v", err)
	}
	if _, err := app.produceCompanyDigest(context.Background(), "key", nil, responder); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy company digest cutover err=%v", err)
	}
	if _, err := app.runCompanyDigestPass(context.Background(), "key", nil, responder, time.Now().UTC()); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy direct company digest cutover err=%v", err)
	}
	engine := &AmbientReplayEngine{}
	if _, err := engine.Plan(context.Background(), AmbientReplayPlanRequest{}); !errors.Is(err, ErrAmbientReplayUnavailable) {
		t.Fatalf("legacy replay plan cutover err=%v", err)
	}
	if _, err := engine.Execute(context.Background(), strings.Repeat("a", 64), "AJ"); !errors.Is(err, ErrAmbientReplayUnavailable) {
		t.Fatalf("legacy replay execute cutover err=%v", err)
	}
	runner := &productionAmbientReplayStageRunner{app: app, responder: responder}
	if _, err := runner.RunAmbientReplayStage(context.Background(), AmbientReplayManifest{}, AmbientReplayStageSpec{}, nil); !errors.Is(err, ErrAmbientReplayUnavailable) {
		t.Fatalf("legacy replay runner cutover err=%v", err)
	}
	t.Setenv(brainProjectionRuntimeModeEnv, brainProjectionRuntimeShadow)
	status := configureProductionBrainProjectionRuntime(nil)
	t.Cleanup(stopProductionBrainProjectionRuntime)
	if status.WorkerRunning || status.Enabled || !strings.Contains(status.Error, "originating canonical tenant authority") {
		t.Fatalf("projection cutover was not unavailable: %+v", status)
	}
	if _, err := (&productionBrainProjectionRuntime{}).ScheduleHistoricalBackfill(context.Background(), BrainProjectionHistoricalBackfillRequest{}); !errors.Is(err, ErrStrideE10TenantAuthorityStale) {
		t.Fatalf("legacy historical backfill scheduler cutover err=%v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/brain-projection/backfill", nil)
	response := httptest.NewRecorder()
	brainProjectionHistoricalBackfillHandler(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("legacy historical backfill HTTP status=%d body=%s", response.Code, response.Body.String())
	}
	if providerCalls != 0 {
		t.Fatalf("legacy Brain roots reached provider %d times", providerCalls)
	}
	if len(app.memory.snapshot(0)) != beforeMemory || !reflect.DeepEqual(app.snapshotState(), beforeBoard) {
		t.Fatal("legacy Brain roots mutated memory or board during cutover")
	}
}
