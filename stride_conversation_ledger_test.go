package main

import (
	"errors"
	"testing"
	"time"
)

func strideConversationEvent(id, sourceID, eventType string, revision int64, audience STRIDEAudience) ConversationEvent {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return ConversationEvent{
		Header: strideTestHeader(STRIDEContractConversationEvent, id), SourceType: "channel_message", SourceID: sourceID, ThreadID: "team",
		AuthorPrincipal: "member_aj", AuthorName: "AJ", OccurredAt: now, IngestedAt: now, EventType: eventType,
		ContentRevision: revision, ContentDigest: strideTestDigest("c"), Audience: audience, ACLVersion: 1, RetentionPolicy: "company_default", PurgeGeneration: 0, Provenance: "client",
	}
}

func strideConversationAppend(id, sourceID, eventType string, revision int64, audience STRIDEAudience) STRIDEConversationAppend {
	event := strideConversationEvent(id, sourceID, eventType, revision, audience)
	if revision > 1 {
		event.Header.Revision = revision
	}
	return STRIDEConversationAppend{Event: event, IdempotencyKey: "idem_" + id}
}

func TestSTRIDEConversationLedgerPrivateCanaryNeverProjects(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	private := STRIDEAudience{Visibility: "private", Principals: []string{"member_aj"}}
	if _, err := ledger.Append(strideConversationAppend("private_event", "private_message", "message", 1, private)); err != nil {
		t.Fatal(err)
	}
	for _, principal := range []string{"member_aj", "member_tim"} {
		projection, projectErr := ledger.ProjectForPrincipal(principal)
		if projectErr != nil || len(projection) != 0 {
			t.Fatalf("private canary projected for %s: %#v %v", principal, projection, projectErr)
		}
	}
}

func TestSTRIDEConversationLedgerACLDifferentialAndUnknownSchemaFailClosed(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	memberOnly := STRIDEAudience{Visibility: "channel", Principals: []string{"member_aj"}}
	append := strideConversationAppend("event_acl", "message_acl", "message", 1, memberOnly)
	if _, err := ledger.Append(append); err != nil {
		t.Fatal(err)
	}
	allowed, err := ledger.ProjectForPrincipal("member_aj")
	if err != nil || len(allowed) != 1 {
		t.Fatalf("authorized projection=%#v err=%v", allowed, err)
	}
	denied, err := ledger.ProjectForPrincipal("member_tim")
	if err != nil || len(denied) != 0 {
		t.Fatalf("ACL differential leaked projection=%#v err=%v", denied, err)
	}
	unknown := strideConversationAppend("event_unknown", "message_unknown", "message", 1, memberOnly)
	unknown.Event.Header.SchemaVersion++
	if _, err := ledger.Append(unknown); !errors.Is(err, ErrSTRIDEConversationInvalid) {
		t.Fatalf("unknown schema append=%v", err)
	}
}

