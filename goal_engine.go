package main

// The /goal execution engine: a persisted state machine on the mode=goal
// artifact record. Where agent_runner_anthropic.go runs the ten-step loop as a
// single in-process tool loop, this engine makes the loop *durable* — each
// stage is its own transition, the plan (metadata["goalPlan"]) is persisted at
// every step, subtasks execute as launchAgentThreadWithOrigin children whose
// completion folds back into the parent plan, and a boot reconciler resumes any
// goal not in a terminal state. The gates (review, ship) are themselves model
// calls, and no external_write ships without a prior human approval record.
//
// State is authoritative (metadata["currentStage"]); percent is advisory. The
// state consts are a superset of the stage strings agent_thread_runner.go
// already writes, so the running-artifact card renders unchanged.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Goal state enum (technical §2.1). These strings are stamped into
// metadata["currentStage"]; they extend the existing stage strings so the UI
// progress card needs no change.
const (
	goalStateIdentify   = "identify_goal"
	goalStateDecompose  = "decompose"
	goalStateAssign     = "assign"
	goalStateCoordinate = "coordinate"
	goalStateExecute    = "execute_in_order"
	goalStateReview     = "review_against_goal"
	goalStateGate       = "gate_before_shipping"
	goalStateSave       = "save_what_worked"
	goalStateReport     = "report"
	goalStateVerify     = "verify_goal_completed"
	goalStateCommit     = "commit_push"       // external_write path only, post-approval
	goalStateVerified   = "verified"          // terminal success
	goalStateBlocked    = "needs_attention"   // terminal-until-human
	goalStateApproval   = "approval_required" // waiting on admin gate
)

// Subtask status enum (technical §2.2). A subtask is `ready` when every
// dependsOn id is `complete`.
const (
	subtaskPending  = "pending"
	subtaskReady    = "ready"
	subtaskRunning  = "running"
	subtaskComplete = "complete"
	subtaskFailed   = "failed"
	subtaskBlocked  = "blocked"
)

const (
	goalReviewPass   = "pass"
	goalReviewFail   = "fail"
	goalReviewRevise = "revise"
)

// goalCommitSubtaskID is the pseudo-subtask id the single external_write
// commit_push child carries in goalSubtaskId, so the shared codex-callback fold
// hook routes it to the commit-completion path rather than a real subtask.
const goalCommitSubtaskID = "__commit_push__"

const (
	goalPlanVersion        = 2
	goalMaxSubtasks        = 6 // six users, one VPS — a plan wanting 40 subtasks is a modeling error
	goalMaxDecomposeTries  = 2 // malformed decompose JSON is retryable, then needs_attention
	goalMaxRevisions       = 2 // review fail/revise re-queues a subtask, then it blocks
	goalReconcileScanLimit = 200
	goalDriveIterationCap  = 64 // guards against a transition cycle looping forever
)

// goalPlan is the persisted state machine. One artifact = one goal = one plan.
type goalPlan struct {
	PlanVersion       int              `json:"planVersion"`
	GoalID            string           `json:"goalId"`
	Objective         string           `json:"objective"`
	CreatedBy         string           `json:"createdBy"`
	Authority         string           `json:"authority"`
	PackageID         string           `json:"packageId,omitempty"`
	ToolTemplate      string           `json:"toolTemplate,omitempty"`
	State             string           `json:"state"`
	Subtasks          []goalSubtask    `json:"subtasks"`
	Gate              goalGate         `json:"gate"`
	Report            goalReport       `json:"report"`
	Verification      goalVerification `json:"verification"`
	DecomposeAttempts int              `json:"decomposeAttempts,omitempty"`
	Blocker           string           `json:"blocker,omitempty"`
	// MaxProgress is the monotonic high-water mark for the advisory percent. A
	// revision re-queue reverts a verified subtask to running, which lowers the
	// raw execute-phase percent; holding the high-water mark keeps the goal card
	// from reading as running backwards while it legitimately revises.
	MaxProgress int `json:"maxProgress,omitempty"`
	// Cancelled marks a user-initiated cancel (spec §2 "misfire economics"): the
	// goal is terminal needs_attention, dispatchReady refuses further subtasks,
	// and a still-running child's completion folds into a no-op. Persisted with
	// the plan so the flag survives restarts alongside the cancelledBy/At record.
	Cancelled   bool   `json:"cancelled,omitempty"`
	CancelledBy string `json:"cancelledBy,omitempty"`
	CancelledAt string `json:"cancelledAt,omitempty"`
}

type goalSubtask struct {
	ID         string             `json:"id"`
	Title      string             `json:"title"`
	Detail     string             `json:"detail,omitempty"`
	Mode       string             `json:"mode"`
	Runner     string             `json:"runner"`
	Authority  string             `json:"authority"`
	DependsOn  []string           `json:"dependsOn"`
	Status     string             `json:"status"`
	ArtifactID string             `json:"artifactId,omitempty"`
	ThreadID   string             `json:"threadId,omitempty"`
	Attempts   int                `json:"attempts"`
	Revisions  int                `json:"revisions,omitempty"`
	Review     *goalSubtaskReview `json:"review"`
	// Protect is the accumulated protect list: everything a reviewer explicitly
	// praised (strengths_to_keep) across review rounds. It lives on the subtask
	// — persisted with the plan in the goal artifact metadata — so later rounds
	// inherit earlier praise, and every requeue prompt carries it as the
	// "DO NOT LOSE (protected)" block a revision must keep intact.
	Protect []string `json:"protect,omitempty"`
}

type goalSubtaskReview struct {
	Verdict string  `json:"verdict"`
	Score   float64 `json:"score,omitempty"`
	Reasons string  `json:"reasons,omitempty"`
	By      string  `json:"by,omitempty"`
}

type goalGate struct {
	Status           string `json:"status"` // pending|passed|blocked|approval_required
	ReviewedBy       string `json:"reviewedBy,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired"`
	Reason           string `json:"reason,omitempty"`
	Command          string `json:"command,omitempty"`       // the external_write command the gate recorded
	CommitChildID    string `json:"commitChildId,omitempty"` // the one external_write sidecar child, for idempotent commit_push
}

type goalReport struct {
	Changed           string   `json:"changed,omitempty"`
	Headline          string   `json:"headline,omitempty"`
	Gap               string   `json:"gap,omitempty"`
	Next              string   `json:"next,omitempty"`
	GateOutcome       string   `json:"gateOutcome,omitempty"`
	AssumedClaimCount int      `json:"assumedClaimCount"`
	ArtifactIDs       []string `json:"artifactIds,omitempty"`
	// DeliverableArtifactID is the salvaged best-draft child artifact of a goal
	// that terminated needs_attention. It is attached to the package and
	// surfaced so an 8/10 draft is never orphaned when revisions run out.
	DeliverableArtifactID string `json:"deliverableArtifactId,omitempty"`
	// SavedLessons is save_what_worked's distilled output (2-4 one-line
	// lessons: reviewer praise that survived revision, what needed revision,
	// what the gate cleared) — persisted with the plan, mirrored into
	// metadata["savedLessons"], and emitted once as a goal_lessons signal so
	// the Taste Analyst can consume them.
	SavedLessons []string `json:"savedLessons,omitempty"`
}

type goalVerification struct {
	Verdict   string `json:"verdict"` // pending|pass|fail
	CheckedAt string `json:"checkedAt,omitempty"`
	Reasons   string `json:"reasons,omitempty"`
}

func (p *goalPlan) subtaskByID(id string) *goalSubtask {
	id = strings.TrimSpace(id)
	for index := range p.Subtasks {
		if p.Subtasks[index].ID == id {
			return &p.Subtasks[index]
		}
	}
	return nil
}

func decodeGoalPlan(raw string) (goalPlan, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return goalPlan{}, false
	}
	var plan goalPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return goalPlan{}, false
	}
	if strings.TrimSpace(plan.State) == "" {
		return goalPlan{}, false
	}
	return plan, true
}

// --- Plan validation ---------------------------------------------------------

// validateGoalPlan enforces the schema invariants a decompose model call must
// satisfy: 1..6 subtasks, unique non-empty ids, a real agent-thread mode, and a
// dependency graph that references only known ids and is acyclic (so the
// topological executor always makes progress).
func validateGoalPlan(plan *goalPlan) error {
	count := len(plan.Subtasks)
	if count == 0 {
		return fmt.Errorf("plan has no subtasks")
	}
	if count > goalMaxSubtasks {
		return fmt.Errorf("plan has %d subtasks, max is %d — coarsen the decomposition", count, goalMaxSubtasks)
	}
	ids := make(map[string]bool, count)
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		st.ID = strings.TrimSpace(st.ID)
		if st.ID == "" {
			return fmt.Errorf("subtask %d has no id", index)
		}
		if ids[st.ID] {
			return fmt.Errorf("duplicate subtask id %q", st.ID)
		}
		ids[st.ID] = true
		if strings.TrimSpace(st.Title) == "" {
			return fmt.Errorf("subtask %q has no title", st.ID)
		}
		if normalizeAgentThreadMode(st.Mode) == "" {
			return fmt.Errorf("subtask %q has invalid mode %q", st.ID, st.Mode)
		}
	}
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		for _, dep := range st.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == st.ID {
				return fmt.Errorf("subtask %q depends on itself", st.ID)
			}
			if !ids[dep] {
				return fmt.Errorf("subtask %q depends on unknown id %q", st.ID, dep)
			}
		}
	}
	if err := goalPlanTopoOrder(plan); err != nil {
		return err
	}
	return nil
}

// goalPlanTopoOrder returns the subtask ids in dependency order; a cycle is an
// error (the executor could never start such a plan).
func goalPlanTopoOrder(plan *goalPlan) error {
	indegree := map[string]int{}
	dependents := map[string][]string{}
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if _, seen := indegree[st.ID]; !seen {
			indegree[st.ID] = 0
		}
		for _, dep := range st.DependsOn {
			dep = strings.TrimSpace(dep)
			indegree[st.ID]++
			dependents[dep] = append(dependents[dep], st.ID)
		}
	}
	queue := make([]string, 0, len(indegree))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range dependents[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(indegree) {
		return fmt.Errorf("subtask dependencies contain a cycle")
	}
	return nil
}

// --- The engine --------------------------------------------------------------

type goalEngine struct {
	app         *kanbanBoardApp
	responder   anthropicMessagesResponder
	apiKey      func() string
	model       string
	reviewModel string
	effort      string
	maxTokens   int
	concurrency int
	timeout     time.Duration
	now         func() time.Time
}

func newGoalEngine(app *kanbanBoardApp) *goalEngine {
	return &goalEngine{
		app:         app,
		responder:   createAnthropicMessagesResponse,
		apiKey:      currentAnthropicAPIKey,
		model:       orchestratorModel(),
		reviewModel: reviewModel(),
		effort:      orchestratorEffort(),
		maxTokens:   orchestratorMaxTokens(),
		concurrency: goalSubtaskConcurrency(),
		timeout:     orchestratorTimeout(),
		now:         time.Now,
	}
}

func goalSubtaskConcurrency() int {
	// VPS memory ceiling: two in-flight subtasks (technical §2.3 / §6 risk).
	return positiveIntEnv("BONFIRE_GOAL_CONCURRENCY", 2)
}

// --- Per-user in-flight cap ---------------------------------------------------

// goalUserInFlightCap is the per-requester ceiling on concurrently running
// goals. BONFIRE_GOAL_CONCURRENCY caps subtasks inside ONE goal; this caps how
// many whole goals one user can have in flight at once, so a single account
// cannot occupy the whole engine (Wave 1 item 6 — precondition for the router
// and the flagship).
func goalUserInFlightCap() int {
	return positiveIntEnv("BONFIRE_GOAL_USER_CAP", 2)
}

// goalInFlightRef names one in-flight goal in a cap breach so the UI can render
// "finish these first" and the voice path can speak them.
type goalInFlightRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// errGoalUserCapExceeded is the typed launch refusal for a user already at the
// in-flight cap. Error() is deliberately a friendly, speakable sentence — the
// voice initiate_goal path surfaces it verbatim, and the HTTP door unpacks the
// structured fields into the 429 body.
type errGoalUserCapExceeded struct {
	Cap   int
	Goals []goalInFlightRef
}

func (e *errGoalUserCapExceeded) Error() string {
	names := make([]string, 0, len(e.Goals))
	for _, goal := range e.Goals {
		names = append(names, fmt.Sprintf("%q (%s)", goal.Title, goal.ID))
	}
	noun := "goals"
	if len(e.Goals) == 1 {
		noun = "goal"
	}
	return fmt.Sprintf("you already have %d %s in flight — %s. Wait for one to finish (or resolve its blocker) before starting another.",
		len(e.Goals), noun, strings.Join(names, ", "))
}

