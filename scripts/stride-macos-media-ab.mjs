#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const schemaVersion = 1;

const pathLabels = Object.freeze({ A: "webkit", B: "native" });
const blockOrders = Object.freeze(["ABBA", "BAAB"]);
const allowedScenarios = new Set(["baseline", "network-impairment", "input-removal"]);
const allowedRouteClasses = new Set(["builtin", "bluetooth", "wired", "usb", "unspecified"]);
const allowedMechanisms = new Set([
  "webkit-platform",
  "webkit-rnnoise-wasm",
  "webkit-adaptive-fallback",
  "native-platform-voice-processing-io",
  "native-webrtc-software",
  "native-webrtc-mixed",
  "native-unprocessed",
  "unknown",
]);
const allowedDegradationStates = new Set([
  "none",
  "processing-fallback",
  "device-fallback",
  "network-degraded",
  "unknown",
]);
const allowedCheckStates = new Set(["pass", "fail", "not-tested"]);
const requiredChecks = Object.freeze([
  "singleActiveMediaOwner",
  "receiverDecodedAudio",
  "noDuplicateParticipant",
  "cleanLeaveNoLeakedMedia",
  "scenarioAppliedAsDeclared",
]);
const allowedReasons = new Set([
  "not-collected",
  "scenario-not-applicable",
  "public-stat-not-exposed",
  "no-common-public-timestamp-hook",
  "no-physical-device-available",
  "no-authorized-network-impairment",
  "process-attribution-incomplete",
  "tooling-unavailable",
]);

export const metricCatalog = Object.freeze({
  noiseAttenuationDb: metric("dB", "higher", 3, "absolute", [
    "remote-residual-noise-relative-to-reference",
  ]),
  speechOnsetRetentionPct: metric("percentage-points", "higher", 5, "absolute", [
    "aligned-reference-energy-window",
  ]),
  speechTailRetentionPct: metric("percentage-points", "higher", 5, "absolute", [
    "aligned-reference-energy-window",
  ]),
  echoReturnLossEnhancementDb: metric("dB", "higher", 3, "absolute", [
    "webrtc-standard-stats",
  ]),
  residualEchoAttenuationProxyDb: metric("dB", "higher", 3, "absolute", [
    "far-end-reference-correlation-proxy",
  ]),
  captureToSendLatencyMs: metric("ms", "lower", 20, "relative-percent", [
    "common-public-timestamp-hook",
  ]),
  stimulusToRemoteDecodeLatencyMs: metric("ms", "lower", 20, "relative-percent", [
    "acoustic-stimulus-to-remote-decode",
  ]),
  processCpuPercent: metric("percent", "lower", 20, "relative-percent", [
    "ps-explicit-roots-and-descendants",
  ]),
  residentMemoryMiB: metric("MiB", "lower", 20, "relative-percent", [
    "ps-explicit-roots-and-descendants",
  ]),
  energyImpactScore: metric("relative-score", "lower", 20, "relative-percent", [
    "xctrace-power-profiler-derived",
  ]),
  packetLossPct: metric("percent", "lower", 20, "relative-percent", [
    "webrtc-safe-aggregate",
  ]),
  jitterMs: metric("ms", "lower", 20, "relative-percent", ["webrtc-safe-aggregate"]),
  rttMs: metric("ms", "lower", 20, "relative-percent", ["webrtc-safe-aggregate"]),
  concealmentPct: metric("percent", "lower", 20, "relative-percent", [
    "webrtc-safe-aggregate",
  ]),
  inputDeviceRecoveryMs: metric("ms", "lower", 20, "relative-percent", [
    "route-event-to-remote-energy-resume",
  ]),
  networkRecoveryMs: metric("ms", "lower", 20, "relative-percent", [
    "network-restore-to-remote-energy-resume",
  ]),
});

function metric(unit, direction, threshold, thresholdKind, methods) {
  return Object.freeze({ unit, direction, threshold, thresholdKind, methods: Object.freeze(methods) });
}

class HarnessError extends Error {}

function fail(message) {
  throw new HarnessError(message);
}

function assertObject(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    fail(`${label} must be an object`);
  }
}

function assertExactKeys(value, allowed, label) {
  assertObject(value, label);
  const unknown = Object.keys(value).filter((key) => !allowed.includes(key));
  if (unknown.length > 0) {
    fail(`${label} contains ${unknown.length} forbidden or unknown field(s)`);
  }
}

function assertFiniteNumber(value, label, { min = -Infinity, max = Infinity } = {}) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < min || value > max) {
    fail(`${label} must be a finite number between ${min} and ${max}`);
  }
}

function assertSafeId(value, label) {
  if (typeof value !== "string" || !/^[a-z0-9][a-z0-9-]{0,63}$/.test(value)) {
    fail(`${label} must use only lowercase ASCII letters, numbers, and hyphens`);
  }
}

function assertSha256OrUnknown(value, label) {
  if (value !== "unknown" && !/^[a-f0-9]{64}$/.test(value)) {
    fail(`${label} must be 64 lowercase hexadecimal characters or unknown`);
  }
}

function assertEnum(value, allowed, label) {
  if (!allowed.has(value)) {
    fail(`${label} is not an allowed value`);
  }
}

function assertBoolean(value, label, expected = undefined) {
  if (typeof value !== "boolean" || (expected !== undefined && value !== expected)) {
    fail(`${label} must be ${expected === undefined ? "a boolean" : String(expected)}`);
  }
}

function stableJson(value) {
  return `${JSON.stringify(value, null, 2)}\n`;
}

