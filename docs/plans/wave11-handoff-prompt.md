# Handoff: STRIDE v2.0 — finish Wave 11, ship it, then Wave 12

You are taking over the STRIDE v2.0 evolution of Bonfire (repo `/Users/ajhart/meetingassist`, Go backend plus a single ~99k-line `index.html`, production at thebonfire.xyz). The founder is AJ. Work was paused mid-flight on 2026-09-03 and handed to you.

## Read these first, in this order

1. **`docs/plans/stride-v2-evolution-execution-log.md`** — start at the top. The first section is titled **"PAUSED BY AJ, 2026-09-03 — RESUME POINT"** and is the authoritative statement of where things stand, with file:line evidence. Everything in this prompt is a summary of it. If the two disagree, the log wins.
2. **`docs/plans/stride-v2-evolution-execution-plan.md`** — the 12-wave map, the Critical Rules section, and the Wave 11 (D1–D18) and Wave 12 (D1–D11) deliverable lists.
3. **`docs/design/stride-design-system-v2.md`** — design canon. One ember `#FF5A19`, earned only. Glass tiers, `bfMenu`, `strideIcon`, motion tokens (`--ease`, `--ease-spring`, `--dur-fast/med/slow`, `--press-scale`), reduced motion via zeroed duration tokens.
4. `docs/plans/stride-v2-evolution-execution-plan.md` §Critical Rules — read them literally; they are enforced by tests.

## Hard rules (a violation is a failed task)

- **Concurrent sessions share this working tree.** NEVER run `git checkout`, `reset`, `restore`, `stash`, `clean`, or `revert`. Never revert another agent's hunks. Some files show as `MM` (staged and unstaged) because another session staged them — leave that alone.
- **Do not touch anything under `apple/`.** It belongs to another session and is excluded from the Wave 11 commit scope.
- **Secrets:** AJ staged an Anthropic key at `/root/secrets/anthropic.key` on the droplet (root, mode 600). Never read, echo, copy, or move it. It is applied only as a base-env patch inside a release transaction at OPS-8 (Wave 12). Never hand-edit the base env.
- **Deploys** go through the exact-release ceremony only: detached worktree → `scripts/bonfire-release.mjs scope/prepare` → 2 MB chunked upload with sha256 verification → droplet build → **rooms-empty check** (`/readyz` participants must be 0) → activate from the *serving* release's sealed tool. **Do not symlink `node_modules` into the release worktree** — `prepare` then fails with "release checkout is not clean", and it needs none.
- **Live data is the docker volume `digitalocean_meeting_data`.** `/opt/meetingassist/data/` is a stale trap; never cite it as live state.
- **Never post probes in public channels.** Never type passwords into forms.
- **Honesty rule, enforced by tests and by AJ personally:** never relabel a degraded lane as healthy, and never manufacture a healthy state from configuration. An honest amber beats a green lie. UI copy must say what actually happened.

## Where things stand

Local `main` is level with `axx/main` at `935fee99`, zero ahead and zero behind. Production serves the code-identical `8c7344d1` as **generation 249**.

| | on main | live |
|---|---|---|
| Waves 1–10 | yes (`4f15435e` + hotfix `8c7344d1`) | yes, gen 249 |
| Wave 11 | **no** | **no** |
| Wave 12 | not started | not started |

**Wave 11 is built and reviewed but has shipped nowhere.** The undeployed work is the working tree: ~141 changed or new entries excluding `apple/`, of which 44 are new `.go` files.

At the pause the tree was green: `go build ./...` clean, `gofmt -l *.go` clean, and a targeted run over `TestCaptureWatchdog|TestTranscript|TestConsent|TestRoomCapability|TestRoomLive|TestOffice|TestTopbarThemeSwitch|TestLocalSeatQuality|TestManualArchive` passed in 12.8s.

### What the pending deploy carries beyond Wave 11 proper

