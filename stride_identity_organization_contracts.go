package main

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var organizationSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// PersonProfile is the person's safe, global, self-controlled identity record.
// Credentials, account-subject digests, memberships, and private mind data do
// not belong in this projection.
type PersonProfile struct {
	Header                 STRIDEContractHeader `json:"header"`
	PersonID               string               `json:"personId"`
	DisplayName            string               `json:"displayName"`
	AvatarBlobRef          string               `json:"avatarBlobRef,omitempty"`
	Pronouns               string               `json:"pronouns,omitempty"`
	Bio                    string               `json:"bio,omitempty"`
	WorkModes              []string             `json:"workModes,omitempty"`
	OpenTo                 []string             `json:"openTo,omitempty"`
	OpenToEnabled          bool                 `json:"openToEnabled"`
	VisibleOrganizationIDs []string             `json:"visibleOrganizationIds,omitempty"`
	Status                 string               `json:"status"`
	UpdatedAt              time.Time            `json:"updatedAt"`
}

func (v PersonProfile) Validate() error {
	if v.Header.Validate(STRIDEContractPersonProfile) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant ||
		!strideIdentifier(v.PersonID) || v.Header.ID != v.PersonID || !stridePlainText(v.DisplayName, 80, v.Status == "active") ||
		!validOptionalSTRIDEID(v.AvatarBlobRef) || !stridePlainText(v.Pronouns, 40, false) || !stridePlainText(v.Bio, 280, false) ||
		!validOptionalUniqueSTRIDEIDs(v.WorkModes) || !validOpenToPreferences(v.OpenTo) || !validOptionalUniqueSTRIDEIDs(v.VisibleOrganizationIDs) ||
		!oneOf(v.Status, "active", "deleted") || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.Header.CreatedAt) {
		return ErrSTRIDEContractInvalid
	}
	if !v.OpenToEnabled && len(v.OpenTo) != 0 {
		return ErrSTRIDEContractInvalid
	}
	if v.Status == "deleted" && (v.DisplayName != "" || v.AvatarBlobRef != "" || v.Pronouns != "" || v.Bio != "" || len(v.WorkModes) != 0 || len(v.OpenTo) != 0 || len(v.VisibleOrganizationIDs) != 0) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

// OrganizationMemberProfile contains only the role context visible to current
// coworkers in one organization. It never becomes the person's global or
// network profile and cannot carry credentials, another membership, or mind
// data.
type OrganizationMemberProfile struct {
	Header                      STRIDEContractHeader `json:"header"`
	PersonID                    string               `json:"personId"`
	OrganizationID              string               `json:"organizationId"`
	MembershipID                string               `json:"membershipId"`
	MembershipRevision          int64                `json:"membershipRevision"`
	Title                       string               `json:"title,omitempty"`
	Team                        string               `json:"team,omitempty"`
	JoinedAt                    time.Time            `json:"joinedAt"`
	UpdatedByMembershipID       string               `json:"updatedByMembershipId"`
	UpdatedByMembershipRevision int64                `json:"updatedByMembershipRevision"`
	UpdatedAt                   time.Time            `json:"updatedAt"`
}

