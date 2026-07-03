# Spectacular OS — Media/RTC Engineering Plan

**Pillar 1: First-class WebRTC.** Author: Media/RTC Engineer · Date: 2026-07-03

**Scope:** flawless video/audio, stable connections across desktop + mobile simultaneous join, first-class noise suppression (no user training, "intelligently just suppress"), lightweight GPU-cheap video "looks" (no ML segmentation/blur this wave — confirmed), per-device+per-account persisted AV settings with an honest settings surface, and a verification harness that proves it.

**Design principles enforced throughout:** (1) never regress the happy path — every change is additive and guarded; (2) mobile is first-class, not a degraded desktop; (3) the user never wonders "is it on?"; (4) prefer platform capabilities over custom DSP at equal quality; (5) everything measurable.

---

## 0. Executive summary — the five things that matter

1. **The mandated case is broken today by design.** The room keys presence and session identity on the *account's participant name* with a single session slot (`app.participantSessions[name]`, `kanban.go:3888`). When the same account joins from a second device, the newer join overwrites the slot and the older device is ejected on its next message with `session_replaced` (`main.go:2753-2755`). **Same user on desktop + mobile simultaneously cannot both stay in the room.** Different users on desktop + mobile already works. Fix in §1 (multi-endpoint sessions).
2. **Noise suppression is a hand-rolled gate stacked on top of RNNoise and on top of the browser's own NS** — triple-processing that can pump, chop word onsets, and fight itself (`rnnoise-processor.js:241-276`, `index.html:23368-23412`). Recommendation in §2: run RNNoise as a true denoiser (trust its output, drop the custom gate to a gentle VAD-driven floor), stop double-applying browser NS when the worklet is active, and add browser-native `voiceIsolation` as the preferred path on Safari/WebKit where it is genuinely better than our WASM.
3. **There is no video "looks" pipeline at all** — video tracks pass straight through untouched (`index.html:23420-23424`) and there is not even a CSS filter on the tiles. §3 specifies a `MediaStreamTrackProcessor` → `OffscreenCanvas`(WebGL) → `MediaStreamTrackGenerator` pipeline with a CSS-filter fallback, four named looks, and mobile thermal guardrails. The far end must see the look, so a local-CSS-only approach is rejected.
4. **Settings are dishonest.** The code computes rich, truthful processor diagnostics (`voiceFocusProcessorType` `index.html:22896`, `audioProcessorDiagnosticsSnapshot` `index.html:22960`) but the settings UI only shows three radio buttons and a static "adaptive profile" label (`index.html:16093-16136`). The user cannot tell whether voice focus is *active*, *falling back*, *loading*, or *unavailable on this browser*. §4 wires the existing diagnostics into a live status chip.
5. **Recovery is strong for the network path, weak for the device path.** ICE-restart choreography is genuinely good on both client (`index.html:22671-22723`) and server (`main.go:2680-2728`). But an active mic being unplugged mid-call has no `ended` handler, `devicechange` only re-lists devices (`index.html:17265-17269`), and mobile AudioContext suspension on screen-lock is unhandled. §5 closes these.

The good news: the connection-stability *machinery* (ICE restart with backoff + throttle, negotiation watchdog, server-side ICE recovery grace window, Safari currentTime-stall detection, mobile orientation handling) is already in place and well-built. This wave is mostly **making it honest, making it handle two-devices, and adding the two missing user-facing capabilities (looks + true suppression state).**

---

## 1. Stability matrix & gap analysis

### 1.1 What's already solid (verify, don't rebuild)

| Machinery | Location | Assessment |
|---|---|---|
| Client ICE restart, throttle 3.5s, 5 attempts, backoff `[0,1,2,4,8]s` | `index.html:22671-22723`, consts `16911-16916` | Solid. Guards `pc !== sessionPeer` prevent stale-peer callbacks. Keep. |
| Server ICE recovery grace window → server-initiated restart | `main.go:2680-2728` | Solid. `iceDisconnectGrace` lets transient blips self-heal before a restart. Keep. |
| Negotiation watchdog (resend offer, then close stuck peer) | `main.go:2161-2192` | Solid. Emits `media_disconnected` before ejecting so the client shows honest reconnect UI. Keep. |
| Connection-recovery fallback (`leaveRoom` after 20s stuck) | `index.html:22661-22668` | Solid. Keep. |
| Signaling reconnect with elapsed cap | `index.html:16915-16916` | Solid. Keep. |
| Safari live-feed stall detection via `currentTime` progress (not rVFC) | `index.html:29542-29573` | **Confirmed present** (memory note 2026-06-16). Correct fix — rVFC is unreliable for live MediaStream on Safari. Keep. |
| Mobile native-orientation capture (no aspect pin on mobile) | `index.html:16824-16850` | **Confirmed present** (memory note 2026-06-16). Correct. Keep. |
| RTP header-extension preservation (mobile rotation/CVO) | `main.go:2667-2670` | Solid — stripping these made phone video unstable. Keep. |

