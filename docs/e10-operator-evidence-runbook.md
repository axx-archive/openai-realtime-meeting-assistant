# STRIDE E10 operator evidence capture

This runbook defines the token-free intake boundary for evidence that must be
captured by people, physical devices, production infrastructure, or approved
real inputs. The validator is read-only. A valid packet proves that an evidence
envelope is complete and bound; it does not prove the underlying observation,
qualify a provider, enable a route, or authorize a release.

## One immutable candidate first

Every registry and packet binds the same five values:

- exact release commit;
- exact Git-tree digest;
- exact application image digest;
- exact deployed Compose/Caddy/config digest; and
- exact final route-map digest.

Do not reuse an external observation after any of those values changes. Raw
media, transcripts, company inputs, keys, credentials, and participant identity
must remain in the approved evidence store; JSON packets contain only stable
IDs, digests, metrics, consent-receipt digests, and attributable operator and
reviewer IDs.

## Approved trust roots and signed target registry

Before capture, product, security, and operations approve one out-of-band
`stride.e10.trust-roots/v2` file. It maps stable signer and identity IDs to the
approved SHA-256 fingerprint of an Ed25519 public key. It requires one each of
the distinct roles `registry_owner`, `operator`, and `independent_reviewer`, plus
at least two distinct `pilot_reviewer` identities and keys. The validator does
not trust a public key merely because a caller supplied it: its fingerprint,
identity, key ID, and role must exactly match this approved trust root.

The trust-root file also contains
`preMeasurementTargetRegistrySha256`: the exact canonical registry digest
approved before any measurement begins. The exact trust-root file must then
match the SHA-256 anchor separately recorded in the approved release ledger.
This double anchor prevents even the approved registry owner from loosening a
threshold or swapping a target after seeing results. A new registry requires a
new independent pre-measurement approval and trust-root anchor; passing a newly
created file, registry, and keys together is not self-approval. The
draft at `deploy/e10/trust-roots.draft.json` is a shape-only aid and is not an
approval record.

The owners then agree on one canonical compact
`stride.e10.target-registry/v3` JSON file. The non-weakenable inventory now
includes `meeting-stt-live-provider-evaluation`,
`composer-dictation-target-device-evaluation`, and
`insights-opportunities-real-input-pilots` in addition to the external
matrices. Each target binds an immutable
fixture digest, named environment, minimum source-artifact count, minimum
sample size, exact SHA-256 digest of the measurement code (not a mutable
revision label), accountable owner, distinct independent
reviewer, rollback trigger, physical/production requirement, and a complete
multi-metric threshold set. The release owner signs the exact canonical bytes
with the offline registry-owner Ed25519 key. A signed label without those
bindings cannot pass. The public key and detached signature files contain one
canonical lowercase hex value; private keys never enter this repository or the
VPS.
The deliberately non-validating draft at
`deploy/e10/target-registry.draft.json` enumerates the current target set. Its
`REPLACE_...` candidate fields must be replaced with the final immutable
candidate before review and signing; the CLI rejects the draft as-is.
After completing and reviewing a packet, serialize it once as compact,
recursively key-sorted JSON (for example, `jq -cSj '.' reviewed.json`) and sign
those exact bytes. Do not pretty-print, re-key, change numeric serialization, or
append a newline after signing; any byte change is intentionally rejected.

```bash
go run ./cmd/e10-evidence \
  -mode registry \
  -input /approved/e10/targets.json \
  -trust-roots /approved/e10/trust-roots.json \
  -approved-trust-root-sha256 "$APPROVED_E10_TRUST_ROOT_SHA256" \
  -registry-signature /approved/e10/targets.sig.hex \
  -registry-public-key /approved/e10/release-owner.pub.hex
```

The command emits a `stride.e10.capture-validation/v2` structural receipt. Its
evidence class is deliberately `operator_packet_structure_only` and explicitly
excludes provider, device, production, release, route, and launch claims. The
receipt contains digests of the exact packet, anchored registry, approved
trust-root bytes, and detached signature set, but the serialized JSON is not a
signature, capability, trust root, or authority on its own. Never authorize a
downstream action from receipt JSON alone.

