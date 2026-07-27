# Voice-First Mobile — Unified Design

> Branch `design/voice-first-mobile` · 2026-07-27
> Context: [`voice-first-mobile-context.md`](voice-first-mobile-context.md)

## Executive Summary

BonfireOS on iOS becomes a single **canvas you talk to**, not a set of screens you
operate. The five-tab shell is removed. The app opens on a waveform that renders
your actual voice in real time, and everything else in the product is pulled *over*
that canvas as a glass sheet or reached by saying what you want. Two voice intents —
**speaking *to*** something (conversation) and **speaking *into*** something
(dictation) — are physically separated by tap versus hold, so the system never has
to guess which one you meant.

The architectural bet is that **speech is the substrate, not an input method**: text,
cards, threads, and files are renderings of what was said. The unfair advantage is
that we own the transcription lane, so dictation is biased with company vocabulary
and spells your teammates' names right where Apple's on-device dictation cannot.

---

## Part I — First Principles

### 1. The inversion

Every "voice feature" ever shipped treats speech as an *alternative input method*:
there is a real interface made of buttons, and a microphone icon that fills in a
text field for you. That framing caps the product at "keyboard, but hands-free."

The premise here is different, and it is the whole design:

> **Speech is the substrate. Everything else is a rendering of what was said.**

A company already runs on conversation. The lossy part is the conversion —
someone hears a decision in a meeting and manually turns it into a ticket, a doc,
an update, a reminder. The product's bet is to capture conversation as the primary
material and let the system do the conversion. If that is true, then the interface's
job is not to present state. It is to **be maximally ready to receive speech**, and
to show you what your speech became.

That single inversion produces every decision below.

### 2. Four corollaries

**C1 — The home screen is not a dashboard. It is a microphone.**
A dashboard says *here is state; go operate on it*. If the promise is "you don't
operate it, you just talk," then the home screen must have almost nothing to
operate. The current `HomeScreen` — greeting, three metric tiles, three nav cards —
is a dashboard, and it is the single most off-thesis surface in the app. It goes.

**C2 — There are exactly two voice intents, and conflating them is the original sin.**

|  | Speak **to** | Speak **into** |
|---|---|---|
| Intent | Converse | Produce text |
| Duplex | Full — you expect an answer | Half — you expect a transcript |
| Ends when | The exchange resolves | You stop holding |
| Lands in | A live line to Scout or a room | A field: message, card, search |
| Gesture | **Tap** | **Hold** |

Siri feels bad because one channel does both jobs, so you must speak a *command
grammar* ("send a message to Dana saying I'll be late") instead of just talking. We
resolve the ambiguity physically rather than semantically. Tap and hold are the two
most learnable gestures on a touchscreen, and users already know hold-to-talk from
walkie-talkie UIs. **No mode indicator is needed because your finger is the mode.**

**C3 — Time-to-first-word is the only performance metric that matters.**
If the app's core promise is "just talk," then any latency between *deciding to
speak* and *being heard* breaks the promise. This has hard architectural
consequences: the canvas is the root route (no auth-gated dashboard fetch in front
of it), the audio session is pre-warmed on mount, the waveform renders before any
network call resolves, and no data fetch may block first paint. Everything else on
the canvas is allowed to arrive late.

**C4 — Silence is a design material.**
A voice-first app that animates constantly is exhausting, and worse, it lies: if the
waveform always moves, movement carries no information. The desktop already
codified the correct rule, and mobile inherits it verbatim:

> **The breathe-only-while-listening law.** Waveform bars rest **static**. They
> animate only while the pipeline is genuinely listening or answering. Ember lights
> only when work is actually happening.
> — `index.html:4211-4217`, `:5911`

This makes motion *semantic*. Stillness means "ready." Movement means "I hear you."
Ember means "something is being done." A user learns this in one session and can
then read the app's state from across a desk.

### 3. What mobile does that desktop cannot

The desktop waveform is decorative — CSS keyframes at a fixed 430ms/1100ms cadence
(`index.html:4208`). It signals *listening* without representing anything.

