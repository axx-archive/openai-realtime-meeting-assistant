package main

// Packaging Studio chat intake (Wave 11 D11).
//
// Founder intent, verbatim: "if a user asks scout in a chat to do work, it
// should process that request for them in the packaging studio and ask any
// necessary clarifying questions specifically to them in threaded replies to
// that message."
//
// The intake is a seam inside the conversation append path
// (appendScoutChatThreadMessageWithReplyAndTool, right after the explicit
// engagement gate): a work ask that reads as research / presentation /
// document / story becomes a Packaging Studio commission whose brief is
// pre-filled from the message, its reply parent, attachments and the thread.
// briefGaps then returns ONLY the questions whose answer changes the output
// (kind ambiguity, audience, sources, imagery mode for decks, depth for
// research, length for documents). Routine facts the studio definitions say to
// infer (slide count, decision, desired response, brand assets, research
// mode…) are never asked — packaging_studio.go:280 and
// process_definitions.go:988 label their inferences instead.
//
// If any gap remains, Scout posts exactly ONE threaded reply on the asking
// message (replyTo = that message, the asker @-mentioned, ≤3 numbered
// questions with quick-answer options where closed-ended) and the commission
// record waits on the asker. Answers — threaded replies by the asker, or by
// anyone once the asker @scouts, or structured `clarifying.answers` from the
// D13 pills — complete the brief and launch. Public channels keep the
// existing proposal gate for the launch step (the accept route in
// conversation_public_work_launch.go stays the single public launch door);
// private threads launch through the commissionLauncher.
//
// Never a new top-level post. Never a launch from a model argument.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// meetingMemoryKindPackagingIntake is the durable intake record: one row
	// per chat commission, updated in place as answers land. UI/bookkeeping
	// material (isUIStateMemoryKind), never recall knowledge.
	meetingMemoryKindPackagingIntake = "packaging_intake"

	packagingIntakeKindResearch     = "research"
	packagingIntakeKindPresentation = "presentation"
	packagingIntakeKindDocument     = "document"
	packagingIntakeKindStory        = "story"

	packagingIntakeStatusWaiting       = "waiting_on"
	packagingIntakeStatusBriefComplete = "brief_complete"
	packagingIntakeStatusProposed      = "proposed"
	packagingIntakeStatusLaunched      = "launched"
	packagingIntakeStatusFailed        = "failed"

	packagingIntakeMaxQuestions   = 3
	packagingIntakeContextLines   = 12
	packagingIntakeClassifyTokens = 600
	packagingIntakeVia            = "packaging_intake"
	packagingIntakeWorkflow       = "packaging_intake_classify"
)

// packagingIntakeQuestion is one clarifying question. Kind "choice" carries
// quick-answer options the D13 pills render; "text" is open-ended.
type packagingIntakeQuestion struct {
	ID      string   `json:"id"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options,omitempty"`
	Kind    string   `json:"kind,omitempty"` // choice | text
}

// scoutChatClarifying is the wire/storage shape on a Scout message that asks
// clarifying questions (message.clarifying). The frontend renders the
// questions inline with pills where Kind == "choice".
type scoutChatClarifying struct {
	CommissionID  string                    `json:"commissionId"`
	ProjectID     string                    `json:"projectId,omitempty"`
	Questions     []packagingIntakeQuestion `json:"questions"`
	WaitingOn     string                    `json:"waitingOn,omitempty"`
	WaitingOnName string                    `json:"waitingOnName,omitempty"`
}

// scoutChatClarifyingAnswer is one structured answer from the D13 pills,
// posted as `clarifying: {commissionId, answers: [{id, value}]}` beside the
// reply text. Free text in the reply is parsed as well.
type scoutChatClarifyingAnswer struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type scoutChatClarifyingAnswers struct {
	CommissionID string                      `json:"commissionId"`
	Answers      []scoutChatClarifyingAnswer `json:"answers"`
}

type packagingIntakeAnswersContextKey struct{}

func withPackagingIntakeAnswers(ctx context.Context, answers *scoutChatClarifyingAnswers) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if answers == nil {
		return ctx
	}
	return context.WithValue(ctx, packagingIntakeAnswersContextKey{}, answers)
}

func packagingIntakeAnswersFromContext(ctx context.Context) *scoutChatClarifyingAnswers {
	if ctx == nil {
		return nil
	}
	answers, _ := ctx.Value(packagingIntakeAnswersContextKey{}).(*scoutChatClarifyingAnswers)
	return answers
}

// packagingIntakeBrief is the structured brief a chat ask pre-fills. Fields
// map onto the studio briefs: Audience/CopyStyle/Imagery/Length feed the deck
// context_snapshot, Depth/Sources feed research, Length/Sources feed the
// document_report shape.
type packagingIntakeBrief struct {
	Kind           string            `json:"kind"`
	Ask            string            `json:"ask"`
	Audience       string            `json:"audience,omitempty"`
	Sources        []string          `json:"sources,omitempty"`
	SourceMode     string            `json:"sourceMode,omitempty"` // attached | named | infer | none
	Length         string            `json:"length,omitempty"`
	Imagery        string            `json:"imagery,omitempty"` // full-bleed | on-slide | hybrid
	Depth          string            `json:"depth,omitempty"`   // brief | standard | deep
	CopyStyle      string            `json:"copyStyle,omitempty"`
	ThreadContext  []string          `json:"threadContext,omitempty"`
	Attachments    []string          `json:"attachments,omitempty"`
	ContextRefs    []string          `json:"contextRefs,omitempty"`
	ThreadID       string            `json:"threadId"`
	ThreadTitle    string            `json:"threadTitle,omitempty"`
	MessageID      string            `json:"messageId"`
	RequesterEmail string            `json:"requesterEmail"`
	RequesterName  string            `json:"requesterName,omitempty"`
	Answers        map[string]string `json:"answers,omitempty"`
	// Inferred lists the fields Scout filled by inference rather than from the
	// asker's words, so the studio row can label them.
	Inferred []string `json:"inferred,omitempty"`
}

// packagingIntakeRecord is the durable commission intake: what was asked,
// what the brief holds, who Scout is waiting on, and how it ended.
type packagingIntakeRecord struct {
	ID                string                    `json:"id"`
	Kind              string                    `json:"kind"`
	Status            string                    `json:"status"`
	ThreadID          string                    `json:"threadId"`
	ThreadVisibility  string                    `json:"threadVisibility"`
	AskMessageID      string                    `json:"askMessageId"`
	QuestionMessageID string                    `json:"questionMessageId,omitempty"`
	RequesterEmail    string                    `json:"requesterEmail"`
	RequesterName     string                    `json:"requesterName,omitempty"`
	WaitingOn         string                    `json:"waitingOn,omitempty"`
	WaitingOnName     string                    `json:"waitingOnName,omitempty"`
	OpenQuestions     []packagingIntakeQuestion `json:"openQuestions,omitempty"`
	AskedQuestionIDs  []string                  `json:"askedQuestionIds,omitempty"`
	// AskRound counts the clarifying replies Scout has posted for this intake.
	// It keys the question message id: a follow-up that re-asks a SUBSET of
	// the same questions leaves AskedQuestionIDs the same length, so anything
	// derived from that length collides with the first question's id and the
	// thread ends up holding two different messages under one id.
	AskRound          int                  `json:"askRound,omitempty"`
	Brief             packagingIntakeBrief `json:"brief"`
	CommissionID      string               `json:"commissionId,omitempty"`
	ArtifactID        string               `json:"artifactId,omitempty"`
	ProposalMessageID string               `json:"proposalMessageId,omitempty"`
	LaunchMessageID   string               `json:"launchMessageId,omitempty"`
	Error             string               `json:"error,omitempty"`
	Classifier        string               `json:"classifier,omitempty"` // deterministic | model:<model>
	// Fence is the asking thread's recall fence (channelThreadRecallFence),
	// carried on the record because the row quotes up to a dozen verbatim
	// lines of that thread. Stamping it flat "organization" would have let
	// recall read a private thread's brief to every member.
	Fence     map[string]string `json:"fence,omitempty"`
	CreatedAt string            `json:"createdAt"`
	UpdatedAt string            `json:"updatedAt"`
}

func (record packagingIntakeRecord) pending() bool {
	return record.Status == packagingIntakeStatusWaiting
}

// packagingCommissionReceipt is what a launcher hands back: the commission id
// the studio row shows, and (optionally) the running work's chat card.
type packagingCommissionReceipt struct {
	CommissionID string
	ArtifactID   string
	Label        string
	Thread       *scoutChatThreadRef
}

// commissionLauncher is the D9 seam. The Packaging Studio commissions API
// (packaging_commissions.go, owned by the other Wave 11 backend dev) exposes
// createPackagingCommission(principal, kind, brief); until it lands, the chat
// intake codes against this interface and the legacy launcher below routes
// through the existing goal/agent-thread doors.
//
// TODO(w11-backend-a): wire packagingCommissionLauncherFactory to an adapter
// over createPackagingCommission(principal, kind, brief) once
// packaging_commissions.go lands, returning its commission id/artifact id.
type commissionLauncher interface {
	createPackagingCommission(principal *userAccount, kind string, brief packagingIntakeBrief) (packagingCommissionReceipt, error)
}

// packagingCommissionLauncherFactory is the wiring point named above. nil
// means "not wired": the chat intake stays DARK unless PACKAGING_CHAT_INTAKE
// forces it on with the legacy launcher. Wired (the adapter's init), fresh
// asks are intercepted unless PACKAGING_CHAT_INTAKE switches intake off.
var packagingCommissionLauncherFactory func(app *kanbanBoardApp) commissionLauncher

// packagingChatIntakeEnabled reports whether fresh work asks are taken into
// the Packaging Studio from chat. Default: ON whenever a launcher is wired
// (packaging_commission_adapter.go wires it at init). PACKAGING_CHAT_INTAKE is
// the operator switch on top: 0|off|false|no|disabled turns intake OFF even
// when wired (the router keeps every ask); 1|true|on turns it ON unwired
// (legacy launcher). Answers to an already-waiting intake are always
// processed (such a record exists only if intake was enabled).
func packagingChatIntakeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PACKAGING_CHAT_INTAKE"))) {
	case "0", "off", "false", "no", "disabled":
		return false
	case "1", "on", "true", "yes":
		return true
	}
	return packagingCommissionLauncherFactory != nil
}

func (app *kanbanBoardApp) packagingCommissionLauncher(kind string) (commissionLauncher, bool) {
	if kind == packagingIntakeKindDocument {
		// Documents are not a commission kind (D6: the chat harness runs
		// document_report directly); the studio adapter refuses them.
		return legacyPackagingCommissionLauncher{app: app}, packagingCommissionLauncherFactory != nil
	}
	if packagingCommissionLauncherFactory != nil {
		if launcher := packagingCommissionLauncherFactory(app); launcher != nil {
			return launcher, true
		}
	}
	return legacyPackagingCommissionLauncher{app: app}, false
}

// legacyPackagingCommissionLauncher launches a completed brief through the
// doors that exist today: the packaging_studio / document_report processes via
// the goal engine (the palette Run path) and the research mode via an agent
// thread. It is the pre-D9 fallback, never the studio's commission record.
type legacyPackagingCommissionLauncher struct {
	app *kanbanBoardApp
}

