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
    "operator-fillable evidence artifacts and a ReleaseEvidence.draft.json shaped",
    "for scripts/native-apple-release-readiness.mjs.",
    "",
    "Default gates are lightweight repo release gates. --full-gates adds Go, Swift,",
    "media, and voice checks. --write-evidence copies the current draft to ignored",
    "apple/ReleaseEvidence.local.json; strict readiness remains the source of truth.",
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

function writeJSON(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
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
  };
}

function pendingTestFlightArtifact(runId, createdAt) {
  return {
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
  };
}

function pendingNotarizationArtifact(runId, createdAt) {
  return {
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
  };
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
  mkdirSync(evidenceDir, { recursive: true });
  const { version, build } = readVersionBuild(appleDir);
  const artifactPaths = {
    iphone: join(evidenceDir, "iphone-media.json"),
    ipad: join(evidenceDir, "ipad-media.json"),
    mac: join(evidenceDir, "mac-media.json"),
    turn: join(evidenceDir, "selected-turn-relay.json"),
    testFlight: join(evidenceDir, "testflight-build.json"),
    notarization: join(evidenceDir, "mac-notarization.json"),
  };
  const refs = Object.fromEntries(Object.entries(artifactPaths).map(([key, path]) => [key, artifactRef(path)]));

  writeJSON(artifactPaths.iphone, pendingDeviceArtifact("iphone", runId, roomId, createdAt));
  writeJSON(artifactPaths.ipad, pendingDeviceArtifact("ipad", runId, roomId, createdAt));
  writeJSON(artifactPaths.mac, pendingDeviceArtifact("mac", runId, roomId, createdAt));
  writeJSON(artifactPaths.turn, pendingTurnArtifact(runId, roomId, createdAt));
  writeJSON(artifactPaths.testFlight, pendingTestFlightArtifact(runId, createdAt));
  writeJSON(artifactPaths.notarization, pendingNotarizationArtifact(runId, createdAt));

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
    gates,
    nextSteps: [
      "Promote real physical-device QA snapshots with scripts/native-apple-promote-media-evidence.mjs.",
      "Replace remaining pending TURN, TestFlight, and notarization artifacts with real non-secret proof.",
      "Copy the completed ReleaseEvidence.draft.json to apple/ReleaseEvidence.local.json with --write-evidence.",
      "Run node scripts/native-apple-release-readiness.mjs --strict.",
    ],
  });

  return { appleDir, proofpackDir, proofpackPath, draftPath, version, build, runId, roomId, gates };
}

function writeLocalEvidence(args, proofpackDir) {
  const appleDir = resolve(rootDir, args.appleDir);
  const draftPath = join(proofpackDir, "ReleaseEvidence.draft.json");
  if (!existsSync(draftPath)) {
    throw new Error(`Missing proof-pack evidence draft: ${draftPath}`);
  }
  const localPath = join(appleDir, "ReleaseEvidence.local.json");
  cpSync(draftPath, localPath);
  return localPath;
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
  return {
    proofpackDir,
    proofpackPath: join(proofpackDir, "proofpack.json"),
    draftPath,
    version: "",
    build: "",
    runId: "",
    roomId: "",
    gates: [],
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
    if (args.writeEvidence) {
      localEvidence = writeLocalEvidence(args, proofpack.proofpackDir);
    }

    const output = {
      ok: proofpack.gates.every((gate) => gate.status === "passed"),
      proofpackDir: proofpack.proofpackDir,
      proofpackPath: proofpack.proofpackPath,
      evidenceDraft: proofpack.draftPath,
      localEvidenceWritten: localEvidence || undefined,
      version: proofpack.version || undefined,
      build: proofpack.build || undefined,
      runId: proofpack.runId || undefined,
      roomId: proofpack.roomId || undefined,
      gateFailures: proofpack.gates.filter((gate) => gate.status !== "passed").map((gate) => gate.command),
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