On a phone the microphone is four inches from your mouth, and `expo-audio` exposes
live `metering` in dBFS. So:

> **The mobile waveform is not an animation. It is an instrument.**
> The bars are a real-time render of your actual voice.

This is the "something even cooler" the brief asked for, and it is not a gimmick —
it does real work:

1. **It kills the core anxiety of voice UIs.** "Is this thing actually listening?" is
   answered continuously and honestly, sub-frame, by physics rather than by a
   spinner. A decorative waveform cannot distinguish *listening* from *listening to
   a muted mic*; an instrument can.
2. **It teaches mic technique.** Users learn to speak up, or move their thumb off the
   mic port, because they can see it.
3. **It is honest.** Under the breathe law, motion means something. An amplitude-
   driven waveform is the maximally honest version of that rule.

And its companion, which is the visual thesis of the entire product in one gesture:

> **The transcript materializes *out of* the waveform.**
> Words rise from the bars as the server returns them. The waveform is not replaced
> by text — it *becomes* text.

Speech becoming structure, rendered literally, every time you use the app.

---

## Part II — The Spatial Model

### 4. Four nouns

Removing the tab bar requires replacing it with something at least as legible. The
answer is a canvas with things pulled *over* it:

```
   ┌──────────────────────────────┐
   │                              │
   │          ▁▃▅█▅▃▁             │   THE CANVAS  ── always the root
   │      Good morning, AJ.       │                  waveform · greeting · one live line
   │   Two rooms live. Scout       │
   │   finished the pricing memo.  │
   │                              │
   │        ╭────────────╮        │   THE DOCK    ── glass pill, thumb zone
   │        │  ◉  ▁▃▅▃▁  │        │                  tap = converse · hold = dictate
   │        ╰────────────╯        │                  drag ↑ = reveal
   └──────────────────────────────┘
                  ↑ drag
   ┌──────────────────────────────┐
   │ ═══                          │   THE DECK    ── detented glass sheet
   │  Threads · Rooms · Work      │                  peek → half → full
   └──────────────────────────────┘

   ┌─────────────────┐
   │ ◉ Pricing · 12:04│              THE ISLAND  ── persistent glass chip, top
   └─────────────────┘                             live call or agent run in flight
```

| Noun | What it is | Why it exists |
|---|---|---|
| **The Canvas** | Root. Waveform, greeting, one line of what's live. Nothing else. | C1 — the home screen is a microphone. |
| **The Dock** | Glass pill at the bottom edge. The mic, and the grab handle. | C2 + reachability — one element, both voice intents, always under the thumb. |
| **The Deck** | Detented glass sheet holding Threads / Rooms / Work. | Everything you can't say yet still needs a physical path. |
| **The Island** | Compact glass chip pinned top when a call or agent run is live. | Background work must never be lost by navigating away. |

Four nouns is a complete shell. Compare: five tabs plus a stack plus modals.

### 5. Why the Dock is one element and not three

The temptation is a mic button, a keyboard button, and a handle. That is three
targets competing for the same 60pt of thumb-reachable screen. Instead, one surface
carries three affordances distinguished by **gesture**, which is free:

| Gesture on the Dock | Result |
|---|---|
| **Tap** | Open a **conversation loop** with Scout. You speak, Scout answers on the canvas, the mic re-arms automatically. Continues until you dismiss. |
| **Hold** | Dictate. One shot. Bars track your voice. Release → transcript into the field. Slide away → cancel. |
| **Drag up** | Reveal the Deck. Follows the finger, detents at peek / half / full. |
| **Tap while a thread is open** | Same loop, but scoped — Scout answers *about this thread*. |

The distinction that matters is **loop versus one-shot**, not full-duplex versus
half-duplex:

| | Tap — converse | Hold — dictate |
|---|---|---|
| Mic re-arms after each turn | **Yes** | No |
| Produces an answer | **Yes**, on the canvas | No, produces text |
| Ends when | You dismiss it | You release |
| Your hands | Free | Holding |

