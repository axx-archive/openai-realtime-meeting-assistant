# Multi-Room Support — Final Synthesis Spec (2026-07-08)

**Status:** APPROVED FOR IMPLEMENTATION — judge-synthesized from Design A (minimal-delta) + Design B (isolation-first) + security critique. Every critical/major security change is folded in (one adapted, justified in §6.4).

**Verdict:** Design A (minimal-delta) is the base. Its explicit `Kind=="guest"` guard in the session resolver and its write-time guest event allowlist are the two mechanisms the security critique proved indispensable, and its wave order (record layer before media plane) fences the cursor-corruption class before a second room can ever write. Grafted from Design B: the per-sitting **ListenOnly latch** on the meeting record, the **/guest/me** boot-resume endpoint, the **migration dress-rehearsal boot test**, and the `applyMeetingBoardAnalysis` refusal backstop. Grafted beyond both: fragment-carried guest tokens (`/g#<token>`) so the bearer token never appears in any server/proxy log or Referer header.

**Hard rules for every implementer (non-negotiable):**
- Other Claude sessions share this working tree. NEVER `git checkout/reset/stash/clean/revert`. Do not touch the untracked `design-system/` dir. Additive edits only; rebase-don't-force-push.
- `go test ./...` must be green at the end of every wave. Each wave leaves the app shippable (the feature stays dark until the client waves light it up).
- The client stays one no-build `index.html`. All new boot-reachable client state is `var`-declared or deferred behind the `/auth/me` await (`frontend_boot_tdz_test.go` law; TDZ outage class of 2026-07-05).
- Frontend behavior is pinned by `TestIndex*` static tests over `index.html` source; update pins in the SAME wave as the markup they pin.
- This plan does NOT deploy; the orchestrator commits and pushes at the end.

---

## 1. Objective mapping

| # | Objective | Delivered by |
|---|-----------|--------------|
| 1 | Rooms list w/ live indicator + counts, open, create | §4 rooms.json + `GET/POST /rooms` + `rooms` office event (W1, W3, W5) |
| 2 | Optional per-room passcode | bcrypt hash on room record, checked once at ws admission (W1, W3, W5) |
| 3 | Shareable guest link → ONE room, name-gated, server-enforced allowlist | §5 guest sessions + §6 security model (W1, W3, W6, W7) |
| 4 | Listen-only pipeline when guests present/enabled | §7 ListenOnly latch + 3-layer enforcement (W2, W4) |
| 5 | Existing office = default room, zero data loss | §9 read-side rule "absent roomId == office" (W2, W7 dress rehearsal) |

---

## 2. Architecture overview

- `kanbanBoardApp` stays the company-OS **singleton** (board, memory JSONL store, artifacts, notifications, accounts, company-tier workers). Only the media/presence/sitting plane is extracted into a per-room struct. Instantiating a full kanbanBoardApp per room would fork the board/memory stores and break objective 5 — forbidden.
- New `roomManager` registry (`map[string]*liveRoom`, RWMutex) hung on kanbanApp. `liveRoom` owns everything that is per-room at runtime (§4.2).
- Guests are a new **session Kind** in the existing `data/sessions.json`, never `userAccount`s (`seedMissingAccounts` reaps non-roster accounts at boot — a guest account would be deleted or, worse, granted the member surface). Because `userFromRequest` explicitly returns nil for guest records, every one of the ~45 session-gated endpoints rejects guests with zero further work: allowlist-by-construction, plus an exhaustive route-walk test (W7) so future endpoints fail closed.
- Ambient workers stay singleton goroutines; their cursors/baselines/nudges/locks become `(agentName, roomID)`-keyed. This is the one structurally unavoidable deep change (`agent_runner.go:609-661` — one room's brain pass must never advance the cursor past another room's transcripts).
- Expensive media (mixer, transcription lane, Realtime peer) becomes **lazy for every room including office**: created in `noteMeetingAdmission` on first seat, torn down after `endMeetingForIdle`'s existing 4-minute grace, fenced by a per-room `mediaGen` counter. Deliberate behavior change: ends today's always-on boot-started OpenAI Realtime spend. Scout voice starts concurrently with the join handshake so the first-join delay is invisible.
- One account may be live in **one room at a time**: joining room B `session_replaced`-evicts the account's room A seat (global name index in roomManager). This keeps every name-keyed cleanup path (`removeParticipantTracksLocked`, `replaceExistingParticipantSessionEndpoint`, `forgetParticipantSessionResult`) safe without adding roomID to every key — the half-measure there reproduces the 2026-07-06 flickering-tile incident class.

---

## 3. Data shapes

### 3.1 `data/rooms.json` (new; roomStore in `rooms.go`, mutex + write-tmp-then-rename like sessions.json)

```go
type roomRecord struct {
    ID           string            `json:"id"`            // "office" for default; else "room-"+hex(crypto/rand 8B)
    Name         string            `json:"name"`
    CreatedBy    string            `json:"createdBy"`     // account email
    CreatedAt    time.Time         `json:"createdAt"`
    PasscodeHash string            `json:"passcodeHash,omitempty"` // bcrypt (accounts.go idiom); "" = no gate
    GuestEnabled bool              `json:"guestEnabled"`  // flips true when first guest link minted; see §7 latch
    GuestLinks   []guestLinkRecord `json:"guestLinks,omitempty"`
    Archived     bool              `json:"archived,omitempty"`     // unjoinable, data retained; office never archivable
}
type guestLinkRecord struct {
    ID        string    `json:"id"`        // first 8 hex of Hash — revoke handle, safe to list
    Hash      string    `json:"hash"`      // sha256 hex of the 64-hex token; the raw token is NEVER stored
    Label     string    `json:"label,omitempty"`
    CreatedBy string    `json:"createdBy"`
    CreatedAt time.Time `json:"createdAt"`
    Expires   time.Time `json:"expires"`   // default 7 days (B's tighter default, not A's 14)
    Revoked   bool      `json:"revoked,omitempty"`
}
```

Boot: if rooms.json is missing, seed `{ID:"office", Name:"the office", GuestEnabled:false, PasscodeHash:""}`. No passcode ever appears on the office room by default — one-click join preserved.

### 3.2 `sessionRecord` extension (`auth_http.go:30-33`)

```go
type sessionRecord struct {
    Email     string    `json:"email"`
    Expires   time.Time `json:"expires"`
    Kind      string    `json:"kind,omitempty"`      // "" == "user" (legacy rows keep working; zero logout on deploy); "guest"
    RoomID    string    `json:"roomId,omitempty"`
    GuestName string    `json:"guestName,omitempty"` // sanitized display name WITHOUT the "Guest " prefix
}
```

