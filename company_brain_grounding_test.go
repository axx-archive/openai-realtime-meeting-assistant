package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type companyBrainViewerArtifactAuthorizer struct {
	deniedEmail string
}

func (authorizer companyBrainViewerArtifactAuthorizer) AuthorizeArtifactHeader(_ context.Context, user *userAccount, action ACLAction, header ArtifactAuthorizationHeader) bool {
	return user != nil && action == ACLReadContent &&
		strings.TrimSpace(header.ObjectID) != "" &&
		strings.TrimSpace(header.TenantID) == canonicalArtifactTenantID() &&
		normalizeAccountEmail(user.Email) != normalizeAccountEmail(authorizer.deniedEmail)
}

type companyBrainCanonicalAudienceMapper struct {
	people map[string]string
}

func (mapper companyBrainCanonicalAudienceMapper) WithMappedLegacyPerson(_ context.Context, digest string, use func(string) error) error {
	personID := mapper.people[digest]
	if personID == "" || use == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return use(personID)
}

func (mapper companyBrainCanonicalAudienceMapper) WithMappedLegacyOrganizationPrincipal(_ context.Context, digest string, request StrideE10TenantPrincipal, use func(StrideE10TenantPrincipal) error) error {
	personID := mapper.people[digest]
	if personID == "" || use == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return use(StrideE10TenantPrincipal{
		TenantID: request.TenantID, PersonID: personID,
		ActiveOrganizationID: request.TenantID, OrganizationMembershipID: "membership-" + personID,
		OrganizationMembershipRev: 1, AuthorityGeneration: request.AuthorityGeneration,
	})
}

type companyBrainLegacyPersonOnlyMapper struct{ people map[string]string }

func (mapper companyBrainLegacyPersonOnlyMapper) WithMappedLegacyPerson(_ context.Context, digest string, use func(string) error) error {
	personID := mapper.people[digest]
	if personID == "" || use == nil {
		return ErrStrideE10TenantAuthorityStale
	}
	return use(personID)
}

type companyBrainCanonicalArtifactAuthorizer struct {
	deniedPersonID string
	seen           []string
}

func (*companyBrainCanonicalArtifactAuthorizer) AuthorizeArtifactHeader(context.Context, *userAccount, ACLAction, ArtifactAuthorizationHeader) bool {
	return false
}

func (authorizer *companyBrainCanonicalArtifactAuthorizer) AuthorizeArtifactHeaderForStridePrincipal(_ context.Context, principal StrideE10TenantPrincipal, action ACLAction, header ArtifactAuthorizationHeader) bool {
	authorizer.seen = append(authorizer.seen, principal.PersonID)
	return action == ACLReadContent && principal.TenantID == header.TenantID && principal.PersonID != authorizer.deniedPersonID
}