function writeJson(path, value) {
  writeFileSync(path, stableJson(value), { encoding: "utf8" });
}

function sha256File(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function seededRandom(seedHex) {
  let counter = 0;
  return () => {
    const digest = createHash("sha256")
      .update(Buffer.from(seedHex, "hex"))
      .update(String(counter++))
      .digest();
    return digest.readUInt32BE(0) / 0x1_0000_0000;
  };
}

export function buildTrialOrder(trialCount, seedHex) {
  if (!Number.isInteger(trialCount) || trialCount < 8 || trialCount > 40 || trialCount % 4 !== 0) {
    fail("trial count must be a multiple of four between 8 and 40");
  }
  if (typeof seedHex !== "string" || !/^[a-f0-9]{32,128}$/.test(seedHex)) {
    fail("seed must be 16-64 bytes encoded as lowercase hex");
  }
  const random = seededRandom(seedHex);
  const trials = [];
  const orders = [];
  for (let blockIndex = 0; blockIndex < trialCount / 4; blockIndex += 1) {
    const order = blockOrders[Math.floor(random() * blockOrders.length)];
    orders.push(order);
    for (const label of order) {
      trials.push({
        trialId: `trial-${String(trials.length + 1).padStart(3, "0")}`,
        position: trials.length + 1,
        block: blockIndex + 1,
        path: pathLabels[label],
      });
    }
  }
  return { blockOrders: orders, trials };
}

function defaultMetricEntry(metricName, scenario) {
  if (metricName === "captureToSendLatencyMs") {
    return { status: "unsupported", reason: "no-common-public-timestamp-hook" };
  }
  if (metricName === "inputDeviceRecoveryMs" && scenario !== "input-removal") {
    return { status: "unavailable", reason: "scenario-not-applicable" };
  }
  if (metricName === "networkRecoveryMs" && scenario !== "network-impairment") {
    return { status: "unavailable", reason: "scenario-not-applicable" };
  }
  return { status: "unavailable", reason: "not-collected" };
}

function mechanismForPath(path) {
  return path === "webkit" ? "webkit-platform" : "unknown";
}

function trialTemplate(trial, scenario) {
  return {
    schemaVersion,
    trialId: trial.trialId,
    processingMechanism: mechanismForPath(trial.path),
    degradationState: "unknown",
    checks: Object.fromEntries(requiredChecks.map((check) => [check, "not-tested"])),
    metrics: Object.fromEntries(
      Object.keys(metricCatalog).map((metricName) => [metricName, defaultMetricEntry(metricName, scenario)])
    ),
  };
}

function operatorChecklist(manifest) {
  const sequence = manifest.trials.map((trial) => `${trial.position}:${trial.path}`).join(" -> ");
  return `# STRIDE macOS media A/B operator checklist

Run: ${manifest.runId}
Scenario: ${manifest.scenario}
Sequence: ${sequence}

1. Keep one receiver joined for the whole block. Do not record its name, room URL, account, device label, IP address, SDP, ICE data, or raw media.
2. Before every trial, confirm both media paths are stopped. Start only the path named in the sequence, then confirm exactly one active media owner.
3. Hold sender hardware placement, gain, stimulus level, receiver, room, and network constant. Warm up for 30 seconds before collecting a trial.
4. Collect only the derived metrics enumerated in the input template. Mark anything not defensibly observed unavailable or unsupported; never estimate it.
5. Leave after each trial. Confirm the sender disappears, capture stops, and no duplicate or phantom participant remains before starting the next trial.
6. For network impairment or input removal, apply the physical/manual event only after the baseline interval. This harness does not alter networking, permissions, or devices.
7. Keep raw audio and Instruments traces, if temporarily required, in a mode-0700 temporary directory outside this run. Derive the allowlisted numbers, then delete the raw material before finalizing evidence.
8. Edit each operator-input JSON using only its enums and numeric fields, then ingest it. Complete blind listening separately and anonymously; it is supporting evidence, never the only materiality evidence.
`;
}

export function createManifest(options) {
  const seed = options.seed ?? randomBytes(16).toString("hex");
  const trialCount = options.trials ?? 8;
  const scenario = options.scenario ?? "baseline";
  const routeClass = options.routeClass ?? "unspecified";
  assertEnum(scenario, allowedScenarios, "scenario");
  assertEnum(routeClass, allowedRouteClasses, "route class");
  const { blockOrders: randomizedBlocks, trials } = buildTrialOrder(trialCount, seed);
  const impairment = scenario === "network-impairment"
    ? validateImpairment(options.impairment)
    : null;
  if (scenario !== "network-impairment" && options.impairment) {
    fail("impairment parameters are only valid for the network-impairment scenario");
  }
  const createdAt = options.createdAt ?? new Date().toISOString();
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{3})?Z$/.test(createdAt) || Number.isNaN(Date.parse(createdAt))) {
    fail("createdAt must be an ISO-8601 UTC timestamp");
  }
  const sourceRevision = options.sourceRevision ?? "unknown";
  if (sourceRevision !== "unknown" && !/^[a-f0-9]{7,40}$/.test(sourceRevision)) {
    fail("source revision must be 7-40 lowercase hexadecimal characters or unknown");
  }
  const appBuild = options.appBuild ?? "unknown";
  if (appBuild !== "unknown" && !/^[0-9]{1,9}$/.test(appBuild)) {
    fail("app build must contain only digits or be unknown");
  }
  const sourceDigestSha256 = options.sourceDigestSha256 ?? "unknown";
  assertSha256OrUnknown(sourceDigestSha256, "source digest SHA-256");
  const artifactSha256 = options.artifactSha256 ?? "unknown";
  assertSha256OrUnknown(artifactSha256, "artifact SHA-256");
  const runId = `stride-media-ab-${createdAt.replace(/[-:.TZ]/g, "").slice(0, 14)}-${seed.slice(0, 8)}`;
  return {
    schemaVersion,
    runId,
    createdAt,
    sourceRevision,
    appBuild,
    sourceDigestSha256,
    artifactSha256,
    scenario,
    routeClass,
    impairment,
    randomization: {
      design: "sequential-randomized-balanced-blocks",
      seed,
      blockOrders: randomizedBlocks,
      pathLabels,
    },
    controlContract: {
      sameSenderMac: true,
      oneActiveMediaOwner: true,
      fixedReceiver: true,
      fixedHardwarePlacement: true,
      fixedStimulusAndGain: true,
      thirtySecondWarmup: true,
      noMidCallOwnershipSwitch: true,
    },
    safetyPolicy: {
      allowlistedDerivedMetricsOnly: true,
      rawMediaRetained: false,
      rawRtcReportsRetained: false,
      identifiersRetained: false,
      privilegedMutationPerformedByHarness: false,
    },
    materialityPolicy: {
      minimumTrialsPerPath: trialCount / 2,
      positiveImprovementMeansNativeBetter: true,
      metricThresholds: Object.fromEntries(
        Object.entries(metricCatalog).map(([name, definition]) => [name, {
          threshold: definition.threshold,
          thresholdKind: definition.thresholdKind,
        }])
      ),
      noMaterialRegressionRequired: true,
      allSafetyChecksMustPass: true,
      listeningIsSupportingEvidenceOnly: true,
    },
    trials,
  };
}