// inFlightGoalsForUser lists this user's mode=goal artifacts still in a
// non-terminal stage (same terminality rule the boot reconciler uses: verified,
// needs_attention, and approval_required do not count — the last waits on a
// human, not the engine). Matching is on the requestedBy stamp launchGoalThread
// writes for every attributed goal, normalized as an account email.
func (app *kanbanBoardApp) inFlightGoalsForUser(email string) []goalInFlightRef {
	email = normalizeAccountEmail(email)
	if app == nil || app.memory == nil || email == "" {
		return nil
	}
	var goals []goalInFlightRef
	for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, goalReconcileScanLimit) {
		if artifact.Metadata["mode"] != "goal" {
			continue
		}
		if isTerminalGoalState(artifact.Metadata["currentStage"]) {
			continue
		}
		if normalizeAccountEmail(artifact.Metadata["requestedBy"]) != email {
			continue
		}
		title := firstNonEmptyString(artifact.Metadata["title"], compactAssistantLine(artifact.Text))
		goals = append(goals, goalInFlightRef{ID: artifact.ID, Title: title})
	}
	return goals
}

// goalEngineLocks serializes every mutation of one goal's plan. The driver, the
// child-completion fold, and the boot reconciler all take the per-parent lock,
// so a child that completes while the driver is mid-dispatch queues its fold
// behind the driver rather than racing the persisted plan. Package-level (not a
// kanbanBoardApp field) so the engine never touches the struct in kanban.go.
var goalEngineLocks sync.Map // parentArtifactID -> *sync.Mutex