func TestCompanyBrainGroundingOlderRelevantSourceOutranksRecentNoiseFromReplyAsk(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	relevant, _, err := app.createOSArtifactWithMetadata("artifacts", "Western creator engagement army thesis", "The western creator engagement army should organize thousands of opt-in creators around attributable experience proof.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	var recent []meetingMemoryEntry
	for index := 0; index < 7; index++ {
		entry, _, createErr := app.createOSArtifactWithMetadata("artifacts", fmt.Sprintf("Recent creator admin note %d", index), fmt.Sprintf("Creator scheduling logistics update %d; no market thesis or audience-engine decision.", index), "AJ", map[string]string{
			"type": artifactTypeMarkdown, "visibility": "organization", "ownerEmail": "aj@shareability.com",
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		recent = append(recent, entry)
	}

	engine := newGoalEngine(app)
	plan := &goalPlan{
		ProcessID:   packagingStudioProcessID,
		Objective:   "Build the requested presentation",
		RequestedBy: "aj@shareability.com",
	}
	packet := "Authorized source packet\nReply-thread context: Assess a western creator engagement army of thousands of on-demand creators."
	grounding, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, packet)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grounding, relevant.ID) {
		t.Fatalf("older exact ask match was lost behind recent artifacts:\n%s", grounding)
	}
	relevantAt := strings.Index(grounding, relevant.ID)
	recentIncludedAt := -1
	for _, entry := range recent {
		if index := strings.Index(grounding, entry.ID); index >= 0 && (recentIncludedAt < 0 || index < recentIncludedAt) {
			recentIncludedAt = index
		}
	}
	if recentIncludedAt < 0 {
		t.Fatalf("test did not retain a recent lower-score control candidate:\n%s", grounding)
	}
	if relevantAt > recentIncludedAt {
		t.Fatalf("recent lexical noise outranked the older exact ask match:\n%s", grounding)
	}
	for _, want := range []string{
		"ask-conditioned",
		"query_digest=",
		"kind=os_artifact",
		"Coverage (included / ask-relevant authorized candidates)",
		"Policy exclusions and limits",
		"Unmatched recency",
	} {
		if !strings.Contains(grounding, want) {
			t.Errorf("grounding missing %q:\n%s", want, grounding)
		}
	}
	if len(grounding) > companyBrainContextMaxBytes {
		t.Fatalf("grounding bytes=%d, max=%d", len(grounding), companyBrainContextMaxBytes)
	}

	authority, err := processInternalAuthoritySources(app, plan)
	if err != nil {
		t.Fatal(err)
	}
	ref := companyBrainEntryAuthorityRef(relevant)
	if source, ok := authority[ref]; !ok || !strings.Contains(source.Text, "western creator engagement army") {
		t.Fatalf("older retrieved source did not remain exact evidence authority: ref=%q source=%+v", ref, source)
	}
}

func TestCompanyBrainGroundingCoversBoundedAuthorizedLanes(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const topic = "trailblazer creator network"
	packageArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "Trailblazer package brief", "The trailblazer creator network pilot uses opt-in correspondents.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "packageId": "package-trailblazer", "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, _, err := app.memory.appendDecision("decision-trailblazer", "The trailblazer creator network pilot starts with attributable opt-in posts.", map[string]string{
		"status": decisionStatusActive, "packageId": "package-trailblazer", "visibility": "organization",
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, created, err := app.ensureScoutChatThread("trailblazer-channel", "aj@shareability.com", "AJ", "Trailblazer", scoutChatVisibilityPublic, nil)
	if err != nil || !created {
		t.Fatalf("create channel: created=%t err=%v", created, err)
	}
	if _, err := app.commitScoutChatThreadMessages("aj@shareability.com", channel.ID, scoutChatMessageRecord{
		ID: "trailblazer-channel-message", Kind: "message", Role: "user", Text: "The trailblazer creator network should prove demand with local field reports.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: "AJ", AuthorEmail: "aj@shareability.com",
	}); err != nil {
		t.Fatal(err)
	}
	meeting, _, err := app.memory.appendRoomChatTranscript("trailblazer-meeting", "Tyler", "The trailblazer creator network needs a rights-safe participation rule.")
	if err != nil {
		t.Fatal(err)
	}
	deliverable, _, err := app.createOSArtifactWithMetadata("artifacts", "Prior trailblazer creator network deck", "A prior deliverable mapped the trailblazer creator network into three launch cohorts.", "AJ", map[string]string{
		"type": artifactTypeHTMLDeck, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := seedTasteProfileArtifact(t, app, "AJ", "Prefer human field detail over generic creator-economy language.")
	style := seedHouseStyleArtifact(t, app, "Banned pattern: unsupported scale language. Lead with an attributable proof point.")

	engine := newGoalEngine(app)
	plan := &goalPlan{ProcessID: packagingStudioProcessID, Objective: "Build a " + topic + " decision deck", RequestedBy: "aj@shareability.com", PackageID: "package-trailblazer"}
	grounding, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []meetingMemoryEntry{packageArtifact, decision, meeting, deliverable, profile, style} {
		if !strings.Contains(grounding, entry.ID) {
			t.Errorf("grounding omitted %s/%s:\n%s", entry.Kind, entry.ID, grounding)
		}
	}
	if !strings.Contains(grounding, "trailblazer-channel-message") {
		t.Fatalf("authorized channel context was not retrieved:\n%s", grounding)
	}
	for _, lane := range companyBrainGroundingLaneOrder {
		if !strings.Contains(grounding, string(lane)) {
			t.Errorf("grounding missing lane %q", lane)
		}
	}
	if len(grounding) > companyBrainContextMaxBytes {
		t.Fatalf("grounding bytes=%d, max=%d", len(grounding), companyBrainContextMaxBytes)
	}
}

func TestCompanyBrainGroundingCannotLaunderPrivateSourcesIntoSharedChannel(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const topic = "copper canyon creator proof"
	visible, _, err := app.createOSArtifactWithMetadata("artifacts", "Copper Canyon public brief", topic+" is an organization-visible pilot.", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "organization", "ownerEmail": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	requesterPrivate, _, err := app.createOSArtifactWithMetadata("artifacts", "AJ private scratchpad", topic+" REQUESTER PRIVATE CANARY", "AJ", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "ownerEmail": "aj@shareability.com", "requestedBy": "aj@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherPrivate, _, err := app.createOSArtifactWithMetadata("artifacts", "Tom private scratchpad", topic+" OTHER OWNER PRIVATE CANARY", "Tom", map[string]string{
		"type": artifactTypeMarkdown, "visibility": "private", "ownerEmail": "tom@shareability.com", "requestedBy": "tom@shareability.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectTranscript, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "copper-private-project-transcript", "", "AJ", "", topic+" PROJECT-ONLY TRANSCRIPT CANARY", map[string]string{
		"visibility": "project", "ownerEmail": "aj@shareability.com", "memberEmails": "aj@shareability.com", "source": transcriptSourceChannel, "threadId": "aj-only-project",
	}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	channel, created, err := app.ensureScoutChatThread("copper-shared-channel", "aj@shareability.com", "AJ", "Copper Canyon", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created {
		t.Fatalf("create shared channel: created=%t err=%v", created, err)
	}

	engine := newGoalEngine(app)
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, Objective: "Build a " + topic + " deck", RequestedBy: "aj@shareability.com",
		RouteReceipt: &goalRouteReceipt{Requester: "aj@shareability.com", OriginKind: agentThreadOriginChannel, OriginID: channel.ID},
	}
	grounding, err := engine.processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grounding, visible.ID) {
		t.Fatalf("positive-control organization source was lost:\n%s", grounding)
	}
	for _, denied := range []meetingMemoryEntry{requesterPrivate, otherPrivate, projectTranscript} {
		if strings.Contains(grounding, denied.ID) || strings.Contains(grounding, "PRIVATE CANARY") || strings.Contains(grounding, "PROJECT-ONLY TRANSCRIPT CANARY") {
			t.Fatalf("shared Company Brain grounding leaked %s/%s:\n%s", denied.Kind, denied.ID, grounding)
		}
	}
	if !strings.Contains(grounding, "readable by every destination member") || !strings.Contains(grounding, "identities and counts are intentionally not exposed") {
		t.Fatalf("shared authorization/exclusion disclosure missing:\n%s", grounding)
	}
}

func TestCompanyBrainGroundingOrganizationPublicDestinationRejectsRequesterReadableNarrowNonArtifacts(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const topic = "ember atlas audience proof"
	projectMetadata := map[string]string{
		"visibility": "project", "ownerEmail": "aj@shareability.com", "memberEmails": "aj@shareability.com", "tenantId": canonicalArtifactTenantID(),
	}
	privateMetadata := map[string]string{
		"visibility": "private", "ownerEmail": "aj@shareability.com", "tenantId": canonicalArtifactTenantID(),
	}
	organizationMetadata := map[string]string{
		"visibility": "organization", "tenantId": canonicalArtifactTenantID(),
	}

	projectGeneric, _, err := app.memory.appendNarrative("brain-project-generic", topic+" PROJECT GENERIC CANARY", projectMetadata)
	if err != nil {
		t.Fatal(err)
	}
	projectDecisionMetadata := cloneStringMap(projectMetadata)
	projectDecisionMetadata["status"] = decisionStatusActive
	projectDecision, _, err := app.memory.appendDecision("brain-project-decision", topic+" PROJECT DECISION CANARY", projectDecisionMetadata)
	if err != nil {
		t.Fatal(err)
	}
	projectTranscriptMetadata := cloneStringMap(projectMetadata)
	projectTranscriptMetadata["source"] = transcriptSourceChannel
	projectTranscript, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "brain-project-transcript", "", "AJ", "", topic+" PROJECT TRANSCRIPT CANARY", projectTranscriptMetadata, true, "")
	if err != nil {
		t.Fatal(err)
	}
	privateDecisionMetadata := cloneStringMap(privateMetadata)
	privateDecisionMetadata["status"] = decisionStatusActive
	privateDecision, _, err := app.memory.appendDecision("brain-private-decision", topic+" PRIVATE DECISION CANARY", privateDecisionMetadata)
	if err != nil {
		t.Fatal(err)
	}
	privateTranscriptMetadata := cloneStringMap(privateMetadata)
	privateTranscriptMetadata["source"] = transcriptSourceChannel
	privateTranscript, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "brain-private-transcript", "", "AJ", "", topic+" PRIVATE TRANSCRIPT CANARY", privateTranscriptMetadata, true, "")
	if err != nil {
		t.Fatal(err)
	}
	crossTenantMetadata := cloneStringMap(organizationMetadata)
	crossTenantMetadata["tenantId"] = "other-tenant"
	crossTenant, _, err := app.memory.appendNarrative("brain-other-tenant", topic+" CROSS TENANT CANARY", crossTenantMetadata)
	if err != nil {
		t.Fatal(err)
	}

	organizationGeneric, _, err := app.memory.appendNarrative("brain-organization-generic", topic+" is an organization-wide narrative source.", organizationMetadata)
	if err != nil {
		t.Fatal(err)
	}
	organizationDecisionMetadata := cloneStringMap(organizationMetadata)
	organizationDecisionMetadata["status"] = decisionStatusActive
	organizationDecision, _, err := app.memory.appendDecision("brain-organization-decision", topic+" is an organization-wide settled decision.", organizationDecisionMetadata)
	if err != nil {
		t.Fatal(err)
	}
	organizationTranscriptMetadata := cloneStringMap(organizationMetadata)
	organizationTranscriptMetadata["source"] = transcriptSourceChannel
	organizationTranscript, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "brain-organization-transcript", "", "AJ", "", topic+" is an organization-wide transcript source.", organizationTranscriptMetadata, true, "")
	if err != nil {
		t.Fatal(err)
	}

	channel, created, err := app.ensureScoutChatThread("ember-atlas-organization", "aj@shareability.com", "AJ", "Ember Atlas", scoutChatVisibilityPublic, nil)
	if err != nil || !created || !scoutChatThreadIsOrganizationPublic(channel) {
		t.Fatalf("create organization-public channel: created=%t thread=%+v err=%v", created, channel, err)
	}
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, Objective: "Build an " + topic + " deck", RequestedBy: "aj@shareability.com",
		RouteReceipt: &goalRouteReceipt{Requester: "aj@shareability.com", OriginKind: agentThreadOriginChannel, OriginID: channel.ID},
	}
	grounding, err := newGoalEngine(app).processStageCompanyContextAuthorized(context.Background(), plan, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []meetingMemoryEntry{organizationGeneric, organizationDecision, organizationTranscript} {
		if !strings.Contains(grounding, allowed.ID) {
			t.Errorf("organization-public grounding omitted legitimate %s/%s:\n%s", allowed.Kind, allowed.ID, grounding)
		}
	}
	for _, denied := range []meetingMemoryEntry{projectGeneric, projectDecision, projectTranscript, privateDecision, privateTranscript, crossTenant} {
		if strings.Contains(grounding, denied.ID) {
			t.Errorf("organization-public grounding admitted narrow or cross-tenant %s/%s:\n%s", denied.Kind, denied.ID, grounding)
		}
	}
	for _, canary := range []string{"PROJECT GENERIC CANARY", "PROJECT DECISION CANARY", "PROJECT TRANSCRIPT CANARY", "PRIVATE DECISION CANARY", "PRIVATE TRANSCRIPT CANARY", "CROSS TENANT CANARY"} {
		if strings.Contains(grounding, canary) {
			t.Errorf("organization-public grounding leaked %q:\n%s", canary, grounding)
		}
	}
}

func TestCompanyBrainGroundingPrivateAndExactProjectDestinationsPreserveNarrowNonArtifacts(t *testing.T) {
	t.Run("requester private result", func(t *testing.T) {
		app := newIsolatedKanbanBoardApp(t)
		const topic = "juniper signal proof"
		projectMetadata := map[string]string{
			"visibility": "project", "ownerEmail": "aj@shareability.com", "memberEmails": "aj@shareability.com", "tenantId": canonicalArtifactTenantID(),
		}
		privateMetadata := map[string]string{
			"visibility": "private", "ownerEmail": "aj@shareability.com", "tenantId": canonicalArtifactTenantID(),
		}
		projectDecisionMetadata := cloneStringMap(projectMetadata)
		projectDecisionMetadata["status"] = decisionStatusActive
		projectDecision, _, err := app.memory.appendDecision("private-run-project-decision", topic+" project decision", projectDecisionMetadata)
		if err != nil {
			t.Fatal(err)
		}
		projectTranscriptMetadata := cloneStringMap(projectMetadata)
		projectTranscriptMetadata["source"] = transcriptSourceChannel
		projectTranscript, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "private-run-project-transcript", "", "AJ", "", topic+" project transcript", projectTranscriptMetadata, true, "")
		if err != nil {
			t.Fatal(err)
		}
		privateDecisionMetadata := cloneStringMap(privateMetadata)
		privateDecisionMetadata["status"] = decisionStatusActive
		privateDecision, _, err := app.memory.appendDecision("private-run-private-decision", topic+" private decision", privateDecisionMetadata)
		if err != nil {
			t.Fatal(err)
		}
		privateTranscriptMetadata := cloneStringMap(privateMetadata)
		privateTranscriptMetadata["source"] = transcriptSourceChannel
		privateTranscript, _, err := app.memory.appendAttributedTranscriptEntry(officeRoomID, "private-run-private-transcript", "", "AJ", "", topic+" private transcript", privateTranscriptMetadata, true, "")
		if err != nil {
			t.Fatal(err)
		}
		thread, created, err := app.ensureScoutChatThread("juniper-private", "aj@shareability.com", "AJ", "Scout", scoutChatVisibilityPrivate, nil)
		if err != nil || !created {
			t.Fatalf("create private destination: created=%t err=%v", created, err)
		}
		plan := &goalPlan{
			ProcessID: packagingStudioProcessID, Objective: "Build a " + topic + " deck", RequestedBy: "aj@shareability.com",
			RouteReceipt: &goalRouteReceipt{Requester: "aj@shareability.com", OriginKind: agentThreadOriginPrivateThread, OriginID: thread.ID},
		}
		grounding, err := newGoalEngine(app).processStageCompanyContextAuthorized(context.Background(), plan, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, allowed := range []meetingMemoryEntry{projectDecision, projectTranscript, privateDecision, privateTranscript} {
			if !strings.Contains(grounding, allowed.ID) {
				t.Errorf("requester-private grounding omitted readable %s/%s:\n%s", allowed.Kind, allowed.ID, grounding)
			}
		}
	})

	t.Run("exact project audience", func(t *testing.T) {
		app := newIsolatedKanbanBoardApp(t)
		const topic = "sagebrush signal proof"
		exactMetadata := map[string]string{
			"visibility": "project", "ownerEmail": "aj@shareability.com", "memberEmails": "aj@shareability.com,e@shareability.com", "tenantId": canonicalArtifactTenantID(), "status": decisionStatusActive,
		}
		narrowMetadata := cloneStringMap(exactMetadata)
		narrowMetadata["memberEmails"] = "aj@shareability.com"
		exact, _, err := app.memory.appendDecision("exact-project-decision", topic+" exact audience decision", exactMetadata)
		if err != nil {
			t.Fatal(err)
		}
		narrow, _, err := app.memory.appendDecision("narrow-project-decision", topic+" narrow audience decision", narrowMetadata)
		if err != nil {
			t.Fatal(err)
		}
		channel, created, err := app.ensureScoutChatThread("sagebrush-project", "aj@shareability.com", "AJ", "Sagebrush", scoutChatVisibilityPublic, []string{"e@shareability.com"})
		if err != nil || !created || scoutChatThreadIsOrganizationPublic(channel) {
			t.Fatalf("create exact project destination: created=%t thread=%+v err=%v", created, channel, err)
		}
		plan := &goalPlan{
			ProcessID: packagingStudioProcessID, Objective: "Build a " + topic + " deck", RequestedBy: "aj@shareability.com",
			RouteReceipt: &goalRouteReceipt{Requester: "aj@shareability.com", OriginKind: agentThreadOriginChannel, OriginID: channel.ID},
		}
		grounding, err := newGoalEngine(app).processStageCompanyContextAuthorized(context.Background(), plan, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(grounding, exact.ID) {
			t.Fatalf("exact project audience source was lost:\n%s", grounding)
		}
		if strings.Contains(grounding, narrow.ID) {
			t.Fatalf("narrower project source was laundered into the project destination:\n%s", grounding)
		}
	})
}

func TestCompanyBrainOrganizationPublicArtifactChecksEveryPersistedAuthenticatedAccount(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})

	store := accountStore()
	seed := store.findUser("aj@shareability.com")
	if seed == nil {
		t.Fatal("seed account is unavailable")
	}
	const extraEmail = "future-teammate@shareability.com"
	extra := &userAccount{
		Email:             extraEmail,
		Name:              "Future Teammate",
		PasswordHash:      append([]byte(nil), seed.PasswordHash...),
		WebAuthnHandle:    []byte("future-teammate-distinct-handle"),
		PasswordChangedAt: time.Now().UTC(),
	}
	store.mu.Lock()
	store.users[extraEmail] = extra
	persistErr := store.persistLocked()
	store.mu.Unlock()
	if persistErr != nil {
		t.Fatalf("persist non-seeded account: %v", persistErr)
	}
	for _, seeded := range seededAccounts {
		if normalizeAccountEmail(seeded.Email) == extraEmail {
			t.Fatalf("adversarial account %q unexpectedly belongs to seededAccounts", extraEmail)
		}
	}
	reloaded, err := newUserAccountStore(usersFilePath())
	if err != nil {
		t.Fatalf("reload persisted account store: %v", err)
	}
	if _, authenticated := reloaded.authenticate(extraEmail, configuredMeetingRoomPassword()); !authenticated {
		t.Fatal("non-seeded persisted account is not an authenticated organization viewer")
	}

	artifact, appended, err := app.memory.appendOSArtifact("brain-org-artifact", "Ember Atlas organization artifact", map[string]string{
		"tenantId":   canonicalArtifactTenantID(),
		"objectId":   "brain-org-artifact",
		"visibility": "organization",
		"ownerEmail": "aj@shareability.com",
		"type":       artifactTypeMarkdown,
	})
	if err != nil || !appended {
		t.Fatalf("append organization artifact: appended=%t err=%v", appended, err)
	}
	organizationChannel, created, err := app.ensureScoutChatThread("brain-org-artifact-destination", "aj@shareability.com", "AJ", "Ember Atlas", scoutChatVisibilityPublic, nil)
	if err != nil || !created || !scoutChatThreadIsOrganizationPublic(organizationChannel) {
		t.Fatalf("create organization-public destination: created=%t thread=%+v err=%v", created, organizationChannel, err)
	}
	organizationPlan := &goalPlan{
		ProcessID:   packagingStudioProcessID,
		Objective:   "Build the Ember Atlas deck",
		RequestedBy: "aj@shareability.com",
		RouteReceipt: &goalRouteReceipt{
			Requester:  "aj@shareability.com",
			OriginKind: agentThreadOriginChannel,
			OriginID:   organizationChannel.ID,
		},
	}

	// The requester and every seeded account may read this artifact, but the
	// persisted non-seeded viewer may not. An organization-public delivery must
	// therefore fail closed instead of treating seededAccounts as the audience.
	artifactObjectAuthorizer = companyBrainViewerArtifactAuthorizer{deniedEmail: extraEmail}
	engine := newGoalEngine(app)
	if engine.companyBrainEntryMayRankForDestination(context.Background(), organizationPlan, artifact, true) {
		t.Fatal("organization-public Company Brain admitted an artifact denied to a persisted non-seeded viewer")
	}
	deniedGrounding, err := engine.processStageCompanyContextAuthorized(context.Background(), organizationPlan, "")
	if err != nil {
		t.Fatalf("build denied organization-public grounding: %v", err)
	}
	if strings.Contains(deniedGrounding, artifact.ID) || strings.Contains(deniedGrounding, artifact.Text) {
		t.Fatalf("organization-public grounding leaked the denied artifact:\n%s", deniedGrounding)
	}

	// A project channel has an exact audience. A non-member's denial must not
	// broaden that audience or suppress material readable by every actual member.
	projectChannel, created, err := app.ensureScoutChatThread("brain-project-artifact-destination", "aj@shareability.com", "AJ", "Ember Atlas Project", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created || scoutChatThreadIsOrganizationPublic(projectChannel) {
		t.Fatalf("create exact project destination: created=%t thread=%+v err=%v", created, projectChannel, err)
	}
	projectPlan := &goalPlan{
		ProcessID:   packagingStudioProcessID,
		Objective:   "Build the Ember Atlas project deck",
		RequestedBy: "aj@shareability.com",
		RouteReceipt: &goalRouteReceipt{
			Requester:  "aj@shareability.com",
			OriginKind: agentThreadOriginChannel,
			OriginID:   projectChannel.ID,
		},
	}
	if !engine.companyBrainEntryMayRankForDestination(context.Background(), projectPlan, artifact, true) {
		t.Fatal("exact project delivery incorrectly treated a non-member as part of its audience")
	}

	artifactObjectAuthorizer = companyBrainViewerArtifactAuthorizer{}
	if !engine.companyBrainEntryMayRankForDestination(context.Background(), organizationPlan, artifact, true) {
		t.Fatal("organization-public artifact readable by every persisted account was rejected")
	}
	allowedGrounding, err := engine.processStageCompanyContextAuthorized(context.Background(), organizationPlan, "")
	if err != nil {
		t.Fatalf("build allowed organization-public grounding: %v", err)
	}
	if !strings.Contains(allowedGrounding, artifact.ID) || !strings.Contains(allowedGrounding, artifact.Text) {
		t.Fatalf("organization-public grounding omitted the artifact readable by every persisted viewer:\n%s", allowedGrounding)
	}
	crossTenant := artifact
	crossTenant.Metadata = cloneStringMap(artifact.Metadata)
	crossTenant.Metadata["tenantId"] = "other-tenant"
	if engine.companyBrainEntryMayRankForDestination(context.Background(), organizationPlan, crossTenant, true) {
		t.Fatal("cross-tenant artifact entered an organization-public Company Brain delivery")
	}
}

func TestCompanyBrainProjectArtifactCannotLaunderThroughOrganizationPublicFallback(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = app
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{TenantID: canonicalArtifactTenantID()}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})

	project, created, err := app.ensureScoutChatThread("brain-project-source", "aj@shareability.com", "AJ", "Project source", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created || scoutChatThreadIsOrganizationPublic(project) {
		t.Fatalf("create exact-member source project: created=%t thread=%+v err=%v", created, project, err)
	}
	organization, created, err := app.ensureScoutChatThread("brain-organization-destination", "aj@shareability.com", "AJ", "Organization destination", scoutChatVisibilityPublic, nil)
	if err != nil || !created || !scoutChatThreadIsOrganizationPublic(organization) {
		t.Fatalf("create organization destination: created=%t thread=%+v err=%v", created, organization, err)
	}
	artifact, appended, err := app.memory.appendOSArtifact("stage-project-proof", "Project-only stage proof", map[string]string{
		"tenantId": canonicalArtifactTenantID(), "objectId": "stage-project-proof", "visibility": scoutChatVisibilityPublic,
		"originKind": agentThreadOriginChannel, "originId": project.ID, "originSurface": "chat:" + project.ID,
		"ownerEmail": "aj@shareability.com", "type": artifactTypeMarkdown,
	})
	if err != nil || !appended || strings.TrimSpace(artifact.Metadata["originThreadId"]) != "" {
		t.Fatalf("append real route-bound stage artifact: appended=%t artifact=%+v err=%v", appended, artifact, err)
	}

	orgMetadata := map[string]string{"originKind": agentThreadOriginChannel, "originId": organization.ID, "requestedBy": "aj@shareability.com"}
	if app.agentThreadEntryAuthorizedForDestination(context.Background(), orgMetadata, artifact) {
		t.Fatal("exact-member project artifact laundered into an organization-public result through the legacy public fallback")
	}
	projectMetadata := map[string]string{"originKind": agentThreadOriginChannel, "originId": project.ID, "requestedBy": "aj@shareability.com"}
	if !app.agentThreadEntryAuthorizedForDestination(context.Background(), projectMetadata, artifact) {
		t.Fatal("exact-member project artifact was rejected for its unchanged exact audience")
	}
}

func TestCompanyBrainEntryAuthorizationUsesEngineAppWhenGlobalAppIsPoisoned(t *testing.T) {
	receiver := newIsolatedKanbanBoardApp(t)
	source, created, err := receiver.ensureScoutChatThread("brain-receiver-scoped-source", "aj@shareability.com", "AJ", "Receiver source", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created {
		t.Fatalf("create receiver source: created=%t err=%v", created, err)
	}
	artifact, appended, err := receiver.memory.appendOSArtifact("brain-receiver-scoped-artifact", "Receiver-scoped Company Brain proof", map[string]string{
		"tenantId": canonicalArtifactTenantID(), "objectId": "brain-receiver-scoped-artifact", "originSurface": "chat:" + source.ID,
		"requestedBy": "aj@shareability.com", "type": artifactTypeMarkdown,
	})
	if err != nil || !appended {
		t.Fatalf("append receiver artifact: appended=%t err=%v", appended, err)
	}

	poison := newIsolatedKanbanBoardApp(t)
	if _, created, err := poison.ensureScoutChatThread(source.ID, "e@shareability.com", "E", "Unrelated private source", scoutChatVisibilityPrivate, nil); err != nil || !created {
		t.Fatalf("create poisoned global source: created=%t err=%v", created, err)
	}
	receiverHeader := receiver.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	poisonHeader := poison.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	if receiverHeader.Visibility != scoutChatVisibilityPublic || poisonHeader.Visibility != scoutChatVisibilityPrivate || poisonHeader.OwnerEmail != "e@shareability.com" {
		t.Fatalf("fixture did not create conflicting receiver/global authority: receiver=%+v poison=%+v", receiverHeader, poisonHeader)
	}

	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = poison
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{TenantID: canonicalArtifactTenantID()}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})
	plan := &goalPlan{ProcessID: packagingStudioProcessID, RequestedBy: "aj@shareability.com"}
	if !newGoalEngine(receiver).companyBrainEntryAuthorizedForDestination(context.Background(), plan, artifact, false) {
		t.Fatal("Company Brain authorization consulted the poisoned process-global app instead of the engine app")
	}
}

