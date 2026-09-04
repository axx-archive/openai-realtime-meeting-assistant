package main

// packaging_story_workshop.go — Wave 11 Story Studio: Scout workshops the
// narrative. The first outline is drafted by the model on the chat seat
// (behind the provider breaker) in the deck engine's narrative contract —
// thesis, audience, 8–12 beats with a headline, a one-line intent and the
// evidence each beat needs, plus open questions. Keyless or breaker-open
// servers fall back to the deterministic scaffold, stamped draftedBy:
// "scaffold" so the UI can say so. Every workshop turn (a message in the
// bound private thread, or PATCH {message}) asks the model to revise the
// outline against the message; the server keeps every beat the message did
// not challenge byte-for-byte, journals a new version, and Scout replies
// with a short diff summary — never a routine question.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	packagingStoryDraftedByMetadataKey = "storyDraftedBy"
	packagingStoryDocMetadataKey       = "storyOutlineDoc"
	// packagingStoryTurn*MetadataKey record which user message the outline's
	// current revision answered, and with what summary. The reply itself may
	// fail to commit (a store error, a lock loss) and the append path then
	// calls the story hook a SECOND time for the same message; without this
	// stamp that retry spends another provider call and journals a second
	// version for one user message.
	packagingStoryTurnMessageIDMetadataKey = "storyTurnMessageId"
	packagingStoryTurnSummaryMetadataKey   = "storyTurnSummary"
	packagingStoryDraftedByScaffold        = "scaffold"
	packagingStoryWorkflowDraft            = "story_outline_draft"
	packagingStoryWorkflowRevise           = "story_outline_revise"
	packagingStoryDraftTokens              = 2400
	packagingStoryMinBeats                 = 8
	packagingStoryMaxBeats                 = 12
	packagingStoryOutlineContract          = "story_outline_v1"
)

// packagingStoryBeat is one narrative beat of the outline.
type packagingStoryBeat struct {
	ID            string   `json:"id"`
	Headline      string   `json:"headline"`
	Intent        string   `json:"intent"`
	EvidenceNeeds []string `json:"evidence_needs"`
}

// packagingStoryDoc is the structured outline (the deck engine's narrative
// contract) the Markdown body renders from and workshop turns revise.
type packagingStoryDoc struct {
	Title         string               `json:"title"`
	Thesis        string               `json:"thesis"`
	Audience      string               `json:"audience"`
	Beats         []packagingStoryBeat `json:"beats"`
	OpenQuestions []string             `json:"open_questions"`
}

type packagingStoryRevision struct {
	packagingStoryDoc
	ChangedBeatIDs []string `json:"changed_beat_ids"`
	Summary        string   `json:"summary"`
}

var (
	packagingStoryBeatLinePattern     = regexp.MustCompile(`^\s*(\d+)\.\s+\*\*(.+?)\*\*(?:\s+—\s+(.*))?\s*$`)
	packagingStoryEvidenceLinePattern = regexp.MustCompile(`^\s*-\s+Evidence:\s*(.*)$`)
	packagingStoryBeatIDPattern       = regexp.MustCompile(`[^a-z0-9]+`)
)

