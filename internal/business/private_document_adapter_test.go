package business

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func documentAdapterFixture(t *testing.T, s *Store, issuer *ProviderAdmin) (fixture, Work, ProviderGrant) {
	t.Helper()
	ctx := context.Background()
	actor := Actor{"person", uuid.NewString()}
	args := setupArgs()
	args.ModelAllowanceMicros = 200000
	r, e := s.SetupBusiness(ctx, actor, args)
	if e != nil {
		t.Fatal(e)
	}
	scope := Scope{r.Organization.ID, actor}
	b, e := s.UpdateBusiness(ctx, scope, UpdateBusinessArgs{uuid.NewString(), r.Business.ID, 1, "active", "agent_ceo", "full_autonomy"})
	if e != nil {
		t.Fatal(e)
	}
	r.Business = b
	emp, e := s.CreateEmployment(ctx, scope, EmploymentArgs{uuid.NewString(), b.ID, "Writer", "document-writer", "1", "reviewed"})
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.GrantMandate(ctx, scope, MandateArgs{uuid.NewString(), b.ID, emp.ID, time.Now().Add(time.Hour), 100000, 10, 2})
	if e != nil {
		t.Fatal(e)
	}
	f := fixture{scope, r, emp, m}
	g, e := issuer.IssueGrant(ctx, ProviderGrantArgs{OrganizationID: scope.OrganizationID, ID: id("grant"), BusinessID: b.ID, AccountID: uuid.NewString(), CredentialRef: "test-secret-reference", AdapterID: PrivateDocumentAdapterID, RouteRevision: PrivateDocumentRouteRevision, PriceRevision: OpenAIDocumentPriceRevision, Retention: "store_false", AllowanceMicros: 200000, MaxOperationMicros: 100000, MaxOperations: 2, ExpiresAt: time.Now().Add(time.Hour), RouteSnapshot: jsonBytes(PrivateDocumentRoute{OpenAIDocumentModel, "proj_fixture"}), PriceSnapshot: jsonBytes(map[string]string{"revision": OpenAIDocumentPriceRevision})})
	if e != nil {
		t.Fatal(e)
	}
	request, source, e := FreezePrivateBusinessBrief(b, "request_qa")
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.AdmitProviderWork(ctx, scope, ProviderWorkArgs{f.work(100000), g.ID, request.Bytes(), jsonBytes(DocumentAdmissionEvidence{request.Digest(), 100, "synthetic-count-receipt", []PrivateBusinessBriefSource{source}})})
	if e != nil {
		t.Fatal(e)
	}
	return f, w, g
}
func documentAdapterConfig(t *testing.T, g ProviderGrant, rt documentTestRT) PrivateDocumentAdapterConfig {
	return PrivateDocumentAdapterConfig{PrivateDocumentCredential{g.AccountID, g.CredentialRef, "proj_fixture"}, documentTestTransport(t, rt), func(ctx context.Context, scope Scope, w Work, e DocumentAdmissionEvidence) error {
		if scope.OrganizationID != g.OrganizationID || w.BusinessID != g.BusinessID || e.TokenCountReceipt != "synthetic-count-receipt" || len(e.Sources) != 1 || e.Sources[0].Kind != "business_setup_v1" {
			return ErrDenied
		}
		return ctx.Err()
	}}
}
func TestPostgresPrivateDocumentAdapter(t *testing.T) {
	s, admin, runtime := testStore(t)
	ctx := context.Background()
	issuer, e := NewProviderAdmin(ctx, admin)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("queued_restart_get_settles_exact_receipt", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		creates, gets := 0, 0
		config := documentAdapterConfig(t, g, func(r *http.Request) (*http.Response, error) {
			if r.Method == "POST" {
				creates++
				return documentTestResponse(strings.Replace(documentTestBody(), `"completed"`, `"queued"`, 1)), nil
			}
			gets++
			return documentTestResponse(documentTestBody()), nil
		})
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || first.State != "reconciliation_required" {
			t.Fatal(first, e)
		}
		view, e := s.GetProviderJournal(ctx, f.scope, first.Attempt.Operation.ID)
		if e != nil || view.ResponseID != "resp_qa" || len(view.Facts) != 1 {
			t.Fatal(view, e)
		}
		expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
		reopened, e := New(ctx, runtime)
		if e != nil {
			t.Fatal(e)
		}
		a, e = LoadPrivateDocumentAdapter(ctx, reopened, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		second, e := providerBridgeWorker(t, reopened, a).Step(ctx, f.scope, w.ID)
		if e != nil || second.State != "completed" || second.Result == nil || !second.Result.Eligible || creates != 1 || gets != 1 {
			t.Fatal(second, e, creates, gets)
		}
		checkProviderBalance(t, s, f, g, 0, 440)
		third, e := providerBridgeWorker(t, reopened, a).Step(ctx, f.scope, w.ID)
		if e != nil || third.Result.ID != second.Result.ID || creates != 1 || gets != 1 {
			t.Fatal(third, e)
		}
	})
	t.Run("lost_ack_never_recreates", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		calls := 0
		config := documentAdapterConfig(t, g, func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("simulated lost ACK") })
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if !errors.Is(e, ErrOpenAIDocumentTransport) {
			t.Fatal(first, e)
		}
		for range 2 {
			expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
			a, e = LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
			if e != nil {
				t.Fatal(e)
			}
			out, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
			if e != nil || out.State != "reconciliation_required" {
				t.Fatal(out, e)
			}
		}
		if calls != 1 {
			t.Fatal("retried unknown creation", calls)
		}
		checkProviderBalance(t, s, f, g, 100000, 0)
	})
	t.Run("unknown_cost_keeps_result_and_advances_same_receipt", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		creates, gets := 0, 0
		config := documentAdapterConfig(t, g, func(r *http.Request) (*http.Response, error) {
			if r.Method == "POST" {
				creates++
				return documentTestResponse(strings.Replace(documentTestBody(), `"cache_write_tokens":0`, `"cache_write_tokens":null`, 1)), nil
			}
			gets++
			return documentTestResponse(documentTestBody()), nil
		})
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || first.State != "reconciliation_required" || first.Result == nil {
			t.Fatal(first, e)
		}
		checkProviderBalance(t, s, f, g, 100000, 0)
		expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
		a, e = LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		second, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || second.State != "completed" || second.Result.ID != first.Result.ID || creates != 1 || gets != 1 {
			t.Fatal(second, e, creates, gets)
		}
		checkProviderBalance(t, s, f, g, 0, 440)
	})
	t.Run("early_ack_survives_malformed_tail", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		creates, gets := 0, 0
		config := documentAdapterConfig(t, g, func(r *http.Request) (*http.Response, error) {
			if r.Method == "POST" {
				creates++
				return documentTestResponse(`{"id":"resp_qa","usage":{oops`), nil
			}
			gets++
			return documentTestResponse(documentTestBody()), nil
		})
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if !errors.Is(e, ErrOpenAIDocumentEnvelope) {
			t.Fatal(first, e)
		}
		view, e := s.GetProviderJournal(ctx, f.scope, first.Attempt.Operation.ID)
		if e != nil || view.ResponseID != "resp_qa" {
			t.Fatal(view, e)
		}
		expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
		a, e = LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		second, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || second.Result == nil || creates != 1 || gets != 1 {
			t.Fatal(second, e)
		}
	})
	t.Run("cancelled_execution_retains_terminal_without_completing_work", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		run, cancel := context.WithCancel(ctx)
		defer cancel()
		calls := 0
		config := documentAdapterConfig(t, g, func(r *http.Request) (*http.Response, error) {
			calls++
			cancel()
			return documentTestResponse(documentTestBody()), nil
		})
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, a).Step(run, f.scope, w.ID)
		if !errors.Is(e, context.Canceled) {
			t.Fatal(first, e)
		}
		view, e := s.GetProviderJournal(ctx, f.scope, first.Attempt.Operation.ID)
		if e != nil || len(view.Facts) != 2 || view.Facts[1].ActualMicros == nil {
			t.Fatal(view, e)
		}
		checkProviderBalance(t, s, f, g, 100000, 0)
		expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
		a, e = LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		out, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil || out.Result == nil || calls != 1 {
			t.Fatal(out, e, calls)
		}
		checkProviderBalance(t, s, f, g, 0, 440)
	})
	t.Run("source_and_credential_authority_before_egress", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		calls := 0
		config := documentAdapterConfig(t, g, func(r *http.Request) (*http.Response, error) {
			calls++
			return documentTestResponse(documentTestBody()), nil
		})
		wrong := config
		wrong.Credential.AccountID = "foreign-account"
		if _, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, wrong); !errors.Is(e, ErrDenied) {
			t.Fatal(e)
		}
		wrong = config
		wrong.Credential.ProjectID = "other-project"
		if _, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, wrong); e == nil {
			t.Fatal("wrong project")
		}
		other := makeFixture(t, s)
		if _, e := LoadPrivateDocumentAdapter(ctx, s, other.scope, w.ID, config); e == nil {
			t.Fatal("cross tenant")
		}
		config.Reauthorize = func(context.Context, Scope, Work, DocumentAdmissionEvidence) error { return ErrDenied }
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		_, e = providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if !errors.Is(e, ErrDenied) || calls != 0 {
			t.Fatal(e, calls)
		}
	})
	for _, kind := range []string{"external_source", "relabeled_arbitrary_prompt", "wrong_business", "zero_count", "excess_count", "under_reserved"} {
		t.Run("preparation_rejects_"+kind, func(t *testing.T) {
			f, w, g := documentAdapterFixture(t, s, issuer)
			r, e := s.GetProviderRequest(ctx, f.scope, w.ID)
			if e != nil {
				t.Fatal(e)
			}
			var evidence DocumentAdmissionEvidence
			if e = json.Unmarshal(r.SourceBindings, &evidence); e != nil {
				t.Fatal(e)
			}
			reserve := int64(100000)
			switch kind {
			case "external_source":
				evidence.Sources[0].Kind = "private_file"
			case "wrong_business":
				evidence.Sources[0].BusinessID = "foreign-business"
			case "relabeled_arbitrary_prompt":
				request := documentTestRequest(t)
				r.Request = request.Bytes()
				evidence.RequestDigest = request.Digest()
			case "zero_count":
				evidence.InputTokens = 0
			case "excess_count":
				evidence.InputTokens = 8193
			case "under_reserved":
				reserve = 100
			}
			bad, e := s.AdmitProviderWork(ctx, f.scope, ProviderWorkArgs{f.work(reserve), g.ID, r.Request, jsonBytes(evidence)})
			if e != nil {
				t.Fatal(e)
			}
			config := documentAdapterConfig(t, g, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected egress"); return nil, nil })
			if _, e = LoadPrivateDocumentAdapter(ctx, s, f.scope, bad.ID, config); e == nil {
				t.Fatal("accepted invalid source/admission")
			}
		})
	}
	t.Run("business_authority_revoked_after_acceptance_no_delivery", func(t *testing.T) {
		f, w, g := documentAdapterFixture(t, s, issuer)
		calls := 0
		config := documentAdapterConfig(t, g, func(*http.Request) (*http.Response, error) {
			calls++
			return documentTestResponse(strings.Replace(documentTestBody(), `"completed"`, `"queued"`, 1)), nil
		})
		a, e := LoadPrivateDocumentAdapter(ctx, s, f.scope, w.ID, config)
		if e != nil {
			t.Fatal(e)
		}
		first, e := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.UpdateBusiness(ctx, f.scope, UpdateBusinessArgs{uuid.NewString(), w.BusinessID, f.result.Business.Revision, "paused", "agent_ceo", "full_autonomy"})
		if e != nil {
			t.Fatal(e)
		}
		b, e := s.GetBusiness(ctx, f.scope, w.BusinessID)
		if e != nil || b.Mission != f.result.Business.Mission || b.Customer != f.result.Business.Customer || b.FirstOutcome != f.result.Business.FirstOutcome {
			t.Fatal("setup inputs changed", e)
		}
		expire(t, admin, f.scope.OrganizationID, first.Attempt.ID)
		out, _ := providerBridgeWorker(t, s, a).Step(ctx, f.scope, w.ID)
		if out.Result != nil && out.Result.Eligible {
			t.Fatal("revoked Work delivered result")
		}
		if _, e = s.GetResult(ctx, f.scope, w.ID); !errors.Is(e, ErrNotFound) {
			t.Fatal("unexpected result", e)
		}
		checkProviderBalance(t, s, f, g, 100000, 0)
	})
}
