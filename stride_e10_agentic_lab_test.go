package main

// The Agentic Lab is a local, providerless product fixture for exercising an
// organization whose only human member is AJ. It deliberately uses the normal
// authenticated STRIDE handlers and signed local runtime; it is never seeded
// by production main and cannot call a provider, publish a network profile, or
// enroll a real user/cohort.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	strideE10AgenticLabOrganizationID = "organization_stride_agentic_lab"
	strideE10AgenticLabPersonID       = "person_stride_agentic_lab_aj"
	strideE10AgenticLabMembershipID   = "membership_stride_agentic_lab_owner"
	strideE10AgenticLabAgentAddress   = "mary.agent@stride.invalid"
)

type strideE10AgenticLabEvidence struct {
	OrganizationID         string
	HumanMemberships       int
	HumanOwner             string
	AgentSeats             []STRIDEProductTeamAgent
	ProjectThreadID        string
	PresentationThreadID   string
	RecurringWorkThreadIDs map[string]string
	PrivateThreadIDs       []string
	AgentMessageCount      int
	AttachmentCount        int
	MeetingID              string
	AgentTranscriptCount   int
	Work                   STRIDEProductWorkRecord
	Run                    STRIDEDurableWorkRun
	ArtifactID             string
	RevisedArtifactID      string
	ArtifactVersion        int
	CorrectedLearning      bool
	RevokedAgentID         string
	DeletedMessageID       string
	ActiveAttestationID    string
	ActiveAttestationRev   int64
	ReleasedFieldsDigest   string
	VerificationTier       string
	PrivatePublicationID   string
	PrivateVisibility      string
	RevokedAttestationID   string
	RevokedAttestationRev  int64
	RevocationEffects      int
	WorkRecordSections     int
	WorkRecordEvidence     int
	ContributionRestored   bool
	NetworkState           string
	NetworkVisibility      string
	ProviderCalls          int
	ExternalEffects        int
}