func packagingStoryBeatID(index int, headline string) string {
	slug := strings.Trim(packagingStoryBeatIDPattern.ReplaceAllString(strings.ToLower(headline), "-"), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	return "beat-" + strconv.Itoa(index+1) + "-" + firstNonEmptyString(slug, "untitled")
}

// normalizePackagingStoryDoc trims, bounds and re-ids beats so the doc is
// always renderable and every beat has a stable id for revisions.
func normalizePackagingStoryDoc(doc packagingStoryDoc) packagingStoryDoc {
	doc.Title = packagingBoundedText(doc.Title, packagingBriefFieldMaxRunes)
	doc.Thesis = packagingBoundedText(doc.Thesis, packagingBriefTextMaxRunes)
	doc.Audience = packagingBoundedText(doc.Audience, packagingBriefFieldMaxRunes)
	beats := make([]packagingStoryBeat, 0, len(doc.Beats))
	seen := map[string]bool{}
	for _, beat := range doc.Beats {
		beat.Headline = packagingBoundedText(beat.Headline, packagingBriefFieldMaxRunes)
		beat.Intent = packagingBoundedText(beat.Intent, packagingBriefFieldMaxRunes)
		if beat.Headline == "" {
			continue
		}
		needs := make([]string, 0, len(beat.EvidenceNeeds))
		for _, need := range beat.EvidenceNeeds {
			if need = packagingBoundedText(need, packagingBriefFieldMaxRunes); need != "" {
				needs = append(needs, need)
			}
		}
		beat.EvidenceNeeds = needs
		beat.ID = strings.TrimSpace(beat.ID)
		if beat.ID == "" || seen[beat.ID] {
			beat.ID = packagingStoryBeatID(len(beats), beat.Headline)
		}
		seen[beat.ID] = true
		beats = append(beats, beat)
		if len(beats) >= packagingStoryMaxBeats*2 {
			break
		}
	}
	doc.Beats = beats
	questions := make([]string, 0, len(doc.OpenQuestions))
	for _, question := range doc.OpenQuestions {
		if question = packagingBoundedText(question, packagingBriefFieldMaxRunes); question != "" {
			questions = append(questions, question)
		}
	}
	doc.OpenQuestions = questions
	return doc
}

// renderPackagingStoryOutline is the one Markdown shape the editor shows and
// parsePackagingStoryOutline reads back, so a hand edit in Document Studio
// and a model revision stay in the same structure.
func renderPackagingStoryOutline(doc packagingStoryDoc) string {
	lines := []string{"# " + firstNonEmptyString(doc.Title, "Story outline"), ""}
	if doc.Audience != "" {
		lines = append(lines, "**Audience:** "+doc.Audience, "")
	}
	if doc.Thesis != "" {
		lines = append(lines, "**Thesis:** "+doc.Thesis, "")
	}
	lines = append(lines, "## Beats", "")
	for index, beat := range doc.Beats {
		line := strconv.Itoa(index+1) + ". **" + beat.Headline + "**"
		if beat.Intent != "" {
			line += " — " + beat.Intent
		}
		lines = append(lines, line)
		if len(beat.EvidenceNeeds) > 0 {
			lines = append(lines, "- Evidence: "+strings.Join(beat.EvidenceNeeds, "; "))
		}
	}
	lines = append(lines, "", "## Open questions", "")
	if len(doc.OpenQuestions) == 0 {
		lines = append(lines, "- None yet — workshop the beats in the story thread.")
	}
	for _, question := range doc.OpenQuestions {
		lines = append(lines, "- "+question)
	}
	return strings.Join(lines, "\n") + "\n"
}

// parsePackagingStoryOutline reads the rendered shape back (title, audience,
// thesis, numbered bold beats with an optional "— intent" and an Evidence
// sub-line, open questions). Beat ids are re-derived from position + headline
// unless a stored doc supplies them by matching headline.
func parsePackagingStoryOutline(markdown string, stored *packagingStoryDoc) packagingStoryDoc {
	doc := packagingStoryDoc{}
	section := ""
	storedByHeadline := map[string]packagingStoryBeat{}
	if stored != nil {
		for _, beat := range stored.Beats {
			storedByHeadline[strings.ToLower(strings.TrimSpace(beat.Headline))] = beat
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "# ") && doc.Title == "":
			doc.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		case strings.HasPrefix(trimmed, "**Audience:**"):
			doc.Audience = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Audience:**"))
		case strings.HasPrefix(trimmed, "**Thesis:**"):
			doc.Thesis = strings.TrimSpace(strings.TrimPrefix(trimmed, "**Thesis:**"))
		case strings.HasPrefix(trimmed, "## "):
			section = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")))
		case section == "beats":
			if match := packagingStoryBeatLinePattern.FindStringSubmatch(line); match != nil {
				beat := packagingStoryBeat{Headline: strings.TrimSpace(match[2]), Intent: strings.TrimSpace(match[3])}
				if prior, ok := storedByHeadline[strings.ToLower(beat.Headline)]; ok {
					beat.ID = prior.ID
				}
				doc.Beats = append(doc.Beats, beat)
			} else if match := packagingStoryEvidenceLinePattern.FindStringSubmatch(line); match != nil && len(doc.Beats) > 0 {
				for _, need := range strings.Split(match[1], ";") {
					if need = strings.TrimSpace(need); need != "" {
						doc.Beats[len(doc.Beats)-1].EvidenceNeeds = append(doc.Beats[len(doc.Beats)-1].EvidenceNeeds, need)
					}
				}
			}
		case section == "open questions" && strings.HasPrefix(trimmed, "- "):
			question := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if !strings.HasPrefix(question, "None yet") {
				doc.OpenQuestions = append(doc.OpenQuestions, question)
			}
		}
	}
	return normalizePackagingStoryDoc(doc)
}