- **Chat intake seam fix** — intake is a fallback, not a front door. A hired agent's own direct thread is the agent's again (it used to answer as "Scout"); voice never asks clarifying questions, it infers and proceeds; anything the router already owns goes straight through. One accepted trade: intake now only interrupts an *imperative* ask, so a request phrased as a question builds without asking.
- **Ambient continuity anchor fix** — the fix that should clear `ambient.channelDigest` on prod.
- **Archive-finalization flake fix** (test-only; proven over 130 green runs).
- **Transcript-blackout work**, server and client halves — **incomplete, see below**.
- **Recap card expands in place** in chat instead of navigating to the raw memory inspector.
- **Topbar light/dark switch** and the **connection-badge honesty fix**.
- **Two inert Dissent library ports** (`dissent_canonical.go`, `dissent_plan.go`) — pure, no I/O, not wired to anything, pinned by golden vectors generated from the real TypeScript.

## Why it was held rather than shipped

An adversarial review of the transcript-blackout work found that **the stall watchdog stamps its liveness signal downstream of the consent gate**, so the exact consent-starvation failure it was built to catch produces zero offered frames and never trips it. Shipping as-is would put a green "Live transcription" pill over the next hole, which is worse than today's behavior. Ten review findings are outstanding, two of them critical.

## Order of work

1. **Resume `wf_67fcb3b2-772`** — the live-office-incident and transcript-repair workflow. This is the last gate before the Wave 11 commit. Use `Workflow({scriptPath: "…/workflows/scripts/live-office-incident-and-transcript-repairs-wf_67fcb3b2-772.js", resumeFromRunId: "wf_67fcb3b2-772"})`. Three of four agents completed and replay from cache; one repair was mid-flight. Its script already restates all ten transcript findings verbatim, so **do not also run `wf_f84c1142-10a`** — it is subsumed.
2. **Full suite:** `go test -count=1 -timeout 40m .` (~15 min). **HEAD is not green.** Baseline these three known-red tests before blaming the diff: `TestDeckStudioRenderedFitEditSaveAndImageJourney`, `TestPD1RenderedBrowserNavigationHistoryFocusAndLayout`, `TestReadinessScoutLanesReportIdleNotDegradedWithHonestShape`.
3. **Commit Wave 11 on main.** Fetch and rebase first, never checkout/reset. Scope the commit to exclude `apple/`, `docs/plans/stride-native-macos-*`, `docs/qa/STRIDE-Native-Media-Local-QA.txt`, `docs/qa/stride-macos-media-ab.md`, `scripts/build-macos-dmg.sh`. Push to `axx/main`.
4. **OPS-7 deploy** via the exact-release ceremony with rooms empty, plus an independent verifier stage. AJ was sitting in the office room at the pause, so check the idle gate first.
5. **Re-run `wf_e220bab1-149`** (degraded-lane diagnosis). All ten of its agents died on Anthropic 529s and nothing was captured, so this is a full re-run. The design is sound and worth reusing as-is: one evidence agent takes two production snapshots four minutes apart (a single sample cannot distinguish "stuck" from "cycling"), then one analyst per lane, each adversarially challenged, then a converged plan that splits fixes into ships-with-the-commit / needs-code / needs-a-server-op / needs-AJ.
6. **Fix the private-voice recording gap and the disabled-vs-idle labelling** (see open bugs).
7. **Ping AJ before starting Wave 12** — he asked to be notified rather than have it start automatically.

### Other resumable runs

- `wf_718f1dbd-d46` — recap card and Scout lanes. Scouts and the recap-card build are cached; the lanes build, both reviews and the gate died on 529s.
- Always read a run's `journal.jsonl` before diagnosing an empty result. A cached result can itself be empty.

## Open bugs, found but NOT fixed

**Live office incident**, captured 2026-09-03 ~16:03Z while AJ sat in the office with a dead transcript. This is a live reproduction of the 2026-09-02 blackout class and the strongest evidence available.

Observed: `participants: 1`, `media: {active: true, actor: true, mixer: false, status: degraded}`, `stt: {connected: true, status: healthy}`, `transcript.freshness: missing`, `mediaGeneration: 1`, `sittingId: meeting-20260902-220131-745713862` (minted ~18 hours earlier), and `meetingSTT: {allocated: true, connected: true, lastSuccessAt: 2026-09-03T15:55:12.937Z, lagSeconds: 486, stale: true}`.

