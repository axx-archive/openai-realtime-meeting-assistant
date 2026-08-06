package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type personMyMindFixture struct {
	service             *PersonMyMindService
	now                 time.Time
	person              PersonPrincipal
	membership          WorkspaceMembership
	membershipAuthority MyMindAuthorityAssertion
	custodyAuthority    MyMindAuthorityAssertion
	recoveryAuthority   MyMindAuthorityAssertion
	exportAuthority     MyMindAuthorityAssertion
	departureAuthority  MyMindAuthorityAssertion
	deleteAuthority     MyMindAuthorityAssertion
}

func newPersonMyMindFixture(t *testing.T) personMyMindFixture {
	t.Helper()
	now := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	service := NewPersonMyMindService()
	person := PersonPrincipal{
		Header:               personMyMindHeader(STRIDEGlobalPersonTenant, STRIDEContractPersonPrincipal, "person_aj", 1, now),
		AccountSubjectDigest: strideTestDigest("1"), Status: "active", RecoveryRevision: 1, CustodyRevision: 1,
	}
	if err := service.PutPerson(person); err != nil {
		t.Fatalf("put person: %v", err)
	}
	fixture := personMyMindFixture{service: service, now: now, person: person}
	authorities := []struct {
		grant     MyMindAuthorityGrant
		assertion *MyMindAuthorityAssertion
	}{
		{MyMindAuthorityGrant{ID: "authority_membership", Authority: MyMindAuthorityWorkspaceMembership, ControllerID: "workspace_admin", WorkspaceID: "workspace_bonfire", GrantedAt: now.Add(-time.Hour)}, &fixture.membershipAuthority},
		{MyMindAuthorityGrant{ID: "authority_custody", Authority: MyMindAuthorityCustody, ControllerID: "person_aj", PersonID: person.Header.ID, GrantedAt: now.Add(-time.Hour)}, &fixture.custodyAuthority},
		{MyMindAuthorityGrant{ID: "authority_recovery", Authority: MyMindAuthorityAccountRecovery, ControllerID: "recovery_service", PersonID: person.Header.ID, GrantedAt: now.Add(-time.Hour)}, &fixture.recoveryAuthority},
		{MyMindAuthorityGrant{ID: "authority_export", Authority: MyMindAuthorityOrganizationExport, ControllerID: "workspace_export_admin", WorkspaceID: "workspace_bonfire", GrantedAt: now.Add(-time.Hour)}, &fixture.exportAuthority},
		{MyMindAuthorityGrant{ID: "authority_departure", Authority: MyMindAuthorityDeparture, ControllerID: "workspace_departure_admin", WorkspaceID: "workspace_bonfire", GrantedAt: now.Add(-time.Hour)}, &fixture.departureAuthority},
		{MyMindAuthorityGrant{ID: "authority_delete", Authority: MyMindAuthorityGlobalDelete, ControllerID: "deletion_service", PersonID: person.Header.ID, GrantedAt: now.Add(-time.Hour)}, &fixture.deleteAuthority},
	}
	for _, entry := range authorities {
		if err := service.InstallAuthority(entry.grant); err != nil {
			t.Fatalf("install %s: %v", entry.grant.ID, err)
		}
		*entry.assertion = MyMindAuthorityAssertion{GrantID: entry.grant.ID, ControllerID: entry.grant.ControllerID}
	}
	fixture.membership = WorkspaceMembership{
		Header:   personMyMindHeader("workspace_bonfire", STRIDEContractWorkspaceMembership, "membership_bonfire_aj", 1, now),
		PersonID: person.Header.ID, WorkspaceID: "workspace_bonfire", Role: "owner", Status: "active", GrantedAt: now,
	}
	if err := service.PutMembership(fixture.membershipAuthority, fixture.membership); err != nil {
		t.Fatalf("put membership: %v", err)
	}
	return fixture
}

func personMyMindHeader(tenant string, kind STRIDEContractType, id string, revision int64, at time.Time) STRIDEContractHeader {
	return STRIDEContractHeader{TenantID: tenant, ID: id, Revision: revision, SchemaVersion: STRIDEContractSchemaVersion, ContractType: kind, ContentDigest: strideTestDigest(fmt.Sprintf("%x", revision%16)), CreatedAt: at}
}

