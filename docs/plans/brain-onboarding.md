# Brain onboarding — "Feed the brain" (card 082)

_Design doc + shipped guided-chat slice. 2026-07-07._

## Why

The OS is "our company as intelligence." That intelligence is only as good as
what it has ingested. A brand-new room knows nothing about Shareability's
history, its hero campaigns, its decks, its house voice, or its client roster —
so Scout's recall is thin until real material lands in meeting memory. Today the
only way to feed the brain is to talk in a live meeting (spoken transcript) or
drop files into a chat and hope the model happens to read them. Neither is a
deliberate, guided act of teaching the company to itself.

"Feed the brain" is that deliberate act: a scripted intake that walks a founder
through the seven things the brain most needs, files every answer and upload
into durable memory as raw material, and triggers a synthesis pass so the
knowledge is recallable — by the whole team — within minutes.

## Three channels (the full program)

1. **Ad-hoc private-chat uploads** — _already exists_ (cards 085/095). Any
   attachment dropped into a private Scout thread is ingested: readable text
   inline, PDFs/images through the derived-text pass, blob bytes filed to the
   content-addressed store. This is the always-on, unstructured door.

2. **Guided "Feed the brain" flow** — _shipped in this card_. A deterministic,
   scripted interview inside a private Scout thread. Scout asks one topic at a
   time; the user answers in words, attaches files, says `skip` to move on, or
   `done` to wrap up early. Every contribution is filed as raw brain material
   and, on completion, synthesized into the room brain. 30–60 minutes if done
   thoroughly; skippable to five minutes.

3. **Voice narration** — _designed here, fast-follow_. A hands-free variant:
   the user taps "Narrate to the brain" and talks through the same seven topics
   out loud while a patient interviewer persona (a private Realtime voice
   session) asks follow-ups. Utterances are captured incrementally and filed as
   raw material, then synthesized on end. This channel is fully templated by the
   existing private-grill session-swap mechanism (`start_private_grill` /
   `end_private_grill`, grill.go + kanban.go + the client ritual in index.html)
   plus the ambient brain worker, and it touches none of the guided-chat code —
   so it lands as its own reviewed unit next wave without rework. See "Voice
   narration (fast-follow)" below.

This card ships channels 1 (reused) and 2 (new), plus this doc specifying all
three. Channel 3 is descoped to a fast-follow per the wave's index.html
contention rule — it would add a new Realtime tool pair and a new incremental
utterance route on top of contested voice surfaces, and it is cleanly separable.

## The question script (channel 2, deterministic)

Seven fixed, ordered steps. Every prompt is a hardcoded string — **zero model
calls**, so the interview runs identically keyed or keyless. The steps
(`brainIntakeSteps` in brain_intake.go):

1. **company_history** — origin story, founders, earliest days, pivot moments.
2. **hero_stories** — best campaigns / case studies / client wins, with results.
3. **decks** — upload pitch/sales/credentials decks (files).
4. **brand** — brand guidelines, logos, key imagery, house look-and-sound (files).
5. **docs** — one-pagers, rate cards, process docs, playbooks (files or text).
6. **comms_style** — real writing samples that show the house voice.
7. **clients_deals** — marquee clients, active deals, partners that matter.

The thread opens with a welcome message that (a) frames the flow, (b) discloses
the privacy contract explicitly, and (c) poses step 1. Each answer advances the
`IntakeStep` cursor and Scout replies with the next scripted prompt. `done` (or
`finish` / `that's all`) completes early; running past step 7 completes
naturally. Completion clears the intake flag, posts a wrap-up message, and fires
the synthesis flush.

## Storage doctrine

- **Raw material = transcript entries.** Every typed answer and every
  attachment's text is filed via `appendAttributedTranscriptEntry` with
  `source=brain_intake`, `intakeStep=<step key>`, and (when card 085 supplies
  one) `blobRef=<blob>` / `attachmentName=<name>` metadata. The usefulness
  filter is bypassed (`bypassUsefulnessFilter=true`) — a terse but deliberate
  answer like a client name is signal, not filler. Each entry is deduped on a
  caller-supplied event id derived from the chat message id, so a retry never
  double-files. Bare control verbs (`skip` / `done`) are not filed as content,
  but any attachments riding them are.

- **Synthesized knowledge = brain entries.** On completion the brain worker's
  produce pass (`produceMeetingBrainWriteUp`) consumes the new transcript window
  into a `kind=brain` write-up, exactly as the ambient loop does every five
  minutes — the intake just forces one pass immediately so the user does not
  wait. The write-up broadcasts over the existing `memory_brain` event.

- **Recall.** Both layers are searchable through the normal path
  (memory_query.go, `answer_memory_question`): raw entries for verbatim lookups,
  brain write-ups for synthesized recall.

