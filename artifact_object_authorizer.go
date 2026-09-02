package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const artifactContentDigestMetadataKey = "contentDigest"

// ArtifactAuthorizationHeader is the body-free security projection persisted
// beside an artifact. Handlers authorize this header before requesting Text.
type ArtifactAuthorizationHeader struct {
	TenantID        string
	ObjectID        string
	ACLVersion      int64
	ContentRevision int64
	ContentDigest   string
	Visibility      string
	OwnerEmail      string
	OriginSurface   string
	RoomID          string
	SittingID       string
	MediaGeneration uint64
	AssetRefs       map[string]struct{}
	Revisions       map[string]ACLRevisionRef // body blob ref -> exact revision
}

// ObjectAuthorizer is the one principal-aware boundary for artifact objects.
// Implementations return only allow/deny; handlers deliberately collapse a
// denial and a missing object into the same 404 response.
type ObjectAuthorizer interface {
	AuthorizeArtifactHeader(context.Context, *userAccount, ACLAction, ArtifactAuthorizationHeader) bool
}

// StrideE10PersonArtifactAuthorizer is the only cutover artifact seam. The
// legacy email principal is deliberately absent; implementations must decide
// from the server-derived current person and organization capability.
type StrideE10PersonArtifactAuthorizer interface {
	AuthorizeArtifactHeaderForStridePrincipal(context.Context, StrideE10TenantPrincipal, ACLAction, ArtifactAuthorizationHeader) bool
}

// LegacyCompatibleObjectAuthorizer uses canonical ACLs when configured. In
// shadow mode an explicit organization-visible legacy artifact remains
// available to signed-in members; private artifacts never use that fallback.
type LegacyCompatibleObjectAuthorizer struct {
	Kernel            *AuthorizationKernel
	CanonicalRequired bool
	TenantID          string
}

func (authorizer LegacyCompatibleObjectAuthorizer) AuthorizeArtifactHeader(ctx context.Context, user *userAccount, action ACLAction, header ArtifactAuthorizationHeader) bool {
	if user == nil || strings.TrimSpace(header.ObjectID) == "" {
		return false
	}
	expectedTenant := strings.TrimSpace(authorizer.TenantID)
	if expectedTenant == "" {
		expectedTenant = canonicalArtifactTenantID()
	}
	if strings.TrimSpace(header.TenantID) == "" || header.TenantID != expectedTenant {
		return false
	}
	private := legacyArtifactHeaderIsPrivate(header)
	if authorizer.Kernel != nil {
		tenantID := expectedTenant
		aclVersion := header.ACLVersion
		if aclVersion < 1 {
			aclVersion = 1
		}
		decision := authorizer.Kernel.Authorize(ctx,
			ACLPrincipal{TenantID: tenantID, ID: normalizeAccountEmail(user.Email), Kind: ACLPrincipalUser, TeamIDs: []string{"organization"}},
			action,
			ACLObjectRef{TenantID: tenantID, Type: "artifact", ID: header.ObjectID, ACLVersion: aclVersion},
			ACLRevisionRef{ContentRevision: header.ContentRevision, ContentDigest: header.ContentDigest},
		)
		if decision.Allowed {
			return true
		}
		if authorizer.CanonicalRequired {
			return false
		}
	}
	// Compatibility is an explicit policy, not an implicit owner/admin bypass:
	// current unstamped/team artifacts are organization-visible; private objects
	// fail closed until canonical grants are available.
	if !private {
		return legacyArtifactHeaderOrganizationVisible(header)
	}
	// The sole legacy private fallback is immutable ownership. There is no
	// admin read bypass: private-thread ownership wins, then a valid persisted
	// requestedBy/createdBy email stamped into OwnerEmail at creation.
	return normalizeAccountEmail(user.Email) != "" && normalizeAccountEmail(user.Email) == normalizeAccountEmail(header.OwnerEmail)
}

func (authorizer LegacyCompatibleObjectAuthorizer) AuthorizeArtifact(ctx context.Context, user *userAccount, action ACLAction, artifact meetingMemoryEntry) bool {
	return authorizer.AuthorizeArtifactHeader(ctx, user, action, artifactAuthorizationHeaderFromEntry(artifact))
}

