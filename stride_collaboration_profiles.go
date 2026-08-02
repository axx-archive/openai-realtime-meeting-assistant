package main

// Deterministic collaboration-preference reduction. The reducer is pure and
// provider-free: it accepts immutable observations, enforces privacy/sensitivity
// policy, and can rebuild the same profile from the same event set in any order.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrSTRIDECollaborationPreferenceDenied = errors.New("collaboration preference is not permitted")

const (
	stridePreferenceObserve  = "observe"
	stridePreferenceCorrect  = "correct"
	stridePreferenceForget   = "forget"
	stridePreferenceExplicit = "explicit"
	stridePreferenceInferred = "inferred"
	stridePreferencePrivate  = "private"
	stridePreferenceShared   = "shared"
)

type STRIDECollaborationPreferenceEvent struct {
	EventID          string
	Action           string
	SubjectPrincipal string
	Scope            string
	ScopeID          string
	PreferenceType   string
	Value            string
	Origin           string
	Evidence         []STRIDEReference
	Confidence       float64
	ObservedAt       time.Time
	ExpiresAt        time.Time
	Audience         STRIDEAudience
	CorrectsEventID  string
}

type STRIDECollaborationPreferenceState struct {
	Key                string
	SubjectPrincipal   string
	Scope              string
	ScopeID            string
	PreferenceType     string
	Value              string
	ValueDigest        string
	Origin             string
	Evidence           []STRIDEReference
	Audience           STRIDEAudience
	Confidence         float64
	FirstObserved      time.Time
	LastObserved       time.Time
	ExpiresAt          time.Time
	ReinforcementCount int
	Status             string
	SourceEventID      string
	CorrectionEventIDs []string
}

type STRIDECollaborationProfile struct {
	SubjectPrincipal string
	AsOf             time.Time
	Preferences      []STRIDECollaborationPreferenceState
	RebuildDigest    string
}

func ReduceSTRIDECollaborationProfile(subject string, events []STRIDECollaborationPreferenceEvent, asOf time.Time) (STRIDECollaborationProfile, error) {
	subject = normalizeAccountEmail(subject)
	if subject == "" || asOf.IsZero() {
		return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
	}
	asOf = asOf.UTC()
	ordered := append([]STRIDECollaborationPreferenceEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].ObservedAt.UTC(), ordered[j].ObservedAt.UTC()
		if !left.Equal(right) {
			return left.Before(right)
		}
		return ordered[i].EventID < ordered[j].EventID
	})
	states := map[string]STRIDECollaborationPreferenceState{}
	seenEventIDs := map[string]struct{}{}
	for _, event := range ordered {
		if event.ObservedAt.After(asOf) {
			continue
		}
		normalized, err := normalizeSTRIDECollaborationPreferenceEvent(event, subject)
		if err != nil {
			return STRIDECollaborationProfile{}, err
		}
		if _, duplicate := seenEventIDs[normalized.EventID]; duplicate {
			return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
		}
		seenEventIDs[normalized.EventID] = struct{}{}
		key := strideCollaborationPreferenceKey(normalized)
		current, exists := states[key]
		switch normalized.Action {
		case stridePreferenceObserve:
			if exists && current.Status == "forgotten" && normalized.Origin != stridePreferenceExplicit {
				return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
			}
			if exists && current.Status == "active" && !sameAudience(current.Audience, normalized.Audience) {
				// A preference reinforcement cannot silently widen or reinterpret
				// its ACL. The subject must create a separately keyed explicit
				// shared/private record through the control surface.
				return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
			}
			if !exists || current.Status == "forgotten" || current.Status == "expired" || current.ValueDigest != strideCollaborationValueDigest(normalized.Value) {
				states[key] = strideCollaborationStateFromEvent(normalized, key)
				continue
			}
			current.LastObserved = normalized.ObservedAt
			current.ExpiresAt = normalized.ExpiresAt
			if normalized.Confidence > current.Confidence {
				current.Confidence = normalized.Confidence
			}
			current.ReinforcementCount++
			current.Evidence = mergeSTRIDEReferences(current.Evidence, normalized.Evidence)
			current.SourceEventID = normalized.EventID
			states[key] = current
		case stridePreferenceCorrect:
			if !exists || current.Status != "active" || normalized.Origin != stridePreferenceExplicit || strings.TrimSpace(normalized.CorrectsEventID) == "" || normalized.CorrectsEventID != current.SourceEventID {
				return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
			}
			replacement := strideCollaborationStateFromEvent(normalized, key)
			replacement.FirstObserved = current.FirstObserved
			replacement.CorrectionEventIDs = append(append([]string(nil), current.CorrectionEventIDs...), normalized.EventID)
			states[key] = replacement
		case stridePreferenceForget:
			if !exists || normalized.Origin != stridePreferenceExplicit || strings.TrimSpace(normalized.Value) != "" {
				return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
			}
			// Forgetting retains only the non-sensitive key and audit event ID. The
			// learned value, digest, evidence, confidence, and correction history are
			// removed so a rebuild cannot accidentally re-project forgotten content.
			states[key] = STRIDECollaborationPreferenceState{
				Key: key, SubjectPrincipal: subject, Scope: normalized.Scope, ScopeID: normalized.ScopeID,
				PreferenceType: normalized.PreferenceType, Status: "forgotten", SourceEventID: normalized.EventID,
				FirstObserved: current.FirstObserved, LastObserved: normalized.ObservedAt, ExpiresAt: normalized.ExpiresAt,
			}
		default:
			return STRIDECollaborationProfile{}, ErrSTRIDECollaborationPreferenceDenied
		}
	}
	preferences := make([]STRIDECollaborationPreferenceState, 0, len(states))
	for key, state := range states {
		if state.Status == "active" && !state.ExpiresAt.After(asOf) {
			state.Status = "expired"
			state.Value = ""
			state.ValueDigest = ""
			state.Evidence = nil
			state.Audience = STRIDEAudience{}
			state.Confidence = 0
			states[key] = state
		}
		preferences = append(preferences, state)
	}
	sort.Slice(preferences, func(i, j int) bool { return preferences[i].Key < preferences[j].Key })
	profile := STRIDECollaborationProfile{SubjectPrincipal: subject, AsOf: asOf, Preferences: preferences}
	profile.RebuildDigest = strideCollaborationProfileDigest(profile)
	return profile, nil
}

