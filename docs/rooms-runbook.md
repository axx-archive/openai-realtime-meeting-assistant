# Rooms Runbook — multi-room + guest links (2026-07-09)

Operational contract for the multi-room build (spec: `docs/plans/multi-room-2026-07-08.md`).
Audience: whoever operates thebonfire.xyz or debugs a room/guest incident.

## 1. The shape of the system

- `kanbanBoardApp` stays the company-OS singleton (board, memory JSONL, artifacts,
  notifications, accounts). Only the media/presence/sitting plane is per-room
  (`room_live.go`). Rooms are **config** in `data/rooms.json`; a room costs nothing
  until someone joins it (lazy mixer/lane, `mediaGen`-fenced teardown after the
  4-minute idle grace).
- The office is room id `office`, seeded into `data/rooms.json` at boot, never
  archivable, no passcode by default — one-click join is preserved exactly.
- One account holds one live room seat: joining room B evicts the same account's
  room A seat via `session_replaced`.
- Ambient workers are singleton goroutines with `(agent, room)`-keyed cursors.
  Legacy artifacts without a `roomId` stamp ARE the office cursors (§9.5), so the
  office pipeline resumes seamlessly across the deploy that ships rooms.

## 2. Guest link lifecycle

- Mint: member → room overflow menu → invite guest, or
  `POST /rooms/{id}/guest-links {label?, ttlHours?}`. The raw token (64 hex) is
  returned **once** as `/g#<token>` and never stored — `data/rooms.json` holds only
  the sha256 (`guestLinkRecord.Hash`); the revoke handle (`id`) is the hash's first
  8 hex. A leaked rooms.json hands out no admission.
- TTL: default 7 days, cap 30 days (`ttlHours` bounded). Expired links are swept on
  the session-persist seam.
- Redeem: `POST /guest/join {token, name}` — origin-checked, rate-limited,
  constant-time hash compare, and expiry/revocation/room-archival are re-checked on
  **every** use. Names are sanitized (1–40 printable runes) and roster collisions
  rejected; the display name is server-prefixed `"Guest <name>"` at admission and in
  every transcript/attribution record, so a guest can never impersonate a member in
  the durable record.
- Guest session: `bonfire_guest` cookie (HttpOnly, 12h TTL) in the SAME
  `data/sessions.json` under `Kind:"guest"`. `userFromRequest` returns nil for guest
  rows (the explicit Kind guard), so every member-gated endpoint fails closed —
  pinned by the exhaustive route walk in `guest_allowlist_test.go`, which breaks the
  build when a future route is registered without a conscious allowlist decision.
- Revoke: `POST /rooms/{id}/guest-links/revoke {id}`. Revocation is immediate for
  future redeems; an already-seated guest is bounded by the 12h session TTL and can
  be dropped now by archiving the room or restarting the container.
- The guest link deliberately **waives the room passcode**: the link is itself a
  member-minted, revocable, expiring capability. The passcode gates members-at-large.

## 3. Token secrecy + the Caddy log scrub (ops action)

- Canonical guest URL: `https://<host>/g#<token>` — the token rides the URL
  fragment, which browsers never send to the server, so it cannot land in server or
  proxy access logs or any Referer header. `/g` is served with
  `Referrer-Policy: no-referrer` + `Cache-Control: no-store`.
- Legacy path form `GET /g/<token>` 302s to the fragment form. That ONE request can
  hit proxy access logs. The handler never logs the token; the residual is the
  proxy's own access log.
- **OPS NOTE (do on next deploy touch):** configure Caddy to drop or truncate `/g/`
  request paths in access logs, e.g. give the `/g/*` matcher its own `log_skip`
  (Caddy ≥2.5: `skip_log @guestshim` / `log_skip` directive) or a dedicated route
  whose access log is disabled. Until then, the exposure is bounded by the 7-day
  expiry and one-tap revoke. Not enforceable from this repo — tracked here.

## 4. Listen-only contract (what a guest room does and does not do)

- Latch: a sitting is **listen-only** when the room is guest-enabled (any live,
  unrevoked link) at the sitting's start, or when the first guest is admitted
  mid-sitting. The latch is per-sitting and one-way: a guest leaving (or all links
  being revoked) mid-meeting does NOT restore full mode; the NEXT sitting does.
- Still runs (the record tier): transcription + speaker attribution, brain
  write-ups, meeting record + auto-title, narrative segments, decision ledger,
  meeting digest, recap/"fill me in", close-flush chain, idle auto-archive.
- Suppressed (three independent layers): board mutations and research suggestions
  (window filter), `proposeCodexTask` / `applyMeetingBoardAnalysis` /
  `workflow_ticker` launches (choke-point backstops), and the Scout realtime peer
  is **never created** for a listen-only sitting (no voice, no grill, no wake pulse;
  `/assistant/realtime-offer|-tool` refuse the room).
