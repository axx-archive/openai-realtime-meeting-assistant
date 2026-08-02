// Package e10probe provides intentionally narrow, auditable OpenAI capability
// probes. It must remain independent of the application runtime: it does not
// load environment files, boot services, or retain request/response bodies.
package e10probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultAPIBase               = "https://api.openai.com/v1"
	TranscribeModel              = "gpt-transcribe"
	MaxProbeDuration             = 10 * time.Second
	MaxProbeBytes          int64 = 2 << 20
	MaxProbeUSD                  = 0.05
	MaxManifestBytes       int64 = 1 << 20
	MaxReferenceBytes      int64 = 1 << 20
	MaxProbeUsageTokens          = int64(1_000_000)
	transcribeUSDPerMinute       = 0.0045
	priceSourceURL               = "https://developers.openai.com/api/docs/pricing#transcription-and-speech"
	priceSourceRevision          = "2026-08-01"
	fixedPrompt                  = "Short synthetic E10 provider contract probe."
	fixedKeyword                 = "STRIDE"
	fixedLanguage                = "en"
)

var accessModels = map[string]bool{
	"gpt-realtime-2.1":       true,
	"gpt-transcribe":         true,
	"gpt-live-transcribe":    true,
	"gpt-5.6-luna":           true,
	"gpt-5.6-terra":          true,
	"gpt-5.6-sol":            true,
	"text-embedding-3-small": true,
	"gpt-image-2":            true,
}

// Config contains only invocation-local values. Callers must obtain APIKey
// directly from OPENAI_API_KEY; this package never reads an env file.
type Config struct {
	CandidateDigest         string
	ReceiptDir              string
	Acknowledgement         string
	APIKey                  string
	Project                 string
	ExpectedProjectSHA256   string
	AllowProjectScopedKey   bool
	CandidateManifestPath   string
	ExpectedFixtureSHA256   string
	ReferencePath           string
	ExpectedReferenceSHA256 string
	Model                   string
	AudioPath               string
	MaxUSD                  float64

	// BaseURL and Client exist for deterministic package tests. The CLI always
	// uses DefaultAPIBase and the default bounded client.
	BaseURL string
	Client  *http.Client
	now     func() time.Time
}

// Receipt deliberately contains no prompt, audio bytes or identifiers,
// transcript, raw response, API key, or project value.
type Receipt struct {
	Schema                     string   `json:"schema"`
	Classification             string   `json:"classification"`
	Success                    bool     `json:"success"`
	Probe                      string   `json:"probe"`
	Endpoint                   string   `json:"endpoint"`
	Method                     string   `json:"method"`
	Model                      string   `json:"model"`
	Outcome                    string   `json:"outcome"`
	FailureClass               string   `json:"failureClass,omitempty"`
	CandidateManifestSHA256    string   `json:"candidateManifestSha256"`
	AcknowledgementSHA256      string   `json:"acknowledgementSha256"`
	RequestShapeSHA256         string   `json:"requestShapeSha256"`
	FixtureSHA256              string   `json:"fixtureSha256,omitempty"`
	ReferenceSHA256            string   `json:"referenceSha256,omitempty"`
	EventSchemaSHA256          string   `json:"eventSchemaSha256"`
	PriceSourceSHA256          string   `json:"priceSourceSha256"`
	PriceSourceURL             string   `json:"priceSourceUrl"`
	PriceSourceRevision        string   `json:"priceSourceRevision"`
	RequestProjectSHA256       string   `json:"requestProjectSha256,omitempty"`
	ExpectedProjectSHA256      string   `json:"expectedProjectSha256,omitempty"`
	CredentialScope            string   `json:"credentialScope"`
	HTTPStatus                 int      `json:"httpStatus"`
	LatencyMS                  int64    `json:"latencyMs"`
	RequestIDSHA256            string   `json:"requestIdSha256,omitempty"`
	ResponseProjectSHA256      string   `json:"responseProjectSha256,omitempty"`
	ResponseOrganizationSHA256 string   `json:"responseOrganizationSha256,omitempty"`
	AttributionVerified        bool     `json:"attributionVerified"`
	AttributionState           string   `json:"attributionState"`
	LocalDurationMS            int64    `json:"localDurationMs,omitempty"`
	ReportedDurationMS         int64    `json:"reportedDurationMs,omitempty"`
	ReportedUsageType          string   `json:"reportedUsageType,omitempty"`
	ReportedInputTokens        *int64   `json:"reportedInputTokens,omitempty"`
	ReportedAudioTokens        *int64   `json:"reportedAudioTokens,omitempty"`
	ReportedOutputTokens       *int64   `json:"reportedOutputTokens,omitempty"`
	ReportedTotalTokens        *int64   `json:"reportedTotalTokens,omitempty"`
	ReportedUsageSeconds       *float64 `json:"reportedUsageSeconds,omitempty"`
	TextSHA256                 string   `json:"textSha256,omitempty"`
	NormalizedTextChars        int      `json:"normalizedTextChars,omitempty"`
	CostBasis                  string   `json:"costBasis,omitempty"`
	ComputedCostUSD            float64  `json:"computedCostUsd"`
	EstimatedCostUSD           float64  `json:"estimatedCostUsd"`
	MaxUSD                     float64  `json:"maxUsd"`
	MaxDurationMS              int64    `json:"maxDurationMs"`
	MaxInputBytes              int64    `json:"maxInputBytes"`
	FixtureByteCount           int64    `json:"fixtureByteCount,omitempty"`
	CreatedAt                  string   `json:"createdAt"`
}

