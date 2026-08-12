package main

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Project is the stable, organization-scoped identity for a body of work.
// Threads, meetings, runs and artifacts bind to it through revisioned edges;
// neither a title nor any one thread is Project identity or authority.
type Project struct {
	Header                STRIDEContractHeader `json:"header"`
	ProjectID             string               `json:"projectId"`
	OrganizationID        string               `json:"organizationId"`
	Title                 string               `json:"title"`
	Aliases               []string             `json:"aliases,omitempty"`
	Lifecycle             string               `json:"lifecycle"`
	RetentionPolicy       string               `json:"retentionPolicy"`
	ControllerMemberships []STRIDEReference    `json:"controllerMemberships"`
	Audience              STRIDEAudience       `json:"audience"`
	ACLRevision           int64                `json:"aclRevision"`
	CreatorPersonID       string               `json:"creatorPersonId"`
	CreatedAt             time.Time            `json:"createdAt"`
	UpdatedAt             time.Time            `json:"updatedAt"`
	Supersedes            *STRIDEReference     `json:"supersedes,omitempty"`
}

func (v Project) Validate() error {
	if v.Header.Validate(STRIDEContractProject) != nil || v.ProjectID != v.Header.ID ||
		v.OrganizationID != v.Header.TenantID || !strideIdentifier(v.ProjectID) ||
		!strideIdentifier(v.OrganizationID) || !stridePlainText(v.Title, 120, true) ||
		!validProjectAliases(v.Aliases) || !oneOf(v.Lifecycle, "draft", "active", "archived") ||
		!strideIdentifier(v.RetentionPolicy) || !validateProjectControllers(v.ControllerMemberships, v.OrganizationID) ||
		v.Audience.Validate() != nil || v.Audience.Visibility != "project" || v.ACLRevision < 1 || !strideIdentifier(v.CreatorPersonID) || v.CreatedAt.IsZero() ||
		v.UpdatedAt.IsZero() || v.Header.CreatedAt.Before(v.CreatedAt) || v.UpdatedAt.Before(v.Header.CreatedAt) {
		return ErrSTRIDEContractInvalid
	}
	if v.Header.Revision == 1 {
		if v.Supersedes != nil {
			return ErrSTRIDEContractInvalid
		}
	} else if v.Supersedes == nil || v.Supersedes.Validate() != nil ||
		v.Supersedes.ContractType != STRIDEContractProject || v.Supersedes.ID != v.ProjectID ||
		v.Supersedes.Revision != v.Header.Revision-1 {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ProjectThreadBinding struct {
	Header                  STRIDEContractHeader `json:"header"`
	Project                 STRIDEReference      `json:"project"`
	ThreadID                string               `json:"threadId"`
	Kind                    string               `json:"kind"`
	State                   string               `json:"state"`
	ThreadAudienceRevision  int64                `json:"threadAudienceRevision"`
	ThreadACLDigest         string               `json:"threadAclDigest"`
	ActorPersonID           string               `json:"actorPersonId"`
	ActorMembershipID       string               `json:"actorMembershipId"`
	ActorMembershipRevision int64                `json:"actorMembershipRevision"`
	BoundAt                 time.Time            `json:"boundAt"`
	Supersedes              *STRIDEReference     `json:"supersedes,omitempty"`
}

func (v ProjectThreadBinding) Validate() error {
	if v.Header.Validate(STRIDEContractProjectThreadBinding) != nil || v.Project.Validate() != nil ||
		v.Project.ContractType != STRIDEContractProject || v.Header.TenantID == STRIDEGlobalPersonTenant ||
		!strideIdentifier(v.ThreadID) || !oneOf(v.Kind, "primary", "related") || !oneOf(v.State, "active", "removed") ||
		v.ThreadAudienceRevision < 1 || !isHexDigest(v.ThreadACLDigest) || !strideIdentifier(v.ActorPersonID) ||
		!strideIdentifier(v.ActorMembershipID) || v.ActorMembershipRevision < 1 || v.BoundAt.IsZero() ||
		v.Header.CreatedAt.After(v.BoundAt) {
		return ErrSTRIDEContractInvalid
	}
	if v.Header.Revision == 1 {
		if v.Supersedes != nil {
			return ErrSTRIDEContractInvalid
		}
	} else if v.Supersedes == nil || v.Supersedes.Validate() != nil ||
		v.Supersedes.ContractType != STRIDEContractProjectThreadBinding || v.Supersedes.ID != v.Header.ID ||
		v.Supersedes.Revision != v.Header.Revision-1 {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ProjectAssociation struct {
	Header                   STRIDEContractHeader `json:"header"`
	Project                  STRIDEReference      `json:"project"`
	Subject                  STRIDEReference      `json:"subject"`
	SourceRefs               []STRIDEReference    `json:"sourceRefs"`
	SourceAuthorityReceiptID string               `json:"sourceAuthorityReceiptId"`
	EvidenceCoverageDigest   string               `json:"evidenceCoverageDigest"`
	State                    string               `json:"state"`
	Basis                    string               `json:"basis"`
	ClassifierRevision       string               `json:"classifierRevision"`
	Confidence               float64              `json:"confidence"`
	ActorPersonID            string               `json:"actorPersonId"`
	ActorMembershipID        string               `json:"actorMembershipId"`
	ActorMembershipRevision  int64                `json:"actorMembershipRevision"`
	SessionSubjectDigest     string               `json:"sessionSubjectDigest"`
	SessionRevision          int64                `json:"sessionRevision"`
	AuthorityGeneration      uint64               `json:"authorityGeneration"`
	SourceAudience           STRIDEAudience       `json:"sourceAudience"`
	SourceACLRevision        int64                `json:"sourceAclRevision"`
	SourceACLDigest          string               `json:"sourceAclDigest"`
	ConsentRevision          int64                `json:"consentRevision"`
	PurgeGeneration          uint64               `json:"purgeGeneration"`
	IdempotencyKeyDigest     string               `json:"idempotencyKeyDigest"`
	ExpiresAt                *time.Time           `json:"expiresAt,omitempty"`
	Supersedes               *STRIDEReference     `json:"supersedes,omitempty"`
	Replacement              *STRIDEReference     `json:"replacement,omitempty"`
	RecordedAt               time.Time            `json:"recordedAt"`
}

func (v ProjectAssociation) Validate() error {
	if v.Header.Validate(STRIDEContractProjectAssociation) != nil || v.Project.Validate() != nil ||
		v.Project.ContractType != STRIDEContractProject || v.Subject.Validate() != nil ||
		!validateSTRIDERefs(v.SourceRefs) || !strideIdentifier(v.SourceAuthorityReceiptID) || !isHexDigest(v.EvidenceCoverageDigest) ||
		!oneOf(v.State, "proposed", "confirmed", "corrected", "removed", "expired", "revoked") ||
		!oneOf(v.Basis, "authoritative_context", "suggested", "selected") ||
		!strideIdentifier(v.ClassifierRevision) || v.Confidence < 0 || v.Confidence > 1 ||
		!strideIdentifier(v.ActorPersonID) || !strideIdentifier(v.ActorMembershipID) || v.ActorMembershipRevision < 1 ||
		!isHexDigest(v.SessionSubjectDigest) || v.SessionRevision < 1 || v.AuthorityGeneration < 1 ||
		v.SourceAudience.Validate() != nil || v.SourceACLRevision < 1 || !isHexDigest(v.SourceACLDigest) ||
		v.ConsentRevision < 1 || v.PurgeGeneration < 1 || !isHexDigest(v.IdempotencyKeyDigest) ||
		v.RecordedAt.IsZero() || v.Header.CreatedAt.After(v.RecordedAt) {
		return ErrSTRIDEContractInvalid
	}
	if v.State == "proposed" {
		if v.ExpiresAt == nil || !v.ExpiresAt.After(v.RecordedAt) || v.Replacement != nil {
			return ErrSTRIDEContractInvalid
		}
	} else if v.ExpiresAt != nil {
		return ErrSTRIDEContractInvalid
	}
	if (v.State == "corrected") != (v.Replacement != nil) || v.Replacement != nil &&
		(v.Replacement.Validate() != nil || v.Replacement.ContractType != STRIDEContractProjectAssociation || v.Replacement.ID == v.Header.ID) {
		return ErrSTRIDEContractInvalid
	}
	if v.Header.Revision == 1 {
		if v.Supersedes != nil {
			return ErrSTRIDEContractInvalid
		}
	} else if v.Supersedes == nil || v.Supersedes.Validate() != nil ||
		v.Supersedes.ContractType != STRIDEContractProjectAssociation || v.Supersedes.ID != v.Header.ID ||
		v.Supersedes.Revision != v.Header.Revision-1 {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

type ProjectAssociationEvent struct {
	Header                  STRIDEContractHeader `json:"header"`
	Association             STRIDEReference      `json:"association"`
	Action                  string               `json:"action"`
	ResultingState          string               `json:"resultingState"`
	PriorRevision           int64                `json:"priorRevision"`
	NewRevision             int64                `json:"newRevision"`
	Replacement             *STRIDEReference     `json:"replacement,omitempty"`
	ActorPersonID           string               `json:"actorPersonId"`
	ActorMembershipID       string               `json:"actorMembershipId"`
	ActorMembershipRevision int64                `json:"actorMembershipRevision"`
	SessionSubjectDigest    string               `json:"sessionSubjectDigest"`
	SessionRevision         int64                `json:"sessionRevision"`
	AuthorityGeneration     uint64               `json:"authorityGeneration"`
	IdempotencyKeyDigest    string               `json:"idempotencyKeyDigest"`
	OccurredAt              time.Time            `json:"occurredAt"`
}

func (v ProjectAssociationEvent) Validate() error {
	if v.Header.Validate(STRIDEContractProjectAssociationEvent) != nil || v.Association.Validate() != nil ||
		v.Association.ContractType != STRIDEContractProjectAssociation ||
		!oneOf(v.Action, "propose", "confirm", "correct", "remove", "expire", "revoke") ||
		!oneOf(v.ResultingState, "proposed", "confirmed", "corrected", "removed", "expired", "revoked") ||
		v.PriorRevision < 0 || v.NewRevision < 1 || v.NewRevision != v.Association.Revision || v.NewRevision < v.PriorRevision ||
		(v.Action == "propose" && (v.PriorRevision != 0 || v.NewRevision != 1)) ||
		(v.Action != "propose" && v.NewRevision != v.PriorRevision+1) ||
		!strideIdentifier(v.ActorPersonID) || !strideIdentifier(v.ActorMembershipID) || v.ActorMembershipRevision < 1 ||
		!isHexDigest(v.SessionSubjectDigest) || v.SessionRevision < 1 || v.AuthorityGeneration < 1 ||
		!isHexDigest(v.IdempotencyKeyDigest) || v.OccurredAt.IsZero() || v.Header.CreatedAt.After(v.OccurredAt) {
		return ErrSTRIDEContractInvalid
	}
	if (v.Action == "correct") != (v.Replacement != nil) || v.Replacement != nil &&
		(v.Replacement.Validate() != nil || v.Replacement.ContractType != STRIDEContractProjectAssociation || v.Replacement.ID == v.Association.ID) {
		return ErrSTRIDEContractInvalid
	}
	if map[string]string{"propose": "proposed", "confirm": "confirmed", "correct": "corrected", "remove": "removed", "expire": "expired", "revoke": "revoked"}[v.Action] != v.ResultingState {
		return ErrSTRIDEContractInvalid
	}
	return nil
}

func validateProjectControllers(values []STRIDEReference, organizationID string) bool {
	if !validateSTRIDERefs(values) {
		return false
	}
	for _, value := range values {
		if value.ContractType != STRIDEContractOrganizationMembership || value.ID == organizationID {
			// Membership identifiers are opaque. The second condition rejects
			// accidentally substituting the organization itself.
			return false
		}
	}
	return true
}

func validProjectAliases(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if !stridePlainText(value, 120, true) || utf8.RuneCountInString(value) > 120 {
			return false
		}
		key := strings.ToLower(value)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
