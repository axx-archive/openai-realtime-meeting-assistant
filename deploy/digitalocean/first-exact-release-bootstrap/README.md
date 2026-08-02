# Bonfire first exact-release bootstrap operator pack

This pack performs the one-time transition from the unqualified legacy VPS
deployment to the repository's exact retained-release mechanism:

- **A** is the reviewed implementation commit.
- **B** is A's direct, docs-only release-checkpoint child and the exact reviewed
  `axx/main` commit.
- A is booted manually, fully verified except for the deliberately absent
  genesis ledger, then B is activated by A's retained tool. That activation
  writes generation 1 with `active=B` and `previous=A`.

The pack is intentionally staged. It never automatically reopens traffic after
an error. It never fabricates a release ledger or deletes an ambiguous release
operation lock.

## Current source/live contracts encoded here

- `bonfire-release.mjs` has no `preflight` or ledger-bypass command.
- Before maintenance, exact `verify` must stop at the known missing
  `digitalocean_render_queue` volume.
- After manual A, exact `verify` must pass every running check and stop only at
  `active release ledger is missing`.
- Manual A startup uses `up -d --no-build --wait --wait-timeout 300` so a cold
  application start is not misclassified by the former two-minute bound. It
  does not use `--remove-orphans`. The old `codex-runner` is removed explicitly.
- The render runner keeps Chrome's namespace sandbox under an exact,
  release-bound AppArmor profile and a narrow Docker seccomp policy. The host
  restriction `kernel.apparmor_restrict_unprivileged_userns=1` stays enabled.
- The VPS currently needs reviewed Ubuntu Node package
  `18.19.1+dfsg-6ubuntu5`. A candidate change stops the ceremony.
- The legacy database has migrations 1-7; A/B add 8-9. Returning to legacy
  requires cold restoration of every pre-A mutable volume.
- The current canonical shadow is degraded and behind. B is not allowed to
  open traffic unless canonical high-water, outbox, checkpoint, coverage, and
  publication parity are all healthy.

## Files

- `prepare-local.sh` — clean detached-worktree A/B source preparation.
- `vps-common.sh` — shared validation functions; never execute directly.
- `vps-bootstrap.sh` — staged VPS bootstrap driver.
- `vps-rollback-legacy.sh` — rehearsed cold legacy restore; refuses use after
  any candidate or restored-legacy public-open attempt.
- `bonfire-bootstrap-ingress-guard.sh` plus its systemd unit and Docker drop-in
  — reboot- and Docker-restart-safe IPv4/IPv6 maintenance isolation.
- `mac-public-probe.sh` — independent outside-network block/open proof.
- `self-check.sh` / `self-check.mjs` — fail-closed syntax, checksum, and
  repository release-contract checks. Run this before copying the pack.
- `PACK-SHA256SUMS` — checksums for every other file in this directory.

The exact A/B candidate archives also carry
`deploy/digitalocean/bonfire-render-runner-v1.apparmor` and
`deploy/digitalocean/bonfire-render-runner-v1.seccomp.json`. They are release
inputs rather than mutable operator-pack files.

## 1. Prepare A and B on the Mac

Do not use the dirty primary worktree. Supply A; B defaults to fetched
`axx/main` and must be A's direct child.

```bash
REPO=/path/to/meetingassist
PACK="$REPO/deploy/digitalocean/first-exact-release-bootstrap"
cd "$PACK"
chmod 700 ./*.sh
./self-check.sh
A=<implementation-full-40-character-SHA> ./prepare-local.sh
```

The script reports an output directory such as
`/tmp/meetingassist-first-release-<B>`. It verifies clean detached worktrees,
scope, both source receipts, A/B release-owned equivalence, and the stricter
ceremony contract that B is A's direct child and adds only
`docs/plans/stride-next-evolution-master-plan.md`. It exports the operator pack
from detached exact B, binds the checksum-manifest digest into the bootstrap
plan, and refuses a dirty or extra working-tree pack.

`sourceArchiveSha256`, commit, Git tree object, and source epoch are
intentionally *not* compared: `git archive` contains commit-dependent tar
metadata. Each archive remains individually bound to its own receipt. The
equivalence gate compares release-owned tree/inventory/transitive/config
digests, count, paths, and `configFiles`.