func (launcher legacyPackagingCommissionLauncher) createPackagingCommission(principal *userAccount, kind string, brief packagingIntakeBrief) (packagingCommissionReceipt, error) {
	app := launcher.app
	if app == nil || app.memory == nil || principal == nil {
		return packagingCommissionReceipt{}, fmt.Errorf("packaging commissions are unavailable")
	}
	objective := renderPackagingIntakeObjective(brief)
	origin := map[string]string{
		"originKind":    agentThreadOriginPrivateThread,
		"originId":      brief.ThreadID,
		"originSurface": "chat:" + brief.ThreadID,
		"requestedBy":   normalizeAccountEmail(principal.Email),
	}
	switch kind {
	case packagingIntakeKindPresentation, packagingIntakeKindDocument, packagingIntakeKindStory:
		processID := packagingStudioProcessID
		if kind != packagingIntakeKindPresentation {
			processID = documentReportProcessID
		}
		process, ok := processByID(processID)
		if !ok {
			return packagingCommissionReceipt{}, fmt.Errorf("the %s process is unavailable", processID)
		}
		goalThread, err := app.launchGoalThread(goalLaunchSpec{
			Objective:    objective,
			CreatedBy:    scoutChatAuthorName(principal),
			Authority:    process.Authority,
			ToolTemplate: process.ID,
			ContextRefs:  encodeAssistantContextRefs(brief.ContextRefs),
			Origin:       origin,
		})
		if err != nil {
			return packagingCommissionReceipt{}, err
		}
		return packagingCommissionReceipt{
			CommissionID: goalThread.ID,
			ArtifactID:   goalThread.Artifact.ID,
			Label:        studioProjectLaunchCopy(process.ID, process.Title),
			Thread: &scoutChatThreadRef{
				ID: goalThread.ID, Mode: goalThread.Mode, ProcessID: process.ID, Query: goalThread.Query, Status: goalThread.Status,
				ArtifactID:   goalThread.Artifact.ID,
				OutputFamily: firstNonEmptyString(scoutChatOutputFamilyForArtifact(goalThread.Artifact), scoutChatOutputFamilyForMode(goalThread.Mode)),
			},
		}, nil
	default:
		spec := agentThreadGoalSpec{
			Objective:     objective,
			ContextRefs:   encodeAssistantContextRefs(brief.ContextRefs),
			OriginSurface: "chat:" + brief.ThreadID,
			RequestedBy:   normalizeAccountEmail(principal.Email),
		}
		agentThread, err := app.launchAgentThreadWithSpec("research", objective, scoutChatAuthorName(principal), origin, spec)
		if err != nil {
			return packagingCommissionReceipt{}, err
		}
		return packagingCommissionReceipt{
			CommissionID: agentThread.ID,
			ArtifactID:   agentThread.Artifact.ID,
			Label:        "research launched — running against its output contract and gate rubric",
			Thread: &scoutChatThreadRef{
				ID: agentThread.ID, Mode: agentThread.Mode, Query: agentThread.Query, Status: agentThread.Status, ArtifactID: agentThread.Artifact.ID,
				OutputFamily: firstNonEmptyString(scoutChatOutputFamilyForArtifact(agentThread.Artifact), scoutChatOutputFamilyForMode(agentThread.Mode)),
			},
		}, nil
	}
}

// ---------------------------------------------------------------------------
// Detection: is this a packaging work ask, and of which kind?
// ---------------------------------------------------------------------------

var packagingIntakeVerbs = map[string]bool{
	"create": true, "prepare": true, "build": true, "design": true, "make": true, "write": true, "draft": true,
	"produce": true, "put": true, "pull": true, "assemble": true, "compile": true, "commission": true, "run": true,
	"research": true, "investigate": true, "do": true, "generate": true, "spin": true, "whip": true, "develop": true,
	"workshop": true,
}

// packagingIntakeNounWindow is how far past the action verb a deliverable
// noun may sit and still be its object: "make me a pitch deck" (4) counts,
// "pull comps for the rodeo doc" (5, and the doc is what the comps are FOR)
// does not.
const packagingIntakeNounWindow = 4

var packagingIntakeKindNouns = map[string]string{
	"deck": packagingIntakeKindPresentation, "decks": packagingIntakeKindPresentation, "slides": packagingIntakeKindPresentation,
	"slide": packagingIntakeKindPresentation, "presentation": packagingIntakeKindPresentation, "keynote": packagingIntakeKindPresentation,
	"pitch":    packagingIntakeKindPresentation,
	"research": packagingIntakeKindResearch, "analysis": packagingIntakeKindResearch, "study": packagingIntakeKindResearch,
	"landscape": packagingIntakeKindResearch, "benchmark": packagingIntakeKindResearch, "diligence": packagingIntakeKindResearch,
	"teardown": packagingIntakeKindResearch,
	"memo":     packagingIntakeKindDocument, "document": packagingIntakeKindDocument, "doc": packagingIntakeKindDocument,
	"pager": packagingIntakeKindDocument, "writeup": packagingIntakeKindDocument, "whitepaper": packagingIntakeKindDocument,
	"brief": packagingIntakeKindDocument, "summary": packagingIntakeKindDocument, "report": packagingIntakeKindDocument,
	"story": packagingIntakeKindStory, "narrative": packagingIntakeKindStory, "storyline": packagingIntakeKindStory,
	"outline": packagingIntakeKindStory,
}

// chatAgentWorkWords splits "don't" into "don","t", so both halves count.
var packagingIntakeNegations = map[string]bool{"don": true, "dont": true, "t": true, "not": true, "never": true, "no": true, "without": true, "stop": true, "cancel": true, "skip": true}

