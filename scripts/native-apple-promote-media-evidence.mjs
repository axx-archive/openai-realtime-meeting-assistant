#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

const mediaAssertionKeys = [
  "cameraPublished",
  "microphonePublished",
  "remoteAudioReceived",
  "remoteVideoRendered",
];

const platformConfig = {
  iphone: {
    target: "MeetingAssistAppleApp",
    clientPlatform: "ios",
    artifactName: "iphone-media.json",
  },
  ipad: {
    target: "MeetingAssistAppleApp",
    clientPlatform: "ipados",
    artifactName: "ipad-media.json",
  },
  mac: {
    target: "MeetingAssistMacApp",
    clientPlatform: "macos",
    artifactName: "mac-media.json",
  },
};

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-promote-media-evidence.mjs --proofpack-dir path",
    "    --platform iphone|ipad|mac --input path --confirm-physical-device",
    "    --confirm-same-room [--apple-dir apple] [--promoted-at iso] [--force]",
    "",
    "Promotes one app-copied native_device_media QA snapshot into the matching",
    "proof-pack physical-device artifact and updates ReleaseEvidence.draft.json.",
    "The command never turns simulator, pending, mismatched, or secret-bearing",
    "artifacts into release proof.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    proofpackDir: "",
    platform: "",
    input: "",
    promotedAt: "",
    confirmPhysicalDevice: false,
    confirmSameRoom: false,
    force: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--proofpack-dir") {
      args.proofpackDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--platform") {
      args.platform = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--input") {
      args.input = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--promoted-at") {
      args.promotedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-physical-device") {
      args.confirmPhysicalDevice = true;
    } else if (arg === "--confirm-same-room") {
      args.confirmSameRoom = true;
    } else if (arg === "--force") {
      args.force = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return args;
}

function requiredValue(argv, index, flag) {
  const value = argv[index + 1] ?? "";
  if (!value || value.startsWith("--")) {
    throw new Error(`${flag} requires a value.`);
  }
  return value;
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function writeJSON(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  const tempPath = `${path}.tmp-${process.pid}-${Date.now()}`;
  writeFileSync(tempPath, `${JSON.stringify(value, null, 2)}\n`);
  renameSync(tempPath, path);
}

function validTimestamp(value) {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2}T/.test(value) && !Number.isNaN(Date.parse(value));
}

function nonPlaceholderString(value) {
  if (typeof value !== "string") {
    return false;
  }
  const trimmed = value.trim();
  if (!trimmed || /^<[^>]+>$/.test(trimmed) || /^0+$/.test(trimmed)) {
    return false;
  }
  const normalized = trimmed.toUpperCase().replace(/[\s-]+/g, "_");
  return !["TODO", "TBD", "FIXME", "CHANGE_ME", "PLACEHOLDER", "EXAMPLE", "SAMPLE", "DUMMY", "UNKNOWN", "N_A", "NA"].includes(
    normalized
  );
}

