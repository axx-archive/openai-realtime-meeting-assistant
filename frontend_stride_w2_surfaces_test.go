package main

import (
	"os"
	"strings"
	"testing"
)

func TestStrideW2ContributionNetworkSurfacesStayPrivateAndBodyFree(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`id="strideW2Surface"`,
		`data-settings-section="organizations"`,
		`'/me'`, `'/work-record'`, `'/org/people'`, `'/org/requests'`, `'/org/contributions'`, `'/org/recruiting'`,
		`'/network/draft'`, `'/network/preview'`, `'/network/recruiter'`,
		`'/network/search'`, `'/network/contact'`, `'/network/blocks'`,
		`coworkerRoutePattern`, `coworker-profile`, `organization-recruiting`,
		`View as recruiter`, `Contribution approvals`, `Search unavailable`,
		`Your private information stays private`,
		`private drafts, source bodies, contact channels, hidden memberships, or ranking scores`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("index.html missing W2 surface contract %q", marker)
		}
	}
	start := strings.Index(html, `/* W2 contribution network product surfaces`)
	end := strings.Index(html[start:], `</style>`)
	if start < 0 || end < 0 {
		t.Fatal("W2 CSS slice missing")
	}
	css := html[start : start+end]
	for _, marker := range []string{
		`-webkit-font-smoothing: antialiased`,
		`text-wrap: balance`,
		`text-wrap: pretty`,
		`font-variant-numeric: tabular-nums`,
		`min-width: 40px`,
		`min-height: 40px`,
		`scale: .96`,
		`transition-property: scale, background-color, box-shadow, opacity`,
		`outline: 1px solid rgba(0,0,0,.1)`,
		`@media (max-width: 760px)`,
		`@media (prefers-reduced-motion: reduce)`,
	} {
		if !strings.Contains(css, marker) {
			t.Errorf("W2 CSS missing desktop/mobile polish marker %q", marker)
		}
	}
	if strings.Contains(css, "transition: all") || strings.Contains(css, "will-change: all") {
		t.Fatal("W2 CSS uses a prohibited broad transition/compositing hint")
	}
}

func TestStrideW2UsesTheMainProductShellAndDirectNetworkNavigation(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`data-tool="network"`, `aria-label="Work network"`, `network: 'Network'`,
		`verified work · person-controlled visibility`, `applyToolState('network')`,
		`window.closeStrideContributionSurface`, `inset: 60px 0 0 72px`,
		`Find people by the work they have chosen to show`, `Private by default`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("cohesive network shell missing %q", marker)
		}
	}
	for _, stale := range []string{`<strong>Stride network</strong>`, `Current authorized view`, `Authorized surface`, `default off</span>`} {
		if strings.Contains(html, stale) {
			t.Errorf("detached/technical network chrome remains: %q", stale)
		}
	}
}

func TestStrideW2TypedProjectionSchemaMatchesCanonicalMobileContract(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.LastIndex(html, `/* W2 contribution network route shell.`)
	end := strings.Index(html[start:], `</script>`)
	if start < 0 || end < 0 {
		t.Fatal("W2 route script slice missing")
	}
	js := html[start : start+end]
	for _, marker := range []string{
		`projectionIsBodyFree`, `forbiddenProjectionKeys`, `safeProjectionDetail`,
		`'self-profile-detail'`, `'coworker-profile-detail'`, `'network-profile-detail'`,
		`'work-record-section'`, `'contribution-evidence'`, `'contribution-review'`,
		`'network-state'`, `'recruiting-governance'`, `'organization-summary'`,
		`'membership-detail'`, `'join-request-detail'`, `'network-query-interpretation'`,
		`'network-search-result'`, `'contact-request-detail'`, `'block-detail'`,
		`'export-receipt'`, `'purge-receipt'`,
		`publicReference`, `^[a-f0-9]{64}$`, `timestamp`, `positiveInteger`,
		`detail.kind !== kind`, `item.kind === undefined && item.detail === undefined`,
		`typeof item.kind === 'string' && safeProjectionDetail(expectedSurface,item.kind,item.detail)`,
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("typed projection admission missing %q", marker)
		}
	}
	for _, forbidden := range []string{`detail.innerHTML =`, `Object.assign(item`, `item.organizationId`, `item.personId`} {
		if strings.Contains(js, forbidden) {
			t.Errorf("typed projection renderer trusts server detail via %q", forbidden)
		}
	}
}

