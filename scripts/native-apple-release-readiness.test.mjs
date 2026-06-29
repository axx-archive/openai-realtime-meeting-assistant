#!/usr/bin/env node
import assert from "node:assert/strict";
import { appendFileSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const rootDir = resolve(scriptsDir, "..");
const scriptPath = resolve(scriptsDir, "native-apple-release-readiness.mjs");

function syntheticTeamId(...parts) {
  return parts.join("");
}

function syntheticUuid() {
  return ["8d7a1a32", "9cde", "4e80", "b3c5", "77f9e0f536b8"].join("-");
}

function runReadiness(args = [], env = {}) {
  const result = spawnSync(process.execPath, [scriptPath, ...args], {
    cwd: rootDir,
    encoding: "utf8",
    env: {
      ...process.env,
      DEVELOPMENT_TEAM: "",
      APPLE_DEVELOPMENT_TEAM: "",
      ...env,
    },
  });

  let output;
  try {
    output = JSON.parse(result.stdout || result.stderr);
  } catch (error) {
    throw new Error(
      `Could not parse release readiness output.\nstatus=${result.status}\nstdout=${result.stdout}\nstderr=${result.stderr}\n${error}`
    );
  }

  return { status: result.status, output };
}

function writeFixtureFile(path, contents) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, contents);
}

