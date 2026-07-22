package main

import (
	"errors"
	"strings"
	"time"
)

var ErrTemporalQueryInvalid = errors.New("temporal query is invalid")

type TemporalInterpretation string

const (
	TemporalExplicitRange   TemporalInterpretation = "explicit_range"
	TemporalFirstMinutes    TemporalInterpretation = "first_minutes"
	TemporalLastMinutes     TemporalInterpretation = "last_minutes"
	TemporalBeforeAdmission TemporalInterpretation = "before_admission"
)

// TemporalQuery is the single half-open interval contract used by live recap
// and historical recall. Admission-relative windows additionally bind to the
// capture sequence observed before access_granted.
type TemporalQuery struct {
	StartUTC              time.Time              `json:"startUtc"`
	EndUTC                time.Time              `json:"endUtc"`
	Timezone              string                 `json:"timezone"`
	RoomID                string                 `json:"roomId,omitempty"`
	SittingID             string                 `json:"sittingId,omitempty"`
	AdmissionAnchorID     string                 `json:"admissionAnchorId,omitempty"`
	CaptureSequenceCutoff uint64                 `json:"captureSequenceCutoff,omitempty"`
	CaptureWatermark      time.Time              `json:"captureWatermark,omitempty"`
	SettleUntil           time.Time              `json:"settleUntil,omitempty"`
	Interpretation        TemporalInterpretation `json:"interpretation"`
	InterpretationNote    string                 `json:"interpretationNote"`
}

func (query TemporalQuery) Validate() error {
	if query.StartUTC.IsZero() || query.EndUTC.IsZero() || !query.StartUTC.Before(query.EndUTC) || strings.TrimSpace(query.Timezone) == "" ||
		strings.TrimSpace(query.InterpretationNote) == "" || !validTemporalInterpretation(query.Interpretation) {
		return ErrTemporalQueryInvalid
	}
	if query.StartUTC.Location() != time.UTC || query.EndUTC.Location() != time.UTC {
		return ErrTemporalQueryInvalid
	}
	if _, err := time.LoadLocation(query.Timezone); err != nil {
		return ErrTemporalQueryInvalid
	}
	if query.Interpretation == TemporalBeforeAdmission {
		if strings.TrimSpace(query.RoomID) == "" || strings.TrimSpace(query.SittingID) == "" || strings.TrimSpace(query.AdmissionAnchorID) == "" ||
			query.SettleUntil.IsZero() || (!query.CaptureWatermark.IsZero() && query.SettleUntil.Before(query.CaptureWatermark)) ||
			(!query.CaptureWatermark.IsZero() && query.CaptureWatermark.Location() != time.UTC) || query.SettleUntil.Location() != time.UTC {
			return ErrTemporalQueryInvalid
		}
	} else if query.AdmissionAnchorID != "" || query.CaptureSequenceCutoff != 0 || !query.CaptureWatermark.IsZero() || !query.SettleUntil.IsZero() {
		return ErrTemporalQueryInvalid
	}
	return nil
}

func NewBeforeAdmissionTemporalQuery(anchor AdmissionAnchor, sittingStart time.Time, timezone string, settleDelay time.Duration, note string) (TemporalQuery, error) {
	anchor = normalizeAdmissionAnchor(anchor)
	if err := validateAdmissionAnchor(anchor); err != nil || sittingStart.IsZero() || settleDelay < 0 {
		return TemporalQuery{}, ErrTemporalQueryInvalid
	}
	query := TemporalQuery{
		StartUTC: sittingStart.UTC(), EndUTC: anchor.AdmittedAt.UTC(), Timezone: strings.TrimSpace(timezone),
		RoomID: anchor.RoomID, SittingID: anchor.SittingID, AdmissionAnchorID: anchor.AnchorID,
		CaptureSequenceCutoff: anchor.CaptureSequenceCutoff, CaptureWatermark: anchor.CaptureWatermark,
		SettleUntil: anchor.AdmittedAt.UTC().Add(settleDelay), Interpretation: TemporalBeforeAdmission,
		InterpretationNote: strings.TrimSpace(note),
	}
	if err := query.Validate(); err != nil {
		return TemporalQuery{}, err
	}
	return query, nil
}

