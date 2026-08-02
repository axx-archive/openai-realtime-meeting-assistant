package main

import (
	"strings"
	"unicode"
)

// chat_mentions.go — @-mention parsing for public chat channels. Mentioning a
// roster member creates a targeted bell notification (model-free, so the path
// is keyless-safe); @scout is deliberately NOT a notification target — that
// mention gates the answer path via scoutChatMentionsScout instead.

type chatMentionToken struct {
	handle string
}

// parseChatMentionTokens is the single lexical gate for both human bells and
// Scout invocation. It reads authored message text only: attachment names,
// derived file text, GIF metadata, reply snapshots, and source metadata never
// become an invocation channel. Email-like strings, longer handles, escaped
// mentions, code spans/fences, Markdown block quotes, and paired quoted text
// are excluded. The quote exclusions cover the common "someone previously
// wrote '@scout ...'" case without trying to infer intent from free prose.
func parseChatMentionTokens(text string) []chatMentionToken {
	runes := []rune(text)
	mentions := []chatMentionToken{}
	inlineCode := false
	fencedCode := false
	var quoteEnd rune

	for index := 0; index < len(runes); index++ {
		current := runes[index]
		if current == '\n' {
			inlineCode = false
			quoteEnd = 0
			continue
		}
		if current == '`' && !chatRuneEscaped(runes, index) {
			runLength := 1
			for index+runLength < len(runes) && runes[index+runLength] == '`' {
				runLength++
			}
			if runLength >= 3 {
				fencedCode = !fencedCode
				inlineCode = false
			} else if !fencedCode {
				inlineCode = !inlineCode
			}
			index += runLength - 1
			continue
		}
		if fencedCode || inlineCode {
			continue
		}
		if quoteEnd != 0 {
			if current == quoteEnd && !chatRuneEscaped(runes, index) {
				quoteEnd = 0
			}
			continue
		}
		if closing, ok := chatQuoteClosingRune(current); ok && chatHasClosingQuoteOnLine(runes, index+1, closing) {
			quoteEnd = closing
			continue
		}
		if current != '@' || chatRuneEscaped(runes, index) || chatMentionInBlockQuote(runes, index) {
			continue
		}
		if index > 0 && isChatMentionLocalRune(runes[index-1]) {
			continue
		}
		end := index + 1
		for end < len(runes) && isChatMentionHandleRune(runes[end]) {
			end++
		}
		if end == index+1 {
			continue
		}
		mentions = append(mentions, chatMentionToken{handle: strings.ToLower(string(runes[index+1 : end]))})
		index = end - 1
	}
	return mentions
}

func chatRuneEscaped(runes []rune, index int) bool {
	backslashes := 0
	for index--; index >= 0 && runes[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func chatQuoteClosingRune(open rune) (rune, bool) {
	switch open {
	case '"':
		return '"', true
	case '“':
		return '”', true
	case '‘':
		return '’', true
	default:
		return 0, false
	}
}

func chatHasClosingQuoteOnLine(runes []rune, start int, closing rune) bool {
	for index := start; index < len(runes) && runes[index] != '\n'; index++ {
		if runes[index] == closing && !chatRuneEscaped(runes, index) {
			return true
		}
	}
	return false
}

func chatMentionInBlockQuote(runes []rune, index int) bool {
	lineStart := index
	for lineStart > 0 && runes[lineStart-1] != '\n' {
		lineStart--
	}
	for lineStart < index && unicode.IsSpace(runes[lineStart]) && runes[lineStart] != '\n' {
		lineStart++
	}
	return lineStart < index && runes[lineStart] == '>'
}

func isChatMentionHandleRune(r rune) bool {
	return isChatMentionNameRune(r) || r == '_' || r == '-' || r == '.'
}

func isChatMentionLocalRune(r rune) bool {
	return isChatMentionHandleRune(r) || r == '+' || r == '%'
}

// scoutChatMentionsScout gates Scout in public channels. It deliberately takes
// only the human-authored text, so non-text message metadata cannot summon a
// model or launch work.
func scoutChatMentionsScout(text string) bool {
	for _, mention := range parseChatMentionTokens(text) {
		if mention.handle == "scout" {
			return true
		}
	}
	return false
}

func scoutChatMessageMentionsScout(message scoutChatMessageRecord) bool {
	return scoutChatMentionsScout(message.Text)
}

// chatMentionNames returns the canonical roster names @-mentioned in authored
// text, in first-appearance order, deduped. The same parser governs Scout, so
// notification and invocation boundaries cannot drift apart.
func chatMentionNames(text string) []string {
	seen := map[string]struct{}{}
	names := []string{}
	for _, mention := range parseChatMentionTokens(text) {
		name := canonicalParticipantName(mention.handle)
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

// notifyScoutChatTargets posts targeted, thread-deep-linked bell notifications
// for direct reply recipients and @-mentioned roster members. A reply is a
// direct conversation target, so it delivers at the Mentions notification
// level as well. Recipients are deduped, self-targets never notify, and callers
// invoke this only after the message persisted. The Table's ambient broadcast
// excludes the author and every direct target so nobody receives duplicates.
func (app *kanbanBoardApp) notifyScoutChatTargets(thread scoutChatThreadRecord, message scoutChatMessageRecord) {
	if app == nil {
		return
	}
	authorEmail := normalizeAccountEmail(message.AuthorEmail)
	author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), "Someone")
	excluded := []string{authorEmail}
	notified := map[string]struct{}{}
	if message.ReplyTo != nil {
		email := normalizeAccountEmail(message.ReplyTo.AuthorEmail)
		if email != "" && email != authorEmail && scoutChatThreadAllowsViewer(thread, email) {
			excluded = append(excluded, email)
			text := author + " replied to you in #" + thread.Title + ": " + trimForStorage(message.Text, 140)
			if _, err := app.createChatNotification(email, nil, text, thread, message); err != nil {
				log.Errorf("Failed to create reply notification for %s: %v", email, err)
			} else {
				notified[email] = struct{}{}
			}
		}
	}
	for _, name := range chatMentionNames(message.Text) {
		email := participantEmail(name)
		if email == "" || email == authorEmail || !scoutChatThreadAllowsViewer(thread, email) {
			continue
		}
		if _, exists := notified[email]; exists {
			continue
		}
		excluded = append(excluded, email)
		text := author + " mentioned you in #" + thread.Title + ": " + trimForStorage(message.Text, 140)
		if _, err := app.createChatNotification(email, nil, text, thread, message); err != nil {
			log.Errorf("Failed to create mention notification for %s: %v", email, err)
		} else {
			notified[email] = struct{}{}
		}
	}
	if thread.Table {
		ambientText := author + " posted in #" + thread.Title + ": " + trimForStorage(message.Text, 140)
		if _, err := app.createChatNotification("", excluded, ambientText, thread, message); err != nil {
			log.Errorf("Failed to create Table notification: %v", err)
		}
	}
}
