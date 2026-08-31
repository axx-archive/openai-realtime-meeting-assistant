# STRIDE macOS media A/B protocol

This protocol compares the existing WebKit media path with the native macOS path on the same sender Mac. It is a sequential, randomized, balanced-block test: only one sender owns media at a time. The harness records allowlisted derived numbers, not raw media or session identity.

The native-media QA package is build 19. Its product shell is the completed build 18 UI baseline plus the additive, mutually exclusive native-media surface; both WebKit and native trials run from that same build 19 binary.

The harness can establish a material audio-quality, reliability, latency, or efficiency difference when a complete run clears its correctness checks and a preregistered median threshold. It does not turn a median threshold into statistical significance. Human listening is a secondary check only.

## Evidence boundary

The run directory may contain only:

- the generated manifest and anonymous trial IDs;
- derived per-trial metrics from the fixed schema;
- enum-valued mechanism, degradation, and pass/fail state;
- the numeric app build, full source revision, exact source-snapshot SHA-256, and tested app/DMG SHA-256;
- the generated aggregate JSON, CSV, checklist, and SHA-256 list.

Never retain raw or encoded audio/video, screenshots containing participant or room identity, complete WebRTC stats reports, SDP, ICE candidates or credentials, IP addresses, hostnames, URLs, room names, participant names, account data, device names or labels, user-agent strings, tokens, or secrets. If temporary audio or an Instruments trace is needed, keep it in a mode-0700 directory outside the run, derive the allowed values, and delete it before evidence is finalized.

The input schema has no free-text evidence fields. Unknown fields and unapproved enum values are rejected. The process sampler stores aggregate CPU, RSS, and process count only; it does not store process IDs, process names, commands, or paths.

## What each metric means

| Metric | Defensible method | Important limit |
| --- | --- | --- |
| Noise attenuation | Align remote decoded output with a fixed reference and compare residual noise energy, normalized to the same calibration interval | Report comparative dB, not acoustic dB SPL, unless the fixture is calibrated |
| Speech onset/tail retention | Reference-aligned energy in preregistered first/last 50–100 ms speech windows | Use the same windows and thresholds for both paths |
| Echo | Prefer exposed standardized WebRTC echo-return-loss-enhancement stats | If unavailable, use far-end-reference residual correlation and label it a proxy, not ERLE |
| Capture-to-send latency | A common public timestamp hook at source capture and sender packetization on both paths | Otherwise mark unsupported; do not infer it |
| Stimulus-to-remote-decode latency | Fixed acoustic/electrical sync stimulus to first aligned decoded receiver energy | This is an end-to-end substitute, not capture-to-send |
| CPU and memory | Median of one-second `ps` samples for explicit app roots and their descendants | Reparented WebKit helpers are excluded unless passed as explicit roots; subtract a separately measured idle-shell baseline when comparing |
| Energy | A derived, process-attributed Instruments Power Profiler score | The script's CPU sample is not energy and must not be reported as joules |
| Loss, jitter, RTT, concealment | Allowlisted aggregates from `getStats`/native stats | Never save the complete stats report, candidate IDs, addresses, or codec/session identity |
| Device recovery | OS route/device event to resumed remote decoded energy | Requires a real removable USB, wired, or Bluetooth input |
| Network recovery | Network restoration to resumed remote decoded energy/packets | Requires an authorized controlled shaper or physical network action |

The default materiality thresholds are 3 dB for noise/echo, 5 percentage points for onset/tail retention, and 20% for latency, resource, network, concealment, and recovery metrics. A positive improvement always means native is better. Any material regression wins over otherwise positive results. All planned trials, all safety checks, resolved processing/degradation truth, and assessable comparisons are required before the aggregate can say `material-benefit` or `no-material-benefit`. `material-benefit` additionally requires a numeric app build, a full 40-character source revision, and lowercase 64-character SHA-256 values for both the exact source snapshot used to build and the tested app artifact. A relative metric whose WebKit median is zero is explicitly unassessable when the native median differs; equal zero medians are a truthful no-regression result.

## Fixture and endpoints

Use one fixed sender Mac and one fixed receiver for an entire run. A second physical receiver is preferred because it avoids coupling sender resource measurements to receiver rendering. Do not record receiver identity in evidence.

