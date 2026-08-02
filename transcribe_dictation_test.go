package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDictationModelIsIndependentButPreservesIncumbentWhenUnset(t *testing.T) {
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "gpt-realtime-whisper")
	t.Setenv("OPENAI_DICTATION_TRANSCRIPT_MODEL", "")
	if got := dictationTranscriptionModel(); got != "gpt-realtime-whisper" {
		t.Fatalf("unset dictation model=%q, want incumbent transcript lane", got)
	}
	t.Setenv("OPENAI_DICTATION_TRANSCRIPT_MODEL", "gpt-transcribe")
	if got := dictationTranscriptionModel(); got != "gpt-transcribe" {
		t.Fatalf("qualified dictation model=%q", got)
	}
}

func TestDictationTranscriptionFieldsAreCapabilityAware(t *testing.T) {
	modern := dictationTranscriptionFields("gpt-transcribe", "company context")
	if !dictationHasField(modern, "languages[]", "en") || !dictationHasField(modern, "keywords[]", "STRIDE") ||
		!dictationHasField(modern, "prompt", "company context") || dictationHasFieldName(modern, "language") {
		t.Fatalf("modern fields=%#v", modern)
	}
	legacy := dictationTranscriptionFields("gpt-realtime-whisper", "")
	want := []dictationTranscriptionField{{Name: "model", Value: "gpt-realtime-whisper"}, {Name: "language", Value: "en"}}
	if !reflect.DeepEqual(legacy, want) {
		t.Fatalf("legacy fields=%#v, want %#v", legacy, want)
	}
}

func TestCommittedTurnTranscriptionConfigUsesModernHintsWithoutInventedNoiseControl(t *testing.T) {
	config := transcriptionLaneSessionConfig("gpt-transcribe")
	session := config["session"].(map[string]any)
	audio := session["audio"].(map[string]any)
	input := audio["input"].(map[string]any)
	transcription := input["transcription"].(map[string]any)
	if transcription["model"] != "gpt-transcribe" || transcription["prompt"] == "" {
		t.Fatalf("modern transcription config=%#v", transcription)
	}
	if _, found := transcription["language"]; found {
		t.Fatalf("modern config must not send legacy singular language: %#v", transcription)
	}
	if got, ok := transcription["languages"].([]string); !ok || !reflect.DeepEqual(got, []string{"en"}) {
		t.Fatalf("modern languages=%#v", transcription["languages"])
	}
	if keywords, ok := transcription["keywords"].([]string); !ok || len(keywords) == 0 {
		t.Fatalf("modern keywords=%#v", transcription["keywords"])
	}
	if _, found := input["noise_reduction"]; found {
		t.Fatal("gpt-transcribe near-field support is not assumed before E10 compatibility testing")
	}
}

func TestDictationCandidateIsVisibleAndPricedWithoutQualificationClaim(t *testing.T) {
	t.Setenv("OPENAI_REALTIME_MODEL", "gpt-realtime-2")
	t.Setenv("OPENAI_REALTIME_REASONING_EFFORT", "low")
	t.Setenv("OPENAI_TRANSCRIPT_MODEL", "gpt-4o-transcribe")
	t.Setenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL", "gpt-4o-transcribe")
	t.Setenv("OPENAI_DICTATION_TRANSCRIPT_MODEL", "gpt-transcribe")
	snapshot := telemetryLaneSnapshot()
	if snapshot["dictation_model"] != "gpt-transcribe" || snapshot["dictation_vocab"] != true {
		t.Fatalf("dictation telemetry=%#v", snapshot)
	}
	warnings := validateRealtimeConfig()
	for _, warning := range warnings {
		if strings.Contains(warning, "OPENAI_DICTATION_TRANSCRIPT_MODEL") || strings.Contains(warning, "gpt-transcribe") {
			t.Fatalf("priced dictation candidate produced a stale pricing warning: %#v", warnings)
		}
	}
}