### 1.2 The scenario matrix

Legend: ✅ works · ⚠️ works with rough edges · ❌ broken.

| # | Scenario | Current behavior | Status | Fix (§) |
|---|---|---|---|---|
| S1 | Desktop Chrome join, solo | Full ladder, RNNoise worklet, orientation pin. | ✅ | — |
| S2 | Desktop Safari join, solo | Same; currentTime-stall guard active; WebAudio worklet path. | ✅ | — |
| S3 | iOS Safari join, solo | Native orientation, no aspect pin; AudioContext gated on gesture. | ⚠️ backgrounding | §5.3 |
| S4 | Android Chrome join, solo | As desktop Chrome; thermal risk with worklet + future looks. | ⚠️ thermal | §2.4, §3.5 |
| S5 | **Different** users, one desktop + one mobile, simultaneous | Distinct participant names → distinct session slots → both admitted. | ✅ | — |
| S6 | **Same** user, desktop + mobile, simultaneous (MANDATED) | Second join overwrites `participantSessions[name]`; first device ejected `session_replaced` on next message. | ❌ | §1.3 |
| S7 | Same user, deliberate device handoff (join mobile, then desktop) | Works *as* the eviction — but silent/abrupt; looks like a bug to the user. | ⚠️ | §1.3, §5 |
| S8 | Network flap (Wi-Fi→cellular) mid-call, mobile | `disconnected`→ICE restart client+server; grace window; usually self-heals. | ✅ | — |
| S9 | Hard network loss > 20s | Client `scheduleConnectionRecovery` → `leaveRoom`; server watchdog closes peer. | ✅ (by design) | §5.1 polish |
| S10 | Active mic unplugged mid-call (USB/BT) | No `track.onended`; `devicechange` only re-lists. Track goes silent, no recovery. | ❌ | §5.2 |
| S11 | Mic switched in settings mid-call | `switchMicrophone` rebuilds; graph rebuilt via `createOutboundAudioForSource`. | ⚠️ verify | §5.2 |
| S12 | Mobile screen-lock / app-backgrounded mid-call | Video repair on visibility-restore only; AudioContext may suspend and not resume. | ⚠️ | §5.3 |
| S13 | Room at capacity, same user re-joins | Capacity check counts by name (`kanban.go:3881`) — second endpoint of same user must NOT consume a seat. | ❌ tied to S6 | §1.3 |
| S14 | Simultaneous join race (two devices within ms) | Session-slot last-writer-wins; one ejected. | ❌ | §1.3 |

### 1.3 The mandated fix — multi-endpoint sessions (S6, S7, S13, S14)

**Root cause.** Identity is modeled as *one participant name = one live session*. Concretely:

- `admitParticipantSession(name, sessionID)` sets `participantCounts[name] = 1` and `participantSessions[name] = sessionID` (`kanban.go:3886-3888`) — a single slot, last-writer-wins.
- `participantSessionCurrent(name, sessionID)` returns true only if the stored slot equals this session (`kanban.go:3924`), and any non-current session is told `session_replaced` and returns (`main.go:2753-2755`).
- Capacity counts distinct names (`activeParticipantCountLocked`, `kanban.go:3881`), so a second endpoint would wrongly not even be counted — but it never gets that far because it evicts itself.

This single-session model is deliberate and *correct for the "one browser tab per person" world* — it's what stops a stale tab from fighting a fresh one. We must not remove that protection; we must let one account hold **multiple concurrent endpoints** while still evicting a *replaced* endpoint (same device, refreshed tab).

**Design: endpoint identity = (participantName, endpointId).**

