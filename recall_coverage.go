package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrRecallCoverageInvalid = errors.New("recall coverage is invalid")

type RecallCoverageStatus string

const (
	RecallCoverageComplete    RecallCoverageStatus = "complete"
	RecallCoveragePartial     RecallCoverageStatus = "partial"
	RecallCoverageUnavailable RecallCoverageStatus = "unavailable"
)

type RecallSourceStatus string

const (
	RecallSourceFresh   RecallSourceStatus = "fresh"
	RecallSourcePartial RecallSourceStatus = "partial"
	RecallSourceStale   RecallSourceStatus = "stale"
	RecallSourceMissing RecallSourceStatus = "missing"
	RecallSourceFailed  RecallSourceStatus = "failed"
	RecallSourceOmitted RecallSourceStatus = "omitted"
)

type RecallLaneState string

const (
	RecallLaneActive      RecallLaneState = "active"
	RecallLaneDegraded    RecallLaneState = "degraded"
	RecallLaneUnavailable RecallLaneState = "unavailable"
	RecallLaneNotRequired RecallLaneState = "not_required"
)

type RecallSourceCoverage struct {
	SourceFamily  string             `json:"sourceFamily"`
	ObjectID      string             `json:"objectId"`
	ContentDigest string             `json:"contentDigest"`
	Status        RecallSourceStatus `json:"status"`
}

type RecallLaneCoverage struct {
	Lexical  RecallLaneState `json:"lexical"`
	Semantic RecallLaneState `json:"semantic"`
	Digest   RecallLaneState `json:"digest"`
	Raw      RecallLaneState `json:"raw"`
}

// RecallCoverage contains only the authorized inventory. Counts or IDs for
// denied sources never enter this record or downstream prompts.
type RecallCoverage struct {
	Digest                 string                 `json:"digest"`
	SnapshotID             string                 `json:"snapshotId"`
	Status                 RecallCoverageStatus   `json:"status"`
	Reason                 string                 `json:"reason,omitempty"`
	RequestedStartUTC      time.Time              `json:"requestedStartUtc"`
	RequestedEndUTC        time.Time              `json:"requestedEndUtc"`
	ResolvedStartUTC       time.Time              `json:"resolvedStartUtc"`
	ResolvedEndUTC         time.Time              `json:"resolvedEndUtc"`
	Timezone               string                 `json:"timezone"`
	SourceHighWater        uint64                 `json:"sourceHighWater"`
	ProjectionHighWater    uint64                 `json:"projectionHighWater"`
	AdmissionRelative      bool                   `json:"admissionRelative"`
	CaptureSequenceCutoff  uint64                 `json:"captureSequenceCutoff,omitempty"`
	CaptureCompleteThrough uint64                 `json:"captureCompleteThrough,omitempty"`
	Settled                bool                   `json:"settled"`
	LateArrivalSources     int                    `json:"lateArrivalSources"`
	Sources                []RecallSourceCoverage `json:"sources"`
	AuthorizedSources      int                    `json:"authorizedSources"`
	FreshSources           int                    `json:"freshSources"`
	PartialSources         int                    `json:"partialSources"`
	StaleSources           int                    `json:"staleSources"`
	MissingSources         int                    `json:"missingSources"`
	FailedSources          int                    `json:"failedSources"`
	OmittedSources         int                    `json:"omittedSources"`
	Lanes                  RecallLaneCoverage     `json:"lanes"`
	AsOf                   time.Time              `json:"asOf"`
}

