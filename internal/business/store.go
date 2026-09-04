package business

import (
	"context"
	"crypto/sha256"
	"embed"
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

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct{ pool *pgxpool.Pool }

// Migrate uses a separate administrative connection. Never pass this connection
// to New. The fixed directory function requires a BYPASSRLS migration owner.
func Migrate(ctx context.Context, admin *pgxpool.Pool) error {
	tx, e := admin.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var privileged bool
	if e = tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&privileged); e != nil {
		return e
	}
	if !privileged {
		return fmt.Errorf("business migration needs a separate RLS-bypassing administrator")
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(728541003); CREATE SCHEMA IF NOT EXISTS business; CREATE TABLE IF NOT EXISTS business.schema_migrations(version text PRIMARY KEY,digest text NOT NULL)`); e != nil {
		return e
	}
	entries, e := migrationFiles.ReadDir("migrations")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		version := strings.SplitN(name, "_", 2)[0]
		data, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		var prior string
		e = tx.QueryRow(ctx, `SELECT digest FROM business.schema_migrations WHERE version=$1`, version).Scan(&prior)
		if e == nil {
			if prior != digest {
				return ErrConflict
			}
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if _, e = tx.Exec(ctx, string(data)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO business.schema_migrations VALUES($1,$2)`, version, digest); e != nil {
			return e
		}
	}

	return tx.Commit(ctx)
}
func New(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	var unsafe bool
	e := pool.QueryRow(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR EXISTS(SELECT 1 FROM pg_roles elevated WHERE (elevated.rolsuper OR elevated.rolbypassrls) AND pg_has_role(current_user,elevated.oid,'MEMBER')) OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname='business' AND pg_has_role(current_user,n.nspowner,'MEMBER')) OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='business' AND pg_has_role(current_user,c.relowner,'MEMBER')) FROM pg_roles r WHERE r.rolname=current_user`).Scan(&unsafe)
	if e != nil {
		return nil, e
	}
	if unsafe {
		return nil, fmt.Errorf("business runtime must not own tables, bypass RLS, or inherit their owner")
	}
	entries, e := migrationFiles.ReadDir("migrations")
	if e != nil {
		return nil, e
	}
	expected := map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, e := migrationFiles.ReadFile("migrations/" + entry.Name())
		if e != nil {
			return nil, e
		}
		h := sha256.Sum256(raw)
		expected[strings.SplitN(entry.Name(), "_", 2)[0]] = hex.EncodeToString(h[:])
	}
	rows, e := pool.Query(ctx, `SELECT version,digest FROM business.schema_migrations`)
	if e != nil {
		return nil, fmt.Errorf("business schema is not ready: %w", e)
	}
	defer rows.Close()
	for rows.Next() {
		var version, digest string
		if e = rows.Scan(&version, &digest); e != nil {
			return nil, e
		}
		if expected[version] != digest {
			return nil, fmt.Errorf("business schema migration mismatch: %s", version)
		}
		delete(expected, version)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(expected) > 0 {
		return nil, fmt.Errorf("business schema has unapplied migrations")
	}
	return &Store{pool}, nil
}
func validActor(a Actor) bool {
	return (a.Kind == "person" || a.Kind == "agent") && len(a.ID) > 0 && len(a.ID) <= 200
}
func validText(s string, max int) bool { return strings.TrimSpace(s) != "" && len(s) <= max }
func money(v int64) bool               { return v >= 0 && v <= MaxMoneyMicros }
func preset(v string) bool {
	return v == "advise" || v == "execute_assigned" || v == "take_initiative" || v == "full_autonomy"
}
func leadership(v string) bool { return v == "human_ceo" || v == "agent_ceo" }
func status(v string) bool     { return v == "draft" || v == "active" || v == "paused" || v == "closed" }
func id(kind string) string    { return kind + "_" + uuid.NewString() }
func jsonBytes(v any) []byte   { b, _ := json.Marshal(v); return b }
func scopeTx(ctx context.Context, p *pgxpool.Pool, s Scope) (pgx.Tx, error) {
	if !validActor(s.Actor) || !validText(s.OrganizationID, 200) {
		return nil, ErrInvalid
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('business.organization_id',$1,true)`, s.OrganizationID); e != nil {
		tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}