- **(A) The office can never report healthy media.** `room_capability_health.go:103-110` requires `pointer.media && pointer.mixer` where `pointer.mixer = state.mixer != nil`, but `ensureRoomMedia` (`room_live.go:1553`) delegates the office to `ensureOfficeMedia` (`kanban.go:985`), which never assigns `state.mixer` — the office uses the package-level `roomMixer` (`kanban.go:1016-1018`). So `rooms.office.media` has read degraded whenever the office is active, permanently, with nothing wrong. `rooms.office.scout` looks like the same class: an uninvited Scout is absent, not broken.
- **(B) A rejoin inherits a dead sitting.** `ensureOfficeMedia` computes a fresh `sittingID` at `kanban.go:986-989` but assigns `state.mediaSittingID` only inside `if state.mediaActor == nil` (`:992-1005`). A join finding a surviving actor keeps the old sitting, and `kanban.go:1017` then re-arms the audio activity listener with that stale id on every call. **Settle this before fixing past it: are today's transcript rows written under the fresh id (reporting bug) or appended into yesterday's sitting (data-integrity bug)?** A missing transcript artifact alongside a real STT success today points at the second. Also unexplained: why `mediaActor` survived the previous sitting, and why `mediaGeneration` is only 1 on a server up ten hours.

**Private voice lane can never report healthy.** There is no `recordCapabilitySuccess` call site for it anywhere in the repo, so its row reads "last success — none yet" permanently no matter how many sessions succeed. Related: production runs `PRIVATE_REALTIME_VOICE_QUALIFIED=false`, and a deliberately-off capability must read `disabled`, not `idle`.

**Answered, needs no product change:** AJ asked why the Settings Scout lanes read "idle / none yet". Neither of his theories was right. The code is deployed and the OpenAI key is present — a missing key would force every lane to `degraded`, so `idle` itself proves the key is there. The capability evidence store is a plain in-memory map with a five-minute window and no persistence, the gen-249 deploy restarted the process at ~05:54:36Z, and no typed Scout turn had reached that server since. Three independent proofs of zero traffic: no `lastSuccessAt`, no `lastPollAt` (and the poll fires *before* the provider call), and `breaker.Known=false`, which the code documents as distinguishing "never used" from "closed after traffic".

## Wave 12 scope (after AJ approves)

Eleven deliverables. The harness: streaming typed answers over the existing websocket, widening the typed tool loop from 4 tools to the product set with per-tool authority, inline collapsible step cards, budgeted turns replacing the fixed 16-turn cap, the Anthropic seat runner, an eval bake-off before any seat flips, and provider stamps.

Dissent is D8 and D10. D8 wires the ported bones in as a STRIDE sub-product behind a `workExecutor` interface routing Packaging Studio deliverables and judgment seats (`direct` for reversible work, `full_dissent` for consequential). D10 is the founder-only admin panel under Settings: token flow per provider/seat/model/day, assurance analytics, receipts browser with verification, qualification registry and per-seat routing controls, capacity view. `dissent_admin.go` gated on `isFounderOwner`, three-registry rule. The canonicalizer and plan compiler are already written and pinned, so what remains is wiring, not cryptography. External onboarding (MCP/REST for outside harnesses) is explicitly deferred past D10. OPS-8 closes the wave and is where the staged Anthropic key is applied.

**Founder doctrine:** the PLATFORM chooses the model per seat, server-owned. Users never see a model picker.

## Verification traps in this repo (learned the hard way)

- **Browser-pane screenshots lag the DOM.** Assert state with JavaScript; use screenshots only for visual judgement. This killed three false findings.
- `index.html` is `os.ReadFile` **once at boot** — restart the server for HTML changes, no rebuild. Rebuild `ma` after Go edits.
- **Three-registry rule for new HTTP routes:** `main.go` HandleFunc + a row appended at the END of `authorization_surfaces.go` + a probe in `guest_allowlist_test.go`.
- Chat JS inside `index.html` is tab-indented in some regions and space-indented in others. Match the surrounding block.
- Light mode is the **absence** of `data-theme`, not a value. Resolve it as `dataset.theme === 'dark' ? 'dark' : 'light'` or assertions pass on `undefined`.
- Before re-pinning a failing literal test, check whether the **product** contract still holds. Two "bugs" here turned out to be stale pins.
- A test that passes without its fix is worthless. This project has already shipped one vacuous assertion that an earlier guard satisfied before the code under test was reached. When you add a pin, revert the fix in an isolated copy and confirm the test goes red.
