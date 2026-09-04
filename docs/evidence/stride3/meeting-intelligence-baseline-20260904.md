# STRIDE 3.0 meeting intelligence baseline — 2026-09-04

The inherited pipeline is a regression base, not an accepted meeting product. This read-only assessment found a reproducible partial-transcription failure that room health hides. It also found useful ownership and consent boundaries worth preserving while replacing the capture/recovery contract. No physical-device, acoustic-quality, live-provider, or production acceptance is claimed.

## What was measured

An isolated Go overlay exercised the current source-queue, aggregate connection, and room watchdog seams. One source was connected and still completing; a second source was disconnected with its 256-frame queue full. The room was seeded with 6,000 offered frames and 3,000 dropped frames from that failed source, while the healthy peer's most recent completion was one second old.

Command, run from the repository root:

```sh
go test -overlay=/tmp/stride3-masked-source-overlay.json -run '^TestSTRIDE3ActiveSourceFailureMustNotBeMaskedByHealthyPeer$' -count=1 -v .
```

Observed result, exit 1 in 0.675 seconds:

```text
connected=true capturing=true stalled=false failedQueue=256 droppedFrames=3000 offeredFrames=6000
active source loss is masked: healthy peer leaves aggregate connection/capture green despite 3000 dropped source frames
```

The intentionally failing test is `/tmp/stride3-masked-source-probe_test.go`; overlay mapping is `/tmp/stride3-masked-source-overlay.json`; full output is `/tmp/stride3-masked-source-probe.log`. No failing test or production source was added to the frozen release candidate. This is a deterministic synthetic seam test. It does not inject real RTP, play acoustic audio, or prove a measured 60-second network outage. The counters represent the missing-source condition; the queue rejection and false aggregate health are actual current-code behavior.

## Current path and findings

| Stage | Current implementation | Implication |
| --- | --- | --- |
| Human media | `main.go` forwards RTP before optional decoding/analysis. Publication admission and media generation are rechecked. | Retain the separate human-media owner. Intelligence failure must not interrupt the call. |
| Capture | `audio_mixer.go` decodes 48 kHz mono, uses an adaptive speech gate without true pre-roll, and sends consent-bound individual publications to STT. Screen-share audio intentionally does not enter this human-speech lane. | Quiet onsets, shared-room acoustics, and shared-content capture need explicit product/measurement decisions. Current source identity is endpoint identity, not diarization of multiple people around one microphone. |
| STT | `transcription_lane.go` owns one provider connection per publication, an in-memory 256-frame queue, 800 ms silence commits and 15-second maximum segments. Successful transcripts persist with source identity and capture sequence. | Source binding is a sound foundation. Queue acceptance is not durable audio acceptance. There is no inspected replay journal for unacknowledged audio across provider loss or process restart. |
| Recovery | `runOnce` resets prior attribution/segment bindings on reconnect. Pending audio/committed-but-unfinished segments have no inspected recoverable payload; repair clears the pending provider buffer. | A reconnection can restore future capture without recovering the lost words. Status repair alone cannot satisfy the product requirement. |
| Coverage/health | `isConnected` returns true when any child is connected. `room_live.go` has one `lastTranscriptCommitAt`; its whole-room stall starts after 45 seconds, refreshes consent at 90 seconds, rebuilds at 105 seconds, and escalates at 180 seconds. Durable coverage rows omit gaps shorter than 45 seconds. | **Most severe demonstrated gap:** a healthy peer can hide another active source's total loss indefinitely. Known transcript sequence completeness is not completeness of all speech that arrived. |
| Analysis | `brain_worker.go` batches four transcripts or nudges a short exchange at 60 seconds, then requests a markdown write-up. `meeting_digest.go` makes a second model pass for structured cumulative facts; both allow 90-second requests. Event nudges drive normal flow; the five/six-minute intervals are recovery sweeps. | This is a multi-stage lossy summarization path with no measured end-to-end latency or semantic accuracy in this audit. A high-water match means known inputs were processed, not that the decisions are correct or all speech was captured. |
| Grounding | Digest verification checks referenced ID membership, span bounds and participant-name normalization. Live projection omits ungrounded facts/themes and binds exact source revisions. | Useful structural checks, but a real transcript ID can still accompany a wrong interpretation or wrong assignment to a valid participant. Fact precision and attribution require an independent labeled evaluation. |
| Presence | Text Scout is projected ready upon invitation, with voice off and no provider session. Room voice has a separate server-owned invitation and exactly one active provider session/stable SFU output, but is hard-off without a trusted qualification adapter. | A visible text seat is not evidence of listening, comprehension, model availability, or a dependable voice participant. Treat these as distinct product states. |
| Native continuity | `useNativeRoom.ts` handles versioned transcript/intelligence snapshots and reconnect generations; it clears agent participants when reconnecting. | This protects against stale transport state, but native render/tests do not qualify capture, analysis or conversation behavior. |

Evidence anchors in the current source:

- `transcription_lane.go:494`, `:539`, `:664`, `:854`, `:890`: bounded queue, any-child connection, session reset and pending-audio exits.
- `room_live.go:1023`, `:1277`, `:1354`, `:1409`, `:1597`: room-wide counters, durable-completion clock, watchdog and coverage-gap floor.
- `audio_mixer.go:19`, `:50`, `:712`: no pre-roll, identity-preserving source interface, speech gate.
- `brain_worker.go:22`, `:101`, `:268`; `meeting_digest.go:626`, `:1107`; `meeting_intelligence.go:493`: analysis cadence and meaning of current/grounded.
- `room_scout_voice_gate.go:24`, `:58`; `room_scout_transport.go:89`; `room_agents.go:317`: qualification and real runtime ownership.
- `mobile/src/realtime/useNativeRoom.ts:2451`, `:2640`: transcript/snapshot handling and reconnect state.

