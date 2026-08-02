package e10probe

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAccessWritesBodyFreePrivateReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing authorization")
		}
		if r.Header.Get("OpenAI-Project") != "test-project" {
			t.Fatal("missing project header")
		}
		w.Header().Set("X-Request-ID", "req_private")
		w.Header().Set("OpenAI-Project", "test-project")
		w.Header().Set("OpenAI-Organization", "org_response")
		_, _ = w.Write([]byte(`{"id":"gpt-transcribe"}`))
	}))
	defer server.Close()

	cfg := baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	receipt, err := RunAccess(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "pass" || !receipt.Success || !receipt.AttributionVerified || receipt.AttributionState != "provider_verified" {
		t.Fatalf("unexpected receipt outcome: %+v", receipt)
	}
	if receipt.RequestIDSHA256 == "req_private" || receipt.ResponseProjectSHA256 == "test-project" {
		t.Fatalf("receipt retained raw identifiers: %+v", receipt)
	}
	info, err := os.Stat(cfg.ReceiptDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("bad receipt dir: %v %v", info, err)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "test-key") || strings.Contains(string(raw), "test-project") || strings.Contains(string(raw), "req_private") {
		t.Fatalf("receipt stored sensitive response/request data: %s", raw)
	}
	var decoded Receipt
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Classification != "provider_contract_attempt" || decoded.ResponseOrganizationSHA256 == "" || decoded.CandidateManifestSHA256 != cfg.CandidateDigest {
		t.Fatalf("missing honest classification or evidence binding: %+v", decoded)
	}
}

