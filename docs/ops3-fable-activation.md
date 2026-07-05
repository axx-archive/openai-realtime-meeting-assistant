# OPS-3 runbook — activate Fable on the live VPS

> **STATUS: NOT YET EXECUTED.** This runbook, the `.env.example` block, and the
> compose/README notes are the *preparation* for OPS-3 — they do not close it.
> As of 2026-07-05 the live `/opt/meetingassist` env has no `ANTHROPIC_API_KEY`,
> no `BONFIRE_AGENT_RUNNER` pin, and no `BONFIRE_CODEX_MODEL=gpt-5.5` pin:
> Fable remains dead code in production and grill scores still run on the
> unpinned sidecar fallback. Wave 1 item 1 stays OPEN until someone runs
> sections 1–3 below against the VPS (env backup, activation block, restart,
> `/assistant/goal` liveness check) and records the before/after
> `grep -E 'ANTHROPIC|BONFIRE_AGENT_RUNNER|BONFIRE_CODEX_MODEL' .env` output
> plus the 30-day-retention confirmation here.

Purpose: the moment the founder supplies the live `ANTHROPIC_API_KEY`, flip the
deployed Bonfire OS from the unpinned Codex fallback to the Fable 5
orchestrator, pin the sidecar model, and prove it live. Until this ships,
`launchGoalThread` 503s keyless and every agentic run (goals, grill reports,
packaging deliverables) falls back to the Codex sidecar on an unpinned model.

Prereqs (once, before touching the VPS):

- **30-day retention check**: in the Anthropic Console, confirm the org that
  owns the key is on 30-day data retention (not zero data retention).
  `claude-fable-5` returns `400 invalid_request_error` on *every* request from
  a ZDR org — if activation 400s with a valid-looking payload, this is the
  first thing to check.
- Key in hand: `sk-ant-...`, scoped to that org.

## 1. Edit the live env on the VPS

App lives at `/opt/meetingassist` on `root@146.190.171.224` (thebonfire.xyz);
compose dir is `/opt/meetingassist/deploy/digitalocean`. The live `.env` there
is NOT tracked in git — never rsync over it.

```bash
ssh root@146.190.171.224
cd /opt/meetingassist/deploy/digitalocean

# Back up the live env first (per bonfire-vps-deploy-ops)
backup_dir="/opt/meetingassist-backups/$(date +%Y%m%d-%H%M%S)-ops3-fable" \
  && mkdir -p "$backup_dir" && cp .env "$backup_dir"/

# Record the before state (should show no ANTHROPIC key yet)
grep -E 'ANTHROPIC|BONFIRE_AGENT_RUNNER|BONFIRE_CODEX_MODEL|ORCHESTRATOR|DELIVERABLE' .env || true
```

Append the activation block to `.env` (same names as
`deploy/digitalocean/.env.example` — the dial names must match
`agent_runner_anthropic.go` exactly):

```bash
ANTHROPIC_API_KEY=sk-ant-...        # the founder's live key
BONFIRE_AGENT_RUNNER=anthropic_fable
BONFIRE_CODEX_MODEL=gpt-5.5         # pin the sidecar; unset = CLI default

# Spec dials (packaging-os-analysis-2026-07-05): Fable 5 at effort high
BONFIRE_ORCHESTRATOR_EFFORT=high
BONFIRE_DELIVERABLE_EFFORT=high
```

Leave the existing `BONFIRE_AGENT_THREAD_WORKER=codex_exec` /
`BONFIRE_CODEX_RUNNER_MODE=sidecar_queue` lines in place — the explicit
`BONFIRE_AGENT_RUNNER=anthropic_fable` outranks them for orchestration, and
the sidecar remains the execution runner for shell/repo subtasks
(`BONFIRE_EXECUTION_RUNNER` default).

## 2. Restart the containers

Env-only change still needs a container restart to be picked up. If code also
changed, rsync per `bonfire-vps-deploy-ops` first.

```bash
cd /opt/meetingassist/deploy/digitalocean
docker compose up -d --build
docker compose ps
docker compose logs --tail=40 meetingassist
```

The compose file passes the whole `.env` into both the `meetingassist` and
`codex-runner` services via `env_file`, so no compose edit is needed.

## 3. Verify Fable is live