func canonicalArtifactTenantID() string {
	if tenant := strings.TrimSpace(os.Getenv("BONFIRE_TENANT_ID")); tenant != "" {
		return tenant
	}
	return "bonfire"
}

func legacyArtifactHeaderOrganizationVisible(header ArtifactAuthorizationHeader) bool {
	visibility := strings.ToLower(strings.TrimSpace(header.Visibility))
	switch visibility {
	case "private", "owner":
		return false
	case "organization", "org", "team", "public":
		return true
	default:
		// This is the pre-ACL production contract: unstamped artifacts were shared
		// with every seeded member. Preserve it explicitly during shadowing.
		return true
	}
}

func legacyArtifactIsPrivate(artifact meetingMemoryEntry) bool {
	return legacyArtifactHeaderIsPrivate(artifactAuthorizationHeaderFromEntry(artifact))
}

func legacyArtifactHeaderIsPrivate(header ArtifactAuthorizationHeader) bool {
	visibility := strings.ToLower(strings.TrimSpace(header.Visibility))
	return visibility == "private" || visibility == "owner" || strings.HasPrefix(strings.TrimSpace(header.OriginSurface), "chat:") && header.OwnerEmail != "" && visibility != "organization" && visibility != "public"
}

func artifactAuthorizationOwner(metadata map[string]string) string {
	normalized := normalizeAccountEmail(metadata["ownerEmail"])
	if strings.Contains(normalized, "@") {
		return normalized
	}
	return ""
}

func (store *meetingMemoryStore) resolveArtifactHeaderSecurityLocked(header ArtifactAuthorizationHeader) ArtifactAuthorizationHeader {
	origin := strings.TrimSpace(header.OriginSurface)
	threadID := strings.TrimPrefix(origin, "chat:")
	if threadID == origin || threadID == "" || store == nil {
		return header
	}
	if index, found := store.scoutChatEntryIndexByIDLocked(threadID); found {
		entry := store.entries[index]
		visibility := normalizeScoutChatVisibility(entry.Metadata["visibility"])
		header.Visibility = visibility
		if visibility == scoutChatVisibilityPrivate {
			header.OwnerEmail = normalizeAccountEmail(entry.Metadata["ownerEmail"])
		}
		return header
	}
	// Declared chat provenance with no body-free security projection is private
	// and ownerless. Legacy compatibility fails closed.
	header.Visibility = scoutChatVisibilityPrivate
	header.OwnerEmail = ""
	return header
}

func (store *meetingMemoryStore) artifactOriginSecurityProjection(origin string) (string, string, bool) {
	threadID := strings.TrimPrefix(strings.TrimSpace(origin), "chat:")
	if threadID == "" || threadID == strings.TrimSpace(origin) || store == nil {
		return "", "", false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if index, found := store.scoutChatEntryIndexByIDLocked(threadID); found {
		entry := store.entries[index]
		visibility := normalizeScoutChatVisibility(entry.Metadata["visibility"])
		owner := ""
		if visibility == scoutChatVisibilityPrivate {
			owner = normalizeAccountEmail(entry.Metadata["ownerEmail"])
		}
		return visibility, owner, true
	}
	return scoutChatVisibilityPrivate, "", true
}

// backfillArtifactAuthorizationProjections upgrades legacy records only in
// memory at boot. The JSONL source is untouched; a later ordinary artifact
// write persists the projection through the normal durable path.
func (store *meetingMemoryStore) backfillArtifactAuthorizationProjections() {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.backfillArtifactAuthorizationProjectionsLocked()
}

func (store *meetingMemoryStore) backfillArtifactAuthorizationProjectionsLocked() {
	for index := range store.entries {
		entry := &store.entries[index]
		if entry.Kind != meetingMemoryKindOSArtifact {
			continue
		}
		if entry.Metadata == nil {
			entry.Metadata = map[string]string{}
		}
		if strings.TrimSpace(entry.Metadata["tenantId"]) == "" {
			entry.Metadata["tenantId"] = canonicalArtifactTenantID()
		}
		if strings.TrimSpace(entry.Metadata["objectId"]) == "" {
			entry.Metadata["objectId"] = entry.ID
		}
		if strings.TrimSpace(entry.Metadata["aclVersion"]) == "" {
			entry.Metadata["aclVersion"] = "1"
		}
		if strings.TrimSpace(entry.Metadata["ownerEmail"]) == "" {
			for _, candidate := range []string{entry.Metadata["requestedBy"], entry.Metadata["createdBy"]} {
				owner := normalizeAccountEmail(candidate)
				if strings.Contains(owner, "@") {
					entry.Metadata["ownerEmail"] = owner
					break
				}
			}
		}
		header := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
		entry.Metadata["visibility"] = firstNonEmptyString(header.Visibility, "organization")
		entry.Metadata["ownerEmail"] = header.OwnerEmail
		if strings.TrimSpace(entry.Metadata[artifactContentDigestMetadataKey]) == "" {
			entry.Metadata[artifactContentDigestMetadataKey] = artifactCapabilityDigest(*entry)
		}
	}
}

func resolveArtifactHeaderOwner(header ArtifactAuthorizationHeader) ArtifactAuthorizationHeader {
	return kanbanApp.resolveArtifactHeaderOwner(header)
}

func (app *kanbanBoardApp) resolveArtifactHeaderOwner(header ArtifactAuthorizationHeader) ArtifactAuthorizationHeader {
	if app == nil || app.memory == nil {
		return header
	}
	app.memory.mu.Lock()
	header = app.memory.resolveArtifactHeaderSecurityLocked(header)
	app.memory.mu.Unlock()
	return header
}

var artifactObjectAuthorizer ObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}

