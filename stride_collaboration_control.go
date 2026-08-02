package main

// Subject-authored collaboration-memory controls are not chat messages, but
// they still need durable provenance. This envelope turns an authenticated
// Settings action into a private, body-free ConversationEvent and binds it to
// the exact relationship revision with the runtime's external MAC authority.
// Raw preference text stays in the corrigible collaboration store; the control
// evidence contains only its digest and is purged with the preference.

import (
	"fmt"
	"strings"
	"time"
)

const strideCollaborationControlDomain = "stride_collaboration_control"

func strideCollaborationNow(config STRIDERuntimeConfig) time.Time {
	if config.Now != nil {
		return config.Now().UTC()
	}
	return time.Now().UTC()
}

type STRIDECollaborationControlEvidence struct {
	Event                ConversationEvent `json:"event"`
	Action               string            `json:"action"`
	Actor                string            `json:"actor"`
	RelationshipID       string            `json:"relationshipId,omitempty"`
	PreferenceType       string            `json:"preferenceType"`
	Scope                string            `json:"scope"`
	ValueDigest          string            `json:"valueDigest"`
	ExpectedRevision     int64             `json:"expectedRevision"`
	ProductReceiptDigest string            `json:"productReceiptDigest"`
	Generation           uint64            `json:"generation"`
	KeyID                string            `json:"keyId"`
	Digest               string            `json:"digest"`
	Signature            string            `json:"signature"`
}

type strideCollaborationControlMaterial struct {
	Event                ConversationEvent `json:"event"`
	Action               string            `json:"action"`
	Actor                string            `json:"actor"`
	RelationshipID       string            `json:"relationshipId,omitempty"`
	PreferenceType       string            `json:"preferenceType"`
	Scope                string            `json:"scope"`
	ValueDigest          string            `json:"valueDigest"`
	ExpectedRevision     int64             `json:"expectedRevision"`
	ProductReceiptDigest string            `json:"productReceiptDigest"`
	Generation           uint64            `json:"generation"`
	KeyID                string            `json:"keyId"`
}

func (evidence STRIDECollaborationControlEvidence) material() strideCollaborationControlMaterial {
	return strideCollaborationControlMaterial{
		Event: evidence.Event, Action: evidence.Action, Actor: evidence.Actor,
		RelationshipID: evidence.RelationshipID, PreferenceType: evidence.PreferenceType,
		Scope: evidence.Scope, ValueDigest: evidence.ValueDigest,
		ExpectedRevision: evidence.ExpectedRevision, ProductReceiptDigest: evidence.ProductReceiptDigest,
		Generation: evidence.Generation, KeyID: evidence.KeyID,
	}
}

func (evidence STRIDECollaborationControlEvidence) Reference() STRIDEReference {
	return referenceFromHeader(evidence.Event.Header)
}

