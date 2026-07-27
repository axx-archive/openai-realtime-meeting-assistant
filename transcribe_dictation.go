package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// transcribe_dictation.go — POST /assistant/transcribe.
//
// Dictation is the mobile app's answer to "why not just use Apple's keyboard
// dictation": Apple's is fast, private, and generic — it has never heard of this
// company, so it writes teammates' names and product names wrong, which is most
// of what work chat actually contains. This endpoint spends a network round trip
// to buy correctness on exactly those words, by running the upload through the
// SAME domain-vocabulary biasing the authoritative meeting transcript lane uses
// (domain_terms.go), then applying the known-mistranscription corrections on the
// way out.
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
)

type dictationTranscriptResponse struct {
	Text       string `json:"text"`
	DurationMS int64  `json:"durationMs"`
	Model      string `json:"model"`
	// Biased reports whether company-vocabulary biasing actually applied. The
	// whisper family rejects the prompt parameter, so a whisper pin silently
	// degrades dictation to generic transcription — surfacing it here means the
	// client can tell the difference instead of guessing why names are wrong.
	Biased bool `json:"biased"`
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

	r.Body = http.MaxBytesReader(w, r.Body, dictationMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAuthError(w, http.StatusRequestEntityTooLarge, "recording is too long")
			return
		}
		writeAuthError(w, http.StatusBadRequest, "could not read the upload form")
		return
	}
	// ParseMultipartForm spills parts over the in-memory threshold to $TMPDIR
	// files that are NOT auto-removed; the long-lived VPS exhausts /tmp without
	// this (same reason files.go does it).
	defer func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}()

	part, header, err := r.FormFile("audio")
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "upload form needs an audio field")
		return
	}
	defer part.Close()

	audio, err := io.ReadAll(part)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the recording")
		return
	}
	if len(audio) == 0 {
		writeAuthError(w, http.StatusBadRequest, "the recording is empty")
		return
	}
	if len(audio) > dictationMaxBytes {
		writeAuthError(w, http.StatusRequestEntityTooLarge, "recording is too long")
		return
	}

	filename := strings.TrimSpace(header.Filename)
	if filename == "" {
		filename = "dictation.m4a"
	}

	// `context` ("chat" | "board" | "search") is accepted and currently unused:
	// every dictation gets the full company vocabulary. It is carried now so the
	// client contract is stable when per-surface vocabulary narrowing lands, and
	// is deliberately NOT wired to a narrower prompt yet — a half-applied bias
	// would silently make some surfaces transcribe worse than others.
	_ = r.FormValue("context")

	model := transcriptionLaneModel()
	// Same gate as the streaming lane: the gpt-4o transcription family accepts a
	// free-text prompt for vocabulary biasing; the whisper family rejects it live
	// with "The 'prompt' parameter is not supported for this model". Sending it
	// unconditionally would 400 every dictation on a whisper-pinned deploy.
	biased := transcriptionModelAcceptsPrompt(model)
	prompt := ""
	if biased {
		prompt = realtimeTranscriptionPrompt()
	}

	started := time.Now()
	text, err := openAITranscribeAudio(r.Context(), audio, filename, model, prompt)
	if err != nil {
		// Bill the attempt: a failed call still burned latency and may have
		// burned vendor time, and a silent hole here hides 429 storms from the
		// rollup alerts.
		recordLLMUsage(llmUsageEntry{
			Provider:     providerOpenAI,
			Model:        model,
			Seat:         seatDictation,
			AudioSeconds: dictationSeconds(r.FormValue("durationMs")),
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

	seconds := dictationSeconds(r.FormValue("durationMs"))
	recordLLMUsage(llmUsageEntry{
		Provider:     providerOpenAI,
		Model:        model,
		Seat:         seatDictation,
		ThreadID:     strings.TrimSpace(r.FormValue("threadId")),
		AudioSeconds: seconds,
		DurationMS:   time.Since(started).Milliseconds(),
		WireSuccess:  true,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dictationTranscriptResponse{
		Text:       text,
		DurationMS: int64(seconds * 1000),
		Model:      model,
		Biased:     biased,
	})
}

// dictationSeconds parses the client-reported recording duration and clamps it
// to the accepted bound.
//
// The duration is client-reported because the gpt-4o transcription family only
// supports `json`/`text` response formats — `verbose_json`, the field that
// carries a server-side duration, is whisper-1 only. Decoding m4a server-side to
// recover it would be a codec dependency for a billing figure. Every caller is
// an authenticated member of this company's roster and the value is clamped, so
// the exposure is a member under-reporting their own dictation minutes; that is
// an acceptable trade for not carrying an audio decoder.
func dictationSeconds(raw string) float64 {
	ms, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || ms <= 0 {
		return 0
	}
	seconds := ms / 1000
	if seconds > dictationMaxSeconds {
		return dictationMaxSeconds
	}
	return seconds
}

// openAITranscribeAudio posts one complete recording to the audio transcriptions
// endpoint and returns the text. `prompt` carries domain-vocabulary biasing and
// MUST be empty for models that reject it (see the caller's gate).
func openAITranscribeAudio(ctx context.Context, audio []byte, filename, model, prompt string) (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not configured")
	}

	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	filePart, err := form.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("build transcription form: %w", err)
	}
	if _, err := filePart.Write(audio); err != nil {
		return "", fmt.Errorf("write transcription audio: %w", err)
	}
	if err := form.WriteField("model", model); err != nil {
		return "", fmt.Errorf("write transcription model: %w", err)
	}
	if err := form.WriteField("language", "en"); err != nil {
		return "", fmt.Errorf("write transcription language: %w", err)
	}
	if strings.TrimSpace(prompt) != "" {
		if err := form.WriteField("prompt", prompt); err != nil {
			return "", fmt.Errorf("write transcription prompt: %w", err)
		}
	}
	if err := form.Close(); err != nil {
		return "", fmt.Errorf("close transcription form: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/audio/transcriptions", body)
	if err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", form.FormDataContentType())

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
		return "", fmt.Errorf("transcription failed (%d): %s", response.StatusCode, strings.TrimSpace(string(raw)))
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
var dictationHTTPClient = &http.Client{Timeout: 90 * time.Second}
