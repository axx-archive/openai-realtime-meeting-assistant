package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openai/openai-realtime-meeting-assistant/internal/business"
)

const businessAPIBase = "/api/business/v1/"

type businessHTTPStore interface {
	ListOrganizations(context.Context, business.Actor) ([]business.Organization, error)
	GetMembership(context.Context, business.Scope) (business.Membership, error)
	ListBusinesses(context.Context, business.Scope) ([]business.Business, error)
	GetBusiness(context.Context, business.Scope, string) (business.Business, error)
	GetBudget(context.Context, business.Scope, string) (business.Budget, error)
	SetupBusiness(context.Context, business.Actor, business.SetupBusinessArgs) (business.SetupBusinessResult, error)
	UpdateBusinessAction(context.Context, business.Scope, business.BusinessAction) (business.Business, error)
	Overview(context.Context, business.Scope, string) (business.Overview, error)
	ReadWorkDetail(context.Context, business.Scope, string, string) (business.WorkDetail, error)
}

type businessViewer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type businessHTTP struct {
	store        businessHTTPStore
	authenticate func(*http.Request) (businessViewer, error)
}

// The login bridge supplies only stable person identity. Active legacy W4
// organization/membership never authorizes the new SQL Business namespace.
// Session locks are held only while resolving identity, never over SQL or I/O.
func authenticateBusinessPerson(r *http.Request) (businessViewer, error) {
	user := userFromRequest(r)
	if user == nil {
		return businessViewer{}, business.ErrDenied
	}
	sessions := userSessionStore()
	if strideE10LiveProductRuntime == nil {
		return businessViewer{}, business.ErrInactive
	}
	organizations := strideE10LiveProductRuntime.organization
	if organizations == nil {
		return businessViewer{}, business.ErrInactive
	}
	digest := sha256Hex([]byte(normalizeAccountEmail(user.Email)))
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	record, ok := sessions.sessions[hashResetToken(sessionTokenFromRequest(r))]
	if !ok || record.Kind != "" || !time.Now().Before(record.Expires) || normalizeAccountEmail(record.Email) != normalizeAccountEmail(user.Email) {
		return businessViewer{}, business.ErrDenied
	}
	organizations.mu.RLock()
	defer organizations.mu.RUnlock()
	personID := organizations.accountPersons[digest]
	person, ok := organizations.persons[personID]
	if !ok || person.Validate() != nil || person.Status != "active" || person.AccountSubjectDigest != digest || !strideIdentifier(personID) {
		return businessViewer{}, business.ErrInactive
	}
	if record.PersonID != "" && record.PersonID != personID {
		return businessViewer{}, business.ErrDenied
	}
	return businessViewer{personID, user.Name}, nil
}

// businessPoolConfig reserves four connections for the serving Business lane.
// Keep the supplied restricted identity; business.New still validates its role.
func businessPoolConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 4
	config.MinConns = 0
	config.MinIdleConns = 0
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	return config, nil
}