func TestStrideE10AgenticLabExercisesOneHumanAgentWorkGraphDefaultOff(t *testing.T) {
	configureStrideE10AgenticLabEnv(t)

	previousApp := kanbanApp
	app := newKanbanBoardApp()
	kanbanApp = app
	t.Cleanup(func() {
		if app != nil {
			_ = app.Close()
		}
		kanbanApp = previousApp
	})

	if health := app.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Configured || health.Restored {
		t.Fatalf("fresh Agentic Lab runtime health=%+v", health)
	}
	cookies := loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword)
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)

	evidence := seedStrideE10AgenticLab(t, app, mux, cookies)
	assertStrideE10AgenticLabEvidence(t, evidence)

	firstGeneration := app.strideRuntime.Health().Generation
	if err := app.strideRuntime.Save(); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	app = nil

	// A real process replacement must restore the exact signed local graph and
	// must not restart a provider session or duplicate a work run/message.
	restarted := newKanbanBoardApp()
	app = restarted
	kanbanApp = restarted
	if health := restarted.strideRuntime.Health(); health.State != STRIDERuntimeStandby || !health.Restored || health.Generation <= firstGeneration {
		t.Fatalf("restored Agentic Lab runtime health=%+v firstGeneration=%d", health, firstGeneration)
	}
	work := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", cookies, nil, ""), http.StatusOK))
	if len(work.Suggestions) != 1 || len(work.Runs) != 1 || work.Suggestions[0].ID != evidence.Work.ID || work.Runs[0].ID != evidence.Run.ID || work.ProviderCalls != 0 {
		t.Fatalf("restored Agentic Lab work graph=%+v", work)
	}
	roster := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"roster", cookies, nil, ""), http.StatusOK))
	if len(roster.Seats) != 3 || roster.ProviderSessionStarted {
		t.Fatalf("restored Agentic Lab roster=%+v", roster)
	}
	project, _, err := restarted.scoutChatThreadByID(artifactLibraryAdminEmail, evidence.ProjectThreadID)
	if err != nil || countAgenticLabMessages(project, "agent") != evidence.AgentMessageCount || countAgenticLabMessages(project, "deleted") != 0 {
		t.Fatalf("restored Agentic Lab project messages=%d err=%v", len(project.Messages), err)
	}
	presentation, _, err := restarted.scoutChatThreadByID(artifactLibraryAdminEmail, evidence.PresentationThreadID)
	if err != nil || len(presentation.Messages) != 2 || presentation.Messages[1].Thread == nil || presentation.Messages[1].Thread.ProgressNote != "Shaping the deck brief" {
		t.Fatalf("restored Agentic Lab presentation=%+v err=%v", presentation, err)
	}
	expectedRecurringNotes := map[string]string{
		"Presentation":       "Shaping the deck brief",
		"Research":           "Gathering reliable sources",
		"Design":             "Building the first draft",
		"Financial model":    "Checking the work",
		"Document":           "Drafting the document",
		"Meeting recap":      "Turning the meeting into decisions",
		"Revision":           "Preparing the revision",
		"Scheduled work":     "Setting the schedule",
		"Build":              "Preparing the handoff",
		"Mixed package":      "Assembling the package",
		"Data visualization": "Building the visualization",
		"Project plan":       "Mapping the plan",
	}
	for family, threadID := range evidence.RecurringWorkThreadIDs {
		recurring, _, recurringErr := restarted.scoutChatThreadByID(artifactLibraryAdminEmail, threadID)
		if recurringErr != nil || len(recurring.Messages) != 2 || recurring.Messages[1].Thread == nil || recurring.Messages[1].Thread.ProgressNote != expectedRecurringNotes[family] {
			t.Fatalf("restored Agentic Lab recurring %s=%+v err=%v", family, recurring, recurringErr)
		}
	}
	transcripts := restarted.memory.snapshot(0)
	if countAgenticLabTranscripts(transcripts) != evidence.AgentTranscriptCount {
		t.Fatalf("restored Agentic Lab transcripts=%d want=%d", countAgenticLabTranscripts(transcripts), evidence.AgentTranscriptCount)
	}

	// Default-off restart hides product affordances while preserving the signed
	// ledger. This is the activation fence, not a completion claim.
	secondGeneration := restarted.strideRuntime.Health().Generation
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	app = nil
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "false")
	fenced := newKanbanBoardApp()
	app = fenced
	kanbanApp = fenced
	if health := fenced.strideRuntime.Health(); !health.Restored || health.Generation <= secondGeneration {
		t.Fatalf("default-off Agentic Lab health=%+v prior=%d", health, secondGeneration)
	}
	fencedWork := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", cookies, nil, ""), http.StatusOK))
	if fencedWork.Available || len(fencedWork.Suggestions) != 0 || len(fencedWork.Runs) != 0 {
		t.Fatalf("default-off Agentic Lab leaked work=%+v", fencedWork)
	}
	if err := fenced.strideRuntime.WithTenantDomains(canonicalTenantID(), func(domains STRIDERuntimeDomains) error {
		snapshot, err := domains.Product.Snapshot()
		if err != nil {
			return err
		}
		if len(snapshot.Work) != 1 || len(snapshot.Agents) != 3 || len(domains.WorkOrchestrator.Store.Runs) != 1 {
			t.Fatalf("default-off Agentic Lab lost ledger: work=%d agents=%d runs=%d", len(snapshot.Work), len(snapshot.Agents), len(domains.WorkOrchestrator.Store.Runs))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestStrideE10AgenticLabRenderedHarness serves the actual STRIDE shell and
// normal authenticated handlers against the isolated Agentic Lab. It is
// opt-in, loopback-only, bounded, and compiled only into the test binary.
//
// Example:
//
//	STRIDE_E10_AGENTIC_LAB_LISTEN=127.0.0.1:19092 \
//	STRIDE_E10_AGENTIC_LAB_DURATION=10m \
//	go test -v . -run '^TestStrideE10AgenticLabRenderedHarness$'
func TestStrideE10AgenticLabRenderedHarness(t *testing.T) {
	listenAddress := strings.TrimSpace(os.Getenv("STRIDE_E10_AGENTIC_LAB_LISTEN"))
	if listenAddress == "" {
		t.Skip("STRIDE_E10_AGENTIC_LAB_LISTEN is unset")
	}
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("Agentic Lab harness must bind exact loopback IPv4: %q", listenAddress)
	}
	duration := 10 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("STRIDE_E10_AGENTIC_LAB_DURATION")); raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 || parsed > 30*time.Minute {
			t.Fatalf("invalid bounded Agentic Lab duration %q", raw)
		}
		duration = parsed
	}
	configureStrideE10AgenticLabEnv(t)
	previousApp := kanbanApp
	app := newKanbanBoardApp()
	kanbanApp = app
	t.Cleanup(func() {
		_ = app.Close()
		kanbanApp = previousApp
	})
	cookies := loginAs(t, artifactLibraryAdminEmail, defaultMeetingRoomPassword)
	mux := http.NewServeMux()
	registerSTRIDERuntimeRoutes(mux)
	evidence := seedStrideE10AgenticLab(t, app, mux, cookies)
	assertStrideE10AgenticLabEvidence(t, evidence)

	mux.HandleFunc("/auth/", authHandler)
	mux.HandleFunc("/assistant/chat-threads", assistantChatThreadsHandler)
	mux.HandleFunc("/assistant/chat-threads/", assistantChatThreadHandler)
	mux.HandleFunc("/assistant/chat-participants", assistantChatParticipantsHandler)
	mux.HandleFunc("/assistant/notifications", assistantNotificationsHandler)
	mux.HandleFunc("/assistant/notifications/read", assistantNotificationsReadHandler)
	mux.HandleFunc("/assistant/notifications/clear", assistantNotificationsClearHandler)
	mux.Handle("/public/", http.StripPrefix("/public/", http.FileServer(http.Dir("public"))))
	mux.HandleFunc("/sw.js", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		http.ServeFile(writer, request, "public/sw.js")
	})
	mux.HandleFunc("/__agentic-lab/evidence", func(writer http.ResponseWriter, request *http.Request) {
		if userFromRequest(request) == nil {
			writeAuthError(writer, http.StatusUnauthorized, "not signed in")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(writer).Encode(evidence)
	})
	mux.HandleFunc("/__agentic-lab/login", func(writer http.ResponseWriter, request *http.Request) {
		for _, cookie := range cookies {
			copy := *cookie
			copy.Path = "/"
			http.SetCookie(writer, &copy)
		}
		http.Redirect(writer, request, "/work", http.StatusFound)
	})
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := os.ReadFile("index.html")
		if readErr != nil {
			http.Error(writer, "index unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = writer.Write(body)
	})

	listener, err := net.Listen("tcp4", listenAddress)
	if err != nil {
		t.Fatal(err)
	}
	// The rendered laboratory exposes its already-seeded, private organization
	// through the normal minimized mobile projection shape. This is test-only
	// presentation data; it does not mint an action or mutate a membership.
	var handler http.Handler = mux
	handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/stride/v1/mobile/surfaces/organizations" {
			if userFromRequest(request) == nil {
				writeAuthError(writer, http.StatusUnauthorized, "not signed in")
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"availability": "available", "surface": "organizations", "revision": int64(1),
				"items": []map[string]any{{
					"id": strideE10AgenticLabMembershipID, "title": "STRIDE Agentic Lab", "status": "current", "kind": "organization-summary", "actions": []any{},
					"detail": map[string]any{"kind": "organization-summary", "activeCount": 1, "capacity": 3, "pendingCount": 0, "isCurrent": true, "role": "owner"},
				}},
			})
			return
		}
		mux.ServeHTTP(writer, request)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("Agentic Lab rendered harness: http://%s/__agentic-lab/login", listenAddress)
	select {
	case serveErr := <-done:
		t.Fatalf("Agentic Lab harness stopped early: %v", serveErr)
	case <-time.After(duration):
	}
	if err := server.Close(); err != nil {
		t.Fatal(fmt.Errorf("close Agentic Lab harness: %w", err))
	}
}

func configureStrideE10AgenticLabEnv(t *testing.T) string {
	t.Helper()
	for _, key := range []string{
		"OPENAI_API_KEY", "OPENAI_REALTIME_API_KEY", "OPENAI_TRANSCRIPTION_API_KEY",
		"ANTHROPIC_API_KEY", "FISCAL_API_KEY", "FISCAL_AI_API_KEY",
	} {
		t.Setenv(key, "")
	}
	setupAuthTestEnv(t)
	dir := t.TempDir()
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "meeting-memory.jsonl"))
	t.Setenv("KANBAN_BOARD_PATH", filepath.Join(dir, "kanban-board.json"))
	t.Setenv("MEETINGS_PATH", filepath.Join(dir, "meetings.json"))
	t.Setenv("ADMISSION_ANCHORS_PATH", filepath.Join(dir, "admission-anchors.json"))
	t.Setenv("NOTIFICATIONS_PATH", filepath.Join(dir, "notifications.json"))
	t.Setenv("BONFIRE_ROOMS_PATH", filepath.Join(dir, "rooms.json"))
	t.Setenv("BONFIRE_CANONICAL_TENANT_ID", "bonfire")
	t.Setenv("STRIDE_RUNTIME_ENABLED", "true")
	t.Setenv("STRIDE_RUNTIME_BOOTSTRAP_EMPTY", "true")
	t.Setenv("STRIDE_RUNTIME_MIN_GENERATION", "211")
	t.Setenv("STRIDE_RUNTIME_RECALL_THREAD_IDS", "stride-agentic-lab")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_KEY_ID", "stride_agentic_lab_local_key")
	t.Setenv("STRIDE_RUNTIME_SNAPSHOT_MAC_KEY", base64.StdEncoding.EncodeToString([]byte("agentic-lab-local-mac-key-32byte")))
	t.Setenv("STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED", "true")
	t.Setenv("STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED", "false")
	t.Setenv("STRIDE_OPENAI_TOOL_RUNTIME_ENABLED", "false")
	return dir
}