// packagingIntakeBuildsFromExistingMaterial reports an ask whose brief is
// already settled by material the user names — "build the deck from the
// outline we already have" (D5: the outline is the deck's settled narrative).
// Those asks carry no output-changing gap and keep the direct Studio route.
func packagingIntakeBuildsFromExistingMaterial(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{"we already have", "already have", "existing outline", "existing story", "existing deck", "existing research",
		"from it", "from that", "from this", "the outline we", "this outline", "that outline", "from the outline", "based on the outline",
		"turn the outline", "from the story", "from this story", "from that story", "the story we", "from our research", "from the research"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// packagingIntakeDetect reads a message (plus its reply parent and attachment
// presence) and returns the packaging kind it asks for. ok is false for
// social mentions, questions about existing work, status checks, and
// unbounded asks. kind is "" (ok true) when two kinds tie — that ambiguity is
// itself the first clarifying question.
func packagingIntakeDetect(text string, files []scoutChatFileAttachment, replyTo *scoutChatReplyRef) (kind string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return "", false
	}
	words := chatAgentWorkWords(lower)
	if len(words) == 0 {
		return "", false
	}
	// A question ABOUT work is not an ask for work.
	if strings.HasSuffix(strings.TrimSpace(text), "?") {
		statusWords := map[string]bool{"how": true, "when": true, "where": true, "did": true, "is": true, "are": true, "status": true, "coming": true, "done": true, "ready": true}
		for _, word := range words[:min(len(words), 4)] {
			if statusWords[word] {
				return "", false
			}
		}
	}
	counts := map[string]int{}
	verbIndex := -1
	lastVerb := -1
	// The last deliverable noun counted, so a compound noun phrase resolves to
	// its HEAD: English puts the head last ("market analysis deck" is a deck,
	// "launch narrative deck" is a deck), and the head may sit one step past
	// the verb window its modifier passed.
	countedNoun, countedKind := -1, ""
	for index, word := range words {
		if packagingIntakeVerbs[word] {
			if index > 0 && packagingIntakeNegations[words[index-1]] {
				return "", false
			}
			if verbIndex < 0 {
				verbIndex = index
			}
			lastVerb = index
		}
		if mapped, hit := packagingIntakeKindNouns[word]; hit {
			// The noun must be the object of an action verb: within the
			// window after one ("research" is its own verb and noun), or the
			// head of a compound whose modifier already qualified.
			head := countedNoun == index-1
			if !head && (lastVerb < 0 || (index != lastVerb && index-lastVerb > packagingIntakeNounWindow)) {
				continue
			}
			// "research" as a bare noun after "the" ("the research looks good")
			// is subject matter; only count it with an action verb in reach.
			if word == "research" && index > 0 && (words[index-1] == "the" || words[index-1] == "that" || words[index-1] == "this") {
				continue
			}
			// "brief"/"summary"/"report" beside "research" reads research, and
			// "pitch" beside "deck" reads deck — collapse the obvious pairs.
			if word == "pitch" || word == "brief" {
				if strings.Contains(lower, "pitch deck") || strings.Contains(lower, "research brief") {
					continue
				}
			}
			if head {
				if countedKind == packagingIntakeKindResearch && mapped == packagingIntakeKindDocument {
					// "research report", "research brief", "research summary":
					// the document noun names the FORMAT of the research, not
					// a second deliverable and not a plain document.
					continue
				}
				// The modifier described this noun; it never asked for its own
				// deliverable ("a market analysis deck" is one deck, not a deck
				// tied with a research report).
				counts[countedKind]--
				if counts[countedKind] <= 0 {
					delete(counts, countedKind)
				}
			}
			counts[mapped]++
			countedNoun, countedKind = index, mapped
		}
	}
	if verbIndex < 0 {
		return "", false
	}
	if len(counts) == 0 {
		return "", false
	}
	if !chatAgentWorkRequestIsBounded(text, files, replyTo) {
		return "", false
	}
	best, second, bestKind := 0, 0, ""
	kinds := make([]string, 0, len(counts))
	for candidate := range counts {
		kinds = append(kinds, candidate)
	}
	sort.Strings(kinds)
	for _, candidate := range kinds {
		count := counts[candidate]
		if count > best {
			second = best
			best, bestKind = count, candidate
		} else if count > second {
			second = count
		}
	}
	if best == second {
		// "a deck and a memo" — two kinds with equal weight: ask.
		return "", true
	}
	return bestKind, true
}

// ---------------------------------------------------------------------------
// Brief pre-fill: deterministic extraction + bounded model refinement.
// ---------------------------------------------------------------------------

var (
	packagingAudiencePattern = regexp.MustCompile(`(?i)\b(?:for|to|aimed at|targeting|audience(?: is|:)?)\s+(?:the\s+|our\s+|an?\s+)?(investors?|board(?: members)?|customers?|clients?|partners?|team|leadership|execs?|executives?|engineers?|recruits?|candidates?|press|public|students?|founders?|buyers?|prospects?|community|employees?|staff|advisors?|users?|analysts?|lps?|vcs?|sponsors?|donors?|regulators?|internal team|all hands)\b`)
	packagingSlideCountPat   = regexp.MustCompile(`(?i)\b(\d{1,2})\s*(?:-|to)?\s*(?:slides?|pages?)\b`)
	packagingURLPattern      = regexp.MustCompile(`(?i)https?://[^\s)>\]]+`)
	packagingNamedSourcePat  = regexp.MustCompile(`(?i)\b(?:based on|using|from|per|drawing on|off of|off)\s+(?:the\s+|our\s+)?([A-Za-z0-9][A-Za-z0-9 _.'-]{2,48}?(?:\.(?:pdf|docx?|pptx?|xlsx?|csv|md|txt)|\s+(?:report|deck|memo|transcript|notes?|data|numbers|research|analysis|doc|document|file|spreadsheet|sheet|brief|findings)))`)
)

// packagingIntakeThreadContext folds the last few human/Scout lines of the
// thread (and the reply parent) into the brief so the studio sees the same
// context the asker did. Bodies are trimmed; this is context, not sources.
func packagingIntakeThreadContext(thread scoutChatThreadRecord, current scoutChatMessageRecord) []string {
	lines := make([]string, 0, packagingIntakeContextLines)
	start := len(thread.Messages) - packagingIntakeContextLines
	if start < 0 {
		start = 0
	}
	for _, message := range thread.Messages[start:] {
		if message.ID == current.ID || message.Kind != "message" || strings.TrimSpace(message.Text) == "" {
			continue
		}
		author := firstNonEmptyString(strings.TrimSpace(message.AuthorName), scoutParticipantName)
		lines = append(lines, author+": "+trimForStorage(compactAssistantLine(message.Text), 240))
	}
	if current.ReplyTo != nil && strings.TrimSpace(current.ReplyTo.Text) != "" {
		lines = append(lines, "Replied-to "+firstNonEmptyString(strings.TrimSpace(current.ReplyTo.AuthorName), "message")+": "+trimForStorage(compactAssistantLine(current.ReplyTo.Text), 320))
	}
	return lines
}

func packagingIntakeDeterministicBrief(kind string, thread scoutChatThreadRecord, message scoutChatMessageRecord, files []scoutChatFileAttachment, contextRefs []string, user *userAccount) packagingIntakeBrief {
	text := strings.TrimSpace(message.Text)
	lower := strings.ToLower(text)
	brief := packagingIntakeBrief{
		Kind:           kind,
		Ask:            text,
		ThreadID:       thread.ID,
		ThreadTitle:    strings.TrimSpace(thread.Title),
		MessageID:      message.ID,
		RequesterEmail: normalizeAccountEmail(user.Email),
		RequesterName:  scoutChatAuthorName(user),
		ThreadContext:  packagingIntakeThreadContext(thread, message),
		ContextRefs:    canonicalAssistantContextRefs(contextRefs),
		Answers:        map[string]string{},
	}
	for _, file := range files {
		if name := strings.TrimSpace(file.Name); name != "" {
			brief.Attachments = append(brief.Attachments, name)
		}
	}
	if match := packagingAudiencePattern.FindStringSubmatch(text); len(match) > 1 {
		brief.Audience = strings.ToLower(strings.TrimSpace(match[1]))
	}
	// Sources: attachments and Drive refs are explicit; URLs and "based on the
	// X report" name a source; nothing at all means the studio infers
	// research_mode on its own — unless the ask leans on unnamed material.
	for _, url := range packagingURLPattern.FindAllString(text, -1) {
		brief.Sources = append(brief.Sources, strings.TrimRight(url, ".,;"))
	}
	for _, match := range packagingNamedSourcePat.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			brief.Sources = append(brief.Sources, strings.TrimSpace(match[1]))
		}
	}
	if message.ReplyTo != nil && strings.TrimSpace(message.ReplyTo.Text) != "" {
		brief.Sources = append(brief.Sources, "reply parent: "+trimForStorage(compactAssistantLine(message.ReplyTo.Text), 120))
	}
	brief.Sources = uniqueSortedStrings(brief.Sources)
	switch {
	case len(brief.Attachments) > 0 || len(brief.ContextRefs) > 0:
		brief.SourceMode = "attached"
	case len(brief.Sources) > 0:
		brief.SourceMode = "named"
	case strings.Contains(lower, "our data") || strings.Contains(lower, "our numbers") || strings.Contains(lower, "the numbers") ||
		strings.Contains(lower, "the data") || strings.Contains(lower, "our metrics") || strings.Contains(lower, "the findings") ||
		strings.Contains(lower, "the research") || strings.Contains(lower, "the report") || strings.Contains(lower, "the transcript"):
		// Leans on material it never names — that's a real gap.
		brief.SourceMode = ""
	default:
		brief.SourceMode = "infer"
		brief.Inferred = append(brief.Inferred, "sources")
	}
	if match := packagingSlideCountPat.FindStringSubmatch(text); len(match) > 1 {
		brief.Length = match[1] + " slides"
		if kind == packagingIntakeKindDocument {
			brief.Length = match[1] + " pages"
		}
	}
	// "brief" the noun (a research brief) is a deliverable, not a depth; only
	// the adjective forms count.
	for _, phrase := range []string{"one-pager", "one pager", "short", "quick", "keep it brief", "brief overview", "tight", "long", "detailed", "comprehensive", "deep dive", "deep-dive", "thorough", "exhaustive", "high-level", "high level", "overview", "lightweight"} {
		if strings.Contains(lower, phrase) {
			switch phrase {
			case "one-pager", "one pager":
				if brief.Length == "" {
					brief.Length = "one page"
				}
			case "short", "quick", "keep it brief", "brief overview", "tight", "lightweight", "high-level", "high level", "overview":
				if brief.Length == "" {
					brief.Length = "short"
				}
				if brief.Depth == "" {
					brief.Depth = "brief"
				}
			default:
				if brief.Length == "" {
					brief.Length = "long"
				}
				if brief.Depth == "" {
					brief.Depth = "deep"
				}
			}
		}
	}
	if strings.Contains(lower, "standard depth") || strings.Contains(lower, "normal depth") {
		brief.Depth = "standard"
	}
	switch {
	case strings.Contains(lower, "full-bleed") || strings.Contains(lower, "full bleed") || strings.Contains(lower, "fullbleed"):
		brief.Imagery = "full-bleed"
	case strings.Contains(lower, "on-slide") || strings.Contains(lower, "on slide image") || strings.Contains(lower, "inline image"):
		brief.Imagery = "on-slide"
	case strings.Contains(lower, "hybrid"):
		brief.Imagery = "hybrid"
	case strings.Contains(lower, "no images") || strings.Contains(lower, "no imagery") || strings.Contains(lower, "text only") || strings.Contains(lower, "text-only"):
		brief.Imagery = "none"
	}
	for _, style := range []string{"crisp", "narrative", "data-led", "data led", "persuasive", "punchy", "formal", "casual"} {
		if strings.Contains(lower, style) {
			brief.CopyStyle = strings.ReplaceAll(style, " ", "-")
			break
		}
	}
	return brief
}

// packagingIntakeClassification is the bounded model pass's strict output:
// the same fields the deterministic pass fills, plus a confidence so a weak
// kind guess never overrides the deterministic read.
type packagingIntakeClassification struct {
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
	Audience   string  `json:"audience"`
	Length     string  `json:"length"`
	Imagery    string  `json:"imagery"`
	Depth      string  `json:"depth"`
	CopyStyle  string  `json:"copy_style"`
	Sources    string  `json:"sources"`
}

func packagingIntakeClassifierSchema() *openAIJSONSchema {
	enumString := func(values ...string) map[string]any {
		return map[string]any{"type": "string", "enum": values}
	}
	return &openAIJSONSchema{
		Name: "packaging_intake_v1",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"kind", "confidence", "audience", "length", "imagery", "depth", "copy_style", "sources"},
			"properties": map[string]any{
				"kind":       enumString(packagingIntakeKindResearch, packagingIntakeKindPresentation, packagingIntakeKindDocument, packagingIntakeKindStory, "unclear"),
				"confidence": map[string]any{"type": "number"},
				"audience":   map[string]any{"type": "string"},
				"length":     map[string]any{"type": "string"},
				"imagery":    enumString("full-bleed", "on-slide", "hybrid", "none", ""),
				"depth":      enumString("brief", "standard", "deep", ""),
				"copy_style": enumString("crisp", "narrative", "data-led", "persuasive", ""),
				"sources":    map[string]any{"type": "string"},
			},
		},
	}
}

func packagingIntakeClassifierInstructions() string {
	return strings.Join([]string{
		"You pre-fill a Packaging Studio brief from one chat message and its thread context at Bonfire.",
		"Read only what the asker actually said. Return kind = research | presentation | document | story, or unclear when two kinds are equally plausible.",
		"Fill audience, length, imagery (decks only), depth (research only), copy_style and sources ONLY when the message or reply parent states them; leave a field empty rather than guessing. Never invent sources.",
		"confidence is 0..1 for kind. Output strict JSON only.",
	}, " ")
}

// packagingIntakeClassifyWithModel refines a deterministic brief with one
// bounded seatChat call. It is skipped without a key and while the chat seat's
// breaker is open; any failure leaves the deterministic brief untouched.
func (app *kanbanBoardApp) packagingIntakeClassifyWithModel(ctx context.Context, brief packagingIntakeBrief, kindAmbiguous bool) (packagingIntakeBrief, string) {
	if app == nil {
		return brief, "deterministic"
	}
	apiKey := app.currentOpenAIAPIKey()
	if apiKey == "" {
		return brief, "deterministic"
	}
	if _, paused := providerBreakers.paused(providerOpenAI, seatChat); paused {
		return brief, "deterministic:breaker_open"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	capture := &providerCallProvenanceCapture{}
	callCtx = withProviderCallProvenanceCapture(callCtx, capture)
	input := strings.Builder{}
	input.WriteString("Message from " + firstNonEmptyString(brief.RequesterName, "the asker") + ":\n" + brief.Ask + "\n")
	if len(brief.Attachments) > 0 {
		input.WriteString("Attachments: " + strings.Join(brief.Attachments, ", ") + "\n")
	}
	if len(brief.ThreadContext) > 0 {
		input.WriteString("Thread context (newest last):\n" + strings.Join(brief.ThreadContext, "\n") + "\n")
	}
	input.WriteString("Deterministic read: kind=" + firstNonEmptyString(brief.Kind, "unclear") + "\n")
	output, err := createOpenAITextResponse(callCtx, apiKey, openAITextRequest{
		Model:           scoutChatModel(),
		Seat:            seatChat,
		Workflow:        packagingIntakeWorkflow,
		Instructions:    packagingIntakeClassifierInstructions(),
		Input:           input.String(),
		ReasoningEffort: "low",
		Verbosity:       "low",
		MaxOutputTokens: packagingIntakeClassifyTokens,
		JSONSchema:      packagingIntakeClassifierSchema(),
		ValidateOutput: func(text string) error {
			var payload packagingIntakeClassification
			return json.Unmarshal([]byte(strings.TrimSpace(text)), &payload)
		},
	})
	if err != nil {
		return brief, "deterministic:model_error"
	}
	var payload packagingIntakeClassification
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &payload) != nil {
		return brief, "deterministic:model_invalid"
	}
	provenance := "model"
	if stamped, ok := capture.snapshot(); ok && strings.TrimSpace(stamped.Model) != "" {
		provenance = "model:" + stamped.Model
	} else {
		provenance = "model:" + scoutChatModel()
	}
	if kindAmbiguous && payload.Confidence >= 0.8 && oneOf(payload.Kind, packagingIntakeKindResearch, packagingIntakeKindPresentation, packagingIntakeKindDocument, packagingIntakeKindStory) {
		brief.Kind = payload.Kind
	}
	fill := func(target *string, value string, field string) {
		value = strings.TrimSpace(value)
		if *target == "" && value != "" {
			*target = value
			brief.Inferred = append(brief.Inferred, field)
		}
	}
	fill(&brief.Audience, payload.Audience, "audience")
	fill(&brief.Length, payload.Length, "length")
	fill(&brief.Imagery, payload.Imagery, "imagery")
	fill(&brief.Depth, payload.Depth, "depth")
	fill(&brief.CopyStyle, payload.CopyStyle, "copyStyle")
	if sources := strings.TrimSpace(payload.Sources); sources != "" && brief.SourceMode == "" {
		brief.Sources = uniqueSortedStrings(append(brief.Sources, sources))
		brief.SourceMode = "named"
	}
	return brief, provenance
}

