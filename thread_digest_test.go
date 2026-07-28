package main

import (
	"strings"
	"testing"
)

func digestMessage(id, author, text, createdAt string) scoutChatMessageRecord {
	return scoutChatMessageRecord{
		ID:         id,
		Role:       "user",
		Text:       text,
		AuthorName: author,
		CreatedAt:  createdAt,
	}
}

// ── Deposits ────────────────────────────────────────────────────────────────

// "I know we shared it here somewhere" is the everything-channel's core failure.
// Files are already on the message record; they just were never surfaced.
func TestThreadDepositsCollectsFilesFromMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "here it is", "2026-07-28T10:00:00Z"),
		{
			ID:         "2",
			Role:       "user",
			Text:       "the brief",
			AuthorName: "Dana",
			CreatedAt:  "2026-07-28T10:01:00Z",
			Files: []scoutChatFileAttachment{
				{Name: "pricing-memo.pdf", Mime: "application/pdf", Ref: "blob-1"},
			},
		},
	}
	deposits := threadDeposits(messages)
	if len(deposits.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(deposits.Files))
	}
	if deposits.Files[0].Name != "pricing-memo.pdf" || deposits.Files[0].MessageID != "2" {
		t.Fatalf("file = %+v, want the pdf anchored to message 2", deposits.Files[0])
	}
}

func TestThreadDepositsExtractsLinks(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "see https://figma.com/file/abc for the mock", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "and http://example.com/spec", "2026-07-28T10:01:00Z"),
	}
	deposits := threadDeposits(messages)
	if len(deposits.Links) != 2 {
		t.Fatalf("links = %d, want 2: %+v", len(deposits.Links), deposits.Links)
	}
	if deposits.Links[0].URL != "https://figma.com/file/abc" {
		t.Fatalf("url = %q", deposits.Links[0].URL)
	}
	// The host is what a chip can actually show — a full URL truncates to
	// nothing useful in a 90pt chip.
	if deposits.Links[0].Host != "figma.com" {
		t.Fatalf("host = %q, want figma.com", deposits.Links[0].Host)
	}
}

// Trailing punctuation is part of the sentence, not the URL. A link chip that
// 404s because it captured the full stop is worse than no chip.
func TestThreadDepositsTrimsTrailingPunctuationFromLinks(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "look at https://example.com/spec.", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "or (https://example.com/other)", "2026-07-28T10:01:00Z"),
	}
	deposits := threadDeposits(messages)
	if deposits.Links[0].URL != "https://example.com/spec" {
		t.Fatalf("url = %q, want the trailing period stripped", deposits.Links[0].URL)
	}
	if deposits.Links[1].URL != "https://example.com/other" {
		t.Fatalf("url = %q, want the closing paren stripped", deposits.Links[1].URL)
	}
}

// The same link pasted five times is one deposit. A rail that repeats is a rail
// nobody scans.
func TestThreadDepositsDeduplicatesLinks(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "https://example.com/a", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "https://example.com/a again", "2026-07-28T10:01:00Z"),
	}
	if got := len(threadDeposits(messages).Links); got != 1 {
		t.Fatalf("links = %d, want 1", got)
	}
}

// An empty rail is chrome that narrates its own emptiness — the client only
// renders when there is something, so the server must report emptiness plainly.
func TestThreadDepositsAreEmptyWhenNothingWasShared(t *testing.T) {
	messages := []scoutChatMessageRecord{digestMessage("1", "Dana", "morning", "2026-07-28T10:00:00Z")}
	deposits := threadDeposits(messages)
	if deposits.Any() {
		t.Fatalf("deposits = %+v, want empty", deposits)
	}
}

// ── Catch-up ────────────────────────────────────────────────────────────────