function validateImpairment(impairment) {
  assertExactKeys(impairment, ["lossPct", "delayMs", "jitterMs", "rateKbps"], "impairment");
  assertFiniteNumber(impairment.lossPct, "impairment.lossPct", { min: 0, max: 30 });
  assertFiniteNumber(impairment.delayMs, "impairment.delayMs", { min: 0, max: 2000 });
  assertFiniteNumber(impairment.jitterMs, "impairment.jitterMs", { min: 0, max: 1000 });
  assertFiniteNumber(impairment.rateKbps, "impairment.rateKbps", { min: 32, max: 1_000_000 });
  return { ...impairment };
}

export function validateManifest(manifest) {
  const topKeys = [
    "schemaVersion", "runId", "createdAt", "sourceRevision", "appBuild",
    "sourceDigestSha256", "artifactSha256", "scenario", "routeClass", "impairment",
    "randomization", "controlContract", "safetyPolicy", "materialityPolicy", "trials",
  ];
  assertExactKeys(manifest, topKeys, "manifest");
  if (manifest.schemaVersion !== schemaVersion) fail("unsupported manifest schemaVersion");
  assertSafeId(manifest.runId, "manifest.runId");
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{3})?Z$/.test(manifest.createdAt) || Number.isNaN(Date.parse(manifest.createdAt))) {
    fail("manifest.createdAt must be an ISO-8601 UTC timestamp");
  }
  if (manifest.sourceRevision !== "unknown" && !/^[a-f0-9]{7,40}$/.test(manifest.sourceRevision)) {
    fail("manifest.sourceRevision is not safe");
  }
  if (manifest.appBuild !== "unknown" && !/^[0-9]{1,9}$/.test(manifest.appBuild)) {
    fail("manifest.appBuild is not safe");
  }
  assertSha256OrUnknown(manifest.sourceDigestSha256, "manifest.sourceDigestSha256");
  assertSha256OrUnknown(manifest.artifactSha256, "manifest.artifactSha256");
  assertEnum(manifest.scenario, allowedScenarios, "manifest.scenario");
  assertEnum(manifest.routeClass, allowedRouteClasses, "manifest.routeClass");
  assertExactKeys(manifest.randomization, ["design", "seed", "blockOrders", "pathLabels"], "manifest.randomization");
  if (manifest.randomization.design !== "sequential-randomized-balanced-blocks") {
    fail("manifest.randomization.design is unsupported");
  }
  assertExactKeys(manifest.randomization.pathLabels, ["A", "B"], "manifest.randomization.pathLabels");
  if (manifest.randomization.pathLabels.A !== "webkit" || manifest.randomization.pathLabels.B !== "native") {
    fail("manifest.randomization.pathLabels is invalid");
  }
  if (!Array.isArray(manifest.randomization.blockOrders) || manifest.randomization.blockOrders.some((value) => !blockOrders.includes(value))) {
    fail("manifest.randomization.blockOrders is invalid");
  }
  const controlKeys = [
    "sameSenderMac", "oneActiveMediaOwner", "fixedReceiver", "fixedHardwarePlacement",
    "fixedStimulusAndGain", "thirtySecondWarmup", "noMidCallOwnershipSwitch",
  ];
  assertExactKeys(manifest.controlContract, controlKeys, "manifest.controlContract");
  for (const key of controlKeys) assertBoolean(manifest.controlContract[key], `manifest.controlContract.${key}`, true);
  const safetyKeys = [
    "allowlistedDerivedMetricsOnly", "rawMediaRetained", "rawRtcReportsRetained",
    "identifiersRetained", "privilegedMutationPerformedByHarness",
  ];
  assertExactKeys(manifest.safetyPolicy, safetyKeys, "manifest.safetyPolicy");
  assertBoolean(manifest.safetyPolicy.allowlistedDerivedMetricsOnly, "manifest.safetyPolicy.allowlistedDerivedMetricsOnly", true);
  for (const key of safetyKeys.filter((key) => key !== "allowlistedDerivedMetricsOnly")) {
    assertBoolean(manifest.safetyPolicy[key], `manifest.safetyPolicy.${key}`, false);
  }
  assertExactKeys(
    manifest.materialityPolicy,
    [
      "minimumTrialsPerPath", "positiveImprovementMeansNativeBetter", "metricThresholds",
      "noMaterialRegressionRequired", "allSafetyChecksMustPass", "listeningIsSupportingEvidenceOnly",
    ],
    "manifest.materialityPolicy"
  );
  for (const key of [
    "positiveImprovementMeansNativeBetter", "noMaterialRegressionRequired",
    "allSafetyChecksMustPass", "listeningIsSupportingEvidenceOnly",
  ]) assertBoolean(manifest.materialityPolicy[key], `manifest.materialityPolicy.${key}`, true);
  assertExactKeys(manifest.materialityPolicy.metricThresholds, Object.keys(metricCatalog), "manifest.materialityPolicy.metricThresholds");
  for (const [name, definition] of Object.entries(metricCatalog)) {
    const threshold = manifest.materialityPolicy.metricThresholds[name];
    assertExactKeys(threshold, ["threshold", "thresholdKind"], `manifest.materialityPolicy.metricThresholds.${name}`);
    if (threshold.threshold !== definition.threshold || threshold.thresholdKind !== definition.thresholdKind) {
      fail(`manifest materiality threshold for ${name} was altered`);
    }
  }
  if (!Array.isArray(manifest.trials) || manifest.trials.length < 8 || manifest.trials.length % 4 !== 0) {
    fail("manifest.trials must contain balanced blocks totaling at least eight trials");
  }
  if (manifest.materialityPolicy.minimumTrialsPerPath !== manifest.trials.length / 2) {
    fail("manifest.minimumTrialsPerPath must cover every planned trial");
  }
  for (const [index, trial] of manifest.trials.entries()) {
    assertExactKeys(trial, ["trialId", "position", "block", "path"], `manifest.trials[${index}]`);
    assertSafeId(trial.trialId, `manifest.trials[${index}].trialId`);
    if (!Number.isInteger(trial.position) || !Number.isInteger(trial.block)) fail("manifest trial position and block must be integers");
    if (trial.path !== "webkit" && trial.path !== "native") fail("manifest trial path is invalid");
  }
  const expected = buildTrialOrder(manifest.trials.length, manifest.randomization.seed);
  if (JSON.stringify(expected.trials) !== JSON.stringify(manifest.trials)) {
    fail("manifest trial order does not match its recorded randomization seed");
  }
  if (JSON.stringify(expected.blockOrders) !== JSON.stringify(manifest.randomization.blockOrders)) {
    fail("manifest block orders do not match its recorded randomization seed");
  }
  const expectedRunId = `stride-media-ab-${manifest.createdAt.replace(/[-:.TZ]/g, "").slice(0, 14)}-${manifest.randomization.seed.slice(0, 8)}`;
  if (manifest.runId !== expectedRunId) fail("manifest.runId does not match its timestamp and seed");
  if (manifest.scenario === "network-impairment") validateImpairment(manifest.impairment);
  if (manifest.scenario !== "network-impairment" && manifest.impairment !== null) {
    fail("non-network manifest must not contain impairment parameters");
  }
  return manifest;
}

