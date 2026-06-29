#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-release-readiness.mjs [--apple-dir apple] [--evidence-file path] [--strict]",
    "",
    "Default mode exits nonzero only for broken repo prerequisites.",
    "--strict also exits nonzero for external distribution blockers such as missing",
    "Apple team configuration, app icons, privacy manifest metadata, physical",
    "device media proof, restrictive-network TURN proof, TestFlight upload, or",
    "macOS notarization evidence.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    evidenceFile: "",
    strict: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = argv[index + 1] ?? "";
      if (!args.appleDir || args.appleDir.startsWith("--")) {
        throw new Error("--apple-dir requires a path.");
      }
      index += 1;
    } else if (arg === "--evidence-file") {
      args.evidenceFile = argv[index + 1] ?? "";
      if (!args.evidenceFile || args.evidenceFile.startsWith("--")) {
        throw new Error("--evidence-file requires a path.");
      }
      index += 1;
    } else if (arg === "--strict") {
      args.strict = true;
    } else if (arg === "--help" || arg === "-h") {
      args.help = true;
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  return args;
}

function readText(path) {
  return readFileSync(path, "utf8");
}

function parsePlist(path) {
  return JSON.parse(execFileSync("plutil", ["-convert", "json", "-o", "-", path], { encoding: "utf8" }));
}

function textHas(text, pattern) {
  return pattern.test(text);
}

function boolPlistValue(plist, key) {
  return plist[key] === true;
}

function nonEmptyPlistString(plist, key) {
  return typeof plist[key] === "string" && plist[key].trim() !== "";
}

function walk(dir, visit) {
  if (!existsSync(dir)) {
    return;
  }

  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      visit(path);
      walk(path, visit);
    }
  }
}

const iosAppIconSlots = [
  ["iphone", "20x20", "2x"],
  ["iphone", "20x20", "3x"],
  ["iphone", "29x29", "2x"],
  ["iphone", "29x29", "3x"],
  ["iphone", "40x40", "2x"],
  ["iphone", "40x40", "3x"],
  ["iphone", "60x60", "2x"],
  ["iphone", "60x60", "3x"],
  ["ipad", "20x20", "1x"],
  ["ipad", "20x20", "2x"],
  ["ipad", "29x29", "1x"],
  ["ipad", "29x29", "2x"],
  ["ipad", "40x40", "1x"],
  ["ipad", "40x40", "2x"],
  ["ipad", "76x76", "1x"],
  ["ipad", "76x76", "2x"],
  ["ipad", "83.5x83.5", "2x"],
  ["ios-marketing", "1024x1024", "1x"],
];

const macAppIconSlots = [
  ["mac", "16x16", "1x"],
  ["mac", "16x16", "2x"],
  ["mac", "32x32", "1x"],
  ["mac", "32x32", "2x"],
  ["mac", "128x128", "1x"],
  ["mac", "128x128", "2x"],
  ["mac", "256x256", "1x"],
  ["mac", "256x256", "2x"],
  ["mac", "512x512", "1x"],
  ["mac", "512x512", "2x"],
];

function appIconSlotKey(idiom, size, scale) {
  return `${idiom}:${size}:${scale}`;
}

function expectedIconPixels(size, scale) {
  const points = Number(size.split("x")[0]);
  const multiplier = Number(scale.replace("x", ""));
  return Math.round(points * multiplier);
}

function pngDimensions(path) {
  const data = readFileSync(path);
  const signature = "89504e470d0a1a0a";
  if (
    data.length < 24 ||
    data.subarray(0, 8).toString("hex") !== signature ||
    data.subarray(12, 16).toString("ascii") !== "IHDR"
  ) {
    return null;
  }
  return {
    width: data.readUInt32BE(16),
    height: data.readUInt32BE(20),
  };
}

function cleanBuildSettingValue(value) {
  return String(value ?? "")
    .trim()
    .replace(/^["']|["']$/g, "")
    .replace(/;$/, "")
    .trim();
}

function expandBuildSettingValue(value, settings, options = {}) {
  const { includeEnv = true } = options;
  return cleanBuildSettingValue(value).replace(/\$\(([^)]+)\)/g, (_match, key) => {
    return cleanBuildSettingValue(settings[key] ?? (includeEnv ? process.env[key] : "") ?? "");
  });
}

function validDevelopmentTeam(value) {
  const normalized = cleanBuildSettingValue(value);
  const placeholders = new Set(["ABCDE12345", "YOURTEAMID", "YOUR_TEAM_ID", "TEAMID1234"]);
  return /^[A-Z0-9]{10}$/.test(normalized) && !placeholders.has(normalized);
}

function nonPlaceholderString(value) {
  if (typeof value !== "string") {
    return false;
  }
  const trimmed = value.trim();
  if (!trimmed) {
    return false;
  }
  if (/^<[^>]+>$/.test(trimmed)) {
    return false;
  }
  if (/^0+$/.test(trimmed)) {
    return false;
  }
  if (/^([A-Za-z0-9])\1{5,}$/.test(trimmed)) {
    return false;
  }
  const normalized = trimmed.toUpperCase().replace(/[\s-]+/g, "_");
  return ![
    "TODO",
    "TBD",
    "FIXME",
    "CHANGE_ME",
    "YOUR_TEAM_ID",
    "YOUR_VALUE",
    "PLACEHOLDER",
    "EXAMPLE",
    "SAMPLE",
    "DUMMY",
    "UNKNOWN",
    "N_A",
    "NA",
    "NONE",
    "NULL",
    "00000000_0000_0000_0000_000000000000",
  ].includes(normalized);
}

function validTimestamp(value) {
  return nonPlaceholderString(value) && /^\d{4}-\d{2}-\d{2}T/.test(value) && !Number.isNaN(Date.parse(value));
}

function strictStringEqual(actual, expected) {
  return String(actual ?? "").trim() === String(expected ?? "").trim();
}

function uniqueLabels(labels) {
  return [...new Set(labels)];
}

function validArtifactRef(value) {
  if (!nonPlaceholderString(value)) {
    return false;
  }
  const trimmed = value.trim();
  return /^(artifacts\/|evidence\/|s3:\/\/|gs:\/\/|https?:\/\/|file:\/)/.test(trimmed);
}

function localArtifactPath(value, evidenceRootDir) {
  if (!nonPlaceholderString(value)) {
    return "";
  }
  const trimmed = value.trim();
  if (/^(artifacts\/|evidence\/)/.test(trimmed)) {
    if (trimmed.split("/").includes("..")) {
      return "__invalid_local_artifact_path__";
    }
    return resolve(evidenceRootDir, trimmed);
  }
  if (trimmed.startsWith("file:/")) {
    try {
      return fileURLToPath(trimmed);
    } catch {
      return "__invalid_file_artifact_url__";
    }
  }
  return "";
}

function collectMissingLocalArtifactRefs(evidence, evidenceRootDir) {
  const refs = [
    ["physicalDeviceMedia.iphone.artifactRef", evidence.physicalDeviceMedia?.iphone?.artifactRef],
    ["physicalDeviceMedia.ipad.artifactRef", evidence.physicalDeviceMedia?.ipad?.artifactRef],
    ["physicalDeviceMedia.mac.artifactRef", evidence.physicalDeviceMedia?.mac?.artifactRef],
    ["restrictiveNetworkTurn.artifactRef", evidence.restrictiveNetworkTurn?.artifactRef],
    ["testFlight.artifactRef", evidence.testFlight?.artifactRef],
    ["macNotarization.artifactRef", evidence.macNotarization?.artifactRef],
  ];
  return refs
    .map(([label, ref]) => {
      const path = localArtifactPath(ref, evidenceRootDir);
      return path && !existsSync(path) ? `${label}:${String(ref ?? "").trim()}` : "";
    })
    .filter(Boolean);
}

const physicalDeviceKinds = {
  iphone: "iphone",
  ipad: "ipad",
  mac: "mac",
};

const physicalDeviceClientPlatforms = {
  iphone: "ios",
  ipad: "ipados",
  mac: "macos",
};

const assertionEvidenceSources = {
  cameraPublished: "outboundVideoFramesSent",
  microphonePublished: "outboundAudioPacketsSent",
  remoteAudioReceived: "inboundAudioPacketsReceived",
  remoteVideoRendered: "remoteVideoTiles+inboundVideoDecoded",
};

function mediaAssertionsAllTrue(assertions) {
  if (!assertions || typeof assertions !== "object" || Array.isArray(assertions)) {
    return false;
  }
  return ["cameraPublished", "microphonePublished", "remoteAudioReceived", "remoteVideoRendered"].every(
    (key) => assertions[key] === true
  );
}

function collectUnsafeMediaArtifactContent(value, path = "$") {
  const problems = [];
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...collectUnsafeMediaArtifactContent(item, `${path}[${index}]`));
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
        /\b[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){2,}\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    if (
      /^(rawSdp|rawIceCandidates|candidateIds?|localCandidateId|remoteCandidateId|turnUsername|turnCredential|cookies?|headers?|apiKeys?|teamIds?|certificates?|provisioningProfiles?)$/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeMediaArtifactContent(item, `${path}.${key}`));
  }
  return problems;
}

