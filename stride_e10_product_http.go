package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const strideE10MaxBodyBytes = 64 << 10

var (
	ErrStrideE10Invalid  = errors.New("invalid stride e10 request")
	ErrStrideE10NotFound = errors.New("stride e10 resource not found")
	ErrStrideE10Denied   = errors.New("stride e10 request denied")
	ErrStrideE10Conflict = errors.New("stride e10 revision conflict")
)

// StrideE10ProductPrincipal is resolved from the authenticated server session.
// Request bodies are never an authority for any of these fields.
type StrideE10ProductPrincipal struct {
	PersonID                     string
	ActiveOrganizationID         string
	OrganizationMembershipID     string
	OrganizationMembershipRev    int64
	ActiveOrganizationSessionRev int64
}

type StrideE10ProductPrincipalResolver func(*http.Request) (StrideE10ProductPrincipal, error)

type StrideE10ProductFeatureGate interface {
	Enabled(STRIDEFeature) bool
}

type StrideE10ProductCommand struct {
	Operation        string
	Method           string
	Path             string
	OrganizationID   string
	MembershipID     string
	ResourceID       string
	TargetID         string
	ExpectedRevision int64
	IdempotencyKey   string
	Body             json.RawMessage
}

type StrideE10ProductBackend interface {
	Execute(context.Context, StrideE10ProductPrincipal, StrideE10ProductCommand) (value any, replayed bool, err error)
}

type strideE10ProductHTTP struct {
	resolve  StrideE10ProductPrincipalResolver
	features StrideE10ProductFeatureGate
	backend  StrideE10ProductBackend
}

// NewStrideE10ProductHTTP returns an intentionally unregistered handler. Callers must
// explicitly mount it; constructing it cannot activate any production route.
func NewStrideE10ProductHTTP(resolve StrideE10ProductPrincipalResolver, features StrideE10ProductFeatureGate, backend StrideE10ProductBackend) http.Handler {
	return &strideE10ProductHTTP{resolve: resolve, features: features, backend: backend}
}

type strideE10Route struct {
	op             string
	features       []STRIDEFeature
	mutation       bool
	requireOrg     bool
	currentOrg     bool
	organizationID string
	membershipID   string
	resourceID     string
}