func (v OrganizationMemberProfile) Validate() error {
	if v.Header.Validate(STRIDEContractOrganizationMemberProfile) != nil || v.Header.TenantID != v.OrganizationID ||
		!strideIdentifier(v.PersonID) || !strideIdentifier(v.OrganizationID) || !strideIdentifier(v.MembershipID) || v.MembershipRevision < 1 ||
		!stridePlainText(v.Title, 120, false) || !stridePlainText(v.Team, 120, false) || v.JoinedAt.IsZero() ||
		!strideIdentifier(v.UpdatedByMembershipID) || v.UpdatedByMembershipRevision < 1 || v.UpdatedAt.Before(v.JoinedAt) || v.Header.CreatedAt.After(v.UpdatedAt) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type Organization struct {
	Header          STRIDEContractHeader `json:"header"`
	Name            string               `json:"name"`
	Slug            string               `json:"slug"`
	Status          string               `json:"status"`
	Discoverability string               `json:"discoverability"`
	CreatorPersonID string               `json:"creatorPersonId"`
	PolicyRevision  int64                `json:"policyRevision"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
}

func (v Organization) Validate() error {
	if v.Header.Validate(STRIDEContractOrganization) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant ||
		!stridePlainText(v.Name, 120, true) || !organizationSlugPattern.MatchString(v.Slug) ||
		!oneOf(v.Status, "active", "archived") || !oneOf(v.Discoverability, "private", "listed") ||
		!strideIdentifier(v.CreatorPersonID) || v.PolicyRevision < 1 || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() ||
		v.Header.CreatedAt.Before(v.CreatedAt) || v.UpdatedAt.Before(v.Header.CreatedAt) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type OrganizationMembership struct {
	Header                STRIDEContractHeader `json:"header"`
	PersonID              string               `json:"personId"`
	OrganizationID        string               `json:"organizationId"`
	Role                  string               `json:"role"`
	Status                string               `json:"status"`
	GrantedAt             time.Time            `json:"grantedAt"`
	EndedAt               *time.Time           `json:"endedAt,omitempty"`
	GrantedByMembershipID string               `json:"grantedByMembershipId,omitempty"`
}

func (v OrganizationMembership) Validate() error {
	if v.Header.Validate(STRIDEContractOrganizationMembership) != nil || !strideIdentifier(v.PersonID) ||
		!strideIdentifier(v.OrganizationID) || v.Header.TenantID != v.OrganizationID ||
		!oneOf(v.Role, "owner", "admin", "member") || !oneOf(v.Status, "active", "departed", "revoked") ||
		v.GrantedAt.IsZero() || v.Header.CreatedAt.Before(v.GrantedAt) || !validOptionalSTRIDEID(v.GrantedByMembershipID) ||
		(v.Status == "active") != (v.EndedAt == nil) || (v.EndedAt != nil && (v.EndedAt.Before(v.GrantedAt) || v.EndedAt.Before(v.Header.CreatedAt))) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type OrganizationJoinRequest struct {
	Header                STRIDEContractHeader `json:"header"`
	PersonID              string               `json:"personId"`
	OrganizationID        string               `json:"organizationId"`
	Status                string               `json:"status"`
	RequestedAt           time.Time            `json:"requestedAt"`
	ExpiresAt             time.Time            `json:"expiresAt"`
	DecidedAt             *time.Time           `json:"decidedAt,omitempty"`
	DecidedByMembershipID string               `json:"decidedByMembershipId,omitempty"`
	DecisionReasonDigest  string               `json:"decisionReasonDigest,omitempty"`
}

func (v OrganizationJoinRequest) Validate() error {
	if v.Header.Validate(STRIDEContractOrganizationJoinRequest) != nil || !strideIdentifier(v.PersonID) ||
		!strideIdentifier(v.OrganizationID) || v.Header.TenantID != v.OrganizationID ||
		!oneOf(v.Status, "pending", "approved", "denied", "cancelled", "expired") || v.RequestedAt.IsZero() ||
		!v.ExpiresAt.After(v.RequestedAt) || v.Header.CreatedAt.Before(v.RequestedAt) ||
		!validOptionalSTRIDEID(v.DecidedByMembershipID) || !validOptionalDigest(v.DecisionReasonDigest) {
		return ErrSTRIDEContractInvalid
	}
	if v.Status == "pending" {
		if v.DecidedAt != nil || v.DecidedByMembershipID != "" || v.DecisionReasonDigest != "" {
			return ErrSTRIDEContractInvalid
		}
		return nil
	}
	if v.DecidedAt == nil || v.DecidedAt.Before(v.Header.CreatedAt) || (v.Status == "approved" || v.Status == "denied") && v.DecidedByMembershipID == "" {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ActiveOrganizationSession struct {
	Header               STRIDEContractHeader `json:"header"`
	SessionSubjectDigest string               `json:"sessionSubjectDigest"`
	PersonID             string               `json:"personId"`
	OrganizationID       string               `json:"organizationId"`
	MembershipID         string               `json:"membershipId"`
	MembershipRevision   int64                `json:"membershipRevision"`
	SessionRevision      int64                `json:"sessionRevision"`
	Status               string               `json:"status"`
	BoundAt              time.Time            `json:"boundAt"`
	ExpiresAt            time.Time            `json:"expiresAt"`
	InvalidatedAt        *time.Time           `json:"invalidatedAt,omitempty"`
}

func (v ActiveOrganizationSession) Validate() error {
	if v.Header.Validate(STRIDEContractActiveOrganizationSession) != nil || v.Header.TenantID != STRIDEGlobalPersonTenant ||
		!isHexDigest(v.SessionSubjectDigest) || !strideIdentifier(v.PersonID) || !strideIdentifier(v.OrganizationID) ||
		!strideIdentifier(v.MembershipID) || v.MembershipRevision < 1 || v.SessionRevision < 1 ||
		!oneOf(v.Status, "active", "invalidated", "expired") || v.BoundAt.IsZero() || !v.ExpiresAt.After(v.BoundAt) ||
		v.Header.CreatedAt.Before(v.BoundAt) || (v.Status == "active") != (v.InvalidatedAt == nil) ||
		(v.InvalidatedAt != nil && (v.InvalidatedAt.Before(v.BoundAt) || v.InvalidatedAt.Before(v.Header.CreatedAt))) ||
		(v.Status == "expired" && (v.InvalidatedAt == nil || v.InvalidatedAt.Before(v.ExpiresAt))) {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type OrganizationAuditEvent struct {
	Header                  STRIDEContractHeader `json:"header"`
	OrganizationID          string               `json:"organizationId"`
	ActorPersonID           string               `json:"actorPersonId,omitempty"`
	ActorMembershipID       string               `json:"actorMembershipId,omitempty"`
	ActorMembershipRevision int64                `json:"actorMembershipRevision,omitempty"`
	SubjectPersonID         string               `json:"subjectPersonId,omitempty"`
	Action                  string               `json:"action"`
	PriorRevision           int64                `json:"priorRevision"`
	NewRevision             int64                `json:"newRevision"`
	ReasonDigest            string               `json:"reasonDigest,omitempty"`
	CorrelationID           string               `json:"correlationId"`
	IdempotencyKeyDigest    string               `json:"idempotencyKeyDigest"`
	OccurredAt              time.Time            `json:"occurredAt"`
}

func (v OrganizationAuditEvent) Validate() error {
	if v.Header.Validate(STRIDEContractOrganizationAuditEvent) != nil || !strideIdentifier(v.OrganizationID) || v.Header.TenantID != v.OrganizationID ||
		!validOptionalSTRIDEID(v.ActorPersonID) || !validOptionalSTRIDEID(v.ActorMembershipID) || !validOptionalSTRIDEID(v.SubjectPersonID) ||
		!oneOf(v.Action, "create", "request", "approve", "deny", "cancel", "expire", "switch", "role_change", "transfer", "leave", "revoke", "archive") ||
		v.PriorRevision < 0 || v.NewRevision < 1 || v.NewRevision < v.PriorRevision || !validOptionalDigest(v.ReasonDigest) ||
		!strideIdentifier(v.CorrelationID) || !isHexDigest(v.IdempotencyKeyDigest) || v.OccurredAt.IsZero() || v.Header.CreatedAt.After(v.OccurredAt) {
		return ErrSTRIDEContractInvalid
	}
	if (v.ActorMembershipID == "") != (v.ActorMembershipRevision == 0) {
		return ErrSTRIDEContractInvalid
	}
	if v.ActorMembershipID != "" && v.ActorPersonID == "" {
		return ErrSTRIDEContractInvalid
	}
	if v.ActorMembershipID == "" && !oneOf(v.Action, "create", "request", "cancel", "expire") {
		return ErrSTRIDEContractInvalid
	}
	if v.SubjectPersonID == "" && !oneOf(v.Action, "create", "archive") {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

func stridePlainText(value string, maxRunes int, required bool) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > maxRunes || required && value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validOpenToPreferences(values []string) bool {
	if !validOptionalUniqueSTRIDEIDs(values) {
		return false
	}
	for _, value := range values {
		if !oneOf(value, "collaboration", "advisory", "employment", "recruiting") {
			return false
		}
	}
	return true
}

func validOptionalUniqueSTRIDEIDs(values []string) bool {
	return len(values) == 0 || uniqueSTRIDEIDs(values)
}