// ---------------------------------------------------------------------------
// briefGaps: ONLY the questions whose answer changes the output.
// ---------------------------------------------------------------------------

var packagingIntakeQuestionCatalog = map[string]packagingIntakeQuestion{
	"kind": {ID: "kind", Kind: "choice", Prompt: "Which deliverable do you want first?",
		Options: []string{"research report", "presentation", "document", "story outline"}},
	"audience": {ID: "audience", Kind: "text", Prompt: "Who is the audience?"},
	"sources":  {ID: "sources", Kind: "text", Prompt: "Which sources should I work from? Attach or name them, or say \"find your own\"."},
	"imagery": {ID: "imagery", Kind: "choice", Prompt: "Imagery mode for the deck?",
		Options: []string{"full-bleed", "on-slide", "hybrid"}},
	"depth": {ID: "depth", Kind: "choice", Prompt: "How deep should the research go?",
		Options: []string{"brief", "standard", "deep"}},
	"length": {ID: "length", Kind: "choice", Prompt: "How long should it be?",
		Options: []string{"one page", "short", "long"}},
}

// briefGaps returns the questions still worth asking, in priority order and
// capped at packagingIntakeMaxQuestions. Kind ambiguity always comes first
// because every other gap depends on it; a kind-less brief asks only that.
func briefGaps(kind string, brief packagingIntakeBrief) []packagingIntakeQuestion {
	asked := func(id string) bool {
		_, answered := brief.Answers[id]
		return answered
	}
	gaps := make([]packagingIntakeQuestion, 0, packagingIntakeMaxQuestions)
	add := func(id string) {
		if len(gaps) >= packagingIntakeMaxQuestions || asked(id) {
			return
		}
		gaps = append(gaps, packagingIntakeQuestionCatalog[id])
	}
	if kind == "" {
		add("kind")
		return gaps
	}
	if strings.TrimSpace(brief.Audience) == "" {
		add("audience")
	}
	switch kind {
	case packagingIntakeKindPresentation:
		if strings.TrimSpace(brief.Imagery) == "" {
			add("imagery")
		}
	case packagingIntakeKindResearch:
		if strings.TrimSpace(brief.Depth) == "" {
			add("depth")
		}
	case packagingIntakeKindDocument:
		if strings.TrimSpace(brief.Length) == "" {
			add("length")
		}
	}
	if brief.SourceMode == "" {
		add("sources")
	}
	return gaps
}

// ---------------------------------------------------------------------------
// Answers: structured pills or free text complete the brief.
// ---------------------------------------------------------------------------

var packagingIntakeMentionPattern = regexp.MustCompile(`(?i)(^|\s)@scout\b[,:]?`)

// packagingIntakeTextAnswer normalizes a free-text answer for one field:
// "for the investors" → "investors" for audience; everything else is kept as
// typed, bounded.
func packagingIntakeTextAnswer(questionID string, segment string) string {
	segment = strings.TrimSpace(segment)
	if questionID == "audience" {
		if match := packagingAudiencePattern.FindStringSubmatch(segment); len(match) > 1 {
			return strings.ToLower(strings.TrimSpace(match[1]))
		}
		lower := strings.ToLower(segment)
		for _, prefix := range []string{"for the ", "for our ", "for ", "to the ", "to our ", "to ", "audience is ", "audience: "} {
			if strings.HasPrefix(lower, prefix) {
				return strings.TrimSpace(segment[len(prefix):])
			}
		}
	}
	return trimForStorage(segment, 200)
}

// packagingIntakeDeferrals are WHOLE phrases ("you decide"), matched
// word-anchored against the reply. A bare "anything" is deliberately absent:
// it lives inside ordinary questions back to Scout ("is there anything else
// you need?") and closing every open question on it launched real work off a
// question.
var packagingIntakeDeferrals = []string{"you decide", "you pick", "your call", "up to you", "whatever you think", "whatever you like",
	"anything is fine", "anything works", "anything you like", "any is fine", "doesn't matter", "does not matter",
	"dealers choice", "dealer's choice", "surprise me", "find your own", "you choose"}

// packagingIntakeMatchText renders a reply as space-delimited words so phrase
// tests are word-anchored: " as long as it covers " never matches "long", and
// "briefing"/"deeply"/"debrief" never match "brief"/"deep".
func packagingIntakeMatchText(text string) string {
	words := chatAgentWorkWords(text)
	if len(words) == 0 {
		return ""
	}
	return " " + strings.Join(words, " ") + " "
}

func packagingIntakePhraseMatches(matchText string, phrase string) bool {
	needle := strings.Join(chatAgentWorkWords(phrase), " ")
	if needle == "" || matchText == "" {
		return false
	}
	return strings.Contains(matchText, " "+needle+" ")
}

// packagingIntakeIsDeferral reports "you decide" and its kin. A reply carrying
// a question mark is a question back to Scout, never a deferral — the same
// fence the take-whole branch applies.
func packagingIntakeIsDeferral(text string) bool {
	if strings.Contains(text, "?") {
		return false
	}
	matchText := packagingIntakeMatchText(text)
	for _, phrase := range packagingIntakeDeferrals {
		if packagingIntakePhraseMatches(matchText, phrase) {
			return true
		}
	}
	return false
}

// packagingIntakeComparativeLeadIns precede a comparative use of an option
// word rather than a choice of it: "as long as it covers the numbers",
// "how long is it", "not short".
var packagingIntakeComparativeLeadIns = map[string]bool{"as": true, "so": true, "how": true, "too": true, "very": true, "not": true, "isn": true, "aren": true, "than": true}

