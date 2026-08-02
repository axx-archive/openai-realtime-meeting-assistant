package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func stridePreferenceEvidence(id string) STRIDEReference {
	return STRIDEReference{ContractType: STRIDEContractConversationEvent, ID: id, Revision: 1, Digest: strings.Repeat("c", 64)}
}

func stridePrivatePreferenceEvent(id, action, preferenceType, value, origin string, observed time.Time) STRIDECollaborationPreferenceEvent {
	return STRIDECollaborationPreferenceEvent{
		EventID: id, Action: action, SubjectPrincipal: "user-aj", Scope: stridePreferencePrivate, ScopeID: "user-aj",
		PreferenceType: preferenceType, Value: value, Origin: origin, Evidence: []STRIDEReference{stridePreferenceEvidence("evidence-" + id)},
		Confidence: 1, ObservedAt: observed, ExpiresAt: observed.Add(60 * 24 * time.Hour),
		Audience: STRIDEAudience{Visibility: "private", Principals: []string{"user-aj"}},
	}
}

func TestSTRIDECollaborationProfileDeterministicRebuildAndReinforcement(t *testing.T) {
	t0 := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	first := stridePrivatePreferenceEvent("preference-1", stridePreferenceObserve, "response_length", "brief", stridePreferenceInferred, t0)
	first.Confidence = 0.72
	second := stridePrivatePreferenceEvent("preference-2", stridePreferenceObserve, "response_length", "brief", stridePreferenceInferred, t0.Add(time.Hour))
	second.Confidence = 0.81
	second.Evidence = []STRIDEReference{stridePreferenceEvidence("z-evidence"), stridePreferenceEvidence("a-evidence")}
	third := stridePrivatePreferenceEvent("preference-3", stridePreferenceObserve, "meeting_pace", "leave space after decisions", stridePreferenceExplicit, t0.Add(2*time.Hour))

	forward, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{first, second, third}, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{third, second, first}, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forward, reversed) || forward.RebuildDigest == "" {
		t.Fatalf("rebuild was order-dependent:\nforward=%+v\nreversed=%+v", forward, reversed)
	}
	if len(forward.Preferences) != 2 {
		t.Fatalf("preferences=%+v", forward.Preferences)
	}
	var responseLength STRIDECollaborationPreferenceState
	for _, preference := range forward.Preferences {
		if preference.PreferenceType == "response_length" {
			responseLength = preference
		}
	}
	if responseLength.Value != "brief" || responseLength.Confidence != 0.81 || responseLength.ReinforcementCount != 2 || len(responseLength.Evidence) != 3 {
		t.Fatalf("reinforced preference=%+v", responseLength)
	}
}

func TestSTRIDECollaborationProfileCorrectionForgetAndExpiry(t *testing.T) {
	t0 := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	original := stridePrivatePreferenceEvent("preference-original", stridePreferenceObserve, "feedback_style", "direct", stridePreferenceExplicit, t0)
	correction := stridePrivatePreferenceEvent("preference-correction", stridePreferenceCorrect, "feedback_style", "direct but explain why", stridePreferenceExplicit, t0.Add(time.Hour))
	correction.CorrectsEventID = original.EventID
	corrected, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{original, correction}, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(corrected.Preferences) != 1 || corrected.Preferences[0].Value != "direct but explain why" || !reflect.DeepEqual(corrected.Preferences[0].CorrectionEventIDs, []string{"preference-correction"}) {
		t.Fatalf("corrected profile=%+v", corrected)
	}

	forget := stridePrivatePreferenceEvent("preference-forget", stridePreferenceForget, "feedback_style", "", stridePreferenceExplicit, t0.Add(2*time.Hour))
	forgotten, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{original, correction, forget}, t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	state := forgotten.Preferences[0]
	if state.Status != "forgotten" || state.Value != "" || state.ValueDigest != "" || state.Evidence != nil || state.Audience.Validate() == nil || state.Confidence != 0 || len(state.CorrectionEventIDs) != 0 {
		t.Fatalf("forgotten state retained learned content: %+v", state)
	}

	expiring := stridePrivatePreferenceEvent("preference-expiring", stridePreferenceObserve, "notification_timing", "morning", stridePreferenceInferred, t0)
	expiring.Confidence = 0.7
	expiring.ExpiresAt = t0.Add(time.Hour)
	expired, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{expiring}, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if expired.Preferences[0].Status != "expired" || expired.Preferences[0].Value != "" || expired.Preferences[0].Evidence != nil || expired.Preferences[0].Audience.Validate() == nil {
		t.Fatalf("expired preference retained projection data: %+v", expired.Preferences[0])
	}

	inferredAfterForget := stridePrivatePreferenceEvent("preference-relearn", stridePreferenceObserve, "feedback_style", "direct", stridePreferenceInferred, t0.Add(3*time.Hour))
	inferredAfterForget.Confidence = 0.7
	if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{original, forget, inferredAfterForget}, t0.Add(4*time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("inferred relearn after forget error=%v", err)
	}
}

