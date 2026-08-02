package main

// Deterministic rich responses for the real public Scout turn. File and direct
// GIF requests remain explicit. The one contextual exception is a closed set
// of deictic social prompts in #team (for example, "@scout what did you think
// of that?") where exactly one safe reaction is present in the replied-to or
// immediately preceding human message. No model output can select a file or
// cause a GIF post, and uncertain context produces a clarification instead of
// a guessed side effect. The existing rich-action services still perform every
// source, destination, revision, audience, and exactly-once check at use time.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type scoutChatCommitMessages func(...scoutChatMessageRecord) (scoutChatThreadRecord, error)

type strideScoutChatRichPlan struct {
	kind          string
	responseMode  STRIDEScoutResponseMode
	file          *assistantFileRecord
	reaction      string
	tone          string
	clarification string
}

func (app *kanbanBoardApp) handleExplicitSTRIDEScoutChatRichAction(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, source scoutChatMessageRecord, commit scoutChatCommitMessages) (map[string]any, bool, error) {
	plan, explicit := app.planExplicitSTRIDEScoutChatRichAction(ctx, user, thread, source)
	if !explicit {
		return nil, false, nil
	}
	if commit == nil {
		return nil, true, ErrSTRIDECoworkerDenied
	}
	if plan.clarification != "" {
		answer := strideScoutDeterministicMessage(plan.clarification)
		saved, err := commit(source, answer)
		if err != nil {
			return nil, true, err
		}
		return map[string]any{
			"ok": true, "message": source, "answer": answer, "thread": saved,
			"responseMode": plan.responseMode, "providerCalls": 0, "providerExecutionFenced": true,
		}, true, nil
	}

	// The user's explicit command must be durable before its authorized
	// response. If the downstream reauthorization fails, the command remains
	// visible and no guessed/fallback rich action runs.
	saved, err := commit(source)
	if err != nil {
		return nil, true, err
	}
	productContext, err := admittedSTRIDECoworkerProduct(app.strideRuntime)
	if err != nil {
		return nil, true, err
	}
	product, err := app.strideCoworkerProduct()
	if err != nil {
		return nil, true, err
	}

	switch plan.kind {
	case "file":
		if plan.file == nil || plan.responseMode != STRIDEScoutResponseFileCard {
			return nil, true, ErrSTRIDECoworkerDenied
		}
		fileSource, err := app.resolveSTRIDECoworkerFileSource(ctx, user, plan.file.ID)
		if err != nil {
			return nil, true, err
		}
		authority := strideCoworkerFileAuthority{app: app}
		destination, err := authority.CurrentDestination(ctx, saved.ID)
		if err != nil {
			return nil, true, err
		}
		service := product.fileService(productContext.Config.Now)
		token, err := service.Mint(ctx, STRIDEFileSelectionMintRequest{
			Requester: user.Email, Source: fileSource.Object, SourceRevision: fileSource.Revision,
			Destination: destination, Purpose: "share_existing_file", TTL: 5 * time.Minute,
		})
		if err != nil {
			return nil, true, err
		}
		executionKey := "scout-chat-file:" + source.ID + ":" + plan.file.ID
		receipt, err := service.Post(ctx, token.ID, user.Email, executionKey)
		if err != nil {
			return nil, true, err
		}
		current, _, err := app.scoutChatThreadByID(user.Email, saved.ID)
		if err != nil {
			return nil, true, err
		}
		index := scoutChatMessageIndex(current, receipt.MessageID)
		if index < 0 {
			return nil, true, ErrSTRIDECoworkerConflict
		}
		answer := current.Messages[index]
		return map[string]any{
			"ok": true, "message": source, "answer": answer, "thread": current, "file": *plan.file,
			"responseMode": plan.responseMode, "providerCalls": 0, "providerExecutionFenced": true,
		}, true, nil
	case "gif":
		if plan.responseMode != STRIDEScoutResponseGIFOnly {
			return nil, true, ErrSTRIDECoworkerDenied
		}
		executionKey := "scout-chat-gif:" + source.ID + ":" + plan.reaction + ":" + plan.tone
		answer, replayed, action, err := product.postLocalGIF(ctx, user, saved, source, plan.reaction, plan.tone, executionKey)
		if err != nil {
			return nil, true, err
		}
		current, _, err := app.scoutChatThreadByID(user.Email, saved.ID)
		if err != nil {
			return nil, true, err
		}
		return map[string]any{
			"ok": true, "message": source, "answer": answer, "thread": current, "replayed": replayed,
			"gif":          map[string]any{"provider": action.Provider, "rating": action.Rating, "immutable": action.Immutable},
			"responseMode": plan.responseMode, "providerCalls": 0, "providerExecutionFenced": true,
		}, true, nil
	default:
		return nil, true, ErrSTRIDECoworkerDenied
	}
}

