# Spectacular OS — Acceptance Suite (manual half)

Date: 2026-07-03 · Wave 14 · Owner: AJ Hart
Automated half: `acceptance_test.go` (runs in `go test ./...`). This file is the
**human** half — the live checklist AJ executes on `https://thebonfire.xyz`
after the OPS-3 deploy. Every box must be green before the wave is "flawless."

How to use: deploy (OPS-3), then run each section top to bottom on the real box
and the listed real devices. A row is green only when the *observable* pass
criterion is met — not "looks plausible." Log failures inline; a red row blocks.

---

## A. The whole-wave demo (design §Acceptance — "no cuts")

Cast: **Maya** (late, on her phone), **Tyler** (sharing on desktop), **Joel**
(joins simultaneously, never enters the room). Two seeded accounts + AJ as admin.

| # | Step | Do this | Observable pass criterion | ✅ |
|---|------|---------|---------------------------|----|
| A1 | Simul-join, multi-endpoint | Tyler joins on desktop and starts screen share; Maya joins on her phone; Joel joins on desktop — all within ~5s | All three land with no tile flicker; roster shows three identities; Maya on two devices (laptop+phone) reads as ONE identity with "· 2 devices", never a duplicate seat or `session_replaced` eviction | ☐ |
| A2 | Catch me up | Maya: "Scout, catch me up" | A private catch-up lands in **Maya's** bell only (audience=me); Scout speaks the headline; nothing posts to room chat | ☐ |
| A3 | Voice dispatch → return card | Maya (keeps talking after): "Scout, pull a comp set for Nimbus and put it in #nimbus" | Maya's turn is not blocked; a running card appears; when it finishes, the return card lands in **#nimbus** and Joel's bell warms **though he never entered the room** (push channel, ≤2s) | ☐ |
| A4 | Text door parity | Joel types `/goal one-pager for Nimbus` in a thread | The identical staged card renders (same 10-node rail) as the voice door produced in A3 — one pipeline, visibly | ☐ |
| A5 | Private grill dial | Maya: "Scout, grill me on Nimbus" (private) | The 3-act ritual runs; scorecard reveals with a spoken + shown readiness like "6.8, up from 6.2"; the binder trend moves | ☐ |
| A6 | Board auto-advance | Watch the Nimbus package/board after A3–A5 complete | The Nimbus stage pill advances **on-screen** with the ember stage-advance sweep (delight #6) — no manual refresh | ☐ |
| A7 | Morning Brief + quarantine | Next morning as Tyler, open BonfireOS home | The Brief greets by name; shows pending approvals, overnight results, unread channels, and a quarantine tray with ≥1 item + a reason; restore one with a tap → it slides out (delight #12) and returns to memory | ☐ |

### Pillar guarantees (spot-check live; the automated suite is the real gate)

| # | Guarantee | Live check | ✅ |
|---|-----------|-----------|----|
| A8 | Three doors, one pipeline | A3 (voice), A4 (`/goal`), and a palette launch all produce the same staged card shape | ☐ |
| A9 | External-write gate — both runners | Ask Scout to do an external write (commit/email/deploy) via a goal; it STOPS at "waiting on AJ" and does nothing until approved — on the Fable in-process runner AND the codex sidecar | ☐ |
| A10 | Approval round-trip | A non-admin requests an external write; AJ approves from the bell; the approved asset returns to the requester's origin surface with a notification ("approved · sent") | ☐ |
| A11 | Disclosure stamp | Have Scout post as a user; the post carries the server "via Scout" chip + `postedOnBehalfOf` regardless of what the model claimed | ☐ |
| A12 | Golden evals | `go test -run 'Eval' ./...` green on the three exemplar tools (research/one-pager/grill) | ☐ |
| A13 | Rename | The shell reads **BonfireOS**; the `office` data-tool key is unchanged (deep links still work) | ☐ |

---

## B. Device matrix (rtc §6.3) — every row green before ship

Run the S-cases against real devices. "Pass" is the observable behavior in the
right column; a stall, a dead-air gap, a frozen tile, or a false chip = red.

| Device / browser | S-cases | Pass criteria | ✅ |
|---|---|---|----|
| macOS Chrome | S1, S8, S10, S11 | Join clean; **unplug the active mic → auto-switch to next mic <2s** with an honest "microphone reconnected" toast; ICE restart on a Wi-Fi blip recovers <5s and the "N/5" counter does NOT show on the first (self-healing) attempt | ☐ |
| macOS Safari | S2, S12 | currentTime-stall guard holds (no video flicker); the noise chip reports the **voiceIsolation** path honestly (not a fake "RNNoise active") | ☐ |
| iOS Safari | S3, S12 | Background/lock the app mid-call, return → **audio resumes** (AudioContext resumed, "resuming audio…" shown briefly, no dead air) AND video resumes; camera never shows a frozen last-frame to peers; video looks default OFF | ☐ |
| Android Chrome | S4, S8 | Thermal governor drops the look before the device gets hot (chip stays honest); standard-cleanup is the default; unplug/replug BT mic recovers | ☐ |
| **Same account: macOS + iPhone** | **S6, S7, S13** | **Both stay in the room; ONE seat counted; no eviction; the calm "you joined from another device" chip is shown (not the alarming banner)** | ☐ |
| Different accounts: laptop + phone | S5 | Both get full independent media | ☐ |

### B-extra — Wave 14 recovery + looks rows (verify the new code paths)

| # | Check | Pass criterion | ✅ |
|---|-------|----------------|----|
| B7 | devicechange — better mic appears | Plug in your usual/preferred mic mid-call → a calm **"switch to &lt;name&gt;?"** offer chip appears; it never auto-yanks; tapping "switch" swaps cleanly; dismiss leaves the current mic | ☐ |
| B8 | devicechange — active device vanishes without an `ended` event | The active mic disappearing from the device list triggers the same auto-recovery as an unplug (no manual step) | ☐ |
| B9 | media_disconnected | Force a server media drop → a single calm "reconnecting — hold on", ONE auto-attempt, and a manual "Rejoin" surfaces only after the ICE ladder is genuinely exhausted | ☐ |
| B10 | Honesty chip vs ground truth | Force RNNoise failure (bad wasm path, `forceSafariMedia=1`-style) → the chip says **"degraded/fallback," not "active"** on every browser | ☐ |

---

## C. Wake-word (ships OFF; verify BOTH halves)

| # | Check | Pass criterion | ✅ |
|---|-------|----------------|----|
| C1 | Presence (always on) | In a recording room, say a sentence containing "Scout" → the brand mark + idle voice island **breathe one cycle**; typed room chat with "scout" does NOT pulse; "scouting"/"discount" do NOT pulse (whole-word only) | ☐ |
| C2 | Arming default OFF | With the "let Scout arm a reply" toggle OFF (default), saying "Scout" in a live room voice session does **nothing** beyond the breathe | ☐ |
| C3 | Arming ON (after the matrix passes) | Turn the toggle ON; with a live room voice session and Scout idle, saying "Scout, …" makes her address the speaker; toggle persists per-device ("✓ saved for this device") | ☐ |
| C4 | No false-fire | Arming never triggers on a private grill, on a typed message, or when no room voice session is open | ☐ |

---

## D. The quiet-Tuesday journey (design §quiet-Tuesday — equal standing)

A low-intensity portfolio-support day, no big `/goal`. Run as **Joel**.

| # | Step | Do this | Observable pass criterion | ✅ |
|---|------|---------|---------------------------|----|
| D1 | Greeting | Open BonfireOS with coffee | The Morning Brief greets Joel **by name** with what moved overnight (quarantined drafts, one approval waiting on AJ, #dealflow unread count) | ☐ |
| D2 | Health nudge | Read Portfolio Health | A stale package surfaces itself — e.g. "Nimbus hasn't moved in 11 days — its rights map still has two ASSUMED items" | ☐ |
| D3 | Read-aloud from the body | Tap it; "Scout, what are the two open rights questions?" | Scout reads them **from the artifact body**, not from card metadata | ☐ |
| D4 | Disclosed post + deferred nudge | "Scout, tell Maya the rights follow-up is on her — and remind me after tomorrow's call" | A disclosed "via Scout" post lands in #nimbus; Maya's bell warms; a deferred reminder queues and fires only after the meeting is archived | ☐ |
| D5 | The feel | Total elapsed | ~3 minutes, zero `/goal` launches, and the OS demonstrably watched the book, tended memory, and moved a real ball | ☐ |

---

## E. Deal Room (capstone)

| # | Check | Pass criterion | ✅ |
|---|-------|----------------|----|
| E1 | Request | On an assembled package's binder, tap "Share as Deal Room" | It lands as a PENDING request; AJ (admin) gets a bell notification | ☐ |
| E2 | Gate | As a non-admin, try to approve | Denied — only the admin can approve/reject/revoke (the external-write gate, at the share surface) | ☐ |
| E3 | Approve → link | AJ approves | A tokenized read-only URL `/deal-room/{token}` is minted; the requester is notified it's live | ☐ |
| E4 | Read-only page | Open the URL logged out / incognito | A clean server-rendered binder renders read-only; any HTML/script in the binder body is escaped (no injection); a provenance appendix is present | ☐ |
| E5 | Revoke | AJ revokes it in settings | The URL now 404s; the room disappears from the active list | ☐ |

---

## Sign-off

- [ ] All A rows green (whole-wave demo + pillar guarantees)
- [ ] All B rows green (device matrix + Wave 14 recovery/looks)
- [ ] All C rows green (wake-word both halves)
- [ ] All D rows green (quiet-Tuesday journey)
- [ ] All E rows green (Deal Room)
- [ ] `go test ./...` green on the build machine (VPS has no Go)
- [ ] Memory file updated with the deploy commit + live-verification date

Only when every box is checked is the Spectacular OS wave "flawless" per the
design's own bar.