1. **Client mints a stable per-device endpoint id.** Persist `bonfire.endpoint.id.v1` in `localStorage` (survives reload → a refreshed tab reuses the same endpoint and correctly *replaces* its own prior session; a different device has a different id → coexists). Send it in the `participant` hello alongside the existing session id.
2. **Server keys sessions by endpoint, not name.** Replace the `map[name]sessionID` with `map[name]map[endpointId]sessionMeta`. `admitParticipantSession` adds/updates the endpoint's entry. `participantSessionCurrent` checks the *endpoint's* slot — so refreshing one device still evicts that device's stale tab (same endpointId, newer sessionId), but the other device is untouched.
3. **Presence/counts become endpoint-aware.** `activeParticipantCountLocked` counts distinct *names* for the "room is full for people" message (a person with two devices is still one seat), but the fan-out, roster tiles, and media routing operate per endpoint. This keeps capacity semantics human ("6 people") while allowing multi-device.
4. **Roster display.** Two endpoints of one account render as one roster identity with a subtle "· 2 devices" affordance; each still gets its own video tile (you genuinely want to see both a person's laptop cam and phone cam if they publish both — or, more commonly, one publishes video and the other is audio-only in-pocket).
5. **Self-echo guard.** Two endpoints of the same account in the same room can loop audio (phone speaker → laptop mic). Because the server already avoids loopback per-peer (`main.go:2230-2237` drops receiving your own senders), extend that to drop tracks whose *source endpoint* differs but whose *account* matches the subscriber **only when both endpoints are co-located** — actually simpler and safer: default the *second* endpoint of a same-account join to **audio-muted playback of the first endpoint's tracks** and surface a one-tap "this is my other device" chip. Do not auto-mute the mic (they may want the phone as the mic and laptop as the screen). This is the one genuinely new UX decision; keep it minimal.

**Guardrails.** Cap endpoints per account (2 this wave) to bound fan-out on the 4GB VPS. Every existing single-session test (`participants_test.go:91-133`) must still pass — the endpointId defaults such that the *same* endpoint replacing itself behaves exactly as today. Add tests in §6.

**Why not "just allow a second tab freely"?** Because `session_replaced` is load-bearing against zombie tabs. Endpoint identity keeps that protection intact while adding the axis (device) the mandate needs.

---

## 2. Noise suppression, first-class

### 2.1 What ships today, precisely

The pipeline in `createOutboundAudioForSource` (`index.html:23427-23522`) is:

```
mic track → highpass(155Hz) → compressor(-40/4:1) → [RNNoise worklet | WebAudio worklet | scriptProcessor] → lowpass(6.8kHz) → destination → outbound track
```

and *simultaneously* the capture constraints request the browser's own `echoCancellation` + `noiseSuppression` + `autoGainControl` + `voiceIsolation` + all the legacy `goog*` flags (`index.html:23368-23412`). Then inside the RNNoise worklet, after RNNoise WASM denoises the frame, the code applies **its own** gate (`this.rnnoiseGate`), hold-frame logic, a heuristic `forcedNoiseFrame()` VAD, and a `noiseBias` subtraction (`rnnoise-processor.js:241-276`).

**Three problems:**

1. **Triple processing.** Browser NS + RNNoise denoise + custom gate all run. Each is tuned assuming it's the last stage. Stacked, they cause pumping (gain fighting), chopped word onsets (the hold logic vs. RNNoise's own attack), and a "swimming" noise floor.
2. **Gating a denoiser.** RNNoise's whole value is that it *attenuates noise while passing speech continuously* — it is not a gate. Wrapping it in `this.rnnoiseGate` that slams to `floorGain` (0.0015) when the heuristic VAD disagrees throws away RNNoise's quality and reintroduces the exact choppiness RNNoise avoids (`rnnoise-processor.js:261-266`).
3. **`voiceIsolation` is requested but not strategized.** It's set as `{ ideal: voiceFocusEnabled() }` (`index.html:23374`) meaning it rides the *same* toggle as our WASM and runs *in addition* to it. On WebKit/Saf 17+ and recent Chrome, the platform `voiceIsolation` (macOS Voice Isolation / Chrome's ML NS) is often *better* than our WASM and runs on the platform's budget — but we're stacking ours on top rather than choosing.

### 2.2 Recommended pipeline — "intelligent, no training"

**Principle 4 (prefer platform at equal quality) decides the browser split:**

