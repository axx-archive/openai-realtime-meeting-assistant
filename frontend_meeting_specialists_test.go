package main

import (
	"os"
	"strings"
	"testing"
)

func TestFrontendMeetingSpecialistsUsesRealControlRouteAndHonestDisclosure(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	for _, want := range []string{
		`id="roomMoreSpecialists"`,
		`id="roomScoutQuickAction"`,
		`id="meetingSpecialistsPanel"`,
		`/api/rooms/agents/scout`,
		`/api/stride/v1/meeting-specialists`,
		`data-room-scout-action=`,
		`Invite Scout`,
		`Dismiss Scout`,
		`Add Scout to this meeting`,
		`data-specialist-action="request"`,
		`data-specialist-action="approved"`,
		`data-specialist-action="declined"`,
		`data-specialist-action="dismissed"`,
		`Employee agents still require approval for the exact request`,
		`Meeting transcription remains independent`,
		`Provider voice remains visibly fenced`,
		`Voice joining stays off until provider qualification is complete`,
		`meetingSpecialistContextLabel(invitation.contextClasses)`,
		`meetingSpecialistAudienceLabel(invitation.audience)`,
		`meetingSpecialistLimitsLabel(invitation.hardLimits)`,
		`Approved context and limits`,
		`Hard limits`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("meeting specialist surface is missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`Mary · Marketing Agent`,
		`providerSessionStarted: true`,
		`data-specialist-action="join"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("meeting specialist surface embeds forbidden claim %q", forbidden)
		}
	}
}

func TestFrontendMeetingSpecialistsEscapesEveryRuntimeInterpolation(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	start := strings.Index(html, "function renderMeetingSpecialists")
	end := strings.Index(html, "async function meetingSpecialistFetch")
	if start < 0 || end <= start {
		t.Fatal("could not isolate meeting specialist renderer")
	}
	renderer := html[start:end]
	for _, interpolation := range []string{
		"escapeHtml(scout?.name || 'Scout')",
		"escapeHtml(scoutState)",
		"escapeHtml(invitation.displayName || invitation.agentId",
		"escapeHtml(meetingSpecialistStatusLabel(invitation.status))",
		"escapeHtml(invitation.id)",
		"escapeHtml(purpose)",
		"escapeHtml(stateCopy)",
		"escapeHtml(contextLabel)",
		"escapeHtml(audienceLabel)",
		"escapeHtml(expectedLabel)",
		"escapeHtml(limitsLabel)",
		"escapeHtml(candidate.displayName || candidate.agentId)",
		"escapeHtml(candidate.agentId)",
	} {
		if !strings.Contains(renderer, interpolation) {
			t.Fatalf("runtime interpolation %q is not escaped", interpolation)
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

func TestFrontendDeclaresMeetingSpecialistBusyGuardBeforeBootstrapProjection(t *testing.T) {
	source, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(source)
	declaration := strings.Index(html, "let meetingSpecialistsBusy = false")
	bootstrapUse := strings.Index(html, "meetingSpecialistsBusy || !appShell.classList.contains('is-in-room')")
	if declaration < 0 || bootstrapUse < 0 || declaration >= bootstrapUse {
		t.Fatal("meetingSpecialistsBusy must be initialized before authenticated room projection can read it")
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
