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
OPENAI_REALTIME_VAD_TYPE=server_vad
# Keep false until the exact-release activation transaction below.
PRIVATE_REALTIME_VOICE_QUALIFIED=false
MEETING_TRANSCRIPT_LANE_ENABLED=true
MEETING_ROOM_PASSWORD=<room-passcode>
MEETING_ROOM_MAX_PARTICIPANTS=10
MEETING_ALLOWED_ORIGINS=https://<droplet-public-ip>.nip.io
MEETING_MEMORY_PATH=/app/data/meeting-memory.jsonl
BONFIRE_CANONICAL_POSTGRES_PASSWORD=<openssl-rand-hex-32>
BONFIRE_CANONICAL_DATABASE_URL=postgres://bonfire:<same-password>@canonical-postgres:5432/bonfire?sslmode=disable
BONFIRE_CANONICAL_TENANT_ID=bonfire
BONFIRE_CANONICAL_MODE=shadow
MEETING_BRAIN_INTERVAL=5m
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

The OpenAI provider/model/reasoning matrix is compiled into the server and is
not deployment configuration: Luna/medium routing and extraction,
Terra/high conversation/brain/board/house-style/taste/tool work, Sol/high
research/orchestration/narrative/deliverables, Sol/max final review, Realtime
2.1 at medium for both private conversational voice and shared rooms, GPT Live Transcribe for live input,
GPT Transcribe for committed/file input, GPT Image 2 high, and
text-embedding-3-small. Stale model/effort variables are ignored and should be
removed from the live environment rather than copied into a new release.

Private native voice has two independent release keys. Build 73's production
EAS profile sets `EXPO_PUBLIC_NATIVE_REALTIME_VOICE_ENABLED=true`, and the VPS
must separately set `PRIVATE_REALTIME_VOICE_QUALIFIED=true` before
`/client-config` qualifies a signed-in launcher. Missing, empty, malformed, and
false server values all fail closed. Source or EAS configuration is not proof
that the server key is live, that OpenAI accepted a call, or that iPhone/iPad
audio works.

Activate the server key only through the transactional exact-release arguments
documented below. The tool binds the exact prior env-file digest, changes only
one canonical `PRIVATE_REALTIME_VOICE_QUALIFIED=false` assignment to `true`, or
appends one canonical `true` assignment when the key is entirely absent. It does
not normalize any other byte. It preserves a mode-`0600` backup and writes a
root-private digest-only receipt under the mode-`0700`, root-owned
`/opt/meetingassist-backups` directory. The receipt contains no env values or
secret bytes; it binds the before/after SHA-256, exact target commit, retained
rollback commit, ledger generation, prior absent-or-false state, and transaction
state.

The currently serving retained release tool must already implement that
transaction. If it does not, use two distinct exact releases: first activate and
verify a no-flag bootstrap release with the old retained tool while the base env
remains byte-identical and fail closed (the current generation-170 production
baseline has the key absent); then use that newly retained bootstrap tool to
activate a distinct successor with the qualification arguments. Never run the
candidate tool, hot-edit a retained bundle, normalize the absent key to `false`,
or mutate the base env outside the release transaction to collapse those two
releases.

After activation, require the committed env-patch receipt, exact-release
verifier, active ledger, running image IDs, public `/healthz` and `/readyz`, and
an authenticated `/client-config` read to agree. Those are server activation
evidence only. A fresh accepted private provider call is the separate provider
receipt; signed-in physical phone and iPad microphone/playback runs are the
separate device acceptance. Do not claim either from source, EAS configuration,
or the server receipt.

An absent or literal `false` value is fail closed, not a hot-reloaded process
flag. Apply a qualification transition atomically and replace the container
through the retained exact-release tool. An unqualified process refuses every
new offer and tool effect; an exact active lease that reaches Renew is durably
marked `qualification_revoked`. The native client renews every 10 seconds. Each
request has a dynamic deadline of at most five seconds and strictly before its
exact-generation local watchdog at `leaseExpiresAt - 3s`. If Renew never
settles, the watchdog still enters visible teardown and synchronously closes the
peer and microphone tracks before native audio deactivation; the 30-second
server TTL remains the final authority bound. Stop remains admitted so cleanup
is never blocked. Do not describe the env change as instantaneous without the
container replacement receipt.