| Browser | Best suppressor | Strategy |
|---|---|---|
| Safari / iOS WebKit | Platform `voiceIsolation` (macOS/iOS Voice Isolation) | Request `voiceIsolation:{ideal:true}` + `noiseSuppression:{ideal:true}`; **do not** run our WASM worklet on top. Our scriptProcessor fallback there is the weakest path anyway. |
| Chrome / Edge desktop | RNNoise WASM as **true denoiser** (gate removed) | Disable browser `noiseSuppression` (`{ideal:false}`) when our worklet is active, so we don't double-suppress; keep `echoCancellation:true` (browser AEC is excellent and orthogonal). |
| Android Chrome | Platform NS (CPU budget) by default; RNNoise opt-in | Default to browser `noiseSuppression:true`; offer "voice focus (uses more battery)" as an explicit opt-in with the thermal guardrail (§2.4). |
| Firefox | RNNoise WASM denoiser | Same as Chrome desktop. |

**Key change inside the worklet:** demote the gate to a *gentle VAD-driven floor*, not a slam-to-zero. Concretely, in `rnnoise-processor.js:261-266`, replace the `targetGate ∈ {floorGain, 1, 0.32}` ladder with `targetGate = 0.6 + 0.4·smoothedVAD` clamped to `[0.5, 1.0]`, and remove the `noiseBias` spectral-subtraction on top of RNNoise output (`rnnoise-processor.js:267,272`) — RNNoise already removed the noise; subtracting again just dulls consonants. Keep the `forcedNoiseFrame()` heuristic **only** as a comfort-noise ducker for sustained non-speech (keyboard, fan) at a *soft* -12dB, never full mute. This is the difference between "gate that cuts you off mid-word" and "denoiser that just makes noise quietly disappear."

**AEC stays browser-native everywhere** — never attempt our own; the constraint `echoCancellation:{ideal:true}` is correct and orthogonal to NS.

### 2.3 Default-ON policy

- Default mode becomes **"voice focus (intelligent)"** — which resolves per-browser to platform `voiceIsolation` (Safari/iOS) or RNNoise-denoiser (Chrome/Firefox) — **on by default** on desktop.
- On mobile, default to **"standard cleanup"** (platform NS only) to protect battery, with voice-focus as a clearly-labeled opt-in. This honors principle 2 (mobile first-class = *right* default, not *degraded*).
- Preserve the three-mode radio (`index.html:16093-16118`) but relabel: **voice focus (intelligent)** / **standard cleanup** / **raw mic**. The mode is the *intent*; the *mechanism* (platform vs WASM) is chosen automatically and reported honestly (§4).

### 2.4 CPU/thermal budget on mobile

- The WASM worklet processes 480-sample frames (`rnnoise-processor.js:126`) at 48kHz — ~100 frames/s of WASM VAD+denoise. On a mid Android this is real but affordable *alone*; combined with a video looks pipeline (§3) it's the thermal risk.
- Guardrail: a shared **thermal governor** (§3.5) that, on sustained high frame-processing latency or `navigator.userActivation`/battery signals, first drops the video look, then falls the worklet back to platform NS, updating the status chip honestly ("voice focus paused to save battery"). Never silently keep burning.

### 2.5 Honest state reporting

The worklet already posts `metrics` with `processor: 'rnnoise-wasm' | 'voice-focus-fallback' | 'rnnoise-loading'` (`rnnoise-processor.js:368-378`) and the client already derives `voiceFocusProcessorType()` (`index.html:22896-22912`). §4 surfaces these as the four honest states.

---

## 3. Video "looks" pipeline

### 3.1 The core decision: pipeline, not CSS-only

Requirement: **the far end must see the look.** A CSS `filter:` on the local `<video>` (or even the tile) only affects local rendering — remote peers receive the raw camera track. Therefore CSS-only is **rejected** for the shipped look. CSS filters are retained *only* as the local-preview mirror hint and as the graceful-degradation fallback when the capture pipeline can't run (§3.4).

**Chosen pipeline (GPU-cheap, no ML):**

```
camera track
  → MediaStreamTrackProcessor (readable frames)
  → OffscreenCanvas + WebGL2 fragment shader (the "look" LUT/curve)
  → MediaStreamTrackGenerator (writable) → processed video track
  → this track is what we addTrack to the PC
```