func packagingIntakeWordRunIndex(words []string, run []string) int {
	if len(run) == 0 || len(run) > len(words) {
		return -1
	}
	for index := 0; index+len(run) <= len(words); index++ {
		matched := true
		for offset, word := range run {
			if words[index+offset] != word {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
}

func packagingIntakeComparativeContext(words []string, index int) bool {
	if index > 0 && packagingIntakeComparativeLeadIns[words[index-1]] {
		return true
	}
	return index+1 < len(words) && words[index+1] == "as"
}

// packagingIntakeOptionMatches reports that the reply NAMES the option. The
// test is token-anchored (hyphen and space spellings collapse to the same
// tokens, so "full-bleed" and "full bleed" both match) and a one-word option
// inside a comparative idiom is not a choice: "as long as it covers the Q3
// numbers" does not answer "how long should it be?".
func packagingIntakeOptionMatches(option string, lower string) bool {
	optionWords := chatAgentWorkWords(strings.TrimSpace(option))
	words := chatAgentWorkWords(lower)
	if len(optionWords) == 0 || len(words) == 0 {
		return false
	}
	if index := packagingIntakeWordRunIndex(words, optionWords); index >= 0 {
		if len(optionWords) > 1 || !packagingIntakeComparativeContext(words, index) {
			return true
		}
	}
	return false
}

// packagingIntakeOptionAliases are the extra spellings that NAME an option,
// per question id. Only the kind question needs them ("a deck" is a
// presentation); every other question matches its options literally.
//
// There is deliberately no general "first word of a multi-word option"
// fallback: three catalog options start with a function word — "on-slide",
// "one page", "full-bleed" — so that fallback made any reply containing "on",
// "one" or "full" a choice. "base it on the transcript Ana posted" set an
// imagery mode the asker never chose, and the phantom close then suppressed
// the take-whole branch that would have stored the answer they DID give.
// The inner keys are the catalog OPTION strings, not the kind constants.
var packagingIntakeOptionAliases = map[string]map[string][]string{
	"kind": {
		"research report": {"research", "study"},
		"presentation":    {"deck", "slides", "slide deck"},
		"document":        {"memo", "write up"},
		"story outline":   {"outline", "narrative"},
	},
	// Whole spellings only — never a bare "one".
	"length": {"one page": {"one pager", "single page", "1 page"}},
}

// packagingIntakeQuestionOptionMatches reports that the reply names one of
// question's options — the option itself, or one of that question's declared
// aliases, matched the same word-anchored way.
func packagingIntakeQuestionOptionMatches(question packagingIntakeQuestion, option string, lower string) bool {
	if packagingIntakeOptionMatches(option, lower) {
		return true
	}
	for _, alias := range packagingIntakeOptionAliases[question.ID][option] {
		if packagingIntakeOptionMatches(alias, lower) {
			return true
		}
	}
	return false
}

// packagingIntakeAsideOpeners start a reply that defers, hedges or narrates
// what the asker is about to do — never the answer to a brief question. They
// are matched as OPENERS only, so "let me know the audience: the exec team"…
// still isn't an answer, while "the exec team" is.
var packagingIntakeAsideOpeners = []string{"let me", "let s", "lets", "i ll", "i will", "i m going to", "im going to", "i need to", "i have to",
	"we need to", "we should", "hold on", "hold up", "one sec", "give me", "gimme", "wait", "not yet", "not sure", "no idea",
	"i don t know", "i dont know", "still thinking", "checking", "i ll check", "i ll ask", "asking", "maybe later"}

// packagingIntakeReadsAsAnswer gates the greedy "one open text question left:
// take the whole reply" branch. A question back to Scout, a fresh work ask, an
// acknowledgement, or a conversational aside is NOT the answer to the open
// question — folding one in used to launch a commission on a nonsense brief.
func packagingIntakeReadsAsAnswer(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.Contains(trimmed, "?") {
		return false
	}
	if _, isAsk := packagingIntakeDetect(trimmed, nil, nil); isAsk {
		return false
	}
	// An ordinary "thanks!" / "ok cool" / "👍" is courtesy, not a brief. The
	// take-whole branch used to store it as the last open text answer and
	// launch on it; the follow-up watcher classifies the same strings as
	// acknowledgement → silent, but only AFTER this branch has run, so the
	// fence has to live here too. scoutFollowupIsAcknowledgement is whole-
	// message anchored and capped at six words, so a real audience or sources
	// answer ("great news for the exec team") is never swallowed.
	if scoutFollowupIsAcknowledgement(trimmed) {
		return false
	}
	matchText := packagingIntakeMatchText(trimmed)
	for _, opener := range packagingIntakeAsideOpeners {
		needle := strings.Join(chatAgentWorkWords(opener), " ")
		if needle != "" && strings.HasPrefix(matchText, " "+needle+" ") {
			return false
		}
	}
	return true
}

// applyBriefAnswers folds answers into the record's brief. Structured answers
// bind by question id; free text is matched against each open question's
// options, numbered lines map by position, and a lone open text question
// takes the whole reply. Deferrals ("you decide") count as answered-by-
// inference so Scout never re-asks. It returns how many questions closed.
func applyBriefAnswers(record *packagingIntakeRecord, structured []scoutChatClarifyingAnswer, text string) int {
	if record == nil {
		return 0
	}
	if record.Brief.Answers == nil {
		record.Brief.Answers = map[string]string{}
	}
	closed := 0
	answer := func(question packagingIntakeQuestion, value string, inferred bool) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, done := record.Brief.Answers[question.ID]; done {
			return
		}
		record.Brief.Answers[question.ID] = value
		if inferred {
			record.Brief.Inferred = append(record.Brief.Inferred, question.ID)
		}
		switch question.ID {
		case "kind":
			switch {
			case strings.Contains(strings.ToLower(value), "research"):
				record.Brief.Kind = packagingIntakeKindResearch
			case strings.Contains(strings.ToLower(value), "present") || strings.Contains(strings.ToLower(value), "deck") || strings.Contains(strings.ToLower(value), "slide"):
				record.Brief.Kind = packagingIntakeKindPresentation
			case strings.Contains(strings.ToLower(value), "story") || strings.Contains(strings.ToLower(value), "outline") || strings.Contains(strings.ToLower(value), "narrative"):
				record.Brief.Kind = packagingIntakeKindStory
			default:
				record.Brief.Kind = packagingIntakeKindDocument
			}
			record.Kind = record.Brief.Kind
		case "audience":
			record.Brief.Audience = value
		case "sources":
			if inferred {
				record.Brief.SourceMode = "infer"
			} else {
				record.Brief.Sources = uniqueSortedStrings(append(record.Brief.Sources, value))
				record.Brief.SourceMode = "named"
			}
		case "imagery":
			record.Brief.Imagery = value
		case "depth":
			record.Brief.Depth = value
		case "length":
			record.Brief.Length = value
		}
		closed++
	}
	open := map[string]packagingIntakeQuestion{}
	for _, question := range record.OpenQuestions {
		open[question.ID] = question
	}
	for _, item := range structured {
		if question, ok := open[strings.TrimSpace(item.ID)]; ok {
			answer(question, item.Value, false)
		}
	}
	// The mention that authorized the answer is not part of the answer.
	text = strings.TrimSpace(packagingIntakeMentionPattern.ReplaceAllString(text, " "))
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, ","), "—"))
	lower := strings.ToLower(text)
	if text != "" {
		// Numbered lines: "1. investors\n2. hybrid" map by question order.
		lines := strings.Split(text, "\n")
		numbered := map[int]string{}
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) < 3 {
				continue
			}
			if trimmed[0] >= '1' && trimmed[0] <= '9' && (trimmed[1] == '.' || trimmed[1] == ')' || trimmed[1] == ':' || trimmed[1] == '-') {
				position, _ := strconv.Atoi(trimmed[:1])
				numbered[position] = strings.TrimSpace(trimmed[2:])
			}
		}
		for position, value := range numbered {
			if position >= 1 && position <= len(record.OpenQuestions) {
				question := record.OpenQuestions[position-1]
				if _, done := record.Brief.Answers[question.ID]; done {
					continue
				}
				answer(question, value, packagingIntakeIsDeferral(value))
			}
		}
		// A bare list with one segment per open question ("investors, hybrid")
		// maps positionally: text questions take the segment as-is, choice
		// questions only when the segment names one of their options.
		if len(numbered) == 0 {
			segments := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
			if len(segments) == len(record.OpenQuestions) && len(segments) > 1 {
				for position, question := range record.OpenQuestions {
					segment := strings.TrimSpace(segments[position])
					if _, done := record.Brief.Answers[question.ID]; done || segment == "" {
						continue
					}
					if question.Kind == "choice" {
						for _, option := range question.Options {
							if packagingIntakeQuestionOptionMatches(question, option, strings.ToLower(segment)) {
								answer(question, option, false)
								break
							}
						}
						continue
					}
					if !strings.Contains(segment, "?") {
						answer(question, packagingIntakeTextAnswer(question.ID, segment), false)
					}
				}
			}
		}
		// Option words anywhere in the reply close choice questions.
		for _, question := range record.OpenQuestions {
			if _, done := record.Brief.Answers[question.ID]; done || question.Kind != "choice" {
				continue
			}
			for _, option := range question.Options {
				if packagingIntakeQuestionOptionMatches(question, option, lower) {
					answer(question, option, false)
					break
				}
			}
		}
		// Audience phrasing ("for investors") closes the audience question.
		if question, ok := open["audience"]; ok {
			if _, done := record.Brief.Answers["audience"]; !done {
				if match := packagingAudiencePattern.FindStringSubmatch(text); len(match) > 1 {
					answer(question, strings.ToLower(match[1]), false)
				}
			}
		}
		// A deferral closes every remaining open question by inference.
		if packagingIntakeIsDeferral(text) {
			for _, question := range record.OpenQuestions {
				answer(question, "Scout's call", true)
			}
		}
		// One open text question left and free text remains: take it whole.
		remaining := make([]packagingIntakeQuestion, 0, len(record.OpenQuestions))
		for _, question := range record.OpenQuestions {
			if _, done := record.Brief.Answers[question.ID]; !done {
				remaining = append(remaining, question)
			}
		}
		if len(remaining) == 1 && remaining[0].Kind == "text" && len(numbered) == 0 && closed == 0 && packagingIntakeReadsAsAnswer(text) {
			answer(remaining[0], packagingIntakeTextAnswer(remaining[0].ID, trimForStorage(text, 400)), false)
		}
	}
	// Recompute open questions.
	remaining := make([]packagingIntakeQuestion, 0, len(record.OpenQuestions))
	for _, question := range record.OpenQuestions {
		if _, done := record.Brief.Answers[question.ID]; !done {
			remaining = append(remaining, question)
		}
	}
	record.OpenQuestions = remaining
	if record.Brief.Kind != "" && record.Kind == "" {
		record.Kind = record.Brief.Kind
	}
	// A kind that just resolved may open kind-specific gaps that were never
	// asked; fold them in (still capped) so the second ask is the last one.
	if len(record.OpenQuestions) == 0 && record.Kind != "" {
		for _, gap := range briefGaps(record.Kind, record.Brief) {
			if !packagingIntakeContains(record.AskedQuestionIDs, gap.ID) {
				record.OpenQuestions = append(record.OpenQuestions, gap)
			}
		}
	}
	return closed
}

func packagingIntakeContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Rendering: the ONE threaded reply, and the objective a launcher receives.
// ---------------------------------------------------------------------------

func packagingIntakeKindLabel(kind string) string {
	switch kind {
	case packagingIntakeKindResearch:
		return "research report"
	case packagingIntakeKindPresentation:
		return "presentation"
	case packagingIntakeKindDocument:
		return "document"
	case packagingIntakeKindStory:
		return "story outline"
	}
	return "deliverable"
}

// packagingIntakeMentionHandle returns the @handle that resolves the asker in
// chatMentionNames: the roster first name (the parser is case-insensitive and
// stops at punctuation), so "@Tim" pages Tim and only Tim.
func packagingIntakeMentionHandle(user *userAccount) string {
	name := strings.TrimSpace(scoutChatAuthorName(user))
	if fields := strings.Fields(name); len(fields) > 0 {
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				return r
			}
			return -1
		}, fields[0])
		if clean != "" {
			return "@" + clean
		}
	}
	return "@" + firstNonEmptyString(participantNameForEmail(user.Email), "you")
}