func mintSTRIDECollaborationControlEvidence(config STRIDERuntimeConfig, receipt STRIDEProductActivationReceipt, actor, action, relationshipID, preferenceType, value, scope string, audience STRIDEAudience, expectedRevision int64, at time.Time) (STRIDECollaborationControlEvidence, error) {
	actor = strings.TrimSpace(actor)
	action = strings.ToLower(strings.TrimSpace(action))
	relationshipID = strings.TrimSpace(relationshipID)
	preferenceType = strings.ToLower(strings.TrimSpace(preferenceType))
	value = strings.TrimSpace(value)
	scope = strings.ToLower(strings.TrimSpace(scope))
	if !config.RelationshipMemoryEnabled || !verifySTRIDEProductActivationReceipt(config, receipt, STRIDEProductScopeCoworker, at) ||
		!strideIdentifier(actor) || !oneOf(action, "remember", "correct") ||
		(action == "remember" && relationshipID != "") || (action == "correct" && !strideIdentifier(relationshipID)) ||
		!safeSTRIDECollaborationPreferenceType(preferenceType) || value == "" || len(value) > 500 || !oneOf(scope, stridePreferencePrivate, stridePreferenceShared) ||
		(action == "remember" && scope != stridePreferencePrivate) || audience.Validate() != nil || expectedRevision < 1 || at.IsZero() {
		return STRIDECollaborationControlEvidence{}, ErrSTRIDECollaborationPreferenceDenied
	}
	if scope == stridePreferencePrivate {
		if audience.Visibility != "private" || len(audience.Principals) != 1 || audience.Principals[0] != actor {
			return STRIDECollaborationControlEvidence{}, ErrSTRIDECollaborationPreferenceDenied
		}
	} else if audience.Visibility == "private" || !containsSTRIDEPrincipal(audience.Principals, actor) {
		return STRIDECollaborationControlEvidence{}, ErrSTRIDECollaborationPreferenceDenied
	}
	valueDigest := temporalDigest(value)
	identity := temporalDigest(strings.Join([]string{actor, fmt.Sprint(expectedRevision), action, relationshipID, preferenceType, valueDigest}, "\x00"))
	eventID := "relationship_control_" + identity[:24]
	contentDigest, err := STRIDEContractDigest(struct {
		Action, Actor, RelationshipID, PreferenceType, Scope, ValueDigest, ProductReceiptDigest string
		ExpectedRevision                                                                        int64
		OccurredAt                                                                              time.Time
	}{action, actor, relationshipID, preferenceType, scope, valueDigest, receipt.Digest, expectedRevision, at.UTC()})
	if err != nil {
		return STRIDECollaborationControlEvidence{}, err
	}
	event := ConversationEvent{
		Header: STRIDEContractHeader{
			TenantID: config.TenantID, ID: eventID, Revision: expectedRevision + 1,
			SchemaVersion: STRIDEContractSchemaVersion, ContractType: STRIDEContractConversationEvent,
			ContentDigest: contentDigest, CreatedAt: at.UTC(),
		},
		SourceType: "relationship_settings", SourceID: eventID, AuthorPrincipal: actor,
		AuthorName: "authenticated subject", OccurredAt: at.UTC(), IngestedAt: at.UTC(),
		EventType: "consent_change", ContentRevision: expectedRevision + 1, ContentDigest: contentDigest,
		Audience: audience, ACLVersion: 1,
		RetentionPolicy: "subject_controlled", ReactionActors: []string{}, Provenance: "server", OnBehalfOf: actor,
	}
	if event.Validate() != nil {
		return STRIDECollaborationControlEvidence{}, ErrSTRIDECollaborationPreferenceDenied
	}
	evidence := STRIDECollaborationControlEvidence{
		Event: event, Action: action, Actor: actor, RelationshipID: relationshipID,
		PreferenceType: preferenceType, Scope: scope, ValueDigest: valueDigest,
		ExpectedRevision: expectedRevision, ProductReceiptDigest: receipt.Digest,
		Generation: receipt.Generation, KeyID: receipt.KeyID,
	}
	evidence.Digest, err = STRIDEContractDigest(evidence.material())
	if err != nil {
		return STRIDECollaborationControlEvidence{}, err
	}
	evidence.Signature, err = strideSnapshotMAC(config.Authority, strideCollaborationControlDomain, evidence.Generation, evidence.Digest)
	if err != nil || !verifySTRIDECollaborationControlEvidence(config.Authority, evidence) {
		return STRIDECollaborationControlEvidence{}, ErrSTRIDECollaborationPreferenceDenied
	}
	return evidence, nil
}

func verifySTRIDECollaborationControlEvidence(authority STRIDESnapshotMACAuthority, evidence STRIDECollaborationControlEvidence) bool {
	if evidence.Event.Validate() != nil || evidence.Event.Header.TenantID != canonicalTenantID() ||
		!oneOf(evidence.Action, "remember", "correct") || evidence.Actor != evidence.Event.AuthorPrincipal ||
		evidence.Event.OnBehalfOf != evidence.Actor || evidence.Event.SourceType != "relationship_settings" ||
		evidence.Event.SourceID != evidence.Event.Header.ID ||
		(evidence.Action == "remember" && evidence.RelationshipID != "") ||
		(evidence.Action == "correct" && !strideIdentifier(evidence.RelationshipID)) ||
		!safeSTRIDECollaborationPreferenceType(evidence.PreferenceType) || !oneOf(evidence.Scope, stridePreferencePrivate, stridePreferenceShared) ||
		(evidence.Action == "remember" && evidence.Scope != stridePreferencePrivate) ||
		!isHexDigest(evidence.ValueDigest) || evidence.ExpectedRevision < 1 ||
		evidence.Event.Header.Revision != evidence.ExpectedRevision+1 || evidence.Event.ContentRevision != evidence.ExpectedRevision+1 ||
		!isHexDigest(evidence.ProductReceiptDigest) || evidence.Generation == 0 || evidence.KeyID != authority.KeyID || !isHexDigest(evidence.Digest) {
		return false
	}
	if evidence.Scope == stridePreferencePrivate {
		if evidence.Event.Audience.Visibility != "private" || len(evidence.Event.Audience.Principals) != 1 || evidence.Event.Audience.Principals[0] != evidence.Actor {
			return false
		}
	} else if evidence.Event.Audience.Visibility == "private" || !containsSTRIDEPrincipal(evidence.Event.Audience.Principals, evidence.Actor) {
		return false
	}
	digest, err := STRIDEContractDigest(evidence.material())
	return err == nil && digest == evidence.Digest && verifySTRIDESnapshotMAC(
		STRIDESnapshotRestorePolicy{Authority: authority, MinimumGeneration: evidence.Generation},
		strideCollaborationControlDomain, evidence.KeyID, evidence.Generation, evidence.Digest, evidence.Signature,
	)
}