- Guest sessions live in the SAME sessions.json (reuses persistence + expiry sweep) under a SEPARATE cookie `bonfire_guest` (HttpOnly, SameSite=Lax, Secure-when-TLS via the existing setSessionCookie flags, Path=/, **TTL 12h**), so a signed-in member clicking a guest link never clobbers their real session; when both cookies exist the member cookie wins.
- **CRITICAL (security-required):** `userFromRequest` gains an explicit `if rec.Kind == "guest" { return nil }` guard. Do NOT rely on the implicit empty-Email → `findUser("")==nil` invariant. This one line is the allowlist guarantee for all ~45 protected endpoints.
- New resolver `guestFromRequest(r) *guestPrincipal{SessionKey, RoomID, Name}` reads `bonfire_guest` only and requires `Kind=="guest"`.

### 3.3 `meetingRecord` extensions (`meetings.go`)

```go
RoomID     string `json:"roomId,omitempty"`     // empty on read == "office"
ListenOnly bool   `json:"listenOnly,omitempty"` // per-sitting latch, §7
```

### 3.4 Memory entries

`appendEntryForMeeting` stamps `metadata.roomId` alongside `meetingId` on every NEW entry. All readers treat absent roomId as `"office"`. `data/meeting-memory.jsonl` is never rewritten.

---

## 4. Room model

### 4.1 Persistence — see §3.1. HTTP surface (all member-session + `websocketOriginAllowed` gated unless noted):

- `GET /rooms` → `[{id, name, live, participantCount, passcodeRequired, guestEnabled, createdBy, archived}]` (never the passcode hash or link hashes)
- `POST /rooms {name, passcode?, guestAccess?}` → creates config; NO media started
- `POST /rooms/{id}/passcode {passcode|""}` set/clear (bcrypt)
- `POST /rooms/{id}/archive` (office rejected)
- `POST /rooms/{id}/guest-links {label?, ttlHours?}` → mint (§5.1); `GET .../guest-links` list (ID/label/created/expires/revoked only); `POST .../guest-links/revoke {id}`

### 4.2 Runtime — new `room_live.go`

`type liveRoom` owns, extracted from today's package globals (main.go:59-88) and kanbanBoardApp fields:

- SFU plane: `peerConnections` slice + signaling debounce timer + negotiation watchdog bookkeeping; `trackLocals/trackParticipants/trackParticipantSessions/trackSourceIDs/trackLayerRIDs/trackLayerGroups`, `subscriberLayerTiers`, `subscriberKeyframeThrottle`. `signalPeerConnectionsWithRestart`, `addTrack/removeTrack`, `dispatchKeyFrame` become room methods. ONE global 3s keyframe ticker iterates the registry's live rooms.
- Presence: `participants` liveness stamps, `participantCounts`, `participantEndpoints`, `participantMedia`, per-room capacity; `guestRoster map[guestSessionKey]guestSeat{DisplayName, EndpointID}`.
- Lazy media: `mixer *audioMixer` (nil until first admission), `lane *meetingTranscriptionLane` (per-room sink key `realtimeMixedAudioSinkKey+":"+roomID`), `rt *realtimeBundle{pc, events, inputTrack, inputEnc, restarting}` (nil forever for listen-only rooms), `mediaGen uint64` fencing teardown vs restart/rejoin.
- Attribution: `audioActivity` frames, `pendingAttributionWindows` FIFO, `activeSpeaker*` state (fed by the room's mixer activity listener).
- Fan-out: per-room socket set with per-socket principal class (`member|guest`); room chat history + `roomChatSeenIds`; `assistantStatus`; `transcriptRecordingEnabled/UpdatedAt/By`.

Scout's realtime output voice track registers ONLY into its room's track table. `broadcastAssistantEvent` is threaded with room identity (closures in OnTrack/mixer/realtime lifecycle capture their room).

### 4.3 Per-room sitting spine (`memory.go`, `meetings.go`)

- `store.meetingID` → `meetingIDs map[string]string` keyed by roomID; `ensureMeetingID/rotateMeetingID/rotateMeetingIDIfCurrent` take roomID, preserving the exact compare-and-clear semantics per room (the 2026-07-08-hardened race guarantees — port the existing tests to run per room and cross-room).
- Boot resume scans the newest non-bookkeeping entry PER roomId (absent == office). `reconcileMeetingRecordsAtBoot` runs per room; a room deleted while its meeting was open closes the record with `reason=restart`.
- meetingStore: `openRecordIndexLocked(roomID)`; `idleTimers map[string]*time.Timer` + `idleGenerations map[string]uint64`; `startMeeting`'s defensive close only closes SAME-room records; `noteMeetingAdmission(roomID, name)` / `noteMeetingOccupancy(roomID)` / `endMeetingForIdle(roomID, gen)` keep the hasEndedRecord re-mint + conditional-rotation guarantees verbatim per room. `activeParticipantCount(roomID)`.
- `brain_intake.go`'s ungated transcript writes (empty expectedMeetingID) are pinned explicitly to `roomId="office"` so intake material can never land in a guest room's live meeting.

### 4.4 Lazy media lifecycle (rides the sitting seams; applies to office too)

- First admission (`noteMeetingAdmission`): lazily create mixer (`newAudioMixer`, activity listener = the room's attribution state) + transcription lane; create the Realtime peer ONLY if the sitting is not listen-only (§7). Capture `mediaGen`.
- `endMeetingForIdle` (existing 4m grace): run the close-flush chain, rotate the room's meeting id, auto-archive, THEN tear down media: `lane.close()`, realtime close + cancel proactive restart, `mixer.close()`, bump `mediaGen`. The proactive-restart loop (kanban.go:877) and lane reconnect loop capture `mediaGen` and exit when it moves — the fence against teardown-vs-restart races. Rejoin during grace cancels teardown; rejoin after teardown restarts media (test under `-race`).
- OnTrack decode submits to the connection's captured room mixer; nil-mixer frames dropped (join racing teardown).
- Pin with a test: no OpenAI Realtime session (mock API asserts zero dials) exists before first admission and none ever for a listen-only room.

### 4.5 Websocket routing

- Client dials `/websocket?room=<id>`. Room + principal resolved BEFORE upgrade (main.go:3558-3574): members validate the room exists and is unarchived (missing `?room` == office for mid-deploy back-compat — the version-gated auto-refresh reloads stale tabs); guest principals have the room FORCED from `session.RoomID` (`?room` mismatch → 403 pre-upgrade). Neither-principal → 401 pre-upgrade (cheap-reject preserved).
- Passcode travels in the participant hello (additive JSON field — never in URL/logs), checked once at admission: bcrypt compare + `authAttemptAllowedForKeys(["roompass:"+roomID, clientIP])`; failure reuses the existing `access_denied` seam with reason `passcode`. The passcode is admission-only — NEVER an API credential anywhere else (May-audit oracle lesson, participants.go:96-98).
- One-account-one-room eviction per §2. Guest seats are keyed by guest session key (two guests named Sam never evict each other). `room_ping` liveness, zombie reap, endpoint caps work unchanged per room (seat identity is socket/session-scoped).
- `broadcastKanbanEvent(roomID, event, data)` iterates that room's sockets; room-scoped payloads (`participants`, `participant_joined/left`, `participant_track`, `active_speaker`, `meeting`, `memory_transcript`, `room_chat`) gain `roomId`. `broadcastOffice/SignedIn` tiers unchanged and structurally guest-free (guests never hold office sockets) — but see §6.2 for the write-time backstop. New `rooms` event on the office tier carries the rooms-list snapshot on every create/join/leave/reap/archive. `broadcastServerShutdown` + `sendServerBuildVersion` iterate the registry.

---

## 5. Guest model

### 5.1 Token lifecycle (clones share_links.go)

Mint: 32 bytes crypto/rand → 64-hex token, returned ONCE as `https://host/g#<token>` (fragment — see §6.3). Stored as `guestLinkRecord{ID: hash[:8], Hash: sha256hex(token), Expires: now+7d default}`. Minting the first link flips `room.GuestEnabled=true`. Redeem validation = constant-time hash compare per candidate + expiry + revoked + room-not-archived, re-checked on EVERY redeem (share_links.go:187-208 idiom). Expired links swept on the session-persist seam.

### 5.2 Redemption + name entry

- `GET /g` serves index.html bytes with headers `Referrer-Policy: no-referrer` and `Cache-Control: no-store`. No token validation at serve time (no 404 oracle).
- Legacy path shim: `GET /g/<64hex>` → 302 to `/g#<same token>` (path→fragment conversion) so pasted path-form links still work; this handler must not log the token (see §6.3 ops note).
- `POST /guest/join {token, name}` — public, `websocketOriginAllowed`-checked (it sets a cookie), rate-limited `authAttemptAllowedForKeys(["guestjoin", clientIP])`. Validates the token per §5.1; sanitizes name (trim, 1–40 runes, printable only, strip control chars); **rejects names that collide with a seeded roster name** — compare `strings.EqualFold` against `participantNameForEmail` over the seeded roster, NOT `canonicalParticipantName(name)!=""` (that predicate is non-empty for any non-blank string and would reject everyone — security-critique fix). Mints the guest session, `Set-Cookie bonfire_guest`, returns `{roomId, roomName, guestName}`.
- `GET /guest/me` (guest-cookie only) → `{roomId, roomName, guestName, live, participantCount}` for reload/deploy-refresh resume (graft from B; fixes the deploy-reload ejection).
- Display name = `"Guest "+name`, deduped with a numeric suffix against the room's guestRoster. **The "Guest " prefix is enforced server-side at admission and stamped into transcript/attribution records** — never applied only at the display layer — so a guest naming themselves "Erick" can never be attributed in the record as the member Erick (security-critique fix). The existing `participantEmail` "guest " carve-out treats it as email-less.
- The guest link deliberately WAIVES the room passcode: the link is itself a member-minted, revocable, expiring capability; a second secret adds friction without security (whoever shares the link would share the passcode in the same message). The passcode gates members-at-large; the token gates guests.

### 5.3 Endpoint allowlist (exhaustive — everything else 401s because userFromRequest returns nil)

`GET /g` (+ path shim), `POST /guest/join`, `GET /guest/me`, `GET /websocket` (guest principal ⇒ room forced from session), plus the already-public statics `/`, `/public/`, `/sw.js`, `/healthz`, `/readyz`. Guests do NOT get `/participants`, `/client-config`, `/auth/*`, or anything else. **Hardening in the same wave: `/native/config` becomes session-gated** (it currently leaks the full member roster unauthenticated — unacceptable once outsiders hold links to the origin). Token-gated public endpoints (`/a/`, `/deal-room/`, `/archives/`, `/artifacts/render`, `/calendar/event.ics`) remain HMAC/token-scoped and are NOT broadened for guests. Signed-out `/participants` keeps only the legacy office seat-count; named rooms are never enumerable pre-auth.

### 5.4 Websocket rules for guests

(a) Only the `participant` hello is accepted — the `office` hello returns `access_denied` and closes. (b) Identity = guest session (name from GuestName, seat keyed by SessionKey); name in the hello payload is ignored. (c) Passcode check skipped (§5.2). (d) The admission replay branches BEFORE the board/memory/notifications/codex_proposals sends (main.go:3948+): guests receive only `access_granted, participants, room_chat_history, meeting, server_version` (+ signaling). (e) Write-time event allowlist per §6.2. (f) Guests may send `room_chat` (rate-limited §6.5) and signaling; every other inbound event kind from a guest socket is dropped and logged.

### 5.5 Client guest boot (single-file, TDZ-safe) — detail in §8.3.

---

## 6. Security model (every critique requiredChange folded)

### 6.1 Pre-upgrade connection caps — the PC-allocation DoS (critique major #1)

The known deferred pre-hello PeerConnection-allocation DoS must not widen from 6 trusted accounts to anyone holding a link. In `websocketHandler`, enforced BEFORE any PeerConnection/transceiver/ICE allocation:

- For **guest principals**, the PeerConnection is not allocated until AFTER the participant hello is admitted (defer the alloc for the guest branch — the strongest fix; members keep today's flow).
- Numeric caps, checked pre-upgrade, reject with 429: max **2** concurrent sockets per guest session; max **5** admitted guests per room (env `BONFIRE_MAX_GUESTS_PER_ROOM`); max **4** concurrent pre-hello/unadmitted sockets per client IP across all guest sessions. Guest sockets count against room capacity at guest-session granularity (many sockets under one "Guest Sam" cannot each hold a seat).
- Counters decrement on socket close; the zombie reap frees stuck slots.

### 6.2 Write-time guest event allowlist (critique major #5)

`threadSafeWriter` carries principal class (`member|guest`). `sendKanbanEvent` enforces, at write time, a guest event ALLOWLIST: `{access_granted, access_denied, session_replaced, server_version, participants, participant_joined, participant_left, participant_track, active_speaker, meeting, room_chat, room_chat_history, offer, answer, candidate}`. Any other event written to a guest socket is dropped and counted (metric/log). This is the belt-and-suspenders that survives future mis-routed broadcasts, since guests necessarily share the media fan-out pool. W7 asserts it against recorded guest writers across artifact/notification/proposal/shutdown/deploy-refresh broadcasts.

### 6.3 Token secrecy (critique major #3)

- Canonical guest URL carries the token in the **fragment**: `/g#<64hex>`. Fragments are never sent to the server, so the token cannot land in server/Caddy access logs or the Referer header of any subresource/POST. This is stronger than the critique's minimum (path + scrub + no-referrer).
- `GET /g` responds with `Referrer-Policy: no-referrer` + `Cache-Control: no-store` anyway (defense in depth for the shim redirect page and any future embed).
- Path shim `GET /g/<token>` 302s to the fragment form; that ONE request can hit proxy logs — the handler itself never logs the token, and **ops note (docs/rooms runbook): configure Caddy to drop or truncate `/g/` paths in access logs on next deploy touch**. 7-day expiry + one-tap revoke bound the residual.
- Client: the fragment is `history.replaceState`-stripped **after successful join** (pre-join it stays for reload safety; fragments leak only to local browser history). After join, reload/deploy-refresh resumes via the `bonfire_guest` cookie + `GET /guest/me` regardless of URL (fixes critique minor: deploy hard-reload to `/` re-enters the guest room instead of dumping the guest on the member login gate).
- `copyRoomLink` copies the canonical internal `/?room=<id>` URL, never `location.href`, so members can't accidentally re-share admission.

### 6.4 Guest-content provenance + company-rollup inclusion (RATIFIED by AJ 2026-07-09)

**RATIFIED: listen-only-sitting content FLOWS INTO company-global rollups, provenance-stamped.** AJ's directive: the entire point of running guests through external rooms is that the meeting's memory reaches the brain — "how was Erick's meeting with X person from NBC?" must get a Scout answer. Mechanics:
- Every entry from a listen-only sitting carries provenance via the meeting record (`meetingListenOnly(meetingID)` lookup helper; brains/digests also stamp `metadata.listenOnly=true` at write) — the stamps are the durable record that content originated in an external-guest meeting, and downstream consumers (digests, recall answers) SHOULD surface that origin.
- Day digest, company digest, entity ledger, and reflection workers INCLUDE listen-only-sitting entries in their input windows exactly like member-only material.
- The per-meeting tier is unchanged: the listen-only room's own meeting record, meeting digest, recap ("fill me in"), decision ledger entries, narratives, artifacts, and archive all exist and are recallable by members.
- ACCEPTED RESIDUAL RISK (deliberate, ratified): guest-spoken (prompt-injectable) content can reach the full-mode office room's recall surface, where Scout has voice + tools — the indirect-injection escalation the security critique flagged. Mitigations that remain: provenance stamps make origin visible/filterable at any read site; listen-only rooms themselves still get zero proactive actions (§7.3); Scout's action surfaces treat recalled content as data, not instructions (house prompt doctrine). Re-quarantining is a read-side toggle: re-add the filter in the four workers keyed on the stamps.

### 6.5 Guest-driven cost bounds (critique major #2)

- **Chat:** per-guest-session token bucket on `room_chat` (burst 5, refill 1 per 3s) enforced server-side before `recordRoomChatMessage`; oversize messages already capped by `maxRoomChatMessageRunes`.
- **Transcription ceiling:** per-sitting cap on listen-only rooms — env `BONFIRE_GUEST_ROOM_TRANSCRIPTION_CAP_MIN` (default 120 minutes of lane-active time per sitting). On hit, `transcriptRecordingEnabled` auto-flips false with `By="system:guest-cap"` and members in the room see the existing recording-off state; a member flipping it back on grants another cap window. Reuses existing machinery, no new UI.
- **Guests-only deferral:** when a room's live participants are guests-only (zero member seats), scheduled brain passes for that room are DEFERRED (nudges accumulate; transcription continues); the close-flush chain still runs one bounded pass. An unattended guest cannot drive unbounded summarization spend.

### 6.6 Identity + resolver hardening

- Explicit `Kind=="guest" → nil` guard in `userFromRequest` (§3.2).
- Guest name roster-collision check fixed (§5.2) + server-side "Guest " prefix at attribution/transcript time (§5.2).
- `/native/config` session-gated (§5.3).

### 6.7 The route-walk allowlist test (critique requiredChange #8)

W7 builds the test from the ACTUAL mux registrations (main.go:555-629): iterate every registered route, present a minted guest session, assert 401/403 everywhere except the explicit §5.3 allowlist. New endpoints added later fail closed (test fails until the author consciously allowlists). Also asserts `/native/config` 401s signed-out and that the token-gated public endpoints keep their existing HMAC/token scoping.

---

## 7. Pipeline policy — listen-only

### 7.1 Decision: per-sitting latch (graft from B)

`meetingRecord.ListenOnly` is set true when (a) the sitting starts (`startMeeting`/first `noteMeetingAdmission`) and the room has any unexpired, unrevoked guest link (guest-enabled), OR (b) the first guest is admitted mid-sitting. **Once true it never unlatches within that sitting** — a guest leaving (or links being revoked) mid-meeting must not let proactive workers act on a window guests contributed to. Helpers: `roomListenOnly(roomID)` (live: guest-enabled or guests present) and `meetingListenOnly(meetingID)` (record lookup, used by workers over historical windows). Office ships GuestEnabled=false → behaves exactly as today. Escape hatch: revoke all the room's links; the NEXT sitting returns to full mode.

### 7.2 What keeps running in listen-only (the record-building tier — do NOT over-suppress)

Transcription lane + speaker attribution, brain worker (subject to §6.5 deferral), meeting record + mission-intel auto-title, narrative maintainer, decision ledger, meeting digest, recap/catch-me-up, close-flush chain, auto-archive + archive artifact. Company-global rollups (day/company digest, entity ledger, reflection) also run over listen-only material, provenance-stamped (§6.4, ratified).

### 7.3 What is suppressed — three layers, independently tested

1. **Window filter (primary):** `board_worker.produceMeetingBoardUpdate` and `suggestion_agent.produceResearchSuggestions` share `filterListenOnly(entries)` — entries whose `metadata.roomId`/meeting resolve to a listen-only sitting are EXCLUDED from the analysis window while the cursor/baseline still advances past them (the suggestion agent's own skip-while-advancing idiom, suggestion_agent.go:184). Muting the brain's A3 nudge alone is NOT enough — the board worker's 2-minute ticker floor still fires; the filter must live at window selection.
2. **Choke-point backstops:** `proposeCodexTask` (codex_proposals.go — the shared seam for both proactive workers) stamps `metadata.originRoomId` at mint and REJECTS (logs + no-op) proposals from a listen-only origin, so its everyone-notification ("confirm to launch" — the primary Scout nudge surface) can never fire from a guest room and every FUTURE caller inherits the gate. `applyMeetingBoardAnalysis` refuses mutation ops (create/update/move/add_tags/add_key_date/propose_codex_task) for listen-only sources (graft from B). `workflow_ticker.launchApprovedProposal` declines listen-only-origin proposals (proposals minted pre-guest still launch).
3. **Not-started:** the Realtime Scout peer is never created for listen-only sittings (no voice, no grill — cheaper AND quieter); `rememberTranscript` skips the Scout wake pulse; `/assistant/realtime-offer` and `/assistant/realtime-tool` bind to the caller's CURRENT room and refuse listen-only rooms (also fixes the room-agnostic hole where a member in room A could attach the assistant to room B's mixer).

### 7.4 Room-partitioned agent bookkeeping (the make-or-break)

`unconsumedEntriesAfter` gains a room dimension: the cursor for `(agent, room)` = newest artifact-of-kind stamped with that roomId; inputs filtered by roomId; legacy artifacts without roomId are the OFFICE cursors (office pipeline resumes seamlessly post-deploy); a brand-new room starts baseline-at-now (never re-consumes history). `startAmbientAgent` keeps singleton goroutines; baselines/nudge-channels/failure-backoff/run-locks become `(agentName, roomID)`-keyed; nudges carry roomID; each tick iterates rooms with unconsumed input. `flushAmbientAgentsForClose(reason, roomID)` flushes only the closing room's chain under per-(agent,room) locks (two rooms closing concurrently neither serialize nor deadlock) and SKIPS the board stage for listen-only sittings (mirrors the existing research-suggestion exclusion, agent_runner.go:530-533). `recap(roomID)`: force that room's brain pass, read that room's meeting snapshot, deliver via room-scoped fan-out — never `broadcastSignedInKanbanEvent` (fixes the current office-wide leak). Mission-intel auto-title + narrative segments resolve THE ROOM's active record.

---

## 8. Client plan (index.html, no build)

### 8.1 Rooms list (members)

Lives on the existing office home (officeTool section, ~20356-20415) as a "Rooms" card block above Morning Brief — no new TOOL_IDS rail slot (a rail slot can be added later without rework). State: `var roomsList = []`, hydrated by `GET /rooms` inside the refreshAuthState success fan-out (AFTER the `/auth/me` await — no TDZ exposure) and kept live by the new `rooms` office-socket event (one new case in handleKanbanMessage). Row: name, live dot + "N inside", lock glyph (passcodeRequired), guest badge (guestEnabled), Open (disabled for the room you're in), overflow menu (invite guest → mint + one-time copy of the `/g#` URL; set/clear passcode; archive). "+ New room" inline form (name, optional passcode, allow-guests toggle) → `POST /rooms`. `renderRoomLandingState` becomes per-selected-room.

### 8.2 Join/open + event scoping

- `var activeJoin = {roomId:'office', passcode:'', guest:false}` (module-level var; reconnects re-send the same room identity + passcode — the reconnect re-seating trap; cleared only in `leaveRoom`). Every existing one-click join affordance (`#join`, `[data-join-room]`) keeps working via the office default.
- `openRoomWebSocket` dials `/websocket?room=${activeJoin.roomId}`; the participant hello gains `passcode: activeJoin.passcode || undefined`. Wrong passcode surfaces through the existing `access_denied` → `handleAccessDenied` seam on the prejoin card. Switching rooms = leaveRoom then open (is-in-room guard stays).
- Room-scoped events (`participants`, `meeting`, `memory_transcript`, `room_chat`, `active_speaker`, `participant_joined/left`) carry `roomId`; `handleKanbanMessage` drops them when `(payload.roomId || 'office') !== activeJoin.roomId` — a second live room can never overwrite this tab's roster, meeting title/clock, or transcript. Rooms-list counters come only from the `rooms` event.
- `copyRoomLink` → `location.origin + '/?room=' + activeJoin.roomId` (never location.href). `?room=` boot param preselects a room after auth.

### 8.3 Guest boot

- Boot block (~23320-23354), var-only: `var guestBootToken = (location.pathname === '/g' || location.pathname.indexOf('/g/') === 0) ? (location.hash.match(/^#([a-f0-9]{64})$/) || [])[1] || '' : '';` plus `var guestMode = Boolean(guestBootToken) || document.cookie.indexOf('bonfire_guest=') !== -1;`
- When guestMode: add `is-guest` class to `#appShell`; SKIP `refreshAuthState`/`loadParticipantPreview`/the 15s poll entirely (no authed fetches ever fire); if no token but a guest cookie exists, probe `GET /guest/me` → resume straight to the name-confirmed prejoin (deploy-refresh survival, §6.3).
- `renderLoginMode('guest')`: free-text `#guestNameInput` + single join button — no roster select, no password, no passkey/forgot chrome; `hasValidAccess`/`validateAccess` accept guest-name-present in guest mode.
- Join: `POST /guest/join {token: guestBootToken, name}` → `var guestSession = {roomId, guestName}` → `joinRoom({guest:true})` (skips the inline `/auth/login` branch, dials `?room=<roomId>`); on success `history.replaceState` strips the fragment.
- Guests never set `authedUser` (all ~99 gates stay closed), never open the office socket (guard at ~24232 holds), `setActiveTool` pinned to 'room' with back-stack pushState suppressed. `leaveRoom`/`handleAccessDenied`/`handleSessionReplaced` gain guest branches to a terminal "you've left the room" card — never `setActiveTool('office')`; board/participant refetches skipped.
- `is-guest` CSS hides `.tool-rail`, topbar bell/account menu, scout rail, board rail, and all data-tool surfaces except the room stage + meeting bar + room chat panel (guests keep chat — it feeds the transcript, which listen-only keeps; server enforces everything else).

### 8.4 Test pins (same wave as markup)

New `TestIndexRooms*`/`TestIndexGuest*` pins: `?room=` in the ws dial; `activeJoin`/`guestBootToken` var-declared in the boot block; passcode field in the hello JSON; roomId filter in the kanban router; copyRoomLink no longer copies location.href; guest branch skips refreshAuthState; ensureOfficeSocket guard excludes guest mode; is-guest CSS blocks; guest leave never calls setActiveTool('office'); /guest/me resume probe present. Update every existing TestIndex* pin that greps the bare `/websocket` dial or hello shape in lockstep. `frontend_boot_tdz_test.go` stays green.

---

## 9. Migration — zero data loss, zero rewrite

Invariant everywhere: **absent roomId == "office"**.

1. `data/rooms.json` seeded at boot with the office room if missing. No passcode on it; one-click join preserved.
2. `data/meeting-memory.jsonl` never rewritten; new appends stamp roomId; every reader (snapshotForMeeting, meetingMemoryDetails, digests, boot resume, agent cursors) defaults absent→office. All historical transcripts/brains/digests/artifacts/archives remain fully recallable.
3. `data/meetings.json`: `RoomID`/`ListenOnly` omitempty; existing records read as office, full-mode.
4. `data/sessions.json`: Kind/RoomID/GuestName omitempty; legacy rows = user; **no member logged out on deploy**.
5. Legacy agent artifacts without roomId are the office cursors — office pipeline resumes exactly where it left off; new rooms baseline-at-now.
6. kanban-board.json, notifications.json, users.json, archives (+ per-archive HMAC tokens), blobs: untouched (company-global).
7. Wire back-compat: `/websocket` without `?room=` == office for the deploy window (version-gated auto-refresh reloads stale tabs). `MEETING_ROOM_PASSWORD` stays account-seed-only — NOT wired to any room.
8. Rollback caveat: under a rolled-back binary, new rooms' entries read as office until roll-forward — acceptable for a short window; documented in the runbook.
9. **W7 migration dress rehearsal (graft from B):** boot against a prod-shaped `data/` copy (JSONL without roomIds, legacy sessions.json, no rooms.json) asserting office seed + all historical meetings/archives resolve to office + office cursors resume + zero logouts.
10. Deliberate behavior change (the only one): office Realtime peer + lane become lazy (§4.4) — functionally invisible (Scout connects during the join handshake), ends always-on spend, pinned by test.

---

## 10. Explicitly-decided defaults

| Decision | Default | Notes |
|---|---|---|
| Guest link waives room passcode | YES | Link is the capability; passcode gates members |
| Guests get in-room chat | YES | Feeds transcript; rate-limited §6.5; everything else allowlist-denied |
| Listen-only semantics | Per-sitting LATCH (guest-enabled at start OR first guest admitted); never unlatches within sitting | Graft from B |
| Guest content in company rollups | INCLUDED, provenance-stamped | **RATIFIED by AJ 2026-07-09** — external-meeting memory must reach the brain/Scout recall; §6.4 |
| One account, one live room seat | YES (cross-room join evicts via session_replaced) | Kills the name-key cleanup bug class |
| Workers | Singleton goroutines, (agent,room)-keyed bookkeeping | Revisit per-room pools past ~5 concurrent rooms |
| Realtime peer lazy for ALL rooms | YES | Behavior change, cost fix, uniform "not-started" layer |
| Guest link TTL / session TTL | 7 days / 12 hours | Revocable anytime |
| Guest caps | 2 sockets/session, 5 guests/room (env), 4 pre-hello sockets/IP | §6.1 |
| Guest transcription cap | 120 min/sitting (env), member-extendable | §6.5 |
| Rooms list placement | Office home card block (no new rail tool) | Rail slot later without rework |
| Token carriage | URL fragment `/g#<token>` + path shim 302 | §6.3 |
| /native/config | Session-gated (breaking for unauthenticated native bootstrap) | Native app owns an account session |

---

## 11. Wave plan

Sequential; every wave ends with `go test ./...` green and the app shippable (server capability dark until W5/W6 light it up; W3+W4 land before any client can join a second room, so the cursor class is fenced first at the record layer (W2) and then at the agent layer (W4) before real multi-room traffic exists).

### W1 — Room registry, guest identity, token secrecy, HTTP surface
**Goal:** All persistence + auth groundwork, zero behavior change to the live room. `rooms.go`: roomRecord/guestLinkRecord/roomStore (data/rooms.json, office seed, tmp-rename), bcrypt passcode set/clear, guest-link mint/list/revoke (crypto/rand 32B, sha256-at-rest, 7d expiry, constant-time redeem, expiry sweep on session-persist seam). `auth_http.go`: sessionRecord + Kind/RoomID/GuestName; **explicit `Kind=="guest" → nil` in userFromRequest**; `guestFromRequest`; `bonfire_guest` cookie (12h). Routes: GET/POST `/rooms`, `/rooms/{id}/passcode`, `/rooms/{id}/archive`, guest-links mint/list/revoke, `GET /g` (index bytes + `Referrer-Policy: no-referrer` + no-store), `GET /g/<token>` 302 path→fragment shim (token never logged), `POST /guest/join` (origin-checked, rate-limited, fixed roster-collision check §5.2, name sanitation), `GET /guest/me`. `/native/config` session-gated.
**Files:** `/Users/ajhart/meetingassist/rooms.go`, `/Users/ajhart/meetingassist/auth_http.go`, `/Users/ajhart/meetingassist/main.go`, `/Users/ajhart/meetingassist/participants.go`, `/Users/ajhart/meetingassist/rooms_test.go`, `/Users/ajhart/meetingassist/guest_auth_test.go`, `/Users/ajhart/meetingassist/auth_http_test.go`
**Tests:** roomStore CRUD/persistence/office-seed; legacy sessions rows resolve as users (no-logout pin); guest cookie NEVER satisfies userFromRequest (explicit Kind pin); link mint/redeem/expiry/revoke + constant-time + re-check per use; /guest/join rate limit + origin + collision rejection (and that legitimate non-roster names PASS — regression on the fixed predicate); /guest/me resume; /g headers + shim redirect; /native/config 401 signed-out; sampled protected-route 401 sweep for a minted guest session.
**Depends on:** —

### W2 — Per-room memory + meeting sitting spine (record layer first)
**Goal:** Room-dimension the record layer BEFORE any second room can write. memory store `meetingIDs` map + ensure/rotate/rotateIfCurrent(roomID) with exact race semantics; appendEntryForMeeting stamps `metadata.roomId`; boot resume per room; meetingRecord gains `RoomID` + `ListenOnly` (latch field only — set/enforced in W4); per-room open-record lookup + idleTimers/idleGenerations; noteMeetingAdmission/noteMeetingOccupancy/endMeetingForIdle take roomID; startMeeting same-room defensive close; reconcile per room; brain_intake pinned to office; rememberTranscript/recordRoomChatMessage carry roomID. Everything still called with roomID="office" — runtime behavior unchanged.
**Files:** `/Users/ajhart/meetingassist/memory.go`, `/Users/ajhart/meetingassist/meetings.go`, `/Users/ajhart/meetingassist/kanban.go`, `/Users/ajhart/meetingassist/brain_intake.go`, `/Users/ajhart/meetingassist/memory_test.go`, `/Users/ajhart/meetingassist/meetings_test.go`
**Tests:** two rooms mint/rotate ids independently; legacy no-roomId entries read as office (snapshot + boot resume); port the existing idle-end race tests (hasEndedRecord re-mint, conditional rotation, admission-vs-fired-timer) per room AND cross-room (A closing never rotates B); brain_intake stamps office; existing meeting/fsync/deflake tests green.
**Depends on:** W1

### W3 — liveRoom media core: per-room SFU, presence, admission, fan-out, lazy mixer+lane, guest ws containment, DoS caps
**Goal:** Extract the media/presence plane into `room_live.go` (liveRoom + roomManager) per §4.2/§4.5: per-room peer pool/track tables/signaling debounce/keyframe (one registry ticker); presence + liveness sweep + capacity per room; `/websocket?room=` pre-upgrade principal+room resolution (guest room forced from session, mismatch 403; missing ?room == office); hello passcode (bcrypt + `roompass:` rate limit → access_denied); guest admission from session (server-side "Guest " prefix + dedupe, seats keyed by session, attribution accepts room-roster guest names); replay branch withholds board/memory/notifications/proposals from guests; **§6.1 pre-upgrade caps + guest PC-alloc deferred until after admission**; **§6.2 write-time guest event allowlist in threadSafeWriter/sendKanbanEvent**; guest inbound events restricted to hello/room_chat/signaling/room_ping; broadcastKanbanEvent(roomID)+roomId stamps; one-account-one-room eviction; lazy mixer+lane on first admission with mediaGen-fenced teardown after endMeetingForIdle; §6.5 guest chat token bucket + transcription cap auto-flip; Scout voice track room-scoped; shutdown/build-version over registry; participantsHandler ?room= (member); new `rooms` office event.
**Files:** `/Users/ajhart/meetingassist/room_live.go`, `/Users/ajhart/meetingassist/main.go`, `/Users/ajhart/meetingassist/kanban.go`, `/Users/ajhart/meetingassist/audio_mixer.go`, `/Users/ajhart/meetingassist/transcription_lane.go`, `/Users/ajhart/meetingassist/speaker_attribution.go`, `/Users/ajhart/meetingassist/participants.go`, `/Users/ajhart/meetingassist/room_live_test.go`, `/Users/ajhart/meetingassist/endpoint_session_test.go`
**Tests:** two-room isolation (room A tracks never offered to room B); guest hello replay contains zero board/memory/notification frames (recorded-writer assertion); guest office hello denied; write-time allowlist drops a deliberately mis-routed board event to a guest writer; wrong passcode → access_denied + rate limit; caps: 3rd socket on one guest session rejected pre-upgrade, room guest cap, per-IP pre-hello cap, no PC allocated for an unadmitted guest socket; guest chat rate limit; transcription cap flips recording off with system:guest-cap; cross-room account eviction without cross-room track teardown; mixer+lane lazy create/teardown with mediaGen (rejoin-during-grace cancels, rejoin-after restarts); zombie reap + room_ping per room; two guests named Sam coexist; guest transcript attribution stored as "Guest Sam"; `go test -race` green; ALL existing endpoint/zombie/flicker-class tests green with office aliasing — this wave is the regression gate.
**Depends on:** W2

### W4 — Per-room realtime + room-partitioned agents + listen-only enforcement + quarantine
**Goal:** Realtime peer per-room and lazy (never for listen-only sittings; restart loop mediaGen-fenced; `/assistant/realtime-offer`/`realtime-tool` bind to the caller's room and refuse listen-only). §7.4 agent bookkeeping (agent,room)-keyed: cursors via unconsumedEntriesAfter(kind, roomID), baselines, nudges w/ roomID, per-(agent,room) run locks; workers iterate rooms with unconsumed input; guests-only brain deferral (§6.5). ListenOnly latch set at startMeeting/first-guest-admission, never unlatches (§7.1). Enforcement: shared filterListenOnly in board_worker + suggestion_agent (skip-while-advancing); proposeCodexTask originRoomId stamp + listen-only rejection; applyMeetingBoardAnalysis refusal; workflow_ticker declines listen-only origins; rememberTranscript skips Scout wake pulse. flushAmbientAgentsForClose(reason, roomID) skips board stage for listen-only. recap(roomID) room-scoped delivery. Mission-intel auto-title + narrative segments resolve the room's record. **§6.4 rollup inclusion (RATIFIED):** listenOnly provenance stamps at write; day/company digest, entity ledger, reflection workers INCLUDE listen-only-sitting entries in their windows like any other material — external-meeting memory must be Scout-recallable company-wide ("how was Erick's meeting with the NBC person?").
**Files:** `/Users/ajhart/meetingassist/kanban.go`, `/Users/ajhart/meetingassist/agent_runner.go`, `/Users/ajhart/meetingassist/brain_worker.go`, `/Users/ajhart/meetingassist/board_worker.go`, `/Users/ajhart/meetingassist/suggestion_agent.go`, `/Users/ajhart/meetingassist/workflow_ticker.go`, `/Users/ajhart/meetingassist/codex_proposals.go`, `/Users/ajhart/meetingassist/recap.go`, `/Users/ajhart/meetingassist/mission_intelligence.go`, `/Users/ajhart/meetingassist/narrative_maintainer.go`, `/Users/ajhart/meetingassist/meeting_digest.go`, `/Users/ajhart/meetingassist/company_digest.go`, `/Users/ajhart/meetingassist/entity_ledger.go`, `/Users/ajhart/meetingassist/agent_runner_test.go`, `/Users/ajhart/meetingassist/listen_only_test.go`, `/Users/ajhart/meetingassist/agent_cursor_room_test.go`
**Tests:** THE cursor test — rooms A+B interleave transcripts, A's brain pass never advances B's window, both fully summarized; listen-only sitting → transcripts+brain+meeting digest+archive exist but ZERO board ops / proposals / ticker launches / everyone-notifications (assert all three layers independently); latch persists after last guest leaves; pre-guest proposal still launches; no realtime dial for listen-only (mock API); guests-only room defers brain until member joins or close-flush; two rooms close concurrently without deadlock (-race); recap for room B never reaches the signed-in union; day/company digest + entity ledger exclude listen-only entries while cursors advance; existing brain/digest/ledger tests green with office defaults.
**Depends on:** W3

### W5 — Client: rooms list, create, passcode join, multi-room event scoping
**Goal:** §8.1/§8.2 member-facing multi-room in index.html: `var roomsList` + GET /rooms hydration after the /auth/me await + `rooms` event handler; Rooms card block (live dot, count, lock, guest badge, Open w/ inline passcode, + New room form, overflow menu incl. guest-link mint showing the one-time `/g#` URL); `var activeJoin` threaded through joinRoom/openRoomWebSocket (?room= dial, passcode in hello, reconnect re-send, cleared in leaveRoom); roomId filters in handleKanbanMessage room-scoped cases; renderRoomLandingState per room; copyRoomLink → `/?room=`; `?room=` boot param preselect; office keeps one-click join.
**Files:** `/Users/ajhart/meetingassist/index.html`, `/Users/ajhart/meetingassist/frontend_rooms_test.go`, `/Users/ajhart/meetingassist/frontend_boot_tdz_test.go`, `/Users/ajhart/meetingassist/frontend_meeting_reset_test.go`
**Tests:** §8.4 TestIndexRooms* pins (ws ?room=, activeJoin var, passcode in hello, roomId filter, copyRoomLink fix, hydration inside auth fan-out); update every existing TestIndex* pin grepping the bare /websocket dial or hello shape; boot TDZ test green.
**Depends on:** W4

### W6 — Client: guest boot mode and guest chrome
**Goal:** §8.3: `var guestBootToken` fragment parse + `guestMode` cookie probe in the boot block; skip refreshAuthState/preview/poll for guests; /guest/me resume path (deploy-refresh survival); renderLoginMode('guest') name gate; POST /guest/join → joinRoom({guest:true}) skipping inline /auth/login; post-join replaceState fragment strip; is-guest CSS axis (hide tool rail/topbar extras/scout rail/board rail; keep stage + meeting bar + room chat); setActiveTool pinned to 'room', back-stack suppressed; terminal "you've left" states for leave/denied/replaced; guests never set authedUser, never open the office socket.
**Files:** `/Users/ajhart/meetingassist/index.html`, `/Users/ajhart/meetingassist/frontend_guest_test.go`, `/Users/ajhart/meetingassist/frontend_boot_tdz_test.go`
**Tests:** TestIndexGuest* pins: fragment parse var-declared in boot block before the refreshAuthState guard; guest branch skips refreshAuthState; /guest/me resume probe on cookie-without-token; replaceState strip only after join; ensureOfficeSocket guard excludes guest mode; is-guest CSS hides the four chrome regions; guest leave never calls setActiveTool('office'); no authedUser assignment in the guest path; boot TDZ + all TestIndex* green.
**Depends on:** W5

### W7 — Acceptance, exhaustive allowlist, migration dress rehearsal, race pass, runbook
**Goal:** Prove the whole objective on a real httptest server. (1) Acceptance flow: member creates passcoded guest-enabled room → mints link → guest redeems with name → guest ws joins THAT room only → guest receives only allowlisted events while office board/memory flow to members → guest 401s across the route table → room empties → close chain runs, recap/digest/archive exist, zero board mutations/proposals, company rollups INCLUDE the sitting's material provenance-stamped (§6.4 ratified — a recall query over the rollup window sees the guest meeting) → office legacy data fully recallable. (2) **Route-walk allowlist test built from the ACTUAL mux registrations** (§6.7). (3) Fan-out leak sweep: recorded guest writers across artifact os_event/notification/proposal/shutdown/deploy-refresh broadcasts. (4) DoS-cap battery under concurrency. (5) **Migration dress rehearsal** (§9.9) on prod-shaped fixtures. (6) Lazy-lifecycle E2E (zero OpenAI dials pre-admission; teardown after grace). (7) `docs/rooms-runbook.md` content folded into the docs file below: link lifecycle, listen-only contract, rollup-inclusion note (ratified 2026-07-09, accepted injection-laundering residual + re-quarantine toggle), Caddy log-scrub ops note, rollback caveat.
**Files:** `/Users/ajhart/meetingassist/acceptance_test.go`, `/Users/ajhart/meetingassist/guest_allowlist_test.go`, `/Users/ajhart/meetingassist/migration_boot_test.go`, `/Users/ajhart/meetingassist/cleanup_test.go`, `/Users/ajhart/meetingassist/docs/rooms-runbook.md`
**Tests:** the single end-to-end acceptance test; exhaustive route-walk 401/403 sweep (fails closed for future routes); token-gated public endpoints unchanged; fan-out leak assertions; caps battery; migration boot on prod-shaped data (zero-data-loss pin, zero logouts, office cursors resume); full `go test ./...` AND `go test -race ./...` green as the exit criterion.
**Depends on:** W4, W6

---

## 12. Risks

1. **Cursor corruption** (agent_runner.go:609-661) remains the make-or-break; W2 (record layer) + W4 (agent layer) land before the client can create real multi-room traffic, and W4's interleave test pins the class.
2. **Teardown-vs-restart races** in the new lazy lifecycle (proactive realtime restart, lane reconnect) — mediaGen fencing + `-race` tests; this is the sharpest concurrency edge of the whole plan.
3. **TestIndex pin churn**: W5/W6 touch join-flow strings dozens of existing pins grep; budget for lockstep updates or the suite goes red mid-wave.
4. **TDZ boot-order** in index.html: all guest/rooms boot code must be var-declared or deferred behind the /auth/me await (2026-07-05 outage class).
5. **kanbanBoardApp extraction blast radius**: ~40 files call the singleton; W3 is deliberately the regression gate wave — the whole existing suite must pass with office aliasing before anything else proceeds.
6. **RESOLVED — AJ ratified INCLUSION (2026-07-09)** (§6.4): guest-room knowledge flows into company recall, provenance-stamped. The injection-laundering escalation is an accepted, documented residual; re-quarantine stays a read-side toggle on the stamps.
7. **Residual token exposure**: path-form shim links hit proxy logs once; ops must add the Caddy log scrub (runbook item, not enforceable from this repo).
8. **Concurrent working-tree sessions**: implementers must be additive-only; a concurrent full-tree rsync once reverted a session's work (2026-07-07 lesson) — verify deploys by source md5, and never checkout/reset/stash.