func (app *kanbanBoardApp) planExplicitSTRIDEScoutChatRichAction(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, source scoutChatMessageRecord) (strideScoutChatRichPlan, bool) {
	if app == nil || user == nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic || thread.ArchivedAt != "" ||
		!scoutChatThreadAllowsViewer(thread, user.Email) || normalizeAccountEmail(source.AuthorEmail) != normalizeAccountEmail(user.Email) ||
		!scoutChatMessageMentionsScout(source) {
		return strideScoutChatRichPlan{}, false
	}
	fileRequest := strideExplicitScoutFileRequest(source.Text)
	directGIFRequest := strideExplicitScoutGIFRequest(source.Text)
	contextualGIFRequest := thread.Table && !directGIFRequest && strideExplicitScoutContextualGIFPrompt(source.Text)
	gifRequest := directGIFRequest || contextualGIFRequest
	if !fileRequest && !gifRequest {
		return strideScoutChatRichPlan{}, false
	}
	member := accountStore().findUser(user.Email) != nil
	if fileRequest && gifRequest {
		return strideScoutChatRichPlan{responseMode: STRIDEScoutResponseText, clarification: "Tell me which one you want first: attach a file or reply with a GIF."}, true
	}
	if app.strideRuntime == nil {
		return strideScoutChatRichPlan{responseMode: STRIDEScoutResponseSafeRefusal, clarification: "I can’t safely post that rich response in this channel yet."}, true
	}
	if _, err := admittedSTRIDECoworkerProduct(app.strideRuntime); err != nil {
		return strideScoutChatRichPlan{responseMode: STRIDEScoutResponseSafeRefusal, clarification: "I can’t safely post that rich response in this channel yet."}, true
	}

	if fileRequest {
		matches := app.matchExplicitSTRIDEScoutFile(ctx, user, source.Text)
		if len(matches) != 1 {
			copy := "I couldn’t identify one exact file safely. Say its full name and I’ll attach it."
			if len(matches) > 1 {
				copy = "I found more than one matching file. Say the full file name and I’ll attach the right one."
			}
			return strideScoutChatRichPlan{kind: "file", responseMode: STRIDEScoutResponseText, clarification: copy}, true
		}
		mode := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{Member: member, ConsentAllowed: true, AuthorizedFile: true})
		if mode != STRIDEScoutResponseFileCard {
			return strideScoutChatRichPlan{kind: "file", responseMode: STRIDEScoutResponseSafeRefusal, clarification: "I can’t safely attach that file here."}, true
		}
		selected := matches[0]
		return strideScoutChatRichPlan{kind: "file", responseMode: mode, file: &selected}, true
	}

	contextText := strideScoutGIFContext(thread, source)
	if contextualGIFRequest {
		var ok bool
		contextText, ok = strideScoutImmediateSocialContext(thread, source)
		if !ok {
			return strideScoutChatRichPlan{kind: "gif", responseMode: STRIDEScoutResponseText, clarification: "I’m not sure what you want me to react to. Reply to the message, or give me a reaction cue."}, true
		}
	}
	if sensitiveSTRIDEGIFContext(strings.ToLower(contextText)) {
		return strideScoutChatRichPlan{kind: "gif", responseMode: STRIDEScoutResponseSafeRefusal, clarification: "I’ll keep this one text-only."}, true
	}
	var reaction, tone string
	var ok bool
	if contextualGIFRequest {
		reaction, tone, ok = strideScoutContextualGIFSemantics(contextText)
	} else {
		reaction, tone, ok = strideScoutGIFSemantics(contextText)
	}
	if !ok {
		copy := "Which reaction should I use: laugh, celebrate, agree, surprised, encourage, facepalm, or thank-you?"
		if contextualGIFRequest {
			copy = "I’m not confident enough to turn that into a reaction. Give me a cue, or ask me to answer in words."
		}
		return strideScoutChatRichPlan{kind: "gif", responseMode: STRIDEScoutResponseText, clarification: copy}, true
	}
	if contextualGIFRequest && unsafeSTRIDEContextualGIFReaction(contextText, reaction) {
		return strideScoutChatRichPlan{kind: "gif", responseMode: STRIDEScoutResponseSafeRefusal, clarification: "I’ll keep this one text-only."}, true
	}
	mode := ChooseSTRIDEScoutResponseMode(STRIDEScoutResponseRequest{
		Member: member, ConsentAllowed: true, Social: true, GIFAllowed: true, ChannelGIFAllowed: thread.Table, RequestGIFOnly: true,
	})
	if mode != STRIDEScoutResponseGIFOnly {
		return strideScoutChatRichPlan{kind: "gif", responseMode: STRIDEScoutResponseSafeRefusal, clarification: "I can’t safely use a GIF in this channel."}, true
	}
	return strideScoutChatRichPlan{kind: "gif", responseMode: mode, reaction: reaction, tone: tone}, true
}