type strideE10AgenticLabRecurringWorkFixture struct {
	Suffix   string
	Title    string
	Request  string
	Mode     string
	Query    string
	Stage    string
	Progress float64
	Note     string
	Offset   time.Duration
}

func seedStrideE10AgenticLabRecurringWorkThread(t *testing.T, app *kanbanBoardApp, now time.Time, fixture strideE10AgenticLabRecurringWorkFixture) string {
	t.Helper()
	thread, _, err := app.ensureScoutChatThread("stride-agentic-lab-"+fixture.Suffix, artifactLibraryAdminEmail, "AJ", fixture.Title, scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := scoutChatMessageRecord{
		ID: "stride-agentic-lab-" + fixture.Suffix + "-request", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: artifactLibraryAdminEmail,
		Text: fixture.Request, CreatedAt: now.Add(fixture.Offset).Format(time.RFC3339Nano),
	}
	work := scoutChatMessageRecord{
		ID: "stride-agentic-lab-" + fixture.Suffix + "-work", Kind: "thread", Role: "scout", AuthorName: scoutParticipantName,
		IntentOutcome: string(conversationIntentStartPrivateWork), CausedByMessageID: request.ID,
		Text: "Work in progress", CreatedAt: now.Add(fixture.Offset + time.Second).Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID: "stride-agentic-lab-" + fixture.Suffix + "-run", Mode: fixture.Mode, Query: fixture.Query,
			Status: "running", AgentName: scoutParticipantName, CurrentStage: fixture.Stage, ProgressPercent: fixture.Progress,
			ProgressNote: fixture.Note, StartedAt: now.Add(fixture.Offset).Format(time.RFC3339Nano),
		},
	}
	if _, err := app.commitScoutChatThreadMessages(artifactLibraryAdminEmail, thread.ID, request, work); err != nil {
		t.Fatal(err)
	}
	restored, _, err := app.scoutChatThreadByID(artifactLibraryAdminEmail, thread.ID)
	if err != nil || len(restored.Messages) != 2 || restored.Messages[1].Thread == nil || restored.Messages[1].IntentOutcome != string(conversationIntentStartPrivateWork) || restored.Messages[1].Thread.Mode != fixture.Mode || restored.Messages[1].Thread.ProgressPercent != fixture.Progress || restored.Messages[1].Thread.ProgressNote != fixture.Note {
		t.Fatalf("Agentic Lab recurring %s fixture=%+v err=%v", fixture.Suffix, restored, err)
	}
	return thread.ID
}