func TestAgentThreadSharedDestinationAuthorizationUsesReceiverAppWhenGlobalAppIsPoisoned(t *testing.T) {
	receiver := newIsolatedKanbanBoardApp(t)
	destination, created, err := receiver.ensureScoutChatThread("shared-receiver-scoped-destination", "aj@shareability.com", "AJ", "Receiver destination", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created {
		t.Fatalf("create receiver destination: created=%t err=%v", created, err)
	}
	artifact, appended, err := receiver.memory.appendOSArtifact("shared-receiver-scoped-artifact", "Receiver-scoped shared destination proof", map[string]string{
		"tenantId": canonicalArtifactTenantID(), "objectId": "shared-receiver-scoped-artifact", "originSurface": "chat:" + destination.ID,
		"requestedBy": "aj@shareability.com", "type": artifactTypeMarkdown,
	})
	if err != nil || !appended {
		t.Fatalf("append receiver artifact: appended=%t err=%v", appended, err)
	}

	poison := newIsolatedKanbanBoardApp(t)
	if _, created, err := poison.ensureScoutChatThread(destination.ID, "e@shareability.com", "E", "Unrelated private destination", scoutChatVisibilityPrivate, nil); err != nil || !created {
		t.Fatalf("create poisoned global destination: created=%t err=%v", created, err)
	}
	receiverHeader := receiver.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	poisonHeader := poison.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact))
	if receiverHeader.Visibility != scoutChatVisibilityPublic || poisonHeader.Visibility != scoutChatVisibilityPrivate || poisonHeader.OwnerEmail != "e@shareability.com" {
		t.Fatalf("fixture did not create conflicting receiver/global authority: receiver=%+v poison=%+v", receiverHeader, poisonHeader)
	}

	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = poison
	artifactObjectAuthorizer = LegacyCompatibleObjectAuthorizer{TenantID: canonicalArtifactTenantID()}
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})
	metadata := map[string]string{
		"originKind": agentThreadOriginChannel, "originId": destination.ID, "requestedBy": "aj@shareability.com",
	}
	if !receiver.agentThreadEntryAuthorizedForDestination(context.Background(), metadata, artifact) {
		t.Fatal("shared-destination authorization consulted the poisoned process-global app instead of the receiver app")
	}
}

