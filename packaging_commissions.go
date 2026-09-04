package main

// packaging_commissions.go — Wave 11 (Packaging Studio) D5/D6/D9: structured
// commissions. The hub's three studios (Research Desk, Presentation Studio,
// Story Studio) POST a typed brief here; the server validates it per kind,
// renders it as the user's own message in a private Scout conversation, and
// launches the EXISTING seams — a research workstream (deep_research contract)
// or the Packaging Studio process — through startConversationPrivateWork so
// every goal keeps its authenticated conversation route receipt. The brief is
// stamped on the resulting work root for provenance and projected on the
// studio row (studio_projects.go). Story Studio mints an outline artifact
// (source story_studio) with the Wave 4 version journal, bound to a private
// thread for turn-based edits; "build the deck" hands that outline to a
// presentation commission as the settled narrative.
//
// A `commissionFirst` presentation chains research → presentation: the
// research root carries the pending presentation brief and the chain advances
// (launches the deck) when the requester next reads the commission or the
// studio list after the research result is complete.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	packagingCommissionKindResearch     = "research"
	packagingCommissionKindPresentation = "presentation"
	packagingCommissionKindStory        = "story"

	packagingCommissionKindMetadataKey      = "packagingCommissionKind"
	packagingCommissionBriefMetadataKey     = "packagingBrief"
	packagingCommissionAtMetadataKey        = "packagingCommissionAt"
	packagingCommissionByMetadataKey        = "packagingCommissionBy"
	packagingCommissionMessageIDMetadataKey = "packagingCommissionMessageId"
	packagingCommissionThreadIDMetadataKey  = "packagingCommissionThreadId"
	packagingCommissionOperationMetadataKey = "packagingCommissionOperationId"
	// packagingCommissionOperationDigestMetadataKey pins the operation BODY a
	// commission was launched from. An operationId is client-supplied, so
	// adopting a prior commission by id alone would hand a different brief
	// (or a different kind) the wrong root; the digest makes the adoption an
	// identity check rather than a name check.
	packagingCommissionOperationDigestMetadataKey = "packagingCommissionOperationDigest"
	// packagingCommissionIntakeIDMetadataKey binds a commission root to the
	// chat-intake record that produced it, so the hub row can project that
	// intake's waiting state ("waiting on you · N questions") for real asks.
	packagingCommissionIntakeIDMetadataKey  = "packagingCommissionIntakeId"
	packagingImageryModeMetadataKey         = "packagingImageryMode"
	packagingChainStateMetadataKey          = "packagingChainState"
	packagingChainBriefMetadataKey          = "packagingChainPresentationBrief"
	packagingChainPresentationIDMetadataKey = "packagingChainPresentationId"
	packagingChainStateWaiting              = "waiting"
	packagingChainStateLaunched             = "launched"

	packagingStorySource              = "story_studio"
	packagingStoryMode                = "story_outline"
	packagingStoryThreadIDMetadataKey = "storyThreadId"
	packagingStoryBriefMetadataKey    = "storyBrief"

	packagingImageryFullBleed = "full-bleed"
	packagingImageryOnSlide   = "on-slide"
	packagingImageryHybrid    = "hybrid"

	packagingBriefMaxSources     = 12
	packagingBriefTextMaxRunes   = 2000
	packagingBriefFieldMaxRunes  = 400
	packagingStoryOutlineMaxByte = documentStudioMaxBytes
	packagingCommissionLaunchSrc = "packaging_studio"
)

var (
	packagingResearchScopes  = []string{"company", "market", "competitor", "technical", "people"}
	packagingResearchDepths  = []string{"brief", "standard", "deep"}
	packagingResearchFormats = []string{"report", "one-pager", "memo"}
	packagingCopyStyles      = []string{"crisp", "narrative", "data-led", "persuasive"}
	packagingImageryModes    = []string{packagingImageryFullBleed, packagingImageryOnSlide, packagingImageryHybrid}
	packagingLengthWords     = map[string]int{"short": 8, "standard": 12, "long": 20}
)