This is why the mapping is honest even before true full-duplex audio exists (§17):
tapping genuinely starts a *conversation* — it answers you and listens again —
while holding genuinely produces *text*. Wave 2 upgrades the loop's transport from
turn-based to full duplex without changing what either gesture means.

This is the same lesson the iPhone home indicator teaches: one physical element can
be a handle, a button, and a gesture surface simultaneously, as long as the
gestures don't collide. Tap, long-press, and vertical drag do not collide.

**Cancel-by-slide-away is load-bearing.** Every dictation UI that lacks it is
stressful, because a misfire commits garbage text. Sliding your thumb away from the
Dock while still holding aborts cleanly — the same affordance as iMessage audio
messages, so it needs no teaching.

### 6. Navigating by saying it

The Deck is the *fallback*. The primary path is speech:

> "Open the pricing thread" · "What did we decide about Q4?" · "Tell the team I'm running late"

These are three different speech acts and they resolve differently:

| Speech act | Example | Resolution |
|---|---|---|
| **Navigate** | "Open the pricing thread" | Deck opens directly to that thread. No answer spoken — you asked to *go*, not to *know*. |
| **Ask** | "What did we decide about Q4?" | Scout answers on the canvas, sourced from memory, with the thread cited and tappable. |
| **Act** | "Tell the team I'm running late" | Composes into the right thread and **shows you the draft** — never auto-sends. |

The third row is the one that earns trust. An agent that sends messages on your
behalf without a confirm is a liability; an agent that drafts perfectly and waits
is a superpower. This also lines up exactly with the product direction diagram:
evidence-bound suggestion → **human approval** → execution.

### 6.5 Inside the Deck

The Deck is not a fourth navigator — it is a sheet containing one stack, with a
segmented header. Three segments, chosen because they answer three different
questions:

| Segment | Answers | Contains |
|---|---|---|
| **Threads** | "What is being said?" | Channels + Scout threads (§14). Default segment. |
| **Rooms** | "Who is together right now?" | Live and recent rooms → existing `RoomScreen`. |
| **Work** | "What came out of it?" | Board, Files, Alerts — the *artifacts* of conversation. |

The segmentation is the product thesis in miniature: **talk → meet → result**.

**Detents.** `peek` (0.14 — just the segment header and the first rows, enough to
glance without committing), `half` (0.5), `full` (1.0). Dragging past `full` does
nothing; dragging below `peek` dismisses.

> **Implementation note (Wave 1).** The Deck is a native `formSheet` route with
> `sheetAllowedDetents`, i.e. a real `UISheetPresentationController`, rather than a
> hand-rolled pan sheet. That buys correct rubber-banding, interactive dismissal,
> and VoiceOver behaviour for free, and adds no dependency — but it means the Dock
> **cannot** literally pin to the rising sheet's top edge, because the sheet is a
> system-presented controller that owns the space below it. The property that
> mattered — *the mic is never more than a thumb-width away* — is preserved by
> giving each Deck destination its own mic in its composer (see `ThreadScreen`), so
> you can still talk without dismissing the Deck. The visual is different from the
> sketch above; the guarantee is not.

**Depth.** Pushing into a thread or a room pushes *within* the Deck's stack at the
`full` detent. Rooms are the exception: joining a live room takes the full screen,
because a call is not a sheet.

**Return-to-canvas** is always one downward drag from anywhere, or saying anything
after a tap on the Dock.

---

## Part III — Material and Motion

### 7. The Liquid Glass law

Liquid Glass fails when everything becomes frosted and depth stops meaning
anything. One rule prevents that:

> **Glass means *floating above the conversation, temporarily*.
> Never use glass for permanent structure or for content.**

| Glass | Not glass |
|---|---|
| The Dock | The Canvas background |
| The Deck sheet | Message bubbles |
| The Island | Thread list rows |
| Composer bar | Board cards |
| Popovers, action sheets | Any text you need to read for more than a moment |