func artifactAuthorized(ctx context.Context, user *userAccount, action ACLAction, artifact meetingMemoryEntry) bool {
	return artifactHeaderAuthorized(ctx, user, action, resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
}

func (app *kanbanBoardApp) artifactAuthorized(ctx context.Context, user *userAccount, action ACLAction, artifact meetingMemoryEntry) bool {
	return artifactHeaderAuthorized(ctx, user, action, app.resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
}

func artifactHeaderAuthorized(ctx context.Context, user *userAccount, action ACLAction, header ArtifactAuthorizationHeader) bool {
	if artifactObjectAuthorizer == nil {
		return false
	}
	if principal, canonical := strideE10TenantPrincipalFromContext(ctx); canonical {
		if strings.TrimSpace(header.TenantID) == "" || header.TenantID != principal.TenantID {
			return false
		}
		canonicalAuthorizer, ok := artifactObjectAuthorizer.(StrideE10PersonArtifactAuthorizer)
		if !ok {
			return false
		}
		return canonicalAuthorizer.AuthorizeArtifactHeaderForStridePrincipal(ctx, principal, action, header)
	}
	// A cutover read without the request's live capability must never fall back
	// to owner email or the process-wide artifact tenant.
	if strideE10TenantCutoverEnabled() {
		return false
	}
	return artifactObjectAuthorizer.AuthorizeArtifactHeader(ctx, user, action, header)
}

func artifactAuthorizationHeaderFromEntry(entry meetingMemoryEntry) ArtifactAuthorizationHeader {
	aclVersion, _ := strconv.ParseInt(strings.TrimSpace(entry.Metadata["aclVersion"]), 10, 64)
	if aclVersion < 1 {
		aclVersion = 1
	}
	header := ArtifactAuthorizationHeader{
		TenantID:        strings.TrimSpace(entry.Metadata["tenantId"]),
		ObjectID:        firstNonEmptyString(strings.TrimSpace(entry.Metadata["objectId"]), strings.TrimSpace(entry.ID)),
		ACLVersion:      aclVersion,
		ContentRevision: int64(artifactVersion(entry)),
		ContentDigest:   strings.TrimSpace(entry.Metadata[artifactContentDigestMetadataKey]),
		Visibility:      firstNonEmptyString(strings.TrimSpace(entry.Metadata["visibility"]), "organization"),
		OwnerEmail:      artifactAuthorizationOwner(entry.Metadata),
		OriginSurface:   strings.TrimSpace(entry.Metadata["originSurface"]),
		RoomID:          normalizeRoomID(entry.Metadata["roomId"]),
		SittingID:       firstNonEmptyString(strings.TrimSpace(entry.Metadata["sittingId"]), strings.TrimSpace(entry.Metadata["meetingId"])),
		AssetRefs:       map[string]struct{}{},
		Revisions:       map[string]ACLRevisionRef{},
	}
	header.MediaGeneration, _ = strconv.ParseUint(strings.TrimSpace(entry.Metadata["mediaGeneration"]), 10, 64)
	var assets []artifactAsset
	_ = json.Unmarshal([]byte(entry.Metadata[artifactAssetsMetadataKey]), &assets)
	for _, asset := range assets {
		if ref := strings.TrimSpace(asset.Ref); validBlobRef(ref) {
			header.AssetRefs[ref] = struct{}{}
		}
	}
	for _, revision := range artifactVersionHistory(entry) {
		if ref := strings.TrimSpace(revision.BodyBlobRef); validBlobRef(ref) {
			header.Revisions[ref] = ACLRevisionRef{ContentRevision: int64(revision.V), ContentDigest: strings.TrimSpace(revision.ContentDigest)}
		}
	}
	return header
}

// artifactAuthorizationHeaderByID projects metadata while holding the store
// lock and deliberately never copies or reads entry.Text.
func (store *meetingMemoryStore) artifactAuthorizationHeaderByID(id string) (ArtifactAuthorizationHeader, bool) {
	return store.artifactAuthorizationHeaderByIDInternal(id, false)
}

// artifactAuthorizationHeaderByIDForEvent is the event-publication projection.
// It admits server-owned soak canaries, which are intentionally hidden from
// every recall/authorization reader, but otherwise has the same locked lookup.
func (store *meetingMemoryStore) artifactAuthorizationHeaderByIDForEvent(id string) (ArtifactAuthorizationHeader, bool) {
	return store.artifactAuthorizationHeaderByIDInternal(id, true)
}

func (store *meetingMemoryStore) artifactAuthorizationHeaderByIDInternal(id string, includeHidden bool) (ArtifactAuthorizationHeader, bool) {
	if store == nil {
		return ArtifactAuthorizationHeader{}, false
	}
	id = strings.TrimSpace(id)
	store.mu.Lock()
	index, found := store.artifactEntryIndexByIDLocked(id)
	if found {
		entry := store.entries[index]
		if includeHidden || !memoryEntryHiddenFromRecall(entry) {
			header := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
			store.mu.Unlock()
			return header, true
		}
	}
	store.mu.Unlock()
	return ArtifactAuthorizationHeader{}, false
}

var artifactBodyReadProbe func(string)
var artifactAuthorizationAfterCheckProbe func()

func artifactAuthorizationHeaderEqual(left, right ArtifactAuthorizationHeader) bool {
	if left.TenantID != right.TenantID || left.ObjectID != right.ObjectID || left.ACLVersion != right.ACLVersion ||
		left.ContentRevision != right.ContentRevision || left.ContentDigest != right.ContentDigest || left.Visibility != right.Visibility ||
		left.OwnerEmail != right.OwnerEmail || left.OriginSurface != right.OriginSurface || left.RoomID != right.RoomID || left.SittingID != right.SittingID ||
		left.MediaGeneration != right.MediaGeneration || len(left.AssetRefs) != len(right.AssetRefs) || len(left.Revisions) != len(right.Revisions) {
		return false
	}
	for ref := range left.AssetRefs {
		if _, ok := right.AssetRefs[ref]; !ok {
			return false
		}
	}
	for ref, revision := range left.Revisions {
		if right.Revisions[ref] != revision {
			return false
		}
	}
	return true
}

func artifactScopeMetadataKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "roomId", "sittingId", "meetingId", "mediaGeneration":
		return true
	default:
		return false
	}
}