function nativeDeviceMediaArtifactProblems({
  platform,
  item,
  artifact,
  expectedVersion,
  expectedBuild,
  runId,
  roomId,
}) {
  const problems = [];
  if (!artifact || typeof artifact !== "object" || Array.isArray(artifact)) {
    return [`${platform}:artifact:not_object`];
  }
  if (artifact.artifactType !== "native_device_media") {
    problems.push(`${platform}:artifactType`);
  }
  if (artifact.schemaVersion !== 1) {
    problems.push(`${platform}:schemaVersion`);
  }
  if (artifact.claimScope !== "physical_device") {
    problems.push(`${platform}:claimScope`);
  }
  if (artifact.releaseEligible !== true) {
    problems.push(`${platform}:releaseEligible`);
  }
  if (artifact.status !== "passed") {
    problems.push(`${platform}:status`);
  }
  if (artifact.platform !== platform) {
    problems.push(`${platform}:platform`);
  }
  if (artifact.lifecycle !== "connected") {
    problems.push(`${platform}:lifecycle`);
  }
  if (!expectedIdentity(artifact.runId, runId)) {
    problems.push(`${platform}:runId`);
  }
  if (!expectedIdentity(artifact.roomId, roomId)) {
    problems.push(`${platform}:roomId`);
  }
  if (!validTimestamp(artifact.capturedAt)) {
    problems.push(`${platform}:capturedAt`);
  } else if (!strictStringEqual(artifact.capturedAt, item.testedAt)) {
    problems.push(`${platform}:capturedAt.match`);
  }
  if (!artifact.app || typeof artifact.app !== "object" || Array.isArray(artifact.app)) {
    problems.push(`${platform}:app`);
  } else {
    if (!expectedIdentity(artifact.app.version, expectedVersion)) {
      problems.push(`${platform}:app.version`);
    }
    if (!expectedIdentity(artifact.app.build, expectedBuild)) {
      problems.push(`${platform}:app.build`);
    }
    if (artifact.app.clientPlatform !== physicalDeviceClientPlatforms[platform]) {
      problems.push(`${platform}:app.clientPlatform`);
    }
  }
  if (!artifact.device || typeof artifact.device !== "object" || Array.isArray(artifact.device)) {
    problems.push(`${platform}:device`);
  } else {
    if (artifact.device.kind !== physicalDeviceKinds[platform]) {
      problems.push(`${platform}:device.kind`);
    }
    if (artifact.device.physical !== true) {
      problems.push(`${platform}:device.physical`);
    }
    if (!nonPlaceholderString(artifact.device.model)) {
      problems.push(`${platform}:device.model`);
    }
    if (!expectedIdentity(artifact.device.os, item.os)) {
      problems.push(`${platform}:device.os`);
    }
  }
  if (!mediaAssertionsAllTrue(artifact.mediaAssertions)) {
    problems.push(`${platform}:mediaAssertions`);
  }
  for (const key of ["cameraPublished", "microphonePublished", "remoteAudioReceived", "remoteVideoRendered"]) {
    if (artifact.mediaAssertions?.[key] !== item.mediaAssertions?.[key]) {
      problems.push(`${platform}:mediaAssertions.${key}.match`);
    }
  }
  if (!artifact.assertionEvidence || typeof artifact.assertionEvidence !== "object") {
    problems.push(`${platform}:assertionEvidence`);
  } else {
    const requiredAssertionEvidence = [
      "cameraPublished",
      "microphonePublished",
      "remoteAudioReceived",
      "remoteVideoRendered",
    ];
    for (const key of requiredAssertionEvidence) {
      const assertion = artifact.assertionEvidence[key];
      if (!assertion || typeof assertion !== "object" || assertion.passed !== true || !(Number(assertion.value) > 0)) {
        problems.push(`${platform}:assertionEvidence.${key}`);
      } else if (assertion.source !== assertionEvidenceSources[key]) {
        problems.push(`${platform}:assertionEvidence.${key}.source`);
      }
    }
  }
  if (!artifact.counters || typeof artifact.counters !== "object") {
    problems.push(`${platform}:counters`);
  } else {
    if (!(Number(artifact.counters.outboundVideoFramesSent) > 0)) {
      problems.push(`${platform}:counters.outboundVideoFramesSent`);
    }
    if (!(Number(artifact.counters.outboundAudioPacketsSent) > 0)) {
      problems.push(`${platform}:counters.outboundAudioPacketsSent`);
    }
    if (!(Number(artifact.counters.inboundAudioPacketsReceived) > 0)) {
      problems.push(`${platform}:counters.inboundAudioPacketsReceived`);
    }
    if (!(Number(artifact.counters.inboundVideoDecoded) > 0)) {
      problems.push(`${platform}:counters.inboundVideoDecoded`);
    }
  }
  if (!(Number(artifact.remoteVideoTiles) > 0)) {
    problems.push(`${platform}:remoteVideoTiles`);
  }
  if (!artifact.releaseEvidenceSummary || typeof artifact.releaseEvidenceSummary !== "object") {
    problems.push(`${platform}:releaseEvidenceSummary`);
  } else {
    if (artifact.releaseEvidenceSummary.status !== "passed") {
      problems.push(`${platform}:releaseEvidenceSummary.status`);
    }
    if (!expectedIdentity(artifact.releaseEvidenceSummary.runId, runId)) {
      problems.push(`${platform}:releaseEvidenceSummary.runId`);
    }
    if (!expectedIdentity(artifact.releaseEvidenceSummary.roomId, roomId)) {
      problems.push(`${platform}:releaseEvidenceSummary.roomId`);
    }
    if (!expectedIdentity(artifact.releaseEvidenceSummary.device, item.device)) {
      problems.push(`${platform}:releaseEvidenceSummary.device`);
    }
    if (!expectedIdentity(artifact.releaseEvidenceSummary.os, item.os)) {
      problems.push(`${platform}:releaseEvidenceSummary.os`);
    }
    if (!validTimestamp(artifact.releaseEvidenceSummary.testedAt)) {
      problems.push(`${platform}:releaseEvidenceSummary.testedAt`);
    } else if (!strictStringEqual(artifact.releaseEvidenceSummary.testedAt, item.testedAt)) {
      problems.push(`${platform}:releaseEvidenceSummary.testedAt.match`);
    }
    if (!mediaAssertionsAllTrue(artifact.releaseEvidenceSummary.mediaAssertions)) {
      problems.push(`${platform}:releaseEvidenceSummary.mediaAssertions`);
    }
  }
  const unsafeContent = [
    ...collectSecretLikeEvidence(artifact, `$.physicalDeviceMedia.${platform}.artifact`),
    ...collectUnsafeMediaArtifactContent(artifact, `$.physicalDeviceMedia.${platform}.artifact`),
  ];
  if (unsafeContent.length > 0) {
    problems.push(`${platform}:unsafeContent:${unsafeContent.slice(0, 3).join("|")}`);
  }
  return problems;
}

function collectPhysicalDeviceArtifactContentProblems(evidence, evidenceRootDir, expectedVersion, expectedBuild) {
  const problems = [];
  for (const platform of ["iphone", "ipad", "mac"]) {
    const item = evidence.physicalDeviceMedia?.[platform];
    const path = localArtifactPath(item?.artifactRef, evidenceRootDir);
    if (!path || path.startsWith("__") || !path.toLowerCase().endsWith(".json") || !existsSync(path)) {
      continue;
    }
    let artifact;
    try {
      artifact = readJSONFile(path);
    } catch {
      problems.push(`${platform}:artifact:not_valid_json`);
      continue;
    }
    problems.push(
      ...nativeDeviceMediaArtifactProblems({
        platform,
        item,
        artifact,
        expectedVersion,
        expectedBuild,
        runId: evidence.runId,
        roomId: evidence.roomId,
      })
    );
  }
  return uniqueLabels(problems);
}

function normalizedTurnRelayProtocol(value) {
  const normalized = String(value ?? "").trim().toLowerCase();
  return ["turn", "turns"].includes(normalized) ? normalized : "";
}

function normalizedTurnRelayCandidateType(value) {
  const normalized = String(value ?? "").trim().toLowerCase();
  return normalized === "relay" ? normalized : "";
}

function finitePositiveNumber(value) {
  return Number.isFinite(Number(value)) && Number(value) > 0;
}

function safeIceReadinessSummary(summary) {
  return summary && typeof summary === "object" && !Array.isArray(summary) ? summary : null;
}

function collectUnsafeTurnArtifactContent(value, path = "$") {
  const problems = [];
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...collectUnsafeTurnArtifactContent(item, `${path}[${index}]`));
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
        /\b[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){2,}\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    if (
      /^(rawSdp|rawIceCandidates|candidateIds?|localCandidateId|remoteCandidateId|turnUsername|turnCredential|turnPassword|turnUrl|turnUri|username|credential|credentials|urls?|uris?|ipAddress|localAddress|remoteAddress|cookies?|headers?|apiKeys?|teamIds?|certificates?|provisioningProfiles?)$/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeTurnArtifactContent(item, `${path}.${key}`));
  }
  return problems;
}