func dictationHasField(fields []dictationTranscriptionField, name, value string) bool {
	for _, field := range fields {
		if field.Name == name && field.Value == value {
			return true
		}
	}
	return false
}

func dictationHasFieldName(fields []dictationTranscriptionField, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func TestServerDerivedDictationSecondsFromM4AWebMAndWAV(t *testing.T) {
	floatPayload := make([]byte, 8)
	binary.BigEndian.PutUint64(floatPayload, math.Float64bits(2500))
	webMInfo := append(ebmlDictationElement([]byte{0x2a, 0xd7, 0xb1}, []byte{0x0f, 0x42, 0x40}), ebmlDictationElement([]byte{0x44, 0x89}, floatPayload)...)
	webM := append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x80}, ebmlDictationElement([]byte{0x18, 0x53, 0x80, 0x67}, ebmlDictationElement([]byte{0x15, 0x49, 0xa9, 0x66}, webMInfo))...)

	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 2500)
	m4a := append(mp4DictationAtom("ftyp", nil), mp4DictationAtom("moov", mp4DictationAtom("mvhd", mvhd))...)

	// Keep the payload larger than the two MiB inspection window. The data
	// chunk header remains in the probe, as it does in a normal WAV file.
	wav := make([]byte, dictationDurationProbeMax+4096)
	copy(wav[:12], []byte("RIFF\x00\x00\x00\x00WAVE"))
	copy(wav[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint32(wav[28:32], 16000)
	copy(wav[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(wav)-44))

	cases := []struct {
		name string
		file []byte
		ext  string
		want float64
	}{
		{name: "m4a", file: m4a, ext: "dictation.m4a", want: 2.5},
		{name: "webm", file: webM, ext: "dictation.webm", want: 2.5},
		{name: "wav beyond probe", file: wav, ext: "dictation.wav", want: float64(len(wav)-44) / 16000},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			file := writeDictationDurationFile(t, test.file)
			got, estimated, err := serverDerivedDictationSeconds(file, test.ext)
			if err != nil || estimated || math.Abs(got-test.want) > 0.001 {
				t.Fatalf("duration=%v estimated=%v err=%v, want %v exact", got, estimated, err, test.want)
			}
		})
	}
}

func TestServerDerivedDictationSecondsFailsConservatively(t *testing.T) {
	file := writeDictationDurationFile(t, []byte("not an audio container"))
	seconds, estimated, err := serverDerivedDictationSeconds(file, "dictation.webm")
	if err != nil || !estimated || seconds != dictationMaxSeconds {
		t.Fatalf("unknown container duration=%v estimated=%v err=%v", seconds, estimated, err)
	}

	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1)
	binary.BigEndian.PutUint32(mvhd[16:20], uint32(dictationMaxSeconds+6))
	file = writeDictationDurationFile(t, append(mp4DictationAtom("ftyp", nil), mp4DictationAtom("moov", mp4DictationAtom("mvhd", mvhd))...))
	_, _, err = serverDerivedDictationSeconds(file, "dictation.m4a")
	if !errors.Is(err, errDictationTooLarge) {
		t.Fatalf("overlong m4a err=%v, want size/duration rejection", err)
	}
}

func TestDictationQuotaManagerEnforcesConcurrencyRateAndBudget(t *testing.T) {
	manager := newDictationQuotaManager()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	release, err := manager.acquire("AJ@shareability.com", now)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := manager.acquire("aj@shareability.com", now); !errors.Is(err, errDictationConcurrent) {
		t.Fatalf("second acquire err=%v, want concurrency limit", err)
	}
	release()

	for request := 1; request < dictationRequestsPerHour; request++ {
		release, err := manager.acquire("aj@shareability.com", now)
		if err != nil {
			t.Fatalf("request %d: %v", request, err)
		}
		release()
	}
	if _, err := manager.acquire("aj@shareability.com", now); !errors.Is(err, errDictationRate) {
		t.Fatalf("rate limit err=%v, want request limit", err)
	}

	manager = newDictationQuotaManager()
	release, err = manager.acquire("aj@shareability.com", now)
	if err != nil {
		t.Fatalf("budget acquire: %v", err)
	}
	defer release()
	if err := manager.charge("aj@shareability.com", now, dictationSecondsPerDay-1); err != nil {
		t.Fatalf("budget charge: %v", err)
	}
	if err := manager.charge("aj@shareability.com", now, 2); !errors.Is(err, errDictationBudget) {
		t.Fatalf("over-budget err=%v, want daily budget", err)
	}
}