// packagingStoryDocForEntry returns the outline's structure: the stored doc
// when its Markdown is still the rendered one, otherwise the Markdown parsed
// back (a hand edit in the editor wins over the stale stored doc).
func packagingStoryDocForEntry(entry meetingMemoryEntry) packagingStoryDoc {
	markdown := documentStudioDocumentFromEntry(entry).Markdown
	var stored *packagingStoryDoc
	if raw := strings.TrimSpace(entry.Metadata[packagingStoryDocMetadataKey]); raw != "" {
		var doc packagingStoryDoc
		if json.Unmarshal([]byte(raw), &doc) == nil {
			doc = normalizePackagingStoryDoc(doc)
			if renderPackagingStoryOutline(doc) == markdown {
				return doc
			}
			stored = &doc
		}
	}
	return parsePackagingStoryOutline(markdown, stored)
}

// packagingStoryScaffoldDoc is the keyless fallback: the same contract shape,
// beats sized by length, no invented facts.
func packagingStoryScaffoldDoc(brief packagingStoryBrief) packagingStoryDoc {
	count, _ := packagingLengthSlides(brief.Length)
	if count == 0 {
		count = packagingLengthWords["standard"]
	}
	beats := []packagingStoryBeat{
		{Headline: "Open — why now", Intent: "Name the moment that makes this worth the audience's attention today.", EvidenceNeeds: []string{"one dated fact that proves the moment"}},
		{Headline: "The audience's world", Intent: "State what this audience believes or needs right now.", EvidenceNeeds: []string{"an audience quote or observed behavior"}},
		{Headline: "Tension — what is at stake", Intent: "Show why the status quo cannot hold.", EvidenceNeeds: []string{"a cost, risk, or trend figure with units and a date"}},
		{Headline: "Turn — the insight", Intent: "Deliver the overlooked truth the thesis rests on.", EvidenceNeeds: []string{"the strongest primary source"}},
		{Headline: "Proof — evidence that it holds", Intent: "Put the decisive numbers side by side.", EvidenceNeeds: []string{"a benchmark table of named peers"}},
		{Headline: "Objection — the strongest counter", Intent: "Confront the best argument against the thesis.", EvidenceNeeds: []string{"the counter-case in its own words"}},
		{Headline: "Answer — why it still holds", Intent: "Resolve the objection without dismissing it.", EvidenceNeeds: []string{"one fact that neutralizes the objection"}},
		{Headline: "Plan — what happens next", Intent: "Lay out the sequence and who owns each step.", EvidenceNeeds: []string{"milestones with dates"}},
		{Headline: "Measure — how we will know", Intent: "Name the metric and threshold that would change the decision.", EvidenceNeeds: []string{"a baseline figure"}},
		{Headline: "Close — the ask", Intent: "Make the one concrete request of this audience.", EvidenceNeeds: nil},
	}
	if count <= 8 {
		beats = append(beats[:6:6], beats[7], beats[9])
	} else if count >= 16 {
		beats = append(beats[:4:4], append([]packagingStoryBeat{{Headline: "Reveal — what changes", Intent: "Show what the insight changes for the audience.", EvidenceNeeds: []string{"a before/after comparison"}}, {Headline: "Comparison — the alternatives", Intent: "Weigh the credible alternatives on one measure.", EvidenceNeeds: []string{"the same measure across alternatives"}}}, beats[4:]...)...)
	}
	doc := packagingStoryDoc{
		Title: brief.Subject, Thesis: brief.Thesis, Audience: brief.Audience, Beats: beats,
		OpenQuestions: []string{"Which single decision should the deck end on?", "What evidence is already on hand versus still to gather?"},
	}
	return normalizePackagingStoryDoc(doc)
}