export function validateTrialInput(input, manifest) {
  validateManifest(manifest);
  assertExactKeys(
    input,
    ["schemaVersion", "trialId", "processingMechanism", "degradationState", "checks", "metrics"],
    "trial input"
  );
  if (input.schemaVersion !== schemaVersion) fail("unsupported trial schemaVersion");
  assertSafeId(input.trialId, "trialId");
  const trial = manifest.trials.find((candidate) => candidate.trialId === input.trialId);
  if (!trial) fail("trialId is not present in the manifest");
  assertEnum(input.processingMechanism, allowedMechanisms, "processingMechanism");
  if (
    input.processingMechanism !== "unknown" &&
    !input.processingMechanism.startsWith(`${trial.path}-`) &&
    !(trial.path === "native" && input.processingMechanism.startsWith("native-"))
  ) {
    fail("processingMechanism does not match the manifest media path");
  }
  assertEnum(input.degradationState, allowedDegradationStates, "degradationState");
  assertExactKeys(input.checks, requiredChecks, "checks");
  for (const check of requiredChecks) assertEnum(input.checks[check], allowedCheckStates, `checks.${check}`);
  assertExactKeys(input.metrics, Object.keys(metricCatalog), "metrics");
  for (const [name, definition] of Object.entries(metricCatalog)) {
    validateMetricEntry(input.metrics[name], name, definition);
  }
  if (manifest.scenario !== "input-removal" && input.metrics.inputDeviceRecoveryMs.status === "measured") {
    fail("inputDeviceRecoveryMs may only be measured in an input-removal run");
  }
  if (manifest.scenario !== "network-impairment" && input.metrics.networkRecoveryMs.status === "measured") {
    fail("networkRecoveryMs may only be measured in a network-impairment run");
  }
  return {
    schemaVersion,
    trialId: trial.trialId,
    position: trial.position,
    block: trial.block,
    path: trial.path,
    processingMechanism: input.processingMechanism,
    degradationState: input.degradationState,
    checks: { ...input.checks },
    metrics: Object.fromEntries(
      Object.keys(metricCatalog).map((name) => [name, { ...input.metrics[name] }])
    ),
  };
}