func goalEngineLock(parentID string) *sync.Mutex {
	lock, _ := goalEngineLocks.LoadOrStore(strings.TrimSpace(parentID), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// goalUserCapLocks serializes one user's cap-check-then-launch in
// launchGoalThread: inFlightGoalsForUser counts persisted goal artifacts, so
// without the lock N concurrent launches from the same account all observe the
// pre-launch count, all pass the cap, and all launch. Keyed by normalized
// account email, mirroring goalEngineLocks.
var goalUserCapLocks sync.Map // normalized email -> *sync.Mutex

func goalUserCapLock(email string) *sync.Mutex {
	lock, _ := goalUserCapLocks.LoadOrStore(email, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// --- Tool-template grounding (Wave 10) ---------------------------------------

// resolvedTool returns the goal's tool template entry, if it carries one.
func (e *goalEngine) resolvedTool(plan *goalPlan) (packagingTool, bool) {
	return toolByID(plan.ToolTemplate)
}

// toolPromptContextForPlan fills the master wrapper's grounding slots from the
// studio's own record so a tool-templated goal cannot write from priors alone
// (the wrapper's quality lever). Missing slots fall back to the wrapper's own
// "(none…)" defaults via assembleToolPrompt.
func (e *goalEngine) toolPromptContextForPlan(plan *goalPlan, tool packagingTool) toolPromptContext {
	ctx := toolPromptContext{
		GoalStatement:   plan.Objective,
		Actor:           firstNonEmptyString(plan.CreatedBy, "the studio"),
		SuccessCriteria: "the output satisfies the " + tool.Name + " contract and passes " + firstNonEmptyString(tool.Rubric.Ref, tool.ID+"_gate"),
	}
	artifacts, decisions, recent, memory := e.app.goalGroundingSlotsForRequester(plan.PackageID, plan.CreatedBy)
	ctx.PackageArtifacts = artifacts
	ctx.RelevantDecisions = decisions
	ctx.RelevantArtifacts = recent
	ctx.RelevantMemory = memory
	if pkg, ok := e.app.venturePackageByID(plan.PackageID); ok {
		ctx.PackageName = pkg.Name
	}
	return ctx
}

// goalGroundingSlots returns the four wrapper grounding strings: package-attached
// artifact titles+bodies, package decisions, recent artifacts, and recent
// durable memory. Each is bounded and compacted; an empty slot returns "" so the
// wrapper falls back to its own default.
func (app *kanbanBoardApp) goalGroundingSlots(packageID string) (artifacts string, decisions string, recent string, memory string) {
	if app == nil || app.memory == nil {
		return "", "", "", ""
	}
	packageID = strings.TrimSpace(packageID)
	const maxLines = 6

	var attached, recentLines, decisionLines []string
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, 40) {
		title := firstNonEmptyString(entry.Metadata["title"], compactAssistantLine(entry.Text))
		line := "- " + title + ": " + compactAssistantLine(entry.Text)
		if packageID != "" && strings.TrimSpace(entry.Metadata["packageId"]) == packageID {
			if len(attached) < maxLines {
				attached = append(attached, line)
			}
		} else if len(recentLines) < maxLines {
			recentLines = append(recentLines, "- "+title)
		}
	}
	for _, entry := range app.memory.entriesOfKind(meetingMemoryKindDecision, 40) {
		if packageID != "" && strings.TrimSpace(entry.Metadata["packageId"]) != packageID {
			continue
		}
		decisionLines = append(decisionLines, "- "+compactAssistantLine(entry.Text))
		if len(decisionLines) >= maxLines {
			break
		}
	}
	var memoryLines []string
	for _, entry := range app.memorySnapshotForClients(12) {
		memoryLines = append(memoryLines, "- "+entry.Kind+": "+compactAssistantLine(entry.Text))
	}
	memory = strings.Join(memoryLines, "\n")
	// The office house style is pinned into the memory slot unconditionally
	// once the Wave-4 distiller writes one (packaging-os §5 — injection is
	// pinning, not search). It lives HERE, not in the requester wrapper below,
	// so both grounding hops inherit it: the engine's decompose wrapper
	// (toolPromptContextForPlan) and the generation hop (toolPromptForThread).
	if style, ok := app.houseStyleArtifact(); ok && strings.TrimSpace(style.Text) != "" {
		memory = prependGroundingBlock("Office house style (pinned):", sanitizedPinnedProfileBody(style.Text), memory)
	}
	return strings.Join(attached, "\n"), strings.Join(decisionLines, "\n"), strings.Join(recentLines, "\n"), memory
}

// goalGroundingSlotsForRequester is goalGroundingSlots plus the requester's
// pinned taste profile (packaging-os §5, Wave 3 item 15): the deliverable
// wrapper must carry the living user_profile of whoever asked, and lexical
// slot-filling can never be trusted to find it. Requester-less callers (and
// users without a profile yet) get goalGroundingSlots' output unchanged.
func (app *kanbanBoardApp) goalGroundingSlotsForRequester(packageID string, requestedBy string) (artifacts string, decisions string, recent string, memory string) {
	artifacts, decisions, recent, memory = app.goalGroundingSlots(packageID)
	if app == nil {
		return artifacts, decisions, recent, memory
	}
	if profile, ok := app.tasteProfileForRequester(requestedBy); ok && strings.TrimSpace(profile.Text) != "" {
		memory = prependGroundingBlock("Requester taste profile (pinned):", sanitizedPinnedProfileBody(profile.Text), memory)
	}
	return artifacts, decisions, recent, memory
}

// prependGroundingBlock puts a pinned block ahead of an existing slot string.
// A previously empty slot deliberately becomes non-empty — pinned taste must
// override the wrapper's "(none on record)" default. The body is untrusted
// (distilled from user-typed signals), so it rides between explicit
// reference-data markers with the shared never-instructions preamble —
// callers pass it through sanitizedPinnedProfileBody first.
func prependGroundingBlock(heading string, body string, existing string) string {
	block := heading + "\n" + pinnedProfilePreamble + "\n<<<PINNED PROFILE\n" + body + "\nPINNED PROFILE>>>"
	if strings.TrimSpace(existing) == "" {
		return block
	}
	return block + "\n" + existing
}

// --- Launch path -------------------------------------------------------------

// goalLaunchSpec is the additive input to launchGoalThread. Only Objective is
// required; the rest is derived when absent.
type goalLaunchSpec struct {
	Objective    string
	CreatedBy    string
	Authority    string
	PackageID    string
	ToolTemplate string
	Origin       map[string]string
}

// launchGoalThread creates the mode=goal thread/artifact with an initial plan
// and drives the engine in the background. The engine only activates when
// ANTHROPIC_API_KEY is present; keyless deploys are unchanged (the caller falls
// back to today's launch_agent_thread path).
func (app *kanbanBoardApp) launchGoalThread(spec goalLaunchSpec) (scoutAgentThread, error) {
	if app == nil || app.memory == nil {
		return scoutAgentThread{}, fmt.Errorf("assistant is unavailable")
	}
	objective := canonicalizeBoardText(spec.Objective)
	if objective == "" {
		return scoutAgentThread{}, fmt.Errorf("goal objective is required")
	}
	if !hasAnthropicAPIKey() {
		return scoutAgentThread{}, errAgentWorkerNotConfigured
	}

	createdBy := strings.TrimSpace(spec.CreatedBy)
	// Per-user in-flight cap (Wave 1 item 6). Every production door (HTTP
	// /assistant/goal, voice initiate_goal) stamps the requester, so the check
	// lives here — one seam guards both. An unattributed launch (tests, internal
	// callers) has no bucket and is not capped. The check counts persisted goal
	// artifacts and the append happens below, so the check-then-append pair must
	// be serialized per user — otherwise N concurrent launches all observe the
	// pre-launch count and all pass. The per-email lock is held through the
	// artifact append (goalUserCapLocks, the goalEngineLocks pattern).
	if normalizedRequester := normalizeAccountEmail(createdBy); normalizedRequester != "" {
		lock := goalUserCapLock(normalizedRequester)
		lock.Lock()
		defer lock.Unlock()
		capLimit := goalUserInFlightCap()
		if inFlight := app.inFlightGoalsForUser(createdBy); len(inFlight) >= capLimit {
			return scoutAgentThread{}, &errGoalUserCapExceeded{Cap: capLimit, Goals: inFlight}
		}
	}
	authority := strings.TrimSpace(spec.Authority)
	if authority == "" {
		authority = codexJobAuthorityForThread(scoutAgentThread{Mode: "workflow", Query: objective})
	}
	authority = normalizeCodexJobAuthority(authority)

	// Resolve the tool template (if any). An unknown id degrades to a plain
	// goal — a stray toolTemplate is never an error, per the registry contract.
	toolTemplate := normalizeToolTemplate(spec.ToolTemplate)

	goalID := fmt.Sprintf("agent-thread-goal-%d", app.nowUnixNano())
	plan := goalPlan{
		PlanVersion:  goalPlanVersion,
		GoalID:       goalID,
		Objective:    objective,
		CreatedBy:    createdBy,
		Authority:    authority,
		PackageID:    strings.TrimSpace(spec.PackageID),
		ToolTemplate: toolTemplate,
		State:        goalStateIdentify,
		Gate:         goalGate{Status: "pending"},
		Verification: goalVerification{Verdict: "pending"},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return scoutAgentThread{}, fmt.Errorf("encode goal plan: %w", err)
	}

	body := buildGoalScaffold(plan)
	metadata := map[string]string{
		"source":          "goal_thread",
		"mode":            "goal",
		"threadId":        goalID,
		"threadQuery":     objective,
		"objective":       objective,
		"authority":       authority,
		"agentLoop":       "goal_execution_engine",
		"goalPlan":        string(raw),
		"currentStage":    goalStateIdentify,
		"goalStatus":      "running",
		"reviewGate":      "pending",
		"progressPercent": "5",
		"status":          "running",
		"threadStatus":    "running",
		"published":       "false",
		"latestThreadRun": goalID,
	}
	if createdBy != "" {
		metadata["requestedBy"] = createdBy
	}
	if plan.PackageID != "" {
		metadata["packageId"] = plan.PackageID
	}
	// A tool-templated goal stamps the tool + its output contract so the running
	// card, recall indexing, and the contract parsers see the same shape a
	// single-shot tool thread would (flywheel write #3: the artifact is indexed
	// under its contract for the next tool's grounding).
	if tool, ok := toolByID(toolTemplate); ok {
		metadata["toolTemplate"] = tool.ID
		metadata["toolGroup"] = tool.Group
		if tool.Contract != "" {
			metadata["artifactContract"] = tool.Contract
		}
	}
	for _, key := range agentThreadOriginMetadataKeys {
		if value := strings.TrimSpace(spec.Origin[key]); value != "" {
			metadata[key] = value
		}
	}
	// originSurface is the fine-grained launch surface ("chat:<threadId>",
	// "channel:<id>", …) the return-to-origin card routes on. It is NOT in
	// agentThreadOriginMetadataKeys (those are the room/channel delivery keys), so
	// stamp it explicitly or the push event falls back to the coarse originKind
	// and the Wave 11 return card can never match its origin thread.
	if surface := strings.TrimSpace(spec.Origin["originSurface"]); surface != "" {
		metadata["originSurface"] = surface
	}

	// Base mode "workflow" so createOSArtifactWithMetadata actually persists the
	// artifact (it no-ops on unknown modes) and stamps the goal-workflow
	// scaffolding; the metadata override above flips mode -> goal.
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", objective, body, createdBy, metadata)
	if err != nil {
		return scoutAgentThread{}, err
	}
	if strings.TrimSpace(artifact.ID) == "" {
		return scoutAgentThread{}, fmt.Errorf("goal artifact was not saved")
	}

	thread := scoutAgentThread{ID: goalID, Mode: "goal", Query: objective, Status: "running", Artifact: artifact}
	broadcastSignedInKanbanEvent("memory", app.memorySnapshotForClients(20))
	broadcastAssistantEvent("action", "Goal thread launched", map[string]any{
		"tool":       "launch_goal_thread",
		"thread":     thread,
		"artifact":   artifact,
		"voiceState": "listening",
	})

	startGoalThreadAsync(app, artifact.ID)
	return thread, nil
}

func buildGoalScaffold(plan goalPlan) string {
	return strings.Join([]string{
		"Goal execution thread",
		"",
		"Vision: " + compactAssistantLine(plan.Objective),
		"Status: running",
		"Authority: " + plan.Authority,
		"",
		"Execution log",
		"- Scout created the goal artifact and started the execution engine.",
		"- The engine decomposes the goal, executes subtasks in order, reviews against the goal, gates before shipping, then verifies.",
		"- This artifact updates at every state transition.",
	}, "\n")
}

// startGoalThreadAsync mirrors startAgentThreadAsync: assigned in init so tests
// can swap it to drive the engine synchronously (or simulate a child fold).
var startGoalThreadAsync = func(app *kanbanBoardApp, parentID string) {
	go app.runGoalThread(parentID)
}

// runGoalThread loads the plan under the parent lock and drives it. The lock is
// held for the whole drive so a child completing mid-dispatch folds only after
// the driver has persisted the plan it dispatched.
func (app *kanbanBoardApp) runGoalThread(parentID string) {
	lock := goalEngineLock(parentID)
	lock.Lock()
	defer lock.Unlock()
	app.driveGoalLocked(parentID)
}

func (app *kanbanBoardApp) driveGoalLocked(parentID string) {
	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return
	}
	engine := newGoalEngine(app)
	ctx, cancel := context.WithTimeout(context.Background(), engine.timeout)
	defer cancel()
	engine.drive(ctx, &plan, parentID)
}

// --- The transition engine ---------------------------------------------------

// drive advances the plan from its current state, persisting after every
// transition, until it reaches a terminal state, an approval stop, or a wait on
// in-flight children. The caller must hold goalEngineLock(parentID).
func (e *goalEngine) drive(ctx context.Context, plan *goalPlan, parentID string) {
	for iteration := 0; iteration < goalDriveIterationCap; iteration++ {
		switch plan.State {
		case goalStateIdentify:
			plan.State = goalStateDecompose
			e.persist(plan, parentID, "")

		case goalStateDecompose:
			if err := e.decompose(ctx, plan); err != nil {
				plan.DecomposeAttempts++
				if plan.DecomposeAttempts >= goalMaxDecomposeTries {
					e.fail(plan, parentID, "decomposition failed: "+err.Error())
					return
				}
				e.persist(plan, parentID, "")
				continue // retry decompose
			}
			plan.State = goalStateAssign
			e.persist(plan, parentID, "")

		case goalStateAssign:
			assignGoalRunners(plan)
			plan.State = goalStateCoordinate
			e.persist(plan, parentID, "")

		case goalStateCoordinate:
			recomputeGoalReadiness(plan)
			plan.State = goalStateExecute
			e.persist(plan, parentID, "")

		case goalStateExecute:
			recomputeGoalReadiness(plan)
			e.dispatchReady(plan, parentID)
			if goalAllComplete(plan) {
				plan.State = goalStateReview
				e.persist(plan, parentID, "")
				continue
			}
			if goalAnyRunning(plan) {
				// Wait: each in-flight child folds back into the plan on
				// completion (foldGoalChildCompletion) and re-drives from here.
				e.persist(plan, parentID, "")
				return
			}
			// No running children and not all complete: the remaining subtasks
			// are failed/blocked (or their deps are). Let review decide retry vs
			// block rather than stalling silently.
			plan.State = goalStateReview
			e.persist(plan, parentID, "")

		case goalStateReview:
			switch e.reviewSubtasks(ctx, plan) {
			case goalReviewOutcomeRequeue:
				plan.State = goalStateExecute
				e.persist(plan, parentID, "")
			case goalReviewOutcomeBlocked:
				e.fail(plan, parentID, goalBlockerLine(plan))
				return
			default: // proceed
				plan.State = goalStateGate
				e.persist(plan, parentID, "")
			}

		case goalStateGate:
			e.gate(ctx, plan)
			if plan.Gate.ApprovalRequired {
				plan.State = goalStateApproval
				e.persistApprovalRequired(plan, parentID)
				return
			}
			if plan.Gate.Status == subtaskBlocked {
				e.fail(plan, parentID, "ship gate blocked: "+plan.Gate.Reason)
				return
			}
			plan.State = goalStateSave
			e.persist(plan, parentID, "")

		case goalStateSave:
			e.saveWhatWorked(plan, parentID)
			plan.State = goalStateReport
			e.persist(plan, parentID, "")

		case goalStateReport:
			e.report(ctx, plan)
			plan.State = goalStateVerify
			e.persist(plan, parentID, composeGoalArtifact(plan))

		case goalStateVerify:
			if e.verify(ctx, plan) {
				plan.State = goalStateVerified
				plan.Verification.Verdict = goalReviewPass
			} else {
				plan.State = goalStateBlocked
				plan.Verification.Verdict = goalReviewFail
				plan.Blocker = firstNonEmptyString(plan.Verification.Reasons, "verification did not confirm the goal")
			}
			plan.Verification.CheckedAt = e.now().UTC().Format(time.RFC3339Nano)
			e.finish(plan, parentID)
			return

		case goalStateCommit:
			// Reached only via resumeApprovedGoal after an admin approval flips
			// the gate. Enqueue the single external_write sidecar job the gate
			// recorded; the codex callback lands the terminal state.
			e.enqueueCommitPush(plan, parentID)
			return

		default:
			// verified / needs_attention / approval_required: terminal or waiting.
			return
		}
	}
	e.fail(plan, parentID, "goal engine exceeded its transition cap")
}

// --- Stage: decompose --------------------------------------------------------

func (e *goalEngine) decompose(ctx context.Context, plan *goalPlan) error {
	system := strings.Join([]string{
		"You are Scout's goal decomposer for Bonfire OS. Break the goal into an ordered plan of independent subtasks.",
		fmt.Sprintf("Return STRICT JSON only, no prose: {\"subtasks\":[{\"id\":\"st-1\",\"title\":\"...\",\"detail\":\"...\",\"mode\":\"research|design|grill|workflow|artifacts\",\"authority\":\"read_only|workspace_write\",\"dependsOn\":[]}]}."),
		fmt.Sprintf("Use at most %d subtasks — coarsen aggressively; this is a small team on one server, not a swarm.", goalMaxSubtasks),
		"Each subtask must have a unique id like st-1, a real mode, and dependsOn referencing only earlier subtask ids (no cycles). Prefer read_only unless the subtask must change the board, memory, or a package.",
		"Do not include any external_write (commit, push, deploy, email, production) work as a subtask; that is gated separately at ship time.",
	}, "\n")
	user := "Goal: " + plan.Objective + "\nRequested by: " + firstNonEmptyString(plan.CreatedBy, "the room") + "\nAuthority: " + plan.Authority
	// A tool-templated goal hands the decomposer the tool's full A++ prompt: the
	// master wrapper (grounded in Bonfire's own record) with the tool body and
	// exact output contract, so the plan's terminal subtask produces that
	// contract. The last subtask must emit the tool's exact output headings.
	if tool, ok := e.resolvedTool(plan); ok {
		prompt := assembleToolPrompt(tool, e.toolPromptContextForPlan(plan, tool))
		user += "\n\nThis goal runs the \"" + tool.Name + "\" tool. Decompose so the FINAL subtask produces its output contract exactly. The tool's full instructions and output contract:\n" + prompt
	}
	if plan.DecomposeAttempts > 0 {
		user += "\n\nYour previous plan was rejected as invalid JSON or schema. Return only the JSON object described."
	}

	text, err := e.callModel(ctx, system, user)
	if err != nil {
		return err
	}
	var decoded struct {
		Subtasks []goalSubtask `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &decoded); err != nil {
		return fmt.Errorf("malformed decompose JSON: %w", err)
	}
	for index := range decoded.Subtasks {
		st := &decoded.Subtasks[index]
		st.ID = strings.TrimSpace(st.ID)
		st.Mode = normalizeAgentThreadMode(st.Mode)
		st.Authority = normalizeCodexJobAuthority(st.Authority)
		st.Status = subtaskPending
		if st.DependsOn == nil {
			st.DependsOn = []string{}
		}
	}
	candidate := *plan
	candidate.Subtasks = decoded.Subtasks
	if err := validateGoalPlan(&candidate); err != nil {
		return err
	}
	plan.Subtasks = candidate.Subtasks
	return nil
}

// --- Stage: assign (pure, re-derivable on restart) ---------------------------

// assignGoalRunners chooses each subtask's runner by capability match: a
// shell/repo subtask (its mode or text implies it) goes to the execution
// runner; everything else to the orchestrator. Concrete runner names are
// stored so selectAgentRunner can honor them without a second mapping.
func assignGoalRunners(plan *goalPlan) {
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if goalSubtaskNeedsExecution(st) {
			st.Runner = selectedExecutionRunnerName()
		} else {
			st.Runner = selectedAgentRunnerName()
		}
	}
}

func goalSubtaskNeedsExecution(st *goalSubtask) bool {
	lower := strings.ToLower(st.Title + " " + st.Detail)
	return hasAssistantPhrase(lower,
		"run the tests", "run tests", "edit the repo", "write code", "implement",
		"build the app", "test the app", "change files", "shell", "git ", "compile",
		"run the build", "apply the patch")
}

// --- Stage: execute (topological dispatch, concurrency cap) ------------------

func recomputeGoalReadiness(plan *goalPlan) {
	complete := map[string]bool{}
	for index := range plan.Subtasks {
		if plan.Subtasks[index].Status == subtaskComplete {
			complete[plan.Subtasks[index].ID] = true
		}
	}
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if st.Status != subtaskPending {
			continue
		}
		ready := true
		for _, dep := range st.DependsOn {
			if !complete[strings.TrimSpace(dep)] {
				ready = false
				break
			}
		}
		if ready {
			st.Status = subtaskReady
		}
	}
}

// dispatchReady launches ready subtasks as child agent threads up to the
// concurrency cap. The caller holds the parent lock, so a child's fold cannot
// interleave; the child goroutine blocks on that lock until the driver returns.
func (e *goalEngine) dispatchReady(plan *goalPlan, parentID string) {
	// A cancelled goal never dispatches another subtask — the whole point of the
	// one-tap cancel (spec §2 misfire economics): a wrong launch costs the work
	// already in flight, never the rest of the plan.
	if plan.Cancelled {
		return
	}
	running := goalCountStatus(plan, subtaskRunning)
	for index := range plan.Subtasks {
		if running >= e.concurrency {
			return
		}
		st := &plan.Subtasks[index]
		if st.Status != subtaskReady {
			continue
		}
		st.Status = subtaskRunning
		st.Attempts++
		if err := e.launchSubtask(plan, st, parentID); err != nil {
			log.Errorf("goal %s subtask %s launch failed: %v", parentID, st.ID, err)
			st.Status = subtaskFailed
			continue
		}
		running++
	}
}

func (e *goalEngine) launchSubtask(plan *goalPlan, st *goalSubtask, parentID string) error {
	query := st.Title
	if strings.TrimSpace(st.Detail) != "" {
		query += " — " + st.Detail
	}
	if st.Revisions > 0 && st.Review != nil && strings.TrimSpace(st.Review.Reasons) != "" {
		query += "\n\nRevision notes from the goal review (address these): " + st.Review.Reasons
	}
	// The protect list rides every requeue so a revision fixes what failed
	// WITHOUT regressing what the reviewer already praised (Phase 1 protect
	// lists — the classic revision failure mode is losing the good parts).
	if st.Revisions > 0 && len(st.Protect) > 0 {
		query += "\n\nDO NOT LOSE (protected) — the review explicitly praised these; keep every one intact in the revision:\n- " + strings.Join(st.Protect, "\n- ")
	}
	spec := agentThreadGoalSpec{
		Objective:      query,
		RequestedBy:    plan.CreatedBy,
		Authority:      goalChildAuthority(st.Authority, plan.Authority),
		ParentGoalID:   parentID,
		SubtaskID:      st.ID,
		AssignedRunner: st.Runner,
	}
	// The deliverable-producing subtask carries the tool template so the model
	// that actually WRITES the artifact receives the tool's full A++ prompt
	// (role, evidence discipline, exact output contract, gate rubric) — the
	// wrapper is the quality lever only if it reaches generation, not just the
	// decomposer. Upstream subtasks (research feeding a one-pager) keep the
	// generic per-mode contract so they don't each try to emit the deliverable.
	if st.ID == goalDeliverableSubtaskID(plan) {
		spec.ToolTemplate = plan.ToolTemplate
		// Mark it the deliverable so the runner gives its generation a heavier
		// effort + token budget (agent_runner_anthropic.go) — the fix for the
		// contract-bearing artifact truncating under the planning default.
		spec.Deliverable = true
	}
	// Children deliver back through the fold + creator notification, not a room
	// origin, so no origin metadata is stamped on the subtask thread.
	thread, err := e.app.launchAgentThreadWithSpec(st.Mode, query, plan.CreatedBy, nil, spec)
	if err != nil {
		return err
	}
	st.ThreadID = thread.ID
	st.ArtifactID = thread.Artifact.ID
	return nil
}

// goalDeliverableSubtaskID picks the subtask that produces the goal's final
// deliverable — the one whose generation should receive the tool template.
// Rule: among sinks (subtasks nothing else depends on), prefer one whose mode
// matches the tool's base mode; otherwise the last sink in plan order. Returns
// "" when the plan carries no resolvable tool (nothing is stamped). Deterministic
// so a boot-time re-dispatch stamps the same subtask.
func goalDeliverableSubtaskID(plan *goalPlan) string {
	tool, ok := toolByID(plan.ToolTemplate)
	if !ok || len(plan.Subtasks) == 0 {
		return ""
	}
	hasDependent := map[string]bool{}
	for index := range plan.Subtasks {
		for _, dep := range plan.Subtasks[index].DependsOn {
			hasDependent[strings.TrimSpace(dep)] = true
		}
	}
	lastSink := ""
	modeSink := ""
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if hasDependent[st.ID] {
			continue
		}
		lastSink = st.ID // plan order; the later sink wins ties
		if normalizeAgentThreadMode(st.Mode) == normalizeAgentThreadMode(tool.Mode) {
			modeSink = st.ID
		}
	}
	if modeSink != "" {
		return modeSink
	}
	if lastSink != "" {
		return lastSink
	}
	// No sink (shouldn't happen for an acyclic plan): fall back to the last subtask.
	return plan.Subtasks[len(plan.Subtasks)-1].ID
}

// goalChildAuthority clamps a child subtask's authority to the LESSER of its own
// and the parent goal's authority, and never above workspace_write. Two
// invariants in one: (1) external_write is gated at ship time, never executed
// inline by a subtask — the structural half of "no external_write without
// approval"; (2) a subtask can never out-privilege the goal that spawned it
// (a read_only goal cannot dispatch a workspace_write subtask, whatever the
// decomposer proposed). This authority flows to the in-process orchestrator
// child's system prompt; codex-sidecar children additionally re-derive their own
// authority from text (codexJobAuthorityForThread) — reconciling those two
// computations so the sidecar honors this clamp is the Wave-6 handoff.
func goalChildAuthority(subtaskAuthority string, parentAuthority string) string {
	rank := goalAuthorityRank(subtaskAuthority)
	if parentRank := goalAuthorityRank(parentAuthority); parentRank < rank {
		rank = parentRank
	}
	if rank >= goalAuthorityRankExternal {
		rank = goalAuthorityRankWorkspace // never external_write for a child
	}
	if rank <= goalAuthorityRankReadOnly {
		return codexJobAuthorityReadOnly
	}
	return codexJobAuthorityWorkspaceWrite
}

const (
	goalAuthorityRankReadOnly  = 0
	goalAuthorityRankWorkspace = 1
	goalAuthorityRankExternal  = 2
)

func goalAuthorityRank(authority string) int {
	switch normalizeCodexJobAuthority(authority) {
	case codexJobAuthorityReadOnly:
		return goalAuthorityRankReadOnly
	case codexJobAuthorityExternalWrite:
		return goalAuthorityRankExternal
	default:
		return goalAuthorityRankWorkspace
	}
}

// foldGoalChildAsync runs a child fold off the caller's goroutine. The codex
// HTTP callback uses it so a re-drive (which may make model calls) never blocks
// the callback response. Assigned as a var, mirroring startGoalThreadAsync, so
// tests can make it synchronous for deterministic, leak-free assertions.
var foldGoalChildAsync = func(app *kanbanBoardApp, parentID string, subtaskID string, child meetingMemoryEntry, status string) {
	go app.foldGoalChildCompletion(parentID, subtaskID, child, status)
}

// foldGoalChildCompletion is called from the child thread's terminal seam
// (runAgentThread) when the child carries goalParentId. It folds the child
// result into the parent plan and re-drives the engine. Idempotent: a subtask
// already off `running` (a duplicate/late callback, or a restart re-fold) is a
// no-op.
func (app *kanbanBoardApp) foldGoalChildCompletion(parentID string, subtaskID string, child meetingMemoryEntry, status string) {
	parentID = strings.TrimSpace(parentID)
	subtaskID = strings.TrimSpace(subtaskID)
	if parentID == "" || subtaskID == "" {
		return
	}
	lock := goalEngineLock(parentID)
	lock.Lock()
	defer lock.Unlock()

	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return
	}
	// A cancelled parent folds nothing: a child already in flight finishes on
	// its own (no preemption seam reaches into a child goroutine or a claimed
	// sidecar job), but its completion must not mutate the plan or re-drive the
	// engine — the goal is terminal needs_attention with the cancel record.
	if plan.Cancelled {
		return
	}
	complete := strings.EqualFold(strings.TrimSpace(status), codexJobStatusComplete)
	engine := newGoalEngine(app)

	// The single external_write commit_push child folds straight to the terminal
	// state; it is not a real subtask. Idempotent: only folds while the goal is
	// actually parked at commit_push (a retried/late callback is a no-op).
	if subtaskID == goalCommitSubtaskID {
		if plan.State != goalStateCommit {
			return
		}
		childStatus := subtaskFailed
		if complete {
			childStatus = subtaskComplete
		}
		engine.foldCommitResult(&plan, parentID, childStatus)
		return
	}

	st := plan.subtaskByID(subtaskID)
	if st == nil || st.Status != subtaskRunning {
		return
	}
	st.ArtifactID = firstNonEmptyString(child.ID, st.ArtifactID)
	if complete {
		st.Status = subtaskComplete
	} else {
		st.Status = subtaskFailed
		if st.Review == nil {
			st.Review = &goalSubtaskReview{Verdict: goalReviewFail, Reasons: "subtask worker returned an error", By: "worker"}
		}
	}

	engine.persist(&plan, parentID, "")
	ctx, cancel := context.WithTimeout(context.Background(), engine.timeout)
	defer cancel()
	engine.drive(ctx, &plan, parentID)
}

// --- Panel primitive (spec §3 "The abstraction", Wave 3 item 12) --------------
//
// A panel is N parallel persona calls plus ONE synthesis call, run as goroutine
// fan-out INSIDE a single engine step — never as engine subtasks — so the DAG
// stays coarse and goalMaxSubtasks stays sane. One primitive covers red-team
// quartets, judge trios, slide juries, and the typographer/story-editor pair.

// goalPanelPersona is one seat on the panel: a name the synthesis (and any
// re-review gate) can address, and the persona's own system prompt.
type goalPanelPersona struct {
	Name   string
	System string
}

// goalPanelSpec configures one panel step. Every persona receives the SAME
// task (user prompt) and the SAME strict-JSON schema appended to its own
// system prompt; the synthesis call then reads all N replies.
type goalPanelSpec struct {
	Task      string
	Schema    string
	Personas  []goalPanelPersona
	Synthesis string // synthesis system prompt; "" falls back to the default
}

// goalPanelVoice is one persona's raw reply (strict JSON by contract). A
// failed call keeps its seat with Err set so the synthesis prompt can say so
// honestly instead of silently shrinking the panel.
type goalPanelVoice struct {
	Persona string
	Text    string
	Err     error
}

type goalPanelOutcome struct {
	Voices    []goalPanelVoice
	Synthesis string
}

const goalPanelDefaultSynthesisSystem = "You are Scout's panel synthesizer for Bonfire OS. Read every panelist's reply below and synthesize them into one decisive result per the task's instructions. Weigh agreement between panelists heavily; name genuine disagreement instead of averaging it away."

// runGoalPanel fans the personas out in parallel (each with its per-persona
// system prompt + the shared strict-JSON schema), waits for all of them, then
// makes one synthesis call that sees all N replies. Degrades per-seat: a
// failed persona call is reported to the synthesizer; only a panel where
// EVERY seat failed (or the synthesis itself fails) returns an error.
func (e *goalEngine) runGoalPanel(ctx context.Context, spec goalPanelSpec) (goalPanelOutcome, error) {
	if len(spec.Personas) == 0 {
		return goalPanelOutcome{}, fmt.Errorf("panel needs at least one persona")
	}
	outcome := goalPanelOutcome{Voices: make([]goalPanelVoice, len(spec.Personas))}
	var wg sync.WaitGroup
	for index := range spec.Personas {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			persona := spec.Personas[index]
			system := strings.TrimSpace(persona.System)
			if schema := strings.TrimSpace(spec.Schema); schema != "" {
				system += "\n\n" + schema
			}
			text, err := e.callModel(ctx, system, spec.Task)
			outcome.Voices[index] = goalPanelVoice{Persona: persona.Name, Text: text, Err: err}
		}(index)
	}
	wg.Wait()

	answered := 0
	var replies strings.Builder
	for _, voice := range outcome.Voices {
		replies.WriteString("### Panelist: ")
		replies.WriteString(voice.Persona)
		replies.WriteByte('\n')
		if voice.Err != nil {
			replies.WriteString("(this panelist's call failed: " + compactAssistantLine(voice.Err.Error()) + ")\n\n")
			continue
		}
		answered++
		replies.WriteString(voice.Text)
		replies.WriteString("\n\n")
	}
	if answered == 0 {
		return outcome, fmt.Errorf("every panelist call failed")
	}

	synthesisSystem := firstNonEmptyString(strings.TrimSpace(spec.Synthesis), goalPanelDefaultSynthesisSystem)
	synthesis, err := e.callModel(ctx, synthesisSystem, spec.Task+"\n\nThe panel's replies:\n\n"+strings.TrimSpace(replies.String()))
	if err != nil {
		return outcome, fmt.Errorf("panel synthesis failed: %w", err)
	}
	outcome.Synthesis = strings.TrimSpace(synthesis)
	return outcome, nil
}

// --- Gate primitive (spec §3 "The abstraction", Wave 3 item 12) ---------------
//
// Threshold + per-dimension floor + bounded rounds + force-accept-with-
// disclosed-gaps, per the SKILL semantics the doc quotes (9.0 threshold, 7.0
// floor, max 2 rounds). Today's tool-rubric review is the DEGENERATE one-round
// verdict case; the grill re-review is the first dimensional consumer.

const (
	goalGateDefaultThreshold = 9.0
	goalGateDefaultFloor     = 7.0
	goalGateDefaultMaxRounds = 2
)

// Gate outcomes. accept ships; revise re-queues (rounds remain); blocked stops
// the line; force_accept_with_gaps is the SKILL escape hatch — rounds are
// spent, the spec allows shipping, and the gaps ride out DISCLOSED, never
// hidden.
const (
	goalGateOutcomeAccept      = "accept"
	goalGateOutcomeRevise      = "revise"
	goalGateOutcomeBlocked     = "blocked"
	goalGateOutcomeForceAccept = "force_accept_with_gaps"
)

// goalGateDimension is one scored rubric dimension; Gap names what closing it
// would take (disclosed verbatim on a force-accept).
type goalGateDimension struct {
	Name  string
	Score float64
	Gap   string
}

// goalGateRound is one scoring pass. A non-empty Verdict wins outright — the
// degenerate case, where the scorer (today's reviewer model against the tool
// rubric, or a law sweep) already folded its judgement into pass/fail/revise.
// With no Verdict, the threshold + floor policy scores the Dimensions.
type goalGateRound struct {
	Verdict    string
	Dimensions []goalGateDimension
	Reasons    string
	Score      float64
}

// goalGateSpec configures one gate evaluation. The engine is a durable
// round-at-a-time state machine, so the gate evaluates the CURRENT round and
// returns the decision; the caller owns the mutation a revise implies
// (requeueOrBlock for subtasks, the readiness hold for the grill loop).
type goalGateSpec struct {
	Threshold   float64 // <=0 -> 9.0
	Floor       float64 // <=0 -> 7.0
	MaxRounds   int     // <=0 -> 2
	Round       int     // revision rounds already spent
	ForceAccept bool    // rounds spent: accept with disclosed gaps instead of blocking
	Score       func(ctx context.Context) goalGateRound
}

type goalGateDecision struct {
	Outcome string
	Verdict string // pass|fail|revise, for callers that persist the verdict vocabulary
	Reasons string
	Score   float64
	Gaps    []string
}

// runGoalGate runs one scoring pass and decides: accept when the round passes
// (verdict pass, or average >= threshold AND every dimension >= floor); revise
// while rounds remain; then force-accept with the gaps disclosed when the spec
// allows it, else blocked.
func runGoalGate(ctx context.Context, spec goalGateSpec) goalGateDecision {
	threshold := spec.Threshold
	if threshold <= 0 {
		threshold = goalGateDefaultThreshold
	}
	floor := spec.Floor
	if floor <= 0 {
		floor = goalGateDefaultFloor
	}
	maxRounds := spec.MaxRounds
	if maxRounds <= 0 {
		maxRounds = goalGateDefaultMaxRounds
	}

	round := goalGateRound{}
	if spec.Score != nil {
		round = spec.Score(ctx)
	}

	verdict := strings.ToLower(strings.TrimSpace(round.Verdict))
	reasons := strings.TrimSpace(round.Reasons)
	score := round.Score
	var gaps []string
	passed := false
	switch {
	case verdict == goalReviewPass:
		passed = true
	case verdict == goalReviewFail || verdict == goalReviewRevise:
		if reasons != "" {
			gaps = append(gaps, reasons)
		}
	case len(round.Dimensions) == 0:
		verdict = goalReviewRevise
		gaps = append(gaps, "the gate round returned no verdict and no dimension scores")
	default:
		sum := 0.0
		for _, dimension := range round.Dimensions {
			sum += dimension.Score
			if dimension.Score < floor {
				gap := fmt.Sprintf("%s scored %.1f, below the %.1f floor", dimension.Name, dimension.Score, floor)
				if detail := strings.TrimSpace(dimension.Gap); detail != "" {
					gap += " — " + detail
				}
				gaps = append(gaps, gap)
			}
		}
		average := sum / float64(len(round.Dimensions))
		if score == 0 {
			score = average
		}
		if average < threshold {
			gaps = append(gaps, fmt.Sprintf("average %.1f is below the %.1f threshold", average, threshold))
		}
		passed = len(gaps) == 0
		if passed {
			verdict = goalReviewPass
		} else {
			verdict = goalReviewRevise
		}
	}

	decision := goalGateDecision{Verdict: verdict, Reasons: reasons, Score: score, Gaps: gaps}
	switch {
	case passed:
		decision.Outcome = goalGateOutcomeAccept
	case spec.Round < maxRounds:
		decision.Outcome = goalGateOutcomeRevise
	case spec.ForceAccept:
		decision.Outcome = goalGateOutcomeForceAccept
	default:
		decision.Outcome = goalGateOutcomeBlocked
	}
	return decision
}

// --- Stage: review_against_goal ---------------------------------------------

type goalReviewOutcome int

const (
	goalReviewOutcomeProceed goalReviewOutcome = iota
	goalReviewOutcomeRequeue
	goalReviewOutcomeBlocked
)

// reviewSubtasks is a model call per not-yet-passed subtask. fail/revise (or a
// worker error) re-queues the subtask with the review notes, bounded to
// goalMaxRevisions; then the subtask blocks and the whole goal blocks.
func (e *goalEngine) reviewSubtasks(ctx context.Context, plan *goalPlan) goalReviewOutcome {
	requeued := false
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if st.Status == subtaskBlocked {
			return goalReviewOutcomeBlocked
		}
		if st.Status == subtaskFailed {
			if !e.requeueOrBlock(plan, st, "the subtask worker returned an error") {
				return goalReviewOutcomeBlocked
			}
			requeued = true
			continue
		}
		if st.Status != subtaskComplete {
			continue
		}
		if st.Review != nil && st.Review.Verdict == goalReviewPass {
			continue
		}
		verdict, reasons, score := e.reviewOneSubtask(ctx, plan, st)
		// A law-sweep verdict is mechanical (a grep, not a judgement); stamp its
		// provenance honestly so the card never claims a model reviewed it.
		reviewedBy := "reviewer_model"
		if strings.HasPrefix(reasons, toolLawSweepPrefix) {
			reviewedBy = "law_sweep"
		}
		if verdict == goalReviewPass {
			st.Review = &goalSubtaskReview{Verdict: goalReviewPass, Score: score, Reasons: reasons, By: reviewedBy}
			continue
		}
		st.Review = &goalSubtaskReview{Verdict: verdict, Score: score, Reasons: reasons, By: reviewedBy}
		if !e.requeueOrBlock(plan, st, reasons) {
			return goalReviewOutcomeBlocked
		}
		requeued = true
	}
	if requeued {
		return goalReviewOutcomeRequeue
	}
	if !goalAllComplete(plan) {
		// Nothing to re-queue and not everything completed: a dependency is
		// stranded behind a blocked/failed subtask.
		return goalReviewOutcomeBlocked
	}
	return goalReviewOutcomeProceed
}

// requeueOrBlock bumps a subtask's revision count and re-queues it (ready)
// unless the revision bound is spent, in which case it blocks. Returns false
// when the subtask (and thus the goal) is blocked.
func (e *goalEngine) requeueOrBlock(plan *goalPlan, st *goalSubtask, reason string) bool {
	if st.Revisions >= goalMaxRevisions {
		st.Status = subtaskBlocked
		plan.Blocker = fmt.Sprintf("subtask %q blocked after %d revisions: %s", st.ID, st.Revisions, compactAssistantLine(reason))
		return false
	}
	st.Revisions++
	st.Status = subtaskReady
	return true
}

// goalProtectListCap bounds the accumulated protect list so a chatty reviewer
// cannot grow the requeue prompt without bound across revision rounds.
const goalProtectListCap = 8

// mergeGoalProtectList folds a reviewer's strengths_to_keep into the subtask's
// inherited protect list: trimmed, deduplicated case-insensitively, first-seen
// order, capped at goalProtectListCap. Earlier rounds' praise always survives a
// later round (existing entries win the cap).
func mergeGoalProtectList(existing []string, incoming []string) []string {
	merged := make([]string, 0, len(existing)+len(incoming))
	seen := make(map[string]bool, len(existing)+len(incoming))
	for _, group := range [][]string{existing, incoming} {
		for _, item := range group {
			item = strings.TrimSpace(item)
			key := strings.ToLower(item)
			if item == "" || seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, item)
			if len(merged) >= goalProtectListCap {
				return merged
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// goalReviewArtifactCap bounds how much artifact body the reviewer and the
// ship gate read per prompt. 48KB is far beyond any honest deliverable; the
// cap only exists so a runaway artifact cannot blow the review context.
const goalReviewArtifactCap = 48 * 1024

// goalReviewArtifactBody returns the FULL artifact text for the reviewer/gate
// prompts — the reviewer judges the work itself, never a flattened thumbnail
// (compactAssistantLine stays the voice of progress/log lines only). Oversized
// bodies keep their head and tail with the truncation announced inline so the
// model knows the middle is missing rather than silently absent.
func goalReviewArtifactBody(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= goalReviewArtifactCap {
		return text
	}
	half := goalReviewArtifactCap / 2
	omitted := len(text) - 2*half
	return text[:half] +
		fmt.Sprintf("\n\n[... artifact truncated for review: %d bytes omitted from the middle ...]\n\n", omitted) +
		text[len(text)-half:]
}

// reviewOneSubtask judges one completed subtask THROUGH the gate primitive:
// the tool-rubric review is the degenerate one-round case (spec §3 — "today's
// toolRubric becomes the degenerate 1-dimension case"), a single scorer (law
// sweep first, then the reviewer model) whose folded verdict decides, with
// rounds bounded by goalMaxRevisions. The returned triple is unchanged and
// requeueOrBlock still applies the plan mutation, so observable behavior is
// identical to the pre-primitive review.
func (e *goalEngine) reviewOneSubtask(ctx context.Context, plan *goalPlan, st *goalSubtask) (string, string, float64) {
	decision := runGoalGate(ctx, goalGateSpec{
		MaxRounds: goalMaxRevisions,
		Round:     st.Revisions,
		Score: func(ctx context.Context) goalGateRound {
			return e.scoreSubtaskAgainstRubric(ctx, plan, st)
		},
	})
	return decision.Verdict, decision.Reasons, decision.Score
}

// scoreSubtaskAgainstRubric is the review's one scoring pass: the zero-cost
// law sweep, then the reviewer model against the tool rubric, folded into a
// verdict-driven gate round.
func (e *goalEngine) scoreSubtaskAgainstRubric(ctx context.Context, plan *goalPlan, st *goalSubtask) goalGateRound {
	full := ""
	if artifact, ok := e.app.osArtifactByID(st.ArtifactID); ok {
		full = artifact.Text
	}
	// LAW SWEEP (zero model cost): the deliverable subtask of a tool-templated
	// goal is grep-checked against its contract before any reviewer tokens are
	// spent — a missing contract heading or a copy-law breach (em dash on a
	// client-facing contract) short-circuits straight to a mechanical revise
	// verdict. Swept on the FULL body, never the truncated review view, so an
	// oversized artifact's omitted middle cannot fake a missing heading.
	if tool, ok := e.resolvedTool(plan); ok && st.ID == goalDeliverableSubtaskID(plan) {
		if reason, violated := toolLawSweep(tool, full); violated {
			return goalGateRound{Verdict: goalReviewRevise, Reasons: reason}
		}
	}
	produced := goalReviewArtifactBody(full)
	system := "You are Scout's reviewer for Bonfire OS. Judge whether a subtask's produced artifact actually satisfies the subtask against the overall goal. Return STRICT JSON only: {\"verdict\":\"pass|fail|revise\",\"score\":0-10,\"reasons\":\"one line\",\"strengths_to_keep\":[\"...\"]}. strengths_to_keep names what the work already does WELL (0-4 short phrases of explicit praise) so a revision never loses it; leave it empty if nothing stands out."
	// For a tool-templated goal, the review scores against the tool's gate rubric
	// (dimensions + bars + kill condition) rather than a generic "does it match"
	// pass — the studio-grade quality bar for this contract.
	if tool, ok := e.resolvedTool(plan); ok {
		system += "\n\n" + toolReviewInstruction(tool)
	}
	user := "Overall goal: " + plan.Objective + "\nSubtask: " + st.Title
	if strings.TrimSpace(st.Detail) != "" {
		user += " — " + st.Detail
	}
	user += "\nProduced artifact:\n" + firstNonEmptyString(produced, "(the subtask produced no artifact text)")
	text, err := e.callReviewModel(ctx, system, user)
	if err != nil {
		// A reviewer error is a soft fail: re-queue rather than silently pass.
		return goalGateRound{Verdict: goalReviewRevise, Reasons: "reviewer model call failed: " + err.Error()}
	}
	var decoded struct {
		Verdict   string   `json:"verdict"`
		Score     float64  `json:"score"`
		Reasons   string   `json:"reasons"`
		Strengths []string `json:"strengths_to_keep"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &decoded); err != nil {
		return goalGateRound{Verdict: goalReviewRevise, Reasons: "reviewer returned malformed JSON"}
	}
	// Fold the reviewer's explicit praise into the subtask's protect list. The
	// merge is cumulative across rounds (persisted with the plan), so a round-2
	// reviewer cannot silently drop what round 1 already protected.
	st.Protect = mergeGoalProtectList(st.Protect, decoded.Strengths)
	round := goalGateRound{Reasons: strings.TrimSpace(decoded.Reasons), Score: decoded.Score}
	switch strings.ToLower(strings.TrimSpace(decoded.Verdict)) {
	case goalReviewPass:
		round.Verdict = goalReviewPass
	case goalReviewFail:
		round.Verdict = goalReviewFail
	default:
		round.Verdict = goalReviewRevise
	}
	return round
}

// --- Stage: gate_before_shipping --------------------------------------------

// gate is a cheaper model call answering "is this safe and complete to ship?".
// An external_write goal (or a gate that flags external write) forces
// approval_required and the engine stops — no code path lets the orchestrator
// self-approve an external write.
func (e *goalEngine) gate(ctx context.Context, plan *goalPlan) {
	system := "You are Scout's ship gate for Bonfire OS. Answer one question: is the work safe and complete to publish/deliver? Return STRICT JSON only: {\"safe\":true|false,\"external_write_required\":true|false,\"command\":\"\",\"reason\":\"one line\"}. Set external_write_required true only if shipping needs a commit, push, deploy, email, or other production side effect."
	tool, hasTool := e.resolvedTool(plan)
	if hasTool {
		// The ship gate also runs the tool's kill condition: a triggered kill
		// condition is not safe to ship regardless of completeness.
		system += "\n\n" + toolReviewInstruction(tool) + "\nIf the kill condition is triggered, set safe=false."
	}
	user := "Goal: " + plan.Objective + "\nAuthority: " + plan.Authority + "\nSubtasks:\n" + goalSubtaskSummary(plan) +
		"\nProduced artifacts (judge the actual work, not the status list):\n" + e.gateArtifactSection(plan)
	text, err := e.callReviewModel(ctx, system, user)

	plan.Gate.ReviewedBy = "gate_model"
	if err != nil {
		plan.Gate.Status = subtaskBlocked
		plan.Gate.Reason = "gate model call failed: " + err.Error()
		return
	}
	var decoded struct {
		Safe                  bool   `json:"safe"`
		ExternalWriteRequired bool   `json:"external_write_required"`
		Command               string `json:"command"`
		Reason                string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &decoded); err != nil {
		plan.Gate.Status = subtaskBlocked
		plan.Gate.Reason = "gate returned malformed JSON"
		return
	}
	plan.Gate.Reason = strings.TrimSpace(decoded.Reason)
	plan.Gate.Command = strings.TrimSpace(decoded.Command)

	// Authority, an external-write-gated tool (the memo/deal-room class whose
	// output crosses the building boundary), OR the gate's own read: any of the
	// three forces the human approval gate. external_write is earned here, never
	// self-granted.
	if plan.Authority == codexJobAuthorityExternalWrite || (hasTool && tool.ExternalWriteGated) || decoded.ExternalWriteRequired {
		plan.Gate.ApprovalRequired = true
		plan.Gate.Status = goalStateApproval
		if hasTool && tool.ExternalWriteGated && strings.TrimSpace(plan.Gate.Reason) == "" {
			plan.Gate.Reason = tool.Name + " leaves the building; it needs human approval before it can be sent."
		}
		return
	}
	if !decoded.Safe {
		plan.Gate.Status = subtaskBlocked
		return
	}
	plan.Gate.Status = "passed"
}

// gateArtifactSection assembles every subtask's full artifact body so the ship
// gate sees the work it is clearing, not a one-line summary per subtask. Each
// body is capped like a review body, and the combined section passes through
// the same cap once more so many large artifacts still cannot blow the context.
func (e *goalEngine) gateArtifactSection(plan *goalPlan) string {
	var builder strings.Builder
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		artifact, ok := e.app.osArtifactByID(st.ArtifactID)
		if !ok || strings.TrimSpace(artifact.Text) == "" {
			continue
		}
		builder.WriteString("### ")
		builder.WriteString(st.ID)
		builder.WriteString(" — ")
		builder.WriteString(st.Title)
		builder.WriteByte('\n')
		builder.WriteString(goalReviewArtifactBody(artifact.Text))
		builder.WriteString("\n\n")
	}
	if builder.Len() == 0 {
		return "(no artifact bodies were produced)"
	}
	return goalReviewArtifactBody(strings.TrimSpace(builder.String()))
}

// --- Stage: save_what_worked -------------------------------------------------

// signalEventGoalLessons: save_what_worked's distilled lessons from a goal
// that passed its gate — the Taste Analyst's positive-example feed. Defined
// beside its one emitter (saveWhatWorked below), like goal_cancelled.
const signalEventGoalLessons = "goal_lessons"

// goalLessonsMax caps save_what_worked's distilled lessons (spec: 2-4 one-line
// lessons; fewer when the run has less to teach, never more).
const goalLessonsMax = 4

// saveWhatWorked is the REAL save_what_worked stage (Wave 3 items 12/15): it
// files the passing plan into its package (idempotent — the flywheel keeps the
// winning decomposition) AND distills 2-4 one-line lessons from the run —
// reviewer praise that survived revision (protect-list survivors), what needed
// revision before it passed, what the gate said when it cleared the work —
// into the plan (mirrored to metadata["savedLessons"] by persist) plus exactly
// ONE goal_lessons signal for the Taste Analyst. Zero model cost: the lessons
// are distilled mechanically from state the engine already holds, per the §5
// rule that tokens are spent at distillation, never at capture.
func (e *goalEngine) saveWhatWorked(plan *goalPlan, parentID string) {
	if plan.PackageID != "" {
		if _, err := e.app.attachToPackage(plan.PackageID, packageRefTypeArtifact, parentID, scoutParticipantName); err != nil {
			log.Errorf("goal %s attachToPackage %s failed: %v", parentID, plan.PackageID, err)
		}
	}
	lessons := distillGoalLessons(plan)
	if len(lessons) == 0 {
		return
	}
	plan.Report.SavedLessons = lessons
	payload := map[string]string{
		"lessons":   strings.Join(lessons, " | "),
		"objective": compactAssistantLine(plan.Objective),
	}
	if plan.ToolTemplate != "" {
		payload["toolTemplate"] = plan.ToolTemplate
	}
	// recordSignalEvent logs and continues; a signal write never fails the stage.
	e.app.recordSignalEvent(plan.CreatedBy, signalEventGoalLessons, signalValencePositive, parentID, plan.PackageID, payload)
}

// distillGoalLessons derives the lessons mechanically from the plan, in taste
// order: praise that survived (the protect lists), what needed revision, then
// what the ship gate cleared. Capped at goalLessonsMax; an uneventful run
// yields fewer, and a run with nothing to teach yields none.
func distillGoalLessons(plan *goalPlan) []string {
	var lessons []string
	add := func(line string) {
		line = compactAssistantLine(line)
		if line == "" || len(lessons) >= goalLessonsMax {
			return
		}
		lessons = append(lessons, line)
	}
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if len(st.Protect) == 0 {
			continue
		}
		add(fmt.Sprintf("Praised and kept on %q: %s", st.Title, strings.Join(st.Protect, "; ")))
	}
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if st.Revisions == 0 {
			continue
		}
		reason := ""
		if st.Review != nil {
			reason = strings.TrimSpace(st.Review.Reasons)
		}
		add(fmt.Sprintf("%q needed %d revision(s) before it passed — final review: %s", st.Title, st.Revisions, firstNonEmptyString(reason, "no reasons recorded")))
	}
	if reason := strings.TrimSpace(plan.Gate.Reason); reason != "" && plan.Gate.Status == "passed" {
		add("Gate cleared: " + reason)
	}
	return lessons
}