func normalizeSTRIDECollaborationPreferenceEvent(event STRIDECollaborationPreferenceEvent, subject string) (STRIDECollaborationPreferenceEvent, error) {
	event.EventID = strings.TrimSpace(event.EventID)
	event.Action = strings.ToLower(strings.TrimSpace(event.Action))
	event.SubjectPrincipal = normalizeAccountEmail(event.SubjectPrincipal)
	event.Scope = strings.ToLower(strings.TrimSpace(event.Scope))
	event.ScopeID = strings.TrimSpace(event.ScopeID)
	event.PreferenceType = strings.ToLower(strings.TrimSpace(event.PreferenceType))
	event.Value = strings.TrimSpace(event.Value)
	event.Origin = strings.ToLower(strings.TrimSpace(event.Origin))
	event.ObservedAt = event.ObservedAt.UTC()
	event.ExpiresAt = event.ExpiresAt.UTC()
	if !strideIdentifier(event.EventID) || event.SubjectPrincipal != subject || !strideIdentifier(event.ScopeID) ||
		!oneOf(event.Action, stridePreferenceObserve, stridePreferenceCorrect, stridePreferenceForget) ||
		!oneOf(event.Scope, stridePreferencePrivate, stridePreferenceShared) ||
		!oneOf(event.Origin, stridePreferenceExplicit, stridePreferenceInferred) ||
		!safeSTRIDECollaborationPreferenceType(event.PreferenceType) || event.ObservedAt.IsZero() ||
		!event.ExpiresAt.After(event.ObservedAt) || event.ExpiresAt.Sub(event.ObservedAt) > 365*24*time.Hour ||
		event.Audience.Validate() != nil || len(event.Evidence) == 0 || !validUniqueSTRIDEReferences(event.Evidence) {
		return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
	}
	if event.Action != stridePreferenceForget && (event.Value == "" || len(event.Value) > 500) {
		return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
	}
	if event.Action == stridePreferenceForget && event.Origin != stridePreferenceExplicit {
		return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
	}
	if event.Origin == stridePreferenceExplicit {
		if event.Confidence < 0.95 || event.Confidence > 1 {
			return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
		}
	} else {
		if event.Action != stridePreferenceObserve || event.Scope != stridePreferencePrivate || event.Confidence < 0.65 || event.Confidence > 0.9 || event.ExpiresAt.Sub(event.ObservedAt) > 90*24*time.Hour {
			return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
		}
	}
	if event.Scope == stridePreferencePrivate {
		if event.ScopeID != subject || event.Audience.Visibility != "private" || len(event.Audience.Principals) != 1 || normalizeAccountEmail(event.Audience.Principals[0]) != subject {
			return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
		}
	} else {
		// ScopeID is the channel/project boundary, not an audience principal.
		// Keeping it separate prevents two organization-public channels with the
		// same member ACL from accidentally sharing a learned preference.
		if event.Origin != stridePreferenceExplicit || event.ScopeID == subject || event.Audience.Visibility == "private" || !containsSTRIDEPrincipal(event.Audience.Principals, subject) {
			return STRIDECollaborationPreferenceEvent{}, ErrSTRIDECollaborationPreferenceDenied
		}
	}
	event.Evidence = SortedSTRIDEReferences(event.Evidence)
	event.Audience.Principals = sortedUniqueSTRIDEIDs(event.Audience.Principals)
	return event, nil
}