Copy only the prepared public release inputs, including the exact-B operator
pack inside `OUT`. Never rsync scripts from the mutable primary worktree:

```bash
OUT=/tmp/meetingassist-first-release-<B>
ssh root@146.190.171.224 'install -d -m 700 /root/bonfire-first-exact-release-pack /opt/meetingassist-releases/<A> /opt/meetingassist-releases/<B>'
rsync -av "$OUT/operator-pack/" root@146.190.171.224:/root/bonfire-first-exact-release-pack/
rsync -av "$OUT/bootstrap-plan.json" root@146.190.171.224:/opt/meetingassist-releases/bootstrap-plan.json
rsync -av "$OUT/<A>/source.tar" "$OUT/<A>/source-receipt.json" root@146.190.171.224:/opt/meetingassist-releases/<A>/
rsync -av "$OUT/<B>/source.tar" "$OUT/<B>/source-receipt.json" root@146.190.171.224:/opt/meetingassist-releases/<B>/
ssh root@146.190.171.224 'chown -R root:root /root/bonfire-first-exact-release-pack /opt/meetingassist-releases/bootstrap-plan.json /opt/meetingassist-releases/<A> /opt/meetingassist-releases/<B>; chmod 700 /root/bonfire-first-exact-release-pack /root/bonfire-first-exact-release-pack/*.sh; chmod 600 /opt/meetingassist-releases/bootstrap-plan.json /opt/meetingassist-releases/<A>/* /opt/meetingassist-releases/<B>/*; cd /root/bonfire-first-exact-release-pack && sha256sum -c PACK-SHA256SUMS && test "$(sha256sum PACK-SHA256SUMS | awk '\''{print $1}'\'')" = "$(jq -er .operatorPackSha256 /opt/meetingassist-releases/bootstrap-plan.json)"'
```

Replace every angle-bracket placeholder before executing. Never put a password,
API key, `.env`, session token, or private backup into the local output pack.

## 2. Pre-maintenance VPS phases

Run each command in a root tmux session. `status` prints only SHAs, paths, and
phase markers.

```bash
cd /root/bonfire-first-exact-release-pack
./vps-bootstrap.sh init-build
./vps-bootstrap.sh preflight
./vps-bootstrap.sh status
```

`init-build` installs/checks Node, builds A and B from their exact archives,
binds build Node/Compose/image identities, and compares the correct A/B source
fields. `preflight` accepts only the exact two-line missing-render-volume error
for each bundle. Any other failure stops before maintenance.

## 3. Maintenance and complete protection

Announce the maintenance window first. Then:

```bash
./vps-bootstrap.sh isolate
```

This installs an exact-pack, root-owned, boot-enabled systemd guard in the
IPv4/IPv6 mangle `PREROUTING` path and a Docker dependency drop-in, then adds
defense-in-depth `DOCKER-USER` rules. Both cover public TCP 80/443/3478 and UDP
3478/40000-40100/49160-49200 on `eth0`; SSH/22 and local loopback remain
untouched. The pack starts the one-shot guard twice to prove idempotent
Docker-restart reapplication, verifies Docker's dependency ordering, and marks
isolation complete only after both layers are exact. A host or Docker restart
during maintenance therefore reapplies the pre-Docker guard before containers
can publish traffic. The phase also creates one marked `/etc/hosts` loopback
entry so the retained Node tool can probe the public hostname with correct
TLS/SNI while public ingress is blocked.

Only after both ingress layers are active, `isolate` installs the identical A/B
renderer policies as root-owned mode-0644 files at
`/etc/apparmor.d/bonfire-render-runner-v1` and
`/etc/docker/seccomp/bonfire-render-runner-v1.json`. It validates the seccomp
JSON, requires the Ubuntu restricted-user-namespace sysctl to remain `1`, loads
the AppArmor policy in enforce mode, byte-compares both installed files to the
sealed release, and records their SHA-256 digests privately. A preexisting path,
profile drift, permissive sysctl, invalid JSON, or non-enforcing AppArmor state
stops the ceremony with ingress still blocked.