The library exposes opaque `VerifiedValidationReceipt` values only from the
combined verification entry points. After a receipt has been serialized, the
appropriate `ReverifyEncoded...Receipt` entry point must receive the original
canonical packet, anchored registry, approved trust roots, public keys, and all
detached signatures. It repeats the full verification and accepts the receipt
only if its bytes exactly match the newly derived receipt. The CLI offers the
same operator flow with `-expected-receipt`: it freshly verifies all original
inputs and then performs the exact-byte comparison. Receipts are emitted with
no trailing newline because formatting drift is intentionally rejected.

At an ingestion boundary, also pass `-consume-ledger` with a ledger inside an
already-created private (`0700`) real directory. The ledger is a locked,
`0600`, append-only hash chain with fsync/rollback behavior and durably permits
one consumption of an exact source identity (while recording the receipt
digest) across threads, processes, consumer
instances, and restarts. A replay, corrupt ledger, symlink, weak file mode,
partial write, or unminted receipt fails closed. Preserve and back up that
ledger with the approved evidence set; deleting or replacing it destroys replay
history and is not an authorized retry mechanism.

Replay identity is the immutable source envelope—packet kind, exact input
digest, anchored registry digest, and candidate—not trust-root metadata or only
the emitted receipt bytes. Re-approving the same signed source under a changed
trust-root ID or an expanded unused signer roster therefore cannot consume or
trigger it again.

## Consented meeting-STT and dictation corpora

Use `stride.e10.corpus-manifest/v3` with lane `meeting_stt` or
`composer_dictation` and evidence class `authorized_real_capture`.

- Meeting STT requires at least 120 non-synthetic clips totaling at least 60
  minutes. Each clip binds unique audio, reference, per-clip consent evidence,
  speaker evidence, track evidence, and source order without embedding raw
  content. Reusing an artifact under a new clip ID fails closed.
- Composer dictation requires at least 250 non-synthetic target-device clips
  and physical coverage of every cross-product of web/iPhone/iPad and the
  `scout`, `private_thread`, `team`, `project`, and `in_room` composer surfaces.
  Every dictation clip is at most 30 seconds; a meeting segment cannot be
  relabeled as a composer clip.
  Product qualification still consumes the separate fidelity, latency,
  exactly-once, cancellation, mic-generation, FPS, privacy, and device
  observations produced by the qualification evaluator.

Every corpus carries the exact preregistered target ID and fixture digest, exact
signed-registry digest, exact candidate, and a
deterministic digest of its typed source-artifact set. A trust-rooted operator
and a different trust-rooted independent reviewer each sign the same exact
canonical packet bytes. Neither signature can substitute for the other.

```bash
go run ./cmd/e10-evidence \
  -mode corpus \
  -input /approved/e10/stt-corpus.json \
  -registry /approved/e10/targets.json \
  -trust-roots /approved/e10/trust-roots.json \
  -approved-trust-root-sha256 "$APPROVED_E10_TRUST_ROOT_SHA256" \
  -registry-signature /approved/e10/targets.sig.hex \
  -registry-public-key /approved/e10/release-owner.pub.hex \
  -operator-signature /approved/e10/stt-corpus.operator.sig.hex \
  -operator-public-key /approved/e10/operator.pub.hex \
  -reviewer-signature /approved/e10/stt-corpus.reviewer.sig.hex \
  -reviewer-public-key /approved/e10/reviewer.pub.hex
```

Repeat for dictation with its own exact packet and detached signatures. The
manifest validator rejects non-canonical JSON, unknown/duplicate fields,
synthetic substitutions, duplicate evidence under relabeled IDs, missing
consent/digests, insufficient duration/count, incomplete physical
platform/surface coverage, registry/candidate drift, unapproved keys, self
review, and either invalid signature.