function restrictiveTurnArtifactProblems({ item, artifact, expectedVersion, expectedBuild, runId, roomId }) {
  const problems = [];
  if (!artifact || typeof artifact !== "object" || Array.isArray(artifact)) {
    return ["artifact:not_object"];
  }
  problems.push(...collectUnexpectedKeys(
    artifact,
    [
      "schemaVersion",
      "artifactType",
      "claimScope",
      "releaseEligible",
      "status",
      "runId",
      "roomId",
      "network",
      "capturedAt",
      "app",
      "device",
      "selectedCandidate",
      "iceReadiness",
      "releaseEvidenceSummary",
      "promotion",
    ],
    "artifact"
  ));
  problems.push(...collectUnexpectedKeys(
    artifact.app,
    ["version", "build", "target", "clientPlatform"],
    "artifact.app"
  ));
  problems.push(...collectUnexpectedKeys(
    artifact.device,
    ["kind", "model", "os", "physical"],
    "artifact.device"
  ));
  problems.push(...collectUnexpectedKeys(
    artifact.selectedCandidate,
    [
      "relayProtocol",
      "relayCandidateType",
      "relayCandidateSelected",
      "localCandidateType",
      "remoteCandidateType",
      "currentRoundTripTime",
      "protocol",
      "networkType",
    ],
    "artifact.selectedCandidate"
  ));
  problems.push(...collectUnexpectedKeys(
    artifact.iceReadiness,
    [
      "ok",
      "hasIceServers",
      "iceServerCount",
      "knownUrlCount",
      "unknownUrlCount",
      "stunCount",
      "stunsCount",
      "turnCount",
      "turnsCount",
      "turnServersWithCredentials",
      "turnServersMissingCredentials",
      "relayTransports",
      "warnings",
      "errors",
    ],
    "artifact.iceReadiness"
  ));
  problems.push(...collectUnexpectedKeys(
    artifact.releaseEvidenceSummary,
    ["status", "runId", "roomId", "network", "testedAt", "relayProtocol", "relayCandidateType"],
    "artifact.releaseEvidenceSummary"
  ));
  problems.push(...collectUnexpectedKeys(
    artifact.promotion,
    [
      "promotedAt",
      "sourceArtifactType",
      "sourceStatus",
      "sourceArtifact",
      "operatorConfirmedRestrictiveNetwork",
      "operatorConfirmedSameRoom",
      "releaseEvidenceArtifactRef",
    ],
    "artifact.promotion"
  ));
  if (artifact.schemaVersion !== 1) {
    problems.push("artifact:schemaVersion");
  }
  if (artifact.artifactType !== "native_restrictive_turn") {
    problems.push("artifact:artifactType");
  }
  if (artifact.claimScope !== "restrictive_network_turn") {
    problems.push("artifact:claimScope");
  }
  if (artifact.releaseEligible !== true) {
    problems.push("artifact:releaseEligible");
  }
  if (artifact.status !== "passed") {
    problems.push("artifact:status");
  }
  if (!expectedIdentity(artifact.runId, runId)) {
    problems.push("artifact:runId");
  }
  if (!expectedIdentity(artifact.roomId, roomId)) {
    problems.push("artifact:roomId");
  }
  if (!expectedIdentity(artifact.network, item.network)) {
    problems.push("artifact:network");
  }
  if (!validTimestamp(artifact.capturedAt)) {
    problems.push("artifact:capturedAt");
  } else if (!strictStringEqual(artifact.capturedAt, item.testedAt)) {
    problems.push("artifact:capturedAt.match");
  }

  if (!artifact.app || typeof artifact.app !== "object" || Array.isArray(artifact.app)) {
    problems.push("artifact:app");
  } else {
    if (!expectedIdentity(artifact.app.version, expectedVersion)) {
      problems.push("artifact:app.version");
    }
    if (!expectedIdentity(artifact.app.build, expectedBuild)) {
      problems.push("artifact:app.build");
    }
    if (!["MeetingAssistAppleApp", "MeetingAssistMacApp"].includes(String(artifact.app.target ?? "").trim())) {
      problems.push("artifact:app.target");
    }
    if (!Object.values(physicalDeviceClientPlatforms).includes(String(artifact.app.clientPlatform ?? "").trim())) {
      problems.push("artifact:app.clientPlatform");
    }
  }

  if (!artifact.device || typeof artifact.device !== "object" || Array.isArray(artifact.device)) {
    problems.push("artifact:device");
  } else {
    if (!Object.values(physicalDeviceKinds).includes(String(artifact.device.kind ?? "").trim())) {
      problems.push("artifact:device.kind");
    }
    if (artifact.device.physical !== true) {
      problems.push("artifact:device.physical");
    }
    if (!nonPlaceholderString(artifact.device.model)) {
      problems.push("artifact:device.model");
    }
    if (!nonPlaceholderString(artifact.device.os)) {
      problems.push("artifact:device.os");
    }
  }

  const selected = artifact.selectedCandidate;
  if (!selected || typeof selected !== "object" || Array.isArray(selected)) {
    problems.push("artifact:selectedCandidate");
  } else {
    const relayProtocol = normalizedTurnRelayProtocol(selected.relayProtocol);
    const relayCandidateType = normalizedTurnRelayCandidateType(selected.relayCandidateType);
    if (!relayProtocol) {
      problems.push("artifact:selectedCandidate.relayProtocol");
    } else if (relayProtocol !== normalizedTurnRelayProtocol(item.relayProtocol)) {
      problems.push("artifact:selectedCandidate.relayProtocol.match");
    }
    if (!relayCandidateType) {
      problems.push("artifact:selectedCandidate.relayCandidateType");
    } else if (relayCandidateType !== normalizedTurnRelayCandidateType(item.relayCandidateType)) {
      problems.push("artifact:selectedCandidate.relayCandidateType.match");
    }
    if (selected.relayCandidateSelected !== true) {
      problems.push("artifact:selectedCandidate.relayCandidateSelected");
    }
    const localCandidateType = String(selected.localCandidateType ?? "").trim().toLowerCase();
    const remoteCandidateType = String(selected.remoteCandidateType ?? "").trim().toLowerCase();
    if (localCandidateType !== "relay" && remoteCandidateType !== "relay") {
      problems.push("artifact:selectedCandidate.localOrRemoteRelay");
    }
    if (!finitePositiveNumber(selected.currentRoundTripTime)) {
      problems.push("artifact:selectedCandidate.currentRoundTripTime");
    }
  }

  const iceReadiness = safeIceReadinessSummary(artifact.iceReadiness);
  if (!iceReadiness) {
    problems.push("artifact:iceReadiness");
  } else {
    if (iceReadiness.ok !== true) {
      problems.push("artifact:iceReadiness.ok");
    }
    if (iceReadiness.hasIceServers !== true) {
      problems.push("artifact:iceReadiness.hasIceServers");
    }
    if (!(Number(iceReadiness.turnCount) + Number(iceReadiness.turnsCount) > 0)) {
      problems.push("artifact:iceReadiness.turnRelayCount");
    }
    if (!(Number(iceReadiness.turnServersWithCredentials) > 0)) {
      problems.push("artifact:iceReadiness.turnServersWithCredentials");
    }
    if (Number(iceReadiness.turnServersMissingCredentials) !== 0) {
      problems.push("artifact:iceReadiness.turnServersMissingCredentials");
    }
    if (!Array.isArray(iceReadiness.errors) || iceReadiness.errors.length > 0) {
      problems.push("artifact:iceReadiness.errors");
    }
    if (!Array.isArray(iceReadiness.warnings) || iceReadiness.warnings.length > 0) {
      problems.push("artifact:iceReadiness.warnings");
    }
  }

  const summary = artifact.releaseEvidenceSummary;
  if (!summary || typeof summary !== "object" || Array.isArray(summary)) {
    problems.push("artifact:releaseEvidenceSummary");
  } else {
    if (summary.status !== "passed") {
      problems.push("artifact:releaseEvidenceSummary.status");
    }
    if (!expectedIdentity(summary.runId, runId)) {
      problems.push("artifact:releaseEvidenceSummary.runId");
    }
    if (!expectedIdentity(summary.roomId, roomId)) {
      problems.push("artifact:releaseEvidenceSummary.roomId");
    }
    if (!expectedIdentity(summary.network, item.network)) {
      problems.push("artifact:releaseEvidenceSummary.network");
    }
    if (!validTimestamp(summary.testedAt)) {
      problems.push("artifact:releaseEvidenceSummary.testedAt");
    } else if (!strictStringEqual(summary.testedAt, item.testedAt)) {
      problems.push("artifact:releaseEvidenceSummary.testedAt.match");
    }
    if (normalizedTurnRelayProtocol(summary.relayProtocol) !== normalizedTurnRelayProtocol(item.relayProtocol)) {
      problems.push("artifact:releaseEvidenceSummary.relayProtocol");
    }
    if (normalizedTurnRelayCandidateType(summary.relayCandidateType) !== normalizedTurnRelayCandidateType(item.relayCandidateType)) {
      problems.push("artifact:releaseEvidenceSummary.relayCandidateType");
    }
  }

  const promotion = artifact.promotion;
  if (!promotion || typeof promotion !== "object" || Array.isArray(promotion)) {
    problems.push("artifact:promotion");
  } else {
    if (!validTimestamp(promotion.promotedAt)) {
      problems.push("artifact:promotion.promotedAt");
    }
    if (promotion.sourceStatus !== "observed") {
      problems.push("artifact:promotion.sourceStatus");
    }
    if (promotion.operatorConfirmedRestrictiveNetwork !== true) {
      problems.push("artifact:promotion.operatorConfirmedRestrictiveNetwork");
    }
    if (promotion.operatorConfirmedSameRoom !== true) {
      problems.push("artifact:promotion.operatorConfirmedSameRoom");
    }
    if (!strictStringEqual(promotion.releaseEvidenceArtifactRef, item.artifactRef)) {
      problems.push("artifact:promotion.releaseEvidenceArtifactRef");
    }
  }

  const unsafeContent = [
    ...collectSecretLikeEvidence(artifact, "$.restrictiveNetworkTurn.artifact"),
    ...collectUnsafeTurnArtifactContent(artifact, "$.restrictiveNetworkTurn.artifact"),
  ];
  if (unsafeContent.length > 0) {
    problems.push(`artifact:unsafeContent:${unsafeContent.slice(0, 3).join("|")}`);
  }
  return problems;
}

function collectRestrictiveTurnArtifactContentProblems(evidence, evidenceRootDir, expectedVersion, expectedBuild) {
  const item = evidence.restrictiveNetworkTurn;
  const path = localArtifactPath(item?.artifactRef, evidenceRootDir);
  if (!path || path.startsWith("__") || !existsSync(path)) {
    return [];
  }
  if (!path.toLowerCase().endsWith(".json")) {
    return ["artifact:not_json"];
  }
  let artifact;
  try {
    artifact = readJSONFile(path);
  } catch {
    return ["artifact:not_valid_json"];
  }
  return uniqueLabels(
    restrictiveTurnArtifactProblems({
      item,
      artifact,
      expectedVersion,
      expectedBuild,
      runId: evidence.runId,
      roomId: evidence.roomId,
    })
  );
}

const testFlightEvidenceStatuses = ["ready", "uploaded", "processing", "accepted"];