**One-line curl** — `/assistant/goal` is the seam: keyless it returns
`503 {"error":"the goal engine is not configured here"}`; with Fable live it
returns `202`. The endpoint needs a signed-in session cookie (login body is
`{"name": ..., "password": ...}`):

```bash
# Sign in (any of the six roster accounts) and keep the cookie
curl -fsS -c /tmp/bonfire.cookies -H 'content-type: application/json' \
  -d '{"name":"aj","password":"<password>"}' https://thebonfire.xyz/auth/login

# The check: non-503 (expect HTTP 202) proves the goal engine is configured
curl -s -o /tmp/goal.json -w '%{http_code}\n' -b /tmp/bonfire.cookies \
  -H 'content-type: application/json' -H 'Origin: https://thebonfire.xyz' \
  -d '{"objective":"OPS-3 smoke: reply DONE and stop","authorityHint":"read_only","originSurface":"ops3-runbook"}' \
  https://thebonfire.xyz/assistant/goal
```

- `503` → key not visible in the container (check `docker compose exec
  meetingassist env | grep ANTHROPIC`) or the runner degraded keyless.
- `202` → the goal launched and the engine is configured. Then confirm Fable
  provenance on the running artifact: as the run progresses its metadata
  accrues `worker=anthropic_fable`, `orchestratorModel=claude-fable-5`,
  `orchestratorEffort=high` (open the thread in the Artifacts app, or re-fetch
  the artifact), and the finished body carries an "Orchestrator evidence"
  footer with runner/model/effort/turns.
- A launched-then-failed goal with a 400 mentioning the request being invalid
  → re-check the 30-day retention prereq above.

Also confirm the sidecar pin took:

```bash
docker compose exec codex-runner env | grep BONFIRE_CODEX_MODEL   # gpt-5.5
```

## 4. Roll back

Remove the appended lines from `.env` (or restore the backup from
`/opt/meetingassist-backups/`), then `docker compose up -d`. Keyless deploys
degrade gracefully to today's worker — nothing else changes.

## 5. Render-runner sidecar (Wave 3 item 14b — PDF export)

The PDF export toolchain ships as a second sidecar on the codex-runner
chassis: the same Go binary in `-render-runner` mode, built from
`Dockerfile.render` (base image + chromium + poppler-utils + fonts), behind
the compose profile `render`. It claims `export_pdf` jobs lexically-first
from the shared `meeting_data` volume at `/app/data/render-jobs` and POSTs
results back with `Bearer BONFIRE_RUNNER_TOKEN` — the SAME token the codex
sidecar already uses, so no new secret is needed in `.env`.

```bash
cd /opt/meetingassist/deploy/digitalocean
docker compose --profile render up -d --build render-runner
docker compose logs --tail=40 render-runner   # "Render runner started id=... queue=/app/data/render-jobs"
```

Dials (all optional; the image defaults are correct): `RENDER_CHROMIUM_BIN`
(default `chromium`), `RENDER_PDFTOPPM_BIN` (default `pdftoppm`),
`BONFIRE_RENDER_QUEUE_PATH`, `BONFIRE_RENDER_CALLBACK_URL`,
`BONFIRE_RENDER_TIMEOUT` (default 3m), `BONFIRE_RENDER_MAX_PDF_BYTES`.

The flatten law is enforced in the runner, not by ops: deck exports ship the
144dpi JPEG-flattened raster PDF (the layered chromium print never leaves the
work dir); the paper kit ("The Talk"/"The Wall") ships chromium's direct
text-native print. Page JPEGs land beside the PDF in
`/app/data/render-jobs/<job-id>-out/` for Wave 5's vision slide juries.

Graceful absence: with the profile off (or chromium/pdftoppm missing) the
heartbeat at `/app/data/render-runner-heartbeat.json` is absent/stale and
jobs fail with an operator message naming the missing binary — the OS side
surfaces this as "render sidecar not available". Nothing here needs an API
key.

NOTE (stage B, still open at this writing): the `-render-runner` flag in
main.go, the `/internal/render/jobs/result` callback route, and the "Export
PDF" trigger are stage-B wiring onto the exported seams `runRenderRunnerLoop`
/ `enqueueRenderExportPDFJob` / `readinessRenderRunnerSnapshot`
(render_runner.go). Until stage B lands, keep the `render` profile off — the
container would exit at boot on the unknown flag.