func TestRunAccessWithoutUndocumentedProjectEchoRemainsRequestBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"gpt-transcribe"}`))
	}))
	defer server.Close()
	cfg := baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	receipt, err := RunAccess(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Success || receipt.AttributionVerified || receipt.AttributionState != "request_bound" || receipt.RequestProjectSHA256 != digest(cfg.Project) {
		t.Fatalf("response-header absence must preserve request-bound attribution: %+v", receipt)
	}
}

func TestTranscribeRequiresHardDurationAndBudgetBeforeNetwork(t *testing.T) {
	long := wavFixture(t, 11*time.Second)
	cfg := approvedTranscriptionConfig(t, long)
	_, err := RunTranscribeFile(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "hard maximum") {
		t.Fatalf("want hard duration rejection, got %v", err)
	}

	short := wavFixture(t, time.Second)
	cfg = approvedTranscriptionConfig(t, short)
	cfg.MaxUSD = 0.000001
	_, err = RunTranscribeFile(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "estimated") {
		t.Fatalf("want budget rejection, got %v", err)
	}
}

func TestTranscribeRejectsMismatchedResponseModelAndExistingReceiptDir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(MaxProbeBytes); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != TranscribeModel || r.FormValue("languages[]") != "en" {
			t.Fatal("unexpected multipart contract")
		}
		w.Header().Set("OpenAI-Project", "test-project")
		_, _ = w.Write([]byte(`{"model":"wrong-model","text":"synthetic","usage":{"seconds":1}}`))
	}))
	defer server.Close()

	cfg := approvedTranscriptionConfig(t, wavFixture(t, time.Second))
	cfg.BaseURL = server.URL
	_, err := RunTranscribeFile(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("want response-model rejection, got %v", err)
	}

	accessCfg := baseConfig(t, TranscribeModel)
	accessCfg.ReceiptDir = cfg.ReceiptDir
	if _, err := RunAccess(context.Background(), accessCfg); err == nil || !strings.Contains(err.Error(), "must not already exist") {
		t.Fatalf("want existing receipt directory rejection, got %v", err)
	}
}

func TestTranscribeSuccessBindsFixtureReferenceShapeTextAndUsageWithoutRawText(t *testing.T) {
	fixture := wavFixture(t, time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(MaxProbeBytes); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("prompt") != fixedPrompt || r.FormValue("keywords[]") != fixedKeyword || r.FormValue("languages[]") != fixedLanguage {
			t.Fatal("multipart declaration drifted")
		}
		w.Header().Set("OpenAI-Project", "test-project")
		_, _ = w.Write([]byte(`{"model":"gpt-transcribe","text":"  hello   STRIDE  ","usage":{"type":"tokens","input_tokens":5,"input_token_details":{"text_tokens":2,"audio_tokens":3},"output_tokens":4,"total_tokens":9}}`))
	}))
	defer server.Close()

	cfg := approvedTranscriptionConfig(t, fixture)
	cfg.BaseURL = server.URL
	receipt, err := RunTranscribeFile(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "pass" || !receipt.Success || receipt.FixtureSHA256 != cfg.ExpectedFixtureSHA256 || receipt.ReferenceSHA256 != cfg.ExpectedReferenceSHA256 {
		t.Fatalf("missing fixture/reference binding: %+v", receipt)
	}
	wantShape := digest(canonicalTranscriptionShape(http.MethodPost, "/audio/transcriptions", TranscribeModel, cfg.ExpectedFixtureSHA256, cfg.ExpectedReferenceSHA256))
	if receipt.RequestShapeSHA256 != wantShape {
		t.Fatalf("request shape drifted: got %s want %s", receipt.RequestShapeSHA256, wantShape)
	}
	if receipt.TextSHA256 == "" || receipt.NormalizedTextChars != len("hello STRIDE") || receipt.ReportedUsageType != "tokens" || receipt.ReportedAudioTokens == nil || *receipt.ReportedAudioTokens != 3 || receipt.CostBasis != "local_wav_duration" {
		t.Fatalf("missing text/usage contract: %+v", receipt)
	}
	if receipt.MaxUSD != cfg.MaxUSD || receipt.MaxDurationMS != MaxProbeDuration.Milliseconds() || receipt.MaxInputBytes != MaxProbeBytes || receipt.EstimatedCostUSD <= 0 {
		t.Fatalf("receipt omitted admission limits: %+v", receipt)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hello") || strings.Contains(string(raw), fixedPrompt) || strings.Contains(string(raw), fixedKeyword) || strings.Contains(string(raw), referenceText) {
		t.Fatalf("receipt retained raw text or fixed content: %s", raw)
	}
}

func TestTranscribeRejectsEmptyTextAndMalformedWAV(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("OpenAI-Project", "test-project")
		_, _ = w.Write([]byte(`{"model":"gpt-transcribe","text":" \t ","usage":{"seconds":1}}`))
	}))
	defer server.Close()

	cfg := approvedTranscriptionConfig(t, wavFixture(t, time.Second))
	cfg.BaseURL = server.URL
	_, err := RunTranscribeFile(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "non-empty text") {
		t.Fatalf("want empty text rejection, got %v", err)
	}

	bad := wavFixture(t, time.Second)
	data, err := os.ReadFile(bad)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data))) // data chunk claims past EOF
	if err := os.WriteFile(bad, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := wavDuration(bad); err == nil || !strings.Contains(err.Error(), "declared size") {
		t.Fatalf("want declared-size rejection, got %v", err)
	}
}

func TestTranscribeRejectsUntrustworthyUsageAndTrailingJSON(t *testing.T) {
	cases := map[string]string{
		"missing usage":       `{"model":"gpt-transcribe","text":"synthetic"}`,
		"malformed usage":     `{"model":"gpt-transcribe","text":"synthetic","usage":"bad"}`,
		"unknown usage":       `{"model":"gpt-transcribe","text":"synthetic","usage":{"type":"mystery"}}`,
		"negative tokens":     `{"model":"gpt-transcribe","text":"synthetic","usage":{"type":"tokens","input_tokens":-1,"input_token_details":{"text_tokens":0,"audio_tokens":-1},"output_tokens":1,"total_tokens":0}}`,
		"inconsistent tokens": `{"model":"gpt-transcribe","text":"synthetic","usage":{"type":"tokens","input_tokens":5,"input_token_details":{"text_tokens":2,"audio_tokens":3},"output_tokens":4,"total_tokens":8}}`,
		"huge duration":       `{"model":"gpt-transcribe","text":"synthetic","usage":{"type":"duration","seconds":1e300}}`,
		"trailing JSON":       `{"model":"gpt-transcribe","text":"synthetic","usage":{"type":"duration","seconds":1}} {}`,
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("OpenAI-Project", "test-project")
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			cfg := approvedTranscriptionConfig(t, wavFixture(t, time.Second))
			cfg.BaseURL = server.URL
			receipt, err := RunTranscribeFile(context.Background(), cfg)
			if err == nil || receipt.Success || receipt.Outcome != "schema_mismatch" {
				t.Fatalf("want strict usage/schema rejection, receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestProbeRejectsUnapprovedHostAndRedirect(t *testing.T) {
	cfg := baseConfig(t, TranscribeModel)
	cfg.BaseURL = "https://example.com/v1"
	if _, err := RunAccess(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "api.openai.com") {
		t.Fatalf("want live-host rejection, got %v", err)
	}

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()
	cfg = baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	receipt, err := RunAccess(context.Background(), cfg)
	if err == nil || receipt.Success || receipt.FailureClass != "transport" || hits != 1 {
		t.Fatalf("want one-hop redirect refusal, hits=%d receipt=%+v err=%v", hits, receipt, err)
	}
}

func TestProjectAssertionAndExpectedHashFailBeforeNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"id":"gpt-transcribe"}`))
	}))
	defer server.Close()

	cfg := baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	cfg.Project = ""
	_, err := RunAccess(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "OPENAI_PROJECT_ID is required") || called {
		t.Fatalf("expected missing-project pre-network refusal, called=%v err=%v", called, err)
	}

	cfg = baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	cfg.ExpectedProjectSHA256 = digest("different-project")
	_, err = RunAccess(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "does not match") || called {
		t.Fatalf("expected project-hash pre-network refusal, called=%v err=%v", called, err)
	}
}