## Insights & Opportunities pilot packet

Use `stride.e10.io-pilot-packet/v4` with evidence class
`authorized_real_input_human_review`. The packet requires exactly ten immutable
real-input pilots and at least eight accepted outcomes (the fixed 8/10 gate).
Every pilot has one
explicit terminal disposition—`accepted`, `rejected`, `blocked`, or `failed`—an
attributable disposition-reason digest, and a terminal-visibility receipt
digest proving that the outcome was shown in the product. Every asserted claim
must be source-bound; invented assertions, unauthorized disclosures, and
external writes must all be exactly zero. Revision count is bounded to two.
The zero-write claim is not a bare count: every pilot also binds a unique
immutable external-effect/write-audit receipt digest. That audit digest is part
of the source-artifact set, each human review signature, and both packet-level
signatures; missing or changed audit evidence fails closed.

The packet also binds the exact preregistered I&O target ID and fixture digest.
The signed reviewer roster binds each reviewer identity, key ID, approved public
key fingerprint, and unique eligibility-receipt digest. Every pilot carries at
least two distinct rostered review decisions. Each decision includes its own
unique review-receipt digest and a `pilot_reviewer` Ed25519 signature that binds
the exact candidate, packet and pilot IDs, input/run/artifact digests,
disposition and reason, terminal visibility, revision and claim counts,
external-effect audit evidence, eligibility evidence, and review disposition.
The review key, identity, and
fingerprint must match the separately anchored trust roots. The packet-level
operator and independent reviewer then sign the exact canonical packet bytes,
including those per-pilot decisions. Missing, substituted, contradictory,
non-rooted, or tampered reviewers fail closed.

```bash
go run ./cmd/e10-evidence \
  -mode io-pilots \
  -input /approved/e10/io-pilots.json \
  -registry /approved/e10/targets.json \
  -trust-roots /approved/e10/trust-roots.json \
  -approved-trust-root-sha256 "$APPROVED_E10_TRUST_ROOT_SHA256" \
  -registry-signature /approved/e10/targets.sig.hex \
  -registry-public-key /approved/e10/release-owner.pub.hex \
  -operator-signature /approved/e10/io-pilots.operator.sig.hex \
  -operator-public-key /approved/e10/operator.pub.hex \
  -reviewer-signature /approved/e10/io-pilots.reviewer.sig.hex \
  -reviewer-public-key /approved/e10/reviewer.pub.hex
```

The cryptographic boundary proves that the approved reviewer identities signed
the exact recorded decisions and eligibility-receipt digests. It does not prove
that an artifact is useful or that the underlying eligibility record is true;
the independently governed source store and Critic gate remain authoritative.

## Signed evaluator-result import

Corpus and pilot validation receipts remain structure-only and can never be
promoted by copying their JSON into the application. After an approved
evaluator has run over the exact source packet, create one canonical
`stride.e10.qualification-result/v2` packet with evidence class
`dual_signed_evaluator_result`. It binds:

- the tenant, lane, result ID, preregistered target, qualification-subject
  digest, and qualified/not-qualified disposition;
- the exact source packet kind and SHA-256;
- the complete candidate binding, including release commit, tree, image,
  configuration, and route-map digests;
- the evaluator configuration SHA-256, which must exactly equal the target's
  preregistered measurement-code digest; and
- the SHA-256 of the evaluator output retained in the governed evidence store.

The result source-artifact-set digest covers the source packet, evaluator
configuration, and evaluator output digests. The target owner and distinct
independent reviewer sign the exact result packet bytes. Call
`VerifyQualificationResultReceipt` with the anchored registry, the original
canonical source bytes, the in-process opaque source receipt, and both detached
result signatures. A copied receipt, hand-built capability, changed target,
fixture, tenant, candidate/config/route digest, evaluator revision, result
digest, source packet, or trust root fails closed.

