package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openai/openai-realtime-meeting-assistant/internal/business"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBusinessHTTPPostgresLifecycle(t *testing.T) {
	connection := os.Getenv("BUSINESS_HTTP_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("requires a disposable PostgreSQL administrator database")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err = business.Migrate(ctx, admin); err != nil {
		t.Fatal(err)
	}
	role := "business_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOBYPASSRLS INHERIT; GRANT business_runtime TO "+role); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(connection)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.User = role
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := business.New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	setupAuthTestEnv(t)
	t.Setenv("BONFIRE_PUBLIC_URL", "http://127.0.0.1:4320")
	organizations := NewOrganizationAuthorityService()
	for i, email := range []string{"aj@shareability.com", "tim@shareability.com"} {
		p := organizationTestPerson(fmt.Sprintf("person-business-http-%d", i), rune('a'+i), time.Now().Add(-time.Hour))
		p.AccountSubjectDigest = sha256Hex([]byte(email))
		if err = organizations.RegisterPerson(p); err != nil {
			t.Fatal(err)
		}
	}
	priorOrg := strideE10LiveProductRuntime.organization
	strideE10LiveProductRuntime.organization = organizations
	defer func() { strideE10LiveProductRuntime.organization = priorOrg }()
	handler := &businessHTTP{store: store, authenticate: authenticateBusinessPerson}
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	call := func(method, path, body string, auth []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://example.com")
		for _, c := range auth {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	payload := `{"idempotencyKey":"http-setup-1","organization":{"name":"Local QA company"},"name":"STRIDE Builders QA","mission":"Build a better onboarding experience","customer":"Independent entrepreneurs","firstOutcome":"A tested onboarding improvement","leadership":"agent_ceo","authorityPreset":"full_autonomy","modelAllowanceMicros":0}`
	first := call("POST", businessAPIBase+"businesses", payload, cookies)
	if first.Code != 200 {
		t.Fatalf("setup %d %s", first.Code, first.Body.String())
	}
	var created struct {
		Business business.Business `json:"business"`
	}
	if err = json.Unmarshal(first.Body.Bytes(), &created); err != nil || created.Business.ID == "" {
		t.Fatal("missing business", err)
	}
	second := call("POST", businessAPIBase+"businesses", payload, cookies)
	if second.Code != 200 {
		t.Fatal("lost ack", second.Code, second.Body.String())
	}
	var replay struct {
		Business business.Business `json:"business"`
	}
	_ = json.Unmarshal(second.Body.Bytes(), &replay)
	if replay.Business.ID != created.Business.ID {
		t.Fatal("duplicate business after retry")
	}
	other := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	if w := call("GET", businessAPIBase+"businesses/"+created.Business.ID, "", other); w.Code != 404 {
		t.Fatal("foreign business disclosed", w.Code)
	}
	action := `{"idempotencyKey":"policy-1","expectedRevision":1,"action":"update_policy","leadership":"human_ceo","authorityPreset":"advise"}`
	for i := 0; i < 2; i++ {
		if w := call("PATCH", businessAPIBase+"businesses/"+created.Business.ID, action, cookies); w.Code != 200 {
			t.Fatal("policy retry", w.Code, w.Body.String())
		}
	}
	// Reopening an independent Store demonstrates database persistence, without
	// borrowing legacy org memberships or relying on an HTTP cache.
	reopened, err := business.New(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	handler.store = reopened
	if w := call("GET", businessAPIBase+"businesses/"+created.Business.ID, "", cookies); w.Code != 200 || !strings.Contains(w.Body.String(), `"authorityPreset":"advise"`) {
		t.Fatal("restart read", w.Code, w.Body.String())
	}
	evidenceBusiness, evidenceWork := seedBusinessHTTPWorkEvidence(t, reopened)
	workPath := businessAPIBase + "businesses/" + evidenceBusiness.ID + "/work/" + evidenceWork.ID
	if w := call("GET", workPath, "", cookies); w.Code != 200 || !strings.Contains(w.Body.String(), "Disposable QA result") || strings.Contains(w.Body.String(), "claimKey") {
		t.Fatal("work read", w.Code, w.Body.String())
	}
	if w := call("GET", workPath, "", other); w.Code != 404 {
		t.Fatal("foreign work disclosed", w.Code)
	}
	if w := call("GET", businessAPIBase+"businesses/"+created.Business.ID+"/work/"+evidenceWork.ID, "", cookies); w.Code != 404 {
		t.Fatal("cross-business work disclosed", w.Code)
	}
	if w := call("GET", businessAPIBase+"businesses/"+evidenceBusiness.ID, "", cookies); w.Code != 200 || !strings.Contains(w.Body.String(), `"spentMicros":250`) || !strings.Contains(w.Body.String(), `"unknownCostOperations":1`) {
		t.Fatal("cost read", w.Code, w.Body.String())
	}
	if addr := os.Getenv("BUSINESS_HTTP_PREVIEW_ADDR"); addr != "" {
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Fatal("preview must bind loopback")
		}
		mux := http.NewServeMux()
		registerBusinessHTTP(mux, handler)
		mux.HandleFunc("/public/", publicAssetHandler)
		mux.HandleFunc("/auth/", authHandler)
		mux.HandleFunc("/__qa/login", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><title>Local STRIDE QA sign in</title><h1>Isolated local QA</h1><p>Disposable database. No provider execution.</p><form id="login"><label>Name <input name="name" value="AJ"></label><label>Password <input name="password" type="password"></label><button>Sign in</button></form><p id="status"></p><script>document.querySelector('form').addEventListener('submit',async e=>{e.preventDefault();const f=new FormData(e.target);const r=await fetch('/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:f.get('name'),password:f.get('password')})});if(r.ok)location.href='/business';else document.querySelector('#status').textContent='Sign in failed.'})</script>`)
		})
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		defer server.Close()
		go server.Serve(listener)
		t.Logf("Business HTTP PostgreSQL preview ready at http://%s/__qa/login", addr)
		t.Logf("Private result QA at http://%s/business?business=%s&view=work&work=%s", addr, evidenceBusiness.ID, evidenceWork.ID)
		timer := time.NewTimer(25 * time.Minute)
		defer timer.Stop()
		<-timer.C
	}
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			userSessionStore().destroy(c.Value)
		}
	}
	if w := call("GET", businessAPIBase+"context", "", cookies); w.Code != 401 {
		t.Fatal("revoked login accepted", w.Code)
	}
}

// These records use the real restricted SQL store and authenticated HTTP path,
// with explicitly synthetic execution receipts. They never call a provider.
func seedBusinessHTTPWorkEvidence(t *testing.T, store *business.Store) (business.Business, business.Work) {
	t.Helper()
	ctx := context.Background()
	actor := business.Actor{Kind: "person", ID: "person-business-http-0"}
	setup, err := store.SetupBusiness(ctx, actor, business.SetupBusinessArgs{IdempotencyKey: uuid.NewString(), OrganizationName: "Disposable operating QA", Name: "STRIDE Builders · local QA", Mission: "Make a new entrepreneur’s first useful result feel effortless.", Customer: "Entrepreneurs starting their first business", FirstOutcome: "A concrete onboarding recommendation", Leadership: "agent_ceo", AuthorityPreset: "full_autonomy", ModelAllowanceMicros: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	scope := business.Scope{OrganizationID: setup.Organization.ID, Actor: actor}
	b, err := store.UpdateBusiness(ctx, scope, business.UpdateBusinessArgs{IdempotencyKey: uuid.NewString(), BusinessID: setup.Business.ID, ExpectedRevision: 1, Status: "active", Leadership: "agent_ceo", AuthorityPreset: "full_autonomy"})
	if err != nil {
		t.Fatal(err)
	}
	emp, err := store.CreateEmployment(ctx, scope, business.EmploymentArgs{IdempotencyKey: uuid.NewString(), BusinessID: b.ID, Name: "Product researcher · QA", OfferingID: "local-qa-researcher", OfferingVersion: "1", OfferingDigest: sha256Hex([]byte("synthetic offering fixture"))})
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.GrantMandate(ctx, scope, business.MandateArgs{IdempotencyKey: uuid.NewString(), BusinessID: b.ID, EmploymentID: emp.ID, ExpiresAt: time.Now().Add(time.Hour), MaxWorkCostMicros: 100000, MaxOpenWork: 3, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	var first business.Work
	for i := 0; i < 2; i++ {
		objective := "Recommend a simpler first-run experience · local QA"
		if i == 1 {
			objective = "Compare the revised onboarding · unresolved cost QA"
		}
		work, err := store.AdmitWork(ctx, scope, business.WorkArgs{IdempotencyKey: uuid.NewString(), BusinessID: b.ID, EmploymentID: emp.ID, MandateID: m.ID, MandateRevision: m.Revision, Objective: objective, OutputContract: "private_document_v1", ReservationMicros: 100000})
		if err != nil {
			t.Fatal(err)
		}
		a, err := store.ClaimAttempt(ctx, scope, business.ClaimAttemptArgs{WorkID: work.ID, WorkerID: "local-qa-worker", IdempotencyKey: uuid.NewString(), LeaseSeconds: 60})
		if err != nil {
			t.Fatal(err)
		}
		lease := business.AttemptLease{AttemptID: a.ID, WorkerID: a.WorkerID, Generation: a.Generation}
		op := business.Operation{ID: uuid.NewString(), RequestDigest: sha256Hex([]byte(objective)), AdapterID: "synthetic-qa-only", RouteRevision: "fixture-1", PriceRevision: "fixture-price-1", MaximumCostMicros: 100000}
		if _, err = store.PrepareOperation(ctx, scope, business.PrepareOperationArgs{Lease: lease, Operation: op}); err != nil {
			t.Fatal(err)
		}
		content := "# Disposable QA result\n\nLead with the first useful outcome. Let the founder describe what should exist, then show one clear next move.\n\nThis is synthetic local test content. No model ran, no customer outcome was measured, and no payment was made."
		actual := int64(250)
		cost := &actual
		if i == 1 {
			cost = nil
		}
		out, err := store.CompleteAttempt(ctx, scope, business.CompleteAttemptArgs{Lease: lease, OperationID: op.ID, Outcome: "succeeded", Content: content, ContentDigest: "sha256:" + sha256Hex([]byte(content)), Cost: business.CostEvidence{ActualMicros: cost, EvidenceRef: "synthetic-qa-cost-receipt"}, OutcomeEvidenceRef: "synthetic-qa-result-receipt"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = out.Work
		}
	}
	return b, first
}