func normalizedArtifactScopeMetadataValue(key, value string) string {
	value = strings.TrimSpace(value)
	switch strings.TrimSpace(key) {
	case "roomId":
		return normalizeRoomID(value)
	case "mediaGeneration":
		generation, err := strconv.ParseUint(value, 10, 64)
		if err == nil {
			return strconv.FormatUint(generation, 10)
		}
	}
	return value
}

func validateArtifactScopeMetadataUpdates(existing, updates map[string]string) error {
	for rawKey, updatedValue := range updates {
		key := strings.TrimSpace(rawKey)
		if !artifactScopeMetadataKey(key) {
			continue
		}
		if normalizedArtifactScopeMetadataValue(key, existing[key]) != normalizedArtifactScopeMetadataValue(key, updatedValue) {
			return fmt.Errorf("artifact authorization scope %s is immutable", key)
		}
	}
	return nil
}

func (store *meetingMemoryStore) artifactSnapshotIfHeaderMatches(id string, authorized ArtifactAuthorizationHeader) (meetingMemoryEntry, bool) {
	return store.artifactSnapshotIfHeaderMatchesInternal(id, authorized, false)
}

func (store *meetingMemoryStore) artifactSnapshotIfHeaderMatchesForEvent(id string, authorized ArtifactAuthorizationHeader) (meetingMemoryEntry, bool) {
	return store.artifactSnapshotIfHeaderMatchesInternal(id, authorized, true)
}