// RunAccess verifies access to one allowlisted model using GET /v1/models/:id.
// It is still acknowledgement-gated because it contacts the provider.
func RunAccess(ctx context.Context, cfg Config) (Receipt, error) {
	if err := validateCommon(cfg); err != nil {
		return Receipt{}, err
	}
	if err := validateProjectBinding(cfg); err != nil {
		return Receipt{}, err
	}
	if !accessModels[cfg.Model] {
		return Receipt{}, fmt.Errorf("model %q is not allowlisted for access preflight", cfg.Model)
	}
	endpoint := "/models/" + cfg.Model
	shape := canonicalAccessShape(http.MethodGet, endpoint, cfg.Model)
	return execute(ctx, cfg, "access", http.MethodGet, endpoint, shape, "model-object-v1", 0, "", "", 0, 0, nil)
}

// RunTranscribeFile verifies only the bounded gpt-transcribe multipart contract.
func RunTranscribeFile(ctx context.Context, cfg Config) (Receipt, error) {
	if err := validateCommon(cfg); err != nil {
		return Receipt{}, err
	}
	if err := validateProjectBinding(cfg); err != nil {
		return Receipt{}, err
	}
	if cfg.Model != TranscribeModel {
		return Receipt{}, fmt.Errorf("transcription probe only permits model %q", TranscribeModel)
	}
	if cfg.MaxUSD <= 0 || cfg.MaxUSD > MaxProbeUSD {
		return Receipt{}, fmt.Errorf("--max-usd must be greater than 0 and no more than %.2f", MaxProbeUSD)
	}
	fixture, duration, fixtureDigest, err := loadApprovedWAV(cfg.AudioPath, cfg.ExpectedFixtureSHA256)
	if err != nil {
		return Receipt{}, err
	}
	if duration > MaxProbeDuration {
		return Receipt{}, fmt.Errorf("audio duration %s exceeds hard maximum %s", duration, MaxProbeDuration)
	}
	estimated := costFor(duration)
	if cfg.MaxUSD < estimated {
		return Receipt{}, fmt.Errorf("--max-usd %.6f is below estimated maximum cost %.6f", cfg.MaxUSD, estimated)
	}
	if _, err := loadApprovedReference(cfg.ReferencePath, cfg.ExpectedReferenceSHA256); err != nil {
		return Receipt{}, err
	}
	body, contentType, err := transcriptionBody(cfg.AudioPath, fixture)
	if err != nil {
		return Receipt{}, err
	}
	endpoint := "/audio/transcriptions"
	shape := canonicalTranscriptionShape(http.MethodPost, endpoint, cfg.Model, fixtureDigest, strings.ToLower(cfg.ExpectedReferenceSHA256))
	return execute(ctx, cfg, "transcribe-file", http.MethodPost, endpoint, shape, "audio-transcriptions-usage-v1", duration, fixtureDigest, strings.ToLower(cfg.ExpectedReferenceSHA256), int64(len(fixture)), estimated, func(req *http.Request) {
		req.Header.Set("Content-Type", contentType)
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	})
}

