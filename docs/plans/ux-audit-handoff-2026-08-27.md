# Handoff — UX audit wave + VPS deploy blocker

Written 2026-08-27 for whoever picks this up. Verified state as of writing.

---

## TL;DR

A UX/UI audit shipped six measured correctness fixes as `25e45068`, now on `axx/main`.
**It is NOT live.** The release is built and staged on the droplet but activation is
hard-blocked by a release-tool gate that blocks *every* deploy, not just this one.
Separately, ~82 GB of droplet disk was reclaimed (94% → 41%).

| | |
|---|---|
| Branch | `design/ux-audit-wave` = `axx/main` = `25e45068` |
| Production serving | `56afdc11` (old code) — `/healthz` ok, `/readyz` ok |
| Staged on droplet | `meetingassist:release-25e45068…` (`8943bd38e2d5`), render (`845d8a65078e`) |
| Ledger | generation 225, active `56afdc11`, previous `28b92428` — **unchanged** |
| Droplet disk | 41% used, 92 G free |

---

## 1. What shipped in `25e45068`

Six correctness defects, each with a measured before/after. Full evidence in
[`ux-audit-2026-08-27.md`](ux-audit-2026-08-27.md).

1. **Chat hover actions rendered up to 12,589px from their message.**
   `.desktop-chat-interactions[data-has-reactions="false"]` is `position:absolute`, but no
   ancestor was positioned, so the containing block fell through to `#scoutPrivatePane` and all
   57 no-reaction toolbars stacked in the pane corner. 81% of messages could not take a first
   reaction / riff / reply-in-thread / overflow menu **by mouse at all**. Fix: `position:
   relative` on `.scout-chat-msg__stack`.
2. **Every semantic hue was unreadable as text in light theme** — the iOS system colours used at
   110 `color:` sites on warm putty: warn **1.40**, live **1.38**, danger 2.32, info 2.49 (AA
   needs 4.5). Worst: `#lobbyJoinError` at 1.40:1/11px. Added `--warn-text` / `--danger-text` /
   `--info-text` / `--live-text` / `--success-text` following the existing `--ember-text` recipe.
   Dark aliases back to the raw hues (already ≥4.55). Verified 1.40 → 6.04.
3. **Search bars 55% inert** — 40px shell, unstretched 18px input; `align-self: stretch`.
4. **Drive clipped metadata on 8 of 9 cards** — `minmax(210px)` → `240px`.
5. **Studio rows clipped their own longest status string by 4px** — row `gap` 11 → 8.
6. **Presenter notes 66px shorter than the note** — fixed `min-height` → `clamp`.

**Test status:** the 683 tests that read `index.html` produce the **same 60 pre-existing
failures as pristine `main`, byte for byte** — zero regressions. One test pinned the literal
string `color: var(--warn)`; it was moved to `--warn-text`, intent preserved.
⚠️ The suite is *already red on `main`* (60 failures) — establish a baseline before blaming a diff.

## 2. Deliberately NOT changed — needs AJ

- **3 of 7 rail icons are ember at rest** (`.pd1-primary-nav__studio-wrap` / `__external`).
  Measured **4.12:1**, so it passes the 3:1 graphics bar — this is *hierarchy/taste, not a11y*.
  Recommendation (unexecuted): the `__external-wrap::before` divider already carries the
  grouping; drop the at-rest tint, keep ember for hover/active. Left alone because ember
  expression is a ratified brand call.
- **Deck toolbar wrap at ≤1024** puts authoring tools below export/save — there is a source
  comment stating that intent, so it is a design decision.
- Raw tracking URLs as card/preview text; raw markdown (`**Pupcasting:**`) leaking into reply
  chips; studio titles that are the raw prompt (two projects are *both* literally titled
  "Turn this into a goal workflow:"); Drive's three simultaneous "new" affordances; the deck
  editor's three simultaneous Save signifiers.
- Sub-44px touch targets: `#topbarBell` / `#topbarMobileAccount` 28×28 (the only two mobile
  top-bar controls), `.scout-chat-msg__expand` 103×**14**.

## 3. 🚨 THE BLOCKER — all VPS deploys are blocked

`scripts/bonfire-release.mjs` ~line 2059, `assertQualificationTransitionBound`:

```js
if (action === 'activated' && currentState === 'true') {
  // The active-release ledger does not yet carry qualification lineage across
  // arbitrary successors. Refuse to create a generation whose only safe
  // rollback receipt is bound to an older active generation.
  throw new Error('qualified current release cannot perform an ordinary activation without durable qualification lineage')
}
```

Production base env has **`PRIVATE_REALTIME_VOICE_QUALIFIED=true`**, so `currentState === 'true'`
and ordinary activation is refused **unconditionally**. No flag satisfies it — the comment says
the lineage feature is not built ("does not *yet* carry").