func (store *meetingMemoryStore) artifactSnapshotIfHeaderMatchesInternal(id string, authorized ArtifactAuthorizationHeader, includeHidden bool) (meetingMemoryEntry, bool) {
	if store == nil {
		return meetingMemoryEntry{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	index, found := store.artifactEntryIndexByIDLocked(id)
	if !found {
		return meetingMemoryEntry{}, false
	}
	entry := store.entries[index]
	if !includeHidden && memoryEntryHiddenFromRecall(entry) {
		return meetingMemoryEntry{}, false
	}
	current := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
	if !artifactAuthorizationHeaderEqual(authorized, current) {
		return meetingMemoryEntry{}, false
	}
	return cloneMemoryEntry(entry), true
}

// artifactListSnapshotIfHeaderMatches is the bounded-body sibling of
// artifactSnapshotIfHeaderMatches. It performs the same exact header fence but
// projects the list excerpt while holding the store lock, so a selected 10 MB
// deck never escapes into list serialization as a full-body entry. Exact-id,
// render, editor, share, and export routes continue using the full-body seam.
func (store *meetingMemoryStore) artifactListSnapshotIfHeaderMatches(id string, authorized ArtifactAuthorizationHeader) (meetingMemoryEntry, bool) {
	if store == nil {
		return meetingMemoryEntry{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	index, found := store.artifactEntryIndexByIDLocked(id)
	if !found {
		return meetingMemoryEntry{}, false
	}
	entry := store.entries[index]
	if memoryEntryHiddenFromRecall(entry) {
		return meetingMemoryEntry{}, false
	}
	current := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
	if !artifactAuthorizationHeaderEqual(authorized, current) {
		return meetingMemoryEntry{}, false
	}
	return artifactListEntryView(entry), true
}

// artifactEventSnapshot requires the caller's complete authorization header to
// match the currently stored row while the store lock is held. The returned
// body is used only to construct the title-only event after that comparison.
func (store *meetingMemoryStore) artifactEventSnapshot(candidate meetingMemoryEntry) (meetingMemoryEntry, ArtifactAuthorizationHeader, bool) {
	if store == nil || strings.TrimSpace(candidate.ID) == "" || candidate.Kind != meetingMemoryKindOSArtifact {
		return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || entry.ID != strings.TrimSpace(candidate.ID) {
			continue
		}
		current := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
		provided := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: candidate.ID, Kind: candidate.Kind, Metadata: candidate.Metadata}))
		if !artifactAuthorizationHeaderEqual(provided, current) {
			return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
		}
		return cloneMemoryEntry(entry), current, true
	}
	return meetingMemoryEntry{}, ArtifactAuthorizationHeader{}, false
}

func authorizedArtifactByID(ctx context.Context, user *userAccount, action ACLAction, id string) (meetingMemoryEntry, bool) {
	return authorizedArtifactForActions(ctx, user, id, action)
}

// authorizedArtifactForActions authorizes every required action against one
// body-free header, then returns only the exact body snapshot whose header
// still matches under the store lock.
func authorizedArtifactForActions(ctx context.Context, user *userAccount, id string, actions ...ACLAction) (meetingMemoryEntry, bool) {
	return kanbanApp.authorizedArtifactForActions(ctx, user, id, actions...)
}

func (app *kanbanBoardApp) authorizedArtifactForActions(ctx context.Context, user *userAccount, id string, actions ...ACLAction) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil {
		return meetingMemoryEntry{}, false
	}
	header, found := app.memory.artifactAuthorizationHeaderByID(id)
	if !found || len(actions) == 0 {
		return meetingMemoryEntry{}, false
	}
	for _, action := range actions {
		if !artifactHeaderAuthorized(ctx, user, action, header) {
			return meetingMemoryEntry{}, false
		}
	}
	if artifactAuthorizationAfterCheckProbe != nil {
		artifactAuthorizationAfterCheckProbe()
	}
	artifact, found := app.memory.artifactSnapshotIfHeaderMatches(id, header)
	if !found {
		return meetingMemoryEntry{}, false
	}
	if !app.projectBoundArtifactCurrent(ctx, artifact) {
		return meetingMemoryEntry{}, false
	}
	if artifactBodyReadProbe != nil {
		artifactBodyReadProbe(header.ObjectID)
	}
	return artifact, true
}

