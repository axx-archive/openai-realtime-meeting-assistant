package main

// Durable collaboration-memory authority. The reducer in
// stride_collaboration_profiles.go remains the sole semantic state machine;
// this store adds explicit human consent, atomic persistence, ACL-scoped
// projection, and irreversible value removal at the forget/revoke boundary.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	strideCollaborationStoreFormat   = 1
	strideCollaborationStoreMaxBytes = 8 << 20
)

var (
	ErrSTRIDECollaborationStoreDisabled = errors.New("STRIDE collaboration memory is disabled")
	ErrSTRIDECollaborationStoreConflict = errors.New("STRIDE collaboration memory revision conflict")
	errSTRIDECollaborationNoReconcile   = errors.New("STRIDE collaboration memory reconciliation made no change")
)

type STRIDECollaborationMemoryConsent struct {
	SubjectPrincipal string    `json:"subjectPrincipal"`
	Revision         int64     `json:"revision"`
	Enabled          bool      `json:"enabled"`
	AllowInferred    bool      `json:"allowInferred"`
	AllowShared      bool      `json:"allowShared"`
	UpdatedAt        time.Time `json:"updatedAt"`
	UpdatedBy        string    `json:"updatedBy"`
}

func (consent STRIDECollaborationMemoryConsent) validate(subject string) error {
	if !strideIdentifier(subject) || consent.SubjectPrincipal != subject || consent.Revision < 1 || consent.UpdatedAt.IsZero() || consent.UpdatedBy != subject {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	if !consent.Enabled && (consent.AllowInferred || consent.AllowShared) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return nil
}

type STRIDECollaborationControlReceipt struct {
	Action            string    `json:"action"`
	Actor             string    `json:"actor"`
	RelationshipID    string    `json:"relationshipId,omitempty"`
	PreferenceType    string    `json:"preferenceType,omitempty"`
	EvidenceID        string    `json:"evidenceId,omitempty"`
	ResultingRevision int64     `json:"resultingRevision"`
	OccurredAt        time.Time `json:"occurredAt"`
}

func (receipt STRIDECollaborationControlReceipt) validate(subject string) error {
	validActor := receipt.Actor == subject
	if receipt.Action == "source_retract" {
		validActor = receipt.Actor == "system:source_authority"
	}
	if !oneOf(receipt.Action, "enable", "disable", "remember", "correct", "forget", "source_retract") || !validActor || receipt.ResultingRevision < 1 || receipt.OccurredAt.IsZero() ||
		(receipt.RelationshipID != "" && !strideIdentifier(receipt.RelationshipID)) || (receipt.PreferenceType != "" && !safeSTRIDECollaborationPreferenceType(receipt.PreferenceType)) ||
		(receipt.EvidenceID != "" && !strideIdentifier(receipt.EvidenceID)) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return nil
}

type durableSTRIDECollaborationSubject struct {
	Consent         *STRIDECollaborationMemoryConsent    `json:"consent,omitempty"`
	Revision        int64                                `json:"revision"`
	Events          []STRIDECollaborationPreferenceEvent `json:"events,omitempty"`
	ControlEvidence []STRIDECollaborationControlEvidence `json:"controlEvidence,omitempty"`
	Forgotten       map[string]time.Time                 `json:"forgotten,omitempty"`
	Controls        []STRIDECollaborationControlReceipt  `json:"controls,omitempty"`
}

type durableSTRIDECollaborationState struct {
	Format   int                                          `json:"format"`
	Subjects map[string]durableSTRIDECollaborationSubject `json:"subjects"`
}

type durableSTRIDECollaborationStore struct {
	mu        sync.Mutex
	path      string
	enabled   bool
	authority STRIDESnapshotMACAuthority
	subjects  map[string]durableSTRIDECollaborationSubject
	write     func(string, []byte) error
}

type STRIDECollaborationContextPreference struct {
	Reference          STRIDEReference         `json:"reference"`
	Relationship       AgentRelationshipMemory `json:"relationship"`
	PreferenceType     string                  `json:"preferenceType"`
	Value              string                  `json:"value"`
	Scope              string                  `json:"scope"`
	Origin             string                  `json:"origin"`
	SourceEventID      string                  `json:"sourceEventId"`
	Evidence           []STRIDEReference       `json:"evidence"`
	Confidence         float64                 `json:"confidence"`
	ExpiresAt          time.Time               `json:"expiresAt"`
	ConsentRevision    int64                   `json:"consentRevision"`
	ProjectionRevision int64                   `json:"projectionRevision"`
}

func (projection STRIDECollaborationContextPreference) validate() error {
	if projection.Reference.Validate() != nil || projection.Reference.ContractType != STRIDEContractAgentRelationshipMemory || projection.Relationship.Validate() != nil ||
		projection.Reference != referenceFromHeader(projection.Relationship.Header) || !safeSTRIDECollaborationPreferenceType(projection.PreferenceType) || strings.TrimSpace(projection.Value) == "" || !oneOf(projection.Scope, stridePreferencePrivate, stridePreferenceShared) ||
		!oneOf(projection.Origin, stridePreferenceExplicit, stridePreferenceInferred) || !strideIdentifier(projection.SourceEventID) || !validUniqueSTRIDEReferences(projection.Evidence) ||
		projection.Confidence < 0 || projection.Confidence > 1 || projection.ExpiresAt.IsZero() || projection.ConsentRevision < 1 || projection.ProjectionRevision < 1 {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return nil
}

func newDurableSTRIDECollaborationStore(path string, enabled bool, authorities ...STRIDESnapshotMACAuthority) (*durableSTRIDECollaborationStore, error) {
	var authority STRIDESnapshotMACAuthority
	if len(authorities) > 0 {
		authority = authorities[0]
	}
	store := &durableSTRIDECollaborationStore{
		path: path, enabled: enabled, authority: authority, subjects: map[string]durableSTRIDECollaborationSubject{},
		write: func(path string, raw []byte) error { return writeFileAtomicallyDurable(path, raw, 0o600) },
	}
	if !enabled {
		return store, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > strideCollaborationStoreMaxBytes {
		return nil, ErrSTRIDECollaborationPreferenceDenied
	}
	raw, err := io.ReadAll(io.LimitReader(file, strideCollaborationStoreMaxBytes+1))
	if err != nil || len(raw) > strideCollaborationStoreMaxBytes {
		return nil, ErrSTRIDECollaborationPreferenceDenied
	}
	var state durableSTRIDECollaborationState
	if err := strictJSONBytes(raw, &state); err != nil || state.Format != strideCollaborationStoreFormat || state.Subjects == nil {
		return nil, ErrSTRIDECollaborationPreferenceDenied
	}
	for subject, value := range state.Subjects {
		if err := validateDurableSTRIDECollaborationSubject(subject, value, authority); err != nil {
			return nil, err
		}
	}
	store.subjects = state.Subjects
	return store, nil
}

func strictJSONBytes(raw []byte, target any) error {
	decoder := jsonNewStrictDecoder(raw)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

// jsonNewStrictDecoder is kept as a small seam to make the durable format use
// the same unknown-field rejection as HTTP and signed snapshots.
func jsonNewStrictDecoder(raw []byte) *json.Decoder {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder
}

func validateDurableSTRIDECollaborationSubject(subject string, state durableSTRIDECollaborationSubject, authorities ...STRIDESnapshotMACAuthority) error {
	var authority STRIDESnapshotMACAuthority
	if len(authorities) > 0 {
		authority = authorities[0]
	}
	if !strideIdentifier(subject) || state.Revision < 0 {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	if state.Consent == nil {
		if state.Revision != 0 || len(state.Events) > 0 || len(state.ControlEvidence) > 0 || len(state.Forgotten) > 0 || len(state.Controls) > 0 {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		return nil
	}
	if err := state.Consent.validate(subject); err != nil || state.Consent.Revision != state.Revision || !state.Consent.Enabled && (len(state.Events) > 0 || len(state.ControlEvidence) > 0 || len(state.Forgotten) > 0) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	for key, forgottenAt := range state.Forgotten {
		if !strings.HasPrefix(key, subject+"|") || forgottenAt.IsZero() {
			return ErrSTRIDECollaborationPreferenceDenied
		}
	}
	var priorControlRevision int64
	controlEvidence := make(map[string]STRIDECollaborationControlEvidence, len(state.ControlEvidence))
	for _, evidence := range state.ControlEvidence {
		id := evidence.Event.Header.ID
		if _, duplicate := controlEvidence[id]; duplicate || !verifySTRIDECollaborationControlEvidence(authority, evidence) {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		controlEvidence[id] = evidence
	}
	for _, receipt := range state.Controls {
		if err := receipt.validate(subject); err != nil || receipt.ResultingRevision <= priorControlRevision || receipt.ResultingRevision > state.Revision {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		if receipt.EvidenceID != "" {
			evidence, ok := controlEvidence[receipt.EvidenceID]
			if !ok || evidence.Action != receipt.Action || evidence.Actor != receipt.Actor || evidence.PreferenceType != receipt.PreferenceType ||
				evidence.Event.Header.Revision != receipt.ResultingRevision || evidence.RelationshipID != receipt.RelationshipID && receipt.Action == "correct" {
				return ErrSTRIDECollaborationPreferenceDenied
			}
		}
		priorControlRevision = receipt.ResultingRevision
	}
	if len(state.Controls) == 0 || priorControlRevision != state.Revision {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	if len(state.Events) == 0 && len(controlEvidence) > 0 {
		// Signed evidence is sensitive provenance, not a standalone audit log.
		// It must always remain attached to exactly one live preference event.
		return ErrSTRIDECollaborationPreferenceDenied
	}
	if len(state.Events) == 0 {
		return nil
	}
	asOf := time.Unix(1<<62, 0).UTC()
	profile, err := ReduceSTRIDECollaborationProfile(subject, state.Events, asOf)
	if err != nil {
		return err
	}
	for _, preference := range profile.Preferences {
		if _, forgotten := state.Forgotten[preference.Key]; forgotten {
			return ErrSTRIDECollaborationPreferenceDenied
		}
	}
	for _, evidence := range controlEvidence {
		uses := 0
		receipts := 0
		for _, receipt := range state.Controls {
			if receipt.EvidenceID == evidence.Event.Header.ID {
				receipts++
			}
		}
		for _, event := range state.Events {
			for _, reference := range event.Evidence {
				if reference == evidence.Reference() {
					uses++
					expectedAction := stridePreferenceObserve
					if evidence.Action == "correct" {
						expectedAction = stridePreferenceCorrect
					}
					if event.Action != expectedAction || event.SubjectPrincipal != evidence.Actor || event.Scope != evidence.Scope ||
						event.PreferenceType != evidence.PreferenceType || temporalDigest(strings.TrimSpace(event.Value)) != evidence.ValueDigest ||
						len(event.Evidence) != 1 || !sameAudience(event.Audience, evidence.Event.Audience) || evidence.Action == "correct" && strideCollaborationRelationshipID(strideCollaborationPreferenceKey(event)) != evidence.RelationshipID {
						return ErrSTRIDECollaborationPreferenceDenied
					}
					break
				}
			}
		}
		if uses != 1 || receipts != 1 {
			return ErrSTRIDECollaborationPreferenceDenied
		}
	}
	return nil
}

func (store *durableSTRIDECollaborationStore) persistLocked() error {
	state := durableSTRIDECollaborationState{Format: strideCollaborationStoreFormat, Subjects: store.subjects}
	raw, err := canonicalJSON(state)
	if err != nil {
		return err
	}
	return store.write(store.path, append(raw, '\n'))
}

func (store *durableSTRIDECollaborationStore) requireEnabled() error {
	if store == nil {
		return ErrSTRIDECollaborationStoreDisabled
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.requireEnabledLocked()
}

func (store *durableSTRIDECollaborationStore) requireEnabledLocked() error {
	if !store.enabled {
		return ErrSTRIDECollaborationStoreDisabled
	}
	return nil
}

func cloneDurableSTRIDECollaborationSubject(value durableSTRIDECollaborationSubject) durableSTRIDECollaborationSubject {
	clone := value
	if value.Consent != nil {
		consent := *value.Consent
		clone.Consent = &consent
	}
	clone.Events = append([]STRIDECollaborationPreferenceEvent(nil), value.Events...)
	for index := range clone.Events {
		clone.Events[index].Evidence = append([]STRIDEReference(nil), clone.Events[index].Evidence...)
		clone.Events[index].Audience.Principals = append([]string(nil), clone.Events[index].Audience.Principals...)
	}
	clone.ControlEvidence = append([]STRIDECollaborationControlEvidence(nil), value.ControlEvidence...)
	clone.Forgotten = make(map[string]time.Time, len(value.Forgotten))
	for key, at := range value.Forgotten {
		clone.Forgotten[key] = at
	}
	clone.Controls = append([]STRIDECollaborationControlReceipt(nil), value.Controls...)
	return clone
}

func (store *durableSTRIDECollaborationStore) mutateSubject(subject string, expectedRevision int64, mutate func(*durableSTRIDECollaborationSubject) error) error {
	if store == nil {
		return ErrSTRIDECollaborationStoreDisabled
	}
	if !strideIdentifier(subject) || mutate == nil {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.requireEnabledLocked(); err != nil {
		return err
	}
	prior := cloneDurableSTRIDECollaborationSubject(store.subjects[subject])
	if prior.Revision != expectedRevision {
		return ErrSTRIDECollaborationStoreConflict
	}
	next := cloneDurableSTRIDECollaborationSubject(prior)
	if next.Forgotten == nil {
		next.Forgotten = map[string]time.Time{}
	}
	if err := mutate(&next); err != nil {
		return err
	}
	next.Revision++
	if len(next.Controls) > 0 {
		next.Controls[len(next.Controls)-1].ResultingRevision = next.Revision
	}
	if next.Consent != nil {
		next.Consent.Revision = next.Revision
	}
	if err := validateDurableSTRIDECollaborationSubject(subject, next, store.authority); err != nil {
		return err
	}
	store.subjects[subject] = next
	if err := store.persistLocked(); err != nil {
		if errors.Is(err, ErrDurableReplaceAmbiguous) {
			// The rename may already be visible. Poison this process-local
			// authority instead of rolling back to a state that could diverge
			// from disk; restart/reload establishes the single visible revision.
			store.enabled = false
			return err
		}
		if prior.Revision == 0 && prior.Consent == nil && len(prior.Events) == 0 && len(prior.Controls) == 0 {
			delete(store.subjects, subject)
		} else {
			store.subjects[subject] = prior
		}
		return err
	}
	return nil
}

func (store *durableSTRIDECollaborationStore) SetConsent(actor string, expectedRevision int64, enable, allowInferred, allowShared bool, at time.Time) error {
	actor = strings.TrimSpace(actor)
	if !strideIdentifier(actor) || at.IsZero() || (!enable && (allowInferred || allowShared)) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return store.mutateSubject(actor, expectedRevision, func(state *durableSTRIDECollaborationSubject) error {
		state.Consent = &STRIDECollaborationMemoryConsent{SubjectPrincipal: actor, Enabled: enable, AllowInferred: allowInferred, AllowShared: allowShared, UpdatedAt: at.UTC(), UpdatedBy: actor}
		action := "enable"
		if !enable {
			action = "disable"
			// Revocation is a purge, not a visibility bit. Learned values and
			// evidence disappear from the durable file before the call returns.
			state.Events = nil
			state.ControlEvidence = nil
			state.Forgotten = map[string]time.Time{}
			for index := range state.Controls {
				state.Controls[index].EvidenceID = ""
			}
		}
		state.Controls = append(state.Controls, STRIDECollaborationControlReceipt{Action: action, Actor: actor, OccurredAt: at.UTC()})
		return nil
	})
}

func (store *durableSTRIDECollaborationStore) Remember(actor string, expectedRevision int64, event STRIDECollaborationPreferenceEvent) error {
	return store.remember(actor, expectedRevision, event, nil)
}

func (store *durableSTRIDECollaborationStore) RememberFromControl(actor string, expectedRevision int64, event STRIDECollaborationPreferenceEvent, evidence STRIDECollaborationControlEvidence) error {
	return store.remember(actor, expectedRevision, event, &evidence)
}

func (store *durableSTRIDECollaborationStore) remember(actor string, expectedRevision int64, event STRIDECollaborationPreferenceEvent, control *STRIDECollaborationControlEvidence) error {
	actor = strings.TrimSpace(actor)
	if event.Action != stridePreferenceObserve || event.SubjectPrincipal != actor {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	if control != nil && (!verifySTRIDECollaborationControlEvidence(store.authority, *control) || control.Action != "remember" || control.Actor != actor || control.ExpectedRevision != expectedRevision || control.PreferenceType != event.PreferenceType || control.Scope != event.Scope || control.ValueDigest != temporalDigest(strings.TrimSpace(event.Value)) || len(event.Evidence) != 1 || event.Evidence[0] != control.Reference()) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return store.mutateSubject(actor, expectedRevision, func(state *durableSTRIDECollaborationSubject) error {
		if state.Consent == nil || !state.Consent.Enabled || event.Origin == stridePreferenceInferred && !state.Consent.AllowInferred || event.Scope == stridePreferenceShared && !state.Consent.AllowShared {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		event.EventID = strideCollaborationControlEventID(actor, state.Revision+1, "remember", event.PreferenceType, event.Value)
		originalTTL := event.ExpiresAt.Sub(event.ObservedAt)
		event.ObservedAt = nextSTRIDECollaborationEventTime(state.Events, event.ObservedAt)
		event.ExpiresAt = event.ObservedAt.Add(originalTTL)
		key := strideCollaborationPreferenceKey(event)
		if _, forgotten := state.Forgotten[key]; forgotten {
			if event.Origin != stridePreferenceExplicit {
				return ErrSTRIDECollaborationPreferenceDenied
			}
			delete(state.Forgotten, key)
		}
		candidate := append(append([]STRIDECollaborationPreferenceEvent(nil), state.Events...), event)
		if _, err := ReduceSTRIDECollaborationProfile(actor, candidate, event.ObservedAt.Add(time.Nanosecond)); err != nil {
			return err
		}
		state.Events = candidate
		evidenceID := ""
		if control != nil {
			state.ControlEvidence = append(state.ControlEvidence, *control)
			evidenceID = control.Event.Header.ID
		}
		state.Controls = append(state.Controls, STRIDECollaborationControlReceipt{Action: "remember", Actor: actor, RelationshipID: strideCollaborationRelationshipID(key), PreferenceType: event.PreferenceType, EvidenceID: evidenceID, OccurredAt: event.ObservedAt.UTC()})
		return nil
	})
}

func (store *durableSTRIDECollaborationStore) Correct(actor, relationshipID string, expectedRevision int64, value string, evidence []STRIDEReference, at time.Time) error {
	return store.correct(actor, relationshipID, expectedRevision, value, evidence, at, nil)
}

func (store *durableSTRIDECollaborationStore) CorrectFromControl(actor, relationshipID string, expectedRevision int64, value string, evidence STRIDECollaborationControlEvidence, at time.Time) error {
	return store.correct(actor, relationshipID, expectedRevision, value, []STRIDEReference{evidence.Reference()}, at, &evidence)
}

func (store *durableSTRIDECollaborationStore) correct(actor, relationshipID string, expectedRevision int64, value string, evidence []STRIDEReference, at time.Time, control *STRIDECollaborationControlEvidence) error {
	actor, relationshipID, value = strings.TrimSpace(actor), strings.TrimSpace(relationshipID), strings.TrimSpace(value)
	if !strideIdentifier(actor) || !strideIdentifier(relationshipID) || value == "" || len(value) > 500 || at.IsZero() || !validUniqueSTRIDEReferences(evidence) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	if control != nil && (!verifySTRIDECollaborationControlEvidence(store.authority, *control) || control.Action != "correct" || control.Actor != actor || control.RelationshipID != relationshipID || control.ExpectedRevision != expectedRevision || control.ValueDigest != temporalDigest(value) || len(evidence) != 1 || evidence[0] != control.Reference()) {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return store.mutateSubject(actor, expectedRevision, func(state *durableSTRIDECollaborationSubject) error {
		if state.Consent == nil || !state.Consent.Enabled {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		observedAt := nextSTRIDECollaborationEventTime(state.Events, at)
		profile, err := ReduceSTRIDECollaborationProfile(actor, state.Events, observedAt)
		if err != nil {
			return err
		}
		current, ok := strideCollaborationStateByRelationshipID(profile, relationshipID)
		if !ok || current.Status != "active" {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		if control != nil && (control.PreferenceType != current.PreferenceType || control.Scope != current.Scope) {
			return ErrSTRIDECollaborationPreferenceDenied
		}
		event := STRIDECollaborationPreferenceEvent{
			EventID: strideCollaborationControlEventID(actor, state.Revision+1, "correct", current.PreferenceType, value), Action: stridePreferenceCorrect,
			SubjectPrincipal: actor, Scope: current.Scope, ScopeID: current.ScopeID, PreferenceType: current.PreferenceType, Value: value,
			Origin: stridePreferenceExplicit, Evidence: append([]STRIDEReference(nil), evidence...), Confidence: 1, ObservedAt: observedAt, ExpiresAt: observedAt.Add(180 * 24 * time.Hour),
			Audience: currentAudienceForState(current), CorrectsEventID: current.SourceEventID,
		}
		candidate := append(append([]STRIDECollaborationPreferenceEvent(nil), state.Events...), event)
		if _, err := ReduceSTRIDECollaborationProfile(actor, candidate, observedAt.Add(time.Nanosecond)); err != nil {
			return err
		}
		state.Events = candidate
		evidenceID := ""
		if control != nil {
			state.ControlEvidence = append(state.ControlEvidence, *control)
			evidenceID = control.Event.Header.ID
		}
		state.Controls = append(state.Controls, STRIDECollaborationControlReceipt{Action: "correct", Actor: actor, RelationshipID: relationshipID, PreferenceType: current.PreferenceType, EvidenceID: evidenceID, OccurredAt: observedAt})
		return nil
	})
}

func (store *durableSTRIDECollaborationStore) Forget(actor, relationshipID string, expectedRevision int64, at time.Time) error {
	actor, relationshipID = strings.TrimSpace(actor), strings.TrimSpace(relationshipID)
	if !strideIdentifier(actor) || !strideIdentifier(relationshipID) || at.IsZero() {
		return ErrSTRIDECollaborationPreferenceDenied
	}
	return store.mutateSubject(actor, expectedRevision, func(state *durableSTRIDECollaborationSubject) error {
		observedAt := nextSTRIDECollaborationEventTime(state.Events, at)
		profile, err := ReduceSTRIDECollaborationProfile(actor, state.Events, observedAt)
		if err != nil {
			return err
		}
		current, ok := strideCollaborationStateByRelationshipID(profile, relationshipID)
		if !ok || current.Status != "active" {
			// Idempotent only after the caller has already observed the newer
			// revision; a stale revision remains a conflict at mutateSubject.
			return ErrSTRIDECollaborationPreferenceDenied
		}
		forget := STRIDECollaborationPreferenceEvent{
			EventID: strideCollaborationControlEventID(actor, state.Revision+1, "forget", current.PreferenceType, ""), Action: stridePreferenceForget,
			SubjectPrincipal: actor, Scope: current.Scope, ScopeID: current.ScopeID, PreferenceType: current.PreferenceType,
			Origin: stridePreferenceExplicit, Evidence: append([]STRIDEReference(nil), current.Evidence...), Confidence: 1, ObservedAt: observedAt, ExpiresAt: observedAt.Add(24 * time.Hour), Audience: currentAudienceForState(current),
		}
		candidate := append(append([]STRIDECollaborationPreferenceEvent(nil), state.Events...), forget)
		if _, err := ReduceSTRIDECollaborationProfile(actor, candidate, observedAt.Add(time.Nanosecond)); err != nil {
			return err
		}
		key := current.Key
		removedEvidence := map[string]struct{}{}
		kept := state.Events[:0]
		for _, event := range state.Events {
			if strideCollaborationPreferenceKey(event) != key {
				kept = append(kept, event)
				continue
			}
			for _, reference := range event.Evidence {
				removedEvidence[reference.ID] = struct{}{}
			}
		}
		state.Events = append([]STRIDECollaborationPreferenceEvent(nil), kept...)
		keptControlEvidence := state.ControlEvidence[:0]
		for _, evidence := range state.ControlEvidence {
			if _, remove := removedEvidence[evidence.Event.Header.ID]; !remove {
				keptControlEvidence = append(keptControlEvidence, evidence)
			}
		}
		state.ControlEvidence = append([]STRIDECollaborationControlEvidence(nil), keptControlEvidence...)
		for index := range state.Controls {
			if _, removed := removedEvidence[state.Controls[index].EvidenceID]; removed {
				state.Controls[index].EvidenceID = ""
			}
		}
		state.Forgotten[key] = observedAt
		state.Controls = append(state.Controls, STRIDECollaborationControlReceipt{Action: "forget", Actor: actor, RelationshipID: relationshipID, PreferenceType: current.PreferenceType, OccurredAt: observedAt})
		return nil
	})
}

// ReconcileSourceAuthority physically retracts relationship values whose
// source event is no longer in the caller's current authorized conversation
// projection. It also purges expired values instead of leaving their raw text
// dormant on disk. Settings controls remain valid because their MAC-verified
// evidence is subject-authored and is not delegated to chat retention.
func (store *durableSTRIDECollaborationStore) ReconcileSourceAuthority(subject string, live map[string]STRIDEReference, at time.Time) (bool, int64, error) {
	if store == nil {
		return false, 0, ErrSTRIDECollaborationStoreDisabled
	}
	if !strideIdentifier(subject) || at.IsZero() {
		return false, 0, ErrSTRIDECollaborationPreferenceDenied
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, revision, err := store.Consent(subject)
		if err != nil {
			return false, revision, err
		}
		err = store.mutateSubject(subject, revision, func(state *durableSTRIDECollaborationSubject) error {
			if state.Consent == nil || !state.Consent.Enabled || len(state.Events) == 0 {
				return errSTRIDECollaborationNoReconcile
			}
			profile, reduceErr := ReduceSTRIDECollaborationProfile(subject, state.Events, at.UTC())
			if reduceErr != nil {
				return reduceErr
			}
			keys := map[string]struct{}{}
			for _, preference := range profile.Preferences {
				if preference.Status == "expired" || preference.Status == "active" && !strideCollaborationEvidenceCurrentlyAuthorized(preference.Evidence, live) {
					keys[preference.Key] = struct{}{}
				}
			}
			if len(keys) == 0 {
				return errSTRIDECollaborationNoReconcile
			}
			removeSTRIDECollaborationPreferenceKeys(state, keys, at.UTC())
			state.Controls = append(state.Controls, STRIDECollaborationControlReceipt{Action: "source_retract", Actor: "system:source_authority", OccurredAt: at.UTC()})
			return nil
		})
		if errors.Is(err, errSTRIDECollaborationNoReconcile) {
			return false, revision, nil
		}
		if errors.Is(err, ErrSTRIDECollaborationStoreConflict) {
			continue
		}
		if err != nil {
			return false, revision, err
		}
		return true, revision + 1, nil
	}
	return false, 0, ErrSTRIDECollaborationStoreConflict
}

func strideCollaborationEvidenceCurrentlyAuthorized(evidence []STRIDEReference, live map[string]STRIDEReference) bool {
	if len(evidence) == 0 {
		return false
	}
	for _, reference := range evidence {
		if strings.HasPrefix(reference.ID, "relationship_control_") {
			continue
		}
		if current, ok := live[strideConversationReferenceKey(reference)]; !ok || current != reference {
			return false
		}
	}
	return true
}

func removeSTRIDECollaborationPreferenceKeys(state *durableSTRIDECollaborationSubject, keys map[string]struct{}, at time.Time) {
	removedEvidence := map[string]struct{}{}
	kept := state.Events[:0]
	for _, event := range state.Events {
		key := strideCollaborationPreferenceKey(event)
		if _, remove := keys[key]; !remove {
			kept = append(kept, event)
			continue
		}
		state.Forgotten[key] = at.UTC()
		for _, reference := range event.Evidence {
			removedEvidence[reference.ID] = struct{}{}
		}
	}
	state.Events = append([]STRIDECollaborationPreferenceEvent(nil), kept...)
	keptControlEvidence := state.ControlEvidence[:0]
	for _, evidence := range state.ControlEvidence {
		if _, remove := removedEvidence[evidence.Event.Header.ID]; !remove {
			keptControlEvidence = append(keptControlEvidence, evidence)
		}
	}
	state.ControlEvidence = append([]STRIDECollaborationControlEvidence(nil), keptControlEvidence...)
	for index := range state.Controls {
		if _, removed := removedEvidence[state.Controls[index].EvidenceID]; removed {
			state.Controls[index].EvidenceID = ""
		}
	}
}

func (store *durableSTRIDECollaborationStore) Consent(subject string) (STRIDECollaborationMemoryConsent, int64, error) {
	if store == nil {
		return STRIDECollaborationMemoryConsent{}, 0, ErrSTRIDECollaborationStoreDisabled
	}
	if !strideIdentifier(subject) {
		return STRIDECollaborationMemoryConsent{}, 0, ErrSTRIDECollaborationPreferenceDenied
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.requireEnabledLocked(); err != nil {
		return STRIDECollaborationMemoryConsent{}, 0, err
	}
	state := store.subjects[subject]
	if state.Consent == nil {
		return STRIDECollaborationMemoryConsent{}, state.Revision, nil
	}
	return *state.Consent, state.Revision, nil
}

func (store *durableSTRIDECollaborationStore) Inspect(actor string, at time.Time) ([]STRIDECollaborationContextPreference, int64, error) {
	if store == nil {
		return nil, 0, ErrSTRIDECollaborationStoreDisabled
	}
	if !strideIdentifier(actor) || at.IsZero() {
		return nil, 0, ErrSTRIDECollaborationPreferenceDenied
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.requireEnabledLocked(); err != nil {
		return nil, 0, err
	}
	state := cloneDurableSTRIDECollaborationSubject(store.subjects[actor])
	if state.Consent == nil || !state.Consent.Enabled {
		return nil, state.Revision, nil
	}
	privateAudience := STRIDEAudience{Visibility: "private", Principals: []string{actor}}
	return projectSTRIDECollaborationPreferences(actor, state, privateAudience, actor, true, at.UTC())
}

func (store *durableSTRIDECollaborationStore) ProjectForContext(subject string, audience STRIDEAudience, scopeID string, at time.Time) ([]STRIDECollaborationContextPreference, int64, error) {
	scopeID = strings.TrimSpace(scopeID)
	if store == nil {
		return nil, 0, ErrSTRIDECollaborationStoreDisabled
	}
	if !strideIdentifier(subject) || audience.Validate() != nil || !strideIdentifier(scopeID) || at.IsZero() || !containsSTRIDEID(audience.Principals, subject) {
		return nil, 0, ErrSTRIDECollaborationPreferenceDenied
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.requireEnabledLocked(); err != nil {
		return nil, 0, err
	}
	state := cloneDurableSTRIDECollaborationSubject(store.subjects[subject])
	if state.Consent == nil || !state.Consent.Enabled {
		return nil, state.Revision, nil
	}
	return projectSTRIDECollaborationPreferences(subject, state, audience, scopeID, false, at.UTC())
}

func (store *durableSTRIDECollaborationStore) AuthorizeContextReference(reference STRIDEReference, subject string, audience STRIDEAudience, scopeID string, at time.Time) bool {
	projections, _, err := store.ProjectForContext(subject, audience, scopeID, at)
	if err != nil {
		return false
	}
	for _, projection := range projections {
		if projection.Reference == reference {
			return true
		}
	}
	return false
}

func projectSTRIDECollaborationPreferences(subject string, state durableSTRIDECollaborationSubject, audience STRIDEAudience, scopeID string, inspect bool, at time.Time) ([]STRIDECollaborationContextPreference, int64, error) {
	// Control calls can share a deterministic clock in tests and in batch
	// clients. Mutations advance equal timestamps by one nanosecond to preserve
	// reducer order, so a current-state read includes that already-committed
	// logical time instead of briefly hiding the newest revision.
	asOf := at
	for _, event := range state.Events {
		if event.ObservedAt.After(asOf) {
			asOf = event.ObservedAt
		}
	}
	profile, err := ReduceSTRIDECollaborationProfile(subject, state.Events, asOf)
	if err != nil {
		return nil, state.Revision, err
	}
	result := make([]STRIDECollaborationContextPreference, 0, len(profile.Preferences))
	for _, preference := range profile.Preferences {
		if preference.Status != "active" || !preference.ExpiresAt.After(at) {
			continue
		}
		if !inspect {
			if preference.Origin == stridePreferenceInferred && !state.Consent.AllowInferred || preference.Scope == stridePreferenceShared && !state.Consent.AllowShared {
				continue
			}
			if preference.Scope == stridePreferencePrivate {
				if audience.Visibility != "private" || len(audience.Principals) != 1 || audience.Principals[0] != subject {
					continue
				}
			} else {
				if preference.ScopeID != scopeID || !audienceContainsSTRIDEAudience(audience, currentAudienceForState(preference)) {
					continue
				}
			}
		}
		projection, projectionErr := makeSTRIDECollaborationContextPreference(state, preference)
		if projectionErr != nil {
			return nil, state.Revision, projectionErr
		}
		result = append(result, projection)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Reference.ID < result[j].Reference.ID })
	return result, state.Revision, nil
}

func makeSTRIDECollaborationContextPreference(state durableSTRIDECollaborationSubject, preference STRIDECollaborationPreferenceState) (STRIDECollaborationContextPreference, error) {
	id := strideCollaborationRelationshipID(preference.Key)
	material := struct {
		ID, Subject, ScopeID, MemoryScope, PreferenceType, Value, Origin, SourceEventID string
		Evidence                                                                        []STRIDEReference
		Confidence                                                                      float64
		ExpiresAt                                                                       time.Time
		Audience                                                                        STRIDEAudience
		ConsentRevision, ProjectionRevision                                             int64
	}{id, preference.SubjectPrincipal, preference.ScopeID, preference.Scope, preference.PreferenceType, preference.Value, preference.Origin, preference.SourceEventID, SortedSTRIDEReferences(preference.Evidence), preference.Confidence, preference.ExpiresAt, preference.Audience, state.Consent.Revision, state.Revision}
	digest, err := STRIDEContractDigest(material)
	if err != nil {
		return STRIDECollaborationContextPreference{}, err
	}
	header := STRIDEContractHeader{TenantID: canonicalTenantID(), ID: id, Revision: state.Revision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractAgentRelationshipMemory, ContentDigest: digest, CreatedAt: preference.LastObserved.UTC()}
	relationship := AgentRelationshipMemory{Header: header, AgentID: "scout", Subject: preference.SubjectPrincipal, Scope: preference.ScopeID, ObservationDigest: strings.TrimPrefix(preference.ValueDigest, "sha256:"), Evidence: SortedSTRIDEReferences(preference.Evidence), Confidence: preference.Confidence, FirstObserved: preference.FirstObserved, LastObserved: preference.LastObserved, ReinforcementCount: preference.ReinforcementCount, Audience: preference.Audience, ExpiresAt: timePtr(preference.ExpiresAt), Status: "present"}
	projection := STRIDECollaborationContextPreference{Reference: referenceFromHeader(header), Relationship: relationship, PreferenceType: preference.PreferenceType, Value: preference.Value, Scope: preference.Scope, Origin: preference.Origin, SourceEventID: preference.SourceEventID, Evidence: SortedSTRIDEReferences(preference.Evidence), Confidence: preference.Confidence, ExpiresAt: preference.ExpiresAt, ConsentRevision: state.Consent.Revision, ProjectionRevision: state.Revision}
	if projection.validate() != nil {
		return STRIDECollaborationContextPreference{}, ErrSTRIDECollaborationPreferenceDenied
	}
	return projection, nil
}

func strideCollaborationStateByRelationshipID(profile STRIDECollaborationProfile, id string) (STRIDECollaborationPreferenceState, bool) {
	for _, preference := range profile.Preferences {
		if strideCollaborationRelationshipID(preference.Key) == id {
			return preference, true
		}
	}
	return STRIDECollaborationPreferenceState{}, false
}

func strideCollaborationRelationshipID(key string) string {
	return "relationship_" + sha256Hex([]byte("stride-collaboration-relationship/v1\x00" + key))[:24]
}

func strideCollaborationControlEventID(subject string, revision int64, action, preferenceType, value string) string {
	return "preference_" + sha256Hex([]byte(fmt.Sprintf("stride-collaboration-control/v1\x00%s\x00%d\x00%s\x00%s\x00%s", subject, revision, action, preferenceType, value)))[:24]
}

func nextSTRIDECollaborationEventTime(events []STRIDECollaborationPreferenceEvent, requested time.Time) time.Time {
	result := requested.UTC()
	for _, event := range events {
		if !event.ObservedAt.Before(result) {
			result = event.ObservedAt.UTC().Add(time.Nanosecond)
		}
	}
	return result
}

func currentAudienceForState(state STRIDECollaborationPreferenceState) STRIDEAudience {
	return state.Audience
}

func audienceContainsSTRIDEAudience(container, candidate STRIDEAudience) bool {
	if container.Validate() != nil || candidate.Validate() != nil || container.Visibility != candidate.Visibility {
		return false
	}
	for _, principal := range candidate.Principals {
		if !containsSTRIDEID(container.Principals, principal) {
			return false
		}
	}
	return true
}

func timePtr(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
