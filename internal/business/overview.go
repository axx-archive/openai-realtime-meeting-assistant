package business

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Overview is a bounded, coherent snapshot under the same organization lock
// used by authority changes and settlement. Unknown liability is not zero cost.
// Migration003 supplies matching indexes and a transactionally maintained read
// projection. Limits bound returned/qualifying rows, not worst-case wall time:
// PostgreSQL may scan tiny tables, and MVCC bloat/statistics still need normal
// vacuum/analyze maintenance. The projection never authorizes spending.
type Overview struct {
	Business              Business
	Membership            Membership
	Budget                Budget
	Team                  []Employment
	Work                  []Work
	TeamMore              bool
	WorkMore              bool
	UnknownCostOperations int64
	UnknownCostMore       bool
}

const overviewTeamQuery = `SELECT body FROM business.employments WHERE organization_id=$1 AND business_id=$2 ORDER BY id LIMIT 101`
const overviewWorkQuery = `SELECT body FROM business.work_intents WHERE organization_id=$1 AND business_id=$2 ORDER BY body->>'createdAt' DESC,id DESC LIMIT 101`
const overviewUnknownCostQuery = `SELECT count(*) FROM (SELECT 1 FROM business.unknown_cost_operations WHERE organization_id=$1 AND business_id=$2 LIMIT 101) bounded`

func (s *Store) Overview(ctx context.Context, scope Scope, bid string) (Overview, error) {
	var out Overview
	err := s.read(ctx, scope, func(tx pgx.Tx) error {
		if err := businessAccess(ctx, tx, scope, bid); err != nil {
			return err
		}
		if err := body(ctx, tx, "businesses", scope.OrganizationID, bid, &out.Business); err != nil {
			return err
		}
		var err error
		if out.Membership, err = member(ctx, tx, scope, false); err != nil {
			return err
		}
		if out.Budget, err = budget(ctx, tx, scope.OrganizationID, bid); err != nil {
			return err
		}
		if out.Team, out.TeamMore, err = overviewBodies[Employment](ctx, tx, overviewTeamQuery, scope.OrganizationID, bid); err != nil {
			return err
		}
		if out.Work, out.WorkMore, err = overviewBodies[Work](ctx, tx, overviewWorkQuery, scope.OrganizationID, bid); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, overviewUnknownCostQuery, scope.OrganizationID, bid).Scan(&out.UnknownCostOperations); err != nil {
			return err
		}
		out.UnknownCostMore = out.UnknownCostOperations > 100
		if out.UnknownCostMore {
			out.UnknownCostOperations = 100
		}
		return nil
	})
	if err != nil {
		return Overview{}, err
	}
	return out, nil
}

// query is a compile-time statement supplied only by this file.
func overviewBodies[T any](ctx context.Context, tx pgx.Tx, query, org, key string) ([]T, bool, error) {
	out := []T{}
	rows, err := tx.Query(ctx, query, org, key)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item T
		if err = rows.Scan(&raw); err != nil {
			return nil, false, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, false, err
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > 100
	if more {
		out = out[:100]
	}
	return out, more, nil
}

type WorkDetail struct {
	Business   Business   `json:"business"`
	Work       Work       `json:"work"`
	Employment Employment `json:"employment"`
	Attempts   []Attempt  `json:"attempts"`
	Result     *Result    `json:"result"`
}

// ReadWorkDetail binds URL business, Work, attempts and result in one current
// authority snapshot. Result content remains a private record even if its
// eligibility for subsequent execution has been revoked.
func (s *Store) ReadWorkDetail(ctx context.Context, scope Scope, bid, wid string) (WorkDetail, error) {
	var out WorkDetail
	err := s.read(ctx, scope, func(tx pgx.Tx) error {
		if err := businessAccess(ctx, tx, scope, bid); err != nil {
			return err
		}
		if err := body(ctx, tx, "businesses", scope.OrganizationID, bid, &out.Business); err != nil {
			return err
		}
		if err := body(ctx, tx, "work_intents", scope.OrganizationID, wid, &out.Work); err != nil {
			return err
		}
		if out.Work.BusinessID != bid {
			return ErrNotFound
		}
		if err := body(ctx, tx, "employments", scope.OrganizationID, out.Work.EmploymentID, &out.Employment); err != nil {
			return err
		}
		var err error
		var more bool
		out.Attempts, more, err = overviewBodies[Attempt](ctx, tx, `SELECT body FROM business.attempts WHERE organization_id=$1 AND work_id=$2 ORDER BY ordinal LIMIT 101`, scope.OrganizationID, wid)
		if err != nil {
			return err
		}
		if more {
			return ErrInvalid
		} // Admission caps attempts at ten; never truncate lineage.
		if out.Work.ResultID != "" {
			result := Result{}
			if err = body(ctx, tx, "results", scope.OrganizationID, out.Work.ResultID, &result); err != nil {
				return err
			}
			if result.WorkID != wid {
				return ErrInvalid
			}
			if result.Eligible {
				if err = currentWorkAuthority(ctx, tx, scope.OrganizationID, out.Work); err != nil {
					result.Eligible = false
					result.IneligibleReason = "authority_changed"
				}
			}
			out.Result = &result
		}
		return nil
	})
	if err != nil {
		return WorkDetail{}, err
	}
	return out, nil
}