For acoustic quality, fix microphone/speaker position, input gain, output level, room, and stimulus. The stimulus should contain separate calibration, noise-only, speech-only, speech-plus-noise, onset/plosive/soft-speech, tail, far-end echo, and double-talk windows plus a sync chirp. Use a versioned, license-compatible fixture and record only its content hash in operator notes outside the harness until a safe hash field is added. Do not silently substitute a different fixture between paths.

Use at least eight trials. The harness divides them into randomized `ABBA` or `BAAB` blocks, where A is WebKit and B is native. Eight trials provide four observations per path; use 12 or 16 when time permits. Keep the generated order—do not rearrange it after observing results.

## Operator phases

### 1. Freeze conditions

1. Build the exact app under test and record its numeric build, full source revision, exact source-snapshot SHA-256, and tested app/DMG SHA-256 in the manifest. The source digest must cover the reviewed source snapshot actually supplied to the build—not a different checkout or a later working tree.
2. Warm the Mac to a stable state, connect power, close unrelated heavy applications, and disable automatic updates for the test window without changing production infrastructure.
3. Fix sender placement, gains, fixture, receiver, room, and network.
4. Join the receiver. Confirm it decodes the WebKit sender, then leave and confirm cleanup before starting the measured run.
5. Create a private temporary directory for raw working material, if needed:

   ```sh
   AB_TEMP_DIR="$(mktemp -d)"
   chmod 700 "$AB_TEMP_DIR"
   ```

### 2. Initialize a baseline run

Use a new output directory. Do not reuse or overwrite a prior run.

Resolve the two hashes before initializing the run. Paths remain outside evidence; only their digests enter the manifest. Substitute the exact reviewed source archive and local QA DMG used for this build:

```sh
STRIDE_SOURCE_ARCHIVE=/absolute/path/to/exact-reviewed-source-archive.tar
STRIDE_QA_DMG=artifacts/macos/STRIDE-1.0.dmg
REVISION="$(git rev-parse HEAD)"
SOURCE_DIGEST_SHA256="$(shasum -a 256 "$STRIDE_SOURCE_ARCHIVE" | awk '{print $1}')"
ARTIFACT_SHA256="$(shasum -a 256 "$STRIDE_QA_DMG" | awk '{print $1}')"
```

Do not hash an arbitrary checkout in place: filesystem metadata, untracked files, or changes made after the build would break the source-to-artifact binding. If either digest or the exact full revision is unavailable, pass `unknown`; the run can still record measurements but cannot conclude `material-benefit`.

```sh
node scripts/stride-macos-media-ab.mjs init \
  --output artifacts/native-media-ab/baseline-build-19 \
  --trials 8 \
  --seed 00112233445566778899aabbccddeeff \
  --scenario baseline \
  --route-class builtin \
  --app-build 19 \
  --revision "$REVISION" \
  --source-digest-sha256 "$SOURCE_DIGEST_SHA256" \
  --artifact-sha256 "$ARTIFACT_SHA256"
```

Generate a fresh lowercase hex seed for a new run with `openssl rand -hex 16`. The manifest stores the seed and exact order, making that order reproducible.

For a real input-removal run, use a new directory and `--scenario input-removal --route-class usb`, `wired`, or `bluetooth`. Do not claim recovery from a built-in device or an injected notification.

For an authorized network-impairment run, record the intended numeric profile:

```sh
node scripts/stride-macos-media-ab.mjs init \
  --output artifacts/native-media-ab/network-build-19 \
  --trials 8 \
  --seed 102132435465768798a9bacbdcedfe0f \
  --scenario network-impairment \
  --route-class builtin \
  --loss-percent 5 \
  --delay-ms 100 \
  --jitter-ms 20 \
  --rate-kbps 1500 \
  --app-build 19 \
  --revision "$REVISION" \
  --source-digest-sha256 "$SOURCE_DIGEST_SHA256" \
  --artifact-sha256 "$ARTIFACT_SHA256"
```

The harness does not run `pfctl`, `dnctl`, Network Link Conditioner, disconnect Wi-Fi, reset media services, change TCC state, or touch a physical device. Apply an impairment only with explicit local authority and a separately verified cleanup path. If none is authorized or available, use `no-authorized-network-impairment` and leave recovery unavailable.

### 3. Run each trial in manifest order

