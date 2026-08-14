package main

import (
	"sort"
	"strings"
	"unicode"
)

// Ask-the-thread source chips — design §10 of docs/plans/the-table-design.md
// and shell §13.5.
//
// "@scout" answering inside a thread already worked. What did not exist was the
// part that makes the answer trustworthy: showing WHICH messages it is grounded
// in, tappable, so a reader can check the claim instead of taking it.
//
// The tempting cheap version is to cite whatever was in the model's context
// window. That is precisely the overclaim the design forbids — an answer must
// render "with no sources, visibly, rather than borrowing unearned authority."
// Context is not evidence.
//
// So citation here is DETERMINISTIC and PROVABLE: a message is cited only when
// the answer verifiably contains a distinctive run of words from it. No model
// call, no cost, no second opinion to be wrong. It deliberately UNDER-claims —
// a paraphrased answer cites nothing, which is the correct and honest failure
// direction for a trust feature.

type answerSource struct {
	Kind      string `json:"kind,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	MeetingID string `json:"meetingId,omitempty"`
	SegmentID string `json:"segmentId,omitempty"`
	Revision  string `json:"revision,omitempty"`
	At        string `json:"at,omitempty"`
	Author    string `json:"author,omitempty"`
	/** The matched phrase, so a reader can see WHY this was cited. */
	Quote string `json:"quote"`
}

// sourceStopwords are words whose co-occurrence proves nothing. Without this,
// "and we are going to be" would cite every message against every answer —
// citing everything is indistinguishable from citing nothing.
var sourceStopwords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "all": {}, "also": {}, "am": {}, "an": {},
	"and": {}, "any": {}, "are": {}, "as": {}, "at": {}, "be": {}, "been": {},
	"before": {}, "being": {}, "but": {}, "by": {}, "can": {}, "did": {}, "do": {},
	"does": {}, "for": {}, "from": {}, "get": {}, "go": {}, "going": {}, "had": {},
	"has": {}, "have": {}, "he": {}, "her": {}, "here": {}, "him": {}, "his": {},
	"how": {}, "i": {}, "if": {}, "in": {}, "into": {}, "is": {}, "it": {},
	"its": {}, "just": {}, "me": {}, "more": {}, "my": {}, "no": {}, "not": {},
	"of": {}, "on": {}, "one": {}, "or": {}, "our": {}, "out": {}, "over": {},
	"she": {}, "so": {}, "some": {}, "than": {}, "that": {}, "the": {}, "their": {},
	"them": {}, "then": {}, "there": {}, "these": {}, "they": {}, "this": {},
	"to": {}, "too": {}, "up": {}, "us": {}, "was": {}, "we": {}, "were": {},
	"what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {},
	"will": {}, "with": {}, "would": {}, "you": {}, "your": {},
}

// normalizeForGrounding lowercases and strips punctuation so "TIERED PRICING
// MODEL on Tuesday." matches "tiered pricing model on Tuesday!".
func normalizeForGrounding(text string) []string {
	lowered := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text)
	return strings.Fields(lowered)
}

func contentWordCount(words []string) int {
	count := 0
	for _, word := range words {
		if _, stop := sourceStopwords[word]; !stop {
			count++
		}
	}
	return count
}

// longestSharedRun returns the longest contiguous run of words present in both
// slices, along with its content-word count.
//
// Contiguity matters: scattered shared vocabulary is a topic overlap, not a
// quotation. Only an unbroken run is evidence that the answer is repeating
// something the message actually said.
func longestSharedRun(answer, message []string) ([]string, int) {
	if len(answer) == 0 || len(message) == 0 {
		return nil, 0
	}
	// Classic DP longest-common-substring over word slices.
	previous := make([]int, len(message)+1)
	current := make([]int, len(message)+1)
	bestLen, bestEnd := 0, 0

	for i := 1; i <= len(answer); i++ {
		for j := 1; j <= len(message); j++ {
			if answer[i-1] == message[j-1] {
				current[j] = previous[j-1] + 1
				if current[j] > bestLen {
					bestLen = current[j]
					bestEnd = i
				}
			} else {
				current[j] = 0
			}
		}
		previous, current = current, previous
		for k := range current {
			current[k] = 0
		}
	}
	if bestLen == 0 {
		return nil, 0
	}
	run := answer[bestEnd-bestLen : bestEnd]
	return run, contentWordCount(run)
}

// groundAnswerInMessages returns the messages the answer provably quotes.
//
// `minContentWords` is the evidence bar: a shared run must carry at least this
// many non-stopword words to count. Below it, the overlap is coincidence.
func groundAnswerInMessages(
	answer string,
	messages []scoutChatMessageRecord,
	limit int,
) []answerSource {
	sources := []answerSource{}
	answerWords := normalizeForGrounding(answer)
	if len(answerWords) == 0 || len(messages) == 0 {
		return sources
	}

	const minContentWords = 3

	type scored struct {
		source   answerSource
		strength int
	}
	candidates := []scored{}

	for _, message := range messages {
		// Scout citing itself is circular: it proves the model is internally
		// consistent, not that the claim is grounded in what the team said.
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "scout" || role == "assistant" || role == "error" {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}

		run, content := longestSharedRun(answerWords, normalizeForGrounding(text))
		if content < minContentWords {
			continue
		}
		candidates = append(candidates, scored{
			source: answerSource{
				MessageID: message.ID,
				Author:    strings.TrimSpace(message.AuthorName),
				// The matched phrase, so a reader can see WHY this was cited
				// without opening the message.
				Quote: strings.Join(run, " "),
			},
			strength: content,
		})
	}

	// Strongest evidence first; a wall of chips is unreadable, so the cap keeps
	// the best rather than whatever happened to come first.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].strength > candidates[j].strength
	})
	for index, candidate := range candidates {
		if limit > 0 && index >= limit {
			break
		}
		sources = append(sources, candidate.source)
	}
	return sources
}