func (fixture personMyMindFixture) putSource(t *testing.T, id, kind, confidentiality, workspace string, purposes ...string) MyMindSource {
	t.Helper()
	source := MyMindSource{
		Header:   personMyMindHeader(myMindVaultTenant(fixture.person.Header.ID), STRIDEContractMyMindSource, id, 1, fixture.now),
		PersonID: fixture.person.Header.ID, SourceKind: kind, BoundWorkspaceID: workspace, Confidentiality: confidentiality,
		CustodyRef: "custody_" + id, AllowedPurposes: purposes, ConsentRevision: 1, ConsentStatus: "granted", CreatedAt: fixture.now,
	}
	if err := fixture.service.PutSource(fixture.custodyAuthority, source); err != nil {
		t.Fatalf("put source: %v", err)
	}
	return source
}

func (fixture personMyMindFixture) putDisclosure(t *testing.T, id string, source MyMindSource, destination MyMindDestination, purpose string, modes ...string) MyMindDisclosureGrant {
	t.Helper()
	grant := MyMindDisclosureGrant{
		Header:   personMyMindHeader(destination.WorkspaceID, STRIDEContractMyMindDisclosureGrant, id, 1, fixture.now),
		PersonID: fixture.person.Header.ID, MembershipID: fixture.membership.Header.ID, MembershipRevision: fixture.membership.Header.Revision,
		Source: source.Ref(), Destination: destination, Purpose: purpose, Modes: modes, Status: "active", GrantedAt: fixture.now,
	}
	if err := fixture.service.PutDisclosure(fixture.custodyAuthority, grant); err != nil {
		t.Fatalf("put disclosure: %v", err)
	}
	return grant
}

func (fixture personMyMindFixture) assemble(source MyMindSource, destination MyMindDestination, purpose string, modes ...string) (MyMindContextSelection, error) {
	return fixture.service.Assemble(MyMindAssembleRequest{
		PersonID: fixture.person.Header.ID, MembershipID: fixture.membership.Header.ID, MembershipRevision: fixture.membership.Header.Revision,
		WorkspaceID: fixture.membership.WorkspaceID, Destination: destination, Purpose: purpose, Modes: modes, Candidates: []MyMindSourceRef{source.Ref()}, At: fixture.now.Add(time.Minute),
	})
}

func TestPersonMyMindIntersectionPrivateAndShared(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	source := fixture.putSource(t, "source_preference", "preference", "private", "", "private_answer", "shared_answer")
	privateDestination := MyMindDestination{Kind: "private_person", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: fixture.person.Header.ID}

	selection, err := fixture.assemble(source, privateDestination, "private_answer", "personalize")
	if err != nil || len(selection.Sources) != 1 || selection.ExcludedCount != 0 {
		t.Fatalf("private personalization selection = %+v, err=%v", selection, err)
	}
	selection, err = fixture.assemble(source, privateDestination, "private_answer", "cite")
	if err != nil || len(selection.Sources) != 0 || selection.ExcludedCount != 1 {
		t.Fatalf("private citation widened without grant: %+v, err=%v", selection, err)
	}

	sharedDestination := MyMindDestination{Kind: "workspace_thread", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: "thread_ball_dogs"}
	selection, err = fixture.assemble(source, sharedDestination, "shared_answer", "cite", "assert_basis")
	if err != nil || len(selection.Sources) != 0 {
		t.Fatalf("shared source should be excluded before exact grant: %+v, err=%v", selection, err)
	}
	sharedGrant := fixture.putDisclosure(t, "disclosure_ball_dogs", source, sharedDestination, "shared_answer", "cite", "assert_basis")
	selection, err = fixture.assemble(source, sharedDestination, "shared_answer", "cite", "assert_basis")
	if err != nil || len(selection.Sources) != 1 {
		t.Fatalf("exact shared grant was not honored: %+v, err=%v", selection, err)
	}

	wrongDestination := sharedDestination
	wrongDestination.AudienceID = "thread_other"
	selection, err = fixture.assemble(source, wrongDestination, "shared_answer", "cite")
	if err != nil || len(selection.Sources) != 0 {
		t.Fatalf("destination-specific grant leaked: %+v, err=%v", selection, err)
	}
	selection, err = fixture.assemble(source, sharedDestination, "collaboration", "cite")
	if err != nil || len(selection.Sources) != 0 {
		t.Fatalf("purpose-specific grant leaked: %+v, err=%v", selection, err)
	}
	if err := fixture.service.RevokeDisclosure(fixture.membershipAuthority, sharedGrant.Header.ID, fixture.now.Add(2*time.Minute)); err != nil {
		t.Fatalf("workspace could not narrow disclosure: %v", err)
	}
	selection, err = fixture.assemble(source, sharedDestination, "shared_answer", "cite")
	if err != nil || len(selection.Sources) != 0 {
		t.Fatalf("workspace narrowing did not fail closed: %+v, err=%v", selection, err)
	}
}