func seedStrideE10AgenticLab(t *testing.T, app *kanbanBoardApp, mux http.Handler, cookies []*http.Cookie) strideE10AgenticLabEvidence {
	t.Helper()
	now := time.Date(2026, 8, 11, 17, 0, 0, 0, time.UTC)
	evidence := strideE10AgenticLabEvidence{
		OrganizationID:  strideE10AgenticLabOrganizationID,
		HumanOwner:      artifactLibraryAdminEmail,
		ProviderCalls:   0,
		ExternalEffects: 0,
	}

	// The canonical organization service carries exactly one human principal
	// and one owner membership. Agent seats live only in Workforce/Product and
	// can never be mistaken for person or membership records.
	w1 := NewStrideE10ProductLiveRuntime(func() time.Time { return now })
	w1.organization.persons[strideE10AgenticLabPersonID] = organizationTestPerson(strideE10AgenticLabPersonID, 'a', now.Add(-time.Hour))
	w1.organization.organizations[strideE10AgenticLabOrganizationID] = Organization{
		Header: organizationTestHeader(STRIDEGlobalPersonTenant, strideE10AgenticLabOrganizationID, 1, STRIDEContractOrganization, 'b', now.Add(-time.Hour)),
		Name:   "STRIDE Agentic Lab", Slug: "stride-agentic-lab", Status: "active", Discoverability: "private",
		CreatorPersonID: strideE10AgenticLabPersonID, PolicyRevision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	w1.organization.memberships[strideE10AgenticLabMembershipID] = organizationTestMembership(strideE10AgenticLabMembershipID, strideE10AgenticLabPersonID, strideE10AgenticLabOrganizationID, "owner", 1, now.Add(-time.Hour), "")
	evidence.HumanMemberships = len(w1.organization.memberships)
	draftProfile := NetworkProfileProjection{
		Header:          contributionNetworkHeader(STRIDEContractNetworkProfileProjection, "projection_stride_agentic_lab_draft", STRIDEGlobalPersonTenant),
		SubjectPersonID: strideE10AgenticLabPersonID,
		Fields:          []NetworkPublishedField{{FieldKey: "display_name", ValueDigest: sha256Hex([]byte(`"AJ"`)), VisibleValue: json.RawMessage(`"AJ"`), EvidenceLabel: "self_described"}},
		Discoverability: "unlisted", Controller: STRIDEControllerRevision{PrincipalID: strideE10AgenticLabPersonID, AuthorityID: strideE10AgenticLabPersonID, AuthorityRevision: 1, PolicyRevision: 1},
		State: "draft", StateChangedAt: now,
	}
	draftProfile.FieldsDigest, _ = STRIDEContractDigest(draftProfile.Fields)
	createdProfile, _, _, err := w1.network.PutProfile(draftProfile.Controller, draftProfile, 0, authorityDigest("stride-agentic-lab-draft-profile"))
	if err != nil {
		t.Fatalf("create private Work to Network draft: %v", err)
	}
	evidence.NetworkState, evidence.NetworkVisibility = createdProfile.State, createdProfile.Discoverability

	for _, listingID := range []string{"mary-marketing", "colton-research", "jules-design"} {
		trial := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+listingID+"/trial", cookies, nil, ""), http.StatusOK))
		hired := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"marketplace/"+listingID+"/hire", cookies, map[string]any{"revision": trial.Seat.Revision}, ""), http.StatusOK))
		if !hired.Seat.ProviderExecutionFenced || hired.ProviderSessionStarted || hired.Seat.OwnerID != strideRuntimePrincipalForEmail(artifactLibraryAdminEmail) {
			t.Fatalf("unsafe Agentic Lab seat=%+v", hired)
		}
		evidence.AgentSeats = append(evidence.AgentSeats, hired.Seat)
		evidence.PrivateThreadIDs = append(evidence.PrivateThreadIDs, hired.Seat.DirectThreadID)
	}

	if accountStore().findUser(strideE10AgenticLabAgentAddress) != nil {
		t.Fatal("Agentic Lab agent address must not resolve to a human account")
	}
	projectMembers := []string{artifactLibraryAdminEmail, strideE10AgenticLabAgentAddress}
	project, _, err := app.ensureScoutChatThread("stride-agentic-lab-project", artifactLibraryAdminEmail, "AJ", "Agentic Lab · Launch room", scoutChatVisibilityPublic, projectMembers)
	if err != nil {
		t.Fatal(err)
	}
	destination, _, err := app.ensureScoutChatThread("stride-agentic-lab-work", artifactLibraryAdminEmail, "AJ", "Agentic Lab · Work", scoutChatVisibilityPublic, projectMembers)
	if err != nil {
		t.Fatal(err)
	}
	evidence.ProjectThreadID = project.ID
	presentation, _, err := app.ensureScoutChatThread("stride-agentic-lab-presentation", artifactLibraryAdminEmail, "AJ", "Pitch the STRIDE platform", scoutChatVisibilityPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	evidence.PresentationThreadID = presentation.ID
	presentationRequest := scoutChatMessageRecord{
		ID: "stride-agentic-lab-presentation-request", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: artifactLibraryAdminEmail,
		Text: "Scout can you make a 10 slide deck pitching me this platform?", CreatedAt: now.Add(20 * time.Minute).Format(time.RFC3339Nano),
	}
	presentationWork := scoutChatMessageRecord{
		ID: "stride-agentic-lab-presentation-work", Kind: "thread", Role: "scout", AuthorName: scoutParticipantName,
		IntentOutcome: string(conversationIntentStartPrivateWork), CausedByMessageID: presentationRequest.ID,
		Text: "Presentation in progress", CreatedAt: now.Add(20*time.Minute + time.Second).Format(time.RFC3339Nano),
		Thread: &scoutChatThreadRef{
			ID: "stride-agentic-lab-presentation-run", Mode: "goal", Query: "Create a polished 10-slide pitch deck for the STRIDE platform",
			Status: "running", AgentName: scoutParticipantName, CurrentStage: "identify_goal", ProgressPercent: 4,
			ProgressNote: "Shaping the deck brief", StartedAt: now.Add(20 * time.Minute).Format(time.RFC3339Nano),
		},
	}
	if _, err := app.commitScoutChatThreadMessages(artifactLibraryAdminEmail, presentation.ID, presentationRequest, presentationWork); err != nil {
		t.Fatal(err)
	}
	presentation, _, err = app.scoutChatThreadByID(artifactLibraryAdminEmail, presentation.ID)
	if err != nil || len(presentation.Messages) != 2 || presentation.Messages[1].Thread == nil || presentation.Messages[1].IntentOutcome != string(conversationIntentStartPrivateWork) || presentation.Messages[1].Thread.ProgressPercent != 4 || presentation.Messages[1].Thread.ProgressNote != "Shaping the deck brief" {
		t.Fatalf("Agentic Lab presentation fixture=%+v err=%v", presentation, err)
	}
	evidence.RecurringWorkThreadIDs = map[string]string{
		"Presentation": presentation.ID,
		"Research": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "research", Title: "Research the agentic work market", Request: "Scout, research the market for AI-native team workspaces and ground it in reliable sources.",
			Mode: "research", Query: "Research the market for AI-native team workspaces with cited sources", Stage: "source_research", Progress: 28, Note: "Gathering reliable sources", Offset: 19 * time.Minute,
		}),
		"Design": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "design", Title: "Design the STRIDE launch image", Request: "Scout, create a launch image and visual direction for STRIDE.",
			Mode: "design", Query: "Create a launch image and visual design system for STRIDE", Stage: "build_draft", Progress: 46, Note: "Building the first draft", Offset: 18 * time.Minute,
		}),
		"Financial model": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "financial-model", Title: "Model STRIDE's operating plan", Request: "Scout, build a three-year financial model and operating forecast for STRIDE.",
			Mode: "financial model", Query: "Build a three-year financial model and operating forecast for STRIDE", Stage: "review_gate", Progress: 72, Note: "Checking the work", Offset: 17 * time.Minute,
		}),
		"Document": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "document", Title: "Draft the STRIDE launch memo", Request: "Scout, draft a concise launch memo for the STRIDE team.",
			Mode: "document", Query: "Draft a concise launch memo for the STRIDE team", Stage: "build_draft", Progress: 38, Note: "Drafting the document", Offset: 16 * time.Minute,
		}),
		"Meeting recap": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "meeting-recap", Title: "Recap the product review", Request: "Scout, turn the product review meeting into a recap, decision log, and action record.",
			Mode: "meeting recap", Query: "Create a meeting recap, decision log, and action record", Stage: "synthesize_recap", Progress: 52, Note: "Turning the meeting into decisions", Offset: 15 * time.Minute,
		}),
		"Revision": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "revision", Title: "Revise the investor deck", Request: "Scout, revise this investor deck with a sharper opening and retain the prior version.",
			Mode: "revision", Query: "Revise this investor deck with a sharper opening and retain the prior version", Stage: "review_gate", Progress: 76, Note: "Preparing the revision", Offset: 14 * time.Minute,
		}),
		"Scheduled work": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "scheduled-work", Title: "Schedule the weekly market brief", Request: "Scout, schedule a weekly market brief for this private team.",
			Mode: "scheduled", Query: "Schedule a weekly market brief for this private team", Stage: "plan_schedule", Progress: 22, Note: "Setting the schedule", Offset: 13 * time.Minute,
		}),
		"Build": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "build", Title: "Prepare the implementation handoff", Request: "Scout, prepare a scoped repository implementation handoff for Codex.",
			Mode: "execution handoff", Query: "Prepare a scoped repository implementation handoff for Codex", Stage: "execute_handoff", Progress: 44, Note: "Preparing the handoff", Offset: 12 * time.Minute,
		}),
		"Mixed package": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "mixed-package", Title: "Assemble the investor package", Request: "Scout, assemble an investor package with cited research, a memo, deck, financial model, and imagery.",
			Mode: "mixed package", Query: "Assemble an investor package with cited research, a memo, deck, financial model, and imagery", Stage: "assemble_package", Progress: 61, Note: "Assembling the package", Offset: 11 * time.Minute,
		}),
		"Data visualization": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "data-visualization", Title: "Chart the market landscape", Request: "Scout, build a source-bound market share chart and reusable data table.",
			Mode: "data visualization", Query: "Build a source-bound market share chart and reusable data table", Stage: "build_chart", Progress: 48, Note: "Building the visualization", Offset: 10 * time.Minute,
		}),
		"Project plan": seedStrideE10AgenticLabRecurringWorkThread(t, app, now, strideE10AgenticLabRecurringWorkFixture{
			Suffix: "project-plan", Title: "Plan the STRIDE launch", Request: "Scout, create a governed launch project plan and task board.",
			Mode: "project plan", Query: "Create a governed launch project plan and task board", Stage: "plan_work", Progress: 32, Note: "Mapping the plan", Offset: 9 * time.Minute,
		}),
	}
	for index, agent := range evidence.AgentSeats {
		message := scoutChatMessageRecord{
			ID: "stride-agentic-lab-agent-" + agent.ListingID, Kind: "message", Role: "scout", AuthorName: agent.DisplayName,
			Via: "stride_agent", Text: []string{
				"I mapped the audience tension and the proof the pitch must earn.",
				"I checked the source trail and marked the claims that still need evidence.",
				"I turned the work into a visual hierarchy and interaction critique.",
			}[index], CreatedAt: now.Add(time.Duration(index) * time.Minute).Format(time.RFC3339Nano),
		}
		if _, err := app.commitScoutChatThreadMessages(artifactLibraryAdminEmail, project.ID, message); err != nil {
			t.Fatal(err)
		}
		evidence.AgentMessageCount++
	}
	attachmentMessage := scoutChatMessageRecord{
		ID: "stride-agentic-lab-attachment", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: artifactLibraryAdminEmail,
		Text: "Use this synthetic brief as private project context.", CreatedAt: now.Add(4 * time.Minute).Format(time.RFC3339Nano),
		Files: []scoutChatFileAttachment{{Name: "agentic-lab-brief.txt", Kind: "text/plain", Size: 64, Text: "Synthetic private brief. No customer or production data."}},
	}
	if _, err := app.commitScoutChatThreadMessages(artifactLibraryAdminEmail, project.ID, attachmentMessage); err != nil {
		t.Fatal(err)
	}
	evidence.AttachmentCount = 1

	sourceMessage := scoutChatMessageRecord{
		ID: "stride-agentic-lab-work-request", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: artifactLibraryAdminEmail,
		Text:      "We need Scout to create an Insights & Opportunities report for the Agentic Lab, grounded in this company conversation.",
		CreatedAt: now.Add(5 * time.Minute).Format(time.RFC3339Nano),
	}
	if _, err := app.commitScoutChatThreadMessages(artifactLibraryAdminEmail, project.ID, sourceMessage); err != nil {
		t.Fatal(err)
	}
	createdSuggestion := e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions", cookies, map[string]any{
		"threadId": project.ID, "messageId": sourceMessage.ID, "title": "Insights & Opportunities report", "outcome": sourceMessage.Text,
	}, "")
	if createdSuggestion.Code != http.StatusCreated {
		t.Fatalf("create Agentic Lab suggestion status=%d body=%s", createdSuggestion.Code, createdSuggestion.Body.String())
	}
	initial := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", cookies, nil, ""), http.StatusOK))
	if len(initial.Suggestions) != 1 {
		t.Fatalf("Agentic Lab suggestion count=%d", len(initial.Suggestions))
	}
	suggestion := initial.Suggestions[0]
	if !sameStringSet(suggestion.RecipientIDs, []string{strideRuntimePrincipalForEmail(artifactLibraryAdminEmail), strideRuntimePrincipalForEmail(strideE10AgenticLabAgentAddress)}) {
		t.Fatalf("Agentic Lab work audience=%v", suggestion.RecipientIDs)
	}
	destinationResult := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/destination", cookies, map[string]any{"revision": suggestion.Revision, "mode": "existing", "threadId": destination.ID}, ""), http.StatusOK))
	approved := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"work/suggestions/"+suggestion.ID+"/approve", cookies, map[string]any{"revision": destinationResult.Suggestion.Revision}, ""), http.StatusOK))
	if approved.ProviderCalls != 0 || approved.Suggestion.Status != "completed" || approved.Suggestion.RunID == "" || approved.Suggestion.ArtifactID == "" {
		t.Fatalf("Agentic Lab approved work=%+v", approved)
	}
	evidence.Work = approved.Suggestion
	evidence.ArtifactID = approved.Suggestion.ArtifactID
	workSurface := e9DecodeFounder[e9FounderWorkResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodGet, strideRuntimeAPIBase+"work", cookies, nil, ""), http.StatusOK))
	if len(workSurface.Runs) != 1 {
		t.Fatalf("Agentic Lab run count=%d", len(workSurface.Runs))
	}
	evidence.Run = workSurface.Runs[0]
	workThread, _, err := app.scoutChatThreadByID(artifactLibraryAdminEmail, destination.ID)
	if err != nil {
		t.Fatal(err)
	}
	workResultCount := 0
	for _, message := range workThread.Messages {
		if message.Kind != "work_result" {
			continue
		}
		workResultCount++
		if message.Work == nil || message.Work.ID != evidence.Work.ID || message.Work.RunID != evidence.Run.ID || message.Work.Status != "completed" || message.Work.WorkerName != "Scout" || message.Work.ProgressPercent != 100 || !message.Work.ProviderExecutionFenced {
			t.Fatalf("Agentic Lab governed completed work=%+v", message.Work)
		}
		if message.Work.ArtifactHref != evidence.Work.ArtifactHref || message.Work.EvidenceHref != evidence.Work.BrainHref || message.Work.ArtifactID != evidence.Work.ArtifactID {
			t.Fatalf("Agentic Lab completed-work links=%+v suggestion=%+v", message.Work, evidence.Work)
		}
	}
	if workResultCount != 1 {
		t.Fatalf("Agentic Lab governed completed-work count=%d", workResultCount)
	}
	evidence.RevisedArtifactID = "stride-agentic-lab-revision-artifact"
	artifact, appended, err := app.memory.appendOSArtifact(evidence.RevisedArtifactID, "# Agentic Lab decision brief\n\nPrivate synthetic draft grounded in the completed work result.", map[string]string{
		"title": "Agentic Lab decision brief", "status": "draft", "visibility": "private", "ownerEmail": artifactLibraryAdminEmail,
		"requestedBy": artifactLibraryAdminEmail, "sourceRunId": evidence.Run.ID, "sourceArtifactId": evidence.ArtifactID,
	})
	if err != nil || !appended {
		t.Fatalf("create Agentic Lab revision artifact appended=%t err=%v", appended, err)
	}
	revised, changed, err := app.memory.updateOSArtifact(evidence.RevisedArtifactID, artifact.Metadata["title"], artifact.Text+"\n\n## Human revision\nKeep every claim visibly source-bound.", artifactLibraryAdminEmail)
	if err != nil || !changed {
		t.Fatalf("Agentic Lab artifact revision changed=%t err=%v", changed, err)
	}
	evidence.ArtifactVersion = artifactVersion(revised)

	// A completed AI deliverable is not itself a professional Work Record.
	// Exercise that separate governed path here: AJ is the subject, organization
	// reviewer, publisher, and signing issuer; agents contribute evidence but
	// never become a person/controller. The projection remains private.
	contribution := installStrideE10AgenticLabContributions(t, w1, evidence.Run.ID, now.Add(6*time.Minute))
	evidence.ActiveAttestationID = contribution.ActiveAttestation.Header.ID
	evidence.ActiveAttestationRev = contribution.ActiveAttestation.Header.Revision
	evidence.ReleasedFieldsDigest = contribution.ActiveAttestation.ReleasedFieldsDigest
	evidence.VerificationTier = contribution.ActiveAttestation.VerificationTier
	evidence.PrivatePublicationID = contribution.ActivePublication.Header.ID
	evidence.PrivateVisibility = contribution.ActivePublication.Visibility
	evidence.RevokedAttestationID = contribution.RevokedAttestation.Header.ID
	evidence.RevokedAttestationRev = contribution.RevokedAttestation.Header.Revision
	evidence.RevocationEffects = len(contribution.RevocationEffects)

	contributionPath := filepath.Join(t.TempDir(), "agentic-lab-contribution-runtime.json")
	keys := strideE10W4TestKeyring()
	// The authenticated W4 envelope deliberately validates the canonical
	// seven-person directory shape. Add six unrelated directory principals
	// with no organization membership; the Agentic Lab organization itself
	// still has exactly one human member and owner.
	for id, marker := range map[string]rune{
		"person_stride_agentic_lab_directory_b": 'b',
		"person_stride_agentic_lab_directory_c": 'c',
		"person_stride_agentic_lab_directory_d": 'd',
		"person_stride_agentic_lab_directory_e": 'e',
		"person_stride_agentic_lab_directory_f": 'f',
		"person_stride_agentic_lab_directory_1": '1',
	} {
		person := organizationTestPerson(id, marker, now.Add(-time.Hour))
		w1.organization.persons[id] = person
		w1.organization.accountPersons[person.AccountSubjectDigest] = id
	}
	w1.organization.accountPersons[w1.organization.persons[strideE10AgenticLabPersonID].AccountSubjectDigest] = strideE10AgenticLabPersonID
	if err := writeStrideE10W4RuntimeSnapshot(contributionPath, 1, keys, w1); err != nil {
		t.Fatalf("persist Agentic Lab contribution runtime: %v", err)
	}
	snapshot, generation, err := loadStrideE10W4Snapshot(contributionPath, keys)
	if err != nil || generation != 1 {
		t.Fatalf("reload Agentic Lab contribution runtime generation=%d err=%v", generation, err)
	}
	restoredContribution := runtimeFromStrideE10W4Snapshot(snapshot, newStrideE10MemoryOperationStore())
	viewer := StrideE10AuthorityViewer{PersonID: strideE10AgenticLabPersonID, OrganizationID: strideE10AgenticLabOrganizationID, MembershipID: strideE10AgenticLabMembershipID, MembershipRevision: 1}
	scope := StrideE10ContributionViewScope{GrantID: contribution.OrganizationGrant.ID, Controller: contribution.OrganizationGrant.Controller}
	view, err := restoredContribution.contribution.ReadStrideE10OrganizationContributionView(restoredContribution.organization, viewer, scope)
	if err != nil {
		t.Fatalf("read restored Agentic Lab contribution view: %v", err)
	}
	identity, err := restoredContribution.organization.ReadStrideE10SelfOrganizationView(strideE10AgenticLabPersonID)
	if err != nil {
		t.Fatalf("read restored Agentic Lab identity: %v", err)
	}
	items := strideE10WorkRecordProjectionItems(view, identity, restoredContribution.contribution.FieldEligible)
	for _, item := range items {
		if item["kind"] == "work-record-section" {
			evidence.WorkRecordSections++
		}
		if item["kind"] == "contribution-evidence" {
			evidence.WorkRecordEvidence++
		}
	}
	evidence.ContributionRestored = true

	meetingID := app.memory.ensureMeetingID("stride-agentic-lab-room")
	if _, changed := app.meetings.startMeeting("stride-agentic-lab-room", meetingID, now.Add(6*time.Minute), []string{"Mary · agent", "Colton · agent", "Jules · agent"}); !changed {
		t.Fatal("Agentic Lab meeting did not start")
	}
	evidence.MeetingID = meetingID
	for index, agent := range evidence.AgentSeats {
		_, appended, err := app.memory.appendAttributedTranscriptEntry("stride-agentic-lab-room", "stride-agentic-lab-transcript-"+agent.ListingID, "", agent.DisplayName, "machine_attributed", "Agent-authored synthetic meeting contribution from "+agent.DisplayName+".", map[string]string{"speakerType": "agent", "agentId": agent.ID, "source": "synthetic_agentic_lab"}, true, meetingID)
		if err != nil || !appended {
			t.Fatalf("Agentic Lab transcript %d appended=%t err=%v", index, appended, err)
		}
		evidence.AgentTranscriptCount++
	}

	// Human correction and revocation are normal roster mutations. An agent can
	// propose/remember, but only AJ resolves the durable learning and access.
	mary := evidence.AgentSeats[0]
	learning := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+mary.ID+"/learning", cookies, map[string]any{"revision": mary.Revision, "subject": "audience", "scope": "project", "summary": "The audience wants maximum automation."}, ""), http.StatusOK))
	learningID := learning.Seat.Learning[len(learning.Seat.Learning)-1].ID
	corrected := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+mary.ID+"/learning/"+learningID+"/correct", cookies, map[string]any{"revision": learning.Seat.Revision, "summary": "The audience wants high agency with explicit human approval for consequential effects."}, ""), http.StatusOK))
	evidence.CorrectedLearning = corrected.Seat.Learning[len(corrected.Seat.Learning)-1].Status == "corrected"
	jules := evidence.AgentSeats[2]
	paused := e9DecodeFounder[e9FounderMarketplaceResponse](t, e9FounderExpect(t, e9FounderRequest(t, mux, http.MethodPost, strideRuntimeAPIBase+"roster/"+jules.ID+"/pause", cookies, map[string]any{"revision": jules.Revision}, ""), http.StatusOK))
	if paused.Seat.Status != "paused" || !paused.Seat.AccessRevoked {
		t.Fatalf("Agentic Lab pause=%+v", paused.Seat)
	}
	evidence.RevokedAgentID = paused.Seat.ID

	deletable := scoutChatMessageRecord{ID: "stride-agentic-lab-delete", Kind: "message", Role: "user", AuthorName: "AJ", AuthorEmail: artifactLibraryAdminEmail, Text: "Synthetic correction target.", CreatedAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano)}
	if _, err := app.commitScoutChatThreadMessages(artifactLibraryAdminEmail, project.ID, deletable); err != nil {
		t.Fatal(err)
	}
	if _, err := app.deleteScoutChatThreadMessage(artifactLibraryAdminEmail, project.ID, deletable.ID); err != nil {
		t.Fatal(err)
	}
	evidence.DeletedMessageID = deletable.ID

	return evidence
}

