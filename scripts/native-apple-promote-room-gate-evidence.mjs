#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const allowedClientPlatforms = new Set(["browser", "ios", "ipados", "macos"]);
const nativePlatforms = new Set(["ios", "ipados", "macos"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-promote-room-gate-evidence.mjs --proofpack-dir path",
    "    --input path --confirm-browser-native-mixed-room",
    "    --confirm-three-plus-participants --confirm-clean-leave",
    "    --confirm-recording-off --confirm-current-build --confirm-no-secrets",
    "    [--promoted-at iso] [--force]",
    "",
    "Promotes one sanitized native_room_interop_observation into the proof-pack",
    "roomInterop artifact and updates ReleaseEvidence.draft.json. The command",
    "rejects raw logs, SDP, ICE candidates, TURN credentials, account data, and",
    "operator-confirmed observations that do not satisfy the browser/native",
    "3+ participant release gate.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    input: "",
    promotedAt: "",
    confirmBrowserNativeMixedRoom: false,
    confirmThreePlusParticipants: false,
    confirmCleanLeave: false,
    confirmRecordingOff: false,
    confirmCurrentBuild: false,
    confirmNoSecrets: false,
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
    } else if (arg === "--promoted-at") {
      args.promotedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-browser-native-mixed-room") {
      args.confirmBrowserNativeMixedRoom = true;
    } else if (arg === "--confirm-three-plus-participants") {
      args.confirmThreePlusParticipants = true;
    } else if (arg === "--confirm-clean-leave") {
      args.confirmCleanLeave = true;
    } else if (arg === "--confirm-recording-off") {
      args.confirmRecordingOff = true;
    } else if (arg === "--confirm-current-build") {
      args.confirmCurrentBuild = true;
    } else if (arg === "--confirm-no-secrets") {
      args.confirmNoSecrets = true;
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
  return !["TODO", "TBD", "FIXME", "CHANGE_ME", "PLACEHOLDER", "EXAMPLE", "SAMPLE", "DUMMY", "UNKNOWN", "N_A", "NA", "NONE"].includes(
    normalized
  );
}

function collectUnexpectedKeys(value, allowedKeys, path) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return [];
  }
  const allowed = new Set(allowedKeys);
  return Object.keys(value)
    .filter((key) => !allowed.has(key))
    .map((key) => `${path}.${key}`);
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
        /\bcandidate:/.test(trimmed) ||
        /\ba=candidate/.test(trimmed) ||
        /^v=0(?:\r?\n|$)/.test(trimmed) ||
        /\bturns?:[^,\s]+/i.test(trimmed) ||
        /\b(?:\d{1,3}\.){3}\d{1,3}\b/.test(trimmed) ||
        /\b[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){2,}\b/i.test(trimmed) ||
        /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\b[A-Z0-9]{10}\b/.test(trimmed) ||
        /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(trimmed) ||
        /-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(trimmed) ||
        /-----BEGIN CERTIFICATE-----/.test(trimmed) ||
        /\.(p8|p12|mobileprovision|provisionprofile)\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    if (
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|issuer_?id|key_?id|keychain|profile|authorization|jwt|apple_?id|username|raw|logs?|stdout|stderr|command|args|env|sdp|ice|candidate|headers?|cookies?|emails?|screenshots?|pixels?|frames?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function artifactRefToPath(ref) {
  if (typeof ref !== "string" || !ref.startsWith("artifacts/") || ref.split("/").includes("..")) {
    throw new Error(`roomInterop artifactRef must be a repo-local artifacts/ path: ${ref}`);
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

function normalizedPlatforms(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  return [...new Set(value.map((item) => String(item ?? "").trim().toLowerCase()).filter(Boolean))];
}

function validateObservation(observation, draft, args) {
  const problems = [];
  if (!observation || typeof observation !== "object" || Array.isArray(observation)) {
    return ["input:not_object"];
  }
  problems.push(
    ...collectUnexpectedKeys(
      observation,
      ["schemaVersion", "artifactType", "status", "runId", "roomId", "testedAt", "app", "room", "media", "lifecycle", "recording"],
      "input"
    ),
    ...collectUnexpectedKeys(observation?.app, ["version", "build"], "input.app"),
    ...collectUnexpectedKeys(observation?.room, ["participantCount", "clientPlatforms", "browserNativeMixed", "threePlusParticipants"], "input.room"),
    ...collectUnexpectedKeys(
      observation?.media,
      ["remoteAudioAudible", "remoteVideoRendered", "noMissingRemoteHealth", "noDuplicateParticipants", "noStalledRemoteMedia"],
      "input.media"
    ),
    ...collectUnexpectedKeys(observation?.lifecycle, ["cleanLeaveParticipantsEmpty", "participantsAfterLeave"], "input.lifecycle"),
    ...collectUnexpectedKeys(
      observation?.recording,
      ["recordingOffStopsForwarding", "recordingOffTranscriptForwarded", "recordingOffRealtimeForwarded"],
      "input.recording"
    )
  );
  if (observation.schemaVersion !== 1) {
    problems.push("schemaVersion");
  }
  if (observation.artifactType !== "native_room_interop_observation") {
    problems.push("artifactType");
  }
  if (observation.status !== "observed") {
    problems.push("status");
  }
  if (!validTimestamp(observation.testedAt)) {
    problems.push("testedAt");
  }
  if (String(observation.runId ?? "").trim() !== draft.runId) {
    problems.push("runId");
  }
  if (String(observation.roomId ?? "").trim() !== draft.roomId) {
    problems.push("roomId");
  }
  if (!args.confirmBrowserNativeMixedRoom) {
    problems.push("confirmBrowserNativeMixedRoom");
  }
  if (!args.confirmThreePlusParticipants) {
    problems.push("confirmThreePlusParticipants");
  }
  if (!args.confirmCleanLeave) {
    problems.push("confirmCleanLeave");
  }
  if (!args.confirmRecordingOff) {
    problems.push("confirmRecordingOff");
  }
  if (!args.confirmCurrentBuild) {
    problems.push("confirmCurrentBuild");
  }
  if (!args.confirmNoSecrets) {
    problems.push("confirmNoSecrets");
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
  }
  const room = observation.room;
  const platforms = normalizedPlatforms(room?.clientPlatforms);
  if (!room || typeof room !== "object" || Array.isArray(room)) {
    problems.push("room");
  } else {
    if (!(Number(room.participantCount) >= 3)) {
      problems.push("room.participantCount");
    }
    if (room.browserNativeMixed !== true) {
      problems.push("room.browserNativeMixed");
    }
    if (room.threePlusParticipants !== true) {
      problems.push("room.threePlusParticipants");
    }
    if (platforms.some((platform) => !allowedClientPlatforms.has(platform))) {
      problems.push("room.clientPlatforms.allowed");
    }
    if (!platforms.includes("browser")) {
      problems.push("room.clientPlatforms.browser");
    }
    if (!platforms.some((platform) => nativePlatforms.has(platform))) {
      problems.push("room.clientPlatforms.native");
    }
  }
  const media = observation.media;
  if (!media || typeof media !== "object" || Array.isArray(media)) {
    problems.push("media");
  } else {
    for (const key of ["remoteAudioAudible", "remoteVideoRendered", "noMissingRemoteHealth", "noDuplicateParticipants", "noStalledRemoteMedia"]) {
      if (media[key] !== true) {
        problems.push(`media.${key}`);
      }
    }
  }
  const lifecycle = observation.lifecycle;
  if (!lifecycle || typeof lifecycle !== "object" || Array.isArray(lifecycle)) {
    problems.push("lifecycle");
  } else {
    if (lifecycle.cleanLeaveParticipantsEmpty !== true) {
      problems.push("lifecycle.cleanLeaveParticipantsEmpty");
    }
    if (Number(lifecycle.participantsAfterLeave) !== 0) {
      problems.push("lifecycle.participantsAfterLeave");
    }
  }
  const recording = observation.recording;
  if (!recording || typeof recording !== "object" || Array.isArray(recording)) {
    problems.push("recording");
  } else {
    if (recording.recordingOffStopsForwarding !== true) {
      problems.push("recording.recordingOffStopsForwarding");
    }
    if (recording.recordingOffTranscriptForwarded !== false) {
      problems.push("recording.recordingOffTranscriptForwarded");
    }
    if (recording.recordingOffRealtimeForwarded !== false) {
      problems.push("recording.recordingOffRealtimeForwarded");
    }
  }
  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    problems.push(`unsafeContent:${unsafe.slice(0, 4).join("|")}`);
  }
  return problems;
}

function promotedArtifact(observation, draft, item, promotedAt, inputPath) {
  const platforms = normalizedPlatforms(observation.room.clientPlatforms);
  return {
    schemaVersion: 1,
    artifactType: "native_room_interop",
    claimScope: "browser_native_room_gate",
    releaseEligible: true,
    status: "passed",
    runId: draft.runId,
    roomId: draft.roomId,
    testedAt: observation.testedAt,
    app: {
      version: String(observation.app.version ?? "").trim(),
      build: String(observation.app.build ?? "").trim(),
    },
    room: {
      participantCount: Number(observation.room.participantCount),
      clientPlatforms: platforms,
      browserNativeMixed: true,
      threePlusParticipants: true,
    },
    media: {
      remoteAudioAudible: true,
      remoteVideoRendered: true,
      noMissingRemoteHealth: true,
      noDuplicateParticipants: true,
      noStalledRemoteMedia: true,
    },
    lifecycle: {
      cleanLeaveParticipantsEmpty: true,
      participantsAfterLeave: 0,
    },
    recording: {
      recordingOffStopsForwarding: true,
      recordingOffTranscriptForwarded: false,
      recordingOffRealtimeForwarded: false,
    },
    releaseEvidenceSummary: {
      status: "passed",
      runId: draft.runId,
      roomId: draft.roomId,
      version: draft.version,
      build: draft.build,
      testedAt: observation.testedAt,
      participantCount: Number(observation.room.participantCount),
      clientPlatforms: platforms,
      browserNativeMixed: true,
      threePlusParticipants: true,
      cleanLeaveParticipantsEmpty: true,
      recordingOffStopsForwarding: true,
    },
    promotion: {
      promotedAt,
      sourceArtifactType: observation.artifactType,
      sourceStatus: observation.status,
      sourceRunId: String(observation.runId ?? "").trim(),
      sourceRoomId: String(observation.roomId ?? "").trim(),
      sourceTestedAt: observation.testedAt,
      sourceArtifact: repoSafeSourceLabel(inputPath),
      operatorConfirmedBrowserNativeMixedRoom: true,
      operatorConfirmedThreePlusParticipants: true,
      operatorConfirmedCleanLeave: true,
      operatorConfirmedRecordingOff: true,
      operatorConfirmedCurrentBuild: true,
      operatorConfirmedNoSecrets: true,
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
    throw new Error(`Missing input room interop observation: ${inputPath}`);
  }

  const draft = readJSON(draftPath);
  const proofpack = readJSON(proofpackPath);
  const item = draft.roomInterop;
  if (!item || typeof item !== "object") {
    throw new Error("ReleaseEvidence.draft.json is missing roomInterop.");
  }
  for (const key of ["version", "build", "runId", "roomId"]) {
    if (String(proofpack[key] ?? "") !== String(draft[key] ?? "")) {
      throw new Error(`proofpack.json ${key} does not match ReleaseEvidence.draft.json.`);
    }
  }
  if (proofpack.evidenceArtifacts?.roomInterop !== item.artifactRef) {
    throw new Error("proofpack.json evidenceArtifacts.roomInterop does not match ReleaseEvidence.draft.json.");
  }
  if (item.status === "passed" && !args.force) {
    throw new Error("roomInterop evidence is already passed. Use --force to replace it.");
  }

  const observation = readJSON(inputPath);
  const problems = validateObservation(observation, draft, args);
  if (problems.length > 0) {
    throw new Error(`Input roomInterop observation cannot be promoted. Invalid: ${[...new Set(problems)].slice(0, 12).join(", ")}`);
  }

  const artifactPath = artifactRefToPath(item.artifactRef);
  const relativeArtifactDir = relative(proofpackDir, artifactPath);
  if (relativeArtifactDir.startsWith("..")) {
    throw new Error(`Target roomInterop artifact must stay inside the proof pack: ${item.artifactRef}`);
  }
  if (!args.force && !replaceableArtifact(artifactPath)) {
    throw new Error("roomInterop artifact already contains non-pending evidence. Use --force to replace it.");
  }
  const promotedAt = args.promotedAt || new Date().toISOString();
  if (!validTimestamp(promotedAt)) {
    throw new Error("--promoted-at must be an ISO-like timestamp.");
  }

  const artifact = promotedArtifact(observation, draft, item, promotedAt, inputPath);
  writeJSON(artifactPath, artifact);
  draft.roomInterop = {
    ...item,
    status: "passed",
    runId: draft.runId,
    roomId: draft.roomId,
    testedAt: artifact.testedAt,
    participantCount: artifact.room.participantCount,
    browserNativeMixed: true,
    threePlusParticipants: true,
    remoteAudioAudible: true,
    remoteVideoRendered: true,
    cleanLeaveParticipantsEmpty: true,
    recordingOffStopsForwarding: true,
    artifactRef: item.artifactRef,
  };
  writeJSON(draftPath, draft);

  return {
    ok: true,
    kind: "roomInterop",
    proofpackDir,
    proofpackPath,
    evidenceDraft: draftPath,
    artifactPath,
    artifactRef: item.artifactRef,
    version: draft.version,
    build: draft.build,
    runId: draft.runId,
    roomId: draft.roomId,
    promotedAt,
    nextSteps: proofpack.nextSteps ?? [
      "Complete remaining physical device, TURN, App Store review metadata, TestFlight, and notarization evidence.",
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
    console.log(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
    process.exitCode = 1;
  }
}

main();
