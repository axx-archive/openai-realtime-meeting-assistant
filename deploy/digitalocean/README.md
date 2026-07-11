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

### Workflow ticker (card 067)

A model-free, ~5-minute status re-scan (`workflow_ticker.go`) that relaunches only human-APPROVED work: a proposal a human confirmed whose launch crashed before stamping a thread, and any `auto_run`-lane proposal carrying a recorded standing approval. It only ever launches one agent thread per proposal (never `/goal` or the packaging studio) and is capped per pass, so token cost is bounded. Finished work is delivered back to the originating public channel, else a best-match channel, else `#general` with a disclosed routing note. Defaults are safe; leave these unset to accept them.

```bash
BONFIRE_WORKFLOW_TICKER_INTERVAL=5m      # duration; 0/off/false/disabled turns it off
BONFIRE_WORKFLOW_TICKER_DISABLED=false   # truthy disables the ticker entirely
BONFIRE_WORKFLOW_TICKER_MAX_PER_PASS=2   # max launches per tick
```

Its live config and last-pass counters appear under `checks.agents.workflowTicker` in `/readyz`.

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

The **add to calendar** buttons on a card's key dates (card 084) need no config —
`GET /calendar/event.ics` serves a downloadable all-day `.ics` for any key date.
The reserved Google Calendar sync seam stays dark until all three creds are set:

```bash
GOOGLE_CALENDAR_CLIENT_ID=...
GOOGLE_CALENDAR_CLIENT_SECRET=...
GOOGLE_CALENDAR_REDIRECT_URL=https://$MEETING_HOST/calendar/google/callback
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

### Data backups (memory-architecture study §6 Phase 0.1)

The entire company brain — meeting memory, board, decisions, users, sessions, rooms, archives — lives in the `data/` volume on this one Droplet. `data/` survives `docker compose up -d --build`, but a disk failure or an accidental wipe is otherwise **total, permanent loss**. To close that, the server runs a nightly in-process snapshot worker (started at boot alongside the other background workers; no cron needed). Configure it with the `BACKUP_*` keys documented in `.env.example`.

**What it does.** Once every `BACKUP_INTERVAL_HOURS` (default 24; first run ~3 min after boot so a fresh deploy proves the path) it tar.gz's the data dir — `meeting-memory.jsonl`, `kanban-board.json`, `meetings.json`, `notifications.json`, `users.json`, `sessions.json`, `rooms.json`, `file-folders.json`, `archives/`, `archive-secret`, `vapid-keys.json`, and any other sibling state files that exist — **plus the `blobs/` upload store (every uploaded file body) by default**, so a restore is complete; the `backups/` dir itself is never included. Set `BACKUP_INCLUDE_BLOBS` to a falsy value (`0`/`off`/`false`/`no`) to exclude `blobs/` and shrink the snapshot — but a restore then comes back with no uploaded file bodies. Each snapshot logs its size (blobs make it larger); watch that line if disk or offsite bandwidth is tight. Each file is read whole so a concurrent append can never corrupt the archive; no app lock is held. Because the store is append-mostly, the worst case a restore can lose is the current day's tail — versus today's exposure of losing everything.

**Where snapshots land.**
- **Local ring** (always): `/app/data/backups/bonfire-data-<UTC-timestamp>.tgz` (or `.tgz.enc` when encrypted). The newest `BACKUP_RING_KEEP` (default 7) are kept; older ones are deleted. This ring lives inside the same volume it protects, so it survives rebuilds but is **NOT offsite** — a disk loss takes it too.
- **Offsite** (when the `BACKUP_S3_*` block is set): the encrypted snapshot is PUT to your S3/Spaces bucket via stdlib SigV4. This is the real disaster-recovery copy. **Offsite requires `BACKUP_ENCRYPTION_KEY`** — with S3 configured but no key, the worker takes the local snapshot and refuses to upload (fail-closed; it will not ship the brain off the box in the clear, and logs a warning).

Check the boot log to confirm posture: it prints one line stating whether offsite is armed, dormant, or skipped-for-no-key, and each snapshot logs its size, duration, ring count, and offsite result.

**Restore.** Pick the newest good snapshot (from `/app/data/backups/`, or downloaded from the bucket).

1. Stop the app so nothing writes mid-restore:
   ```bash
   cd /opt/meetingassist/deploy/digitalocean && docker compose down
   ```
2. If the snapshot is **encrypted** (`.tgz.enc`), decrypt it to a plain `.tgz` first. `openssl enc` cannot handle AES-GCM from the CLI, so use this self-contained Go decryptor (the file format is `magic(8) | nonce(12) | ciphertext+tag`, GCM-authenticated with `BACKUP_ENCRYPTION_KEY`). Save it as `decrypt-backup.go`:
   ```go
   package main

   import (
       "bytes"
       "crypto/aes"
       "crypto/cipher"
       "crypto/sha256"
       "encoding/base64"
       "encoding/hex"
       "os"
       "strings"
   )

   // deriveKey mirrors the server's deriveBackupKey EXACTLY: a value that
   // hex-, standard-base64-, or RAW (unpadded) base64-decodes to 32 bytes is used
   // as the raw key; anything else is a passphrase SHA-256'd to 32 bytes. The
   // raw-base64 branch matters — a 32-byte key pasted without '=' padding is
   // valid to the server and must decrypt here too.
   func deriveKey(raw string) []byte {
       raw = strings.TrimSpace(raw)
       if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
           return b
       }
       if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
           return b
       }
       if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
           return b
       }
       s := sha256.Sum256([]byte(raw))
       return s[:]
   }

   // usage: BACKUP_ENCRYPTION_KEY=... go run decrypt-backup.go in.tgz.enc out.tgz
   func main() {
       blob, err := os.ReadFile(os.Args[1])
       if err != nil {
           panic(err)
       }
       magic := []byte("BFBKUP01")
       block, err := aes.NewCipher(deriveKey(os.Getenv("BACKUP_ENCRYPTION_KEY")))
       if err != nil {
           panic(err)
       }
       gcm, err := cipher.NewGCM(block)
       if err != nil {
           panic(err)
       }
       ns := gcm.NonceSize()
       // Guard the layout before slicing so a truncated/foreign download fails with
       // a clear message instead of an index-out-of-range panic.
       if len(blob) < len(magic)+ns || !bytes.Equal(blob[:len(magic)], magic) {
           panic("not a BFBKUP01 snapshot (too short or wrong magic): " + os.Args[1])
       }
       plain, err := gcm.Open(nil, blob[len(magic):len(magic)+ns], blob[len(magic)+ns:], magic)
       if err != nil {
           panic("decrypt failed (wrong key or tampered file): " + err.Error())
       }
       if err := os.WriteFile(os.Args[2], plain, 0o600); err != nil {
           panic(err)
       }
   }
   ```
   Run it where Go is available (your laptop, or `docker run --rm -v "$PWD":/w -w /w -e BACKUP_ENCRYPTION_KEY golang:1.25 go run decrypt-backup.go snap.tgz.enc snap.tgz`). A plain `.tgz` from an unencrypted ring skips this step entirely.
3. Untar into the volume, replacing the current data dir contents:
   ```bash
   tar xzf snap.tgz -C /var/lib/docker/volumes/<meetingassist-data-volume>/_data
   # or, if you bind-mount, into /opt/meetingassist/data
   ```
4. Bring the app back up and verify:
   ```bash
   docker compose up -d --build
   curl -s https://$MEETING_HOST/readyz | jq .checks.memoryFile
   ```

To pull an offsite snapshot first, use any S3 client against the bucket (DO Spaces works with `aws s3 cp --endpoint-url https://<region>.digitaloceanspaces.com s3://<bucket>/<prefix>/<file> .`, `s3cmd`, `rclone`, or the DO control panel), then run the same steps.

