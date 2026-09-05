// stride-business-proof is an operator-only, one-operation local qualification
// tool. It is not an execution service and never retries a model generation.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	b "github.com/openai/openai-realtime-meeting-assistant/internal/business"
)

const capMicros int64 = 100000

type state struct {
	Version                                int
	Database, CredentialFingerprint        string
	Credential                             b.PrivateDocumentCredential
	Scope                                  b.Scope
	BusinessID, WorkID, GrantID, RequestID string
	Request                                []byte
	Count                                  b.OpenAIDocumentTokenCount
	Evidence                               b.DocumentAdmissionEvidence
}

func digest(v []byte) string { h := sha256.Sum256(v); return hex.EncodeToString(h[:]) }
func wire(v any) []byte      { out, _ := json.MarshalIndent(v, "", "  "); return append(out, '\n') }

func localConfig(dsn string) (*pgxpool.Config, error) {
	if dsn == "" {
		return nil, errors.New("local database environment is required")
	}
	c, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, errors.New("invalid local database configuration")
	}
	local := func(host string) bool {
		return host == "localhost" || host == "127.0.0.1" || host == "::1" || filepath.IsAbs(host)
	}
	if !local(c.ConnConfig.Host) || !strings.HasPrefix(c.ConnConfig.Database, "stride_business_proof_") {
		return nil, errors.New("only local stride_business_proof_ databases are allowed")
	}
	for _, f := range c.ConnConfig.Fallbacks {
		if !local(f.Host) {
			return nil, errors.New("remote database fallback is forbidden")
		}
	}
	c.MaxConns = 2
	c.MinConns = 0
	c.ConnConfig.ConnectTimeout = 5 * time.Second
	return c, nil
}
func databaseIdentity(c *pgxpool.Config) string {
	return digest(wire([]any{c.ConnConfig.Host, c.ConnConfig.Port, c.ConnConfig.Database, c.ConnConfig.User}))
}
func exclusive(path string, content []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.Write(content); e != nil {
		return e
	}
	return f.Sync()
}
func privateDir(dir string) error {
	i, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode().Perm() != 0700 {
		return errors.New("state directory must be a real directory with mode 0700")
	}
	return nil
}
func readState(dir string) (state, error) {
	var s state
	if e := privateDir(dir); e != nil {
		return s, e
	}
	p := filepath.Join(dir, "state.json")
	i, e := os.Lstat(p)
	if e != nil {
		return s, e
	}
	if !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || i.Size() > 1000000 {
		return s, errors.New("invalid private state file")
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return s, e
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	e = dec.Decode(&s)
	if e == nil && (s.Version != 1 || s.WorkID == "" || s.Count.InputTokens <= 0 || s.Count.InputTokens > b.OpenAIDocumentInputTokenLimit || s.Count.EnvelopeDigest == "" || s.Count.CountedAt.IsZero()) {
		e = errors.New("incomplete proof state")
	}
	return s, e
}

func main() {
	if e := run(); e != nil { // Never print upstream errors: DSNs and provider data may be embedded.
		fmt.Fprintln(os.Stderr, "Proof stopped; no automatic retry. Inspect private state and local database. Failure category:", safeError(e))
		os.Exit(1)
	}
}
func safeError(e error) string {
	switch {
	case errors.Is(e, b.ErrDenied):
		return "authority"
	case errors.Is(e, b.ErrBudget):
		return "budget"
	case errors.Is(e, b.ErrConcurrency):
		return "lease busy"
	case errors.Is(e, b.ErrReconciliation):
		return "reconciliation required"
	default:
		return "configuration or operation"
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("prepare or step required")
	}
	mode := os.Args[1]
	fs := flag.NewFlagSet("stride-business-proof "+mode, flag.ContinueOnError)
	dir := fs.String("state-dir", "", "new private proof directory for prepare; existing directory for step")
	allow := fs.Bool("allow-live-model", false, "authorize private setup token counting and at most one admitted generation; step may retrieve its existing response")
	if e := fs.Parse(os.Args[2:]); e != nil {
		return e
	}
	if (mode != "prepare" && mode != "step") || !*allow || *dir == "" || !filepath.IsAbs(*dir) || fs.NArg() != 0 {
		return errors.New("explicit mode, absolute state directory, and live model authorization required")
	}
	runtime, e := localConfig(os.Getenv("STRIDE_PROOF_DATABASE_URL"))
	if e != nil {
		return e
	}
	key, project := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_PROJECT_ID")
	if key == "" || project == "" {
		return errors.New("provider credentials required")
	}
	transport, e := b.NewOpenAIDocumentTransport(b.OpenAIDocumentTransportConfig{APIKey: key, ProjectID: project, Timeout: 20 * time.Second})
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if mode == "prepare" {
		return prepare(ctx, *dir, runtime, key, project, transport)
	}
	s, e := readState(*dir)
	if e != nil {
		return e
	}
	if s.Database != databaseIdentity(runtime) || s.CredentialFingerprint != digest([]byte(key)) || s.Credential.ProjectID != project {
		return b.ErrDenied
	}
	pool, e := pgxpool.NewWithConfig(ctx, runtime)
	if e != nil {
		return e
	}
	defer pool.Close()
	store, e := b.New(ctx, pool)
	if e != nil {
		return e
	}
	check := func(ctx context.Context, scope b.Scope, w b.Work, evidence b.DocumentAdmissionEvidence) error {
		if scope != s.Scope || w.ID != s.WorkID || w.BusinessID != s.BusinessID || !bytes.Equal(wire(evidence), wire(s.Evidence)) || evidence.RequestDigest != s.Count.RequestDigest || evidence.InputTokens != s.Count.InputTokens || evidence.TokenCountReceipt != "local-count:"+digest(wire(s.Count)) {
			return b.ErrDenied
		}
		business, e := store.GetBusiness(ctx, scope, s.BusinessID)
		if e != nil {
			return e
		}
		r, source, e := b.FreezePrivateBusinessBrief(business, s.RequestID)
		if e != nil || !bytes.Equal(r.Bytes(), s.Request) || r.Digest() != s.Count.RequestDigest || len(evidence.Sources) != 1 || evidence.Sources[0] != source {
			return b.ErrDenied
		}
		return nil
	}
	work, e := store.GetWork(ctx, s.Scope, s.WorkID)
	if e != nil {
		return e
	}
	if e = check(ctx, s.Scope, work, s.Evidence); e != nil {
		return e
	}
	adapter, e := b.LoadPrivateDocumentAdapter(ctx, store, s.Scope, s.WorkID, b.PrivateDocumentAdapterConfig{Credential: s.Credential, Transport: transport, Reauthorize: check})
	if e != nil {
		return e
	}
	bridge, e := b.NewProviderWorkerStore(store)
	if e != nil {
		return e
	}
	worker, e := b.NewWorker(bridge, adapter, b.WorkerConfig{WorkerID: "local-proof-" + uuid.NewString(), LeaseSeconds: 35, StepTimeout: 30 * time.Second})
	if e != nil {
		return e
	}
	result, stepErr := worker.Step(ctx, s.Scope, s.WorkID)
	receipt := struct {
		Step          b.WorkerStep
		ErrorCategory string
	}{Step: result}
	if stepErr != nil {
		receipt.ErrorCategory = safeError(stepErr)
	}
	if e = exclusive(filepath.Join(*dir, "step-"+uuid.NewString()+".json"), wire(receipt)); e != nil {
		return e
	}
	if result.Result != nil {
		if e = exclusive(filepath.Join(*dir, "result-"+uuid.NewString()+".json"), wire(result.Result)); e != nil {
			return e
		}
		if e = exclusive(filepath.Join(*dir, "result-"+uuid.NewString()+".md"), []byte(result.Result.Content)); e != nil {
			return e
		}
	}
	fmt.Printf("state=%s work=%s result=%s\n", result.State, s.WorkID, result.Work.ResultID)
	return stepErr
}

func prepare(ctx context.Context, dir string, runtime *pgxpool.Config, key, project string, t *b.OpenAIDocumentTransport) error {
	admin, e := localConfig(os.Getenv("STRIDE_PROOF_ADMIN_DATABASE_URL"))
	if e != nil {
		return e
	}
	if admin.ConnConfig.Host != runtime.ConnConfig.Host || admin.ConnConfig.Port != runtime.ConnConfig.Port || admin.ConnConfig.Database != runtime.ConnConfig.Database || admin.ConnConfig.User == runtime.ConnConfig.User {
		return errors.New("separate administrator and runtime roles on the same local database required")
	}
	if e = os.Mkdir(dir, 0700); e != nil {
		return e
	}
	ap, e := pgxpool.NewWithConfig(ctx, admin)
	if e != nil {
		return e
	}
	defer ap.Close()
	if e = b.Migrate(ctx, ap); e != nil {
		return e
	}
	if _, e = ap.Exec(ctx, "GRANT business_runtime TO "+pgx.Identifier{runtime.ConnConfig.User}.Sanitize()); e != nil {
		return e
	}
	pool, e := pgxpool.NewWithConfig(ctx, runtime)
	if e != nil {
		return e
	}
	defer pool.Close()
	store, e := b.New(ctx, pool)
	if e != nil {
		return e
	}
	issuer, e := b.NewProviderAdmin(ctx, ap)
	if e != nil {
		return e
	}
	actor := b.Actor{Kind: "person", ID: "local-proof-operator-" + uuid.NewString()}
	setup, e := store.SetupBusiness(ctx, actor, b.SetupBusinessArgs{IdempotencyKey: uuid.NewString(), OrganizationName: "STRIDE private local proof", Name: "STRIDE", Mission: "Enable people and persistent agents to build and operate businesses together, with durable company memory and explicit authority.", Customer: "Entrepreneurs leading a small team of humans and AI agents who need work to continue reliably across sessions.", FirstOutcome: "Produce a private first-customer experiment brief for STRIDE's autonomous business workspace. Select one narrow initial customer, a falsifiable offer, and one concrete experiment. This is a proposed experiment, not evidence of demand.", Leadership: "agent_ceo", AuthorityPreset: "full_autonomy", ModelAllowanceMicros: capMicros})
	if e != nil {
		return e
	}
	scope := b.Scope{OrganizationID: setup.Organization.ID, Actor: actor}
	business, e := store.UpdateBusiness(ctx, scope, b.UpdateBusinessArgs{IdempotencyKey: uuid.NewString(), BusinessID: setup.Business.ID, ExpectedRevision: 1, Status: "active", Leadership: "agent_ceo", AuthorityPreset: "full_autonomy"})
	if e != nil {
		return e
	}
	emp, e := store.CreateEmployment(ctx, scope, b.EmploymentArgs{IdempotencyKey: uuid.NewString(), BusinessID: business.ID, Name: "First customer experiment writer", OfferingID: b.PrivateDocumentAdapterID, OfferingVersion: b.PrivateDocumentRouteRevision, OfferingDigest: digest([]byte(b.PrivateDocumentRouteRevision))})
	if e != nil {
		return e
	}
	mandate, e := store.GrantMandate(ctx, scope, b.MandateArgs{IdempotencyKey: uuid.NewString(), BusinessID: business.ID, EmploymentID: emp.ID, ExpiresAt: time.Now().Add(time.Hour), MaxWorkCostMicros: capMicros, MaxOpenWork: 1, MaxAttempts: 1})
	if e != nil {
		return e
	}
	credential := b.PrivateDocumentCredential{AccountID: "openai-project-" + digest([]byte(project)), CredentialRef: "local-env-key-" + digest([]byte(key)), ProjectID: project}
	grant, e := issuer.IssueGrant(ctx, b.ProviderGrantArgs{OrganizationID: scope.OrganizationID, ID: "grant_" + uuid.NewString(), BusinessID: business.ID, AccountID: credential.AccountID, CredentialRef: credential.CredentialRef, AdapterID: b.PrivateDocumentAdapterID, RouteRevision: b.PrivateDocumentRouteRevision, PriceRevision: b.OpenAIDocumentPriceRevision, Retention: "store_false", AllowanceMicros: capMicros, MaxOperationMicros: capMicros, MaxOperations: 1, ExpiresAt: time.Now().Add(time.Hour), RouteSnapshot: wire(b.PrivateDocumentRoute{Model: b.OpenAIDocumentModel, ProjectID: project}), PriceSnapshot: wire(map[string]any{"revision": b.OpenAIDocumentPriceRevision, "currency": "USD", "inputMicrosPerMillion": 2000000, "cachedInputMicrosPerMillion": 200000, "cacheWriteMicrosPerMillion": 2500000, "outputMicrosPerMillion": 12000000})})
	if e != nil {
		return e
	}
	requestID := "request_" + uuid.NewString()
	business, e = store.GetBusiness(ctx, scope, business.ID)
	if e != nil {
		return e
	}
	frozen, source, e := b.FreezePrivateBusinessBrief(business, requestID)
	if e != nil {
		return e
	}
	s := state{Version: 1, Database: databaseIdentity(runtime), CredentialFingerprint: digest([]byte(key)), Credential: credential, Scope: scope, BusinessID: business.ID, GrantID: grant.ID, RequestID: requestID, Request: frozen.Bytes()}
	if e = exclusive(filepath.Join(dir, "prepared.json"), wire(s)); e != nil {
		return e
	}
	count, e := t.CountInputTokens(ctx, frozen)
	if e != nil {
		return e
	}
	s.Count = count
	s.Evidence = b.DocumentAdmissionEvidence{RequestDigest: count.RequestDigest, InputTokens: count.InputTokens, TokenCountReceipt: "local-count:" + digest(wire(count)), Sources: []b.PrivateBusinessBriefSource{source}}
	if e = exclusive(filepath.Join(dir, "counted.json"), wire(s)); e != nil {
		return e
	}
	work, e := store.AdmitProviderWork(ctx, scope, b.ProviderWorkArgs{Work: b.WorkArgs{IdempotencyKey: uuid.NewString(), BusinessID: business.ID, EmploymentID: emp.ID, MandateID: mandate.ID, MandateRevision: mandate.Revision, Objective: business.FirstOutcome, OutputContract: "private_document_v1", ReservationMicros: capMicros}, GrantID: grant.ID, Request: frozen.Bytes(), SourceBindings: wire(s.Evidence)})
	if e != nil {
		return e
	}
	s.WorkID = work.ID
	if e = exclusive(filepath.Join(dir, "state.json"), wire(s)); e != nil {
		return e
	}
	fmt.Printf("state=admitted work=%s generation_cap=1 maximum_usd=0.10\n", work.ID)
	return nil
}
