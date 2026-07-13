package main

import (
	"context"
	"encoding/json"
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
	AssetRefs       map[string]struct{}
	Revisions       map[string]ACLRevisionRef // body blob ref -> exact revision
}

// ObjectAuthorizer is the one principal-aware boundary for artifact objects.
// Implementations return only allow/deny; handlers deliberately collapse a
// denial and a missing object into the same 404 response.
type ObjectAuthorizer interface {
	AuthorizeArtifactHeader(context.Context, *userAccount, ACLAction, ArtifactAuthorizationHeader) bool
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
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindScoutChat || entry.ID != threadID {
			continue
		}
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
	for _, entry := range store.entries {
		if entry.Kind == meetingMemoryKindScoutChat && entry.ID == threadID {
			visibility := normalizeScoutChatVisibility(entry.Metadata["visibility"])
			owner := ""
			if visibility == scoutChatVisibilityPrivate {
				owner = normalizeAccountEmail(entry.Metadata["ownerEmail"])
			}
			return visibility, owner, true
		}
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
	if kanbanApp == nil || kanbanApp.memory == nil {
		return header
	}
	kanbanApp.memory.mu.Lock()
	header = kanbanApp.memory.resolveArtifactHeaderSecurityLocked(header)
	kanbanApp.memory.mu.Unlock()
	return header
}

var artifactObjectAuthorizer ObjectAuthorizer = LegacyCompatibleObjectAuthorizer{}

func artifactAuthorized(ctx context.Context, user *userAccount, action ACLAction, artifact meetingMemoryEntry) bool {
	return artifactHeaderAuthorized(ctx, user, action, resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
}

func artifactHeaderAuthorized(ctx context.Context, user *userAccount, action ACLAction, header ArtifactAuthorizationHeader) bool {
	return artifactObjectAuthorizer != nil && artifactObjectAuthorizer.AuthorizeArtifactHeader(ctx, user, action, header)
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
		AssetRefs:       map[string]struct{}{},
		Revisions:       map[string]ACLRevisionRef{},
	}
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
	if store == nil {
		return ArtifactAuthorizationHeader{}, false
	}
	id = strings.TrimSpace(id)
	store.mu.Lock()
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || entry.ID != id || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		header := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
		store.mu.Unlock()
		return header, true
	}
	store.mu.Unlock()
	return ArtifactAuthorizationHeader{}, false
}

var artifactBodyReadProbe func(string)
var artifactAuthorizationAfterCheckProbe func()

func artifactAuthorizationHeaderEqual(left, right ArtifactAuthorizationHeader) bool {
	if left.TenantID != right.TenantID || left.ObjectID != right.ObjectID || left.ACLVersion != right.ACLVersion ||
		left.ContentRevision != right.ContentRevision || left.ContentDigest != right.ContentDigest || left.Visibility != right.Visibility ||
		left.OwnerEmail != right.OwnerEmail || left.OriginSurface != right.OriginSurface || len(left.AssetRefs) != len(right.AssetRefs) || len(left.Revisions) != len(right.Revisions) {
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

func (store *meetingMemoryStore) artifactSnapshotIfHeaderMatches(id string, authorized ArtifactAuthorizationHeader) (meetingMemoryEntry, bool) {
	if store == nil {
		return meetingMemoryEntry{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, entry := range store.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || entry.ID != strings.TrimSpace(id) || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		current := store.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata}))
		if !artifactAuthorizationHeaderEqual(authorized, current) {
			return meetingMemoryEntry{}, false
		}
		return cloneMemoryEntry(entry), true
	}
	return meetingMemoryEntry{}, false
}

func authorizedArtifactByID(ctx context.Context, user *userAccount, action ACLAction, id string) (meetingMemoryEntry, bool) {
	return authorizedArtifactForActions(ctx, user, id, action)
}

// authorizedArtifactForActions authorizes every required action against one
// body-free header, then returns only the exact body snapshot whose header
// still matches under the store lock.
func authorizedArtifactForActions(ctx context.Context, user *userAccount, id string, actions ...ACLAction) (meetingMemoryEntry, bool) {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return meetingMemoryEntry{}, false
	}
	header, found := kanbanApp.memory.artifactAuthorizationHeaderByID(id)
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
	artifact, found := kanbanApp.memory.artifactSnapshotIfHeaderMatches(id, header)
	if !found {
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

func blobAuthorized(ctx context.Context, user *userAccount, ref string) bool {
	if user == nil || !validBlobRef(ref) || kanbanApp == nil {
		return false
	}
	for _, header := range artifactOwnersForBlob(ref) {
		if artifactHeaderAuthorized(ctx, user, ACLReadContent, header) {
			return true
		}
	}
	// Files uploads are explicitly organization-visible under the current
	// product contract. Resolve their stored blob reference without touching
	// the blob itself; possession of an otherwise-known hash is never enough.
	if kanbanApp.memory != nil {
		for _, file := range kanbanApp.memory.entriesOfKind(meetingMemoryKindFile, 0) {
			if strings.TrimSpace(file.Metadata["blobRef"]) == ref {
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
				if strings.TrimSpace(file.Ref) == ref {
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

func authorizedArtifactsSnapshot(ctx context.Context, user *userAccount, action ACLAction, publishedOnly bool) []meetingMemoryEntry {
	if kanbanApp == nil || kanbanApp.memory == nil {
		return nil
	}
	kanbanApp.memory.mu.Lock()
	headers := make([]ArtifactAuthorizationHeader, 0)
	for _, entry := range kanbanApp.memory.entries {
		if entry.Kind != meetingMemoryKindOSArtifact || memoryEntryHiddenFromRecall(entry) {
			continue
		}
		if publishedOnly && !artifactIsPublished(meetingMemoryEntry{Metadata: entry.Metadata}) {
			continue
		}
		headers = append(headers, kanbanApp.memory.resolveArtifactHeaderSecurityLocked(artifactAuthorizationHeaderFromEntry(meetingMemoryEntry{ID: entry.ID, Kind: entry.Kind, Metadata: entry.Metadata})))
	}
	kanbanApp.memory.mu.Unlock()
	artifacts := make([]meetingMemoryEntry, 0, len(headers))
	for _, header := range headers {
		if !artifactHeaderAuthorized(ctx, user, action, header) {
			continue
		}
		if artifactAuthorizationAfterCheckProbe != nil {
			artifactAuthorizationAfterCheckProbe()
		}
		if artifact, found := kanbanApp.memory.artifactSnapshotIfHeaderMatches(header.ObjectID, header); found {
			if artifactBodyReadProbe != nil {
				artifactBodyReadProbe(header.ObjectID)
			}
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts
}