The reason is legibility, not taste: glass is a *variable* backdrop, so text on it
has no contrast guarantee. Content must sit on opaque surfaces. This also matches
Apple's own framing — glass is the interactive layer floating over content, and
content itself stays solid.

**Fallback matrix.** `expo-glass-effect` needs iOS 26; the deployment target is 16.4.

| Runtime | Implementation |
|---|---|
| iOS 26+, `isLiquidGlassAvailable()` true | `GlassView`, `glassEffectStyle="regular"`, `isInteractive` on tappable glass |
| iOS 16.4–25, or API unavailable | `expo-blur` `BlurView` + hairline `glassBorder` + `glassPanel` fill |
| Reduce Transparency enabled | Opaque `surface1` + hairline border. No blur at all. |

All three paths go through one `<Glass>` component so no call site branches. Note
the documented trap: setting `opacity: 0` on a `GlassView` kills the effect
entirely — animate with the built-in `animate`/`animationDuration` in
`glassEffectStyle` instead of via opacity.

### 8. Motion canon

Inherited from the desktop, extended for touch:

1. **Breathe only while listening.** Bars static at rest. This is a law, not a
   preference.
2. **Ember is earned.** Coral appears only for agent work and live listening. Never
   ambient, never decorative chrome.
3. **Amplitude drives the waveform.** While listening, bar heights come from real
   metering, not from a keyframe loop.
4. **Transforms only.** Animate `transform` and `opacity`. Never animate `width` or
   `height` on the waveform — that is the exact width→transform lesson already paid
   for on the web client (see memory: animation wave 2).
5. **Reduced Motion** collapses the waveform to a static amplitude bar and disables
   the Deck's spring, but **never** disables the amplitude *response* — that is
   information, not decoration.
6. **Haptics mark state transitions, not events.** Light impact on listen-start,
   soft on transcript-landed, rigid on cancel. Not per word, not per bar.

### 9. Typography and restraint on the canvas

The canvas holds three text elements and no more:

| Element | Style | Content |
|---|---|---|
| Mark | `type.label`, `text3`, uppercase | `SCOUT` |
| Greeting | `type.title1`, `text1` | "Good morning, AJ." |
| Live line | `type.bodySm`, `text2` | One sentence about what's live. Absent if nothing is. |

If nothing is live, the line is **absent**, not "Nothing live." Empty states that
narrate their own emptiness are noise. The quiet page stays quiet.

### 9.5 The silent path

A voice-first app that only works if you speak is a broken app. Most users are
silent most of the time — in an open-plan office, on a train, in a meeting, with a
sleeping baby nearby — and some users are always silent. **Silence is the common
case, not the accessibility case.** Designing it as an afterthought would be both an
accessibility failure and a product failure.

The rule:

> **Every voice affordance has a typed equivalent reachable in the same number of
> taps, in the same place.** The keyboard is never a downgrade path.

| Voice action | Silent equivalent |
|---|---|
| Tap → converse | Tap the Dock's text edge → the composer opens with the keyboard, same Scout loop |
| Hold → dictate | Type in any composer; the mic is a peer control, not the primary one |
| "Open the pricing thread" | Drag up → Threads → tap |
| Scout's spoken answer | Always rendered as text on the canvas first; speech is the optional layer |

Scout's answers are **text-primary**: rendered on the canvas, spoken only if the
conversation loop is active and the device is not silenced. We never build an
interaction whose output exists only as audio.

**VoiceOver.** The waveform is decorative to a screen reader and must be
`accessibilityElementsHidden`. The *state* is what matters, so the Dock carries it:

| State | `accessibilityLabel` | `accessibilityValue` |
|---|---|---|
| idle | "Talk to Scout" | — |
| listening | "Listening" | live-region updates: "Listening, 3 seconds" |
| transcribing | "Transcribing" | — |
| landed | "Transcript ready" | the transcript text |

Amplitude is announced as nothing — a screen-reader user gets *"Listening"* and a
duration, not a stream of bar heights. The `landed` transcript is announced so a
blind user can verify what was heard before it sends.