// salvageBlockedDeliverable rescues the best produced work of a goal that
// terminated needs_attention. When a subtask produced a real deliverable but the
// review/gate bar was missed and revisions ran out, the goal blocks — yet the
// produced artifact is genuinely useful (an 8/10 draft the studio can finish).
// Rather than orphan it, we attach it to the package (when set), surface it as
// the goal's result (deliverableArtifactId), and stamp an HONEST gap line naming
// what it missed. No gate bar is lowered: the goal is still needs_attention, but
// the work is saved, linked, and openable.
func (e *goalEngine) salvageBlockedDeliverable(plan *goalPlan, parentID string) {
	st := e.bestDeliverable(plan)
	if st == nil {
		return
	}
	artifactID := strings.TrimSpace(st.ArtifactID)
	if artifactID == "" {
		return
	}
	// A re-drive of an already-salvaged goal must not double-count the failure
	// signal below (the salvage itself is idempotent).
	alreadySalvaged := strings.TrimSpace(plan.Report.DeliverableArtifactID) != ""
	plan.Report.DeliverableArtifactID = artifactID
	gap := ""
	if st.Review != nil {
		gap = strings.TrimSpace(st.Review.Reasons)
	}
	if strings.TrimSpace(plan.Report.Gap) == "" {
		plan.Report.Gap = firstNonEmptyString(gap, "the deliverable missed the review bar")
	}
	// Point the blocker at the saved draft so the card's error line is a next
	// step, not a dead end. Idempotent across re-drives.
	if !strings.Contains(plan.Blocker, "draft is saved") {
		plan.Blocker = strings.TrimSpace(firstNonEmptyString(plan.Blocker, "goal needs attention")) +
			" — the best draft is saved and attached; finish it or retry."
	}
	if strings.TrimSpace(plan.PackageID) != "" {
		if _, err := e.app.attachToPackage(plan.PackageID, packageRefTypeArtifact, artifactID, scoutParticipantName); err != nil {
			log.Errorf("goal %s salvage attach %s failed: %v", parentID, artifactID, err)
		}
	}
	// Signal capture (spec §5 item 2): a salvage IS an agent failure worth
	// studying — the honest gap line names exactly which bar the draft missed.
	// Log-and-continue inside; never fails the salvage.
	if !alreadySalvaged {
		e.app.recordSignalEvent(plan.CreatedBy, signalEventArtifactSalvaged, signalValenceNegative, artifactID, plan.PackageID, map[string]string{
			"goalId":    parentID,
			"objective": plan.Objective,
			"gap":       plan.Report.Gap,
		})
	}
}