func packagingStoryOutlineSchema(revision bool) *openAIJSONSchema {
	beat := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"id":             map[string]any{"type": "string"},
			"headline":       map[string]any{"type": "string"},
			"intent":         map[string]any{"type": "string"},
			"evidence_needs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"id", "headline", "intent", "evidence_needs"},
	}
	properties := map[string]any{
		"title":          map[string]any{"type": "string"},
		"thesis":         map[string]any{"type": "string"},
		"audience":       map[string]any{"type": "string"},
		"beats":          map[string]any{"type": "array", "items": beat},
		"open_questions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}
	required := []string{"title", "thesis", "audience", "beats", "open_questions"}
	name := packagingStoryOutlineContract
	if revision {
		properties["changed_beat_ids"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		properties["summary"] = map[string]any{"type": "string"}
		required = append(required, "changed_beat_ids", "summary")
		name = "story_outline_revision_v1"
	}
	return &openAIJSONSchema{Name: name, Description: "Story Studio narrative outline", Schema: map[string]any{
		"type": "object", "additionalProperties": false, "properties": properties, "required": required,
	}}
}

func packagingStoryNarrativeInstructions() string {
	return strings.Join([]string{
		"You are Scout's narrative architect for Bonfire's Story Studio. Build the story spine a presentation will be written from: the current reality, the tension that makes the status quo untenable, the decisive turn, proof, the strongest objection answered, and the action this audience should take.",
		"Return exactly " + strconv.Itoa(packagingStoryMinBeats) + " to " + strconv.Itoa(packagingStoryMaxBeats) + " beats in order. Each beat has a stable id (kebab-case), a headline that lands in one spoken breath, a one-line intent (what this beat must make the audience think or feel), and evidence_needs: the specific facts, figures, or sources the beat needs before it can be written. Never invent facts, numbers, or quotes; name them as needs.",
		"thesis is one sentence. open_questions lists only decisions the requester must still make; keep it short. This is a story, not a topic list. No visual, imagery, palette, or layout direction. Output strict JSON only.",
	}, " ")
}

func packagingStoryRevisionInstructions() string {
	return strings.Join([]string{
		packagingStoryNarrativeInstructions(),
		"You are REVISING an existing outline against one workshop message. Keep every beat the message does not challenge exactly as it is, including its id. Change, reorder, add or cut only what the message asks for or clearly implies; list every beat id you changed, added, moved, or removed in changed_beat_ids. Give new beats a new id. Do not ask questions back; make the best call and state it in summary. summary is one or two short sentences describing what moved, changed, or was cut (for example: moved the risk beat before the ask; cut beat 7).",
	}, " ")
}

func packagingStoryBriefInput(brief packagingStoryBrief) string {
	lines := []string{"Subject: " + brief.Subject}
	if brief.Audience != "" {
		lines = append(lines, "Audience: "+brief.Audience)
	}
	if brief.Thesis != "" {
		lines = append(lines, "Working thesis: "+brief.Thesis)
	}
	if count, _ := packagingLengthSlides(brief.Length); count > 0 {
		lines = append(lines, "Target deck length: "+strconv.Itoa(count)+" slides")
	}
	return strings.Join(lines, "\n")
}

// packagingStoryModelCall runs one bounded seatChat call. ok=false means the
// caller must fall back deterministically; provenance says why.
func (app *kanbanBoardApp) packagingStoryModelCall(ctx context.Context, workflow string, instructions string, input string, schema *openAIJSONSchema, validate func(string) error) (string, string, bool) {
	if app == nil {
		return "", packagingStoryDraftedByScaffold, false
	}
	apiKey := app.currentOpenAIAPIKey()
	if apiKey == "" {
		return "", packagingStoryDraftedByScaffold, false
	}
	if _, paused := providerBreakers.paused(providerOpenAI, seatChat); paused {
		return "", packagingStoryDraftedByScaffold + ":breaker_open", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	capture := &providerCallProvenanceCapture{}
	callCtx = withProviderCallProvenanceCapture(callCtx, capture)
	output, err := createOpenAITextResponse(callCtx, apiKey, openAITextRequest{
		Model: scoutChatModel(), Seat: seatChat, Workflow: workflow,
		Instructions: instructions, Input: input,
		ReasoningEffort: "medium", Verbosity: "low", MaxOutputTokens: packagingStoryDraftTokens,
		JSONSchema: schema, ValidateOutput: validate,
	})
	if err != nil {
		return "", packagingStoryDraftedByScaffold + ":model_error", false
	}
	provenance := "model:" + scoutChatModel()
	if stamped, ok := capture.snapshot(); ok && strings.TrimSpace(stamped.Model) != "" {
		provenance = "model:" + stamped.Model
	}
	return strings.TrimSpace(output), provenance, true
}

func packagingStoryDocValid(doc packagingStoryDoc) error {
	if len(doc.Beats) < packagingStoryMinBeats || len(doc.Beats) > packagingStoryMaxBeats {
		return fmt.Errorf("outline must carry %d to %d beats, got %d", packagingStoryMinBeats, packagingStoryMaxBeats, len(doc.Beats))
	}
	if strings.TrimSpace(doc.Thesis) == "" {
		return fmt.Errorf("outline thesis is required")
	}
	return nil
}

// draftPackagingStory produces the first outline: the model's narrative
// contract when the chat seat is available, else the scaffold (draftedBy
// "scaffold[:reason]").
func (app *kanbanBoardApp) draftPackagingStory(ctx context.Context, brief packagingStoryBrief) (packagingStoryDoc, string) {
	output, provenance, ok := app.packagingStoryModelCall(ctx, packagingStoryWorkflowDraft, packagingStoryNarrativeInstructions(), packagingStoryBriefInput(brief), packagingStoryOutlineSchema(false), func(text string) error {
		var doc packagingStoryDoc
		if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &doc); err != nil {
			return err
		}
		return packagingStoryDocValid(normalizePackagingStoryDoc(doc))
	})
	if ok {
		var doc packagingStoryDoc
		if json.Unmarshal([]byte(output), &doc) == nil {
			doc = normalizePackagingStoryDoc(doc)
			if packagingStoryDocValid(doc) == nil {
				doc.Title = firstNonEmptyString(doc.Title, brief.Subject)
				doc.Audience = firstNonEmptyString(doc.Audience, brief.Audience)
				return doc, provenance
			}
		}
		provenance = packagingStoryDraftedByScaffold + ":model_invalid"
	}
	return packagingStoryScaffoldDoc(brief), provenance
}

// mergePackagingStoryRevision enforces "keep what was not challenged": every
// prior beat whose id is not in changed ids keeps its exact prior content
// and, when the model dropped it, its prior position; beats the model added
// are kept as additions.
func mergePackagingStoryRevision(prior packagingStoryDoc, revision packagingStoryRevision) packagingStoryDoc {
	changed := map[string]bool{}
	for _, id := range revision.ChangedBeatIDs {
		changed[strings.TrimSpace(id)] = true
	}
	priorByID := map[string]packagingStoryBeat{}
	for _, beat := range prior.Beats {
		priorByID[beat.ID] = beat
	}
	merged := make([]packagingStoryBeat, 0, len(revision.Beats)+len(prior.Beats))
	present := map[string]bool{}
	for _, beat := range revision.Beats {
		if previous, ok := priorByID[beat.ID]; ok && !changed[beat.ID] {
			beat = previous
		}
		if present[beat.ID] {
			continue
		}
		present[beat.ID] = true
		merged = append(merged, beat)
	}
	// Unchallenged beats the model silently dropped come back at their prior
	// neighbourhood (after the nearest surviving predecessor).
	for index, beat := range prior.Beats {
		if present[beat.ID] || changed[beat.ID] {
			continue
		}
		insertAt := 0
		for back := index - 1; back >= 0; back-- {
			if position := packagingStoryBeatPosition(merged, prior.Beats[back].ID); position >= 0 {
				insertAt = position + 1
				break
			}
		}
		merged = append(merged[:insertAt], append([]packagingStoryBeat{beat}, merged[insertAt:]...)...)
		present[beat.ID] = true
	}
	doc := packagingStoryDoc{
		Title: firstNonEmptyString(revision.Title, prior.Title), Thesis: firstNonEmptyString(revision.Thesis, prior.Thesis),
		Audience: firstNonEmptyString(revision.Audience, prior.Audience), Beats: merged, OpenQuestions: revision.OpenQuestions,
	}
	if revision.OpenQuestions == nil {
		doc.OpenQuestions = prior.OpenQuestions
	}
	return normalizePackagingStoryDoc(doc)
}

func packagingStoryBeatPosition(beats []packagingStoryBeat, id string) int {
	for index, beat := range beats {
		if beat.ID == id {
			return index
		}
	}
	return -1
}

// packagingStoryDiffSummary is the server-computed change list: cut, added,
// moved, revised — the fallback when the model's summary is empty and the
// audit trail either way.
func packagingStoryDiffSummary(prior packagingStoryDoc, next packagingStoryDoc) string {
	priorIndex := map[string]int{}
	for index, beat := range prior.Beats {
		priorIndex[beat.ID] = index
	}
	nextIndex := map[string]int{}
	for index, beat := range next.Beats {
		nextIndex[beat.ID] = index
	}
	var cut, added, moved, revised []string
	for _, beat := range prior.Beats {
		if _, ok := nextIndex[beat.ID]; !ok {
			cut = append(cut, "beat "+strconv.Itoa(priorIndex[beat.ID]+1)+" ("+beat.Headline+")")
		}
	}
	kept := make([]string, 0)
	for _, beat := range next.Beats {
		if _, ok := priorIndex[beat.ID]; !ok {
			added = append(added, beat.Headline)
			continue
		}
		kept = append(kept, beat.ID)
	}
	// A beat "moved" when its order among the kept beats changed.
	priorKept := make([]string, 0, len(kept))
	for _, beat := range prior.Beats {
		if _, ok := nextIndex[beat.ID]; ok {
			priorKept = append(priorKept, beat.ID)
		}
	}
	for position, id := range kept {
		if position < len(priorKept) && priorKept[position] != id {
			headline := next.Beats[nextIndex[id]].Headline
			if nextIndex[id]+1 < len(next.Beats) {
				moved = append(moved, "moved "+headline+" before "+next.Beats[nextIndex[id]+1].Headline)
			} else {
				moved = append(moved, "moved "+headline+" to the end")
			}
			break
		}
	}
	for _, beat := range next.Beats {
		index, ok := priorIndex[beat.ID]
		if !ok {
			continue
		}
		previous := prior.Beats[index]
		if previous.Headline != beat.Headline || previous.Intent != beat.Intent || strings.Join(previous.EvidenceNeeds, ";") != strings.Join(beat.EvidenceNeeds, ";") {
			revised = append(revised, beat.Headline)
		}
	}
	parts := make([]string, 0, 4)
	parts = append(parts, moved...)
	if len(revised) > 0 {
		parts = append(parts, "revised "+strings.Join(revised, ", "))
	}
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(cut) > 0 {
		parts = append(parts, "cut "+strings.Join(cut, ", "))
	}
	if prior.Thesis != next.Thesis && next.Thesis != "" {
		parts = append(parts, "sharpened the thesis")
	}
	if len(parts) == 0 {
		return "Kept the outline as it is."
	}
	sort.Strings(parts[len(moved):])
	summary := strings.Join(parts, "; ")
	return strings.ToUpper(summary[:1]) + summary[1:] + "."
}

// revisePackagingStory asks the model to revise the outline against one
// workshop message and merges the answer under the keep-unchallenged law.
// Keyless servers keep the outline and say so; nothing is ever asked back.
func (app *kanbanBoardApp) revisePackagingStory(ctx context.Context, prior packagingStoryDoc, brief packagingStoryBrief, message string) (packagingStoryDoc, string, string) {
	priorJSON, _ := json.Marshal(prior)
	input := strings.Join([]string{packagingStoryBriefInput(brief), "", "Current outline (JSON):", string(priorJSON), "", "Workshop message:", strings.TrimSpace(message)}, "\n")
	output, provenance, ok := app.packagingStoryModelCall(ctx, packagingStoryWorkflowRevise, packagingStoryRevisionInstructions(), input, packagingStoryOutlineSchema(true), func(text string) error {
		var revision packagingStoryRevision
		if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &revision); err != nil {
			return err
		}
		return packagingStoryDocValid(mergePackagingStoryRevision(prior, revision))
	})
	if !ok {
		return prior, "Kept the outline as it is; no model available to revise it right now — edit the outline directly or try again later.", provenance
	}
	var revision packagingStoryRevision
	if json.Unmarshal([]byte(output), &revision) != nil {
		return prior, "Kept the outline as it is — Scout's revision was not usable.", packagingStoryDraftedByScaffold + ":model_invalid"
	}
	next := mergePackagingStoryRevision(prior, revision)
	if packagingStoryDocValid(next) != nil {
		return prior, "Kept the outline as it is — Scout's revision was not usable.", packagingStoryDraftedByScaffold + ":model_invalid"
	}
	summary := packagingStoryDiffSummary(prior, next)
	if modelSummary := packagingBoundedText(revision.Summary, packagingBriefFieldMaxRunes); modelSummary != "" {
		summary = modelSummary
	}
	return next, summary, provenance
}