This runs entirely on the GPU via a fragment shader (one draw call per frame), is available in Chrome/Edge/Android-Chrome, and degrades cleanly where unavailable. It slots in at `createLocalMediaStream` (`index.html:23418-23424`) where video tracks currently pass through untouched — we wrap the video track exactly as audio is already wrapped, keeping the change additive and symmetric with the existing audio design.

**Safari note:** `MediaStreamTrackProcessor`/`Generator` are not in Safari yet. Safari uses the **canvas-capture fallback**: draw the `<video>` to a `<canvas>` on a rAF loop with the WebGL shader and `canvas.captureStream(30)`. Slightly higher overhead than insertable streams but works, and Safari desktop has the thermal headroom. iOS Safari defaults looks **off** (§3.5).

### 3.2 The four named looks (concrete recipes)

All implemented as a single parameterized fragment shader (brightness/contrast/saturation/temperature/gamma/vignette/soft-clip uniforms) so switching looks is a uniform swap, not a pipeline rebuild:

| Look | Intent | Recipe (shader uniforms) |
|---|---|---|
| **Bonfire warm** (default) | Flattering, on-brand warmth | temp +8 (toward amber), saturation ×1.08, contrast ×1.05, gamma 0.96, soft highlight roll-off, subtle 6% vignette. |
| **Studio** | Clean, neutral, crisp | contrast ×1.12, saturation ×1.02, mild sharpen (unsharp via 3×3), temp 0, black-point lift +2%. |
| **Mono** | Editorial black & white | luma (Rec.709 weights), contrast ×1.15, gentle S-curve, film-grain 2% optional. |
| **Low-light boost** | Rescue a dim room | gamma 0.78 (brighten mids), adaptive gain toward a target mean luma, denoise-lite (bilateral-cheap 3×3), saturation ×1.04 to counter wash-out. |

A **"none / raw"** option always exists and is the only guaranteed-correct path — selecting it tears down the pipeline entirely so there's zero overhead when unused.

### 3.3 Per-device + per-account persistence

Extend the settings schema (§4) with `video.look` (`'none'|'bonfire-warm'|'studio'|'mono'|'lowlight'`) and `video.lookIntensity` (0–1 to scale all uniforms toward identity). Persisted with the same per-device+per-account dual-key pattern as audio (`index.html:17440-17453`).

### 3.4 Graceful degradation

Three-tier fallback, each with an honest status:
1. **Insertable streams** (Chrome/Android/Firefox-ish) → full GPU look, far-end sees it.
2. **canvas.captureStream** (Safari desktop) → same look, higher cost.
3. **Neither available OR pipeline errors** → look disabled for the outbound track; apply the equivalent CSS filter to the **local preview only** so the user sees *something* representative, with the status chip stating "looks not supported on this browser — preview only." Never claim the far end sees a look it doesn't.

Any pipeline exception (context loss, `VideoFrame` close errors) must catch → tear down → fall to raw camera track → status chip → never a black tile. Mirror the audio design's defensive try/catch (`index.html:23454-23504`).

### 3.5 Battery/thermal guardrails (mobile)

- iOS Safari: looks **default off**; available as explicit opt-in labeled "may reduce battery."
- Android: looks available; the shared **thermal governor** monitors per-frame processing time (from the pipeline) and `navigator.getBattery()` where present. On sustained budget breach: intensity → 0, then look → none, then (if still hot) audio worklet → platform NS. Each step updates the chip. Restore automatically when cool for 60s.
- Hard cap output at capture resolution/30fps; never upscale in-shader.

---

## 4. Settings persistence & honesty

### 4.1 Schema (extends `bonfire.audio.settings.v1`, bump to v8)

Current schema (v7) holds `{mode, version, profile:{noiseFloor,speechFloor,trainedAt}, preferredInput:{deviceId,groupId,label}}` (`index.html:17381-17399`), dual-keyed per-account + global (`index.html:17427-17453`). Extend to a unified **AV** settings record:

```jsonc
{
  "version": 8,
  "audio": {
    "mode": "voice-focus" | "standard" | "off",   // intent
    "preferredInput": { "deviceId", "groupId", "label" },
    "profile": { "noiseFloor", "speechFloor", "trainedAt" }  // legacy adaptive; retained
  },
  "video": {
    "look": "none" | "bonfire-warm" | "studio" | "mono" | "lowlight",
    "lookIntensity": 0.0-1.0,
    "preferredCamera": { "deviceId", "groupId", "label" }
  }
}
```

