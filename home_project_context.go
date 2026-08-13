package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	homeProjectContextVersion = 1
	homeProjectContextV2      = 2
	homeProjectContextV3      = 3
	homeProjectContextTTL     = 15 * time.Minute
	homeProjectContextModeEnv = "STRIDE_E10_PROJECT_HOME_MODE"
)

func homeProjectTokenHasSourceManifest(version int) bool {
	return version == homeProjectContextV2 || version == homeProjectContextV3
}

func projectChatManifestVersionForToken(version int) int {
	if version == homeProjectContextV3 {
		return projectChatSourceManifestV3
	}
	return projectChatSourceManifestVersion
}

func homeProjectFeatureEnabled(feature STRIDEFeature) bool {
	if strideE10LiveProductRuntime.Enabled(feature) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv(homeProjectContextModeEnv)), "enabled") && currentHomeProjectStore() != nil &&
		strideE10LiveProductRuntime.Enabled(STRIDEFeatureOrganizationAuthorityRead) &&
		(feature != STRIDEFeatureProjectAuthorityWrite || strideE10LiveProductRuntime.Enabled(STRIDEFeatureOrganizationAuthorityWrite))
}

var (
	errHomeProjectUnavailable = errors.New("Project context is unavailable")
	errHomeProjectStale       = errors.New("Project context changed; choose the Project again")
)

type homeProjectDestination struct {
	Route    string `json:"route"`
	ThreadID string `json:"threadId,omitempty"`
}

func (d homeProjectDestination) normalized() (homeProjectDestination, error) {
	d.Route = strings.TrimSpace(d.Route)
	d.ThreadID = strings.TrimSpace(d.ThreadID)
	if d.Route == "new-private" && d.ThreadID == "" {
		return d, nil
	}
	if d.Route == "thread" && strideIdentifier(d.ThreadID) {
		return d, nil
	}
	return homeProjectDestination{}, errHomeProjectStale
}

type homeProjectContextToken struct {
	Version              int                    `json:"version"`
	Kind                 string                 `json:"kind"`
	TextDigest           string                 `json:"textDigest"`
	Destination          homeProjectDestination `json:"destination"`
	PersonID             string                 `json:"personId"`
	OrganizationID       string                 `json:"organizationId"`
	MembershipID         string                 `json:"membershipId"`
	MembershipRevision   int64                  `json:"membershipRevision"`
	SessionSubjectDigest string                 `json:"sessionSubjectDigest"`
	SessionRevision      int64                  `json:"sessionRevision"`
	AuthorityGeneration  uint64                 `json:"authorityGeneration"`
	ProjectID            string                 `json:"projectId,omitempty"`
	ProjectRevision      int64                  `json:"projectRevision,omitempty"`
	ProjectDigest        string                 `json:"projectDigest,omitempty"`
	ProjectTitle         string                 `json:"projectTitle"`
	ChoiceKey            string                 `json:"choiceKey,omitempty"`
	SourceManifestDigest string                 `json:"sourceManifestDigest,omitempty"`
	Basis                string                 `json:"basis"`
	ClassifierRevision   string                 `json:"classifierRevision"`
	Confidence           float64                `json:"confidence"`
	IssuedAt             time.Time              `json:"issuedAt"`
	ExpiresAt            time.Time              `json:"expiresAt"`
	KeyID                string                 `json:"keyId"`
	KeyVersion           uint64                 `json:"keyVersion"`
}

type homeProjectChoice struct {
	Title     string `json:"title"`
	Token     string `json:"token"`
	ChoiceKey string `json:"choiceKey,omitempty"`
	Suggested bool   `json:"suggested,omitempty"`
}

type homeProjectPreviewResponse struct {
	Available bool                `json:"available"`
	ScopeKey  string              `json:"scopeKey,omitempty"`
	Status    string              `json:"status"`
	Suggested *homeProjectChoice  `json:"suggested,omitempty"`
	Choices   []homeProjectChoice `json:"choices,omitempty"`
}

