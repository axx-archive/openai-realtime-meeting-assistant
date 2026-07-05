# DigitalOcean VPS deployment

This deployment runs the meeting assistant as a long-lived Go server behind Caddy.
Caddy terminates HTTPS/WSS, and Docker publishes a small UDP range for WebRTC media.

## Droplet requirements

- Ubuntu 24.04 LTS or 22.04 LTS.
- TCP ports 80, 443, and 3478 open.
- UDP ports 3478, 40000-40100, and 49160-49200 open.
- A public IPv4 address.
- A DNS host that points at the Droplet. For a quick demo, use `PUBLIC_IP.nip.io`.
- Enough sustained outbound bandwidth for the room size. The default 10-seat video room needs roughly 110 Mbps egress before protocol overhead, so leave comfortable headroom.

## One-time Droplet setup

SSH into the Droplet and run:

```bash
sudo ./deploy/digitalocean/bootstrap-ubuntu.sh
```

Copy `.env.example` to `.env`:

```bash
cd deploy/digitalocean
cp .env.example .env
```

Edit `.env`:

```bash
OPENAI_API_KEY=sk-proj-...
OPENAI_REALTIME_MODEL=gpt-realtime-2
OPENAI_REALTIME_REASONING_EFFORT=high
OPENAI_REALTIME_VAD_TYPE=server_vad
MEETING_TRANSCRIPT_LANE_ENABLED=true
OPENAI_TRANSCRIPT_MODEL=gpt-realtime-whisper
MEETING_ROOM_PASSWORD=<room-passcode>
MEETING_ROOM_MAX_PARTICIPANTS=10
MEETING_ALLOWED_ORIGINS=https://<droplet-public-ip>.nip.io
MEETING_MEMORY_PATH=/app/data/meeting-memory.jsonl
MEETING_BRAIN_INTERVAL=5m
OPENAI_BRAIN_MODEL=gpt-5.5
MEETING_BRAIN_BACKFILL=false
MEETING_TIME_ZONE=America/Los_Angeles
PION_NAT1TO1_IP=<droplet-public-ip>
PION_UDP_PORT_RANGE=40000-40100
# TURN relay fallback for restrictive networks:
MEETING_STUN_URLS=stun:stun.l.google.com:19302
MEETING_TURN_URLS=turn:<domain>:3478?transport=udp,turn:<domain>:3478?transport=tcp
MEETING_TURN_SECRET=<openssl-rand-hex-32>
MEETING_TURN_REALM=<domain>
MEETING_HOST=<droplet-public-ip>.nip.io
```

To let Scout launch real Codex work threads from Realtime 2 or private Chat, enable the sidecar runner. The public `meetingassist` container only enqueues jobs and receives signed callbacks; Codex CLI auth lives in the private `codex-runner` service volume.

Prepare a real git checkout for Codex. The deployed `/opt/meetingassist` app copy may be an rsynced snapshot, so prefer a separate checkout such as `/opt/meetingassist-workspace`:

```bash
git clone <repo-url> /opt/meetingassist-workspace
cd /opt/meetingassist-workspace
git checkout main
```

Then add the runner settings to `.env`:

```bash
BONFIRE_AGENT_THREAD_WORKER=codex_exec
BONFIRE_CODEX_RUNNER_MODE=sidecar_queue
BONFIRE_CODEX_WORKSPACE_HOST_PATH=/opt/meetingassist-workspace
BONFIRE_CODEX_CWD=/workspace/meetingassist
BONFIRE_CODEX_QUEUE_PATH=/app/data/codex-runner-jobs
BONFIRE_RUNNER_TOKEN=<openssl-rand-hex-32>
BONFIRE_CODEX_SANDBOX=workspace-write
BONFIRE_CODEX_APPROVAL_POLICY=never
BONFIRE_CODEX_REASONING_EFFORT=high
BONFIRE_CODEX_TIMEOUT=20m
# Only for a deliberately prepared non-git directory, not the normal VPS path:
# BONFIRE_CODEX_SKIP_GIT_REPO_CHECK=true
```

Start the sidecar profile:

```bash
docker compose --profile codex up -d --build
```

The `codex-runner` service grants `SYS_ADMIN` with unconfined seccomp/AppArmor so the Codex CLI can create its Linux sandbox with `bubblewrap`. Keep that permission scoped to the private runner sidecar; the public `meetingassist` container should not receive Codex auth or these sandbox capabilities.

Populate the private Codex auth volume by running the Codex login command through the runner image:

```bash
docker compose --profile codex run --rm --entrypoint codex codex-runner login
docker compose --profile codex exec codex-runner codex --version
```

Keep this disabled until the runner host is intentionally prepared. Realtime can start `read_only` and `workspace_write` jobs. Commit, push, deploy, SSH, external APIs, email, and production mutations are blocked behind an approval-required artifact until an operator approves or rejects the gate in the Artifacts app.

To activate the Fable 5 orchestrator (goals, grill reports, packaging deliverables) once a live `ANTHROPIC_API_KEY` is available, follow the Anthropic block in `.env.example` and the step-by-step runbook in `docs/ops3-fable-activation.md` (env lines, restart, and the `/assistant/goal` liveness check). Pin `BONFIRE_CODEX_MODEL=gpt-5.5` at the same time so the sidecar never runs on the CLI's default model.

For a real domain, set `MEETING_HOST` to the domain after creating an A record that points at the Droplet.

To email generated meeting notes when **Send notes** archives the room, also configure SMTP:

```bash
MEETING_NOTES_SMTP_HOST=smtp.example.com
MEETING_NOTES_SMTP_PORT=587
MEETING_NOTES_SMTP_USERNAME=...
MEETING_NOTES_SMTP_PASSWORD=...
MEETING_NOTES_SMTP_FROM=meeting-notes@shareability.com
```

## Launch

From the repo root on the Droplet:

```bash
cd deploy/digitalocean
docker compose up -d --build
```

The room will be available at:

```text
https://$MEETING_HOST
```

Open the URL, choose a listed participant name, enter the room password, click Join room, and allow camera and microphone access. Other participants can join the same URL natively in the browser.

## Operations

View logs:

```bash
docker compose logs -f
```

Restart after code changes:

```bash
docker compose up -d --build
```

Stop:

```bash
docker compose down
```

This demo has a lightweight room gate enforced by the server-side participant/password check. Treat it as a meeting-room passcode, not as full identity or account authentication.