**Migration:** v7→v8 wraps the flat fields under `audio` and defaults `video`. Keep `parseStoredAudioSettings` tolerant (`index.html:17381`) — unknown/old versions degrade to defaults, never throw.

### 4.2 Local vs server

- **Local (per-device):** everything above. Device ids, look choice, and mode are inherently device-specific (a phone and a laptop want different defaults) — they stay in `localStorage`, dual-keyed by account so a shared device keeps per-account prefs (`index.html:17432-17438`).
- **Server (per-account, optional this wave):** a *soft* default — "preferred look" and "preferred mode" as account-level hints synced via the existing settings path so a *new* device inherits a sensible starting point, then the device's local choice takes over. Keep this additive; if the server sync 503s (keyless local dev), local settings fully drive. Do not block media on any server round-trip.

### 4.3 The honest status surface (the core UX of principle 3)

Today the "audio & video" section (`index.html:16085-16137`) shows radios + a static "adaptive profile" label. Replace the static label with a **live status chip** driven by the *already-computed* diagnostics:

| Chip state | Condition (from existing code) | Chip copy |
|---|---|---|
| **Active — voice focus (RNNoise)** | `voiceFocusProcessorType()` = `rnnoise-wasm` (`index.html:22897`) | "Voice focus active · RNNoise" |
| **Active — voice focus (platform)** | mode=voice-focus resolved to platform `voiceIsolation` | "Voice focus active · this browser's isolation" |
| **Falling back** | `= rnnoise-fallback` / `voice-focus-fallback` | "Voice focus degraded — using heuristic cleanup" |
| **Loading** | `= rnnoise-loading` | "Voice focus starting…" |
| **Standard** | mode=standard | "Standard cleanup · browser noise suppression" |
| **Off** | mode=off | "Raw mic · no added cleanup" |
| **Unavailable on this browser** | worklet + platform both unsupported | "Voice focus isn't supported here — using standard cleanup" |

Same pattern for video looks: **Active (far end sees it)** / **Preview only (not supported here)** / **Paused (battery)** / **Off**. The distinction "far end sees it" vs "preview only" is the honesty-critical one from §3.4.

Wire it from the existing `metrics` message flow (`rnnoise-processor.js:368`, consumed by `updateVoiceFocusDiagnostics` `index.html:22914`) — the chip is a thin render of state that already exists. Add a one-line live suppression meter (input vs output RMS → `suppressionDb`, already computed at `index.html:22917-22919`) so the user *sees* it working. No new plumbing, just surfacing.

### 4.4 "Re-enable needed" honesty

When a device is switched or the graph rebuilds and the worklet has to reload, the chip shows **Loading** then resolves — never a stale "Active." When a browser silently drops `voiceIsolation` support after an update, the resolved processor type reflects reality on next graph build. The rule: **the chip is a function of live state, never of the persisted intent.**

---

## 5. Failure & recovery UX

### 5.1 Network reconnect choreography (mostly present — polish)

The user-visible ladder already exists via `setConnectionState` (`index.html:22595-22608`, `22717`):
`connected → "reconnecting…" (disconnected) → "reconnecting media N/5" (active ICE restart) → "media stalled; rejoin" (exhausted, 22679-22681)`.

Polish: (1) don't show the "N/5" counter until attempt ≥2 (a single blip self-heals in <1s and the counter looks alarming); (2) when the server sends `media_disconnected` (`main.go:2183`), show a single clear "reconnecting — hold on" and auto-attempt rejoin *once* before surfacing a manual "Rejoin" button; (3) surface the button only after auto-recovery is genuinely exhausted (`iceRestartAttempts >= maxIceRestartAttempts`, `index.html:22678`). Auto-recover silently; escalate to a button only when the user must act.

### 5.2 Device switch / unplug (the real gap — S10, S11)

- **Add `track.onended` on the active source mic/camera.** Today unplugging the active USB/BT mic silently kills audio with no recovery — there is no `ended` handler anywhere on the source track. On `ended`: pick the next preferred device (reuse `preferredInput` matching, `index.html:17410`), rebuild the outbound graph via `createOutboundAudioForSource`, `replaceTrack` on the sender (no renegotiation needed), and show "microphone changed — switched to <name>."
- **`devicechange` should do more than re-list** (`index.html:17265-17269`). If the *currently active* device disappeared from the list, trigger the same recovery path. If a *more-preferred* device appears (user plugged in their good mic), offer a non-intrusive "switch to <name>?" chip — do not auto-yank mid-sentence.
- **Verify `switchMicrophone` rebuilds the full graph** (S11) — it must tear down the old worklet nodes (`destroy` message, `rnnoise-processor.js:51`) to avoid leaking AudioWorklet processors on every switch. Audit and add a teardown assertion in the harness.