function normalizeTrialRecord(record, manifest) {
  const storedKeys = [
    "schemaVersion", "trialId", "position", "block", "path", "processingMechanism",
    "degradationState", "checks", "metrics",
  ];
  const isStored = record && typeof record === "object" && ("position" in record || "path" in record);
  if (!isStored) return validateTrialInput(record, manifest);
  assertExactKeys(record, storedKeys, "stored trial");
  const raw = {
    schemaVersion: record.schemaVersion,
    trialId: record.trialId,
    processingMechanism: record.processingMechanism,
    degradationState: record.degradationState,
    checks: record.checks,
    metrics: record.metrics,
  };
  const sanitized = validateTrialInput(raw, manifest);
  if (record.position !== sanitized.position || record.block !== sanitized.block || record.path !== sanitized.path) {
    fail("stored trial position, block, or path does not match the manifest");
  }
  return sanitized;
}

function validateMetricEntry(entry, name, definition) {
  assertObject(entry, `metrics.${name}`);
  if (entry.status === "measured") {
    assertExactKeys(entry, ["status", "value", "method", "sampleCount"], `metrics.${name}`);
    const signedDbMetric = definition.unit === "dB";
    assertFiniteNumber(entry.value, `metrics.${name}.value`, {
      min: signedDbMetric ? -200 : 0,
      max: signedDbMetric ? 200 : 1_000_000_000,
    });
    if (!definition.methods.includes(entry.method)) fail(`metrics.${name}.method is not allowed`);
    if (!Number.isInteger(entry.sampleCount) || entry.sampleCount < 1 || entry.sampleCount > 10_000_000) {
      fail(`metrics.${name}.sampleCount must be an integer between 1 and 10000000`);
    }
    return;
  }
  if (entry.status === "unavailable" || entry.status === "unsupported") {
    assertExactKeys(entry, ["status", "reason"], `metrics.${name}`);
    assertEnum(entry.reason, allowedReasons, `metrics.${name}.reason`);
    return;
  }
  fail(`metrics.${name}.status must be measured, unavailable, or unsupported`);
}

export function quantile(values, probability) {
  if (!Array.isArray(values) || values.length === 0) return null;
  const sorted = [...values].sort((left, right) => left - right);
  if (sorted.length === 1) return sorted[0];
  const position = (sorted.length - 1) * probability;
  const lower = Math.floor(position);
  const fraction = position - lower;
  return sorted[lower] + (sorted[Math.min(lower + 1, sorted.length - 1)] - sorted[lower]) * fraction;
}

function rounded(value) {
  return value === null || !Number.isFinite(value) ? null : Number(value.toFixed(4));
}

export function summarizeTrials(manifest, trials) {
  validateManifest(manifest);
  const byId = new Map();
  for (const trialInput of trials) {
    const sanitized = normalizeTrialRecord(trialInput, manifest);
    if (byId.has(sanitized.trialId)) fail(`duplicate ingested trial ${sanitized.trialId}`);
    byId.set(sanitized.trialId, sanitized);
  }
  const ordered = manifest.trials.flatMap((trial) => byId.has(trial.trialId) ? [byId.get(trial.trialId)] : []);
  const comparisons = {};
  for (const [name, definition] of Object.entries(metricCatalog)) {
    const values = { webkit: [], native: [] };
    for (const trial of ordered) {
      const entry = trial.metrics[name];
      if (entry.status === "measured") values[trial.path].push(entry.value);
    }
    comparisons[name] = compareMetric(definition, values, manifest.materialityPolicy.minimumTrialsPerPath);
  }
  const missingTrialIds = manifest.trials
    .filter((trial) => !byId.has(trial.trialId))
    .map((trial) => trial.trialId);
  const failedChecks = ordered.flatMap((trial) =>
    requiredChecks
      .filter((check) => trial.checks[check] !== "pass")
      .map((check) => ({ trialId: trial.trialId, check, state: trial.checks[check] }))
  );
  const unresolvedRuntimeTruth = ordered
    .filter((trial) => trial.processingMechanism === "unknown" || trial.degradationState === "unknown")
    .map((trial) => trial.trialId);
  const comparisonValues = Object.values(comparisons);
  const unassessableComparisons = Object.entries(comparisons)
    .filter(([, comparison]) => comparison.materiality.startsWith("unassessable-"))
    .map(([name]) => name);
  const wins = comparisonValues.filter((comparison) => comparison.materiality === "material-win");
  const regressions = comparisonValues.filter((comparison) => comparison.materiality === "material-regression");
  const missingArtifactIdentity = [];
  if (manifest.appBuild === "unknown") missingArtifactIdentity.push("appBuild");
  if (!/^[a-f0-9]{40}$/.test(manifest.sourceRevision)) missingArtifactIdentity.push("sourceRevision");
  if (manifest.sourceDigestSha256 === "unknown") missingArtifactIdentity.push("sourceDigestSha256");
  if (manifest.artifactSha256 === "unknown") missingArtifactIdentity.push("artifactSha256");
  let conclusion = "insufficient-evidence";
  if (
    missingTrialIds.length === 0
    && failedChecks.length === 0
    && unresolvedRuntimeTruth.length === 0
    && unassessableComparisons.length === 0
  ) {
    if (regressions.length > 0) conclusion = "material-regression";
    else if (wins.length > 0) {
      if (missingArtifactIdentity.length === 0) conclusion = "material-benefit";
    }
    else if (comparisonValues.some((comparison) => comparison.materiality === "within-threshold")) {
      conclusion = "no-material-benefit";
    }
  }
  return {
    schemaVersion,
    runId: manifest.runId,
    scenario: manifest.scenario,
    expectedTrialCount: manifest.trials.length,
    ingestedTrialCount: ordered.length,
    missingTrialIds,
    failedChecks,
    unresolvedRuntimeTruth,
    unassessableComparisons,
    missingArtifactIdentity,
    comparisons,
    conclusion,
    conclusionLimits: [
      "median-threshold-screen-not-statistical-significance",
      "listening-check-not-included-in-conclusion",
      "stimulus-to-remote-decode-is-not-capture-to-send",
      "process-sampler-is-not-an-energy-measurement",
    ],
  };
}