func TestStrideW2RendersFullAcceptanceStatesFromLocalClosedFixture(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`strideW2FixtureProjection`, `['localhost','127.0.0.1']`, `strideW2Fixture`,
		`state === 'zero' ? [{id:'organization-onboarding'`, `zero-organizations`, `3 of 3`, `Final owner. Transfer ownership`,
		`Problems and outcomes`, `How I contribute`, `Organizations and roles`, `Work evidence`, `People and agents I helped`,
		`organization_verified_redacted`, `artifactAccess:'redacted'`,
		`fieldDiffs:`, `namedPartyStates:`, `auditEntries:`,
		`Decide named-party approval`, `Revoke attestation`,
		`state:'paused'`, `network-searchable-fields-update`, `View as recruiter`,
		`network-query-interpretation`, `network-search-result`, `channelRevealed:false`, `block-detail`,
		`export-receipt`, `purge-receipt`, `talent_searcher`,
		`personSearchLimit:`, `organizationSearchLimit:`, `globalSearchLimit:`,
		`personContactLimit:`, `organizationContactLimit:`, `globalContactLimit:`,
		`organization-member-revoke`, `network-profile-off`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("local rendered fixture missing W2 state %q", marker)
		}
	}
	if !strings.Contains(html, `new URLSearchParams(location.search).get('strideW2Fixture') !== '1'`) {
		t.Fatal("fixture is not explicit-query gated")
	}
}

func TestStrideW2ActionValuesAreExactAndBounded(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.Index(html, `const actionValueSchemas = new Map([`)
	end := strings.Index(html[start:], `let projectionRequest = null`)
	if start < 0 || end < 0 {
		t.Fatal("closed action value schema missing")
	}
	schema := html[start : start+end]
	for _, exact := range []string{
		`displayName:{max:80}`, `pronouns:{max:40}`, `bio:{max:280}`,
		`workModes:{max:64,list:true,maxItems:20}`, `openTo:{max:64,list:true,maxItems:20}`,
		`name:{max:120,required:true}`, `slug:{max:63,required:true,pattern:'slug'}`,
		`joinCode:{max:128,required:true}`, `intro:{max:280}`, `query:{max:240,required:true}`,
		`purpose:{max:80,required:true}`, `note:{max:1000}`,
		`collaborationType:{max:32,required:true,values:['collaboration','advisory','employment','recruiting','organization_join']}`,
		`decision:{max:8,required:true,values:['approved','denied']}`,
		`reason:{max:500}`,
	} {
		if !strings.Contains(schema, exact) {
			t.Errorf("action values differ from server contract: missing %q", exact)
		}
	}
	if strings.Contains(schema, `['network-block',{`) || strings.Contains(schema, `['network-unblock',{`) {
		t.Fatal("block actions must carry the server-contract empty values object")
	}
	if got := strings.Count(schema, `reason:{max:500}`); got != 14 {
		t.Fatalf("optional reason schema count=%d, want 14 exact canonical decisions", got)
	}
	for _, forbidden := range []string{"email", "contactChannel", "source", "body", "authority", "score", "hiddenMembership"} {
		if strings.Contains(schema, forbidden) {
			t.Errorf("action value schema exposes forbidden field %q", forbidden)
		}
	}
}

