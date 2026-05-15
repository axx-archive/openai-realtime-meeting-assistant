package main

import (
	"os"
	"regexp"
	"strings"
)

const defaultRealtimeTranscriptionModel = "gpt-4o-transcribe"

var domainTermCorrections = []struct {
	pattern *regexp.Regexp
	value   string
}{
	{regexp.MustCompile(`(?i)\bsuit\s+barn\b`), "Boot Barn"},
	{regexp.MustCompile(`(?i)\bboot\s+burn\b`), "Boot Barn"},
	{regexp.MustCompile(`(?i)\bboot\s*borne\b`), "Boot Barn"},
	{regexp.MustCompile(`(?i)\bboot\s*bourne\b`), "Boot Barn"},
	{regexp.MustCompile(`(?i)\bbon\s+fire\b`), "Bonfire"},
	{regexp.MustCompile(`(?i)\bopen\s+ai\b`), "OpenAI"},
	{regexp.MustCompile(`(?i)\bdigital\s+ocean\b`), "DigitalOcean"},
	{regexp.MustCompile(`(?i)\bweb[\s.-]*r[\s.-]*t[\s.-]*c\b`), "WebRTC"},
	{regexp.MustCompile(`(?i)\bh[\s.-]*e[\s.-]*v[\s.-]*c\b`), "HEVC"},
	{regexp.MustCompile(`(?i)\bd[\s.-]*t[\s.-]*l[\s.-]*s\b`), "DTLS"},
	{regexp.MustCompile(`(?i)\bs[\s.-]*r[\s.-]*t[\s.-]*p\b`), "SRTP"},
	{regexp.MustCompile(`(?i)\br[\s.-]*t[\s.-]*p\b`), "RTP"},
	{regexp.MustCompile(`(?i)\bn[\s.-]*a[\s.-]*c[\s.-]*k\b`), "NACK"},
}

var meetingDomainVocabulary = []string{
	"Boot Barn",
	"The Bonfire",
	"Bonfire",
	"Scout",
	"Shareability",
	"OpenAI",
	"OpenAI Realtime",
	"Realtime API",
	"WebRTC",
	"Pion WebRTC",
	"Opus",
	"RTP",
	"NACK",
	"ICE",
	"DTLS",
	"SRTP",
	"HEVC",
	"Simulcast",
	"Caddy",
	"DigitalOcean",
	"VPS",
	"Kanban",
}

func realtimeTranscriptionModel() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_REALTIME_TRANSCRIPTION_MODEL")); model != "" {
		return model
	}

	return defaultRealtimeTranscriptionModel
}

func realtimeTranscriptionPrompt() string {
	return strings.Join([]string{
		"Transcribe this live standup in English with maximum accuracy.",
		"Preserve exact proper nouns, brand names, participant names, acronyms, and technical terms.",
		"Never substitute a nearby-sounding brand or project name when a known term fits the audio.",
		"Important correction: Boot Barn is a known brand; do not write Suit Barn for it.",
		"Keywords: " + strings.Join(domainVocabulary(), ", ") + ".",
	}, " ")
}

func domainVocabulary() []string {
	terms := make([]string, 0, len(meetingParticipantNames)+len(meetingDomainVocabulary))
	terms = append(terms, meetingParticipantNames...)
	terms = append(terms, meetingDomainVocabulary...)

	return uniqueStrings(terms)
}

func canonicalizeDomainTerms(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	for _, correction := range domainTermCorrections {
		value = correction.pattern.ReplaceAllString(value, correction.value)
	}

	return value
}

func canonicalizeBoardText(value string) string {
	return strings.Join(strings.Fields(canonicalizeDomainTerms(value)), " ")
}

func canonicalizeBoardTags(values []string) []string {
	canonicalValues := make([]string, 0, len(values))
	for _, value := range values {
		if canonicalValue := canonicalizeBoardText(value); canonicalValue != "" {
			canonicalValues = append(canonicalValues, canonicalValue)
		}
	}

	return uniqueStrings(canonicalValues)
}