### 5.3 Mobile backgrounding / screen-lock (S12)

- Today only *video* is repaired on visibility-restore (`index.html:17250-17254`). Add: on `visibilitychange → hidden` on mobile, note the time; on `→ visible`, if the AudioContext is `suspended`, `resume()` it (pattern exists at `index.html:30107`) and re-verify the outbound track is live, rebuilding if the OS reclaimed the mic (iOS aggressively does on lock).
- iOS Safari suspends AudioContext on lock and may end the capture track. On restore, if the source track is `ended`, run the §5.2 recovery. Show "resuming audio…" briefly rather than a dead-air mystery.
- Never *publish* a frozen track: if on restore the camera track is `muted`/`ended`, drop to placeholder and recover, so remote peers see "camera reconnecting," not a frozen last-frame.

### 5.4 Multi-endpoint handoff UX (from §1.3)

When a user's second device joins, the first device shows a calm "you joined from another device" chip (not the alarming `session_replaced` ejection). If they truly meant to hand off, the chip offers "leave here." If they meant two devices, both stay. This turns S7 from a silent eviction into an intentional, legible moment.

---

## 6. Verification harness — proving flawlessness

Principle 5: if we claim flawless, a harness proves it. Three layers.

### 6.1 Go tests (must pass in `go test ./...`, VPS has no Go so this is the gate)

Extend the existing suite (`ice_recovery_test.go`, `negotiation_watchdog_test.go`, `transport_hardening_test.go`, `simulcast_test.go`, `participants_test.go`):

- **`endpoint_session_test.go` (NEW, the mandated case):**
  - Same name + two distinct endpointIds → both admitted, both current, neither `session_replaced`.
  - Same name + same endpointId, new sessionId → old session *is* replaced (refresh-tab semantics preserved).
  - Capacity: same-account two endpoints = one seat (`activeParticipantCountLocked` counts names).
  - All **existing** `participants_test.go:91-133` assertions still green (backward-compat proof).