function collectUnsafeDistributionArtifactContent(value, path = "$") {
  const problems = [];
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...collectUnsafeDistributionArtifactContent(item, `${path}[${index}]`));
    });
    return problems;
  }
  if (!value || typeof value !== "object") {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (
        /-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(trimmed) ||
        /-----BEGIN CERTIFICATE-----/.test(trimmed) ||
        /\bAKIA[0-9A-Z]{16}\b/.test(trimmed) ||
        /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(trimmed) ||
        /\/Users\/[^/\s]+/i.test(trimmed) ||
        /Developer ID (Application|Installer):.+\([A-Z0-9]{10}\)/.test(trimmed) ||
        /\.(p8|p12|mobileprovision|provisionprofile)\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    const isSafeDistributionConfirmation = /^operatorConfirmedNoSecrets$/i.test(key);
    if (
      !isSafeDistributionConfirmation &&
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|issuer_?id|key_?id|keychain|profile|authorization|jwt|apple_?id|username|raw(Log|Output)|uploadLog|log|stdout|stderr|command|args|env|notarytool(Output|Log)?|altool(Output|Log)?|codesign(Output|Log)?|spctl(Output|Log)?|headers?|cookies?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeDistributionArtifactContent(item, `${path}.${key}`));
  }
  return problems;
}

function distributionArtifactPathProblems(path) {
  if (!path || path.startsWith("__") || !existsSync(path)) {
    return { skip: true, problems: [] };
  }
  if (!path.toLowerCase().endsWith(".json")) {
    return { skip: false, problems: ["artifact:not_json"] };
  }
  return { skip: false, problems: [] };
}

function testFlightArtifactProblems({ item, artifact, expectedVersion, expectedBuild, runId }) {
  const problems = [];
  if (!artifact || typeof artifact !== "object" || Array.isArray(artifact)) {
    return ["artifact:not_object"];
  }
  problems.push(
    ...collectUnexpectedKeys(
      artifact,
      [
        "schemaVersion",
        "artifactType",
        "claimScope",
        "releaseEligible",
        "status",
        "runId",
        "uploadedAt",
        "app",
        "appStoreConnect",
        "releaseEvidenceSummary",
        "promotion",
      ],
      "artifact"
    ),
    ...collectUnexpectedKeys(
      artifact.app,
      ["version", "build", "target", "clientPlatform", "bundleIdentifier"],
      "artifact.app"
    ),
    ...collectUnexpectedKeys(
      artifact.appStoreConnect,
      ["buildId", "processingStatus"],
      "artifact.appStoreConnect"
    ),
    ...collectUnexpectedKeys(
      artifact.releaseEvidenceSummary,
      ["status", "runId", "version", "build", "appStoreConnectBuildId", "uploadedAt"],
      "artifact.releaseEvidenceSummary"
    ),
    ...collectUnexpectedKeys(
      artifact.promotion,
      [
        "promotedAt",
        "sourceArtifactType",
        "sourceStatus",
        "sourceArtifact",
        "operatorConfirmedAppStoreConnectUpload",
        "operatorConfirmedNoSecrets",
        "operatorConfirmedCurrentBuild",
        "releaseEvidenceArtifactRef",
      ],
      "artifact.promotion"
    )
  );
  if (artifact.schemaVersion !== 1) {
    problems.push("artifact:schemaVersion");
  }
  if (artifact.artifactType !== "native_testflight_upload") {
    problems.push("artifact:artifactType");
  }
  if (artifact.claimScope !== "app_store_connect_upload") {
    problems.push("artifact:claimScope");
  }
  if (artifact.releaseEligible !== true) {
    problems.push("artifact:releaseEligible");
  }
  const status = String(artifact.status ?? "").trim();
  if (!testFlightEvidenceStatuses.includes(status)) {
    problems.push("artifact:status");
  } else if (!strictStringEqual(status, item.status)) {
    problems.push("artifact:status.match");
  }
  if (!expectedIdentity(artifact.runId, runId)) {
    problems.push("artifact:runId");
  }
  if (!validTimestamp(artifact.uploadedAt)) {
    problems.push("artifact:uploadedAt");
  } else if (!strictStringEqual(artifact.uploadedAt, item.uploadedAt)) {
    problems.push("artifact:uploadedAt.match");
  }
  if (!artifact.app || typeof artifact.app !== "object" || Array.isArray(artifact.app)) {
    problems.push("artifact:app");
  } else {
    if (!expectedIdentity(artifact.app.version, expectedVersion)) {
      problems.push("artifact:app.version");
    }
    if (!expectedIdentity(artifact.app.build, expectedBuild)) {
      problems.push("artifact:app.build");
    }
    if (artifact.app.target !== "MeetingAssistAppleApp") {
      problems.push("artifact:app.target");
    }
    if (!["ios", "ipados"].includes(String(artifact.app.clientPlatform ?? "").trim())) {
      problems.push("artifact:app.clientPlatform");
    }
    if (!nonPlaceholderString(artifact.app.bundleIdentifier)) {
      problems.push("artifact:app.bundleIdentifier");
    }
  }
  if (!artifact.appStoreConnect || typeof artifact.appStoreConnect !== "object" || Array.isArray(artifact.appStoreConnect)) {
    problems.push("artifact:appStoreConnect");
  } else {
    if (!expectedIdentity(artifact.appStoreConnect.buildId, item.appStoreConnectBuildId)) {
      problems.push("artifact:appStoreConnect.buildId");
    }
    if (!strictStringEqual(artifact.appStoreConnect.processingStatus, item.status)) {
      problems.push("artifact:appStoreConnect.processingStatus");
    }
  }
  const summary = artifact.releaseEvidenceSummary;
  if (!summary || typeof summary !== "object" || Array.isArray(summary)) {
    problems.push("artifact:releaseEvidenceSummary");
  } else {
    if (!strictStringEqual(summary.status, item.status)) {
      problems.push("artifact:releaseEvidenceSummary.status");
    }
    if (!expectedIdentity(summary.runId, runId)) {
      problems.push("artifact:releaseEvidenceSummary.runId");
    }
    if (!expectedIdentity(summary.version, expectedVersion)) {
      problems.push("artifact:releaseEvidenceSummary.version");
    }
    if (!expectedIdentity(summary.build, expectedBuild)) {
      problems.push("artifact:releaseEvidenceSummary.build");
    }
    if (!expectedIdentity(summary.appStoreConnectBuildId, item.appStoreConnectBuildId)) {
      problems.push("artifact:releaseEvidenceSummary.appStoreConnectBuildId");
    }
    if (!validTimestamp(summary.uploadedAt) || !strictStringEqual(summary.uploadedAt, item.uploadedAt)) {
      problems.push("artifact:releaseEvidenceSummary.uploadedAt");
    }
  }
  const promotion = artifact.promotion;
  if (!promotion || typeof promotion !== "object" || Array.isArray(promotion)) {
    problems.push("artifact:promotion");
  } else {
    if (!validTimestamp(promotion.promotedAt)) {
      problems.push("artifact:promotion.promotedAt");
    }
    if (promotion.sourceStatus !== "observed") {
      problems.push("artifact:promotion.sourceStatus");
    }
    if (promotion.operatorConfirmedAppStoreConnectUpload !== true) {
      problems.push("artifact:promotion.operatorConfirmedAppStoreConnectUpload");
    }
    if (promotion.operatorConfirmedNoSecrets !== true) {
      problems.push("artifact:promotion.operatorConfirmedNoSecrets");
    }
    if (promotion.operatorConfirmedCurrentBuild !== true) {
      problems.push("artifact:promotion.operatorConfirmedCurrentBuild");
    }
    if (!strictStringEqual(promotion.releaseEvidenceArtifactRef, item.artifactRef)) {
      problems.push("artifact:promotion.releaseEvidenceArtifactRef");
    }
  }
  const unsafeContent = [
    ...collectSecretLikeEvidence(artifact, "$.testFlight.artifact"),
    ...collectUnsafeDistributionArtifactContent(artifact, "$.testFlight.artifact"),
  ];
  if (unsafeContent.length > 0) {
    problems.push(`artifact:unsafeContent:${unsafeContent.slice(0, 3).join("|")}`);
  }
  return problems;
}