func TestPersonMyMindRejectsOrganizationWideAndCrossPersonEnumeration(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	duplicateAccount := fixture.person
	duplicateAccount.Header.ID = "person_duplicate_account"
	duplicateAccount.Header.ContentDigest = strideTestDigest("d")
	if err := fixture.service.PutPerson(duplicateAccount); !errors.Is(err, ErrMyMindConflict) {
		t.Fatalf("duplicate account subject digest = %v", err)
	}
	source := fixture.putSource(t, "source_private", "private_import", "private", "", "shared_answer")
	organization := MyMindDestination{Kind: "workspace_channel", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: "organization"}
	grant := MyMindDisclosureGrant{
		Header:   personMyMindHeader(fixture.membership.WorkspaceID, STRIDEContractMyMindDisclosureGrant, "disclosure_org", 1, fixture.now),
		PersonID: fixture.person.Header.ID, MembershipID: fixture.membership.Header.ID, MembershipRevision: 1, Source: source.Ref(), Destination: organization,
		Purpose: "shared_answer", Modes: []string{"cite"}, Status: "active", GrantedAt: fixture.now,
	}
	if err := fixture.service.PutDisclosure(fixture.custodyAuthority, grant); !errors.Is(err, ErrMyMindInvalid) {
		t.Fatalf("organization-wide grant error = %v", err)
	}

	request := MyMindAssembleRequest{
		PersonID: "person_other", MembershipID: fixture.membership.Header.ID, MembershipRevision: 1, WorkspaceID: fixture.membership.WorkspaceID,
		Destination: MyMindDestination{Kind: "private_person", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: "person_other"},
		Purpose:     "shared_answer", Modes: []string{"personalize"}, Candidates: []MyMindSourceRef{source.Ref()}, At: fixture.now,
	}
	if _, err := fixture.service.Assemble(request); !errors.Is(err, ErrMyMindInvalid) {
		t.Fatalf("cross-person candidate should fail without confirming existence: %v", err)
	}

	adapter := FixedUserPersonCompatibilityAdapter{WorkspaceID: fixture.membership.WorkspaceID, Users: map[string]string{"aj@example.com": fixture.person.Header.ID}}
	if person, workspace, err := adapter.ResolveForContext("aj@example.com"); person != "" || workspace != "" || !errors.Is(err, ErrMyMindFeatureDisabled) {
		t.Fatalf("legacy adapter activated a consumer: person=%q workspace=%q err=%v", person, workspace, err)
	}
}

func TestPersonMyMindRevisionConsentAndWorkspaceBoundaries(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	source := fixture.putSource(t, "source_workspace", "reflection", "workspace_confidential", fixture.membership.WorkspaceID, "private_answer")
	destination := MyMindDestination{Kind: "private_person", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: fixture.person.Header.ID}

	withdrawn := source
	withdrawn.Header.Revision = 2
	withdrawn.Header.ContentDigest = strideTestDigest("2")
	withdrawn.Header.CreatedAt = fixture.now.Add(time.Minute)
	withdrawn.ConsentRevision = 2
	withdrawn.ConsentStatus = "withdrawn"
	if err := fixture.service.PutSource(fixture.custodyAuthority, withdrawn); err != nil {
		t.Fatalf("withdraw source: %v", err)
	}
	selection, err := fixture.assemble(source, destination, "private_answer", "personalize")
	if err != nil || len(selection.Sources) != 0 {
		t.Fatalf("stale revision remained eligible: %+v, err=%v", selection, err)
	}
	selection, err = fixture.assemble(withdrawn, destination, "private_answer", "personalize")
	if err != nil || len(selection.Sources) != 0 {
		t.Fatalf("withdrawn source remained eligible: %+v, err=%v", selection, err)
	}
}

func TestPersonMyMindConcurrentRevokeFailsClosed(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	source := fixture.putSource(t, "source_shared", "correction", "private", "", "shared_answer")
	destination := MyMindDestination{Kind: "workspace_thread", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: "thread_ball_dogs"}
	grant := fixture.putDisclosure(t, "disclosure_shared", source, destination, "shared_answer", "cite")

	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for attempt := 0; attempt < 100; attempt++ {
				_, _ = fixture.assemble(source, destination, "shared_answer", "cite")
			}
		}()
	}
	if err := fixture.service.RevokeDisclosure(fixture.custodyAuthority, grant.Header.ID, fixture.now.Add(2*time.Minute)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	workers.Wait()
	for attempt := 0; attempt < 25; attempt++ {
		selection, err := fixture.assemble(source, destination, "shared_answer", "cite")
		if err != nil || len(selection.Sources) != 0 {
			t.Fatalf("post-revoke assembly %d = %+v, err=%v", attempt, selection, err)
		}
	}
}