**Dynamic Type.** The canvas greeting scales; the waveform does not. At
accessibility text sizes the greeting is allowed to push the waveform smaller, to a
44pt floor, and below that the greeting truncates rather than the waveform
vanishing — the waveform is the app's state indicator and must never disappear.

**Reduce Motion** and **Reduce Transparency** are handled in §7–8. Note the rule that
matters: Reduce Motion collapses the *animation* but preserves the *amplitude
response*, because that response is information.

---

## Part IV — Dictation

### 10. Why ours beats Apple's

Apple's on-device dictation is fast, private, and *generic*. It has never heard of
your company. It writes "Dana" as "Donna," your product as two words, and your
internal jargon as nonsense. For work chat — which is 80% proper nouns — that is
the difference between usable and infuriating.

We own the transcription lane (`transcription_lane.go`) and it already supports
**domain-vocabulary biasing** (`domain_terms.go`). So:

> **Dictation is a server round trip, biased with your company's vocabulary.**

The tradeoff is honest: we trade ~1s of latency for correctness on exactly the
words that matter most. For a held-to-dictate paragraph, that trade is
overwhelmingly right — nobody re-reads a dictated message hoping the names are
wrong.

### 11. The state machine

```
   idle ──hold──▶ listening ──release──▶ transcribing ──▶ landed
     ▲                │                       │              │
     └──slide-away────┘                       └──error───────┘
        (cancel, rigid haptic)                   (keep audio, offer retry)
```

| State | Canvas | Dock |
|---|---|---|
| `idle` | Static bars, `text2` | Glass at rest |
| `listening` | Bars track amplitude, **ember** | Expanded, ember ring |
| `transcribing` | Bars settle; words rise from them | Progress shimmer |
| `landed` | Text in the target field | Collapses, soft haptic |
| `error` | Bars fade to `text3` | Retry affordance — **audio is retained** |

**Errors never discard audio.** A failed upload that loses the user's paragraph is
unforgivable; the recording stays on disk and the retry re-uploads it.

### 12. Contract

Client records to `.m4a` with metering enabled, then:

```
POST /assistant/transcribe        (multipart)
  audio: <m4a blob>
  context: "chat" | "board" | "search"    // reserved — see note
  threadId?: string                        // ledger attribution
→ 200 { text, durationMs, model, biased }
```

`biased` reports whether company-vocabulary biasing actually applied. A
whisper-family model pin silently degrades dictation to generic transcription, and
surfacing it means the client can tell the difference rather than leaving the user
to wonder why names are suddenly wrong.

`context` is accepted but **not yet used** — Wave 1 applies the full company
vocabulary to every dictation. It is carried now so the client contract is stable
when per-surface narrowing lands; wiring it half-way would make some surfaces
transcribe measurably worse than others for no stated reason.

Server reuses the existing lane. **Two traps already paid for in this codebase and
re-stated here so they are not re-learned:**

- The `prompt` (vocabulary bias) parameter is **gated to the gpt-4o family** —
  whisper-family models reject it live with a 400. Prod pins
  `OPENAI_TRANSCRIPT_MODEL`, so the endpoint must check the resolved model before
  attaching bias. (`transcription_lane.go:954-957`)
- Duration must be billed to the usage ledger per-minute like every other
  transcription lane, or dictation becomes an unmetered cost leak.
  (`models_pricing.go:137`)

### 12.5 No signal, and the audio you didn't mean to send

Server-side transcription buys correctness (§10) and pays for it with a hard
dependency on the network. Two consequences must be designed, not discovered.

**Offline.** Recording is local and always works — the network is only needed to
*resolve* a recording into text. So a dropped connection degrades, it never blocks:

```
release ─▶ upload ──ok──▶ text lands
              │
              └──no network──▶ pending transcript
                               (bubble shows a waveform + duration,
                                marked "will transcribe")
                               ─▶ retries on reconnect ─▶ text replaces it
```

