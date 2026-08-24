package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSTRIDELiveTranscriptProjectionNeverInventoriesLifetimeCorpus(t *testing.T) {
	raw, err := os.ReadFile("stride_runtime_adapters.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start := strings.Index(source, "func (app *kanbanBoardApp) projectSTRIDEAuthoritativeTranscript")
	end := strings.Index(source[start:], "// AdmitSuggestedWorkCandidate")
	if start < 0 || end < 0 {
		t.Fatal("could not isolate live transcript projection")
	}
	body := source[start : start+end]
	for _, forbidden := range []string{"InventoryBrainSources(", "ReadBrainSource(", "ApplyTemporalEvidenceAndSave(", ".Save()"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("live transcript projection performs lifetime source scan via %q", forbidden)
		}
	}
	for _, required := range []string{"authoritativeRecentMemoryEntry", "CurrentBrainObject", "CurrentPurgeGeneration", "VerifyBrainSourceConsent", "ApplyLiveTemporalEvidence"} {
		if !strings.Contains(body, required) {
			t.Fatalf("targeted projection is missing %q revalidation", required)
		}
	}
}

func TestSTRIDETeamChatAdapterProjectsOnlyPublicDurableMessages(t *testing.T) {
	setupAuthTestEnv(t)
	_ = loginAs(t, "aj@shareability.com", "B0NFIRE!")
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.RecallThreadIDs = []string{"team"}
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	app := &kanbanBoardApp{strideRuntime: runtime}
	now := time.Date(2026, 7, 30, 22, 0, 0, 0, time.UTC)
	message := scoutChatMessageRecord{
		ID: "message_one", Kind: "message", Role: "user", Text: "Ship the signed plan",
		CreatedAt: now.Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}
	private := scoutChatThreadRecord{ID: "private_thread", Visibility: scoutChatVisibilityPrivate}
	app.observeSTRIDETeamChatMessage(private, message, "message", "")
	if err := runtime.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		if len(snapshot.Events) != 0 {
			t.Fatalf("private chat entered STRIDE ledger: %+v", snapshot.Events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	public := scoutChatThreadRecord{ID: "team", Visibility: scoutChatVisibilityPublic, Title: "Team"}
	app.observeSTRIDETeamChatMessage(public, message, "message", "")
	principal := strideRuntimePrincipalForEmail("aj@shareability.com")
	assertProjection := func(want int) {
		t.Helper()
		if err := runtime.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
			projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal("bonfire", principal)
			if projectErr != nil {
				return projectErr
			}
			if len(projection) != want {
				t.Fatalf("projection=%+v, want %d rows", projection, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertProjection(1)

	message.Text = "Ship the signed, reviewed plan"
	message.EditedAt = now.Add(time.Second).Format(time.RFC3339Nano)
	app.observeSTRIDETeamChatMessage(public, message, "edit", "aj@shareability.com")
	assertProjection(1)
	app.observeSTRIDETeamChatMessage(public, message, "delete", "aj@shareability.com")
	assertProjection(0)

	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	config.BootstrapEmpty = false
	restarted, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.WithTenantDomains("bonfire", func(domains STRIDERuntimeDomains) error {
		snapshot, snapshotErr := domains.ConversationLedger.Snapshot()
		if snapshotErr != nil {
			return snapshotErr
		}
		if len(snapshot.Events) != 3 {
			t.Fatalf("restart events=%d, want append/edit/delete", len(snapshot.Events))
		}
		projection, projectErr := domains.ConversationLedger.ProjectForTenantPrincipal("bonfire", principal)
		if projectErr != nil || len(projection) != 0 {
			t.Fatalf("deleted message reappeared after restart: %+v err=%v", projection, projectErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSTRIDESuggestedWorkAdapterRemainsActivationFenced(t *testing.T) {
	runtime, err := NewSTRIDERuntime(strideIntegratedRuntimeConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_, err = runtime.AdmitSuggestedWorkCandidate(context.Background(), "bonfire", STRIDEWorkIntentCandidate{})
	if !errors.Is(err, ErrSTRIDEWorkDisabled) {
		t.Fatalf("suggested work admission=%v, want disabled fence", err)
	}
}