func newBusinessHTTP(ctx context.Context, databaseURL string) (*businessHTTP, *pgxpool.Pool, error) {
	handler := &businessHTTP{authenticate: authenticateBusinessPerson}
	if strings.TrimSpace(databaseURL) == "" {
		return handler, nil, nil
	}
	config, err := businessPoolConfig(databaseURL)
	if err != nil {
		return nil, nil, errors.New("invalid Business database configuration")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, nil, errors.New("Business database connection unavailable")
	}
	store, err := business.New(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, nil, errors.New("Business database is unavailable or runtime role is unsafe")
	}
	handler.store = store
	return handler, pool, nil
}
func registerBusinessHTTP(mux *http.ServeMux, handler *businessHTTP) {
	mux.Handle(businessAPIBase, handler)
	mux.HandleFunc("/business", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/business" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, "business.html")
	})
}
func businessWriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func businessWriteError(w http.ResponseWriter, err error) {
	status, code, message := 503, "business_unavailable", "Business services are temporarily unavailable."
	switch {
	case errors.Is(err, business.ErrDenied), errors.Is(err, business.ErrNotFound):
		status, code, message = 404, "business_unavailable", "This business is unavailable."
	case errors.Is(err, business.ErrInvalid):
		status, code, message = 400, "invalid_request", "Check the business details and try again."
	case errors.Is(err, business.ErrConflict):
		status, code, message = 409, "revision_conflict", "This business changed. Refresh before making another change."
	case errors.Is(err, business.ErrInactive):
		status, code, message = 409, "authority_inactive", "This action is not available in the current business state."
	}
	businessWriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func businessDecode(w http.ResponseWriter, r *http.Request, into any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return business.ErrInvalid
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil {
		return business.ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return business.ErrInvalid
	}
	return nil
}
func businessMutationOriginAllowed(r *http.Request) bool {
	// Native clients supply an explicit bearer credential, never an ambient cookie.
	_, credentialSource := sessionAuthorityFromRequest(r)
	if credentialSource == sessionAuthorityBearer || credentialSource == sessionAuthorityExplicitHeader {
		return true
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == "" && (parsed.Scheme == "https" || parsed.Scheme == "http") && strings.EqualFold(parsed.Host, r.Host)
}
func (h *businessHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Vary", "Cookie, Authorization")
	if h == nil || h.authenticate == nil {
		businessWriteError(w, errors.New("unavailable"))
		return
	}
	viewer, err := h.authenticate(r)
	if err != nil {
		businessWriteJSON(w, 401, map[string]any{"error": map[string]string{"code": "sign_in_required", "message": "Sign in to open your businesses."}})
		return
	}
	if h.store == nil {
		businessWriteError(w, errors.New("unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	actor := business.Actor{Kind: "person", ID: viewer.ID}
	path := strings.TrimPrefix(r.URL.Path, businessAPIBase)
	if path == "context" && r.Method == "GET" {
		h.context(w, r, viewer, actor)
		return
	}
	if path == "businesses" && r.Method == "POST" {
		if !businessMutationOriginAllowed(r) {
			businessWriteJSON(w, 403, map[string]any{"error": map[string]string{"code": "origin_denied"}})
			return
		}
		h.create(w, r, actor)
		return
	}
	if strings.HasPrefix(path, "businesses/") {
		id := strings.TrimPrefix(path, "businesses/")
		parts := strings.Split(id, "/")
		if len(parts) == 3 && parts[1] == "work" && businessHTTPID(parts[0]) && businessHTTPID(parts[2]) {
			if r.Method != "GET" {
				w.Header().Set("Allow", "GET")
				w.WriteHeader(405)
				return
			}
			scope, _, err := h.resolve(r.Context(), actor, parts[0])
			if err != nil {
				businessWriteError(w, err)
				return
			}
			h.workDetail(w, r, scope, parts[0], parts[2])
			return
		}
		if id == "" || len(id) > 200 || strings.ContainsAny(id, "/\\\x00") {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "PATCH" {
			w.Header().Set("Allow", "GET, PATCH")
			w.WriteHeader(405)
			return
		}
		if r.Method == "PATCH" && !businessMutationOriginAllowed(r) {
			businessWriteJSON(w, 403, map[string]any{"error": map[string]string{"code": "origin_denied"}})
			return
		}
		scope, b, err := h.resolve(r.Context(), actor, id)
		if err != nil {
			businessWriteError(w, err)
			return
		}
		if r.Method == "PATCH" {
			var in business.BusinessAction
			if err = businessDecode(w, r, &in); err != nil {
				businessWriteError(w, err)
				return
			}
			in.BusinessID = id
			_, err = h.store.UpdateBusinessAction(r.Context(), scope, in)
			if err != nil {
				businessWriteError(w, err)
				return
			}
			// Replay receipts are historical; always show current business state.
			b, err = h.store.GetBusiness(r.Context(), scope, id)
			if err != nil {
				businessWriteError(w, err)
				return
			}
		}
		h.detail(w, r, scope, b)
		return
	}
	http.NotFound(w, r)
}
func (h *businessHTTP) context(w http.ResponseWriter, r *http.Request, viewer businessViewer, actor business.Actor) {
	orgs, err := h.store.ListOrganizations(r.Context(), actor)
	if err != nil {
		businessWriteError(w, err)
		return
	}
	if len(orgs) > 100 {
		businessWriteError(w, errors.New("directory pagination required"))
		return
	}
	organizations := []map[string]any{}
	businesses := []business.Business{}
	for _, org := range orgs {
		scope := business.Scope{OrganizationID: org.ID, Actor: actor}
		membership, err := h.store.GetMembership(r.Context(), scope)
		if err != nil {
			businessWriteError(w, err)
			return
		}
		list, err := h.store.ListBusinesses(r.Context(), scope)
		if err != nil {
			businessWriteError(w, err)
			return
		}
		businesses = append(businesses, list...)
		if len(businesses) > 100 {
			businessWriteError(w, errors.New("directory pagination required"))
			return
		}
		organizations = append(organizations, map[string]any{"id": org.ID, "name": org.Name, "canCreateBusiness": membership.Role == "owner"})
	}
	businessWriteJSON(w, 200, map[string]any{"viewer": viewer, "organizations": organizations, "businesses": businesses, "capabilities": map[string]bool{"createBusiness": true, "createOrganization": true}, "coverage": "complete"})
}
func (h *businessHTTP) resolve(ctx context.Context, actor business.Actor, id string) (business.Scope, business.Business, error) {
	orgs, err := h.store.ListOrganizations(ctx, actor)
	if err != nil {
		return business.Scope{}, business.Business{}, err
	}
	if len(orgs) > 100 {
		return business.Scope{}, business.Business{}, errors.New("directory pagination required")
	}
	// Only current memberships are searched. Neither URL nor body selects Actor;
	// opaque business IDs are never looked up with an administrator connection.
	for _, org := range orgs {
		scope := business.Scope{OrganizationID: org.ID, Actor: actor}
		b, err := h.store.GetBusiness(ctx, scope, id)
		if err == nil {
			return scope, b, nil
		}
		if !errors.Is(err, business.ErrNotFound) && !errors.Is(err, business.ErrDenied) {
			return scope, b, err
		}
	}
	return business.Scope{}, business.Business{}, business.ErrNotFound
}
func (h *businessHTTP) create(w http.ResponseWriter, r *http.Request, actor business.Actor) {
	var in struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Organization   struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organization"`
		Name                 string `json:"name"`
		Mission              string `json:"mission"`
		Customer             string `json:"customer"`
		FirstOutcome         string `json:"firstOutcome"`
		Leadership           string `json:"leadership"`
		AuthorityPreset      string `json:"authorityPreset"`
		ModelAllowanceMicros *int64 `json:"modelAllowanceMicros"`
	}
	if err := businessDecode(w, r, &in); err != nil {
		businessWriteError(w, err)
		return
	}
	allowance := int64(0)
	if in.ModelAllowanceMicros != nil {
		allowance = *in.ModelAllowanceMicros
	}
	out, err := h.store.SetupBusiness(r.Context(), actor, business.SetupBusinessArgs{IdempotencyKey: in.IdempotencyKey, OrganizationID: in.Organization.ID, OrganizationName: in.Organization.Name, Name: in.Name, Mission: in.Mission, Customer: in.Customer, FirstOutcome: in.FirstOutcome, Leadership: in.Leadership, AuthorityPreset: in.AuthorityPreset, ModelAllowanceMicros: allowance})
	if err != nil {
		businessWriteError(w, err)
		return
	}
	scope := business.Scope{OrganizationID: out.Organization.ID, Actor: actor}
	b, err := h.store.GetBusiness(r.Context(), scope, out.Business.ID)
	if err != nil {
		businessWriteError(w, err)
		return
	}
	h.detail(w, r, scope, b)
}
func (h *businessHTTP) detail(w http.ResponseWriter, r *http.Request, scope business.Scope, b business.Business) {
	view, err := h.store.Overview(r.Context(), scope, b.ID)
	if err != nil {
		businessWriteError(w, err)
		return
	}
	b, budget := view.Business, view.Budget
	owner := view.Membership.Role == "owner"
	allowance := min(budget.FundedMicros, budget.CapMicros)
	state := "allowance_set"
	if view.UnknownCostOperations > 0 {
		state = "cost_unresolved"
	}
	if budget.SettledMicros+budget.ReservedMicros > allowance {
		state = "overdrawn"
	}
	businessWriteJSON(w, 200, map[string]any{
		"business":     b,
		"team":         view.Team,
		"work":         view.Work,
		"coverage":     map[string]any{"teamMore": view.TeamMore, "workMore": view.WorkMore, "limit": 100},
		"budget":       map[string]any{"currency": "USD", "allowanceMicros": allowance, "reservedMicros": budget.ReservedMicros, "spentMicros": budget.SettledMicros, "unknownCostOperations": view.UnknownCostOperations, "unknownCostMore": view.UnknownCostMore, "state": state},
		"availability": map[string]string{"team": "available", "work": "available", "initiatives": "unavailable", "decisions": "unavailable", "activity": "unavailable"},
		"execution":    map[string]string{"status": "not_active", "reason": "Business saved. Agent execution is not connected yet."},
		"capabilities": map[string]bool{"updatePolicy": owner && b.Status != "closed", "pause": owner && b.Status == "active", "resume": owner && b.Status == "paused", "hireAgent": false, "createWork": false},
	})
}

func businessHTTPID(id string) bool {
	return id != "" && len(id) <= 200 && !strings.ContainsAny(id, "/\\\x00")
}

func (h *businessHTTP) workDetail(w http.ResponseWriter, r *http.Request, scope business.Scope, bid, wid string) {
	detail, err := h.store.ReadWorkDetail(r.Context(), scope, bid, wid)
	if err != nil {
		businessWriteError(w, err)
		return
	}
	// Lease claim keys and worker ownership are internal coordination, not the
	// Work evidence presented to a member. External references are plain text.
	attempts := []map[string]any{}
	for _, a := range detail.Attempts {
		attempts = append(attempts, map[string]any{"id": a.ID, "ordinal": a.Ordinal, "state": a.State, "mode": a.Mode, "outcome": a.Outcome, "costState": a.CostState, "operation": a.Operation, "outcomeEvidenceRef": a.OutcomeEvidenceRef, "resultId": a.ResultID})
	}
	businessWriteJSON(w, 200, map[string]any{"business": detail.Business, "work": detail.Work, "employment": detail.Employment, "attempts": attempts, "result": detail.Result, "visibility": "private", "coverage": "complete", "capabilities": map[string]bool{"retry": false, "publish": false}})
}
