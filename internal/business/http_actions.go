package business

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// BusinessAction hashes the original HTTP intent. Deriving omitted fields before
// idempotency lookup would make a lost-ack retry conflict with its own mutation.
type BusinessAction struct {
	IdempotencyKey   string `json:"idempotencyKey"`
	BusinessID       string `json:"-"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Action           string `json:"action"`
	Leadership       string `json:"leadership,omitempty"`
	AuthorityPreset  string `json:"authorityPreset,omitempty"`
}

func (s *Store) UpdateBusinessAction(ctx context.Context, scope Scope, in BusinessAction) (Business, error) {
	// BusinessID must be part of the command digest despite being path-bound on
	// HTTP. The anonymous value has no JSON omission tag.
	digestInput := struct {
		BusinessID string
		Intent     BusinessAction
	}{in.BusinessID, in}
	return command(ctx, s, scope, in.IdempotencyKey, "business_action", digestInput, true, func(tx pgx.Tx) (Business, error) {
		var b Business
		if err := body(ctx, tx, "businesses", scope.OrganizationID, in.BusinessID, &b); err != nil {
			return b, err
		}
		if b.Revision != in.ExpectedRevision {
			return b, ErrConflict
		}
		switch in.Action {
		case "update_policy":
			if !leadership(in.Leadership) || !preset(in.AuthorityPreset) || b.Status == "closed" {
				return b, ErrInvalid
			}
			b.Leadership, b.AuthorityPreset = in.Leadership, in.AuthorityPreset
		case "pause":
			if in.Leadership != "" || in.AuthorityPreset != "" || b.Status != "active" {
				return b, ErrInvalid
			}
			b.Status = "paused"
		case "resume":
			if in.Leadership != "" || in.AuthorityPreset != "" || b.Status != "paused" {
				return b, ErrInvalid
			}
			b.Status = "active"
		default:
			return b, ErrInvalid
		}
		b.Revision++
		if err := saveBody(ctx, tx, "businesses", scope.OrganizationID, b.ID, b); err != nil {
			return b, err
		}
		if err := cancelWork(ctx, tx, scope.OrganizationID, func(w Work) bool { return w.BusinessID == b.ID }, "business_policy_changed"); err != nil {
			return b, err
		}
		return b, nil
	})
}