// bestDeliverable picks the subtask whose produced artifact is the goal's best
// salvageable work: the tool deliverable subtask when it produced substantial
// text, else the subtask with the largest produced artifact. Returns nil when no
// subtask produced anything substantial — a short stub or error body is never
// surfaced as a "draft to finish".
func (e *goalEngine) bestDeliverable(plan *goalPlan) *goalSubtask {
	const minSalvageLen = 400
	if id := goalDeliverableSubtaskID(plan); id != "" {
		if st := plan.subtaskByID(id); st != nil && e.producedArtifactLen(st) >= minSalvageLen {
			return st
		}
	}
	var best *goalSubtask
	bestLen := minSalvageLen - 1
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if n := e.producedArtifactLen(st); n > bestLen {
			bestLen = n
			best = st
		}
	}
	return best
}

func (e *goalEngine) producedArtifactLen(st *goalSubtask) int {
	id := strings.TrimSpace(st.ArtifactID)
	if id == "" {
		return 0
	}
	artifact, ok := e.app.osArtifactByID(id)
	if !ok {
		return 0
	}
	return len(strings.TrimSpace(artifact.Text))
}

// goalRevisionNote returns an honest "revising (attempt N of 2)" line while a
// re-queued subtask is back in flight (ready or running with a revision count),
// so the goal card can show a deliberate revision rather than a stall. Empty
// when no revision is in progress or the goal is terminal.
func goalRevisionNote(plan *goalPlan) string {
	if isTerminalGoalState(plan.State) {
		return ""
	}
	attempt := 0
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if st.Revisions > 0 && (st.Status == subtaskReady || st.Status == subtaskRunning) && st.Revisions > attempt {
			attempt = st.Revisions
		}
	}
	if attempt == 0 {
		return ""
	}
	return fmt.Sprintf("revising (attempt %d of %d)", attempt, goalMaxRevisions)
}