function compareMetric(definition, values, minimumTrialsPerPath) {
  const webkitMedian = quantile(values.webkit, 0.5);
  const nativeMedian = quantile(values.native, 0.5);
  const nativeMinusWebkit = webkitMedian === null || nativeMedian === null
    ? null
    : nativeMedian - webkitMedian;
  const improvement = nativeMinusWebkit === null
    ? null
    : (definition.direction === "higher" ? nativeMinusWebkit : -nativeMinusWebkit);
  const relativeImprovementPct = improvement === null || webkitMedian === 0
    ? null
    : (improvement / Math.abs(webkitMedian)) * 100;
  const sufficient = values.webkit.length >= minimumTrialsPerPath && values.native.length >= minimumTrialsPerPath;
  let materiality = "insufficient-samples";
  if (sufficient) {
    if (definition.thresholdKind === "relative-percent" && webkitMedian === 0) {
      materiality = nativeMedian === 0 ? "within-threshold" : "unassessable-zero-baseline";
    } else {
      const thresholdValue = definition.thresholdKind === "absolute" ? improvement : relativeImprovementPct;
      if (thresholdValue !== null && thresholdValue >= definition.threshold) materiality = "material-win";
      else if (thresholdValue !== null && thresholdValue <= -definition.threshold) materiality = "material-regression";
      else materiality = "within-threshold";
    }
  }
  return {
    unit: definition.unit,
    direction: definition.direction,
    threshold: definition.threshold,
    thresholdKind: definition.thresholdKind,
    webkit: distribution(values.webkit),
    native: distribution(values.native),
    nativeMinusWebkit: rounded(nativeMinusWebkit),
    improvement: rounded(improvement),
    relativeImprovementPct: rounded(relativeImprovementPct),
    materiality,
  };
}

function distribution(values) {
  return {
    n: values.length,
    median: rounded(quantile(values, 0.5)),
    p95: rounded(quantile(values, 0.95)),
  };
}

export function parseProcessTable(output) {
  if (typeof output !== "string") fail("process table must be text");
  return output.split(/\r?\n/).flatMap((line) => {
    const match = line.trim().match(/^(\d+)\s+(\d+)\s+([0-9.]+)\s+(\d+)$/);
    if (!match) return [];
    return [{
      pid: Number(match[1]),
      ppid: Number(match[2]),
      cpuPercent: Number(match[3]),
      rssKiB: Number(match[4]),
    }];
  });
}

export function aggregateProcessTree(rows, rootPids) {
  const selected = new Set(rootPids);
  let changed = true;
  while (changed) {
    changed = false;
    for (const row of rows) {
      if (selected.has(row.ppid) && !selected.has(row.pid)) {
        selected.add(row.pid);
        changed = true;
      }
    }
  }
  const processes = rows.filter((row) => selected.has(row.pid));
  return {
    processCount: processes.length,
    cpuPercent: processes.reduce((sum, row) => sum + row.cpuPercent, 0),
    residentMemoryMiB: processes.reduce((sum, row) => sum + row.rssKiB, 0) / 1024,
  };
}

export function summarizeProcessSamples(samples) {
  if (!Array.isArray(samples) || samples.length === 0) fail("at least one process sample is required");
  return {
    schemaVersion,
    source: "process-tree-sampler",
    coverage: "explicit-roots-and-descendants",
    sampleCount: samples.length,
    processCount: distribution(samples.map((sample) => sample.processCount)),
    metrics: {
      processCpuPercent: {
        status: "measured",
        value: rounded(quantile(samples.map((sample) => sample.cpuPercent), 0.5)),
        method: "ps-explicit-roots-and-descendants",
        sampleCount: samples.length,
      },
      residentMemoryMiB: {
        status: "measured",
        value: rounded(quantile(samples.map((sample) => sample.residentMemoryMiB), 0.5)),
        method: "ps-explicit-roots-and-descendants",
        sampleCount: samples.length,
      },
    },
    p95: {
      processCpuPercent: rounded(quantile(samples.map((sample) => sample.cpuPercent), 0.95)),
      residentMemoryMiB: rounded(quantile(samples.map((sample) => sample.residentMemoryMiB), 0.95)),
    },
    limitations: [
      "auxiliary-processes-not-descended-from-an-explicit-root-are-excluded",
      "cpu-percent-is-an-os-sample-not-energy",
      "no-process-identifiers-or-names-retained",
    ],
  };
}