// packagingSource is one authorized input a brief names: a Drive file (the
// same {ref, sourceId, sourceRevision} shape as workRequestContextRef), an
// existing artifact (research report, document) the requester can read, or a
// public URL. Drive refs become goal contextRefs; artifacts and URLs are
// folded into the objective by title/id so the runner can recall them.
type packagingSource struct {
	Ref            string `json:"ref,omitempty"`
	SourceID       string `json:"sourceId,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
	ArtifactID     string `json:"artifactId,omitempty"`
	URL            string `json:"url,omitempty"`
	// Title is server-resolved for artifacts (never trusted from the client).
	Title string `json:"title,omitempty"`
}

type packagingResearchBrief struct {
	Scope    string            `json:"scope"`
	Depth    string            `json:"depth"`
	Format   string            `json:"format"`
	Audience string            `json:"audience,omitempty"`
	Question string            `json:"question"`
	Sources  []packagingSource `json:"sources,omitempty"`
}

type packagingLookFeel struct {
	ThemeID string `json:"themeId,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

type packagingResearchInput struct {
	ArtifactID      string `json:"artifactId,omitempty"`
	CommissionFirst bool   `json:"commissionFirst,omitempty"`
	// Brief is the research brief used when CommissionFirst is set; when
	// absent the server derives one from the presentation subject/audience.
	Brief *packagingResearchBrief `json:"brief,omitempty"`
}

type packagingPresentationBrief struct {
	Subject                string                  `json:"subject"`
	Audience               string                  `json:"audience,omitempty"`
	CopyStyle              string                  `json:"copyStyle,omitempty"`
	LookFeel               packagingLookFeel       `json:"lookFeel"`
	ImageryMode            string                  `json:"imageryMode,omitempty"`
	Research               *packagingResearchInput `json:"research,omitempty"`
	StoryOutlineArtifactID string                  `json:"storyOutlineArtifactId,omitempty"`
	// StoryVersion is the outline version handed over as the settled
	// narrative (provenance; stamped server-side, never trusted from a client).
	StoryVersion int    `json:"storyVersion,omitempty"`
	Length       string `json:"length,omitempty"`
	// Sources are the Drive files, artifacts and links the asker named for the
	// deck itself — authorized exactly like the research brief's, so a chat ask
	// that attaches "Q3-results.xlsx" builds the deck from that file instead of
	// inventing the numbers.
	Sources []packagingSource `json:"sources,omitempty"`
}

type packagingStoryBrief struct {
	Subject  string `json:"subject"`
	Audience string `json:"audience,omitempty"`
	Thesis   string `json:"thesis,omitempty"`
	Length   string `json:"length,omitempty"`
}

// packagingBrief is the persisted union: exactly one kind-specific brief is
// set. It is stored verbatim (JSON) on the work root for provenance.
type packagingBrief struct {
	Kind         string                      `json:"kind"`
	Research     *packagingResearchBrief     `json:"research,omitempty"`
	Presentation *packagingPresentationBrief `json:"presentation,omitempty"`
	Story        *packagingStoryBrief        `json:"story,omitempty"`
}

type packagingCommissionView struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Brief     *packagingBrief `json:"brief,omitempty"`
	ProjectID string          `json:"projectId"`
	ThreadID  string          `json:"threadId,omitempty"`
	MessageID string          `json:"messageId,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
	// Chain describes a research → presentation chain: waiting while the
	// research runs, launched once the deck commission exists.
	Chain *packagingChainView `json:"chain,omitempty"`
	// Waiting state comes from the chat-intake record (packaging_intake.go)
	// bound to this commission: who Scout is waiting on and the open
	// clarifying questions, so "waiting on you · N questions" survives reload.
	packagingCommissionWaitingState
}

// packagingCommissionWaitingState is the intake projection shared by the
// commission view and the studio row's commission ref.
type packagingCommissionWaitingState struct {
	IntakeID      string                    `json:"intakeId,omitempty"`
	WaitingOn     string                    `json:"waitingOn,omitempty"`
	WaitingOnName string                    `json:"waitingOnName,omitempty"`
	Questions     []packagingIntakeQuestion `json:"questions,omitempty"`
	BriefComplete bool                      `json:"briefComplete"`
}

// packagingCommissionWaitingStateFor resolves the intake record bound to a
// commission root — by commission id, else the record whose ask is the
// commission's own message — and projects it only when the viewer can read
// the intake's thread (the same visibility fence the thread UI applies). A
// commission with no intake (a studio sheet post) is simply brief-complete.
func (app *kanbanBoardApp) packagingCommissionWaitingStateFor(ctx context.Context, viewer *userAccount, root meetingMemoryEntry) packagingCommissionWaitingState {
	state := packagingCommissionWaitingState{BriefComplete: true}
	if app == nil || app.memory == nil || viewer == nil || strings.TrimSpace(root.ID) == "" {
		return state
	}
	var record packagingIntakeRecord
	found := false
	for _, entry := range app.memory.entriesOfKindByMetadata(meetingMemoryKindPackagingIntake, "commissionId", root.ID) {
		candidate, ok := decodePackagingIntakeRecord(entry)
		if !ok {
			continue
		}
		if !found || candidate.UpdatedAt > record.UpdatedAt {
			record, found = candidate, true
		}
	}
	if !found {
		// The launch-time binding: an intake record only stamps its own
		// commissionId once the launch SUCCEEDS (packaging_intake.go), which is
		// also the moment it stops being pending — so a commission launched
		// from chat would otherwise never resolve back to its intake. The root
		// carries the record id from the launch itself.
		if intakeID := strings.TrimSpace(root.Metadata[packagingCommissionIntakeIDMetadataKey]); intakeID != "" {
			if candidate, ok := app.packagingIntakeRecordByID(intakeID); ok {
				record, found = candidate, true
			}
		}
	}
	if !found {
		threadID := strings.TrimSpace(root.Metadata[packagingCommissionThreadIDMetadataKey])
		messageID := strings.TrimSpace(root.Metadata[packagingCommissionMessageIDMetadataKey])
		if threadID != "" && messageID != "" {
			for _, candidate := range app.packagingIntakeRecordsForThread(threadID) {
				if candidate.AskMessageID == messageID {
					record, found = candidate, true
				}
			}
		}
	}
	if !found {
		return state
	}
	if thread, _, err := app.scoutChatThreadByID(viewer.Email, record.ThreadID); err != nil || !scoutChatThreadAllowsViewer(thread, viewer.Email) {
		return state
	}
	state.IntakeID = record.ID
	state.BriefComplete = !record.pending()
	if record.pending() {
		state.WaitingOn = record.WaitingOn
		state.WaitingOnName = record.WaitingOnName
		state.Questions = append([]packagingIntakeQuestion(nil), record.OpenQuestions...)
	}
	return state
}

type packagingChainView struct {
	State          string `json:"state"`
	PresentationID string `json:"presentationId,omitempty"`
}

type packagingBriefError struct{ message string }

func (err *packagingBriefError) Error() string { return err.message }

func packagingBriefInvalid(format string, args ...any) error {
	return &packagingBriefError{message: fmt.Sprintf(format, args...)}
}

func packagingBoundedText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return value
}

func packagingLower(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// packagingLengthSlides maps a brief length onto the slide-count vocabulary
// the engine parses from the objective (packagingRequestedSlideCount). A bare
// integer is an exact slide count; short/standard/long are the three pills.
func packagingLengthSlides(length string) (int, error) {
	length = packagingLower(length)
	if length == "" {
		return 0, nil
	}
	if count, ok := packagingLengthWords[length]; ok {
		return count, nil
	}
	count, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(length, " slides"), " slide"))
	if err != nil || count < 1 || count > 40 {
		return 0, packagingBriefInvalid("length must be short, standard, long, or a slide count between 1 and 40")
	}
	return count, nil
}

func validatePackagingSources(sources []packagingSource) ([]packagingSource, error) {
	if len(sources) > packagingBriefMaxSources {
		return nil, packagingBriefInvalid("at most %d sources per brief", packagingBriefMaxSources)
	}
	cleaned := make([]packagingSource, 0, len(sources))
	for _, source := range sources {
		source.Ref = strings.TrimSpace(source.Ref)
		source.SourceID = strings.TrimSpace(source.SourceID)
		source.SourceRevision = strings.TrimSpace(source.SourceRevision)
		source.ArtifactID = strings.TrimSpace(source.ArtifactID)
		source.URL = strings.TrimSpace(source.URL)
		source.Title = ""
		// A bare "artifact|<id>" or "https://…" ref is accepted the way the
		// Drive picker's "file|<id>" is, so the sheet can post one list.
		if source.Ref != "" && source.ArtifactID == "" && source.URL == "" {
			switch {
			case strings.HasPrefix(source.Ref, "artifact|"):
				source.ArtifactID = strings.TrimSpace(strings.TrimPrefix(source.Ref, "artifact|"))
				source.Ref = ""
			case strings.HasPrefix(strings.ToLower(source.Ref), "http://"), strings.HasPrefix(strings.ToLower(source.Ref), "https://"):
				source.URL = source.Ref
				source.Ref = ""
			}
		}
		set := 0
		if source.Ref != "" {
			set++
			if !strings.HasPrefix(source.Ref, "file|") || strings.TrimSpace(strings.TrimPrefix(source.Ref, "file|")) == "" {
				return nil, packagingBriefInvalid("a Drive source must be a file|<id> ref")
			}
		}
		if source.ArtifactID != "" {
			set++
			if !strideIdentifier(source.ArtifactID) && !strings.HasPrefix(source.ArtifactID, "os-artifact-") {
				return nil, packagingBriefInvalid("an artifact source must name an artifact id")
			}
		}
		if source.URL != "" {
			set++
			parsed, err := url.Parse(source.URL)
			if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || len(source.URL) > 2048 {
				return nil, packagingBriefInvalid("a link source must be an http(s) URL")
			}
		}
		if set != 1 {
			return nil, packagingBriefInvalid("each source is exactly one Drive file, artifact, or link")
		}
		cleaned = append(cleaned, source)
	}
	return cleaned, nil
}

func validatePackagingResearchBrief(brief *packagingResearchBrief) error {
	if brief == nil {
		return packagingBriefInvalid("research brief is required")
	}
	brief.Scope = packagingLower(brief.Scope)
	brief.Depth = packagingLower(brief.Depth)
	brief.Format = packagingLower(brief.Format)
	brief.Audience = packagingBoundedText(brief.Audience, packagingBriefFieldMaxRunes)
	brief.Question = packagingBoundedText(brief.Question, packagingBriefTextMaxRunes)
	if !oneOf(brief.Scope, packagingResearchScopes...) {
		return packagingBriefInvalid("scope must be one of %s", strings.Join(packagingResearchScopes, ", "))
	}
	if !oneOf(brief.Depth, packagingResearchDepths...) {
		return packagingBriefInvalid("depth must be one of %s", strings.Join(packagingResearchDepths, ", "))
	}
	if !oneOf(brief.Format, packagingResearchFormats...) {
		return packagingBriefInvalid("format must be one of %s", strings.Join(packagingResearchFormats, ", "))
	}
	if brief.Question == "" {
		return packagingBriefInvalid("question is required")
	}
	sources, err := validatePackagingSources(brief.Sources)
	if err != nil {
		return err
	}
	brief.Sources = sources
	return nil
}

func validatePackagingPresentationBrief(brief *packagingPresentationBrief) error {
	if brief == nil {
		return packagingBriefInvalid("presentation brief is required")
	}
	brief.Subject = packagingBoundedText(brief.Subject, packagingBriefTextMaxRunes)
	brief.Audience = packagingBoundedText(brief.Audience, packagingBriefFieldMaxRunes)
	brief.CopyStyle = packagingLower(brief.CopyStyle)
	brief.ImageryMode = packagingLower(brief.ImageryMode)
	brief.LookFeel.ThemeID = packagingLower(brief.LookFeel.ThemeID)
	brief.LookFeel.Notes = packagingBoundedText(brief.LookFeel.Notes, packagingBriefFieldMaxRunes)
	brief.StoryOutlineArtifactID = strings.TrimSpace(brief.StoryOutlineArtifactID)
	brief.Length = packagingLower(brief.Length)
	if brief.Subject == "" {
		return packagingBriefInvalid("subject is required")
	}
	if brief.CopyStyle != "" && !oneOf(brief.CopyStyle, packagingCopyStyles...) {
		return packagingBriefInvalid("copyStyle must be one of %s", strings.Join(packagingCopyStyles, ", "))
	}
	if brief.ImageryMode == "" {
		brief.ImageryMode = packagingImageryHybrid
	}
	if !oneOf(brief.ImageryMode, packagingImageryModes...) {
		return packagingBriefInvalid("imageryMode must be one of %s", strings.Join(packagingImageryModes, ", "))
	}
	if brief.LookFeel.ThemeID != "" {
		if _, ok := deckThemeByID(brief.LookFeel.ThemeID); !ok {
			return packagingBriefInvalid("lookFeel.themeId must name a deck theme")
		}
	}
	if _, err := packagingLengthSlides(brief.Length); err != nil {
		return err
	}
	sources, err := validatePackagingSources(brief.Sources)
	if err != nil {
		return err
	}
	brief.Sources = sources
	if brief.Research != nil {
		brief.Research.ArtifactID = strings.TrimSpace(brief.Research.ArtifactID)
		if brief.Research.ArtifactID == "" && !brief.Research.CommissionFirst && brief.Research.Brief == nil {
			brief.Research = nil
		} else if brief.Research.ArtifactID != "" && brief.Research.CommissionFirst {
			return packagingBriefInvalid("research names either an existing artifact or commissionFirst, not both")
		} else if brief.Research.CommissionFirst {
			if brief.Research.Brief == nil {
				brief.Research.Brief = &packagingResearchBrief{
					Scope: "market", Depth: "standard", Format: "report",
					Audience: brief.Audience, Question: "What must this presentation get right about " + brief.Subject + "?",
				}
			}
			if err := validatePackagingResearchBrief(brief.Research.Brief); err != nil {
				return err
			}
		} else {
			brief.Research.Brief = nil
		}
	}
	return nil
}

func validatePackagingStoryBrief(brief *packagingStoryBrief) error {
	if brief == nil {
		return packagingBriefInvalid("story brief is required")
	}
	brief.Subject = packagingBoundedText(brief.Subject, packagingBriefTextMaxRunes)
	brief.Audience = packagingBoundedText(brief.Audience, packagingBriefFieldMaxRunes)
	brief.Thesis = packagingBoundedText(brief.Thesis, packagingBriefTextMaxRunes)
	brief.Length = packagingLower(brief.Length)
	if brief.Subject == "" {
		return packagingBriefInvalid("subject is required")
	}
	if _, err := packagingLengthSlides(brief.Length); err != nil {
		return err
	}
	return nil
}

// decodePackagingBrief parses the request {kind, brief} envelope into the
// validated union. Unknown kinds and briefs of another kind fail closed.
func decodePackagingBrief(kind string, raw json.RawMessage) (packagingBrief, error) {
	kind = packagingLower(kind)
	if len(raw) == 0 || string(raw) == "null" {
		return packagingBrief{}, packagingBriefInvalid("brief is required")
	}
	brief := packagingBrief{Kind: kind}
	switch kind {
	case packagingCommissionKindResearch:
		var research packagingResearchBrief
		if err := json.Unmarshal(raw, &research); err != nil {
			return packagingBrief{}, packagingBriefInvalid("research brief could not be read")
		}
		if err := validatePackagingResearchBrief(&research); err != nil {
			return packagingBrief{}, err
		}
		brief.Research = &research
	case packagingCommissionKindPresentation:
		var presentation packagingPresentationBrief
		if err := json.Unmarshal(raw, &presentation); err != nil {
			return packagingBrief{}, packagingBriefInvalid("presentation brief could not be read")
		}
		if err := validatePackagingPresentationBrief(&presentation); err != nil {
			return packagingBrief{}, err
		}
		brief.Presentation = &presentation
	case packagingCommissionKindStory:
		var story packagingStoryBrief
		if err := json.Unmarshal(raw, &story); err != nil {
			return packagingBrief{}, packagingBriefInvalid("story brief could not be read")
		}
		if err := validatePackagingStoryBrief(&story); err != nil {
			return packagingBrief{}, err
		}
		brief.Story = &story
	default:
		return packagingBrief{}, packagingBriefInvalid("kind must be research, presentation, or story")
	}
	return brief, nil
}

func decodePackagingBriefMetadata(metadata map[string]string) *packagingBrief {
	raw := strings.TrimSpace(metadata[packagingCommissionBriefMetadataKey])
	if raw == "" {
		return nil
	}
	var brief packagingBrief
	if json.Unmarshal([]byte(raw), &brief) != nil || strings.TrimSpace(brief.Kind) == "" {
		return nil
	}
	return &brief
}

// packagingBriefMap is the studio-row projection of a stored brief (a plain
// object so the frontend renders it without knowing the Go union).
func packagingBriefMap(brief *packagingBrief) map[string]any {
	if brief == nil {
		return nil
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func packagingCommissionMetadata(entry meetingMemoryEntry) (kind string, ok bool) {
	kind = strings.TrimSpace(entry.Metadata[packagingCommissionKindMetadataKey])
	return kind, oneOf(kind, packagingCommissionKindResearch, packagingCommissionKindPresentation, packagingCommissionKindStory)
}

// ---- objective + message composition -------------------------------------

func packagingSourceLines(sources []packagingSource) []string {
	lines := make([]string, 0, len(sources))
	for _, source := range sources {
		switch {
		case source.Ref != "":
			lines = append(lines, "Drive file "+strings.TrimPrefix(source.Ref, "file|"))
		case source.ArtifactID != "":
			label := firstNonEmptyString(source.Title, "artifact")
			lines = append(lines, label+" (artifact "+source.ArtifactID+")")
		case source.URL != "":
			lines = append(lines, source.URL)
		}
	}
	return lines
}

func packagingResearchObjective(brief packagingResearchBrief) string {
	depth := map[string]string{
		"brief":    "a brief pass: the decisive facts only, kept short",
		"standard": "a standard pass with graded sources and a benchmark table where peers exist",
		"deep":     "a deep pass: exhaustive sourcing, counter-cases, and thresholded triggers",
	}[brief.Depth]
	format := map[string]string{
		"report":    "a full research report",
		"one-pager": "a one-page brief (keep every section tight enough for one page)",
		"memo":      "a decision memo addressed to the reader",
	}[brief.Format]
	parts := []string{
		brief.Question,
		"Scope: " + brief.Scope + " research. Depth: " + depth + ". Deliver " + format + ".",
	}
	if brief.Audience != "" {
		parts = append(parts, "Audience: "+brief.Audience+".")
	}
	if lines := packagingSourceLines(brief.Sources); len(lines) > 0 {
		parts = append(parts, "Start from these sources and cite them where used: "+strings.Join(lines, "; ")+".")
	}
	return strings.Join(parts, " ")
}

func packagingPresentationObjective(brief packagingPresentationBrief, researchTitle string, outline string) string {
	parts := []string{"Build a presentation: " + brief.Subject + "."}
	if brief.Audience != "" {
		parts = append(parts, "Audience: "+brief.Audience+".")
	}
	if brief.CopyStyle != "" {
		style := map[string]string{
			"crisp":      "crisp — short declarative lines, one claim per slide",
			"narrative":  "narrative — a through-line the audience can retell",
			"data-led":   "data-led — numbers carry the argument, copy frames them",
			"persuasive": "persuasive — build to a clear ask",
		}[brief.CopyStyle]
		parts = append(parts, "Copy style: "+style+".")
	}
	if brief.LookFeel.ThemeID != "" || brief.LookFeel.Notes != "" {
		look := []string{}
		if theme, ok := deckThemeByID(brief.LookFeel.ThemeID); ok {
			look = append(look, "theme "+theme.ID+" (background "+theme.Background+", accent "+theme.Accent+", text "+theme.TextColor+", "+theme.FontFamily+")")
		}
		if brief.LookFeel.Notes != "" {
			look = append(look, brief.LookFeel.Notes)
		}
		parts = append(parts, "Look and feel: "+strings.Join(look, "; ")+".")
	}
	imagery := map[string]string{
		packagingImageryFullBleed: "full-bleed — every image slide may use a full-bleed background with copy over a scrim",
		packagingImageryOnSlide:   "on-slide — imagery sits in plates beside the copy, never full-bleed",
		packagingImageryHybrid:    "hybrid — at most one full-bleed crescendo, plates elsewhere",
	}[brief.ImageryMode]
	parts = append(parts, "Imagery mode: "+imagery+".")
	if count, _ := packagingLengthSlides(brief.Length); count > 0 {
		parts = append(parts, strconv.Itoa(count)+" slides.")
	}
	if brief.Research != nil && brief.Research.ArtifactID != "" {
		label := firstNonEmptyString(researchTitle, "the attached research report")
		parts = append(parts, "Ground the deck in the research report \""+label+"\" (artifact "+brief.Research.ArtifactID+"); prefer its cited facts over new claims.")
	}
	if lines := packagingSourceLines(brief.Sources); len(lines) > 0 {
		parts = append(parts, "Build from these attached sources and cite them where used: "+strings.Join(lines, "; ")+".")
	}
	if outline != "" {
		parts = append(parts, "Settled narrative (Story Studio outline, follow its order and beats): "+outline)
	}
	return strings.Join(parts, " ")
}

func packagingBriefMessageText(brief packagingBrief) string {
	switch brief.Kind {
	case packagingCommissionKindResearch:
		research := brief.Research
		lines := []string{
			"Commission research — " + research.Scope + " · " + research.Depth + " · " + research.Format,
			"Question: " + research.Question,
		}
		if research.Audience != "" {
			lines = append(lines, "Audience: "+research.Audience)
		}
		if sources := packagingSourceLines(research.Sources); len(sources) > 0 {
			lines = append(lines, "Sources: "+strings.Join(sources, "; "))
		}
		return strings.Join(lines, "\n")
	case packagingCommissionKindPresentation:
		presentation := brief.Presentation
		lines := []string{"Commission a presentation — " + presentation.Subject}
		if presentation.Audience != "" {
			lines = append(lines, "Audience: "+presentation.Audience)
		}
		if presentation.CopyStyle != "" {
			lines = append(lines, "Copy style: "+presentation.CopyStyle)
		}
		if presentation.LookFeel.ThemeID != "" || presentation.LookFeel.Notes != "" {
			lines = append(lines, "Look & feel: "+strings.TrimSpace(strings.Join([]string{presentation.LookFeel.ThemeID, presentation.LookFeel.Notes}, " ")))
		}
		lines = append(lines, "Imagery: "+presentation.ImageryMode)
		if presentation.Length != "" {
			lines = append(lines, "Length: "+presentation.Length)
		}
		if presentation.Research != nil && presentation.Research.ArtifactID != "" {
			lines = append(lines, "Research: artifact "+presentation.Research.ArtifactID)
		}
		if presentation.StoryOutlineArtifactID != "" {
			lines = append(lines, "Story outline: artifact "+presentation.StoryOutlineArtifactID)
		}
		if sources := packagingSourceLines(presentation.Sources); len(sources) > 0 {
			lines = append(lines, "Sources: "+strings.Join(sources, "; "))
		}
		return strings.Join(lines, "\n")
	case packagingCommissionKindStory:
		story := brief.Story
		lines := []string{"Workshop a story — " + story.Subject}
		if story.Audience != "" {
			lines = append(lines, "Audience: "+story.Audience)
		}
		if story.Thesis != "" {
			lines = append(lines, "Thesis: "+story.Thesis)
		}
		if story.Length != "" {
			lines = append(lines, "Length: "+story.Length)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func packagingCommissionTitle(brief packagingBrief) string {
	switch brief.Kind {
	case packagingCommissionKindResearch:
		return boundedStudioProjectTitle("Research: " + brief.Research.Question)
	case packagingCommissionKindPresentation:
		return boundedStudioProjectTitle("Presentation: " + brief.Presentation.Subject)
	case packagingCommissionKindStory:
		return boundedStudioProjectTitle("Story: " + brief.Story.Subject)
	}
	return "Packaging Studio"
}

// ---- launch ---------------------------------------------------------------

// packagingCommissionThread resolves the private conversation a commission
// lands in: the caller's named thread (must be a private, unarchived thread
// this user can read) or a fresh private Scout thread titled for the brief.
func (app *kanbanBoardApp) packagingCommissionThread(user *userAccount, threadID string, title string) (scoutChatThreadRecord, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID != "" {
		thread, _, err := app.scoutChatThreadByID(user.Email, threadID)
		if err != nil {
			return scoutChatThreadRecord{}, fmt.Errorf("conversation not found")
		}
		if thread.ArchivedAt != "" || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate || !scoutChatThreadAllowsViewer(thread, user.Email) {
			return scoutChatThreadRecord{}, fmt.Errorf("commissions start in a private conversation you can read")
		}
		return thread, nil
	}
	return app.createScoutChatThread(user.Email, user.Name, title, scoutChatVisibilityPrivate)
}

// packagingCommissionIntakeContextKey carries the chat-intake record id from
// the launcher adapter down to the launch, where it is stamped on the
// commission root. The intake record only learns its commissionId AFTER a
// successful launch (packaging_intake.go), so without this the root could
// never be resolved back to the intake that produced it.
type packagingCommissionIntakeContextKey struct{}

func withPackagingCommissionIntake(ctx context.Context, intakeID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(intakeID) == "" {
		return ctx
	}
	return context.WithValue(ctx, packagingCommissionIntakeContextKey{}, strings.TrimSpace(intakeID))
}

func packagingCommissionIntakeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	intakeID, _ := ctx.Value(packagingCommissionIntakeContextKey{}).(string)
	return strings.TrimSpace(intakeID)
}

// packagingCommissionOperation binds the launch to one durable idempotent
// turn operation the way the chat door does: the client's operationId when
// supplied, else a digest of the requester + brief. The thread is deliberately
// NOT part of the digest so a retried sheet post (which names no thread)
// adopts the commission it already launched instead of opening a second
// conversation.
func packagingCommissionOperation(user *userAccount, operationID string, brief packagingBrief) (conversationTurnOperation, error) {
	body, err := canonicalJSON(map[string]any{
		"version": "packaging-commission/v1", "requester": normalizeAccountEmail(user.Email), "brief": brief,
	})
	if err != nil {
		return conversationTurnOperation{}, err
	}
	digest := sha256Hex(body)
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		operationID = "packaging-commission-" + digest[:24]
	}
	normalized, err := normalizeScoutIdempotencyKey(operationID)
	if err != nil {
		return conversationTurnOperation{}, packagingBriefInvalid("operationId must be a short printable token")
	}
	return conversationTurnOperation{ID: normalized, BodyDigest: digest}, nil
}

// authorizePackagingSources re-authorizes every named source for this
// requester + destination thread: Drive refs through the Wave 5 seam
// (returned as goal contextRefs), artifacts through the canonical reader
// authorizer (titles resolved server-side), URLs syntactically. One refusal
// launches nothing.
func (app *kanbanBoardApp) authorizePackagingSources(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, sources []packagingSource) ([]workRequestContextRef, error) {
	drive := make([]workRequestContextRef, 0)
	for index := range sources {
		source := &sources[index]
		switch {
		case source.Ref != "":
			drive = append(drive, workRequestContextRef{Ref: source.Ref, SourceID: source.SourceID, SourceRevision: source.SourceRevision})
		case source.ArtifactID != "":
			artifact, ok := authorizedArtifactByID(ctx, user, ACLReadContent, source.ArtifactID)
			if !ok {
				return nil, errWorkRequestContextRefForbidden
			}
			source.Title = boundedStudioProjectTitle(firstNonEmptyString(artifact.Metadata["title"], artifact.Metadata["threadQuery"]))
		}
	}
	if len(drive) == 0 {
		return nil, nil
	}
	if _, err := app.authorizeWorkRequestContextRefs(ctx, user, thread, drive); err != nil {
		return nil, err
	}
	return drive, nil
}

type packagingLaunchResult struct {
	Thread    scoutChatThreadRecord
	Message   scoutChatMessageRecord
	Launched  scoutAgentThread
	Commit    map[string]any
	Operation conversationTurnOperation
}

// launchPackagingCommission commits the brief as the requester's own message
// in the private thread and launches through startConversationPrivateWork,
// the same seam a typed chat turn uses — no new launcher, no client-chosen
// tool or authority. Story briefs never reach here (they mint an outline).
func (app *kanbanBoardApp) launchPackagingCommission(ctx context.Context, user *userAccount, brief packagingBrief, threadID string, operationID string) (packagingLaunchResult, error) {
	if app == nil || app.memory == nil || user == nil {
		return packagingLaunchResult{}, fmt.Errorf("packaging studio is unavailable")
	}
	if strings.TrimSpace(app.currentOpenAIAPIKey()) == "" {
		return packagingLaunchResult{}, errAgentWorkerNotConfigured
	}
	operation, err := packagingCommissionOperation(user, operationID, brief)
	if err != nil {
		return packagingLaunchResult{}, err
	}
	// One launch per requester + operation. The replay check below reads a
	// stamp that is only written at the END of this function, so without a
	// lock spanning check → thread → message → launch → stamp two overlapping
	// posts of the same operation both miss it and mint two commissions in two
	// threads. This is the same per-operation lock the story path takes.
	unlock := app.packagingCommissionOperationLock(user, operation)
	defer unlock()
	// Replay across threads: the same requester + operation already owns a
	// commission root → adopt it (no new thread, no second message).
	existing, ok, err := app.packagingCommissionForOperation(user, operation, brief.Kind)
	if err != nil {
		return packagingLaunchResult{}, err
	}
	if ok {
		thread, _, readErr := app.scoutChatThreadByID(user.Email, existing.Artifact.Metadata[packagingCommissionThreadIDMetadataKey])
		if readErr != nil {
			return packagingLaunchResult{}, readErr
		}
		message := scoutChatMessageRecord{ID: strings.TrimSpace(existing.Artifact.Metadata[packagingCommissionMessageIDMetadataKey])}
		if index := scoutChatMessageIndex(thread, message.ID); index >= 0 {
			message = thread.Messages[index]
		}
		return packagingLaunchResult{Thread: thread, Message: message, Launched: existing, Operation: operation}, nil
	}
	thread, err := app.packagingCommissionThread(user, threadID, packagingCommissionTitle(brief))
	if err != nil {
		return packagingLaunchResult{}, err
	}

	var work conversationWorkDecision
	var driveRefs []workRequestContextRef
	switch brief.Kind {
	case packagingCommissionKindResearch:
		driveRefs, err = app.authorizePackagingSources(ctx, user, thread, brief.Research.Sources)
		if err != nil {
			return packagingLaunchResult{}, err
		}
		work = conversationWorkDecision{Kind: conversationWorkWorkstream, Mode: "research", Objective: packagingResearchObjective(*brief.Research)}
	case packagingCommissionKindPresentation:
		presentation := brief.Presentation
		// The deck's own named sources: authorized (and their titles resolved)
		// exactly like the research brief's, so Drive files ride the goal as
		// contextRefs instead of being dropped on the floor.
		driveRefs, err = app.authorizePackagingSources(ctx, user, thread, presentation.Sources)
		if err != nil {
			return packagingLaunchResult{}, err
		}
		researchTitle := ""
		if presentation.Research != nil && presentation.Research.ArtifactID != "" {
			sources := []packagingSource{{ArtifactID: presentation.Research.ArtifactID}}
			if _, err := app.authorizePackagingSources(ctx, user, thread, sources); err != nil {
				return packagingLaunchResult{}, err
			}
			researchTitle = sources[0].Title
		}
		outline := ""
		if presentation.StoryOutlineArtifactID != "" {
			story, ok := authorizedArtifactByID(ctx, user, ACLReadContent, presentation.StoryOutlineArtifactID)
			if !ok || !packagingStoryOutlineArtifact(story) {
				return packagingLaunchResult{}, errWorkRequestContextRefForbidden
			}
			outline = packagingBoundedText(documentStudioDocumentFromEntry(story).Markdown, 6000)
			presentation.StoryVersion = artifactVersion(story)
			brief.Presentation = presentation
		}
		work = conversationWorkDecision{Kind: conversationWorkRegistryTool, ToolID: packagingStudioProcessID, Objective: packagingPresentationObjective(*presentation, researchTitle, outline)}
	default:
		return packagingLaunchResult{}, packagingBriefInvalid("kind %q does not launch work", brief.Kind)
	}

	now := time.Now().UTC()
	messageID := "scout-chat-message-" + sha256Hex([]byte("conversation-turn/v1\x00" + normalizeAccountEmail(user.Email) + "\x00" + thread.ID + "\x00" + operation.ID))[:24]
	userMessage := scoutChatMessageRecord{
		ID: messageID, Kind: "message", Role: "user", Text: packagingBriefMessageText(brief),
		CreatedAt: now.Format(time.RFC3339Nano), AuthorName: scoutChatAuthorName(user), AuthorEmail: normalizeAccountEmail(user.Email),
		SourceOperationID: operation.ID, SourceOperationDigest: operation.BodyDigest,
	}
	launchCtx := withConversationTurnOperation(ctx, operation)
	if len(driveRefs) > 0 {
		launchCtx = withWorkRequestContextRefs(launchCtx, driveRefs)
	}
	commit := func(messages ...scoutChatMessageRecord) (scoutChatThreadRecord, error) {
		return app.commitScoutChatThreadMessagesWithContext(launchCtx, user.Email, thread.ID, messages...)
	}
	// Replay: the same operation already committed its message → the launch
	// seam adopts the existing root (launchConversationStudioProcessOnce) or
	// this handler returns the recorded commission instead of a second turn.
	if current, _, readErr := app.scoutChatThreadByID(user.Email, thread.ID); readErr == nil {
		if index := scoutChatMessageIndex(current, messageID); index >= 0 {
			if existing, ok := app.packagingCommissionForMessage(current, messageID); ok {
				return packagingLaunchResult{Thread: current, Message: current.Messages[index], Launched: existing, Operation: operation}, nil
			}
		} else if _, commitErr := commit(userMessage); commitErr != nil {
			return packagingLaunchResult{}, commitErr
		}
	} else {
		return packagingLaunchResult{}, readErr
	}
	response, err := app.startConversationPrivateWork(launchCtx, user, thread, userMessage, work, "", packagingCommissionLaunchSrc, commit)
	if err != nil {
		return packagingLaunchResult{}, err
	}
	launched, ok := response["agentThread"].(scoutAgentThread)
	if !ok || strings.TrimSpace(launched.Artifact.ID) == "" {
		return packagingLaunchResult{}, fmt.Errorf("commission did not produce a work root")
	}
	stamp := map[string]string{
		packagingCommissionKindMetadataKey:      brief.Kind,
		packagingCommissionAtMetadataKey:        now.Format(time.RFC3339Nano),
		packagingCommissionByMetadataKey:        normalizeAccountEmail(user.Email),
		packagingCommissionMessageIDMetadataKey: messageID,
		packagingCommissionThreadIDMetadataKey:  thread.ID,
		packagingCommissionOperationMetadataKey: operation.ID,

		packagingCommissionOperationDigestMetadataKey: operation.BodyDigest,
	}
	if intakeID := packagingCommissionIntakeFromContext(ctx); intakeID != "" {
		stamp[packagingCommissionIntakeIDMetadataKey] = intakeID
	}
	if raw, marshalErr := json.Marshal(brief); marshalErr == nil {
		stamp[packagingCommissionBriefMetadataKey] = string(raw)
	}
	if brief.Kind == packagingCommissionKindPresentation {
		stamp[packagingImageryModeMetadataKey] = brief.Presentation.ImageryMode
	}
	if brief.Kind == packagingCommissionKindResearch && brief.Presentation != nil {
		// A commissionFirst chain: the research root carries the waiting deck.
		if raw, marshalErr := json.Marshal(brief.Presentation); marshalErr == nil {
			stamp[packagingChainBriefMetadataKey] = string(raw)
			stamp[packagingChainStateMetadataKey] = packagingChainStateWaiting
		}
	}
	if updated, matched, updateErr := app.memory.updateOSArtifactMetadata(launched.Artifact.ID, stamp); updateErr == nil && matched {
		launched.Artifact = updated
	}
	saved, _ := response["thread"].(scoutChatThreadRecord)
	if saved.ID == "" {
		saved = thread
	}
	return packagingLaunchResult{Thread: saved, Message: userMessage, Launched: launched, Commit: response, Operation: operation}, nil
}

// packagingCommissionOperationLock serializes one requester's launches of one
// operation id: replay check → thread → message → launch → stamp all happen
// under it, because the stamp the replay check reads is only written at the
// end. EVERY door of a commission takes THIS lock — the story path
// (createPackagingStoryWithContext) included — so two kinds of the same
// requester+operationId serialize on one mutex instead of both passing the
// packagingCommissionForOperation reuse check before either writes its digest
// stamp and minting two roots that claim the same operation id.
func (app *kanbanBoardApp) packagingCommissionOperationLock(user *userAccount, operation conversationTurnOperation) func() {
	lock := app.scoutChatThreadLock("packaging-commission-operation-" + sha256Hex([]byte(normalizeAccountEmail(user.Email) + "\x00" + operation.ID))[:24])
	lock.Lock()
	return lock.Unlock
}

// packagingCommissionForOperation finds the commission root this requester
// already launched for an operation id, whichever thread it landed in. The
// operation id is client-supplied, so identity — not the name — decides: a
// stored body digest must match, and (for roots stamped before digests) the
// commission kind must match. A reused id with different content is a
// conflict, never a silent adoption of the wrong root.
func (app *kanbanBoardApp) packagingCommissionForOperation(user *userAccount, operation conversationTurnOperation, kind string) (scoutAgentThread, bool, error) {
	if app == nil || app.memory == nil || user == nil || strings.TrimSpace(operation.ID) == "" {
		return scoutAgentThread{}, false, nil
	}
	kind = packagingLower(kind)
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 0) {
		if strings.TrimSpace(entry.Metadata[packagingCommissionOperationMetadataKey]) != operation.ID ||
			normalizeAccountEmail(entry.Metadata[packagingCommissionByMetadataKey]) != normalizeAccountEmail(user.Email) {
			continue
		}
		storedDigest := strings.TrimSpace(entry.Metadata[packagingCommissionOperationDigestMetadataKey])
		storedKind := packagingLower(entry.Metadata[packagingCommissionKindMetadataKey])
		if (storedDigest != "" && storedDigest != operation.BodyDigest) ||
			(storedDigest == "" && kind != "" && storedKind != "" && storedKind != kind) {
			return scoutAgentThread{}, false, fmt.Errorf("%w: operationId was reused for a different commission", ErrSTRIDEConversationConflict)
		}
		return scoutAgentThread{
			ID: firstNonEmptyString(strings.TrimSpace(entry.Metadata["threadId"]), entry.ID), Mode: strings.TrimSpace(entry.Metadata["mode"]),
			Query: strings.TrimSpace(entry.Metadata["threadQuery"]), Status: firstNonEmptyString(agentThreadStatusValue(entry), "running"), Artifact: entry,
		}, true, nil
	}
	return scoutAgentThread{}, false, nil
}

// packagingCommissionForMessage finds the work root a committed commission
// message already launched (replay of the same operation).
func (app *kanbanBoardApp) packagingCommissionForMessage(thread scoutChatThreadRecord, messageID string) (scoutAgentThread, bool) {
	for _, message := range thread.Messages {
		if message.Thread == nil || strings.TrimSpace(message.CausedByMessageID) != messageID || strings.TrimSpace(message.Thread.ArtifactID) == "" {
			continue
		}
		root, ok := app.osArtifactByID(message.Thread.ArtifactID)
		if !ok {
			continue
		}
		if strings.TrimSpace(root.Metadata[packagingCommissionMessageIDMetadataKey]) != messageID {
			continue
		}
		return scoutAgentThread{ID: message.Thread.ID, Mode: message.Thread.Mode, Query: message.Thread.Query, Status: firstNonEmptyString(agentThreadStatusValue(root), message.Thread.Status), Artifact: root}, true
	}
	return scoutAgentThread{}, false
}

// packagingCommissionStatus folds the root's lifecycle onto the studio status
// vocabulary so the commission view and the hub row agree.
func packagingCommissionStatus(root meetingMemoryEntry) string {
	kind, plan, canonical, ok := studioProjectClassification(root)
	if ok && kind != "" {
		if canonical {
			return studioProjectStatus(root, plan, false)
		}
		return studioProjectStatus(root, goalPlan{}, false)
	}
	return studioProjectStatus(root, goalPlan{}, false)
}

func packagingCommissionViewFromRoot(root meetingMemoryEntry) packagingCommissionView {
	return kanbanApp.packagingCommissionViewForViewer(context.Background(), nil, root)
}

// packagingCommissionViewForViewer projects one commission root plus the
// viewer-fenced intake waiting state.
func (app *kanbanBoardApp) packagingCommissionViewForViewer(ctx context.Context, viewer *userAccount, root meetingMemoryEntry) packagingCommissionView {
	kind, _ := packagingCommissionMetadata(root)
	view := packagingCommissionView{
		ID: root.ID, Kind: kind, Status: packagingCommissionStatus(root), Brief: decodePackagingBriefMetadata(root.Metadata),
		ProjectID: root.ID, ThreadID: strings.TrimSpace(root.Metadata[packagingCommissionThreadIDMetadataKey]),
		MessageID:                       strings.TrimSpace(root.Metadata[packagingCommissionMessageIDMetadataKey]),
		CreatedAt:                       strings.TrimSpace(root.Metadata[packagingCommissionAtMetadataKey]),
		packagingCommissionWaitingState: packagingCommissionWaitingState{BriefComplete: true},
	}
	if viewer != nil {
		view.packagingCommissionWaitingState = app.packagingCommissionWaitingStateFor(ctx, viewer, root)
	}
	if state := strings.TrimSpace(root.Metadata[packagingChainStateMetadataKey]); state != "" {
		view.Chain = &packagingChainView{State: state, PresentationID: strings.TrimSpace(root.Metadata[packagingChainPresentationIDMetadataKey])}
	}
	return view
}

// advancePackagingCommissionChain launches the waiting presentation of a
// research → presentation chain once the research root is complete. Only the
// commissioning requester advances it (the deck is their work, launched in
// their thread); every other reader sees the chain state unchanged.
func (app *kanbanBoardApp) advancePackagingCommissionChain(ctx context.Context, user *userAccount, root meetingMemoryEntry) (meetingMemoryEntry, bool) {
	if app == nil || user == nil || strings.TrimSpace(root.Metadata[packagingChainStateMetadataKey]) != packagingChainStateWaiting {
		return root, false
	}
	if normalizeAccountEmail(root.Metadata[packagingCommissionByMetadataKey]) != normalizeAccountEmail(user.Email) {
		return root, false
	}
	// waiting-check → launch → launched-stamp is one critical section: the
	// commission poll and the studio list both advance the same chain, and an
	// unguarded window launches the deck twice. Re-read the root under the
	// lock so the loser sees the winner's stamp.
	chainLock := app.scoutChatThreadLock("packaging-chain-" + root.ID)
	chainLock.Lock()
	defer chainLock.Unlock()
	if current, ok := app.osArtifactByID(root.ID); ok {
		root = current
		if strings.TrimSpace(root.Metadata[packagingChainStateMetadataKey]) != packagingChainStateWaiting {
			return root, false
		}
	}
	if !oneOf(agentThreadStatusValue(root), artifactStatusComplete, artifactStatusPublished) {
		return root, false
	}
	var presentation packagingPresentationBrief
	if json.Unmarshal([]byte(root.Metadata[packagingChainBriefMetadataKey]), &presentation) != nil {
		return root, false
	}
	presentation.Research = &packagingResearchInput{ArtifactID: root.ID}
	if err := validatePackagingPresentationBrief(&presentation); err != nil {
		return root, false
	}
	brief := packagingBrief{Kind: packagingCommissionKindPresentation, Presentation: &presentation}
	result, err := app.launchPackagingCommission(ctx, user, brief, root.Metadata[packagingCommissionThreadIDMetadataKey], "packaging-chain-"+sha256Hex([]byte(root.ID))[:24])
	if err != nil {
		log.Warnf("Packaging chain for %s could not launch its presentation: %v", root.ID, err)
		return root, false
	}
	updated, matched, updateErr := app.memory.updateOSArtifactMetadata(root.ID, map[string]string{
		packagingChainStateMetadataKey:          packagingChainStateLaunched,
		packagingChainPresentationIDMetadataKey: result.Launched.Artifact.ID,
	})
	if updateErr != nil || !matched {
		return root, true
	}
	return updated, true
}

// advancePackagingCommissionChainsForViewer walks the viewer's own waiting
// chains (research roots they commissioned) and launches any deck whose
// research result is complete. Bounded: only roots stamped waiting are read.
func (app *kanbanBoardApp) advancePackagingCommissionChainsForViewer(ctx context.Context, user *userAccount) {
	if app == nil || app.memory == nil || user == nil {
		return
	}
	for _, candidate := range app.memory.studioProjectProjectionSnapshot() {
		metadata := candidate.Entry.Metadata
		if strings.TrimSpace(metadata[packagingChainStateMetadataKey]) != packagingChainStateWaiting ||
			normalizeAccountEmail(metadata[packagingCommissionByMetadataKey]) != normalizeAccountEmail(user.Email) {
			continue
		}
		if !artifactHeaderAuthorized(ctx, user, ACLReadContent, candidate.Header) {
			continue
		}
		root, ok := app.osArtifactByID(candidate.Entry.ID)
		if !ok {
			continue
		}
		app.advancePackagingCommissionChain(ctx, user, root)
	}
}

// ---- story studio ---------------------------------------------------------

func packagingStoryOutlineArtifact(entry meetingMemoryEntry) bool {
	return strings.TrimSpace(entry.ID) != "" && artifactType(entry) == artifactTypeMarkdown &&
		strings.TrimSpace(entry.Metadata["source"]) == packagingStorySource &&
		strings.TrimSpace(entry.Metadata[packagingCommissionKindMetadataKey]) == packagingCommissionKindStory
}

type packagingStoryView struct {
	ID        string                          `json:"id"`
	Title     string                          `json:"title"`
	Version   int                             `json:"version"`
	Outline   string                          `json:"outline"`
	DraftedBy string                          `json:"draftedBy,omitempty"`
	Doc       *packagingStoryDoc              `json:"doc,omitempty"`
	ThreadID  string                          `json:"threadId,omitempty"`
	Brief     *packagingStoryBrief            `json:"brief,omitempty"`
	Versions  []studioProjectResultVersionRef `json:"versions,omitempty"`
	Project   string                          `json:"project,omitempty"`
	Updated   string                          `json:"updatedAt,omitempty"`
	Created   string                          `json:"createdAt,omitempty"`
}

func packagingStoryViewFromEntry(entry meetingMemoryEntry) packagingStoryView {
	view := packagingStoryView{
		ID: entry.ID, Title: strings.TrimSpace(entry.Metadata["title"]), Version: artifactVersion(entry),
		Outline: documentStudioDocumentFromEntry(entry).Markdown, ThreadID: strings.TrimSpace(entry.Metadata[packagingStoryThreadIDMetadataKey]),
		Project: strings.TrimSpace(entry.Metadata[artifactProjectMetadataKey]),
		Updated: strings.TrimSpace(entry.Metadata["updatedAt"]), Created: entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		DraftedBy: strings.TrimSpace(entry.Metadata[packagingStoryDraftedByMetadataKey]),
	}
	if doc := packagingStoryDocForEntry(entry); len(doc.Beats) > 0 {
		view.Doc = &doc
	}
	if raw := strings.TrimSpace(entry.Metadata[packagingStoryBriefMetadataKey]); raw != "" {
		var brief packagingStoryBrief
		if json.Unmarshal([]byte(raw), &brief) == nil {
			view.Brief = &brief
		}
	}
	view.Versions = studioProjectResultVersions(entry, view.Version)
	return view
}

// createPackagingStory mints the outline artifact and its bound private
// thread. The brief is the thread's opening message so the workshop starts
// with the ask on the record; the outline itself lives on the artifact.
func (app *kanbanBoardApp) createPackagingStory(user *userAccount, brief packagingStoryBrief) (meetingMemoryEntry, scoutChatThreadRecord, error) {
	return app.createPackagingStoryWithContext(context.Background(), user, brief, "")
}

// createPackagingStoryWithContext mints the story. operationID (client
// operationId, or the intake's thread+message identity) dedupes a double
// trigger: the requester's existing story for that operation is adopted.
func (app *kanbanBoardApp) createPackagingStoryWithContext(ctx context.Context, user *userAccount, brief packagingStoryBrief, operationID string) (meetingMemoryEntry, scoutChatThreadRecord, error) {
	if app == nil || app.memory == nil || user == nil {
		return meetingMemoryEntry{}, scoutChatThreadRecord{}, fmt.Errorf("story studio is unavailable")
	}
	full := packagingBrief{Kind: packagingCommissionKindStory, Story: &brief}
	operation, err := packagingCommissionOperation(user, operationID, full)
	if err != nil {
		return meetingMemoryEntry{}, scoutChatThreadRecord{}, err
	}
	// The same mutex the non-story doors take: a story and a presentation that
	// reuse one operationId must conflict, not race past the reuse check.
	defer app.packagingCommissionOperationLock(user, operation)()
	existing, adopted, operationErr := app.packagingCommissionForOperation(user, operation, packagingCommissionKindStory)
	if operationErr != nil {
		return meetingMemoryEntry{}, scoutChatThreadRecord{}, operationErr
	}
	if adopted && packagingStoryOutlineArtifact(existing.Artifact) {
		thread, _, readErr := app.scoutChatThreadByID(user.Email, existing.Artifact.Metadata[packagingStoryThreadIDMetadataKey])
		if readErr != nil {
			return meetingMemoryEntry{}, scoutChatThreadRecord{}, readErr
		}
		return existing.Artifact, thread, nil
	}
	thread, err := app.createScoutChatThread(user.Email, user.Name, packagingCommissionTitle(full), scoutChatVisibilityPrivate)
	if err != nil {
		return meetingMemoryEntry{}, scoutChatThreadRecord{}, err
	}
	now := time.Now().UTC()
	opening := scoutChatMessageRecord{
		ID: fmt.Sprintf("scout-chat-message-%d", now.UnixNano()), Kind: "message", Role: "user", Text: packagingBriefMessageText(full),
		CreatedAt: now.Format(time.RFC3339Nano), AuthorName: scoutChatAuthorName(user), AuthorEmail: normalizeAccountEmail(user.Email),
	}
	if saved, commitErr := app.commitScoutChatThreadMessages(user.Email, thread.ID, opening); commitErr == nil {
		thread = saved
	}
	rawBrief, _ := json.Marshal(brief)
	rawFull, _ := json.Marshal(full)
	doc, draftedBy := app.draftPackagingStory(ctx, brief)
	rawDoc, _ := json.Marshal(doc)
	storyMetadata := map[string]string{
		"source": packagingStorySource, "mode": packagingStoryMode,
		// The outline is the private thread's own material: it carries the
		// thread's security fence, exactly as a commission root does, so
		// appendOSArtifact resolves it private-to-the-owner instead of leaving
		// the Document Studio default (organization-visible) in place, which
		// would let any member read and rewrite it via /assistant/packaging/stories.
		"originSurface":                         "chat:" + thread.ID,
		"visibility":                            scoutChatVisibilityPrivate,
		"ownerEmail":                            normalizeAccountEmail(user.Email),
		packagingStoryDocMetadataKey:            string(rawDoc),
		packagingStoryDraftedByMetadataKey:      draftedBy,
		packagingCommissionKindMetadataKey:      packagingCommissionKindStory,
		packagingCommissionBriefMetadataKey:     string(rawFull),
		packagingStoryBriefMetadataKey:          string(rawBrief),
		packagingStoryThreadIDMetadataKey:       thread.ID,
		packagingCommissionAtMetadataKey:        now.Format(time.RFC3339Nano),
		packagingCommissionByMetadataKey:        normalizeAccountEmail(user.Email),
		packagingCommissionOperationMetadataKey: operation.ID,

		packagingCommissionOperationDigestMetadataKey: operation.BodyDigest,
	}
	if intakeID := packagingCommissionIntakeFromContext(ctx); intakeID != "" {
		storyMetadata[packagingCommissionIntakeIDMetadataKey] = intakeID
	}
	entry, err := createDocumentStudioArtifact(user, boundedStudioProjectTitle(brief.Subject), renderPackagingStoryOutline(doc), storyMetadata)
	if err != nil {
		return meetingMemoryEntry{}, scoutChatThreadRecord{}, err
	}
	return entry, thread, nil
}

// updatePackagingStoryOutline saves a new outline version through the same
// header-fenced update the document editor uses, so the Wave 4 journal keeps
// every prior version and the studio history rail can restore any of them.
func (app *kanbanBoardApp) updatePackagingStoryOutline(user *userAccount, prior meetingMemoryEntry, outline string, title string, restoredFrom int) (meetingMemoryEntry, error) {
	if len([]byte(outline)) > packagingStoryOutlineMaxByte {
		return meetingMemoryEntry{}, packagingBriefInvalid("outline exceeds the 1MB editing bound")
	}
	title = firstNonEmptyString(strings.TrimSpace(title), strings.TrimSpace(prior.Metadata["title"]))
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(prior))
	storedBody, emptyMarker := documentStudioStoredBody(outline)
	restored := ""
	if restoredFrom > 0 {
		restored = strconv.Itoa(restoredFrom)
	}
	updated, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, prior.ID, title, storedBody, user.Name,
		map[string]string{"type": artifactTypeMarkdown, "documentSchemaVersion": "1", documentStudioEmptyMetadataKey: emptyMarker, artifactRestoredFromMetadataKey: restored})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	if !changed {
		return prior, nil
	}
	return updated, nil
}

func (app *kanbanBoardApp) listPackagingStories(ctx context.Context, user *userAccount) []packagingStoryView {
	views := make([]packagingStoryView, 0)
	if app == nil || app.memory == nil || user == nil {
		return views
	}
	for _, candidate := range app.memory.artifactListAuthorizationSnapshot() {
		if !packagingStoryOutlineArtifact(candidate.Entry) || !artifactHeaderAuthorized(ctx, user, ACLReadContent, candidate.Header) {
			continue
		}
		entry, ok := app.osArtifactByID(candidate.Entry.ID)
		if !ok {
			continue
		}
		views = append(views, packagingStoryViewFromEntry(entry))
	}
	sort.SliceStable(views, func(left, right int) bool {
		if views[left].Updated == views[right].Updated {
			return views[left].ID > views[right].ID
		}
		return views[left].Updated > views[right].Updated
	})
	return views
}

// ---- HTTP -----------------------------------------------------------------

func packagingCommissionHandlerUser(w http.ResponseWriter, r *http.Request) *userAccount {
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil
	}
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "packaging studio is unavailable")
		return nil
	}
	return user
}

func packagingLaunchErrorStatus(err error) (int, string) {
	var briefErr *packagingBriefError
	switch {
	case errors.As(err, &briefErr):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, errAgentWorkerNotConfigured):
		return http.StatusServiceUnavailable, "Scout is unavailable — the provider key is not configured on this server"
	case errors.Is(err, errWorkRequestContextRefForbidden):
		return http.StatusForbidden, "a named source is not readable by you; attach it again"
	case errors.Is(err, ErrSTRIDEConversationConflict):
		return http.StatusConflict, err.Error()
	}
	var capErr *errGoalUserCapExceeded
	if errors.As(err, &capErr) {
		return http.StatusTooManyRequests, err.Error()
	}
	return http.StatusBadRequest, err.Error()
}

func (app *kanbanBoardApp) packagingCommissionProjectView(ctx context.Context, user *userAccount, rootID string) (studioProjectView, bool) {
	candidates, index := studioProjectProjectionDirectory()
	for _, candidate := range authorizedStudioProjectCandidates(ctx, user, candidates) {
		if candidate.Entry.ID != rootID {
			continue
		}
		return studioProjectViewForCandidate(ctx, user, candidate, index)
	}
	return studioProjectView{}, false
}

// packagingCommissionsHandler serves POST /assistant/packaging/commissions
// {kind, brief, threadId?, operationId?} → 201 {commission, project?}.
func packagingCommissionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := packagingCommissionHandlerUser(w, r)
	if user == nil {
		return
	}
	payload := struct {
		Kind        string          `json:"kind"`
		Brief       json.RawMessage `json:"brief"`
		ThreadID    string          `json:"threadId"`
		OperationID string          `json:"operationId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the commission")
		return
	}
	brief, err := decodePackagingBrief(payload.Kind, payload.Brief)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	if brief.Kind == packagingCommissionKindStory {
		entry, thread, createErr := kanbanApp.createPackagingStoryWithContext(r.Context(), user, *brief.Story, payload.OperationID)
		if createErr != nil {
			if errors.Is(createErr, ErrSTRIDEConversationConflict) {
				writeAuthError(w, http.StatusConflict, createErr.Error())
				return
			}
			log.Errorf("Story create failed: %v", createErr)
			writeAuthError(w, http.StatusInternalServerError, "the story could not be created")
			return
		}
		writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "story": packagingStoryViewFromEntry(entry), "threadId": thread.ID})
		return
	}
	// commissionFirst: the research runs first and carries the deck brief.
	if brief.Kind == packagingCommissionKindPresentation && brief.Presentation.Research != nil && brief.Presentation.Research.CommissionFirst {
		research := *brief.Presentation.Research.Brief
		presentation := *brief.Presentation
		presentation.Research = nil
		brief = packagingBrief{Kind: packagingCommissionKindResearch, Research: &research, Presentation: &presentation}
	}
	result, err := kanbanApp.launchPackagingCommission(r.Context(), user, brief, payload.ThreadID, payload.OperationID)
	if err != nil {
		var pending *conversationWorkProjectionPendingError
		if errors.As(err, &pending) {
			writeAuthError(w, http.StatusConflict, err.Error())
			return
		}
		status, message := packagingLaunchErrorStatus(err)
		writeAuthError(w, status, message)
		return
	}
	response := map[string]any{"ok": true, "commission": kanbanApp.packagingCommissionViewForViewer(r.Context(), user, result.Launched.Artifact), "threadId": result.Thread.ID, "messageId": result.Message.ID}
	if project, ok := kanbanApp.packagingCommissionProjectView(r.Context(), user, result.Launched.Artifact.ID); ok {
		response["project"] = project
	}
	writeAuthJSON(w, http.StatusCreated, response)
}

