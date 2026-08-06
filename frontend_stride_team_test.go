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
		`profiles are read-only`,
		`Best used for`,
		`usageGuidance`,
		`const teamListings = strideMarketplaceRecords.filter`,
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
	cardStart := strings.Index(html, "function strideAgentCard(record, kind)")
	if cardStart < 0 {
		t.Fatal("could not isolate member-facing agent card")
	}
	cardEnd := strings.Index(html[cardStart:], "function strideList(value)")
	if cardEnd < 0 {
		t.Fatal("could not isolate member-facing agent card")
	}
	card := html[cardStart : cardStart+cardEnd]
	for _, forbidden := range []string{`Start preview`, `Hire with approval`, `Create private agent`} {
		if strings.Contains(card, forbidden) {
			t.Fatalf("member-facing team surface retained workforce control %q", forbidden)
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

func TestFrontendAgentTeamSurfacesRichReadOnlyProfiles(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="strideAgentDetail"`,
		`Details`,
		`Best used for`,
		`Personality`,
		`Skills`,
		`Access`,
		`Memory`,
		`Sample outcomes`,
		`Identity and provenance`,
		`Responsibilities`,
		`Identity and authority`,
		`Open private chat`,
		`future administrator surface`,
		`strideMarketplaceCanManage = false`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rich agent details are missing %q", want)
		}
	}
	detailStart := strings.Index(html, "function openStrideAgentDetail(record, kind)")
	if detailStart < 0 {
		t.Fatal("could not isolate read-only agent detail")
	}
	detailEnd := strings.Index(html[detailStart:], "strideAgentDetailClose?.addEventListener")
	if detailEnd < 0 {
		t.Fatal("could not isolate read-only agent detail")
	}
	detail := html[detailStart : detailStart+detailEnd]
	for _, forbidden := range []string{`appendStrideMarketplaceControls`, `appendStrideRosterControls`, `Propose update`, `Pause coworker`, `Offboard coworker`} {
		if strings.Contains(detail, forbidden) {
			t.Fatalf("read-only agent detail retained control %q", forbidden)
		}
	}
}

func TestFrontendMemberSurfaceDoesNotExposePrivateAgentAuthoring(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, forbidden := range []string{`id="stridePrivateAgentCreate"`, `Create private agent`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("member-facing surface exposes private agent authoring %q", forbidden)
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