func TestDictationQuotaManagerConcurrentAcquireIsAtomic(t *testing.T) {
	manager := newDictationQuotaManager()
	const workers = 32
	start := make(chan struct{})
	events := make(chan bool, workers)
	releaseGate := make(chan struct{})
	var completed sync.WaitGroup
	var successes atomic.Int32
	for worker := 0; worker < workers; worker++ {
		completed.Add(1)
		go func() {
			defer completed.Done()
			<-start
			release, err := manager.acquire("aj@shareability.com", time.Now())
			events <- err == nil
			if err == nil {
				successes.Add(1)
				<-releaseGate
				release()
			}
		}()
	}
	close(start)
	for worker := 0; worker < workers; worker++ {
		<-events
	}
	close(releaseGate)
	completed.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("concurrent acquires succeeded=%d, want exactly 1", got)
	}
}

func TestSpoolDictationIsSeekableAndEnforcesTheAudioByteLimit(t *testing.T) {
	file, size, err := spoolDictation(strings.NewReader("audio"))
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	t.Cleanup(func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	})
	if size != 5 {
		t.Fatalf("spooled size=%d, want 5", size)
	}
	got, err := io.ReadAll(file)
	if err != nil || string(got) != "audio" {
		t.Fatalf("spooled contents=%q err=%v", got, err)
	}
	if _, _, err := spoolDictation(io.LimitReader(dictationZeroReader{}, int64(dictationMaxBytes)+1)); !errors.Is(err, errDictationTooLarge) {
		t.Fatalf("oversize spool err=%v, want size rejection", err)
	}
}