func lockOrg(ctx context.Context, tx pgx.Tx, s Scope) error {
	var key string
	e := tx.QueryRow(ctx, `SELECT id FROM business.organizations WHERE id=$1 FOR UPDATE`, s.OrganizationID).Scan(&key)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return e
}
func member(ctx context.Context, tx pgx.Tx, s Scope, owner bool) (Membership, error) {
	var m Membership
	if s.Actor.Kind != "person" {
		return m, ErrDenied
	}
	e := tx.QueryRow(ctx, `SELECT id,person_id,role,status,revision FROM business.memberships WHERE organization_id=$1 AND person_id=$2`, s.OrganizationID, s.Actor.ID).Scan(&m.ID, &m.PersonID, &m.Role, &m.Status, &m.Revision)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && (m.Status != "active" || owner && m.Role != "owner") {
		return m, ErrDenied
	}
	return m, e
}
func actorCurrent(ctx context.Context, tx pgx.Tx, s Scope, owner bool) error {
	if s.Actor.Kind == "person" {
		_, e := member(ctx, tx, s, owner)
		return e
	}
	if owner {
		return ErrDenied
	}
	var emp Employment
	if e := body(ctx, tx, "employments", s.OrganizationID, s.Actor.ID, &emp); e != nil {
		return ErrDenied
	}
	if emp.Status != "active" {
		return ErrDenied
	}
	return nil
}