Every forward command rejects a ceremony that has ever marked
`public-open-attempted`, `legacy-restored`, or `legacy-reopened`. In addition,
`retire-legacy`, `bootstrap-a`, and `activate-b` re-prove the live maintenance
boundary immediately before acting: both persistent and ephemeral ingress
guards must be exact, `eth0` and the single marked loopback hosts entry must be
unchanged, the public hostname must resolve first to loopback, the external
block acknowledgment must exist, and both renderer profiles must still be exact
and enforcing. Historical phase markers alone never authorize forward motion.

From the Mac or another genuinely outside network:

```bash
PACK=/path/to/meetingassist/deploy/digitalocean/first-exact-release-bootstrap
cd "$PACK"
./mac-public-probe.sh blocked
```

Then return to the VPS:

```bash
./vps-bootstrap.sh acknowledge-external-block
./vps-bootstrap.sh prove-empty
./vps-bootstrap.sh backup
./vps-bootstrap.sh rehearse
```

`prove-empty` prompts for one current member roster name/password, requests a
short-lived native session, enumerates authenticated `/rooms`, and checks the
full `/participants?room=<id>` snapshot for every room twice across a liveness
sweep. The password and token are not written or printed; the session is
revoked when the proof ends. This member credential is the one unavoidable
credential input in the pack.

`backup` then:

- asserts the exact eight-volume legacy inventory;
- privately captures Compose, Caddy, `.env`, `/opt/meetingassist` excluding its
  stale `data/`, `/opt/meetingassist-workspace`, Docker container/network/image
  metadata, and exact legacy images;
- stops all app/runner/edge writers while retaining PostgreSQL long enough for
  `pg_dump`;
- records archive-list validation, migrations 1-7 and every non-system table
  count;
- stops PostgreSQL and archives all eight raw volumes with owners, ACLs, and
  xattrs;
- writes and checks an immutable payload checksum manifest covering every
  current root and nested backup artifact, including the PostgreSQL dump/list,
  migration hashes, and table-count baseline later trusted by rehearsal and
  rollback.

The backup directory is mode 700. Its `.env`, container inspection, company
data, and `codex_home` may contain credentials. Do not print their contents or
copy them off-host without encryption.

`rehearse` restores and byte-compares every raw volume into an unlabelled
temporary volume, then restores the custom PostgreSQL dump into a networkless
temporary container using A's receipted PostgreSQL image and compares migration
hashes plus every table count. No legacy resource is deleted before this passes.

If backup or rehearsal fails before `retire-legacy`, no production resource has
been deleted. After inspecting the failure, restart the untouched exact legacy
containers while keeping ingress blocked:

```bash
./vps-rollback-legacy.sh restart-untouched
```

## 4. Genesis cutover

```bash
./vps-bootstrap.sh retire-legacy
./vps-bootstrap.sh bootstrap-a
```

`retire-legacy` removes exactly one stopped `codex-runner`, then removes only
the already-backed and unreferenced `digitalocean_codex_home` and
`digitalocean_codex_runner_data`. It never removes `codex_queue`. Before the
retirement marker or any deletion, it runs exact A's render image in a
disposable, networkless, read-only, capability-free container under the
installed AppArmor and seccomp profiles. Chrome must create a non-empty PDF and
`pdftoppm` must create a non-empty JPEG; the evidence digests are retained. The
same canary also proves UID/GID 65532, all-zero capability sets,
`NoNewPrivs: 1`, `Seccomp: 2`, denied outer chroot, denied unapproved namespace
operations and `setns`, plus the one exact user-namespace probe Chrome needs.

`bootstrap-a` uses the exact candidate Compose project directory, two env
files, render profile, sanitized environment, `--no-build`, a 300-second cold
health bound, and no orphan shortcut. It accepts A only when:

- its verifier's sole error is the terminal missing-ledger error;
- services, networks, volumes, render initializer, and absence of codex-runner
  are exact;
- migrations 1-9 match the candidate files byte-for-byte;
- no preexisting table disappeared or lost rows;
- canonical parity becomes completely healthy within ten minutes.