// savePackagingStoryDoc journals one new outline version (Wave 4 journal via
// the header-fenced update) with the structured doc and drafting provenance.
// turnMessageID/turnSummary record the workshop message this revision answered
// so a retry of the same turn is a no-op rather than a second revision.
func (app *kanbanBoardApp) savePackagingStoryDoc(user *userAccount, prior meetingMemoryEntry, doc packagingStoryDoc, draftedBy string, turnMessageID string, turnSummary string) (meetingMemoryEntry, error) {
	rawDoc, err := json.Marshal(doc)
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	header := resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(prior))
	storedBody, emptyMarker := documentStudioStoredBody(renderPackagingStoryOutline(doc))
	updated, changed, err := app.memory.updateOSArtifactWithMetadataIfHeaderMatches(header, prior.ID, firstNonEmptyString(doc.Title, prior.Metadata["title"]), storedBody, user.Name, map[string]string{
		"type": artifactTypeMarkdown, "documentSchemaVersion": "1", documentStudioEmptyMetadataKey: emptyMarker, artifactRestoredFromMetadataKey: "",
		packagingStoryDocMetadataKey:           string(rawDoc),
		packagingStoryDraftedByMetadataKey:     draftedBy,
		packagingStoryTurnMessageIDMetadataKey: strings.TrimSpace(turnMessageID),
		packagingStoryTurnSummaryMetadataKey:   packagingBoundedText(turnSummary, packagingBriefFieldMaxRunes),
	})
	if err != nil {
		return meetingMemoryEntry{}, err
	}
	if !changed {
		return prior, nil
	}
	return updated, nil
}