Codex-style server-side execution is disabled in the production-style Compose
candidate. The former reusable `codex-runner` image/profile was not a qualified
per-run E9 sandbox: it combined provider credentials with a broad host
workspace and ordinary network access. Its image target and Compose service
are removed. Do not recreate them or add
`BONFIRE_AGENT_THREAD_WORKER=codex_exec` to production.

The app may retain queued control-plane records, but no local Compose worker
executes them. Production execution requires a separately reviewed external
per-run orchestrator, default-deny egress gateway, short-lived credential
broker, bounded mounts/quotas, signed nonce-replay callback receipts, and its
own digest-pinned release evidence. See
`docs/e9-operations-runbook.md#worker-isolation-boundary`. An authorized
cutover must also inventory and remove any already-running orphan/legacy
runner container; deleting the service from this file does not stop one.

### Workflow ticker (card 067)

A model-free, ~5-minute status re-scan (`workflow_ticker.go`) that relaunches only human-APPROVED work: a proposal a human confirmed whose launch crashed before stamping a thread, and any `auto_run`-lane proposal carrying a recorded standing approval. It only ever launches one agent thread per proposal (never `/goal` or the packaging studio) and is capped per pass, so token cost is bounded. Finished work is delivered back to the originating public channel, else a best-match channel, else `#general` with a disclosed routing note. Defaults are safe; leave these unset to accept them.

```bash
BONFIRE_WORKFLOW_TICKER_INTERVAL=5m      # duration; 0/off/false/disabled turns it off
BONFIRE_WORKFLOW_TICKER_DISABLED=false   # truthy disables the ticker entirely
BONFIRE_WORKFLOW_TICKER_MAX_PER_PASS=2   # max launches per tick
```

Its live config and last-pass counters appear under `checks.agents.workflowTicker` in `/readyz`.

Do not install or activate an Anthropic/Fable route. Provider-backed product
work is OpenAI-only, with model and reasoning assignments owned by the server.
`docs/ops3-fable-activation.md` is retained solely as superseded operational
history and must not be executed.

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

### Exact release identity (required before a production rollout)

The live directory is an rsynced application tree, not a Git checkout. Do not
derive a release identity from `/opt/meetingassist` or from a mutable
`meetingassist:local` tag. The release tool binds a reviewed clean exact commit
to an allowlisted source inventory, retained candidate Compose/Caddy
configuration, pinned build inputs, app and render-runner images,
digest-pinned Postgres/coturn/Caddy images, embedded binary fields, OCI labels,
running executables, and both `/healthz` and `/readyz`. The app and render
images come from the same archive and must contain byte-identical Go binaries.
The process reports only `processQualified:true` and `qualified:false`:
receipts remain unsigned local evidence. Independent signing, registry custody,
off-host verification, and production observation remain separate gates.

On a clean local checkout, fetch remote main and pass its full reviewed SHA. A
mutable ref such as `axx/main` is deliberately rejected by `prepare`:

```bash
git fetch --prune axx main
reviewed_sha="$(git rev-parse axx/main)"
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = "$reviewed_sha"
local_release_dir="/tmp/meetingassist-release-$reviewed_sha"
mkdir -m 700 "$local_release_dir"
node scripts/bonfire-release.mjs scope --reviewed-ref "$reviewed_sha"
node scripts/bonfire-release.mjs prepare \
  --reviewed-ref "$reviewed_sha" \
  --archive "$local_release_dir/source.tar" \
  --source-receipt "$local_release_dir/source-receipt.json"
ssh root@146.190.171.224 "mkdir -p -m 700 /opt/meetingassist-releases/$reviewed_sha"
rsync -av "$local_release_dir/source.tar" "$local_release_dir/source-receipt.json" \
  "root@146.190.171.224:/opt/meetingassist-releases/$reviewed_sha/"
```