A pending transcript is a **real message in the thread**, not a lost draft — it
holds its place in the conversation, shows its duration, and fills in when the
network returns. Recordings are held in the app's document directory (not cache, so
iOS cannot evict them) and are deleted only after a transcript is confirmed
persisted. **The user's paragraph is never lost**, which is the whole point of §11's
`error` state retaining audio.

If the phone stays offline long enough that the queue exceeds ~10 pending items or
25MB, we stop recording new dictations and say so plainly rather than silently
accumulating.

**Consent.** The app now records audio outside a call, which is a materially
different privacy posture from the WebRTC rooms — and this repo already has a
consent surface (`/api/consent`, `RoomConsentSheet`, `mobile/src/api/consent.ts`)
that establishes the standard. Dictation must meet it:

1. **Explicit start.** Recording begins only on an intentional hold or tap. There is
   no ambient listening, no wake word, no always-on mic. The design has no place to
   put one, which is the strongest guarantee available.
2. **Visible while live.** The ember waveform *is* the recording indicator, plus
   iOS's own orange mic dot. Recording is never invisible.
3. **Transient by default.** Dictation audio is transcribed and the file deleted;
   only the text persists. Dictation audio is **not** added to company memory as
   audio — the transcript is the artifact. This is the opposite of room recordings,
   which are consented and retained, and the difference must be stated in Settings.
4. **First-use disclosure** names the server round trip explicitly: *"Your voice is
   sent to Bonfire to transcribe with your company's vocabulary, then deleted."*
   Burying a server upload behind the system mic prompt would be a dark pattern —
   the system prompt says nothing about where audio goes.

---

## Part V — Messaging

### 13. Why a team would actually leave Slack

Not features. Slack wins on features and will keep winning. The argument is
narrower and sharper:

> **In Slack, the conversation dies. Here, the conversation compounds.**

Three things Slack structurally cannot do, all of which already have backend
support:

1. **Dictation that knows your company** (§10). Slack gets Apple's generic
   dictation, same as everyone.
2. **`@scout` is a participant, not an integration.** `chat_mentions.go` already
   routes `@scout` to the answer path rather than to a notification. The agent is
   *in* the room.
3. **Ask the thread.** Any thread can be queried, not just scrolled. "What did we
   decide here?" against a 400-message channel returns a sourced answer, because
   every message is already in company memory.

The honest gap: **no 1:1 DMs.** Public threads are channels; there is no direct
message model server-side. Real teams need DMs to leave Slack. That is scoped as
the next wave (§17), not hand-waved.

### 13.5 "Ask the thread"

The third claim above is the sharpest one, so it gets a real design rather than an
assertion.

**Invocation.** Tapping the Dock while a thread is open scopes the conversation loop
to that thread (§5). No new control, no new gesture — the Dock already means "talk,"
and context comes from where you are. Typing `@scout` in the composer is the silent
equivalent (§9.5), and it already routes correctly server-side (`chat_mentions.go`
deliberately does not treat `@scout` as a notification target).

**Rendering.** The answer arrives as a message *in the thread*, authored by Scout,
in ember. It is a real message — everyone in the channel sees it, and it becomes
part of the thread's memory. This is the design choice that makes Scout a
participant rather than a private assistant with a shared address, and it is what
`@scout`-in-a-public-channel already implies server-side.

**Evidence.** Every answer carries its sources as tappable chips beneath it —
message anchors within this thread, or other threads and meetings. Tapping scrolls
to the cited message or opens the cited thread. An answer with no sources renders as
an answer with no sources, visibly, rather than borrowing unearned authority. This
is the same evidence-bound-suggestion discipline as the product-direction pipeline:
a claim is only as good as what it can point at.

**Private variant.** Holding the Dock in a thread dictates *into the composer*
instead — so "ask privately" is simply: don't send. There is no separate private-ask
mode to learn, and no risk of a question you meant to keep to yourself appearing in
a public channel, because asking publicly requires the tap gesture, which visibly
posts.

### 14. What changes on the client