func assertStrideE10AgenticLabEvidence(t *testing.T, evidence strideE10AgenticLabEvidence) {
	t.Helper()
	if evidence.OrganizationID != strideE10AgenticLabOrganizationID || evidence.HumanMemberships != 1 || evidence.HumanOwner != artifactLibraryAdminEmail || evidence.PresentationThreadID == "" || len(evidence.RecurringWorkThreadIDs) != 12 {
		t.Fatalf("Agentic Lab human authority=%+v", evidence)
	}
	if len(evidence.AgentSeats) != 3 || len(evidence.PrivateThreadIDs) != 3 || evidence.AgentMessageCount != 3 || evidence.AttachmentCount != 1 || evidence.AgentTranscriptCount != 3 {
		t.Fatalf("Agentic Lab agent graph=%+v", evidence)
	}
	for _, seat := range evidence.AgentSeats {
		if seat.ID == "" || seat.DisplayName == "" || seat.OwnerID == "" || !seat.ProviderExecutionFenced || seat.ProviderExecutionFenced == false {
			t.Fatalf("Agentic Lab agent identity=%+v", seat)
		}
		if seat.ID == strideE10AgenticLabPersonID || seat.ID == strideE10AgenticLabMembershipID {
			t.Fatalf("agent masqueraded as human authority: %+v", seat)
		}
	}
	if evidence.Work.Status != "completed" || evidence.Run.Status != STRIDERunCompleted || evidence.ArtifactID == "" || evidence.RevisedArtifactID == "" || evidence.ArtifactVersion < 2 {
		t.Fatalf("Agentic Lab work/artifact=%+v", evidence)
	}
	if !evidence.CorrectedLearning || evidence.RevokedAgentID == "" || evidence.DeletedMessageID == "" {
		t.Fatalf("Agentic Lab governance=%+v", evidence)
	}
	if evidence.ActiveAttestationID == "" || evidence.ActiveAttestationRev != 1 || !isHexDigest(evidence.ReleasedFieldsDigest) || evidence.VerificationTier != "organization_verified_redacted" || evidence.PrivatePublicationID == "" || evidence.PrivateVisibility != "private" {
		t.Fatalf("Agentic Lab active contribution authority=%+v", evidence)
	}
	if evidence.RevokedAttestationID == "" || evidence.RevokedAttestationRev != 2 || evidence.RevocationEffects == 0 || evidence.WorkRecordSections != 6 || evidence.WorkRecordEvidence != 1 || !evidence.ContributionRestored {
		t.Fatalf("Agentic Lab Work Record lifecycle=%+v", evidence)
	}
	if evidence.NetworkState != "draft" || evidence.NetworkVisibility != "unlisted" || evidence.ProviderCalls != 0 || evidence.ExternalEffects != 0 {
		t.Fatalf("Agentic Lab default-off boundary=%+v", evidence)
	}
}

