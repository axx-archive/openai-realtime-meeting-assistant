#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-release-proofpack.mjs [--apple-dir apple] [--artifacts-dir artifacts/native-apple]",
    "    [--run-id id] [--room-id id] [--created-at iso] [--proofpack-dir path]",
    "    [--skip-gates] [--full-gates] [--write-evidence] [--force]",
    "",
    "Creates a non-secret native Apple release proof pack. The pack contains",
    "pending evidence artifacts, inbox observation templates, and a",
    "ReleaseEvidence.draft.json shaped for scripts/native-apple-release-readiness.mjs.",
    "",
    "Default gates are lightweight repo release gates. --full-gates adds Go, Swift,",
    "media, and voice checks. --write-evidence copies a completed draft to ignored",
    "apple/ReleaseEvidence.local.json; use --force only for diagnostic pending copies.",
    "Strict readiness remains the source of truth.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    appleDir: "apple",
    artifactsDir: "artifacts/native-apple",
    runId: "",
    roomId: "",
    createdAt: "",
    proofpackDir: "",
    skipGates: false,
    fullGates: false,
    writeEvidence: false,
    force: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--apple-dir") {
      args.appleDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--artifacts-dir") {
      args.artifactsDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--run-id") {
      args.runId = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--room-id") {
      args.roomId = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--created-at") {
      args.createdAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--proofpack-dir") {
      args.proofpackDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--skip-gates") {
      args.skipGates = true;
    } else if (arg === "--full-gates") {
      args.fullGates = true;
    } else if (arg === "--write-evidence") {
      args.writeEvidence = true;
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

function readText(path) {
  return readFileSync(path, "utf8");
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function writeJSON(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

function writeText(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, value);
}

function isoForId(value) {
  return value.replaceAll(":", "").replaceAll("-", "").replace(/\.\d+Z$/, "Z");
}

function defaultRunId(createdAt) {
  return `native-apple-${isoForId(createdAt).replace(/[TZ]/g, "-").replace(/-$/, "")}`;
}

function cleanBuildValue(value) {
  return String(value ?? "").trim().replace(/^["']|["']$/g, "").replace(/;$/, "").trim();
}

function bundleIdentifierForTarget(projectText, targetName) {
  const start = projectText.indexOf(`  ${targetName}:`);
  if (start === -1) {
    return "";
  }
  const targetBlock = projectText.slice(start + targetName.length + 4);
  const nextTarget = targetBlock.search(/\n  [A-Za-z0-9_]+:/);
  const block = nextTarget >= 0 ? targetBlock.slice(0, nextTarget) : targetBlock;
  return cleanBuildValue(/PRODUCT_BUNDLE_IDENTIFIER:\s*([^\n#]+)/.exec(block)?.[1] ?? "");
}

function readVersionBuild(appleDir) {
  const projectText = readText(join(appleDir, "project.yml"));
  const marketing = /MARKETING_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  const build = /CURRENT_PROJECT_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  if (!marketing || !build) {
    throw new Error(`Could not read MARKETING_VERSION and CURRENT_PROJECT_VERSION from ${join(appleDir, "project.yml")}`);
  }
  return {
    version: cleanBuildValue(marketing),
    build: cleanBuildValue(build),
    bundleIdentifiers: {
      ios: bundleIdentifierForTarget(projectText, "MeetingAssistAppleApp"),
      macos: bundleIdentifierForTarget(projectText, "MeetingAssistMacApp"),
    },
  };
}

function nonSecretIdentifier(value, label) {
  const trimmed = String(value ?? "").trim();
  if (!trimmed) {
    throw new Error(`${label} is required.`);
  }
  if (!/^[A-Za-z0-9._-]{3,96}$/.test(trimmed)) {
    throw new Error(`${label} must use only letters, numbers, dot, underscore, or dash.`);
  }
  if (
    /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
    /\bxox[baprs]-[A-Za-z0-9-]{20,}\b/.test(trimmed) ||
    /\b[A-Z0-9]{10}\b/.test(trimmed) ||
    /\.(p8|p12|mobileprovision|provisionprofile)$/i.test(trimmed)
  ) {
    throw new Error(`${label} looks like a secret or account identifier and must not be used.`);
  }
  return trimmed;
}

function artifactRef(path) {
  const repoRelative = relative(rootDir, path);
  if (repoRelative.startsWith("..")) {
    throw new Error(`Proof-pack artifact must stay under the repository: ${path}`);
  }
  return repoRelative.split(/[/\\]/).join("/");
}

function pendingDeviceArtifact(platform, runId, roomId, createdAt) {
  return {
    artifactType: "native_device_media",
    status: "pending",
    platform,
    runId,
    roomId,
    capturedAt: createdAt,
    operatorChecklist: [
      "Join the same mixed room from this physical device.",
      "Confirm local camera is published from native stats, not only UI state.",
      "Confirm local microphone packets are published from native stats.",
      "Confirm remote audio is received/audible from browser or another native peer.",
      "Confirm remote video is decoded/rendered from browser or another native peer.",
    ],
    mediaAssertions: {
      cameraPublished: false,
      microphonePublished: false,
      remoteAudioReceived: false,
      remoteVideoRendered: false,
    },
    notes: "Use scripts/native-apple-promote-media-evidence.mjs with a real app-copied QA snapshot from this physical device before copying ReleaseEvidence.draft.json to apple/ReleaseEvidence.local.json.",
  };
}

function pendingTurnArtifact(runId, roomId, createdAt) {
  return {
    schemaVersion: 1,
    artifactType: "native_restrictive_turn",
    status: "pending",
    runId,
    roomId,
    capturedAt: createdAt,
    operatorChecklist: [
      "Run native media on a restrictive network for the same release room.",
      "Capture selected candidate-pair metadata only; do not include TURN credentials or raw ICE candidates.",
      "Confirm selected relayCandidateType is relay and relayProtocol is turn or turns.",
    ],
    selectedCandidate: {
      relayProtocol: "",
      relayCandidateType: "",
    },
    notes: "Use scripts/native-apple-promote-turn-evidence.mjs with a sanitized native_turn_relay_observation from the restrictive network before copying ReleaseEvidence.draft.json to apple/ReleaseEvidence.local.json.",
  };
}

function pendingTestFlightArtifact(runId, createdAt) {
  return {
    schemaVersion: 1,
    artifactType: "native_testflight_upload",
    status: "pending",
    runId,
    capturedAt: createdAt,
    operatorChecklist: [
      "Archive the iOS/iPadOS app with a real Apple Developer Team ID.",
      "Upload to App Store Connect/TestFlight.",
      "Record only the App Store Connect build id and processing status; do not include API keys or profiles.",
    ],
    appStoreConnectBuildId: "",
    notes: "Use scripts/native-apple-promote-distribution-evidence.mjs --kind testflight with a sanitized native_testflight_upload_observation before copying ReleaseEvidence.draft.json to apple/ReleaseEvidence.local.json.",
  };
}

function pendingNotarizationArtifact(runId, createdAt) {
  return {
    schemaVersion: 1,
    artifactType: "native_macos_notarization",
    status: "pending",
    runId,
    capturedAt: createdAt,
    operatorChecklist: [
      "Archive and sign the native macOS app outside the sandbox with real credentials.",
      "Submit with notarytool, wait for accepted status, staple the app, and validate Gatekeeper.",
      "Record request id, accepted status, and stapling result; do not include certificate private keys or profiles.",
    ],
    requestId: "",
    stapled: false,
    notes: "Use scripts/native-apple-promote-distribution-evidence.mjs --kind notarization with a sanitized native_macos_notarization_observation before copying ReleaseEvidence.draft.json to apple/ReleaseEvidence.local.json.",
  };
}

function deviceAppMetadata(platform, version, build) {
  return {
    version,
    build,
    target: platform === "mac" ? "MeetingAssistMacApp" : "MeetingAssistAppleApp",
    clientPlatform: platform === "ipad" ? "ipados" : platform === "mac" ? "macos" : "ios",
  };
}

function deviceObservationTemplate(platform, runId, roomId, createdAt, version, build) {
  return {
    schemaVersion: 1,
    artifactType: "native_device_media",
    claimScope: "qa_snapshot",
    releaseEligible: false,
    status: "template",
    lifecycle: "connected",
    runId,
    roomId,
    capturedAt: createdAt,
    app: deviceAppMetadata(platform, version, build),
    device: {
      kind: platform,
      physical: false,
      model: "",
      os: "",
    },
    mediaAssertions: {
      cameraPublished: false,
      microphonePublished: false,
      remoteAudioReceived: false,
      remoteVideoRendered: false,
    },
    assertionEvidence: {
      cameraPublished: { passed: false, value: 0, source: "cumulative_peer_connection_stats" },
      microphonePublished: { passed: false, value: 0, source: "cumulative_peer_connection_stats" },
      remoteAudioReceived: { passed: false, value: 0, source: "cumulative_peer_connection_stats" },
      remoteVideoRendered: { passed: false, value: 0, source: "cumulative_peer_connection_stats" },
    },
    counters: {
      outboundAudioPacketsSent: 0,
      outboundVideoFramesSent: 0,
      inboundAudioPacketsReceived: 0,
      inboundVideoDecoded: 0,
    },
    remoteVideoTiles: 0,
  };
}

function turnObservationTemplate(runId, roomId, createdAt, version, build) {
  return {
    schemaVersion: 1,
    artifactType: "native_turn_relay_observation",
    status: "template",
    runId,
    roomId,
    network: "",
    capturedAt: createdAt,
    app: {
      version,
      build,
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
    },
    device: {
      kind: "iphone",
      physical: false,
      model: "",
      os: "",
    },
    selectedCandidate: {
      relayProtocol: "",
      relayCandidateType: "",
      relayCandidateSelected: false,
      localCandidateType: "",
      remoteCandidateType: "",
      currentRoundTripTime: 0,
      protocol: "",
      networkType: "",
    },
    iceReadiness: {
      ok: false,
      hasIceServers: false,
      iceServerCount: 0,
      knownUrlCount: 0,
      unknownUrlCount: 0,
      stunCount: 0,
      stunsCount: 0,
      turnCount: 0,
      turnsCount: 0,
      turnServersWithCredentials: 0,
      turnServersMissingCredentials: 0,
      relayTransports: [],
      warnings: [],
      errors: [],
    },
  };
}

function testFlightObservationTemplate(runId, createdAt, version, build, bundleIdentifier) {
  return {
    schemaVersion: 1,
    artifactType: "native_testflight_upload_observation",
    status: "template",
    runId,
    uploadedAt: createdAt,
    app: {
      version,
      build,
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
      bundleIdentifier,
    },
    appStoreConnect: {
      buildId: "",
      processingStatus: "",
    },
  };
}

function notarizationObservationTemplate(runId, createdAt, version, build, bundleIdentifier) {
  return {
    schemaVersion: 1,
    artifactType: "native_macos_notarization_observation",
    status: "template",
    runId,
    checkedAt: createdAt,
    distributionArtifact: {
      kind: "",
      filename: "",
      sha256: "",
    },
    app: {
      version,
      build,
      target: "MeetingAssistMacApp",
      clientPlatform: "macos",
      bundleIdentifier,
    },
    signing: {
      style: "",
      signed: false,
      hardenedRuntime: false,
      timestamped: false,
    },
    notarization: {
      requestId: "",
      status: "",
      issueCount: 1,
    },
    staple: {
      stapled: false,
      validated: false,
    },
    gatekeeper: {
      assessment: "",
      source: "",
    },
  };
}

function inboxReadme(runId) {
  return `# Native Apple Proof-Pack Inbox

This folder is for real external-run observations for ${runId}.

Files ending in .template.json are scaffolds, not release proof. Copy a template
to the same folder without .template.json only after replacing placeholders with
values from the real physical-device, restrictive-network, TestFlight, or
notarization run.

Promotion helpers validate inbox observations and then write the release proof
under ../evidence/ plus ReleaseEvidence.draft.json. Do not edit ../evidence/
directly except to inspect generated pending or promoted artifacts.

Keep this folder non-secret: no raw SDP, raw ICE candidates, TURN URLs, account
identifiers, private key material, profiles, certificates, raw logs, headers,
cookies, host names, or device names.
`;
}

function releaseEvidenceDraft({ version, build, runId, roomId, createdAt, refs }) {
  return {
    version,
    build,
    runId,
    roomId,
    physicalDeviceMedia: {
      iphone: {
        status: "pending",
        runId,
        roomId,
        device: "",
        os: "",
        testedAt: createdAt,
        artifactRef: refs.iphone,
        mediaAssertions: {
          cameraPublished: false,
          microphonePublished: false,
          remoteAudioReceived: false,
          remoteVideoRendered: false,
        },
      },
      ipad: {
        status: "pending",
        runId,
        roomId,
        device: "",
        os: "",
        testedAt: createdAt,
        artifactRef: refs.ipad,
        mediaAssertions: {
          cameraPublished: false,
          microphonePublished: false,
          remoteAudioReceived: false,
          remoteVideoRendered: false,
        },
      },
      mac: {
        status: "pending",
        runId,
        roomId,
        device: "",
        os: "",
        testedAt: createdAt,
        artifactRef: refs.mac,
        mediaAssertions: {
          cameraPublished: false,
          microphonePublished: false,
          remoteAudioReceived: false,
          remoteVideoRendered: false,
        },
      },
    },
    restrictiveNetworkTurn: {
      status: "pending",
      runId,
      roomId,
      network: "",
      relayProtocol: "",
      relayCandidateType: "",
      testedAt: createdAt,
      artifactRef: refs.turn,
    },
    testFlight: {
      status: "pending",
      appStoreConnectBuildId: "",
      uploadedAt: createdAt,
      artifactRef: refs.testFlight,
    },
    macNotarization: {
      status: "pending",
      requestId: "",
      stapled: false,
      checkedAt: createdAt,
      artifactRef: refs.notarization,
    },
  };
}

function nonEmptyString(value) {
  return typeof value === "string" && value.trim().length > 0;
}

function releaseEvidenceCompletion(draft) {
  const missing = [];
  if (!draft || typeof draft !== "object" || Array.isArray(draft)) {
    return { complete: false, missing: ["draft"] };
  }
  for (const key of ["version", "build", "runId", "roomId"]) {
    if (!nonEmptyString(draft[key])) {
      missing.push(key);
    }
  }

  const media = draft.physicalDeviceMedia;
  for (const platform of ["iphone", "ipad", "mac"]) {
    const item = media?.[platform];
    if (!item || typeof item !== "object" || Array.isArray(item)) {
      missing.push(`physicalDeviceMedia.${platform}`);
      continue;
    }
    if (item.status !== "passed") {
      missing.push(`physicalDeviceMedia.${platform}.status`);
    }
    for (const key of ["runId", "roomId", "device", "os", "testedAt", "artifactRef"]) {
      if (!nonEmptyString(item[key])) {
        missing.push(`physicalDeviceMedia.${platform}.${key}`);
      }
    }
    for (const key of ["cameraPublished", "microphonePublished", "remoteAudioReceived", "remoteVideoRendered"]) {
      if (item.mediaAssertions?.[key] !== true) {
        missing.push(`physicalDeviceMedia.${platform}.mediaAssertions.${key}`);
      }
    }
  }

  const turn = draft.restrictiveNetworkTurn;
  if (!turn || typeof turn !== "object" || Array.isArray(turn)) {
    missing.push("restrictiveNetworkTurn");
  } else {
    if (turn.status !== "passed") {
      missing.push("restrictiveNetworkTurn.status");
    }
    for (const key of ["runId", "roomId", "network", "relayProtocol", "relayCandidateType", "testedAt", "artifactRef"]) {
      if (!nonEmptyString(turn[key])) {
        missing.push(`restrictiveNetworkTurn.${key}`);
      }
    }
  }

  const testFlight = draft.testFlight;
  if (!testFlight || typeof testFlight !== "object" || Array.isArray(testFlight)) {
    missing.push("testFlight");
  } else {
    if (!["ready", "uploaded", "processing", "accepted"].includes(String(testFlight.status ?? "").trim())) {
      missing.push("testFlight.status");
    }
    for (const key of ["appStoreConnectBuildId", "uploadedAt", "artifactRef"]) {
      if (!nonEmptyString(testFlight[key])) {
        missing.push(`testFlight.${key}`);
      }
    }
  }

  const mac = draft.macNotarization;
  if (!mac || typeof mac !== "object" || Array.isArray(mac)) {
    missing.push("macNotarization");
  } else {
    if (mac.status !== "accepted") {
      missing.push("macNotarization.status");
    }
    if (mac.stapled !== true) {
      missing.push("macNotarization.stapled");
    }
    for (const key of ["requestId", "checkedAt", "artifactRef"]) {
      if (!nonEmptyString(mac[key])) {
        missing.push(`macNotarization.${key}`);
      }
    }
  }

  return { complete: missing.length === 0, missing };
}

function gateCommands(fullGates) {
  const gates = [
    ["node", ["scripts/native-apple-release-readiness.mjs"]],
    ["node", ["scripts/native-apple-release-readiness.test.mjs"]],
    ["node", ["scripts/native-ice-readiness.test.mjs"]],
  ];
  if (fullGates) {
    gates.push(
      ["go", ["test", "./..."]],
      ["swift", ["test", "--package-path", "apple"]],
      ["node", ["scripts/media-fix-verification.mjs"]],
      ["node", ["scripts/voice-focus-benchmark.mjs"]]
    );
  }
  return gates;
}

function runGates(fullGates) {
  return gateCommands(fullGates).map(([command, args]) => {
    const startedAt = new Date().toISOString();
    const result = spawnSync(command, args, {
      cwd: rootDir,
      encoding: "utf8",
      maxBuffer: 1024 * 1024 * 20,
    });
    return {
      command: [command, ...args].join(" "),
      status: result.status === 0 ? "passed" : "failed",
      exitCode: result.status,
      startedAt,
      completedAt: new Date().toISOString(),
      outputTail: `${result.stdout ?? ""}${result.stderr ?? ""}`.slice(-4000),
    };
  });
}

function createProofpack(args) {
  const appleDir = resolve(rootDir, args.appleDir);
  const createdAt = args.createdAt || new Date().toISOString();
  if (!Number.isFinite(Date.parse(createdAt))) {
    throw new Error("--created-at must be an ISO-like timestamp.");
  }
  const runId = nonSecretIdentifier(args.runId || defaultRunId(createdAt), "runId");
  const roomId = nonSecretIdentifier(args.roomId || `${runId}-mixed-room`, "roomId");
  const proofpackDir = resolve(rootDir, args.proofpackDir || join(args.artifactsDir, runId));
  if (existsSync(proofpackDir) && !args.force) {
    throw new Error(`Proof pack already exists: ${proofpackDir}. Use --force or choose another run id.`);
  }

  const evidenceDir = join(proofpackDir, "evidence");
  const inboxDir = join(proofpackDir, "inbox");
  mkdirSync(evidenceDir, { recursive: true });
  mkdirSync(inboxDir, { recursive: true });
  const { version, build, bundleIdentifiers } = readVersionBuild(appleDir);
  const artifactPaths = {
    iphone: join(evidenceDir, "iphone-media.json"),
    ipad: join(evidenceDir, "ipad-media.json"),
    mac: join(evidenceDir, "mac-media.json"),
    turn: join(evidenceDir, "selected-turn-relay.json"),
    testFlight: join(evidenceDir, "testflight-build.json"),
    notarization: join(evidenceDir, "mac-notarization.json"),
  };
  const refs = Object.fromEntries(Object.entries(artifactPaths).map(([key, path]) => [key, artifactRef(path)]));
  const templatePaths = {
    iphoneMedia: join(inboxDir, "iphone-qa_snapshot.template.json"),
    ipadMedia: join(inboxDir, "ipad-qa_snapshot.template.json"),
    macMedia: join(inboxDir, "mac-qa_snapshot.template.json"),
    turnRelay: join(inboxDir, "turn-relay-observation.template.json"),
    testFlight: join(inboxDir, "testflight-observation.template.json"),
    notarization: join(inboxDir, "notarization-observation.template.json"),
  };
  const templateRefs = Object.fromEntries(Object.entries(templatePaths).map(([key, path]) => [key, artifactRef(path)]));

  writeJSON(artifactPaths.iphone, pendingDeviceArtifact("iphone", runId, roomId, createdAt));
  writeJSON(artifactPaths.ipad, pendingDeviceArtifact("ipad", runId, roomId, createdAt));
  writeJSON(artifactPaths.mac, pendingDeviceArtifact("mac", runId, roomId, createdAt));
  writeJSON(artifactPaths.turn, pendingTurnArtifact(runId, roomId, createdAt));
  writeJSON(artifactPaths.testFlight, pendingTestFlightArtifact(runId, createdAt));
  writeJSON(artifactPaths.notarization, pendingNotarizationArtifact(runId, createdAt));
  writeJSON(templatePaths.iphoneMedia, deviceObservationTemplate("iphone", runId, roomId, createdAt, version, build));
  writeJSON(templatePaths.ipadMedia, deviceObservationTemplate("ipad", runId, roomId, createdAt, version, build));
  writeJSON(templatePaths.macMedia, deviceObservationTemplate("mac", runId, roomId, createdAt, version, build));
  writeJSON(templatePaths.turnRelay, turnObservationTemplate(runId, roomId, createdAt, version, build));
  writeJSON(templatePaths.testFlight, testFlightObservationTemplate(runId, createdAt, version, build, bundleIdentifiers.ios));
  writeJSON(templatePaths.notarization, notarizationObservationTemplate(runId, createdAt, version, build, bundleIdentifiers.macos));
  writeText(join(inboxDir, "README.md"), inboxReadme(runId));

  const gates = args.skipGates ? [] : runGates(args.fullGates);
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  writeJSON(draftPath, releaseEvidenceDraft({ version, build, runId, roomId, createdAt, refs }));
  const proofpackPath = join(proofpackDir, "proofpack.json");
  writeJSON(proofpackPath, {
    schemaVersion: 1,
    createdAt,
    version,
    build,
    runId,
    roomId,
    appleDir: relative(rootDir, appleDir).split(/[/\\]/).join("/"),
    evidenceDraft: artifactRef(draftPath),
    evidenceArtifacts: refs,
    observationTemplates: templateRefs,
    gates,
    nextSteps: [
      "Copy generated inbox/*.template.json files to non-template JSON files only after replacing placeholders with real external-run observations.",
      "Promote real physical-device QA snapshots with scripts/native-apple-promote-media-evidence.mjs.",
      "Promote sanitized restrictive-network TURN relay observations with scripts/native-apple-promote-turn-evidence.mjs.",
      "Promote sanitized App Store Connect/TestFlight upload observations with scripts/native-apple-promote-distribution-evidence.mjs --kind testflight.",
      "Promote sanitized macOS notarization observations with scripts/native-apple-promote-distribution-evidence.mjs --kind notarization.",
      "Copy the completed ReleaseEvidence.draft.json to apple/ReleaseEvidence.local.json with --write-evidence.",
      "Run node scripts/native-apple-release-readiness.mjs --strict.",
    ],
  });

  const draft = readJSON(draftPath);
  const releaseEvidence = releaseEvidenceCompletion(draft);
  return { appleDir, proofpackDir, proofpackPath, draftPath, version, build, runId, roomId, gates, releaseEvidence };
}

function writeLocalEvidence(args, proofpackDir) {
  const appleDir = resolve(rootDir, args.appleDir);
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack evidence draft: ${draftPath}`);
  }
  const draft = readJSON(draftPath);
  const releaseEvidence = releaseEvidenceCompletion(draft);
  if (!releaseEvidence.complete && !args.force) {
    throw new Error(
      `ReleaseEvidence.draft.json is incomplete and cannot be copied to apple/ReleaseEvidence.local.json yet. Missing or invalid: ${releaseEvidence.missing
        .slice(0, 8)
        .join(", ")}. Promote real inbox observations first, or pass --force only for diagnostic local checks.`
    );
  }
  const localPath = join(appleDir, "ReleaseEvidence.local.json");
  cpSync(draftPath, localPath);
  return { localPath, releaseEvidence };
}

function existingProofpack(args) {
  if (!args.proofpackDir) {
    throw new Error("--write-evidence requires --proofpack-dir so pending evidence is not copied accidentally.");
  }
  const proofpackDir = resolve(rootDir, args.proofpackDir);
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  if (!existsSync(proofpackDir)) {
    throw new Error(`Proof pack does not exist: ${proofpackDir}`);
  }
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack evidence draft: ${draftPath}`);
  }
  const draft = readJSON(draftPath);
  const releaseEvidence = releaseEvidenceCompletion(draft);
  return {
    proofpackDir,
    proofpackPath: join(proofpackDir, "proofpack.json"),
    draftPath,
    version: draft.version || "",
    build: draft.build || "",
    runId: draft.runId || "",
    roomId: draft.roomId || "",
    gates: [],
    releaseEvidence,
  };
}

function main() {
  try {
    const args = parseArgs(process.argv.slice(2));
    if (args.help) {
      console.log(usage());
      return;
    }

    const proofpack = args.writeEvidence ? existingProofpack(args) : createProofpack(args);

    let localEvidence = "";
    let releaseEvidence = proofpack.releaseEvidence ?? { complete: false, missing: ["releaseEvidence"] };
    if (args.writeEvidence) {
      const written = writeLocalEvidence(args, proofpack.proofpackDir);
      localEvidence = written.localPath;
      releaseEvidence = written.releaseEvidence;
    }

    const gatesOk = proofpack.gates.every((gate) => gate.status === "passed");
    const warnings = [];
    if (!releaseEvidence.complete) {
      warnings.push("Release evidence is incomplete; this proof pack is not release proof until inbox observations are promoted and strict readiness passes.");
    }
    if (args.writeEvidence && !releaseEvidence.complete) {
      warnings.push("Incomplete ReleaseEvidence.local.json was written only because --force was passed.");
    }

    const output = {
      ok: gatesOk,
      gatesOk,
      releaseEvidenceComplete: releaseEvidence.complete,
      releaseEvidenceMissing: releaseEvidence.complete ? [] : releaseEvidence.missing.slice(0, 12),
      proofpackDir: proofpack.proofpackDir,
      proofpackPath: proofpack.proofpackPath,
      evidenceDraft: proofpack.draftPath,
      localEvidenceWritten: localEvidence || undefined,
      version: proofpack.version || undefined,
      build: proofpack.build || undefined,
      runId: proofpack.runId || undefined,
      roomId: proofpack.roomId || undefined,
      gateFailures: proofpack.gates.filter((gate) => gate.status !== "passed").map((gate) => gate.command),
      warnings,
    };
    console.log(JSON.stringify(output, null, 2));
    if (!output.ok) {
      process.exitCode = 1;
    }
  } catch (error) {
    console.error(JSON.stringify({ ok: false, error: error.message }, null, 2));
    process.exitCode = 1;
  }
}

main();
