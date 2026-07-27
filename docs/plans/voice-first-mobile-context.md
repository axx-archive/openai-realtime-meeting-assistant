# Voice-First Mobile — Context Brief

> Authored 2026-07-27 on branch `design/voice-first-mobile`. Single-author design
> (no specialist swarm): this is a taste-driven first-principles problem where a
> coherent single voice beats a four-agent collage. The specialist *lenses*
> (product, UX, domain, technical) are applied in sequence by one author and the
> result is held to the same critic gate.

## Mission

BonfireOS is a voice-first OS for running a company. You talk to your team, you
talk to agents, and agents talk to each other. The system builds company memory,
connects storylines across meetings, and turns conversations into coordinated
action. **You don't operate it like software. You just talk.**

The iOS app today does not express any of that. It is a competent React Native
port of the web dashboard: a five-tab shell (Home / Rooms / Chat / Board / More),
lists of cards, and a text box with an "Ask" button. It has no voice capability
outside a WebRTC call, no real messaging surface, and no Liquid Glass. It is
software you operate — the precise opposite of the product thesis.

This brief scopes a redesign from first principles: the phone becomes the purest
expression of the thesis, because the phone is where a microphone is always four
inches from your mouth.

## Current System

**Mobile app** — `mobile/`, Expo SDK 57, React Native 0.86, React 19.2, TypeScript 6.
Bundle `xyz.thebonfire.app`, build 16, deployment target iOS 16.4. 66 TS/TSX files.

| Area | File | State |
|---|---|---|
| Shell | `mobile/src/navigation/RootNavigator.tsx` | 5-tab bottom bar + native stack. Tab bar is a flat `glassPanel` rgba fill, not real glass. |
| Home | `mobile/src/screens/HomeScreen.tsx` | Dashboard: greeting, three metric tiles, three navigation cards. |
| Chat | `mobile/src/screens/ScoutScreen.tsx` | Thread list rendered as static `Card`s + a textarea with an "Ask" button. Not messaging. |
| Thread | `mobile/src/screens/ThreadScreen.tsx` | Message list + text composer + attachment picker. Refetches whole thread on every office event. |
| Tokens | `mobile/src/theme/tokens.ts` | Glass & Ink palette mirrored from web `:root`. `DynamicColorIOS` light/dark. Ember marked "earned only". No motion tokens. |
| Chrome | `mobile/src/components/Screen.tsx` | Title + subtitle + ScrollView wrapper used by every screen. |
| Realtime | `mobile/src/realtime/` | Mature WebRTC room stack (SFU, ICE recovery, codec negotiation). **Does not touch `/assistant/realtime-offer`** — there is no voice-to-Scout path on mobile at all. |

**Installed capability gaps:** no `expo-audio`, no `expo-glass-effect`, no speech
module. `expo-blur` is installed but unused for material.

**Server** — Go, root of repo, deployed at `thebonfire.xyz`.

| Capability | Location | Note |
|---|---|---|
| Channels | `scout_chat_threads.go` | Public-visibility Scout threads **are already channels** — `#`-prefixed titles, broadcast notifications (`:1620`). |
| @-mentions | `chat_mentions.go` | Word-boundary roster parsing → targeted bell notifications. `@scout` gates the answer path instead of notifying. |
| Transcription | `transcription_lane.go` | `gpt-4o-transcribe` default, **with domain-vocabulary biasing** via `domain_terms.go`. Prompt param is gated to the gpt-4o family (whisper rejects it). |
| Realtime voice | `/assistant/realtime-offer` (`main.go:968`) | WebRTC offer/answer for live Scout conversation. Web only today. |
| Live events | `OfficeEventsContext` ← `/websocket` | Office-wide event bus already wired into the mobile app. |
| Attachments | `/assistant/attachments`, `/assistant/files/upload` | Multipart upload path exists. |

**The load-bearing discovery:** every backend capability the vision needs already
exists. This is overwhelmingly a **client** problem.

## Design Requirements

1. **Voice is the shell, not a feature.** The tab bar is removed. The app opens on
   a live waveform canvas that *is* the home. Ratified by the user 2026-07-27.
2. **Inherit the desktop motion canon.** `index.html` establishes the
   *breathe-only-while-listening law*: waveforms rest **static**; they animate only
   while the pipeline genuinely listens. Ember is **earned** — agent work and
   ignition only, never ambient chrome. Mobile must not weaken this.
3. **Beat native iOS dictation.** The differentiator is server-side transcription
   with company-vocabulary biasing: it must spell teammates' names, product names,
   and internal jargon correctly where Apple's on-device dictation cannot.
4. **Liquid Glass with a stated law**, not decoration. `expo-glass-effect` requires
   iOS 26; deployment target is 16.4, so every glass surface needs a defined
   fallback. Simulator available for verification is iOS 26.5 / Xcode 26.6.
5. **Messaging good enough to replace Slack for a work team**, built on the
   existing public-thread substrate. No new server messaging model this wave.
6. **Time-to-first-word is the performance metric.** Cold launch → able to speak
   must feel instant. Nothing may block the canvas mount.
7. **Reachability.** One-handed operation on a 6.9" phone. Every primary control
   lives in the bottom third.
8. **Accessibility is not optional.** A voice-first app must work for users who
   cannot or will not speak: every voice affordance needs a typed equivalent, and
   the waveform needs a non-visual state announcement.
9. **Scale:** single company, tens of users, hundreds of threads. Correctness and
   feel outrank throughput.
10. **Deliverable:** thesis + built spine on `design/voice-first-mobile`, verified
    running in the iOS Simulator. Remaining screens follow in a later wave.

## What The Team Needs To Produce

Single author; the lenses below are applied in sequence and synthesized into
`docs/plans/voice-first-mobile-design.md`.

| Lens | Owns | Key Questions |
|---|---|---|
| Product | The premise and the spatial model | What replaces tabs? What is the home screen *for*? Why would a team abandon Slack? |
| UX / Interaction | Gestures, states, motion, glass law | How does one surface serve two voice intents? What does the waveform mean at rest? Where is glass legitimate? |
| Domain | Speech → structure | What are the distinct speech acts in running a company, and which surface catches each? |
| Technical | Feasibility and contracts | Audio session, metering→amplitude pipeline, transcription endpoint contract, fallback matrix, list performance. |

## Team Roles

- **Lead / sole author:** this session. Owns the brief, the thesis, all revisions,
  and the build.
- **Critic gate:** `critic-loop` scored against the pinned goal text — the design is
  not accepted on the author's own judgment.
- **Code gate:** `/code-review` on the diff plus Simulator verification before ship.
