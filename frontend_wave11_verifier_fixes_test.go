package main

import (
	"os"
	"strings"
	"testing"
)

// Two defects the Wave 11 repair round left open, caught by the independent
// verifier. Each pin holds the SCENARIO, not just the literal that encodes it.

func readIndexForVerifierFixes(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// runGlobalSearch leaves a scope UNSETTLED only when its fetcher rethrows
// AbortError — that is the whole mechanism the re-open resume predicate
// (!results.has(scope) && !pending.has(scope)) reads. The meetings scope is the
// only one built from two reads, and it tolerated a missing source with a bare
// `.catch(() => ({}))`, which also swallowed the abort: closing the rail
// mid-fetch settled meetings with zero rows, so the rail answered "nothing in
// meetings" for a search that never finished and re-opening did NOT resume it.
func TestIndexUnifiedSearchMeetingsScopeStaysUnsettledOnAbort(t *testing.T) {
	html := readIndexForVerifierFixes(t)

	meetings := functionBody(html, "async function globalSearchMeetings(query, signal) {")
	if meetings == "" {
		t.Fatal("globalSearchMeetings missing")
	}
	// a swallow-everything catch on either read re-opens the defect
	if strings.Contains(meetings, ".catch(() => ({}))") {
		t.Errorf("an abort is not an empty source: swallowing it settles the meetings scope with zero rows and the resume predicate can never see it:\n%s", meetings)
	}
	// both reads must route their tolerance through the abort-aware helper
	if count := strings.Count(meetings, ".catch(globalSearchOptionalPayload)"); count != 2 {
		t.Errorf("both meetings reads must tolerate a missing source WITHOUT swallowing the abort (found %d, want 2):\n%s", count, meetings)
	}

	helper := functionBody(html, "function globalSearchOptionalPayload(error) {")
	if helper == "" {
		t.Fatal("globalSearchOptionalPayload missing — the multi-read scope has nothing abort-aware to lean on")
	}
	if !strings.Contains(helper, "if (error?.name === 'AbortError') throw error") {
		t.Errorf("the optional-read helper must rethrow the abort so the scope stays unsettled:\n%s", helper)
	}
	if !strings.Contains(helper, "return {}") {
		t.Errorf("a genuinely missing source must still degrade to an empty payload, not fail the scope:\n%s", helper)
	}

	// and the contract the helper depends on: runGlobalSearch settles a scope
	// for every throw EXCEPT an abort, which returns without writing a result
	run := functionBody(html, "async function runGlobalSearch(query) {")
	if !strings.Contains(run, "if (error?.name === 'AbortError') return") {
		t.Errorf("an aborted scope must not be recorded as settled:\n%s", run)
	}
}

// Project codenames are per-viewer secrets: listArtifactProjects scopes the
// names it returns by folder authorship and per-artifact ACL. resetStudioProjectsState
// is the identity boundary — it runs from setAuthenticatedUser on an identity
// change and ITS OWN last line re-renders the Work chips synchronously — so a
// surviving packagingProjectsCache painted account A's codenames into account
// B's chip row, and served them to B's project picker straight off the 30s TTL
// with no fetch at all. packagingCommissionsLocal is the same per-identity shape.
func TestIndexIdentityResetClearsPackagingProjectAndCommissionCaches(t *testing.T) {
	html := readIndexForVerifierFixes(t)

	reset := functionBody(html, "function resetStudioProjectsState() {")
	if reset == "" {
		t.Fatal("resetStudioProjectsState missing")
	}
	if !strings.Contains(reset, "packagingProjectsCache = { at: 0, projects: [] }") {
		t.Errorf("ACL-scoped project codenames are a per-identity projection — clearing at:0 is also what forces the next account's own read instead of a TTL hit:\n%s", reset)
	}
	if !strings.Contains(reset, "packagingCommissionsLocal instanceof Map && packagingCommissionsLocal.clear()") {
		t.Errorf("the previous account's commissions must not survive into the next one's hub or follow-up poll:\n%s", reset)
	}
	// the active project chip is one of those names: loadStudioProjects sends
	// it as ?project=, so a survivor scopes the next account's first Work read
	// by the previous account's codename
	if !strings.Contains(reset, "studioProjectProjectFilter = ''") {
		t.Errorf("the project chip is a codename too — it must not ride across the identity boundary:\n%s", reset)
	}
	// the signature carries its own braces (`options = {}`), so the body scan
	// starts at the signature's LAST brace
	const listSignature = "async function loadStudioProjects(options = {}) {"
	from := strings.Index(html, listSignature)
	if from < 0 {
		t.Fatal("loadStudioProjects missing")
	}
	if loader := functionBody(html[from+len(listSignature)-1:], "{"); !strings.Contains(loader, "params.set('project', studioProjectProjectFilter)") {
		t.Error("the project chip is sent server-side — that is why the reset must drop it")
	}

	// the two readers this protects, pinned so the leak path stays the one
	// this reset closes
	filter := functionBody(html, "function renderStudioProjectProjectFilter() {")
	if !strings.Contains(filter, "packagingProjectsCache?.projects") {
		t.Errorf("the Work chip row reads the cache directly — that is why the reset must empty it:\n%s", filter)
	}
	if !strings.Contains(reset, "renderStudioProjects()") {
		t.Error("the reset repaints the Work surface synchronously, so the caches must already be empty when it does")
	}
	loader := functionBody(html, "async function loadPackagingProjects(force = false) {")
	if !strings.Contains(loader, "Date.now() - packagingProjectsCache.at < 30000") {
		t.Errorf("the picker is served from a 30s TTL, so a stale cache means B never fetches at all:\n%s", loader)
	}
}