async function sampleProcesses({ rootPids, durationSeconds, intervalMs, output }) {
  const samples = [];
  const started = Date.now();
  while (Date.now() - started < durationSeconds * 1000 || samples.length === 0) {
    let table;
    try {
      table = execFileSync("ps", ["-axo", "pid=,ppid=,%cpu=,rss="], { encoding: "utf8" });
    } catch {
      fail("process table sampling failed");
    }
    const aggregate = aggregateProcessTree(parseProcessTable(table), rootPids);
    if (aggregate.processCount === 0) fail("none of the explicit process roots are running");
    samples.push(aggregate);
    const remaining = durationSeconds * 1000 - (Date.now() - started);
    if (remaining > 0) await new Promise((accept) => setTimeout(accept, Math.min(intervalMs, remaining)));
  }
  if (existsSync(output)) fail("process summary output already exists");
  mkdirSync(dirname(output), { recursive: true, mode: 0o700 });
  writeJson(output, summarizeProcessSamples(samples));
  return { sampleCount: samples.length };
}

function readJson(path, label) {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch {
    fail(`${label} is not valid JSON`);
  }
}

function initializeRun(args) {
  const output = resolve(requiredArg(args, "output"));
  if (existsSync(output) && (!statSync(output).isDirectory() || readdirSync(output).length > 0)) {
    fail("output must be a new or empty directory");
  }
  mkdirSync(resolve(output, "operator-input"), { recursive: true, mode: 0o700 });
  mkdirSync(resolve(output, "trials"), { recursive: true, mode: 0o700 });
  const scenario = args.scenario ?? "baseline";
  const networkOptions = ["loss-percent", "delay-ms", "jitter-ms", "rate-kbps"];
  if (scenario !== "network-impairment" && networkOptions.some((name) => args[name] !== undefined)) {
    fail("network impairment parameters require --scenario network-impairment");
  }
  const impairment = scenario === "network-impairment" ? {
    lossPct: numberArg(args, "loss-percent"),
    delayMs: numberArg(args, "delay-ms"),
    jitterMs: numberArg(args, "jitter-ms"),
    rateKbps: numberArg(args, "rate-kbps"),
  } : undefined;
  const manifest = createManifest({
    seed: args.seed,
    trials: args.trials === undefined ? 8 : numberArg(args, "trials"),
    scenario,
    routeClass: args["route-class"],
    impairment,
    appBuild: args["app-build"],
    sourceRevision: args.revision,
    sourceDigestSha256: args["source-digest-sha256"],
    artifactSha256: args["artifact-sha256"],
  });
  writeJson(resolve(output, "manifest.json"), manifest);
  writeFileSync(resolve(output, "operator-checklist.md"), operatorChecklist(manifest), "utf8");
  for (const trial of manifest.trials) {
    writeJson(resolve(output, "operator-input", `${trial.trialId}.json`), trialTemplate(trial, scenario));
  }
  return { runId: manifest.runId, trials: manifest.trials.length };
}

function ingestTrial(args) {
  const manifestPath = resolve(requiredArg(args, "manifest"));
  const inputPath = resolve(requiredArg(args, "input"));
  const manifest = validateManifest(readJson(manifestPath, "manifest"));
  const sanitized = validateTrialInput(readJson(inputPath, "trial input"), manifest);
  const output = resolve(dirname(manifestPath), "trials", `${sanitized.trialId}.json`);
  if (existsSync(output)) fail("trial has already been ingested; start a new run rather than overwriting evidence");
  writeJson(output, sanitized);
  return { trialId: sanitized.trialId, path: sanitized.path };
}

function reportRun(args) {
  const manifestPath = resolve(requiredArg(args, "manifest"));
  const runDir = dirname(manifestPath);
  const manifest = validateManifest(readJson(manifestPath, "manifest"));
  const allowedTopLevel = new Set([
    "manifest.json", "operator-checklist.md", "operator-input", "trials",
    "summary.json", "trials.csv", "SHA256SUMS",
  ]);
  if (readdirSync(runDir).some((name) => !allowedTopLevel.has(name))) {
    fail("run directory contains an unexpected artifact");
  }
  const expectedTrialFiles = new Set(manifest.trials.map((trial) => `${trial.trialId}.json`));
  const operatorInputDir = resolve(runDir, "operator-input");
  const operatorInputFiles = readdirSync(operatorInputDir);
  if (
    operatorInputFiles.length !== expectedTrialFiles.size ||
    operatorInputFiles.some((name) => !expectedTrialFiles.has(name))
  ) fail("operator-input directory does not match the manifest");
  const validatedOperatorInputs = new Map(operatorInputFiles.map((name) => {
    const value = readJson(resolve(operatorInputDir, name), "operator input");
    const validated = validateTrialInput(value, manifest);
    return [validated.trialId, value];
  }));
  const trialDir = resolve(runDir, "trials");
  const trialFiles = existsSync(trialDir) ? readdirSync(trialDir) : [];
  if (trialFiles.some((name) => !expectedTrialFiles.has(name))) {
    fail("trials directory contains an unexpected artifact");
  }
  const trials = trialFiles.map((name) => readJson(resolve(trialDir, name), "stored trial"));
  for (const record of trials) {
    const validated = normalizeTrialRecord(record, manifest);
    const currentInput = validatedOperatorInputs.get(validated.trialId);
    const currentSanitized = validateTrialInput(currentInput, manifest);
    if (JSON.stringify(validated) !== JSON.stringify(currentSanitized)) {
      fail("an ingested trial no longer matches its operator input");
    }
  }
  const summary = summarizeTrials(manifest, trials);
  const summaryPath = resolve(runDir, "summary.json");
  const csvPath = resolve(runDir, "trials.csv");
  writeJson(summaryPath, summary);
  writeFileSync(csvPath, trialsCsv(manifest, trials), "utf8");
  const evidenceFiles = [manifestPath, resolve(runDir, "operator-checklist.md"), summaryPath, csvPath]
    .concat(operatorInputFiles.sort().map((name) => resolve(operatorInputDir, name)))
    .concat(trialFiles.sort().map((name) => resolve(trialDir, name)));
  const sums = evidenceFiles
    .map((path) => `${sha256File(path)}  ${relative(runDir, path)}`)
    .join("\n");
  writeFileSync(resolve(runDir, "SHA256SUMS"), `${sums}\n`, "utf8");
  return { conclusion: summary.conclusion, ingestedTrialCount: summary.ingestedTrialCount };
}

