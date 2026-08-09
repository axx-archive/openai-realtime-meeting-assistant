package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var strideE10W4MigrateFlag = flag.Bool("stride-e10-w4-migrate", false, "run the authority-bound Bonfire W4 migration and exit")

const (
	strideE10W4StatePathEnv         = "STRIDE_E10_W4_STATE_PATH"
	strideE10W4BackupPathEnv        = "STRIDE_E10_W4_BACKUP_PATH"
	strideE10W4PublicReceiptPathEnv = "STRIDE_E10_W4_PUBLIC_RECEIPT_PATH"
	strideE10W4TargetPathEnv        = "STRIDE_E10_W4_TARGET_PATH"
)

func strideE10W4Digest(domain string, bodies ...[]byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	for _, body := range bodies {
		h.Write([]byte{0})
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func strideE10W4RequiredAbsolutePath(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name))
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%s must be an exact absolute path", name)
	}
	return path, nil
}

func runStrideE10W4MigrationCLI(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("STRIDE W4 migration must run as root")
	}
	keyring, err := strideE10W4KeyringFromEnvironment()
	if err != nil {
		return err
	}
	statePath, err := strideE10W4RequiredAbsolutePath(strideE10W4StatePathEnv)
	if err != nil {
		return err
	}
	backupPath, err := strideE10W4RequiredAbsolutePath(strideE10W4BackupPathEnv)
	if err != nil {
		return err
	}
	publicPath, err := strideE10W4RequiredAbsolutePath(strideE10W4PublicReceiptPathEnv)
	if err != nil {
		return err
	}
	targetPath, err := strideE10W4RequiredAbsolutePath(strideE10W4TargetPathEnv)
	if err != nil {
		return err
	}
	snapshotPath, err := strideE10W4RequiredAbsolutePath(strideE10W4SnapshotPathEnv)
	if err != nil {
		return err
	}
	operationPath, err := strideE10W4RequiredAbsolutePath(strideE10W4OperationPathEnv)
	if err != nil {
		return err
	}
	if err := strideE10RequireDistinctMigrationPaths(map[string]string{"state": statePath, "backup": backupPath, "public": publicPath, "target": targetPath, "snapshot": snapshotPath, "operations": operationPath, "accounts": accountStore().path, "sessions": userSessionStore().path}); err != nil {
		return err
	}

	databaseURL := strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("BONFIRE_CANONICAL_DATABASE_URL is required")
	}
	store, err := OpenPostgresCanonicalStore(ctx, databaseURL, NewCanonicalPayloadRegistry())
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ApplyMigrations(ctx); err != nil {
		return err
	}
	var sourceHighWater uint64
	if err := store.pool.QueryRow(ctx, "SELECT COALESCE(max(sequence),0) FROM canonical_events").Scan(&sourceHighWater); err != nil || sourceHighWater == 0 {
		return fmt.Errorf("read canonical source high-water: %w", err)
	}
	migrationBodies := make([][]byte, 0, 3)
	embedded, err := loadCanonicalMigrations()
	if err != nil {
		return err
	}
	for _, migration := range embedded {
		if migration.Version >= 14 && migration.Version <= 16 {
			migrationBodies = append(migrationBodies, []byte(migration.SQL))
		}
	}
	if len(migrationBodies) != 3 {
		return errors.New("exact STRIDE migrations 14-16 are required")
	}
	contract := StrideE10MigrationContractInput{
		OrganizationName: "Bonfire", OrganizationSlug: "bonfire",
		SchemaDigest:           strideE10W4Digest("schema", migrationBodies...),
		PolicyDigest:           strideE10W4Digest("policy", []byte("stride-e10-w0-revision-1")),
		MigrationDigest:        strideE10W4Digest("migration", migrationBodies...),
		SwitchDigest:           strideE10W4Digest("switches", []byte("person_profile_authority,organization_authority_read,organization_authority_write,active_organization_session,work_record_private")),
		OperatorDigest:         strideE10W4Digest("operator", []byte("aj-production-owner")),
		ReviewerDigest:         strideE10W4Digest("reviewer", []byte("stride-e10-w3-independent-critic-pass")),
		RollbackIdentityDigest: strideE10W4Digest("rollback", []byte("exact-release-and-source-backup")),
	}
	target := NewStrideE10DisposableMigrationTarget(targetPath, sourceHighWater, keyring)
	result, err := RunStrideE10MigrationRehearsal(ctx, StrideE10MigrationRunConfig{StatePath: statePath, BackupPath: backupPath, PublicReceiptPath: publicPath, Source: NewStrideE10LocalMigrationSource(accountStore(), userSessionStore()), Writer: target, Keys: keyring, Contract: contract})
	if err != nil {
		return err
	}
	if result.Receipt.MigratedRoots != 7 || result.Receipt.MigratedMemberships != 7 || result.Receipt.TargetHighWater-result.Receipt.SourceHighWater != 15 {
		return ErrStrideE10Invalid
	}
	organization, err := strideE10W4OrganizationFromMigration(result.PrivateManifest, time.Unix(1_700_000_000+int64(result.Receipt.SourceHighWater), 0).UTC())
	if err != nil {
		return err
	}
	if err := strideE10W4ApplyPostgres(ctx, store, result.PrivateManifest, organization); err != nil {
		return err
	}
	initialRuntime := newStrideE10ProductLiveRuntimeWithStores(nil, newStrideE10MemoryPortableDeletionStore(), newStrideE10MemoryOperationStore())
	initialRuntime.organization = organization
	if err := writeStrideE10W4RuntimeSnapshot(snapshotPath, 1, keyring, initialRuntime); err != nil {
		return err
	}
	if err := strideE10W4BindExistingSessions(result.PrivateManifest); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "{\"schema\":\"stride.e10.w4.production-migration.v1\",\"organizationId\":%q,\"migratedRoots\":7,\"migratedMemberships\":7,\"targetDelta\":15,\"publicReceipt\":%q}\n", result.Receipt.OrganizationID, publicPath)
	return nil
}