func validateCommon(cfg Config) error {
	if !validDigest(cfg.CandidateDigest) {
		return errors.New("--candidate-digest must be a 64-character SHA-256 digest")
	}
	if digest, err := readRegularDigest(cfg.CandidateManifestPath, MaxManifestBytes, false); err != nil || !strings.EqualFold(digest, cfg.CandidateDigest) {
		if err != nil {
			return fmt.Errorf("candidate manifest: %w", err)
		}
		return errors.New("--candidate-manifest does not match --candidate-digest")
	}
	if len(strings.TrimSpace(cfg.Acknowledgement)) < 16 {
		return errors.New("--acknowledge-paid-probe must be a non-empty acknowledgement nonce of at least 16 characters")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("OPENAI_API_KEY is required; the probe does not load env files")
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.ReceiptDir) {
		return errors.New("--receipt-dir must be an absolute path to a new private directory")
	}
	return nil
}

func validateProjectBinding(cfg Config) error {
	if strings.TrimSpace(cfg.Project) == "" {
		if !cfg.AllowProjectScopedKey {
			return errors.New("OPENAI_PROJECT_ID is required unless --allow-project-scoped-key is explicitly set")
		}
		if !strings.HasPrefix(strings.TrimSpace(cfg.APIKey), "sk-proj-") {
			return errors.New("--allow-project-scoped-key requires an sk-proj credential")
		}
		if strings.TrimSpace(cfg.ExpectedProjectSHA256) != "" {
			return errors.New("--expected-project-sha256 cannot be asserted without OPENAI_PROJECT_ID")
		}
		return nil
	}
	if cfg.AllowProjectScopedKey {
		return errors.New("use either explicit OPENAI_PROJECT_ID binding or --allow-project-scoped-key, not both")
	}
	if !validDigest(cfg.ExpectedProjectSHA256) {
		return errors.New("--expected-project-sha256 must be a 64-character SHA-256 digest when OPENAI_PROJECT_ID is set")
	}
	if !strings.EqualFold(digest(cfg.Project), cfg.ExpectedProjectSHA256) {
		return errors.New("OPENAI_PROJECT_ID does not match --expected-project-sha256")
	}
	return nil
}