func NewBoundedTemporalQuery(interpretation TemporalInterpretation, start, end time.Time, timezone, roomID, sittingID, note string) (TemporalQuery, error) {
	if interpretation != TemporalExplicitRange && interpretation != TemporalFirstMinutes && interpretation != TemporalLastMinutes {
		return TemporalQuery{}, ErrTemporalQueryInvalid
	}
	query := TemporalQuery{StartUTC: start.UTC(), EndUTC: end.UTC(), Timezone: strings.TrimSpace(timezone), RoomID: strings.TrimSpace(roomID),
		SittingID: strings.TrimSpace(sittingID), Interpretation: interpretation, InterpretationNote: strings.TrimSpace(note)}
	if err := query.Validate(); err != nil {
		return TemporalQuery{}, err
	}
	return query, nil
}

func NewFirstMinutesTemporalQuery(sittingStart, sittingEnd time.Time, minutes int, timezone, roomID, sittingID, note string) (TemporalQuery, error) {
	if minutes <= 0 || sittingStart.IsZero() || sittingEnd.IsZero() || !sittingStart.Before(sittingEnd) {
		return TemporalQuery{}, ErrTemporalQueryInvalid
	}
	end := sittingStart.Add(time.Duration(minutes) * time.Minute)
	if end.After(sittingEnd) {
		end = sittingEnd
	}
	return NewBoundedTemporalQuery(TemporalFirstMinutes, sittingStart, end, timezone, roomID, sittingID, note)
}

func NewLastMinutesTemporalQuery(sittingStart, sittingEnd time.Time, minutes int, timezone, roomID, sittingID, note string) (TemporalQuery, error) {
	if minutes <= 0 || sittingStart.IsZero() || sittingEnd.IsZero() || !sittingStart.Before(sittingEnd) {
		return TemporalQuery{}, ErrTemporalQueryInvalid
	}
	start := sittingEnd.Add(-time.Duration(minutes) * time.Minute)
	if start.Before(sittingStart) {
		start = sittingStart
	}
	return NewBoundedTemporalQuery(TemporalLastMinutes, start, sittingEnd, timezone, roomID, sittingID, note)
}

func validTemporalInterpretation(value TemporalInterpretation) bool {
	switch value {
	case TemporalExplicitRange, TemporalFirstMinutes, TemporalLastMinutes, TemporalBeforeAdmission:
		return true
	default:
		return false
	}
}

type CapturedTemporalSegment struct {
	OccurredStart   time.Time `json:"occurredStart"`
	OccurredEnd     time.Time `json:"occurredEnd"`
	CaptureSequence uint64    `json:"captureSequence"`
	CapturedAt      time.Time `json:"capturedAt"`
}

type TemporalSegmentDecision struct {
	Include      bool      `json:"include"`
	ClippedStart time.Time `json:"clippedStart,omitempty"`
	ClippedEnd   time.Time `json:"clippedEnd,omitempty"`
	Clipped      bool      `json:"clipped"`
	LateArrival  bool      `json:"lateArrival"`
}

func (query TemporalQuery) DecideSegment(segment CapturedTemporalSegment) TemporalSegmentDecision {
	if query.Validate() != nil || segment.OccurredStart.IsZero() || segment.OccurredEnd.IsZero() || !segment.OccurredStart.Before(segment.OccurredEnd) ||
		segment.CapturedAt.IsZero() || !segment.OccurredStart.Before(query.EndUTC) || !query.StartUTC.Before(segment.OccurredEnd) {
		return TemporalSegmentDecision{}
	}
	if query.Interpretation == TemporalBeforeAdmission && (segment.CaptureSequence == 0 || segment.CaptureSequence > query.CaptureSequenceCutoff) {
		return TemporalSegmentDecision{}
	}
	start, end := segment.OccurredStart, segment.OccurredEnd
	if start.Before(query.StartUTC) {
		start = query.StartUTC
	}
	if end.After(query.EndUTC) {
		end = query.EndUTC
	}
	return TemporalSegmentDecision{
		Include: true, ClippedStart: start, ClippedEnd: end,
		Clipped:     start.After(segment.OccurredStart) || end.Before(segment.OccurredEnd),
		LateArrival: query.Interpretation == TemporalBeforeAdmission && segment.CapturedAt.After(query.CaptureWatermark),
	}
}

func (query TemporalQuery) Settled(at time.Time) bool {
	if query.Interpretation != TemporalBeforeAdmission {
		return true
	}
	return !query.SettleUntil.IsZero() && !at.Before(query.SettleUntil)
}
