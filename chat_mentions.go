package main

import (
	"strings"
	"unicode"
)

// chat_mentions.go — @-mention parsing for public chat channels. Mentioning a
// roster member creates a targeted bell notification (model-free, so the path
// is keyless-safe); @scout is deliberately NOT a notification target — that
// mention gates the answer path via scoutChatMentionsScout instead.

// chatMentionNames returns the canonical roster names @-mentioned in text, in
// first-appearance order, deduped. A mention is an "@" at a word boundary (the
// rune before it must not be a name rune, so emails like aj@shareability.com
// never count) followed by a roster name that ends at a word boundary
// (case-insensitive: "@tyler," hits, "@Tylerish" does not).
func chatMentionNames(text string) []string {
	seen := map[string]struct{}{}
	names := []string{}
	runes := []rune(text)
	for index := 0; index < len(runes); index++ {
		if runes[index] != '@' {
			continue
		}
		if index > 0 && isChatMentionNameRune(runes[index-1]) {
			continue
		}
		end := index + 1
		for end < len(runes) && isChatMentionNameRune(runes[end]) {
			end++
		}
		if end == index+1 {
			continue
		}
		name := canonicalParticipantName(string(runes[index+1 : end]))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func isChatMentionNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// notifyScoutChatMentions posts one targeted, thread-deep-linked bell
// notification per roster member @-mentioned in a committed public-channel
// message. Self-mentions never notify, and unresolvable names (guests, typos)
// are silently skipped. Callers invoke this only after the message persisted —
// a rejected send must never ring anyone's bell.
func (app *kanbanBoardApp) notifyScoutChatChannelMessage(thread scoutChatThreadRecord, message scoutChatMessageRecord) {
	if app == nil {
		return
	}
	authorEmail := normalizeAccountEmail(message.AuthorEmail)
	author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), "Someone")
	excluded := []string{authorEmail}
	mentions := []string{}
	for _, name := range chatMentionNames(message.Text) {
		email := participantEmail(name)
		if email == "" || email == authorEmail {
			continue
		}
		excluded = append(excluded, email)
		mentions = append(mentions, email)
	}
	if thread.Table {
		ambientText := author + " posted in #" + thread.Title + ": " + trimForStorage(message.Text, 140)
		if _, err := app.createChatNotification("", excluded, ambientText, thread, message); err != nil {
			log.Errorf("Failed to create Table notification: %v", err)
		}
	}
	for _, email := range mentions {
		text := author + " mentioned you in #" + thread.Title + ": " + trimForStorage(message.Text, 140)
		if _, err := app.createChatNotification(email, nil, text, thread, message); err != nil {
			log.Errorf("Failed to create mention notification for %s: %v", email, err)
		}
	}
}
