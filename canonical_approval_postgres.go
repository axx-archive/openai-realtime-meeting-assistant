package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var approvalDispatchNamespace = uuid.MustParse("470ab830-3013-5ddb-9508-60c55fb91bc7")

type PostgresApprovalRepository struct {
	pool      *pgxpool.Pool
	Failpoint func(string) error
}

func NewPostgresApprovalRepository(pool *pgxpool.Pool) *PostgresApprovalRepository {
	return &PostgresApprovalRepository{pool: pool}
}

func (repository *PostgresApprovalRepository) CreateApproval(ctx context.Context, request ApprovalRequest) error {
	if repository == nil || repository.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	approvalID, bindingJSON, actionDigest, executionKey, err := approvalPostgresCreateValues(request)
	if err != nil {
		return err
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `INSERT INTO approvals (
		approval_id,tenant_id,object_type,object_id,action,content_revision,content_sha256,
		action_input_sha256,policy_version,status,required_endorsements,requested_by_type,
		requested_by_id,requested_by_tenant,requested_at,expires_at,consumed_at,execution_key,
		revision,binding,reason,rejected_by,revoked_by
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12,$13,$14,$15,$16,$17,1,$18::jsonb,$19,$20,$21)`,
		approvalID, request.Binding.Target.TenantID, request.Binding.Target.Type, request.Binding.Target.ID,
		string(request.Binding.Action), request.Binding.Revision.ContentRevision, approvalDigestBytes(request.Binding.Revision.ContentDigest),
		actionDigest, request.Binding.Plan.PolicyVersion, string(request.Status), string(request.RequestedBy.Kind), request.RequestedBy.ID,
		request.RequestedBy.TenantID, request.CreatedAt, request.ExpiresAt, request.ConsumedAt, executionKey,
		bindingJSON, request.Reason, request.RejectedBy, request.RevokedBy)
	if err != nil {
		return fmt.Errorf("insert approval: %w", err)
	}
	if err := replacePostgresApprovalEndorsements(ctx, tx, approvalID, request.Endorsements); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (repository *PostgresApprovalRepository) ReadApproval(ctx context.Context, id string) (ApprovalRequest, *ApprovalDispatch, error) {
	if repository == nil || repository.pool == nil {
		return ApprovalRequest{}, nil, ErrCanonicalStoreUnhealthy
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ApprovalRequest{}, nil, ErrApprovalNotFound
	}
	return readPostgresApproval(ctx, repository.pool, parsed, false)
}

func (repository *PostgresApprovalRepository) TransactApproval(ctx context.Context, id string, mutate func(*ApprovalRequest, **ApprovalDispatch) error) error {
	if repository == nil || repository.pool == nil {
		return ErrCanonicalStoreUnhealthy
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ErrApprovalNotFound
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	request, dispatch, err := readPostgresApproval(ctx, tx, parsed, true)
	if err != nil {
		return err
	}
	before := cloneApprovalRequest(request)
	var beforeDispatch *ApprovalDispatch
	if dispatch != nil {
		copyDispatch := *dispatch
		beforeDispatch = &copyDispatch
	}
	mutationErr := mutate(&request, &dispatch)
	if mutationErr != nil {
		// Preserve the service's explicit expiry-revocation behavior while every
		// other callback error remains a full rollback.
		if before.Status != request.Status && request.Status == ApprovalRevoked {
			if err := updatePostgresApproval(ctx, tx, before, request); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
		}
		return mutationErr
	}
	if !approvalStatusTransitionAllowed(before.Status, request.Status) {
		return ErrApprovalState
	}
	if err := updatePostgresApproval(ctx, tx, before, request); err != nil {
		return err
	}
	if err := replacePostgresApprovalEndorsements(ctx, tx, parsed, request.Endorsements); err != nil {
		return err
	}
	if beforeDispatch == nil && dispatch != nil {
		if err := repository.insertApprovalDispatch(ctx, tx, request, *dispatch); err != nil {
			return err
		}
	} else if beforeDispatch != nil && dispatch != nil {
		if !dispatchTransitionAllowed(beforeDispatch.Status, dispatch.Status) {
			return ErrApprovalState
		}
		if err := updatePostgresApprovalDispatch(ctx, tx, *dispatch); err != nil {
			return err
		}
	} else if beforeDispatch != nil && dispatch == nil {
		return ErrApprovalState
	}
	return tx.Commit(ctx)
}

func (repository *PostgresApprovalRepository) insertApprovalDispatch(ctx context.Context, tx pgx.Tx, request ApprovalRequest, dispatch ApprovalDispatch) error {
	if dispatch.Status != DispatchPrepared || dispatch.ApprovalID != request.ID || dispatch.ActionPlanDigest != request.ActionPlanDigest ||
		dispatch.ExecutionKey != stableExecutionKey(request.ID, request.ActionPlanDigest) {
		return ErrApprovalBindingChanged
	}
	executionKey := approvalDigestBytes(dispatch.ExecutionKey)
	actionDigest := approvalDigestBytes(dispatch.ActionPlanDigest)
	if len(executionKey) != 32 || len(actionDigest) != 32 {
		return ErrApprovalInvalid
	}
	approvalID := uuid.MustParse(request.ID)
	if _, err := tx.Exec(ctx, `INSERT INTO execution_receipts (
		execution_key,approval_id,provider,status,attempts,updated_at,action_plan_sha256,created_at,error_text
	) VALUES ($1,$2,$3,'prepared',0,$4,$5,$6,'')`, executionKey, approvalID, request.Binding.Plan.Destination,
		dispatch.UpdatedAt, actionDigest, dispatch.CreatedAt); err != nil {
		return fmt.Errorf("insert approval execution receipt: %w", err)
	}
	if repository.Failpoint != nil {
		if err := repository.Failpoint("after_receipt_before_dispatch"); err != nil {
			return err
		}
	}
	jobID := uuid.NewSHA1(approvalDispatchNamespace, executionKey)
	payload, err := json.Marshal(map[string]any{"approval_id": request.ID, "binding": request.Binding, "execution_key": dispatch.ExecutionKey})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO jobs (
		job_id,tenant_id,kind,status,authority,idempotency_key,execution_key,payload,available_at
	) VALUES ($1,$2,'approval_dispatch','queued','external_write',$3,$4,$5::jsonb,$6)`,
		jobID, request.Binding.Target.TenantID, dispatch.ExecutionKey, executionKey, payload, dispatch.CreatedAt); err != nil {
		return fmt.Errorf("insert approval dispatch job: %w", err)
	}
	outboxPayload, err := json.Marshal(map[string]any{"approval_id": request.ID, "job_id": jobID.String(), "execution_key": dispatch.ExecutionKey})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (approval_id,topic,payload,available_at) VALUES ($1,'approval.dispatch',$2::jsonb,$3)`, approvalID, outboxPayload, dispatch.CreatedAt); err != nil {
		return fmt.Errorf("insert approval dispatch outbox: %w", err)
	}
	return nil
}

func approvalPostgresCreateValues(request ApprovalRequest) (uuid.UUID, []byte, []byte, []byte, error) {
	id, err := uuid.Parse(request.ID)
	if err != nil {
		return uuid.Nil, nil, nil, nil, ErrApprovalInvalid
	}
	digest, err := approvalBindingDigest(request.Binding)
	if err != nil || digest != request.ActionPlanDigest {
		return uuid.Nil, nil, nil, nil, ErrApprovalBindingChanged
	}
	if request.Status != ApprovalPending && request.Status != ApprovalApproved {
		return uuid.Nil, nil, nil, nil, ErrApprovalState
	}
	bindingJSON, err := json.Marshal(request.Binding)
	if err != nil {
		return uuid.Nil, nil, nil, nil, err
	}
	actionDigest := approvalDigestBytes(digest)
	executionKey := approvalDigestBytes(stableExecutionKey(request.ID, digest))
	if len(actionDigest) != 32 || len(executionKey) != 32 {
		return uuid.Nil, nil, nil, nil, ErrApprovalInvalid
	}
	return id, bindingJSON, actionDigest, executionKey, nil
}

type postgresApprovalQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readPostgresApproval(ctx context.Context, querier postgresApprovalQuerier, id uuid.UUID, lock bool) (ApprovalRequest, *ApprovalDispatch, error) {
	query := `SELECT binding,status,requested_by_type,requested_by_id,requested_by_tenant,requested_at,expires_at,
		consumed_at,encode(action_input_sha256,'hex'),reason,rejected_by,revoked_by FROM approvals WHERE approval_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var bindingJSON []byte
	var request ApprovalRequest
	var status string
	var requestedKind string
	var consumedAt *time.Time
	if err := querier.QueryRow(ctx, query, id).Scan(&bindingJSON, &status, &requestedKind, &request.RequestedBy.ID,
		&request.RequestedBy.TenantID, &request.CreatedAt, &request.ExpiresAt, &consumedAt, &request.ActionPlanDigest,
		&request.Reason, &request.RejectedBy, &request.RevokedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ApprovalRequest{}, nil, ErrApprovalNotFound
		}
		return ApprovalRequest{}, nil, err
	}
	if err := json.Unmarshal(bindingJSON, &request.Binding); err != nil {
		return ApprovalRequest{}, nil, err
	}
	request.ID = id.String()
	request.Status = ApprovalStatus(status)
	request.RequestedBy.Kind = ACLPrincipalKind(requestedKind)
	request.ConsumedAt = consumedAt
	request.Endorsements = make(map[string]ApprovalEndorsement)
	rows, err := querier.Query(ctx, `SELECT principal_type,principal_id,principal_tenant,role,decided_at
		FROM approval_endorsements WHERE approval_id=$1 AND decision='approve'`, id)
	if err != nil {
		return ApprovalRequest{}, nil, err
	}
	for rows.Next() {
		var endorsement ApprovalEndorsement
		var kind, role string
		if err := rows.Scan(&kind, &endorsement.Principal.ID, &endorsement.Principal.TenantID, &role, &endorsement.At); err != nil {
			rows.Close()
			return ApprovalRequest{}, nil, err
		}
		endorsement.Principal.Kind = ACLPrincipalKind(kind)
		endorsement.Role = ApprovalEndorserRole(role)
		request.Endorsements[approvalPrincipalKey(endorsement.Principal)] = endorsement
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ApprovalRequest{}, nil, err
	}
	rows.Close()
	var dispatch ApprovalDispatch
	var receiptStatus string
	err = querier.QueryRow(ctx, `SELECT encode(execution_key,'hex'),encode(action_plan_sha256,'hex'),status,created_at,updated_at,
		COALESCE(provider_receipt,''),error_text FROM execution_receipts WHERE approval_id=$1`, id).Scan(
		&dispatch.ExecutionKey, &dispatch.ActionPlanDigest, &receiptStatus, &dispatch.CreatedAt, &dispatch.UpdatedAt,
		&dispatch.ProviderReceipt, &dispatch.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return request, nil, nil
	}
	if err != nil {
		return ApprovalRequest{}, nil, err
	}
	dispatch.ApprovalID = request.ID
	dispatch.Status = DispatchStatus(receiptStatus)
	return request, &dispatch, nil
}

func updatePostgresApproval(ctx context.Context, tx pgx.Tx, before, after ApprovalRequest) error {
	if before.ID != after.ID || before.Binding != after.Binding || before.ActionPlanDigest != after.ActionPlanDigest ||
		!approvalPrincipalEqual(before.RequestedBy, after.RequestedBy) || !before.CreatedAt.Equal(after.CreatedAt) || !before.ExpiresAt.Equal(after.ExpiresAt) {
		return ErrApprovalBindingChanged
	}
	command, err := tx.Exec(ctx, `UPDATE approvals SET status=$2,consumed_at=$3,reason=$4,rejected_by=$5,revoked_by=$6,
		revision=revision+1 WHERE approval_id=$1`, uuid.MustParse(after.ID), string(after.Status), after.ConsumedAt,
		after.Reason, after.RejectedBy, after.RevokedBy)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrApprovalNotFound
	}
	return nil
}

func approvalPrincipalEqual(left, right ACLPrincipal) bool {
	if left.TenantID != right.TenantID || left.ID != right.ID || left.Kind != right.Kind || left.RoomID != right.RoomID || left.SittingID != right.SittingID || len(left.TeamIDs) != len(right.TeamIDs) {
		return false
	}
	for index := range left.TeamIDs {
		if left.TeamIDs[index] != right.TeamIDs[index] {
			return false
		}
	}
	return true
}

func replacePostgresApprovalEndorsements(ctx context.Context, tx pgx.Tx, approvalID uuid.UUID, endorsements map[string]ApprovalEndorsement) error {
	for _, endorsement := range endorsements {
		if !validEndorser(endorsement.Principal, endorsement.Role) {
			return ErrApprovalInvalid
		}
		_, err := tx.Exec(ctx, `INSERT INTO approval_endorsements (
			approval_id,principal_type,principal_id,principal_tenant,decision,decided_at,approval_revision,role
		) VALUES ($1,$2,$3,$4,'approve',$5,1,$6)
		ON CONFLICT (approval_id,principal_type,principal_id) DO UPDATE SET
			principal_tenant=EXCLUDED.principal_tenant,decision='approve',decided_at=EXCLUDED.decided_at,role=EXCLUDED.role`,
			approvalID, string(endorsement.Principal.Kind), endorsement.Principal.ID, endorsement.Principal.TenantID,
			endorsement.At, string(endorsement.Role))
		if err != nil {
			return err
		}
	}
	return nil
}

func updatePostgresApprovalDispatch(ctx context.Context, tx pgx.Tx, dispatch ApprovalDispatch) error {
	command, err := tx.Exec(ctx, `UPDATE execution_receipts SET status=$2,updated_at=$3,provider_receipt=NULLIF($4,''),
		error_text=$5,attempts=attempts+CASE WHEN status='prepared' AND $2='sending' THEN 1 ELSE 0 END
		WHERE approval_id=$1`, uuid.MustParse(dispatch.ApprovalID), string(dispatch.Status), dispatch.UpdatedAt,
		dispatch.ProviderReceipt, dispatch.Error)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrApprovalState
	}
	executionKey := approvalDigestBytes(dispatch.ExecutionKey)
	switch dispatch.Status {
	case DispatchAmbiguous:
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='ambiguous',completed_at=$2
			WHERE execution_key=$1 AND status IN ('queued','claimed')`, executionKey, dispatch.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET delivered_at=COALESCE(delivered_at,$2),last_error_code='ambiguous'
			WHERE approval_id=$1`, uuid.MustParse(dispatch.ApprovalID), dispatch.UpdatedAt); err != nil {
			return err
		}
	case DispatchConfirmed:
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='completed',completed_at=$2
			WHERE execution_key=$1 AND status IN ('queued','claimed')`, executionKey, dispatch.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET delivered_at=COALESCE(delivered_at,$2)
			WHERE approval_id=$1`, uuid.MustParse(dispatch.ApprovalID), dispatch.UpdatedAt); err != nil {
			return err
		}
	case DispatchFailed:
		if _, err := tx.Exec(ctx, `UPDATE jobs SET status='failed',completed_at=$2
			WHERE execution_key=$1 AND status IN ('queued','claimed')`, executionKey, dispatch.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET delivered_at=COALESCE(delivered_at,$2),last_error_code='dispatch_failed'
			WHERE approval_id=$1`, uuid.MustParse(dispatch.ApprovalID), dispatch.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func approvalStatusTransitionAllowed(from, to ApprovalStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case ApprovalPending:
		return to == ApprovalApproved || to == ApprovalRejected || to == ApprovalRevoked
	case ApprovalApproved:
		return to == ApprovalConsumed || to == ApprovalRevoked
	default:
		return false
	}
}

func approvalDigestBytes(value string) []byte {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return decoded
}
