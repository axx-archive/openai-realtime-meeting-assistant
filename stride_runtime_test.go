package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func strideIntegratedRuntimeConfig(dir string) STRIDERuntimeConfig {
	return STRIDERuntimeConfig{
		Enabled: true, TenantID: "bonfire",
		SnapshotPath: filepath.Join(dir, "runtime.snapshot.json"), GenerationPath: filepath.Join(dir, "runtime.generation.json"),
		Authority:         STRIDESnapshotMACAuthority{KeyID: "stride_runtime_test_key", Key: []byte("0123456789abcdef0123456789abcdef")},
		MinimumGeneration: 7, RecallThreadIDs: []string{"team"}, BootstrapEmpty: true,
		Now: func() time.Time { return time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC) },
	}
}

func TestSTRIDERuntimeDefaultOffHasNoStateOrActivation(t *testing.T) {
	dir := t.TempDir()
	runtime, err := NewSTRIDERuntime(STRIDERuntimeConfig{SnapshotPath: filepath.Join(dir, "snapshot"), GenerationPath: filepath.Join(dir, "generation")})
	if err != nil {
		t.Fatal(err)
	}
	health := runtime.Health()
	if health.State != STRIDERuntimeDisabled || health.Configured || health.Restored || health.Generation != 0 {
		t.Fatalf("disabled health=%+v", health)
	}
	for _, capability := range health.Capabilities {
		if capability.FeatureEnabled || !capability.ActivationFenced || capability.DurableState {
			t.Fatalf("disabled capability exposed authority: %+v", capability)
		}
	}
	for _, feature := range health.Features {
		if feature.Enabled {
			t.Fatalf("feature %s enabled by default", feature.Feature)
		}
	}
	if err := runtime.WithTenantDomains("bonfire", func(STRIDERuntimeDomains) error { return nil }); !errors.Is(err, ErrSTRIDERuntimeDisabled) {
		t.Fatalf("disabled domain access=%v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "snapshot"), filepath.Join(dir, "generation")} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("default-off runtime wrote %s: %v", path, statErr)
		}
	}
}

func TestSTRIDERuntimeRequiresTrustAndExplicitEmptyGenerationBootstrap(t *testing.T) {
	dir := t.TempDir()
	missingTrust := strideIntegratedRuntimeConfig(dir)
	missingTrust.Authority = STRIDESnapshotMACAuthority{}
	runtime, err := NewSTRIDERuntime(missingTrust)
	if !errors.Is(err, ErrSTRIDERuntimeConfiguration) || runtime.Health().State != STRIDERuntimeUnavailable {
		t.Fatalf("missing trust runtime=%+v err=%v", runtime.Health(), err)
	}

	missingGeneration := strideIntegratedRuntimeConfig(t.TempDir())
	missingGeneration.BootstrapEmpty = false
	runtime, err = NewSTRIDERuntime(missingGeneration)
	if !errors.Is(err, ErrSTRIDERuntimeGeneration) || runtime.Health().State != STRIDERuntimeUnavailable {
		t.Fatalf("missing generation runtime=%+v err=%v", runtime.Health(), err)
	}
	if accessErr := runtime.WithTenantDomains("bonfire", func(STRIDERuntimeDomains) error { return nil }); !errors.Is(accessErr, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("untrusted runtime exposed domains: %v", accessErr)
	}
}