func TestStrideW2RouteShellPreservesHistoryAndUsesNoProviderCalls(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.LastIndex(html, `/* W2 contribution network route shell.`)
	end := strings.Index(html[start:], `</script>`)
	if start < 0 || end < 0 {
		t.Fatal("W2 route script slice missing")
	}
	js := html[start : start+end]
	for _, marker := range []string{
		`history.pushState({ strideW2: path }, '', path)`,
		`window.addEventListener('popstate'`,
		`window.openStrideContributionSurface`,
		`renderRoute(location.pathname, { push: false })`,
		`disabled>Publish unavailable`,
		`/api/stride/v1/mobile/surfaces/`,
		`/api/stride/v1/mobile/actions/`,
		`safeProjectionEnvelope`,
		`new Set(['availability','surface','revision','items','reason'])`,
		`new Set(['id','title','summary','status','context','updatedAt','kind','detail','actions'])`,
		`new Set(['id','type','label','expectedRevision'])`,
		`[403,404,501,503].includes(response.status)`,
		`response.status === 409`,
		`'Idempotency-Key': idempotencyKey`,
		`body: JSON.stringify({ action, expectedRevision, surface: surfaceName, values })`,
		`freezeMutationUI(); button.setAttribute('aria-busy', 'true')`,
		`renderProjectionItems(target, envelope, path)`,
		`const actionValueSchemas = new Map([`,
		`const actionSurfaces = new Map([`,
		`actionSurfaces.get(action.type) === expectedSurface`,
		`actionSurfaces.get(action) !== surfaceName`,
		`list:true,maxItems:20`,
		`value.split(',').map(item => item.trim()).filter(Boolean)`,
		`spec.required && !value`,
		`spec.pattern === 'slug'`,
		`/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/`,
		`spec.values && !spec.values.includes(value)`,
		`Object.prototype.hasOwnProperty.call(schema, key)`,
		`Object.keys(values).length === Object.keys(schema).length`,
		`response.status === 400`,
		`showActionError(article, 'Check the required fields and allowed values.')`,
		`The action could not be applied. Retry the exact operation or discard it.`,
		`let pendingActionOperation = null`,
		`const pendingActionOperationsByAccount = new Map()`,
		`pendingActionOperation.bodyFingerprint !== bodyFingerprint`,
		`Resolve or discard the pending retry before changing another action.`,
		`pendingActionOperation ||= { accountFingerprint:accountFingerprint(requestAuthority.accountKey), actionId, action, surface:surfaceName, expectedRevision, values, bodyFingerprint, idempotencyKey, path }`,
		`pendingActionOperationsByAccount.set(pendingActionOperation.accountFingerprint,pendingActionOperation)`,
		`freezeMutationUI(exactPendingRetryButton())`,
		`data-stride-w2-discard-operation`,
		`discard.addEventListener('click', clearPendingActionOperation)`,
		`response.status === 409) { clearPendingActionOperation()`,
		`response.status === 400) { clearPendingActionOperation()`,
		`The server response could not be applied safely.`,
		`let authorityGeneration = 1`,
		`const authoritySnapshot = () =>`,
		`const authorityIsCurrent = snapshot =>`,
		`projectionRequests.forEach(controller => controller.abort())`,
		`mutationRequest?.abort()`,
		`if (!authorityIsCurrent(requestAuthority)) return`,
		`function blockPendingNavigation()`,
		`Retry the exact operation or discard it before leaving this view.`,
		`if (pendingPersistenceInvalid || pendingActionOperation && path !== pendingActionOperation.path) { blockPendingNavigation(); return true }`,
		`if (blockPendingNavigation()) return`,
		`settingsSurface && !blockPendingNavigation()`,
		`function openOrganizationSettingsFromMenu()`,
		`organizationSwitcher?.addEventListener('click', () => setOrganizationMenuOpen(organizationMenu.hidden))`,
		`if (previousFingerprint && pendingActionOperation) pendingActionOperationsByAccount.set(previousFingerprint, pendingActionOperation)`,
		`pendingActionOperation = pendingPersistenceInvalid ? null : persistedOperation || pendingActionOperationsByAccount.get(nextFingerprint) || null`,
		`function syncPendingActionControl()`,
		`dataset.strideW2PendingControl`,
		`dataset.strideW2ReturnPending`,
		`returnToPendingActionOrigin`,
		`Reconcile`,
		`control.dataset.strideW2PendingAccount = pendingActionOperation?.accountFingerprint`,
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("W2 route shell missing %q", marker)
		}
	}
	organizationSettingsStart := strings.Index(js, `function openOrganizationSettingsFromMenu()`)
	if organizationSettingsStart < 0 {
		t.Fatal("organization settings helper boundary missing")
	}
	organizationSettingsEnd := strings.Index(js[organizationSettingsStart:], `function renderOrganizationSwitcher(envelope)`)
	if organizationSettingsEnd < 0 {
		t.Fatal("organization settings helper boundary missing")
	}
	organizationSettingsHelper := js[organizationSettingsStart : organizationSettingsStart+organizationSettingsEnd]
	if !strings.Contains(organizationSettingsHelper, `if (blockPendingNavigation()) return`) {
		t.Fatal("organization settings helper must hold the exact pending-operation navigation fence")
	}
	if !strings.Contains(js, `['/network/draft','network-draft']`) || strings.Contains(js, `['/network/draft','profile']`) {
		t.Fatal("private network draft must use its distinct server projection, never the public identity profile")
	}
	if !strings.Contains(js, `'network-profile-detail':['network-draft','network-preview','network-recruiter-view','network-search']`) {
		t.Fatal("private network draft must admit the server-authored network profile detail used by the live runtime")
	}
	if !strings.Contains(js, `['/work-record','work-record']`) {
		t.Fatal("work record must have its own route and server projection")
	}
	for _, actionType := range []string{"profile-update", "organization-create", "organization-join", "organization-request-approve", "organization-request-deny", "organization-switch", "organization-leave", "organization-member-role-change", "organization-member-revoke", "organization-ownership-transfer", "organization-recruiting-grant-create", "organization-recruiting-grant-revoke", "network-draft-save", "network-search-propose", "network-search-confirm", "contribution-subject-approve", "contribution-subject-dispute", "contribution-organization-approve", "contribution-organization-deny", "contribution-named-party-decision", "contribution-attestation-revoke", "contribution-publish", "contribution-withdraw", "contribution-correct", "contribution-revoke", "work-record-export", "work-record-delete", "network-publish", "network-pause", "network-profile-off", "network-profile-export", "network-profile-delete", "network-searchable-fields-update", "contact-send", "exact-link-contact-send", "contact-accept", "contact-decline", "contact-withdraw", "network-block", "network-unblock"} {
		if !strings.Contains(js, actionType) {
			t.Errorf("W2 route shell missing closed action type %q", actionType)
		}
	}
	for _, forbidden := range []string{"XMLHttpRequest", "WebSocket(", "EventSource(", "MyMind", "AgentMind", "sourceBody", "authorityId", "contactChannel", "rankingScore", "Object.assign(item", "dataset.authorized = 'true'", `actionOperationKeys`} {
		if strings.Contains(js, forbidden) {
			t.Errorf("W2 route shell contains forbidden provider/private mechanism %q", forbidden)
		}
	}
	if !strings.Contains(html, `window.__strideW2FenceAuthority?.(previousIdentity, nextIdentity)`) || !strings.Contains(js, `window.__strideW2FenceAuthority = (previousIdentity, nextIdentity) =>`) {
		t.Fatal("W2 requests are not fenced by the existing authenticated-account transition")
	}
	if strings.Contains(js, `body: JSON.stringify({ action, expectedRevision, surface: surfaceName, values,`) || strings.Contains(js, `organizationId:`) || strings.Contains(js, `personId:`) {
		t.Fatal("W2 mutation body carries client authority fields")
	}
}