type homeProjectRow struct {
	ID       string
	Revision int64
	Digest   string
	Title    string
	Aliases  []string
}

func homeProjectScopeKey(snapshot StrideE10TenantAuthoritySnapshot) string {
	return sha256Hex([]byte(strings.Join([]string{
		snapshot.Person.Header.ID, snapshot.Organization.Header.ID, snapshot.Membership.Header.ID,
		fmt.Sprint(snapshot.Membership.Header.Revision), fmt.Sprint(snapshot.ActiveSession.SessionRevision), fmt.Sprint(snapshot.Generation),
	}, "\x00")))
}

func withCurrentHomeProjectAuthority(r *http.Request, use func(StrideE10TenantAuthoritySnapshot) error) error {
	if r == nil || use == nil || !homeProjectFeatureEnabled(STRIDEFeatureProjectAuthorityRead) {
		return errHomeProjectUnavailable
	}
	converter := currentStrideE10TenantRuntimeConverter()
	if converter == nil {
		return errHomeProjectUnavailable
	}
	resolver, ok := converter.resolver.(*strideE10MainTenantAuthorityResolver)
	if !ok || resolver == nil {
		return errHomeProjectUnavailable
	}
	hash := strideE10SessionHashFromRequest(r)
	return resolver.WithCurrentTenantAuthority(r.Context(), StrideE10TenantSurfaceHTTP, hash, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		if snapshot.Person.Validate() != nil || snapshot.Organization.Validate() != nil || snapshot.Membership.Validate() != nil || snapshot.ActiveSession.Validate() != nil ||
			snapshot.Organization.Status != "active" || snapshot.Membership.Status != "active" || snapshot.ActiveSession.Status != "active" ||
			snapshot.Generation < 1 || snapshot.Generation != uint64(snapshot.ActiveSession.SessionRevision) {
			return errHomeProjectUnavailable
		}
		return use(snapshot)
	})
}

func currentHomeProjectStore() *PostgresCanonicalStore {
	runtime := currentCanonicalRuntime()
	if runtime == nil || runtime.postgres == nil || runtime.postgres.pool == nil {
		return nil
	}
	return runtime.postgres
}