### Web Push / installable PWA (card 089)

Bonfire installs to a phone home screen and can send Web Push notifications for durable alerts (chat mentions, task proposals, agent milestones). This needs no configuration: on first boot the server mints a VAPID keypair and writes it to `/app/data/vapid-keys.json`, and device subscriptions live in `/app/data/push-subscriptions.json`. Both sit under `data/`, which is already preserved across `docker compose up -d --build`, so pushes survive redeploys. To pin your own keypair (e.g. so subscriptions survive a `data/` wipe) set `WEB_PUSH_VAPID_PUBLIC_KEY` + `WEB_PUSH_VAPID_PRIVATE_KEY`. The container must be able to reach the push services over HTTPS (`fcm.googleapis.com`, `web.push.apple.com`, `push.mozilla.org`); a standard Droplet already has `ca-certificates` and open outbound 443. iOS caveat: push there works only from a home-screen install launched standalone (iOS 16.4+) — a Safari tab has no Notification API, and there is no install prompt, so users add Bonfire via Share → Add to Home Screen.

### Background blur — vendored MediaPipe assets (card 079)

The "blur bg" video look runs person-segmentation on the client via a pinned MediaPipe Tasks Vision build vendored under `public/video-blur/` (~9 MB wasm + a ~250 KB model). There is **no runtime CDN dependency and no new env var** — the files are committed to the repo, the Dockerfile already `COPY public /app/public`, and rsync deploys carry them unchanged. The asset handler serves them with a long-lived `Cache-Control: public, max-age=604800, immutable`, and the browser fetches them lazily only when a user selects blur, so nobody pays the download otherwise (blur is insertable-tier only: Chrome/Edge/Android; other browsers show an honest "using raw camera" status). To re-derive or verify the exact bytes: `node scripts/vendor-video-blur.mjs --check` (drop `--check` to re-download the pinned version; the sha256 of each file is recorded in the script and in `public/video-blur/MEDIAPIPE_TASKS_COPYING.txt`).
