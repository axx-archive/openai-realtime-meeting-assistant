package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const (
	projectOperationCreateProject = "create_project"
	projectOperationReviseProject = "revise_project"
	projectOperationBindThread    = "bind_thread"
	projectOperationUnbindThread  = "unbind_thread"
)

func validProjectOperation(operation string) bool {
	switch operation {
	case projectOperationCreateProject, projectOperationReviseProject, projectOperationBindThread, projectOperationUnbindThread:
		return true
	default:
		return false
	}
}

// ResolveProjectOperationReplay is the durable lost-response boundary. An
// exact organization/operation/key/fingerprint replays successfully; the same
// scope with different bytes conflicts; a different operation is independent.
func (store *PostgresCanonicalStore) ResolveProjectOperationReplay(ctx context.Context, organizationID, operation, keyDigest, requestFingerprint string) (bool, error) {
	if store == nil || store.pool == nil || !strideIdentifier(organizationID) || !validProjectOperation(operation) || !isHexDigest(keyDigest) || !isHexDigest(requestFingerprint) {
		return false, ErrProjectAuthorityInvalid
	}
	var storedFingerprint string
	err := store.pool.QueryRow(ctx, `SELECT encode(request_fingerprint,'hex')
FROM stride_project_operation_receipts
WHERE organization_id=$1 AND operation_kind=$2 AND idempotency_key_digest=decode($3,'hex')`, organizationID, operation, keyDigest).Scan(&storedFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if storedFingerprint != requestFingerprint {
		return false, ErrProjectAuthorityConflict
	}
	return true, nil
}
