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
)

const (
	homeProjectContextVersion = 1
	homeProjectContextTTL     = 15 * time.Minute
	homeProjectContextModeEnv = "STRIDE_E10_PROJECT_HOME_MODE"
)

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

func homeProjectTokenMAC(key StrideE10TenantAuthorityEnvelopeKey, raw []byte) []byte {
	mac := hmac.New(sha256.New, key.Secret)
	_, _ = mac.Write([]byte("home-project-context/v1\x00"))
	_, _ = mac.Write(raw)
	return mac.Sum(nil)
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
	return resolveHomeProjectTokenForRetry(ctx, encoded, text, destination, snapshot, false)
}

// resolveHomeProjectTokenForRetry may ignore token expiry only after the
// legacy chat store has proved that this exact token digest is already the
// pending half of an accepted Home send. Signature, requester, organization,
// session generation, destination, draft, and current Project authority are
// still revalidated. This lets an authenticated response-loss retry finish the
// durable cross-store association without turning an expired token into a new
// capability.
func resolveHomeProjectTokenForRetry(ctx context.Context, encoded, text string, destination homeProjectDestination, snapshot StrideE10TenantAuthoritySnapshot, acceptedPending bool) (homeProjectContextToken, error) {
	var token homeProjectContextToken
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 {
		return token, errHomeProjectStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &token) != nil {
		return token, errHomeProjectStale
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	runtime := strideE10CurrentTenantEnvelopeRuntime()
	if err != nil || runtime == nil || runtime.keys == nil {
		return token, errHomeProjectStale
	}
	key, err := runtime.keys.ResolveStrideE10TenantAuthorityEnvelopeKey(ctx, token.KeyID, token.KeyVersion)
	if err != nil || !validStrideE10TenantEnvelopeKey(key) || !hmac.Equal(signature, homeProjectTokenMAC(key, raw)) {
		return token, errHomeProjectStale
	}
	wantDestination, err := destination.normalized()
	if err != nil || token.Version != homeProjectContextVersion || !oneOf(token.Kind, "project", "create") ||
		token.TextDigest != sha256Hex([]byte(strings.TrimSpace(text))) || token.Destination != wantDestination ||
		token.PersonID != snapshot.Person.Header.ID || token.OrganizationID != snapshot.Organization.Header.ID ||
		token.MembershipID != snapshot.Membership.Header.ID || token.MembershipRevision != snapshot.Membership.Header.Revision ||
		token.SessionSubjectDigest != snapshot.SessionHash || token.SessionRevision != snapshot.ActiveSession.SessionRevision ||
		token.AuthorityGeneration != snapshot.Generation || token.IssuedAt.IsZero() || token.ExpiresAt.IsZero() || !token.ExpiresAt.After(token.IssuedAt) ||
		(!acceptedPending && !time.Now().UTC().Before(token.ExpiresAt)) ||
		!stridePlainText(token.ProjectTitle, 120, true) {
		return token, errHomeProjectStale
	}
	if token.Kind == "project" && (!strideIdentifier(token.ProjectID) || token.ProjectRevision < 1 || !isHexDigest(token.ProjectDigest)) {
		return token, errHomeProjectStale
	}
	if token.Kind == "create" && (token.ProjectID != "" || token.ProjectRevision != 0 || token.ProjectDigest != "") {
		return token, errHomeProjectStale
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
	if !websocketOriginAllowed(r) || userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	payload := struct {
		Text        string                 `json:"text"`
		Destination homeProjectDestination `json:"destination"`
		CreateTitle string                 `json:"createTitle"`
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
		if destination.Route == "thread" {
			return nil
		}
		response.Available = true
		response.ScopeKey = homeProjectScopeKey(snapshot)
		projects, listErr := visibleHomeProjects(r.Context(), snapshot)
		if listErr != nil {
			return listErr
		}
		for _, project := range projects {
			token, mintErr := mintHomeProjectToken(r.Context(), snapshot, payload.Text, destination, project, "project", "selected", 1)
			if mintErr != nil {
				return mintErr
			}
			response.Choices = append(response.Choices, homeProjectChoice{Title: project.Title, Token: token})
		}
		createTitle := strings.TrimSpace(payload.CreateTitle)
		if createTitle != "" {
			if !stridePlainText(createTitle, 120, true) || !homeProjectFeatureEnabled(STRIDEFeatureProjectAuthorityWrite) {
				return errHomeProjectStale
			}
			token, mintErr := mintHomeProjectToken(r.Context(), snapshot, payload.Text, destination, homeProjectRow{Title: createTitle}, "create", "selected", 1)
			if mintErr != nil {
				return mintErr
			}
			response.Status = "selected"
			response.Suggested = &homeProjectChoice{Title: createTitle, Token: token}
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
			token, mintErr := mintHomeProjectToken(r.Context(), snapshot, payload.Text, destination, project, "project", "suggested", float64(matches[0].score)/100)
			if mintErr != nil {
				return mintErr
			}
			choice := homeProjectChoice{Title: project.Title, Token: token, Suggested: true}
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