- Cost bounds: guest chat is token-bucketed (burst 5, 1 per 3s); listen-only
  transcription is capped per sitting (`BONFIRE_GUEST_ROOM_TRANSCRIPTION_CAP_MIN`,
  default 120 min — on hit, recording flips off as `system:guest-cap`; a member
  flipping it back on grants another window); a guests-only room defers scheduled
  brain passes until a member is present (the close flush still runs one bounded
  pass).
- DoS caps (pre-upgrade, before any PeerConnection exists): 2 sockets per guest
  session, 4 pre-hello sockets per IP, `BONFIRE_MAX_GUESTS_PER_ROOM` (default 5)
  admitted guests per room. Guest PeerConnections are allocated only AFTER the
  hello is admitted.

## 5. Rollup inclusion (RATIFIED by AJ 2026-07-09) + accepted residual

- Listen-only-sitting content **flows into company-global rollups** — day digest,
  company digest, entity ledger, reflection — exactly like member material, so
  Scout can answer "how was Erick's meeting with the NBC person?" company-wide.
- Provenance: every brain/T2-digest from a listen-only sitting carries
  `metadata.listenOnly="true"` at write time. That stamp is the durable record that
  the content originated in an external-guest meeting; recall surfaces should show
  the origin.
- **Accepted residual risk (deliberate, ratified):** guest-spoken — hence
  prompt-injectable — content can reach the full-mode office recall surface where
  Scout has voice + tools. Mitigations that remain: the provenance stamps make
  origin visible/filterable at every read site; listen-only rooms themselves get
  zero proactive actions; house prompt doctrine treats recalled content as data,
  not instructions.
- **Re-quarantine toggle:** re-quarantining is a read-side change — re-add the
  listen-only filter (keyed on the stamps) in the four rollup workers
  (`company_digest.go` delta filter, `entity_ledger.go` digest+decision filters,
  `meeting_digest.go` day-discovery filter, reflection window). The write-time
  stamps never went away, so the toggle needs no data migration.

## 6. Migration + rollback

- Invariant everywhere: **absent roomId == office.** `data/meeting-memory.jsonl` is
  never rewritten; new appends stamp `metadata.roomId`. `meetings.json` /
  `sessions.json` gained omitempty fields only — legacy rows read as office /
  member; **no member is logged out by the deploy.** Office records persist with an
  EMPTY RoomID so meetings.json stays byte-compatible with the pre-room shape.
- `data/rooms.json` is seeded with the office at boot if missing. The dress
  rehearsal for all of this is `migration_boot_test.go` (boots against a
  prod-shaped data/ copy: no rooms.json, roomId-less JSONL, legacy sessions).
- Wire back-compat: `/websocket` without `?room=` == office for the deploy window;
  the version-gated auto-refresh reloads stale tabs. `MEETING_ROOM_PASSWORD` stays
  account-seed-only — never wired to any room.
- **Rollback caveat:** under a rolled-back (pre-rooms) binary, NAMED rooms' entries
  read as office until roll-forward — transcripts from a guest room would appear in
  office recall for the rollback window, and guest links/sessions stop being
  honored (routes are gone; guests are simply locked out, members unaffected).
  Acceptable for a short window; prefer roll-forward. Data written by the new
  binary is never destructive to the old shapes.
- Deliberate behavior change shipped with rooms (the only one): the office Realtime
  peer + transcription lane are **lazy** — created at first admission, torn down
  after the idle grace. Ends the always-on OpenAI Realtime spend; Scout connects
  during the join handshake so it is functionally invisible.

## 7. Quick triage

| Symptom | First look |
|---|---|
| Guest link "expired or revoked" | `data/rooms.json` → the link's `expires`/`revoked`; room `archived`? Mint a fresh link. |
| Guest can't rejoin after deploy | `bonfire_guest` cookie + `GET /guest/me` is the resume path; 401 means session expired (12h) or room archived → new link. |
| Room shows stale "live" dot | `rooms` office event rides create/join/leave/reap/archive; check the zombie reap logs (`room_ping` liveness). |
| Guest sees member data | Should be impossible: §6.2 write-time allowlist (`guest_event_dropped` log lines count drops) + the route walk. Any 2xx to a guest on a member route is a P0 — check `userFromRequest`'s Kind guard first. |
| Recording flipped off by itself in a guest room | Transcription cap hit (`system:guest-cap` as the updater). A member re-enabling grants another window. |
| Board cards/proposals appeared from a guest meeting | Should be impossible (three layers); check `originRoomId` on the proposal and the room's `ListenOnly` latch on the meeting record. |
