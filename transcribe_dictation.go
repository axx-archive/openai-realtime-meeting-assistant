package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// transcribe_dictation.go — POST /assistant/transcribe.
//
// Dictation is the mobile app's answer to "why not just use Apple's keyboard
// dictation": Apple's is fast, private, and generic — it has never heard of this
// company, so it writes teammates' names and product names wrong, which is most
// of what work chat actually contains. This endpoint spends a network round trip
// to buy correctness on exactly those words. The upload uses its own separately
// qualified route, the same domain vocabulary as the authoritative meeting
// transcript lane, and the known-mistranscription corrections on the way out.
//
// This is the file-at-a-time sibling of transcription_lane.go's streaming
// WebSocket lane: dictation is a short, complete utterance, so a single
// /v1/audio/transcriptions call is both simpler and cheaper than standing up a
// realtime session per held button.

const (
	// A held dictation is an utterance, not a meeting. 10 minutes is already
	// far past any real dictation and keeps a stuck recorder from billing a
	// surprise; the client stops at the same bound.
	dictationMaxSeconds = 600
	// 25MB is the OpenAI audio upload cap. m4a at the client's recording
	// bitrate reaches ~10 minutes well inside it.
	dictationMaxBytes = 25 << 20
	// Composer dictation is a completed-file request. Keep its default separate
	// from the live meeting lane, whose model is Realtime-only.
	defaultDictationTranscriptionModel = "gpt-transcribe"
)

var (
	// Dictation in a private Scout composer has one useful bit of surface
	// context the general meeting transcript does not: a leading greeting is
	// almost certainly addressing the assistant. Keep the repair deliberately
	// narrow so a teammate genuinely named Scott is never rewritten in ordinary
	// chat, meetings, or mid-sentence prose.
	leadingScoutGreetingPattern   = regexp.MustCompile(`(?i)^(\s*(?:hey|hi|hello)\s+)scott\b`)
	leadingScoutPunctuatedPattern = regexp.MustCompile(`(?i)^(\s*)scott(\s*[,!?:;])`)
)

type dictationTranscriptResponse struct {
	Text              string `json:"text"`
	DurationMS        int64  `json:"durationMs"`
	DurationEstimated bool   `json:"durationEstimated"`
	Model             string `json:"model"`
	// Biased reports whether company-vocabulary biasing actually applied. The
	// whisper family rejects the prompt parameter, so a whisper pin silently
	// degrades dictation to generic transcription — surfacing it here means the
	// client can tell the difference instead of guessing why names are wrong.
	Biased bool `json:"biased"`
}

type dictationTranscriptionField struct {
	Name  string
	Value string
}

// Dictation intentionally owns a route independent of the meeting transcript
// lane. Until E10 explicitly qualifies and sets the new dial, an unset value
// uses the known-compatible file-transcription default. It must not inherit the
// live transcript lane: that lane can validly use a Realtime-only model such as
// gpt-realtime-whisper, which the file endpoint rejects.
func dictationTranscriptionModel() string {
	return defaultDictationTranscriptionModel
}

func dictationTranscriptionFields(model, prompt string) []dictationTranscriptionField {
	fields := []dictationTranscriptionField{{Name: "model", Value: model}}
	if transcriptionModelUsesModernHints(model) {
		fields = append(fields, dictationTranscriptionField{Name: "languages[]", Value: "en"})
		for _, keyword := range domainVocabulary() {
			fields = append(fields, dictationTranscriptionField{Name: "keywords[]", Value: keyword})
		}
	} else {
		fields = append(fields, dictationTranscriptionField{Name: "language", Value: "en"})
	}
	if strings.TrimSpace(prompt) != "" {
		fields = append(fields, dictationTranscriptionField{Name: "prompt", Value: prompt})
	}
	return fields
}

func scoutDictationContext(contextValue string) bool {
	return strings.EqualFold(strings.TrimSpace(contextValue), "scout")
}

func dictationTranscriptionPrompt(contextValue string) string {
	prompt := realtimeTranscriptionPrompt()
	if scoutDictationContext(contextValue) {
		prompt += " Scout is the assistant's name. When the speaker addresses Scout, spell the name Scout, never Scott."
	}
	return prompt
}

func canonicalizeScoutDictationVocative(value, contextValue string) string {
	if !scoutDictationContext(contextValue) {
		return value
	}
	if leadingScoutGreetingPattern.MatchString(value) {
		return leadingScoutGreetingPattern.ReplaceAllString(value, "${1}Scout")
	}
	return leadingScoutPunctuatedPattern.ReplaceAllString(value, "${1}Scout${2}")
}

func assistantTranscribeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "dictation is unavailable — OPENAI_API_KEY is not configured")
		return
	}
	release, err := dictationQuotas.acquire(user.Email, time.Now())
	if err != nil {
		w.Header().Set("Retry-After", "60")
		writeAuthError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	defer release()

	// Parse the multipart stream directly. ParseMultipartForm retains form values
	// in memory and creates its own file before we make the seekable provider
	// spool, which turns one 25 MB upload into avoidable heap and disk pressure.
	r.Body = http.MaxBytesReader(w, r.Body, dictationMaxBytes+(1<<20))
	audio, filename, contextValue, threadID, err := streamDictationUpload(r)
	var tooLarge *http.MaxBytesError
	if errors.Is(err, errDictationTooLarge) || errors.As(err, &tooLarge) {
		writeAuthError(w, http.StatusRequestEntityTooLarge, "recording is too long")
		return
	}
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the upload form")
		return
	}
	defer audio.Close()
	defer os.Remove(audio.Name())

	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "" {
		filename = "dictation.m4a"
	}
	seconds, durationEstimated, err := serverDerivedDictationSeconds(audio, filename)
	if errors.Is(err, errDictationTooLarge) {
		writeAuthError(w, http.StatusRequestEntityTooLarge, "recording is too long")
		return
	}
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not inspect the recording")
		return
	}
	if err := dictationQuotas.charge(user.Email, time.Now(), seconds); err != nil {
		w.Header().Set("Retry-After", "3600")
		writeAuthError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	model := dictationTranscriptionModel()
	// Same gate as the streaming lane: the gpt-4o transcription family accepts a
	// free-text prompt for vocabulary biasing; the whisper family rejects it live
	// with "The 'prompt' parameter is not supported for this model". Sending it
	// unconditionally would 400 every dictation on a whisper-pinned deploy.
	biased := transcriptionModelAcceptsPrompt(model)
	prompt := ""
	if biased {
		prompt = dictationTranscriptionPrompt(contextValue)
	}

	started := time.Now()
	recordCapabilityPoll(capabilityDictation, started.UTC())
	text, err := openAITranscribeAudio(r.Context(), audio, filename, model, prompt)
	if err != nil {
		recordCapabilityFailure(capabilityDictation, time.Now().UTC(), err)
		// Bill the attempt: a failed call still burned latency and may have
		// burned vendor time, and a silent hole here hides 429 storms from the
		// rollup alerts.
		recordLLMUsage(llmUsageEntry{
			Provider:     providerOpenAI,
			Model:        model,
			Seat:         seatDictation,
			AudioSeconds: seconds,
			DurationMS:   time.Since(started).Milliseconds(),
			Error:        err.Error(),
		})
		writeAuthError(w, http.StatusBadGateway, "could not transcribe the recording")
		return
	}

	// The corrections pass the meeting transcript writer already runs — known
	// mistranscriptions of this company's terms, fixed the same way in both
	// lanes so dictation and transcripts never disagree about a name.
	text = canonicalizeDomainTerms(strings.TrimSpace(text))
	text = canonicalizeScoutDictationVocative(text, contextValue)
	if text == "" {
		recordCapabilityFailure(capabilityDictation, time.Now().UTC(), fmt.Errorf("provider returned an empty transcript"))
		recordLLMUsage(llmUsageEntry{
			Provider:     providerOpenAI,
			Model:        model,
			Seat:         seatDictation,
			ThreadID:     strings.TrimSpace(threadID),
			AudioSeconds: seconds,
			DurationMS:   time.Since(started).Milliseconds(),
			WireSuccess:  true,
			Error:        "provider returned an empty transcript",
		})
		writeAuthError(w, http.StatusUnprocessableEntity, "no speech was detected; the recording can be retried")
		return
	}

	recordLLMUsage(llmUsageEntry{
		Provider:       providerOpenAI,
		Model:          model,
		Seat:           seatDictation,
		ThreadID:       strings.TrimSpace(threadID),
		AudioSeconds:   seconds,
		DurationMS:     time.Since(started).Milliseconds(),
		WireSuccess:    true,
		AcceptedOutput: true,
	})
	recordCapabilitySuccess(capabilityDictation, time.Now().UTC())

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dictationTranscriptResponse{
		Text:              text,
		DurationMS:        int64(seconds * 1000),
		DurationEstimated: durationEstimated,
		Model:             model,
		Biased:            biased,
	})
}

// openAITranscribeAudio posts one complete recording to the audio transcriptions
// endpoint and returns the text. `prompt` carries domain-vocabulary biasing and
// MUST be empty for models that reject it (see the caller's gate).
func openAITranscribeAudio(ctx context.Context, audio io.ReadSeeker, filename, model, prompt string) (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	body, err := os.CreateTemp("", "meetingassist-transcription-form-*")
	if err != nil {
		return "", fmt.Errorf("create transcription form spool: %w", err)
	}
	defer body.Close()
	defer os.Remove(body.Name())
	form := multipart.NewWriter(body)
	filePart, err := form.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("build transcription form: %w", err)
	}
	if _, err := audio.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind transcription audio: %w", err)
	}
	if _, err := io.Copy(filePart, io.LimitReader(audio, dictationMaxBytes+1)); err != nil {
		return "", fmt.Errorf("write transcription audio: %w", err)
	}
	for _, field := range dictationTranscriptionFields(model, prompt) {
		if err := form.WriteField(field.Name, field.Value); err != nil {
			return "", fmt.Errorf("write transcription field %s: %w", field.Name, err)
		}
	}
	if err := form.Close(); err != nil {
		return "", fmt.Errorf("close transcription form: %w", err)
	}

	contentType := form.FormDataContentType()
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind transcription form: %w", err)
	}
	info, err := body.Stat()
	if err != nil {
		return "", fmt.Errorf("stat transcription form: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/transcriptions", body)
	if err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", contentType)
	request.ContentLength = info.Size()

	response, err := dictationHTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("transcription request: %w", err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read transcription response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", apiRequestFailedError("OpenAI transcription failed", response.Status, raw)
	}

	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("decode transcription response: %w", err)
	}
	return decoded.Text, nil
}

// A dictation is short and the user is holding their phone waiting for it, so
// this lane gets a tighter timeout than the long-running agent clients.
var dictationHTTPClient = aiProviderHTTPClient(90 * time.Second)