// artifactOwnersForBlob resolves the hash through every artifact asset and
// archived body revision. It performs no blob read and returns copies only.
func artifactOwnersForBlob(ref string) []ArtifactAuthorizationHeader {
	if kanbanApp == nil || !validBlobRef(ref) {
		return nil
	}
	var owners []ArtifactAuthorizationHeader
	kanbanApp.memory.mu.Lock()
	for _, entry := range kanbanApp.memory.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		header := artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata})
		if revision, ok := header.Revisions[ref]; ok {
			header.ContentRevision = revision.ContentRevision
			header.ContentDigest = revision.ContentDigest
			owners = append(owners, header)
			continue
		}
		if _, ok := header.AssetRefs[ref]; ok {
			owners = append(owners, header)
		}
	}
	for index := range owners {
		owners[index] = kanbanApp.memory.resolveArtifactHeaderSecurityLocked(owners[index])
	}
	kanbanApp.memory.mu.Unlock()
	return owners
}

func artifactAssetRequiresFinalExportAdmission(asset artifactAsset) bool {
	mime := strings.ToLower(strings.TrimSpace(asset.Mime))
	return strings.EqualFold(strings.TrimSpace(asset.Kind), "pdf") || mime == "application/pdf" ||
		mime == "application/vnd.openxmlformats-officedocument.presentationml.presentation" ||
		mime == "application/vnd.ms-powerpoint"
}

func artifactAssetRefRequiresFinalExportAdmission(asset artifactAsset) bool {
	if artifactAssetRequiresFinalExportAdmission(asset) {
		return true
	}
	meta, err := blobStatForRef(asset.Ref)
	if err != nil {
		// Missing or corrupt immutable sidecar metadata must never downgrade a
		// declared asset into the read-only lane. The blob serve will still 404.
		return true
	}
	mime := strings.ToLower(strings.TrimSpace(meta.Mime))
	return mime == "application/pdf" ||
		mime == "application/vnd.openxmlformats-officedocument.presentationml.presentation" ||
		mime == "application/vnd.ms-powerpoint"
}

func artifactOwnsFinalExportAssetRef(entry meetingMemoryEntry, ref string) bool {
	ref = strings.TrimSpace(ref)
	if !validBlobRef(ref) {
		return false
	}
	for _, asset := range artifactAssets(entry) {
		if asset.Ref != ref {
			continue
		}
		if artifactAssetRefRequiresFinalExportAdmission(asset) {
			return true
		}
	}
	return false
}

