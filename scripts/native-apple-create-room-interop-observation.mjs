#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const allowedClientPlatforms = new Set(["browser", "ios", "ipados", "macos"]);
const nativeClientPlatforms = new Set(["ios", "ipados", "macos"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-create-room-interop-observation.mjs --proofpack-dir path",
    "    --participant-count count --client-platforms browser,ios,ipados,macos",
    "    --confirm-browser-native-mixed-room --confirm-three-plus-participants",
    "    --confirm-remote-audio-audible --confirm-remote-video-rendered",
    "    --confirm-no-missing-remote-health --confirm-no-duplicate-participants",
    "    --confirm-no-stalled-remote-media --confirm-clean-leave",
    "    --confirm-recording-off --confirm-current-build --confirm-no-secrets",
    "    [--tested-at iso] [--force]",
    "",
    "Creates a sanitized room-interop-observation.json in a proof-pack inbox.",
    "It does not join a room, run a smoke test, mutate release evidence, or",
    "prove browser/native media by itself. Promote the created observation separately.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    participantCount: "",
    clientPlatforms: "",
    testedAt: "",
    confirmBrowserNativeMixedRoom: false,
    confirmThreePlusParticipants: false,
    confirmRemoteAudioAudible: false,
    confirmRemoteVideoRendered: false,
    confirmNoMissingRemoteHealth: false,
    confirmNoDuplicateParticipants: false,
    confirmNoStalledRemoteMedia: false,
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
    } else if (arg === "--participant-count") {
      args.participantCount = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--client-platforms") {
      args.clientPlatforms = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--tested-at") {
      args.testedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-browser-native-mixed-room") {
      args.confirmBrowserNativeMixedRoom = true;
    } else if (arg === "--confirm-three-plus-participants") {
      args.confirmThreePlusParticipants = true;
    } else if (arg === "--confirm-remote-audio-audible") {
      args.confirmRemoteAudioAudible = true;
    } else if (arg === "--confirm-remote-video-rendered") {
      args.confirmRemoteVideoRendered = true;
    } else if (arg === "--confirm-no-missing-remote-health") {
      args.confirmNoMissingRemoteHealth = true;
    } else if (arg === "--confirm-no-duplicate-participants") {
      args.confirmNoDuplicateParticipants = true;
    } else if (arg === "--confirm-no-stalled-remote-media") {
      args.confirmNoStalledRemoteMedia = true;
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

function writeJSONAtomic(path, value, { force = false } = {}) {
  if (existsSync(path) && !force) {
    throw new Error(`Refusing to overwrite existing room interop observation: ${path}. Use --force to replace it.`);
  }
  mkdirSync(dirname(path), { recursive: true });
  const tempPath = `${path}.tmp-${process.pid}-${Date.now()}`;
  writeFileSync(tempPath, `${JSON.stringify(value, null, 2)}\n`);
  renameSync(tempPath, path);
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

function validTimestamp(value) {
  return nonPlaceholderString(value) && /^\d{4}-\d{2}-\d{2}T/.test(value) && !Number.isNaN(Date.parse(value));
}

function cleanBuildValue(value) {
  return String(value ?? "").trim().replace(/^["']|["']$/g, "").replace(/;$/, "").trim();
}

function targetBlockForTarget(projectText, targetName) {
  const start = projectText.indexOf(`  ${targetName}:`);
  if (start === -1) {
    return "";
  }
  const targetBlock = projectText.slice(start + targetName.length + 4);
  const nextTarget = targetBlock.search(/\n  [A-Za-z0-9_]+:/);
  return nextTarget >= 0 ? targetBlock.slice(0, nextTarget) : targetBlock;
}

function readTargetMetadata(appleDir, targetName) {
  const projectPath = join(appleDir, "project.yml");
  if (!existsSync(projectPath)) {
    return { error: `missing:${repoSafe(projectPath)}` };
  }
  const projectText = readFileSync(projectPath, "utf8");
  const targetBlock = targetBlockForTarget(projectText, targetName);
  const marketing = /MARKETING_VERSION:\s*([^\n#]+)/.exec(targetBlock)?.[1];
  const build = /CURRENT_PROJECT_VERSION:\s*([^\n#]+)/.exec(targetBlock)?.[1];
  if (!marketing || !build) {
    return { error: `version_build:${targetName}` };
  }
  return {
    target: targetName,
    version: cleanBuildValue(marketing),
    build: cleanBuildValue(build),
  };
}

function readProjectMetadata(appleDir) {
  const ios = readTargetMetadata(appleDir, "MeetingAssistAppleApp");
  const mac = readTargetMetadata(appleDir, "MeetingAssistMacApp");
  if (ios.error || mac.error) {
    return { error: ios.error || mac.error };
  }
  return { ios, mac };
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
        /\b(?=[A-Z0-9]*[A-Z])[A-Z0-9]{10}\b/.test(trimmed) ||
        /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(trimmed) ||
        /\/Users\/[^/\s]+/i.test(trimmed) ||
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

function normalizedPlatforms(value) {
  if (Array.isArray(value)) {
    return [...new Set(value.map((item) => String(item ?? "").trim().toLowerCase()).filter(Boolean))];
  }
  return [
    ...new Set(
      String(value ?? "")
        .split(",")
        .map((item) => item.trim().toLowerCase())
        .filter(Boolean)
    ),
  ];
}

function validPlatforms(platforms) {
  return platforms.length >= 2 && platforms.every((platform) => allowedClientPlatforms.has(platform));
}

function hasBrowserAndNative(platforms) {
  return platforms.includes("browser") && platforms.some((platform) => nativeClientPlatforms.has(platform));
}

function cleanParticipantCount(value) {
  const count = Number(value);
  return Number.isInteger(count) ? count : NaN;
}

function artifactRef(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    throw new Error(`Room interop observation must stay under the repository: ${path}`);
  }
  return relativePath.split(/[/\\]/).join("/");
}

function repoSafe(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    return basename(path);
  }
  return relativePath.split(/[/\\]/).join("/");
}

function validateProofpack({ proofpack, draft, template, currentProject, args }) {
  const problems = [];
  for (const key of ["version", "build", "runId", "roomId"]) {
    if (String(proofpack[key] ?? "") !== String(draft[key] ?? "")) {
      problems.push(`${key}.match`);
    }
  }
  if (!currentProject || currentProject.error) {
    problems.push("currentProject");
  } else {
    for (const [label, target] of [
      ["ios", currentProject.ios],
      ["mac", currentProject.mac],
    ]) {
      if (String(target.version ?? "") !== String(draft.version ?? "")) {
        problems.push(`currentProject.${label}.version`);
      }
      if (String(target.build ?? "") !== String(draft.build ?? "")) {
        problems.push(`currentProject.${label}.build`);
      }
    }
  }
  if (!proofpack.observationTemplates?.roomInterop) {
    problems.push("proofpack.observationTemplates.roomInterop");
  }
  if (!proofpack.evidenceArtifacts?.roomInterop) {
    problems.push("proofpack.evidenceArtifacts.roomInterop");
  }
  if (!draft.roomInterop || typeof draft.roomInterop !== "object" || Array.isArray(draft.roomInterop)) {
    problems.push("draft.roomInterop");
  } else if (proofpack.evidenceArtifacts?.roomInterop !== draft.roomInterop.artifactRef) {
    problems.push("draft.roomInterop.artifactRef");
  }
  if (!template || typeof template !== "object" || Array.isArray(template)) {
    problems.push("template");
  } else {
    problems.push(
      ...collectUnexpectedKeys(template, ["schemaVersion", "artifactType", "status", "runId", "roomId", "testedAt", "app", "room", "media", "lifecycle", "recording"], "template"),
      ...collectUnexpectedKeys(template.app, ["version", "build"], "template.app"),
      ...collectUnexpectedKeys(template.room, ["participantCount", "clientPlatforms", "browserNativeMixed", "threePlusParticipants"], "template.room"),
      ...collectUnexpectedKeys(
        template.media,
        ["remoteAudioAudible", "remoteVideoRendered", "noMissingRemoteHealth", "noDuplicateParticipants", "noStalledRemoteMedia"],
        "template.media"
      ),
      ...collectUnexpectedKeys(template.lifecycle, ["cleanLeaveParticipantsEmpty", "participantsAfterLeave"], "template.lifecycle"),
      ...collectUnexpectedKeys(
        template.recording,
        ["recordingOffStopsForwarding", "recordingOffTranscriptForwarded", "recordingOffRealtimeForwarded"],
        "template.recording"
      )
    );
    if (template.schemaVersion !== 1) {
      problems.push("template.schemaVersion");
    }
    if (template.artifactType !== "native_room_interop_observation") {
      problems.push("template.artifactType");
    }
    if (template.status !== "template") {
      problems.push("template.status");
    }
    if (String(template.runId ?? "") !== String(draft.runId ?? "")) {
      problems.push("template.runId");
    }
    if (String(template.roomId ?? "") !== String(draft.roomId ?? "")) {
      problems.push("template.roomId");
    }
    if (String(template.app?.version ?? "") !== String(draft.version ?? "")) {
      problems.push("template.app.version");
    }
    if (String(template.app?.build ?? "") !== String(draft.build ?? "")) {
      problems.push("template.app.build");
    }
  }
  const participantCount = cleanParticipantCount(args.participantCount);
  const platforms = normalizedPlatforms(args.clientPlatforms);
  if (!(participantCount >= 3)) {
    problems.push("participantCount");
  }
  if (!validPlatforms(platforms) || !hasBrowserAndNative(platforms)) {
    problems.push("clientPlatforms");
  }
  for (const key of [
    "confirmBrowserNativeMixedRoom",
    "confirmThreePlusParticipants",
    "confirmRemoteAudioAudible",
    "confirmRemoteVideoRendered",
    "confirmNoMissingRemoteHealth",
    "confirmNoDuplicateParticipants",
    "confirmNoStalledRemoteMedia",
    "confirmCleanLeave",
    "confirmRecordingOff",
    "confirmCurrentBuild",
    "confirmNoSecrets",
  ]) {
    if (args[key] !== true) {
      problems.push(key);
    }
  }
  if (args.testedAt && !validTimestamp(args.testedAt)) {
    problems.push("testedAt");
  }
  return [...new Set(problems)];
}

function createObservation(args) {
  if (!args.proofpackDir) {
    throw new Error("--proofpack-dir is required.");
  }
  const proofpackDir = resolve(rootDir, args.proofpackDir);
  const proofpackPath = join(proofpackDir, "proofpack.json");
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  if (!existsSync(proofpackPath)) {
    throw new Error(`Missing proof-pack manifest: ${proofpackPath}`);
  }
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack draft: ${draftPath}`);
  }
  const proofpack = readJSON(proofpackPath);
  const draft = readJSON(draftPath);
  const templateRef = proofpack.observationTemplates?.roomInterop ?? "";
  const templatePath = templateRef ? resolve(rootDir, templateRef) : join(proofpackDir, "inbox", "room-interop-observation.template.json");
  if (!existsSync(templatePath)) {
    throw new Error(`Missing room interop observation template: ${templatePath}`);
  }
  const template = readJSON(templatePath);
  const appleDir = resolve(rootDir, proofpack.appleDir || "apple");
  const currentProject = readProjectMetadata(appleDir);
  const problems = validateProofpack({ proofpack, draft, template, currentProject, args });
  if (problems.length > 0) {
    throw new Error(`Cannot create room interop observation. Invalid: ${problems.slice(0, 14).join(", ")}`);
  }

  const testedAt = args.testedAt || new Date().toISOString();
  if (!validTimestamp(testedAt)) {
    throw new Error("--tested-at must be an ISO-like timestamp.");
  }
  const observation = {
    schemaVersion: 1,
    artifactType: "native_room_interop_observation",
    status: "observed",
    runId: draft.runId,
    roomId: draft.roomId,
    testedAt,
    app: {
      version: draft.version,
      build: draft.build,
    },
    room: {
      participantCount: cleanParticipantCount(args.participantCount),
      clientPlatforms: normalizedPlatforms(args.clientPlatforms),
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
  };
  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    throw new Error(`Room interop observation contains unsafe fields or values: ${unsafe.slice(0, 6).join(", ")}`);
  }

  const outputPath = join(proofpackDir, "inbox", "room-interop-observation.json");
  writeJSONAtomic(outputPath, observation, { force: args.force });
  return {
    ok: true,
    proofpackDir,
    proofpackPath,
    evidenceDraft: draftPath,
    templatePath,
    outputPath,
    outputRef: artifactRef(outputPath),
    version: draft.version,
    build: draft.build,
    runId: draft.runId,
    roomId: draft.roomId,
    testedAt,
    participantCount: observation.room.participantCount,
    clientPlatforms: observation.room.clientPlatforms,
    nextSteps: [
      `Promote ${repoSafe(outputPath)} with scripts/native-apple-promote-room-gate-evidence.mjs.`,
      "Run node scripts/native-apple-release-readiness.mjs --strict --evidence-file <proofpack>/ReleaseEvidence.draft.json after all proof-pack evidence is promoted.",
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
    console.log(JSON.stringify(createObservation(args), null, 2));
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
    process.exitCode = 1;
  }
}

main();
