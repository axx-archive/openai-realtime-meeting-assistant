package business

import "time"

// Worker identity comes from the server execution adapter, not an agent prompt.
// A lease never authorizes a provider call by itself.
type ClaimAttemptArgs struct {
	WorkID         string
	WorkerID       string
	IdempotencyKey string
	LeaseSeconds   int
}
type AttemptLease struct {
	AttemptID  string
	WorkerID   string
	Generation int64
}
type Attempt struct {
	OutcomeEvidenceRef string     `json:"outcomeEvidenceRef,omitempty"`
	ID                 string     `json:"id"`
	WorkID             string     `json:"workId"`
	Ordinal            int        `json:"ordinal"`
	WorkerID           string     `json:"workerId"`
	Generation         int64      `json:"generation"`
	LeaseExpiresAt     time.Time  `json:"leaseExpiresAt"`
	ClaimKey           string     `json:"claimKey"`
	State              string     `json:"state"`
	Mode               string     `json:"mode"`
	Operation          *Operation `json:"operation,omitempty"`
	Outcome            string     `json:"outcome,omitempty"`
	CostState          string     `json:"costState"`
	ResultID           string     `json:"resultId,omitempty"`
}

// Preparing is a durable "may have issued" marker. OperationID is the exact
// adapter idempotency/reconciliation key, not evidence that a provider accepted.
type Operation struct {
	ID                string `json:"id"`
	RequestDigest     string `json:"requestDigest"`
	AdapterID         string `json:"adapterId"`
	RouteRevision     string `json:"routeRevision"`
	PriceRevision     string `json:"priceRevision"`
	MaximumCostMicros int64  `json:"maximumCostMicros"`
}
type PrepareOperationArgs struct {
	Lease     AttemptLease
	Operation Operation
}
type CostEvidence struct {
	ActualMicros *int64 `json:"actualMicros"`
	EvidenceRef  string `json:"evidenceRef"`
}
type Result struct {
	ID               string    `json:"id"`
	WorkID           string    `json:"workId"`
	AttemptID        string    `json:"attemptId"`
	OperationID      string    `json:"operationId"`
	Generation       int64     `json:"generation"`
	Content          string    `json:"content"`
	Digest           string    `json:"digest"`
	ContentType      string    `json:"contentType"`
	Eligible         bool      `json:"eligible"`
	IneligibleReason string    `json:"ineligibleReason,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// Outcome is succeeded, failed, or not_accepted. A result is only accepted for
// succeeded. ActualMicros nil is unknown, never an implicit zero.
type CompleteAttemptArgs struct {
	Lease              AttemptLease
	OperationID        string
	Outcome            string
	Content            string
	ContentDigest      string
	Cost               CostEvidence
	OutcomeEvidenceRef string
}
type ReconcileAttemptArgs struct {
	Lease              AttemptLease
	OperationID        string
	Outcome            string
	Content            string
	ContentDigest      string
	Cost               CostEvidence
	OutcomeEvidenceRef string
}
type AttemptCompletion struct {
	Attempt Attempt `json:"attempt"`
	Work    Work    `json:"work"`
	Result  *Result `json:"result,omitempty"`
}