// Every bullet must be a REAL message, carrying its id. This is the extractive
// discipline composeEvidenceLinkedCatchUp enforces for rooms: a recap that
// paraphrases a colleague inaccurately is worse than no recap, because it gets
// quoted back at them.
func TestThreadCatchUpBulletsAreRealMessagesWithIds(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "We're going with the tiered pricing for launch", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "ok", "2026-07-28T10:01:00Z"),
	}
	recap := threadCatchUp(messages, "", "aj@x.com", 5)
	if len(recap.Bullets) == 0 {
		t.Fatal("no bullets")
	}
	for _, bullet := range recap.Bullets {
		found := false
		for _, message := range messages {
			if message.ID == bullet.MessageID && strings.Contains(message.Text, bullet.Text) {
				found = true
			}
		}
		if !found {
			t.Fatalf("bullet %+v is not a verbatim slice of any real message", bullet)
		}
	}
}

// Compression is the whole point. "ok", "thanks", a lone emoji carry nothing,
// and a recap made of them is just the thread again.
func TestThreadCatchUpDropsFillerMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "ok", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "thanks!", "2026-07-28T10:01:00Z"),
		digestMessage("3", "Erick", "👍", "2026-07-28T10:02:00Z"),
		digestMessage("4", "Dana", "We decided to ship the tiered pricing on Tuesday", "2026-07-28T10:03:00Z"),
	}
	recap := threadCatchUp(messages, "", "aj@x.com", 5)
	if len(recap.Bullets) != 1 || recap.Bullets[0].MessageID != "4" {
		t.Fatalf("bullets = %+v, want only the substantive message", recap.Bullets)
	}
}

func TestThreadCatchUpOnlyCoversUnreadMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "This was already read and is quite substantive", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "This one is new and also quite substantive", "2026-07-28T11:00:00Z"),
	}
	recap := threadCatchUp(messages, "2026-07-28T10:30:00Z", "aj@x.com", 5)
	if len(recap.Bullets) != 1 || recap.Bullets[0].MessageID != "2" {
		t.Fatalf("bullets = %+v, want only the unread message", recap.Bullets)
	}
}

// Your own messages are not news to you.
func TestThreadCatchUpExcludesTheViewersOwnMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		{
			ID: "1", Role: "user", Text: "I shipped the tiered pricing change today",
			AuthorName: "AJ", AuthorEmail: "aj@x.com", CreatedAt: "2026-07-28T10:00:00Z",
		},
	}
	if got := len(threadCatchUp(messages, "", "aj@x.com", 5).Bullets); got != 0 {
		t.Fatalf("bullets = %d, want 0", got)
	}
}

// A cap that silently drops the rest would misrepresent the thread as smaller
// than it is, so the count has to be reported.
func TestThreadCatchUpReportsWhatItLeftOut(t *testing.T) {
	messages := []scoutChatMessageRecord{}
	for index := 0; index < 12; index++ {
		messages = append(messages, digestMessage(
			string(rune('a'+index)),
			"Dana",
			"A genuinely substantive message about the pricing work",
			"2026-07-28T10:0"+string(rune('0'+index%10))+":00Z",
		))
	}
	recap := threadCatchUp(messages, "", "aj@x.com", 5)
	if len(recap.Bullets) != 5 {
		t.Fatalf("bullets = %d, want the cap of 5", len(recap.Bullets))
	}
	if recap.TotalUnread != 12 {
		t.Fatalf("totalUnread = %d, want 12", recap.TotalUnread)
	}
}

func TestThreadCatchUpHeadlineNamesTheParticipants(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "The pricing memo is ready for review", "2026-07-28T10:00:00Z"),
		digestMessage("2", "Tim", "I pushed the tiered model into the deck", "2026-07-28T10:01:00Z"),
	}
	recap := threadCatchUp(messages, "", "aj@x.com", 5)
	if !strings.Contains(recap.Headline, "Dana") || !strings.Contains(recap.Headline, "Tim") {
		t.Fatalf("headline = %q, want both speakers named", recap.Headline)
	}
}

func TestThreadCatchUpIsEmptyWhenNothingIsUnread(t *testing.T) {
	messages := []scoutChatMessageRecord{
		digestMessage("1", "Dana", "Something substantive about pricing", "2026-07-28T10:00:00Z"),
	}
	recap := threadCatchUp(messages, "2026-07-28T11:00:00Z", "aj@x.com", 5)
	if len(recap.Bullets) != 0 || recap.Headline != "" {
		t.Fatalf("recap = %+v, want empty", recap)
	}
}
