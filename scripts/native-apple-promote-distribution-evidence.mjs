#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { isIP } from "node:net";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const testFlightStatuses = new Set(["ready", "uploaded", "processing", "accepted"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-promote-distribution-evidence.mjs --proofpack-dir path",
    "    --kind testflight|notarization|app-review --input path --confirm-current-build",
    "    [--confirm-app-store-connect-upload] [--confirm-no-secrets]",
    "    [--confirm-review-metadata-complete] [--confirm-app-privacy-complete]",
    "    [--confirm-external-testing-ready]",
    "    [--confirm-developer-id-archive] [--confirm-notary-accepted]",
    "    [--confirm-stapled-app] [--confirm-gatekeeper-accepted]",
    "    [--promoted-at iso] [--force]",
    "",
    "Promotes one sanitized App Store Connect/TestFlight upload, App Store",
    "review metadata, or macOS notarization observation into the matching",
    "proof-pack artifact and updates",
    "ReleaseEvidence.draft.json. The command rejects secret-bearing upload logs,",
    "API keys, provisioning profiles, certificates, private keys, and mismatched",
    "build or run evidence.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    kind: "",
    input: "",
    promotedAt: "",
    confirmAppleUpload: false,
    confirmReviewMetadataComplete: false,
    confirmAppPrivacyComplete: false,
    confirmExternalTestingReady: false,
    confirmNoSecrets: false,
    confirmDeveloperIdArchive: false,
    confirmNotarizedApp: false,
    confirmStapledApp: false,
    confirmGatekeeperAccepted: false,
    confirmCurrentBuild: false,
    force: false,
    help: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--proofpack-dir") {
      args.proofpackDir = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--kind") {
      args.kind = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--input") {
      args.input = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--promoted-at") {
      args.promotedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-apple-upload" || arg === "--confirm-app-store-connect-upload") {
      args.confirmAppleUpload = true;
    } else if (arg === "--confirm-review-metadata-complete") {
      args.confirmReviewMetadataComplete = true;
    } else if (arg === "--confirm-app-privacy-complete") {
      args.confirmAppPrivacyComplete = true;
    } else if (arg === "--confirm-external-testing-ready") {
      args.confirmExternalTestingReady = true;
    } else if (arg === "--confirm-no-secrets") {
      args.confirmNoSecrets = true;
    } else if (arg === "--confirm-developer-id-archive") {
      args.confirmDeveloperIdArchive = true;
    } else if (arg === "--confirm-notary-accepted") {
      args.confirmNotarizedApp = true;
    } else if (arg === "--confirm-notarized-app") {
      args.confirmNotarizedApp = true;
    } else if (arg === "--confirm-stapled-app") {
      args.confirmStapledApp = true;
    } else if (arg === "--confirm-gatekeeper-accepted") {
      args.confirmGatekeeperAccepted = true;
    } else if (arg === "--confirm-current-build") {
      args.confirmCurrentBuild = true;
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

function normalizedStatus(value) {
  return String(value ?? "").trim().toLowerCase();
}

function isPrivateIPv4(hostname) {
  const parts = hostname.split(".").map((part) => Number(part));
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) {
    return false;
  }
  const [first, second] = parts;
  return first === 10 || first === 127 || (first === 172 && second >= 16 && second <= 31) || (first === 192 && second === 168);
}