function notarizationArtifactProblems({ item, artifact, expectedVersion, expectedBuild, runId }) {
  const problems = [];
  if (!artifact || typeof artifact !== "object" || Array.isArray(artifact)) {
    return ["artifact:not_object"];
  }
  problems.push(
    ...collectUnexpectedKeys(
      artifact,
      [
        "schemaVersion",
        "artifactType",
        "claimScope",
        "releaseEligible",
        "status",
        "runId",
        "checkedAt",
        "distributionArtifact",
        "app",
        "signing",
        "notarization",
        "staple",
        "gatekeeper",
        "releaseEvidenceSummary",
        "promotion",
      ],
      "artifact"
    ),
    ...collectUnexpectedKeys(
      artifact.distributionArtifact,
      ["kind", "filename", "sha256"],
      "artifact.distributionArtifact"
    ),
    ...collectUnexpectedKeys(
      artifact.app,
      ["version", "build", "target", "clientPlatform", "bundleIdentifier"],
      "artifact.app"
    ),
    ...collectUnexpectedKeys(
      artifact.signing,
      ["style", "signed", "hardenedRuntime", "timestamped"],
      "artifact.signing"
    ),
    ...collectUnexpectedKeys(
      artifact.notarization,
      ["requestId", "status", "issueCount"],
      "artifact.notarization"
    ),
    ...collectUnexpectedKeys(
      artifact.staple,
      ["stapled", "validated"],
      "artifact.staple"
    ),
    ...collectUnexpectedKeys(
      artifact.gatekeeper,
      ["assessment", "source"],
      "artifact.gatekeeper"
    ),
    ...collectUnexpectedKeys(
      artifact.releaseEvidenceSummary,
      ["status", "runId", "version", "build", "requestId", "stapled", "checkedAt"],
      "artifact.releaseEvidenceSummary"
    ),
    ...collectUnexpectedKeys(
      artifact.promotion,
      [
        "promotedAt",
        "sourceArtifactType",
        "sourceStatus",
        "sourceArtifact",
        "operatorConfirmedDeveloperIdArchive",
        "operatorConfirmedNotaryAccepted",
        "operatorConfirmedStapledApp",
        "operatorConfirmedGatekeeperAccepted",
        "operatorConfirmedCurrentBuild",
        "releaseEvidenceArtifactRef",
      ],
      "artifact.promotion"
    )
  );
  if (artifact.schemaVersion !== 1) {
    problems.push("artifact:schemaVersion");
  }
  if (artifact.artifactType !== "native_macos_notarization") {
    problems.push("artifact:artifactType");
  }
  if (artifact.claimScope !== "macos_notarization") {
    problems.push("artifact:claimScope");
  }
  if (artifact.releaseEligible !== true) {
    problems.push("artifact:releaseEligible");
  }
  if (artifact.status !== "accepted") {
    problems.push("artifact:status");
  }
  if (!expectedIdentity(artifact.runId, runId)) {
    problems.push("artifact:runId");
  }
  if (!validTimestamp(artifact.checkedAt)) {
    problems.push("artifact:checkedAt");
  } else if (!strictStringEqual(artifact.checkedAt, item.checkedAt)) {
    problems.push("artifact:checkedAt.match");
  }
  if (!artifact.distributionArtifact || typeof artifact.distributionArtifact !== "object" || Array.isArray(artifact.distributionArtifact)) {
    problems.push("artifact:distributionArtifact");
  } else {
    if (!["zip", "dmg", "pkg", "app"].includes(String(artifact.distributionArtifact.kind ?? "").trim())) {
      problems.push("artifact:distributionArtifact.kind");
    }
    if (!nonPlaceholderString(artifact.distributionArtifact.filename) || String(artifact.distributionArtifact.filename).includes("/")) {
      problems.push("artifact:distributionArtifact.filename");
    }
    if (!/^[a-f0-9]{64}$/i.test(String(artifact.distributionArtifact.sha256 ?? "").trim())) {
      problems.push("artifact:distributionArtifact.sha256");
    }
  }
  if (!artifact.app || typeof artifact.app !== "object" || Array.isArray(artifact.app)) {
    problems.push("artifact:app");
  } else {
    if (!expectedIdentity(artifact.app.version, expectedVersion)) {
      problems.push("artifact:app.version");
    }
    if (!expectedIdentity(artifact.app.build, expectedBuild)) {
      problems.push("artifact:app.build");
    }
    if (artifact.app.target !== "MeetingAssistMacApp") {
      problems.push("artifact:app.target");
    }
    if (artifact.app.clientPlatform !== "macos") {
      problems.push("artifact:app.clientPlatform");
    }
    if (!nonPlaceholderString(artifact.app.bundleIdentifier)) {
      problems.push("artifact:app.bundleIdentifier");
    }
  }
  if (!artifact.signing || typeof artifact.signing !== "object" || Array.isArray(artifact.signing)) {
    problems.push("artifact:signing");
  } else {
    if (artifact.signing.style !== "developer_id") {
      problems.push("artifact:signing.style");
    }
    if (artifact.signing.signed !== true) {
      problems.push("artifact:signing.signed");
    }
    if (artifact.signing.hardenedRuntime !== true) {
      problems.push("artifact:signing.hardenedRuntime");
    }
    if (artifact.signing.timestamped !== true) {
      problems.push("artifact:signing.timestamped");
    }
  }
  if (!artifact.notarization || typeof artifact.notarization !== "object" || Array.isArray(artifact.notarization)) {
    problems.push("artifact:notarization");
  } else {
    if (!expectedIdentity(artifact.notarization.requestId, item.requestId)) {
      problems.push("artifact:notarization.requestId");
    }
    if (artifact.notarization.status !== "accepted") {
      problems.push("artifact:notarization.status");
    }
    if (Number(artifact.notarization.issueCount) !== 0) {
      problems.push("artifact:notarization.issueCount");
    }
  }
  if (!artifact.staple || typeof artifact.staple !== "object" || Array.isArray(artifact.staple)) {
    problems.push("artifact:staple");
  } else {
    if (artifact.staple.stapled !== true) {
      problems.push("artifact:staple.stapled");
    }
    if (artifact.staple.validated !== true) {
      problems.push("artifact:staple.validated");
    }
  }
  if (!artifact.gatekeeper || typeof artifact.gatekeeper !== "object" || Array.isArray(artifact.gatekeeper)) {
    problems.push("artifact:gatekeeper");
  } else {
    if (artifact.gatekeeper.assessment !== "accepted") {
      problems.push("artifact:gatekeeper.assessment");
    }
    if (!nonPlaceholderString(artifact.gatekeeper.source)) {
      problems.push("artifact:gatekeeper.source");
    }
  }
  const summary = artifact.releaseEvidenceSummary;
  if (!summary || typeof summary !== "object" || Array.isArray(summary)) {
    problems.push("artifact:releaseEvidenceSummary");
  } else {
    if (summary.status !== "accepted") {
      problems.push("artifact:releaseEvidenceSummary.status");
    }
    if (!expectedIdentity(summary.runId, runId)) {
      problems.push("artifact:releaseEvidenceSummary.runId");
    }
    if (!expectedIdentity(summary.version, expectedVersion)) {
      problems.push("artifact:releaseEvidenceSummary.version");
    }
    if (!expectedIdentity(summary.build, expectedBuild)) {
      problems.push("artifact:releaseEvidenceSummary.build");
    }
    if (!expectedIdentity(summary.requestId, item.requestId)) {
      problems.push("artifact:releaseEvidenceSummary.requestId");
    }
    if (summary.stapled !== true) {
      problems.push("artifact:releaseEvidenceSummary.stapled");
    }
    if (!validTimestamp(summary.checkedAt) || !strictStringEqual(summary.checkedAt, item.checkedAt)) {
      problems.push("artifact:releaseEvidenceSummary.checkedAt");
    }
  }
  const promotion = artifact.promotion;
  if (!promotion || typeof promotion !== "object" || Array.isArray(promotion)) {
    problems.push("artifact:promotion");
  } else {
    if (!validTimestamp(promotion.promotedAt)) {
      problems.push("artifact:promotion.promotedAt");
    }
    if (promotion.sourceStatus !== "accepted") {
      problems.push("artifact:promotion.sourceStatus");
    }
    if (promotion.operatorConfirmedDeveloperIdArchive !== true) {
      problems.push("artifact:promotion.operatorConfirmedDeveloperIdArchive");
    }
    if (promotion.operatorConfirmedNotaryAccepted !== true) {
      problems.push("artifact:promotion.operatorConfirmedNotaryAccepted");
    }
    if (promotion.operatorConfirmedStapledApp !== true) {
      problems.push("artifact:promotion.operatorConfirmedStapledApp");
    }
    if (promotion.operatorConfirmedGatekeeperAccepted !== true) {
      problems.push("artifact:promotion.operatorConfirmedGatekeeperAccepted");
    }
    if (promotion.operatorConfirmedCurrentBuild !== true) {
      problems.push("artifact:promotion.operatorConfirmedCurrentBuild");
    }
    if (!strictStringEqual(promotion.releaseEvidenceArtifactRef, item.artifactRef)) {
      problems.push("artifact:promotion.releaseEvidenceArtifactRef");
    }
  }
  const unsafeContent = [
    ...collectSecretLikeEvidence(artifact, "$.macNotarization.artifact"),
    ...collectUnsafeDistributionArtifactContent(artifact, "$.macNotarization.artifact"),
  ];
  if (unsafeContent.length > 0) {
    problems.push(`artifact:unsafeContent:${unsafeContent.slice(0, 3).join("|")}`);
  }
  return problems;
}

function collectTestFlightArtifactContentProblems(evidence, evidenceRootDir, expectedVersion, expectedBuild) {
  const item = evidence.testFlight;
  const path = localArtifactPath(item?.artifactRef, evidenceRootDir);
  const pathCheck = distributionArtifactPathProblems(path);
  if (pathCheck.skip) {
    return [];
  }
  if (pathCheck.problems.length > 0) {
    return pathCheck.problems;
  }
  let artifact;
  try {
    artifact = readJSONFile(path);
  } catch {
    return ["artifact:not_valid_json"];
  }
  return uniqueLabels(testFlightArtifactProblems({ item, artifact, expectedVersion, expectedBuild, runId: evidence.runId }));
}

function collectNotarizationArtifactContentProblems(evidence, evidenceRootDir, expectedVersion, expectedBuild) {
  const item = evidence.macNotarization;
  const path = localArtifactPath(item?.artifactRef, evidenceRootDir);
  const pathCheck = distributionArtifactPathProblems(path);
  if (pathCheck.skip) {
    return [];
  }
  if (pathCheck.problems.length > 0) {
    return pathCheck.problems;
  }
  let artifact;
  try {
    artifact = readJSONFile(path);
  } catch {
    return ["artifact:not_valid_json"];
  }
  return uniqueLabels(notarizationArtifactProblems({ item, artifact, expectedVersion, expectedBuild, runId: evidence.runId }));
}

function expectedIdentity(value, expected) {
  return nonPlaceholderString(value) && strictStringEqual(value, expected);
}

function collectUnexpectedKeys(value, allowedKeys, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return [];
  }
  return Object.keys(value)
    .filter((key) => !allowedKeys.includes(key))
    .map((key) => `${label}.${key}`);
}

function collectSecretLikeEvidence(value, path = "$") {
  const problems = [];
  if (Array.isArray(value)) {
    value.forEach((item, index) => {
      problems.push(...collectSecretLikeEvidence(item, `${path}[${index}]`));
    });
    return problems;
  }
  if (!value || typeof value !== "object") {
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (
        validDevelopmentTeam(trimmed) ||
        /-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(trimmed) ||
        /-----BEGIN CERTIFICATE-----/.test(trimmed) ||
        /\bAKIA[0-9A-Z]{16}\b/.test(trimmed) ||
        /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\bxox[baprs]-[A-Za-z0-9-]{20,}\b/.test(trimmed) ||
        /\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\.(p8|p12|mobileprovision|provisionprofile)\b/i.test(trimmed)
      ) {
        problems.push(path);
      }
    }
    return problems;
  }

  for (const [key, item] of Object.entries(value)) {
    const isSafeEvidenceKey = /^(turnServersWithCredentials|turnServersMissingCredentials|operatorConfirmedNoSecrets)$/i.test(
      key
    );
    if (
      !isSafeEvidenceKey &&
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|turn_?(user|pass|credential))/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectSecretLikeEvidence(item, `${path}.${key}`));
  }
  return problems;
}