Run `e10-evidence -mode qualification-result` with the canonical result,
registry, source packet, and all registry/source/result signature and public-key
files. It emits a canonical `stride.e10.qualification-import-bundle/v1`.
`OpenTrustedQualificationEvidenceStore` requires an operator-provisioned
`QualificationEvidenceAnchorAuthority`; local sibling arguments cannot assert
their own trust-root pin or ledger head. The authority must atomically return
the approved trust-root bytes and pin with the exact tenant-bound ledger head,
and every head compare-and-swap must also require that same root pin. This
fences a root rotation racing bundle verification. Each custody call is
deadline bounded. `ImportQualificationBundle` re-verifies every signed byte, persists
the complete bundle in the locked `0600`, fsynced journal, and accepts the
append only after custody advances. A rejected CAS rolls the local append back;
a lost response is reconciled by rereading the head; an unreadable or third
state poisons the process pending explicit operator reconciliation. Reload
re-verifies every trusted event and requires the local journal to equal the
exact external head, refusing rewrites, stale competing processes, and
valid-prefix rollback. This repository intentionally contains no production
local/file implementation of the authority: external custody provisioning is
a release prerequisite and the feature remains default-off without it.
The old opaque-capability import path is deliberately unavailable because it
cannot be re-verified across processes.
`QualificationEvidenceSeed`, local test
rows, and the existing structure-only evaluator candidates cannot mint or
enter this trusted map. Reading a stored result does not enable a route,
deployment, release, or launch; those remain separate gates. Preserve the
original packets, evaluator output, signatures, trust-root and ledger anchors, and journal
together as the audit set.

## Physical-device, real-WebRTC, HA/DR, and worker matrices

Use `stride.e10.external-matrix/v3` with one of these categories:

- `physical_device_webrtc` for real iPhone/iPad/web, guest, restrictive TURN,
  mixed-client, two-hour room, route-change, background/lock, screen-share,
  packet-loss, disconnect, and induced-AI-failure evidence;
- `ha_dr` for encrypted immutable offsite custody, independent keys, signed
  restore, four-root and purge continuity, RPO/RTO, app/TURN failover, rollback,
  and post-failover observation; or
- `worker_orchestrator` for the installed external worker boundary, default-deny
  egress, short-lived run-bound credentials, resource caps, signed callback,
  replay fencing, no production/company-brain mounts, crash recovery, and
  idempotent side effects.

Every observation references a signed-registry target and unique source-artifact
digest, repeats the exact fixture, environment, minimum sample size, and
measurement-code digest, carries an RFC3339 timestamp and a pass verdict, and
reports the exact preregistered metric set. The target owner and independent
reviewer must be different approved identities, and both detached signatures
cover the exact canonical matrix bytes, including registry, candidate, and
source-artifact-set digests. Unknown targets or metrics, missing or duplicate
artifacts, self-review, unapproved keys, future timestamps, failed verdicts,
threshold misses, contract/candidate drift, and incomplete target coverage fail
closed.

The repeated measurement-code digest is an exact SHA-256 from
the pre-measurement registry. A mutable label such as `measurement-v1` is
invalid. Changing measurement code produces a different digest and therefore
requires a newly approved pre-measurement registry; it cannot validate old
observations.

Rate observations must include the exact numerator and denominator plus the
recomputed 95% Wilson interval. Latency observations must include the exact
sample vector, p50/p95/p99, and a recomputable deterministic 95% bootstrap
interval. A point estimate is insufficient: `at_least` gates use the lower
confidence bound and `at_most` gates use the upper bound. Exact 100% rate gates
are zero-failure gates and still report their Wilson interval. Scalar metrics
must not carry fake statistical fields.

The immutable registry contains all 30 required current targets. Its room
aggregate requires at least 200 joins, join success of at least 99.5%, first
remote audio/video p95 at most 2.5/3.5 seconds, recovery after ten-second loss
at most eight seconds, two simultaneous three-person two-hour rooms, zero
cross-room events, unintended fatal disconnects, or participant outages over
five seconds, CPU/RSS p95 at most 110% of baseline, and at most 5% RSS drift
after at least 20 join/leave cycles. TURN failover requires media interruption
p95 at most two seconds and zero interruptions over two seconds. Locked-device
push/deep-link is a separate physical target with 100 trials and zero wrong,
unauthorized, or private-content opens/disclosures. The validator permits
additive targets and tighter thresholds but rejects any omission or weakening
of category, artifact count, sample count, or metric floor.