func (coverage RecallCoverage) Validate() error {
	if !isHexDigest(coverage.Digest) || !isHexDigest(coverage.SnapshotID) || !validRecallCoverageStatus(coverage.Status) || coverage.RequestedStartUTC.IsZero() || coverage.RequestedEndUTC.IsZero() ||
		!coverage.RequestedStartUTC.Before(coverage.RequestedEndUTC) || coverage.ResolvedStartUTC.IsZero() || coverage.ResolvedEndUTC.IsZero() ||
		!coverage.ResolvedStartUTC.Before(coverage.ResolvedEndUTC) || strings.TrimSpace(coverage.Timezone) == "" || coverage.AsOf.IsZero() ||
		!validRecallLaneState(coverage.Lanes.Lexical) || !validRecallLaneState(coverage.Lanes.Semantic) ||
		!validRecallLaneState(coverage.Lanes.Digest) || !validRecallLaneState(coverage.Lanes.Raw) {
		return ErrRecallCoverageInvalid
	}
	if _, err := time.LoadLocation(coverage.Timezone); err != nil {
		return ErrRecallCoverageInvalid
	}
	seen := make(map[string]bool, len(coverage.Sources))
	counts := map[RecallSourceStatus]int{}
	for _, source := range coverage.Sources {
		if strings.TrimSpace(source.SourceFamily) == "" || strings.TrimSpace(source.ObjectID) == "" || !isHexDigest(source.ContentDigest) || !validRecallSourceStatus(source.Status) {
			return ErrRecallCoverageInvalid
		}
		key := source.SourceFamily + "\x00" + source.ObjectID
		if seen[key] {
			return ErrRecallCoverageInvalid
		}
		seen[key] = true
		counts[source.Status]++
	}
	if coverage.AuthorizedSources != len(coverage.Sources) || coverage.FreshSources != counts[RecallSourceFresh] ||
		coverage.PartialSources != counts[RecallSourcePartial] || coverage.StaleSources != counts[RecallSourceStale] ||
		coverage.MissingSources != counts[RecallSourceMissing] || coverage.FailedSources != counts[RecallSourceFailed] ||
		coverage.OmittedSources != counts[RecallSourceOmitted] {
		return ErrRecallCoverageInvalid
	}
	if coverage.LateArrivalSources < 0 || coverage.LateArrivalSources > coverage.AuthorizedSources ||
		(!coverage.AdmissionRelative && (coverage.CaptureSequenceCutoff != 0 || coverage.LateArrivalSources != 0)) {
		return ErrRecallCoverageInvalid
	}
	want := deriveRecallCoverageStatus(coverage)
	if coverage.Status != want || (coverage.Status == RecallCoverageComplete && strings.TrimSpace(coverage.Reason) != "") ||
		(coverage.Status != RecallCoverageComplete && strings.TrimSpace(coverage.Reason) == "") {
		return ErrRecallCoverageInvalid
	}
	wantDigest, err := coverage.CanonicalDigest()
	if err != nil || coverage.Digest != wantDigest {
		return ErrRecallCoverageInvalid
	}
	return nil
}

func (coverage RecallCoverage) CanonicalDigest() (string, error) {
	copy := coverage
	copy.Digest = ""
	raw, err := canonicalJSON(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func deriveRecallCoverageStatus(coverage RecallCoverage) RecallCoverageStatus {
	if coverage.Lanes.Raw == RecallLaneUnavailable && coverage.Lanes.Lexical == RecallLaneUnavailable && coverage.Lanes.Digest == RecallLaneUnavailable {
		return RecallCoverageUnavailable
	}
	// Coverage describes whether the authorized primary inventory can support
	// the answer, not whether every acceleration lane is healthy. Projection,
	// digest, or semantic lag remains visible in Lanes/high-waters, while a
	// complete raw-primary fallback may still honestly cover the whole range.
	if coverage.AuthorizedSources == 0 || !coverage.ResolvedStartUTC.Equal(coverage.RequestedStartUTC) || !coverage.ResolvedEndUTC.Equal(coverage.RequestedEndUTC) ||
		coverage.Lanes.Raw != RecallLaneActive {
		return RecallCoveragePartial
	}
	if coverage.AdmissionRelative && (!coverage.Settled || coverage.CaptureCompleteThrough < coverage.CaptureSequenceCutoff || coverage.LateArrivalSources > 0) {
		return RecallCoveragePartial
	}
	for _, source := range coverage.Sources {
		if source.Status != RecallSourceFresh {
			return RecallCoveragePartial
		}
	}
	return RecallCoverageComplete
}

func validRecallCoverageStatus(status RecallCoverageStatus) bool {
	return status == RecallCoverageComplete || status == RecallCoveragePartial || status == RecallCoverageUnavailable
}

func validRecallSourceStatus(status RecallSourceStatus) bool {
	switch status {
	case RecallSourceFresh, RecallSourcePartial, RecallSourceStale, RecallSourceMissing, RecallSourceFailed, RecallSourceOmitted:
		return true
	default:
		return false
	}
}

func validRecallLaneState(state RecallLaneState) bool {
	switch state {
	case RecallLaneActive, RecallLaneDegraded, RecallLaneUnavailable, RecallLaneNotRequired:
		return true
	default:
		return false
	}
}