The substrate is right; the rendering is wrong. `ScoutScreen` shows threads as
static `Card`s with a "Ask" textarea above — that reads as a search tool, not a
messaging app.

| Concern | Today | Target |
|---|---|---|
| Thread list | `Card` per thread, no unread | Channel rows: `#name`, last message, timestamp, unread dot |
| Messages | Flat list | Bubbles with author identity, own-vs-other alignment, grouped by author |
| Live delivery | Refetch whole thread on any `chat_thread` event | Same event, but append-and-scroll; no full refetch |
| Composer | Plain `TextInput` + Send | Glass composer with mic as a **first-class** control, not an afterthought |
| Mentions | Rendered as plain text | `@name` styled and tappable; `@scout` rendered in ember |
| Scout | Separate "Ask" flow | A participant in the thread |

### 14.5 Where unread lives when there is no tab bar

Killing the tab bar (§4) removes the conventional home of the unread badge, and a
messaging app that cannot tell you something is waiting is not a messaging app. This
is the single largest cost of the radical structure, so it gets a direct answer
rather than a workaround.

**The canvas's live line is the unread surface.** §9 reserves one line of `bodySm`
beneath the greeting for "what's live." Unread *is* what's live:

> Three unread in **#pricing**. Dana mentioned you.

It is a sentence, not a badge, and that is deliberate — it can say *who* and *where*,
which a red dot cannot, and it costs no chrome on the quiet page. Tapping it opens
the Deck directly to that thread. When nothing is unread the line is **absent**
(§9), so its mere presence is the signal.

Three supporting affordances, in strict priority order:

| Surface | Carries | Why |
|---|---|---|
| Canvas live line | The full sentence: count, channel, mentioner | Primary. Present only when there is something to say. |
| Dock, at rest | A single ember dot when a **direct mention** is unread | The Dock is always visible, including over the Deck. Mentions only — never volume, or it becomes wallpaper. |
| Deck → Threads rows | Per-channel unread dot and count | The detail view, once you've come looking. |
| iOS app icon badge | Direct mentions only | Matches the Dock's rule, so the phone and the app never disagree. |

The escalation is deliberate: **ambient volume never earns pixels outside the Deck;
only a direct mention does.** This is the `chat_mentions.go` distinction expressed in
the UI — the server already separates "posted in a channel" from "mentioned you,"
and the client should not flatten that back into one undifferentiated red dot.

**Notification deep links** open the Deck at `full`, pushed to the thread, scrolled
to the mentioning message — never to the canvas. A notification is a request to see
one specific thing; landing on the home screen would make the user navigate twice.

### 15. List performance

`@shopify/flash-list` v2 is already a dependency and unused. Threads with hundreds
of messages need it. Bubbles must have a stable `keyExtractor` on message id, and
mention parsing must be memoized per message — parsing every message on every
render is the obvious performance trap in a chat list.

---

## Part VI — Architecture

### 16. Component inventory

**New**

| Path | Purpose |
|---|---|
| `mobile/src/theme/glass.tsx` | `<Glass>` — GlassView / BlurView / opaque, one call site. Encodes §7. |
| `mobile/src/theme/motion.ts` | Durations, easings, reduced-motion helpers. Encodes §8. |
| `mobile/src/voice/useDictation.ts` | Recording, metering, upload, state machine (§11). |
| `mobile/src/voice/amplitude.ts` | dBFS → normalized bar heights. Smoothing, noise floor. |
| `mobile/src/voice/transcriptQueue.ts` | Offline pending-transcript queue, reconnect retry, eviction limits (§12.5). |
| `mobile/src/voice/VoiceConsent.tsx` | First-use disclosure naming the server round trip (§12.5). |
| `mobile/src/components/Waveform.tsx` | The instrument. Amplitude-driven, breathe-law-compliant. |
| `mobile/src/components/Dock.tsx` | The Dock (§5). |
| `mobile/src/components/Deck.tsx` | Detented glass sheet (§4). |
| `mobile/src/screens/CanvasScreen.tsx` | The Canvas (§4, §9). |
| `mobile/src/messaging/` | Channel list, bubbles, composer (§14). |

