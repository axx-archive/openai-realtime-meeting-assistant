package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type PostgresMeetingSourceEpisodeStore struct {
	canonical *PostgresCanonicalStore
	Failpoint func(string) error
}

func NewPostgresMeetingSourceEpisodeStore(canonical *PostgresCanonicalStore) *PostgresMeetingSourceEpisodeStore {
	return &PostgresMeetingSourceEpisodeStore{canonical: canonical}
}

func (store *PostgresMeetingSourceEpisodeStore) ResolveAuthority(ctx context.Context, material postCloseMeetingSourceMaterial) (postCloseMeetingSourceMaterial, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil {
		return material, ErrSourceEpisodeUnavailable
	}
	// This is a short authority-CAS preparation transaction, not a passive
	// read: it takes row locks and ensures the tenant purge-fence row exists.
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return material, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	aclRevision, aclDigest, purge, err := store.resolveAuthorityTx(ctx, tx, material)
	if err != nil {
		return material, err
	}
	material.ACLRevision, material.ACLDigest, material.Purge = aclRevision, aclDigest, purge
	if err := tx.Commit(ctx); err != nil {
		return material, err
	}
	return material, nil
}

func (store *PostgresMeetingSourceEpisodeStore) CommitMeetingSourceEpisode(ctx context.Context, episode SourceEpisode, expected *STRIDEReference, material postCloseMeetingSourceMaterial) (SourceEpisodeDualWriteResult, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || episode.Validate() != nil || episode.Kind != SourceEpisodeMeetingAnalysis ||
		(expected == nil) != (episode.Supersedes == nil) || expected != nil && *expected != *episode.Supersedes {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeInvalid
	}
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, fence := range sortedMeetingSourceConsentFences(material.Consent) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, consentBindingAdvisoryKey(fence.binding, fence.policy)); err != nil {
			return SourceEpisodeDualWriteResult{}, err
		}
	}
	if err := validateMeetingSourceConsentTx(ctx, tx, material.Consent); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	aclRevision, aclDigest, purge, err := store.resolveAuthorityTx(ctx, tx, material)
	if err != nil || aclRevision != material.ACLRevision || aclDigest != material.ACLDigest || purge != material.Purge ||
		episode.Authority.ACLRevision != aclRevision || episode.Authority.ACLDigest != aclDigest || episode.Authority.PurgeGeneration != purge ||
		episode.Authority.ConsentRevision != material.ConsentRev || episode.Authority.ConsentDigest != material.ConsentDigest {
		return SourceEpisodeDualWriteResult{}, ErrMeetingSourceEpisodeStale
	}
	if store.Failpoint != nil {
		if err := store.Failpoint("after_authority_before_insert"); err != nil {
			return SourceEpisodeDualWriteResult{}, err
		}
	}

	if prior, found, err := postgresSourceEpisodeByIdempotency(ctx, tx, episode.Header.TenantID, episode.IdempotencyKeyDigest); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	} else if found {
		replayed, replayErr := SourceEpisodeReplayDecision(prior, episode)
		if replayErr != nil || !replayed {
			return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
		}
		return SourceEpisodeDualWriteResult{Reference: referenceFromHeader(prior.Header), Replayed: true}, tx.Commit(ctx)
	}

	current, found, active, err := postgresSourceEpisodeHeadTx(ctx, tx, episode.Header.TenantID, episode.Header.ID, true)
	if err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	if (expected == nil) != !found || expected != nil && *expected != referenceFromHeader(current.Header) || found && !active && episode.Header.Revision != current.Header.Revision+1 {
		return SourceEpisodeDualWriteResult{}, ErrSourceEpisodeConflict
	}
	raw, err := json.Marshal(episode)
	if err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	digest := func(value string) []byte { decoded, _ := hex.DecodeString(value); return decoded }
	var supersedesRevision any
	var supersedesDigest any
	if expected != nil {
		supersedesRevision, supersedesDigest = expected.Revision, digest(expected.Digest)
	}
	_, err = tx.Exec(ctx, `INSERT INTO stride_source_episode_revisions(
		tenant_id,episode_id,revision,content_digest,kind,source_family,source_object_id,source_revision,source_digest,
		retrieval_family,retrieval_object_id,retrieval_revision,retrieval_digest,sitting_id,acl_revision,acl_digest,
		consent_revision,consent_digest,purge_generation,phase_receipt_digest,idempotency_key_digest,
		supersedes_revision,supersedes_digest,episode)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24::jsonb)`,
		episode.Header.TenantID, episode.Header.ID, episode.Header.Revision, digest(episode.Header.ContentDigest), string(episode.Kind),
		episode.Source.SourceFamily, episode.Source.ObjectID, episode.Source.ContentRevision, digest(episode.Source.ContentDigest),
		episode.RetrievalBody.SourceFamily, episode.RetrievalBody.ObjectID, episode.RetrievalBody.ContentRevision, digest(episode.RetrievalBody.ContentDigest),
		episode.Scope.SittingID, episode.Authority.ACLRevision, digest(episode.Authority.ACLDigest), episode.Authority.ConsentRevision,
		digest(episode.Authority.ConsentDigest), episode.Authority.PurgeGeneration, digest(episode.PhaseProof.ReceiptDigest),
		digest(episode.IdempotencyKeyDigest), supersedesRevision, supersedesDigest, raw)
	if err != nil {
		return SourceEpisodeDualWriteResult{}, postgresSourceEpisodeConflict(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stride_source_episode_heads(tenant_id,episode_id,revision,content_digest,sitting_id,purge_generation,active)
		VALUES($1,$2,$3,$4,$5,$6,true)
		ON CONFLICT(tenant_id,episode_id) DO UPDATE SET revision=EXCLUDED.revision,content_digest=EXCLUDED.content_digest,
		sitting_id=EXCLUDED.sitting_id,purge_generation=EXCLUDED.purge_generation,active=true,updated_at=clock_timestamp()`,
		episode.Header.TenantID, episode.Header.ID, episode.Header.Revision, digest(episode.Header.ContentDigest), episode.Scope.SittingID, purge); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	for _, source := range meetingSourceCanonicalObjects(material) {
		if _, err = tx.Exec(ctx, `INSERT INTO stride_source_episode_sources(tenant_id,episode_id,episode_revision,object_type,object_id,content_digest)
			VALUES($1,$2,$3,'memory',$4,$5)`, episode.Header.TenantID, episode.Header.ID, episode.Header.Revision, source.ID, digest(source.BodyDigest)); err != nil {
			return SourceEpisodeDualWriteResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceEpisodeDualWriteResult{}, err
	}
	return SourceEpisodeDualWriteResult{Reference: referenceFromHeader(episode.Header)}, nil
}

type meetingSourceACLObject struct {
	ID, Digest string
	Revision   int64
	ACL        int64
}

type meetingSourceACLGrant struct {
	ObjectID, SubjectType, SubjectID, Action, RoomID, SittingID string
	ACLRevision                                                 int64
	Revision                                                    *int64
}

func (store *PostgresMeetingSourceEpisodeStore) resolveAuthorityTx(ctx context.Context, tx pgx.Tx, material postCloseMeetingSourceMaterial) (int64, string, int64, error) {
	sources := meetingSourceCanonicalObjects(material)
	if len(sources) < 3 {
		return 0, "", 0, ErrMeetingSourceEpisodeStale
	}
	ids := make([]string, len(sources))
	expected := map[string]string{}
	for index, source := range sources {
		ids[index], expected[source.ID] = source.ID, source.BodyDigest
	}
	rows, err := tx.Query(ctx, `SELECT object_id,content_revision,encode(content_sha256,'hex'),acl_version
		FROM objects WHERE tenant_id=$1 AND object_type='memory' AND object_id=ANY($2) AND deleted_at IS NULL
		ORDER BY object_id FOR SHARE`, canonicalTenantID(), ids)
	if err != nil {
		return 0, "", 0, err
	}
	objects := []meetingSourceACLObject{}
	var maxACL int64
	for rows.Next() {
		var object meetingSourceACLObject
		if err := rows.Scan(&object.ID, &object.Revision, &object.Digest, &object.ACL); err != nil {
			rows.Close()
			return 0, "", 0, err
		}
		if expected[object.ID] != object.Digest {
			rows.Close()
			return 0, "", 0, ErrMeetingSourceEpisodeStale
		}
		if object.ACL > maxACL {
			maxACL = object.ACL
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, "", 0, err
	}
	rows.Close()
	if len(objects) != len(sources) || maxACL < 1 {
		return 0, "", 0, ErrMeetingSourceEpisodeStale
	}
	grantRows, err := tx.Query(ctx, `SELECT g.object_id,g.acl_version,g.subject_type,g.subject_id,g.action,COALESCE(g.room_id,''),COALESCE(g.sitting_id,''),g.revision
		FROM object_grants g JOIN objects o ON o.tenant_id=g.tenant_id AND o.object_type=g.object_type AND o.object_id=g.object_id
		WHERE g.tenant_id=$1 AND g.object_type='memory' AND g.object_id=ANY($2)
		AND g.acl_version=o.acl_version AND (g.revision IS NULL OR g.revision=o.content_revision)
		AND g.revoked_at IS NULL AND (g.expires_at IS NULL OR g.expires_at>clock_timestamp())
		ORDER BY object_id,acl_version,subject_type,subject_id,action`, canonicalTenantID(), ids)
	if err != nil {
		return 0, "", 0, err
	}
	grants := []meetingSourceACLGrant{}
	for grantRows.Next() {
		var grant meetingSourceACLGrant
		if err := grantRows.Scan(&grant.ObjectID, &grant.ACLRevision, &grant.SubjectType, &grant.SubjectID, &grant.Action, &grant.RoomID, &grant.SittingID, &grant.Revision); err != nil {
			grantRows.Close()
			return 0, "", 0, err
		}
		grants = append(grants, grant)
	}
	grantRows.Close()
	if len(grants) == 0 {
		return 0, "", 0, ErrMeetingSourceEpisodeStale
	}
	if !meetingSourceAudienceCanReadAll(material.Audience, objects, grants) {
		return 0, "", 0, ErrMeetingSourceEpisodeStale
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stride_source_episode_tenant_fences(tenant_id,purge_generation) VALUES($1,0) ON CONFLICT DO NOTHING`, canonicalTenantID()); err != nil {
		return 0, "", 0, err
	}
	var purge int64
	if err := tx.QueryRow(ctx, `SELECT purge_generation FROM stride_source_episode_tenant_fences WHERE tenant_id=$1 FOR UPDATE`, canonicalTenantID()).Scan(&purge); err != nil {
		return 0, "", 0, err
	}
	var purged bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purge_ledger WHERE tenant_id=$1 AND object_type='memory' AND object_id=ANY($2))`, canonicalTenantID(), ids).Scan(&purged); err != nil || purged {
		return 0, "", 0, ErrMeetingSourceEpisodeStale
	}
	aclDigest, err := STRIDEContractDigest(struct {
		Objects  []meetingSourceACLObject
		Grants   []meetingSourceACLGrant
		Audience STRIDEAudience
	}{objects, grants, material.Audience})
	return maxACL, aclDigest, purge, err
}

func meetingSourceAudienceCanReadAll(audience STRIDEAudience, objects []meetingSourceACLObject, grants []meetingSourceACLGrant) bool {
	if audience.Validate() != nil || len(objects) == 0 {
		return false
	}
	aclStore := immutableCanonicalParityACLStore{objects: map[string]ACLObject{}, grants: map[string][]ACLGrant{}}
	for _, object := range objects {
		ref := ACLObjectRef{TenantID: canonicalTenantID(), Type: "memory", ID: object.ID, ACLVersion: object.ACL}
		aclStore.objects[aclObjectKey(ref)] = ACLObject{
			Ref: ref, CurrentContentRevision: object.Revision, CurrentContentDigest: object.Digest,
		}
	}
	for index, grant := range grants {
		ref := ACLObjectRef{TenantID: canonicalTenantID(), Type: "memory", ID: grant.ObjectID, ACLVersion: grant.ACLRevision}
		converted := ACLGrant{
			ID: "meeting-source-" + strconv.Itoa(index), TenantID: canonicalTenantID(), ObjectType: "memory", ObjectID: grant.ObjectID,
			ACLVersion: grant.ACLRevision, SubjectID: grant.SubjectID, Actions: []ACLAction{ACLAction(grant.Action)}, RoomID: grant.RoomID, SittingID: grant.SittingID,
		}
		if grant.SubjectType == string(ACLSubjectTeam) {
			converted.SubjectKind = ACLSubjectTeam
		} else {
			converted.SubjectKind = ACLSubjectPrincipal
			converted.SubjectPrincipalKind = ACLPrincipalKind(grant.SubjectType)
		}
		aclStore.grants[aclObjectKey(ref)] = append(aclStore.grants[aclObjectKey(ref)], converted)
	}
	kernel := AuthorizationKernel{Store: aclStore}
	for _, rawPrincipal := range audience.Principals {
		principal := meetingSourceACLPrincipal(rawPrincipal)
		if principal.ID == "" {
			return false
		}
		for _, object := range objects {
			ref := ACLObjectRef{TenantID: canonicalTenantID(), Type: "memory", ID: object.ID, ACLVersion: object.ACL}
			if !kernel.Authorize(context.Background(), principal, ACLReadMetadata, ref, ACLRevisionRef{}).Allowed ||
				!kernel.Authorize(context.Background(), principal, ACLReadContent, ref, ACLRevisionRef{ContentRevision: object.Revision, ContentDigest: object.Digest}).Allowed {
				return false
			}
		}
	}
	return true
}

func meetingSourceACLPrincipal(raw string) ACLPrincipal {
	kind, id, ok := splitCanonicalImportPrincipal(raw)
	if !ok {
		kind, id = ACLPrincipalUser, strings.TrimSpace(raw)
	}
	principal := ACLPrincipal{TenantID: canonicalTenantID(), Kind: kind, ID: id}
	if kind == ACLPrincipalUser {
		principal.TeamIDs = []string{canonicalLegacyOrgTeamID}
	}
	return principal
}

func meetingSourceCanonicalObjects(material postCloseMeetingSourceMaterial) []meetingMemoryEntry {
	result := append(cloneMemoryEntries(material.Transcripts), cloneMemoryEntry(material.Brain), cloneMemoryEntry(material.Digest))
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func sortedMeetingSourceConsentFences(fences []ConsentFence) []ConsentFence {
	result := append([]ConsentFence(nil), fences...)
	sort.Slice(result, func(i, j int) bool {
		left, right := consentBindingKey(result[i].binding), consentBindingKey(result[j].binding)
		if left != right {
			return left < right
		}
		return result[i].lane < result[j].lane
	})
	return result
}

func validateMeetingSourceConsentTx(ctx context.Context, tx pgx.Tx, fences []ConsentFence) error {
	for _, fence := range sortedMeetingSourceConsentFences(fences) {
		scopes, ok := consentLaneScopes(fence.lane)
		if !ok {
			return ErrConsentFenceStale
		}
		scopes, err := normalizeConsentQuery(ConsentQuery{
			TenantID: fence.binding.TenantID, PrincipalKind: fence.binding.PrincipalKind, PrincipalID: fence.binding.PrincipalID,
			RoomID: fence.binding.RoomID, SittingID: fence.binding.SittingID, PolicyVersion: fence.policy, Scopes: scopes,
		})
		if err != nil {
			return ErrConsentFenceStale
		}
		records := map[ConsentScope]string{}
		for _, scope := range scopes {
			if fence.binding.PrincipalKind == ACLPrincipalUser {
				authority := &ConsentLaneAuthority{PolicyVersion: fence.policy}
				records[scope] = authority.policyDecisionRecordID(fence.binding, scope, "internal-rules-of-road")
				continue
			}
			var id, status string
			err := tx.QueryRow(ctx, `SELECT consent_id::text,status FROM consent_records
				WHERE tenant_id=$1 AND principal_type=$2 AND principal_id=$3 AND room_id=$4 AND sitting_id=$5 AND policy_version=$6
				AND $7=ANY(scopes) AND (expires_at IS NULL OR expires_at>clock_timestamp())
				ORDER BY effective_at DESC,CASE status WHEN 'withdrawn' THEN 3 WHEN 'denied' THEN 2 ELSE 1 END DESC,consent_id DESC LIMIT 1`,
				fence.binding.TenantID, string(fence.binding.PrincipalKind), fence.binding.PrincipalID, fence.binding.RoomID, fence.binding.SittingID, fence.policy, string(scope)).Scan(&id, &status)
			if errors.Is(err, pgx.ErrNoRows) {
				authority := &ConsentLaneAuthority{PolicyVersion: fence.policy}
				records[scope] = authority.policyDecisionRecordID(fence.binding, scope, "guest-default")
				continue
			}
			if err != nil || ConsentDisposition(status) != ConsentGranted {
				return ErrConsentFenceStale
			}
			records[scope] = id
		}
		if consentRecordSetDigest(records) != fence.recordDigest {
			return ErrConsentFenceStale
		}
	}
	return nil
}

func postgresSourceEpisodeByIdempotency(ctx context.Context, tx pgx.Tx, tenantID, digest string) (SourceEpisode, bool, error) {
	key, _ := hex.DecodeString(digest)
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT episode FROM stride_source_episode_revisions WHERE tenant_id=$1 AND idempotency_key_digest=$2`, tenantID, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceEpisode{}, false, nil
	}
	if err != nil {
		return SourceEpisode{}, false, err
	}
	var episode SourceEpisode
	if json.Unmarshal(raw, &episode) != nil || episode.Validate() != nil {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	return episode, true, nil
}