The HA/DR restore floor also requires a snapshot no more than 15 minutes old
before mutation, exact offsite-digest/local-manifest agreement, 100% canonical
database, file, workflow, and purge-manifest parity, and zero lost events after
the snapshot watermark. The 24-hour/ten-sitting soak requires zero safety-gate
failures in addition to zero fatal failures and cross-tenant leaks.
The authenticated restore target also requires 100% approved restore
authentications and zero invalid or unapproved restore acceptances. The signed
callback target requires 100% acceptance of valid signed callbacks in addition
to zero invalid or replayed callback acceptances, so denial of every callback
cannot false-pass either gate.
Likewise, independent custody records an actual independent restore host with
zero production membership or mounts, live app/control failover requires 100%
successful failovers as well as the latency bound, and default-deny egress must
allow 100% of approved egress while blocking every unapproved attempt.
Restore, RPO, and app/control failover also carry explicit zero-over-SLO counts;
their reported quantiles cannot hide one attempt beyond the 60-minute,
five-minute, or 60-second hard boundary.

The draft deliberately makes rooms, TURN, authenticated four-root restore,
24-hour/ten-sitting soak, and worker mount isolation multi-dimensional. For
example, duration alone cannot prove a room, an RTO alone cannot prove a safe
restore, and one successful denial cannot prove worker isolation.

```bash
go run ./cmd/e10-evidence \
  -mode external-matrix \
  -input /approved/e10/device-webrtc.json \
  -registry /approved/e10/targets.json \
  -trust-roots /approved/e10/trust-roots.json \
  -approved-trust-root-sha256 "$APPROVED_E10_TRUST_ROOT_SHA256" \
  -registry-signature /approved/e10/targets.sig.hex \
  -registry-public-key /approved/e10/release-owner.pub.hex \
  -operator-signature /approved/e10/device-webrtc.operator.sig.hex \
  -operator-public-key /approved/e10/operator.pub.hex \
  -reviewer-signature /approved/e10/device-webrtc.reviewer.sig.hex \
  -reviewer-public-key /approved/e10/reviewer.pub.hex
```

Run the same command for HA/DR and worker-orchestrator packets. Preserve the
canonical input JSON, detached registry/operator/reviewer signatures, approved
trust-root file, public keys and fingerprints, emitted receipt, referenced source
artifacts, and their storage/custody receipts together.

For a downstream exact re-verification and one-use claim, rerun the same command
with the complete original argument set plus:

```bash
  -expected-receipt /approved/e10/original-receipt.json \
  -consume-ledger /approved/e10/private/consumed-receipts.jsonl
```

Do not use `-consume-ledger` during exploratory validation. Consumption is an
explicit ingestion event and cannot be undone by the validator.

## Stop and promotion rules

- Packet validation never performs capture, provider inference, a traffic
  shift, a deployment, a restore, a failover, or a device submission.
- A packet may be ingested only after its candidate and registry digests match
  the final release ledger, the registry digest matches the separately anchored
  pre-measurement digest, its source-artifact-set digest recomputes exactly, all
  required packet/reviewer signatures match approved distinct signers, the
  serialized receipt has been reverified from those original sources and
  claimed exactly once, and its source artifacts remain authorized.
- Structural validation cannot promote any state beyond evidence collected for
  independent review. Provider qualification, physical-device acceptance,
  production acceptance, exact release qualification, route activation, and
  launch readiness require their separate gates in the master plan.
- Any candidate, route-map, consent, audience, package, model, prompt, schema,
  price, or retention change invalidates the affected packet and requires
  recapture; do not silently edit or re-sign old evidence.