func TestCompanyBrainCanonicalAudienceProofMapsEveryViewerInsteadOfReusingRequester(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	previousApp, previousAuthorizer := kanbanApp, artifactObjectAuthorizer
	kanbanApp = app
	t.Cleanup(func() {
		kanbanApp = previousApp
		artifactObjectAuthorizer = previousAuthorizer
	})

	store := accountStore()
	seed := store.findUser("aj@shareability.com")
	if seed == nil {
		t.Fatal("seed account is unavailable")
	}
	const extraEmail = "canonical-future-teammate@shareability.com"
	if store.findUser(extraEmail) == nil {
		extra := &userAccount{
			Email: extraEmail, Name: "Canonical Future Teammate", PasswordHash: append([]byte(nil), seed.PasswordHash...),
			WebAuthnHandle: []byte("canonical-future-teammate-distinct-handle"), PasswordChangedAt: time.Now().UTC(),
		}
		store.mu.Lock()
		store.users[extraEmail] = extra
		persistErr := store.persistLocked()
		store.mu.Unlock()
		if persistErr != nil {
			t.Fatalf("persist canonical audience account: %v", persistErr)
		}
	}

	organization, created, err := app.ensureScoutChatThread("canonical-brain-organization", "aj@shareability.com", "AJ", "Canonical organization", scoutChatVisibilityPublic, nil)
	if err != nil || !created || !scoutChatThreadIsOrganizationPublic(organization) {
		t.Fatalf("create organization destination: created=%t thread=%+v err=%v", created, organization, err)
	}
	project, created, err := app.ensureScoutChatThread("canonical-brain-project", "aj@shareability.com", "AJ", "Canonical project", scoutChatVisibilityPublic, []string{"e@shareability.com"})
	if err != nil || !created || scoutChatThreadIsOrganizationPublic(project) {
		t.Fatalf("create exact project destination: created=%t thread=%+v err=%v", created, project, err)
	}
	artifact, appended, err := app.memory.appendOSArtifact("canonical-org-proof", "Organization proof", map[string]string{
		"tenantId": canonicalArtifactTenantID(), "objectId": "canonical-org-proof", "visibility": "organization", "ownerEmail": "aj@shareability.com", "type": artifactTypeMarkdown,
	})
	if err != nil || !appended {
		t.Fatalf("append canonical organization artifact: appended=%t err=%v", appended, err)
	}

	people := map[string]string{}
	for _, email := range store.accountEmails() {
		people[sha256Hex([]byte(normalizeAccountEmail(email)))] = "person-" + sha256Hex([]byte(normalizeAccountEmail(email)))[:12]
	}
	deniedPerson := people[sha256Hex([]byte(extraEmail))]
	authorizer := &companyBrainCanonicalArtifactAuthorizer{deniedPersonID: deniedPerson}
	artifactObjectAuthorizer = authorizer
	mapper := companyBrainCanonicalAudienceMapper{people: people}
	restoreConverter := InstallStrideE10TenantRuntimeConverter(NewStrideE10TenantConverter(nil, nil, nil, mapper, StrideE10TenantReceiptKey{}, StrideE10TenantConversionShadow))
	t.Cleanup(func() { restoreConverter() })

	requesterPerson := people[sha256Hex([]byte("aj@shareability.com"))]
	ctx := context.WithValue(context.Background(), strideE10TenantPrincipalContextKey{}, StrideE10TenantPrincipal{
		TenantID: canonicalArtifactTenantID(), PersonID: requesterPerson, ActiveOrganizationID: canonicalArtifactTenantID(), AuthorityGeneration: 7,
	})
	orgMetadata := map[string]string{"originKind": agentThreadOriginChannel, "originId": organization.ID, "requestedBy": "aj@shareability.com"}
	if app.agentThreadEntryAuthorizedForDestination(ctx, orgMetadata, artifact) {
		t.Fatal("canonical organization proof reused the requester's principal for a denied persisted viewer")
	}
	if !containsString(authorizer.seen, deniedPerson) {
		t.Fatalf("canonical organization proof never evaluated the denied viewer; seen=%v want=%s", authorizer.seen, deniedPerson)
	}

	authorizer.seen = nil
	projectMetadata := map[string]string{"originKind": agentThreadOriginChannel, "originId": project.ID, "requestedBy": "aj@shareability.com"}
	if !app.agentThreadEntryAuthorizedForDestination(ctx, projectMetadata, artifact) {
		t.Fatalf("canonical exact-project proof rejected an artifact readable by every actual member; seen=%v", authorizer.seen)
	}
	if containsString(authorizer.seen, deniedPerson) {
		t.Fatalf("canonical project proof evaluated non-member %s; seen=%v", deniedPerson, authorizer.seen)
	}

	restoreConverter()
	restoreConverter = InstallStrideE10TenantRuntimeConverter(NewStrideE10TenantConverter(nil, nil, nil, companyBrainLegacyPersonOnlyMapper{people: people}, StrideE10TenantReceiptKey{}, StrideE10TenantConversionShadow))
	if app.agentThreadEntryAuthorizedForDestination(ctx, projectMetadata, artifact) {
		t.Fatal("canonical audience proof fell back to the request principal without an exact per-viewer mapper")
	}
}
