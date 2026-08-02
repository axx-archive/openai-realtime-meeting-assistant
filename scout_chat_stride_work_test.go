package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScoutChatPublicInsightsRequestCreatesSuggestedWorkBeforeAnyRun(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	app := newIsolatedKanbanBoardApp(t)
	if app.strideRuntime != nil {
		_ = app.strideRuntime.Close()
	}
	config := strideIntegratedRuntimeConfig(t.TempDir())
	config.ProductPreviewEnabled = true
	config.Now = func() time.Time { return time.Now().UTC() }
	runtime, err := NewSTRIDERuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	app.strideRuntime = runtime
	t.Cleanup(func() { _ = runtime.Close() })

	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	project, err := app.createScoutChatThread("aj@shareability.com", "AJ", "Dog Perfect", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })

	providerCalls := 0
	previousOpenAI := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "", errors.New("provider must remain fenced")
	}
	t.Cleanup(func() { createOpenAITextResponse = previousOpenAI })
	previousAnthropic := createAnthropicTextResponse
	createAnthropicTextResponse = func(context.Context, string, anthropicTextRequest) (string, error) {
		providerCalls++
		return "", errors.New("provider must remain fenced")
	}
	t.Cleanup(func() { createAnthropicTextResponse = previousAnthropic })

	response, err := app.appendScoutChatThreadMessage(
		context.Background(),
		user,
		channel.ID,
		"@scout research: please create an Insights & Opportunities report for the launch",
		nil,
		"",
	)
	if err != nil {
		t.Fatalf("append public I&O request: %v", err)
	}
	if launches != 0 || providerCalls != 0 {
		t.Fatalf("proposal crossed execution boundary: launches=%d providerCalls=%d", launches, providerCalls)
	}
	if _, exists := response["agentThread"]; exists {
		t.Fatalf("public I&O proposal returned a launched agent thread: keys=%v", responseKeys(response))
	}
	if response["approvalRequired"] != true || response["providerCalls"] != 0 {
		t.Fatalf("proposal response omitted explicit fence: %#v", response)
	}

	suggestion, ok := response["suggestion"].(STRIDEProductWorkRecord)
	if !ok {
		t.Fatalf("response suggestion=%#v", response["suggestion"])
	}
	timPrincipal := strideRuntimePrincipalForEmail(user.Email)
	ajPrincipal := strideRuntimePrincipalForEmail(artifactLibraryAdminEmail)
	if suggestion.Status != "suggested" || suggestion.Revision != 1 || suggestion.RunID != "" || suggestion.DestinationThreadID != "" ||
		!suggestion.ProviderExecutionFenced || len(suggestion.RecipientIDs) != 2 ||
		!strideWorkContainsString(suggestion.RecipientIDs, timPrincipal) || !strideWorkContainsString(suggestion.RecipientIDs, ajPrincipal) {
		t.Fatalf("suggestion escaped recipient/revision/approval boundary: %+v", suggestion)
	}
	answer, ok := response["answer"].(scoutChatMessageRecord)
	if !ok || answer.Kind != "message" || answer.Thread != nil || !strings.Contains(answer.Text, "nothing is running yet") {
		t.Fatalf("Scout proposal answer=%#v", response["answer"])
	}
	saved, ok := response["thread"].(scoutChatThreadRecord)
	if !ok || len(saved.Messages) != 2 {
		t.Fatalf("persisted proposal thread=%#v", response["thread"])
	}
	for _, message := range saved.Messages {
		if message.Thread != nil {
			t.Fatalf("proposal persisted an executable thread ref: %+v", message.Thread)
		}
	}

	err = runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		for principal, want := range map[string]int{timPrincipal: 1, ajPrincipal: 1, strideRuntimePrincipalForEmail("tom@shareability.com"): 0} {
			work, _ := ctx.Product.workForPrincipal(principal)
			if len(work) != want {
				t.Fatalf("principal %s sees %d suggestions, want %d: %+v", principal, len(work), want, work)
			}
		}
		ctx.WorkStore.mu.Lock()
		runsBeforeApproval := len(ctx.WorkStore.Runs)
		ctx.WorkStore.mu.Unlock()
		if runsBeforeApproval != 0 {
			t.Fatalf("runs before approval=%d, want 0", runsBeforeApproval)
		}
		if _, approveErr := ctx.approveAndRunWork(timPrincipal, suggestion.ID, suggestion.Revision, ctx.Receipt.IssuedAt); !errors.Is(approveErr, ErrSTRIDEProductConflict) {
			t.Fatalf("approval without explicit destination error=%v, want conflict", approveErr)
		}
		ctx.WorkStore.mu.Lock()
		runsAfterDeniedApproval := len(ctx.WorkStore.Runs)
		ctx.WorkStore.mu.Unlock()
		if runsAfterDeniedApproval != 0 {
			t.Fatalf("denied approval created %d runs", runsAfterDeniedApproval)
		}

		selected, selectErr := ctx.Product.reviseWork(suggestion.ID, suggestion.Revision, timPrincipal, func(record *STRIDEProductWorkRecord) error {
			record.DestinationMode = "existing"
			record.DestinationThreadID = project.ID
			record.DestinationTitle = project.Title
			audience := strideRuntimeOrganizationAudience()
			record.DestinationAudience = &audience
			record.DestinationACLVersion = 1
			record.Lifecycle = append(record.Lifecycle, "destination_explicitly_selected")
			return nil
		}, ctx.Receipt.IssuedAt)
		if selectErr != nil {
			t.Fatalf("select explicit destination: %v", selectErr)
		}
		completed, approveErr := ctx.approveAndRunWork(timPrincipal, selected.ID, selected.Revision, ctx.Receipt.IssuedAt)
		if approveErr != nil {
			t.Fatalf("explicit approval: %v", approveErr)
		}
		if completed.Status != "completed" || completed.RunID == "" || completed.DestinationThreadID != project.ID {
			t.Fatalf("completed work=%+v", completed)
		}
		ctx.WorkStore.mu.Lock()
		runsAfterApproval := len(ctx.WorkStore.Runs)
		ctx.WorkStore.mu.Unlock()
		if runsAfterApproval != 1 {
			t.Fatalf("runs after explicit destination + approval=%d, want 1", runsAfterApproval)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if launches != 0 || providerCalls != 0 {
		t.Fatalf("deterministic approval contacted legacy/provider execution: launches=%d providerCalls=%d", launches, providerCalls)
	}
}