func safeSTRIDECollaborationPreferenceType(value string) bool {
	if sensitivePreference(value) {
		return false
	}
	switch value {
	case "communication_format", "response_length", "meeting_pace", "feedback_style", "notification_timing", "decision_detail", "collaboration_channel", "working_hours", "name_pronunciation":
		return true
	default:
		return false
	}
}

func containsSTRIDEPrincipal(values []string, want string) bool {
	wantEmail := normalizeAccountEmail(want)
	for _, value := range values {
		if value == want || wantEmail != "" && normalizeAccountEmail(value) == wantEmail {
			return true
		}
	}
	return false
}

func validUniqueSTRIDEReferences(values []STRIDEReference) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value.Validate() != nil {
			return false
		}
		key := string(value.ContractType) + "\x00" + value.ID + "\x00" + fmt.Sprint(value.Revision)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return len(seen) > 0
}

func strideCollaborationPreferenceKey(event STRIDECollaborationPreferenceEvent) string {
	return strings.Join([]string{event.SubjectPrincipal, event.Scope, event.ScopeID, event.PreferenceType}, "|")
}

func strideCollaborationValueDigest(value string) string {
	digest := sha256.Sum256([]byte("stride-collaboration-value/v1\x00" + strings.TrimSpace(value)))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func strideCollaborationStateFromEvent(event STRIDECollaborationPreferenceEvent, key string) STRIDECollaborationPreferenceState {
	return STRIDECollaborationPreferenceState{
		Key: key, SubjectPrincipal: event.SubjectPrincipal, Scope: event.Scope, ScopeID: event.ScopeID,
		PreferenceType: event.PreferenceType, Value: event.Value, ValueDigest: strideCollaborationValueDigest(event.Value),
		Origin: event.Origin, Evidence: append([]STRIDEReference(nil), event.Evidence...), Audience: event.Audience, Confidence: event.Confidence,
		FirstObserved: event.ObservedAt, LastObserved: event.ObservedAt, ExpiresAt: event.ExpiresAt,
		ReinforcementCount: 1, Status: "active", SourceEventID: event.EventID,
	}
}

func mergeSTRIDEReferences(left, right []STRIDEReference) []STRIDEReference {
	combined := append(append([]STRIDEReference(nil), left...), right...)
	sort.Slice(combined, func(i, j int) bool {
		if combined[i].ContractType != combined[j].ContractType {
			return combined[i].ContractType < combined[j].ContractType
		}
		if combined[i].ID != combined[j].ID {
			return combined[i].ID < combined[j].ID
		}
		return combined[i].Revision < combined[j].Revision
	})
	result := combined[:0]
	for _, value := range combined {
		if len(result) > 0 && result[len(result)-1].ContractType == value.ContractType && result[len(result)-1].ID == value.ID && result[len(result)-1].Revision == value.Revision && result[len(result)-1].Digest == value.Digest {
			continue
		}
		result = append(result, value)
	}
	return result
}

func strideCollaborationProfileDigest(profile STRIDECollaborationProfile) string {
	parts := []string{"stride-collaboration-profile/v1", profile.SubjectPrincipal, profile.AsOf.UTC().Format(time.RFC3339Nano)}
	for _, preference := range profile.Preferences {
		parts = append(parts, strings.Join([]string{
			preference.Key, preference.ValueDigest, preference.Origin, fmt.Sprintf("%.6f", preference.Confidence),
			preference.FirstObserved.UTC().Format(time.RFC3339Nano), preference.LastObserved.UTC().Format(time.RFC3339Nano),
			preference.ExpiresAt.UTC().Format(time.RFC3339Nano), fmt.Sprint(preference.ReinforcementCount), preference.Status,
			preference.SourceEventID, strings.Join(preference.CorrectionEventIDs, ","),
		}, "\x00"))
		parts = append(parts, preference.Audience.Visibility, strings.Join(preference.Audience.Principals, ","))
		for _, evidence := range preference.Evidence {
			parts = append(parts, string(evidence.ContractType), evidence.ID, fmt.Sprint(evidence.Revision), evidence.Digest)
		}
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return fmt.Sprintf("sha256:%x", digest[:])
}