func (h *strideE10ProductHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if h.resolve == nil || h.features == nil || h.backend == nil {
		strideE10WriteError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	principal, err := h.resolve(r)
	if err != nil || strings.TrimSpace(principal.PersonID) == "" {
		strideE10WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if handled := h.serveMobile(w, r, principal); handled {
		return
	}
	route, ok := strideE10MatchRoute(r.Method, r.URL.Path)
	if !ok {
		strideE10WriteOpaqueNotFound(w)
		return
	}
	for _, feature := range route.features {
		if !h.features.Enabled(feature) {
			strideE10WriteError(w, http.StatusServiceUnavailable, "feature_unavailable")
			return
		}
	}
	if route.requireOrg && !strideE10CompleteOrganizationPrincipal(principal) {
		strideE10WriteOpaqueNotFound(w)
		return
	}
	if route.currentOrg && (route.organizationID == "" || route.organizationID != principal.ActiveOrganizationID) {
		strideE10WriteOpaqueNotFound(w)
		return
	}

	commandOrganizationID := route.organizationID
	if route.requireOrg && commandOrganizationID == "" {
		commandOrganizationID = principal.ActiveOrganizationID
	}
	command := StrideE10ProductCommand{
		Operation: route.op, Method: r.Method, Path: r.URL.Path,
		OrganizationID: commandOrganizationID, MembershipID: route.membershipID,
		ResourceID: route.resourceID, TargetID: route.resourceID, ExpectedRevision: -1,
	}
	if route.mutation {
		command.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if command.IdempotencyKey == "" {
			strideE10WriteError(w, http.StatusBadRequest, "idempotency_key_required")
			return
		}
		body, revision, err := strideE10ReadMutationBody(w, r)
		if err != nil {
			strideE10WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		command.Body, command.ExpectedRevision = body, revision
	}
	value, replayed, err := h.backend.Execute(r.Context(), principal, command)
	if strideE10WriteBackendError(w, err) {
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	if value == nil {
		value = map[string]any{"ok": true}
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}

type strideE10MobileSurfaceSpec struct {
	op         string
	features   []STRIDEFeature
	requireOrg bool
}

var strideE10MobileSurfaces = map[string]strideE10MobileSurfaceSpec{
	"profile":                 {op: "identity.self_profile", features: []STRIDEFeature{STRIDEFeaturePersonProfileAuthority}},
	"work-record":             {op: "work_record.self", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-draft":           {op: "network.profile_draft", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"organizations":           {op: "organizations.collection", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}},
	"organization-people":     {op: "mobile.organization_people", features: []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true},
	"organization-requests":   {op: "organizations.join_requests", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true},
	"contribution-approvals":  {op: "contributions.approvals", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureContributionReview}, requireOrg: true},
	"network-preview":         {op: "network.preview", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-recruiter-view":  {op: "mobile.network_recruiter_view", features: []STRIDEFeature{STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow}},
	"network-search":          {op: "network.search", features: []STRIDEFeature{STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch}, requireOrg: true},
	"contact-inbox":           {op: "network.contacts", features: []STRIDEFeature{STRIDEFeatureNetworkContact}},
	"network-blocks":          {op: "mobile.network_blocks", features: []STRIDEFeature{STRIDEFeatureNetworkContact}},
	"coworker-profile":        {op: "identity.coworker_profile", features: []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true},
	"organization-recruiting": {op: "network.recruiting", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true},
}

type strideE10MobileActionSpec struct {
	op         string
	features   []STRIDEFeature
	requireOrg bool
}

var strideE10MobileActions = map[string]strideE10MobileActionSpec{
	"organization-create":                  {op: "organizations.collection", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityWrite}},
	"organization-join":                    {op: "organizations.join_requests", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityWrite}},
	"organization-request-approve":         {op: "organizations.decide_join_request", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"organization-request-deny":            {op: "organizations.decide_join_request", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"organization-switch":                  {op: "session.switch_organization", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureActiveOrganizationSession}},
	"organization-leave":                   {op: "organizations.leave", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"organization-member-revoke":           {op: "organizations.revoke_member", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"network-publish":                      {op: "network.profile", features: []STRIDEFeature{STRIDEFeatureNetworkProfilePublication}},
	"network-pause":                        {op: "network.profile", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-profile-off":                  {op: "network.profile_off", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"contact-accept":                       {op: "network.decide_contact", features: []STRIDEFeature{STRIDEFeatureNetworkContact}},
	"contact-decline":                      {op: "network.decide_contact", features: []STRIDEFeature{STRIDEFeatureNetworkContact}},
	"contact-withdraw":                     {op: "network.decide_contact", features: []STRIDEFeature{STRIDEFeatureNetworkContact}, requireOrg: true},
	"network-block":                        {op: "network.block", features: []STRIDEFeature{STRIDEFeatureNetworkContact}},
	"network-unblock":                      {op: "network.block", features: []STRIDEFeature{STRIDEFeatureNetworkContact}},
	"profile-update":                       {op: "identity.self_profile", features: []STRIDEFeature{STRIDEFeaturePersonProfileAuthority}},
	"network-draft-save":                   {op: "network.profile_draft", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"contribution-subject-approve":         {op: "contributions.subject_review", features: []STRIDEFeature{STRIDEFeatureContributionReview}},
	"contribution-subject-dispute":         {op: "contributions.subject_review", features: []STRIDEFeature{STRIDEFeatureContributionReview}},
	"contribution-organization-approve":    {op: "contributions.decide_approval", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, requireOrg: true},
	"contribution-organization-deny":       {op: "contributions.decide_approval", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, requireOrg: true},
	"contribution-named-party-decision":    {op: "contributions.named_party_decision", features: []STRIDEFeature{STRIDEFeatureContributionReview}},
	"contribution-attestation-revoke":      {op: "contributions.revoke_attestation", features: []STRIDEFeature{STRIDEFeatureContributionReview}},
	"contribution-publish":                 {op: "contributions.publish", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate, STRIDEFeatureNetworkProfilePublication}},
	"contribution-withdraw":                {op: "contributions.withdraw", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-search-submit":                {op: "network.search", features: []STRIDEFeature{STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch}, requireOrg: true},
	"contact-send":                         {op: "network.contacts", features: []STRIDEFeature{STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch, STRIDEFeatureNetworkContact}, requireOrg: true},
	"organization-member-role-change":      {op: "organizations.change_member_role", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"organization-ownership-transfer":      {op: "organizations.transfer_ownership", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"organization-recruiting-grant-create": {op: "network.recruiting_grant", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"organization-recruiting-grant-revoke": {op: "network.recruiting_grant", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, requireOrg: true},
	"contribution-correct":                 {op: "contributions.correct", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, requireOrg: true},
	"contribution-revoke":                  {op: "contributions.revoke", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, requireOrg: true},
	"work-record-export":                   {op: "work_record.export", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"work-record-delete":                   {op: "work_record.delete", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-profile-export":               {op: "network.profile_export", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-profile-delete":               {op: "network.profile_delete", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
	"network-searchable-fields-update":     {op: "network.searchable_fields", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}},
}

var strideE10MobileActionSurfaces = map[string]string{
	"organization-create":                  "organizations",
	"organization-join":                    "organizations",
	"organization-request-approve":         "organization-requests",
	"organization-request-deny":            "organization-requests",
	"organization-switch":                  "organizations",
	"organization-leave":                   "organizations",
	"organization-member-revoke":           "organization-people",
	"network-publish":                      "network-preview",
	"network-pause":                        "network-preview",
	"network-profile-off":                  "network-preview",
	"contact-accept":                       "contact-inbox",
	"contact-decline":                      "contact-inbox",
	"contact-withdraw":                     "contact-inbox",
	"network-block":                        "network-blocks",
	"network-unblock":                      "network-blocks",
	"profile-update":                       "profile",
	"network-draft-save":                   "network-draft",
	"contribution-subject-approve":         "work-record",
	"contribution-subject-dispute":         "work-record",
	"contribution-organization-approve":    "contribution-approvals",
	"contribution-organization-deny":       "contribution-approvals",
	"contribution-named-party-decision":    "contribution-approvals",
	"contribution-attestation-revoke":      "contribution-approvals",
	"contribution-publish":                 "work-record",
	"contribution-withdraw":                "work-record",
	"network-search-submit":                "network-search",
	"contact-send":                         "network-search",
	"organization-member-role-change":      "organization-people",
	"organization-ownership-transfer":      "organization-people",
	"organization-recruiting-grant-create": "organization-recruiting",
	"organization-recruiting-grant-revoke": "organization-recruiting",
	"contribution-correct":                 "contribution-approvals",
	"contribution-revoke":                  "contribution-approvals",
	"work-record-export":                   "work-record",
	"work-record-delete":                   "work-record",
	"network-profile-export":               "network-preview",
	"network-profile-delete":               "network-preview",
	"network-searchable-fields-update":     "network-preview",
}

func (h *strideE10ProductHTTP) serveMobile(w http.ResponseWriter, r *http.Request, principal StrideE10ProductPrincipal) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "stride" || parts[2] != "v1" || parts[3] != "mobile" {
		return false
	}
	if parts[4] == "surfaces" && r.Method == http.MethodGet {
		surface := parts[5]
		spec, ok := strideE10MobileSurfaces[surface]
		if !ok {
			strideE10WriteOpaqueNotFound(w)
			return true
		}
		if !h.strideE10MobileAllowed(w, spec.features, spec.requireOrg, principal) {
			return true
		}
		targetID := ""
		if surface == "coworker-profile" {
			values := r.URL.Query()
			targetID = strings.TrimSpace(values.Get("person"))
			if len(values) != 1 || len(values["person"]) != 1 || !strideIdentifier(targetID) {
				strideE10WriteOpaqueNotFound(w)
				return true
			}
		} else if r.URL.RawQuery != "" {
			strideE10WriteOpaqueNotFound(w)
			return true
		}
		command := StrideE10ProductCommand{Operation: spec.op, Method: r.Method, Path: r.URL.Path, OrganizationID: principal.ActiveOrganizationID, ResourceID: surface, TargetID: targetID, ExpectedRevision: -1}
		h.strideE10ExecuteMobile(w, r, principal, command, surface)
		return true
	}
	if parts[4] == "actions" && r.Method == http.MethodPost {
		actionID := strings.TrimSpace(parts[5])
		if actionID == "" {
			strideE10WriteOpaqueNotFound(w)
			return true
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" {
			strideE10WriteError(w, http.StatusBadRequest, "idempotency_key_required")
			return true
		}
		body, action, surface, revision, err := strideE10ReadMobileActionBody(w, r)
		if err != nil {
			strideE10WriteError(w, http.StatusBadRequest, "invalid_request")
			return true
		}
		spec, ok := strideE10MobileActions[action]
		if !ok {
			strideE10WriteError(w, http.StatusBadRequest, "invalid_request")
			return true
		}
		if wantSurface, ok := strideE10MobileActionSurfaces[action]; !ok || surface != wantSurface {
			strideE10WriteError(w, http.StatusBadRequest, "invalid_request")
			return true
		}
		if !h.strideE10MobileAllowed(w, spec.features, spec.requireOrg, principal) {
			return true
		}
		command := StrideE10ProductCommand{Operation: spec.op, Method: r.Method, Path: r.URL.Path, OrganizationID: principal.ActiveOrganizationID, ResourceID: actionID, ExpectedRevision: revision, IdempotencyKey: key, Body: body}
		h.strideE10ExecuteMobile(w, r, principal, command, surface)
		return true
	}
	return false
}

func (h *strideE10ProductHTTP) strideE10MobileAllowed(w http.ResponseWriter, features []STRIDEFeature, requireOrg bool, principal StrideE10ProductPrincipal) bool {
	for _, feature := range features {
		if !h.features.Enabled(feature) {
			strideE10WriteError(w, http.StatusServiceUnavailable, "feature_unavailable")
			return false
		}
	}
	if requireOrg && !strideE10CompleteOrganizationPrincipal(principal) {
		strideE10WriteOpaqueNotFound(w)
		return false
	}
	return true
}

func (h *strideE10ProductHTTP) strideE10ExecuteMobile(w http.ResponseWriter, r *http.Request, principal StrideE10ProductPrincipal, command StrideE10ProductCommand, surface string) {
	value, replayed, err := h.backend.Execute(r.Context(), principal, command)
	if strideE10WriteBackendError(w, err) {
		return
	}
	if err := strideE10ValidateMobileProjection(value, surface); err != nil {
		strideE10WriteError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if surface == "coworker-profile" && !strideE10ProjectionBindsSingleTarget(value, command.TargetID) {
		strideE10WriteError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func strideE10ProjectionBindsSingleTarget(value any, targetID string) bool {
	if !strideIdentifier(targetID) {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var envelope struct {
		Availability string `json:"availability"`
		Items        []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	return json.Unmarshal(encoded, &envelope) == nil && envelope.Availability == "available" && len(envelope.Items) == 1 && envelope.Items[0].ID == targetID
}

func strideE10ReadMobileActionBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, string, string, int64, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, strideE10MaxBodyBytes))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return nil, "", "", 0, ErrStrideE10Invalid
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || len(raw) != 4 {
		return nil, "", "", 0, ErrStrideE10Invalid
	}
	for key := range raw {
		if key != "action" && key != "expectedRevision" && key != "surface" && key != "values" {
			return nil, "", "", 0, ErrStrideE10Invalid
		}
	}
	var action, surface string
	var revision int64
	if json.Unmarshal(raw["action"], &action) != nil || json.Unmarshal(raw["surface"], &surface) != nil || json.Unmarshal(raw["expectedRevision"], &revision) != nil || strings.TrimSpace(action) == "" || strings.TrimSpace(surface) == "" || revision < 1 {
		return nil, "", "", 0, ErrStrideE10Invalid
	}
	var values map[string]any
	if json.Unmarshal(raw["values"], &values) != nil || values == nil || strideE10ContainsAuthorityKey(values) || !strideE10ValidMobileActionValues(action, values) {
		return nil, "", "", 0, ErrStrideE10Invalid
	}
	return append(json.RawMessage(nil), body...), action, surface, revision, nil
}

var strideE10SlugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func strideE10ValidMobileActionValues(action string, values map[string]any) bool {
	allowed := func(keys ...string) bool {
		set := make(map[string]bool, len(keys))
		for _, key := range keys {
			set[key] = true
		}
		for key := range values {
			if !set[key] {
				return false
			}
		}
		return true
	}
	stringValue := func(key string, max int, required bool) bool {
		value, exists := values[key]
		if !exists {
			return !required
		}
		text, ok := value.(string)
		return ok && len(text) <= max && (!required || strings.TrimSpace(text) != "")
	}
	stringList := func(key string) bool {
		value, exists := values[key]
		if !exists {
			return true
		}
		items, ok := value.([]any)
		if !ok || len(items) > 20 {
			return false
		}
		seen := map[string]bool{}
		for _, raw := range items {
			item, ok := raw.(string)
			item = strings.TrimSpace(item)
			if !ok || item == "" || len(item) > 64 || seen[item] {
				return false
			}
			seen[item] = true
		}
		return true
	}
	switch action {
	case "profile-update":
		return len(values) > 0 && allowed("displayName", "pronouns", "bio", "workModes", "openTo") && stringValue("displayName", 80, false) && stringValue("pronouns", 40, false) && stringValue("bio", 280, false) && stringList("workModes") && stringList("openTo")
	case "organization-create":
		if !allowed("name", "slug") || !stringValue("name", 120, true) || !stringValue("slug", 63, true) {
			return false
		}
		slug, _ := values["slug"].(string)
		return strideE10SlugPattern.MatchString(slug)
	case "organization-join":
		return allowed("joinCode") && stringValue("joinCode", 128, true)
	case "network-draft-save":
		return len(values) > 0 && allowed("intro", "workModes", "openTo") && stringValue("intro", 280, false) && stringList("workModes") && stringList("openTo")
	case "network-searchable-fields-update":
		if !allowed("fields") {
			return false
		}
		raw, ok := values["fields"].([]any)
		if !ok || len(raw) > 9 {
			return false
		}
		allowedFields := map[string]bool{"display_name": true, "pronouns": true, "bio": true, "work_modes": true, "open_to": true, "visible_organizations": true, "contribution_problem_classes": true, "contribution_roles": true, "verified_contributions": true}
		seen := map[string]bool{}
		for _, candidate := range raw {
			field, ok := candidate.(string)
			if !ok || !allowedFields[field] || seen[field] {
				return false
			}
			seen[field] = true
		}
		return true
	case "network-search-submit":
		return allowed("query") && stringValue("query", 500, true)
	case "contact-send":
		if !allowed("purpose", "note", "collaborationType") || !stringValue("purpose", 80, true) || !stringValue("note", 1000, false) || !stringValue("collaborationType", 32, true) {
			return false
		}
		kind, _ := values["collaborationType"].(string)
		return kind == "collaboration" || kind == "advisory" || kind == "employment" || kind == "recruiting" || kind == "organization_join"
	case "organization-member-role-change":
		if !allowed("role") || !stringValue("role", 16, true) {
			return false
		}
		role, _ := values["role"].(string)
		return role == "member" || role == "admin"
	case "organization-recruiting-grant-revoke", "contribution-correct", "contribution-revoke":
		return allowed("reason") && stringValue("reason", 500, false)
	case "contribution-named-party-decision":
		if !allowed("decision", "reason") || !stringValue("decision", 16, true) || !stringValue("reason", 500, false) {
			return false
		}
		decision, _ := values["decision"].(string)
		return decision == "approved" || decision == "denied"
	case "contribution-attestation-revoke":
		return allowed("reason") && stringValue("reason", 500, false)
	case "organization-request-approve", "organization-request-deny", "contribution-subject-approve", "contribution-subject-dispute", "contribution-organization-approve", "contribution-organization-deny", "contact-accept", "contact-decline", "contact-withdraw":
		return allowed("reason") && stringValue("reason", 500, false)
	default:
		return len(values) == 0
	}
}

func strideE10ValidateMobileProjection(value any, expectedSurface string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var envelope map[string]any
	if json.Unmarshal(encoded, &envelope) != nil || envelope["surface"] != expectedSurface {
		return ErrStrideE10Invalid
	}
	allowedEnvelope := map[string]bool{"availability": true, "surface": true, "revision": true, "items": true, "reason": true}
	for key := range envelope {
		if !allowedEnvelope[key] {
			return ErrStrideE10Invalid
		}
	}
	availability, _ := envelope["availability"].(string)
	if availability == "unavailable" {
		reason, ok := envelope["reason"].(string)
		if len(envelope) != 3 || !ok || !strideE10BoundedRequiredString(reason, 240) {
			return ErrStrideE10Invalid
		}
		return nil
	}
	if availability != "available" || len(envelope) != 4 || envelope["reason"] != nil {
		return ErrStrideE10Invalid
	}
	revision, ok := envelope["revision"].(float64)
	if !ok || revision < 1 || revision != float64(int64(revision)) {
		return ErrStrideE10Invalid
	}
	items, ok := envelope["items"].([]any)
	if !ok {
		return ErrStrideE10Invalid
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || !strideE10AllowedMobileObject(item, map[string]bool{"id": true, "title": true, "summary": true, "status": true, "context": true, "updatedAt": true, "actions": true, "kind": true, "detail": true}) || !strideE10BoundedRequiredValue(item["id"], 160) || !strideE10BoundedRequiredValue(item["title"], 240) {
			return ErrStrideE10Invalid
		}
		if kind, exists := item["kind"]; exists {
			kindName, ok := kind.(string)
			detail, detailOK := item["detail"].(map[string]any)
			if !ok || !detailOK || strideE10ValidateMobileDetail(expectedSurface, kindName, detail) != nil {
				return ErrStrideE10Invalid
			}
		} else if _, exists := item["detail"]; exists {
			return ErrStrideE10Invalid
		}
		for _, key := range []string{"summary", "status", "context", "updatedAt"} {
			if value, exists := item[key]; exists && value != nil {
				text, ok := value.(string)
				if !ok || len(text) > 500 {
					return ErrStrideE10Invalid
				}
			}
		}
		if actions, exists := item["actions"]; exists {
			list, ok := actions.([]any)
			if !ok {
				return ErrStrideE10Invalid
			}
			for _, rawAction := range list {
				action, ok := rawAction.(map[string]any)
				if !ok || !strideE10AllowedMobileObject(action, map[string]bool{"id": true, "type": true, "label": true, "expectedRevision": true}) || !strideE10BoundedRequiredValue(action["id"], 160) || !strideE10BoundedRequiredValue(action["label"], 120) {
					return ErrStrideE10Invalid
				}
				typeName, ok := action["type"].(string)
				if !ok {
					return ErrStrideE10Invalid
				}
				if _, ok := strideE10MobileActions[typeName]; !ok || strideE10MobileActionSurfaces[typeName] != expectedSurface {
					return ErrStrideE10Invalid
				}
				rev, ok := action["expectedRevision"].(float64)
				if !ok || rev < 1 || rev != float64(int64(rev)) {
					return ErrStrideE10Invalid
				}
			}
		}
	}
	return nil
}

func strideE10ValidateMobileDetail(surface, kind string, detail map[string]any) error {
	allowed := map[string]map[string]bool{
		"self-profile-detail":          {"kind": true, "displayName": true, "pronouns": true, "bio": true, "workModes": true, "openTo": true, "openToEnabled": true, "organizationChoices": true},
		"coworker-profile-detail":      {"kind": true, "displayName": true, "role": true, "title": true, "team": true, "joinedAt": true},
		"network-profile-detail":       {"kind": true, "displayName": true, "pronouns": true, "bio": true, "visibleOrganizations": true, "workModes": true, "openTo": true},
		"work-record-section":          {"kind": true, "section": true, "entries": true, "openToEnabled": true},
		"contribution-evidence":        {"kind": true, "problem": true, "outcome": true, "contribution": true, "verificationTier": true, "releasedFields": true, "attestation": true, "publishedClaim": true, "artifactAccess": true, "reviewedInfluence": true},
		"contribution-review":          {"kind": true, "claim": true, "sourceRevision": true, "sourceDigest": true, "fieldDiffs": true, "namedPartyStates": true, "auditEntries": true},
		"network-state":                {"kind": true, "state": true, "searchableFields": true},
		"recruiting-governance":        {"kind": true, "grantState": true, "grantRevision": true, "expiresAt": true, "capability": true, "personSearchLimit": true, "organizationSearchLimit": true, "globalSearchLimit": true, "personContactLimit": true, "organizationContactLimit": true, "globalContactLimit": true, "receiptSummaries": true, "auditEntries": true},
		"organization-summary":         {"kind": true, "activeCount": true, "capacity": true, "pendingCount": true, "isCurrent": true, "role": true},
		"membership-detail":            {"kind": true, "role": true, "status": true, "isFinalOwner": true},
		"join-request-detail":          {"kind": true, "status": true, "expiresAt": true},
		"network-query-interpretation": {"kind": true, "verdict": true, "filters": true},
		"network-search-result":        {"kind": true, "why": true, "unknown": true, "verificationLabels": true, "publishedRefs": true},
		"contact-request-detail":       {"kind": true, "purpose": true, "collaborationType": true, "state": true, "channelRevealed": true},
		"block-detail":                 {"kind": true, "state": true, "targetKind": true},
		"export-receipt":               {"kind": true, "status": true, "packageDigest": true, "expiresAt": true},
		"purge-receipt":                {"kind": true, "status": true, "receiptId": true, "stores": true},
	}
	allowedSurfaces := map[string]map[string]bool{
		"self-profile-detail": {"profile": true}, "coworker-profile-detail": {"coworker-profile": true},
		"network-profile-detail": {"network-draft": true, "network-preview": true, "network-recruiter-view": true, "network-search": true},
		"work-record-section":    {"work-record": true}, "contribution-evidence": {"work-record": true, "network-preview": true, "network-recruiter-view": true},
		"contribution-review": {"contribution-approvals": true}, "network-state": {"network-draft": true, "network-preview": true},
		"recruiting-governance": {"organization-recruiting": true}, "organization-summary": {"organizations": true},
		"membership-detail": {"organization-people": true}, "join-request-detail": {"organization-requests": true},
		"network-query-interpretation": {"network-search": true}, "network-search-result": {"network-search": true},
		"contact-request-detail": {"contact-inbox": true}, "block-detail": {"network-blocks": true},
		"export-receipt": {"work-record": true, "network-preview": true}, "purge-receipt": {"work-record": true, "network-preview": true},
	}
	keys, ok := allowed[kind]
	if !ok || !allowedSurfaces[kind][surface] || detail["kind"] != kind || !strideE10AllowedMobileObject(detail, keys) {
		return ErrStrideE10Invalid
	}
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > 16<<10 {
		return ErrStrideE10Invalid
	}
	return strideE10ValidateMobileDetailValues(kind, detail)
}

func strideE10ValidateMobileDetailValues(kind string, value map[string]any) error {
	text := func(key string, max int, required bool) bool {
		raw, exists := value[key]
		if !exists {
			return !required
		}
		item, ok := raw.(string)
		return ok && len(item) <= max && (!required || strings.TrimSpace(item) != "")
	}
	list := func(key string, count, length int) bool { return strideE10DetailStrings(value[key], count, length) }
	boolean := func(key string) bool { _, ok := value[key].(bool); return ok }
	integer := func(key string, min int64) bool {
		number, ok := value[key].(float64)
		return ok && number == float64(int64(number)) && int64(number) >= min
	}
	timestamp := func(key string) bool {
		raw, ok := value[key].(string)
		if !ok {
			return false
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		return err == nil && !parsed.IsZero()
	}
	switch kind {
	case "self-profile-detail":
		return strideE10DetailValidity(text("displayName", 80, true) && text("pronouns", 40, false) && text("bio", 280, false) && list("workModes", 20, 64) && list("openTo", 20, 64) && boolean("openToEnabled") && list("organizationChoices", 3, 120))
	case "coworker-profile-detail":
		return strideE10DetailValidity(text("displayName", 80, true) && strideE10DetailEnum(value["role"], "owner", "admin", "member") && text("title", 120, false) && text("team", 120, false) && timestamp("joinedAt"))
	case "network-profile-detail":
		valid := text("displayName", 80, false) && text("pronouns", 40, false) && text("bio", 280, false) && strideE10DetailOptionalStrings(value, "visibleOrganizations", 3, 120) && strideE10DetailOptionalStrings(value, "workModes", 20, 64) && strideE10DetailOptionalStrings(value, "openTo", 20, 64)
		return strideE10DetailValidity(valid && len(value) > 1)
	case "work-record-section":
		sectionOK := strideE10DetailEnum(value["section"], "problems-outcomes", "how-i-contribute", "organizations-roles", "work-evidence", "people-agents-helped", "open-to")
		if value["section"] == "open-to" {
			return strideE10DetailValidity(sectionOK && list("entries", 50, 240) && boolean("openToEnabled"))
		}
		_, hasEnabled := value["openToEnabled"]
		return strideE10DetailValidity(sectionOK && !hasEnabled && list("entries", 50, 240))
	case "contribution-evidence":
		return strideE10DetailValidity(text("problem", 160, true) && text("outcome", 160, true) && text("contribution", 160, true) && strideE10DetailEnum(value["verificationTier"], "self_described", "organization_verified_opaque", "organization_verified_redacted", "public_source_verified") && list("releasedFields", 30, 64) && strideE10DetailReference(value["attestation"]) && strideE10DetailReference(value["publishedClaim"]) && strideE10DetailEnum(value["artifactAccess"], "authorized", "redacted") && text("reviewedInfluence", 240, false))
	case "contribution-review":
		return strideE10DetailValidity(strideE10DetailReference(value["claim"]) && integer("sourceRevision", 1) && strideE10DetailDigest(value["sourceDigest"]) && strideE10DetailFieldDiffs(value["fieldDiffs"]) && strideE10DetailPartyStates(value["namedPartyStates"]) && strideE10DetailAudit(value["auditEntries"]))
	case "network-state":
		return strideE10DetailValidity(strideE10DetailEnum(value["state"], "off", "draft", "live", "paused") && strideE10DetailSearchableFields(value["searchableFields"]))
	case "recruiting-governance":
		return strideE10DetailValidity(strideE10DetailEnum(value["grantState"], "active", "revoked", "expired") && integer("grantRevision", 1) && timestamp("expiresAt") && value["capability"] == "talent_searcher" && strideE10DetailLimit(value["personSearchLimit"]) && strideE10DetailLimit(value["organizationSearchLimit"]) && strideE10DetailLimit(value["globalSearchLimit"]) && strideE10DetailLimit(value["personContactLimit"]) && strideE10DetailLimit(value["organizationContactLimit"]) && strideE10DetailLimit(value["globalContactLimit"]) && strideE10DetailReceipts(value["receiptSummaries"]) && strideE10DetailAudit(value["auditEntries"]))
	case "organization-summary":
		capacity, ok := value["capacity"].(float64)
		active, activeOK := value["activeCount"].(float64)
		pending, pendingOK := value["pendingCount"].(float64)
		return strideE10DetailValidity(integer("activeCount", 0) && activeOK && active <= 3 && ok && capacity == 3 && integer("pendingCount", 0) && pendingOK && pending <= 3 && boolean("isCurrent") && strideE10DetailEnum(value["role"], "owner", "admin", "member"))
	case "membership-detail":
		return strideE10DetailValidity(strideE10DetailEnum(value["role"], "owner", "admin", "member") && strideE10DetailEnum(value["status"], "active", "departed", "revoked") && boolean("isFinalOwner"))
	case "join-request-detail":
		return strideE10DetailValidity(strideE10DetailEnum(value["status"], "pending", "approved", "denied", "cancelled", "expired") && timestamp("expiresAt"))
	case "network-query-interpretation":
		return strideE10DetailValidity(strideE10DetailEnum(value["verdict"], "admitted", "denied") && list("filters", 30, 240))
	case "network-search-result":
		return strideE10DetailValidity(list("why", 30, 240) && list("unknown", 30, 240) && list("verificationLabels", 30, 64) && strideE10DetailReferences(value["publishedRefs"], 50))
	case "contact-request-detail":
		state, _ := value["state"].(string)
		revealed, _ := value["channelRevealed"].(bool)
		return strideE10DetailValidity(text("purpose", 80, true) && strideE10DetailEnum(value["collaborationType"], "collaboration", "advisory", "employment", "recruiting", "organization_join") && strideE10DetailEnum(state, "pending", "accepted", "declined", "withdrawn", "expired") && boolean("channelRevealed") && (!revealed || state == "accepted"))
	case "block-detail":
		return strideE10DetailValidity(strideE10DetailEnum(value["state"], "active", "withdrawn") && strideE10DetailEnum(value["targetKind"], "person", "organization"))
	case "export-receipt":
		return strideE10DetailValidity(strideE10DetailEnum(value["status"], "pending", "ready", "expired", "failed") && strideE10DetailDigest(value["packageDigest"]) && timestamp("expiresAt"))
	case "purge-receipt":
		return strideE10DetailValidity(strideE10DetailEnum(value["status"], "queued", "completed", "failed_escalated") && text("receiptId", 160, true) && strideE10DetailPurgeStores(value["stores"]))
	}
	return ErrStrideE10Invalid
}

func strideE10DetailValidity(valid bool) error {
	if !valid {
		return ErrStrideE10Invalid
	}
	return nil
}
func strideE10DetailEnum(value any, allowed ...string) bool {
	text, ok := value.(string)
	return ok && containsSTRIDEString(allowed, text)
}
func strideE10DetailDigest(value any) bool {
	text, ok := value.(string)
	return ok && isHexDigest(text)
}
func strideE10DetailStrings(value any, maxItems, maxLength int) bool {
	items, ok := value.([]any)
	if !ok || len(items) > maxItems {
		return false
	}
	seen := map[string]bool{}
	for _, raw := range items {
		text, ok := raw.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > maxLength || seen[text] {
			return false
		}
		seen[text] = true
	}
	return true
}
func strideE10DetailReference(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || !strideE10AllowedMobileObject(object, map[string]bool{"id": true, "revision": true, "digest": true}) || !strideE10BoundedRequiredValue(object["id"], 160) || !strideE10DetailDigest(object["digest"]) {
		return false
	}
	revision, ok := object["revision"].(float64)
	return ok && revision >= 1 && revision == float64(int64(revision))
}
func strideE10DetailReferences(value any, max int) bool {
	items, ok := value.([]any)
	if !ok || len(items) > max {
		return false
	}
	seen := map[string]bool{}
	for _, raw := range items {
		if !strideE10DetailReference(raw) {
			return false
		}
		id := raw.(map[string]any)["id"].(string)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func strideE10DetailObjects(value any, max int, validate func(map[string]any) bool) bool {
	items, ok := value.([]any)
	if !ok || len(items) > max {
		return false
	}
	for _, raw := range items {
		object, ok := raw.(map[string]any)
		if !ok || !validate(object) {
			return false
		}
	}
	return true
}
func strideE10DetailFieldDiffs(value any) bool {
	return strideE10DetailObjects(value, 50, func(v map[string]any) bool {
		return strideE10AllowedMobileObject(v, map[string]bool{"field": true, "before": true, "after": true, "disclosureTier": true}) && strideE10BoundedRequiredValue(v["field"], 64) && strideE10BoundedRequiredValue(v["before"], 240) && strideE10BoundedRequiredValue(v["after"], 240) && strideE10DetailEnum(v["disclosureTier"], "public", "redacted", "opaque")
	})
}
func strideE10DetailPartyStates(value any) bool {
	return strideE10DetailObjects(value, 50, func(v map[string]any) bool {
		_, required := v["required"].(bool)
		return required && strideE10AllowedMobileObject(v, map[string]bool{"partyLabel": true, "state": true, "required": true}) && strideE10BoundedRequiredValue(v["partyLabel"], 120) && strideE10DetailEnum(v["state"], "pending", "approved", "denied", "withdrawn", "expired", "superseded")
	})
}
func strideE10DetailAudit(value any) bool {
	return strideE10DetailObjects(value, 100, func(v map[string]any) bool {
		if !strideE10AllowedMobileObject(v, map[string]bool{"action": true, "actorRole": true, "revision": true, "occurredAt": true}) || !strideE10BoundedRequiredValue(v["action"], 80) || !strideE10BoundedRequiredValue(v["actorRole"], 80) {
			return false
		}
		revision, ok := v["revision"].(float64)
		occurred, ok2 := v["occurredAt"].(string)
		_, err := time.Parse(time.RFC3339, occurred)
		return ok && revision >= 1 && revision == float64(int64(revision)) && ok2 && err == nil
	})
}
func strideE10DetailReceipts(value any) bool {
	return strideE10DetailObjects(value, 100, func(v map[string]any) bool {
		if !strideE10AllowedMobileObject(v, map[string]bool{"kind": true, "verdict": true, "revision": true, "occurredAt": true}) || !strideE10DetailEnum(v["kind"], "search", "contact") || !strideE10DetailEnum(v["verdict"], "admitted", "denied") {
			return false
		}
		revision, ok := v["revision"].(float64)
		occurred, ok2 := v["occurredAt"].(string)
		_, err := time.Parse(time.RFC3339, occurred)
		return ok && revision >= 1 && revision == float64(int64(revision)) && ok2 && err == nil
	})
}
func strideE10DetailLimit(value any) bool {
	object, ok := value.(map[string]any)
	if !ok || !strideE10AllowedMobileObject(object, map[string]bool{"used": true, "limit": true, "windowEndsAt": true}) {
		return false
	}
	used, usedOK := object["used"].(float64)
	limit, limitOK := object["limit"].(float64)
	ends, endsOK := object["windowEndsAt"].(string)
	_, err := time.Parse(time.RFC3339, ends)
	return usedOK && limitOK && endsOK && used >= 0 && limit >= 1 && used <= limit && used == float64(int64(used)) && limit == float64(int64(limit)) && err == nil
}
func strideE10DetailOptionalStrings(value map[string]any, key string, maxItems, maxLength int) bool {
	raw, exists := value[key]
	return !exists || strideE10DetailStrings(raw, maxItems, maxLength)
}
func strideE10DetailPurgeStores(value any) bool {
	items, ok := value.([]any)
	if !ok || len(items) > 13 {
		return false
	}
	allowed := []string{"projection", "lexical_index", "vector_index", "reranker_cache", "application_cache", "cdn", "push_queue", "job_queue", "analytics", "audit_log", "test_fixture", "export", "backup_manifest"}
	seen := map[string]bool{}
	for _, raw := range items {
		text, ok := raw.(string)
		if !ok || !containsSTRIDEString(allowed, text) || seen[text] {
			return false
		}
		seen[text] = true
	}
	return true
}
func strideE10DetailOptionalText(value any, max int) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && len(text) <= max
}
func strideE10DetailSearchableFields(value any) bool {
	items, ok := value.([]any)
	if !ok || len(items) > 9 {
		return false
	}
	allowed := []string{"display_name", "pronouns", "bio", "work_modes", "open_to", "visible_organizations", "contribution_problem_classes", "contribution_roles", "verified_contributions"}
	seen := map[string]bool{}
	for _, raw := range items {
		text, ok := raw.(string)
		if !ok || !containsSTRIDEString(allowed, text) || seen[text] {
			return false
		}
		seen[text] = true
	}
	return true
}

func strideE10AllowedMobileObject(object map[string]any, allowed map[string]bool) bool {
	for key := range object {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func strideE10BoundedRequiredValue(value any, max int) bool {
	text, ok := value.(string)
	return ok && strideE10BoundedRequiredString(text, max)
}

func strideE10BoundedRequiredString(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max
}

func strideE10WriteBackendError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, ErrStrideE10NotFound), errors.Is(err, ErrStrideE10Denied):
		strideE10WriteOpaqueNotFound(w)
	case errors.Is(err, ErrStrideE10Conflict):
		strideE10WriteError(w, http.StatusConflict, "conflict")
	case errors.Is(err, ErrStrideE10Invalid):
		strideE10WriteError(w, http.StatusBadRequest, "invalid_request")
	default:
		strideE10WriteError(w, http.StatusInternalServerError, "internal_error")
	}
	return true
}

func strideE10CompleteOrganizationPrincipal(p StrideE10ProductPrincipal) bool {
	return p.ActiveOrganizationID != "" && p.OrganizationMembershipID != "" &&
		p.OrganizationMembershipRev > 0 && p.ActiveOrganizationSessionRev > 0
}

func strideE10ReadMutationBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, int64, error) {
	reader := http.MaxBytesReader(w, r.Body, strideE10MaxBodyBytes)
	body, err := io.ReadAll(reader)
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return nil, 0, ErrStrideE10Invalid
	}
	var envelope struct {
		ExpectedRevision *int64 `json:"expectedRevision"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&envelope); err != nil || envelope.ExpectedRevision == nil || *envelope.ExpectedRevision < 0 {
		return nil, 0, ErrStrideE10Invalid
	}
	var authorityScan any
	if err := json.Unmarshal(body, &authorityScan); err != nil || strideE10ContainsAuthorityKey(authorityScan) {
		return nil, 0, ErrStrideE10Invalid
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, 0, ErrStrideE10Invalid
	}
	return append(json.RawMessage(nil), body...), *envelope.ExpectedRevision, nil
}

func strideE10ContainsAuthorityKey(value any) bool {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			if strideE10ContainsAuthorityKey(child) {
				return true
			}
		}
	case map[string]any:
		for key, child := range typed {
			normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
			if normalized == "personid" || normalized == "organizationid" || normalized == "orgid" || normalized == "tenantid" ||
				strings.Contains(normalized, "membershipid") || strings.Contains(normalized, "membershiprevision") || normalized == "membershiprev" ||
				strings.Contains(normalized, "sessionrevision") || strings.Contains(normalized, "controller") || strings.Contains(normalized, "authority") ||
				strings.Contains(normalized, "grant") || strings.Contains(normalized, "signing") {
				return true
			}
			if strideE10ContainsAuthorityKey(child) {
				return true
			}
		}
	}
	return false
}

func strideE10MatchRoute(method, path string) (strideE10Route, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	exact := func(want ...string) bool {
		if len(parts) != len(want) {
			return false
		}
		for i := range want {
			if want[i] != "*" && parts[i] != want[i] {
				return false
			}
		}
		return true
	}
	mutation := method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut || method == http.MethodDelete
	if exact("api", "identity", "v1", "me", "profile") && (method == http.MethodGet || method == http.MethodPatch) {
		return strideE10Route{op: "identity.self_profile", features: []STRIDEFeature{STRIDEFeaturePersonProfileAuthority}, mutation: mutation}, true
	}
	if exact("api", "identity", "v1", "people", "*") && method == http.MethodGet {
		return strideE10Route{op: "identity.coworker_profile", features: []STRIDEFeature{STRIDEFeaturePersonProfileAuthority, STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true, resourceID: parts[4]}, true
	}
	if exact("api", "organizations") && (method == http.MethodGet || method == http.MethodPost) {
		feature := STRIDEFeatureOrganizationAuthorityRead
		if mutation {
			feature = STRIDEFeatureOrganizationAuthorityWrite
		}
		return strideE10Route{op: "organizations.collection", features: []STRIDEFeature{feature}, mutation: mutation}, true
	}
	if exact("api", "organizations", "*", "members", "*", "profile") && (method == http.MethodGet || method == http.MethodPatch) {
		fs := []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}
		if mutation {
			fs = append(fs, STRIDEFeatureOrganizationAuthorityWrite)
		}
		return strideE10Route{op: "organizations.member_profile", features: fs, mutation: mutation, requireOrg: true, currentOrg: true, organizationID: parts[2], membershipID: parts[4]}, true
	}
	if exact("api", "organizations", "*", "join-requests") && (method == http.MethodGet || method == http.MethodPost) {
		fs := []STRIDEFeature{STRIDEFeatureOrganizationAuthorityWrite}
		current := false
		if method == http.MethodGet {
			fs = []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}
			current = true
		}
		return strideE10Route{op: "organizations.join_requests", features: fs, mutation: mutation, requireOrg: current, currentOrg: current, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "join-requests", "*") && method == http.MethodDelete {
		return strideE10Route{op: "organizations.close_join_request", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, organizationID: parts[2], resourceID: parts[4]}, true
	}
	if exact("api", "organizations", "*", "join-requests", "*", "decision") && method == http.MethodPost {
		return strideE10Route{op: "organizations.decide_join_request", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2], resourceID: parts[4]}, true
	}
	if exact("api", "organizations", "*", "leave") && method == http.MethodPost {
		return strideE10Route{op: "organizations.leave", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "members", "*", "role") && method == http.MethodPost {
		return strideE10Route{op: "organizations.change_member_role", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2], membershipID: parts[4]}, true
	}
	if exact("api", "organizations", "*", "members", "*") && method == http.MethodDelete {
		return strideE10Route{op: "organizations.revoke_member", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2], membershipID: parts[4]}, true
	}
	if exact("api", "organizations", "*", "ownership-transfer") && method == http.MethodPost {
		return strideE10Route{op: "organizations.transfer_ownership", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "audit") && method == http.MethodGet {
		return strideE10Route{op: "organizations.audit", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "join-requests", "*", "expire") && method == http.MethodPost {
		return strideE10Route{op: "organizations.expire_join_request", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2], resourceID: parts[4]}, true
	}
	if exact("api", "session", "active-organization", "*") && method == http.MethodPost {
		return strideE10Route{op: "session.switch_organization", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureActiveOrganizationSession}, mutation: true, resourceID: parts[3]}, true
	}
	if exact("api", "work-record", "v1", "me") && method == http.MethodGet {
		return strideE10Route{op: "work_record.self", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}}, true
	}
	if exact("api", "contributions", "v1", "claims", "*", "subject-approve") && method == http.MethodPost {
		return strideE10Route{op: "contributions.subject_review", features: []STRIDEFeature{STRIDEFeatureContributionReview}, mutation: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "claims", "*", "subject-dispute") && method == http.MethodPost {
		return strideE10Route{op: "contributions.subject_review", features: []STRIDEFeature{STRIDEFeatureContributionReview}, mutation: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "claims", "*", "publication") && method == http.MethodPost {
		return strideE10Route{op: "contributions.publish", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate, STRIDEFeatureNetworkProfilePublication}, mutation: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "claims", "*", "correction") && method == http.MethodPost {
		return strideE10Route{op: "contributions.correct", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, mutation: true, requireOrg: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "claims", "*", "revocation") && method == http.MethodPost {
		return strideE10Route{op: "contributions.revoke", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, mutation: true, requireOrg: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "approvals", "*", "named-party-decision") && method == http.MethodPost {
		return strideE10Route{op: "contributions.named_party_decision", features: []STRIDEFeature{STRIDEFeatureContributionReview}, mutation: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "attestations", "*", "revocation") && method == http.MethodPost {
		return strideE10Route{op: "contributions.revoke_attestation", features: []STRIDEFeature{STRIDEFeatureContributionReview}, mutation: true, resourceID: parts[4]}, true
	}
	if exact("api", "contributions", "v1", "publications", "*", "withdrawal") && method == http.MethodPost {
		return strideE10Route{op: "contributions.withdraw", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: true, resourceID: parts[4]}, true
	}
	if exact("api", "organizations", "*", "contribution-approvals") && method == http.MethodGet {
		return strideE10Route{op: "contributions.approvals", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureContributionReview}, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "contribution-audit") && method == http.MethodGet {
		return strideE10Route{op: "contributions.audit", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureContributionReview}, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "recruiting", "grants") && (method == http.MethodGet || method == http.MethodPost) {
		features := []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}
		if mutation {
			features = append(features, STRIDEFeatureOrganizationAuthorityWrite)
		}
		return strideE10Route{op: "network.recruiting_grants", features: features, mutation: mutation, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "recruiting", "grants", "*", "revocation") && method == http.MethodPost {
		return strideE10Route{op: "network.recruiting_grant_revoke", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2], resourceID: parts[5]}, true
	}
	if exact("api", "organizations", "*", "recruiting", "audit") && method == http.MethodGet {
		return strideE10Route{op: "network.recruiting_audit", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "recruiting", "receipts") && method == http.MethodGet {
		return strideE10Route{op: "network.recruiting_receipts", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "recruiting", "limits") && method == http.MethodGet {
		return strideE10Route{op: "network.recruiting_limits", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead}, requireOrg: true, currentOrg: true, organizationID: parts[2]}, true
	}
	if exact("api", "organizations", "*", "contribution-approvals", "*", "decision") && method == http.MethodPost {
		return strideE10Route{op: "contributions.decide_approval", features: []STRIDEFeature{STRIDEFeatureOrganizationAuthorityRead, STRIDEFeatureOrganizationAuthorityWrite, STRIDEFeatureContributionReview}, mutation: true, requireOrg: true, currentOrg: true, organizationID: parts[2], resourceID: parts[4]}, true
	}
	if exact("api", "network", "v1", "me", "profile") && (method == http.MethodGet || method == http.MethodPatch) {
		return strideE10Route{op: "network.profile", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: mutation}, true
	}
	if exact("api", "network", "v1", "me", "profile", "*") && method == http.MethodPost && oneOf(parts[5], "publish", "pause", "off", "delete") {
		feature := STRIDEFeatureWorkRecordPrivate
		if parts[5] == "publish" {
			feature = STRIDEFeatureNetworkProfilePublication
		}
		return strideE10Route{op: "network.profile_" + parts[5], features: []STRIDEFeature{feature}, mutation: true, resourceID: parts[5]}, true
	}
	if exact("api", "work-record", "v1", "me", "export") && method == http.MethodPost {
		return strideE10Route{op: "work_record.export", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: true}, true
	}
	if exact("api", "work-record", "v1", "me", "exports", "*") && method == http.MethodGet {
		return strideE10Route{op: "work_record.export_download", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, resourceID: parts[5]}, true
	}
	if exact("api", "work-record", "v1", "me", "deletion") && method == http.MethodPost {
		return strideE10Route{op: "work_record.delete", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: true}, true
	}
	if exact("api", "network", "v1", "me", "export") && method == http.MethodPost {
		return strideE10Route{op: "network.profile_export", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: true}, true
	}
	if exact("api", "network", "v1", "me", "exports", "*") && method == http.MethodGet {
		return strideE10Route{op: "network.profile_export_download", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, resourceID: parts[5]}, true
	}
	if exact("api", "network", "v1", "me", "draft") && (method == http.MethodGet || method == http.MethodPatch) {
		return strideE10Route{op: "network.profile_draft", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: mutation}, true
	}
	if exact("api", "network", "v1", "me", "searchable-fields") && method == http.MethodPatch {
		return strideE10Route{op: "network.searchable_fields", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}, mutation: true}, true
	}
	if exact("api", "network", "v1", "me", "preview") && method == http.MethodPost {
		return strideE10Route{op: "network.preview", features: []STRIDEFeature{STRIDEFeatureWorkRecordPrivate}}, true
	}
	if exact("api", "network", "v1", "search") && method == http.MethodPost {
		return strideE10Route{op: "network.search", features: []STRIDEFeature{STRIDEFeatureNetworkProfilePublication, STRIDEFeatureNetworkProjectionShadow, STRIDEFeatureNetworkSearch}, mutation: true, requireOrg: true}, true
	}
	if exact("api", "network", "v1", "contacts") && (method == http.MethodGet || method == http.MethodPost) {
		return strideE10Route{op: "network.contacts", features: []STRIDEFeature{STRIDEFeatureNetworkContact}, mutation: mutation, requireOrg: mutation}, true
	}
	if exact("api", "network", "v1", "contacts", "*", "decision") && method == http.MethodPost {
		return strideE10Route{op: "network.decide_contact", features: []STRIDEFeature{STRIDEFeatureNetworkContact}, mutation: true, requireOrg: true, resourceID: parts[4]}, true
	}
	if exact("api", "network", "v1", "blocks", "*") && (method == http.MethodPut || method == http.MethodDelete) {
		return strideE10Route{op: "network.block", features: []STRIDEFeature{STRIDEFeatureNetworkContact}, mutation: true, resourceID: parts[4]}, true
	}
	return strideE10Route{}, false
}

func strideE10WriteOpaqueNotFound(w http.ResponseWriter) {
	strideE10WriteError(w, http.StatusNotFound, "not_found")
}

func strideE10WriteError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
}