func TestScoutChatPublicInsightsRequestFailsClosedWhenOwnedRuntimeIsUnavailable(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	app := newIsolatedKanbanBoardApp(t)
	if app.strideRuntime != nil {
		_ = app.strideRuntime.Close()
	}
	app.strideRuntime = &STRIDERuntime{
		config:    STRIDERuntimeConfig{Enabled: true, TenantID: canonicalTenantID(), ProductPreviewEnabled: true},
		state:     STRIDERuntimeUnavailable,
		healthErr: ErrSTRIDERuntimeUnavailable,
	}
	channel, err := app.createScoutChatThread("aj@shareability.com", "AJ", "team", scoutChatVisibilityPublic)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	user := accountStore().findUser("tim@shareability.com")
	if user == nil {
		t.Fatal("seed user tim@shareability.com missing")
	}

	launches := 0
	previousRunner := startAgentThreadAsync
	startAgentThreadAsync = func(_ *kanbanBoardApp, _ scoutAgentThread) { launches++ }
	t.Cleanup(func() { startAgentThreadAsync = previousRunner })
	providerCalls := 0
	previousOpenAI := createOpenAITextResponse
	createOpenAITextResponse = func(context.Context, string, openAITextRequest) (string, error) {
		providerCalls++
		return "", errors.New("provider must remain fenced")
	}
	t.Cleanup(func() { createOpenAITextResponse = previousOpenAI })
	previousAnthropic := createAnthropicTextResponse
	createAnthropicTextResponse = func(context.Context, string, anthropicTextRequest) (string, error) {
		providerCalls++
		return "", errors.New("provider must remain fenced")
	}
	t.Cleanup(func() { createAnthropicTextResponse = previousAnthropic })

	_, err = app.appendScoutChatThreadMessage(
		context.Background(),
		user,
		channel.ID,
		"@scout research: create an Insights & Opportunities report for launch",
		nil,
		"",
	)
	if !errors.Is(err, ErrSTRIDERuntimeUnavailable) {
		t.Fatalf("unavailable owned runtime error=%v, want fail-closed runtime error", err)
	}
	if launches != 0 || providerCalls != 0 {
		t.Fatalf("unavailable preview fell through to execution: launches=%d providerCalls=%d", launches, providerCalls)
	}
	saved, _, err := app.scoutChatThreadByID(user.Email, channel.ID)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if len(saved.Messages) != 1 || saved.Messages[0].Role != "user" || saved.Messages[0].Thread != nil {
		t.Fatalf("fail-closed path did not preserve exactly the human request: %+v", saved.Messages)
	}
}
