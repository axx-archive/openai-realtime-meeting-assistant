package main

import "testing"

func TestNormalizeLayerTier(t *testing.T) {
	cases := map[string]layerTier{
		"low": layerTierLow, "LOW": layerTierLow, "q": layerTierLow, "0": layerTierLow,
		"medium": layerTierMedium, "mid": layerTierMedium, "1": layerTierMedium,
		"high": layerTierHigh, "f": layerTierHigh, "2": layerTierHigh,
		"":        layerTierHigh, // unset defaults to best quality
		"garbage": layerTierHigh, // unrecognised defaults to best quality
	}
	for raw, want := range cases {
		if got := normalizeLayerTier(raw); got != want {
			t.Errorf("normalizeLayerTier(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSortLayersByQualityIsAscendingAndStable(t *testing.T) {
	in := []layerOption{
		{trackID: "hi", rid: "f"},
		{trackID: "lo", rid: "q"},
		{trackID: "mid", rid: "h"},
	}
	sorted := sortLayersByQuality(in)
	want := []string{"lo", "mid", "hi"}
	for i, w := range want {
		if sorted[i].trackID != w {
			t.Fatalf("sorted[%d].trackID = %q, want %q (full order %+v)", i, sorted[i].trackID, w, sorted)
		}
	}
	// Input must not be mutated.
	if in[0].trackID != "hi" {
		t.Fatalf("sortLayersByQuality mutated its input: %+v", in)
	}
}

func TestChooseLayerForTier(t *testing.T) {
	group := []layerOption{
		{trackID: "lo", rid: "q"},
		{trackID: "mid", rid: "h"},
		{trackID: "hi", rid: "f"},
	}
	cases := map[layerTier]string{
		layerTierLow:    "lo",
		layerTierMedium: "mid",
		layerTierHigh:   "hi",
	}
	for tier, want := range cases {
		if got := chooseLayerForTier(group, tier); got != want {
			t.Errorf("chooseLayerForTier(%q) = %q, want %q", tier, got, want)
		}
	}
}

func TestChooseLayerForTierTwoLayerMediumBiasesLow(t *testing.T) {
	group := []layerOption{{trackID: "lo", rid: "q"}, {trackID: "hi", rid: "f"}}
	if got := chooseLayerForTier(group, layerTierMedium); got != "lo" {
		t.Fatalf("two-layer medium = %q, want lower layer %q", got, "lo")
	}
}

func TestChooseLayerForTierNoSelectionWhenNotSimulcast(t *testing.T) {
	// Zero or one layer => "" meaning "forward everything unchanged".
	if got := chooseLayerForTier(nil, layerTierHigh); got != "" {
		t.Errorf("empty group: got %q, want \"\"", got)
	}
	single := []layerOption{{trackID: "only", rid: ""}}
	if got := chooseLayerForTier(single, layerTierLow); got != "" {
		t.Errorf("single-layer group: got %q, want \"\"", got)
	}
}

func TestIsSimulcastGroup(t *testing.T) {
	cases := []struct {
		name   string
		layers []layerOption
		want   bool
	}{
		{"nil group", nil, false},
		{"single layer", []layerOption{{trackID: "a"}}, false},
		{"all empty rids", []layerOption{{trackID: "a"}, {trackID: "b"}}, false},
		{"duplicated rid", []layerOption{{trackID: "a", rid: "f"}, {trackID: "b", rid: "f"}}, false},
		{"one empty one set", []layerOption{{trackID: "a"}, {trackID: "b", rid: "f"}}, false},
		{"distinct rids", []layerOption{{trackID: "a", rid: "q"}, {trackID: "b", rid: "f"}}, true},
	}
	for _, tc := range cases {
		if got := isSimulcastGroup(tc.layers); got != tc.want {
			t.Errorf("%s: isSimulcastGroup = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestChooseLayerForTierAllEmptyRIDGroupForwardsEverything(t *testing.T) {
	// Duplicate forwarded tracks of the same source (renegotiation/SSRC churn)
	// share a group with all-empty RIDs. That is not simulcast: no layer may be
	// chosen, otherwise a stale twin could win the lexicographic tie-break and
	// filter the live track.
	group := []layerOption{
		{trackID: "stream:cam:999"},  // stale twin, sorts after the live track
		{trackID: "stream:cam:1000"}, // live track
	}
	for _, tier := range []layerTier{layerTierLow, layerTierMedium, layerTierHigh} {
		if got := chooseLayerForTier(group, tier); got != "" {
			t.Errorf("tier %q: chooseLayerForTier = %q, want \"\" (forward everything)", tier, got)
		}
		if !subscriberWantsLayer("stream:cam:1000", tier, group) {
			t.Errorf("tier %q: live duplicate must be forwarded", tier)
		}
	}
}

func TestSubscriberAcceptsLayerLockedEmptyRIDDuplicatesAreForwarded(t *testing.T) {
	snapshotPeerState(t)
	listLock.Lock()
	trackLayerGroups = map[string]string{
		"stream:cam:999":  "stream:cam", // stale twin left behind by SSRC churn
		"stream:cam:1000": "stream:cam", // live republished track
	}
	trackLayerRIDs = map[string]string{"stream:cam:999": "", "stream:cam:1000": ""}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if !subscriberAcceptsLayerLocked("sub-1", "stream:cam:1000") {
		t.Fatal("live track must be forwarded when its RID-less group has a stale duplicate")
	}
	if !subscriberAcceptsLayerLocked("sub-1", "stream:cam:999") {
		t.Fatal("RID-less duplicates must all be forwarded (no false simulcast filtering)")
	}
}

func TestSubscriberWantsLayer(t *testing.T) {
	group := []layerOption{
		{trackID: "lo", rid: "q"},
		{trackID: "mid", rid: "h"},
		{trackID: "hi", rid: "f"},
	}
	// A low-tier subscriber accepts only the low layer.
	if !subscriberWantsLayer("lo", layerTierLow, group) {
		t.Error("low-tier subscriber should accept the low layer")
	}
	if subscriberWantsLayer("hi", layerTierLow, group) {
		t.Error("low-tier subscriber should reject the high layer")
	}
	// A single-layer (non-simulcast) source is always accepted regardless of tier.
	solo := []layerOption{{trackID: "only", rid: ""}}
	if !subscriberWantsLayer("only", layerTierLow, solo) {
		t.Error("non-simulcast track should always be forwarded")
	}
}

func TestSubscriberAcceptsLayerLockedNonSimulcastIsForwarded(t *testing.T) {
	snapshotPeerState(t)
	listLock.Lock()
	trackLayerGroups = map[string]string{"stream:cam:1": "stream:cam"}
	trackLayerRIDs = map[string]string{"stream:cam:1": ""}
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if !subscriberAcceptsLayerLocked("sub-1", "stream:cam:1") {
		t.Fatal("a lone (non-simulcast) layer must be forwarded to every subscriber")
	}
	// An untracked track id has no group and must also be forwarded.
	if !subscriberAcceptsLayerLocked("sub-1", "unknown-track") {
		t.Fatal("an untracked track must be forwarded (backward compatible)")
	}
}

func TestSubscriberAcceptsLayerLockedSimulcastHonoursTier(t *testing.T) {
	snapshotPeerState(t)
	listLock.Lock()
	trackLayerGroups = map[string]string{
		"stream:cam:1": "stream:cam",
		"stream:cam:2": "stream:cam",
		"stream:cam:3": "stream:cam",
	}
	trackLayerRIDs = map[string]string{
		"stream:cam:1": "q",
		"stream:cam:2": "h",
		"stream:cam:3": "f",
	}
	subscriberLayerTiers = map[string]string{"sub-low": "low", "sub-high": "high"}
	listLock.Unlock()

	listLock.RLock()
	defer listLock.RUnlock()
	if !subscriberAcceptsLayerLocked("sub-low", "stream:cam:1") {
		t.Error("low-tier subscriber should receive the q layer")
	}
	if subscriberAcceptsLayerLocked("sub-low", "stream:cam:3") {
		t.Error("low-tier subscriber should not receive the f layer")
	}
	if !subscriberAcceptsLayerLocked("sub-high", "stream:cam:3") {
		t.Error("high-tier subscriber should receive the f layer")
	}
	// A subscriber with no recorded tier defaults to high quality.
	if !subscriberAcceptsLayerLocked("sub-default", "stream:cam:3") {
		t.Error("default subscriber should receive the f (high) layer")
	}
	if subscriberAcceptsLayerLocked("sub-default", "stream:cam:1") {
		t.Error("default subscriber should not receive the q (low) layer")
	}
}

func TestSetSubscriberLayerTierReportsChange(t *testing.T) {
	snapshotPeerState(t)
	listLock.Lock()
	subscriberLayerTiers = map[string]string{}
	listLock.Unlock()

	if !setSubscriberLayerTier("sub-1", layerTierLow) {
		t.Fatal("first selection should report a change")
	}
	if setSubscriberLayerTier("sub-1", layerTierLow) {
		t.Fatal("re-selecting the same tier should report no change")
	}
	if !setSubscriberLayerTier("sub-1", layerTierHigh) {
		t.Fatal("changing tier should report a change")
	}
	if setSubscriberLayerTier("", layerTierLow) {
		t.Fatal("empty session id should be ignored")
	}
}
