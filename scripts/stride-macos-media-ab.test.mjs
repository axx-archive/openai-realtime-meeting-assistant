#!/usr/bin/env node

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  aggregateProcessTree,
  buildTrialOrder,
  createManifest,
  metricCatalog,
  parseProcessTable,
  quantile,
  summarizeProcessSamples,
  summarizeTrials,
  validateManifest,
  validateTrialInput,
} from "./stride-macos-media-ab.mjs";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "stride-macos-media-ab.mjs");
const fixedSeed = "00112233445566778899aabbccddeeff";
const fixedTime = "2026-08-31T12:00:00.000Z";
const fixedRevision = "abcdef1234567890abcdef1234567890abcdef12";
const fixedSourceDigest = "1".repeat(64);
const fixedArtifactDigest = "2".repeat(64);

function baselineManifest(overrides = {}) {
  return createManifest({
    seed: fixedSeed,
    trials: 8,
    scenario: "baseline",
    createdAt: fixedTime,
    appBuild: "18",
    sourceRevision: fixedRevision,
    sourceDigestSha256: fixedSourceDigest,
    artifactSha256: fixedArtifactDigest,
    ...overrides,
  });
}

function unavailableMetrics() {
  return Object.fromEntries(Object.keys(metricCatalog).map((name) => [name, {
    status: name === "captureToSendLatencyMs" ? "unsupported" : "unavailable",
    reason: name === "captureToSendLatencyMs"
      ? "no-common-public-timestamp-hook"
      : ((name === "inputDeviceRecoveryMs" || name === "networkRecoveryMs")
        ? "scenario-not-applicable"
        : "not-collected"),
  }]));
}

function measured(value, method, sampleCount = 20) {
  return { status: "measured", value, method, sampleCount };
}

