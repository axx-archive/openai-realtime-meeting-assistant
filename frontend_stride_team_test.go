package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendAgentTeamReachesAuthenticatedFencedProductLifecycles(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`data-tool="team"`,
		`id="teamTool"`,
		`/api/stride/v1/status`,
		`/api/stride/v1/roster`,
		`/api/stride/v1/marketplace`,
		`/api/stride/v1/work`,
		`Start preview`,
		`'reviewed preview'`,
		`Hire with approval`,
		`IDs are transport identifiers, not display copy`,
		`const id = String(`,
		`function scoutChatDirectAgentName(thread)`,
		`const privateTarget = directAgent || 'Scout'`,
		`Approve & run`,
		`Edit scope`,
		`provider execution fenced`,
		`activation fenced`,
		`strideTeamJSON`,
		`credentials: 'same-origin'`,
		`'Content-Type': 'application/json'`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("agent team surface is missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`tenantId=`,
		`orgId=`,
		`Mary · Marketing Agent`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("agent team surface embeds forbidden activation/demo path %q", forbidden)
		}
	}
}

func TestFrontendAgentTeamUsesTextNodesForRuntimeRecords(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	start := strings.Index(html, "function strideAgentCard")
	if start < 0 {
		t.Fatal("could not isolate agent card renderer")
	}
	end := strings.Index(html[start:], "async function strideTeamJSON")
	if end < 0 {
		t.Fatal("could not isolate agent card renderer")
	}
	renderer := html[start : start+end]
	if strings.Contains(renderer, "innerHTML") {
		t.Fatal("agent card renderer must not interpolate runtime records into HTML")
	}
	if !strings.Contains(renderer, "textContent") || !strings.Contains(renderer, "createElement") {
		t.Fatal("agent card renderer must build escaped DOM text nodes")
	}
}

func TestFrontendAgentTeamSurfacesRichProfilesAndHumanControlledLifecycle(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="strideAgentDetail"`,
		`Details`,
		`Personality`,
		`Skills`,
		`Access`,
		`Memory`,
		`Sample outcomes`,
		`Cost and package`,
		`Responsibilities`,
		`Identity and authority`,
		`Semantic diff`,
		`Propose update`,
		`Approve update`,
		`Rollback`,
		`Assign responsibility`,
		`Record reviewed learning`,
		`Correct`,
		`Forget`,
		`Clean export receipt`,
		`Open private chat`,
		`/updates/`,
		`/assign`,
		`/learning/`,
		`/export`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rich agent details are missing %q", want)
		}
	}
	if strings.Contains(html, `/api/stride/v1/roster/${encodeURIComponent(id)}/configure`) {
		t.Fatal("material profile changes must be proposed with a semantic diff, never applied through direct configure")
	}
}

func TestFrontendPrivateAgentTemplateIsAdminGatedAndClosed(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="stridePrivateAgentCreate"`,
		`id="stridePrivateAgentDialog"`,
		`strideMarketplaceCanManage = strideField(marketplacePayload, 'canManage', 'CanManage') === true`,
		`stridePrivateAgentCreate.hidden = !strideMarketplaceCanManage`,
		`if (!strideMarketplaceCanManage) return`,
		`/api/stride/v1/marketplace/templates`,
		`requestedCapabilities`,
		`requiredAccess`,
		`provider execution fenced`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("private agent authoring is missing %q", want)
		}
	}
	start := strings.Index(html, `stridePrivateAgentForm?.addEventListener('submit'`)
	if start < 0 {
		t.Fatal("could not isolate the private agent request builder")
	}
	end := strings.Index(html[start:], `function strideWorkCard`)
	if end < 0 {
		t.Fatal("could not isolate the private agent request builder")
	}
	requestBuilder := html[start : start+end]
	for _, forbidden := range []string{`code:`, `command:`, `hook:`, `credential:`, `environment:`, `mcp:`} {
		if strings.Contains(strings.ToLower(requestBuilder), forbidden) {
			t.Fatalf("private template request exposes unsafe field %q", forbidden)
		}
	}
}

func TestFrontendAgentExportReceiptNamesEveryExcludedPrivateClass(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`tenant data: excluded`,
		`credentials: excluded`,
		`memory: excluded`,
		`assignments: excluded`,
		`private evidence: excluded`,
		`historicalAttributionHash`,
		`provider execution fenced:`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("clean export receipt is missing %q", want)
		}
	}
}

func TestFrontendSuggestedWorkRequiresAnExplicitProjectDestination(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="strideProjectPicker"`,
		`Choose project`,
		`thread.visibility === 'public'`,
		`thread.table !== true`,
		`title !== 'team'`,
		`title !== 'general'`,
		`body: { revision, mode: 'existing', threadId: thread.id }`,
		`body: { revision, mode: 'new', title: threadTitle }`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("explicit project chooser is missing %q", want)
		}
	}
	if strings.Contains(html, `Use source thread`) {
		t.Fatal("suggested work may not silently bind to its source conversation")
	}
}