function collectUnsafeContent(value, path = "$") {
  const problems = [];
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...collectUnsafeContent(item, `${path}[${index}]`));
    });
    return problems;
  }
  if (!value || typeof value !== "object") {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (
        /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\b[A-Z0-9]{10}\b/.test(trimmed) ||
        /-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(trimmed) ||
        /-----BEGIN CERTIFICATE-----/.test(trimmed) ||
        /\.(p8|p12|mobileprovision|provisionprofile)\b/i.test(trimmed) ||
        /\bcandidate:/.test(trimmed) ||
        /\ba=candidate/.test(trimmed) ||
        /^v=0(?:\r?\n|$)/.test(trimmed) ||
        /\bturns?:[^,\s]+/i.test(trimmed) ||
        /\b(?:\d{1,3}\.){3}\d{1,3}\b/.test(trimmed) ||
        /\b[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){2,}\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    if (
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|turn_?(user|pass|credential)|rawSdp|rawIceCandidates|candidateIds?|localCandidateId|remoteCandidateId|headers?|cookies?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function allAssertionsTrue(assertions) {
  return Boolean(assertions) && typeof assertions === "object" && mediaAssertionKeys.every((key) => assertions[key] === true);
}

function positiveAssertionEvidence(snapshot, key) {
  const assertion = snapshot.assertionEvidence?.[key];
  return assertion && typeof assertion === "object" && assertion.passed === true && Number(assertion.value) > 0;
}

function rendererEvidenceProblems(snapshot) {
  const problems = [];
  const renderer = snapshot.renderer;
  if (!renderer || typeof renderer !== "object" || Array.isArray(renderer)) {
    return ["renderer"];
  }
  if (renderer.source !== "native_remote_video_renderer") {
    problems.push("renderer.source");
  }
  if (!(Number(renderer.remoteVideoFramesRendered) > 0)) {
    problems.push("renderer.remoteVideoFramesRendered");
  }
  if (!(Number(renderer.observedRemoteVideoTracks) > 0)) {
    problems.push("renderer.observedRemoteVideoTracks");
  }
  if (!(Number(renderer.latestFrameWidth) > 0)) {
    problems.push("renderer.latestFrameWidth");
  }
  if (!(Number(renderer.latestFrameHeight) > 0)) {
    problems.push("renderer.latestFrameHeight");
  }
  if (!validTimestamp(renderer.latestRenderedAt)) {
    problems.push("renderer.latestRenderedAt");
  }
  if (renderer.capturesPixels !== false) {
    problems.push("renderer.capturesPixels");
  }
  const remoteVideoEvidence = snapshot.assertionEvidence?.remoteVideoRendered;
  if (remoteVideoEvidence?.source !== "nativeRemoteVideoRenderer+inboundVideoDecoded") {
    problems.push("assertionEvidence.remoteVideoRendered.source");
  }
  return problems;
}

function validateSnapshot(snapshot, draft, platform, args) {
  const problems = [];
  const config = platformConfig[platform];
  if (!snapshot || typeof snapshot !== "object" || Array.isArray(snapshot)) {
    return ["input:not_object"];
  }
  if (snapshot.schemaVersion !== 1) {
    problems.push("schemaVersion");
  }
  if (snapshot.artifactType !== "native_device_media") {
    problems.push("artifactType");
  }
  if (snapshot.claimScope !== "qa_snapshot") {
    problems.push("claimScope");
  }
  if (snapshot.releaseEligible !== false) {
    problems.push("releaseEligible");
  }
  if (snapshot.status !== "observed") {
    problems.push("status");
  }
  if (snapshot.lifecycle !== "connected") {
    problems.push("lifecycle");
  }
  if (!validTimestamp(snapshot.capturedAt)) {
    problems.push("capturedAt");
  }
  if (!args.confirmPhysicalDevice) {
    problems.push("confirmPhysicalDevice");
  }
  if (!args.confirmSameRoom) {
    problems.push("confirmSameRoom");
  }
  for (const [key, expected] of [
    ["runId", draft.runId],
    ["roomId", draft.roomId],
  ]) {
    const value = String(snapshot[key] ?? "").trim();
    if (!value) {
      problems.push(`${key}:empty`);
    } else if (value !== expected) {
      problems.push(key);
    }
  }
  if (!snapshot.app || typeof snapshot.app !== "object") {
    problems.push("app");
  } else {
    if (snapshot.app.version !== draft.version) {
      problems.push("app.version");
    }
    if (snapshot.app.build !== draft.build) {
      problems.push("app.build");
    }
    if (snapshot.app.target !== config.target) {
      problems.push("app.target");
    }
    if (snapshot.app.clientPlatform !== config.clientPlatform) {
      problems.push("app.clientPlatform");
    }
  }
  if (!snapshot.device || typeof snapshot.device !== "object") {
    problems.push("device");
  } else {
    if (snapshot.device.kind !== platform) {
      problems.push("device.kind");
    }
    if (snapshot.device.physical !== true) {
      problems.push("device.physical");
    }
    if (!nonPlaceholderString(snapshot.device.model)) {
      problems.push("device.model");
    }
    if (!nonPlaceholderString(snapshot.device.os)) {
      problems.push("device.os");
    }
  }
  if (!allAssertionsTrue(snapshot.mediaAssertions)) {
    problems.push("mediaAssertions");
  }
  for (const key of mediaAssertionKeys) {
    if (!positiveAssertionEvidence(snapshot, key)) {
      problems.push(`assertionEvidence.${key}`);
    }
  }
  if (!snapshot.counters || typeof snapshot.counters !== "object") {
    problems.push("counters");
  } else {
    for (const key of [
      "outboundAudioPacketsSent",
      "outboundVideoFramesSent",
      "inboundAudioPacketsReceived",
      "inboundVideoDecoded",
    ]) {
      if (!(Number(snapshot.counters[key]) > 0)) {
        problems.push(`counters.${key}`);
      }
    }
  }
  if (!(Number(snapshot.remoteVideoTiles) > 0)) {
    problems.push("remoteVideoTiles");
  }
  problems.push(...rendererEvidenceProblems(snapshot));
  const unsafe = collectUnsafeContent(snapshot);
  if (unsafe.length > 0) {
    problems.push(`unsafeContent:${unsafe.slice(0, 4).join("|")}`);
  }
  return problems;
}

function artifactRefToPath(ref) {
  if (typeof ref !== "string" || !ref.startsWith("artifacts/") || ref.split("/").includes("..")) {
    throw new Error(`physicalDeviceMedia artifactRef must be a repo-local artifacts/ path: ${ref}`);
  }
  return resolve(rootDir, ref);
}

function repoSafeSourceLabel(path) {
  const repoRelative = relative(rootDir, path);
  if (!repoRelative.startsWith("..") && !repoRelative.startsWith("/")) {
    return repoRelative.split(/[/\\]/).join("/");
  }
  return `external:${basename(path)}`;
}

function replaceableArtifact(path) {
  if (!existsSync(path)) {
    return true;
  }
  let existing;
  try {
    existing = readJSON(path);
  } catch {
    return false;
  }
  if (!existing || typeof existing !== "object" || Array.isArray(existing)) {
    return false;
  }
  return existing.status === "pending" && existing.releaseEligible !== true;
}

function promotedArtifact(snapshot, draft, platform, promotedAt, inputPath) {
  const item = draft.physicalDeviceMedia[platform];
  const testedAt = snapshot.capturedAt;
  return {
    ...snapshot,
    claimScope: "physical_device",
    releaseEligible: true,
    status: "passed",
    runId: draft.runId,
    roomId: draft.roomId,
    platform,
    releaseEvidenceSummary: {
      status: "passed",
      runId: draft.runId,
      roomId: draft.roomId,
      device: snapshot.device.model,
      os: snapshot.device.os,
      testedAt,
      mediaAssertions: { ...snapshot.mediaAssertions },
    },
    promotion: {
      promotedAt,
      sourceClaimScope: snapshot.claimScope,
      sourceStatus: snapshot.status,
      sourceReleaseEligible: snapshot.releaseEligible,
      sourceArtifact: repoSafeSourceLabel(inputPath),
      operatorConfirmedPhysicalDevice: true,
      operatorConfirmedSameRoom: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
}

function promote(args) {
  const platform = args.platform;
  if (!platformConfig[platform]) {
    throw new Error("--platform must be one of iphone, ipad, or mac.");
  }
  if (!args.proofpackDir) {
    throw new Error("--proofpack-dir is required.");
  }
  if (!args.input) {
    throw new Error("--input is required.");
  }
  const proofpackDir = resolve(rootDir, args.proofpackDir);
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  const proofpackPath = join(proofpackDir, "proofpack.json");
  const inputPath = resolve(args.input);
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack evidence draft: ${draftPath}`);
  }
  if (!existsSync(inputPath)) {
    throw new Error(`Missing input evidence snapshot: ${inputPath}`);
  }

  const draft = readJSON(draftPath);
  if (!existsSync(proofpackPath)) {
    throw new Error(`Missing proof-pack manifest: ${proofpackPath}`);
  }
  const proofpack = readJSON(proofpackPath);
  const item = draft.physicalDeviceMedia?.[platform];
  if (!item || typeof item !== "object") {
    throw new Error(`ReleaseEvidence.draft.json is missing physicalDeviceMedia.${platform}.`);
  }
  for (const key of ["version", "build", "runId", "roomId"]) {
    if (String(proofpack[key] ?? "") !== String(draft[key] ?? "")) {
      throw new Error(`proofpack.json ${key} does not match ReleaseEvidence.draft.json.`);
    }
  }
  if (proofpack.evidenceArtifacts?.[platform] !== item.artifactRef) {
    throw new Error(`proofpack.json evidenceArtifacts.${platform} does not match ReleaseEvidence.draft.json.`);
  }
  if (item.status === "passed" && !args.force) {
    throw new Error(`${platform} media evidence is already passed. Use --force to replace it.`);
  }

  const snapshot = readJSON(inputPath);
  const problems = validateSnapshot(snapshot, draft, platform, args);
  if (problems.length > 0) {
    throw new Error(`Input evidence cannot be promoted for ${platform}. Invalid: ${[...new Set(problems)].slice(0, 10).join(", ")}`);
  }

  const artifactPath = artifactRefToPath(item.artifactRef);
  const relativeArtifactDir = relative(proofpackDir, artifactPath);
  if (relativeArtifactDir.startsWith("..")) {
    throw new Error(`Target media artifact must stay inside the proof pack: ${item.artifactRef}`);
  }
  if (!args.force && !replaceableArtifact(artifactPath)) {
    throw new Error(`${platform} media artifact already contains non-pending evidence. Use --force to replace it.`);
  }
  const promotedAt = args.promotedAt || new Date().toISOString();
  if (!validTimestamp(promotedAt)) {
    throw new Error("--promoted-at must be an ISO-like timestamp.");
  }
  const artifact = promotedArtifact(snapshot, draft, platform, promotedAt, inputPath);
  writeJSON(artifactPath, artifact);

  draft.physicalDeviceMedia[platform] = {
    ...item,
    status: "passed",
    runId: draft.runId,
    roomId: draft.roomId,
    device: snapshot.device.model,
    os: snapshot.device.os,
    testedAt: snapshot.capturedAt,
    artifactRef: item.artifactRef,
    mediaAssertions: { ...snapshot.mediaAssertions },
  };
  writeJSON(draftPath, draft);

  return {
    ok: true,
    platform,
    proofpackDir,
    proofpackPath: existsSync(proofpackPath) ? proofpackPath : undefined,
    evidenceDraft: draftPath,
    artifactPath,
    artifactRef: item.artifactRef,
    version: draft.version,
    build: draft.build,
    runId: draft.runId,
    roomId: draft.roomId,
    promotedAt,
    physicalDeviceMediaComplete: Object.values(draft.physicalDeviceMedia).every((entry) => entry?.status === "passed"),
    nextSteps: proofpack.nextSteps ?? [
      "Complete remaining device, TURN, TestFlight, and notarization evidence.",
      "Run node scripts/native-apple-release-readiness.mjs --strict --evidence-file <proofpack>/ReleaseEvidence.draft.json.",
    ],
  };
}

function main() {
  try {
    const args = parseArgs(process.argv.slice(2));
    if (args.help) {
      console.log(usage());
      return;
    }
    console.log(JSON.stringify(promote(args), null, 2));
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
    process.exitCode = 1;
  }
}

main();