- **`negotiation_watchdog_test.go`:** add a two-endpoint-of-one-account renegotiation case (no stale-answer cross-talk between a person's two devices).
- **Frontend-contract tests** (`frontend_latency_test.go`, `assistant_http_test.go` style): assert the new status-chip strings and the v8 settings-migration branch exist in `index.html` (these Go tests grep the frontend, e.g. `frontend_latency_test.go:937`), so a refactor that drops honest-state copy fails CI.

### 6.2 Node smoke scripts (extend the existing harnesses)

Existing: `scripts/live-media-smoke.mjs`, `media-fix-e2e-call.mjs`, `voice-focus-benchmark.mjs`.

- **`scripts/multi-endpoint-smoke.mjs` (NEW):** two Playwright contexts authenticated as the *same* account (reuse the keyless two-context recipe from the CompanyOS comms memory note), both join, assert both remain in roster >30s, no `session_replaced` console event, both media tracks flowing. This is the automated proof of S6.
- **`voice-focus-benchmark.mjs` (extend):** add before/after suppression-dB and *speech-onset preservation* metrics (feed a known "keyboard + speech" WAV, assert consonant energy retained vs. the old gate — proves §2.2 fixed the choppiness, not just the noise floor).
- **`scripts/video-look-smoke.mjs` (NEW):** run each look through the pipeline in headless Chrome, capture the *outbound* track (via a loopback PC) and assert the far-end frame differs from raw by the expected per-look signature (mean luma up for low-light, saturation ~0 for mono) — proves "far end sees the look," not just local preview.

### 6.3 Manual device matrix (the checklist that gates deploy)

A living checklist in this doc; every row must be green before the wave ships to `thebonfire.xyz`:

| Device / browser | S-cases to run | Pass criteria |
|---|---|---|
| macOS Chrome | S1, S8, S10, S11 | Join clean; unplug mic → auto-switch <2s; ICE restart on Wi-Fi drop recovers <5s. |
| macOS Safari | S2, S12 | currentTime-stall guard holds (no flicker); voiceIsolation path reported honestly in chip. |
| iOS Safari | S3, S12 | Backgrounding/lock → resume audio+video on return; looks default off; no dead air. |
| Android Chrome | S4, S8 | Thermal governor drops look before device gets hot; standard-cleanup default. |
| **Same account: macOS + iPhone** | **S6, S7, S13** | **Both stay in room; one seat counted; no eviction; "other device" chip shown.** |
| Different accounts: laptop + phone | S5 | Both full media, independent. |

**Pass bar for "flawless":** every matrix row green, all Go tests green, all three smoke scripts green, and the honesty chip verified to match ground truth on each browser (force RNNoise failure via a bad wasm path — the existing `forceSafariMedia=1` style flag at `index.html:16822` is the pattern — and confirm the chip says "degraded," not "active").

---

## 7. Working inside the 34.9k-line monolith (operability)

- All new JS lands as guarded additions near the existing audio graph (`index.html:23418-23522`) and settings render (`index.html:16085-16137`) — same neighborhood, same idioms, no build step.
- The video-look shader lives in a new `public/voice-focus/`-sibling asset dir `public/video-looks/look.frag` + a small `video-look-pipeline.js` (mirroring how `rnnoise-processor.js` is a separate worklet file) so we don't inflate the monolith with GLSL and it's independently cacheable.
- Every change is feature-flag-guarded (`video.look === 'none'` = zero new code path; multi-endpoint gated so a client that doesn't send endpointId behaves exactly as today) → happy path never regresses (principle 1).
- Keyless-local (`go run .` on :3000) must keep working: looks and suppression are pure client-side; multi-endpoint is server logic that needs no OpenAI key. Do not couple any of this to Scout/`OPENAI_API_KEY`.
- Do not touch the native Apple `/native/config` contract; the endpoint-id field is additive and optional.

---

## 8. Sequencing (for /wave-plan)

1. **Wave A — Multi-endpoint sessions (the mandate).** Server endpoint-keyed sessions + client endpointId + tests + `multi-endpoint-smoke.mjs`. Highest priority, unblocks the mandated case. Pure stability, no UI risk.
2. **Wave B — Honest suppression.** Demote the RNNoise gate to a denoiser floor, per-browser strategy (stop double-processing), default-ON policy, and the live status chip. Ships the "intelligently just suppress" + honesty requirements.
3. **Wave C — Video looks.** Pipeline + four looks + thermal governor + persistence + `video-look-smoke.mjs`.
4. **Wave D — Device/recovery hardening.** `track.onended`, devicechange recovery, mobile backgrounding, handoff chip.

A depends on nothing; B/C/D are independent of A and each other (can parallelize after A). Each wave has its own green-harness gate before merge.

---

## Appendix: file:line index of every claim

- Single-session eviction root cause: `kanban.go:3871-3894` (admit), `3924-3939` (current-check), `main.go:2753-2755` (`session_replaced`), capacity `kanban.go:3881`.
- Audio graph: `index.html:23418-23522`; constraints `23368-23412`; mediaConstraints `16859-16886`.
- RNNoise worklet gate/denoise/VAD: `rnnoise-processor.js:241-276` (gate), `278-327` (heuristics), `368-378` (metrics).
- Settings UI (radios + static label): `index.html:16085-16137`; persistence `17367-17475`; schema version `16817`.
- Diagnostics already computed (unused by UI): `voiceFocusProcessorType` `22896-22912`, `updateVoiceFocusDiagnostics` `22914-22936`, `audioProcessorDiagnosticsSnapshot` `22960-22984`.
- Video passthrough (no look): `index.html:23418-23424`; no CSS filter on tiles (confirmed absent).
- ICE restart client: `index.html:22671-22723`, consts `16911-16916`; server recovery `main.go:2680-2728`; watchdog `2161-2192`.
- Safari currentTime stall guard: `index.html:29542-29573`. Mobile orientation: `16824-16850`.
- Backgrounding/devicechange handlers: `index.html:17250-17269`; AudioContext resume `30107`.
- Existing harnesses: `scripts/live-media-smoke.mjs`, `media-fix-e2e-call.mjs`, `voice-focus-benchmark.mjs`; tests `ice_recovery_test.go`, `negotiation_watchdog_test.go`, `transport_hardening_test.go`, `simulcast_test.go`, `participants_test.go`.
