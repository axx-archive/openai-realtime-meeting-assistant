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
