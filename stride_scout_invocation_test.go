package main

import (
	"testing"
	"time"
)

func TestSTRIDEScoutLexicalMentionsRejectSubstrings(t *testing.T) {
	for _, test := range []struct {
		text string
		want bool
	}{
		{"@scout what happened?", true},
		{"hey (@Scout), please help", true},
		{"foo@scout should stay literal", false},
		{"@scouting should not invoke", false},
		{"@scout-team should not invoke", false},
		{"@scout_2 should not invoke", false},
	} {
		if got := strideLexicallyMentionsScout(test.text); got != test.want {
			t.Fatalf("mention(%q)=%v, want %v", test.text, got, test.want)
		}
	}
}

func TestSTRIDEScoutVoiceMentionsAcceptNaturalNameReferences(t *testing.T) {
	for _, test := range []struct {
		text string
		want bool
	}{
		{"Hey Scout, can you help?", true},
		{"What is Scout's opinion on this?", true},
		{"Could Scott weigh in?", true},
		{"The scouting report is ready", false},
		{"We should wrap up", false},
	} {
		if got := strideVoiceMentionsScout(test.text); got != test.want {
			t.Fatalf("voice mention(%q)=%v, want %v", test.text, got, test.want)
		}
	}
}

func TestSTRIDEScoutInvocationSurfaceRulesAndAudienceBoundary(t *testing.T) {
	machine := NewSTRIDEScoutInvocationMachine(time.Minute)
	base := STRIDEScoutInvocationInput{Member: true, ConsentAllowed: true, At: time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)}
	if decision := machine.Evaluate(withScoutSurface(base, STRIDEScoutPrivate, "ordinary direct message")); !decision.Invoke || decision.Reason != "direct_private" {
		t.Fatalf("private decision: %#v", decision)
	}
	if decision := machine.Evaluate(withScoutSurface(base, STRIDEScoutPublic, "ordinary chatter")); decision.Invoke || decision.Reason != "mention_required" {
		t.Fatalf("public ordinary chatter decision: %#v", decision)
	}
	if decision := machine.Evaluate(withScoutSurface(base, STRIDEScoutProject, "@scout find the brief")); !decision.Invoke {
		t.Fatalf("project explicit mention decision: %#v", decision)
	}
	meetingText := withScoutSurface(base, STRIDEScoutMeetingText, "ordinary meeting chat")
	if decision := machine.Evaluate(meetingText); decision.Invoke {
		t.Fatalf("ordinary meeting text decision: %#v", decision)
	}
	meetingText.ExplicitButton = true
	if decision := machine.Evaluate(meetingText); !decision.Invoke {
		t.Fatalf("meeting text button decision: %#v", decision)
	}
	quoted := withScoutSurface(base, STRIDEScoutPublic, "@scout")
	quoted.Quoted = true
	if decision := machine.Evaluate(quoted); decision.Invoke || decision.Reason != "non_addressable_content" {
		t.Fatalf("quoted mention decision: %#v", decision)
	}
	guest := withScoutSurface(base, STRIDEScoutPrivate, "hello")
	guest.Guest = true
	if decision := machine.Evaluate(guest); decision.Invoke || decision.Reason != "membership_or_consent_required" {
		t.Fatalf("guest boundary decision: %#v", decision)
	}
}

func TestSTRIDEScoutMeetingVoiceHasBoundedVisibleFollowUpAndHumanPriority(t *testing.T) {
	machine := NewSTRIDEScoutInvocationMachine(20 * time.Second)
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	base := STRIDEScoutInvocationInput{Surface: STRIDEScoutMeetingVoice, Member: true, ConsentAllowed: true, At: now, SpeakerID: "AJ"}
	if decision := machine.Evaluate(base); decision.Invoke || decision.Reason != "spoken_wake_required" {
		t.Fatalf("ordinary voice decision: %#v", decision)
	}
	base.SpokenWake = true
	if decision := machine.Evaluate(base); !decision.Invoke || decision.State != STRIDEScoutEngaged || !decision.FollowUpUntil.Equal(now.Add(20*time.Second)) {
		t.Fatalf("wake decision: %#v", decision)
	}
	followUp := base
	followUp.SpokenWake = false
	followUp.At = now.Add(10 * time.Second)
	if decision := machine.Evaluate(followUp); !decision.Invoke || decision.Reason != "visible_follow_up_window" {
		t.Fatalf("follow up decision: %#v", decision)
	}
	crossSpeaker := followUp
	crossSpeaker.SpeakerID = "Tom"
	if decision := machine.Evaluate(crossSpeaker); decision.Invoke || decision.Reason != "spoken_wake_required" {
		t.Fatalf("cross-speaker crosstalk decision: %#v", decision)
	}
	base.SpokenWake = true
	machine.Evaluate(base)
	followUp.HumanSpeechPriority = true
	if decision := machine.Evaluate(followUp); decision.Invoke || decision.Reason != "human_barge_in" || decision.State != STRIDEScoutIdle {
		t.Fatalf("barge-in decision: %#v", decision)
	}
	base.SpokenWake = true
	machine.Evaluate(base)
	timeout := base
	timeout.SpokenWake = false
	timeout.At = now.Add(21 * time.Second)
	if decision := machine.Evaluate(timeout); decision.Invoke || decision.State != STRIDEScoutIdle || decision.Reason != "spoken_wake_required" {
		t.Fatalf("timeout decision: %#v", decision)
	}
	machine.Evaluate(base)
	machine.Dismiss()
	followUp.HumanSpeechPriority = false
	if decision := machine.Evaluate(followUp); decision.Invoke || decision.State != STRIDEScoutDismissed {
		t.Fatalf("dismissed decision: %#v", decision)
	}
}

func TestSTRIDEScoutResponseModeKeepsPersonalityBehindSafety(t *testing.T) {
	if got := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{Member: true, ConsentAllowed: true, Sensitive: true, Social: true, GIFAllowed: true, ChannelGIFAllowed: true}); got != STRIDEScoutResponseSafeRefusal {
		t.Fatalf("sensitive response mode=%q", got)
	}
	if got := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{Member: true, ConsentAllowed: true, Authority: "internal_write", AuthorizedArtifact: true}); got != STRIDEScoutResponseSafeRefusal {
		t.Fatalf("authority response mode=%q", got)
	}
	if got := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{Member: true, ConsentAllowed: true, AuthorizedArtifact: true}); got != STRIDEScoutResponseArtifactCard {
		t.Fatalf("artifact response mode=%q", got)
	}
	if got := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{Member: true, ConsentAllowed: true, Social: true, GIFAllowed: true, ChannelGIFAllowed: true}); got != STRIDEScoutResponseTextGIF {
		t.Fatalf("text GIF response mode=%q", got)
	}
	if got := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{Member: true, ConsentAllowed: true, Social: true, GIFAllowed: true, ChannelGIFAllowed: true, RequestGIFOnly: true}); got != STRIDEScoutResponseGIFOnly {
		t.Fatalf("GIF-only response mode=%q", got)
	}
}

func withScoutSurface(input STRIDEScoutInvocationInput, surface STRIDEScoutInvocationSurface, text string) STRIDEScoutInvocationInput {
	input.Surface = surface
	input.Text = text
	return input
}