func strideE10W4OrganizationFromMigration(manifest StrideE10PrivateMigrationManifest, at time.Time) (*OrganizationAuthorityService, error) {
	service := NewOrganizationAuthorityService()
	accounts := accountStore()
	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	bindings := append([]StrideE10PrivateMigrationBinding(nil), manifest.Bindings...)
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].NormalizedSubject < bindings[j].NormalizedSubject })
	ownerPersonID := ""
	for _, binding := range bindings {
		digest := sha256Hex([]byte(normalizeAccountEmail(binding.NormalizedSubject)))
		person := PersonPrincipal{Header: strideE10LiveHeader(STRIDEContractPersonPrincipal, STRIDEGlobalPersonTenant, binding.PersonID, 1, "w4-person-"+digest, at), AccountSubjectDigest: digest, Status: "active", RecoveryRevision: 1, CustodyRevision: 1}
		if err := service.RegisterPerson(person); err != nil {
			return nil, err
		}
		account := accounts.users[normalizeAccountEmail(binding.NormalizedSubject)]
		if account == nil {
			return nil, ErrStrideE10NotFound
		}
		profile := PersonProfile{Header: strideE10LiveHeader(STRIDEContractPersonProfile, STRIDEGlobalPersonTenant, binding.PersonID, 1, "w4-profile-"+binding.ProfileDigest, at), PersonID: binding.PersonID, DisplayName: account.Name, Status: "active", UpdatedAt: at}
		if err := service.PutSelfProfile(binding.PersonID, 0, profile); err != nil {
			return nil, err
		}
		if binding.Role == "owner" {
			ownerPersonID = binding.PersonID
		}
	}
	if ownerPersonID == "" {
		return nil, ErrStrideE10Invalid
	}
	organization := Organization{Header: strideE10LiveHeader(STRIDEContractOrganization, STRIDEGlobalPersonTenant, manifest.OrganizationID, 1, "w4-organization-"+manifest.TargetDigest, at), Name: "Bonfire", Slug: "bonfire", Status: "active", Discoverability: "private", CreatorPersonID: ownerPersonID, PolicyRevision: 1, CreatedAt: at, UpdatedAt: at}
	service.organizations[organization.Header.ID] = organization
	for _, binding := range bindings {
		membership := OrganizationMembership{Header: strideE10LiveHeader(STRIDEContractOrganizationMembership, manifest.OrganizationID, binding.MembershipID, 1, "w4-membership-"+binding.PersonID+"-"+binding.Role, at), PersonID: binding.PersonID, OrganizationID: manifest.OrganizationID, Role: binding.Role, Status: "active", GrantedAt: at}
		if membership.Validate() != nil {
			return nil, ErrStrideE10Invalid
		}
		service.memberships[membership.Header.ID] = membership
	}
	return service, nil
}