// packagingCommissionHandler serves GET /assistant/packaging/commissions/{id}.
func packagingCommissionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := packagingCommissionHandlerUser(w, r)
	if user == nil {
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/assistant/packaging/commissions/"))
	root, ok := authorizedArtifactByID(r.Context(), user, ACLReadContent, id)
	if _, isCommission := packagingCommissionMetadata(root); !ok || !isCommission {
		writeAuthError(w, http.StatusNotFound, "commission not found")
		return
	}
	root, _ = kanbanApp.advancePackagingCommissionChain(r.Context(), user, root)
	response := map[string]any{"ok": true, "commission": kanbanApp.packagingCommissionViewForViewer(r.Context(), user, root)}
	if project, found := kanbanApp.packagingCommissionProjectView(r.Context(), user, root.ID); found {
		response["project"] = project
	}
	writeAuthJSON(w, http.StatusOK, response)
}

// packagingStoriesHandler serves GET/POST /assistant/packaging/stories.
func packagingStoriesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := packagingCommissionHandlerUser(w, r)
	if user == nil {
		return
	}
	if r.Method == http.MethodGet {
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "stories": kanbanApp.listPackagingStories(r.Context(), user)})
		return
	}
	payload := struct {
		Brief       json.RawMessage `json:"brief"`
		OperationID string          `json:"operationId"`
	}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read the story brief")
		return
	}
	brief, err := decodePackagingBrief(packagingCommissionKindStory, payload.Brief)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, thread, err := kanbanApp.createPackagingStoryWithContext(r.Context(), user, *brief.Story, payload.OperationID)
	if err != nil {
		if errors.Is(err, ErrSTRIDEConversationConflict) {
			writeAuthError(w, http.StatusConflict, err.Error())
			return
		}
		log.Errorf("Story create failed: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "the story could not be created")
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "story": packagingStoryViewFromEntry(entry), "threadId": thread.ID})
}