Both documented escape hatches were checked and neither applies:
- `--target-base-env-patch-*` only handles `absent → true` / `false → true`. Verified by running
  `privateRealtimeVoiceQualificationEnvPatch` against a `true` env → rejects.
- `--qualification-rollback-receipt` needs a receipt that **does not exist**:
  `/opt/meetingassist-backups` has 301 entries, **zero** qualification/env-patch receipts.

**Do NOT hand-edit the base env to work around this.** `deploy/digitalocean/README.md` forbids
mutating base env outside the release transaction; that receipt chain is what makes rollback
safe. Breaking it strands production with no verified rollback.

**Failure was clean** — the tool refuses *before* mutating. Ledger untouched at generation 225,
containers up, health 200 throughout. Build + upload succeeded; only activation is gated.

### Two paths (AJ's call, not yet decided)
1. **Build qualification lineage** into the ledger/tool. ⚠️ Chicken-and-egg: activation is always
   orchestrated by the **currently serving** release's sealed tool, so a tool fix cannot deploy
   itself through the same gate. Needs a deliberate sequence.
2. **Receipted de-qualify → activate → re-qualify.** Takes private native voice down briefly and
   needs the transactional path, since no prior receipt exists to roll back against.

### To resume the deploy once the gate is resolved
Everything is staged; only `activate` needs to re-run:
```bash
SHA=25e45068fe66c464afdfb8fe789038b512cf7f35
PRIOR=56afdc116f8dd4069df0d280a768287a7fe38077
node /opt/meetingassist-releases/$PRIOR/sealed-candidate/scripts/bonfire-release.mjs activate \
  --release-dir /opt/meetingassist-releases/$SHA \
  --rollback-release-dir /opt/meetingassist-releases/$PRIOR \
  --base-env /opt/meetingassist/deploy/digitalocean/.env \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz
```

## 4. Disk cleanup — done, but the causes are unfixed

94% → **41%**, ~82 GB reclaimed, production healthy throughout.
**None of the bloat was the brain**: live compounding memory is only **528 MB** (blobs 294M,
meeting-memory.jsonl 76M, canonical 46M, archives 37M, render-jobs 30M, stride-e10 24M,
embeddings 21M) plus 401M canonical Postgres — ~0.4% of the disk.

| removed | cause |
|---|---|
| 15 GB | **83 orphaned `.tmp-tar-*` files** — the backup engine writes a temp tarball, renames to `.tgz`, and never cleans up on the failure path. Burst Aug 14–20, dormant since. **BUG UNFIXED.** |
| 49.5 GB | docker build cache (`docker system df` under-reports it as 1.8 GB reclaimable) |
| ~17 GB | 92 unreferenced release images — there were 100, only **4** are ledger-pinned |

### Open work — retention (nothing exists today, so the bloat WILL return)
1. Fix the backup `.tmp-tar-*` leak (clean on failure + sweep stale at startup).
2. Prune release images after successful activation — the tool already writes the 4 IDs to keep
   into the ledger.
3. `docker builder prune` after each release build.
4. Trim `source.tar` (28 MB × 278 dirs ≈ 6 GB) beyond the last N releases; keep receipts (4 K).

**Cleanup safety rules used:** protect image IDs pinned by `active-release.json` as `active`
**and** `previous` (both `meetingassistImageId` and `renderRunnerImageId`) plus anything a running
container references; delete temps with `-links 1 -user root` and an exact name pattern, never a
glob sweep.

## 5. Environment / method notes

- **Work happened in an isolated worktree** `/Users/ajhart/meetingassist-ux-audit` because the
  shared tree `/Users/ajhart/meetingassist` has another session's ~90 modified files. That tree
  was never touched — verified `0` occurrences of this change leaked into it. `main` was advanced
  by a **remote fast-forward push**, never a local checkout. Do not `git stash` or reset there.
- **Audit against production with real data; fix and verify locally.** Seed data is nearly empty
  and hides every truncation/density defect. Candidate fixes were proven on real content by
  **injecting CSS client-side into the production tab** (read-only, no deploy, no data written).
- **Browser-pane screenshots trail the DOM.** Three "findings" died on inspection (blank Home,
  tabs one step behind, missing slide numbers) — all screenshot lag. Verify *state* with JS
  measurement; use screenshots only for visual judgement. `computer{action:"zoom"}` is
  unsupported here and coordinate-based hover is unreliable — prefer refs or `.click()`.
- **`index.html` is `os.ReadFile` once at boot** (not `go:embed`) — restart to pick up edits, no
  rebuild needed for HTML/CSS. Run local with `-addr :3100` and `MEETING_MEMORY_PATH` pointed at
  a scratch dir; `MEETING_ROOM_PASSWORD` seeds all `seededAccounts` with that password.

## 6. Not verified

**In-call video is unaudited** — tiles, control island, Recap/Transcript/Chat meeting tabs, PiP.
The harness browser blocks camera/mic capture, so every join lands in the permission-error state.
(That state is where the worst contrast finding came from.) Needs a real device.