## Why voice is disabled

The absence of a production verifier is intentional, not a missing environment switch. `room_scout_voice_gate.go` explicitly says configuration is not qualification. `installRoomScoutVoiceQualificationVerifier` has test callers only. `transcription_qualification.go` likewise rejects local synthetic evidence as provider qualification and its declared corpus requirements are not proof that a corpus was completed. Do not enable voice by changing this gate or treating a syntactically valid receipt as acceptance.

Keep the room/sitting/media-generation/consent fences, the independent provider generation, the stable SFU output, and one active session owner. Replace or qualify the provider/presence implementation behind that ownership boundary. Voice evaluation must include interrupted speech, interruption cancellation, duplicate output after reconnect, echo, late joins, session expiry, participant changes, and tool/source authorization at completion.

## Smallest next architectural slice

Build one recoverable meeting-source stream from already-authorized decoded audio through exact transcript segments and coverage. Start with two speakers on two endpoints, one failed STT connection, and a healthy peer. This should precede a visual status patch or voice enablement.

1. Give each admitted publication a stable source identity and monotonically increasing audio sample/chunk sequence, independent of provider sessions. Carry room, sitting, media generation, recording epoch and consent fence. Keep endpoint identity distinct from a claimed human speaker.
2. Introduce a bounded capture journal before the lossy speech gate, with short pre-roll. Record chunk acknowledgement/disposition: pending, transcribed, intentionally excluded, revoked, expired or irrecoverably missing. A proposed retention starting point is encrypted transient storage, deleted after durable acknowledgement, with a hard time/byte ceiling and immediate revocation invalidation. The exact retention policy remains a design decision, not an implemented or approved claim.
3. Let one transcription owner replay unacknowledged eligible chunks after provider replacement. Reauthorize at ingress and publication. Reconcile overlapping provider results by stable source/chunk identities; never publish duplicate words or transfer a speaker binding by arrival order. Process restart must recover queued chunks or persist explicit source/time gaps.
4. Derive per-source coverage and the room aggregate from chunk accounting, not the last successful peer. A degraded source must remain visible while other sources work. Quiet sources must remain distinct from broken sources. Healthy call media should continue without intelligence backpressure.
5. Feed analysis immutable transcript revisions plus explicit per-source gaps. Any correction/revocation invalidates derived claims before they are presented as current. Produce structured decisions/actions directly with exact source edges; compare direct extraction against the existing markdown-then-digest path on a labeled corpus before choosing the winner. Presence should expose listening/catching up/answering/degraded from real state, not invitation alone.

## Proposed gates, not achieved results

| Metric | Baseline in this audit | Next-slice acceptance target |
| --- | --- | --- |
| Hidden partial-source failure | Reproduced: aggregate connected/capturing true with one saturated disconnected source | Zero hidden sources; degraded within 5 seconds of detected failed delivery or overdue acknowledgement, with an explicit source/time gap when recovery cannot cover it |
| Source accounting | Room counts and persisted segments; no inspected replayable audio journal | Every accepted chunk has one durable disposition; zero unaccounted chunks and zero duplicate final segments over disconnect/restart/revocation injection |
| Recovery | Future-connection retry exists; lost-word recovery unproven | A 30-second provider interruption while a peer remains healthy recovers all still-authorized buffered speech within the agreed bounded retention/cost envelope, or identifies the precise unrecoverable interval |
| Final transcript latency | Configuration: 800 ms silence commit / 15-second segment ceiling; real latency unknown | Measure speech-end→durable-final and chunk-start→durable-final separately; preregister p50/p95/p99 targets before provider comparison |
| Word/number/company-term accuracy | No physical or live-provider measurements here | Existing qualification thresholds provide a starting bar: WER ≤10%, number accuracy ≥98%, domain terms ≥97%, with separate quiet, noise, overlap, accent and short-phrase slices |
| Speaker attribution | Source-publication attribution implemented | Zero cross-endpoint swaps in the controlled corpus; shared-microphone speakers remain uncertain unless separately qualified diarization resolves them |
| Decisions/actions | Structural source checks implemented; semantic precision unknown | Human-labeled precision/recall, owner/date accuracy and correction latency; no unsupported decision may become an autonomous action solely because its anchor ID exists |
| Meeting intelligence latency | Two sequential model stages, both with 90-second request bounds | Measure finalized speech→visible corrected fact and speech→grounded answer under load; choose a product SLO from observed distributions, not queue timers |
| Human media isolation | Separate direct RTP path exists; current physical baseline unknown | No added audible interruption/frozen-video regression during STT/analysis faults, verified on actual iPhone/iPad/browser participants |
| Agent presence/voice | Trusted qualification absent; voice correctly unavailable | Independent acoustic/interaction evaluation, one active audio owner, interruption/recovery/authority gates, then a real installed-device room pilot |

The first deliverable should be an instrumented two-source failure/recovery demonstration with a transcript and exact gap accounting, followed by physical corpus results. Existing unit tests and screenshots remain useful regression evidence only.