func postgresSourceEpisodeHeadTx(ctx context.Context, tx pgx.Tx, tenantID, episodeID string, lock bool) (SourceEpisode, bool, bool, error) {
	query := `SELECT r.episode,h.active FROM stride_source_episode_heads h JOIN stride_source_episode_revisions r USING(tenant_id,episode_id,revision) WHERE h.tenant_id=$1 AND h.episode_id=$2`
	if lock {
		query += ` FOR UPDATE OF h`
	}
	var raw []byte
	var active bool
	err := tx.QueryRow(ctx, query, tenantID, episodeID).Scan(&raw, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceEpisode{}, false, false, nil
	}
	if err != nil {
		return SourceEpisode{}, false, false, err
	}
	var episode SourceEpisode
	if json.Unmarshal(raw, &episode) != nil || episode.Validate() != nil {
		return SourceEpisode{}, false, false, ErrSourceEpisodeInvalid
	}
	return episode, true, active, nil
}

func postgresSourceEpisodeConflict(err error) error {
	if isUniqueViolation(err) {
		return ErrSourceEpisodeConflict
	}
	return err
}

func (store *PostgresMeetingSourceEpisodeStore) CurrentSourceEpisode(ctx context.Context, tenantID, episodeID string) (SourceEpisode, bool, error) {
	if store == nil || store.canonical == nil {
		return SourceEpisode{}, false, ErrSourceEpisodeUnavailable
	}
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SourceEpisode{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	episode, found, active, err := postgresSourceEpisodeHeadTx(ctx, tx, tenantID, episodeID, false)
	if err != nil || !found || !active {
		return SourceEpisode{}, false, err
	}
	return episode, true, tx.Commit(ctx)
}

func (store *PostgresMeetingSourceEpisodeStore) ListSourceEpisodes(ctx context.Context, tenantID, cursor string) (SourceEpisodeCatalogPage, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || !strideIdentifier(tenantID) {
		return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
	}
	start := 0
	if cursor != "" {
		var err error
		start, err = strconv.Atoi(cursor)
		if err != nil || start < 0 {
			return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
		}
	}
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var purge int64
	var fenceAt time.Time
	err = tx.QueryRow(ctx, `SELECT purge_generation,updated_at FROM stride_source_episode_tenant_fences WHERE tenant_id=$1`, tenantID).Scan(&purge, &fenceAt)
	if errors.Is(err, pgx.ErrNoRows) {
		purge, fenceAt, err = 0, time.Unix(0, 0).UTC(), nil
	}
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	rows, err := tx.Query(ctx, `SELECT r.episode,h.updated_at FROM stride_source_episode_heads h
		JOIN stride_source_episode_revisions r USING(tenant_id,episode_id,revision)
		WHERE h.tenant_id=$1 AND h.active AND h.purge_generation=$2 ORDER BY h.episode_id`, tenantID, purge)
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	episodes := []SourceEpisode{}
	snapshotAt := fenceAt.UTC()
	for rows.Next() {
		var raw []byte
		var updated time.Time
		if err := rows.Scan(&raw, &updated); err != nil {
			rows.Close()
			return SourceEpisodeCatalogPage{}, err
		}
		var episode SourceEpisode
		if json.Unmarshal(raw, &episode) != nil || episode.Validate() != nil || episode.Header.TenantID != tenantID || episode.Authority.PurgeGeneration != purge {
			rows.Close()
			return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
		}
		episodes = append(episodes, episode)
		if updated.After(snapshotAt) {
			snapshotAt = updated.UTC()
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return SourceEpisodeCatalogPage{}, err
	}
	rows.Close()
	if start > len(episodes) {
		return SourceEpisodeCatalogPage{}, ErrSourceEpisodeInvalid
	}
	snapshotID, err := STRIDEContractDigest(struct {
		TenantID string
		Purge    int64
		Episodes []SourceEpisode
	}{tenantID, purge, episodes})
	if err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	end := start + 128
	if end > len(episodes) {
		end = len(episodes)
	}
	terminal := end == len(episodes)
	next := ""
	if !terminal {
		next = strconv.Itoa(end)
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceEpisodeCatalogPage{}, err
	}
	return SourceEpisodeCatalogPage{
		SnapshotID: snapshotID, SnapshotAt: snapshotAt, PurgeGeneration: purge,
		Episodes: cloneSourceEpisodes(episodes[start:end]), NextCursor: next, Terminal: terminal,
	}, nil
}

func (store *PostgresMeetingSourceEpisodeStore) FindSourceEpisodeByRetrievalBody(ctx context.Context, tenantID string, locator SourceEpisodeBodyLocator) (SourceEpisode, bool, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || !strideIdentifier(tenantID) || !strideIdentifier(locator.SourceFamily) ||
		!strideIdentifier(locator.ObjectID) || locator.ContentRevision < 1 || !isHexDigest(locator.ContentDigest) {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	digest, _ := hex.DecodeString(locator.ContentDigest)
	rows, err := store.canonical.pool.Query(ctx, `SELECT r.episode FROM stride_source_episode_heads h
		JOIN stride_source_episode_revisions r USING(tenant_id,episode_id,revision)
		JOIN stride_source_episode_tenant_fences f USING(tenant_id)
		WHERE h.tenant_id=$1 AND h.active AND h.purge_generation=f.purge_generation
		AND r.retrieval_family=$2 AND r.retrieval_object_id=$3 AND r.retrieval_revision=$4 AND r.retrieval_digest=$5
		ORDER BY h.episode_id LIMIT 2`, tenantID, locator.SourceFamily, locator.ObjectID, locator.ContentRevision, digest)
	if err != nil {
		return SourceEpisode{}, false, err
	}
	defer rows.Close()
	var matched *SourceEpisode
	for rows.Next() {
		if matched != nil {
			return SourceEpisode{}, false, ErrSourceEpisodeConflict
		}
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return SourceEpisode{}, false, err
		}
		var episode SourceEpisode
		if json.Unmarshal(raw, &episode) != nil || episode.Validate() != nil || episode.Header.TenantID != tenantID || sourceEpisodeBodyLocator(episode.RetrievalBody) != locator {
			return SourceEpisode{}, false, ErrSourceEpisodeInvalid
		}
		matched = &episode
	}
	if err := rows.Err(); err != nil {
		return SourceEpisode{}, false, err
	}
	if matched == nil {
		return SourceEpisode{}, false, nil
	}
	return cloneSourceEpisode(*matched), true, nil
}

func (store *PostgresMeetingSourceEpisodeStore) FindSourceEpisodeByACLObject(ctx context.Context, ref ACLObjectRef) (SourceEpisode, bool, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || !strideIdentifier(ref.TenantID) || !strideIdentifier(ref.Type) ||
		!strideIdentifier(ref.ID) || ref.ACLVersion < 1 {
		return SourceEpisode{}, false, ErrSourceEpisodeInvalid
	}
	rows, err := store.canonical.pool.Query(ctx, `SELECT r.episode FROM stride_source_episode_heads h
		JOIN stride_source_episode_revisions r USING(tenant_id,episode_id,revision)
		JOIN stride_source_episode_tenant_fences f USING(tenant_id)
		WHERE h.tenant_id=$1 AND h.active AND h.purge_generation=f.purge_generation
		AND r.retrieval_family=$2 AND r.retrieval_object_id=$3 AND r.acl_revision=$4
		ORDER BY h.episode_id LIMIT 2`, ref.TenantID, ref.Type, ref.ID, ref.ACLVersion)
	if err != nil {
		return SourceEpisode{}, false, err
	}
	defer rows.Close()
	var matched *SourceEpisode
	for rows.Next() {
		if matched != nil {
			return SourceEpisode{}, false, ErrSourceEpisodeConflict
		}
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return SourceEpisode{}, false, err
		}
		var episode SourceEpisode
		if json.Unmarshal(raw, &episode) != nil || episode.Validate() != nil || episode.Header.TenantID != ref.TenantID ||
			episode.RetrievalBody.SourceFamily != ref.Type || episode.RetrievalBody.ObjectID != ref.ID || episode.Authority.ACLRevision != ref.ACLVersion {
			return SourceEpisode{}, false, ErrSourceEpisodeInvalid
		}
		matched = &episode
	}
	if err := rows.Err(); err != nil {
		return SourceEpisode{}, false, err
	}
	if matched == nil {
		return SourceEpisode{}, false, nil
	}
	return cloneSourceEpisode(*matched), true, nil
}

func (store *PostgresMeetingSourceEpisodeStore) CurrentPurgeGeneration(ctx context.Context, tenantID string) (int64, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || !strideIdentifier(tenantID) {
		return 0, ErrSourceEpisodeInvalid
	}
	var purge int64
	err := store.canonical.pool.QueryRow(ctx, `SELECT purge_generation FROM stride_source_episode_tenant_fences WHERE tenant_id=$1`, tenantID).Scan(&purge)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return purge, err
}

// WithCurrentSourceEpisodeAuthority keeps PostgreSQL's exact active heads,
// tenant purge fence, source projections, and current grants stable through a
// native body read. A concurrent source/ACL/consent/purge mutation either
// waits or forces this serializable transaction to fail before the caller may
// use the body.
func (store *PostgresMeetingSourceEpisodeStore) WithCurrentSourceEpisodeAuthority(ctx context.Context, episodes []SourceEpisode, use func() error) error {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || len(episodes) == 0 || use == nil {
		return ErrSourceEpisodeAuthorityStale
	}
	tenantID := episodes[0].Header.TenantID
	episodeIDs := make([]string, 0, len(episodes))
	expected := make(map[string]STRIDEReference, len(episodes))
	for _, episode := range episodes {
		if episode.Validate() != nil || episode.Kind != SourceEpisodeMeetingAnalysis || episode.Header.TenantID != tenantID {
			return ErrSourceEpisodeAuthorityStale
		}
		if _, duplicate := expected[episode.Header.ID]; duplicate {
			return ErrSourceEpisodeAuthorityStale
		}
		episodeIDs = append(episodeIDs, episode.Header.ID)
		expected[episode.Header.ID] = referenceFromHeader(episode.Header)
	}
	sort.Strings(episodeIDs)
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var purge int64
	if err := tx.QueryRow(ctx, `SELECT purge_generation FROM stride_source_episode_tenant_fences WHERE tenant_id=$1 FOR SHARE`, tenantID).Scan(&purge); err != nil {
		return ErrSourceEpisodeAuthorityStale
	}
	rows, err := tx.Query(ctx, `SELECT h.episode_id,r.episode FROM stride_source_episode_heads h
		JOIN stride_source_episode_revisions r USING(tenant_id,episode_id,revision)
		WHERE h.tenant_id=$1 AND h.episode_id=ANY($2) AND h.active AND h.purge_generation=$3
		ORDER BY h.episode_id FOR SHARE OF h`, tenantID, episodeIDs, purge)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var episodeID string
		var raw []byte
		if err := rows.Scan(&episodeID, &raw); err != nil {
			rows.Close()
			return err
		}
		var current SourceEpisode
		if json.Unmarshal(raw, &current) != nil || current.Validate() != nil || referenceFromHeader(current.Header) != expected[episodeID] || current.Authority.PurgeGeneration != purge {
			rows.Close()
			return ErrSourceEpisodeAuthorityStale
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(episodes) {
		return ErrSourceEpisodeAuthorityStale
	}
	sourceRows, err := tx.Query(ctx, `SELECT s.episode_id,s.object_id FROM stride_source_episode_sources s
		JOIN objects o ON o.tenant_id=s.tenant_id AND o.object_type=s.object_type AND o.object_id=s.object_id
		WHERE s.tenant_id=$1 AND s.episode_id=ANY($2) AND o.deleted_at IS NULL AND o.content_sha256=s.content_digest
		ORDER BY s.episode_id,s.object_id FOR SHARE OF o`, tenantID, episodeIDs)
	if err != nil {
		return err
	}
	sourceCount := 0
	for sourceRows.Next() {
		sourceCount++
	}
	if err := sourceRows.Err(); err != nil {
		sourceRows.Close()
		return err
	}
	sourceRows.Close()
	var expectedSourceCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM stride_source_episode_sources WHERE tenant_id=$1 AND episode_id=ANY($2)`, tenantID, episodeIDs).Scan(&expectedSourceCount); err != nil || sourceCount == 0 || sourceCount != expectedSourceCount {
		return ErrSourceEpisodeAuthorityStale
	}
	grantRows, err := tx.Query(ctx, `SELECT g.grant_id FROM object_grants g
		JOIN stride_source_episode_sources s ON s.tenant_id=g.tenant_id AND s.object_type=g.object_type AND s.object_id=g.object_id
		JOIN objects o ON o.tenant_id=g.tenant_id AND o.object_type=g.object_type AND o.object_id=g.object_id
		WHERE s.tenant_id=$1 AND s.episode_id=ANY($2) AND g.acl_version=o.acl_version
		AND (g.revision IS NULL OR g.revision=o.content_revision) AND g.revoked_at IS NULL
		AND (g.expires_at IS NULL OR g.expires_at>clock_timestamp()) FOR SHARE OF g`, tenantID, episodeIDs)
	if err != nil {
		return err
	}
	grantCount := 0
	for grantRows.Next() {
		grantCount++
	}
	if err := grantRows.Err(); err != nil {
		grantRows.Close()
		return err
	}
	grantRows.Close()
	if grantCount == 0 {
		return ErrSourceEpisodeAuthorityStale
	}
	if err := use(); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrSourceEpisodeAuthorityStale
	}
	return nil
}

var (
	_ DurableSourceEpisodeCatalog              = (*PostgresMeetingSourceEpisodeStore)(nil)
	_ DurableSourceEpisodeAuthorizationCatalog = (*PostgresMeetingSourceEpisodeStore)(nil)
	_ BrainPurgeGenerationResolver             = (*PostgresMeetingSourceEpisodeStore)(nil)
)

func (store *PostgresMeetingSourceEpisodeStore) LatestSourceEpisode(ctx context.Context, tenantID, episodeID string) (SourceEpisode, bool, error) {
	if store == nil || store.canonical == nil {
		return SourceEpisode{}, false, ErrSourceEpisodeUnavailable
	}
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SourceEpisode{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	episode, found, _, err := postgresSourceEpisodeHeadTx(ctx, tx, tenantID, episodeID, false)
	if err != nil || !found {
		return SourceEpisode{}, found, err
	}
	return episode, true, tx.Commit(ctx)
}

func (store *PostgresMeetingSourceEpisodeStore) TombstoneSourceEpisode(ctx context.Context, tombstone SourceEpisodeTombstone) (bool, error) {
	if store == nil || store.canonical == nil || store.canonical.pool == nil || tombstone.Validate() != nil {
		return false, ErrSourceEpisodeInvalid
	}
	tx, err := store.canonical.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	key, _ := hex.DecodeString(tombstone.IdempotencyKeyDigest)
	var existingCause string
	err = tx.QueryRow(ctx, `SELECT cause FROM stride_source_episode_tombstones WHERE tenant_id=$1 AND idempotency_key_digest=$2`, tombstone.TenantID, key).Scan(&existingCause)
	if err == nil {
		if existingCause != tombstone.Cause {
			return false, ErrSourceEpisodeConflict
		}
		return true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	current, found, active, err := postgresSourceEpisodeHeadTx(ctx, tx, tombstone.TenantID, tombstone.Episode.ID, true)
	if err != nil || !found || !active || referenceFromHeader(current.Header) != tombstone.Episode {
		return false, ErrSourceEpisodeConflict
	}
	var purge int64
	if err := tx.QueryRow(ctx, `SELECT purge_generation FROM stride_source_episode_tenant_fences WHERE tenant_id=$1 FOR UPDATE`, tombstone.TenantID).Scan(&purge); err != nil {
		return false, err
	}
	if tombstone.Cause == SourceEpisodeTombstonePurge {
		if tombstone.PurgeGeneration <= purge {
			return false, ErrSourceEpisodeConflict
		}
		purge = tombstone.PurgeGeneration
		if _, err := tx.Exec(ctx, `UPDATE stride_source_episode_tenant_fences SET purge_generation=$2,updated_at=clock_timestamp() WHERE tenant_id=$1`, tombstone.TenantID, purge); err != nil {
			return false, err
		}
	} else if tombstone.PurgeGeneration != purge {
		return false, ErrSourceEpisodeConflict
	}
	digest := func(value string) []byte { decoded, _ := hex.DecodeString(value); return decoded }
	if _, err := tx.Exec(ctx, `INSERT INTO stride_source_episode_tombstones(tenant_id,episode_id,episode_revision,episode_digest,cause,purge_generation,reason_digest,idempotency_key_digest,occurred_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, tombstone.TenantID, tombstone.Episode.ID, tombstone.Episode.Revision, digest(tombstone.Episode.Digest), tombstone.Cause,
		purge, digest(tombstone.ReasonDigest), key, tombstone.OccurredAt); err != nil {
		return false, postgresSourceEpisodeConflict(err)
	}
	if tombstone.Cause == SourceEpisodeTombstonePurge {
		if _, err := tx.Exec(ctx, `UPDATE stride_source_episode_heads SET active=false,updated_at=clock_timestamp() WHERE tenant_id=$1 AND active`, tombstone.TenantID); err != nil {
			return false, err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE stride_source_episode_heads SET active=false,updated_at=clock_timestamp() WHERE tenant_id=$1 AND episode_id=$2`, tombstone.TenantID, tombstone.Episode.ID); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}