On the VPS, build only the reviewed archive. The build re-extracts it and
independently recomputes its tree-equivalent, complete inventory, and config
digests before Docker runs. Base images are digest-pinned and Debian packages
come from the timestamped snapshot recorded in
`release-build-inputs.json`. `release.env` and all receipts contain no API keys,
but they are integrity inputs and must remain root-readable:

```bash
release_sha=<full-reviewed-commit>
release_dir="/opt/meetingassist-releases/$release_sha"
prior_sha=<currently-serving-full-reviewed-commit>
prior_dir="/opt/meetingassist-releases/$prior_sha"
mkdir -p -m 700 "$release_dir/tool"
tar -xf "$release_dir/source.tar" -C "$release_dir/tool" scripts/bonfire-release.mjs
node "$release_dir/tool/scripts/bonfire-release.mjs" build \
  --archive "$release_dir/source.tar" \
  --source-receipt "$release_dir/source-receipt.json" \
  --image "meetingassist:release-$release_sha" \
  --render-image "meetingassist-render:release-$release_sha" \
  --build-manifest "$release_dir/build-manifest.json" \
  --release-receipt "$release_dir/release-receipt.json" \
  --runtime-env "$release_dir/release.env"
node "$prior_dir/sealed-candidate/scripts/bonfire-release.mjs" activate \
  --release-dir "$release_dir" \
  --rollback-release-dir "$prior_dir" \
  --base-env /opt/meetingassist/deploy/digitalocean/.env \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz
```

That command is the ordinary/no-flag activation path. It is also the only safe
way to install the bootstrap release when the currently serving retained tool
predates transactional base-env patching. The bootstrap activation must leave
the canonical base env byte-for-byte unchanged. The actual generation-170
production baseline has `PRIVATE_REALTIME_VOICE_QUALIFIED` absent; do not add or
normalize it during bootstrap. Verify the fail-closed bootstrap and retain its
exact release directory and images before preparing the distinct qualification
successor.

Activation must be orchestrated by the private, read-only release tool from the
**currently serving rollback bundle** (`$prior_dir/sealed-candidate/...`), not
the candidate's newly built tool. It uses the target's sealed-candidate Compose
and Caddy files, the fixed `digitalocean`
Compose project, the existing secret-bearing base `.env`, named production
volumes, and `--no-build`. It first verifies that the complete retained rollback
bundle is exactly what is serving, rejects unexpected/orphan project
containers, and verifies both target and rollback image sets before mutation.
It must not copy secrets into the candidate bundle or switch project names.
One durable sibling lock serializes baseline verification, Compose mutation,
post-verification, and the ledger compare-and-swap. After successful probes it
atomically advances
`/opt/meetingassist-releases/active-release.json`, retaining exact active and
previous bundle/image identities. If target verification fails, the tool
automatically executes the retained rollback bundle's own verified tool,
restores its exact ledger, and verifies both before returning failure. An
ambiguous recovery leaves the operation lock in place and requires an explicit
operator inspection; never delete that lock merely because it is old.

### Transactional private Realtime qualification

Use this only for the distinct qualification successor, and only after its
currently serving retained bootstrap tool has itself been activated and
verified. The base env must be a root-owned mode-`0600` regular file whose exact
bytes either contain no mention or assignment of
`PRIVATE_REALTIME_VOICE_QUALIFIED` at all, or contain exactly one unquoted
canonical `PRIVATE_REALTIME_VOICE_QUALIFIED=false` line. The current production
baseline is the absent-key case. Comments mentioning the key, `export`, quotes,
whitespace variants, malformed values, and duplicates fail closed. The backup
root must be a root-owned, non-symlink mode-`0700` directory:

```bash
release_sha=<distinct-qualification-successor-full-commit>
release_dir="/opt/meetingassist-releases/$release_sha"
prior_sha=<verified-bootstrap-full-commit>
prior_dir="/opt/meetingassist-releases/$prior_sha"
base_env=/opt/meetingassist/deploy/digitalocean/.env
backup_dir=/opt/meetingassist-backups

install -d -o root -g root -m 0700 "$backup_dir"
test "$(stat -c '%U:%G:%a:%F' "$backup_dir")" = 'root:root:700:directory'
test "$(stat -c '%U:%G:%a:%F' "$base_env")" = 'root:root:600:regular file'
prior_env_sha256="$(sha256sum "$base_env" | awk '{print $1}')"

node "$prior_dir/sealed-candidate/scripts/bonfire-release.mjs" activate \
  --release-dir "$release_dir" \
  --rollback-release-dir "$prior_dir" \
  --base-env "$base_env" \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz \
  --target-base-env-expected-sha256 "$prior_env_sha256" \
  --target-base-env-patch-key PRIVATE_REALTIME_VOICE_QUALIFIED \
  --target-base-env-patch-value true \
  --target-base-env-backup-dir "$backup_dir"
```

The successful command emits one JSON line containing
`"qualificationState":"target"` and the exact root-private
`qualificationReceipt` path. Record that path as part of the release handoff;
do not print or copy the receipt or backup contents. The operation lock and
journal are removed after success, so this explicit receipt handoff is required
for the safe rollback command below.

The four `--target-base-env-*` arguments are an all-or-none, `activate`-only
contract. They are invalid for ordinary activation, rollback, resume, recover,
or verification; resume/recover uses only the redacted plan already bound into
the root-private transaction journal. Duplicate arguments, any other key or
value, any other backup path, digest drift, symlinks, non-root ownership, or
wrong modes fail closed before a target start.

Under the existing release lock, the retained baseline and prior env digest are
verified before mutation. The host qualification state must also exactly match
the currently serving application container; an out-of-band host/container
mismatch fails closed. The journal records patch intent; the tool writes and
fsyncs the mode-`0600` backup, atomically installs the target env, preflights and
starts the target privately, and requires the actual application container to
report `PRIVATE_REALTIME_VOICE_QUALIFIED=true`. It commits the receipt only after
private verification, ledger compare-and-swap, ingress opening, and the
ledger-bound external verifier all pass.

The secret-bearing atomic-write temporary is not random or untracked: its exact
mode-`0600` path is derived from the journaled transaction token beside the base
env. Resume/recover removes only that exact owner-private, single-link file
before replaying the write. This closes the SIGKILL window after temp fsync and
before rename without globbing for, logging, or retaining extra credential
copies.

If interruption occurs during the first exclusive backup write itself, the
canonical env is still prior and no receipt exists. No-op recovery inventories
and removes only that exact owner-private, single-link transaction backup before
releasing the lock; a wrong owner, mode, link count, or any receipt ambiguity
keeps the lock fail closed.

On failure, target-owned transition cleanup runs while the target env is still
installed. The tool then journals restore intent and atomically restores the
exact prior env before any retained-release preflight container, maintenance
container, or private application start. The retained application must inspect
with the exact receipted prior state: the key absent for an absent prior, or the
literal value `false` for a canonical-false prior. Only then may the prior ledger
and ingress be restored and externally verified. A third env digest or ambiguous
ledger retains the lock and starts nothing further.

After an abrupt process death, run the verified retained bootstrap tool against
the locked target. Supply neither target-patch nor rollback-receipt arguments;
the private journal already binds the exact transition:

```bash
node "$prior_dir/sealed-candidate/scripts/bonfire-release.mjs" resume \
  --release-dir "$release_dir" \
  --base-env "$base_env" \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz

# Choose recovery instead of forward resume only after inspecting the journal.
node "$prior_dir/sealed-candidate/scripts/bonfire-release.mjs" recover \
  --release-dir "$release_dir" \
  --base-env "$base_env" \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz
```

Do not delete the operation lock, edit the journal, repeat the target-only
arguments, or run either command from the candidate bundle.

After the container is healthy, verification is mandatory and fail-closed:

```bash
node "$release_dir/sealed-candidate/scripts/bonfire-release.mjs" verify \
  --release-dir "$release_dir" \
  --base-env /opt/meetingassist/deploy/digitalocean/.env \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz
```