function trialsCsv(manifest, trials) {
  const sanitized = trials.map((trial) => normalizeTrialRecord(trial, manifest));
  const rows = [[
    "trial_id", "position", "block", "path", "processing_mechanism", "degradation_state",
    "metric", "status", "value", "unit", "method", "sample_count", "reason",
  ]];
  for (const trial of sanitized.sort((left, right) => left.position - right.position)) {
    for (const [name, definition] of Object.entries(metricCatalog)) {
      const entry = trial.metrics[name];
      rows.push([
        trial.trialId, trial.position, trial.block, trial.path, trial.processingMechanism,
        trial.degradationState, name, entry.status, entry.value ?? "", definition.unit,
        entry.method ?? "", entry.sampleCount ?? "", entry.reason ?? "",
      ]);
    }
  }
  return `${rows.map((row) => row.map(csvCell).join(",")).join("\n")}\n`;
}

function csvCell(value) {
  const text = String(value);
  return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

function parseArgs(argv) {
  const [command, ...tokens] = argv;
  const args = {};
  for (let index = 0; index < tokens.length; index += 1) {
    const token = tokens[index];
    if (!token.startsWith("--")) fail("unexpected positional argument");
    const key = token.slice(2);
    if (key.length === 0 || key in args) fail("invalid or duplicate option");
    const value = tokens[index + 1];
    if (value === undefined || value.startsWith("--")) fail("option requires a value");
    args[key] = value;
    index += 1;
  }
  return { command, args };
}

function requiredArg(args, name) {
  if (typeof args[name] !== "string" || args[name].length === 0) fail(`--${name} is required`);
  return args[name];
}

function numberArg(args, name) {
  const value = Number(requiredArg(args, name));
  if (!Number.isFinite(value)) fail(`--${name} must be a finite number`);
  return value;
}

function usage() {
  return `Usage:
  node scripts/stride-macos-media-ab.mjs init --output <new-dir> [--trials 8] [--seed <hex>] [--scenario baseline|network-impairment|input-removal] [--route-class builtin|bluetooth|wired|usb|unspecified] [--app-build <number>] [--revision <git-sha>] [--source-digest-sha256 <sha256>] [--artifact-sha256 <sha256>]
  node scripts/stride-macos-media-ab.mjs ingest --manifest <manifest.json> --input <trial.json>
  node scripts/stride-macos-media-ab.mjs report --manifest <manifest.json>
  node scripts/stride-macos-media-ab.mjs sample-process --pids <pid[,pid...]> --duration-seconds <seconds> --interval-ms <milliseconds> --output <summary.json>

Network-impairment init also requires --loss-percent, --delay-ms, --jitter-ms, and --rate-kbps.
`;
}

async function main() {
  const { command, args } = parseArgs(process.argv.slice(2));
  let result;
  if (command === "init") {
    assertOptionKeys(args, [
      "output", "trials", "seed", "scenario", "route-class", "app-build", "revision",
      "source-digest-sha256", "artifact-sha256",
      "loss-percent", "delay-ms", "jitter-ms", "rate-kbps",
    ]);
    result = initializeRun(args);
  } else if (command === "ingest") {
    assertOptionKeys(args, ["manifest", "input"]);
    result = ingestTrial(args);
  } else if (command === "report") {
    assertOptionKeys(args, ["manifest"]);
    result = reportRun(args);
  }
  else if (command === "sample-process") {
    assertOptionKeys(args, ["pids", "duration-seconds", "interval-ms", "output"]);
    const rootPids = requiredArg(args, "pids").split(",").map((value) => Number(value));
    if (rootPids.length === 0 || rootPids.some((pid) => !Number.isInteger(pid) || pid < 1)) {
      fail("--pids must be a comma-separated list of positive integers");
    }
    const durationSeconds = numberArg(args, "duration-seconds");
    const intervalMs = numberArg(args, "interval-ms");
    if (durationSeconds < 2 || durationSeconds > 3600) fail("duration must be between 2 and 3600 seconds");
    if (intervalMs < 100 || intervalMs > 60_000) fail("interval must be between 100 and 60000 milliseconds");
    result = await sampleProcesses({
      rootPids,
      durationSeconds,
      intervalMs,
      output: resolve(requiredArg(args, "output")),
    });
  } else {
    process.stderr.write(usage());
    process.exitCode = 2;
    return;
  }
  process.stdout.write(stableJson({ ok: true, ...result }));
}

function assertOptionKeys(args, allowed) {
  const unknown = Object.keys(args).filter((key) => !allowed.includes(key));
  if (unknown.length > 0) fail(`${unknown.length} unknown option(s)`);
}

const isEntrypoint = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isEntrypoint) {
  main().catch((error) => {
    const message = error instanceof HarnessError
      ? error.message
      : "operation failed without retaining system details";
    process.stderr.write(stableJson({ ok: false, error: message }));
    process.exitCode = 1;
  });
}