// Table names are compile-time constants at every callsite, never user input.
func body(ctx context.Context, tx pgx.Tx, table, org, key string, out any) error {
	var b []byte
	e := tx.QueryRow(ctx, `SELECT body FROM business.`+table+` WHERE organization_id=$1 AND id=$2`, org, key).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func saveBody(ctx context.Context, tx pgx.Tx, table, org, key string, v any) error {
	_, e := tx.Exec(ctx, `UPDATE business.`+table+` SET body=$3 WHERE organization_id=$1 AND id=$2`, org, key, jsonBytes(v))
	return e
}
func event(ctx context.Context, tx pgx.Tx, org, kind, key string, v any) error {
	_, e := tx.Exec(ctx, `INSERT INTO business.events(organization_id,kind,entity_id,body) VALUES($1,$2,$3,$4)`, org, kind, key, jsonBytes(v))
	return e
}
func command[T any](ctx context.Context, s *Store, scope Scope, key, kind string, args any, owner bool, fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	if !validText(key, 200) {
		return zero, ErrInvalid
	}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return zero, e
	}
	if e = actorCurrent(ctx, tx, scope, owner); e != nil {
		return zero, e
	}
	result, found, e := replay[T](ctx, tx, scope, key, kind, args)
	if e != nil {
		return zero, e
	}
	if found {
		return result, tx.Commit(ctx)
	}
	result, e = fn(tx)
	if e != nil {
		return zero, e
	}
	if e = receipt(ctx, tx, scope, key, kind, args, result); e != nil {
		return zero, e
	}
	return result, tx.Commit(ctx)
}
func digest(scope Scope, kind string, args any) string {
	b := jsonBytes(struct {
		Actor Actor
		Kind  string
		Args  any
	}{scope.Actor, kind, args})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func replay[T any](ctx context.Context, tx pgx.Tx, s Scope, key, kind string, args any) (T, bool, error) {
	var v T
	var d string
	var b []byte
	e := tx.QueryRow(ctx, `SELECT digest,result FROM business.operations WHERE organization_id=$1 AND idempotency_key=$2`, s.OrganizationID, key).Scan(&d, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, false, nil
	}
	if e != nil {
		return v, false, e
	}
	if d != digest(s, kind, args) {
		return v, false, ErrConflict
	}
	e = json.Unmarshal(b, &v)
	return v, true, e
}
func receipt(ctx context.Context, tx pgx.Tx, s Scope, key, kind string, args, result any) error {
	_, e := tx.Exec(ctx, `INSERT INTO business.operations VALUES($1,$2,$3,$4,$5,$6)`, s.OrganizationID, key, s.Actor.Kind, s.Actor.ID, digest(s, kind, args), jsonBytes(result))
	if e != nil {
		return e
	}
	return event(ctx, tx, s.OrganizationID, kind, key, struct {
		Actor  Actor `json:"actor"`
		Result any   `json:"result"`
	}{s.Actor, result})
}

func (s *Store) SetupBusiness(ctx context.Context, a Actor, in SetupBusinessArgs) (SetupBusinessResult, error) {
	var out SetupBusinessResult
	if a.Kind != "person" || !validActor(a) || !validText(in.IdempotencyKey, 200) || !validText(in.Name, 200) || !validText(in.Mission, 10000) || !validText(in.Customer, 2000) || !validText(in.FirstOutcome, 10000) || !leadership(in.Leadership) || !preset(in.AuthorityPreset) || !money(in.ModelAllowanceMicros) {
		return out, ErrInvalid
	}
	org := in.OrganizationID
	fresh := org == ""
	if fresh {
		if !validText(in.OrganizationName, 200) {
			return out, ErrInvalid
		}
		org = "org_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(a.ID+"\x00"+in.IdempotencyKey)).String()
	}
	scope := Scope{org, a}
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if fresh {
		tag, e := tx.Exec(ctx, `INSERT INTO business.organizations(id,name) VALUES($1,$2) ON CONFLICT DO NOTHING`, org, in.OrganizationName)
		if e != nil {
			return out, e
		}
		if tag.RowsAffected() == 1 {
			_, e = tx.Exec(ctx, `INSERT INTO business.memberships VALUES($1,$2,$3,'owner','active',1)`, org, id("member"), a.ID)
			if e != nil {
				return out, e
			}
		}
	}
	if e = lockOrg(ctx, tx, scope); e != nil {
		return out, e
	}
	if _, e = member(ctx, tx, scope, true); e != nil {
		return out, e
	}
	prior, found, e := replay[SetupBusinessResult](ctx, tx, scope, in.IdempotencyKey, "setup_business", in)
	if e != nil {
		return out, e
	}
	if found {
		return prior, tx.Commit(ctx)
	}
	e = tx.QueryRow(ctx, `SELECT id,name,revision FROM business.organizations WHERE id=$1`, org).Scan(&out.Organization.ID, &out.Organization.Name, &out.Organization.Revision)
	if e != nil {
		return out, e
	}
	out.Business = Business{id("biz"), org, in.Name, in.Mission, in.Customer, in.FirstOutcome, in.Leadership, in.AuthorityPreset, "draft", 1}
	out.Budget = Budget{FundedMicros: in.ModelAllowanceMicros, CapMicros: in.ModelAllowanceMicros, Revision: 1}
	if _, e = tx.Exec(ctx, `INSERT INTO business.businesses VALUES($1,$2,$3)`, org, out.Business.ID, jsonBytes(out.Business)); e != nil {
		return out, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO business.budgets(organization_id,business_id,funded_micros,cap_micros) VALUES($1,$2,$3,$3)`, org, out.Business.ID, in.ModelAllowanceMicros); e != nil {
		return out, e
	}
	if e = receipt(ctx, tx, scope, in.IdempotencyKey, "setup_business", in, out); e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
func (s *Store) ListOrganizations(ctx context.Context, a Actor) ([]Organization, error) {
	if a.Kind != "person" || !validActor(a) {
		return nil, ErrDenied
	}
	rows, e := s.pool.Query(ctx, `SELECT id,name,revision FROM business.organizations_for_person($1)`, a.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Organization{}
	for rows.Next() {
		var o Organization
		if e = rows.Scan(&o.ID, &o.Name, &o.Revision); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) read(ctx context.Context, scope Scope, fn func(pgx.Tx) error) error {
	tx, e := scopeTx(ctx, s.pool, scope)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockOrg(ctx, tx, scope); e != nil {
		return e
	}
	if e = actorCurrent(ctx, tx, scope, false); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func businessAccess(ctx context.Context, tx pgx.Tx, s Scope, bid string) error {
	if s.Actor.Kind == "person" {
		return nil
	}
	var emp Employment
	if e := body(ctx, tx, "employments", s.OrganizationID, s.Actor.ID, &emp); e != nil {
		return e
	}
	if emp.BusinessID != bid {
		return ErrDenied
	}
	return nil
}
func (s *Store) GetBusiness(ctx context.Context, scope Scope, key string) (Business, error) {
	var out Business
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		if e := businessAccess(ctx, tx, scope, key); e != nil {
			return e
		}
		return body(ctx, tx, "businesses", scope.OrganizationID, key, &out)
	})
	return out, e
}
func (s *Store) ListBusinesses(ctx context.Context, scope Scope) ([]Business, error) {
	out := []Business{}
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT body FROM business.businesses WHERE organization_id=$1 ORDER BY id`, scope.OrganizationID)
		if e != nil {
			return e
		}
		var all []Business
		for rows.Next() {
			var raw []byte
			var b Business
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			if e = json.Unmarshal(raw, &b); e != nil {
				rows.Close()
				return e
			}
			all = append(all, b)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, b := range all {
			if businessAccess(ctx, tx, scope, b.ID) == nil {
				out = append(out, b)
			}
		}
		return nil
	})
	return out, e
}
func budget(ctx context.Context, tx pgx.Tx, org, bid string) (Budget, error) {
	var b Budget
	e := tx.QueryRow(ctx, `SELECT funded_micros,cap_micros,reserved_micros,revision,settled_micros FROM business.budgets WHERE organization_id=$1 AND business_id=$2`, org, bid).Scan(&b.FundedMicros, &b.CapMicros, &b.ReservedMicros, &b.Revision, &b.SettledMicros)
	if errors.Is(e, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, e
}
func (s *Store) GetBudget(ctx context.Context, scope Scope, bid string) (Budget, error) {
	var b Budget
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		if e := businessAccess(ctx, tx, scope, bid); e != nil {
			return e
		}
		var e error
		b, e = budget(ctx, tx, scope.OrganizationID, bid)
		return e
	})
	return b, e
}