function developmentTeamValuesFromText(text) {
  const values = [];
  const patterns = [
    /DEVELOPMENT_TEAM:\s*([^\n#]+)/g,
    /DEVELOPMENT_TEAM\s*=\s*([^;\n#]+)/g,
    /DevelopmentTeam:\s*([^\n#]+)/g,
    /DevelopmentTeam\s*=\s*([^;\n#]+)/g,
  ];
  for (const pattern of patterns) {
    let match = pattern.exec(text);
    while (match) {
      values.push(cleanBuildSettingValue(match[1]));
      match = pattern.exec(text);
    }
  }
  return values;
}

function releaseEvidencePath(appleDir, requestedPath) {
  if (requestedPath) {
    return resolve(requestedPath);
  }

  const localPath = join(appleDir, "ReleaseEvidence.local.json");
  if (existsSync(localPath)) {
    return localPath;
  }

  const trackedPath = join(appleDir, "ReleaseEvidence.json");
  if (existsSync(trackedPath)) {
    return trackedPath;
  }

  return localPath;
}

function readJSONFile(path) {
  return JSON.parse(readText(path));
}

function distributionEvidenceBlockers({ appleDir, requestedPath, expectedVersion, expectedBuild }) {
  const path = releaseEvidencePath(appleDir, requestedPath);
  if (!existsSync(path)) {
    return [
      {
        id: "release_evidence_file",
        detail:
          "Add ignored apple/ReleaseEvidence.local.json or pass --evidence-file with physical device, TURN relay, TestFlight, and macOS notarization proof for this build.",
      },
    ];
  }

  let evidence;
  try {
    evidence = readJSONFile(path);
  } catch {
    return [{ id: "release_evidence_file", detail: `Release evidence is not valid JSON: ${path}` }];
  }
  if (!evidence || typeof evidence !== "object" || Array.isArray(evidence)) {
    return [{ id: "release_evidence_file", detail: `Release evidence must be a JSON object: ${path}` }];
  }

  const blockers = [];
  const schemaProblems = [
    ...collectUnexpectedKeys(
      evidence,
      [
        "version",
        "build",
        "runId",
        "roomId",
        "physicalDeviceMedia",
        "restrictiveNetworkTurn",
        "testFlight",
        "macNotarization",
      ],
      "$"
    ),
    ...collectUnexpectedKeys(evidence.physicalDeviceMedia, ["iphone", "ipad", "mac"], "$.physicalDeviceMedia"),
  ];
  for (const platform of ["iphone", "ipad", "mac"]) {
    const item = evidence.physicalDeviceMedia?.[platform];
    schemaProblems.push(
      ...collectUnexpectedKeys(
        item,
        ["status", "runId", "roomId", "device", "os", "testedAt", "artifactRef", "mediaAssertions"],
        `$.physicalDeviceMedia.${platform}`
      ),
      ...collectUnexpectedKeys(
        item?.mediaAssertions,
        ["cameraPublished", "microphonePublished", "remoteAudioReceived", "remoteVideoRendered"],
        `$.physicalDeviceMedia.${platform}.mediaAssertions`
      )
    );
  }
  schemaProblems.push(
    ...collectUnexpectedKeys(
      evidence.restrictiveNetworkTurn,
      ["status", "runId", "roomId", "network", "relayProtocol", "relayCandidateType", "testedAt", "artifactRef"],
      "$.restrictiveNetworkTurn"
    ),
    ...collectUnexpectedKeys(
      evidence.testFlight,
      ["status", "appStoreConnectBuildId", "uploadedAt", "artifactRef"],
      "$.testFlight"
    ),
    ...collectUnexpectedKeys(
      evidence.macNotarization,
      ["status", "requestId", "stapled", "checkedAt", "artifactRef"],
      "$.macNotarization"
    )
  );
  if (schemaProblems.length > 0) {
    blockers.push({
      id: "release_evidence_schema",
      detail: `Release evidence must use the known non-secret schema. Unexpected fields: ${schemaProblems.slice(0, 6).join(", ")}`,
    });
  }

  const secretProblems = collectSecretLikeEvidence(evidence);
  if (secretProblems.length > 0) {
    blockers.push({
      id: "release_evidence_secret_safety",
      detail: `Release evidence must not contain Team IDs, API keys, tokens, TURN credentials, private keys, certificates, or provisioning profiles. Problem fields: ${secretProblems.slice(0, 6).join(", ")}`,
    });
  }

  const missingLocalArtifactRefs = collectMissingLocalArtifactRefs(evidence, dirname(appleDir));
  if (missingLocalArtifactRefs.length > 0) {
    blockers.push({
      id: "release_evidence_artifacts",
      detail: `Local release evidence artifact references must point to files that exist. Missing: ${missingLocalArtifactRefs.slice(0, 6).join(", ")}`,
    });
  }

  const hasExpectedVersion = strictStringEqual(evidence.version, expectedVersion);
  const hasExpectedBuild = strictStringEqual(evidence.build, expectedBuild);
  if (!hasExpectedVersion || !hasExpectedBuild) {
    blockers.push({
      id: "release_evidence_version_build",
      detail: `Release evidence must match MARKETING_VERSION=${expectedVersion} and CURRENT_PROJECT_VERSION=${expectedBuild}.`,
    });
  }

  const runId = evidence.runId;
  const roomId = evidence.roomId;
  const identityProblems = [];
  if (!nonPlaceholderString(runId)) {
    identityProblems.push("runId");
  }
  if (!nonPlaceholderString(roomId)) {
    identityProblems.push("roomId");
  }
  if (identityProblems.length > 0) {
    blockers.push({
      id: "release_evidence_run_identity",
      detail: `Release evidence must include a shared non-placeholder runId and roomId. Missing or invalid: ${identityProblems.join(", ")}`,
    });
  }

  const media = evidence.physicalDeviceMedia;
  const deviceProblems = [
    ...collectPhysicalDeviceArtifactContentProblems(evidence, dirname(appleDir), expectedVersion, expectedBuild),
  ];
  for (const platform of ["iphone", "ipad", "mac"]) {
    const item = media?.[platform];
    if (!item || typeof item !== "object" || Array.isArray(item)) {
      deviceProblems.push(`${platform}:missing`);
      continue;
    }
    if (item.status !== "passed") {
      deviceProblems.push(`${platform}:status`);
    }
    for (const key of ["runId", "roomId"]) {
      const expected = key === "runId" ? runId : roomId;
      if (!expectedIdentity(item[key], expected)) {
        deviceProblems.push(`${platform}:${key}`);
      }
    }
    for (const key of ["device", "os", "artifactRef"]) {
      if (!nonPlaceholderString(item[key])) {
        deviceProblems.push(`${platform}:${key}`);
      }
    }
    if (!validArtifactRef(item.artifactRef)) {
      deviceProblems.push(`${platform}:artifactRef`);
    }
    if (!validTimestamp(item.testedAt)) {
      deviceProblems.push(`${platform}:testedAt`);
    }
    const assertions = item.mediaAssertions;
    if (!assertions || typeof assertions !== "object" || Array.isArray(assertions)) {
      deviceProblems.push(`${platform}:mediaAssertions`);
    } else {
      for (const key of ["cameraPublished", "microphonePublished", "remoteAudioReceived", "remoteVideoRendered"]) {
        if (assertions[key] !== true) {
          deviceProblems.push(`${platform}:mediaAssertions.${key}`);
        }
      }
    }
  }
  if (deviceProblems.length > 0) {
    const missing = uniqueLabels(deviceProblems);
    blockers.push({
      id: "physical_device_media_evidence",
      detail: `Add passed physical iPhone, iPad, and Mac mixed-room media evidence. Missing or invalid: ${missing.slice(0, 6).join(", ")}`,
    });
  }

  const turn = evidence.restrictiveNetworkTurn;
  const turnProblems = [
    ...collectRestrictiveTurnArtifactContentProblems(evidence, dirname(appleDir), expectedVersion, expectedBuild),
  ];
  if (!turn || typeof turn !== "object" || Array.isArray(turn)) {
    turnProblems.push("missing");
  } else {
    if (turn.status !== "passed") {
      turnProblems.push("status");
    }
    if (!validTimestamp(turn.testedAt)) {
      turnProblems.push("testedAt");
    }
    if (!expectedIdentity(turn.runId, runId)) {
      turnProblems.push("runId");
    }
    if (!expectedIdentity(turn.roomId, roomId)) {
      turnProblems.push("roomId");
    }
    if (!nonPlaceholderString(turn.network)) {
      turnProblems.push("network");
    }
    if (!validArtifactRef(turn.artifactRef)) {
      turnProblems.push("artifactRef");
    }
    if (!normalizedTurnRelayProtocol(turn.relayProtocol)) {
      turnProblems.push("relayProtocol");
    }
    if (!normalizedTurnRelayCandidateType(turn.relayCandidateType)) {
      turnProblems.push("relayCandidateType");
    }
  }
  if (turnProblems.length > 0) {
    const missing = uniqueLabels(turnProblems);
    blockers.push({
      id: "restrictive_turn_evidence",
      detail: `Add restrictive-network proof that native media used a selected TURN relay, tied to the release run, with a log/artifact reference. Missing or invalid: ${missing.join(", ")}`,
    });
  }

  const testFlight = evidence.testFlight;
  const testFlightProblems = [
    ...collectTestFlightArtifactContentProblems(evidence, dirname(appleDir), expectedVersion, expectedBuild),
  ];
  if (!testFlight || typeof testFlight !== "object" || Array.isArray(testFlight)) {
    testFlightProblems.push("missing");
  } else {
    if (!["ready", "uploaded", "processing", "accepted"].includes(String(testFlight.status ?? "").trim())) {
      testFlightProblems.push("status");
    }
    if (!nonPlaceholderString(testFlight.appStoreConnectBuildId)) {
      testFlightProblems.push("appStoreConnectBuildId");
    }
    if (!validTimestamp(testFlight.uploadedAt)) {
      testFlightProblems.push("uploadedAt");
    }
    if (!validArtifactRef(testFlight.artifactRef)) {
      testFlightProblems.push("artifactRef");
    }
  }
  if (testFlightProblems.length > 0) {
    const missing = uniqueLabels(testFlightProblems);
    blockers.push({
      id: "testflight_evidence",
      detail: `Add App Store Connect/TestFlight upload evidence for this build. Missing or invalid: ${missing.join(", ")}`,
    });
  }

  const mac = evidence.macNotarization;
  const macProblems = [
    ...collectNotarizationArtifactContentProblems(evidence, dirname(appleDir), expectedVersion, expectedBuild),
  ];
  if (!mac || typeof mac !== "object" || Array.isArray(mac)) {
    macProblems.push("missing");
  } else {
    if (mac.status !== "accepted") {
      macProblems.push("status");
    }
    if (!nonPlaceholderString(mac.requestId)) {
      macProblems.push("requestId");
    }
    if (mac.stapled !== true) {
      macProblems.push("stapled");
    }
    if (!validTimestamp(mac.checkedAt)) {
      macProblems.push("checkedAt");
    }
    if (!validArtifactRef(mac.artifactRef)) {
      macProblems.push("artifactRef");
    }
  }
  if (macProblems.length > 0) {
    const missing = uniqueLabels(macProblems);
    blockers.push({
      id: "mac_notarization_evidence",
      detail: `Add accepted and stapled macOS notarization evidence for this build. Missing or invalid: ${missing.join(", ")}`,
    });
  }

  return blockers;
}

function stripXcconfigComment(line) {
  const commentStart = line.indexOf("//");
  return (commentStart === -1 ? line : line.slice(0, commentStart)).trim();
}

function parseXcconfigSettings(path, options = {}, seen = new Set()) {
  const { includeOptional = true } = options;
  const settings = {};
  if (!existsSync(path)) {
    return settings;
  }

  const resolved = resolve(path);
  if (seen.has(resolved)) {
    return settings;
  }
  seen.add(resolved);

  for (const rawLine of readText(resolved).split(/\r?\n/)) {
    const line = stripXcconfigComment(rawLine);
    if (!line) {
      continue;
    }

    const includeMatch = /^#include(\?)?\s+"([^"]+)"/.exec(line);
    if (includeMatch) {
      const optional = includeMatch[1] === "?";
      if (optional && !includeOptional) {
        continue;
      }
      const includePath = resolve(dirname(resolved), includeMatch[2]);
      if (existsSync(includePath) || !optional) {
        Object.assign(settings, parseXcconfigSettings(includePath, options, seen));
      }
      continue;
    }

    const assignmentMatch = /^([A-Za-z0-9_]+)\s*=\s*(.*)$/.exec(line);
    if (assignmentMatch) {
      settings[assignmentMatch[1]] = cleanBuildSettingValue(assignmentMatch[2]);
    }
  }

  return settings;
}

function privacyManifestStatus(path) {
  if (!existsSync(path)) {
    return { ok: false, missing: ["missing_file"] };
  }

  let manifest;
  try {
    manifest = parsePlist(path);
  } catch {
    return { ok: false, missing: ["invalid_plist"] };
  }

  const missing = [];
  if (typeof manifest.NSPrivacyTracking !== "boolean") {
    missing.push("NSPrivacyTracking");
  }

  if (!Array.isArray(manifest.NSPrivacyTrackingDomains)) {
    missing.push("NSPrivacyTrackingDomains");
  } else if (manifest.NSPrivacyTracking === true && manifest.NSPrivacyTrackingDomains.length === 0) {
    missing.push("NSPrivacyTrackingDomains:required_when_tracking");
  }

  if (!Array.isArray(manifest.NSPrivacyAccessedAPITypes)) {
    missing.push("NSPrivacyAccessedAPITypes");
  } else {
    manifest.NSPrivacyAccessedAPITypes.forEach((entry, index) => {
      if (!entry || typeof entry !== "object") {
        missing.push(`NSPrivacyAccessedAPITypes[${index}]`);
        return;
      }
      if (!nonEmptyPlistString(entry, "NSPrivacyAccessedAPIType")) {
        missing.push(`NSPrivacyAccessedAPITypes[${index}].NSPrivacyAccessedAPIType`);
      }
      if (
        !Array.isArray(entry.NSPrivacyAccessedAPITypeReasons) ||
        entry.NSPrivacyAccessedAPITypeReasons.length === 0 ||
        entry.NSPrivacyAccessedAPITypeReasons.some((reason) => typeof reason !== "string" || reason.trim() === "")
      ) {
        missing.push(`NSPrivacyAccessedAPITypes[${index}].NSPrivacyAccessedAPITypeReasons`);
      }
    });
  }

  if (!Array.isArray(manifest.NSPrivacyCollectedDataTypes)) {
    missing.push("NSPrivacyCollectedDataTypes");
  } else if (manifest.NSPrivacyCollectedDataTypes.length === 0) {
    missing.push("NSPrivacyCollectedDataTypes:empty");
  } else {
    manifest.NSPrivacyCollectedDataTypes.forEach((entry, index) => {
      if (!entry || typeof entry !== "object") {
        missing.push(`NSPrivacyCollectedDataTypes[${index}]`);
        return;
      }
      if (!nonEmptyPlistString(entry, "NSPrivacyCollectedDataType")) {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataType`);
      }
      if (typeof entry.NSPrivacyCollectedDataTypeLinked !== "boolean") {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataTypeLinked`);
      }
      if (typeof entry.NSPrivacyCollectedDataTypeTracking !== "boolean") {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataTypeTracking`);
      }
      if (
        !Array.isArray(entry.NSPrivacyCollectedDataTypePurposes) ||
        entry.NSPrivacyCollectedDataTypePurposes.length === 0 ||
        entry.NSPrivacyCollectedDataTypePurposes.some((purpose) => typeof purpose !== "string" || purpose.trim() === "")
      ) {
        missing.push(`NSPrivacyCollectedDataTypes[${index}].NSPrivacyCollectedDataTypePurposes`);
      }
    });
  }

  return { ok: missing.length === 0, missing };
}

function findAppIconSet(appleDir, iconName) {
  let found = "";
  walk(appleDir, (path) => {
    if (path.endsWith(`${iconName}.appiconset`) && existsSync(join(path, "Contents.json"))) {
      found = path;
    }
  });
  return found;
}

function appIconSetStatus(appleDir, iconName, requiredSlots) {
  if (!iconName) {
    return { ok: false, missing: ["missing_app_icon_name"] };
  }
  const iconSetPath = findAppIconSet(appleDir, iconName);
  if (!iconSetPath) {
    return { ok: false, missing: [`missing_${iconName}.appiconset`] };
  }

  let contents;
  try {
    contents = JSON.parse(readText(join(iconSetPath, "Contents.json")));
  } catch {
    return { ok: false, missing: [`invalid_${iconName}_contents_json`] };
  }

  const images = Array.isArray(contents.images) ? contents.images : [];
  const imagesBySlot = new Map(
    images.map((image) => [appIconSlotKey(image.idiom, image.size, image.scale), image])
  );
  const missing = [];
  for (const [idiom, size, scale] of requiredSlots) {
    const key = appIconSlotKey(idiom, size, scale);
    const image = imagesBySlot.get(key);
    if (!image?.filename) {
      missing.push(key);
      continue;
    }
    const imagePath = join(iconSetPath, image.filename);
    if (!existsSync(imagePath)) {
      missing.push(`${key}:file`);
      continue;
    }
    const dimensions = pngDimensions(imagePath);
    const pixels = expectedIconPixels(size, scale);
    if (!dimensions || dimensions.width !== pixels || dimensions.height !== pixels) {
      missing.push(`${key}:dimensions`);
    }
  }

  return { ok: missing.length === 0, missing };
}

function extractSetting(text, key, assignmentPattern) {
  const yamlMatch = new RegExp(`${key}:\\s*([^\\n]+)`).exec(text);
  if (yamlMatch) {
    return yamlMatch[1].trim().replace(/^["']|["']$/g, "");
  }
  const pbxMatch = assignmentPattern.exec(text);
  if (pbxMatch) {
    return pbxMatch[1].trim().replace(/^"|"$/g, "");
  }
  return "";
}

function hasBuildSetting(text, key, valuePattern) {
  return new RegExp(`${key}:\\s*${valuePattern.source}`).test(text) ||
    new RegExp(`${key} = ${valuePattern.source};`).test(text);
}

function addCheck(checks, ok, id, detail) {
  checks.push({ id, ok, detail });
}

function addBlocker(blockers, id, detail) {
  blockers.push({ id, detail });
}

function analyze(options) {
  const appleDir = resolve(options.appleDir);
  const projectYmlPath = join(appleDir, "project.yml");
  const projectPath = join(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj");
  const iosInfoPath = join(appleDir, "Xcode", "MeetingAssistAppleApp-Info.plist");
  const macInfoPath = join(appleDir, "Xcode", "MeetingAssistMacApp-Info.plist");
  const macEntitlementsPath = join(appleDir, "Xcode", "MeetingAssistMacApp.entitlements");
  const privacyManifestPath = join(appleDir, "Xcode", "PrivacyInfo.xcprivacy");
  const signingConfigPath = join(appleDir, "Config", "Signing.xcconfig");

  const checks = [];
  const blockers = [];
  const warnings = [];

  for (const [id, path] of [
    ["apple_dir", appleDir],
    ["xcodegen_spec", projectYmlPath],
    ["xcode_project", projectPath],
    ["ios_info_plist", iosInfoPath],
    ["mac_info_plist", macInfoPath],
  ]) {
    addCheck(checks, existsSync(path), id, path);
  }

  if (checks.some((check) => !check.ok)) {
    return { appleDir, ok: false, readyForDistribution: false, checks, blockers, warnings };
  }

  const projectYml = readText(projectYmlPath);
  const pbxproj = readText(projectPath);
  const iosInfo = parsePlist(iosInfoPath);
  const macInfo = parsePlist(macInfoPath);

  addCheck(
    checks,
    textHas(projectYml, /MeetingAssistAppleApp:/) && textHas(projectYml, /TARGETED_DEVICE_FAMILY:\s*["']?1,2["']?/),
    "ios_universal_target",
    "MeetingAssistAppleApp should target iPhone and iPad."
  );
  addCheck(
    checks,
    textHas(projectYml, /MeetingAssistMacApp:/),
    "mac_native_target",
    "MeetingAssistMacApp should remain a native macOS target."
  );
  addCheck(
    checks,
    nonEmptyPlistString(iosInfo, "NSCameraUsageDescription") &&
      nonEmptyPlistString(iosInfo, "NSMicrophoneUsageDescription"),
    "ios_camera_microphone_usage_strings",
    "iOS/iPadOS app needs camera and microphone usage descriptions."
  );
  addCheck(
    checks,
    nonEmptyPlistString(macInfo, "NSCameraUsageDescription") &&
      nonEmptyPlistString(macInfo, "NSMicrophoneUsageDescription"),
    "mac_camera_microphone_usage_strings",
    "macOS app needs camera and microphone usage descriptions."
  );
  addCheck(
    checks,
    iosInfo.CFBundleShortVersionString === "$(MARKETING_VERSION)" &&
      macInfo.CFBundleShortVersionString === "$(MARKETING_VERSION)" &&
      iosInfo.CFBundleVersion === "$(CURRENT_PROJECT_VERSION)" &&
      macInfo.CFBundleVersion === "$(CURRENT_PROJECT_VERSION)" &&
      hasBuildSetting(projectYml, "MARKETING_VERSION", /1\.0/) &&
      hasBuildSetting(projectYml, "CURRENT_PROJECT_VERSION", /\d+/) &&
      textHas(pbxproj, /MARKETING_VERSION = 1\.0;/) &&
      textHas(pbxproj, /CURRENT_PROJECT_VERSION = \d+;/),
    "version_build_settings",
    "App versions should come from MARKETING_VERSION and CURRENT_PROJECT_VERSION build settings."
  );
  addCheck(
    checks,
    textHas(projectYml, /CODE_SIGN_ENTITLEMENTS:\s*Xcode\/MeetingAssistMacApp\.entitlements/) &&
      textHas(pbxproj, /CODE_SIGN_ENTITLEMENTS = Xcode\/MeetingAssistMacApp\.entitlements;/),
    "mac_entitlements_wired",
    "macOS target should reference MeetingAssistMacApp.entitlements."
  );
  addCheck(
    checks,
    textHas(projectYml, /ENABLE_HARDENED_RUNTIME:\s*YES/) && textHas(pbxproj, /ENABLE_HARDENED_RUNTIME = YES;/),
    "mac_hardened_runtime_enabled",
    "macOS target should enable hardened runtime before Developer ID notarization."
  );

  if (existsSync(macEntitlementsPath)) {
    const macEntitlements = parsePlist(macEntitlementsPath);
    addCheck(
      checks,
      boolPlistValue(macEntitlements, "com.apple.security.device.camera") &&
        boolPlistValue(macEntitlements, "com.apple.security.device.audio-input"),
      "mac_media_entitlements",
      "macOS entitlements should allow camera and audio input."
    );
  } else {
    addCheck(checks, false, "mac_media_entitlements", "Missing macOS entitlements file.");
  }

  const iosBundleId = extractSetting(
    projectYml,
    "PRODUCT_BUNDLE_IDENTIFIER",
    /PRODUCT_BUNDLE_IDENTIFIER = ([^;]+);/
  );
  const hasBundleIds =
    iosBundleId &&
    textHas(projectYml, /PRODUCT_BUNDLE_IDENTIFIER:\s*co\.thebonfire\.meetingassist\.mac/) &&
    textHas(pbxproj, /PRODUCT_BUNDLE_IDENTIFIER = co\.thebonfire\.meetingassist\.ios;/) &&
    textHas(pbxproj, /PRODUCT_BUNDLE_IDENTIFIER = co\.thebonfire\.meetingassist\.mac;/);
  addCheck(checks, Boolean(hasBundleIds), "bundle_identifiers", "iOS and macOS bundle identifiers should be stable.");

  addCheck(
    checks,
    textHas(projectYml, /path:\s*Xcode\/Assets\.xcassets/) &&
      textHas(pbxproj, /Assets\.xcassets in Resources/),
    "asset_catalog_wired",
    "iOS and macOS app targets should include the shared asset catalog."
  );
  addCheck(
    checks,
    textHas(projectYml, /ASSETCATALOG_COMPILER_APPICON_NAME:\s*AppIcon/) &&
      (pbxproj.match(/ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;/g) ?? []).length >= 2,
    "app_icon_build_settings",
    "Generated Xcode project should compile AppIcon for both app targets."
  );
  addCheck(
    checks,
    existsSync(signingConfigPath) &&
      (projectYml.match(/Config\/Signing\.xcconfig/g) ?? []).length >= 2 &&
      textHas(pbxproj, /Signing\.xcconfig/),
    "signing_xcconfig_wired",
    "App targets should use Config/Signing.xcconfig while keeping local team IDs out of git."
  );

  const marketingVersion = extractSetting(projectYml, "MARKETING_VERSION", /MARKETING_VERSION = ([^;]+);/);
  const currentProjectVersion = extractSetting(
    projectYml,
    "CURRENT_PROJECT_VERSION",
    /CURRENT_PROJECT_VERSION = ([^;]+);/
  );

  const signingSettings = parseXcconfigSettings(signingConfigPath);
  const signingTeamValue = expandBuildSettingValue(signingSettings.DEVELOPMENT_TEAM, signingSettings);
  const trackedSigningText = existsSync(signingConfigPath) ? readText(signingConfigPath) : "";
  const committedTeamValues = [
    ...developmentTeamValuesFromText(projectYml),
    ...developmentTeamValuesFromText(pbxproj),
    ...developmentTeamValuesFromText(trackedSigningText),
  ];
  addCheck(
    checks,
    !committedTeamValues.some(validDevelopmentTeam),
    "no_committed_development_team",
    "Apple development team IDs should come from environment or ignored local xcconfig, not committed project files or tracked xcconfig."
  );
  const hasTeam = [
    process.env.DEVELOPMENT_TEAM,
    process.env.APPLE_DEVELOPMENT_TEAM,
    signingTeamValue,
  ].some(validDevelopmentTeam);
  if (!hasTeam) {
    addBlocker(
      blockers,
      "apple_development_team",
      "Set DEVELOPMENT_TEAM or APPLE_DEVELOPMENT_TEAM in the build environment, or copy apple/Config/Signing.local.example.xcconfig to ignored apple/Config/Signing.local.xcconfig and set DEVELOPMENT_TEAM."
    );
  }

  const iosIconName = extractSetting(
    projectYml,
    "ASSETCATALOG_COMPILER_APPICON_NAME",
    /ASSETCATALOG_COMPILER_APPICON_NAME = ([^;]*);/
  );
  const iosIconStatus = appIconSetStatus(appleDir, iosIconName, iosAppIconSlots);
  if (!iosIconStatus.ok) {
    addBlocker(
      blockers,
      "ios_app_icon",
      `Add a complete iOS/iPadOS AppIcon asset catalog before TestFlight upload. Missing: ${iosIconStatus.missing.slice(0, 5).join(", ")}`
    );
  }

  const macIconName = extractSetting(
    projectYml,
    "ASSETCATALOG_COMPILER_APPICON_NAME",
    /ASSETCATALOG_COMPILER_APPICON_NAME = ([^;]*);/
  );
  const macIconStatus = appIconSetStatus(appleDir, macIconName, macAppIconSlots);
  if (!macIconStatus.ok) {
    addBlocker(
      blockers,
      "mac_app_icon",
      `Add a complete macOS AppIcon asset catalog before distribution. Missing: ${macIconStatus.missing.slice(0, 5).join(", ")}`
    );
  }

  const privacyStatus = privacyManifestStatus(privacyManifestPath);
  if (!privacyStatus.ok) {
    addBlocker(
      blockers,
      "privacy_manifest",
      `Add apple/Xcode/PrivacyInfo.xcprivacy only after docs/native-apple-privacy-review.md decisions are final. Missing or invalid: ${privacyStatus.missing.slice(0, 6).join(", ")}`
    );
  }

  for (const blocker of distributionEvidenceBlockers({
    appleDir,
    requestedPath: options.evidenceFile,
    expectedVersion: marketingVersion,
    expectedBuild: currentProjectVersion,
  })) {
    addBlocker(blockers, blocker.id, blocker.detail);
  }

  const failedChecks = checks.filter((check) => !check.ok);
  return {
    appleDir,
    ok: failedChecks.length === 0,
    readyForDistribution: failedChecks.length === 0 && blockers.length === 0,
    checks,
    blockers,
    warnings,
  };
}

try {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    console.log(usage());
    process.exit(0);
  }

  const result = analyze(args);
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.ok && (!args.strict || result.readyForDistribution) ? 0 : 1);
} catch (error) {
  console.error(JSON.stringify({ ok: false, error: error.message, usage: usage() }, null, 2));
  process.exit(1);
}
