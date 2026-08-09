package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var strideE10W4ActivateNetworkFlag = flag.Bool("stride-e10-w4-activate-network", false, "activate Bonfire organization sessions and the STRIDE network, then exit")
var strideE10W4RollbackNetworkFlag = flag.Bool("stride-e10-w4-rollback-network", false, "verify and roll back the last STRIDE W4 network activation, then exit")
var strideE10W4VerifyNetworkActivationFlag = flag.Bool("stride-e10-w4-verify-network-activation", false, "verify the committed STRIDE W4 network activation against current release and state, then exit")
var strideE10W4VerifyNetworkRuntimeFlag = flag.Bool("stride-e10-w4-verify-network-runtime", false, "verify an evolved STRIDE W4 live runtime against its authenticated activation lineage, then exit")
var strideE10W4VerifyNetworkRollbackFlag = flag.Bool("stride-e10-w4-verify-network-rollback", false, "verify that the committed STRIDE W4 network activation was exactly rolled back, then exit")

const (
	strideE10W4NetworkMode              = "bonfire_network_live"
	strideE10W4ActivationBackupDirEnv   = "STRIDE_E10_W4_ACTIVATION_BACKUP_DIR"
	strideE10W4ActivationReceiptPathEnv = "STRIDE_E10_W4_ACTIVATION_RECEIPT_PATH"
)

var strideE10W4NetworkFeatures = []STRIDEFeature{
	STRIDEFeaturePersonProfileAuthority,
	STRIDEFeatureOrganizationAuthorityRead,
	STRIDEFeatureOrganizationAuthorityWrite,
	STRIDEFeatureActiveOrganizationSession,
	STRIDEFeatureContributionReview,
	STRIDEFeatureWorkRecordPrivate,
}

type strideE10W4ActivationReceipt struct {
	Schema                  string   `json:"schema"`
	ActivationID            string   `json:"activationId"`
	ReleaseCommit           string   `json:"releaseCommit"`
	ActivationReceiptDigest string   `json:"activationReceiptDigest"`
	SnapshotGeneration      uint64   `json:"snapshotGeneration"`
	OrganizationCount       int      `json:"organizationCount"`
	PersonCount             int      `json:"personCount"`
	MembershipCount         int      `json:"membershipCount"`
	BoundMemberSessions     int      `json:"boundMemberSessions"`
	PreservedGuestSessions  int      `json:"preservedGuestSessions"`
	ContributionGrantCount  int      `json:"contributionGrantCount"`
	NetworkDraftCount       int      `json:"networkDraftCount"`
	RecruiterGrantCount     int      `json:"recruiterGrantCount"`
	EnabledFeatures         []string `json:"enabledFeatures"`
	ActivatedAt             string   `json:"activatedAt"`
	SnapshotDigest          string   `json:"snapshotDigest"`
	SessionsDigest          string   `json:"sessionsDigest"`
}

type strideE10W4ActivationPaths struct {
	Snapshot, Sessions, BackupDir, Receipt, Journal string
}

type strideE10W4ActivationPhase string

const (
	strideE10W4ActivationPrepared       strideE10W4ActivationPhase = "prepared"
	strideE10W4ActivationSessions       strideE10W4ActivationPhase = "sessions"
	strideE10W4ActivationSnapshot       strideE10W4ActivationPhase = "snapshot"
	strideE10W4ActivationReceiptWritten strideE10W4ActivationPhase = "receipt"
	strideE10W4ActivationCommitted      strideE10W4ActivationPhase = "committed"
	strideE10W4RollbackStarted          strideE10W4ActivationPhase = "rollback_started"
	strideE10W4RollbackSessions         strideE10W4ActivationPhase = "sessions_restored"
	strideE10W4RollbackSnapshot         strideE10W4ActivationPhase = "snapshot_restored"
	strideE10W4RolledBack               strideE10W4ActivationPhase = "rolled_back"
)

type strideE10W4ActivationJournal struct {
	Schema                  string                     `json:"schema"`
	ActivationID            string                     `json:"activationId"`
	ReleaseCommit           string                     `json:"releaseCommit"`
	Phase                   strideE10W4ActivationPhase `json:"phase"`
	SnapshotPath            string                     `json:"snapshotPath"`
	SessionsPath            string                     `json:"sessionsPath"`
	ReceiptPath             string                     `json:"receiptPath"`
	SnapshotBackupPath      string                     `json:"snapshotBackupPath"`
	SessionsBackupPath      string                     `json:"sessionsBackupPath"`
	SourceGeneration        uint64                     `json:"sourceGeneration"`
	TargetGeneration        uint64                     `json:"targetGeneration"`
	OriginalSnapshotDigest  string                     `json:"originalSnapshotDigest"`
	OriginalSessionsDigest  string                     `json:"originalSessionsDigest"`
	TargetSnapshotDigest    string                     `json:"targetSnapshotDigest"`
	TargetSessionsDigest    string                     `json:"targetSessionsDigest"`
	ReceiptDigest           string                     `json:"receiptDigest"`
	ActivationReceiptDigest string                     `json:"activationReceiptDigest"`
	ActivatedAt             string                     `json:"activatedAt"`
}

type strideE10W4AuthenticatedArtifact struct {
	Schema     string          `json:"schema"`
	KeyID      string          `json:"keyId"`
	KeyVersion uint64          `json:"keyVersion"`
	Payload    json.RawMessage `json:"payload"`
	MAC        string          `json:"mac"`
}