type strideE10AgenticLabContributionFixture struct {
	OrganizationGrant  ContributionAuthorityGrant
	ActiveAttestation  ContributionAttestation
	ActivePublication  PublishedContributionClaim
	RevokedAttestation ContributionAttestation
	RevocationEffects  []ContributionFenceEffect
}

func installStrideE10AgenticLabContributions(t *testing.T, runtime *StrideE10ProductLiveRuntime, sourceRunID string, at time.Time) strideE10AgenticLabContributionFixture {
	t.Helper()
	organization := authorityGrant("grant_stride_agentic_lab_organization", "organization_reviewer", strideE10AgenticLabOrganizationID, "", "", strideE10AgenticLabPersonID)
	subject := authorityGrant("grant_stride_agentic_lab_subject", "subject", "", strideE10AgenticLabPersonID, "", strideE10AgenticLabPersonID)
	issuer := authorityGrant("grant_stride_agentic_lab_issuer", "signing_issuer", strideE10AgenticLabOrganizationID, "", "", strideE10AgenticLabPersonID)
	publisher := authorityGrant("grant_stride_agentic_lab_publisher", "person_publisher", "", strideE10AgenticLabPersonID, "", strideE10AgenticLabPersonID)
	service, err := NewContributionAuthorityService([]ContributionAuthorityGrant{organization, subject, issuer, publisher})
	if err != nil {
		t.Fatalf("create Agentic Lab contribution authority: %v", err)
	}
	runtime.contribution = service

	issue := func(suffix, outcome string, offset time.Duration) (ContributionAttestation, PublishedContributionClaim) {
		claimHeader := contributionNetworkHeader(STRIDEContractContributionClaim, "claim_stride_agentic_lab_"+suffix, strideE10AgenticLabOrganizationID)
		claim := ContributionClaim{
			Header: claimHeader, OrganizationID: strideE10AgenticLabOrganizationID, SubjectPersonID: strideE10AgenticLabPersonID,
			ContributionKind: "delivered", ProblemClass: "agentic_work", OutcomeClass: "decision_clarity",
			SourceRefs:             []STRIDEReference{contributionNetworkRef(STRIDEContractOutcome, "outcome_stride_agentic_lab_"+suffix)},
			EvidenceManifestDigest: authorityDigest("agentic-lab-manifest-" + suffix + "-" + sourceRunID), AttributionMethod: "source_observed",
			ACLRevision: 1, ConsentRevision: 1, PurgeGeneration: 1, PolicyRevision: 1, State: "candidate", StateChangedAt: at.Add(offset),
		}
		created, err := service.CreateClaim(claim, authorityAssertion(organization, 0, "agentic-lab-create-"+suffix, at.Add(offset)))
		if err != nil {
			t.Fatalf("create Agentic Lab contribution %s: %v", suffix, err)
		}
		reviewed, err := service.SubjectReview(created.Header.ID, false, authorityAssertion(subject, created.Header.Revision, "agentic-lab-subject-"+suffix, at.Add(offset+time.Minute)))
		if err != nil {
			t.Fatalf("subject-review Agentic Lab contribution %s: %v", suffix, err)
		}
		verified, err := service.VerifyClaim(reviewed.Header.ID, authorityAssertion(organization, reviewed.Header.Revision, "agentic-lab-verify-"+suffix, at.Add(offset+2*time.Minute)))
		if err != nil {
			t.Fatalf("verify Agentic Lab contribution %s: %v", suffix, err)
		}

		attestationHeader := contributionNetworkHeader(STRIDEContractContributionAttestation, "attestation_stride_agentic_lab_"+suffix, strideE10AgenticLabOrganizationID)
		attestationRef := refForHeader(attestationHeader)
		valueDigest := sha256Hex([]byte(outcome))
		approvals := []struct {
			ID         string
			Role       string
			Controller ContributionAuthorityGrant
		}{
			{"approval_stride_agentic_lab_subject_" + suffix, "subject", subject},
			{"approval_stride_agentic_lab_organization_" + suffix, "organization", organization},
		}
		approvalRefs := make([]STRIDEReference, 0, len(approvals))
		for index, definition := range approvals {
			pending := FieldReleaseApproval{
				Header:         contributionNetworkHeader(STRIDEContractFieldReleaseApproval, definition.ID, strideE10AgenticLabOrganizationID),
				OrganizationID: strideE10AgenticLabOrganizationID, SubjectPersonID: strideE10AgenticLabPersonID, Attestation: attestationRef,
				FieldKey: "outcome", FieldValueDigest: valueDigest, Source: verified.SourceRefs[0], SourceConsentRevision: verified.ConsentRevision,
				SourceACLRevision: verified.ACLRevision, SourcePurgeGeneration: verified.PurgeGeneration, Visibility: "private", ApproverRole: definition.Role,
				Controller: definition.Controller.Controller, State: "pending", StateChangedAt: at.Add(offset + time.Duration(3+index)*time.Minute),
			}
			stored, err := service.PutFieldApproval(pending, authorityAssertion(organization, 0, "agentic-lab-put-"+definition.ID, pending.StateChangedAt))
			if err != nil {
				t.Fatalf("put Agentic Lab approval %s: %v", definition.ID, err)
			}
			approvedAt := pending.StateChangedAt.Add(time.Minute)
			approved, effects, err := service.DecideFieldApproval(stored.Header.ID, "approved", authorityAssertion(definition.Controller, stored.Header.Revision, "agentic-lab-approve-"+definition.ID, approvedAt))
			if err != nil || len(effects) != 0 {
				t.Fatalf("approve Agentic Lab field %s effects=%d err=%v", definition.ID, len(effects), err)
			}
			approvalRefs = append(approvalRefs, refForHeader(approved.Header))
		}
		field := ReleasedContributionField{FieldKey: "outcome", ValueDigest: valueDigest, ApprovalRefs: approvalRefs}
		attestation := ContributionAttestation{
			Header: attestationHeader, OrganizationID: strideE10AgenticLabOrganizationID, SubjectPersonID: strideE10AgenticLabPersonID,
			Claim: refForHeader(verified.Header), EvidenceManifestDigest: verified.EvidenceManifestDigest, ReleasedFields: []ReleasedContributionField{field},
			VerificationTier: "organization_verified_redacted", Issuer: issuer.Controller, SigningKeyID: "key_stride_agentic_lab", SigningKeyRevision: 1,
			SignatureDigest: authorityDigest("agentic-lab-signature-" + suffix), State: "active",
		}
		attestation.ReleasedFieldsDigest, _ = STRIDEContractDigest(attestation.ReleasedFields)
		issued, err := service.IssueAttestation(attestation, authorityAssertion(issuer, 0, "agentic-lab-issue-"+suffix, at.Add(offset+6*time.Minute)))
		if err != nil {
			t.Fatalf("issue Agentic Lab attestation %s: %v", suffix, err)
		}
		publication := PublishedContributionClaim{
			Header:          contributionNetworkHeader(STRIDEContractPublishedContributionClaim, "publication_stride_agentic_lab_"+suffix, STRIDEGlobalPersonTenant),
			SubjectPersonID: strideE10AgenticLabPersonID, NarrativeDigest: authorityDigest("agentic-lab-private-narrative-" + suffix),
			Attestations: []STRIDEReference{refForHeader(issued.Header)}, ReleasedFieldsDigest: issued.ReleasedFieldsDigest, Visibility: "private",
			Controller: publisher.Controller, State: "published", StateChangedAt: at.Add(offset + 7*time.Minute),
		}
		published, err := service.Publish(publication, authorityAssertion(publisher, 0, "agentic-lab-publish-private-"+suffix, publication.StateChangedAt))
		if err != nil {
			t.Fatalf("project Agentic Lab private contribution %s: %v", suffix, err)
		}
		return issued, published
	}

	active, activePublication := issue("active", "AJ delivered a source-bound Agentic Lab opportunity report.", 0)
	revoked, _ := issue("revoked", "AJ delivered a synthetic contribution later revoked from the private projection.", 10*time.Minute)
	revoked, effects, err := service.RevokeAttestation(revoked.Header.ID, authorityAssertion(issuer, revoked.Header.Revision, "agentic-lab-revoke-attestation", at.Add(18*time.Minute)))
	if err != nil {
		t.Fatalf("revoke Agentic Lab attestation: %v", err)
	}
	if active.State != "active" || activePublication.Visibility != "private" || revoked.State != "revoked" || len(effects) == 0 {
		t.Fatalf("Agentic Lab contribution lifecycle active=%+v publication=%+v revoked=%+v effects=%d", active, activePublication, revoked, len(effects))
	}
	return strideE10AgenticLabContributionFixture{OrganizationGrant: organization, ActiveAttestation: active, ActivePublication: activePublication, RevokedAttestation: revoked, RevocationEffects: effects}
}

func countAgenticLabMessages(thread scoutChatThreadRecord, kind string) int {
	count := 0
	for _, message := range thread.Messages {
		switch kind {
		case "agent":
			if message.Via == "stride_agent" && message.Role == "scout" && message.AuthorEmail == "" {
				count++
			}
		case "deleted":
			if message.ID == "stride-agentic-lab-delete" {
				count++
			}
		}
	}
	return count
}

func countAgenticLabTranscripts(entries []meetingMemoryEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Metadata["source"] == "synthetic_agentic_lab" && entry.Metadata["speakerType"] == "agent" && strings.HasPrefix(entry.Metadata["agentId"], "agent_") {
			count++
		}
	}
	return count
}

func TestStrideE10AgenticLabIsTestOnlyAndProviderless(t *testing.T) {
	productionSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(productionSource), "STRIDE Agentic Lab") || strings.Contains(string(productionSource), "stride-agentic-lab") {
		t.Fatal("production main must not register or seed the Agentic Lab")
	}
}