const appIconSlots = [
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

function pixelsForSlot(size, scale) {
  return Math.round(Number(size.split("x")[0]) * Number(scale.replace("x", "")));
}

function pngWithDimensions(pixels) {
  const png = Buffer.alloc(33);
  Buffer.from("89504e470d0a1a0a", "hex").copy(png, 0);
  png.writeUInt32BE(13, 8);
  png.write("IHDR", 12, "ascii");
  png.writeUInt32BE(pixels, 16);
  png.writeUInt32BE(pixels, 20);
  png[24] = 8;
  png[25] = 6;
  return png;
}

function writeAppIconFixture(appleDir) {
  const iconSetDir = resolve(appleDir, "Xcode", "Assets.xcassets", "AppIcon.appiconset");
  writeFixtureFile(
    resolve(appleDir, "Xcode", "Assets.xcassets", "Contents.json"),
    `${JSON.stringify({ info: { author: "xcode", version: 1 } }, null, 2)}\n`
  );
  const images = appIconSlots.map(([idiom, size, scale]) => {
    const filename = `AppIcon-${idiom}-${size.replaceAll(".", "_")}@${scale}.png`;
    writeFixtureFile(resolve(iconSetDir, filename), pngWithDimensions(pixelsForSlot(size, scale)));
    return { idiom, size, scale, filename };
  });
  writeFixtureFile(
    resolve(iconSetDir, "Contents.json"),
    `${JSON.stringify({ images, info: { author: "xcode", version: 1 } }, null, 2)}\n`
  );
}

function writeSigningFixture(appleDir, localTeam = "") {
  writeFixtureFile(
    resolve(appleDir, "Config", "Signing.xcconfig"),
    `CODE_SIGN_STYLE = Automatic
DEVELOPMENT_TEAM = $(APPLE_DEVELOPMENT_TEAM)
#include? "Signing.local.xcconfig"
`
  );
  if (localTeam) {
    writeFixtureFile(resolve(appleDir, "Config", "Signing.local.xcconfig"), `DEVELOPMENT_TEAM = ${localTeam}\n`);
  }
}

function writePrivacyManifestFixture(appleDir, body = "complete") {
  if (body === "empty") {
    writeFixtureFile(resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"), "{}\n");
    return;
  }

  if (body === "incomplete") {
    writePlist(
      resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"),
      `<dict>
  <key>NSPrivacyTracking</key>
  <false/>
  <key>NSPrivacyTrackingDomains</key>
  <array/>
  <key>NSPrivacyAccessedAPITypes</key>
  <array/>
  <key>NSPrivacyCollectedDataTypes</key>
  <array>
    <dict>
      <key>NSPrivacyCollectedDataType</key>
      <string>NSPrivacyCollectedDataTypeName</string>
      <key>NSPrivacyCollectedDataTypePurposes</key>
      <array/>
    </dict>
  </array>
</dict>`
    );
    return;
  }

  writePlist(
    resolve(appleDir, "Xcode", "PrivacyInfo.xcprivacy"),
    `<dict>
  <key>NSPrivacyTracking</key>
  <false/>
  <key>NSPrivacyTrackingDomains</key>
  <array/>
  <key>NSPrivacyAccessedAPITypes</key>
  <array/>
  <key>NSPrivacyCollectedDataTypes</key>
  <array>
    <dict>
      <key>NSPrivacyCollectedDataType</key>
      <string>NSPrivacyCollectedDataTypeName</string>
      <key>NSPrivacyCollectedDataTypeLinked</key>
      <true/>
      <key>NSPrivacyCollectedDataTypeTracking</key>
      <false/>
      <key>NSPrivacyCollectedDataTypePurposes</key>
      <array>
        <string>NSPrivacyCollectedDataTypePurposeAppFunctionality</string>
      </array>
    </dict>
  </array>
</dict>`
  );
}

function wirePrivacyManifestFixture(appleDir) {
  const projectYmlPath = resolve(appleDir, "project.yml");
  let projectYml = readFileSync(projectYmlPath, "utf8");
  for (let index = 0; index < 2; index += 1) {
    projectYml = projectYml.replace(
      "      - path: Xcode/Assets.xcassets\n    settings:",
      "      - path: Xcode/Assets.xcassets\n      - path: Xcode/PrivacyInfo.xcprivacy\n    settings:"
    );
  }
  writeFixtureFile(projectYmlPath, projectYml);
  appendFileSync(
    resolve(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj"),
    "PrivacyInfo.xcprivacy in Resources;\nPrivacyInfo.xcprivacy in Resources;\n"
  );
}

function releaseEvidence(overrides = {}) {
  const runId = "native-release-run-20260629-a";
  const roomId = "release-room-smoke-20260629";
  const base = {
    version: "1.0",
    build: "15",
    runId,
    roomId,
    physicalDeviceMedia: {
      iphone: {
        status: "passed",
        runId,
        roomId,
        device: "iPhone 17 physical",
        os: "iOS 26.5",
        testedAt: "2026-06-29T12:00:00Z",
        artifactRef: "artifacts/native-release-run-20260629-a/iphone-media.json",
        mediaAssertions: {
          cameraPublished: true,
          microphonePublished: true,
          remoteAudioReceived: true,
          remoteVideoRendered: true,
        },
      },
      ipad: {
        status: "passed",
        runId,
        roomId,
        device: "iPad Pro physical",
        os: "iPadOS 26.5",
        testedAt: "2026-06-29T12:10:00Z",
        artifactRef: "artifacts/native-release-run-20260629-a/ipad-media.json",
        mediaAssertions: {
          cameraPublished: true,
          microphonePublished: true,
          remoteAudioReceived: true,
          remoteVideoRendered: true,
        },
      },
      mac: {
        status: "passed",
        runId,
        roomId,
        device: "MacBook Pro physical",
        os: "macOS 26.5",
        testedAt: "2026-06-29T12:20:00Z",
        artifactRef: "artifacts/native-release-run-20260629-a/mac-media.json",
        mediaAssertions: {
          cameraPublished: true,
          microphonePublished: true,
          remoteAudioReceived: true,
          remoteVideoRendered: true,
        },
      },
    },
    restrictiveNetworkTurn: {
      status: "passed",
      runId,
      roomId,
      network: "restricted guest network",
      relayProtocol: "turns",
      relayCandidateType: "relay",
      testedAt: "2026-06-29T12:25:00Z",
      artifactRef: "artifacts/native-release-run-20260629-a/turn-selected-relay.json",
    },
    testFlight: {
      status: "ready",
      appStoreConnectBuildId: `asc-${syntheticTeamId("82", "91", "74", "65", "02")}`,
      uploadedAt: "2026-06-29T12:30:00Z",
      artifactRef: "artifacts/native-release-run-20260629-a/testflight-build.json",
    },
    macNotarization: {
      status: "accepted",
      requestId: syntheticUuid(),
      stapled: true,
      checkedAt: "2026-06-29T12:40:00Z",
      artifactRef: "artifacts/native-release-run-20260629-a/notarization.json",
    },
  };

  return {
    ...base,
    ...overrides,
    physicalDeviceMedia: {
      ...base.physicalDeviceMedia,
      ...(overrides.physicalDeviceMedia ?? {}),
    },
    restrictiveNetworkTurn: {
      ...base.restrictiveNetworkTurn,
      ...(overrides.restrictiveNetworkTurn ?? {}),
    },
    testFlight: {
      ...base.testFlight,
      ...(overrides.testFlight ?? {}),
    },
    macNotarization: {
      ...base.macNotarization,
      ...(overrides.macNotarization ?? {}),
    },
  };
}

function evidenceRootForPath(path) {
  const evidenceDir = dirname(path);
  return evidenceDir.endsWith("/apple") ? dirname(evidenceDir) : evidenceDir;
}

function promotedPhysicalMediaArtifact(platform, evidence, overrides = {}) {
  const item = evidence.physicalDeviceMedia[platform];
  const base = {
    schemaVersion: 1,
    artifactType: "native_device_media",
    claimScope: "physical_device",
    releaseEligible: true,
    status: "passed",
    runId: evidence.runId,
    roomId: evidence.roomId,
    platform,
    capturedAt: item.testedAt,
    lifecycle: "connected",
    remoteVideoTiles: 1,
    renderer: {
      source: "native_remote_video_renderer",
      remoteVideoFramesRendered: 3,
      observedRemoteVideoTracks: 1,
      latestFrameWidth: 1280,
      latestFrameHeight: 720,
      latestRenderedAt: "2026-06-29T12:00:01Z",
      capturesPixels: false,
    },
    app: {
      version: evidence.version,
      build: evidence.build,
      target: platform === "mac" ? "MeetingAssistMacApp" : "MeetingAssistAppleApp",
      clientPlatform: platform === "ipad" ? "ipados" : platform === "mac" ? "macos" : "ios",
      clientVersion: "test",
    },
    device: {
      kind: platform,
      model: item.device,
      os: item.os,
      physical: true,
    },
    mediaAssertions: { ...item.mediaAssertions },
    assertionEvidence: {
      cameraPublished: { source: "outboundVideoFramesSent", value: 90, passed: true },
      microphonePublished: { source: "outboundAudioPacketsSent", value: 120, passed: true },
      remoteAudioReceived: { source: "inboundAudioPacketsReceived", value: 180, passed: true },
      remoteVideoRendered: { source: "nativeRemoteVideoRenderer+inboundVideoDecoded", value: 3, passed: true },
    },
    counters: {
      outboundAudioPacketsSent: 120,
      outboundVideoFramesSent: 90,
      inboundAudioPacketsReceived: 180,
      inboundVideoDecoded: 140,
    },
    releaseEvidenceSummary: {
      status: "passed",
      runId: evidence.runId,
      roomId: evidence.roomId,
      device: item.device,
      os: item.os,
      testedAt: item.testedAt,
      mediaAssertions: { ...item.mediaAssertions },
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    device: { ...base.device, ...(overrides.device ?? {}) },
    renderer: overrides.renderer === null ? null : { ...base.renderer, ...(overrides.renderer ?? {}) },
    mediaAssertions: { ...base.mediaAssertions, ...(overrides.mediaAssertions ?? {}) },
    releaseEvidenceSummary: {
      ...base.releaseEvidenceSummary,
      ...(overrides.releaseEvidenceSummary ?? {}),
      mediaAssertions: {
        ...base.releaseEvidenceSummary.mediaAssertions,
        ...(overrides.releaseEvidenceSummary?.mediaAssertions ?? {}),
      },
    },
  };
}

function promotedTurnArtifact(evidence, overrides = {}) {
  const item = evidence.restrictiveNetworkTurn;
  const base = {
    schemaVersion: 1,
    artifactType: "native_restrictive_turn",
    claimScope: "restrictive_network_turn",
    releaseEligible: true,
    status: "passed",
    runId: evidence.runId,
    roomId: evidence.roomId,
    network: item.network,
    capturedAt: item.testedAt,
    app: {
      version: evidence.version,
      build: evidence.build,
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
    },
    device: {
      kind: "iphone",
      model: "iPhone 17 physical",
      os: "iOS 26.5",
      physical: true,
    },
    selectedCandidate: {
      relayProtocol: item.relayProtocol,
      relayCandidateType: item.relayCandidateType,
      relayCandidateSelected: true,
      localCandidateType: "relay",
      remoteCandidateType: "srflx",
      currentRoundTripTime: 0.087,
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
    releaseEvidenceSummary: {
      status: "passed",
      runId: evidence.runId,
      roomId: evidence.roomId,
      network: item.network,
      testedAt: item.testedAt,
      relayProtocol: item.relayProtocol,
      relayCandidateType: item.relayCandidateType,
    },
    promotion: {
      promotedAt: "2026-06-29T12:26:00Z",
      sourceArtifactType: "native_turn_relay_observation",
      sourceStatus: "observed",
      sourceArtifact: "artifacts/native-release-run-20260629-a/inbox/turn-observation.json",
      operatorConfirmedRestrictiveNetwork: true,
      operatorConfirmedSameRoom: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
  return {
    ...base,
    ...overrides,
    app: {
      ...base.app,
      ...(overrides.app ?? {}),
    },
    device: {
      ...base.device,
      ...(overrides.device ?? {}),
    },
    selectedCandidate: {
      ...base.selectedCandidate,
      ...(overrides.selectedCandidate ?? {}),
    },
    iceReadiness: {
      ...base.iceReadiness,
      ...(overrides.iceReadiness ?? {}),
    },
    releaseEvidenceSummary: {
      ...base.releaseEvidenceSummary,
      ...(overrides.releaseEvidenceSummary ?? {}),
    },
    promotion: {
      ...base.promotion,
      ...(overrides.promotion ?? {}),
    },
  };
}

function promotedTestFlightArtifact(evidence, overrides = {}) {
  const item = evidence.testFlight;
  const base = {
    schemaVersion: 1,
    artifactType: "native_testflight_upload",
    claimScope: "app_store_connect_upload",
    releaseEligible: true,
    status: item.status,
    runId: evidence.runId,
    uploadedAt: item.uploadedAt,
    app: {
      version: evidence.version,
      build: evidence.build,
      target: "MeetingAssistAppleApp",
      clientPlatform: "ios",
      bundleIdentifier: "co.thebonfire.meetingassist.ios",
    },
    appStoreConnect: {
      buildId: item.appStoreConnectBuildId,
      processingStatus: item.status,
    },
    releaseEvidenceSummary: {
      status: item.status,
      runId: evidence.runId,
      version: evidence.version,
      build: evidence.build,
      appStoreConnectBuildId: item.appStoreConnectBuildId,
      uploadedAt: item.uploadedAt,
    },
    promotion: {
      promotedAt: "2026-06-29T12:31:00Z",
      sourceArtifactType: "native_testflight_upload_observation",
      sourceStatus: "observed",
      sourceArtifact: "artifacts/native-release-run-20260629-a/inbox/testflight-observation.json",
      operatorConfirmedAppStoreConnectUpload: true,
      operatorConfirmedNoSecrets: true,
      operatorConfirmedCurrentBuild: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
  return {
    ...base,
    ...overrides,
    app: { ...base.app, ...(overrides.app ?? {}) },
    appStoreConnect: { ...base.appStoreConnect, ...(overrides.appStoreConnect ?? {}) },
    releaseEvidenceSummary: { ...base.releaseEvidenceSummary, ...(overrides.releaseEvidenceSummary ?? {}) },
    promotion: { ...base.promotion, ...(overrides.promotion ?? {}) },
  };
}

function promotedNotarizationArtifact(evidence, overrides = {}) {
  const item = evidence.macNotarization;
  const base = {
    schemaVersion: 1,
    artifactType: "native_macos_notarization",
    claimScope: "macos_notarization",
    releaseEligible: true,
    status: "accepted",
    runId: evidence.runId,
    checkedAt: item.checkedAt,
    distributionArtifact: {
      kind: "zip",
      filename: "MeetingAssistMacApp.zip",
      sha256: "9cde1a328d7a4e80b3c577f9e0f536b89cde1a328d7a4e80b3c577f9e0f536b8",
    },
    app: {
      version: evidence.version,
      build: evidence.build,
      target: "MeetingAssistMacApp",
      clientPlatform: "macos",
      bundleIdentifier: "co.thebonfire.meetingassist.mac",
    },
    signing: {
      style: "developer_id",
      signed: true,
      hardenedRuntime: true,
      timestamped: true,
    },
    notarization: {
      requestId: item.requestId,
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
    releaseEvidenceSummary: {
      status: "accepted",
      runId: evidence.runId,
      version: evidence.version,
      build: evidence.build,
      requestId: item.requestId,
      stapled: true,
      checkedAt: item.checkedAt,
    },
    promotion: {
      promotedAt: "2026-06-29T12:41:00Z",
      sourceArtifactType: "native_macos_notarization_observation",
      sourceStatus: "accepted",
      sourceArtifact: "artifacts/native-release-run-20260629-a/inbox/notarization-observation.json",
      operatorConfirmedDeveloperIdArchive: true,
      operatorConfirmedNotaryAccepted: true,
      operatorConfirmedStapledApp: true,
      operatorConfirmedGatekeeperAccepted: true,
      operatorConfirmedCurrentBuild: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
  return {
    ...base,
    ...overrides,
    distributionArtifact: {
      ...base.distributionArtifact,
      ...(overrides.distributionArtifact ?? {}),
    },
    app: { ...base.app, ...(overrides.app ?? {}) },
    signing: { ...base.signing, ...(overrides.signing ?? {}) },
    notarization: { ...base.notarization, ...(overrides.notarization ?? {}) },
    staple: { ...base.staple, ...(overrides.staple ?? {}) },
    gatekeeper: { ...base.gatekeeper, ...(overrides.gatekeeper ?? {}) },
    releaseEvidenceSummary: { ...base.releaseEvidenceSummary, ...(overrides.releaseEvidenceSummary ?? {}) },
    promotion: { ...base.promotion, ...(overrides.promotion ?? {}) },
  };
}

function writeEvidenceArtifactFixtures(path, evidence, options = {}) {
  const rootDir = evidenceRootForPath(path);
  for (const platform of ["iphone", "ipad", "mac"]) {
    const ref = evidence.physicalDeviceMedia?.[platform]?.artifactRef;
    if (typeof ref !== "string" || !/^(artifacts\/|evidence\/)/.test(ref) || ref.split("/").includes("..")) {
      continue;
    }
    const artifact =
      options.physicalMediaArtifacts?.[platform] ??
      promotedPhysicalMediaArtifact(platform, evidence, options.physicalMediaArtifactOverrides?.[platform]);
    writeFixtureFile(resolve(rootDir, ref), `${JSON.stringify(artifact, null, 2)}\n`);
  }
  const turnRef = evidence.restrictiveNetworkTurn?.artifactRef;
  if (typeof turnRef === "string" && /^(artifacts\/|evidence\/)/.test(turnRef) && !turnRef.split("/").includes("..")) {
    const artifact = options.turnArtifact ?? promotedTurnArtifact(evidence, options.turnArtifactOverrides);
    writeFixtureFile(resolve(rootDir, turnRef), `${JSON.stringify(artifact, null, 2)}\n`);
  }
  const testFlightRef = evidence.testFlight?.artifactRef;
  if (
    typeof testFlightRef === "string" &&
    /^(artifacts\/|evidence\/)/.test(testFlightRef) &&
    !testFlightRef.split("/").includes("..")
  ) {
    const artifact = options.testFlightArtifact ?? promotedTestFlightArtifact(evidence, options.testFlightArtifactOverrides);
    writeFixtureFile(resolve(rootDir, testFlightRef), `${JSON.stringify(artifact, null, 2)}\n`);
  }
  const notarizationRef = evidence.macNotarization?.artifactRef;
  if (
    typeof notarizationRef === "string" &&
    /^(artifacts\/|evidence\/)/.test(notarizationRef) &&
    !notarizationRef.split("/").includes("..")
  ) {
    const artifact = options.notarizationArtifact ?? promotedNotarizationArtifact(evidence, options.notarizationArtifactOverrides);
    writeFixtureFile(resolve(rootDir, notarizationRef), `${JSON.stringify(artifact, null, 2)}\n`);
  }
}

function writeReleaseEvidenceFixture(path, overrides = {}, options = {}) {
  const evidence = releaseEvidence(overrides);
  writeFixtureFile(path, `${JSON.stringify(evidence, null, 2)}\n`);
  if (options.createArtifacts !== false) {
    writeEvidenceArtifactFixtures(path, evidence, options);
  }
}

function writePlist(path, body) {
  writeFixtureFile(
    path,
    `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
${body}
</plist>
`
  );
}

function makeFixture({ includeIcons, includePrivacy, localTeam = "", includeLaunchScheme = true }) {
  const dir = mkdtempSync(resolve(tmpdir(), "meetingassist-apple-release-"));
  const appleDir = resolve(dir, "apple");
  mkdirSync(resolve(appleDir, "MeetingAssist.xcodeproj"), { recursive: true });
  const launchSchemeYaml = includeLaunchScheme
    ? `    info:
      properties:
        CFBundleURLTypes:
          - CFBundleURLName: co.thebonfire.meetingassist
            CFBundleURLSchemes:
              - meetingassist
`
    : "";

  writeFixtureFile(
    resolve(appleDir, "project.yml"),
    `targets:
  MeetingAssistAppleApp:
    configFiles:
      Debug: Config/Signing.xcconfig
      Release: Config/Signing.xcconfig
    sources:
      - path: Xcode/MeetingAssistAppleApp.swift
      - path: Xcode/Assets.xcassets
    settings:
      base:
        ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon
        CURRENT_PROJECT_VERSION: 15
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.ios
        TARGETED_DEVICE_FAMILY: "1,2"
${launchSchemeYaml}  MeetingAssistMacApp:
    configFiles:
      Debug: Config/Signing.xcconfig
      Release: Config/Signing.xcconfig
    sources:
      - path: Xcode/MeetingAssistMacApp.swift
      - path: Xcode/Assets.xcassets
    settings:
      base:
        CODE_SIGN_ENTITLEMENTS: Xcode/MeetingAssistMacApp.entitlements
        CURRENT_PROJECT_VERSION: 15
        ENABLE_HARDENED_RUNTIME: YES
        MARKETING_VERSION: 1.0
        PRODUCT_BUNDLE_IDENTIFIER: co.thebonfire.meetingassist.mac
${launchSchemeYaml}`
  );
  writeFixtureFile(
    resolve(appleDir, "MeetingAssist.xcodeproj", "project.pbxproj"),
    `PRODUCT_BUNDLE_IDENTIFIER = co.thebonfire.meetingassist.ios;
PRODUCT_BUNDLE_IDENTIFIER = co.thebonfire.meetingassist.mac;
CODE_SIGN_ENTITLEMENTS = Xcode/MeetingAssistMacApp.entitlements;
ENABLE_HARDENED_RUNTIME = YES;
MARKETING_VERSION = 1.0;
CURRENT_PROJECT_VERSION = 15;
Assets.xcassets in Resources;
ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;
ASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;
Signing.xcconfig;
`
  );
  writeSigningFixture(appleDir, localTeam);
  const launchSchemePlist = includeLaunchScheme
    ? `  <key>CFBundleURLTypes</key>
  <array>
    <dict>
      <key>CFBundleURLName</key>
      <string>co.thebonfire.meetingassist</string>
      <key>CFBundleURLSchemes</key>
      <array>
        <string>meetingassist</string>
      </array>
    </dict>
  </array>
`
    : "";
  const infoBody = `<dict>
  <key>CFBundleShortVersionString</key>
  <string>$(MARKETING_VERSION)</string>
${launchSchemePlist}  <key>CFBundleVersion</key>
  <string>$(CURRENT_PROJECT_VERSION)</string>
  <key>NSCameraUsageDescription</key>
  <string>MeetingAssist uses the camera when you join a video room.</string>
  <key>NSMicrophoneUsageDescription</key>
  <string>MeetingAssist uses the microphone when you join a video room.</string>
</dict>`;
  writePlist(resolve(appleDir, "Xcode", "MeetingAssistAppleApp-Info.plist"), infoBody);
  writePlist(resolve(appleDir, "Xcode", "MeetingAssistMacApp-Info.plist"), infoBody);
  writePlist(
    resolve(appleDir, "Xcode", "MeetingAssistMacApp.entitlements"),
    `<dict>
  <key>com.apple.security.device.audio-input</key>
  <true/>
  <key>com.apple.security.device.camera</key>
  <true/>
</dict>`
  );
  if (includeIcons) {
    writeAppIconFixture(appleDir);
  }
  if (includePrivacy) {
    writePrivacyManifestFixture(appleDir, includePrivacy);
    wirePrivacyManifestFixture(appleDir);
  }
  return appleDir;
}

const defaultRepo = runReadiness();
assert.equal(defaultRepo.status, 0);
assert.equal(defaultRepo.output.ok, true);
assert.equal(defaultRepo.output.blockers.some((blocker) => blocker.id === "ios_app_icon"), false);
assert.equal(defaultRepo.output.blockers.some((blocker) => blocker.id === "mac_app_icon"), false);

const blockedFixturePath = makeFixture({ includeIcons: false, includePrivacy: false });
const blockedFixture = runReadiness(["--apple-dir", blockedFixturePath]);
assert.equal(blockedFixture.status, 0);
assert.equal(blockedFixture.output.ok, true);
assert.equal(blockedFixture.output.readyForDistribution, false);
assert.deepEqual(
  blockedFixture.output.blockers.map((blocker) => blocker.id).sort(),
  ["apple_development_team", "ios_app_icon", "mac_app_icon", "privacy_manifest", "release_evidence_file"]
);

const strictBlockedFixture = runReadiness(["--apple-dir", blockedFixturePath, "--strict"]);
assert.equal(strictBlockedFixture.status, 1);
assert.equal(strictBlockedFixture.output.ok, true);
assert.equal(strictBlockedFixture.output.readyForDistribution, false);

const readyFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(readyFixturePath, "ReleaseEvidence.local.json"));
const readyFixture = runReadiness(["--apple-dir", readyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(readyFixture.status, 0);
assert.equal(readyFixture.output.ok, true);
assert.equal(readyFixture.output.readyForDistribution, true);
assert.deepEqual(readyFixture.output.blockers, []);

const missingLaunchSchemeFixturePath = makeFixture({ includeIcons: true, includePrivacy: true, includeLaunchScheme: false });
writeReleaseEvidenceFixture(resolve(missingLaunchSchemeFixturePath, "ReleaseEvidence.local.json"));
const missingLaunchSchemeFixture = runReadiness(["--apple-dir", missingLaunchSchemeFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(missingLaunchSchemeFixture.status, 1);
assert.equal(missingLaunchSchemeFixture.output.ok, false);
assert.equal(
  missingLaunchSchemeFixture.output.checks.some(
    (check) => check.id === "app_launch_url_scheme" && !check.ok
  ),
  true
);

const localTeamFixturePath = makeFixture({
  includeIcons: true,
  includePrivacy: true,
  localTeam: syntheticTeamId("B1", "C2", "D3", "E4", "F5"),
});
writeReleaseEvidenceFixture(resolve(localTeamFixturePath, "ReleaseEvidence.local.json"));
const localTeamFixture = runReadiness(["--apple-dir", localTeamFixturePath, "--strict"]);
assert.equal(localTeamFixture.status, 0);
assert.equal(localTeamFixture.output.ok, true);
assert.equal(localTeamFixture.output.readyForDistribution, true);
assert.deepEqual(localTeamFixture.output.blockers, []);

const emptyPrivacyFixturePath = makeFixture({ includeIcons: true, includePrivacy: "empty" });
writeReleaseEvidenceFixture(resolve(emptyPrivacyFixturePath, "ReleaseEvidence.local.json"));
const emptyPrivacyFixture = runReadiness(["--apple-dir", emptyPrivacyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(emptyPrivacyFixture.status, 1);
assert.equal(emptyPrivacyFixture.output.ok, true);
assert.equal(emptyPrivacyFixture.output.readyForDistribution, false);
assert.equal(emptyPrivacyFixture.output.blockers.some((blocker) => blocker.id === "privacy_manifest"), true);

const incompletePrivacyFixturePath = makeFixture({ includeIcons: true, includePrivacy: "incomplete" });
writeReleaseEvidenceFixture(resolve(incompletePrivacyFixturePath, "ReleaseEvidence.local.json"));
const incompletePrivacyFixture = runReadiness(["--apple-dir", incompletePrivacyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(incompletePrivacyFixture.status, 1);
assert.equal(incompletePrivacyFixture.output.ok, true);
assert.equal(incompletePrivacyFixture.output.readyForDistribution, false);
assert.equal(incompletePrivacyFixture.output.blockers.some((blocker) => blocker.id === "privacy_manifest"), true);

const unwiredPrivacyFixturePath = makeFixture({ includeIcons: true, includePrivacy: false });
writePrivacyManifestFixture(unwiredPrivacyFixturePath, "complete");
writeReleaseEvidenceFixture(resolve(unwiredPrivacyFixturePath, "ReleaseEvidence.local.json"));
const unwiredPrivacyFixture = runReadiness(["--apple-dir", unwiredPrivacyFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(unwiredPrivacyFixture.status, 1);
assert.equal(unwiredPrivacyFixture.output.ok, true);
assert.equal(unwiredPrivacyFixture.output.readyForDistribution, false);
assert.equal(
  unwiredPrivacyFixture.output.blockers.some(
    (blocker) => blocker.id === "privacy_manifest" && blocker.detail.includes("project_yml_sources")
  ),
  true
);

const missingEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const missingEvidenceFixture = runReadiness(["--apple-dir", missingEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(missingEvidenceFixture.status, 1);
assert.equal(missingEvidenceFixture.output.ok, true);
assert.equal(missingEvidenceFixture.output.readyForDistribution, false);
assert.equal(missingEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_file"), true);

const invalidJsonEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeFixtureFile(resolve(invalidJsonEvidenceFixturePath, "ReleaseEvidence.local.json"), "{\n");
const invalidJsonEvidenceFixture = runReadiness(["--apple-dir", invalidJsonEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(invalidJsonEvidenceFixture.status, 1);
assert.equal(invalidJsonEvidenceFixture.output.ok, true);
assert.equal(invalidJsonEvidenceFixture.output.readyForDistribution, false);
assert.equal(invalidJsonEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_file"), true);

const arrayEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeFixtureFile(resolve(arrayEvidenceFixturePath, "ReleaseEvidence.local.json"), "[]\n");
const arrayEvidenceFixture = runReadiness(["--apple-dir", arrayEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(arrayEvidenceFixture.status, 1);
assert.equal(arrayEvidenceFixture.output.ok, true);
assert.equal(arrayEvidenceFixture.output.readyForDistribution, false);
assert.equal(arrayEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_file"), true);

const qaSnapshotEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeFixtureFile(
  resolve(qaSnapshotEvidenceFixturePath, "ReleaseEvidence.local.json"),
  `${JSON.stringify(
    {
      schemaVersion: 1,
      artifactType: "native_device_media",
      claimScope: "qa_snapshot",
      releaseEligible: false,
      status: "observed",
      runId: "native-release-run-20260629-a",
      roomId: "release-room-smoke-20260629",
      releaseEvidenceSummary: {
        status: "pending",
        mediaAssertions: {
          cameraPublished: true,
          microphonePublished: true,
          remoteAudioReceived: true,
          remoteVideoRendered: true,
        },
      },
    },
    null,
    2
  )}\n`
);
const qaSnapshotEvidenceFixture = runReadiness(["--apple-dir", qaSnapshotEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(qaSnapshotEvidenceFixture.status, 1);
assert.equal(qaSnapshotEvidenceFixture.output.ok, true);
assert.equal(qaSnapshotEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  qaSnapshotEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_schema"),
  true
);

const placeholderEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(placeholderEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  testFlight: { appStoreConnectBuildId: "<App Store Connect build ID>" },
});
const placeholderEvidenceFixture = runReadiness(["--apple-dir", placeholderEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(placeholderEvidenceFixture.status, 1);
assert.equal(placeholderEvidenceFixture.output.ok, true);
assert.equal(placeholderEvidenceFixture.output.readyForDistribution, false);
assert.equal(placeholderEvidenceFixture.output.blockers.some((blocker) => blocker.id === "testflight_evidence"), true);

const staleEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(staleEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  build: "14",
});
const staleEvidenceFixture = runReadiness(["--apple-dir", staleEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(staleEvidenceFixture.status, 1);
assert.equal(staleEvidenceFixture.output.ok, true);
assert.equal(staleEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  staleEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_version_build"),
  true
);

const partialDeviceEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(partialDeviceEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  physicalDeviceMedia: { ipad: null },
});
const partialDeviceEvidenceFixture = runReadiness(["--apple-dir", partialDeviceEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(partialDeviceEvidenceFixture.status, 1);
assert.equal(partialDeviceEvidenceFixture.output.ok, true);
assert.equal(partialDeviceEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  partialDeviceEvidenceFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const incompleteAssertionEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(incompleteAssertionEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  physicalDeviceMedia: {
    iphone: {
      status: "passed",
      runId: "native-release-run-20260629-a",
      roomId: "release-room-smoke-20260629",
      device: "iPhone 17 physical",
      os: "iOS 26.5",
      testedAt: "2026-06-29T12:00:00Z",
      artifactRef: "artifacts/native-release-run-20260629-a/iphone-media.json",
      mediaAssertions: {
        cameraPublished: true,
        microphonePublished: true,
        remoteAudioReceived: true,
        remoteVideoRendered: false,
      },
    },
  },
});
const incompleteAssertionEvidenceFixture = runReadiness(
  ["--apple-dir", incompleteAssertionEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(incompleteAssertionEvidenceFixture.status, 1);
assert.equal(incompleteAssertionEvidenceFixture.output.ok, true);
assert.equal(incompleteAssertionEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  incompleteAssertionEvidenceFixture.output.blockers.some(
    (blocker) => blocker.id === "physical_device_media_evidence"
  ),
  true
);

const qaSnapshotArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(qaSnapshotArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    iphone: {
      claimScope: "qa_snapshot",
      releaseEligible: false,
      status: "observed",
      releaseEvidenceSummary: { status: "pending" },
    },
  },
});
const qaSnapshotArtifactFixture = runReadiness(["--apple-dir", qaSnapshotArtifactFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(qaSnapshotArtifactFixture.status, 1);
assert.equal(qaSnapshotArtifactFixture.output.ok, true);
assert.equal(qaSnapshotArtifactFixture.output.readyForDistribution, false);
assert.equal(
  qaSnapshotArtifactFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const simulatorArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(simulatorArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    ipad: {
      device: { kind: "simulator", physical: false },
    },
  },
});
const simulatorArtifactFixture = runReadiness(["--apple-dir", simulatorArtifactFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(simulatorArtifactFixture.status, 1);
assert.equal(simulatorArtifactFixture.output.ok, true);
assert.equal(simulatorArtifactFixture.output.readyForDistribution, false);
assert.equal(
  simulatorArtifactFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const staleArtifactBuildFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(staleArtifactBuildFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    mac: {
      app: { build: "14" },
    },
  },
});
const staleArtifactBuildFixture = runReadiness(["--apple-dir", staleArtifactBuildFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(staleArtifactBuildFixture.status, 1);
assert.equal(staleArtifactBuildFixture.output.ok, true);
assert.equal(staleArtifactBuildFixture.output.readyForDistribution, false);
assert.equal(
  staleArtifactBuildFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const wrongArtifactRunFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(wrongArtifactRunFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    iphone: {
      runId: "native-release-run-other",
      releaseEvidenceSummary: { runId: "native-release-run-other" },
    },
  },
});
const wrongArtifactRunFixture = runReadiness(["--apple-dir", wrongArtifactRunFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(wrongArtifactRunFixture.status, 1);
assert.equal(wrongArtifactRunFixture.output.ok, true);
assert.equal(wrongArtifactRunFixture.output.readyForDistribution, false);
assert.equal(
  wrongArtifactRunFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const wrongArtifactPlatformFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(wrongArtifactPlatformFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    ipad: {
      platform: "iphone",
      app: { clientPlatform: "ios" },
    },
  },
});
const wrongArtifactPlatformFixture = runReadiness(["--apple-dir", wrongArtifactPlatformFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(wrongArtifactPlatformFixture.status, 1);
assert.equal(wrongArtifactPlatformFixture.output.ok, true);
assert.equal(wrongArtifactPlatformFixture.output.readyForDistribution, false);
assert.equal(
  wrongArtifactPlatformFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const mismatchedArtifactTimestampFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(mismatchedArtifactTimestampFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    mac: {
      capturedAt: "2026-06-29T13:20:00Z",
      releaseEvidenceSummary: { testedAt: "2026-06-29T13:20:00Z" },
    },
  },
});
const mismatchedArtifactTimestampFixture = runReadiness(
  ["--apple-dir", mismatchedArtifactTimestampFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(mismatchedArtifactTimestampFixture.status, 1);
assert.equal(mismatchedArtifactTimestampFixture.output.ok, true);
assert.equal(mismatchedArtifactTimestampFixture.output.readyForDistribution, false);
assert.equal(
  mismatchedArtifactTimestampFixture.output.blockers.some(
    (blocker) => blocker.id === "physical_device_media_evidence"
  ),
  true
);

const wrongAssertionSourceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(wrongAssertionSourceFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    iphone: {
      assertionEvidence: {
        cameraPublished: { source: "uiCameraToggle", value: 1, passed: true },
      },
    },
  },
});
const wrongAssertionSourceFixture = runReadiness(["--apple-dir", wrongAssertionSourceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(wrongAssertionSourceFixture.status, 1);
assert.equal(wrongAssertionSourceFixture.output.ok, true);
assert.equal(wrongAssertionSourceFixture.output.readyForDistribution, false);
assert.equal(
  wrongAssertionSourceFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const missingRendererEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(missingRendererEvidenceFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    mac: {
      renderer: null,
    },
  },
});
const missingRendererEvidenceFixture = runReadiness(
  ["--apple-dir", missingRendererEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(missingRendererEvidenceFixture.status, 1);
assert.equal(missingRendererEvidenceFixture.output.ok, true);
assert.equal(missingRendererEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  missingRendererEvidenceFixture.output.blockers.some(
    (blocker) => blocker.id === "physical_device_media_evidence"
  ),
  true
);

const placeholderDeviceArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(placeholderDeviceArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifacts: {
    iphone: { artifactRef: "artifacts/native-release-run-20260629-a/iphone-media.json", fixture: true },
  },
});
const placeholderDeviceArtifactFixture = runReadiness(
  ["--apple-dir", placeholderDeviceArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(placeholderDeviceArtifactFixture.status, 1);
assert.equal(placeholderDeviceArtifactFixture.output.ok, true);
assert.equal(placeholderDeviceArtifactFixture.output.readyForDistribution, false);
assert.equal(
  placeholderDeviceArtifactFixture.output.blockers.some(
    (blocker) => blocker.id === "physical_device_media_evidence"
  ),
  true
);

const unsafeDeviceArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(unsafeDeviceArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  physicalMediaArtifactOverrides: {
    mac: {
      diagnostics: {
        rawSdp: "v=0\r\na=candidate:842163049 1 udp 1677729535 192.168.1.25 56143 typ host\r\n",
        turnCredential: "secret-turn-password",
      },
    },
  },
});
const unsafeDeviceArtifactFixture = runReadiness(["--apple-dir", unsafeDeviceArtifactFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(unsafeDeviceArtifactFixture.status, 1);
assert.equal(unsafeDeviceArtifactFixture.output.ok, true);
assert.equal(unsafeDeviceArtifactFixture.output.readyForDistribution, false);
assert.equal(
  unsafeDeviceArtifactFixture.output.blockers.some((blocker) => blocker.id === "physical_device_media_evidence"),
  true
);

const nonRelayTurnEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(nonRelayTurnEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  restrictiveNetworkTurn: { relayProtocol: "stun", relayCandidateType: "host" },
});
const nonRelayTurnEvidenceFixture = runReadiness(["--apple-dir", nonRelayTurnEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(nonRelayTurnEvidenceFixture.status, 1);
assert.equal(nonRelayTurnEvidenceFixture.output.ok, true);
assert.equal(nonRelayTurnEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  nonRelayTurnEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const missingTurnArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(missingTurnArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  restrictiveNetworkTurn: { artifactRef: "" },
});
const missingTurnArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", missingTurnArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(missingTurnArtifactEvidenceFixture.status, 1);
assert.equal(missingTurnArtifactEvidenceFixture.output.ok, true);
assert.equal(missingTurnArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  missingTurnArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const missingLocalArtifactFilesFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(missingLocalArtifactFilesFixturePath, "ReleaseEvidence.local.json"), {}, {
  createArtifacts: false,
});
const missingLocalArtifactFilesFixture = runReadiness(
  ["--apple-dir", missingLocalArtifactFilesFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(missingLocalArtifactFilesFixture.status, 1);
assert.equal(missingLocalArtifactFilesFixture.output.ok, true);
assert.equal(missingLocalArtifactFilesFixture.output.readyForDistribution, false);
assert.equal(
  missingLocalArtifactFilesFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_artifacts"),
  true
);

const missingFileArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(missingFileArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  restrictiveNetworkTurn: {
    artifactRef: pathToFileURL(resolve(missingFileArtifactEvidenceFixturePath, "missing-turn-artifact.json")).href,
  },
});
const missingFileArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", missingFileArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(missingFileArtifactEvidenceFixture.status, 1);
assert.equal(missingFileArtifactEvidenceFixture.output.ok, true);
assert.equal(missingFileArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  missingFileArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_artifacts"),
  true
);

const existingFileArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const existingFileArtifactPath = resolve(existingFileArtifactEvidenceFixturePath, "turn-artifact.json");
const existingFileArtifactOverrides = {
  restrictiveNetworkTurn: {
    artifactRef: pathToFileURL(existingFileArtifactPath).href,
  },
};
writeReleaseEvidenceFixture(resolve(existingFileArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), existingFileArtifactOverrides);
writeFixtureFile(
  existingFileArtifactPath,
  `${JSON.stringify(promotedTurnArtifact(releaseEvidence(existingFileArtifactOverrides)), null, 2)}\n`
);
const existingFileArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", existingFileArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(existingFileArtifactEvidenceFixture.status, 0);
assert.equal(existingFileArtifactEvidenceFixture.output.ok, true);
assert.equal(existingFileArtifactEvidenceFixture.output.readyForDistribution, true);
assert.equal(
  existingFileArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_artifacts"),
  false
);

const placeholderTurnArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const placeholderTurnArtifactPath = resolve(placeholderTurnArtifactEvidenceFixturePath, "placeholder-turn-artifact.json");
writeFixtureFile(placeholderTurnArtifactPath, "{}\n");
writeReleaseEvidenceFixture(resolve(placeholderTurnArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  restrictiveNetworkTurn: {
    artifactRef: pathToFileURL(placeholderTurnArtifactPath).href,
  },
});
const placeholderTurnArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", placeholderTurnArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(placeholderTurnArtifactEvidenceFixture.status, 1);
assert.equal(placeholderTurnArtifactEvidenceFixture.output.ok, true);
assert.equal(placeholderTurnArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  placeholderTurnArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const nonJsonTurnArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const nonJsonTurnArtifactPath = resolve(nonJsonTurnArtifactEvidenceFixturePath, "turn-artifact.txt");
writeFixtureFile(nonJsonTurnArtifactPath, "selected relay proof lives elsewhere\n");
writeReleaseEvidenceFixture(resolve(nonJsonTurnArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  restrictiveNetworkTurn: {
    artifactRef: pathToFileURL(nonJsonTurnArtifactPath).href,
  },
});
const nonJsonTurnArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", nonJsonTurnArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(nonJsonTurnArtifactEvidenceFixture.status, 1);
assert.equal(nonJsonTurnArtifactEvidenceFixture.output.ok, true);
assert.equal(nonJsonTurnArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  nonJsonTurnArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const nonRelayTurnArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(nonRelayTurnArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {}, {
  turnArtifactOverrides: {
    selectedCandidate: {
      relayCandidateType: "relay",
      relayCandidateSelected: true,
      localCandidateType: "host",
      remoteCandidateType: "srflx",
      currentRoundTripTime: 0,
    },
  },
});
const nonRelayTurnArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", nonRelayTurnArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(nonRelayTurnArtifactEvidenceFixture.status, 1);
assert.equal(nonRelayTurnArtifactEvidenceFixture.output.ok, true);
assert.equal(nonRelayTurnArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  nonRelayTurnArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const staleTurnArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(staleTurnArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {}, {
  turnArtifactOverrides: {
    app: { build: "14" },
    network: "old restricted network",
    releaseEvidenceSummary: { network: "old restricted network" },
  },
});
const staleTurnArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", staleTurnArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(staleTurnArtifactEvidenceFixture.status, 1);
assert.equal(staleTurnArtifactEvidenceFixture.output.ok, true);
assert.equal(staleTurnArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  staleTurnArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const unsafeTurnArtifactEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(unsafeTurnArtifactEvidenceFixturePath, "ReleaseEvidence.local.json"), {}, {
  turnArtifactOverrides: {
    diagnostics: {
      rawIceCandidates: ["candidate:842163049 1 udp 1677729535 192.168.1.25 56143 typ host"],
      turnCredential: "secret-turn-password",
    },
  },
});
const unsafeTurnArtifactEvidenceFixture = runReadiness(
  ["--apple-dir", unsafeTurnArtifactEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(unsafeTurnArtifactEvidenceFixture.status, 1);
assert.equal(unsafeTurnArtifactEvidenceFixture.output.ok, true);
assert.equal(unsafeTurnArtifactEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  unsafeTurnArtifactEvidenceFixture.output.blockers.some((blocker) => blocker.id === "restrictive_turn_evidence"),
  true
);

const placeholderTestFlightArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(placeholderTestFlightArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  testFlightArtifact: { artifactRef: "artifacts/native-release-run-20260629-a/testflight-build.json", fixture: true },
});
const placeholderTestFlightArtifactFixture = runReadiness(
  ["--apple-dir", placeholderTestFlightArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(placeholderTestFlightArtifactFixture.status, 1);
assert.equal(placeholderTestFlightArtifactFixture.output.ok, true);
assert.equal(placeholderTestFlightArtifactFixture.output.readyForDistribution, false);
assert.equal(
  placeholderTestFlightArtifactFixture.output.blockers.some((blocker) => blocker.id === "testflight_evidence"),
  true
);

const nonJsonTestFlightArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const nonJsonTestFlightArtifactPath = resolve(nonJsonTestFlightArtifactFixturePath, "testflight-artifact.txt");
writeFixtureFile(nonJsonTestFlightArtifactPath, "uploaded in App Store Connect\n");
writeReleaseEvidenceFixture(resolve(nonJsonTestFlightArtifactFixturePath, "ReleaseEvidence.local.json"), {
  testFlight: { artifactRef: pathToFileURL(nonJsonTestFlightArtifactPath).href },
});
const nonJsonTestFlightArtifactFixture = runReadiness(
  ["--apple-dir", nonJsonTestFlightArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(nonJsonTestFlightArtifactFixture.status, 1);
assert.equal(nonJsonTestFlightArtifactFixture.output.ok, true);
assert.equal(nonJsonTestFlightArtifactFixture.output.readyForDistribution, false);
assert.equal(
  nonJsonTestFlightArtifactFixture.output.blockers.some((blocker) => blocker.id === "testflight_evidence"),
  true
);

const staleTestFlightArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(staleTestFlightArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  testFlightArtifactOverrides: {
    app: { build: "14" },
    appStoreConnect: { buildId: "asc-stale-build" },
    releaseEvidenceSummary: { appStoreConnectBuildId: "asc-stale-build" },
  },
});
const staleTestFlightArtifactFixture = runReadiness(
  ["--apple-dir", staleTestFlightArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(staleTestFlightArtifactFixture.status, 1);
assert.equal(staleTestFlightArtifactFixture.output.ok, true);
assert.equal(staleTestFlightArtifactFixture.output.readyForDistribution, false);
assert.equal(
  staleTestFlightArtifactFixture.output.blockers.some((blocker) => blocker.id === "testflight_evidence"),
  true
);

const unsafeTestFlightArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(unsafeTestFlightArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  testFlightArtifactOverrides: {
    uploadLog: "xcodebuild uploaded with API key /Users/example/AuthKey_ABC123DEFG.p8",
  },
});
const unsafeTestFlightArtifactFixture = runReadiness(
  ["--apple-dir", unsafeTestFlightArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(unsafeTestFlightArtifactFixture.status, 1);
assert.equal(unsafeTestFlightArtifactFixture.output.ok, true);
assert.equal(unsafeTestFlightArtifactFixture.output.readyForDistribution, false);
assert.equal(
  unsafeTestFlightArtifactFixture.output.blockers.some((blocker) => blocker.id === "testflight_evidence"),
  true
);

const placeholderNotarizationArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(placeholderNotarizationArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  notarizationArtifact: { artifactRef: "artifacts/native-release-run-20260629-a/notarization.json", fixture: true },
});
const placeholderNotarizationArtifactFixture = runReadiness(
  ["--apple-dir", placeholderNotarizationArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(placeholderNotarizationArtifactFixture.status, 1);
assert.equal(placeholderNotarizationArtifactFixture.output.ok, true);
assert.equal(placeholderNotarizationArtifactFixture.output.readyForDistribution, false);
assert.equal(
  placeholderNotarizationArtifactFixture.output.blockers.some((blocker) => blocker.id === "mac_notarization_evidence"),
  true
);

const nonJsonNotarizationArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const nonJsonNotarizationArtifactPath = resolve(nonJsonNotarizationArtifactFixturePath, "notarization-artifact.txt");
writeFixtureFile(nonJsonNotarizationArtifactPath, "notarytool accepted\n");
writeReleaseEvidenceFixture(resolve(nonJsonNotarizationArtifactFixturePath, "ReleaseEvidence.local.json"), {
  macNotarization: { artifactRef: pathToFileURL(nonJsonNotarizationArtifactPath).href },
});
const nonJsonNotarizationArtifactFixture = runReadiness(
  ["--apple-dir", nonJsonNotarizationArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(nonJsonNotarizationArtifactFixture.status, 1);
assert.equal(nonJsonNotarizationArtifactFixture.output.ok, true);
assert.equal(nonJsonNotarizationArtifactFixture.output.readyForDistribution, false);
assert.equal(
  nonJsonNotarizationArtifactFixture.output.blockers.some((blocker) => blocker.id === "mac_notarization_evidence"),
  true
);

const rejectedNotarizationArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(rejectedNotarizationArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  notarizationArtifactOverrides: {
    staple: { stapled: false, validated: false },
    gatekeeper: { assessment: "rejected" },
    releaseEvidenceSummary: { stapled: false },
  },
});
const rejectedNotarizationArtifactFixture = runReadiness(
  ["--apple-dir", rejectedNotarizationArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(rejectedNotarizationArtifactFixture.status, 1);
assert.equal(rejectedNotarizationArtifactFixture.output.ok, true);
assert.equal(rejectedNotarizationArtifactFixture.output.readyForDistribution, false);
assert.equal(
  rejectedNotarizationArtifactFixture.output.blockers.some((blocker) => blocker.id === "mac_notarization_evidence"),
  true
);

const missingHashNotarizationArtifactFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(missingHashNotarizationArtifactFixturePath, "ReleaseEvidence.local.json"), {}, {
  notarizationArtifactOverrides: {
    distributionArtifact: { sha256: "" },
  },
});
const missingHashNotarizationArtifactFixture = runReadiness(
  ["--apple-dir", missingHashNotarizationArtifactFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(missingHashNotarizationArtifactFixture.status, 1);
assert.equal(missingHashNotarizationArtifactFixture.output.ok, true);
assert.equal(missingHashNotarizationArtifactFixture.output.readyForDistribution, false);
assert.equal(
  missingHashNotarizationArtifactFixture.output.blockers.some((blocker) => blocker.id === "mac_notarization_evidence"),
  true
);

const unstapledNotarizationEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(unstapledNotarizationEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  macNotarization: { stapled: false },
});
const unstapledNotarizationEvidenceFixture = runReadiness(
  ["--apple-dir", unstapledNotarizationEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(unstapledNotarizationEvidenceFixture.status, 1);
assert.equal(unstapledNotarizationEvidenceFixture.output.ok, true);
assert.equal(unstapledNotarizationEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  unstapledNotarizationEvidenceFixture.output.blockers.some((blocker) => blocker.id === "mac_notarization_evidence"),
  true
);

const missingNotaryRequestEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(missingNotaryRequestEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  macNotarization: { requestId: "" },
});
const missingNotaryRequestEvidenceFixture = runReadiness(
  ["--apple-dir", missingNotaryRequestEvidenceFixturePath, "--strict"],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(missingNotaryRequestEvidenceFixture.status, 1);
assert.equal(missingNotaryRequestEvidenceFixture.output.ok, true);
assert.equal(missingNotaryRequestEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  missingNotaryRequestEvidenceFixture.output.blockers.some((blocker) => blocker.id === "mac_notarization_evidence"),
  true
);

const secretKeyEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(secretKeyEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  turnPassword: "redacted but should not be here",
});
const secretKeyEvidenceFixture = runReadiness(["--apple-dir", secretKeyEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(secretKeyEvidenceFixture.status, 1);
assert.equal(secretKeyEvidenceFixture.output.ok, true);
assert.equal(secretKeyEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  secretKeyEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_secret_safety"),
  true
);

const teamIdValueEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(teamIdValueEvidenceFixturePath, "ReleaseEvidence.local.json"), {
  testFlight: { appStoreConnectBuildId: syntheticTeamId("A1", "B2", "C3", "D4", "E5") },
});
const teamIdValueEvidenceFixture = runReadiness(["--apple-dir", teamIdValueEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("B1", "C2", "D3", "E4", "F5"),
});
assert.equal(teamIdValueEvidenceFixture.status, 1);
assert.equal(teamIdValueEvidenceFixture.output.ok, true);
assert.equal(teamIdValueEvidenceFixture.output.readyForDistribution, false);
assert.equal(
  teamIdValueEvidenceFixture.output.blockers.some((blocker) => blocker.id === "release_evidence_secret_safety"),
  true
);

const explicitEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const explicitEvidencePath = resolve(dirname(explicitEvidenceFixturePath), "ExternalReleaseEvidence.json");
writeReleaseEvidenceFixture(explicitEvidencePath);
const explicitEvidenceFixture = runReadiness(
  ["--apple-dir", explicitEvidenceFixturePath, "--strict", "--evidence-file", explicitEvidencePath],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(explicitEvidenceFixture.status, 0);
assert.equal(explicitEvidenceFixture.output.ok, true);
assert.equal(explicitEvidenceFixture.output.readyForDistribution, true);

const trackedEvidenceFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(trackedEvidenceFixturePath, "ReleaseEvidence.json"));
const trackedEvidenceFixture = runReadiness(["--apple-dir", trackedEvidenceFixturePath, "--strict"], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(trackedEvidenceFixture.status, 0);
assert.equal(trackedEvidenceFixture.output.ok, true);
assert.equal(trackedEvidenceFixture.output.readyForDistribution, true);

const flagAsEvidenceValueFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
const flagAsEvidenceValueFixture = runReadiness([
  "--apple-dir",
  flagAsEvidenceValueFixturePath,
  "--evidence-file",
  "--strict",
]);
assert.equal(flagAsEvidenceValueFixture.status, 1);
assert.equal(flagAsEvidenceValueFixture.output.ok, false);
assert.match(flagAsEvidenceValueFixture.output.error, /--evidence-file requires a path/);

const committedTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(committedTeamFixturePath, "ReleaseEvidence.local.json"));
appendFileSync(
  resolve(committedTeamFixturePath, "MeetingAssist.xcodeproj", "project.pbxproj"),
  `DEVELOPMENT_TEAM = ${syntheticTeamId("C1", "D2", "E3", "F4", "G5")};\n`
);
const committedTeamFixture = runReadiness(["--apple-dir", committedTeamFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(committedTeamFixture.status, 1);
assert.equal(committedTeamFixture.output.ok, false);
assert.equal(
  committedTeamFixture.output.checks.some((check) => check.id === "no_committed_development_team" && !check.ok),
  true
);

const committedXcodeTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(committedXcodeTeamFixturePath, "ReleaseEvidence.local.json"));
appendFileSync(
  resolve(committedXcodeTeamFixturePath, "MeetingAssist.xcodeproj", "project.pbxproj"),
  `DevelopmentTeam = ${syntheticTeamId("D1", "E2", "F3", "G4", "H5")};\n`
);
const committedXcodeTeamFixture = runReadiness(["--apple-dir", committedXcodeTeamFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(committedXcodeTeamFixture.status, 1);
assert.equal(committedXcodeTeamFixture.output.ok, false);
assert.equal(
  committedXcodeTeamFixture.output.checks.some((check) => check.id === "no_committed_development_team" && !check.ok),
  true
);

const committedXcconfigTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(committedXcconfigTeamFixturePath, "ReleaseEvidence.local.json"));
writeSigningFixture(committedXcconfigTeamFixturePath, "");
writeFixtureFile(
  resolve(committedXcconfigTeamFixturePath, "Config", "Signing.xcconfig"),
  `CODE_SIGN_STYLE = Automatic
DEVELOPMENT_TEAM = ${syntheticTeamId("E1", "F2", "G3", "H4", "J5")}
DEVELOPMENT_TEAM = $(APPLE_DEVELOPMENT_TEAM)
#include? "Signing.local.xcconfig"
`
);
const committedXcconfigTeamFixture = runReadiness(["--apple-dir", committedXcconfigTeamFixturePath], {
  DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5"),
});
assert.equal(committedXcconfigTeamFixture.status, 1);
assert.equal(committedXcconfigTeamFixture.output.ok, false);
assert.equal(
  committedXcconfigTeamFixture.output.checks.some(
    (check) => check.id === "no_committed_development_team" && !check.ok
  ),
  true
);

const committedXcconfigTrailingTeamFixturePath = makeFixture({ includeIcons: true, includePrivacy: true });
writeReleaseEvidenceFixture(resolve(committedXcconfigTrailingTeamFixturePath, "ReleaseEvidence.local.json"));
appendFileSync(
  resolve(committedXcconfigTrailingTeamFixturePath, "Config", "Signing.xcconfig"),
  `DEVELOPMENT_TEAM = ${syntheticTeamId("F1", "G2", "H3", "J4", "K5")}\n`
);
const committedXcconfigTrailingTeamFixture = runReadiness(
  ["--apple-dir", committedXcconfigTrailingTeamFixturePath],
  { DEVELOPMENT_TEAM: syntheticTeamId("A1", "B2", "C3", "D4", "E5") }
);
assert.equal(committedXcconfigTrailingTeamFixture.status, 1);
assert.equal(committedXcconfigTrailingTeamFixture.output.ok, false);
assert.equal(
  committedXcconfigTrailingTeamFixture.output.checks.some(
    (check) => check.id === "no_committed_development_team" && !check.ok
  ),
  true
);

console.log("native-apple-release-readiness: 55 checks passed");