func strideE10W4ActivationMAC(key StrideE10MigrationMACKey, domain string, payload []byte) string {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = io.WriteString(mac, "meetingassist/stride/e10/w4-activation/"+domain+"/v1\x00")
	_, _ = mac.Write(payload)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func strideE10W4EncodeAuthenticatedArtifact(schema, domain string, value any, keyring *strideE10W4Keyring) ([]byte, error) {
	if keyring == nil || validateStrideE10MigrationMACKey(keyring.key) != nil {
		return nil, ErrStrideE10Invalid
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	envelope := strideE10W4AuthenticatedArtifact{Schema: schema, KeyID: keyring.key.ID, KeyVersion: keyring.key.Version, Payload: payload, MAC: strideE10W4ActivationMAC(keyring.key, domain, payload)}
	return json.MarshalIndent(envelope, "", "  ")
}

func strideE10W4DecodeAuthenticatedArtifact(body []byte, schema, domain string, keyring *strideE10W4Keyring, value any) error {
	if keyring == nil || value == nil {
		return ErrStrideE10Invalid
	}
	var envelope strideE10W4AuthenticatedArtifact
	var compact bytes.Buffer
	if json.Unmarshal(body, &envelope) != nil || json.Compact(&compact, envelope.Payload) != nil || envelope.Schema != schema || envelope.KeyID != keyring.key.ID || envelope.KeyVersion != keyring.key.Version || !hmac.Equal([]byte(envelope.MAC), []byte(strideE10W4ActivationMAC(keyring.key, domain, compact.Bytes()))) || json.Unmarshal(envelope.Payload, value) != nil {
		return ErrStrideE10Denied
	}
	return nil
}

func strideE10W4InitializeActivationMaps(snapshot *strideE10W4RuntimeSnapshot) {
	if snapshot.Organization.Sessions == nil {
		snapshot.Organization.Sessions = map[string]ActiveOrganizationSession{}
	}
	if snapshot.Contribution.Grants == nil {
		snapshot.Contribution.Grants = map[string]ContributionAuthorityGrant{}
	}
	if snapshot.Network.Profiles == nil {
		snapshot.Network.Profiles = map[string]NetworkProfileProjection{}
	}
	if snapshot.Network.Grants == nil {
		snapshot.Network.Grants = map[string]TalentSearchGrant{}
	}
	if snapshot.Network.CapabilityAuthorities == nil {
		snapshot.Network.CapabilityAuthorities = map[string]TalentSearchCapabilityAuthority{}
	}
	if snapshot.Network.MembershipAuthorities == nil {
		snapshot.Network.MembershipAuthorities = map[string]NetworkMembershipAuthority{}
	}
	if snapshot.Network.ProfileVersions == nil {
		snapshot.Network.ProfileVersions = map[string]NetworkProfileProjection{}
	}
	if snapshot.Network.GrantVersions == nil {
		snapshot.Network.GrantVersions = map[string]TalentSearchGrant{}
	}
}

func strideE10W4ActivateSnapshot(snapshot strideE10W4RuntimeSnapshot, sessions map[string]sessionRecord, at time.Time) (strideE10W4RuntimeSnapshot, map[string]sessionRecord, strideE10W4ActivationReceipt, error) {
	at = at.UTC()
	if at.IsZero() || len(snapshot.Organization.Persons) != 7 || len(snapshot.Organization.Organizations) != 1 || len(snapshot.Organization.Memberships) != 7 {
		return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
	}
	strideE10W4InitializeActivationMaps(&snapshot)
	membershipsByPerson := map[string][]OrganizationMembership{}
	var owner OrganizationMembership
	for _, membership := range snapshot.Organization.Memberships {
		if membership.Validate() != nil || membership.Status != "active" {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		membershipsByPerson[membership.PersonID] = append(membershipsByPerson[membership.PersonID], membership)
		if membership.Role == "owner" {
			if owner.Header.ID != "" {
				return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
			}
			owner = membership
		}
		snapshot.Network.MembershipAuthorities[membership.Header.ID] = NetworkMembershipAuthority{MembershipID: membership.Header.ID, OrganizationID: membership.OrganizationID, PersonID: membership.PersonID, Revision: membership.Header.Revision, Active: true}
	}
	if owner.Header.ID == "" {
		return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
	}

	grant := func(role, personID, organizationID, principalID string) ContributionAuthorityGrant {
		seed := role + "\x00" + personID + "\x00" + organizationID
		id := "grant_" + sha256Hex([]byte(seed))[:24]
		return ContributionAuthorityGrant{ID: id, Role: role, PersonID: personID, OrganizationID: organizationID, Controller: STRIDEControllerRevision{PrincipalID: principalID, AuthorityID: "authority_" + sha256Hex([]byte(seed))[:24], AuthorityRevision: 1, PolicyRevision: 1}}
	}
	personIDs := make([]string, 0, len(snapshot.Organization.Persons))
	for personID := range snapshot.Organization.Persons {
		personIDs = append(personIDs, personID)
	}
	sort.Strings(personIDs)
	for _, personID := range personIDs {
		if len(membershipsByPerson[personID]) != 1 {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
		}
		for _, role := range []string{"subject", "person_publisher"} {
			candidate := grant(role, personID, "", personID)
			if candidate.Validate() != nil {
				return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
			}
			snapshot.Contribution.Grants[candidate.ID] = candidate
		}
		publisher := grant("person_publisher", personID, "", personID)
		profile := snapshot.Organization.Profiles[personID]
		if profile.Validate() != nil || strings.TrimSpace(profile.DisplayName) == "" {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		visible, _ := json.Marshal(profile.DisplayName)
		fields := []NetworkPublishedField{{FieldKey: "display_name", ValueDigest: sha256Hex(visible), VisibleValue: visible, EvidenceLabel: "self_described"}}
		fieldsDigest, _ := STRIDEContractDigest(fields)
		profileID := "network_profile_" + sha256Hex([]byte(personID + "\x00network-profile"))[:24]
		networkProfile := NetworkProfileProjection{Header: strideE10LiveHeader(STRIDEContractNetworkProfileProjection, STRIDEGlobalPersonTenant, profileID, 1, personID+"\x00network-profile", at), SubjectPersonID: personID, Fields: fields, FieldsDigest: fieldsDigest, Discoverability: "unlisted", Controller: publisher.Controller, State: "draft", StateChangedAt: at}
		if networkProfile.Validate() != nil {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		if current, exists := snapshot.Network.Profiles[profileID]; !exists {
			snapshot.Network.Profiles[profileID] = networkProfile
			snapshot.Network.ProfileVersions[networkVersionKey(profileID, 1)] = networkProfile
		} else if current.SubjectPersonID != personID || current.Controller != publisher.Controller {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
		}
	}
	for _, role := range []string{"organization_reviewer", "signing_issuer", "outcome_reviewer", "drift_controller"} {
		candidate := grant(role, "", owner.OrganizationID, owner.PersonID)
		if candidate.Validate() != nil {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		snapshot.Contribution.Grants[candidate.ID] = candidate
	}

	capabilityID := "talent_capability_" + sha256Hex([]byte(owner.OrganizationID + "\x00" + owner.PersonID))[:24]
	capability := TalentSearchCapabilityAuthority{ID: capabilityID, Revision: 1, OrganizationID: owner.OrganizationID, ControllerPersonID: owner.PersonID, MembershipID: owner.Header.ID, MembershipRevision: owner.Header.Revision, PolicyRevision: 1, Active: true}
	if capability.validate() != nil {
		return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
	}
	snapshot.Network.CapabilityAuthorities[capability.ID] = capability
	grantID := "talent_grant_" + sha256Hex([]byte(owner.OrganizationID + "\x00" + owner.PersonID))[:24]
	talentGrant, exists := snapshot.Network.Grants[grantID]
	if !exists {
		talentGrant = TalentSearchGrant{Header: strideE10LiveHeader(STRIDEContractTalentSearchGrant, owner.OrganizationID, grantID, 1, owner.PersonID+"\x00talent-search", at), OrganizationID: owner.OrganizationID, MembershipID: owner.Header.ID, MembershipRevision: owner.Header.Revision, SearcherPersonID: owner.PersonID, CapabilityAdministrator: STRIDEControllerRevision{PrincipalID: owner.PersonID, AuthorityID: capability.ID, AuthorityRevision: capability.Revision, PolicyRevision: capability.PolicyRevision}, PolicyRevision: 1, State: "active", GrantedAt: at, ExpiresAt: at.Add(366 * 24 * time.Hour)}
	} else if talentGrant.OrganizationID != owner.OrganizationID || talentGrant.MembershipID != owner.Header.ID || talentGrant.SearcherPersonID != owner.PersonID || talentGrant.CapabilityAdministrator.AuthorityID != capability.ID || talentGrant.State != "active" {
		return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
	}
	if talentGrant.Validate() != nil {
		return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
	}
	snapshot.Network.Grants[talentGrant.Header.ID] = talentGrant
	snapshot.Network.GrantVersions[networkVersionKey(talentGrant.Header.ID, talentGrant.Header.Revision)] = talentGrant

	updatedSessions := make(map[string]sessionRecord, len(sessions))
	bound, guests := 0, 0
	for hash, record := range sessions {
		if record.Kind != "" {
			updatedSessions[hash] = record
			guests++
			continue
		}
		if !at.Before(record.Expires) {
			updatedSessions[hash] = record
			continue
		}
		memberships := membershipsByPerson[record.PersonID]
		if len(memberships) != 1 || !isHexDigest(hash) {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		membership := memberships[0]
		changed := record.ActiveOrganizationID != membership.OrganizationID || record.OrganizationMembershipID != membership.Header.ID || record.OrganizationMembershipRev != membership.Header.Revision || record.ActiveOrganizationSessionRev < 1
		if changed {
			record.ActiveOrganizationID, record.OrganizationMembershipID = membership.OrganizationID, membership.Header.ID
			record.OrganizationMembershipRev, record.ActiveOrganizationSessionRev = membership.Header.Revision, 1
			record.AuthorityGeneration++
			if record.AuthorityGeneration == 0 {
				record.AuthorityGeneration = 1
			}
		}
		active, exists := snapshot.Organization.Sessions[hash]
		if !exists || changed {
			seed := record.PersonID + "\x00" + membership.OrganizationID + "\x00" + hash
			active = ActiveOrganizationSession{Header: strideE10LiveHeader(STRIDEContractActiveOrganizationSession, STRIDEGlobalPersonTenant, "active_session_"+hash[:24], record.ActiveOrganizationSessionRev, seed, at), SessionSubjectDigest: hash, PersonID: record.PersonID, OrganizationID: membership.OrganizationID, MembershipID: membership.Header.ID, MembershipRevision: membership.Header.Revision, SessionRevision: record.ActiveOrganizationSessionRev, Status: "active", BoundAt: at, ExpiresAt: record.Expires.UTC()}
		} else if active.PersonID != record.PersonID || active.OrganizationID != membership.OrganizationID || active.MembershipID != membership.Header.ID || active.MembershipRevision != membership.Header.Revision || active.SessionRevision != record.ActiveOrganizationSessionRev || active.Status != "active" || !active.ExpiresAt.Equal(record.Expires.UTC()) {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
		}
		if active.Validate() != nil {
			return strideE10W4RuntimeSnapshot{}, nil, strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		snapshot.Organization.Sessions[hash] = active
		updatedSessions[hash] = record
		bound++
	}
	receipt := strideE10W4ActivationReceipt{Schema: "stride.e10.w4.network-activation.v1", OrganizationCount: len(snapshot.Organization.Organizations), PersonCount: len(snapshot.Organization.Persons), MembershipCount: len(snapshot.Organization.Memberships), BoundMemberSessions: bound, PreservedGuestSessions: guests, ContributionGrantCount: len(snapshot.Contribution.Grants), NetworkDraftCount: len(snapshot.Network.Profiles), RecruiterGrantCount: len(snapshot.Network.Grants), ActivatedAt: at.Format(time.RFC3339)}
	for _, feature := range strideE10W4NetworkFeatures {
		receipt.EnabledFeatures = append(receipt.EnabledFeatures, string(feature))
	}
	return snapshot, updatedSessions, receipt, nil
}

func runStrideE10W4NetworkActivationCLI(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("STRIDE W4 network activation must run as root")
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	paths, err := strideE10W4ActivationPathsFromEnvironment()
	if err != nil {
		return err
	}
	receipt, err := strideE10W4RunActivation(ctx, paths, keyring, time.Now().UTC(), strideE10W4ActivationCommitted)
	if err != nil {
		return err
	}
	userSessionStore().mu.Lock()
	sessionsBody, readErr := os.ReadFile(paths.Sessions)
	var updatedSessions map[string]sessionRecord
	if readErr != nil || json.Unmarshal(sessionsBody, &updatedSessions) != nil {
		userSessionStore().mu.Unlock()
		return ErrStrideE10Invalid
	}
	userSessionStore().sessions = updatedSessions
	userSessionStore().mu.Unlock()
	fmt.Fprintf(os.Stdout, "{\"schema\":%q,\"activationId\":%q,\"releaseCommit\":%q,\"snapshotGeneration\":%d,\"boundMemberSessions\":%d,\"networkDrafts\":%d,\"receipt\":%q}\n", receipt.Schema, receipt.ActivationID, receipt.ReleaseCommit, receipt.SnapshotGeneration, receipt.BoundMemberSessions, receipt.NetworkDraftCount, paths.Receipt)
	return nil
}

func runStrideE10W4NetworkRollbackCLI(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("STRIDE W4 network rollback must run as root")
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	paths, err := strideE10W4ActivationPathsFromEnvironment()
	if err != nil {
		return err
	}
	return strideE10W4RollbackActivation(ctx, paths, keyring)
}

func runStrideE10W4NetworkActivationVerifyCLI(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrStrideE10Invalid
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	paths, err := strideE10W4ActivationPathsFromEnvironment()
	if err != nil {
		return err
	}
	releaseCommit := strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT"))
	if !releaseCommitPattern.MatchString(releaseCommit) {
		return ErrStrideE10Invalid
	}
	if err := strideE10W4VerifyCommittedActivation(paths, keyring, releaseCommit); err != nil {
		return err
	}
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "{\"schema\":\"stride.e10.w4.activation-verification.v1\",\"activationId\":%q,\"releaseCommit\":%q,\"snapshotGeneration\":%d,\"status\":\"committed\"}\n", journal.ActivationID, journal.ReleaseCommit, journal.TargetGeneration)
	return nil
}

func strideE10W4VerifySuccessorRuntime(paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, candidateReleaseCommit string) (strideE10W4SnapshotEnvelope, strideE10W4ActivationJournal, error) {
	if !releaseCommitPattern.MatchString(strings.TrimSpace(candidateReleaseCommit)) {
		return strideE10W4SnapshotEnvelope{}, strideE10W4ActivationJournal{}, ErrStrideE10Invalid
	}
	envelope, err := strideE10W4VerifyLiveActivationLineage(paths, keyring)
	if err != nil {
		return strideE10W4SnapshotEnvelope{}, strideE10W4ActivationJournal{}, err
	}
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err != nil || journal.Phase != strideE10W4ActivationCommitted || envelope.ActivationID != journal.ActivationID || envelope.ActivationReceiptDigest != journal.ActivationReceiptDigest {
		return strideE10W4SnapshotEnvelope{}, strideE10W4ActivationJournal{}, ErrStrideE10Denied
	}
	return envelope, journal, nil
}

func runStrideE10W4NetworkRuntimeVerifyCLI(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrStrideE10Invalid
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	paths, err := strideE10W4ActivationPathsFromEnvironment()
	if err != nil {
		return err
	}
	candidateReleaseCommit := strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT"))
	envelope, journal, err := strideE10W4VerifySuccessorRuntime(paths, keyring, candidateReleaseCommit)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "{\"schema\":\"stride.e10.w4.runtime-lineage-verification.v1\",\"activationId\":%q,\"activationReleaseCommit\":%q,\"candidateReleaseCommit\":%q,\"snapshotGeneration\":%d,\"status\":\"verified\"}\n", journal.ActivationID, journal.ReleaseCommit, candidateReleaseCommit, envelope.Generation)
	return nil
}

func runStrideE10W4NetworkRollbackVerifyCLI(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrStrideE10Invalid
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	paths, err := strideE10W4ActivationPathsFromEnvironment()
	if err != nil {
		return err
	}
	releaseCommit := strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT"))
	if err := strideE10W4VerifyRolledBackActivation(paths, keyring, releaseCommit); err != nil {
		return err
	}
	journal, _ := strideE10W4LoadJournal(paths.Journal, keyring)
	fmt.Fprintf(os.Stdout, "{\"schema\":\"stride.e10.w4.rollback-verification.v1\",\"activationId\":%q,\"releaseCommit\":%q,\"sourceGeneration\":%d,\"status\":\"rolled_back\"}\n", journal.ActivationID, journal.ReleaseCommit, journal.SourceGeneration)
	return nil
}

func strideE10W4ActivationPathsFromEnvironment() (strideE10W4ActivationPaths, error) {
	snapshot, err := strideE10W4RequiredAbsolutePath(strideE10W4SnapshotPathEnv)
	if err != nil {
		return strideE10W4ActivationPaths{}, err
	}
	backup, err := strideE10W4RequiredAbsolutePath(strideE10W4ActivationBackupDirEnv)
	if err != nil {
		return strideE10W4ActivationPaths{}, err
	}
	receipt, err := strideE10W4RequiredAbsolutePath(strideE10W4ActivationReceiptPathEnv)
	if err != nil {
		return strideE10W4ActivationPaths{}, err
	}
	paths := strideE10W4ActivationPaths{Snapshot: snapshot, Sessions: userSessionStore().path, BackupDir: backup, Receipt: receipt, Journal: filepath.Join(backup, "activation.journal.json")}
	if err := strideE10W4ValidateActivationPaths(paths); err != nil {
		return strideE10W4ActivationPaths{}, err
	}
	return paths, nil
}

func strideE10W4ValidateActivationPaths(paths strideE10W4ActivationPaths) error {
	values := map[string]string{"snapshot": paths.Snapshot, "sessions": paths.Sessions, "backup": paths.BackupDir, "receipt": paths.Receipt, "journal": paths.Journal}
	for _, path := range values {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ErrStrideE10Invalid
		}
	}
	return strideE10RequireDistinctMigrationPaths(values)
}

func strideE10W4PhaseRank(phase strideE10W4ActivationPhase) int {
	for index, candidate := range []strideE10W4ActivationPhase{strideE10W4ActivationPrepared, strideE10W4ActivationSessions, strideE10W4ActivationSnapshot, strideE10W4ActivationReceiptWritten, strideE10W4ActivationCommitted, strideE10W4RollbackStarted, strideE10W4RollbackSessions, strideE10W4RollbackSnapshot, strideE10W4RolledBack} {
		if phase == candidate {
			return index + 1
		}
	}
	return 0
}

func strideE10W4WriteImmutableBackup(path string, body []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if !hmac.Equal(existing, body) {
			return ErrStrideE10Conflict
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func strideE10W4WriteJournal(path string, journal strideE10W4ActivationJournal, keyring *strideE10W4Keyring) error {
	body, err := strideE10W4EncodeAuthenticatedArtifact("stride.e10.w4.activation-journal-envelope.v1", "journal", journal, keyring)
	if err != nil {
		return err
	}
	return writeFileAtomicallyDurable(path, body, 0o600)
}

func strideE10W4LoadJournal(path string, keyring *strideE10W4Keyring) (strideE10W4ActivationJournal, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return strideE10W4ActivationJournal{}, err
	}
	var journal strideE10W4ActivationJournal
	if strideE10W4DecodeAuthenticatedArtifact(body, "stride.e10.w4.activation-journal-envelope.v1", "journal", keyring, &journal) != nil || journal.Schema != "stride.e10.w4.activation-journal.v1" || strideE10W4PhaseRank(journal.Phase) == 0 || !releaseCommitPattern.MatchString(journal.ReleaseCommit) || !isHexDigest(journal.ActivationID) || !isHexDigest(journal.ActivationReceiptDigest) || !isHexDigest(journal.OriginalSnapshotDigest) || !isHexDigest(journal.OriginalSessionsDigest) || !isHexDigest(journal.TargetSnapshotDigest) || !isHexDigest(journal.TargetSessionsDigest) || !isHexDigest(journal.ReceiptDigest) || journal.TargetGeneration != journal.SourceGeneration+1 {
		return strideE10W4ActivationJournal{}, ErrStrideE10Denied
	}
	return journal, nil
}

func strideE10W4PrepareActivation(paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, at time.Time, releaseCommit string) (strideE10W4ActivationJournal, strideE10W4ActivationReceipt, []byte, []byte, []byte, error) {
	originalSnapshot, err := os.ReadFile(paths.Snapshot)
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	originalSessions, err := os.ReadFile(paths.Sessions)
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	snapshot, generation, err := loadStrideE10W4Snapshot(paths.Snapshot, keyring)
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	var sessions map[string]sessionRecord
	if json.Unmarshal(originalSessions, &sessions) != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Invalid
	}
	activated, updated, receipt, err := strideE10W4ActivateSnapshot(snapshot, sessions, at)
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	targetSessions, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	activationID := sha256Hex([]byte(strings.Join([]string{releaseCommit, paths.Snapshot, paths.Sessions, sha256Hex(originalSnapshot), sha256Hex(originalSessions), fmt.Sprint(generation + 1), at.UTC().Format(time.RFC3339Nano)}, "\x00")))
	receipt.ActivationID, receipt.ReleaseCommit = activationID, releaseCommit
	receipt.SnapshotGeneration, receipt.SessionsDigest = generation+1, sha256Hex(targetSessions)
	receipt.ActivationReceiptDigest, _ = STRIDEContractDigest(receipt)
	targetSnapshot, err := encodeStrideE10W4RuntimeSnapshotWithLineage(activated, generation+1, keyring, activationID, receipt.ActivationReceiptDigest)
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	receipt.SnapshotDigest = sha256Hex(targetSnapshot)
	receiptBody, err := strideE10W4EncodeAuthenticatedArtifact("stride.e10.w4.activation-receipt-envelope.v1", "receipt", receipt, keyring)
	if err != nil {
		return strideE10W4ActivationJournal{}, strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	runDir := filepath.Join(paths.BackupDir, "activations", activationID)
	journal := strideE10W4ActivationJournal{Schema: "stride.e10.w4.activation-journal.v1", ActivationID: activationID, ReleaseCommit: releaseCommit, ActivationReceiptDigest: receipt.ActivationReceiptDigest, Phase: strideE10W4ActivationPrepared, SnapshotPath: paths.Snapshot, SessionsPath: paths.Sessions, ReceiptPath: paths.Receipt, SnapshotBackupPath: filepath.Join(runDir, "runtime.snapshot.before.json"), SessionsBackupPath: filepath.Join(runDir, "sessions.before.json"), SourceGeneration: generation, TargetGeneration: generation + 1, OriginalSnapshotDigest: sha256Hex(originalSnapshot), OriginalSessionsDigest: sha256Hex(originalSessions), TargetSnapshotDigest: sha256Hex(targetSnapshot), TargetSessionsDigest: sha256Hex(targetSessions), ReceiptDigest: sha256Hex(receiptBody), ActivatedAt: at.UTC().Format(time.RFC3339Nano)}
	return journal, receipt, targetSnapshot, targetSessions, receiptBody, nil
}

func strideE10W4LoadActivationPlan(paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, journal strideE10W4ActivationJournal) (strideE10W4ActivationReceipt, []byte, []byte, []byte, error) {
	runDir := filepath.Join(paths.BackupDir, "activations", journal.ActivationID)
	if journal.SnapshotPath != paths.Snapshot || journal.SessionsPath != paths.Sessions || journal.ReceiptPath != paths.Receipt || journal.SnapshotBackupPath != filepath.Join(runDir, "runtime.snapshot.before.json") || journal.SessionsBackupPath != filepath.Join(runDir, "sessions.before.json") {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	originalSnapshot, err := os.ReadFile(journal.SnapshotBackupPath)
	if err != nil || sha256Hex(originalSnapshot) != journal.OriginalSnapshotDigest {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	originalSessions, err := os.ReadFile(journal.SessionsBackupPath)
	if err != nil || sha256Hex(originalSessions) != journal.OriginalSessionsDigest {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	snapshot, sourceEnvelope, err := decodeStrideE10W4SnapshotEnvelope(originalSnapshot, keyring)
	if err != nil || sourceEnvelope.Generation != journal.SourceGeneration {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	var sessions map[string]sessionRecord
	at, timeErr := time.Parse(time.RFC3339Nano, journal.ActivatedAt)
	if json.Unmarshal(originalSessions, &sessions) != nil || timeErr != nil {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	activated, updated, receipt, err := strideE10W4ActivateSnapshot(snapshot, sessions, at)
	if err != nil {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	targetSessions, _ := json.MarshalIndent(updated, "", "  ")
	receipt.ActivationID, receipt.ReleaseCommit = journal.ActivationID, journal.ReleaseCommit
	receipt.SnapshotGeneration, receipt.SessionsDigest = journal.TargetGeneration, sha256Hex(targetSessions)
	receipt.ActivationReceiptDigest, _ = STRIDEContractDigest(receipt)
	if receipt.ActivationReceiptDigest != journal.ActivationReceiptDigest {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	targetSnapshot, err := encodeStrideE10W4RuntimeSnapshotWithLineage(activated, journal.TargetGeneration, keyring, journal.ActivationID, journal.ActivationReceiptDigest)
	if err != nil {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, err
	}
	receipt.SnapshotDigest = sha256Hex(targetSnapshot)
	receiptBody, err := strideE10W4EncodeAuthenticatedArtifact("stride.e10.w4.activation-receipt-envelope.v1", "receipt", receipt, keyring)
	if err != nil || sha256Hex(targetSnapshot) != journal.TargetSnapshotDigest || sha256Hex(targetSessions) != journal.TargetSessionsDigest || sha256Hex(receiptBody) != journal.ReceiptDigest {
		return strideE10W4ActivationReceipt{}, nil, nil, nil, ErrStrideE10Denied
	}
	return receipt, targetSnapshot, targetSessions, receiptBody, nil
}

func strideE10W4RunActivation(ctx context.Context, paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, at time.Time, stopAfter strideE10W4ActivationPhase) (strideE10W4ActivationReceipt, error) {
	if ctx == nil || ctx.Err() != nil || strideE10W4ValidateActivationPaths(paths) != nil || strideE10W4PhaseRank(stopAfter) == 0 || !releaseCommitPattern.MatchString(strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT"))) {
		return strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
	}
	if err := os.MkdirAll(paths.BackupDir, 0o700); err != nil {
		return strideE10W4ActivationReceipt{}, err
	}
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err == nil && journal.Phase == strideE10W4RolledBack {
		if verifyErr := strideE10W4VerifyRolledBackActivation(paths, keyring, journal.ReleaseCommit); verifyErr != nil {
			return strideE10W4ActivationReceipt{}, verifyErr
		}
		journalBody, _ := os.ReadFile(paths.Journal)
		receiptBody, _ := os.ReadFile(paths.Receipt)
		runDir := filepath.Dir(journal.SnapshotBackupPath)
		archiveErr := strideE10W4WriteImmutableBackup(filepath.Join(runDir, "activation.journal.rolled-back.json"), journalBody)
		if archiveErr == nil {
			archiveErr = strideE10W4WriteImmutableBackup(filepath.Join(runDir, "activation.receipt.json"), receiptBody)
		}
		if archiveErr != nil {
			return strideE10W4ActivationReceipt{}, archiveErr
		}
		journal, _, _, _, _, err = strideE10W4PrepareActivation(paths, keyring, at.UTC(), strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT")))
		if err == nil {
			err = os.MkdirAll(filepath.Dir(journal.SnapshotBackupPath), 0o700)
		}
		if err == nil {
			originalSnapshot, _ := os.ReadFile(paths.Snapshot)
			originalSessions, _ := os.ReadFile(paths.Sessions)
			err = strideE10W4WriteImmutableBackup(journal.SnapshotBackupPath, originalSnapshot)
			if err == nil {
				err = strideE10W4WriteImmutableBackup(journal.SessionsBackupPath, originalSessions)
			}
		}
		if err == nil {
			err = strideE10W4WriteJournal(paths.Journal, journal, keyring)
		}
	}
	if err == nil && oneOf(string(journal.Phase), string(strideE10W4RollbackStarted), string(strideE10W4RollbackSessions), string(strideE10W4RollbackSnapshot)) {
		return strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
	}
	if errors.Is(err, os.ErrNotExist) {
		journal, _, _, _, _, err = strideE10W4PrepareActivation(paths, keyring, at.UTC(), strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT")))
		if err != nil {
			return strideE10W4ActivationReceipt{}, err
		}
		if err = os.MkdirAll(filepath.Dir(journal.SnapshotBackupPath), 0o700); err != nil {
			return strideE10W4ActivationReceipt{}, err
		}
		originalSnapshot, _ := os.ReadFile(paths.Snapshot)
		originalSessions, _ := os.ReadFile(paths.Sessions)
		if err = strideE10W4WriteImmutableBackup(journal.SnapshotBackupPath, originalSnapshot); err == nil {
			err = strideE10W4WriteImmutableBackup(journal.SessionsBackupPath, originalSessions)
		}
		if err == nil {
			err = strideE10W4WriteJournal(paths.Journal, journal, keyring)
		}
	}
	if err != nil {
		return strideE10W4ActivationReceipt{}, err
	}
	receipt, targetSnapshot, targetSessions, receiptBody, err := strideE10W4LoadActivationPlan(paths, keyring, journal)
	if err != nil {
		return strideE10W4ActivationReceipt{}, err
	}
	for strideE10W4PhaseRank(journal.Phase) < strideE10W4PhaseRank(stopAfter) {
		if err := ctx.Err(); err != nil {
			return strideE10W4ActivationReceipt{}, err
		}
		switch journal.Phase {
		case strideE10W4ActivationPrepared:
			current, readErr := os.ReadFile(paths.Sessions)
			if readErr != nil || !oneOf(sha256Hex(current), journal.OriginalSessionsDigest, journal.TargetSessionsDigest) {
				return strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
			}
			if err := writeFileAtomicallyDurable(paths.Sessions, targetSessions, 0o600); err != nil {
				return strideE10W4ActivationReceipt{}, err
			}
			journal.Phase = strideE10W4ActivationSessions
		case strideE10W4ActivationSessions:
			current, readErr := os.ReadFile(paths.Snapshot)
			if readErr != nil || !oneOf(sha256Hex(current), journal.OriginalSnapshotDigest, journal.TargetSnapshotDigest) {
				return strideE10W4ActivationReceipt{}, ErrStrideE10Conflict
			}
			if err := writeFileAtomicallyDurable(paths.Snapshot, targetSnapshot, 0o600); err != nil {
				return strideE10W4ActivationReceipt{}, err
			}
			journal.Phase = strideE10W4ActivationSnapshot
		case strideE10W4ActivationSnapshot:
			if err := writeFileAtomicallyDurable(paths.Receipt, receiptBody, 0o600); err != nil {
				return strideE10W4ActivationReceipt{}, err
			}
			journal.Phase = strideE10W4ActivationReceiptWritten
		case strideE10W4ActivationReceiptWritten:
			journal.Phase = strideE10W4ActivationCommitted
		default:
			return strideE10W4ActivationReceipt{}, ErrStrideE10Invalid
		}
		if err := strideE10W4WriteJournal(paths.Journal, journal, keyring); err != nil {
			return strideE10W4ActivationReceipt{}, err
		}
	}
	if journal.Phase == strideE10W4ActivationCommitted {
		if err := strideE10W4VerifyCommittedActivation(paths, keyring, strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT"))); err != nil {
			return strideE10W4ActivationReceipt{}, err
		}
	}
	return receipt, nil
}

func strideE10W4VerifyCommittedActivation(paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, releaseCommit string) error {
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err != nil || journal.Phase != strideE10W4ActivationCommitted || journal.ReleaseCommit != releaseCommit {
		return ErrStrideE10Denied
	}
	receipt, _, _, _, err := strideE10W4LoadActivationPlan(paths, keyring, journal)
	if err != nil {
		return err
	}
	snapshotBody, snapshotErr := os.ReadFile(paths.Snapshot)
	sessionsBody, sessionsErr := os.ReadFile(paths.Sessions)
	receiptBody, receiptErr := os.ReadFile(paths.Receipt)
	var authenticated strideE10W4ActivationReceipt
	if snapshotErr != nil || sessionsErr != nil || receiptErr != nil || sha256Hex(snapshotBody) != journal.TargetSnapshotDigest || sha256Hex(sessionsBody) != journal.TargetSessionsDigest || sha256Hex(receiptBody) != journal.ReceiptDigest || strideE10W4DecodeAuthenticatedArtifact(receiptBody, "stride.e10.w4.activation-receipt-envelope.v1", "receipt", keyring, &authenticated) != nil {
		return ErrStrideE10Denied
	}
	authenticatedDigest, _ := STRIDEContractDigest(authenticated)
	receiptDigest, _ := STRIDEContractDigest(receipt)
	if authenticatedDigest != receiptDigest || authenticated.ReleaseCommit != releaseCommit || authenticated.SnapshotGeneration != journal.TargetGeneration {
		return ErrStrideE10Denied
	}
	snapshot, envelope, err := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keyring)
	var sessions map[string]sessionRecord
	if err != nil || envelope.Generation != journal.TargetGeneration || envelope.ActivationID != journal.ActivationID || envelope.ActivationReceiptDigest != journal.ActivationReceiptDigest || json.Unmarshal(sessionsBody, &sessions) != nil || strideE10W4ValidateLiveSemantics(authenticated, snapshot, sessions) != nil {
		return ErrStrideE10Denied
	}
	return nil
}

func strideE10W4ValidateLiveSemantics(receipt strideE10W4ActivationReceipt, snapshot strideE10W4RuntimeSnapshot, sessions map[string]sessionRecord) error {
	wantFeatures := make([]string, 0, len(strideE10W4NetworkFeatures))
	for _, feature := range strideE10W4NetworkFeatures {
		wantFeatures = append(wantFeatures, string(feature))
	}
	if receipt.Schema != "stride.e10.w4.network-activation.v1" || !isHexDigest(receipt.ActivationID) || !isHexDigest(receipt.ActivationReceiptDigest) || !releaseCommitPattern.MatchString(receipt.ReleaseCommit) || receipt.OrganizationCount != 1 || receipt.PersonCount != 7 || receipt.MembershipCount != 7 || receipt.ContributionGrantCount != 18 || receipt.NetworkDraftCount != 7 || receipt.RecruiterGrantCount != 1 || len(receipt.EnabledFeatures) != len(wantFeatures) {
		return ErrStrideE10Denied
	}
	for index := range wantFeatures {
		if receipt.EnabledFeatures[index] != wantFeatures[index] {
			return ErrStrideE10Denied
		}
	}
	if len(snapshot.Organization.Persons) != 7 || len(snapshot.Organization.AccountPersons) != 7 || len(snapshot.Organization.Organizations) < 1 || len(snapshot.Network.Profiles) > 7 || len(snapshot.Network.SearchReceipts) != 0 || len(snapshot.Network.Contacts) != 0 || len(snapshot.Network.Publications) != 0 || len(snapshot.Contribution.Publications) != 0 {
		return ErrStrideE10Denied
	}
	for _, grant := range snapshot.Contribution.Grants {
		if grant.Validate() != nil {
			return ErrStrideE10Denied
		}
	}
	for _, grant := range snapshot.Network.Grants {
		membership := snapshot.Organization.Memberships[grant.MembershipID]
		if grant.Validate() != nil || membership.PersonID != grant.SearcherPersonID || membership.OrganizationID != grant.OrganizationID || membership.Header.Revision != grant.MembershipRevision || grant.State == "active" && membership.Status != "active" {
			return ErrStrideE10Denied
		}
	}
	seenProfiles := map[string]bool{}
	for _, profile := range snapshot.Network.Profiles {
		if profile.Validate() != nil || profile.State == "published" || profile.State == "deleted" || profile.Discoverability != "unlisted" || profile.Publication != (STRIDEReference{}) || snapshot.Organization.Persons[profile.SubjectPersonID].Header.ID == "" || seenProfiles[profile.SubjectPersonID] {
			return ErrStrideE10Denied
		}
		seenProfiles[profile.SubjectPersonID] = true
	}
	for hash, record := range sessions {
		if record.Kind != "" {
			continue
		}
		person, ok := snapshot.Organization.Persons[record.PersonID]
		if !ok || person.Validate() != nil || person.Status != "active" || record.AccountSubjectDigest != person.AccountSubjectDigest || snapshot.Organization.AccountPersons[record.AccountSubjectDigest] != record.PersonID || record.AuthorityGeneration < 1 || !isHexDigest(hash) {
			return ErrStrideE10Denied
		}
		zeroOrganization := record.ActiveOrganizationID == "" && record.OrganizationMembershipID == "" && record.OrganizationMembershipRev == 0 && record.ActiveOrganizationSessionRev == 0
		if zeroOrganization {
			continue
		}
		membership, membershipOK := snapshot.Organization.Memberships[record.OrganizationMembershipID]
		active, activeOK := snapshot.Organization.Sessions[hash]
		if !membershipOK || !activeOK || membership.Status != "active" || membership.PersonID != record.PersonID || membership.OrganizationID != record.ActiveOrganizationID || membership.Header.Revision != record.OrganizationMembershipRev || active.Validate() != nil || active.Status != "active" || active.PersonID != record.PersonID || active.OrganizationID != record.ActiveOrganizationID || active.MembershipID != record.OrganizationMembershipID || active.MembershipRevision != record.OrganizationMembershipRev || active.SessionRevision != record.ActiveOrganizationSessionRev || !active.ExpiresAt.Equal(record.Expires.UTC()) {
			return ErrStrideE10Denied
		}
	}
	return nil
}

func strideE10W4VerifyLiveActivationLineage(paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring) (strideE10W4SnapshotEnvelope, error) {
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err != nil || journal.Phase != strideE10W4ActivationCommitted {
		return strideE10W4SnapshotEnvelope{}, ErrStrideE10Denied
	}
	receiptBody, err := os.ReadFile(paths.Receipt)
	var receipt strideE10W4ActivationReceipt
	if err != nil || sha256Hex(receiptBody) != journal.ReceiptDigest || strideE10W4DecodeAuthenticatedArtifact(receiptBody, "stride.e10.w4.activation-receipt-envelope.v1", "receipt", keyring, &receipt) != nil || receipt.ActivationID != journal.ActivationID || receipt.ActivationReceiptDigest != journal.ActivationReceiptDigest || receipt.ReleaseCommit != journal.ReleaseCommit {
		return strideE10W4SnapshotEnvelope{}, ErrStrideE10Denied
	}
	snapshot, envelope, err := loadStrideE10W4SnapshotEnvelope(paths.Snapshot, keyring)
	if err != nil || envelope.Generation < journal.TargetGeneration || envelope.ActivationID != journal.ActivationID || envelope.ActivationReceiptDigest != journal.ActivationReceiptDigest {
		return strideE10W4SnapshotEnvelope{}, ErrStrideE10Denied
	}
	sessionsBody, err := os.ReadFile(paths.Sessions)
	var sessions map[string]sessionRecord
	if err != nil || json.Unmarshal(sessionsBody, &sessions) != nil || strideE10W4ValidateLiveSemantics(receipt, snapshot, sessions) != nil {
		return strideE10W4SnapshotEnvelope{}, ErrStrideE10Denied
	}
	return envelope, nil
}

func strideE10W4RollbackActivation(ctx context.Context, paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring) error {
	return strideE10W4RunRollback(ctx, paths, keyring, strideE10W4RolledBack)
}

func strideE10W4RunRollback(ctx context.Context, paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, stopAfter strideE10W4ActivationPhase) error {
	if ctx == nil || ctx.Err() != nil || strideE10W4ValidateActivationPaths(paths) != nil {
		return ErrStrideE10Invalid
	}
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err != nil || strideE10W4PhaseRank(journal.Phase) < strideE10W4PhaseRank(strideE10W4ActivationCommitted) || strideE10W4PhaseRank(stopAfter) < strideE10W4PhaseRank(strideE10W4RollbackStarted) || strideE10W4PhaseRank(stopAfter) > strideE10W4PhaseRank(strideE10W4RolledBack) || journal.ReleaseCommit != strings.TrimSpace(os.Getenv("BONFIRE_RELEASE_COMMIT")) {
		return ErrStrideE10Denied
	}
	if _, _, _, _, err := strideE10W4LoadActivationPlan(paths, keyring, journal); err != nil {
		return err
	}
	originalSnapshot, _ := os.ReadFile(journal.SnapshotBackupPath)
	originalSessions, _ := os.ReadFile(journal.SessionsBackupPath)
	if sha256Hex(originalSnapshot) != journal.OriginalSnapshotDigest || sha256Hex(originalSessions) != journal.OriginalSessionsDigest {
		return ErrStrideE10Denied
	}
	for strideE10W4PhaseRank(journal.Phase) < strideE10W4PhaseRank(stopAfter) {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch journal.Phase {
		case strideE10W4ActivationCommitted:
			// Rollback is an initial activation failure recovery, not a live
			// downgrade mechanism. Once governed state or sessions evolve, do
			// not enter rollback or restore stale pre-activation bytes.
			currentSnapshot, snapshotErr := os.ReadFile(paths.Snapshot)
			currentSessions, sessionsErr := os.ReadFile(paths.Sessions)
			if snapshotErr != nil || sessionsErr != nil || sha256Hex(currentSnapshot) != journal.TargetSnapshotDigest || sha256Hex(currentSessions) != journal.TargetSessionsDigest {
				return ErrStrideE10Conflict
			}
			journal.Phase = strideE10W4RollbackStarted
		case strideE10W4RollbackStarted:
			current, readErr := os.ReadFile(paths.Sessions)
			if readErr != nil || !oneOf(sha256Hex(current), journal.TargetSessionsDigest, journal.OriginalSessionsDigest) {
				return ErrStrideE10Conflict
			}
			if err := writeFileAtomicallyDurable(paths.Sessions, originalSessions, 0o600); err != nil {
				return err
			}
			journal.Phase = strideE10W4RollbackSessions
		case strideE10W4RollbackSessions:
			current, readErr := os.ReadFile(paths.Snapshot)
			if readErr != nil || !oneOf(sha256Hex(current), journal.TargetSnapshotDigest, journal.OriginalSnapshotDigest) {
				return ErrStrideE10Conflict
			}
			if err := writeFileAtomicallyDurable(paths.Snapshot, originalSnapshot, 0o600); err != nil {
				return err
			}
			journal.Phase = strideE10W4RollbackSnapshot
		case strideE10W4RollbackSnapshot:
			journal.Phase = strideE10W4RolledBack
		default:
			return ErrStrideE10Denied
		}
		if err := strideE10W4WriteJournal(paths.Journal, journal, keyring); err != nil {
			return err
		}
	}
	if journal.Phase == strideE10W4RolledBack {
		return strideE10W4VerifyRolledBackActivation(paths, keyring, journal.ReleaseCommit)
	}
	return nil
}

func strideE10W4VerifyRolledBackActivation(paths strideE10W4ActivationPaths, keyring *strideE10W4Keyring, releaseCommit string) error {
	if !releaseCommitPattern.MatchString(releaseCommit) {
		return ErrStrideE10Invalid
	}
	journal, err := strideE10W4LoadJournal(paths.Journal, keyring)
	if err != nil || journal.Phase != strideE10W4RolledBack || journal.ReleaseCommit != releaseCommit {
		return ErrStrideE10Denied
	}
	if _, _, _, _, err := strideE10W4LoadActivationPlan(paths, keyring, journal); err != nil {
		return err
	}
	receiptBody, receiptErr := os.ReadFile(paths.Receipt)
	var receipt strideE10W4ActivationReceipt
	if receiptErr != nil || sha256Hex(receiptBody) != journal.ReceiptDigest || strideE10W4DecodeAuthenticatedArtifact(receiptBody, "stride.e10.w4.activation-receipt-envelope.v1", "receipt", keyring, &receipt) != nil || receipt.ActivationID != journal.ActivationID || receipt.ReleaseCommit != releaseCommit {
		return ErrStrideE10Denied
	}
	currentSnapshot, snapshotErr := os.ReadFile(paths.Snapshot)
	currentSessions, sessionsErr := os.ReadFile(paths.Sessions)
	backupSnapshot, backupSnapshotErr := os.ReadFile(journal.SnapshotBackupPath)
	backupSessions, backupSessionsErr := os.ReadFile(journal.SessionsBackupPath)
	if snapshotErr != nil || sessionsErr != nil || backupSnapshotErr != nil || backupSessionsErr != nil || sha256Hex(currentSnapshot) != journal.OriginalSnapshotDigest || sha256Hex(currentSessions) != journal.OriginalSessionsDigest || sha256Hex(backupSnapshot) != journal.OriginalSnapshotDigest || sha256Hex(backupSessions) != journal.OriginalSessionsDigest {
		return ErrStrideE10Denied
	}
	_, generation, err := loadStrideE10W4Snapshot(paths.Snapshot, keyring)
	if err != nil || generation != journal.SourceGeneration {
		return ErrStrideE10Denied
	}
	return nil
}