func TestPersonMyMindRecoveryDepartureExportAndDeleteAuthorities(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	portable := fixture.putSource(t, "source_receipt", "portable_receipt", "portable", fixture.membership.WorkspaceID, "portable_export", "private_answer")

	if _, err := fixture.service.RecoverAccount(fixture.custodyAuthority, fixture.person.Header.ID, strideTestDigest("9"), fixture.now.Add(time.Minute)); !errors.Is(err, ErrMyMindDenied) {
		t.Fatalf("custody implied recovery: %v", err)
	}
	otherPerson := fixture.person
	otherPerson.Header.ID = "person_recovery_collision"
	otherPerson.Header.ContentDigest = strideTestDigest("8")
	otherPerson.AccountSubjectDigest = strideTestDigest("8")
	if err := fixture.service.PutPerson(otherPerson); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RecoverAccount(fixture.recoveryAuthority, fixture.person.Header.ID, otherPerson.AccountSubjectDigest, fixture.now.Add(time.Minute)); !errors.Is(err, ErrMyMindConflict) {
		t.Fatalf("recovery accepted another person's account subject: %v", err)
	}
	recovered, err := fixture.service.RecoverAccount(fixture.recoveryAuthority, fixture.person.Header.ID, strideTestDigest("9"), fixture.now.Add(time.Minute))
	if err != nil || recovered.RecoveryRevision != 2 || recovered.CustodyRevision != 1 {
		t.Fatalf("recovery result = %+v, err=%v", recovered, err)
	}

	if _, err := fixture.service.Export(fixture.custodyAuthority, fixture.custodyAuthority, "export_receipt", fixture.person.Header.ID, fixture.membership.Header.ID, []MyMindSourceRef{portable.Ref()}, fixture.now.Add(time.Minute)); !errors.Is(err, ErrMyMindDenied) {
		t.Fatalf("custody implied organization export: %v", err)
	}
	exportDestination := MyMindDestination{Kind: "public_export", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: "export_receipt"}
	fixture.putDisclosure(t, "disclosure_export", portable, exportDestination, "portable_export", "export")
	receipt, err := fixture.service.Export(fixture.exportAuthority, fixture.custodyAuthority, "export_receipt", fixture.person.Header.ID, fixture.membership.Header.ID, []MyMindSourceRef{portable.Ref()}, fixture.now.Add(time.Minute))
	if err != nil || len(receipt.Sources) != 1 || receipt.OrganizationGrant == receipt.CustodyGrant {
		t.Fatalf("export result = %+v, err=%v", receipt, err)
	}

	if err := fixture.service.DeleteAccount(fixture.deleteAuthority, fixture.custodyAuthority, fixture.person.Header.ID, "missing_export", "deletion_missing", fixture.now.Add(2*time.Minute)); !errors.Is(err, ErrMyMindExportRequired) {
		t.Fatalf("delete accepted an explicitly named but nonexistent export = %v", err)
	}
	otherWorkspaceAuthority := MyMindAuthorityGrant{ID: "authority_other_membership", Authority: MyMindAuthorityWorkspaceMembership, ControllerID: "other_admin", WorkspaceID: "workspace_other", GrantedAt: fixture.now}
	if err := fixture.service.InstallAuthority(otherWorkspaceAuthority); err != nil {
		t.Fatal(err)
	}
	otherMembership := WorkspaceMembership{
		Header:   personMyMindHeader("workspace_other", STRIDEContractWorkspaceMembership, "membership_other_aj", 1, fixture.now.Add(time.Minute)),
		PersonID: fixture.person.Header.ID, WorkspaceID: "workspace_other", Role: "member", Status: "active", GrantedAt: fixture.now.Add(time.Minute),
	}
	otherAssertion := MyMindAuthorityAssertion{GrantID: otherWorkspaceAuthority.ID, ControllerID: otherWorkspaceAuthority.ControllerID}
	if err := fixture.service.PutMembership(otherAssertion, otherMembership); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteAccount(fixture.deleteAuthority, fixture.custodyAuthority, fixture.person.Header.ID, receipt.ID, "deletion_missing", fixture.now.Add(2*time.Minute)); !errors.Is(err, ErrMyMindCustodyDeletionRequired) {
		t.Fatalf("delete without custody deletion effect = %v", err)
	}
	deletedAt := fixture.now.Add(2 * time.Minute)
	effects := []MyMindCustodyDeletionEffect{{Source: portable.Ref(), CustodyRef: portable.CustodyRef, Effect: "body_destroyed", DeletedAt: deletedAt}}
	manifestDigest, err := myMindCustodyDeletionManifestDigest(effects)
	if err != nil {
		t.Fatal(err)
	}
	deletionReceipt := MyMindCustodyDeletionReceipt{
		Header:   personMyMindHeader(myMindVaultTenant(fixture.person.Header.ID), STRIDEContractMyMindCustodyDeletion, "deletion_receipt", 1, deletedAt),
		PersonID: fixture.person.Header.ID, AuthorityGrantID: fixture.custodyAuthority.GrantID, SourceCount: len(effects),
		SourceManifestDigest: manifestDigest, ExternalEvidenceDigest: strideTestDigest("e"), Effects: effects, RecordedAt: deletedAt,
	}
	deletionReceipt.Header.ContentDigest = manifestDigest
	wrongReceipt := deletionReceipt
	wrongReceipt.Header.ID = "deletion_wrong"
	wrongReceipt.Effects = append([]MyMindCustodyDeletionEffect(nil), deletionReceipt.Effects...)
	wrongReceipt.Effects[0].CustodyRef = "custody_wrong"
	wrongDigest, _ := myMindCustodyDeletionManifestDigest(wrongReceipt.Effects)
	wrongReceipt.SourceManifestDigest, wrongReceipt.Header.ContentDigest = wrongDigest, wrongDigest
	if err := fixture.service.RecordCustodyDeletion(fixture.custodyAuthority, wrongReceipt); !errors.Is(err, ErrMyMindCustodyDeletionRequired) {
		t.Fatalf("mismatched custody deletion effect = %v", err)
	}
	if err := fixture.service.RecordCustodyDeletion(fixture.custodyAuthority, deletionReceipt); err != nil {
		t.Fatalf("record custody deletion: %v", err)
	}
	if err := fixture.service.DeleteAccount(fixture.deleteAuthority, fixture.custodyAuthority, fixture.person.Header.ID, receipt.ID, deletionReceipt.Header.ID, fixture.now.Add(3*time.Minute)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	tombstone, err := fixture.service.PersonTombstone(fixture.deleteAuthority, fixture.person.Header.ID, fixture.now.Add(3*time.Minute))
	if err != nil || tombstone.Status != "deleted" || tombstone.DeletedAt == nil {
		t.Fatalf("person tombstone = %+v, err=%v", tombstone, err)
	}
	selection, err := fixture.assemble(portable, MyMindDestination{Kind: "private_person", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: fixture.person.Header.ID}, "private_answer", "personalize")
	if !errors.Is(err, ErrMyMindDenied) || len(selection.Sources) != 0 {
		t.Fatalf("deleted account leaked context: %+v, err=%v", selection, err)
	}
}

func TestPersonMyMindDeleteDoesNotForcePortableExport(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	recordedAt := fixture.now.Add(time.Minute)
	manifestDigest, err := myMindCustodyDeletionManifestDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := MyMindCustodyDeletionReceipt{
		Header:   personMyMindHeader(myMindVaultTenant(fixture.person.Header.ID), STRIDEContractMyMindCustodyDeletion, "deletion_empty_vault", 1, recordedAt),
		PersonID: fixture.person.Header.ID, AuthorityGrantID: fixture.custodyAuthority.GrantID, SourceCount: 0,
		SourceManifestDigest: manifestDigest, ExternalEvidenceDigest: strideTestDigest("f"), RecordedAt: recordedAt,
	}
	receipt.Header.ContentDigest = manifestDigest
	if err := fixture.service.RecordCustodyDeletion(fixture.custodyAuthority, receipt); err != nil {
		t.Fatalf("record empty-vault deletion evidence: %v", err)
	}
	if err := fixture.service.DeleteAccount(fixture.deleteAuthority, fixture.custodyAuthority, fixture.person.Header.ID, "", receipt.Header.ID, recordedAt.Add(time.Minute)); err != nil {
		t.Fatalf("delete independently of optional export: %v", err)
	}
}

func TestPersonMyMindDepartureRevokesWorkspaceButPreservesPortableMyMind(t *testing.T) {
	fixture := newPersonMyMindFixture(t)
	private := fixture.putSource(t, "source_private_continuity", "preference", "private", "", "private_answer")
	shared := fixture.putSource(t, "source_shared_continuity", "correction", "private", "", "shared_answer")
	sharedDestination := MyMindDestination{Kind: "workspace_thread", WorkspaceID: fixture.membership.WorkspaceID, AudienceID: "thread_ball_dogs"}
	fixture.putDisclosure(t, "disclosure_departure", shared, sharedDestination, "shared_answer", "cite")

	if err := fixture.service.Depart(fixture.departureAuthority, fixture.membership.Header.ID, fixture.now.Add(time.Minute)); err != nil {
		t.Fatalf("depart: %v", err)
	}
	if selection, err := fixture.assemble(shared, sharedDestination, "shared_answer", "cite"); !errors.Is(err, ErrMyMindDenied) || len(selection.Sources) != 0 {
		t.Fatalf("departed workspace retained access: %+v, err=%v", selection, err)
	}

	freelanceAuthority := MyMindAuthorityGrant{ID: "authority_freelance_membership", Authority: MyMindAuthorityWorkspaceMembership, ControllerID: "person_aj", WorkspaceID: "workspace_personal", GrantedAt: fixture.now}
	if err := fixture.service.InstallAuthority(freelanceAuthority); err != nil {
		t.Fatalf("install freelance authority: %v", err)
	}
	personal := WorkspaceMembership{
		Header:   personMyMindHeader("workspace_personal", STRIDEContractWorkspaceMembership, "membership_personal_aj", 1, fixture.now.Add(2*time.Minute)),
		PersonID: fixture.person.Header.ID, WorkspaceID: "workspace_personal", Role: "freelance", Status: "active", GrantedAt: fixture.now.Add(2 * time.Minute),
	}
	if err := fixture.service.PutMembership(MyMindAuthorityAssertion{GrantID: freelanceAuthority.ID, ControllerID: freelanceAuthority.ControllerID}, personal); err != nil {
		t.Fatalf("put personal membership: %v", err)
	}
	selection, err := fixture.service.Assemble(MyMindAssembleRequest{
		PersonID: fixture.person.Header.ID, MembershipID: personal.Header.ID, MembershipRevision: 1, WorkspaceID: personal.WorkspaceID,
		Destination: MyMindDestination{Kind: "private_person", WorkspaceID: personal.WorkspaceID, AudienceID: fixture.person.Header.ID},
		Purpose:     "private_answer", Modes: []string{"personalize"}, Candidates: []MyMindSourceRef{private.Ref()}, At: fixture.now.Add(3 * time.Minute),
	})
	if err != nil || len(selection.Sources) != 1 {
		t.Fatalf("portable private MyMind did not survive departure: %+v, err=%v", selection, err)
	}
}

func TestPersonMyMindContractTypesAndFeatureRemainFenced(t *testing.T) {
	for _, kind := range []STRIDEContractType{STRIDEContractPersonPrincipal, STRIDEContractWorkspaceMembership, STRIDEContractMyMindSource, STRIDEContractMyMindDisclosureGrant, STRIDEContractMyMindCustodyDeletion} {
		if !validSTRIDEContractType(kind) {
			t.Fatalf("contract type %s is not closed into structured references", kind)
		}
	}
	registry := NewSTRIDERegistry()
	if err := registry.SetFeatureEnabled(STRIDEFeaturePersonMyMindContext, true); !errors.Is(err, ErrSTRIDEActivationFenced) {
		t.Fatalf("person/MyMind feature activation = %v", err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range snapshot.Features {
		if feature.Feature == STRIDEFeaturePersonMyMindContext && feature.Enabled {
			t.Fatal("person/MyMind context enabled by default")
		}
	}
}

func TestPostgresPersonMyMindMigrationIsDefaultOffAndRejectsWidening(t *testing.T) {
	ctx, store, _ := migratedPostgresCanonicalStore(t)
	var enabled bool
	if err := store.pool.QueryRow(ctx, "SELECT enabled FROM stride_feature_switches WHERE feature_key='person_mymind_context'").Scan(&enabled); err != nil || enabled {
		t.Fatalf("person/MyMind feature enabled=%t err=%v", enabled, err)
	}
	digest := strideTestDigest("a")
	structured := fmt.Sprintf(`[{"contractType":"mymind_source","id":"source_1","revision":1,"digest":"%s"}]`, digest)
	var valid bool
	if err := store.pool.QueryRow(ctx, "SELECT stride_structured_refs_are_valid($1::jsonb)", structured).Scan(&valid); err != nil || !valid {
		t.Fatalf("new body-free structured ref valid=%t err=%v", valid, err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals
		(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at)
		VALUES ('person_aj',1,decode($1,'hex'),'active',1,1,now())`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals
		(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at)
		VALUES ('person_duplicate',1,decode($1,'hex'),'active',1,1,now())`, digest); err == nil {
		t.Fatal("database accepted duplicate account subject digest")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_person_principals
		(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at)
		VALUES ('person_other',1,decode($1,'hex'),'active',1,1,now())`, strideTestDigest("c")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_workspace_memberships
		(membership_id,workspace_id,person_id,revision,role,status,granted_at)
		VALUES ('membership_aj','workspace_bonfire','person_aj',1,'owner','active',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_sources
		(person_id,source_id,revision,content_digest,source_kind,bound_workspace_id,confidentiality,custody_ref,allowed_purposes,consent_revision,consent_status,created_at)
		VALUES ('person_aj','source_1',1,decode($1,'hex'),'portable_receipt','workspace_bonfire','portable','custody_source_1',ARRAY['shared_answer','portable_export'],1,'granted',now())`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_disclosure_grants
		(grant_id,revision,person_id,membership_id,membership_revision,source_id,source_revision,source_consent_revision,source_digest,
		 workspace_id,destination_kind,destination_audience_id,purpose,modes,status,granted_at)
		VALUES ('org_wide',1,'person_aj','membership_aj',1,'source_1',1,1,decode($1,'hex'),
		'workspace_bonfire','workspace_channel','organization','shared_answer',ARRAY['cite'],'active',now())`, digest); err == nil {
		t.Fatal("database accepted an organization-wide MyMind disclosure")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_disclosure_grants
		(grant_id,revision,person_id,membership_id,membership_revision,source_id,source_revision,source_consent_revision,source_digest,
		 workspace_id,destination_kind,destination_audience_id,purpose,modes,status,granted_at)
		VALUES ('wrong_membership_revision',1,'person_aj','membership_aj',2,'source_1',1,1,decode($1,'hex'),
		'workspace_bonfire','workspace_thread','thread_ball_dogs','shared_answer',ARRAY['cite'],'active',now())`, digest); err == nil {
		t.Fatal("database accepted disclosure against an unbound membership revision")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_disclosure_grants
		(grant_id,revision,person_id,membership_id,membership_revision,source_id,source_revision,source_consent_revision,source_digest,
		 workspace_id,destination_kind,destination_audience_id,purpose,modes,status,granted_at)
		VALUES ('wrong_membership_workspace',1,'person_aj','membership_aj',1,'source_1',1,1,decode($1,'hex'),
		'workspace_other','workspace_thread','thread_ball_dogs','shared_answer',ARRAY['cite'],'active',now())`, digest); err == nil {
		t.Fatal("database accepted disclosure against another workspace")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_disclosure_grants
		(grant_id,revision,person_id,membership_id,membership_revision,source_id,source_revision,source_consent_revision,source_digest,
		 workspace_id,destination_kind,destination_audience_id,purpose,modes,status,granted_at)
		VALUES ('wrong_membership_person',1,'person_other','membership_aj',1,'source_1',1,1,decode($1,'hex'),
		'workspace_bonfire','workspace_thread','thread_ball_dogs','shared_answer',ARRAY['cite'],'active',now())`, digest); err == nil {
		t.Fatal("database accepted disclosure against another person")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_disclosure_grants
		(grant_id,revision,person_id,membership_id,membership_revision,source_id,source_revision,source_consent_revision,source_digest,
		 workspace_id,destination_kind,destination_audience_id,purpose,modes,status,granted_at)
		VALUES ('wrong_source_consent',1,'person_aj','membership_aj',1,'source_1',1,2,decode($1,'hex'),
		'workspace_bonfire','workspace_thread','thread_ball_dogs','shared_answer',ARRAY['cite'],'active',now())`, digest); err == nil {
		t.Fatal("database accepted disclosure against an unbound source consent revision")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_disclosure_grants
		(grant_id,revision,person_id,membership_id,membership_revision,source_id,source_revision,source_consent_revision,source_digest,
		 workspace_id,destination_kind,destination_audience_id,purpose,modes,status,granted_at)
		VALUES ('wrong_source_digest',1,'person_aj','membership_aj',1,'source_1',1,1,decode($1,'hex'),
		'workspace_bonfire','workspace_thread','thread_ball_dogs','shared_answer',ARRAY['cite'],'active',now())`, strideTestDigest("b")); err == nil {
		t.Fatal("database accepted disclosure against an unbound source digest")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_authority_grants
		(authority_grant_id,authority,controller_id,person_id,workspace_id,granted_at) VALUES
		('authority_export','organization_export','workspace_admin',NULL,'workspace_bonfire',now()),
		('authority_export_other','organization_export','other_admin',NULL,'workspace_other',now()),
		('authority_custody','mymind_custody','person_aj','person_aj',NULL,now()),
		('authority_custody_second','mymind_custody','person_aj','person_aj',NULL,now()),
		('authority_custody_other','mymind_custody','person_other','person_other',NULL,now())`); err != nil {
		t.Fatal(err)
	}
	refs := fmt.Sprintf(`[{"personId":"person_aj","sourceId":"source_1","revision":1,"consentRevision":1,"digest":"%s"}]`, digest)
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_export_receipts
		(export_receipt_id,person_id,workspace_id,organization_authority_grant_id,custody_authority_grant_id,source_refs,created_at)
		VALUES ('export_wrong_type','person_aj','workspace_bonfire','authority_custody_second','authority_custody',$1::jsonb,now())`, refs); err == nil {
		t.Fatal("database accepted custody authority as organization export authority")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_export_receipts
		(export_receipt_id,person_id,workspace_id,organization_authority_grant_id,custody_authority_grant_id,source_refs,created_at)
		VALUES ('export_wrong_scope','person_aj','workspace_bonfire','authority_export_other','authority_custody',$1::jsonb,now())`, refs); err == nil {
		t.Fatal("database accepted organization export authority from another workspace")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_export_receipts
		(export_receipt_id,person_id,workspace_id,organization_authority_grant_id,custody_authority_grant_id,source_refs,created_at)
		VALUES ('export_wrong_custody_scope','person_aj','workspace_bonfire','authority_export','authority_custody_other',$1::jsonb,now())`, refs); err == nil {
		t.Fatal("database accepted MyMind custody authority from another person")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_export_receipts
		(export_receipt_id,person_id,workspace_id,organization_authority_grant_id,custody_authority_grant_id,source_refs,created_at)
		VALUES ('export_valid','person_aj','workspace_bonfire','authority_export','authority_custody',$1::jsonb,now())`, refs); err != nil {
		t.Fatalf("valid scoped export receipt: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_person_principals
		SET status='deleted',deleted_at=now(),custody_deletion_receipt_id='deletion_missing' WHERE person_id='person_aj'`); err == nil {
		t.Fatal("database allowed deletion without a custody deletion receipt")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_custody_deletion_receipts
		(deletion_receipt_id,person_id,custody_authority_grant_id,source_count,source_manifest_digest,external_evidence_digest,recorded_at)
		VALUES ('deletion_wrong_authority','person_aj','authority_export',1,decode($1,'hex'),decode($1,'hex'),now())`, digest); err == nil {
		t.Fatal("database accepted export authority as custody deletion authority")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_custody_deletion_receipts
		(deletion_receipt_id,person_id,custody_authority_grant_id,source_count,source_manifest_digest,external_evidence_digest,recorded_at)
		VALUES ('deletion_valid','person_aj','authority_custody',1,decode($1,'hex'),decode($1,'hex'),now())`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_custody_deletion_items
		(deletion_receipt_id,person_id,source_id,source_revision,source_consent_revision,source_digest,custody_ref,effect,deleted_at)
		VALUES ('deletion_valid','person_aj','source_1',2,1,decode($1,'hex'),'custody_source_1','body_destroyed',now())`, digest); err == nil {
		t.Fatal("database accepted deletion effect for a nonexistent source revision")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_custody_deletion_items
		(deletion_receipt_id,person_id,source_id,source_revision,source_consent_revision,source_digest,custody_ref,effect,deleted_at)
		VALUES ('deletion_valid','person_aj','source_1',1,1,decode($1,'hex'),'custody_source_1','body_destroyed',now())`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE stride_person_principals
		SET status='deleted',deleted_at=now(),custody_deletion_receipt_id='deletion_valid' WHERE person_id='person_aj'`); err != nil {
		t.Fatalf("exact custody deletion receipt did not open deletion gate: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO stride_mymind_sources
		(person_id,source_id,revision,content_digest,source_kind,confidentiality,custody_ref,allowed_purposes,consent_revision,consent_status,created_at)
		VALUES ('person_aj','source_after_delete',1,decode($1,'hex'),'preference','private','custody_after_delete',ARRAY['private_answer'],1,'granted',now())`, digest); err == nil {
		t.Fatal("database accepted a new custody source after the exact deletion manifest")
	}
	malformedRef := fmt.Sprintf(`[{"personId":"person_aj","sourceId":"source_1","revision":1,"consentRevision":1,"digest":"%s","body":"private text"}]`, digest)
	if err := store.pool.QueryRow(ctx, "SELECT stride_mymind_source_refs_are_valid($1::jsonb)", malformedRef).Scan(&valid); err != nil || valid {
		t.Fatalf("body-bearing MyMind source ref valid=%t err=%v", valid, err)
	}
}