func TestProjectScopedCredentialModeIsExplicitAndStaysUnreconciled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenAI-Project") != "" {
			t.Fatal("project-scoped key mode must not invent a raw project header")
		}
		_, _ = w.Write([]byte(`{"id":"gpt-transcribe"}`))
	}))
	defer server.Close()

	cfg := baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	cfg.APIKey = "sk-proj-test-only"
	cfg.Project = ""
	cfg.ExpectedProjectSHA256 = ""
	cfg.AllowProjectScopedKey = true
	receipt, err := RunAccess(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Success || receipt.AttributionVerified || receipt.AttributionState != "project_credential_bound_unreconciled" || receipt.CredentialScope != "project_scoped_api_key" || receipt.RequestProjectSHA256 != "" || receipt.ExpectedProjectSHA256 != "" {
		t.Fatalf("project-key state was overstated: %+v", receipt)
	}

	cfg = baseConfig(t, TranscribeModel)
	cfg.APIKey = "test-key"
	cfg.Project = ""
	cfg.ExpectedProjectSHA256 = ""
	cfg.AllowProjectScopedKey = true
	if _, err := RunAccess(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "sk-proj") {
		t.Fatalf("non-project key must not enter project-key mode: %v", err)
	}
}

func TestProviderProjectEchoMismatchWritesOnlyHashedEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("OpenAI-Project", "actual-project")
		_, _ = w.Write([]byte(`{"id":"gpt-transcribe"}`))
	}))
	defer server.Close()

	cfg := baseConfig(t, TranscribeModel)
	cfg.BaseURL = server.URL
	receipt, err := RunAccess(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "attribution echo") || receipt.Outcome != "attribution_mismatch" {
		t.Fatalf("expected attribution mismatch, receipt=%+v err=%v", receipt, err)
	}
	raw, readErr := os.ReadFile(filepath.Join(cfg.ReceiptDir, "receipt.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(raw), "actual-project") || strings.Contains(string(raw), "test-project") {
		t.Fatalf("receipt exposed raw project attribution: %s", raw)
	}
}

func TestManifestFixtureAndReferenceMustBeExactRegularFiles(t *testing.T) {
	cfg := baseConfig(t, TranscribeModel)
	cfg.CandidateDigest = digest("different manifest")
	if _, err := RunAccess(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("want candidate-manifest mismatch, got %v", err)
	}

	fixture := wavFixture(t, time.Second)
	cfg = approvedTranscriptionConfig(t, fixture)
	cfg.ExpectedFixtureSHA256 = digest("different fixture")
	if _, err := RunTranscribeFile(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("want fixture mismatch, got %v", err)
	}

	target := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(target, []byte("manifest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "manifest-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg = baseConfig(t, TranscribeModel)
	cfg.CandidateManifestPath = link
	cfg.CandidateDigest = digestBytes([]byte("manifest\n"))
	if _, err := RunAccess(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("want symlink rejection, got %v", err)
	}
}

const referenceText = "STRIDE reference phrase for a synthetic provider contract probe.\n"

func baseConfig(t *testing.T, model string) Config {
	t.Helper()
	root := t.TempDir()
	manifestBytes := []byte("stride-e10-candidate-manifest-v1\n")
	manifest := filepath.Join(root, "candidate.manifest")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{
		CandidateDigest:       digestBytes(manifestBytes),
		CandidateManifestPath: manifest,
		ReceiptDir:            filepath.Join(root, "receipt"),
		Acknowledgement:       "local-test-acknowledgement",
		APIKey:                "test-key",
		Project:               "test-project",
		ExpectedProjectSHA256: digest("test-project"),
		Model:                 model,
	}
}

func approvedTranscriptionConfig(t *testing.T, fixture string) Config {
	t.Helper()
	cfg := baseConfig(t, TranscribeModel)
	fixtureBytes, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(t.TempDir(), "reference.txt")
	if err := os.WriteFile(reference, []byte(referenceText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.AudioPath = fixture
	cfg.ExpectedFixtureSHA256 = digestBytes(fixtureBytes)
	cfg.ReferencePath = reference
	cfg.ExpectedReferenceSHA256 = digestBytes([]byte(referenceText))
	cfg.MaxUSD = 0.01
	return cfg
}

func wavFixture(t *testing.T, duration time.Duration) string {
	t.Helper()
	const sampleRate = 8000
	const channels = 1
	const bits = 16
	dataLen := int(duration.Seconds() * sampleRate * channels * bits / 8)
	buf := make([]byte, 44+dataLen)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+dataLen))
	copy(buf[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], channels)
	binary.LittleEndian.PutUint32(buf[24:], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:], sampleRate*channels*bits/8)
	binary.LittleEndian.PutUint16(buf[32:], channels*bits/8)
	binary.LittleEndian.PutUint16(buf[34:], bits)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(dataLen))
	path := filepath.Join(t.TempDir(), "fixture.wav")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