func strideE10W4ApplyPostgres(ctx context.Context, store *PostgresCanonicalStore, manifest StrideE10PrivateMigrationManifest, organization *OrganizationAuthorityService) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	organization.mu.RLock()
	defer organization.mu.RUnlock()
	for _, person := range organization.persons {
		_, err = tx.Exec(ctx, `INSERT INTO stride_person_principals(person_id,revision,account_subject_digest,status,recovery_revision,custody_revision,created_at) VALUES($1,1,decode($2,'hex'),'active',1,1,$3) ON CONFLICT (person_id) DO NOTHING`, person.Header.ID, person.AccountSubjectDigest, person.Header.CreatedAt)
		if err != nil {
			return err
		}
		mappingID := "mapping_" + person.Header.ID
		_, err = tx.Exec(ctx, `INSERT INTO stride_account_person_mappings(mapping_id,revision,account_subject_digest,person_id,status,created_at) VALUES($1,1,decode($2,'hex'),$3,'active',$4) ON CONFLICT DO NOTHING`, mappingID, person.AccountSubjectDigest, person.Header.ID, person.Header.CreatedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_account_person_mappings_current(mapping_id,revision,account_subject_digest,person_id,status,updated_at) VALUES($1,1,decode($2,'hex'),$3,'active',$4) ON CONFLICT DO NOTHING`, mappingID, person.AccountSubjectDigest, person.Header.ID, person.Header.CreatedAt)
		if err != nil {
			return err
		}
		profile := organization.profiles[person.Header.ID]
		_, err = tx.Exec(ctx, `INSERT INTO stride_person_profile_revisions(person_id,revision,display_name,status,created_at,created_by_person_id) VALUES($1,1,$2,'active',$3,$1) ON CONFLICT DO NOTHING`, person.Header.ID, profile.DisplayName, profile.Header.CreatedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_person_profiles_current(person_id,revision,status,updated_at) VALUES($1,1,'active',$2) ON CONFLICT DO NOTHING`, person.Header.ID, profile.Header.CreatedAt)
		if err != nil {
			return err
		}
	}
	var org Organization
	for _, candidate := range organization.organizations {
		org = candidate
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_organizations(organization_id,revision,name,slug,status,discoverability,creator_person_id,created_at,updated_at) VALUES($1,1,$2,$3,'active','private',$4,$5,$5) ON CONFLICT DO NOTHING`, org.Header.ID, org.Name, org.Slug, org.CreatorPersonID, org.CreatedAt)
	if err != nil {
		return err
	}
	bindingsByPerson := map[string]StrideE10PrivateMigrationBinding{}
	for _, binding := range manifest.Bindings {
		bindingsByPerson[binding.PersonID] = binding
	}
	for _, membership := range organization.memberships {
		_, err = tx.Exec(ctx, `INSERT INTO stride_organization_membership_revisions(membership_id,revision,organization_id,person_id,role,status,granted_at,created_at,created_by_person_id) VALUES($1,1,$2,$3,$4,'active',$5,$5,$6) ON CONFLICT DO NOTHING`, membership.Header.ID, membership.OrganizationID, membership.PersonID, membership.Role, membership.GrantedAt, org.CreatorPersonID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO stride_organization_memberships_current(membership_id,revision,organization_id,person_id,role,status,active_slot,updated_at) VALUES($1,1,$2,$3,$4,'active',NULL,$5) ON CONFLICT DO NOTHING`, membership.Header.ID, membership.OrganizationID, membership.PersonID, membership.Role, membership.GrantedAt)
		if err != nil {
			return err
		}
		_ = bindingsByPerson[membership.PersonID]
	}
	var persons, memberships, owners int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM stride_person_principals WHERE person_id = ANY($1)),(SELECT count(*) FROM stride_organization_memberships_current WHERE organization_id=$2 AND status='active'),(SELECT count(*) FROM stride_organization_memberships_current WHERE organization_id=$2 AND status='active' AND role='owner')`, mapKeysStrideE10Persons(organization.persons), org.Header.ID).Scan(&persons, &memberships, &owners); err != nil {
		return err
	}
	if persons != 7 || memberships != 7 || owners != 1 {
		return ErrStrideE10Invalid
	}
	for _, person := range organization.persons {
		var revision int64
		var digest, status string
		if err = tx.QueryRow(ctx, `SELECT revision,encode(account_subject_digest,'hex'),status FROM stride_person_principals WHERE person_id=$1`, person.Header.ID).Scan(&revision, &digest, &status); err != nil || revision != 1 || digest != person.AccountSubjectDigest || status != "active" {
			return ErrStrideE10Conflict
		}
		profile := organization.profiles[person.Header.ID]
		var display string
		if err = tx.QueryRow(ctx, `SELECT display_name FROM stride_person_profile_revisions WHERE person_id=$1 AND revision=1`, person.Header.ID).Scan(&display); err != nil || display != profile.DisplayName {
			return ErrStrideE10Conflict
		}
	}
	for _, membership := range organization.memberships {
		var personID, organizationID, role, status string
		var revision int64
		if err = tx.QueryRow(ctx, `SELECT revision,person_id,organization_id,role,status FROM stride_organization_memberships_current WHERE membership_id=$1`, membership.Header.ID).Scan(&revision, &personID, &organizationID, &role, &status); err != nil || revision != 1 || personID != membership.PersonID || organizationID != membership.OrganizationID || role != membership.Role || status != "active" {
			return ErrStrideE10Conflict
		}
	}
	return tx.Commit(ctx)
}

func mapKeysStrideE10Persons(values map[string]PersonPrincipal) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func strideE10W4BindExistingSessions(manifest StrideE10PrivateMigrationManifest) error {
	bySubject := map[string]StrideE10PrivateMigrationBinding{}
	for _, binding := range manifest.Bindings {
		bySubject[normalizeAccountEmail(binding.NormalizedSubject)] = binding
	}
	sessions := userSessionStore()
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	raw, err := json.Marshal(sessions.sessions)
	if err != nil {
		return err
	}
	updated := map[string]sessionRecord{}
	if json.Unmarshal(raw, &updated) != nil {
		return ErrStrideE10Invalid
	}
	for hash, record := range updated {
		if record.Kind != "" {
			continue
		}
		binding, ok := bySubject[normalizeAccountEmail(record.Email)]
		if !ok {
			return ErrStrideE10Invalid
		}
		record.PersonID = binding.PersonID
		record.AccountSubjectDigest = sha256Hex([]byte(normalizeAccountEmail(record.Email)))
		record.ActiveOrganizationID, record.OrganizationMembershipID = "", ""
		record.OrganizationMembershipRev, record.ActiveOrganizationSessionRev = 0, 0
		if record.AuthorityGeneration < 1 {
			record.AuthorityGeneration = 1
		} else {
			record.AuthorityGeneration++
		}
		updated[hash] = record
	}
	body, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomicallyDurable(sessions.path, body, 0o600); err != nil {
		return err
	}
	sessions.sessions = updated
	return nil
}