func blobAuthorized(ctx context.Context, user *userAccount, ref string) bool {
	if user == nil || !validBlobRef(ref) || kanbanApp == nil {
		return false
	}
	for _, header := range artifactOwnersForBlob(ref) {
		if _, isAsset := header.AssetRefs[ref]; isAsset {
			// Asset authority is always current-revision authority. Re-resolve the
			// live header rather than using a header collected just before an
			// asset removal or ACL change. Historical body revisions take the
			// separate path below and intentionally retain their revision header.
			currentHeader, found := kanbanApp.memory.artifactAuthorizationHeaderByID(header.ObjectID)
			if !found {
				continue
			}
			if _, stillOwned := currentHeader.AssetRefs[ref]; !stillOwned || !artifactHeaderAuthorized(ctx, user, ACLReadContent, currentHeader) {
				continue
			}
			artifact, found := kanbanApp.memory.artifactSnapshotIfHeaderMatches(header.ObjectID, currentHeader)
			if !found || !kanbanApp.projectBoundArtifactCurrent(ctx, artifact) {
				continue
			}
			if artifactOwnsFinalExportAssetRef(artifact, ref) {
				if !artifactHeaderAuthorized(ctx, user, ACLExport, currentHeader) {
					continue
				}
				_, canExport, stable := kanbanApp.authoredResultFinalExportState(artifact)
				if !stable || !canExport {
					continue
				}
			}
			return true
		}
		// The owner header identifies the exact historical revision that created
		// this immutable blob. Requiring it to match the artifact's current
		// revision would make every valid history entry unreadable after an edit.
		// Authorize that historical header, then apply the Project-source
		// freshness fence to the current artifact record that still owns it.
		artifact, found := kanbanApp.osArtifactByID(header.ObjectID)
		if found && artifactHeaderAuthorized(ctx, user, ACLReadContent, header) && kanbanApp.projectBoundArtifactCurrent(ctx, artifact) {
			return true
		}
	}
	// Meeting recordings (Wave 7 D2) are authorized exactly like the Meeting
	// Record that owns the ref: the viewer must currently hold an authorized
	// source for that meeting. A learned ref is never authority.
	if kanbanApp.meetingRecordingBlobAuthorized(ctx, user, ref) {
		return true
	}
	// Files uploads carry a per-file ACL (files.go: private/company/people)
	// that the blob route honors on every request. A chat-promoted Files row is
	// only another durable handle to its exact source, so its committed source
	// is reauthorized too instead of laundering the ref into independent Files
	// authority. fileEntryReadableByViewer composes both. The store-level
	// metadata lookup clones only the one or two rows behind this ref (bytes
	// are content-addressed, so a re-upload shares the ref) instead of every
	// Drive row on every blob GET/HEAD.
	if kanbanApp.memory != nil {
		for _, file := range kanbanApp.memory.entriesOfKindByMetadata(meetingMemoryKindFile, "blobRef", ref) {
			if kanbanApp.fileEntryReadableByViewer(ctx, user, file) {
				return true
			}
		}
	}
	// Chat attachment authority follows the exact thread snapshot the Files UI
	// uses: owners can see their private Scout threads, while every signed-in
	// member can see public office channels. This also covers generated image
	// messages that have not (or no longer) resolve through an artifact asset.
	for _, thread := range kanbanApp.scoutChatThreadsSnapshot(user.Email, true, 0) {
		for _, message := range thread.Messages {
			for _, file := range message.Files {
				// A visible message snapshot is deliberately not sufficient
				// authority for a chat attachment. Re-resolve its committed
				// source handle on every download check so a revoked legacy or
				// unhealthy source cannot serve bytes from a learned ref.
				if strings.TrimSpace(file.Ref) == ref && kanbanApp.committedChatAttachmentAuthorized(user.Email, thread.ID, message.ID, file) {
					return true
				}
			}
			if message.Image != nil && strings.TrimSpace(message.Image.Ref) == ref {
				return true
			}
		}
	}
	return false
}

// artifactListAuthorizationCandidate is the body-free directory row used by
// /artifacts list reads. Entry carries the exact metadata/ordering projection
// the list needs; Header is the immutable authorization fence that must still
// match before the selected body's bytes are copied out of the store.
type artifactListAuthorizationCandidate struct {
	Entry  meetingMemoryEntry
	Header ArtifactAuthorizationHeader
}