func visibleHomeProjects(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot) ([]homeProjectRow, error) {
	store := currentHomeProjectStore()
	if store == nil {
		return nil, errHomeProjectUnavailable
	}
	rows, err := store.pool.Query(ctx, `SELECT revision.project_id,revision.revision,encode(revision.content_digest,'hex'),revision.title,revision.aliases
FROM stride_projects_current current_project
JOIN stride_project_revisions revision ON revision.project_id=current_project.project_id AND revision.revision=current_project.revision
WHERE current_project.organization_id=$1 AND current_project.lifecycle<>'archived'
  AND (revision.audience->'principals' @> jsonb_build_array($2::text)
       OR revision.controller_memberships @> jsonb_build_array(jsonb_build_object('contractType','organization_membership','id',$3::text,'revision',$4::bigint,'digest',$5::text)))
ORDER BY lower(revision.title),revision.project_id`, snapshot.Organization.Header.ID, snapshot.Person.Header.ID, snapshot.Membership.Header.ID, snapshot.Membership.Header.Revision, snapshot.Membership.Header.ContentDigest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []homeProjectRow
	for rows.Next() {
		var row homeProjectRow
		var aliases []byte
		if err := rows.Scan(&row.ID, &row.Revision, &row.Digest, &row.Title, &aliases); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(aliases, &row.Aliases)
		result = append(result, row)
	}
	return result, rows.Err()
}

func boundHomeProjectForThread(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, threadID string, visible []homeProjectRow) (*homeProjectRow, error) {
	store := currentHomeProjectStore()
	if store == nil || !strideIdentifier(threadID) {
		return nil, errHomeProjectUnavailable
	}
	var projectID string
	err := store.pool.QueryRow(ctx, `SELECT project_id FROM stride_project_thread_bindings_current WHERE organization_id=$1 AND thread_id=$2 AND state='active'`, snapshot.Organization.Header.ID, threadID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for index := range visible {
		if visible[index].ID == projectID {
			project := visible[index]
			return &project, nil
		}
	}
	return nil, errHomeProjectStale
}

func homeProjectTokenMAC(key StrideE10TenantAuthorityEnvelopeKey, raw []byte) []byte {
	return homeProjectTokenMACVersion(key, raw, homeProjectContextVersion)
}

func homeProjectTokenMACVersion(key StrideE10TenantAuthorityEnvelopeKey, raw []byte, version int) []byte {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte(fmt.Sprintf("home-project-context/v%d\x00", version)))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
}

func mintHomeProjectTokenV2(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, text string, destination homeProjectDestination, manifest projectChatSourceManifest, project homeProjectRow, kind, basis string, confidence float64) (string, string, error) {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if runtime == nil || runtime.keys == nil || (manifest.Version != projectChatSourceManifestVersion && manifest.Version != projectChatSourceManifestV3) || !isHexDigest(manifest.Digest) {
		return "", "", errHomeProjectUnavailable
	}
	key, err := runtime.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(ctx)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) {
		return "", "", errHomeProjectUnavailable
	}
	now := time.Now().UTC()
	choiceKey := stableHomeProjectChoiceKey(key, snapshot, kind, project)
	tokenVersion := homeProjectContextV2
	if manifest.Version == projectChatSourceManifestV3 {
		tokenVersion = homeProjectContextV3
	}
	token := homeProjectContextToken{
		Version: tokenVersion, Kind: kind, TextDigest: sha256Hex([]byte(strings.TrimSpace(text))), Destination: destination,
		PersonID: snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID, MembershipID: snapshot.Membership.Header.ID,
		MembershipRevision: snapshot.Membership.Header.Revision, SessionSubjectDigest: snapshot.SessionHash,
		SessionRevision: snapshot.ActiveSession.SessionRevision, AuthorityGeneration: snapshot.Generation,
		ProjectID: project.ID, ProjectRevision: project.Revision, ProjectDigest: project.Digest, ProjectTitle: project.Title,
		ChoiceKey: choiceKey, SourceManifestDigest: manifest.Digest,
		Basis: basis, ClassifierRevision: "project_linker_v2", Confidence: confidence,
		IssuedAt: now, ExpiresAt: now.Add(homeProjectContextTTL), KeyID: key.ID, KeyVersion: key.Version,
	}
	raw, _ := json.Marshal(token)
	encoded := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(homeProjectTokenMACVersion(key, raw, token.Version))
	return encoded, choiceKey, nil
}

func mintHomeProjectToken(ctx context.Context, snapshot StrideE10TenantAuthoritySnapshot, text string, destination homeProjectDestination, project homeProjectRow, kind, basis string, confidence float64) (string, error) {
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if runtime == nil || runtime.keys == nil {
		return "", errHomeProjectUnavailable
	}
	key, err := runtime.keys.CurrentStrideE10TenantAuthorityEnvelopeKey(ctx)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) {
		return "", errHomeProjectUnavailable
	}
	now := time.Now().UTC()
	token := homeProjectContextToken{
		Version: homeProjectContextVersion, Kind: kind, TextDigest: sha256Hex([]byte(strings.TrimSpace(text))), Destination: destination,
		PersonID: snapshot.Person.Header.ID, OrganizationID: snapshot.Organization.Header.ID, MembershipID: snapshot.Membership.Header.ID,
		MembershipRevision: snapshot.Membership.Header.Revision, SessionSubjectDigest: snapshot.SessionHash,
		SessionRevision: snapshot.ActiveSession.SessionRevision, AuthorityGeneration: snapshot.Generation,
		ProjectID: project.ID, ProjectRevision: project.Revision, ProjectDigest: project.Digest, ProjectTitle: project.Title,
		Basis: basis, ClassifierRevision: "project_linker_v1", Confidence: confidence,
		IssuedAt: now, ExpiresAt: now.Add(homeProjectContextTTL), KeyID: key.ID, KeyVersion: key.Version,
	}
	raw, _ := json.Marshal(token)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(homeProjectTokenMAC(key, raw)), nil
}

