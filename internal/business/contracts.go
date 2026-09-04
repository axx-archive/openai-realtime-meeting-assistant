// Package business owns the tenant-native Business namespace. It never creates
// legacy goals. Actors must come from server authentication, not request bodies.
package business

import (
	"errors"
	"time"
)

var (
	ErrDenied         = errors.New("business: permission denied")
	ErrConflict       = errors.New("business: revision or idempotency conflict")
	ErrInvalid        = errors.New("business: invalid request")
	ErrNotFound       = errors.New("business: not found")
	ErrBudget         = errors.New("business: insufficient allowance")
	ErrInactive       = errors.New("business: inactive authority")
	ErrConcurrency    = errors.New("business: open work limit reached")
	ErrLease          = errors.New("business: stale or expired lease")
	ErrReconciliation = errors.New("business: reconciliation required")
)

const MaxMoneyMicros int64 = 1_000_000_000_000_000

type Actor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Scope struct {
	OrganizationID string
	Actor          Actor
}
type Organization struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
}
type Business struct {
	ID              string `json:"id"`
	OrganizationID  string `json:"organizationId"`
	Name            string `json:"name"`
	Mission         string `json:"mission"`
	Customer        string `json:"customer"`
	FirstOutcome    string `json:"firstOutcome"`
	Leadership      string `json:"leadership"`
	AuthorityPreset string `json:"authorityPreset"`
	Status          string `json:"status"`
	Revision        int64  `json:"revision"`
}
type Budget struct {
	SettledMicros  int64 `json:"settledMicros"`
	FundedMicros   int64 `json:"fundedMicros"`
	CapMicros      int64 `json:"capMicros"`
	ReservedMicros int64 `json:"reservedMicros"`
	Revision       int64 `json:"revision"`
}
type SetupBusinessArgs struct {
	IdempotencyKey       string `json:"idempotencyKey"`
	OrganizationID       string `json:"organizationId,omitempty"`
	OrganizationName     string `json:"organizationName,omitempty"`
	Name                 string `json:"name"`
	Mission              string `json:"mission"`
	Customer             string `json:"customer"`
	FirstOutcome         string `json:"firstOutcome"`
	Leadership           string `json:"leadership"`
	AuthorityPreset      string `json:"authorityPreset"`
	ModelAllowanceMicros int64  `json:"modelAllowanceMicros"`
}
type SetupBusinessResult struct {
	Organization Organization `json:"organization"`
	Business     Business     `json:"business"`
	Budget       Budget       `json:"budget"`
}
type UpdateBusinessArgs struct {
	IdempotencyKey   string
	BusinessID       string
	ExpectedRevision int64
	Status           string
	Leadership       string
	AuthorityPreset  string
}
type MemberArgs struct {
	IdempotencyKey   string
	PersonID         string
	Role             string
	ExpectedRevision int64
}
type Membership struct {
	ID       string `json:"id"`
	PersonID string `json:"personId"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}
type Employment struct {
	ID              string `json:"id"`
	BusinessID      string `json:"businessId"`
	Name            string `json:"name"`
	OfferingID      string `json:"offeringId"`
	OfferingVersion string `json:"offeringVersion"`
	OfferingDigest  string `json:"offeringDigest"`
	Status          string `json:"status"`
	Revision        int64  `json:"revision"`
}
type EmploymentArgs struct {
	IdempotencyKey  string
	BusinessID      string
	Name            string
	OfferingID      string
	OfferingVersion string
	OfferingDigest  string
}
type Mandate struct {
	ID                string    `json:"id"`
	BusinessID        string    `json:"businessId"`
	EmploymentID      string    `json:"employmentId"`
	IssuerID          string    `json:"issuerId"`
	IssuerRevision    int64     `json:"issuerRevision"`
	Revision          int64     `json:"revision"`
	Status            string    `json:"status"`
	ExpiresAt         time.Time `json:"expiresAt"`
	MaxWorkCostMicros int64     `json:"maxWorkCostMicros"`
	MaxOpenWork       int       `json:"maxOpenWork"`
	MaxAttempts       int       `json:"maxAttempts"`
}
type MandateArgs struct {
	IdempotencyKey    string
	BusinessID        string
	EmploymentID      string
	ExpiresAt         time.Time
	MaxWorkCostMicros int64
	MaxOpenWork       int
	MaxAttempts       int
}
type RevokeMandateArgs struct {
	IdempotencyKey   string
	MandateID        string
	ExpectedRevision int64
}
type BudgetArgs struct {
	IdempotencyKey   string
	BusinessID       string
	ExpectedRevision int64
	AmountMicros     int64
}
type WorkArgs struct {
	IdempotencyKey    string
	BusinessID        string
	EmploymentID      string
	MandateID         string
	MandateRevision   int64
	Objective         string
	OutputContract    string
	ReservationMicros int64
}
type Work struct {
	BusinessRevision   int64     `json:"businessRevision"`
	EmploymentRevision int64     `json:"employmentRevision"`
	CancelRequested    bool      `json:"cancelRequested"`
	HeldMicros         int64     `json:"heldMicros"`
	SettledMicros      int64     `json:"settledMicros"`
	ResultID           string    `json:"resultId,omitempty"`
	ID                 string    `json:"id"`
	BusinessID         string    `json:"businessId"`
	EmploymentID       string    `json:"employmentId"`
	MandateID          string    `json:"mandateId"`
	MandateRevision    int64     `json:"mandateRevision"`
	Actor              Actor     `json:"actor"`
	Objective          string    `json:"objective"`
	OutputContract     string    `json:"outputContract"`
	ReservationMicros  int64     `json:"reservationMicros"`
	MaxAttempts        int       `json:"maxAttempts"`
	Status             string    `json:"status"`
	CreatedAt          time.Time `json:"createdAt"`
}