**Modified**

| Path | Change |
|---|---|
| `mobile/src/navigation/RootNavigator.tsx` | Tab navigator removed; Canvas becomes root. |
| `mobile/src/theme/tokens.ts` | Motion + glass constants; no palette changes. |
| `mobile/app.config.ts` | `expo-audio` plugin, mic usage string, `expo-glass-effect`. |
| `transcribe.go` *(new)* + `main.go` | `/assistant/transcribe` endpoint (§12). |

**Retained unchanged.** The entire `mobile/src/realtime/` WebRTC stack, `RoomScreen`,
`FilesScreen`, `BoardScreen`, `AlertsScreen`, auth, and the API client. This redesign
replaces the *shell*, not the machinery — those screens become Deck destinations.

### 17. Rollout

| Wave | Scope | Gate |
|---|---|---|
| **1 — this branch** | Glass layer, dictation engine, Canvas/Dock/Deck, messaging rebuild | Simulator verification + `/code-review` |
| **2** | Realtime voice-to-Scout on mobile via `/assistant/realtime-offer`; tap-to-converse becomes truly full-duplex | Live device test |
| **3** | Server DM model — 1:1 and ad-hoc groups, read receipts, typing (§13 gap) | Server deploy + tests |
| **4** | Remaining screens (Board, Files, Alerts, Settings) restyled as Deck destinations | Visual audit |

**Wave 1's conversation loop is turn-based, not full duplex.** Tap still means
converse — you speak, Scout answers on the canvas, the mic re-arms — but the
transport is record→transcribe→answer rather than a live WebRTC line, so there is a
beat between turns and you cannot interrupt Scout mid-answer. Wave 2 swaps the
transport for `/assistant/realtime-offer`. **Neither gesture changes meaning
between waves** (§5), which is the property that makes shipping Wave 1 safe: users
learn the correct mapping on day one and only ever experience it getting faster.

### 18. Risks

| Risk | Mitigation |
|---|---|
| Glass unavailable below iOS 26 | Three-way fallback through one component (§7). Verified on the 26.5 sim; fallback path unit-tested. |
| Dictation latency feels slow | Optimistic UI: bars settle and a shimmer appears immediately; text lands when it lands. Never a blocking spinner. |
| Removing tabs strands users | Deck is reachable by drag from anywhere; first launch shows the drag affordance once. |
| Amplitude waveform janks the JS thread | Metering polls at 100ms and drives native-driver transforms only. No per-frame JS layout. |
| Mic permission denied | Dock falls back to keyboard-first; every voice path has a typed equivalent (§9.5). |
| Network loss mid-dictation | Recording is local; a pending transcript holds its place in the thread and fills in on reconnect (§12.5). Audio is never discarded. |
| Users unaware audio leaves the device | First-use disclosure names the round trip explicitly; transcripts persist, audio does not (§12.5). |
| Voice-only interactions exclude silent users | Typed equivalents are peers, not fallbacks; Scout's answers are text-primary (§9.5). |

---

## Open Questions

None blocking Wave 1. Two judgment calls made and documented rather than deferred:

1. **Tap-to-converse in Wave 1** routes to a Scout thread rather than opening a live
   WebRTC line (§17). Rationale: the realtime path needs its own device-tested wave;
   shipping a half-working duplex line would poison the core gesture.
2. **No DMs in Wave 1** (§13). Rationale: public threads genuinely are channels and
   deliver most of the value; a rushed DM model would be a schema we regret.

## Appendix: Lens Attribution

| Section | Lens |
|---|---|
| §1–3 Premise, corollaries, the instrument | Product |
| §4–6 Spatial model, Dock gestures, speech acts | UX / Interaction |
| §7–9 Glass law, motion canon, canvas typography | UX / Interaction |
| §10–12 Dictation rationale, state machine, contract | Technical + Domain |
| §13–15 Messaging argument and rebuild | Product + Domain |
| §16–18 Inventory, rollout, risks | Technical |