If any A condition fails, traffic remains blocked and the script points to the
cold rollback command:

```bash
./vps-rollback-legacy.sh restore
```

After A passes:

```bash
./vps-bootstrap.sh activate-b
```

B is activated by A's retained, receipted tool. The script requires successful
double verification, generation-1 ledger with exact `active=B`/`previous=A`,
migration/data/topology/canonical gates, and authenticated read-only product
smokes whose endpoint-specific success schemas are checked rather than merely
accepting any JSON object. If retained-tool recovery is ambiguous, its operation
lock is preserved and cold rollback is refused. If recovery is clean, exact
ledgerless A is proved and traffic stays blocked for diagnosis or an explicit
cold rollback.

If activation has already committed the exact generation-1 B/A ledger but a
later topology, canonical-parity, data, or authenticated-smoke gate fails, fix
only the proven gate failure and rerun `activate-b`. The command detects the
exact private ledger, refuses any unexpected ledger or surviving operation
lock, skips a second release transition, and repeats every post-activation gate
before it can mark B accepted. This is the only supported activation resume
path.

## 5. Reopen exact B

After reviewing all private evidence and, if prepared, completing local API or
SSH-tunnel smokes while still isolated:

```bash
./vps-bootstrap.sh reopen
```

The script first records the irreversible public-open boundary, then removes
only its marked hosts line and both firewall layers, probes public
health/readiness and exact B identity, and reblocks on failure. Only after the
probe passes does it disable/remove the exact systemd/Docker guard and delete
the now-unused chains. A process restart safely re-establishes both guard layers
before repeating the reopen sequence.

From the Mac:

```bash
./mac-public-probe.sh open <B>
```

Also perform real desktop and mobile acceptance: sign-in, #team and private
thread rendering, files/artifacts, room join/leave, camera/mic/TURN, and the
changed mobile/date/desktop surfaces. Then record the independent proof:

```bash
./vps-bootstrap.sh acknowledge-public
./vps-bootstrap.sh status
```

## Cold rollback boundaries

While public traffic has never been opened or attempted and the retained
release operation lock is absent:

```bash
./vps-rollback-legacy.sh restore
```

This removes all candidate project containers, removes new `render_queue`,
then unloads and deletes only the exact release-owned renderer profiles,
recreates the two legacy volumes from saved driver/options/labels, restores all
eight cold archives, restores exact legacy image refs/IDs, starts legacy
PostgreSQL first, proves migrations/counts, then starts the exact legacy
profiles. It leaves ingress blocked.

Renderer-profile removal is interruption-safe and refuses any running or
restartable container that references the custom AppArmor profile. It accepts
only exact loaded files, exact files already unloaded, an owned interrupted
unlink with exact surviving files, or a fully absent state; drift and
unexplained partial state stop rollback. `restart-untouched` performs the same
cleanup before starting any saved legacy container, and cold restore can be
rerun safely after an interruption.

After reviewing the captured legacy health/degraded baseline:

```bash
./vps-rollback-legacy.sh reopen-legacy
# From the Mac:
./mac-public-probe.sh legacy
```

The cold rollback script refuses to run after any B or restored-legacy
public-open attempt, even when the pack immediately reblocked after a failed
public probe: a brief open window could have admitted a write. The irreversible
marker is written before either firewall layer is removed. Restoring the pre-A
database then becomes an incident-specific freeze, post-cutover backup, and
reconciliation plan.

After successful B, ordinary exact B-to-A rollback uses B's retained tool and
does not downgrade the schema:

```bash
node /opt/meetingassist-releases/<B>/sealed-candidate/scripts/bonfire-release.mjs rollback \
  --release-dir /opt/meetingassist-releases/<A> \
  --rollback-release-dir /opt/meetingassist-releases/<B> \
  --base-env /opt/meetingassist/deploy/digitalocean/.env \
  --health-url https://thebonfire.xyz/healthz \
  --ready-url https://thebonfire.xyz/readyz
```

Keep A/B release directories, exact images, the private backup, and evidence
until the rollback/retention window is explicitly closed. These receipts remain
`verified-local-unsigned`; registry signing, off-host custody, and independent
longer-running production observation are separate gates.