// --- Stage: report -----------------------------------------------------------

// report is one short model call producing the 4-line Changed/Headline/Gap/Next
// card plus the assumed-claim count the future return card will surface. Only
// the headline is meant to be spoken/notified; the detail lives in the artifact.
func (e *goalEngine) report(ctx context.Context, plan *goalPlan) {
	system := "You are Scout reporting a finished goal for Bonfire OS. Report only what matters. Return STRICT JSON only: {\"changed\":\"one line\",\"headline\":\"one line\",\"gap\":\"one line or empty\",\"next\":\"one line or empty\",\"assumed_claim_count\":0,\"decision\":\"\"}. assumed_claim_count is how many claims in the work are assumptions not backed by a produced artifact. decision is the ONE concrete decision this goal explicitly established (a price, an attach/no-attach, a go/no-go) that the team should be held to later — leave it empty unless the work clearly settled one; never invent a decision."
	user := "Goal: " + plan.Objective + "\nSubtasks:\n" + goalSubtaskSummary(plan) + "\nGate: " + plan.Gate.Status
	text, err := e.callModel(ctx, system, user)

	plan.Report.GateOutcome = plan.Gate.Status
	plan.Report.ArtifactIDs = goalArtifactIDs(plan)
	if err != nil {
		plan.Report.Headline = "Goal finished; report model call failed"
		return
	}
	var decoded struct {
		Changed           string `json:"changed"`
		Headline          string `json:"headline"`
		Gap               string `json:"gap"`
		Next              string `json:"next"`
		AssumedClaimCount int    `json:"assumed_claim_count"`
		Decision          string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &decoded); err != nil {
		plan.Report.Headline = compactAssistantLine(text)
		return
	}
	plan.Report.Changed = strings.TrimSpace(decoded.Changed)
	plan.Report.Headline = firstNonEmptyString(strings.TrimSpace(decoded.Headline), "Goal complete")
	plan.Report.Gap = strings.TrimSpace(decoded.Gap)
	plan.Report.Next = strings.TrimSpace(decoded.Next)
	plan.Report.AssumedClaimCount = decoded.AssumedClaimCount
	// Flywheel write #2 (design §4): a decision the goal explicitly established is
	// logged to the ledger, linked to the package, so the next tool's wrapper
	// pulls it as relevant_decisions and cannot contradict it.
	e.recordGoalDecision(plan, decoded.Decision)
}

// recordGoalDecision fires the decision-ledger flywheel write for a goal that
// settled one. It rides the existing appendDecision + attachToPackage seams the
// decision-ledger worker already uses, so the entry lands in decisionLedger
// snapshots and grounds the next tool. No package = nothing to link to = skip
// (the design's linkage requirement); an empty decision line is a no-op.
func (e *goalEngine) recordGoalDecision(plan *goalPlan, decision string) {
	decision = strings.TrimSpace(decision)
	if decision == "" || strings.TrimSpace(plan.PackageID) == "" || e.app == nil || e.app.memory == nil {
		return
	}
	id := fmt.Sprintf("goal-decision-%d", e.app.nowUnixNano())
	entry, ok, err := e.app.memory.appendDecision(id, decision, map[string]string{
		"packageId": plan.PackageID,
		"source":    "goal_completion",
		"goalId":    plan.GoalID,
	})
	if err != nil || !ok {
		log.Errorf("goal %s decision-ledger write failed: ok=%v err=%v", plan.GoalID, ok, err)
		return
	}
	if _, err := e.app.attachToPackage(plan.PackageID, packageRefTypeDecision, entry.ID, scoutParticipantName); err != nil {
		log.Errorf("goal %s decision attach failed: %v", plan.GoalID, err)
	}
}

// --- Stage: verify_goal_completed -------------------------------------------

