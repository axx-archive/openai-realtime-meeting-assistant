#!/usr/bin/env node
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-release-proofpack.mjs");
const readinessScriptPath = resolve(scriptsDir, "native-apple-release-readiness.mjs");
const promoteMediaScriptPath = resolve(scriptsDir, "native-apple-promote-media-evidence.mjs");
const promoteTurnScriptPath = resolve(scriptsDir, "native-apple-promote-turn-evidence.mjs");
const promoteRoomScriptPath = resolve(scriptsDir, "native-apple-promote-room-gate-evidence.mjs");
const promoteDistributionScriptPath = resolve(scriptsDir, "native-apple-promote-distribution-evidence.mjs");

function runScript(path, args = []) {
  const result = spawnSync(process.execPath, [path, ...args], {
    cwd: rootDir,
    encoding: "utf8",
  });

  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(
      `Could not parse proofpack output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`
    );
  }
  return { status: result.status, output };
}

function runProofpack(args = []) {
  return runScript(scriptPath, args);
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

function writeJSONFile(path, value) {
  writeFixtureFile(path, `${JSON.stringify(value, null, 2)}\n`);
  return path;
}

function makeAppleFixture() {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-proofpack-"));
  const appleDir = resolve(dir, "apple");
  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
  MeetingAssistMacApp:
    settings:
      base:
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
`
  );
  return { dir, appleDir };
}

const fixture = makeAppleFixture();
const runId = `native-apple-proofpack-test-${process.pid}`;
const roomId = "proofpack-room-test";
const createdAt = "2026-06-29T16:00:00Z";
const artifactsDir = resolve(rootDir, "artifacts", "native-apple");

const created = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  artifactsDir,
  "--run-id",
  runId,
  "--room-id",
  roomId,
  "--created-at",
  createdAt,
  "--skip-gates",
]);
assert.equal(created.status, 0);
assert.equal(created.output.ok, true);
assert.equal(created.output.version, "1.0");
assert.equal(created.output.build, "15");
assert.equal(created.output.runId, runId);
assert.equal(created.output.roomId, roomId);
assert.ok(existsSync(created.output.proofpackPath));
assert.ok(existsSync(created.output.evidenceDraft));
assert.equal(created.output.releaseEvidenceComplete, false);
assert.ok(created.output.releaseEvidenceMissing.includes("physicalDeviceMedia.iphone.status"));
assert.ok(created.output.warnings.some((warning) => warning.includes("not release proof")));

const proofpack = JSON.parse(readFileSync(created.output.proofpackPath, "utf8"));
assert.equal(proofpack.schemaVersion, 1);
assert.equal(proofpack.runId, runId);
assert.equal(proofpack.roomId, roomId);
assert.ok(proofpack.nextSteps.some((step) => step.includes("native-apple-create-app-review-observation.mjs")));
assert.ok(proofpack.nextSteps.some((step) => step.includes("native-apple-promote-distribution-evidence.mjs --kind app-review")));
assert.ok(proofpack.nextSteps.some((step) => step.includes("native-apple-promote-distribution-evidence.mjs --kind testflight")));
assert.ok(proofpack.nextSteps.some((step) => step.includes("native-apple-promote-distribution-evidence.mjs --kind notarization")));
assert.ok(proofpack.nextSteps.some((step) => step.includes("native-apple-release-package-plan.mjs")));
for (const ref of Object.values(proofpack.evidenceArtifacts)) {
  assert.match(ref, new RegExp(`^artifacts/native-apple/${runId}/evidence/`));
  assert.ok(existsSync(resolve(rootDir, ref)));
}
for (const ref of Object.values(proofpack.observationTemplates)) {
  assert.match(ref, new RegExp(`^artifacts/native-apple/${runId}/inbox/`));
  assert.match(ref, /\.template\.json$/);
  assert.ok(existsSync(resolve(rootDir, ref)));
}
assert.equal(proofpack.observationTemplates.iphoneMedia, `artifacts/native-apple/${runId}/inbox/iphone-qa_snapshot.template.json`);
assert.equal(proofpack.observationTemplates.roomInterop, `artifacts/native-apple/${runId}/inbox/room-interop-observation.template.json`);
assert.equal(proofpack.observationTemplates.appStoreReview, `artifacts/native-apple/${runId}/inbox/app-store-review-observation.template.json`);
assert.equal(proofpack.observationTemplates.testFlight, `artifacts/native-apple/${runId}/inbox/testflight-observation.template.json`);
const inboxReadme = readFileSync(resolve(rootDir, "artifacts", "native-apple", runId, "inbox", "README.md"), "utf8");
assert.match(inboxReadme, /scaffolds, not release proof/);
assert.match(inboxReadme, /Do not edit \.\.\/evidence\//);
assert.match(inboxReadme, new RegExp(`runId=${runId}`));
assert.match(inboxReadme, new RegExp(`roomId=${roomId}`));
assert.match(inboxReadme, /Replace only <participant-name>/);
assert.match(inboxReadme, /Do not add passwords, tokens, cookies/);
assert.match(inboxReadme, /iPhone media: iphone-qa_snapshot\.json/);
assert.match(inboxReadme, /iPad media: ipad-qa_snapshot\.json/);
assert.match(inboxReadme, /Mac media: mac-qa_snapshot\.json/);
assert.match(inboxReadme, /Restrictive TURN: turn-relay-observation\.json/);
assert.match(inboxReadme, /Browser\/native room gate: room-interop-observation\.json/);
assert.match(inboxReadme, /App Store review metadata: app-store-review-observation\.json/);
assert.match(inboxReadme, /native-apple-create-app-review-observation\.mjs/);
const iphoneTemplate = JSON.parse(readFileSync(resolve(rootDir, proofpack.observationTemplates.iphoneMedia), "utf8"));
assert.equal(iphoneTemplate.artifactType, "native_device_media");
assert.equal(iphoneTemplate.status, "template");
assert.equal(iphoneTemplate.releaseEligible, false);
assert.equal(iphoneTemplate.app.target, "MeetingAssistAppleApp");
assert.equal(iphoneTemplate.device.physical, false);
assert.equal(iphoneTemplate.renderer.source, "native_remote_video_renderer");
assert.equal(iphoneTemplate.renderer.remoteVideoFramesRendered, 0);
const turnTemplate = JSON.parse(readFileSync(resolve(rootDir, proofpack.observationTemplates.turnRelay), "utf8"));
assert.equal(turnTemplate.artifactType, "native_turn_relay_observation");
assert.equal(turnTemplate.status, "template");
assert.equal(turnTemplate.selectedCandidate.relayCandidateSelected, false);
const roomInteropArtifact = JSON.parse(readFileSync(resolve(rootDir, proofpack.evidenceArtifacts.roomInterop), "utf8"));
assert.equal(roomInteropArtifact.schemaVersion, 1);
assert.match(roomInteropArtifact.notes, /native-apple-promote-room-gate-evidence\.mjs/);
const roomInteropTemplate = JSON.parse(readFileSync(resolve(rootDir, proofpack.observationTemplates.roomInterop), "utf8"));
assert.equal(roomInteropTemplate.artifactType, "native_room_interop_observation");
assert.equal(roomInteropTemplate.status, "template");
assert.equal(roomInteropTemplate.room.participantCount, 0);
assert.equal(roomInteropTemplate.recording.recordingOffRealtimeForwarded, true);
const appStoreReviewArtifact = JSON.parse(readFileSync(resolve(rootDir, proofpack.evidenceArtifacts.appStoreReview), "utf8"));
assert.equal(appStoreReviewArtifact.schemaVersion, 1);
assert.match(appStoreReviewArtifact.notes, /native-apple-promote-distribution-evidence\.mjs --kind app-review/);
const appStoreReviewTemplate = JSON.parse(readFileSync(resolve(rootDir, proofpack.observationTemplates.appStoreReview), "utf8"));
assert.equal(appStoreReviewTemplate.artifactType, "native_app_store_review_metadata_observation");
assert.equal(appStoreReviewTemplate.status, "template");
assert.equal(appStoreReviewTemplate.app.bundleIdentifier, "co.thebonfire.meetingassist.ios");
assert.equal(appStoreReviewTemplate.metadata.supportURL, "");
assert.equal(appStoreReviewTemplate.metadata.keywordsReady, false);
const testFlightArtifact = JSON.parse(readFileSync(resolve(rootDir, proofpack.evidenceArtifacts.testFlight), "utf8"));
assert.equal(testFlightArtifact.schemaVersion, 1);
assert.match(testFlightArtifact.notes, /native-apple-promote-distribution-evidence\.mjs --kind testflight/);
const notarizationArtifact = JSON.parse(readFileSync(resolve(rootDir, proofpack.evidenceArtifacts.notarization), "utf8"));
assert.equal(notarizationArtifact.schemaVersion, 1);
assert.match(notarizationArtifact.notes, /native-apple-promote-distribution-evidence\.mjs --kind notarization/);
const testFlightTemplate = JSON.parse(readFileSync(resolve(rootDir, proofpack.observationTemplates.testFlight), "utf8"));
assert.equal(testFlightTemplate.artifactType, "native_testflight_upload_observation");
assert.equal(testFlightTemplate.status, "template");
assert.equal(testFlightTemplate.app.bundleIdentifier, "co.thebonfire.meetingassist.ios");
assert.equal(testFlightTemplate.appStoreConnect.buildId, "");
const notarizationTemplate = JSON.parse(readFileSync(resolve(rootDir, proofpack.observationTemplates.notarization), "utf8"));
assert.equal(notarizationTemplate.artifactType, "native_macos_notarization_observation");
assert.equal(notarizationTemplate.status, "template");
assert.equal(notarizationTemplate.app.bundleIdentifier, "co.thebonfire.meetingassist.mac");
assert.equal(notarizationTemplate.notarization.issueCount, 1);

const draft = JSON.parse(readFileSync(created.output.evidenceDraft, "utf8"));
assert.equal(draft.version, "1.0");
assert.equal(draft.build, "15");
assert.equal(draft.physicalDeviceMedia.iphone.status, "pending");
assert.equal(draft.physicalDeviceMedia.iphone.mediaAssertions.cameraPublished, false);
assert.equal(draft.restrictiveNetworkTurn.status, "pending");
assert.equal(draft.appStoreReview.status, "pending");
assert.equal(draft.appStoreReview.keywordsReady, false);
assert.equal(draft.testFlight.status, "pending");
assert.equal(draft.macNotarization.status, "pending");

const promotionRunId = `native-apple-proofpack-template-promotion-${process.pid}`;
const promotionProofpack = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  artifactsDir,
  "--run-id",
  promotionRunId,
  "--room-id",
  "template-promotion-room-test",
  "--created-at",
  createdAt,
  "--skip-gates",
]);
assert.equal(promotionProofpack.status, 0);
const promotionManifest = JSON.parse(readFileSync(promotionProofpack.output.proofpackPath, "utf8"));
for (const platform of ["iphone", "ipad", "mac"]) {
  const templateKey = platform === "iphone" ? "iphoneMedia" : platform === "ipad" ? "ipadMedia" : "macMedia";
  const observation = JSON.parse(readFileSync(resolve(rootDir, promotionManifest.observationTemplates[templateKey]), "utf8"));
  const inputPath = resolve(promotionProofpack.output.proofpackDir, "inbox", `${platform}-qa_snapshot.json`);
  writeJSONFile(inputPath, {
    ...observation,
    status: "observed",
    device: {
      ...observation.device,
      physical: true,
      model: `${platform} physical`,
      os: platform === "mac" ? "macOS 26.5" : platform === "ipad" ? "iPadOS 26.5" : "iOS 26.5",
    },
    mediaAssertions: {
      cameraPublished: true,
      microphonePublished: true,
      remoteAudioReceived: true,
      remoteVideoRendered: true,
    },
    assertionEvidence: {
      cameraPublished: { passed: true, value: 12, source: "cumulative_peer_connection_stats" },
      microphonePublished: { passed: true, value: 24, source: "cumulative_peer_connection_stats" },
      remoteAudioReceived: { passed: true, value: 36, source: "cumulative_peer_connection_stats" },
      remoteVideoRendered: { passed: true, value: 3, source: "nativeRemoteVideoRenderer+inboundVideoDecoded" },
    },
    counters: {
      outboundAudioPacketsSent: 24,
      outboundVideoFramesSent: 12,
      inboundAudioPacketsReceived: 36,
      inboundVideoDecoded: 48,
    },
    remoteVideoTiles: 1,
    renderer: {
      source: "native_remote_video_renderer",
      remoteVideoFramesRendered: 3,
      observedRemoteVideoTracks: 1,
      latestFrameWidth: 1280,
      latestFrameHeight: 720,
      latestRenderedAt: "2026-06-29T17:45:01Z",
      capturesPixels: false,
    },
  });
  const promoted = runScript(promoteMediaScriptPath, [
    "--proofpack-dir",
    promotionProofpack.output.proofpackDir,
    "--platform",
    platform,
    "--input",
    inputPath,
    "--confirm-physical-device",
    "--confirm-same-room",
  ]);
  assert.equal(promoted.status, 0);
}

const turnObservation = JSON.parse(readFileSync(resolve(rootDir, promotionManifest.observationTemplates.turnRelay), "utf8"));
const turnInputPath = writeJSONFile(resolve(promotionProofpack.output.proofpackDir, "inbox", "turn-relay-observation.json"), {
  ...turnObservation,
  status: "observed",
  network: "restricted guest network",
  device: {
    ...turnObservation.device,
    physical: true,
    model: "iPhone physical",
    os: "iOS 26.5",
  },
  selectedCandidate: {
    relayProtocol: "turns",
    relayCandidateType: "relay",
    relayCandidateSelected: true,
    localCandidateType: "relay",
    remoteCandidateType: "srflx",
    currentRoundTripTime: 0.082,
    protocol: "udp",
    networkType: "wifi",
  },
  iceReadiness: {
    ok: true,
    hasIceServers: true,
    iceServerCount: 2,
    knownUrlCount: 3,
    unknownUrlCount: 0,
    stunCount: 1,
    stunsCount: 0,
    turnCount: 1,
    turnsCount: 1,
    turnServersWithCredentials: 1,
    turnServersMissingCredentials: 0,
    relayTransports: ["tls", "udp"],
    warnings: [],
    errors: [],
  },
});
const promotedTurn = runScript(promoteTurnScriptPath, [
  "--proofpack-dir",
  promotionProofpack.output.proofpackDir,
  "--input",
  turnInputPath,
  "--network",
  "restricted guest network",
  "--confirm-restrictive-network",
  "--confirm-same-room",
]);
assert.equal(promotedTurn.status, 0);

const roomInteropObservation = JSON.parse(readFileSync(resolve(rootDir, promotionManifest.observationTemplates.roomInterop), "utf8"));
const roomInteropInputPath = writeJSONFile(
  resolve(promotionProofpack.output.proofpackDir, "inbox", "room-interop-observation.json"),
  {
    ...roomInteropObservation,
    status: "observed",
    room: {
      participantCount: 4,
      clientPlatforms: ["browser", "ios", "ipados", "macos"],
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
  }
);
const promotedRoomInterop = runScript(promoteRoomScriptPath, [
  "--proofpack-dir",
  promotionProofpack.output.proofpackDir,
  "--input",
  roomInteropInputPath,
  "--confirm-browser-native-mixed-room",
  "--confirm-three-plus-participants",
  "--confirm-clean-leave",
  "--confirm-recording-off",
  "--confirm-current-build",
  "--confirm-no-secrets",
]);
assert.equal(promotedRoomInterop.status, 0);

const appStoreReviewObservation = JSON.parse(readFileSync(resolve(rootDir, promotionManifest.observationTemplates.appStoreReview), "utf8"));
const appStoreReviewInputPath = writeJSONFile(
  resolve(promotionProofpack.output.proofpackDir, "inbox", "app-store-review-observation.json"),
  {
    ...appStoreReviewObservation,
    status: "observed",
    metadata: {
      supportURL: "https://thebonfire.xyz/support",
      privacyPolicyURL: "https://thebonfire.xyz/privacy",
      descriptionReady: true,
      keywordsReady: true,
      screenshotsReady: true,
      appPrivacyReady: true,
      ageRatingComplete: true,
      exportComplianceComplete: true,
      testInformationReady: true,
      externalTestingGroupReady: true,
    },
  }
);
const promotedAppStoreReview = runScript(promoteDistributionScriptPath, [
  "--proofpack-dir",
  promotionProofpack.output.proofpackDir,
  "--kind",
  "app-review",
  "--input",
  appStoreReviewInputPath,
  "--confirm-review-metadata-complete",
  "--confirm-app-privacy-complete",
  "--confirm-external-testing-ready",
  "--confirm-no-secrets",
  "--confirm-current-build",
]);
assert.equal(promotedAppStoreReview.status, 0);

const testFlightObservation = JSON.parse(readFileSync(resolve(rootDir, promotionManifest.observationTemplates.testFlight), "utf8"));
const testFlightInputPath = writeJSONFile(
  resolve(promotionProofpack.output.proofpackDir, "inbox", "testflight-observation.json"),
  {
    ...testFlightObservation,
    status: "observed",
    appStoreConnect: {
      buildId: "asc-build-20260629-15",
      processingStatus: "ready",
    },
  }
);
const promotedTestFlight = runScript(promoteDistributionScriptPath, [
  "--proofpack-dir",
  promotionProofpack.output.proofpackDir,
  "--kind",
  "testflight",
  "--input",
  testFlightInputPath,
  "--confirm-app-store-connect-upload",
  "--confirm-no-secrets",
  "--confirm-current-build",
]);
assert.equal(promotedTestFlight.status, 0);

const notarizationObservation = JSON.parse(readFileSync(resolve(rootDir, promotionManifest.observationTemplates.notarization), "utf8"));
const notarizationInputPath = writeJSONFile(
  resolve(promotionProofpack.output.proofpackDir, "inbox", "notarization-observation.json"),
  {
    ...notarizationObservation,
    status: "accepted",
    distributionArtifact: {
      kind: "zip",
      filename: "MeetingAssistMacApp.zip",
      sha256: "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8",
    },
    signing: {
      style: "developer_id",
      signed: true,
      hardenedRuntime: true,
      timestamped: true,
    },
    notarization: {
      requestId: "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8",
      status: "accepted",
      issueCount: 0,
    },
    staple: {
      stapled: true,
      validated: true,
    },
    gatekeeper: {
      assessment: "accepted",
      source: "Notarized Developer ID",
    },
  }
);
const promotedNotarization = runScript(promoteDistributionScriptPath, [
  "--proofpack-dir",
  promotionProofpack.output.proofpackDir,
  "--kind",
  "notarization",
  "--input",
  notarizationInputPath,
  "--confirm-developer-id-archive",
  "--confirm-notary-accepted",
  "--confirm-stapled-app",
  "--confirm-gatekeeper-accepted",
  "--confirm-current-build",
]);
assert.equal(promotedNotarization.status, 0);
const promotedDraft = JSON.parse(readFileSync(promotionProofpack.output.evidenceDraft, "utf8"));
assert.equal(promotedDraft.physicalDeviceMedia.iphone.status, "passed");
assert.equal(promotedDraft.physicalDeviceMedia.ipad.status, "passed");
assert.equal(promotedDraft.physicalDeviceMedia.mac.status, "passed");
assert.equal(promotedDraft.restrictiveNetworkTurn.status, "passed");
assert.equal(promotedDraft.roomInterop.status, "passed");
assert.equal(promotedDraft.appStoreReview.status, "ready");
assert.equal(promotedDraft.appStoreReview.supportURL, "https://thebonfire.xyz/support");
assert.equal(promotedDraft.appStoreReview.keywordsReady, true);
assert.equal(promotedDraft.testFlight.status, "ready");
assert.equal(promotedDraft.macNotarization.status, "accepted");

const realStrictRunId = `native-apple-proofpack-real-strict-${process.pid}`;
const realProofpack = runProofpack([
  "--apple-dir",
  "apple",
  "--artifacts-dir",
  artifactsDir,
  "--run-id",
  realStrictRunId,
  "--room-id",
  "real-strict-proofpack-room-test",
  "--created-at",
  createdAt,
  "--skip-gates",
]);
assert.equal(realProofpack.status, 0);
const strictReadiness = spawnSync(process.execPath, [
  readinessScriptPath,
  "--apple-dir",
  "apple",
  "--strict",
  "--evidence-file",
  realProofpack.output.evidenceDraft,
], {
  cwd: rootDir,
  encoding: "utf8",
});
const strictReadinessOutput = JSON.parse(strictReadiness.stdout || strictReadiness.stderr);
assert.equal(strictReadiness.status, 1);
assert.equal(strictReadinessOutput.ok, true);
assert.equal(strictReadinessOutput.readyForDistribution, false);
assert.ok(strictReadinessOutput.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"));
assert.ok(strictReadinessOutput.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"));
assert.ok(strictReadinessOutput.blockers.some((blocker) => blocker.id === "room_interop_evidence"));
assert.ok(strictReadinessOutput.blockers.some((blocker) => blocker.id === "app_store_review_metadata"));

const localEvidencePath = resolve(fixture.appleDir, "ReleaseEvidence.local.json");
const pendingWrite = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  created.output.proofpackDir,
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(pendingWrite.status, 1);
assert.match(pendingWrite.output.error, /incomplete/);
assert.equal(existsSync(localEvidencePath), false);

const forcedPendingWrite = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  created.output.proofpackDir,
  "--write-evidence",
  "--force",
  "--skip-gates",
]);
assert.equal(forcedPendingWrite.status, 0);
assert.equal(forcedPendingWrite.output.releaseEvidenceComplete, false);
assert.ok(forcedPendingWrite.output.warnings.some((warning) => warning.includes("--force")));
assert.deepEqual(JSON.parse(readFileSync(forcedPendingWrite.output.localEvidenceWritten, "utf8")), draft);
rmSync(localEvidencePath, { force: true });

const completedDraft = {
  ...draft,
  physicalDeviceMedia: {
    iphone: {
      ...draft.physicalDeviceMedia.iphone,
      status: "passed",
      device: "iPhone physical",
      os: "iOS 26.5",
      mediaAssertions: {
        cameraPublished: true,
        microphonePublished: true,
        remoteAudioReceived: true,
        remoteVideoRendered: true,
      },
    },
    ipad: {
      ...draft.physicalDeviceMedia.ipad,
      status: "passed",
      device: "iPad physical",
      os: "iPadOS 26.5",
      mediaAssertions: {
        cameraPublished: true,
        microphonePublished: true,
        remoteAudioReceived: true,
        remoteVideoRendered: true,
      },
    },
    mac: {
      ...draft.physicalDeviceMedia.mac,
      status: "passed",
      device: "Mac physical",
      os: "macOS 26.5",
      mediaAssertions: {
        cameraPublished: true,
        microphonePublished: true,
        remoteAudioReceived: true,
        remoteVideoRendered: true,
      },
    },
  },
  restrictiveNetworkTurn: {
    ...draft.restrictiveNetworkTurn,
    status: "passed",
    network: "restricted guest network",
    relayProtocol: "turns",
    relayCandidateType: "relay",
  },
  roomInterop: {
    ...draft.roomInterop,
    status: "passed",
    participantCount: 4,
    browserNativeMixed: true,
    threePlusParticipants: true,
    remoteAudioAudible: true,
    remoteVideoRendered: true,
    cleanLeaveParticipantsEmpty: true,
    recordingOffStopsForwarding: true,
  },
  appStoreReview: {
    ...draft.appStoreReview,
    status: "ready",
    supportURL: "https://thebonfire.xyz/support",
    privacyPolicyURL: "https://thebonfire.xyz/privacy",
    descriptionReady: true,
    keywordsReady: true,
    screenshotsReady: true,
    appPrivacyReady: true,
    ageRatingComplete: true,
    exportComplianceComplete: true,
    testInformationReady: true,
    externalTestingGroupReady: true,
  },
  testFlight: {
    ...draft.testFlight,
    status: "ready",
    appStoreConnectBuildId: "asc-build-20260629-15",
  },
  macNotarization: {
    ...draft.macNotarization,
    status: "accepted",
    requestId: "8d7a1a32-9cde-4e80-b3c5-77f9e0f536b8",
    stapled: true,
  },
};
writeFileSync(created.output.evidenceDraft, `${JSON.stringify(completedDraft, null, 2)}\n`);

const manualCompletedWrite = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  created.output.proofpackDir,
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(manualCompletedWrite.status, 1);
assert.match(manualCompletedWrite.output.error, /incomplete/);
assert.equal(existsSync(localEvidencePath), false);

const wroteEvidence = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  promotionProofpack.output.proofpackDir,
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(wroteEvidence.status, 0);
assert.ok(wroteEvidence.output.localEvidenceWritten.endsWith("apple/ReleaseEvidence.local.json"));
assert.equal(wroteEvidence.output.releaseEvidenceComplete, true);
assert.deepEqual(wroteEvidence.output.releaseEvidenceMissing, []);
assert.deepEqual(JSON.parse(readFileSync(wroteEvidence.output.localEvidenceWritten, "utf8")), promotedDraft);
rmSync(localEvidencePath, { force: true });

const duplicate = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  artifactsDir,
  "--run-id",
  runId,
  "--room-id",
  roomId,
  "--created-at",
  createdAt,
  "--skip-gates",
]);
assert.equal(duplicate.status, 1);
assert.match(duplicate.output.error, /already exists/);

const secretRunId = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  resolve(rootDir, "artifacts", "native-apple"),
  "--run-id",
  "sk-thisShouldNotAppearInAProofPack000000",
  "--skip-gates",
]);
assert.equal(secretRunId.status, 1);
assert.match(secretRunId.output.error, /secret/);

const writeWithoutProofpackDir = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--artifacts-dir",
  resolve(rootDir, "artifacts", "native-apple"),
  "--run-id",
  `native-apple-proofpack-write-${process.pid}`,
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(writeWithoutProofpackDir.status, 1);
assert.match(writeWithoutProofpackDir.output.error, /requires --proofpack-dir/);

rmSync(localEvidencePath, { force: true });
const missingProofpackWrite = runProofpack([
  "--apple-dir",
  fixture.appleDir,
  "--proofpack-dir",
  resolve(rootDir, "artifacts", "native-apple", `missing-proofpack-${process.pid}`),
  "--write-evidence",
  "--skip-gates",
]);
assert.equal(missingProofpackWrite.status, 1);
assert.match(missingProofpackWrite.output.error, /does not exist/);
assert.equal(existsSync(localEvidencePath), false);

console.log("native-apple-release-proofpack: 7 checks passed");