func renderPackagingIntakeQuestionText(user *userAccount, kind string, questions []packagingIntakeQuestion) string {
	handle := packagingIntakeMentionHandle(user)
	lead := handle + " — I can take this into the Packaging Studio as a " + packagingIntakeKindLabel(kind) + "."
	if kind == "" {
		lead = handle + " — I can take this into the Packaging Studio."
	}
	if len(questions) == 1 {
		lead += " One thing before I start:"
	} else {
		lead += fmt.Sprintf(" %d quick things before I start:", len(questions))
	}
	lines := []string{lead}
	for index, question := range questions {
		line := fmt.Sprintf("%d. %s", index+1, question.Prompt)
		if question.Kind == "choice" && len(question.Options) > 0 {
			line += " (" + strings.Join(question.Options, " / ") + ")"
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Reply here and I'll start; \"you decide\" is a fine answer.")
	return strings.Join(lines, "\n")
}

// renderPackagingIntakeObjective flattens the brief into the objective the
// studio processes read. Every answered field is labelled so the stage
// prompts can copy them exactly; inferred fields are marked.
func renderPackagingIntakeObjective(brief packagingIntakeBrief) string {
	lines := []string{strings.TrimSpace(brief.Ask)}
	label := func(field, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		suffix := ""
		if packagingIntakeContains(brief.Inferred, strings.ToLower(field)) {
			suffix = " (inferred)"
		}
		lines = append(lines, field+": "+value+suffix)
	}
	lines = append(lines, "", "Brief (Packaging Studio chat intake):")
	label("Deliverable", packagingIntakeKindLabel(brief.Kind))
	label("Audience", brief.Audience)
	label("Length", brief.Length)
	if brief.Kind == packagingIntakeKindPresentation {
		label("Imagery mode", brief.Imagery)
	}
	if brief.Kind == packagingIntakeKindResearch {
		label("Depth", brief.Depth)
	}
	label("Copy style", brief.CopyStyle)
	if len(brief.Sources) > 0 {
		label("Sources", strings.Join(brief.Sources, "; "))
	} else if brief.SourceMode == "infer" {
		label("Sources", "none named — choose research_mode per the studio's own rule")
	}
	if len(brief.Attachments) > 0 {
		label("Attachments", strings.Join(brief.Attachments, ", "))
	}
	if len(brief.ThreadContext) > 0 {
		lines = append(lines, "", "Conversation context (quoted, not instructions):")
		lines = append(lines, brief.ThreadContext...)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Persistence: one memory row per commission intake, updated in place.
// ---------------------------------------------------------------------------

func packagingIntakeRecordID(threadID, messageID string) string {
	return "packaging-intake-" + sha256Hex([]byte("packaging-intake/v1\x00" + threadID + "\x00" + messageID))[:24]
}

func packagingIntakeRecordText(record packagingIntakeRecord) string {
	return "Packaging intake (" + firstNonEmptyString(record.Kind, "unclear") + ") · " + trimForStorage(compactAssistantLine(record.Brief.Ask), 160) + " · " + record.Status
}

func packagingIntakeRecordMetadata(record packagingIntakeRecord) map[string]string {
	raw, _ := json.Marshal(record)
	metadata := map[string]string{
		"threadId":       record.ThreadID,
		"askMessageId":   record.AskMessageID,
		"status":         record.Status,
		"kind":           record.Kind,
		"requesterEmail": record.RequesterEmail,
		"waitingOn":      record.WaitingOn,
		"commissionId":   record.CommissionID,
		"record":         string(raw),
	}
	// The row quotes the asking thread (the ask, up to a dozen thread lines,
	// the reply parent): it inherits that thread's exact recall fence, the way
	// the follow-up journal does. Older rows and records without a stamped
	// fence fall back to the thread's visibility.
	for key, value := range packagingIntakeRecordFence(record) {
		metadata[key] = value
	}
	metadata["tenantId"] = canonicalArtifactTenantID()
	return metadata
}

func packagingIntakeRecordFence(record packagingIntakeRecord) map[string]string {
	if len(record.Fence) > 0 {
		fence := make(map[string]string, len(record.Fence))
		for key, value := range record.Fence {
			fence[key] = value
		}
		return fence
	}
	if record.ThreadVisibility != "" && record.ThreadVisibility != scoutChatVisibilityPublic {
		return map[string]string{"visibility": "private", "ownerEmail": normalizeAccountEmail(record.RequesterEmail)}
	}
	return map[string]string{"visibility": "organization"}
}

func decodePackagingIntakeRecord(entry meetingMemoryEntry) (packagingIntakeRecord, bool) {
	if entry.Kind != meetingMemoryKindPackagingIntake {
		return packagingIntakeRecord{}, false
	}
	var record packagingIntakeRecord
	if err := json.Unmarshal([]byte(entry.Metadata["record"]), &record); err != nil || record.ID == "" {
		return packagingIntakeRecord{}, false
	}
	return record, true
}

func (app *kanbanBoardApp) savePackagingIntakeRecord(record packagingIntakeRecord) error {
	if app == nil || app.memory == nil {
		return fmt.Errorf("packaging intake store is unavailable")
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if record.CreatedAt == "" {
		record.CreatedAt = record.UpdatedAt
	}
	text := packagingIntakeRecordText(record)
	metadata := packagingIntakeRecordMetadata(record)
	if _, exists := app.memory.entryByKindAndID(meetingMemoryKindPackagingIntake, record.ID); exists {
		_, _, err := app.memory.updateEntryWithMetadata(meetingMemoryKindPackagingIntake, record.ID, text, metadata)
		return err
	}
	_, _, err := app.memory.appendEntry(meetingMemoryKindPackagingIntake, record.ID, text, metadata)
	return err
}

func (app *kanbanBoardApp) packagingIntakeRecordByID(id string) (packagingIntakeRecord, bool) {
	if app == nil || app.memory == nil {
		return packagingIntakeRecord{}, false
	}
	entry, ok := app.memory.entryByKindAndID(meetingMemoryKindPackagingIntake, strings.TrimSpace(id))
	if !ok {
		return packagingIntakeRecord{}, false
	}
	return decodePackagingIntakeRecord(entry)
}

// packagingIntakeRecordsForThread lists a thread's intake records, oldest
// first. Callers filter by status.
func (app *kanbanBoardApp) packagingIntakeRecordsForThread(threadID string) []packagingIntakeRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	entries := app.memory.entriesOfKindByMetadata(meetingMemoryKindPackagingIntake, "threadId", strings.TrimSpace(threadID))
	records := make([]packagingIntakeRecord, 0, len(entries))
	for _, entry := range entries {
		if record, ok := decodePackagingIntakeRecord(entry); ok {
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt < records[j].CreatedAt })
	return records
}

// pendingPackagingIntakeForThread returns the newest record still waiting on
// an answer in the thread.
func (app *kanbanBoardApp) pendingPackagingIntakeForThread(threadID string) (packagingIntakeRecord, bool) {
	records := app.packagingIntakeRecordsForThread(threadID)
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].pending() {
			return records[index], true
		}
	}
	return packagingIntakeRecord{}, false
}

// pendingPackagingIntakes lists every waiting record (the follow-up watcher's
// thread sweep).
func (app *kanbanBoardApp) pendingPackagingIntakes() []packagingIntakeRecord {
	if app == nil || app.memory == nil {
		return nil
	}
	entries := app.memory.entriesOfKindByMetadata(meetingMemoryKindPackagingIntake, "status", packagingIntakeStatusWaiting)
	records := make([]packagingIntakeRecord, 0, len(entries))
	for _, entry := range entries {
		if record, ok := decodePackagingIntakeRecord(entry); ok && record.pending() {
			records = append(records, record)
		}
	}
	return records
}

// ---------------------------------------------------------------------------
// The seam: called from the append path once Scout is explicitly engaged.
// ---------------------------------------------------------------------------

// packagingIntakeAnswerBinding is how firmly a message is tied to one intake.
// Only a bound message may complete a brief: an unrelated @scout ask in the
// same channel used to answer someone else's open questions.
type packagingIntakeAnswerBinding int

const (
	// Nothing ties the message to this record.
	packagingIntakeAnswerUnbound packagingIntakeAnswerBinding = iota
	// The requester's own top-level message after the ask: theirs to answer,
	// but never a greedy read (packagingIntakeReadsAsAnswer fences that).
	packagingIntakeAnswerImplicit
	// A structured clarifying payload naming the record, or a reply whose
	// chain reaches the ask or Scout's question.
	packagingIntakeAnswerExplicit
)

// packagingIntakeAnswerBindingFor classifies message against record. Callers
// decide what an implicit binding is worth: the append path takes it only in a
// private thread (a channel's untethered answers belong to the follow-up
// watcher, which takes them everywhere).
func packagingIntakeAnswerBindingFor(thread scoutChatThreadRecord, record packagingIntakeRecord, message scoutChatMessageRecord, answers *scoutChatClarifyingAnswers) packagingIntakeAnswerBinding {
	if answers != nil && strings.TrimSpace(answers.CommissionID) == record.ID {
		return packagingIntakeAnswerExplicit
	}
	if message.ReplyTo != nil {
		parent := strings.TrimSpace(message.ReplyTo.MessageID)
		if parent == record.AskMessageID || (record.QuestionMessageID != "" && parent == record.QuestionMessageID) {
			return packagingIntakeAnswerExplicit
		}
		root := scoutChatReplyRootID(thread, parent)
		if root == record.AskMessageID || root == scoutChatReplyRootID(thread, record.AskMessageID) {
			return packagingIntakeAnswerExplicit
		}
		// A reply in some OTHER chain of the same thread is that chain's
		// business, whoever wrote it.
		return packagingIntakeAnswerUnbound
	}
	if normalizeAccountEmail(message.AuthorEmail) != record.RequesterEmail {
		return packagingIntakeAnswerUnbound
	}
	// Only AFTER the ask, and never when the message is itself a fresh work
	// ask: "actually, write me a research report on competitor pricing" starts
	// new work, it does not answer "who is the audience?".
	if askIndex := scoutChatMessageIndex(thread, record.AskMessageID); askIndex >= 0 {
		if index := scoutChatMessageIndex(thread, message.ID); index >= 0 && index <= askIndex {
			return packagingIntakeAnswerUnbound
		}
	}
	if _, isAsk := packagingIntakeDetect(message.Text, nil, nil); isAsk {
		return packagingIntakeAnswerUnbound
	}
	return packagingIntakeAnswerImplicit
}

// packagingIntakeAnswerTarget picks WHICH waiting intake a message answers.
// Every pending record of the thread is considered, newest first, so a second
// commission started in the same channel never strands the first: the record
// the message actually binds to wins, and an explicit binding beats a
// top-level one. acceptImplicit is the caller's policy on an implicit
// binding: the append path takes one only in a private thread (a channel's
// untethered answers are the follow-up watcher's lane, which takes them
// everywhere).
func (app *kanbanBoardApp) packagingIntakeAnswerTarget(thread scoutChatThreadRecord, message scoutChatMessageRecord, answers *scoutChatClarifyingAnswers, acceptImplicit bool) (packagingIntakeRecord, bool) {
	records := app.packagingIntakeRecordsForThread(thread.ID)
	best, found := packagingIntakeRecord{}, false
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if !record.pending() {
			continue
		}
		binding := packagingIntakeAnswerBindingFor(thread, record, message, answers)
		if binding == packagingIntakeAnswerUnbound || (binding == packagingIntakeAnswerImplicit && !acceptImplicit) {
			continue
		}
		if !packagingIntakeAnswerAuthorized(thread, record, message) {
			continue
		}
		if binding == packagingIntakeAnswerExplicit {
			return record, true
		}
		if !found {
			best, found = record, true
		}
	}
	return best, found
}

// packagingIntakeAnswerAuthorized: the asker always may answer; anyone else
// only once the asker has @scout'd AGAIN after the ask (a public ask itself
// always carries @scout, so counting it would make the fence vacuous) or the
// answer @scouts explicitly.
func packagingIntakeAnswerAuthorized(thread scoutChatThreadRecord, record packagingIntakeRecord, message scoutChatMessageRecord) bool {
	author := normalizeAccountEmail(message.AuthorEmail)
	if author == "" || !scoutChatThreadAllowsViewer(thread, author) {
		return false
	}
	if author == record.RequesterEmail {
		return true
	}
	if scoutChatMessageMentionsScout(message) {
		return true
	}
	askIndex := scoutChatMessageIndex(thread, record.AskMessageID)
	if askIndex < 0 {
		return false
	}
	for index := askIndex + 1; index < len(thread.Messages); index++ {
		candidate := thread.Messages[index]
		if normalizeAccountEmail(candidate.AuthorEmail) == record.RequesterEmail && scoutChatMessageMentionsScout(candidate) {
			return true
		}
	}
	return false
}

// packagingIntakeGate carries the facts the append path has already resolved
// about who owns this turn. Chat intake ENRICHES an under-specified plain work
// ask; it is a fallback, never a pre-empt. Every field here says "the ordinary
// routing already owns this", and each one bails the intake so the turn reaches
// the router exactly as it did before the seam existed.
type packagingIntakeGate struct {
	// TargetedAgentWork: the turn @-mentions a hired agent for work.
	TargetedAgentWork bool
	// AgentDirectThread: the thread IS a hired agent's direct thread
	// (strideAgentDirectThreadContext resolved a coworker profile). An
	// UNMENTIONED ask there is still that seat's conversation, and an authored
	// output must reach the server-owned studio exactly as before — Scout must
	// never answer in an agent's own thread.
	AgentDirectThread bool
	// SourceBound: the turn already carries bound source context — a signed
	// meeting-range manifest, a resolved chat/Files attachment, an authorized
	// Drive ref. The router's exact source binding owns those.
	SourceBound bool
}

// packagingIntakeTurn is the append-path seam. It returns (response, true)
// when the intake owned the turn: it asked, completed, proposed, or launched.
// (nil, false) hands the turn back to the ordinary router/launch path.
func (app *kanbanBoardApp) packagingIntakeTurn(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, files []scoutChatFileAttachment, contextRefs []string, gate packagingIntakeGate, commit scoutChatMessageCommitter) (map[string]any, bool) {
	if app == nil || app.memory == nil || user == nil || commit == nil {
		return nil, false
	}
	// An agent seat's turn is never Scout's to hold. This covers the ask AND any
	// later answer: an intake can never have opened here, so there is nothing to
	// close, and a stray interception would put Scout's name on an agent thread.
	if gate.TargetedAgentWork || gate.AgentDirectThread {
		return nil, false
	}
	// A realtime VOICE turn keeps typed parity: it routes. Spoken clarification
	// is not this seam's job — a numbered, bracketed multiple-choice list is a
	// visual affordance and reading it aloud is worse than inferring. Under-
	// specified voice asks infer and proceed on the ordinary route.
	if conversationTurnModalityFromContext(ctx) == conversationModalityPrivateRealtimeVoice {
		return nil, false
	}
	if thread.Riff != nil || thread.MeetingRecord != nil || thread.Intake != "" || strings.TrimSpace(thread.AgentID) != "" || thread.ArchivedAt != "" {
		return nil, false
	}
	// 0. A private thread bound to a Story Studio outline: turns are outline
	// revisions (packaging_story_workshop.go), never intake questions.
	if response, ok := app.packagingStoryThreadTurn(ctx, user, thread, userMessage, commit); ok {
		return response, true
	}
	answers := packagingIntakeAnswersFromContext(ctx)
	// 1. An answer to a waiting intake — whichever waiting record this message
	// is actually bound to.
	if record, waiting := app.packagingIntakeAnswerTarget(thread, userMessage, answers, scoutChatThreadVisibility(thread) != scoutChatVisibilityPublic); waiting {
		structured := []scoutChatClarifyingAnswer(nil)
		if answers != nil && answers.CommissionID == record.ID {
			structured = answers.Answers
		}
		if response, handled := app.packagingIntakeAnswerTurn(ctx, user, thread, userMessage, &record, structured, commit); handled {
			return response, true
		}
		// Not an answer after all (a question back, an aside): the ordinary
		// path — and the follow-up watcher — take it.
		return nil, false
	}
	// 2. A fresh ask — only while intake is enabled, only for an unambiguous
	// kind, and never for the asks other deterministic guards already own
	// (channel keyword/prefix modes, the STRIDE Insights & Opportunities
	// request). A two-kind tie ("a deck and a memo") is left to the router
	// rather than asked: the intake never opens with "which deliverable?".
	if !packagingChatIntakeEnabled() {
		return nil, false
	}
	// Intake never swallows a request the existing routing would have bound or
	// started. Three deterministic yields, in the order they cost least:
	//   - SourceBound: a meeting-range manifest or a resolved source is already
	//     attached to this turn; the router carries that binding into the work.
	//   - scoutGuardEligibleMessage: the SAME bar the pre-router guard uses for
	//     "this is an unambiguous work instruction". A question to Scout ("can
	//     you make a 10-slide deck?") is answered by building it on the existing
	//     premium route — Scout never answers a question with two questions.
	//   - deterministicRouterGuard: an exact registry/process name or a reviewed
	//     full-run phrase is a conversation-owned route (the goal spine). It
	//     names its own capability and must not be reopened as a commission.
	if gate.SourceBound || !scoutGuardEligibleMessage(userMessage.Text) || deterministicRouterGuard(userMessage.Text) != nil {
		return nil, false
	}
	kind, isAsk := packagingIntakeDetect(userMessage.Text, files, userMessage.ReplyTo)
	if !isAsk || kind == "" || isSTRIDEInsightsOutcomeRequest(userMessage.Text) || packagingIntakeBuildsFromExistingMaterial(userMessage.Text) {
		return nil, false
	}
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		// Channel keyword/prefix modes keep their deterministic proposal card;
		// a reply-sourced ask keeps the router's exact source binding.
		if scoutChatThreadModeForChannelText(userMessage.Text) != "" || userMessage.ReplyTo != nil {
			return nil, false
		}
	} else if kind == packagingIntakeKindResearch {
		// Private research stays on the router: its deep_research contract and
		// material-spend approval lane own that spine (D3's Research Desk sheet
		// is the structured private intake). Chat-intake research runs in
		// channels and through the studio sheet.
		return nil, false
	}
	brief := packagingIntakeDeterministicBrief(kind, thread, userMessage, files, contextRefs, user)
	classifier := "deterministic"
	if len(briefGaps(kind, brief)) > 0 {
		// The bounded model pass only runs when it can remove a question.
		brief, classifier = app.packagingIntakeClassifyWithModel(ctx, brief, false)
	}
	gaps := briefGaps(kind, brief)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := packagingIntakeRecord{
		ID: packagingIntakeRecordID(thread.ID, userMessage.ID), Kind: kind, ThreadID: thread.ID,
		ThreadVisibility: scoutChatThreadVisibility(thread), AskMessageID: userMessage.ID,
		RequesterEmail: normalizeAccountEmail(user.Email), RequesterName: scoutChatAuthorName(user),
		Brief: brief, Classifier: classifier, Fence: channelThreadRecallFence(thread), CreatedAt: now, UpdatedAt: now,
	}
	if len(gaps) == 0 {
		if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
			// Public channels: the existing deterministic proposal gate below
			// (in the append path) is the launch door — nothing to add here.
			record.Status = packagingIntakeStatusBriefComplete
			if err := app.savePackagingIntakeRecord(record); err != nil {
				log.Errorf("packaging intake: save complete public record: %v", err)
			}
			return nil, false
		}
		record.Status = packagingIntakeStatusBriefComplete
		return app.packagingIntakeContinue(ctx, user, thread, userMessage, &record, commit)
	}
	record.Status = packagingIntakeStatusWaiting
	record.OpenQuestions = gaps
	record.WaitingOn = normalizeAccountEmail(user.Email)
	record.WaitingOnName = scoutChatAuthorName(user)
	record.AskRound++
	for _, gap := range gaps {
		record.AskedQuestionIDs = append(record.AskedQuestionIDs, gap.ID)
	}
	question := app.packagingIntakeQuestionMessage(thread, userMessage, user, record)
	record.QuestionMessageID = question.ID
	if err := app.savePackagingIntakeRecord(record); err != nil {
		log.Errorf("packaging intake: save waiting record: %v", err)
		return nil, false
	}
	saved, err := commit(userMessage, question)
	if err != nil {
		log.Errorf("packaging intake: commit question: %v", err)
		return nil, false
	}
	return map[string]any{
		"ok": true, "message": userMessage, "answer": question, "thread": saved,
		"intentOutcome": string(conversationIntentClarifyOnce), "clarifying": question.Clarifying,
		"packagingIntake": record.ID, "providerCalls": 0,
	}, true
}