## Refresh / re-run policy

The intake is idempotent by design: it always files _new_ raw material and
synthesizes _new_ brain write-ups over the delta since the last brain pass. A
user can start a fresh "Feed the brain" thread anytime to add more — there is no
"already onboarded" lock. Re-running does not overwrite prior knowledge; it
layers on top, and the brain worker naturally reconciles across windows because
it consumes by kind. A future "refresh" affordance can re-pose the seven topics
against what the brain already knows; out of scope for this slice.

## Privacy contract (load-bearing — reviewer-checked)

The intake thread is **private** (owner + Scout, enforced by
`scoutChatThreadsSnapshot` / `scoutChatThreadByID`), but the knowledge it
ingests becomes **room-global**: transcript and brain entries are shared memory
that Scout recalls for the whole office. This asymmetry is exactly the kind of
surprise that erodes trust, so the seeded welcome message discloses it
verbatim and up front: _"this thread is private to you, but everything you share
here becomes part of the shared room brain … don't paste anything you wouldn't
want the office to know."_ The disclosure is not optional copy — it is the
consent surface, and it is pinned by a test.

## Known boundaries (documented, acceptable for the slice)

- **Boot baseline.** Contributions filed before a server restart sit behind the
  brain worker's boot baseline (`bootBaselineIDOfKind`) and are not backfilled
  into synthesis without `MEETING_BRAIN_BACKFILL` — a restart mid-intake strands
  pre-restart contributions from _synthesis_ (the raw entries stay fully
  searchable). Acceptable: intake is a single short session; a restart mid-flow
  is rare, and the raw material is never lost.
- **Meeting-window split.** Intake transcript entries stamp the current meeting
  id; a room archive or 30-min-empty reset (card 078) mid-intake splits the
  batch across meeting windows. Brain synthesis consumes by kind so write-ups
  still generate; only per-meeting snapshot views scatter.
- **Keyless degrade.** The guided chat intake works fully keyless — the script
  is deterministic and raw material is always filed. Only the synthesis pass
  (and, later, voice) need `OPENAI_API_KEY`; keyless, the raw entries persist and
  become searchable the moment a key is configured and the next brain pass runs.

## Voice narration (fast-follow)

Not shipped in this card; specified here so the next wave can land it cleanly.

- **Session swap.** Add `start_brain_narration` / `end_brain_narration` Realtime
  tools mirroring `start_private_grill` / `end_private_grill` (schemas near the
  grill tools in kanban.go; allowlist in `privateRealtimeVoiceToolAllowed`;
  dispatch in `applyPrivateRealtimeVoiceTool`). `start` returns an interviewer
  persona instruction set (one question at a time, the same seven topics) plus a
  `maxDurationMs` cap (`BRAIN_NARRATION_MAX_DURATION`, default 45m); `end`
  returns `privateRealtimeVoiceSessionInstructions()` to revert — the exact
  grill contract, instructions-only, pinned by a narration twin of
  `TestPrivateGrillClientSwapIsInstructionsOnly`.
- **Incremental capture.** The grill's tool-args cap is lossy for 45 minutes, so
  narration captures transcripts incrementally: the client buffers
  `conversation.item.input_audio_transcription.completed` (user) and
  `*audio_transcript.done` (Scout) exchanges in `handlePrivateRealtimeVoiceEvent`
  and batch-flushes to a new session-gated route
  `POST /assistant/brain-narration/utterances` (64KB cap, cloned from the
  `assistantRealtimeToolHandler` gate), which files each utterance as a
  transcript entry keyed by client utterance id (dedupe) with
  `source=brain_narration`. `end` fires the same synthesis flush as the chat
  intake.
- **Env.** `BRAIN_NARRATION_MAX_DURATION` (optional, default 45m; mirrors
  `GRILL_MAX_DURATION`). No new dependencies. Reuses `OPENAI_API_KEY` and the
  existing `MEETING_BRAIN_*` envs.

## What shipped (channel 2)

- `brain_intake.go` — the seven-step script, `startBrainIntakeThread`, the
  intake message handler (persist → ingest → advance → next prompt; `skip` /
  `done` verbs), `appendBrainIntakeContribution`, and `flushBrainForIntake`.
- `scout_chat_threads.go` — `Intake` / `IntakeStep` fields on the thread record
  (JSON round-trips, old records untouched), the `intake:"brain"` create-payload
  branch, and the early intake branch in `appendScoutChatThreadMessageWithTool`
  that bypasses the propose-confirm router entirely.
- index.html — a "Feed the brain" entry in the private empty state,
  `startBrainIntake()`, and an intake-aware composer placeholder.