func (s *Store) UpdateBusiness(ctx context.Context, scope Scope, in UpdateBusinessArgs) (Business, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "update_business", in, true, func(tx pgx.Tx) (Business, error) {
		var b Business
		if !status(in.Status) || !leadership(in.Leadership) || !preset(in.AuthorityPreset) {
			return b, ErrInvalid
		}
		if e := body(ctx, tx, "businesses", scope.OrganizationID, in.BusinessID, &b); e != nil {
			return b, e
		}
		if b.Revision != in.ExpectedRevision {
			return b, ErrConflict
		}
		b.Status = in.Status
		b.Leadership = in.Leadership
		b.AuthorityPreset = in.AuthorityPreset
		b.Revision++
		if e := saveBody(ctx, tx, "businesses", scope.OrganizationID, b.ID, b); e != nil {
			return b, e
		}
		if e := cancelWork(ctx, tx, scope.OrganizationID, func(w Work) bool { return w.BusinessID == b.ID }, "business_policy_changed"); e != nil {
			return b, e
		}
		return b, nil
	})
}
func (s *Store) AddMember(ctx context.Context, scope Scope, in MemberArgs) (Membership, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "add_member", in, true, func(tx pgx.Tx) (Membership, error) {
		var m Membership
		if !validText(in.PersonID, 200) || (in.Role != "owner" && in.Role != "member") {
			return m, ErrInvalid
		}
		e := tx.QueryRow(ctx, `SELECT id,person_id,role,status,revision FROM business.memberships WHERE organization_id=$1 AND person_id=$2`, scope.OrganizationID, in.PersonID).Scan(&m.ID, &m.PersonID, &m.Role, &m.Status, &m.Revision)
		if e == nil {
			if in.ExpectedRevision != m.Revision {
				return m, ErrConflict
			}
			if m.Role == "owner" && m.Status == "active" && in.Role != "owner" {
				if e = anotherOwner(ctx, tx, scope.OrganizationID, m.ID); e != nil {
					return m, e
				}
			}
			m.Role = in.Role
			m.Status = "active"
			m.Revision++
			_, e = tx.Exec(ctx, `UPDATE business.memberships SET role=$3,status=$4,revision=$5 WHERE organization_id=$1 AND id=$2`, scope.OrganizationID, m.ID, m.Role, m.Status, m.Revision)
			if e != nil {
				return m, e
			}
			if e = cancelIssuerWork(ctx, tx, scope.OrganizationID, m.ID); e != nil {
				return m, e
			}
			return m, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return m, e
		}
		if in.ExpectedRevision != 0 {
			return m, ErrConflict
		}
		m = Membership{id("member"), in.PersonID, in.Role, "active", 1}
		_, e = tx.Exec(ctx, `INSERT INTO business.memberships VALUES($1,$2,$3,$4,$5,$6)`, scope.OrganizationID, m.ID, m.PersonID, m.Role, m.Status, m.Revision)
		return m, e
	})
}
func anotherOwner(ctx context.Context, tx pgx.Tx, org, exclude string) error {
	var n int
	e := tx.QueryRow(ctx, `SELECT count(*) FROM business.memberships WHERE organization_id=$1 AND id<>$2 AND role='owner' AND status='active'`, org, exclude).Scan(&n)
	if e != nil {
		return e
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}
func (s *Store) RevokeMembership(ctx context.Context, scope Scope, in MemberArgs) (Membership, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "revoke_membership", in, true, func(tx pgx.Tx) (Membership, error) {
		var m Membership
		e := tx.QueryRow(ctx, `SELECT id,person_id,role,status,revision FROM business.memberships WHERE organization_id=$1 AND person_id=$2`, scope.OrganizationID, in.PersonID).Scan(&m.ID, &m.PersonID, &m.Role, &m.Status, &m.Revision)
		if errors.Is(e, pgx.ErrNoRows) {
			return m, ErrNotFound
		}
		if e != nil {
			return m, e
		}
		if m.Revision != in.ExpectedRevision {
			return m, ErrConflict
		}
		if m.Role == "owner" && m.Status == "active" {
			if e = anotherOwner(ctx, tx, scope.OrganizationID, m.ID); e != nil {
				return m, e
			}
		}
		m.Status = "revoked"
		m.Revision++
		if _, e = tx.Exec(ctx, `UPDATE business.memberships SET status='revoked',revision=$3 WHERE organization_id=$1 AND id=$2`, scope.OrganizationID, m.ID, m.Revision); e != nil {
			return m, e
		}
		if e = cancelIssuerWork(ctx, tx, scope.OrganizationID, m.ID); e != nil {
			return m, e
		}
		e = cancelWork(ctx, tx, scope.OrganizationID, func(w Work) bool { return w.Actor.Kind == "person" && w.Actor.ID == m.PersonID }, "membership_revoked")
		return m, e
	})
}
func (s *Store) CreateEmployment(ctx context.Context, scope Scope, in EmploymentArgs) (Employment, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "create_employment", in, true, func(tx pgx.Tx) (Employment, error) {
		var e Employment
		var b Business
		if !validText(in.Name, 200) || !validText(in.OfferingID, 200) || !validText(in.OfferingVersion, 200) || !validText(in.OfferingDigest, 200) {
			return e, ErrInvalid
		}
		if err := body(ctx, tx, "businesses", scope.OrganizationID, in.BusinessID, &b); err != nil {
			return e, err
		}
		e = Employment{id("employment"), b.ID, in.Name, in.OfferingID, in.OfferingVersion, in.OfferingDigest, "active", 1}
		_, err := tx.Exec(ctx, `INSERT INTO business.employments VALUES($1,$2,$3,$4)`, scope.OrganizationID, e.ID, e.BusinessID, jsonBytes(e))
		return e, err
	})
}
func (s *Store) GrantMandate(ctx context.Context, scope Scope, in MandateArgs) (Mandate, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "grant_mandate", in, true, func(tx pgx.Tx) (Mandate, error) {
		var m Mandate
		now, e := databaseNow(ctx, tx)
		if e != nil {
			return m, e
		}
		if !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(366*24*time.Hour)) || !money(in.MaxWorkCostMicros) || in.MaxOpenWork < 1 || in.MaxOpenWork > 100 || in.MaxAttempts < 1 || in.MaxAttempts > 10 {
			return m, ErrInvalid
		}
		var emp Employment
		if e = body(ctx, tx, "employments", scope.OrganizationID, in.EmploymentID, &emp); e != nil {
			return m, e
		}
		if emp.BusinessID != in.BusinessID || emp.Status != "active" {
			return m, ErrDenied
		}
		issuer, e := member(ctx, tx, scope, true)
		if e != nil {
			return m, e
		}
		m = Mandate{id("mandate"), in.BusinessID, emp.ID, issuer.ID, issuer.Revision, 1, "active", in.ExpiresAt.UTC(), in.MaxWorkCostMicros, in.MaxOpenWork, in.MaxAttempts}
		_, e = tx.Exec(ctx, `INSERT INTO business.mandates VALUES($1,$2,$3,$4,$5)`, scope.OrganizationID, m.ID, m.BusinessID, m.EmploymentID, jsonBytes(m))
		return m, e
	})
}
func (s *Store) RevokeMandate(ctx context.Context, scope Scope, in RevokeMandateArgs) (Mandate, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "revoke_mandate", in, true, func(tx pgx.Tx) (Mandate, error) {
		var m Mandate
		if e := body(ctx, tx, "mandates", scope.OrganizationID, in.MandateID, &m); e != nil {
			return m, e
		}
		if m.Revision != in.ExpectedRevision {
			return m, ErrConflict
		}
		m.Status = "revoked"
		m.Revision++
		if e := saveBody(ctx, tx, "mandates", scope.OrganizationID, m.ID, m); e != nil {
			return m, e
		}
		e := cancelWork(ctx, tx, scope.OrganizationID, func(w Work) bool { return w.MandateID == m.ID }, "mandate_revoked")
		return m, e
	})
}
func (s *Store) FundBudget(ctx context.Context, scope Scope, in BudgetArgs) (Budget, error) {
	return s.changeBudget(ctx, scope, in, false)
}
func (s *Store) SetBudgetCap(ctx context.Context, scope Scope, in BudgetArgs) (Budget, error) {
	return s.changeBudget(ctx, scope, in, true)
}
func (s *Store) changeBudget(ctx context.Context, scope Scope, in BudgetArgs, cap bool) (Budget, error) {
	kind := "fund_budget"
	if cap {
		kind = "set_budget_cap"
	}
	return command(ctx, s, scope, in.IdempotencyKey, kind, in, true, func(tx pgx.Tx) (Budget, error) {
		b, e := budget(ctx, tx, scope.OrganizationID, in.BusinessID)
		if e != nil {
			return b, e
		}
		if !money(in.AmountMicros) {
			return b, ErrInvalid
		}
		if b.Revision != in.ExpectedRevision {
			return b, ErrConflict
		}
		if cap {
			if in.AmountMicros < b.ReservedMicros+b.SettledMicros {
				return b, ErrBudget
			}
			b.CapMicros = in.AmountMicros
		} else {
			if in.AmountMicros > MaxMoneyMicros-b.FundedMicros {
				return b, ErrInvalid
			}
			b.FundedMicros += in.AmountMicros
		}
		b.Revision++
		_, e = tx.Exec(ctx, `UPDATE business.budgets SET funded_micros=$3,cap_micros=$4,revision=$5 WHERE organization_id=$1 AND business_id=$2`, scope.OrganizationID, in.BusinessID, b.FundedMicros, b.CapMicros, b.Revision)
		return b, e
	})
}
func databaseNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var t time.Time
	e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t)
	return t, e
}