func execute(ctx context.Context, cfg Config, probe, method, endpoint, requestShape, eventSchema string, localDuration time.Duration, fixtureDigest, referenceDigest string, fixtureBytes int64, estimatedCost float64, prepare func(*http.Request)) (Receipt, error) {
	dir, err := newPrivateDir(cfg.ReceiptDir)
	if err != nil {
		return Receipt{}, err
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	receipt := Receipt{
		Schema:                  "stride.e10.openai-probe-receipt/v1",
		Classification:          "provider_contract_attempt",
		Probe:                   probe,
		Endpoint:                endpoint,
		Method:                  method,
		Model:                   cfg.Model,
		Outcome:                 "transport_error",
		CandidateManifestSHA256: strings.ToLower(cfg.CandidateDigest),
		AcknowledgementSHA256:   digest(cfg.Acknowledgement),
		RequestShapeSHA256:      digest(requestShape),
		FixtureSHA256:           fixtureDigest,
		ReferenceSHA256:         referenceDigest,
		EventSchemaSHA256:       digest(eventSchema),
		PriceSourceSHA256:       digest(priceDeclaration(cfg.Model, probe)),
		PriceSourceURL:          priceSourceURLFor(cfg.Model, probe),
		PriceSourceRevision:     priceSourceRevision,
		RequestProjectSHA256:    optionalDigest(cfg.Project),
		ExpectedProjectSHA256:   strings.ToLower(strings.TrimSpace(cfg.ExpectedProjectSHA256)),
		CredentialScope:         credentialScope(cfg),
		AttributionState:        initialAttributionState(cfg),
		LocalDurationMS:         localDuration.Milliseconds(),
		EstimatedCostUSD:        estimatedCost,
		MaxUSD:                  cfg.MaxUSD,
		MaxDurationMS:           MaxProbeDuration.Milliseconds(),
		MaxInputBytes:           MaxProbeBytes,
		FixtureByteCount:        fixtureBytes,
		CreatedAt:               now().UTC().Format(time.RFC3339Nano),
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("redirects are not permitted for provider probes")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultAPIBase
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+endpoint, nil)
	if err == nil {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		if strings.TrimSpace(cfg.Project) != "" {
			req.Header.Set("OpenAI-Project", cfg.Project)
		}
		if prepare != nil {
			prepare(req)
		}
	}
	if err != nil {
		receipt.FailureClass = "request_construction"
		return receipt, writeThenReturn(dir, receipt, err)
	}
	started := now()
	resp, requestErr := client.Do(req)
	receipt.LatencyMS = now().Sub(started).Milliseconds()
	if requestErr != nil {
		receipt.FailureClass = "transport"
		return receipt, writeThenReturn(dir, receipt, requestErr)
	}
	defer resp.Body.Close()
	receipt.HTTPStatus = resp.StatusCode
	if id := resp.Header.Get("X-Request-ID"); id != "" {
		receipt.RequestIDSHA256 = digest(id)
	}
	if project := resp.Header.Get("OpenAI-Project"); project != "" {
		receipt.ResponseProjectSHA256 = digest(project)
	}
	if organization := resp.Header.Get("OpenAI-Organization"); organization != "" {
		receipt.ResponseOrganizationSHA256 = digest(organization)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxProbeBytes))
		receipt.Outcome = "provider_error"
		receipt.FailureClass = failureClass(resp.StatusCode)
		return receipt, writeThenReturn(dir, receipt, fmt.Errorf("provider returned HTTP %d (%s)", resp.StatusCode, receipt.FailureClass))
	}
	if err := verifyAttribution(&receipt); err != nil {
		receipt.Outcome = "attribution_mismatch"
		receipt.FailureClass = "project_attribution"
		return receipt, writeThenReturn(dir, receipt, err)
	}

	if probe == "access" {
		var got struct {
			ID string `json:"id"`
		}
		if err := decodeOneJSON(resp.Body, &got); err != nil || got.ID != cfg.Model {
			receipt.Outcome = "schema_mismatch"
			receipt.FailureClass = "schema"
			return receipt, writeThenReturn(dir, receipt, errors.New("provider model object did not match requested model"))
		}
	} else {
		var got struct {
			Model string          `json:"model"`
			Usage json.RawMessage `json:"usage"`
			Text  string          `json:"text"`
		}
		if err := decodeOneJSON(resp.Body, &got); err != nil {
			receipt.Outcome = "schema_mismatch"
			receipt.FailureClass = "schema"
			return receipt, writeThenReturn(dir, receipt, errors.New("provider response was not the expected JSON contract"))
		}
		if got.Model != "" && got.Model != cfg.Model {
			receipt.Outcome = "schema_mismatch"
			receipt.FailureClass = "schema"
			return receipt, writeThenReturn(dir, receipt, errors.New("provider response model did not match requested model"))
		}
		normalizedText := strings.Join(strings.Fields(got.Text), " ")
		if normalizedText == "" {
			receipt.Outcome = "schema_mismatch"
			receipt.FailureClass = "schema"
			return receipt, writeThenReturn(dir, receipt, errors.New("provider transcription response did not contain non-empty text"))
		}
		receipt.TextSHA256 = digest(got.Text)
		receipt.NormalizedTextChars = utf8.RuneCountInString(normalizedText)
		usage, err := parseUsage(got.Usage, localDuration)
		if err != nil {
			receipt.Outcome = "schema_mismatch"
			receipt.FailureClass = "usage_schema"
			return receipt, writeThenReturn(dir, receipt, fmt.Errorf("provider transcription usage was invalid: %w", err))
		}
		receipt.ReportedUsageType = usage.Type
		receipt.ReportedInputTokens = usage.InputTokens
		receipt.ReportedAudioTokens = usage.AudioTokens
		receipt.ReportedOutputTokens = usage.OutputTokens
		receipt.ReportedTotalTokens = usage.TotalTokens
		receipt.ReportedUsageSeconds = usage.Seconds
		if usage.Seconds != nil && *usage.Seconds > 0 {
			receipt.ReportedDurationMS = int64(*usage.Seconds * 1000)
			receipt.CostBasis = "provider_duration"
			receipt.ComputedCostUSD = costFor(time.Duration(*usage.Seconds * float64(time.Second)))
		} else {
			receipt.CostBasis = "local_wav_duration"
			receipt.ComputedCostUSD = costFor(localDuration)
		}
	}
	if receipt.ComputedCostUSD > cfg.MaxUSD && probe == "transcribe-file" {
		receipt.Outcome = "cost_cap_exceeded"
		receipt.FailureClass = "post_call_cost_cap"
		return receipt, writeThenReturn(dir, receipt, errors.New("provider-reported cost basis exceeded --max-usd"))
	}
	receipt.Outcome = "pass"
	receipt.Success = true
	return receipt, writeThenReturn(dir, receipt, nil)
}