func (e *goalEngine) verify(ctx context.Context, plan *goalPlan) bool {
	system := "You are Scout's final verifier for Bonfire OS. Check the produced work against the original goal. Return STRICT JSON only: {\"verdict\":\"pass|fail\",\"reasons\":\"one line\"}."
	user := "Goal: " + plan.Objective + "\nSubtasks:\n" + goalSubtaskSummary(plan) + "\nReport headline: " + plan.Report.Headline
	text, err := e.callModel(ctx, system, user)
	if err != nil {
		plan.Verification.Reasons = "verifier model call failed: " + err.Error()
		return false
	}
	var decoded struct {
		Verdict string `json:"verdict"`
		Reasons string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &decoded); err != nil {
		plan.Verification.Reasons = "verifier returned malformed JSON"
		return false
	}
	plan.Verification.Reasons = strings.TrimSpace(decoded.Reasons)
	return strings.EqualFold(strings.TrimSpace(decoded.Verdict), goalReviewPass)
}

// --- User-facing cancel (spec §2 "misfire economics", Wave 2 item 8c) ---------

// signalEventGoalCancelled: a user cancelled a running goal — negative routing
// data on whatever proposed or launched it. The payload carries the stage at
// cancellation and the tool template so the router's tuning can learn which
// mappings misfire. Defined beside the cancel seam rather than signals.go (the
// one seam that emits it lives in this file).
const signalEventGoalCancelled = "goal_cancelled"

// cancelGoalThread parks a running goal at needs_attention on one tap, so a
// wrong launch costs one tap, not six subtasks. Semantics: the plan is stamped
// cancelled (cancelledBy/cancelledAt persisted with the plan and mirrored into
// artifact metadata), the goal lands terminal needs_attention — which frees the
// requester's in-flight cap slot immediately — dispatchReady refuses further
// subtasks, and any child still running finishes on its own but folds as a
// no-op (there is no preemption seam into a child goroutine or a claimed
// sidecar job; the cheap, safe half is refusing NEW work). No salvage runs for
// a cancel: the user deliberately abandoned the launch, so nothing is attached
// to a package as a "draft to finish". Idempotent: a second cancel is a no-op.
// Works keyless (no model calls). Authorization — the goal's requester or the
// approval admin — is the HTTP door's job, mirroring artifactRunnerActionHandler.
func (app *kanbanBoardApp) cancelGoalThread(parentID string, cancelledBy string) error {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return fmt.Errorf("goal id is required")
	}
	lock := goalEngineLock(parentID)
	lock.Lock()
	defer lock.Unlock()

	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return fmt.Errorf("goal artifact not found")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return fmt.Errorf("goal plan not found")
	}
	if plan.Cancelled {
		return nil // idempotent: the one tap already landed
	}
	if plan.State == goalStateVerified {
		return fmt.Errorf("goal already finished; there is nothing to cancel")
	}

	engine := newGoalEngine(app)
	stageAtCancel := plan.State
	plan.Cancelled = true
	plan.CancelledBy = firstNonEmptyString(strings.TrimSpace(cancelledBy), "unknown")
	plan.CancelledAt = engine.now().UTC().Format(time.RFC3339Nano)
	plan.State = goalStateBlocked
	plan.Blocker = "cancelled by " + plan.CancelledBy
	engine.finish(&plan, parentID)

	// Misfire signal (spec §2): which stage the user pulled the cord at and
	// which tool template misfired — the router's tuning data. recordSignalEvent
	// logs and continues; a signal write never fails the cancel.
	app.recordSignalEvent(plan.CancelledBy, signalEventGoalCancelled, signalValenceNegative, parentID, plan.PackageID, map[string]string{
		"stage":        stageAtCancel,
		"toolTemplate": plan.ToolTemplate,
	})
	return nil
}

// --- Stage: commit_push (external_write only, post-approval) ------------------

// resumeApprovedGoal is the entry an admin approval handler calls to unblock an
// external_write goal. It refuses unless the plan is actually parked at
// approval_required with the gate's approvalRequired flag set — the second half
// of the "no external_write without a prior approval record" guarantee. The
// approvedBy record is written into the plan before commit_push runs.
func (app *kanbanBoardApp) resumeApprovedGoal(parentID string, approvedBy string) error {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return fmt.Errorf("goal id is required")
	}
	lock := goalEngineLock(parentID)
	lock.Lock()
	defer lock.Unlock()

	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return fmt.Errorf("goal artifact not found")
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return fmt.Errorf("goal plan not found")
	}
	if plan.State != goalStateApproval || !plan.Gate.ApprovalRequired {
		return fmt.Errorf("goal is not waiting on an approval gate")
	}
	plan.Gate.Status = "passed"
	plan.Gate.ReviewedBy = firstNonEmptyString(strings.TrimSpace(approvedBy), "admin")
	plan.State = goalStateCommit

	engine := newGoalEngine(app)
	engine.persist(&plan, parentID, "")
	ctx, cancel := context.WithTimeout(context.Background(), engine.timeout)
	defer cancel()
	engine.drive(ctx, &plan, parentID)
	return nil
}

// enqueueCommitPush enqueues the single external_write sidecar job the gate
// recorded, exactly once. Commit/push therefore stays behind BOTH the sidecar
// isolation and the admin gate. The job runs against a dedicated commit child
// artifact (not the parent, whose body is the report) carrying goalParentId so
// the shared codex-callback fold lands the terminal state.
//
// Idempotent across restarts: once a commit child exists, this never enqueues a
// second external_write job. On re-drive it folds the child if it already
// finished, otherwise waits for the callback — so a parked commit_push cannot
// fire a duplicate git push/deploy on every boot.
func (e *goalEngine) enqueueCommitPush(plan *goalPlan, parentID string) {
	if existing := strings.TrimSpace(plan.Gate.CommitChildID); existing != "" {
		if childStatus, terminal := goalChildTerminalStatus(e.app, existing); terminal {
			e.foldCommitResult(plan, parentID, childStatus)
		}
		// Otherwise the commit job is still in flight; wait for its callback.
		return
	}

	command := firstNonEmptyString(plan.Gate.Command, plan.Objective)
	child, _, err := e.app.createOSArtifactWithMetadata("workflow", command, buildGoalCommitScaffold(plan, command), plan.CreatedBy, map[string]string{
		"mode":          "goal_commit",
		"goalParentId":  parentID,
		"goalSubtaskId": goalCommitSubtaskID,
		"authority":     codexJobAuthorityExternalWrite,
	})
	if err != nil || strings.TrimSpace(child.ID) == "" {
		e.fail(plan, parentID, "commit/push child artifact failed")
		return
	}
	thread := scoutAgentThread{
		ID:       fmt.Sprintf("%s-commit", plan.GoalID),
		Mode:     "workflow",
		Query:    command,
		Artifact: child,
	}
	result, err := e.app.enqueueCodexAgentThreadJob(thread, codexJobAuthorityExternalWrite)
	if err != nil {
		log.Errorf("goal %s commit_push enqueue failed: %v", parentID, err)
		e.fail(plan, parentID, "commit/push enqueue failed: "+err.Error())
		return
	}
	// Stamp the runner job id onto the child so the callback's expectedJobID
	// guard matches and lands on this exact artifact.
	if _, _, err := e.app.updateOSArtifactWithMetadata(child.ID, "", child.Text, scoutParticipantName, result.Metadata); err != nil {
		log.Errorf("goal %s commit child metadata failed: %v", parentID, err)
	}
	plan.Gate.CommitChildID = child.ID
	e.persist(plan, parentID, "")
}

// foldCommitResult lands the terminal state once the external_write commit job
// finishes: a clean push verifies the goal; a failed push needs attention.
func (e *goalEngine) foldCommitResult(plan *goalPlan, parentID string, childStatus string) {
	if childStatus == subtaskComplete {
		plan.State = goalStateVerified
		plan.Verification.Verdict = goalReviewPass
		plan.Verification.Reasons = "external write shipped and confirmed by the sidecar"
	} else {
		plan.State = goalStateBlocked
		plan.Verification.Verdict = goalReviewFail
		plan.Blocker = "commit/push job failed"
	}
	plan.Verification.CheckedAt = e.now().UTC().Format(time.RFC3339Nano)
	e.finish(plan, parentID)
}

func buildGoalCommitScaffold(plan *goalPlan, command string) string {
	return strings.Join([]string{
		"Goal commit/push job",
		"",
		"Vision: " + compactAssistantLine(plan.Objective),
		"Approved command: " + compactAssistantLine(command),
		"Status: running",
		"",
		"This is the external_write sidecar job an admin approved for the parent goal.",
	}, "\n")
}

// --- Persistence -------------------------------------------------------------

// persist writes the plan JSON plus the derived display metadata onto the
// artifact. body="" keeps the current artifact text (updateOSArtifactWithMetadata
// rejects empty text, so the current body is loaded).
func (e *goalEngine) persist(plan *goalPlan, parentID string, body string) meetingMemoryEntry {
	status, gate, percent := goalStateDisplay(plan)
	// Monotonic advisory percent: a revision re-queue legitimately lowers the raw
	// execute-phase percent (a verified subtask reverts to running), which reads
	// as the goal running backwards. Hold a high-water mark for non-terminal
	// states so the card only ever advances; a terminal state keeps its canonical
	// percent (verified 100 / needs_attention 72). Computed before the marshal
	// below so MaxProgress survives in the persisted plan across fold re-drives.
	if !isTerminalGoalState(plan.State) {
		if percent < plan.MaxProgress {
			percent = plan.MaxProgress
		} else {
			plan.MaxProgress = percent
		}
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		log.Errorf("goal %s encode plan failed: %v", parentID, err)
		return meetingMemoryEntry{}
	}
	if strings.TrimSpace(body) == "" {
		if current, ok := e.app.osArtifactByID(parentID); ok {
			body = current.Text
		}
	}
	metadata := map[string]string{
		"goalPlan":        string(raw),
		"mode":            "goal",
		"currentStage":    plan.State,
		"goalStatus":      status,
		"reviewGate":      gate,
		"progressPercent": strconv.Itoa(percent),
	}
	// An honest "revising (attempt N of 2)" signal while a re-queued subtask is
	// back in flight, so the card shows a deliberate revision rather than a
	// stall or an oscillating bar.
	if note := goalRevisionNote(plan); note != "" {
		metadata["goalRevisionNote"] = note
	}
	// Salvaged best-draft linkage for a needs_attention goal: the openable draft
	// id and the honest gap it missed, so the card can point at the saved work.
	if id := strings.TrimSpace(plan.Report.DeliverableArtifactID); id != "" {
		metadata["deliverableArtifactId"] = id
	}
	if gap := strings.TrimSpace(plan.Report.Gap); gap != "" {
		metadata["goalGap"] = gap
	}
	// save_what_worked's distilled lessons ride the artifact metadata so the
	// Taste Analyst (and the artifact pane) can read them without decoding the
	// plan JSON.
	if len(plan.Report.SavedLessons) > 0 {
		if raw, lessonsErr := json.Marshal(plan.Report.SavedLessons); lessonsErr == nil {
			metadata["savedLessons"] = string(raw)
		}
	}
	if strings.TrimSpace(plan.Blocker) != "" {
		metadata["goalBlocker"] = plan.Blocker
	}
	// The cancel record rides the artifact so the card can say who pulled the
	// cord and when, without decoding the plan.
	if plan.Cancelled {
		metadata["cancelled"] = "true"
		metadata["cancelledBy"] = plan.CancelledBy
		metadata["cancelledAt"] = plan.CancelledAt
	}
	artifact, _, err := e.app.updateOSArtifactWithMetadata(parentID, "", body, scoutParticipantName, metadata)
	if err != nil {
		log.Errorf("goal %s persist failed: %v", parentID, err)
		return meetingMemoryEntry{}
	}
	broadcastSignedInKanbanEvent("memory", e.app.memorySnapshotForClients(20))
	return artifact
}

// fail lands the terminal needs_attention state with a blocker line.
func (e *goalEngine) fail(plan *goalPlan, parentID string, blocker string) {
	plan.State = goalStateBlocked
	plan.Blocker = firstNonEmptyString(blocker, plan.Blocker, "goal needs attention")
	e.finish(plan, parentID)
}

// finish persists a terminal state, updates the linked card, and notifies the
// creator — reusing the same seams the single-shot thread terminal seam uses.
func (e *goalEngine) finish(plan *goalPlan, parentID string) {
	// A goal that terminates needs_attention must not orphan its best work: if a
	// subtask produced a real deliverable, salvage it (attach + surface) before
	// composing the terminal brief. A CANCELLED goal is the exception — the user
	// deliberately abandoned the launch, so nothing gets attached to a package as
	// a "draft to finish" (and no salvage signal double-counts the misfire).
	if plan.State == goalStateBlocked && !plan.Cancelled {
		e.salvageBlockedDeliverable(plan, parentID)
	}
	artifact := e.persist(plan, parentID, composeGoalArtifact(plan))
	if strings.TrimSpace(artifact.ID) == "" {
		if current, ok := e.app.osArtifactByID(parentID); ok {
			artifact = current
		}
	}
	terminalStatus := codexJobStatusComplete
	message := "Goal verified"
	if plan.State != goalStateVerified {
		terminalStatus = "error"
		message = "Goal needs attention"
		if plan.Cancelled {
			message = "Goal cancelled"
		}
	}
	e.app.syncLinkedCardForArtifact(artifact, terminalStatus)
	e.app.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText(message, artifact))
	broadcastAssistantEvent("action", message, map[string]any{
		"tool":       "launch_goal_thread",
		"artifact":   artifact,
		"voiceState": "listening",
	})
}