func TestSTRIDERuntimeLiveTemporalApplyNeverSnapshotsHistoricalBrains(t *testing.T) {
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.TenantID = "tenant-1"
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	const historicalBrains = 256
	for index := 0; index < historicalBrains; index++ {
		historical, newErr := NewTemporalMeetingBrain(TemporalMeetingBrainConfig{TenantID: "tenant-1", RoomID: "room-history", SittingID: fmt.Sprintf("history-%03d", index), SittingStart: start.Add(-time.Duration(index+1) * time.Hour)})
		if newErr != nil {
			t.Fatal(newErr)
		}
		runtime.liveTemporal[strideRuntimeTemporalKey("room-history", fmt.Sprintf("history-%03d", index))] = historical
	}
	brainConfig := TemporalMeetingBrainConfig{TenantID: "tenant-1", RoomID: "room-1", SittingID: "sitting-1", SittingStart: start, SittingEnd: start.Add(time.Hour)}
	first := temporalTestTranscript("segment-first", "revision-first", "first durable turn", "authoritative_final", "", 1, 1, start, start.Add(time.Minute), start.Add(time.Minute), []string{"member_aj"}, nil)
	commits := 0
	if err := runtime.ApplyLiveTemporalEvidence("tenant-1", brainConfig, first, func() error {
		commits++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if commits != 1 || runtime.generation != config.MinimumGeneration-1 {
		t.Fatalf("live projection persisted compound runtime: commits=%d generation=%d", commits, runtime.generation)
	}
	denied := errors.New("authority commit rejected")
	second := temporalTestTranscript("segment-second", "revision-second", "must remain staged only", "authoritative_final", "", 1, 2, start.Add(2*time.Minute), start.Add(3*time.Minute), start.Add(3*time.Minute), []string{"member_aj"}, nil)
	if err := runtime.ApplyLiveTemporalEvidence("tenant-1", brainConfig, second, func() error { return denied }); !errors.Is(err, denied) {
		t.Fatalf("rejected authority commit error=%v", err)
	}
	if brain := runtime.liveTemporal[strideRuntimeTemporalKey("room-1", "sitting-1")]; len(brain.sources) != 1 || brain.transcriptHighWater != 1 {
		t.Fatalf("rejected authority commit leaked staged source: highWater=%d sources=%+v", brain.transcriptHighWater, brain.sources)
	}
	third := temporalTestTranscript("segment-third", "revision-third", "visible only after authority commit", "authoritative_final", "", 1, 2, start.Add(4*time.Minute), start.Add(5*time.Minute), start.Add(5*time.Minute), []string{"member_aj"}, nil)
	commitEntered, releaseCommit := make(chan struct{}), make(chan struct{})
	applyDone := make(chan error, 1)
	go func() {
		applyDone <- runtime.ApplyLiveTemporalEvidence("tenant-1", brainConfig, third, func() error {
			close(commitEntered)
			<-releaseCommit
			return nil
		})
	}()
	<-commitEntered
	readDone := make(chan error, 1)
	go func() {
		readDone <- runtime.ReadTemporalMeetingBrain("tenant-1", "room-1", "sitting-1", func(brain *TemporalMeetingBrain) error {
			if _, found := brain.sources["segment-third"]; !found {
				return errors.New("committed source is absent")
			}
			return nil
		})
	}()
	select {
	case err := <-readDone:
		t.Fatalf("reader crossed uncommitted authority boundary: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-applyDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	for key, brain := range runtime.liveTemporal {
		if key == strideRuntimeTemporalKey("room-1", "sitting-1") {
			continue
		}
		if brain.snapshotGeneration != 0 {
			t.Fatalf("live projection snapshotted historical brain %s at generation %d", key, brain.snapshotGeneration)
		}
	}
	if err := runtime.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(config.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("history-")) || bytes.Contains(raw, []byte("first durable turn")) || len(runtime.domains.temporal) != 0 {
		t.Fatalf("ordinary compound Save serialized ephemeral live brains")
	}
	if len(runtime.liveTemporal) != historicalBrains+1 {
		t.Fatalf("ordinary Save mutated live meeting cache: count=%d", len(runtime.liveTemporal))
	}
	if err := runtime.ClearLiveTemporalMeetingBrain("tenant-1", "room-1", "sitting-1"); err != nil {
		t.Fatal(err)
	}
	if _, found := runtime.liveTemporal[strideRuntimeTemporalKey("room-1", "sitting-1")]; found {
		t.Fatal("finalized current meeting raw brain was not cleared")
	}
}

func TestSTRIDERuntimeRestartRestoresOneAuthoritativeDomainSet(t *testing.T) {
	config := strideIntegratedRuntimeConfig(t.TempDir())
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	audience := STRIDEAudience{Visibility: "channel", Principals: []string{"member_aj"}}
	if err := runtime.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		if domains.WorkOrchestrator.Enabled || domains.WorkOrchestrator.Store == nil {
			t.Fatalf("work orchestrator activation=%t store=%p", domains.WorkOrchestrator.Enabled, domains.WorkOrchestrator.Store)
		}
		if _, appendErr := domains.ConversationLedger.Append(strideConversationAppend("runtime_event", "runtime_message", "message", 1, audience)); appendErr != nil {
			return appendErr
		}
		entry := strideTestRegistryEntry()
		entry.Key = "runtime_registry"
		return domains.Registry.Register(entry)
	}); err != nil {
		t.Fatal(err)
	}
	brainConfig := TemporalMeetingBrainConfig{TenantID: "bonfire", RoomID: "office", SittingID: "runtime_sitting", SittingStart: time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)}
	if err := runtime.WithTemporalMeetingBrain("bonfire", brainConfig, func(brain *TemporalMeetingBrain) error {
		if brain.CurrentState().Config != brainConfig {
			t.Fatal("temporal brain was not scoped to the requested sitting")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Health(); got.State != STRIDERuntimeClosed || got.Generation != 7 || got.LastPersistedAt.IsZero() {
		t.Fatalf("closed health=%+v", got)
	}

	config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.Health(); got.State != STRIDERuntimeStandby || !got.Restored || got.Generation != 7 {
		t.Fatalf("restart health=%+v", got)
	}
	if err := restarted.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal("bonfire", "member_aj")
		if projectErr != nil || len(projection) != 1 || projection[0].SourceID != "runtime_message" {
			t.Fatalf("restored conversation=%+v err=%v", projection, projectErr)
		}
		registry, snapshotErr := domains.Registry.Snapshot()
		if snapshotErr != nil || len(registry.Entries) != 1 || registry.Entries[0].Key != "runtime_registry" {
			t.Fatalf("restored registry=%+v err=%v", registry, snapshotErr)
		}
		if enableErr := domains.Registry.SetFeatureEnabled(STRIDEFeatureMarketplaceDiscovery, true); !errors.Is(enableErr, ErrSTRIDEActivationFenced) {
			t.Fatalf("restore bypassed activation fence: %v", enableErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.WithTemporalMeetingBrain("bonfire", brainConfig, func(brain *TemporalMeetingBrain) error {
		if brain.CurrentState().Config.SittingID != "runtime_sitting" {
			t.Fatalf("restored temporal config=%+v", brain.CurrentState().Config)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDERuntimeTenantBoundaryRejectsAndPoisonsCrossTenantState(t *testing.T) {
	config := strideIntegratedRuntimeConfig(t.TempDir())
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := runtime.WithTenantDomains("other_tenant", func(STRIDERuntimeDomains) error {
		called = true
		return nil
	}); !errors.Is(err, ErrSTRIDERuntimeTenantDenied) || called {
		t.Fatalf("wrong-tenant access err=%v called=%t", err, called)
	}
	foreign := strideConversationAppend("foreign_event", "foreign_message", "message", 1, STRIDEAudience{Visibility: "channel", Principals: []string{"member_aj"}})
	foreign.Event.Header.TenantID = "other_tenant"
	if err := runtime.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		_, appendErr := domains.ConversationLedger.Append(foreign)
		return appendErr
	}); !errors.Is(err, ErrSTRIDERuntimeCrossTenant) {
		t.Fatalf("foreign state err=%v", err)
	}
	if got := runtime.Health(); got.State != STRIDERuntimeUnavailable || !stringsContains(got.Error, ErrSTRIDERuntimeCrossTenant.Error()) {
		t.Fatalf("cross-tenant runtime did not fail closed: %+v", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.SnapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("poisoned cross-tenant state was persisted: %v", err)
	}
}

func TestSTRIDERuntimeCorruptSnapshotFailsClosedAndIsNotOverwritten(t *testing.T) {
	config := strideIntegratedRuntimeConfig(t.TempDir())
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(config.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), original...)
	corrupt[len(corrupt)/2] ^= 0x01
	if err := os.WriteFile(config.SnapshotPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(config)
	if !errors.Is(err, ErrSTRIDERuntimeSnapshot) || restarted.Health().State != STRIDERuntimeUnavailable {
		t.Fatalf("corrupt restart health=%+v err=%v", restarted.Health(), err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(config.SnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(corrupt, after) {
		t.Fatal("unavailable runtime overwrote corrupt evidence during close")
	}
}

func TestSTRIDERuntimeCleanShutdownIsConcurrentAndIdempotent(t *testing.T) {
	config := strideIntegratedRuntimeConfig(t.TempDir())
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	const closers = 16
	errs := make(chan error, closers)
	var wait sync.WaitGroup
	for index := 0; index < closers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- runtime.Close()
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent close: %v", err)
		}
	}
	if health := runtime.Health(); health.State != STRIDERuntimeClosed || health.Generation != 7 {
		t.Fatalf("clean shutdown health=%+v", health)
	}
	for _, path := range []string{config.SnapshotPath, config.GenerationPath} {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("shutdown artifact %s info=%v err=%v", path, info, err)
		}
	}
	config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(config)
	if err != nil || !restarted.Health().Restored {
		t.Fatalf("clean shutdown did not restore: health=%+v err=%v", restarted.Health(), err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func stringsContains(value, fragment string) bool {
	return len(fragment) == 0 || bytes.Contains([]byte(value), []byte(fragment))
}