func strideScoutDeterministicMessage(text string) scoutChatMessageRecord {
	return scoutChatMessageRecord{
		ID: fmt.Sprintf("scout-chat-message-%d", time.Now().UTC().UnixNano()), Kind: "message", Role: "scout",
		Text: strings.TrimSpace(text), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorName: scoutParticipantName,
	}
}

func strideExplicitScoutFileRequest(text string) bool {
	lower := strings.ToLower(text)
	if !scoutChatMentionsScout(lower) || (!containsWordBoundedPhrase(lower, "file") && !containsWordBoundedPhrase(lower, "files") && !strings.Contains(lower, "from drive")) {
		return false
	}
	for _, verb := range []string{"share", "send", "post", "drop", "attach", "add", "put"} {
		if containsWordBoundedPhrase(lower, verb) {
			return true
		}
	}
	return false
}

func strideExplicitScoutGIFRequest(text string) bool {
	lower := strings.ToLower(text)
	if !scoutChatMentionsScout(lower) || (!containsWordBoundedPhrase(lower, "gif") && !containsWordBoundedPhrase(lower, "giphy")) {
		return false
	}
	for _, verb := range []string{"send", "post", "drop", "reply", "respond", "answer", "react", "use"} {
		if containsWordBoundedPhrase(lower, verb) {
			return true
		}
	}
	return false
}