1. Confirm both media paths are stopped and the prior sender seat is absent.
2. Start only the path named by the next trial. Never switch ownership mid-call.
3. Confirm the runtime mechanism and degradation state from public diagnostics; select the matching enum in the trial input. Use `unknown` rather than guessing.
4. Confirm exactly one media owner, one participant seat, and decoded receiver audio.
5. Warm up for 30 seconds, then play the identical fixture and collect only derived values.
6. For an impairment or removal run, perform the declared manual event at the same preregistered fixture timestamp in every trial. Mark `scenarioAppliedAsDeclared` pass only if it actually occurred.
7. Leave. Confirm capture stopped, the sender seat disappeared, and no media remains before the next trial.

For best-effort process-tree sampling, find the app process without writing its PID into evidence, then run the sampler from a separate terminal during the same measurement interval:

```sh
STRIDE_ROOT_PID="$(pgrep -x STRIDE | head -n 1)"
node scripts/stride-macos-media-ab.mjs sample-process \
  --pids "$STRIDE_ROOT_PID" \
  --duration-seconds 60 \
  --interval-ms 1000 \
  --output "$AB_TEMP_DIR/process-summary.json"
```

If a verified WebKit auxiliary process is not a descendant, pass it as an additional comma-separated root. If attribution cannot be established equally for both paths, mark CPU and RSS unavailable with `process-attribution-incomplete`. Copy only the two metric objects from the sampler summary into the trial input. Measure an idle SwiftUI/WKWebView shell separately and apply the same subtraction rule to both paths; the harness intentionally does not invent that baseline.

### 4. Ingest each trial

Edit the matching generated file under `operator-input/`. Every metric must remain present and must be one of:

```json
{
  "status": "measured",
  "value": 6.2,
  "method": "remote-residual-noise-relative-to-reference",
  "sampleCount": 20
}
```

or an honest non-measurement:

```json
{
  "status": "unsupported",
  "reason": "public-stat-not-exposed"
}
```

Then ingest it once:

```sh
node scripts/stride-macos-media-ab.mjs ingest \
  --manifest artifacts/native-media-ab/baseline-build-19/manifest.json \
  --input artifacts/native-media-ab/baseline-build-19/operator-input/trial-001.json
```

Ingestion refuses unknown fields, free-text reasons, mismatched native/WebKit mechanism labels, scenario-inapplicable recovery measurements, and overwrites. If evidence is wrong, preserve the failed run and start a new one.

### 5. Finalize and listen

After every trial is ingested:

```sh
node scripts/stride-macos-media-ab.mjs report \
  --manifest artifacts/native-media-ab/baseline-build-19/manifest.json

cd artifacts/native-media-ab/baseline-build-19
shasum -a 256 -c SHA256SUMS
```

Randomize temporary WebKit/native listening excerpts outside the evidence directory, blind the labels, and use the same level normalization. Record only aggregate anonymous preference and artifact hashes in the broader QA proof pack. Do not promote the excerpts. Delete the private temporary directory after deriving all allowlisted metrics:

```sh
rm -r "$AB_TEMP_DIR"
```

This deletion targets only the explicit directory returned by `mktemp`; verify the variable before running it.

## Interpretation

- `material-benefit`: all trials and correctness checks passed, artifact identity is complete, at least one objective metric cleared its preregistered threshold, and none materially regressed.
- `no-material-benefit`: all trials and checks passed, at least one metric had the required sample count, but no metric cleared a threshold.
- `material-regression`: a measured metric crossed its regression threshold, even if another metric improved.
- `insufficient-evidence`: missing trials, non-pass correctness checks, unknown runtime processing/degradation truth, incomplete artifact identity when metrics otherwise show a win, an unassessable zero-baseline relative comparison, or too few measured observations. Inspect `missingArtifactIdentity` in `summary.json` for the exact absent or abbreviated fields.

Keep synthetic RNNoise benchmarks separate from this result. They verify an algorithmic fixture, not microphone capture, native/WebKit ownership, room interoperability, recovery, or installed-app behavior. Keep public-distribution claims separate too: an A/B result does not prove Developer ID signing, hardened runtime, notarization, stapling, Sparkle feed signing, privacy review, or Gatekeeper acceptance.

## Focused harness test

```sh
node --test scripts/stride-macos-media-ab.test.mjs
```