func TestStrideW2PendingMutationRecoveryIsAccountScopedAndAlwaysReachable(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.LastIndex(html, `/* W2 contribution network route shell.`)
	end := strings.Index(html[start:], `</script>`)
	if start < 0 || end < 0 {
		t.Fatal("W2 route script slice missing")
	}
	js := html[start : start+end]
	for _, marker := range []string{
		`const pendingActionOperationsByAccount = new Map()`,
		`if (previousFingerprint && pendingActionOperation) pendingActionOperationsByAccount.set(previousFingerprint, pendingActionOperation)`,
		`const persistedOperation = nextIdentity ? loadPendingActionOperation(nextIdentity) : null`,
		`pendingActionOperation = pendingPersistenceInvalid ? null : persistedOperation || pendingActionOperationsByAccount.get(nextFingerprint) || null`,
		`function syncPendingActionControl()`,
		`document.body.append(control)`,
		`returnButton.addEventListener('click', returnToPendingActionOrigin)`,
		`discard.addEventListener('click', clearPendingActionOperation)`,
		`if (isStrideRoute(path))`,
		`if (path === '/settings/organizations')`,
		`loadProjection('organizations', target, path)`,
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("pending mutation recovery missing %q", marker)
		}
	}
	if !strings.Contains(html, `window.__strideW2FenceAuthority?.(previousIdentity, nextIdentity)`) {
		t.Error("authenticated account refresh does not invoke the W2 account-slot fence")
	}
	if strings.Contains(js, `if (identityChanged) clearPendingActionOperation()`) || strings.Contains(js, `pendingActionOperationsByAccount.clear()`) {
		t.Fatal("same-account refresh or A-B-A switch destroys an unresolved idempotency slot")
	}
}