function validPublicHttpsURL(value) {
  if (!nonPlaceholderString(value)) {
    return false;
  }
  try {
    const url = new URL(String(value).trim());
    const hostname = url.hostname.toLowerCase();
    const ipHostname = hostname.replace(/^\[|\]$/g, "");
    if (url.protocol !== "https:" || !hostname || url.username || url.password) {
      return false;
    }
    if (hostname === "localhost" || hostname.endsWith(".localhost") || hostname.endsWith(".local")) {
      return false;
    }
    const ipVersion = isIP(ipHostname);
    if (ipVersion === 4 && isPrivateIPv4(ipHostname)) {
      return false;
    }
    if (ipVersion === 6 && (ipHostname === "::1" || ipHostname.startsWith("fc") || ipHostname.startsWith("fd") || ipHostname.startsWith("fe80"))) {
      return false;
    }
    return true;
  } catch {
    return false;
  }
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
        /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\bAKIA[0-9A-Z]{16}\b/.test(trimmed) ||
        /\b(?=[A-Z0-9]*[A-Z])[A-Z0-9]{10}\b/.test(trimmed) ||
        /\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
        /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(trimmed) ||
        /\/Users\/[^/\s]+/i.test(trimmed) ||
        /Developer ID (Application|Installer):.+\([A-Z0-9]{10}\)/.test(trimmed) ||
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
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|issuer_?id|key_?id|keychain|profile|authorization|jwt|apple_?id|username|raw(Log|Output)|uploadLog|log|stdout|stderr|command|args|env|notarytool(Output|Log)?|altool(Output|Log)?|codesign(Output|Log)?|spctl(Output|Log)?|headers?|cookies?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function artifactRefToPath(ref, label) {
  if (typeof ref !== "string" || !ref.startsWith("artifacts/") || ref.split("/").includes("..")) {
    throw new Error(`${label} artifactRef must be a repo-local artifacts/ path: ${ref}`);
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

function validateSharedObservation(observation, draft, args, expectedType, expectedStatus, timestampKey) {
  const problems = [];
  if (!observation || typeof observation !== "object" || Array.isArray(observation)) {
    return ["input:not_object"];
  }
  if (observation.schemaVersion !== 1) {
    problems.push("schemaVersion");
  }
  if (observation.artifactType !== expectedType) {
    problems.push("artifactType");
  }
  if (observation.status !== expectedStatus) {
    problems.push("status");
  }
  if (!validTimestamp(observation[timestampKey])) {
    problems.push(timestampKey);
  }
  if (!args.confirmCurrentBuild) {
    problems.push("confirmCurrentBuild");
  }
  const runId = String(observation.runId ?? "").trim();
  if (!runId) {
    problems.push("runId:empty");
  } else if (runId !== draft.runId) {
    problems.push("runId");
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
    if (!nonPlaceholderString(observation.app.bundleIdentifier)) {
      problems.push("app.bundleIdentifier");
    }
  }
  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    problems.push(`unsafeContent:${unsafe.slice(0, 4).join("|")}`);
  }
  return problems;
}

function validateTestFlightObservation(observation, draft, args) {
  const problems = validateSharedObservation(
    observation,
    draft,
    args,
    "native_testflight_upload_observation",
    "observed",
    "uploadedAt"
  );
  problems.push(
    ...collectUnexpectedKeys(
      observation,
      ["schemaVersion", "artifactType", "status", "runId", "uploadedAt", "app", "appStoreConnect"],
      "input"
    ),
    ...collectUnexpectedKeys(observation?.app, ["version", "build", "target", "clientPlatform", "bundleIdentifier"], "input.app"),
    ...collectUnexpectedKeys(observation?.appStoreConnect, ["buildId", "processingStatus"], "input.appStoreConnect")
  );
  if (!args.confirmAppleUpload) {
    problems.push("confirmAppleUpload");
  }
  if (!args.confirmNoSecrets) {
    problems.push("confirmNoSecrets");
  }
  if (observation?.app && typeof observation.app === "object" && !Array.isArray(observation.app)) {
    if (observation.app.target !== "MeetingAssistAppleApp") {
      problems.push("app.target");
    }
    if (!["ios", "ipados"].includes(String(observation.app.clientPlatform ?? "").trim())) {
      problems.push("app.clientPlatform");
    }
  }
  const appStoreConnect = observation?.appStoreConnect;
  if (!appStoreConnect || typeof appStoreConnect !== "object" || Array.isArray(appStoreConnect)) {
    problems.push("appStoreConnect");
  } else {
    const processingStatus = normalizedStatus(appStoreConnect.processingStatus);
    if (!nonPlaceholderString(appStoreConnect.buildId)) {
      problems.push("appStoreConnect.buildId");
    }
    if (!testFlightStatuses.has(processingStatus)) {
      problems.push("appStoreConnect.processingStatus");
    }
  }
  return problems;
}

function validateAppStoreReviewObservation(observation, draft, args) {
  const problems = validateSharedObservation(
    observation,
    draft,
    args,
    "native_app_store_review_metadata_observation",
    "observed",
    "reviewedAt"
  );
  problems.push(
    ...collectUnexpectedKeys(
      observation,
      ["schemaVersion", "artifactType", "status", "runId", "reviewedAt", "app", "metadata"],
      "input"
    ),
    ...collectUnexpectedKeys(observation?.app, ["version", "build", "target", "clientPlatform", "bundleIdentifier"], "input.app"),
    ...collectUnexpectedKeys(
      observation?.metadata,
      [
        "supportURL",
        "privacyPolicyURL",
        "descriptionReady",
        "keywordsReady",
        "screenshotsReady",
        "appPrivacyReady",
        "ageRatingComplete",
        "exportComplianceComplete",
        "testInformationReady",
        "externalTestingGroupReady",
      ],
      "input.metadata"
    )
  );
  if (!args.confirmReviewMetadataComplete) {
    problems.push("confirmReviewMetadataComplete");
  }
  if (!args.confirmAppPrivacyComplete) {
    problems.push("confirmAppPrivacyComplete");
  }
  if (!args.confirmExternalTestingReady) {
    problems.push("confirmExternalTestingReady");
  }
  if (!args.confirmNoSecrets) {
    problems.push("confirmNoSecrets");
  }
  if (observation?.app && typeof observation.app === "object" && !Array.isArray(observation.app)) {
    if (observation.app.target !== "MeetingAssistAppleApp") {
      problems.push("app.target");
    }
    if (!["ios", "ipados"].includes(String(observation.app.clientPlatform ?? "").trim())) {
      problems.push("app.clientPlatform");
    }
  }
  const metadata = observation?.metadata;
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) {
    problems.push("metadata");
  } else {
    if (!validPublicHttpsURL(metadata.supportURL)) {
      problems.push("metadata.supportURL");
    }
    if (!validPublicHttpsURL(metadata.privacyPolicyURL)) {
      problems.push("metadata.privacyPolicyURL");
    }
    for (const key of [
      "descriptionReady",
      "keywordsReady",
      "screenshotsReady",
      "appPrivacyReady",
      "ageRatingComplete",
      "exportComplianceComplete",
      "testInformationReady",
      "externalTestingGroupReady",
    ]) {
      if (metadata[key] !== true) {
        problems.push(`metadata.${key}`);
      }
    }
  }
  return problems;
}

function validateNotarizationObservation(observation, draft, args) {
  const problems = validateSharedObservation(
    observation,
    draft,
    args,
    "native_macos_notarization_observation",
    "accepted",
    "checkedAt"
  );
  if (!args.confirmDeveloperIdArchive) {
    problems.push("confirmDeveloperIdArchive");
  }
  if (!args.confirmNotarizedApp) {
    problems.push("confirmNotarizedApp");
  }
  if (!args.confirmStapledApp) {
    problems.push("confirmStapledApp");
  }
  if (!args.confirmGatekeeperAccepted) {
    problems.push("confirmGatekeeperAccepted");
  }
  if (observation?.app && typeof observation.app === "object" && !Array.isArray(observation.app)) {
    if (observation.app.target !== "MeetingAssistMacApp") {
      problems.push("app.target");
    }
    if (observation.app.clientPlatform !== "macos") {
      problems.push("app.clientPlatform");
    }
  }
  const distributionArtifact = observation?.distributionArtifact;
  if (!distributionArtifact || typeof distributionArtifact !== "object" || Array.isArray(distributionArtifact)) {
    problems.push("distributionArtifact");
  } else {
    if (!["zip", "dmg", "pkg", "app"].includes(String(distributionArtifact.kind ?? "").trim())) {
      problems.push("distributionArtifact.kind");
    }
    if (!nonPlaceholderString(distributionArtifact.filename) || String(distributionArtifact.filename).includes("/")) {
      problems.push("distributionArtifact.filename");
    }
    if (!/^[a-f0-9]{64}$/i.test(String(distributionArtifact.sha256 ?? "").trim())) {
      problems.push("distributionArtifact.sha256");
    }
  }
  const signing = observation?.signing;
  if (!signing || typeof signing !== "object" || Array.isArray(signing)) {
    problems.push("signing");
  } else {
    if (signing.style !== "developer_id") {
      problems.push("signing.style");
    }
    if (signing.signed !== true) {
      problems.push("signing.signed");
    }
    if (signing.hardenedRuntime !== true) {
      problems.push("signing.hardenedRuntime");
    }
    if (signing.timestamped !== true) {
      problems.push("signing.timestamped");
    }
  }
  const notarization = observation?.notarization;
  if (!notarization || typeof notarization !== "object" || Array.isArray(notarization)) {
    problems.push("notarization");
  } else {
    if (!nonPlaceholderString(notarization.requestId)) {
      problems.push("notarization.requestId");
    }
    if (notarization.status !== "accepted") {
      problems.push("notarization.status");
    }
    if (Number(notarization.issueCount) !== 0) {
      problems.push("notarization.issueCount");
    }
  }
  const staple = observation?.staple;
  if (!staple || typeof staple !== "object" || Array.isArray(staple)) {
    problems.push("staple");
  } else {
    if (staple.stapled !== true) {
      problems.push("staple.stapled");
    }
    if (staple.validated !== true) {
      problems.push("staple.validated");
    }
  }
  const gatekeeper = observation?.gatekeeper;
  if (!gatekeeper || typeof gatekeeper !== "object" || Array.isArray(gatekeeper)) {
    problems.push("gatekeeper");
  } else {
    if (gatekeeper.assessment !== "accepted") {
      problems.push("gatekeeper.assessment");
    }
    if (!nonPlaceholderString(gatekeeper.source)) {
      problems.push("gatekeeper.source");
    }
  }
  return problems;
}

function promotedTestFlightArtifact(observation, draft, item, promotedAt, inputPath) {
  const processingStatus = normalizedStatus(observation.appStoreConnect.processingStatus);
  const buildId = String(observation.appStoreConnect.buildId ?? "").trim();
  return {
    schemaVersion: 1,
    artifactType: "native_testflight_upload",
    claimScope: "app_store_connect_upload",
    releaseEligible: true,
    status: processingStatus,
    runId: draft.runId,
    uploadedAt: observation.uploadedAt,
    app: {
      version: String(observation.app.version ?? "").trim(),
      build: String(observation.app.build ?? "").trim(),
      target: "MeetingAssistAppleApp",
      clientPlatform: String(observation.app.clientPlatform ?? "").trim(),
      bundleIdentifier: String(observation.app.bundleIdentifier ?? "").trim(),
    },
    appStoreConnect: {
      buildId,
      processingStatus,
    },
    releaseEvidenceSummary: {
      status: processingStatus,
      runId: draft.runId,
      version: draft.version,
      build: draft.build,
      appStoreConnectBuildId: buildId,
      uploadedAt: observation.uploadedAt,
    },
    promotion: {
      promotedAt,
      sourceArtifactType: observation.artifactType,
      sourceStatus: observation.status,
      sourceRunId: String(observation.runId ?? "").trim(),
      sourceUploadedAt: observation.uploadedAt,
      sourceArtifact: repoSafeSourceLabel(inputPath),
      operatorConfirmedAppStoreConnectUpload: true,
      operatorConfirmedNoSecrets: true,
      operatorConfirmedCurrentBuild: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
}

function promotedAppStoreReviewArtifact(observation, draft, item, promotedAt, inputPath) {
  const metadata = observation.metadata;
  return {
    schemaVersion: 1,
    artifactType: "native_app_store_review_metadata",
    claimScope: "app_store_external_testing_review",
    releaseEligible: true,
    status: "ready",
    runId: draft.runId,
    reviewedAt: observation.reviewedAt,
    app: {
      version: String(observation.app.version ?? "").trim(),
      build: String(observation.app.build ?? "").trim(),
      target: "MeetingAssistAppleApp",
      clientPlatform: String(observation.app.clientPlatform ?? "").trim(),
      bundleIdentifier: String(observation.app.bundleIdentifier ?? "").trim(),
    },
    metadata: {
      supportURL: String(metadata.supportURL ?? "").trim(),
      privacyPolicyURL: String(metadata.privacyPolicyURL ?? "").trim(),
      descriptionReady: true,
      keywordsReady: true,
      screenshotsReady: true,
      appPrivacyReady: true,
      ageRatingComplete: true,
      exportComplianceComplete: true,
      testInformationReady: true,
      externalTestingGroupReady: true,
    },
    releaseEvidenceSummary: {
      status: "ready",
      runId: draft.runId,
      version: draft.version,
      build: draft.build,
      reviewedAt: observation.reviewedAt,
      supportURL: String(metadata.supportURL ?? "").trim(),
      privacyPolicyURL: String(metadata.privacyPolicyURL ?? "").trim(),
      externalTestingReady: true,
    },
    promotion: {
      promotedAt,
      sourceArtifactType: observation.artifactType,
      sourceStatus: observation.status,
      sourceRunId: String(observation.runId ?? "").trim(),
      sourceReviewedAt: observation.reviewedAt,
      sourceArtifact: repoSafeSourceLabel(inputPath),
      operatorConfirmedReviewMetadataComplete: true,
      operatorConfirmedAppPrivacyComplete: true,
      operatorConfirmedExternalTestingReady: true,
      operatorConfirmedNoSecrets: true,
      operatorConfirmedCurrentBuild: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
}

function promotedNotarizationArtifact(observation, draft, item, promotedAt, inputPath) {
  const requestId = String(observation.notarization.requestId ?? "").trim();
  return {
    schemaVersion: 1,
    artifactType: "native_macos_notarization",
    claimScope: "macos_notarization",
    releaseEligible: true,
    status: "accepted",
    runId: draft.runId,
    checkedAt: observation.checkedAt,
    distributionArtifact: {
      kind: String(observation.distributionArtifact.kind ?? "").trim(),
      filename: basename(String(observation.distributionArtifact.filename ?? "").trim()),
      sha256: String(observation.distributionArtifact.sha256 ?? "").trim().toLowerCase(),
    },
    app: {
      version: String(observation.app.version ?? "").trim(),
      build: String(observation.app.build ?? "").trim(),
      target: "MeetingAssistMacApp",
      clientPlatform: "macos",
      bundleIdentifier: String(observation.app.bundleIdentifier ?? "").trim(),
    },
    signing: {
      style: "developer_id",
      signed: true,
      hardenedRuntime: true,
      timestamped: true,
    },
    notarization: {
      requestId,
      status: "accepted",
      issueCount: 0,
    },
    staple: {
      stapled: true,
      validated: true,
    },
    gatekeeper: {
      assessment: "accepted",
      source: String(observation.gatekeeper.source ?? "").trim(),
    },
    releaseEvidenceSummary: {
      status: "accepted",
      runId: draft.runId,
      version: draft.version,
      build: draft.build,
      requestId,
      stapled: true,
      checkedAt: observation.checkedAt,
    },
    promotion: {
      promotedAt,
      sourceArtifactType: observation.artifactType,
      sourceStatus: observation.status,
      sourceRunId: String(observation.runId ?? "").trim(),
      sourceCheckedAt: observation.checkedAt,
      sourceArtifact: repoSafeSourceLabel(inputPath),
      operatorConfirmedDeveloperIdArchive: true,
      operatorConfirmedNotaryAccepted: true,
      operatorConfirmedStapledApp: true,
      operatorConfirmedGatekeeperAccepted: true,
      operatorConfirmedCurrentBuild: true,
      releaseEvidenceArtifactRef: item.artifactRef,
    },
  };
}

function kindConfig(kind) {
  if (kind === "testflight") {
    return {
      itemKey: "testFlight",
      artifactKey: "testFlight",
      label: "testFlight",
      validate: validateTestFlightObservation,
      promote: promotedTestFlightArtifact,
      alreadyPassed(item) {
        return ["ready", "uploaded", "processing", "accepted"].includes(String(item.status ?? "").trim());
      },
      updateDraft(item, artifact) {
        return {
          ...item,
          status: artifact.status,
          appStoreConnectBuildId: artifact.appStoreConnect.buildId,
          uploadedAt: artifact.uploadedAt,
          artifactRef: item.artifactRef,
        };
      },
    };
  }
  if (kind === "app-review") {
    return {
      itemKey: "appStoreReview",
      artifactKey: "appStoreReview",
      label: "appStoreReview",
      validate: validateAppStoreReviewObservation,
      promote: promotedAppStoreReviewArtifact,
      alreadyPassed(item) {
        return item.status === "ready";
      },
      updateDraft(item, artifact) {
        return {
          ...item,
          status: "ready",
          reviewedAt: artifact.reviewedAt,
          supportURL: artifact.metadata.supportURL,
          privacyPolicyURL: artifact.metadata.privacyPolicyURL,
          descriptionReady: true,
          keywordsReady: true,
          screenshotsReady: true,
          appPrivacyReady: true,
          ageRatingComplete: true,
          exportComplianceComplete: true,
          testInformationReady: true,
          externalTestingGroupReady: true,
          artifactRef: item.artifactRef,
        };
      },
    };
  }
  if (kind === "notarization") {
    return {
      itemKey: "macNotarization",
      artifactKey: "notarization",
      label: "macNotarization",
      validate: validateNotarizationObservation,
      promote: promotedNotarizationArtifact,
      alreadyPassed(item) {
        return item.status === "accepted";
      },
      updateDraft(item, artifact) {
        return {
          ...item,
          status: "accepted",
          requestId: artifact.notarization.requestId,
          stapled: artifact.staple.stapled,
          checkedAt: artifact.checkedAt,
          artifactRef: item.artifactRef,
        };
      },
    };
  }
  throw new Error("--kind must be testflight, app-review, or notarization.");
}

function promote(args) {
  if (!args.proofpackDir) {
    throw new Error("--proofpack-dir is required.");
  }
  if (!args.kind) {
    throw new Error("--kind is required.");
  }
  if (!args.input) {
    throw new Error("--input is required.");
  }
  const config = kindConfig(args.kind);
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
    throw new Error(`Missing input distribution observation: ${inputPath}`);
  }

  const draft = readJSON(draftPath);
  const proofpack = readJSON(proofpackPath);
  const item = draft[config.itemKey];
  if (!item || typeof item !== "object") {
    throw new Error(`ReleaseEvidence.draft.json is missing ${config.itemKey}.`);
  }
  for (const key of ["version", "build", "runId", "roomId"]) {
    if (String(proofpack[key] ?? "") !== String(draft[key] ?? "")) {
      throw new Error(`proofpack.json ${key} does not match ReleaseEvidence.draft.json.`);
    }
  }
  if (proofpack.evidenceArtifacts?.[config.artifactKey] !== item.artifactRef) {
    throw new Error(`proofpack.json evidenceArtifacts.${config.artifactKey} does not match ReleaseEvidence.draft.json.`);
  }
  if (config.alreadyPassed(item) && !args.force) {
    throw new Error(`${config.label} evidence is already passed. Use --force to replace it.`);
  }

  const observation = readJSON(inputPath);
  const problems = config.validate(observation, draft, args);
  if (problems.length > 0) {
    throw new Error(`Input ${config.label} observation cannot be promoted. Invalid: ${[...new Set(problems)].slice(0, 10).join(", ")}`);
  }

  const artifactPath = artifactRefToPath(item.artifactRef, config.label);
  const relativeArtifactDir = relative(proofpackDir, artifactPath);
  if (relativeArtifactDir.startsWith("..")) {
    throw new Error(`Target ${config.label} artifact must stay inside the proof pack: ${item.artifactRef}`);
  }
  if (!args.force && !replaceableArtifact(artifactPath)) {
    throw new Error(`${config.label} artifact already contains non-pending evidence. Use --force to replace it.`);
  }
  const promotedAt = args.promotedAt || new Date().toISOString();
  if (!validTimestamp(promotedAt)) {
    throw new Error("--promoted-at must be an ISO-like timestamp.");
  }

  const artifact = config.promote(observation, draft, item, promotedAt, inputPath);
  writeJSON(artifactPath, artifact);
  draft[config.itemKey] = config.updateDraft(item, artifact);
  writeJSON(draftPath, draft);

  return {
    ok: true,
    kind: args.kind,
    proofpackDir,
    proofpackPath,
    evidenceDraft: draftPath,
    artifactPath,
    artifactRef: item.artifactRef,
    version: draft.version,
    build: draft.build,
    runId: draft.runId,
    promotedAt,
    nextSteps: proofpack.nextSteps ?? [
      "Complete remaining physical device, TURN, room, app review metadata, TestFlight, and notarization evidence.",
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