The verifier refuses any service image-ID/state, owned OCI-label, app/render
`/proc/1/exe`, image-file, package inventory, render Chrome/heartbeat, mounted
Caddy configuration, runtime-environment, health, or readiness mismatch. It
reports `verified-local-unsigned`, never fully qualified. A local Docker image
ID is content-addressed but is not a registry signature or an off-host custody
record.

Keep every versioned release directory and its Docker image until its rollback
window closes; do not prune them. Before activation, record the currently active
release SHA and confirm its directory and image still exist. Exact rollback from
a qualified release additionally requires the exact `qualificationReceipt` path
emitted by that activation. It waits for Compose health and repeats both external
probes:

```bash
prior_sha=<previous-full-reviewed-commit>
prior_dir="/opt/meetingassist-releases/$prior_sha"
qualification_receipt=<exact-qualificationReceipt-path-emitted-by-activation>
test -r "$prior_dir/release-receipt.json"
test -r "$qualification_receipt"
node "$release_dir/sealed-candidate/scripts/bonfire-release.mjs" rollback \
  --release-dir "$prior_dir" \
  --rollback-release-dir "$release_dir" \
  --base-env /opt/meetingassist/deploy/digitalocean/.env \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz \
  --qualification-rollback-receipt "$qualification_receipt"
```

That receipt is accepted only when it is `target_committed` and binds the exact
currently active qualified commit, rollback target commit, active ledger
generation, base-env path, backup, and before/after digests. The tool restores
the exact absent-or-false prior bytes before any rollback-target preflight or
container start and requires the actual target container to match that prior
state. If rollback fails, recovery atomically reinstalls the exact qualified
`true` env before restarting the still-current qualified release, verifies the
actual container as literal `true`, and returns the receipt to
`target_committed`. A legacy rollback may omit the argument only while the host
env is absent or canonical false.

Until qualification lineage is carried through the active-release ledger, an
ordinary activation while the current base env is `true` is deliberately
blocked. Do not strand rollback by advancing to another generation. First use
the receipt-bound rollback above, then activate a newly reviewed successor from
the unqualified retained release with a fresh target patch transaction.

If the prior directory or immutable image ID is absent, rollback is blocked; do
not rebuild an old tag and call it the prior image. The active-release ledger
must also bind `release_dir` as its exact active release and `prior_dir` as its
exact previous release. An arbitrary valid sibling cannot be selected as a
rollback target.

The currently observed legacy VPS image is unqualified and cannot be supplied
as this rollback bundle. The first exact-release cutover therefore requires a
separately authorized bootstrap/parallel-environment ceremony that establishes
and verifies an exact retained baseline before traffic is mutated. There is no
`--force`, legacy-image, or missing-ledger bypass in this tool.

Use the versioned [first exact-release bootstrap operator pack](./first-exact-release-bootstrap/README.md)
for that one-time ceremony. It requires reviewed implementation commit **A**
followed by a docs-only, direct-child release-checkpoint commit **B** at the
reviewed `axx/main`; do not collapse, reorder, or bypass those two commits.

### Development/demo launch (unqualified)

The legacy convenience command below rebuilds a mutable local tag. It is useful
for development only and must not be used for a production rollout or after an
exact release has been installed, because it does not emit or verify a release
receipt.

From the repo root on a development Droplet:

```bash
cd deploy/digitalocean
# The W1 PostgreSQL volume is external so `docker compose down -v` cannot
# erase canonical history. This command is idempotent.
docker volume create digitalocean_canonical_postgres
docker compose up -d --build
```

W1 runs PostgreSQL as a private, resource-capped shadow target on the existing
Droplet. JSON/JSONL remains the serving authority. Do not set
`BONFIRE_CANONICAL_MODE=required` until `/readyz` reports equal canonical dirty
and reconciled high-water marks with no pending/frozen families and the
principal-aware parity gate has passed. Managed HA PostgreSQL is a separate W4
cutover decision.

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
