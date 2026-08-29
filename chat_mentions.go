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
		handleEnd := end
		for handleEnd > index+1 && runes[handleEnd-1] == '.' {
			handleEnd--
		}
		if handleEnd == index+1 {
			continue
		}
		mentions = append(mentions, chatMentionToken{handle: strings.ToLower(string(runes[index+1 : handleEnd]))})
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

func strideAgentMentionHandles(name string) []string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(name)))
	if len(parts) == 0 {
		return nil
	}
	handles := []string{parts[0]}
	if len(parts) > 1 {
		handles = append(handles, strings.Join(parts, "-"), strings.Join(parts, "_"), strings.Join(parts, ""))
	}
	return uniqueSortedStrings(handles)
}

func chatAgentWorkWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// chatAgentExplicitWorkAction recognizes explicit authored work verbs without
// treating a social mention or a noun such as "the research looks good" as
// launch intent. The result is still only proposal intent; this function never
// grants execution authority.
func chatAgentExplicitWorkAction(text string, targetHandles []string) string {
	words := chatAgentWorkWords(text)
	handle := map[string]bool{}
	for _, value := range targetHandles {
		handle[value] = true
	}
	for index, word := range words {
		if word == "create" || word == "prepare" || word == "build" || word == "design" || word == "make" {
			for _, object := range words[index+1:] {
				if object == "presentation" || object == "deck" || object == "slides" {
					return word
				}
			}
		}
		if (word == "dig" || word == "look") && index+1 < len(words) && words[index+1] == "into" {
			return word + " into"
		}
		if word == "search" && index+1 < len(words) && (words[index+1] == "web" || words[index+1] == "the" && index+2 < len(words) && words[index+2] == "web") {
			return "search the web"
		}
		if word != "research" && word != "investigate" && word != "review" && word != "analyze" && word != "analyse" {
			continue
		}
		if index+1 < len(words) && (words[index+1] == "is" || words[index+1] == "was" || words[index+1] == "looks" || words[index+1] == "looked" || words[index+1] == "seems" || words[index+1] == "moved" || words[index+1] == "finished") {
			continue
		}
		imperative := index == 0
		if index > 0 {
			previous := words[index-1]
			imperative = handle[previous] || previous == "please" || previous == "you" || previous == "to"
		}
		if !imperative && index > 1 {
			previous := words[index-2]
			imperative = previous == "can" || previous == "could" || previous == "would" || previous == "will" || previous == "should"
		}
		// Natural teammate asks often put a small verb phrase between the modal
		// and the exact capability word: "@Colton can you run a quick research
		// report". Keep the window bounded and, for the ambiguous noun
		// "research", require an authored execution verb so "should we discuss
		// the research?" stays conversational.
		if !imperative {
			windowStart := index - 8
			if windowStart < 0 {
				windowStart = 0
			}
			modal := false
			researchVerb := word != "research"
			for _, prior := range words[windowStart:index] {
				if prior == "can" || prior == "could" || prior == "would" || prior == "will" || prior == "should" {
					modal = true
				}
				if prior == "do" || prior == "run" || prior == "conduct" || prior == "prepare" || prior == "create" || prior == "produce" || prior == "write" || prior == "compile" || prior == "pull" {
					researchVerb = true
				}
			}
			imperative = modal && researchVerb
		}
		if imperative {
			return word
		}
	}
	return ""
}

func chatAgentWorkRequestIsBounded(text string, files []scoutChatFileAttachment, replyTo *scoutChatReplyRef) bool {
	if len(files) > 0 || replyTo != nil || strings.Contains(strings.ToLower(text), "http://") || strings.Contains(strings.ToLower(text), "https://") {
		return true
	}
	objectWords := map[string]bool{
		"article": true, "brief": true, "company": true, "competitor": true, "document": true, "industry": true,
		"deck": true, "link": true, "market": true, "opportunity": true, "page": true, "post": true, "presentation": true,
		"report": true, "slides": true, "source": true, "topic": true,
	}
	for _, word := range chatAgentWorkWords(text) {
		if objectWords[word] {
			return true
		}
	}
	return !directResearchRequestNeedsInput(text, files, nil)
}

// strideTargetedAgentWorkRequest resolves an authored @Agent mention into a
// currently valid, channel-authorized profile only when the same message also
// contains an explicit bounded work ask. Social mentions intentionally return
// false and continue down the ordinary human-chat path.
func (app *kanbanBoardApp) strideTargetedAgentWorkRequest(thread scoutChatThreadRecord, text string, files []scoutChatFileAttachment, replyTo *scoutChatReplyRef) (STRIDEProductAgentContextProfile, string, bool) {
	mentions := parseChatMentionTokens(text)
	if len(mentions) == 0 || !chatAgentWorkRequestIsBounded(text, files, replyTo) {
		return STRIDEProductAgentContextProfile{}, "", false
	}
	profilesByHandle := map[string][]STRIDEProductAgentContextProfile{}
	for _, profile := range app.strideMentionableAgentProfiles() {
		for _, handle := range strideAgentMentionHandles(profile.DisplayName) {
			profilesByHandle[handle] = append(profilesByHandle[handle], profile)
		}
	}
	for _, mention := range mentions {
		matches := profilesByHandle[mention.handle]
		if len(matches) != 1 {
			continue
		}
		profile := matches[0]
		handles := strideAgentMentionHandles(profile.DisplayName)
		action := chatAgentExplicitWorkAction(text, handles)
		if action == "" {
			return STRIDEProductAgentContextProfile{}, "", false
		}
		mode := "research"
		if containsSTRIDEID(profile.Capabilities, "presentation_deck") || containsSTRIDEID(profile.Capabilities, "design_brief") {
			mode = "design"
		}
		current, ok := app.strideAgentContextForChatWork(profile.AgentID, thread, mode)
		if !ok {
			return STRIDEProductAgentContextProfile{}, "", false
		}
		return current, mode, true
	}
	return STRIDEProductAgentContextProfile{}, "", false
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