func writeThenReturn(dir string, receipt Receipt, runErr error) error {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return runErr
}

func newPrivateDir(requested string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(requested))
	if err != nil {
		return "", fmt.Errorf("resolve receipt parent: %w", err)
	}
	dir := filepath.Join(parent, filepath.Base(requested))
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return "", errors.New("--receipt-dir must not already exist")
		}
		return "", fmt.Errorf("inspect receipt directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("create private receipt directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.New("receipt directory is not a private 0700 directory")
	}
	return dir, nil
}

func transcriptionBody(path string, fixture []byte) ([]byte, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(fixture); err != nil {
		return nil, "", err
	}
	if body.Len() > int(MaxProbeBytes)+4096 {
		return nil, "", errors.New("probe file exceeds maximum size")
	}
	for _, field := range [][2]string{{"model", TranscribeModel}, {"prompt", fixedPrompt}, {"keywords[]", fixedKeyword}, {"languages[]", fixedLanguage}} {
		if err := w.WriteField(field[0], field[1]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

func wavDuration(path string) (time.Duration, error) {
	data, err := readRegularFile(path, MaxProbeBytes)
	if err != nil {
		return 0, err
	}
	return wavDurationBytes(data)
}

func wavDurationBytes(data []byte) (time.Duration, error) {
	if len(data) == 0 || int64(len(data)) > MaxProbeBytes {
		return 0, fmt.Errorf("probe file must be between 1 and %d bytes", MaxProbeBytes)
	}
	infoSize := int64(len(data))
	f := bytes.NewReader(data)
	var header [12]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return 0, errors.New("probe file must be a RIFF/WAVE PCM file")
	}
	riffEnd := int64(binary.LittleEndian.Uint32(header[4:])) + 8
	if riffEnd != infoSize {
		return 0, errors.New("WAV RIFF declared size does not equal actual file length")
	}
	var byteRate uint32
	var blockAlign uint16
	var dataSize uint32
	foundFmt, foundData := false, false
	offset := int64(len(header))
	for offset < riffEnd {
		if riffEnd-offset < 8 {
			return 0, errors.New("truncated WAV chunk header")
		}
		var chunk [8]byte
		if _, err := io.ReadFull(f, chunk[:]); err != nil {
			return 0, errors.New("truncated WAV chunk header")
		}
		size := binary.LittleEndian.Uint32(chunk[4:])
		padded := int64(size)
		if size%2 == 1 {
			padded++
		}
		if padded > riffEnd-offset-8 || padded > infoSize-offset-8 {
			return 0, errors.New("WAV chunk declared size exceeds actual file length")
		}
		if string(chunk[:4]) == "fmt " {
			if foundFmt {
				return 0, errors.New("WAV must contain exactly one fmt chunk")
			}
			if size < 16 || size > 1024 {
				return 0, errors.New("invalid WAV fmt chunk")
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(f, data); err != nil {
				return 0, errors.New("truncated WAV fmt chunk")
			}
			if binary.LittleEndian.Uint16(data[:2]) != 1 {
				return 0, errors.New("probe WAV must use PCM encoding")
			}
			channels := binary.LittleEndian.Uint16(data[2:4])
			sampleRate := binary.LittleEndian.Uint32(data[4:8])
			byteRate = binary.LittleEndian.Uint32(data[8:12])
			blockAlign = binary.LittleEndian.Uint16(data[12:14])
			bitsPerSample := binary.LittleEndian.Uint16(data[14:16])
			if channels == 0 || sampleRate == 0 || bitsPerSample == 0 || bitsPerSample%8 != 0 {
				return 0, errors.New("invalid WAV PCM format values")
			}
			expectedBlockAlign := uint32(channels) * uint32(bitsPerSample) / 8
			if uint32(blockAlign) != expectedBlockAlign || byteRate != sampleRate*expectedBlockAlign {
				return 0, errors.New("incoherent WAV PCM byte rate or block alignment")
			}
			foundFmt = true
		} else if string(chunk[:4]) == "data" {
			if foundData {
				return 0, errors.New("WAV must contain exactly one data chunk")
			}
			dataSize = size
			if _, err := f.Seek(int64(size), io.SeekCurrent); err != nil {
				return 0, errors.New("truncated WAV data chunk")
			}
			foundData = true
		} else if _, err := f.Seek(int64(size), io.SeekCurrent); err != nil {
			return 0, errors.New("truncated WAV chunk")
		}
		if size%2 == 1 {
			if _, err := f.Seek(1, io.SeekCurrent); err != nil {
				return 0, errors.New("truncated WAV padding")
			}
		}
		offset += 8 + padded
	}
	if !foundFmt || !foundData || byteRate == 0 || dataSize == 0 {
		return 0, errors.New("WAV must contain non-empty fmt and data chunks")
	}
	if dataSize%uint32(blockAlign) != 0 {
		return 0, errors.New("WAV data size is not aligned to PCM frames")
	}
	d := time.Duration(float64(dataSize) / float64(byteRate) * float64(time.Second))
	if d <= 0 {
		return 0, errors.New("invalid WAV duration")
	}
	return d, nil
}

type usageSummary struct {
	Type                                                string
	InputTokens, AudioTokens, OutputTokens, TotalTokens *int64
	Seconds                                             *float64
}

func parseUsage(raw json.RawMessage, localDuration time.Duration) (usageSummary, error) {
	var usage struct {
		Type              string   `json:"type"`
		InputTokens       *int64   `json:"input_tokens"`
		OutputTokens      *int64   `json:"output_tokens"`
		TotalTokens       *int64   `json:"total_tokens"`
		Seconds           *float64 `json:"seconds"`
		InputTokenDetails *struct {
			TextTokens  *int64 `json:"text_tokens"`
			AudioTokens *int64 `json:"audio_tokens"`
		} `json:"input_token_details"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return usageSummary{}, errors.New("usage is required")
	}
	if err := json.Unmarshal(raw, &usage); err != nil {
		return usageSummary{}, errors.New("usage is not the documented JSON object")
	}
	switch usage.Type {
	case "tokens":
		if usage.Seconds != nil || usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil || usage.InputTokenDetails == nil || usage.InputTokenDetails.TextTokens == nil || usage.InputTokenDetails.AudioTokens == nil {
			return usageSummary{}, errors.New("token usage requires input, text/audio detail, output, and total counts only")
		}
		counts := []*int64{usage.InputTokens, usage.InputTokenDetails.TextTokens, usage.InputTokenDetails.AudioTokens, usage.OutputTokens, usage.TotalTokens}
		for _, count := range counts {
			if *count < 0 || *count > MaxProbeUsageTokens {
				return usageSummary{}, errors.New("token usage count is outside the bounded range")
			}
		}
		if *usage.InputTokens != *usage.InputTokenDetails.TextTokens+*usage.InputTokenDetails.AudioTokens || *usage.TotalTokens != *usage.InputTokens+*usage.OutputTokens {
			return usageSummary{}, errors.New("token usage totals are inconsistent")
		}
		return usageSummary{
			Type: usage.Type, InputTokens: usage.InputTokens, AudioTokens: usage.InputTokenDetails.AudioTokens,
			OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		}, nil
	case "duration":
		if usage.Seconds == nil || usage.InputTokens != nil || usage.OutputTokens != nil || usage.TotalTokens != nil || usage.InputTokenDetails != nil {
			return usageSummary{}, errors.New("duration usage requires seconds only")
		}
		seconds := *usage.Seconds
		maxSeconds := math.Ceil(localDuration.Seconds()) + 1
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > maxSeconds {
			return usageSummary{}, errors.New("duration usage seconds are outside the bounded fixture range")
		}
		return usageSummary{Type: usage.Type, Seconds: usage.Seconds}, nil
	default:
		return usageSummary{}, errors.New("usage type must be tokens or duration")
	}
}

func decodeOneJSON(body io.Reader, destination any) error {
	raw, err := io.ReadAll(io.LimitReader(body, MaxProbeBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > MaxProbeBytes {
		return errors.New("provider response exceeded the bounded body limit")
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return errors.New("provider response was not exactly one JSON document")
	}
	return nil
}

func canonicalAccessShape(method, endpoint, model string) string {
	return strings.Join([]string{"e10-access-shape-v1", "method=" + method, "endpoint=" + endpoint, "model=" + model, "fields=none"}, "\n")
}

func canonicalTranscriptionShape(method, endpoint, model, fixtureSHA256, referenceSHA256 string) string {
	return strings.Join([]string{
		"e10-transcription-shape-v1", "method=" + method, "endpoint=" + endpoint, "model=" + model,
		"fields=file,model,prompt,keywords[],languages[]", "fixture_sha256=" + fixtureSHA256,
		"prompt_sha256=" + digest(fixedPrompt), "keyword_sha256=" + digest(fixedKeyword), "language_sha256=" + digest(fixedLanguage), "reference_sha256=" + referenceSHA256,
	}, "\n")
}

func priceDeclaration(model, probe string) string {
	if model == TranscribeModel && probe == "transcribe-file" {
		return strings.Join([]string{"official-pricing-declaration-v1", "source=" + priceSourceURL, "revision=" + priceSourceRevision, "model=" + model, "unit=usd_per_minute", "rate=0.0045"}, "\n")
	}
	return strings.Join([]string{"official-pricing-declaration-v1", "source=https://developers.openai.com/api/docs/pricing", "revision=" + priceSourceRevision, "model=" + model, "operation=" + probe, "pricing=not_applicable_to_access_preflight"}, "\n")
}

func priceSourceURLFor(model, probe string) string {
	if model == TranscribeModel && probe == "transcribe-file" {
		return priceSourceURL
	}
	return "https://developers.openai.com/api/docs/pricing"
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxProbeBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func loadApprovedWAV(path, expected string) ([]byte, time.Duration, string, error) {
	if !validDigest(expected) {
		return nil, 0, "", errors.New("--expected-fixture-sha256 must be a 64-character SHA-256 digest")
	}
	data, err := readRegularFile(path, MaxProbeBytes)
	if err != nil {
		return nil, 0, "", err
	}
	digest := digestBytes(data)
	if !strings.EqualFold(digest, expected) {
		return nil, 0, "", errors.New("--audio-file does not match --expected-fixture-sha256")
	}
	duration, err := wavDurationBytes(data)
	if err != nil {
		return nil, 0, "", err
	}
	return data, duration, digest, nil
}

func loadApprovedReference(path, expected string) ([]byte, error) {
	if !validDigest(expected) {
		return nil, errors.New("--expected-reference-sha256 must be a 64-character SHA-256 digest")
	}
	data, err := readRegularFile(path, MaxReferenceBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("--reference-file must be UTF-8 text without NUL bytes")
	}
	if !strings.EqualFold(digestBytes(data), expected) {
		return nil, errors.New("--reference-file does not match --expected-reference-sha256")
	}
	return data, nil
}

func readRegularDigest(path string, max int64, text bool) (string, error) {
	data, err := readRegularFile(path, max)
	if err != nil {
		return "", err
	}
	if text && (!utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0) {
		return "", errors.New("must be UTF-8 text without NUL bytes")
	}
	return digestBytes(data), nil
}

func readRegularFile(path string, max int64) ([]byte, error) {
	if path == "" {
		return nil, errors.New("required regular non-symlink file path is missing")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path must be a regular non-symlink file")
	}
	if info.Size() <= 0 || info.Size() > max {
		return nil, fmt.Errorf("file must be between 1 and %d bytes", max)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, errors.New("file changed while being read")
	}
	return data, nil
}

func validateBaseURL(base string) error {
	if base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("invalid API base: %w", err)
	}
	if u.Scheme == "https" && u.Host == "api.openai.com" && strings.TrimRight(u.Path, "/") == "/v1" {
		return nil
	}
	host := u.Hostname()
	if (host == "localhost" || net.ParseIP(host).IsLoopback()) && (u.Scheme == "http" || u.Scheme == "https") {
		return nil
	}
	return errors.New("API base must be https://api.openai.com/v1 (loopback is test-only)")
}

func optionalDigest(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return digest(value)
}

func verifyAttribution(receipt *Receipt) error {
	response := receipt.ResponseProjectSHA256
	if response == "" {
		return nil
	}
	if receipt.RequestProjectSHA256 == "" || receipt.ExpectedProjectSHA256 == "" {
		// A project-scoped credential binds the request to one provider project,
		// but an unsolicited response echo cannot identify the intended project
		// without an independently expected raw ID. Retain only its hash and keep
		// the honest unreconciled state.
		return nil
	}
	if response != receipt.ExpectedProjectSHA256 || response != receipt.RequestProjectSHA256 {
		return errors.New("provider project attribution echo did not match request-bound project")
	}
	receipt.AttributionVerified = true
	receipt.AttributionState = "provider_verified"
	return nil
}

func credentialScope(cfg Config) string {
	if strings.TrimSpace(cfg.Project) != "" {
		return "explicit_project_header"
	}
	return "project_scoped_api_key"
}

func initialAttributionState(cfg Config) string {
	if strings.TrimSpace(cfg.Project) != "" {
		return "request_bound"
	}
	return "project_credential_bound_unreconciled"
}

func costFor(d time.Duration) float64 { return d.Seconds() / 60 * transcribeUSDPerMinute }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func digestBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func validDigest(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil
}

func failureClass(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_or_project_access"
	case http.StatusNotFound:
		return "model_or_endpoint_access"
	case http.StatusTooManyRequests:
		return "rate_limit_or_quota"
	default:
		return "provider_http_error"
	}
}