// packagingIntakeQuestionMessage builds the ONE threaded reply: replyTo = the
// asking message, the asker mentioned, numbered questions, pill data.
func (app *kanbanBoardApp) packagingIntakeQuestionMessage(thread scoutChatThreadRecord, ask scoutChatMessageRecord, user *userAccount, record packagingIntakeRecord) scoutChatMessageRecord {
	replyTo := &scoutChatReplyRef{
		MessageID: ask.ID, RootMessageID: scoutChatMessageReplyRootID(thread, ask),
		AuthorName:  firstNonEmptyString(strings.TrimSpace(ask.AuthorName), scoutChatAuthorName(user)),
		AuthorEmail: normalizeAccountEmail(ask.AuthorEmail), Text: trimForStorage(ask.Text, 280),
	}
	// The id varies per ASK: the round plus the exact questions being asked.
	// Two asks of the same intake are two messages and must never share an id
	// (the thread appends without id dedupe, and scoutChatMessageIndex would
	// then resolve both to the first).
	open := make([]string, 0, len(record.OpenQuestions))
	for _, question := range record.OpenQuestions {
		open = append(open, question.ID)
	}
	sort.Strings(open)
	return scoutChatMessageRecord{
		ID:                "scout-chat-message-intake-" + sha256Hex([]byte("packaging-intake-question/v1\x00" + record.ID + "\x00" + strconv.Itoa(record.AskRound) + "\x00" + strings.Join(open, ",")))[:24],
		Kind:              "message",
		Role:              "scout",
		AuthorName:        scoutParticipantName,
		IntentOutcome:     string(conversationIntentClarifyOnce),
		Via:               packagingIntakeVia,
		CausedByMessageID: ask.ID,
		Text:              renderPackagingIntakeQuestionText(user, record.Kind, record.OpenQuestions),
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:           replyTo,
		Clarifying: &scoutChatClarifying{
			CommissionID: record.ID, Questions: record.OpenQuestions,
			WaitingOn: record.WaitingOn, WaitingOnName: record.WaitingOnName,
		},
	}
}

// packagingIntakeAnswerTurn folds a reply into a waiting intake. The
// per-intake lock spans read → apply → decide → launch → status write: the
// record is re-read INSIDE the lock and the answers are applied to that fresh
// copy, so two answers landing at once (two tabs, the append path racing the
// watcher) both survive instead of the loser writing a stale brief over the
// winner's field. record is updated in place for the caller.
// (nil, false) means the reply closed nothing — not an answer after all.
func (app *kanbanBoardApp) packagingIntakeAnswerTurn(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, record *packagingIntakeRecord, structured []scoutChatClarifyingAnswer, commit scoutChatMessageCommitter) (map[string]any, bool) {
	if record == nil {
		return nil, false
	}
	unlock := app.lockPackagingIntake(record.ID)
	defer unlock()
	current, ok := app.packagingIntakeRecordByID(record.ID)
	if ok {
		if packagingIntakeTerminal(current.Status) || !current.pending() {
			return nil, false
		}
		*record = current
	}
	if applyBriefAnswers(record, structured, userMessage.Text) == 0 && len(structured) == 0 {
		return nil, false
	}
	return app.packagingIntakeContinueLocked(ctx, user, thread, userMessage, record, commit)
}

// packagingIntakeContinue runs after answers land (or for a gap-free ask):
// still-open questions → one more threaded ask; complete → launch (private)
// or proposal card (public, the existing accept door).
func (app *kanbanBoardApp) packagingIntakeContinue(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, record *packagingIntakeRecord, commit scoutChatMessageCommitter) (map[string]any, bool) {
	// One launch per intake: the append path and the follow-up watcher (or two
	// tabs answering at once) can reach here for the same record concurrently.
	// The per-intake lock spans read → decide → launch → status write, and the
	// re-read after acquiring turns the loser into a no-op that hands the turn
	// back (the winner's card is already in the thread).
	unlock := app.lockPackagingIntake(record.ID)
	defer unlock()
	if current, ok := app.packagingIntakeRecordByID(record.ID); ok && packagingIntakeTerminal(current.Status) {
		return nil, false
	}
	return app.packagingIntakeContinueLocked(ctx, user, thread, userMessage, record, commit)
}

func (app *kanbanBoardApp) lockPackagingIntake(recordID string) func() {
	lock := app.scoutChatThreadLock("packaging-intake-launch-" + recordID)
	lock.Lock()
	return lock.Unlock
}

func packagingIntakeTerminal(status string) bool {
	return oneOf(status, packagingIntakeStatusLaunched, packagingIntakeStatusProposed)
}

func (app *kanbanBoardApp) packagingIntakeContinueLocked(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, record *packagingIntakeRecord, commit scoutChatMessageCommitter) (map[string]any, bool) {
	asker := user
	if normalizeAccountEmail(user.Email) != record.RequesterEmail {
		if requester := accountStore().findUser(record.RequesterEmail); requester != nil {
			asker = requester
		}
	}
	if len(record.OpenQuestions) > 0 {
		record.Status = packagingIntakeStatusWaiting
		record.WaitingOn = record.RequesterEmail
		record.WaitingOnName = record.RequesterName
		record.AskRound++
		for _, question := range record.OpenQuestions {
			if !packagingIntakeContains(record.AskedQuestionIDs, question.ID) {
				record.AskedQuestionIDs = append(record.AskedQuestionIDs, question.ID)
			}
		}
		ask := userMessage
		if index := scoutChatMessageIndex(thread, record.AskMessageID); index >= 0 {
			ask = thread.Messages[index]
		}
		question := app.packagingIntakeQuestionMessage(thread, ask, asker, *record)
		// The follow-up question threads under the ANSWER so the asker sees
		// it where they typed, while the ancestry still roots at the ask.
		question.ReplyTo = &scoutChatReplyRef{
			MessageID: userMessage.ID, RootMessageID: firstNonEmptyString(scoutChatMessageReplyRootID(thread, userMessage), record.AskMessageID),
			AuthorName: scoutChatAuthorName(user), AuthorEmail: normalizeAccountEmail(user.Email), Text: trimForStorage(userMessage.Text, 280),
		}
		question.CausedByMessageID = userMessage.ID
		record.QuestionMessageID = question.ID
		if err := app.savePackagingIntakeRecord(*record); err != nil {
			log.Errorf("packaging intake: save follow-up record: %v", err)
			return nil, false
		}
		saved, err := commit(userMessage, question)
		if err != nil {
			log.Errorf("packaging intake: commit follow-up question: %v", err)
			return nil, false
		}
		return map[string]any{
			"ok": true, "message": userMessage, "answer": question, "thread": saved,
			"intentOutcome": string(conversationIntentClarifyOnce), "clarifying": question.Clarifying,
			"packagingIntake": record.ID, "providerCalls": 0,
		}, true
	}
	record.Status = packagingIntakeStatusBriefComplete
	record.WaitingOn = ""
	record.WaitingOnName = ""
	if scoutChatThreadVisibility(thread) == scoutChatVisibilityPublic {
		return app.packagingIntakePropose(thread, userMessage, user, record, commit)
	}
	return app.packagingIntakeLaunchLocked(ctx, thread, userMessage, asker, record, commit)
}