// strideExplicitScoutContextualGIFPrompt is deliberately phrased as an exact,
// human-authored deictic question. It must not convert general questions such
// as "what did you think of that meeting?" into a GIF side effect.
func strideExplicitScoutContextualGIFPrompt(text string) bool {
	if !scoutChatMentionsScout(text) {
		return false
	}
	normalized := strings.ToLower(text)
	normalized = strings.NewReplacer(
		"@scout", " ", "?", " ", "!", " ", ".", " ", ",", " ", ":", " ", ";", " ",
	).Replace(normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")
	return oneOf(normalized,
		"what did you think of that",
		"what'd you think of that",
		"what do you think of that",
		"thoughts on that",
		"your reaction to that",
		"what was your reaction to that",
		"how would you react to that",
		"how do you feel about that",
	)
}

func (app *kanbanBoardApp) matchExplicitSTRIDEScoutFile(ctx context.Context, user *userAccount, text string) []assistantFileRecord {
	type scored struct {
		row   assistantFileRecord
		score int
	}
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	normalized := strings.NewReplacer(".", " ", "_", " ", "-", " ", "/", " ").Replace(lower)
	var candidates []scored
	for _, row := range app.assistantFilesForPrincipal(ctx, user) {
		name := strings.ToLower(strings.TrimSpace(row.Name))
		stem := strings.ToLower(strings.TrimSuffix(row.Name, filepath.Ext(row.Name)))
		score := 0
		switch {
		case name != "" && strings.Contains(lower, name):
			score = 100
		case len(stem) >= 3 && strings.Contains(lower, stem):
			score = 80
		default:
			tokens := uniqueMemoryTokens(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(stem))
			meaningful := 0
			matched := 0
			for _, token := range tokens {
				if len(token) < 3 || oneOf(token, "file", "final", "copy") {
					continue
				}
				meaningful++
				if containsWordBoundedPhrase(normalized, token) {
					matched++
				}
			}
			if meaningful > 0 && matched == meaningful {
				score = 60 + matched
			}
		}
		if score > 0 {
			candidates = append(candidates, scored{row: row, score: score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].row.ID < candidates[j].row.ID
	})
	if len(candidates) == 0 {
		return nil
	}
	top := candidates[0].score
	result := make([]assistantFileRecord, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.score != top {
			break
		}
		result = append(result, candidate.row)
	}
	return result
}

func strideScoutGIFContext(thread scoutChatThreadRecord, source scoutChatMessageRecord) string {
	parts := []string{source.Text}
	index := scoutChatMessageIndex(thread, source.ID)
	if index < 0 {
		index = len(thread.Messages)
	}
	for prior := index - 1; prior >= 0 && len(parts) < 4; prior-- {
		message := thread.Messages[prior]
		if message.Role == "user" && strings.TrimSpace(message.Text) != "" {
			parts = append(parts, message.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// strideScoutImmediateSocialContext returns only context the member explicitly
// pointed at or the immediately preceding human turn. It never skips over a
// Scout/system turn to hunt for a more GIF-friendly message, and it never uses
// attachment metadata, derived text, or a post made on somebody's behalf.
func strideScoutImmediateSocialContext(thread scoutChatThreadRecord, source scoutChatMessageRecord) (string, bool) {
	if source.ReplyTo != nil && strings.TrimSpace(source.ReplyTo.MessageID) != "" {
		index := scoutChatMessageIndex(thread, source.ReplyTo.MessageID)
		if index < 0 || !strideScoutHumanSocialContext(thread.Messages[index]) {
			return "", false
		}
		expected, err := scoutChatReplyRefFromThread(thread, source.ReplyTo.MessageID)
		if err != nil || expected == nil || expected.MessageID != source.ReplyTo.MessageID || expected.AuthorName != source.ReplyTo.AuthorName ||
			normalizeAccountEmail(expected.AuthorEmail) != normalizeAccountEmail(source.ReplyTo.AuthorEmail) || expected.Text != source.ReplyTo.Text {
			return "", false
		}
		text := strings.TrimSpace(source.ReplyTo.Text)
		if text == "" {
			return "", false
		}
		return text, true
	}
	if len(thread.Messages) == 0 {
		return "", false
	}
	message := thread.Messages[len(thread.Messages)-1]
	if !strideScoutHumanSocialContext(message) {
		return "", false
	}
	return strings.TrimSpace(message.Text), true
}

func strideScoutHumanSocialContext(message scoutChatMessageRecord) bool {
	return strings.EqualFold(strings.TrimSpace(message.Role), "user") &&
		oneOf(strings.ToLower(strings.TrimSpace(message.Kind)), "", "message") &&
		normalizeAccountEmail(message.AuthorEmail) != "" &&
		accountStore().findUser(message.AuthorEmail) != nil &&
		strings.TrimSpace(message.PostedOnBehalfOf) == "" &&
		strings.TrimSpace(message.Text) != ""
}

type strideScoutGIFSemanticRule struct {
	terms    []string
	reaction string
	tone     string
}

func strideScoutGIFSemanticRules() []strideScoutGIFSemanticRule {
	return []strideScoutGIFSemanticRule{
		{[]string{"facepalm", "ridiculous", "absurd", "seriously", "unbelievable"}, "facepalm", "dry"},
		{[]string{"funny", "laugh", "hilarious", "lol", "lmao"}, "laugh", "playful"},
		{[]string{"celebrate", "congrats", "congratulations", "win", "shipped", "nailed it"}, "celebrate", "playful"},
		{[]string{"thank you", "thanks", "appreciate"}, "thank_you", "warm"},
		{[]string{"agree", "exactly", "yes", "correct"}, "agree", "warm"},
		{[]string{"surprised", "surprise", "wow", "wild"}, "surprised", "playful"},
		{[]string{"encourage", "you got this", "keep going", "good luck"}, "encourage", "supportive"},
	}
}

func strideScoutGIFSemantics(text string) (string, string, bool) {
	lower := strings.ToLower(text)
	for _, candidate := range strideScoutGIFSemanticRules() {
		for _, term := range candidate.terms {
			if containsWordBoundedPhrase(lower, term) {
				return candidate.reaction, candidate.tone, true
			}
		}
	}
	return "", "", false
}

// Contextual selection requires exactly one semantic class. A direct GIF
// command can disambiguate with words such as "facepalm GIF"; a deictic social
// prompt cannot, so mixed signals fail closed instead of depending on rule
// order. Additional event-shaped cues cover a few common, low-risk #team jokes
// without pretending to understand arbitrary ridicule.
func strideScoutContextualGIFSemantics(text string) (string, string, bool) {
	lower := strings.ToLower(text)
	matches := map[string]strideScoutGIFSemanticRule{}
	for _, candidate := range strideScoutGIFSemanticRules() {
		for _, term := range candidate.terms {
			if containsWordBoundedPhrase(lower, term) {
				matches[candidate.reaction] = candidate
				break
			}
		}
	}
	if len(matches) == 0 {
		for _, cue := range []string{"what could go wrong", "works on my machine", "skip qa", "without qa", "delete the tests", "deploy on friday"} {
			if containsWordBoundedPhrase(lower, cue) {
				matches["facepalm"] = strideScoutGIFSemanticRule{reaction: "facepalm", tone: "dry"}
				break
			}
		}
	}
	if len(matches) != 1 {
		return "", "", false
	}
	for _, match := range matches {
		return match.reaction, match.tone, true
	}
	return "", "", false
}

// A negative reaction must stay aimed at an event or idea, never at a person.
// These checks are intentionally conservative: contextual personality is an
// optional delight, while turning a coworker's name, mention, identity, or an
// insult into ridicule would violate the human-control boundary.
func unsafeSTRIDEContextualGIFReaction(text, reaction string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, term := range []string{"idiot", "stupid", "moron", "dumb", "incompetent", "pathetic", "loser", "ugly", "hate you", "racist", "sexist", "trash person"} {
		if containsWordBoundedPhrase(lower, term) {
			return true
		}
	}
	if !oneOf(reaction, "facepalm", "laugh") {
		return false
	}
	if strings.Contains(lower, "@") {
		return true
	}
	for _, term := range []string{"you are", "you're", "your idea", "he is", "he's", "she is", "she's", "they are", "they're", "that guy", "that woman", "that person", "someone is", "someone said"} {
		if containsWordBoundedPhrase(lower, term) {
			return true
		}
	}
	for _, account := range seededAccounts {
		name := strings.ToLower(strings.TrimSpace(account.Name))
		if len(name) >= 2 && containsWordBoundedPhrase(lower, name) {
			return true
		}
		if email := normalizeAccountEmail(account.Email); email != "" && strings.Contains(lower, email) {
			return true
		}
	}
	return false
}