function trialInput(manifest, trial, overrides = {}) {
  const isNative = trial.path === "native";
  const metrics = unavailableMetrics();
  metrics.noiseAttenuationDb = measured(isNative ? 9 + trial.position / 100 : 5 + trial.position / 100, "remote-residual-noise-relative-to-reference");
  metrics.speechOnsetRetentionPct = measured(isNative ? 96 : 97, "aligned-reference-energy-window");
  metrics.speechTailRetentionPct = measured(isNative ? 95 : 96, "aligned-reference-energy-window");
  metrics.processCpuPercent = measured(isNative ? 72 : 100, "ps-explicit-roots-and-descendants", 60);
  return {
    schemaVersion: 1,
    trialId: trial.trialId,
    processingMechanism: isNative ? "native-platform-voice-processing-io" : "webkit-platform",
    degradationState: "none",
    checks: {
      singleActiveMediaOwner: "pass",
      receiverDecodedAudio: "pass",
      noDuplicateParticipant: "pass",
      cleanLeaveNoLeakedMedia: "pass",
      scenarioAppliedAsDeclared: "pass",
    },
    metrics,
    ...overrides,
  };
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

test("balanced randomization is deterministic and preserves equal path counts", () => {
  const first = buildTrialOrder(12, fixedSeed);
  const second = buildTrialOrder(12, fixedSeed);
  assert.deepEqual(first, second);
  assert.equal(first.trials.filter((trial) => trial.path === "webkit").length, 6);
  assert.equal(first.trials.filter((trial) => trial.path === "native").length, 6);
  assert.ok(first.blockOrders.every((order) => order === "ABBA" || order === "BAAB"));
  assert.throws(() => buildTrialOrder(6, fixedSeed), /multiple of four/);
  assert.throws(() => buildTrialOrder(8, "not-a-seed"), /lowercase hex/);
});

test("manifest validation rejects unallowlisted nested identity fields and altered safety policy", () => {
  const manifest = baselineManifest();
  assert.equal(validateManifest(manifest), manifest);

  const withIdentity = clone(manifest);
  withIdentity.controlContract.accountName = "forbidden";
  assert.throws(() => validateManifest(withIdentity), /forbidden or unknown field/);

  const unsafePolicy = clone(manifest);
  unsafePolicy.safetyPolicy.rawMediaRetained = true;
  assert.throws(() => validateManifest(unsafePolicy), /must be false/);

  const alteredOrder = clone(manifest);
  alteredOrder.trials.reverse();
  assert.throws(() => validateManifest(alteredOrder), /does not match its recorded randomization seed/);

  const uppercaseDigest = clone(manifest);
  uppercaseDigest.artifactSha256 = "A".repeat(64);
  assert.throws(() => validateManifest(uppercaseDigest), /64 lowercase hexadecimal/);

  const shortDigest = clone(manifest);
  shortDigest.sourceDigestSha256 = "a".repeat(63);
  assert.throws(() => validateManifest(shortDigest), /64 lowercase hexadecimal/);
});

test("trial input accepts only enum metadata and allowlisted derived metrics", () => {
  const manifest = baselineManifest();
  const input = trialInput(manifest, manifest.trials[0]);
  const sanitized = validateTrialInput(input, manifest);
  assert.equal(sanitized.path, manifest.trials[0].path);
  assert.equal(sanitized.metrics.noiseAttenuationDb.status, "measured");

  const withSdp = clone(input);
  withSdp.sdp = "forbidden";
  assert.throws(() => validateTrialInput(withSdp, manifest), /forbidden or unknown field/);

  const withDeviceName = clone(input);
  withDeviceName.metrics.noiseAttenuationDb.deviceName = "forbidden";
  assert.throws(() => validateTrialInput(withDeviceName, manifest), /forbidden or unknown field/);

  const withFreeText = clone(input);
  withFreeText.metrics.jitterMs.reason = "my room and account";
  assert.throws(() => validateTrialInput(withFreeText, manifest), /not an allowed value/);

  const wrongPath = clone(input);
  wrongPath.processingMechanism = input.processingMechanism.startsWith("webkit")
    ? "native-webrtc-software"
    : "webkit-platform";
  assert.throws(() => validateTrialInput(wrongPath, manifest), /does not match/);
});

test("scenario-specific recovery metrics cannot be fabricated in a baseline run", () => {
  const manifest = baselineManifest();
  const input = trialInput(manifest, manifest.trials[0]);
  input.metrics.networkRecoveryMs = measured(250, "network-restore-to-remote-energy-resume", 1);
  assert.throws(() => validateTrialInput(input, manifest), /only be measured in a network-impairment run/);
});

test("network and physical recovery require their declared manual scenarios", () => {
  const networkManifest = createManifest({
    seed: fixedSeed,
    trials: 8,
    scenario: "network-impairment",
    createdAt: fixedTime,
    impairment: { lossPct: 5, delayMs: 100, jitterMs: 20, rateKbps: 1500 },
  });
  const networkInput = trialInput(networkManifest, networkManifest.trials[0]);
  networkInput.metrics.networkRecoveryMs = measured(240, "network-restore-to-remote-energy-resume", 1);
  assert.doesNotThrow(() => validateTrialInput(networkInput, networkManifest));

  const removalManifest = createManifest({
    seed: fixedSeed,
    trials: 8,
    scenario: "input-removal",
    routeClass: "usb",
    createdAt: fixedTime,
  });
  const removalInput = trialInput(removalManifest, removalManifest.trials[0]);
  removalInput.metrics.inputDeviceRecoveryMs = measured(410, "route-event-to-remote-energy-resume", 1);
  assert.doesNotThrow(() => validateTrialInput(removalInput, removalManifest));

  assert.throws(
    () => createManifest({ seed: fixedSeed, trials: 8, scenario: "network-impairment", createdAt: fixedTime }),
    /impairment must be an object/
  );
});

test("summary computes median, p95, deltas, and a safety-gated material benefit", () => {
  const manifest = baselineManifest();
  const inputs = manifest.trials.map((trial) => trialInput(manifest, trial));
  const summary = summarizeTrials(manifest, inputs);
  assert.equal(summary.conclusion, "material-benefit");
  assert.equal(summary.comparisons.noiseAttenuationDb.materiality, "material-win");
  assert.equal(summary.comparisons.processCpuPercent.materiality, "material-win");
  assert.equal(summary.comparisons.speechOnsetRetentionPct.materiality, "within-threshold");
  assert.equal(summary.comparisons.noiseAttenuationDb.webkit.n, 4);
  assert.equal(summary.comparisons.noiseAttenuationDb.native.n, 4);
  assert.ok(summary.comparisons.noiseAttenuationDb.native.p95 > summary.comparisons.noiseAttenuationDb.native.median);
  assert.deepEqual(summary.missingArtifactIdentity, []);
});

test("material benefit requires complete build revision and digest identity", () => {
  const cases = [
    ["appBuild", { appBuild: "unknown" }],
    ["sourceRevision", { sourceRevision: "abcdef1" }],
    ["sourceDigestSha256", { sourceDigestSha256: "unknown" }],
    ["artifactSha256", { artifactSha256: "unknown" }],
  ];
  for (const [missing, override] of cases) {
    const manifest = baselineManifest(override);
    const inputs = manifest.trials.map((trial) => trialInput(manifest, trial));
    const summary = summarizeTrials(manifest, inputs);
    assert.equal(summary.conclusion, "insufficient-evidence");
    assert.deepEqual(summary.missingArtifactIdentity, [missing]);
  }

  const manifest = createManifest({
    seed: fixedSeed,
    trials: 8,
    scenario: "baseline",
    createdAt: fixedTime,
  });
  const inputs = manifest.trials.map((trial) => trialInput(manifest, trial));
  const summary = summarizeTrials(manifest, inputs);
  assert.deepEqual(summary.missingArtifactIdentity, [
    "appBuild",
    "sourceRevision",
    "sourceDigestSha256",
    "artifactSha256",
  ]);
  assert.equal(summary.conclusion, "insufficient-evidence");
});

test("zero WebKit baseline cannot hide an unassessable native regression behind another win", () => {
  const manifest = baselineManifest();
  const inputs = manifest.trials.map((trial) => {
    const input = trialInput(manifest, trial);
    input.metrics.packetLossPct = measured(
      trial.path === "native" ? 5 : 0,
      "webrtc-safe-aggregate"
    );
    return input;
  });

  const summary = summarizeTrials(manifest, inputs);

  assert.equal(summary.comparisons.packetLossPct.materiality, "unassessable-zero-baseline");
  assert.deepEqual(summary.unassessableComparisons, ["packetLossPct"]);
  assert.equal(summary.conclusion, "insufficient-evidence");
});

test("equal zero medians remain an assessable no-regression comparison", () => {
  const manifest = baselineManifest();
  const inputs = manifest.trials.map((trial) => trialInput(manifest, trial));
  for (const input of inputs) {
    input.metrics.packetLossPct = measured(0, "webrtc-safe-aggregate");
  }

  const summary = summarizeTrials(manifest, inputs);

  assert.equal(summary.comparisons.packetLossPct.materiality, "within-threshold");
  assert.equal(summary.unassessableComparisons.includes("packetLossPct"), false);
  assert.equal(summary.conclusion, "material-benefit");
});

test("unknown processing or degradation truth prevents a material-benefit conclusion", () => {
  const manifest = baselineManifest();
  const inputs = manifest.trials.map((trial) => trialInput(manifest, trial));
  inputs[0].processingMechanism = "unknown";

  const summary = summarizeTrials(manifest, inputs);

  assert.deepEqual(summary.unresolvedRuntimeTruth, [inputs[0].trialId]);
  assert.equal(summary.conclusion, "insufficient-evidence");
});

test("failed ownership or incomplete trials prevent a positive conclusion", () => {
  const manifest = baselineManifest();
  const inputs = manifest.trials.map((trial) => trialInput(manifest, trial));
  inputs[0].checks.singleActiveMediaOwner = "fail";
  assert.equal(summarizeTrials(manifest, inputs).conclusion, "insufficient-evidence");
  assert.equal(summarizeTrials(manifest, inputs.slice(0, 7)).conclusion, "insufficient-evidence");
});

test("material regressions outrank otherwise positive metrics", () => {
  const manifest = baselineManifest();
  const inputs = manifest.trials.map((trial) => {
    const input = trialInput(manifest, trial);
    input.metrics.speechOnsetRetentionPct = measured(
      trial.path === "native" ? 80 : 98,
      "aligned-reference-energy-window"
    );
    return input;
  });
  const summary = summarizeTrials(manifest, inputs);
  assert.equal(summary.comparisons.speechOnsetRetentionPct.materiality, "material-regression");
  assert.equal(summary.conclusion, "material-regression");
});

test("quantile uses linear interpolation", () => {
  assert.equal(quantile([], 0.5), null);
  assert.equal(quantile([4], 0.95), 4);
  assert.equal(quantile([1, 2, 3, 4], 0.5), 2.5);
  assert.equal(quantile([1, 2, 3, 4], 0.95), 3.8499999999999996);
});

test("process sampler aggregates explicit roots and descendants without retaining identity", () => {
  const rows = parseProcessTable("101 1 10.5 1024\n102 101 20.0 2048\n103 102 2.5 1024\n999 1 90.0 9999\n");
  const aggregate = aggregateProcessTree(rows, [101]);
  assert.deepEqual(aggregate, { processCount: 3, cpuPercent: 33, residentMemoryMiB: 4 });

  const summary = summarizeProcessSamples([
    aggregate,
    { processCount: 3, cpuPercent: 25, residentMemoryMiB: 5 },
  ]);
  assert.equal(summary.metrics.processCpuPercent.method, "ps-explicit-roots-and-descendants");
  assert.equal(summary.metrics.residentMemoryMiB.value, 4.5);
  assert.equal("pid" in summary, false);
  assert.equal("processName" in summary, false);
});

test("CLI initializes, ingests, reports, and emits no absolute output path", () => {
  const temp = mkdtempSync(resolve(tmpdir(), "stride-media-ab-test-"));
  const runDir = resolve(temp, "run");
  const init = spawnSync(process.execPath, [
    scriptPath,
    "init",
    "--output", runDir,
    "--trials", "8",
    "--seed", fixedSeed,
    "--app-build", "18",
    "--revision", fixedRevision,
    "--source-digest-sha256", fixedSourceDigest,
    "--artifact-sha256", fixedArtifactDigest,
  ], { cwd: rootDir, encoding: "utf8" });
  assert.equal(init.status, 0, init.stderr);
  assert.doesNotMatch(init.stdout, new RegExp(temp.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));

  const manifestPath = resolve(runDir, "manifest.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  assert.equal(manifest.sourceRevision, fixedRevision);
  assert.equal(manifest.sourceDigestSha256, fixedSourceDigest);
  assert.equal(manifest.artifactSha256, fixedArtifactDigest);
  const templatePath = resolve(runDir, "operator-input", "trial-001.json");
  const template = JSON.parse(readFileSync(templatePath, "utf8"));
  for (const check of Object.keys(template.checks)) template.checks[check] = "pass";

  const unsafe = clone(template);
  unsafe.metrics.noiseAttenuationDb.deviceName = "private-device-label";
  writeFileSync(templatePath, `${JSON.stringify(unsafe, null, 2)}\n`);
  const rejected = spawnSync(process.execPath, [
    scriptPath, "ingest", "--manifest", manifestPath, "--input", templatePath,
  ], { cwd: rootDir, encoding: "utf8" });
  assert.equal(rejected.status, 1);
  assert.doesNotMatch(rejected.stderr, /private-device-label|deviceName/);
  assert.doesNotMatch(rejected.stderr, new RegExp(temp.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));

  writeFileSync(templatePath, `${JSON.stringify(template, null, 2)}\n`);

  const ingest = spawnSync(process.execPath, [
    scriptPath, "ingest", "--manifest", manifestPath, "--input", templatePath,
  ], { cwd: rootDir, encoding: "utf8" });
  assert.equal(ingest.status, 0, ingest.stderr);
  assert.doesNotMatch(ingest.stdout, new RegExp(temp.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));

  const report = spawnSync(process.execPath, [scriptPath, "report", "--manifest", manifestPath], {
    cwd: rootDir,
    encoding: "utf8",
  });
  assert.equal(report.status, 0, report.stderr);
  const result = JSON.parse(report.stdout);
  assert.equal(result.conclusion, "insufficient-evidence");
  assert.equal(result.ingestedTrialCount, 1);
  assert.doesNotMatch(report.stdout, new RegExp(temp.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  assert.ok(readFileSync(resolve(runDir, "SHA256SUMS"), "utf8").includes("trials/trial-001.json"));
  assert.equal(manifest.safetyPolicy.rawMediaRetained, false);
});