func TestSTRIDECollaborationProfileRejectsSensitiveAndHighRiskInference(t *testing.T) {
	t0 := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*STRIDECollaborationPreferenceEvent)
	}{
		{"medical inference", func(event *STRIDECollaborationPreferenceEvent) { event.PreferenceType = "medical_condition" }},
		{"political inference", func(event *STRIDECollaborationPreferenceEvent) { event.PreferenceType = "political_affiliation" }},
		{"unknown psychological trait", func(event *STRIDECollaborationPreferenceEvent) { event.PreferenceType = "personality_disorder" }},
		{"low confidence", func(event *STRIDECollaborationPreferenceEvent) { event.Confidence = 0.4 }},
		{"overconfident inference", func(event *STRIDECollaborationPreferenceEvent) { event.Confidence = 0.99 }},
		{"long-lived inference", func(event *STRIDECollaborationPreferenceEvent) {
			event.ExpiresAt = event.ObservedAt.Add(91 * 24 * time.Hour)
		}},
		{"inferred correction", func(event *STRIDECollaborationPreferenceEvent) {
			event.Action = stridePreferenceCorrect
			event.CorrectsEventID = "preference-old"
		}},
		{"inferred forget", func(event *STRIDECollaborationPreferenceEvent) {
			event.Action = stridePreferenceForget
			event.Value = ""
		}},
		{"missing evidence", func(event *STRIDECollaborationPreferenceEvent) { event.Evidence = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := stridePrivatePreferenceEvent("preference-risk", stridePreferenceObserve, "response_length", "brief", stridePreferenceInferred, t0)
			event.Confidence = 0.7
			tc.mutate(&event)
			if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{event}, t0.Add(time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
				t.Fatalf("error=%v, want denied", err)
			}
		})
	}
}

func TestSTRIDECollaborationProfileEnforcesPrivateAndSharedScopes(t *testing.T) {
	t0 := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	privateLeak := stridePrivatePreferenceEvent("preference-private-leak", stridePreferenceObserve, "response_length", "brief", stridePreferenceExplicit, t0)
	privateLeak.Audience = STRIDEAudience{Visibility: "channel", Principals: []string{"user-aj", "channel-team"}}
	if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{privateLeak}, t0.Add(time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("private audience widening error=%v", err)
	}

	shared := stridePrivatePreferenceEvent("preference-shared", stridePreferenceObserve, "collaboration_channel", "use the team channel", stridePreferenceExplicit, t0)
	shared.Scope = stridePreferenceShared
	shared.ScopeID = "channel-team"
	shared.Audience = STRIDEAudience{Visibility: "channel", Principals: []string{"user-aj", "user-peer"}}
	profile, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{shared}, t0.Add(time.Hour))
	if err != nil || len(profile.Preferences) != 1 || profile.Preferences[0].Scope != stridePreferenceShared {
		t.Fatalf("explicit shared profile=%+v err=%v", profile, err)
	}

	inferredShared := shared
	inferredShared.EventID = "preference-inferred-shared"
	inferredShared.Origin = stridePreferenceInferred
	inferredShared.Confidence = 0.7
	if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{inferredShared}, t0.Add(time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("inferred shared error=%v", err)
	}

	missingChannelScope := shared
	missingChannelScope.EventID = "preference-shared-missing-scope"
	missingChannelScope.ScopeID = "user-aj"
	if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{missingChannelScope}, t0.Add(time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("missing separate shared scope error=%v", err)
	}
}

func TestSTRIDECollaborationProfileRejectsBadCorrectionAndDuplicateEvents(t *testing.T) {
	t0 := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	original := stridePrivatePreferenceEvent("preference-original", stridePreferenceObserve, "feedback_style", "direct", stridePreferenceExplicit, t0)
	badCorrection := stridePrivatePreferenceEvent("preference-correction", stridePreferenceCorrect, "feedback_style", "gentle", stridePreferenceExplicit, t0.Add(time.Hour))
	badCorrection.CorrectsEventID = "wrong-event"
	if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{original, badCorrection}, t0.Add(2*time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("bad correction error=%v", err)
	}
	duplicate := original
	duplicate.ObservedAt = duplicate.ObservedAt.Add(time.Minute)
	if _, err := ReduceSTRIDECollaborationProfile("user-aj", []STRIDECollaborationPreferenceEvent{original, duplicate}, t0.Add(2*time.Hour)); !errors.Is(err, ErrSTRIDECollaborationPreferenceDenied) {
		t.Fatalf("duplicate event error=%v", err)
	}
}