func (s *Store) AdmitWork(ctx context.Context, scope Scope, in WorkArgs) (Work, error) {
	return command(ctx, s, scope, in.IdempotencyKey, "admit_private_work", in, false, func(tx pgx.Tx) (Work, error) {
		var w Work
		if !validText(in.Objective, 10000) || in.OutputContract != "private_document_v1" || !money(in.ReservationMicros) {
			return w, ErrInvalid
		}
		var b Business
		if e := body(ctx, tx, "businesses", scope.OrganizationID, in.BusinessID, &b); e != nil {
			return w, e
		}
		if b.Status != "active" {
			return w, ErrInactive
		}
		if scope.Actor.Kind == "person" {
			if _, e := member(ctx, tx, scope, true); e != nil {
				return w, e
			}
		} else if scope.Actor.ID != in.EmploymentID || (b.AuthorityPreset != "take_initiative" && b.AuthorityPreset != "full_autonomy") {
			return w, ErrDenied
		}
		var emp Employment
		if e := body(ctx, tx, "employments", scope.OrganizationID, in.EmploymentID, &emp); e != nil {
			return w, e
		}
		if emp.Status != "active" || emp.BusinessID != b.ID {
			return w, ErrDenied
		}
		var m Mandate
		if e := body(ctx, tx, "mandates", scope.OrganizationID, in.MandateID, &m); e != nil {
			return w, e
		}
		now, e := databaseNow(ctx, tx)
		if e != nil {
			return w, e
		}
		if m.BusinessID != b.ID || m.EmploymentID != emp.ID {
			return w, ErrDenied
		}
		if m.Revision != in.MandateRevision {
			return w, ErrConflict
		}
		if m.Status != "active" || !m.ExpiresAt.After(now) {
			return w, ErrInactive
		}
		var issuerValid bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business.memberships WHERE organization_id=$1 AND id=$2 AND revision=$3 AND status='active' AND role='owner')`, scope.OrganizationID, m.IssuerID, m.IssuerRevision).Scan(&issuerValid)
		if e != nil {
			return w, e
		}
		if !issuerValid {
			return w, ErrInactive
		}
		if in.ReservationMicros > m.MaxWorkCostMicros {
			return w, ErrBudget
		}
		if e = businessLiability(ctx, tx, scope.OrganizationID, b.ID); e != nil {
			return w, e
		}
		var open int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM business.work_intents WHERE organization_id=$1 AND employment_id=$2 AND body->>'status' IN ('admitted','reconciling')`, scope.OrganizationID, emp.ID).Scan(&open)
		if e != nil {
			return w, e
		}
		if open >= m.MaxOpenWork {
			return w, ErrConcurrency
		}
		fund, e := budget(ctx, tx, scope.OrganizationID, b.ID)
		if e != nil {
			return w, e
		}
		if in.ReservationMicros > min(fund.FundedMicros, fund.CapMicros)-fund.ReservedMicros-fund.SettledMicros {
			return w, ErrBudget
		}
		w = Work{BusinessRevision: b.Revision, EmploymentRevision: emp.Revision, ID: id("work"), BusinessID: b.ID, EmploymentID: emp.ID, MandateID: m.ID, MandateRevision: m.Revision, Actor: scope.Actor, Objective: in.Objective, OutputContract: in.OutputContract, ReservationMicros: in.ReservationMicros, HeldMicros: in.ReservationMicros, MaxAttempts: m.MaxAttempts, Status: "admitted", CreatedAt: now}
		if _, e = tx.Exec(ctx, `INSERT INTO business.work_intents VALUES($1,$2,$3,$4,$5,$6)`, scope.OrganizationID, w.ID, b.ID, emp.ID, m.ID, jsonBytes(w)); e != nil {
			return w, e
		}
		if _, e = tx.Exec(ctx, `UPDATE business.budgets SET reserved_micros=reserved_micros+$3,revision=revision+1 WHERE organization_id=$1 AND business_id=$2`, scope.OrganizationID, b.ID, in.ReservationMicros); e != nil {
			return w, e
		}
		return w, event(ctx, tx, scope.OrganizationID, "budget_reserved", w.ID, struct {
			Amount int64 `json:"amountMicros"`
		}{in.ReservationMicros})
	})
}
func (s *Store) ListWork(ctx context.Context, scope Scope, bid string) ([]Work, error) {
	out := []Work{}
	e := s.read(ctx, scope, func(tx pgx.Tx) error {
		if e := businessAccess(ctx, tx, scope, bid); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT body FROM business.work_intents WHERE organization_id=$1 AND business_id=$2 ORDER BY id`, scope.OrganizationID, bid)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var w Work
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &w); e != nil {
				return e
			}
			out = append(out, w)
		}
		return rows.Err()
	})
	return out, e
}

// Revocation releases only definitely unissued work. A prepared operation may
// have escaped the process; fence it and retain its allowance for reconciliation.
func cancelWork(ctx context.Context, tx pgx.Tx, org string, match func(Work) bool, reason string) error {
	rows, e := tx.Query(ctx, `SELECT body FROM business.work_intents WHERE organization_id=$1 AND body->>'status' IN ('admitted','reconciling')`, org)
	if e != nil {
		return e
	}
	var work []Work
	for rows.Next() {
		var raw []byte
		var w Work
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(raw, &w); e != nil {
			rows.Close()
			return e
		}
		if match(w) {
			work = append(work, w)
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, w := range work {
		if e = cancelAttemptWork(ctx, tx, org, &w, reason); e != nil {
			return e
		}
	}
	return nil
}

func cancelIssuerWork(ctx context.Context, tx pgx.Tx, org, issuer string) error {
	rows, e := tx.Query(ctx, `SELECT id FROM business.mandates WHERE organization_id=$1 AND body->>'issuerId'=$2`, org, issuer)
	if e != nil {
		return e
	}
	ids := map[string]bool{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return e
		}
		ids[key] = true
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	return cancelWork(ctx, tx, org, func(w Work) bool { return ids[w.MandateID] }, "mandate_issuer_changed")
}