func TestSTRIDEConversationLedgerVersionsReplyReactionFileAndLink(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	audience := strideTestAudience()
	message := strideConversationAppend("event_message", "message_1", "message", 1, audience)
	if _, err := ledger.Append(message); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []STRIDEConversationAppend{
		strideConversationAppend("event_reply", "message_1", "reply", 2, audience),
		strideConversationAppend("event_reaction", "message_1", "reaction", 3, audience),
		strideConversationAppend("event_file", "message_1", "file", 4, audience),
		strideConversationAppend("event_link", "message_1", "link", 5, audience),
	} {
		mutation.Event.StructuredRefs = []STRIDEReference{strideTestRef(STRIDEContractRichMessagePart, "asset_"+mutation.Event.Header.ID)}
		if mutation.Event.EventType == "reply" {
			mutation.Event.ReplyToEventID = "event_message"
		}
		if _, err := ledger.Append(mutation); err != nil {
			t.Fatalf("append %s: %v", mutation.Event.EventType, err)
		}
	}
	projection, err := ledger.ProjectForPrincipal("member_aj")
	if err != nil || len(projection) != 1 {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	got := projection[0]
	if got.ReplyToEventID != "event_message" || len(got.ReactionActors) != 1 || len(got.AttachmentRefs) != 1 || len(got.LinkRefs) != 1 {
		t.Fatalf("version projection lost lineage: %#v", got)
	}
}

func TestSTRIDEConversationLedgerIdempotencyConflictAndReplayIdentity(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	append := strideConversationAppend("event_1", "message_1", "message", 1, strideTestAudience())
	first, err := ledger.Append(append)
	if err != nil || first.Existing {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := ledger.Append(append)
	if err != nil || !second.Existing || second.Record.Sequence != first.Record.Sequence {
		t.Fatalf("idempotent append=%#v err=%v", second, err)
	}
	conflict := append
	conflict.Event.ContentDigest = strideTestDigest("d")
	if _, err := ledger.Append(conflict); !errors.Is(err, ErrSTRIDEConversationConflict) {
		t.Fatalf("conflict=%v", err)
	}
	before, err := ledger.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ledger.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}}, snapshot)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	after, err := restored.Rebuild()
	if err != nil || before.Checksum != after.Checksum {
		t.Fatalf("replay checksum before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestSTRIDEConversationLedgerInvalidationFanoutAndPrivateShare(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	message := strideConversationAppend("event_1", "message_1", "message", 1, strideTestAudience())
	if _, err := ledger.Append(message); err != nil {
		t.Fatal(err)
	}
	source := strideConversationEventReference(message.Event)
	derived := strideTestRef(STRIDEContractKnowledgeAssertion, "knowledge_1")
	if err := ledger.AddDerivedEdge(STRIDESourceDerivedEdge{TenantID: "bonfire", Source: source, Derived: derived, Lane: STRIDEDerivedKnowledge}); err != nil {
		t.Fatal(err)
	}
	answer := strideTestRef(STRIDEContractCompanyAnswer, "answer_1")
	if err := ledger.AddDerivedEdge(STRIDESourceDerivedEdge{TenantID: "bonfire", Source: derived, Derived: answer, Lane: STRIDEDerivedAnswer}); err != nil {
		t.Fatal(err)
	}
	edit := strideConversationAppend("event_2", "message_1", "edit", 2, strideTestAudience())
	edit.Event.SupersedesEventID = "event_1"
	if _, err := ledger.Append(edit); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ledger.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, invalidation := range snapshot.Invalidations {
		found[invalidation.Reference.ID] = true
	}
	if !found["event_1"] || !found["knowledge_1"] || !found["answer_1"] {
		t.Fatalf("invalidation fanout=%#v", snapshot.Invalidations)
	}
	restored, err := RestoreSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}}, snapshot)
	if err != nil {
		t.Fatalf("restart restore invalidation state: %v", err)
	}
	restoredSnapshot, err := restored.Snapshot()
	if err != nil || len(restoredSnapshot.Invalidations) != len(snapshot.Invalidations) {
		t.Fatalf("restart lost invalidations: %#v %#v %v", snapshot.Invalidations, restoredSnapshot.Invalidations, err)
	}

	privateShare := strideConversationAppend("event_share", "shared_message", "message", 1, strideTestAudience())
	privateShare.Event.SourceType = "private_share"
	privateShare.PrivateShareSource = &STRIDEReference{ContractType: STRIDEContractConversationEvent, ID: "private_source", Revision: 1, Digest: strideTestDigest("e")}
	privateShare.PrivateShareAuthorization = &STRIDEReference{ContractType: STRIDEContractWorkProposal, ID: "share_authorization", Revision: 1, Digest: strideTestDigest("f")}
	if _, err := ledger.Append(privateShare); !errors.Is(err, ErrSTRIDEConversationDenied) {
		t.Fatalf("private share without canonical authority=%v", err)
	}
	badShare := privateShare
	badShare.Event.Header.ID = "event_bad_share"
	badShare.IdempotencyKey = "idem_event_bad_share"
	badShare.PrivateShareAuthorization = nil
	if _, err := ledger.Append(badShare); !errors.Is(err, ErrSTRIDEConversationInvalid) {
		t.Fatalf("unauthorized private share=%v", err)
	}
	forged := privateShare
	forged.Event.Header.ID = "event_forged_share"
	forged.IdempotencyKey = "idem_event_forged_share"
	forged.PrivateShareAuthorization = &STRIDEReference{ContractType: STRIDEContractWorkProposal, ID: "forged_authorization", Revision: 1, Digest: strideTestDigest("a")}
	if _, err := ledger.Append(forged); !errors.Is(err, ErrSTRIDEConversationDenied) {
		t.Fatalf("fabricated private-share authority=%v", err)
	}
}