// packagingStoryHandler serves /assistant/packaging/stories/{id} (GET, PATCH
// {outline?, message?, title?, expectedVersion, restoredFrom?} — an absent
// outline leaves the body unchanged, so a rename is title-only) and POST
// /assistant/packaging/stories/{id}/deck {brief?} which hands the outline to
// a presentation commission as the settled narrative.
func packagingStoryHandler(w http.ResponseWriter, r *http.Request) {
	user := packagingCommissionHandlerUser(w, r)
	if user == nil {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/assistant/packaging/stories/")
	id, action, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	id = strings.TrimSpace(id)
	switch {
	case action == "" && r.Method == http.MethodGet:
		story, ok := authorizedArtifactByID(r.Context(), user, ACLReadContent, id)
		if !ok || !packagingStoryOutlineArtifact(story) {
			writeAuthError(w, http.StatusNotFound, "story not found")
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "story": packagingStoryViewFromEntry(story)})
	case action == "" && r.Method == http.MethodPatch:
		// Outline is a pointer: an absent field leaves the body untouched (a
		// rename sends {title, expectedVersion} only), and only an explicit
		// "outline":"" clears it. A plain string would silently journal an
		// empty body — thesis, audience and every beat gone — for a rename.
		payload := struct {
			Outline         *string `json:"outline"`
			Message         string  `json:"message"`
			Title           string  `json:"title"`
			ExpectedVersion int     `json:"expectedVersion"`
			RestoredFrom    int     `json:"restoredFrom"`
		}{}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, documentStudioMaxBytes+64<<10)).Decode(&payload); err != nil {
			writeAuthError(w, http.StatusBadRequest, "could not read the outline update")
			return
		}
		if len([]rune(strings.TrimSpace(payload.Title))) > studioProjectTitleMaxRunes {
			writeAuthError(w, http.StatusBadRequest, "the title is too long")
			return
		}
		prior, ok := authorizedArtifactForActions(r.Context(), user, id, ACLReadContent, ACLWrite)
		if !ok || !packagingStoryOutlineArtifact(prior) {
			writeAuthError(w, http.StatusNotFound, "story not found")
			return
		}
		if payload.ExpectedVersion < 1 || artifactVersion(prior) != payload.ExpectedVersion {
			writeAuthJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "the outline changed; reload it before saving", "currentVersion": artifactVersion(prior)})
			return
		}
		if _, restoredOK := studioRestoredFromMetadata(payload.RestoredFrom, payload.ExpectedVersion); !restoredOK {
			writeAuthError(w, http.StatusBadRequest, "restoredFrom must name an existing prior version")
			return
		}
		// A workshop turn: the message goes on the bound thread as the user's
		// own turn, Scout revises the outline against it (keeping every
		// unchallenged beat), journals the version, and replies with the diff.
		if message := strings.TrimSpace(payload.Message); message != "" {
			if payload.Outline != nil && strings.TrimSpace(*payload.Outline) != "" {
				writeAuthError(w, http.StatusBadRequest, "send either an outline edit or a workshop message, not both")
				return
			}
			// A workshop turn spends a provider call and commits into the
			// bound private thread as a conversation turn, so it takes the
			// same fence the chat door takes (packagingStoryThreadTurn): the
			// caller must own that thread. Artifact write authority alone is
			// not authority to speak in someone else's conversation.
			threadID := strings.TrimSpace(prior.Metadata[packagingStoryThreadIDMetadataKey])
			boundThread, _, threadErr := kanbanApp.scoutChatThreadByID(user.Email, threadID)
			if threadID == "" || threadErr != nil || normalizeAccountEmail(boundThread.OwnerEmail) != normalizeAccountEmail(user.Email) ||
				!scoutChatThreadAllowsViewer(boundThread, user.Email) {
				writeAuthError(w, http.StatusForbidden, "only the owner of the story's conversation can workshop it")
				return
			}
			now := time.Now().UTC()
			userMessage := scoutChatMessageRecord{
				ID: fmt.Sprintf("scout-chat-message-%d", now.UnixNano()), Kind: "message", Role: "user", Text: packagingBoundedText(message, packagingBriefTextMaxRunes*4),
				CreatedAt: now.Format(time.RFC3339Nano), AuthorName: scoutChatAuthorName(user), AuthorEmail: normalizeAccountEmail(user.Email),
			}
			updated, reply, ok := kanbanApp.packagingStoryWorkshopTurn(r.Context(), user, prior, userMessage)
			if !ok {
				writeAuthError(w, http.StatusConflict, "the story could not be workshopped")
				return
			}
			// The turn IS the conversation: a commit failure is reported, never
			// swallowed, so the caller never sees a revision that left no trace
			// in the workshop thread.
			saved, commitErr := kanbanApp.commitScoutChatThreadMessages(user.Email, threadID, userMessage, reply)
			if commitErr != nil {
				log.Errorf("Story workshop commit failed: %v", commitErr)
				writeAuthError(w, http.StatusConflict, "the outline was revised but the workshop reply could not be committed; reload the story")
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{
				"ok": true, "story": packagingStoryViewFromEntry(updated), "answer": reply, "message": userMessage, "thread": saved,
			})
			return
		}
		outline := documentStudioDocumentFromEntry(prior).Markdown
		if payload.Outline != nil {
			outline = *payload.Outline
		}
		updated, err := kanbanApp.updatePackagingStoryOutline(user, prior, outline, payload.Title, payload.RestoredFrom)
		if err != nil {
			status, message := packagingLaunchErrorStatus(err)
			if status == http.StatusBadRequest && !errors.As(err, new(*packagingBriefError)) {
				status, message = http.StatusConflict, "the outline changed; reload it before saving"
			}
			writeAuthError(w, status, message)
			return
		}
		writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "story": packagingStoryViewFromEntry(updated)})
	case action == "deck" && r.Method == http.MethodPost:
		story, ok := authorizedArtifactByID(r.Context(), user, ACLReadContent, id)
		if !ok || !packagingStoryOutlineArtifact(story) {
			writeAuthError(w, http.StatusNotFound, "story not found")
			return
		}
		payload := struct {
			Brief       json.RawMessage `json:"brief"`
			OperationID string          `json:"operationId"`
		}{}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
				writeAuthError(w, http.StatusBadRequest, "could not read the deck brief")
				return
			}
		}
		presentation := packagingPresentationBrief{}
		if len(payload.Brief) > 0 && string(payload.Brief) != "null" {
			if err := json.Unmarshal(payload.Brief, &presentation); err != nil {
				writeAuthError(w, http.StatusBadRequest, "could not read the deck brief")
				return
			}
		}
		storyView := packagingStoryViewFromEntry(story)
		if storyView.Brief != nil {
			presentation.Subject = firstNonEmptyString(strings.TrimSpace(presentation.Subject), storyView.Brief.Subject)
			presentation.Audience = firstNonEmptyString(strings.TrimSpace(presentation.Audience), storyView.Brief.Audience)
			presentation.Length = firstNonEmptyString(strings.TrimSpace(presentation.Length), storyView.Brief.Length)
		}
		presentation.Subject = firstNonEmptyString(strings.TrimSpace(presentation.Subject), storyView.Title)
		presentation.StoryOutlineArtifactID = story.ID
		if err := validatePackagingPresentationBrief(&presentation); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		if presentation.Research != nil && presentation.Research.CommissionFirst {
			writeAuthError(w, http.StatusBadRequest, "build the deck from an existing research report; commission research from the Presentation Studio instead")
			return
		}
		brief := packagingBrief{Kind: packagingCommissionKindPresentation, Presentation: &presentation}
		result, err := kanbanApp.launchPackagingCommission(r.Context(), user, brief, story.Metadata[packagingStoryThreadIDMetadataKey], payload.OperationID)
		if err != nil {
			status, message := packagingLaunchErrorStatus(err)
			writeAuthError(w, status, message)
			return
		}
		response := map[string]any{"ok": true, "commission": kanbanApp.packagingCommissionViewForViewer(r.Context(), user, result.Launched.Artifact), "threadId": result.Thread.ID, "messageId": result.Message.ID}
		if project, found := kanbanApp.packagingCommissionProjectView(r.Context(), user, result.Launched.Artifact.ID); found {
			response["project"] = project
		}
		writeAuthJSON(w, http.StatusCreated, response)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
