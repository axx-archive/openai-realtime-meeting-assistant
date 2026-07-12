package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type CanonicalParitySet struct {
	IDs      []string
	Count    int
	Checksum string
}

type CanonicalParitySnapshot struct {
	Families   map[string]CanonicalParitySet
	Principals map[string]map[string]CanonicalParitySet
}

type CanonicalRepairCandidate struct {
	Family             string
	ObjectID           string
	Kind               string // missing_event | tombstone_required | state_mismatch | principal_* | target_history_gap
	StateDigest        string
	SourceStateDigest  string
	TargetStateDigest  string
	SourceVersion      int64
	TargetVersion      int64
	Principal          string
	Event              *CanonicalEvent
	ConfirmedByJournal bool
}

type CanonicalReconcileReport struct {
	Source                CanonicalParitySnapshot
	Target                CanonicalParitySnapshot
	Candidates            []CanonicalRepairCandidate
	PrincipalParityProven bool
	Diverged              bool
}

// CanonicalParityACLResolver is the target-side authorization proof. It must
// resolve current canonical ACL/grant state; reconciliation never infers target
// visibility from the source plan.
type CanonicalParityACLResolver interface {
	CanReadCanonicalObject(context.Context, string, CanonicalEvent) (bool, error)
}

type CanonicalReconcileOptions struct {
	ACL              CanonicalParityACLResolver
	TestedPrincipals []string
}

func BuildCanonicalParitySnapshot(objects []CanonicalImportedObject) CanonicalParitySnapshot {
	snapshot := CanonicalParitySnapshot{Families: map[string]CanonicalParitySet{}, Principals: map[string]map[string]CanonicalParitySet{}}
	byFamily := map[string][]CanonicalImportedObject{}
	byPrincipal := map[string]map[string][]CanonicalImportedObject{}
	for _, object := range objects {
		byFamily[object.Family] = append(byFamily[object.Family], object)
		for _, principal := range object.Principals {
			if byPrincipal[principal] == nil {
				byPrincipal[principal] = map[string][]CanonicalImportedObject{}
			}
			byPrincipal[principal][object.Family] = append(byPrincipal[principal][object.Family], object)
		}
	}
	for family, members := range byFamily {
		snapshot.Families[family] = canonicalParitySet(members)
	}
	for principal, families := range byPrincipal {
		snapshot.Principals[principal] = map[string]CanonicalParitySet{}
		for family, members := range families {
			snapshot.Principals[principal][family] = canonicalParitySet(members)
		}
	}
	return snapshot
}

func canonicalParitySet(objects []CanonicalImportedObject) CanonicalParitySet {
	sort.Slice(objects, func(i, j int) bool { return objects[i].ObjectID < objects[j].ObjectID })
	ids := make([]string, 0, len(objects))
	hasher := sha256.New()
	for _, object := range objects {
		ids = append(ids, object.ObjectID)
		hasher.Write([]byte(object.ObjectID))
		hasher.Write([]byte{0})
		hasher.Write([]byte(object.StateDigest))
		hasher.Write([]byte{0})
	}
	return CanonicalParitySet{IDs: ids, Count: len(ids), Checksum: hex.EncodeToString(hasher.Sum(nil))}
}

func ReconcileCanonicalPlanWithStore(ctx context.Context, source CanonicalImportPlan, store CanonicalEventStore) (CanonicalReconcileReport, error) {
	return ReconcileCanonicalPlanWithOptions(ctx, source, store, CanonicalReconcileOptions{})
}

