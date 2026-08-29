package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendMeetingAgentSurfaceExposesOnlyScout(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="roomScoutQuickAction"`,
		`roomScoutVoiceAvailability = { enabled: false, reason: 'quality_gate_pending' }`,
		`function loadRoomScoutVoiceAvailability()`,
		`roomScoutQuickActionButton.hidden = Boolean(guestMode) || !roomScoutVoiceAvailability.enabled`,
		`/api/rooms/agents/scout`,
		`Add Scout to this meeting`,
		`Remove Scout from this meeting`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Scout meeting surface is missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`id="meetingSpecialistsPanel"`,
		`/api/stride/v1/meeting-specialists`,
		`data-specialist-action=`,
		`function requestMeetingSpecialist`,
		`function resolveMeetingSpecialist`,
		`Scout and specialist participants`,
		`Agent team`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("arbitrary fourth-agent addressability remains in the customer shell: %q", forbidden)
		}
	}
}

func TestFrontendWorkHasNoStandaloneLegacyDesignOrGrillSurface(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, forbidden := range []string{
		`id="designTool"`,
		`id="grillTool"`,
		`data-tool="design"`,
		`data-tool="grill"`,
		`data-agent-tool="design"`,
		`data-agent-tool="grill"`,
		`data-agent-tool-form="design"`,
		`data-agent-tool-form="grill"`,
		`function renderOfficeAgentWorkforce`,
		`function openToolPalette`,
		`/assistant/tools`,
		`research, design, grill, and plans land here`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dead standalone Work tool surface remains: %q", forbidden)
		}
	}
	for _, want := range []string{`id="researchTool"`, `Presentations and research stay organized`, `research and presentations land here as durable outputs.`} {
		if !strings.Contains(html, want) {
			t.Fatalf("canonical Work surface is missing %q", want)
		}
	}
}

func TestFrontendProjectsAgentsAndCameraOffHumansIntoAudioPresenceBar(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`case 'agent_participants':`,
		`handleRoomAgentParticipants(message.data)`,
		`id="roomPresenceBar"`,
		`function renderRoomPresenceBar()`,
		`room-presence-bar__member`,
		`function participantUsesAudioPresence(name)`,
		`state.cameraOff && !state.screenSharing`,
		`tile.hidden = audioOnly`,
		`tile.classList.toggle('is-audio-only-presence', audioOnly)`,
		`.filter(tile => !tile.hidden).length`,
		`member.dataset.personType = 'human'`,
		`member.style.setProperty('--room-presence-color', '#C8C6C3')`,
		`member.dataset.personType = 'agent'`,
		`member.style.setProperty('--room-presence-color', '#FF5A19')`,
		`...roomAgentParticipants.map(agent => agent.name)`,
		`room-presence-waveform`,
		`data-voice-state`,
		`...roomAgentParticipants.map(agent => agent.name.toLowerCase())`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("room agent participant projection is missing %q", want)
		}
	}
	for _, forbidden := range []string{`room-agent-cradle`, `is-agent-participant`, `id="roomAgentBench"`, `Agents in the room`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("room agents must not occupy camera tiles or duplicate the home cradle: found %q", forbidden)
		}
	}
}

func TestFrontendExpectsAgentAudioOnlyForLiveProviderSession(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(string(source), "function expectedRemoteAudioParticipantNames()")
	for _, want := range []string{
		"agent.providerSessionStarted === true",
		"agent.voiceState || agent.status",
		"'starting', 'degraded', 'error', 'closed'",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("agent audio expectation is missing %q", want)
		}
	}
}

func TestFrontendAudioPresenceAlwaysWinsParticipantTileLayoutCascade(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	selector := `.video-tile.is-audio-only-presence {`
	start := strings.Index(html, selector)
	if start == -1 {
		t.Fatalf("missing %q CSS rule", selector)
	}
	block := html[start:]
	if end := strings.Index(block, "}"); end != -1 {
		block = block[:end]
	}
	if !strings.Contains(block, "display: none !important") {
		t.Fatal("audio-only participant tiles must win later grid, pinned, screen-share, and mobile display rules")
	}

	for _, laterDisplayRule := range []string{
		`.hearth-stage[data-room-layout="pinned"] .hearth-seat.is-on-stage {`,
		`#appShell.is-in-room[data-tool="room"] .presentation-tile.is-screen-sharing .hearth-seat {`,
	} {
		if later := strings.LastIndex(html, laterDisplayRule); later <= start {
			t.Fatalf("test precondition failed: expected later participant layout rule %q", laterDisplayRule)
		}
	}

	for _, behavior := range []string{
		`return Boolean(state.cameraOff && !state.screenSharing)`,
		`tile.hidden = audioOnly`,
		`tile.classList.toggle('is-audio-only-presence', audioOnly)`,
		`member.setAttribute('aria-label'`,
	} {
		if !strings.Contains(html, behavior) {
			t.Fatalf("audio-presence projection is missing %q", behavior)
		}
	}
}

func TestFrontendDeclaresRoomScoutBusyGuardBeforeBootstrapProjection(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	declaration := strings.Index(html, "let roomScoutMutationBusy = false")
	bootstrapUse := strings.Index(html, "roomScoutMutationBusy || !appShell.classList.contains('is-in-room')")
	if declaration < 0 || bootstrapUse < 0 || declaration >= bootstrapUse {
		t.Fatal("roomScoutMutationBusy must be initialized before authenticated room projection can read it")
	}
}

func TestFrontendScoutQuickActionAlwaysShowsProgressAndOutcome(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`? (scout ? 'Removing Scout…' : 'Adding Scout…')`,
		`'adding Scout to the room…'`,
		`'Scout joined the room'`,
		`showToast({ text: message, kind: 'error' })`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Scout quick action is missing visible feedback %q", want)
		}
	}
}