// packagingStoryTurnReply is the one shape of Scout's workshop answer. The id
// is derived from the user message so a re-commit of the same turn cannot land
// two replies in the thread.
func packagingStoryTurnReply(userMessage scoutChatMessageRecord, summary string, version int) scoutChatMessageRecord {
	return scoutChatMessageRecord{
		ID:            "scout-chat-message-story-" + sha256Hex([]byte("packaging-story-turn/v1\x00" + strings.TrimSpace(userMessage.ID)))[:24],
		Kind:          "message",
		Role:          "scout",
		AuthorName:    scoutParticipantName,
		IntentOutcome: string(conversationIntentConversationalReply), CausedByMessageID: userMessage.ID,
		Text:      summary + " (outline v" + strconv.Itoa(version) + ")",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// packagingStoryWorkshopTurn revises the outline against one committed user
// message in the bound thread, journals the version, and returns the Scout
// reply (diff summary) for the caller to commit. ok=false when the thread is
// not a story thread the viewer may workshop.
func (app *kanbanBoardApp) packagingStoryWorkshopTurn(ctx context.Context, user *userAccount, story meetingMemoryEntry, userMessage scoutChatMessageRecord) (meetingMemoryEntry, scoutChatMessageRecord, bool) {
	if app == nil || user == nil || !packagingStoryOutlineArtifact(story) || strings.TrimSpace(userMessage.Text) == "" {
		return story, scoutChatMessageRecord{}, false
	}
	// read outline → model → save is one critical section per outline. The
	// chat door and PATCH {message} both land here, and the save compares only
	// the authorization header (no version CAS), so two overlapping turns would
	// each report their revision applied while one silently overwrote the
	// other. The artifact is re-read under the lock so the model always
	// revises the current version.
	turnLock := app.scoutChatThreadLock("packaging-story-turn-" + strings.TrimSpace(story.ID))
	turnLock.Lock()
	defer turnLock.Unlock()
	if current, ok := app.osArtifactByID(story.ID); ok && packagingStoryOutlineArtifact(current) {
		story = current
	}
	// Idempotent per user message even when the reply never reached the
	// thread: the append path calls the story hook again for the same message
	// after a commit failure, and a second model call would journal a second
	// version for one ask.
	if strings.TrimSpace(userMessage.ID) != "" && strings.TrimSpace(story.Metadata[packagingStoryTurnMessageIDMetadataKey]) == strings.TrimSpace(userMessage.ID) {
		replayed := strings.TrimSpace(story.Metadata[packagingStoryTurnSummaryMetadataKey])
		if replayed == "" {
			replayed = "Kept the outline as it is."
		}
		return story, packagingStoryTurnReply(userMessage, replayed, artifactVersion(story)), true
	}
	brief := packagingStoryBrief{}
	if raw := strings.TrimSpace(story.Metadata[packagingStoryBriefMetadataKey]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &brief)
	}
	brief.Subject = firstNonEmptyString(brief.Subject, story.Metadata["title"])
	prior := packagingStoryDocForEntry(story)
	next, summary, provenance := app.revisePackagingStory(ctx, prior, brief, userMessage.Text)
	updated := story
	if renderPackagingStoryOutline(next) != renderPackagingStoryOutline(prior) {
		saved, err := app.savePackagingStoryDoc(user, story, next, provenance, userMessage.ID, summary)
		if err != nil {
			summary = "Kept the outline as it is — the new version could not be saved: " + err.Error()
		} else {
			updated = saved
		}
	} else if strings.TrimSpace(userMessage.ID) != "" {
		// No body change: still record that this message was answered, so a
		// retry replays the same answer instead of paying for another pass.
		if stamped, changed, err := app.memory.updateOSArtifactMetadata(story.ID, map[string]string{
			packagingStoryTurnMessageIDMetadataKey: strings.TrimSpace(userMessage.ID),
			packagingStoryTurnSummaryMetadataKey:   packagingBoundedText(summary, packagingBriefFieldMaxRunes),
		}); err == nil && changed {
			updated = stamped
		}
	}
	return updated, packagingStoryTurnReply(userMessage, summary, artifactVersion(updated)), true
}

// packagingStoryForThread resolves the outline bound to a thread (the
// append-path seam: a message in a story thread is a workshop turn).
func (app *kanbanBoardApp) packagingStoryForThread(threadID string) (meetingMemoryEntry, bool) {
	if app == nil || app.memory == nil || strings.TrimSpace(threadID) == "" {
		return meetingMemoryEntry{}, false
	}
	for _, candidate := range app.memory.artifactListAuthorizationSnapshot() {
		if !packagingStoryOutlineArtifact(candidate.Entry) || strings.TrimSpace(candidate.Entry.Metadata[packagingStoryThreadIDMetadataKey]) != strings.TrimSpace(threadID) {
			continue
		}
		return app.osArtifactByID(candidate.Entry.ID)
	}
	return meetingMemoryEntry{}, false
}

// packagingStoryThreadTurn is the hook the chat append path calls for a
// private thread bound to a story outline: the user's message is already
// committed by the caller's committer; Scout's diff-summary reply is
// committed here and the turn is owned. (nil, false) hands the turn back.
func (app *kanbanBoardApp) packagingStoryThreadTurn(ctx context.Context, user *userAccount, thread scoutChatThreadRecord, userMessage scoutChatMessageRecord, commit scoutChatMessageCommitter) (map[string]any, bool) {
	if app == nil || user == nil || commit == nil || scoutChatThreadVisibility(thread) != scoutChatVisibilityPrivate ||
		normalizeAccountEmail(thread.OwnerEmail) != normalizeAccountEmail(user.Email) || !scoutChatThreadAllowsViewer(thread, user.Email) {
		return nil, false
	}
	story, ok := app.packagingStoryForThread(thread.ID)
	if !ok {
		return nil, false
	}
	if _, authorized := app.authorizedArtifactForActions(ctx, user, story.ID, ACLReadContent, ACLWrite); !authorized {
		return nil, false
	}
	// Idempotent per message id: a redelivered turn that Scout already
	// answered returns the recorded reply and revises nothing again.
	if current, _, err := app.scoutChatThreadByID(user.Email, thread.ID); err == nil {
		for _, message := range current.Messages {
			if strings.EqualFold(strings.TrimSpace(message.Role), "scout") && strings.TrimSpace(message.CausedByMessageID) == strings.TrimSpace(userMessage.ID) && strings.TrimSpace(userMessage.ID) != "" {
				return map[string]any{
					"ok": true, "message": userMessage, "answer": message, "thread": current, "replayed": true,
					"intentOutcome": string(conversationIntentConversationalReply), "story": packagingStoryViewFromEntry(story),
				}, true
			}
		}
	}
	updated, reply, ok := app.packagingStoryWorkshopTurn(ctx, user, story, userMessage)
	if !ok {
		return nil, false
	}
	saved, err := commit(userMessage, reply)
	if err != nil {
		return nil, false
	}
	return map[string]any{
		"ok": true, "message": userMessage, "answer": reply, "thread": saved,
		"intentOutcome": string(conversationIntentConversationalReply), "story": packagingStoryViewFromEntry(updated),
	}, true
}
