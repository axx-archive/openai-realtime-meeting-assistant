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

var (
	leadingNarratedRequestSentencePattern = regexp.MustCompile(`(?i)^\s*user\s+(?:requested|asked)(?:\s+scout)?(?:\s+to)?\s+[^.?!]*[.?!]\s*`)
	leadingUserNarrationPattern           = regexp.MustCompile(`(?i)^\s*user\s+(?:said|says|requested|asked)(?:\s+scout)?(?:\s+to|that)?\s+`)
	boardOnlyRequestPattern               = regexp.MustCompile(`(?i)^(?:add(?:ing)?|create|creating|put|place|placing)\s+["']?[^"'.]+["']?\s+(?:to|on)\s+the\s+board\.?$`)
	leadingStatusLabelPattern             = regexp.MustCompile(`(?i)^\s*(?:blocked|done|in progress|backlog)\s*:\s*`)
)

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

// recallSynonymGroups is the curated synonym table that powers query
// expansion (Wave 7). Every token in a group is treated as interchangeable at
// recall time, so a question asked in one vocabulary still surfaces an entry
// written in another ("runway" ⇆ "cash-out"). Seeded from the studio's
// packaging-company vocabulary plus the domain acronyms; kept deliberately
// small and lowercase (matched against uniqueMemoryTokens, ≥3 chars). Extend
// here — there is no model call in the search path.
var recallSynonymGroups = [][]string{
	{"runway", "cash", "cashout", "burn"},
	{"comp", "comparable", "comparables", "compset", "benchmark"},
	{"revenue", "sales", "topline"},
	{"acquisition", "cac"},
	{"churn", "attrition"},
	{"objective", "goal", "mission", "aim"},
	{"deck", "pitch", "presentation"},
	{"hire", "hiring", "headcount", "recruit"},
	{"launch", "ship", "release", "shipping"},
	{"pricing", "price"},
	{"customer", "client", "buyer"},
	{"competitor", "competition", "rival"},
	{"decision", "decided", "call"},
	{"timeline", "schedule", "roadmap"},
	{"budget", "spend", "cost"},
}

// recallSynonymMap indexes recallSynonymGroups token → the other tokens in its
// group, built once at init.
var recallSynonymMap = buildRecallSynonymMap()

func buildRecallSynonymMap() map[string][]string {
	index := map[string][]string{}
	for _, group := range recallSynonymGroups {
		for _, token := range group {
			for _, other := range group {
				if other == token {
					continue
				}
				index[token] = append(index[token], other)
			}
		}
	}
	return index
}

// expandRecallSynonyms returns the synonyms of the supplied query tokens that
// are NOT already query tokens themselves, deduplicated. The caller ORs them
// into token matching at a lower weight than the raw tokens.
func expandRecallSynonyms(queryTokens []string) []string {
	if len(queryTokens) == 0 {
		return nil
	}
	present := make(map[string]struct{}, len(queryTokens))
	for _, token := range queryTokens {
		present[token] = struct{}{}
	}
	expanded := make([]string, 0, len(queryTokens))
	seen := map[string]struct{}{}
	for _, token := range queryTokens {
		for _, synonym := range recallSynonymMap[token] {
			if _, isQueryToken := present[synonym]; isQueryToken {
				continue
			}
			if _, already := seen[synonym]; already {
				continue
			}
			seen[synonym] = struct{}{}
			expanded = append(expanded, synonym)
		}
	}
	return expanded
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

func cleanBoardNotes(value string) string {
	notes := strings.Trim(canonicalizeBoardText(value), "\"'")
	if notes == "" {
		return ""
	}

	if leadingNarratedRequestSentencePattern.MatchString(notes) {
		cleanedNotes := strings.TrimSpace(leadingNarratedRequestSentencePattern.ReplaceAllString(notes, ""))
		if cleanedNotes == "" {
			return ""
		}
		notes = cleanedNotes
	}
	notes = strings.TrimSpace(leadingUserNarrationPattern.ReplaceAllString(notes, ""))
	if boardOnlyRequestPattern.MatchString(notes) {
		return ""
	}
	notes = strings.TrimSpace(leadingStatusLabelPattern.ReplaceAllString(notes, ""))

	return capitalizeLeadingASCII(strings.Trim(notes, "\"'"))
}

func capitalizeLeadingASCII(value string) string {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return value
	}

	return strings.ToUpper(value[:1]) + value[1:]
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