// persistApprovalRequired stops the engine at the human gate, reusing the exact
// approval metadata shape codexApprovalRequiredResult writes so the existing
// admin approve/reject UI lights up unchanged.
func (e *goalEngine) persistApprovalRequired(plan *goalPlan, parentID string) {
	artifact := e.persist(plan, parentID, composeGoalArtifact(plan))
	if strings.TrimSpace(artifact.ID) == "" {
		if current, ok := e.app.osArtifactByID(parentID); ok {
			artifact = current
		}
	}
	// Extra keys the approval surface keys off (threadStatus/reviewGate).
	if _, _, err := e.app.updateOSArtifactWithMetadata(parentID, "", artifact.Text, scoutParticipantName, map[string]string{
		"threadStatus": codexJobStatusApprovalRequired,
		"status":       codexJobStatusApprovalRequired,
		"reviewGate":   "approval_required",
	}); err != nil {
		log.Errorf("goal %s approval metadata failed: %v", parentID, err)
	}
	e.app.notifyAgentThreadCreator(artifact, notificationKindAgent, agentThreadNotificationText("Goal needs approval to ship", artifact))
}

// --- Boot reconciler ---------------------------------------------------------

// reconcileGoalThreadsAtBoot resumes every mode=goal artifact not in a terminal
// (or approval-waiting) state. It mirrors the ambient-agent single-pass shape:
// one scan at boot, fold any completed children, re-dispatch ready subtasks
// idempotently, and drive from the earliest non-complete state. Skips when
// keyless (the engine only activates with ANTHROPIC_API_KEY, so keyless deploys
// are unchanged).
func (app *kanbanBoardApp) reconcileGoalThreadsAtBoot() {
	if app == nil || app.memory == nil || !hasAnthropicAPIKey() {
		return
	}
	for _, artifact := range app.memory.entriesOfKind(meetingMemoryKindOSArtifact, goalReconcileScanLimit) {
		if artifact.Metadata["mode"] != "goal" {
			continue
		}
		if isTerminalGoalState(artifact.Metadata["currentStage"]) {
			continue
		}
		go app.reconcileGoalThread(artifact.ID)
	}
}

func isTerminalGoalState(state string) bool {
	switch strings.TrimSpace(state) {
	case goalStateVerified, goalStateBlocked, goalStateApproval:
		// approval_required waits on a human, not on the engine.
		return true
	default:
		return false
	}
}

// reconcileGoalThread folds any terminal children of one goal and re-drives it.
// A restart loses in-flight goroutines, not state: any running subtask whose
// child artifact is already terminal is folded; the rest are re-marked ready so
// the executor re-dispatches them (idempotent by subtask id).
func (app *kanbanBoardApp) reconcileGoalThread(parentID string) {
	lock := goalEngineLock(parentID)
	lock.Lock()
	defer lock.Unlock()

	parent, ok := app.osArtifactByID(parentID)
	if !ok {
		return
	}
	plan, ok := decodeGoalPlan(parent.Metadata["goalPlan"])
	if !ok {
		return
	}
	// A cancelled goal is terminal by decree: never re-queue or re-drive it,
	// whatever states its subtasks were stranded in (the boot scan already skips
	// its needs_attention stage; this guards a direct call).
	if plan.Cancelled {
		return
	}
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		if st.Status != subtaskRunning {
			continue
		}
		if childStatus, terminal := goalChildTerminalStatus(app, st.ArtifactID); terminal {
			if childStatus == subtaskComplete {
				st.Status = subtaskComplete
			} else {
				st.Status = subtaskFailed
			}
			continue
		}
		// No live goroutine after restart and the child never finished: re-queue.
		st.Status = subtaskReady
	}

	engine := newGoalEngine(app)
	engine.persist(&plan, parentID, "")
	ctx, cancel := context.WithTimeout(context.Background(), engine.timeout)
	defer cancel()
	engine.drive(ctx, &plan, parentID)
}

func goalChildTerminalStatus(app *kanbanBoardApp, artifactID string) (string, bool) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return "", false
	}
	child, ok := app.osArtifactByID(artifactID)
	if !ok {
		return "", false
	}
	status := strings.ToLower(strings.TrimSpace(firstNonEmptyString(child.Metadata["threadStatus"], child.Metadata["status"])))
	switch status {
	case codexJobStatusComplete:
		return subtaskComplete, true
	case codexJobStatusFailed, "error":
		return subtaskFailed, true
	default:
		return "", false
	}
}

// --- Display + helpers -------------------------------------------------------

// goalStateDisplay maps the authoritative plan state to the advisory UI fields
// the running-artifact card renders.
func goalStateDisplay(plan *goalPlan) (goalStatus string, reviewGate string, percent int) {
	switch plan.State {
	case goalStateVerified:
		return "verified", "passed", 100
	case goalStateBlocked:
		return "needs_attention", "blocked", 72
	case goalStateApproval:
		return "approval_required", "approval_required", 68
	case goalStateReview:
		return "review", "pending", goalExecutePercent(plan, 82)
	case goalStateGate:
		return "running", firstNonEmptyString(plan.Gate.Status, "pending"), 88
	case goalStateSave:
		return "running", "passed", 90
	case goalStateReport:
		return "running", "passed", 94
	case goalStateVerify:
		return "running", "passed", 97
	case goalStateCommit:
		return "running", "passed", 96
	case goalStateExecute, goalStateCoordinate:
		return "running", "pending", goalExecutePercent(plan, 25)
	default:
		return "running", "pending", goalStagePercent(plan.State)
	}
}

// goalExecutePercent reserves 25..80 for subtask completion so review/gate/verify
// have headroom above (technical §2.3).
func goalExecutePercent(plan *goalPlan, floor int) int {
	total := len(plan.Subtasks)
	if total == 0 {
		return floor
	}
	done := goalCountStatus(plan, subtaskComplete)
	percent := 25 + (done*55)/total
	if percent < floor {
		return floor
	}
	if percent > 80 {
		return 80
	}
	return percent
}

func goalStagePercent(state string) int {
	switch state {
	case goalStateIdentify:
		return 5
	case goalStateDecompose:
		return 15
	case goalStateAssign:
		return 20
	default:
		return 25
	}
}

func goalCountStatus(plan *goalPlan, status string) int {
	count := 0
	for index := range plan.Subtasks {
		if plan.Subtasks[index].Status == status {
			count++
		}
	}
	return count
}

func goalAllComplete(plan *goalPlan) bool {
	if len(plan.Subtasks) == 0 {
		return false
	}
	for index := range plan.Subtasks {
		if plan.Subtasks[index].Status != subtaskComplete {
			return false
		}
	}
	return true
}

func goalAnyRunning(plan *goalPlan) bool {
	return goalCountStatus(plan, subtaskRunning) > 0
}

func goalArtifactIDs(plan *goalPlan) []string {
	ids := make([]string, 0, len(plan.Subtasks))
	for index := range plan.Subtasks {
		if id := strings.TrimSpace(plan.Subtasks[index].ArtifactID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func goalBlockerLine(plan *goalPlan) string {
	if strings.TrimSpace(plan.Blocker) != "" {
		return plan.Blocker
	}
	for index := range plan.Subtasks {
		if plan.Subtasks[index].Status == subtaskBlocked {
			return fmt.Sprintf("subtask %q is blocked", plan.Subtasks[index].ID)
		}
	}
	return "goal review could not proceed"
}

func goalSubtaskSummary(plan *goalPlan) string {
	var builder strings.Builder
	for index := range plan.Subtasks {
		st := &plan.Subtasks[index]
		builder.WriteString("- ")
		builder.WriteString(st.ID)
		builder.WriteString(" [")
		builder.WriteString(st.Status)
		builder.WriteString("] ")
		builder.WriteString(st.Title)
		builder.WriteByte('\n')
	}
	return builder.String()
}

// composeGoalArtifact renders the durable Markdown brief from the plan.
func composeGoalArtifact(plan *goalPlan) string {
	lines := []string{
		"Goal execution thread",
		"",
		"Vision: " + compactAssistantLine(plan.Objective),
		"Status: " + goalStatusLabel(plan.State),
		"Authority: " + plan.Authority,
		"",
		"## Report",
	}
	if plan.Report.Changed != "" {
		lines = append(lines, "- Changed: "+plan.Report.Changed)
	}
	if plan.Report.Headline != "" {
		lines = append(lines, "- Headline: "+plan.Report.Headline)
	}
	if plan.Report.Gap != "" {
		lines = append(lines, "- Gap: "+plan.Report.Gap)
	}
	if plan.Report.Next != "" {
		lines = append(lines, "- Next: "+plan.Report.Next)
	}
	lines = append(lines, "- Gate outcome: "+firstNonEmptyString(plan.Report.GateOutcome, plan.Gate.Status, "pending"))
	lines = append(lines, fmt.Sprintf("- Assumed claims: %d", plan.Report.AssumedClaimCount))
	if len(plan.Report.SavedLessons) > 0 {
		lines = append(lines, "", "## What worked")
		for _, lesson := range plan.Report.SavedLessons {
			lines = append(lines, "- "+lesson)
		}
	}
	lines = append(lines, "", "## Work decomposition")
	lines = append(lines, strings.TrimRight(goalSubtaskSummary(plan), "\n"))
	lines = append(lines, "", "## Gate", "- Status: "+firstNonEmptyString(plan.Gate.Status, "pending"))
	if plan.Gate.Reason != "" {
		lines = append(lines, "- Reason: "+plan.Gate.Reason)
	}
	lines = append(lines, "", "## Verification", "- Verdict: "+firstNonEmptyString(plan.Verification.Verdict, "pending"))
	if plan.Verification.Reasons != "" {
		lines = append(lines, "- Reasons: "+plan.Verification.Reasons)
	}
	if plan.Blocker != "" {
		lines = append(lines, "", "## Blocker", "- "+plan.Blocker)
	}
	if id := strings.TrimSpace(plan.Report.DeliverableArtifactID); id != "" {
		lines = append(lines, "", "## Draft saved",
			"- The best deliverable draft is saved and attached; it missed the review bar but is ready to finish.",
			"- Artifact: "+id)
		if plan.Report.Gap != "" {
			lines = append(lines, "- Gap: "+plan.Report.Gap)
		}
	}
	return strings.Join(lines, "\n")
}

func goalStatusLabel(state string) string {
	switch state {
	case goalStateVerified:
		return "verified"
	case goalStateBlocked:
		return "needs attention"
	case goalStateApproval:
		return "waiting on approval"
	default:
		return "running"
	}
}

func (app *kanbanBoardApp) nowUnixNano() int64 { return time.Now().UnixNano() }

// callModel is a single no-tools orchestrator model call returning the
// concatenated text. It reuses the Wave-1 injectable anthropic responder.
func (e *goalEngine) callModel(ctx context.Context, system string, user string) (string, error) {
	return e.callModelAs(ctx, e.model, system, user)
}

// callReviewModel routes a call to the dedicated review model (Wave 3 item 16
// — the per-subtask review and the ship gate read WHOLE artifact bodies, which
// wants Opus-tier context at Opus rates, not the Fable ceiling). Orchestration
// calls (decompose, panel, report, verify) stay on callModel. Same
// env-with-override shape as the assignedRunner per-subtask pattern.
func (e *goalEngine) callReviewModel(ctx context.Context, system string, user string) (string, error) {
	return e.callModelAs(ctx, firstNonEmptyString(e.reviewModel, e.model), system, user)
}

// callModelAs is callModel with the model chosen per call; everything else
// (key, effort, token ceiling, refusal handling) is shared.
func (e *goalEngine) callModelAs(ctx context.Context, model string, system string, user string) (string, error) {
	apiKey := strings.TrimSpace(e.apiKey())
	if apiKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not configured")
	}
	response, err := e.responder(ctx, apiKey, anthropicMessagesRequest{
		Model:     model,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: []json.RawMessage{anthropicTextBlock(user)}}},
		MaxTokens: e.maxTokens,
		Effort:    e.effort,
	})
	if err != nil {
		return "", err
	}
	if response.StopReason == "refusal" {
		return "", fmt.Errorf("orchestrator request was declined by safety classifiers")
	}
	return anthropicResponseText(response), nil
}

func anthropicResponseText(response anthropicMessagesResponse) string {
	var builder strings.Builder
	for _, raw := range response.Content {
		block := decodeAnthropicBlock(raw)
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			builder.WriteString(block.Text)
			builder.WriteByte('\n')
		}
	}
	return strings.TrimSpace(builder.String())
}

// extractJSONObject pulls the first balanced {...} out of a model response,
// tolerating code fences and surrounding prose.
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "{}"
	}
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		return "{}"
	}
	return text[start : end+1]
}