func ReconcileCanonicalPlanWithOptions(ctx context.Context, source CanonicalImportPlan, store CanonicalEventStore, options CanonicalReconcileOptions) (CanonicalReconcileReport, error) {
	if strings.TrimSpace(source.TenantID) == "" {
		return CanonicalReconcileReport{}, errors.New("canonical reconciliation source tenant is required")
	}
	events, err := store.Events(ctx)
	if err != nil {
		return CanonicalReconcileReport{}, err
	}
	sourceByKey := map[string]CanonicalImportedObject{}
	eventByKey := map[string]CanonicalEvent{}
	journaled := map[string]bool{}
	for index, object := range source.Objects {
		key := object.Family + "\x00" + object.ObjectID
		sourceByKey[key] = object
		if index < len(source.Events) {
			eventByKey[key] = source.Events[index]
		}
		if object.Family == "tombstone" || object.Family == "eviction" {
			journaled[object.ObjectID] = true
		}
	}
	testedPrincipals := append([]string(nil), options.TestedPrincipals...)
	for _, object := range source.Objects {
		testedPrincipals = append(testedPrincipals, object.Principals...)
	}
	testedPrincipals = uniqueSortedStrings(testedPrincipals)

	targetObjects := make([]CanonicalImportedObject, 0, len(events))
	targetByKey := map[string]CanonicalEvent{}
	targetVisibleByKey := map[string]map[string]bool{}
	eventsByKey := map[string][]CanonicalEvent{}
	for _, event := range events {
		if event.TenantID != source.TenantID {
			continue
		}
		key := event.AggregateType + "\x00" + event.AggregateID
		eventsByKey[key] = append(eventsByKey[key], event)
	}
	report := CanonicalReconcileReport{Source: BuildCanonicalParitySnapshot(source.Objects), PrincipalParityProven: options.ACL != nil}
	for key, history := range eventsByKey {
		sort.Slice(history, func(i, j int) bool { return history[i].AggregateVersion < history[j].AggregateVersion })
		expected := int64(1)
		var current CanonicalEvent
		for _, event := range history {
			if event.AggregateVersion != expected {
				report.Candidates = append(report.Candidates, CanonicalRepairCandidate{Family: event.AggregateType, ObjectID: event.AggregateID, Kind: "target_history_gap", TargetVersion: event.AggregateVersion})
				break
			}
			current = event
			expected++
		}
		if current.EventID == uuid.Nil {
			continue
		}
		targetByKey[key] = current
		var principals []string
		if options.ACL != nil {
			targetVisibleByKey[key] = map[string]bool{}
			for _, principal := range testedPrincipals {
				allowed, err := options.ACL.CanReadCanonicalObject(ctx, principal, current)
				if err != nil {
					return CanonicalReconcileReport{}, err
				}
				if allowed {
					principals = append(principals, principal)
					targetVisibleByKey[key][principal] = true
				}
			}
		}
		targetObjects = append(targetObjects, CanonicalImportedObject{Family: current.AggregateType, ObjectID: current.AggregateID, StateDigest: eventPayloadStateDigest(current), AggregateVersion: current.AggregateVersion, EventID: current.EventID, Principals: principals})
	}
	report.Target = BuildCanonicalParitySnapshot(targetObjects)
	for key, object := range sourceByKey {
		target, ok := targetByKey[key]
		if ok {
			targetDigest := eventPayloadStateDigest(target)
			if object.StateDigest != targetDigest || object.AggregateVersion != target.AggregateVersion {
				report.Candidates = append(report.Candidates, CanonicalRepairCandidate{Family: object.Family, ObjectID: object.ObjectID, Kind: "state_mismatch", StateDigest: object.StateDigest, SourceStateDigest: object.StateDigest, TargetStateDigest: targetDigest, SourceVersion: object.AggregateVersion, TargetVersion: target.AggregateVersion})
			}
			continue
		}
		event := eventByKey[key]
		report.Candidates = append(report.Candidates, CanonicalRepairCandidate{Family: object.Family, ObjectID: object.ObjectID, Kind: "missing_event", StateDigest: object.StateDigest, Event: &event})
	}
	if options.ACL != nil {
		for key, target := range targetByKey {
			sourceObject, sourceExists := sourceByKey[key]
			if !sourceExists {
				continue
			}
			expected := map[string]bool{}
			for _, principal := range sourceObject.Principals {
				expected[principal] = true
			}
			for _, principal := range testedPrincipals {
				actual := targetVisibleByKey[key][principal]
				if expected[principal] && !actual {
					report.Candidates = append(report.Candidates, CanonicalRepairCandidate{Family: target.AggregateType, ObjectID: target.AggregateID, Kind: "principal_missing_access", Principal: principal})
				}
				if !expected[principal] && actual {
					report.Candidates = append(report.Candidates, CanonicalRepairCandidate{Family: target.AggregateType, ObjectID: target.AggregateID, Kind: "principal_extra_access", Principal: principal})
				}
			}
		}
	}
	for key, event := range targetByKey {
		if _, ok := sourceByKey[key]; ok {
			continue
		}
		journalKey := event.AggregateType + ":" + event.AggregateID
		report.Candidates = append(report.Candidates, CanonicalRepairCandidate{Family: event.AggregateType, ObjectID: event.AggregateID, Kind: "tombstone_required", StateDigest: eventPayloadStateDigest(event), ConfirmedByJournal: journaled[journalKey]})
	}
	sort.Slice(report.Candidates, func(i, j int) bool {
		if report.Candidates[i].Family != report.Candidates[j].Family {
			return report.Candidates[i].Family < report.Candidates[j].Family
		}
		if report.Candidates[i].ObjectID != report.Candidates[j].ObjectID {
			return report.Candidates[i].ObjectID < report.Candidates[j].ObjectID
		}
		if report.Candidates[i].Kind != report.Candidates[j].Kind {
			return report.Candidates[i].Kind < report.Candidates[j].Kind
		}
		return report.Candidates[i].Principal < report.Candidates[j].Principal
	})
	report.Diverged = len(report.Candidates) > 0 || !report.PrincipalParityProven || !paritySnapshotsEqual(report.Source, report.Target)
	return report, nil
}

func eventPayloadStateDigest(event CanonicalEvent) string {
	var payload struct {
		StateDigest string `json:"payload_sha256"`
	}
	if json.Unmarshal(event.Payload, &payload) == nil && isHexDigest(payload.StateDigest) {
		return payload.StateDigest
	}
	sum := sha256.Sum256(event.Payload)
	return hex.EncodeToString(sum[:])
}

func paritySnapshotsEqual(left, right CanonicalParitySnapshot) bool {
	if len(left.Families) != len(right.Families) || len(left.Principals) != len(right.Principals) {
		return false
	}
	for family, leftSet := range left.Families {
		rightSet, ok := right.Families[family]
		if !ok || leftSet.Count != rightSet.Count || leftSet.Checksum != rightSet.Checksum {
			return false
		}
	}
	for principal, leftFamilies := range left.Principals {
		rightFamilies, ok := right.Principals[principal]
		if !ok || len(leftFamilies) != len(rightFamilies) {
			return false
		}
		for family, leftSet := range leftFamilies {
			rightSet, ok := rightFamilies[family]
			if !ok || leftSet.Count != rightSet.Count || leftSet.Checksum != rightSet.Checksum {
				return false
			}
		}
	}
	return true
}