func TestStreamDictationUploadStreamsOneAudioPartAndBoundsMetadata(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("context", "chat"); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("threadId", "thread-42"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("audio", "../dictation.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("audio")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/assistant/transcribe", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	audio, filename, contextValue, threadID, err := streamDictationUpload(request)
	if err != nil {
		t.Fatalf("stream upload: %v", err)
	}
	t.Cleanup(func() {
		_ = audio.Close()
		_ = os.Remove(audio.Name())
	})
	got, err := io.ReadAll(audio)
	if err != nil || string(got) != "audio" || filename != "dictation.m4a" || contextValue != "chat" || threadID != "thread-42" {
		t.Fatalf("audio=%q filename=%q context=%q thread=%q err=%v", got, filename, contextValue, threadID, err)
	}

	var oversized bytes.Buffer
	overForm := multipart.NewWriter(&oversized)
	if err := overForm.WriteField("context", strings.Repeat("x", dictationMetadataMaxBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := overForm.Close(); err != nil {
		t.Fatal(err)
	}
	overRequest := httptest.NewRequest(http.MethodPost, "/assistant/transcribe", &oversized)
	overRequest.Header.Set("Content-Type", overForm.FormDataContentType())
	if _, _, _, _, err := streamDictationUpload(overRequest); !errors.Is(err, errDictationUpload) {
		t.Fatalf("oversized metadata err=%v, want upload rejection", err)
	}
}

func TestAssistantTranscribeHandlerFailsClosedBeforeAnyProviderWork(t *testing.T) {
	method := httptest.NewRequest(http.MethodGet, "/assistant/transcribe", nil)
	methodResult := httptest.NewRecorder()
	assistantTranscribeHandler(methodResult, method)
	if methodResult.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d, want %d", methodResult.Code, http.StatusMethodNotAllowed)
	}

	unauthenticated := httptest.NewRequest(http.MethodPost, "/assistant/transcribe", strings.NewReader("not multipart"))
	unauthenticatedResult := httptest.NewRecorder()
	assistantTranscribeHandler(unauthenticatedResult, unauthenticated)
	if unauthenticatedResult.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d, want %d", unauthenticatedResult.Code, http.StatusUnauthorized)
	}
}

type dictationRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn dictationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAssistantTranscribeRejectsBlankProviderSuccessSoClientsRetainAudio(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	previousClient := dictationHTTPClient
	previousQuotas := dictationQuotas
	dictationQuotas = newDictationQuotaManager()
	dictationHTTPClient = &http.Client{Transport: dictationRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"text":"   "}`)),
			Request:    request,
		}, nil
	})}
	t.Cleanup(func() {
		dictationHTTPClient = previousClient
		dictationQuotas = previousQuotas
	})

	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 2500)
	audio := append(mp4DictationAtom("ftyp", nil), mp4DictationAtom("moov", mp4DictationAtom("mvhd", mvhd))...)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("audio", "dictation.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(audio); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/assistant/transcribe", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	assistantTranscribeHandler(result, request)

	if result.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank transcript status=%d body=%s, want 422", result.Code, result.Body.String())
	}
	if !strings.Contains(result.Body.String(), "recording can be retried") {
		t.Fatalf("blank transcript response=%s, want retryable guidance", result.Body.String())
	}
}

func TestAssistantTranscribeAcceptedOutputAccounting(t *testing.T) {
	setupAuthTestEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_DICTATION_TRANSCRIPT_MODEL", "gpt-transcribe")
	t.Setenv("MEETING_ALLOWED_ORIGINS", "")
	ledgerDir := realtimeLedgerSetup(t)
	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")

	previousClient := dictationHTTPClient
	previousQuotas := dictationQuotas
	dictationQuotas = newDictationQuotaManager()
	dictationHTTPClient = &http.Client{Transport: dictationRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"text":"STRIDE launch"}`)),
			Request:    request,
		}, nil
	})}
	t.Cleanup(func() {
		dictationHTTPClient = previousClient
		dictationQuotas = previousQuotas
	})

	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 2500)
	audio := append(mp4DictationAtom("ftyp", nil), mp4DictationAtom("moov", mp4DictationAtom("mvhd", mvhd))...)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("audio", "dictation.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(audio); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/assistant/transcribe", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	assistantTranscribeHandler(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("dictation status=%d body=%s", result.Code, result.Body.String())
	}
	rows := readLedgerLines(t, filepath.Join(ledgerDir, "usage-2026-07-11.jsonl"))
	if len(rows) != 1 || rows[0]["wire_success"] != true || rows[0]["accepted_output"] != true || rows[0]["seat"] != seatDictation {
		t.Fatalf("dictation usage receipt=%#v, want one accepted wire output", rows)
	}
}

type dictationZeroReader struct{}

func (dictationZeroReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = 0
	}
	return len(destination), nil
}

func writeDictationDurationFile(t *testing.T, raw []byte) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "dictation-duration-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func mp4DictationAtom(kind string, payload []byte) []byte {
	raw := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(raw[:4], uint32(len(raw)))
	copy(raw[4:8], kind)
	copy(raw[8:], payload)
	return raw
}

func ebmlDictationElement(id, payload []byte) []byte {
	if len(payload) > 126 {
		panic("test EBML payload exceeds one-byte size")
	}
	raw := make([]byte, 0, len(id)+len(payload)+1)
	raw = append(raw, id...)
	raw = append(raw, byte(0x80|len(payload)))
	raw = append(raw, payload...)
	return raw
}