func resolveHomeProjectToken(ctx context.Context, encoded, text string, destination homeProjectDestination, snapshot StrideE10TenantAuthoritySnapshot) (homeProjectContextToken, error) {
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion, Destination: destination, TextDigest: sha256Hex([]byte(strings.TrimSpace(text)))}
	manifest.Digest = projectChatManifestDigest(manifest)
	return resolveHomeProjectTokenForRetryWithManifest(ctx, encoded, text, destination, manifest, snapshot, false)
}

// resolveHomeProjectTokenForRetry may ignore token expiry only after the
// legacy chat store has proved that this exact token digest is already the
// pending half of an accepted Home send. Signature, requester, organization,
// session generation, destination, draft, and current Project authority are
// still revalidated. This lets an authenticated response-loss retry finish the
// durable cross-store association without turning an expired token into a new
// capability.
func resolveHomeProjectTokenForRetry(ctx context.Context, encoded, text string, destination homeProjectDestination, snapshot StrideE10TenantAuthoritySnapshot, acceptedPending bool) (homeProjectContextToken, error) {
	manifest := projectChatSourceManifest{Version: projectChatSourceManifestVersion, Destination: destination, TextDigest: sha256Hex([]byte(strings.TrimSpace(text)))}
	manifest.Digest = projectChatManifestDigest(manifest)
	return resolveHomeProjectTokenForRetryWithManifest(ctx, encoded, text, destination, manifest, snapshot, acceptedPending)
}

func resolveHomeProjectTokenForRetryWithManifest(ctx context.Context, encoded, text string, destination homeProjectDestination, manifest projectChatSourceManifest, snapshot StrideE10TenantAuthoritySnapshot, acceptedPending bool) (homeProjectContextToken, error) {
	return resolveHomeProjectTokenForRetryWithManifestState(ctx, encoded, text, destination, manifest, snapshot, acceptedPending, false)
}

func decodeSignedHomeProjectToken(ctx context.Context, encoded string) (homeProjectContextToken, StrideE10TenantAuthorityEnvelopeKey, error) {
	var token homeProjectContextToken
	var resolvedKey StrideE10TenantAuthorityEnvelopeKey
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		return token, resolvedKey, errHomeProjectStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &token) != nil {
		return token, resolvedKey, errHomeProjectStale
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if err != nil || runtime == nil || runtime.keys == nil {
		return token, resolvedKey, errHomeProjectStale
	}
	key, err := runtime.keys.ResolveStrideE10TenantAuthorityEnvelopeKey(ctx, token.KeyID, token.KeyVersion)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) || (token.Version != homeProjectContextVersion && token.Version != homeProjectContextV2 && token.Version != homeProjectContextV3) || !hmac.Equal(signature, homeProjectTokenMACVersion(key, raw, token.Version)) {
		return token, resolvedKey, errHomeProjectStale
	}
	return token, key, nil
}