func packagingIntakeWorkstreamMode(kind string) string {
	if kind == packagingIntakeKindPresentation {
		return "design"
	}
	return "research"
}

// packagingIntakeProcessID is the studio process a completed brief runs — the
// SAME routing the private launcher uses (legacyPackagingCommissionLauncher):
// presentations are the Packaging Studio, documents and story outlines are
// document_report. "" means research, which stays a research workstream on
// both sides. Without this a channel commission accepted from the card ran a
// plain research agent whatever the asker commissioned.
func packagingIntakeProcessID(kind string) string {
	switch kind {
	case packagingIntakeKindPresentation:
		return packagingStudioProcessID
	case packagingIntakeKindDocument, packagingIntakeKindStory:
		return documentReportProcessID
	}
	return ""
}

// packagingIntakeProposal builds the card the accept route reads: a tool_run
// bound to the studio process for deliverable kinds (conversationWorkRegistryTool
// → launchGoalThread with that process), a research workstream otherwise.
func packagingIntakeProposal(record *packagingIntakeRecord) *scoutRouterProposal {
	objective := renderPackagingIntakeObjective(record.Brief)
	query := strings.TrimSpace(record.Brief.Ask)
	summary := "Brief complete — Scout prepared the " + packagingIntakeKindLabel(record.Kind) + " commission. Review or edit it before this runs once."
	if processID := packagingIntakeProcessID(record.Kind); processID != "" {
		if proposal := scoutRouterProposalForToolID(processID, objective, query); proposal != nil {
			proposal.IntentOutcome = string(conversationIntentApprovalRequired)
			proposal.EffectClass = "expanded_audience"
			proposal.ContextRefs = encodeAssistantContextRefs(record.Brief.ContextRefs)
			proposal.Summary = summary
			return proposal
		}
	}
	mode := packagingIntakeWorkstreamMode(record.Kind)
	return &scoutRouterProposal{
		Kind:          scoutRouterProposalKindWorkstream,
		IntentOutcome: string(conversationIntentApprovalRequired),
		EffectClass:   "expanded_audience",
		Mode:          mode,
		Objective:     objective,
		Query:         query,
		ContextRefs:   encodeAssistantContextRefs(record.Brief.ContextRefs),
		Lane:          scoutProposalLane(mode, "", ""),
		WeightLabel:   scoutProposalWeightQuickPass,
		Summary:       summary,
	}
}

// packagingIntakePropose mints the proposal card, threaded under the answer.
// The accept route (resolveScoutChatProposal → startAcceptedPublicScoutWork)
// stays the only public launch door.
func (app *kanbanBoardApp) packagingIntakePropose(thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, user *userAccount, record *packagingIntakeRecord, commit scoutChatMessageCommitter) (map[string]any, bool) {
	proposal := packagingIntakeProposal(record)
	proposalMessage := scoutChatMessageRecord{
		ID:            "scout-chat-message-intake-proposal-" + sha256Hex([]byte("packaging-intake-proposal/v1\x00" + record.ID))[:24],
		Kind:          scoutChatMessageKindProposal,
		Role:          "scout",
		AuthorName:    scoutParticipantName,
		IntentOutcome: string(conversationIntentApprovalRequired),
		Via:           packagingIntakeVia,
		Text:          proposal.Summary,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Proposal:      proposal, CausedByMessageID: userMessage.ID,
		ReplyTo: &scoutChatReplyRef{
			MessageID: userMessage.ID, RootMessageID: firstNonEmptyString(scoutChatMessageReplyRootID(thread, userMessage), record.AskMessageID),
			AuthorName: scoutChatAuthorName(user), AuthorEmail: normalizeAccountEmail(user.Email), Text: trimForStorage(userMessage.Text, 280),
		},
	}
	record.Status = packagingIntakeStatusProposed
	record.ProposalMessageID = proposalMessage.ID
	if err := app.savePackagingIntakeRecord(*record); err != nil {
		log.Errorf("packaging intake: save proposed record: %v", err)
		return nil, false
	}
	saved, err := commit(userMessage, proposalMessage)
	if err != nil {
		log.Errorf("packaging intake: commit proposal: %v", err)
		return nil, false
	}
	recordProposalEvent(proposalEventMinted, proposalMessage.ID, scoutChatProposalMintFields(
		proposalSourceDeterministicGuard, thread.ID, userMessage.ID, proposal,
	))
	return map[string]any{
		"ok": true, "message": userMessage, "answer": proposalMessage, "proposal": proposal, "thread": saved,
		"approvalRequired": true, "intentOutcome": string(conversationIntentApprovalRequired),
		"packagingIntake": record.ID, "providerCalls": 0,
	}, true
}

// packagingIntakeLaunch starts the commission through the launcher and posts
// one threaded status card. A launch failure is reported in the thread, never
// swallowed, and the record keeps the error.
func (app *kanbanBoardApp) packagingIntakeLaunch(ctx context.Context, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, asker *userAccount, record *packagingIntakeRecord, commit scoutChatMessageCommitter) (map[string]any, bool) {
	unlock := app.lockPackagingIntake(record.ID)
	defer unlock()
	if current, ok := app.packagingIntakeRecordByID(record.ID); ok && packagingIntakeTerminal(current.Status) {
		return nil, false
	}
	return app.packagingIntakeLaunchLocked(ctx, thread, userMessage, asker, record, commit)
}

func (app *kanbanBoardApp) packagingIntakeLaunchLocked(ctx context.Context, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, asker *userAccount, record *packagingIntakeRecord, commit scoutChatMessageCommitter) (map[string]any, bool) {
	launcher, _ := app.packagingCommissionLauncher(record.Kind)
	receipt, err := launcher.createPackagingCommission(asker, record.Kind, record.Brief)
	replyTo := &scoutChatReplyRef{
		MessageID: userMessage.ID, RootMessageID: firstNonEmptyString(scoutChatMessageReplyRootID(thread, userMessage), record.AskMessageID),
		AuthorName: firstNonEmptyString(strings.TrimSpace(userMessage.AuthorName), scoutChatAuthorName(asker)), AuthorEmail: normalizeAccountEmail(userMessage.AuthorEmail), Text: trimForStorage(userMessage.Text, 280),
	}
	if userMessage.ID == record.AskMessageID {
		replyTo.RootMessageID = scoutChatMessageReplyRootID(thread, userMessage)
	}
	if err != nil {
		record.Status = packagingIntakeStatusFailed
		record.Error = err.Error()
		if saveErr := app.savePackagingIntakeRecord(*record); saveErr != nil {
			log.Errorf("packaging intake: save failed record: %v", saveErr)
		}
		unavailable := scoutChatMessageRecord{
			ID: "scout-chat-message-intake-failed-" + sha256Hex([]byte("packaging-intake-failed/v1\x00" + record.ID))[:24], Kind: "message", Role: "scout",
			AuthorName: scoutParticipantName, IntentOutcome: string(conversationIntentUnavailable), Via: packagingIntakeVia,
			Text:      "I couldn't start that " + packagingIntakeKindLabel(record.Kind) + " safely: " + err.Error() + ". Nothing else was launched.",
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ReplyTo: replyTo, CausedByMessageID: userMessage.ID,
		}
		saved, commitErr := commit(userMessage, unavailable)
		if commitErr != nil {
			log.Errorf("packaging intake: commit failure notice: %v", commitErr)
			return nil, false
		}
		return map[string]any{
			"ok": true, "message": userMessage, "answer": unavailable, "thread": saved,
			"intentOutcome": string(conversationIntentUnavailable), "packagingIntake": record.ID,
			"unavailable": map[string]any{"code": "launch_unavailable", "message": unavailable.Text},
		}, true
	}
	record.Status = packagingIntakeStatusLaunched
	record.CommissionID = receipt.CommissionID
	record.ArtifactID = receipt.ArtifactID
	status := scoutChatMessageRecord{
		ID:                "scout-chat-message-intake-launch-" + sha256Hex([]byte("packaging-intake-launch/v1\x00" + record.ID))[:24],
		Kind:              "message",
		Role:              "scout",
		AuthorName:        scoutParticipantName,
		IntentOutcome:     string(conversationIntentStartPrivateWork),
		Via:               packagingIntakeVia,
		Text:              firstNonEmptyString(strings.TrimSpace(receipt.Label), "Brief complete — the "+packagingIntakeKindLabel(record.Kind)+" is running in the Packaging Studio."),
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		ReplyTo:           replyTo,
		CausedByMessageID: userMessage.ID,
	}
	if receipt.Thread != nil {
		status.Kind = "thread"
		status.Thread = receipt.Thread
	}
	record.LaunchMessageID = status.ID
	if err := app.savePackagingIntakeRecord(*record); err != nil {
		log.Errorf("packaging intake: save launched record: %v", err)
	}
	saved, commitErr := commit(userMessage, status)
	if commitErr != nil {
		log.Errorf("packaging intake: commit launch card: %v", commitErr)
		return nil, false
	}
	response := map[string]any{
		"ok": true, "message": userMessage, "answer": status, "thread": saved,
		"intentOutcome": string(conversationIntentStartPrivateWork), "packagingIntake": record.ID,
		"commissionId": receipt.CommissionID,
	}
	if receipt.ArtifactID != "" {
		if artifact, ok := app.osArtifactByID(receipt.ArtifactID); ok {
			response["artifact"] = artifact
		}
	}
	_ = ctx
	return response, true
}

// packagingIntakeSnapshotForThread is the read projection the studio row and
// the thread UI share: every intake of the thread with status, waiting-on,
// open questions and the message ids to deep-link back.
func (app *kanbanBoardApp) packagingIntakeSnapshotForThread(threadID string) []map[string]any {
	records := app.packagingIntakeRecordsForThread(threadID)
	rows := make([]map[string]any, 0, len(records))
	for _, record := range records {
		rows = append(rows, map[string]any{
			"id": record.ID, "kind": record.Kind, "status": record.Status, "threadId": record.ThreadID,
			"askMessageId": record.AskMessageID, "questionMessageId": record.QuestionMessageID,
			"waitingOn": record.WaitingOn, "waitingOnName": record.WaitingOnName, "openQuestions": record.OpenQuestions,
			"commissionId": record.CommissionID, "artifactId": record.ArtifactID, "briefComplete": record.Status != packagingIntakeStatusWaiting,
			"updatedAt": record.UpdatedAt,
		})
	}
	return rows
}