func TestSTRIDEConversationLedgerClonesMutableAppendState(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	input := strideConversationAppend("immutable_event", "immutable_message", "message", 1, strideTestAudience())
	input.Event.StructuredRefs = []STRIDEReference{strideTestRef(STRIDEContractRichMessagePart, "immutable_asset")}
	if _, err := ledger.Append(input); err != nil {
		t.Fatal(err)
	}
	input.Event.Audience.Principals[0] = "member_attacker"
	input.Event.StructuredRefs[0].ID = "mutated_asset"
	projection, err := ledger.ProjectForPrincipal("member_aj")
	if err != nil || len(projection) != 1 || projection[0].Audience.Principals[0] != "member_aj" {
		t.Fatalf("caller mutation changed accepted audience: projection=%#v err=%v", projection, err)
	}
	snapshot, err := ledger.Snapshot()
	if err != nil || snapshot.Events[0].Append.Event.StructuredRefs[0].ID != "immutable_asset" {
		t.Fatalf("caller mutation changed accepted references: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestSTRIDEConversationLedgerTenantIsolation(t *testing.T) {
	ledger, err := NewSTRIDEConversationLedger(STRIDEConversationLedgerConfig{RecallThreadIDs: []string{"team"}})
	if err != nil {
		t.Fatal(err)
	}
	audience := STRIDEAudience{Visibility: "channel", Principals: []string{"member_aj"}}
	bonfire := strideConversationAppend("shared_event_id", "shared_source_id", "message", 1, audience)
	if _, err := ledger.Append(bonfire); err != nil {
		t.Fatal(err)
	}
	other := strideConversationAppend("shared_event_id", "shared_source_id", "message", 1, audience)
	other.Event.Header.TenantID = "other_org"
	other.IdempotencyKey = "other_shared_event"
	if _, err := ledger.Append(other); err != nil {
		t.Fatalf("same event identity in another tenant should be isolated: %v", err)
	}

	for tenant, want := range map[string]string{"bonfire": "bonfire", "other_org": "other_org"} {
		projected, err := ledger.ProjectForTenantPrincipal(tenant, "member_aj")
		if err != nil || len(projected) != 1 || projected[0].TenantID != want {
			t.Fatalf("tenant %s projection=%#v err=%v", tenant, projected, err)
		}
	}
	if projected, err := ledger.ProjectForPrincipal("member_aj"); !errors.Is(err, ErrSTRIDEConversationDenied) || projected != nil {
		t.Fatalf("tenantless multi-tenant projection=%#v err=%v", projected, err)
	}

	edit := strideConversationAppend("other_edit", "shared_source_id", "edit", 2, audience)
	edit.Event.Header.TenantID = "other_org"
	edit.Event.SupersedesEventID = "bonfire_only_event"
	edit.IdempotencyKey = "other_edit"
	if _, err := ledger.Append(edit); !errors.Is(err, ErrSTRIDEConversationUnknown) {
		t.Fatalf("cross-tenant supersession error=%v", err)
	}

	bonfireRef := strideConversationEventReference(bonfire.Event)
	if err := ledger.InvalidateForTenant("bonfire", bonfireRef, "tenant_revoke"); err != nil {
		t.Fatal(err)
	}
	otherProjection, err := ledger.ProjectForTenantPrincipal("other_org", "member_aj")
	if err != nil || len(otherProjection) != 1 {
		t.Fatalf("bonfire invalidation crossed tenant boundary: %#v %v", otherProjection, err)
	}
}
