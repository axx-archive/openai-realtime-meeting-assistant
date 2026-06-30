#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const allowedAppTargets = new Set(["MeetingAssistAppleApp", "MeetingAssistMacApp"]);
const allowedClientPlatforms = new Set(["ios", "ipados", "macos"]);
const allowedDeviceKinds = new Set(["iphone", "ipad", "mac"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-promote-turn-evidence.mjs --proofpack-dir path",
    "    --input path --network name --confirm-restrictive-network --confirm-same-room",
    "    [--promoted-at iso] [--force]",
    "",
    "Promotes one sanitized native_turn_relay_observation into the proof-pack",
    "restrictive-network TURN artifact and updates ReleaseEvidence.draft.json.",
    "The command rejects raw ICE candidates, TURN credentials, IP addresses,",
    "secret-shaped fields, non-relay observations, and mismatched run/room proof.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    input: "",
    network: "",
    promotedAt: "",
    confirmRestrictiveNetwork: false,
    confirmSameRoom: false,
    force: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--proofpack-dir") {
      args.proofpackDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--input") {
      args.input = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--network") {
      args.network = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--promoted-at") {
      args.promotedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-restrictive-network") {
      args.confirmRestrictiveNetwork = true;
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

function normalizedRelayProtocol(value) {
  const normalized = String(value ?? "").trim().toLowerCase();
  return ["turn", "turns"].includes(normalized) ? normalized : "";
}

function normalizedRelayCandidateType(value) {
  const normalized = String(value ?? "").trim().toLowerCase();
  return normalized === "relay" ? normalized : "";
}

function finitePositiveNumber(value) {
  return Number.isFinite(Number(value)) && Number(value) > 0;
}

function safeIceReadiness(summary) {
  return summary && typeof summary === "object" && !Array.isArray(summary)
    ? {
        ok: summary.ok === true,
        hasIceServers: summary.hasIceServers === true,
        iceServerCount: Number(summary.iceServerCount ?? 0),
        knownUrlCount: Number(summary.knownUrlCount ?? 0),
        unknownUrlCount: Number(summary.unknownUrlCount ?? 0),
        stunCount: Number(summary.stunCount ?? 0),
        stunsCount: Number(summary.stunsCount ?? 0),
        turnCount: Number(summary.turnCount ?? 0),
        turnsCount: Number(summary.turnsCount ?? 0),
        turnServersWithCredentials: Number(summary.turnServersWithCredentials ?? 0),
        turnServersMissingCredentials: Number(summary.turnServersMissingCredentials ?? 0),
        relayTransports: Array.isArray(summary.relayTransports)
          ? summary.relayTransports.map((item) => String(item).trim().toLowerCase()).filter(Boolean)
          : [],
        warnings: Array.isArray(summary.warnings) ? summary.warnings.map((item) => String(item)) : [],
        errors: Array.isArray(summary.errors) ? summary.errors.map((item) => String(item)) : [],
      }
    : null;
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
    const isSafeCountField = /^(turnServersWithCredentials|turnServersMissingCredentials)$/i.test(key);
    if (
      (!isSafeCountField &&
        /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|turn_?(user|pass|credential|url)|rawSdp|rawIceCandidates|candidateIds?|localCandidateId|remoteCandidateId|headers?|cookies?)/i.test(
          key
        )) ||
      /^(usernames?|urls?|uris?|ipAddress|localAddress|remoteAddress)$/i.test(key)
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function artifactRefToPath(ref) {
  if (typeof ref !== "string" || !ref.startsWith("artifacts/") || ref.split("/").includes("..")) {
    throw new Error(`restrictiveNetworkTurn artifactRef must be a repo-local artifacts/ path: ${ref}`);
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

function selectedCandidate(observation) {
  return observation?.selectedCandidate && typeof observation.selectedCandidate === "object" && !Array.isArray(observation.selectedCandidate)
    ? observation.selectedCandidate
    : {};
}

function resolvedNetwork(observation, args) {
  const inputNetwork = String(observation?.network ?? "").trim();
  const cliNetwork = String(args.network ?? "").trim();
  return cliNetwork || inputNetwork;
}

function validateObservation(observation, draft, args) {
  const problems = [];
  if (!observation || typeof observation !== "object" || Array.isArray(observation)) {
    return ["input:not_object"];
  }
  if (observation.schemaVersion !== 1) {
    problems.push("schemaVersion");
  }
  if (observation.artifactType !== "native_turn_relay_observation") {
    problems.push("artifactType");
  }
  if (observation.status !== "observed") {
    problems.push("status");
  }
  if (!validTimestamp(observation.capturedAt)) {
    problems.push("capturedAt");
  }
  if (!args.confirmRestrictiveNetwork) {
    problems.push("confirmRestrictiveNetwork");
  }
  if (!args.confirmSameRoom) {
    problems.push("confirmSameRoom");
  }
  if (!nonPlaceholderString(args.network)) {
    problems.push("network");
  }
  for (const [key, expected] of [
    ["runId", draft.runId],
    ["roomId", draft.roomId],
  ]) {
    const value = String(observation[key] ?? "").trim();
    if (!value) {
      problems.push(`${key}:empty`);
    } else if (value !== expected) {
      problems.push(key);
    }
  }

  const network = resolvedNetwork(observation, args);
  if (!nonPlaceholderString(network)) {
    problems.push("network");
  }
  if (args.network && observation.network && String(args.network).trim() !== String(observation.network).trim()) {
    problems.push("network.match");
  }

  const candidate = selectedCandidate(observation);
  if (!observation.selectedCandidate || Object.keys(candidate).length === 0) {
    problems.push("selectedCandidate");
  }
  if (!normalizedRelayProtocol(candidate.relayProtocol)) {
    problems.push("selectedCandidate.relayProtocol");
  }
  if (!normalizedRelayCandidateType(candidate.relayCandidateType)) {
    problems.push("selectedCandidate.relayCandidateType");
  }
  if (candidate.relayCandidateSelected !== true) {
    problems.push("selectedCandidate.relayCandidateSelected");
  }
  const localCandidateType = String(candidate.localCandidateType ?? "").trim().toLowerCase();
  const remoteCandidateType = String(candidate.remoteCandidateType ?? "").trim().toLowerCase();
  if (localCandidateType !== "relay" && remoteCandidateType !== "relay") {
    problems.push("selectedCandidate.localOrRemoteRelay");
  }
  if (!finitePositiveNumber(candidate.currentRoundTripTime)) {
    problems.push("selectedCandidate.currentRoundTripTime");
  }

  if (!observation.app || typeof observation.app !== "object" || Array.isArray(observation.app)) {
    problems.push("app");
  } else {
    if (String(observation.app.version ?? "").trim() !== draft.version) {
      problems.push("app.version");
    }
    if (String(observation.app.build ?? "").trim() !== draft.build) {
      problems.push("app.build");
    }
    if (!allowedAppTargets.has(String(observation.app.target ?? "").trim())) {
      problems.push("app.target");
    }
    if (!allowedClientPlatforms.has(String(observation.app.clientPlatform ?? "").trim())) {
      problems.push("app.clientPlatform");
    }
  }
  if (!observation.device || typeof observation.device !== "object" || Array.isArray(observation.device)) {
    problems.push("device");
  } else {
    if (!allowedDeviceKinds.has(String(observation.device.kind ?? "").trim())) {
      problems.push("device.kind");
    }
    if (observation.device.physical !== true) {
      problems.push("device.physical");
    }
    if (!nonPlaceholderString(observation.device.model)) {
      problems.push("device.model");
    }
    if (!nonPlaceholderString(observation.device.os)) {
      problems.push("device.os");
    }
  }

  const iceReadiness = safeIceReadiness(observation.iceReadiness);
  if (!iceReadiness) {
    problems.push("iceReadiness");
  } else {
    if (iceReadiness.ok !== true) {
      problems.push("iceReadiness.ok");
    }
    if (iceReadiness.hasIceServers !== true) {
      problems.push("iceReadiness.hasIceServers");
    }
    if (!(iceReadiness.turnCount + iceReadiness.turnsCount > 0)) {
      problems.push("iceReadiness.turnRelayCount");
    }
    if (!(iceReadiness.turnServersWithCredentials > 0)) {
      problems.push("iceReadiness.turnServersWithCredentials");
    }
    if (iceReadiness.turnServersMissingCredentials !== 0) {
      problems.push("iceReadiness.turnServersMissingCredentials");
    }
    if (iceReadiness.errors.length > 0) {
      problems.push("iceReadiness.errors");
    }
    if (iceReadiness.warnings.length > 0) {
      problems.push("iceReadiness.warnings");
    }
  }

  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    problems.push(`unsafeContent:${unsafe.slice(0, 4).join("|")}`);
  }
  return problems;
}

function promotedArtifact(observation, draft, args, promotedAt, inputPath) {
  const item = draft.restrictiveNetworkTurn;
  const candidate = selectedCandidate(observation);
  const network = resolvedNetwork(observation, args).trim();
  const relayProtocol = normalizedRelayProtocol(candidate.relayProtocol);
  const relayCandidateType = normalizedRelayCandidateType(candidate.relayCandidateType);
  const selected = {
    relayProtocol,
    relayCandidateType,
    relayCandidateSelected: true,
    localCandidateType: String(candidate.localCandidateType ?? "").trim().toLowerCase(),
    remoteCandidateType: String(candidate.remoteCandidateType ?? "").trim().toLowerCase(),
    currentRoundTripTime: Number(candidate.currentRoundTripTime),
  };
  for (const key of ["protocol", "networkType"]) {
    if (nonPlaceholderString(candidate[key])) {
      selected[key] = String(candidate[key]).trim().toLowerCase();
    }
  }
  return {
    schemaVersion: 1,
    artifactType: "native_restrictive_turn",
    claimScope: "restrictive_network_turn",
    releaseEligible: true,
    status: "passed",
    runId: draft.runId,
    roomId: draft.roomId,
    network,
    capturedAt: observation.capturedAt,
    app: {
      version: String(observation.app.version ?? "").trim(),
      build: String(observation.app.build ?? "").trim(),
      target: String(observation.app.target ?? "").trim(),
      clientPlatform: String(observation.app.clientPlatform ?? "").trim(),
    },
    device: {
      kind: String(observation.device.kind ?? "").trim(),
      model: String(observation.device.model ?? "").trim(),
      os: String(observation.device.os ?? "").trim(),
      physical: true,
    },
    selectedCandidate: selected,
    iceReadiness: safeIceReadiness(observation.iceReadiness),
    releaseEvidenceSummary: {
      status: "passed",
      runId: draft.runId,
      roomId: draft.roomId,
      network,
      testedAt: observation.capturedAt,
      relayProtocol,
      relayCandidateType,
    },
    promotion: {
      promotedAt,
      sourceArtifactType: observation.artifactType,
      sourceStatus: observation.status,
      sourceRunId: String(observation.runId ?? "").trim(),
      sourceRoomId: String(observation.roomId ?? "").trim(),
      sourceCapturedAt: observation.capturedAt,
      sourceArtifact: repoSafeSourceLabel(inputPath),
      operatorConfirmedRestrictiveNetwork: true,
      operatorConfirmedSameRoom: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
}

function promote(args) {
  if (!args.proofpackDir) {
    throw new Error("--proofpack-dir is required.");
  }
  if (!args.input) {
    throw new Error("--input is required.");
  }
  if (!args.network) {
    throw new Error("--network is required.");
  }
  const proofpackDir = resolve(rootDir, args.proofpackDir);
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  const proofpackPath = join(proofpackDir, "proofpack.json");
  const inputPath = resolve(args.input);
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack evidence draft: ${draftPath}`);
  }
  if (!existsSync(proofpackPath)) {
    throw new Error(`Missing proof-pack manifest: ${proofpackPath}`);
  }
  if (!existsSync(inputPath)) {
    throw new Error(`Missing input TURN observation: ${inputPath}`);
  }

  const draft = readJSON(draftPath);
  const proofpack = readJSON(proofpackPath);
  const item = draft.restrictiveNetworkTurn;
  if (!item || typeof item !== "object") {
    throw new Error("ReleaseEvidence.draft.json is missing restrictiveNetworkTurn.");
  }
  for (const key of ["version", "build", "runId", "roomId"]) {
    if (String(proofpack[key] ?? "") !== String(draft[key] ?? "")) {
      throw new Error(`proofpack.json ${key} does not match ReleaseEvidence.draft.json.`);
    }
  }
  if (proofpack.evidenceArtifacts?.turn !== item.artifactRef) {
    throw new Error("proofpack.json evidenceArtifacts.turn does not match ReleaseEvidence.draft.json.");
  }
  if (item.status === "passed" && !args.force) {
    throw new Error("Restrictive-network TURN evidence is already passed. Use --force to replace it.");
  }

  const observation = readJSON(inputPath);
  const problems = validateObservation(observation, draft, args);
  if (problems.length > 0) {
    throw new Error(`Input TURN observation cannot be promoted. Invalid: ${[...new Set(problems)].slice(0, 10).join(", ")}`);
  }

  const artifactPath = artifactRefToPath(item.artifactRef);
  const relativeArtifactDir = relative(proofpackDir, artifactPath);
  if (relativeArtifactDir.startsWith("..")) {
    throw new Error(`Target TURN artifact must stay inside the proof pack: ${item.artifactRef}`);
  }
  if (!args.force && !replaceableArtifact(artifactPath)) {
    throw new Error("Restrictive-network TURN artifact already contains non-pending evidence. Use --force to replace it.");
  }
  const promotedAt = args.promotedAt || new Date().toISOString();
  if (!validTimestamp(promotedAt)) {
    throw new Error("--promoted-at must be an ISO-like timestamp.");
  }

  const artifact = promotedArtifact(observation, draft, args, promotedAt, inputPath);
  writeJSON(artifactPath, artifact);

  draft.restrictiveNetworkTurn = {
    ...item,
    status: "passed",
    runId: draft.runId,
    roomId: draft.roomId,
    network: artifact.network,
    relayProtocol: artifact.selectedCandidate.relayProtocol,
    relayCandidateType: artifact.selectedCandidate.relayCandidateType,
    testedAt: artifact.capturedAt,
    artifactRef: item.artifactRef,
  };
  writeJSON(draftPath, draft);

  return {
    ok: true,
    proofpackDir,
    proofpackPath,
    evidenceDraft: draftPath,
    artifactPath,
    artifactRef: item.artifactRef,
    version: draft.version,
    build: draft.build,
    runId: draft.runId,
    roomId: draft.roomId,
    network: artifact.network,
    relayProtocol: artifact.selectedCandidate.relayProtocol,
    relayCandidateType: artifact.selectedCandidate.relayCandidateType,
    promotedAt,
    nextSteps: proofpack.nextSteps ?? [
      "Complete remaining device, room, App Store review metadata, TestFlight, and notarization evidence.",
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
