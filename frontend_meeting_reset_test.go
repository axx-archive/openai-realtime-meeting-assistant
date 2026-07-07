package main

// Card 078 (room reset): a fresh untitled session must never wear the PRIOR
// meeting's mission theme in the topbar — the visible "continuation" Tyler
// saw. meetingDisplayName gates the dominant-theme fallback on insight
// freshness: the insight payload's createdAt must be at/after the live
// record's startedAt (or there is no live record at all); otherwise the label
// falls through to the dated 'meeting · <date>' name.

import (
	"os"
	"strings"
	"testing"
)

func TestMeetingDisplayNameGatesThemeFallbackOnInsightFreshness(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(raw)

	body := functionBody(html, "function meetingDisplayName()")
	if body == "" {
		t.Fatal("could not extract meetingDisplayName body")
	}

	// the theme source keeps its wrapper payload so createdAt rides along
	// (intelData.themes first, latestMissionInsight fallback — both are the
	// missionInsightEventPayload shape).
	if !strings.Contains(body, "const insightPayload = (intelData?.themes?.insight?.themes ? intelData.themes : null) || latestMissionInsight") {
		t.Error("meetingDisplayName must keep the insight wrapper payload so its createdAt is available to the freshness gate")
	}
	// freshness anchors: the ACTIVE record's start vs the insight's creation.
	if !strings.Contains(body, "meetingRecord?.active ? Date.parse(meetingRecord.startedAt || '') : NaN") {
		t.Error("meetingDisplayName must anchor freshness to the ACTIVE record's startedAt (no live record = no gate)")
	}
	if !strings.Contains(body, "Date.parse(insightPayload?.createdAt || '')") {
		t.Error("meetingDisplayName must read the insight payload's createdAt")
	}
	if !strings.Contains(body, "insightAtMs >= startedAtMs") {
		t.Error("meetingDisplayName must only use a theme synthesized at/after the live record started")
	}
	if !strings.Contains(body, "if (dominant && insightFresh)") {
		t.Error("meetingDisplayName must gate the dominant-theme label on insight freshness")
	}
	// a fresh untitled session falls back to the dated meeting name.
	if !strings.Contains(body, "return `meeting · ${new Date().toLocaleDateString(") {
		t.Error("meetingDisplayName must keep the dated 'meeting · <date>' fallback for a fresh untitled session")
	}
}