func resolveHomeProjectTokenForRetryWithManifestState(ctx context.Context, encoded, text string, destination homeProjectDestination,
	manifest projectChatSourceManifest, snapshot StrideE10TenantAuthoritySnapshot, acceptedPending, acceptedDurable bool) (homeProjectContextToken, error) {
	token, key, err := decodeSignedHomeProjectToken(ctx, encoded)
	if err != nil {
		return token, err
	}
	wantDestination, err := destination.normalized()
	if err != nil || !oneOf(token.Kind, "project", "create") ||
		token.TextDigest != sha256Hex([]byte(strings.TrimSpace(text))) || token.Destination != wantDestination ||
		token.PersonID != snapshot.Person.Header.ID || token.OrganizationID != snapshot.Organization.Header.ID ||
		token.MembershipID != snapshot.Membership.Header.ID ||
		(!acceptedDurable && (token.MembershipRevision != snapshot.Membership.Header.Revision ||
			token.SessionSubjectDigest != snapshot.SessionHash || token.SessionRevision != snapshot.ActiveSession.SessionRevision ||
			token.AuthorityGeneration != snapshot.Generation)) || token.IssuedAt.IsZero() || token.ExpiresAt.IsZero() || !token.ExpiresAt.After(token.IssuedAt) ||
		(!acceptedPending && !acceptedDurable && !time.Now().UTC().Before(token.ExpiresAt)) ||
		!stridePlainText(token.ProjectTitle, 120, true) {
		return token, errHomeProjectStale
	}
	if token.Version == homeProjectContextV2 || token.Version == homeProjectContextV3 {
		project := homeProjectRow{ID: token.ProjectID, Revision: token.ProjectRevision, Digest: token.ProjectDigest, Title: token.ProjectTitle}
		wantManifestVersion := projectChatSourceManifestVersion
		if token.Version == homeProjectContextV3 {
			wantManifestVersion = projectChatSourceManifestV3
		}
		if manifest.Version != wantManifestVersion || !isHexDigest(manifest.Digest) || token.SourceManifestDigest != manifest.Digest ||
			(!acceptedDurable && token.ChoiceKey != stableHomeProjectChoiceKey(key, snapshot, token.Kind, project)) {
			return token, errHomeProjectStale
		}
	} else if len(manifest.Attachments) != 0 || manifest.Reply != nil || token.SourceManifestDigest != "" || token.ChoiceKey != "" {
		return token, errHomeProjectStale
	}
	if token.Kind == "project" && (!strideIdentifier(token.ProjectID) || token.ProjectRevision < 1 || !isHexDigest(token.ProjectDigest)) {
		return token, errHomeProjectStale
	}
	if token.Kind == "create" && (token.ProjectID != "" || token.ProjectRevision != 0 || token.ProjectDigest != "") {
		return token, errHomeProjectStale
	}
	if acceptedDurable {
		// The signed historical envelope remains the immutable request identity,
		// while all subsequent authority checks must use the authenticated current
		// session. This path is reachable only after the exact confirmed legacy
		// journal and canonical send receipt have both been proved by the caller.
		token.MembershipRevision = snapshot.Membership.Header.Revision
		token.SessionSubjectDigest = snapshot.SessionHash
		token.SessionRevision = snapshot.ActiveSession.SessionRevision
		token.AuthorityGeneration = snapshot.Generation
	}
	return token, nil
}

func projectMatchScore(text string, project homeProjectRow) int {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return 0
	}
	names := append([]string{project.Title}, project.Aliases...)
	best := 0
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if normalized == name {
			best = 100
		} else if strings.Contains(normalized, name) && best < 90 {
			best = 90
		}
	}
	return best
}

func assistantProjectContextHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := userFromRequest(r)
	if !websocketOriginAllowed(r) || user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	payload := struct {
		Text              string                        `json:"text"`
		Destination       homeProjectDestination        `json:"destination"`
		CreateTitle       string                        `json:"createTitle"`
		AttachmentHandles []projectChatAttachmentHandle `json:"attachmentHandles"`
		ReplyToMessageID  string                        `json:"replyToMessageId"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read Project context request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAuthError(w, http.StatusBadRequest, "Project context request must contain exactly one object")
		return
	}
	payload.Text = strings.TrimSpace(payload.Text)
	destination, err := payload.Destination.normalized()
	if err != nil || len([]rune(payload.Text)) > scoutHomeOpeningMaxRunes {
		writeAuthError(w, http.StatusBadRequest, "Project context request is invalid")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	response := homeProjectPreviewResponse{Status: "unlinked"}
	err = withCurrentHomeProjectAuthority(r, func(snapshot StrideE10TenantAuthoritySnapshot) error {
		manifest, manifestErr := kanbanApp.resolveProjectChatSourceManifest(r.Context(), user, snapshot, payload.Text, destination, payload.AttachmentHandles, payload.ReplyToMessageID)
		if manifestErr != nil {
			return manifestErr
		}
		response.Available = true
		response.ScopeKey = homeProjectScopeKey(snapshot)
		projects, listErr := visibleHomeProjects(r.Context(), snapshot)
		if listErr != nil {
			return listErr
		}
		for _, project := range projects {
			token, choiceKey, mintErr := mintHomeProjectTokenV2(r.Context(), snapshot, payload.Text, destination, manifest, project, "project", "selected", 1)
			if mintErr != nil {
				return mintErr
			}
			response.Choices = append(response.Choices, homeProjectChoice{Title: project.Title, Token: token, ChoiceKey: choiceKey})
		}
		if destination.Route == "thread" {
			bound, boundErr := boundHomeProjectForThread(r.Context(), snapshot, destination.ThreadID, projects)
			if boundErr != nil {
				return boundErr
			}
			if bound != nil {
				token, choiceKey, mintErr := mintHomeProjectTokenV2(r.Context(), snapshot, payload.Text, destination, manifest, *bound, "project", "authoritative_context", 1)
				if mintErr != nil {
					return mintErr
				}
				// A canonical thread binding is authoritative context, not an
				// inference. Clients display it as Project rather than Suggested.
				choice := homeProjectChoice{Title: bound.Title, Token: token, ChoiceKey: choiceKey}
				response.Status, response.Suggested = "bound", &choice
				return nil
			}
		}
		createTitle := strings.TrimSpace(payload.CreateTitle)
		if createTitle != "" {
			if destination.Route == "thread" {
				return errHomeProjectStale
			}
			if !stridePlainText(createTitle, 120, true) || !homeProjectFeatureEnabled(STRIDEFeatureProjectAuthorityWrite) {
				return errHomeProjectStale
			}
			createProject := homeProjectRow{Title: createTitle}
			token, choiceKey, mintErr := mintHomeProjectTokenV2(r.Context(), snapshot, payload.Text, destination, manifest, createProject, "create", "selected", 1)
			if mintErr != nil {
				return mintErr
			}
			response.Status = "selected"
			response.Suggested = &homeProjectChoice{Title: createTitle, Token: token, ChoiceKey: choiceKey}
			return nil
		}
		type ranked struct {
			row   homeProjectRow
			score int
		}
		var matches []ranked
		for _, project := range projects {
			if score := projectMatchScore(payload.Text, project); score >= 90 {
				matches = append(matches, ranked{project, score})
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			return matches[i].row.ID < matches[j].row.ID
		})
		if len(matches) == 1 && homeProjectFeatureEnabled(STRIDEFeatureProjectSmartLink) {
			project := matches[0].row
			token, choiceKey, mintErr := mintHomeProjectTokenV2(r.Context(), snapshot, payload.Text, destination, manifest, project, "project", "suggested", float64(matches[0].score)/100)
			if mintErr != nil {
				return mintErr
			}
			choice := homeProjectChoice{Title: project.Title, Token: token, ChoiceKey: choiceKey, Suggested: true}
			response.Status, response.Suggested = "suggested", &choice
		} else if len(matches) > 1 {
			response.Status = "clarify"
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errHomeProjectUnavailable) || errors.Is(err, ErrStrideE10TenantAuthorityStale) {
			writeAuthJSON(w, http.StatusOK, response)
			return
		}
		writeAuthError(w, http.StatusConflict, errHomeProjectStale.Error())
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "projectContext": response})
}

func homeProjectTokenDigest(token string) string {
	return sha256Hex([]byte(strings.TrimSpace(token)))
}
