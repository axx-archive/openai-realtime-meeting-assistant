package main

import (
	"sort"
	"strings"
)

// Simulcast forwarding controls.
//
// A publisher may send the same video as several encodings ("layers") that
// differ in resolution/bitrate, distinguished on the wire by their RTP RID.
// Each layer arrives as its own remote track and becomes its own forwarded
// TrackLocal, so a single source produces a *group* of sibling layer tracks.
//
// These helpers let the SFU forward just one layer of a group to each
// subscriber, chosen from a coarse quality tier the subscriber requests
// (adapting to its bandwidth / viewport size). The logic is deliberately
// inert for non-simulcast sources: a group with zero or one layer always
// forwards as-is, so behaviour is identical to a plain single-encoding room.

// layerOption is one forwarded simulcast layer within a source group.
type layerOption struct {
	trackID string // forwarded TrackLocal ID
	rid     string // RTP RID of the encoding ("" when not simulcast)
}

// layerTier is a coarse, transport-independent quality request from a subscriber.
type layerTier string

const (
	layerTierLow    layerTier = "low"
	layerTierMedium layerTier = "medium"
	layerTierHigh   layerTier = "high"
)

// defaultLayerTier is used when a subscriber has expressed no preference: send
// the best quality so behaviour matches a non-adaptive room until the client
// opts into a lower layer.
const defaultLayerTier = layerTierHigh

// normalizeLayerTier maps arbitrary client input to a known tier, defaulting to
// the highest quality for anything unrecognised.
func normalizeLayerTier(raw string) layerTier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low", "lo", "q", "quarter", "0":
		return layerTierLow
	case "medium", "mid", "m", "half", "h", "1":
		return layerTierMedium
	case "high", "hi", "full", "f", "2", "":
		return layerTierHigh
	default:
		return defaultLayerTier
	}
}

// rankSimulcastRID gives a stable quality ordering for the common RID naming
// conventions (browsers and SFUs variously use q/h/f, low/mid/high, or 0/1/2).
// Higher rank = higher quality. Unrecognised RIDs sort in the middle and then
// lexicographically, so ordering stays deterministic.
func rankSimulcastRID(rid string) int {
	switch strings.ToLower(strings.TrimSpace(rid)) {
	case "q", "quarter", "low", "lo", "0":
		return 0
	case "h", "half", "mid", "medium", "1":
		return 1
	case "f", "full", "high", "hi", "2":
		return 2
	default:
		return 1
	}
}

// sortLayersByQuality returns the layers ordered ascending by quality (lowest
// first). It does not mutate the input slice.
func sortLayersByQuality(layers []layerOption) []layerOption {
	sorted := make([]layerOption, len(layers))
	copy(sorted, layers)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rankSimulcastRID(sorted[i].rid), rankSimulcastRID(sorted[j].rid)
		if ri != rj {
			return ri < rj
		}
		if sorted[i].rid != sorted[j].rid {
			return sorted[i].rid < sorted[j].rid
		}
		return sorted[i].trackID < sorted[j].trackID
	})

	return sorted
}

// chooseLayerForTier picks the forwarded TrackLocal ID a subscriber on the given
// tier should receive from a source group. It returns "" when no selection is
// needed — i.e. the group has zero or one layer (a non-simulcast source) — in
// which case the caller forwards every track of the group unchanged.
func chooseLayerForTier(layers []layerOption, tier layerTier) string {
	if len(layers) <= 1 {
		return ""
	}

	sorted := sortLayersByQuality(layers)
	switch tier {
	case layerTierLow:
		return sorted[0].trackID
	case layerTierHigh:
		return sorted[len(sorted)-1].trackID
	default: // medium and anything else: the middle layer, biased to the lower of two.
		return sorted[(len(sorted)-1)/2].trackID
	}
}

// subscriberWantsLayer reports whether a subscriber on the given tier should be
// forwarded the specified track from its source group. Non-simulcast groups
// (<=1 layer) always forward; simulcast groups forward only the chosen layer.
func subscriberWantsLayer(trackID string, tier layerTier, group []layerOption) bool {
	chosen := chooseLayerForTier(group, tier)
	return chosen == "" || chosen == trackID
}