// artifactListAuthorizationSnapshot returns the visible artifact directory in
// durable order without reading or retaining any artifact Text. The handler
// can therefore authorize and window years of artifacts before hydrating only
// the bounded rows it will actually send.
func (store *meetingMemoryStore) artifactListAuthorizationSnapshot() []artifactListAuthorizationCandidate {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.indexedEntryCount != len(store.entries) {
		store.rebuildMeetingEntryIndexesLocked()
	}
	candidates := make([]artifactListAuthorizationCandidate, 0, len(store.artifactIndexes))
	for _, index := range store.artifactIndexes {
		if index < 0 || index >= len(store.entries) {
			continue
		}
		if store.artifactEntryVisitHook != nil {
			store.artifactEntryVisitHook()
		}
		entry := store.entries[index]
		if entry.Kind != meetingMemoryKindOSArtifact || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		metadata := make(map[string]string, len(entry.Metadata))
		for key, value := range entry.Metadata {
			metadata[key] = value
		}
		projected := meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, CreatedAt: entry.CreatedAt, Metadata: metadata}
		header := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(projected))
		candidates = append(candidates, artifactListAuthorizationCandidate{Entry: projected, Header: header})
	}
	return candidates
}

// authorizedArtifactListCandidates applies principal and source-currentness
// checks to body-free rows. Full bodies remain behind the exact header-match
// fence until the handler has applied its 100/10 (or pagination) bounds.
func authorizedArtifactListCandidates(ctx context.Context, user *userAccount, action ACLAction) []artifactListAuthorizationCandidate {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return nil
	}
	candidates := kanbanApp.memory.artifactListAuthorizationSnapshot()
	authorized := make([]artifactListAuthorizationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !artifactHeaderAuthorized(ctx, user, action, candidate.Header) || !kanbanApp.projectBoundArtifactCurrent(ctx, candidate.Entry) {
			continue
		}
		authorized = append(authorized, candidate)
	}
	return authorized
}

// artifactListAuthorizationCandidateCurrent revalidates a body-free row that
// influences response control flow without itself being hydrated (notably a
// pagination cursor). This preserves the same opaque-denial/TOCTOU behavior as
// an exact object read without copying the cursor's body.
func artifactListAuthorizationCandidateCurrent(ctx context.Context, candidate artifactListAuthorizationCandidate) bool {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return false
	}
	if artifactAuthorizationAfterCheckProbe != nil {
		artifactAuthorizationAfterCheckProbe()
	}
	current, found := kanbanApp.memory.artifactAuthorizationHeaderByID(candidate.Header.ObjectID)
	return found && artifactAuthorizationHeaderEqual(candidate.Header, current) && kanbanApp.projectBoundArtifactCurrent(ctx, candidate.Entry)
}

func artifactListAuthorizationCandidatesCurrent(ctx context.Context, groups ...[]artifactListAuthorizationCandidate) bool {
	seen := map[string]bool{}
	for _, group := range groups {
		for _, candidate := range group {
			id := strings.TrimSpace(candidate.Header.ObjectID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if !artifactListAuthorizationCandidateCurrent(ctx, candidate) {
				return false
			}
		}
	}
	return true
}

// hydrateAuthorizedArtifactListCandidates copies bounded body excerpts only for
// rows selected by the handler. A shared cache ensures an artifact appearing in
// both the recent and published windows is projected once. The second
// project-currentness check preserves the prior post-body-fetch source fence;
// a concurrent metadata/body mutation fails the exact header match and drops
// the row rather than serving bytes authorized against stale authority.
func hydrateAuthorizedArtifactListCandidates(ctx context.Context, candidates []artifactListAuthorizationCandidate, cache map[string]meetingMemoryEntry) []meetingMemoryEntry {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return nil
	}
	artifacts := make([]meetingMemoryEntry, 0, len(candidates))
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.Header.ObjectID)
		if artifact, found := cache[id]; found {
			artifacts = append(artifacts, artifact)
			continue
		}
		if artifactAuthorizationAfterCheckProbe != nil {
			artifactAuthorizationAfterCheckProbe()
		}
		artifact, found := kanbanApp.memory.artifactListSnapshotIfHeaderMatches(id, candidate.Header)
		if !found || !kanbanApp.projectBoundArtifactCurrent(ctx, artifact) {
			continue
		}
		if artifactBodyReadProbe != nil {
			artifactBodyReadProbe(id)
		}
		artifact = decorateArchiveDownloadURLForClient(artifact)
		cache[id] = artifact
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}