func TestStrideW2PendingMutationPersistsClosedAccountScopedEnvelopeBeforeSend(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	start := strings.LastIndex(html, `/* W2 contribution network route shell.`)
	end := strings.Index(html[start:], `</script>`)
	if start < 0 || end < 0 {
		t.Fatal("W2 route script slice missing")
	}
	js := html[start : start+end]
	for _, marker := range []string{
		`const pendingStoragePrefix = 'stride.w2.pending.v1.'`,
		`new Set(['version','accountFingerprint','actionId','action','surface','expectedRevision','values','idempotencyKey','origin'])`,
		`function accountFingerprint(accountKey)`,
		`function closedStoredValues(action, candidate)`,
		`function validateStoredPending(candidate, accountKey)`,
		`exactKeys(candidate,pendingStorageKeys)`,
		`candidate.accountFingerprint !== fingerprint`,
		`actionSurfaces.get(candidate.action) !== candidate.surface`,
		`surfaceForPath(candidate.origin) === candidate.surface`,
		`localStorage.setItem(key,JSON.stringify(record))`,
		`localStorage.getItem(key)`,
		`localStorage.removeItem(key)`,
		`pendingPersistenceInvalid = true`,
		`pendingActionOperation = pendingPersistenceInvalid ? null`,
		`if (!persistPendingActionOperation(requestAuthority.accountKey,pendingActionOperation))`,
		`if (pendingActionOperation?.actionId === action.id`,
		`control.value = Array.isArray(persisted) ? persisted.join(', ') : persisted`,
		`Reconcile with the current server-minted action, retry that exact operation, or discard it`,
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("durable pending mutation contract missing %q", marker)
		}
	}
	store := strings.Index(js, `persistPendingActionOperation(requestAuthority.accountKey,pendingActionOperation)`)
	send := strings.Index(js, "fetch(`/api/stride/v1/mobile/actions/")
	if store < 0 || send < 0 || store > send {
		t.Fatal("pending mutation envelope is not persisted before the mutation request")
	}
	persistStart := strings.Index(js, `function persistPendingActionOperation`)
	persistEnd := strings.Index(js[persistStart:], `function loadPendingActionOperation`)
	if persistStart < 0 || persistEnd < 0 {
		t.Fatal("pending persistence function missing")
	}
	persist := js[persistStart : persistStart+persistEnd]
	for _, forbidden := range []string{"token", "session", "authority", "email", "organizationId", "personId", "membershipId", "grantId"} {
		if strings.Contains(strings.ToLower(persist), strings.ToLower(forbidden)) {
			t.Errorf("persisted retry record contains forbidden authority/private field %q", forbidden)
		}
	}
}

func TestStrideW2OrganizationSwitcherIsServerProjectedAndHonest(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, marker := range []string{
		`id="topbarOrganizationSwitcher"`, `min-height: 40px`,
		`id="topbarOrganizationName"`, `>Organizations</strong>`,
		`id="topbarOrganizationMenu"`, `id="topbarOrganizationMenuItems"`,
		`id="topbarOrganizationCreate"`, `<span>Create organization</span>`,
		`renderOrganizationSwitcher(envelope)`, `item.status === 'current'`,
		`const current = active.find(item => item.status === 'current')`, `active.length > 3`,
		`button.setAttribute('aria-checked', item === current ? 'true' : 'false')`,
		`loadProjection('organizations'`, `openSettings({ section: 'organizations'`,
		`window.__strideCurrentOrganizationLabel`,
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("organization switcher missing %q", marker)
		}
	}
	if strings.Contains(html, `items.find(item => item.status === 'current') || active[0]`) {
		t.Fatal("organization switcher must never infer current authority from an active membership")
	}
	for _, stale := range []string{`id="topbarOrganizationRole"`, `id="topbarOrganizationCount"`, `id="topbarOrganizationPending"`, `0 of 3 active`, `item.detail?.pendingCount`} {
		if strings.Contains(html, stale) {
			t.Errorf("closed organization switcher still exposes superseded status detail %q", stale)
		}
	}
	for _, stale := range []string{`class="topbar__organization-name">Bonfire`, `Stride · Bonfire organization`} {
		if strings.Contains(html, stale) {
			t.Errorf("organization switcher retains hardcoded authority %q", stale)
		}
	}
	settingsStart := strings.Index(html, `data-settings-section="organizations"`)
	settingsEnd := strings.Index(html[settingsStart:], `</section>`)
	if settingsStart < 0 || settingsEnd < 0 {
		t.Fatal("Settings Organizations section missing")
	}
	settings := html[settingsStart : settingsStart+settingsEnd]
	for _, stale := range []string{"Bonfire", "Current workspace", `data-stride-w2-open="/org/people"`} {
		if strings.Contains(settings, stale) {
			t.Errorf("Settings Organizations assumes static membership authority %q", stale)
		}
	}
}
