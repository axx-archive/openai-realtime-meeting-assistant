#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { isIP } from "node:net";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const requiredReadinessFlags = [
  ["descriptionReady", "confirmDescriptionReady"],
  ["keywordsReady", "confirmKeywordsReady"],
  ["screenshotsReady", "confirmScreenshotsReady"],
  ["appPrivacyReady", "confirmAppPrivacyReady"],
  ["ageRatingComplete", "confirmAgeRatingComplete"],
  ["exportComplianceComplete", "confirmExportComplianceComplete"],
  ["testInformationReady", "confirmTestInformationReady"],
  ["externalTestingGroupReady", "confirmExternalTestingGroupReady"],
];

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-create-app-review-observation.mjs --proofpack-dir path",
    "    --support-url https://... --privacy-policy-url https://...",
    "    --confirm-description-ready --confirm-keywords-ready",
    "    --confirm-screenshots-ready --confirm-app-privacy-ready",
    "    --confirm-age-rating-complete --confirm-export-compliance-complete",
    "    --confirm-test-information-ready --confirm-external-testing-group-ready",
    "    --confirm-current-build --confirm-no-secrets [--reviewed-at iso] [--force]",
    "",
    "Creates a sanitized app-store-review-observation.json in a proof-pack inbox.",
    "It does not contact Apple, upload to TestFlight, mark release evidence ready,",
    "or prove Apple approval. Promote the created observation separately.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    supportURL: "",
    privacyPolicyURL: "",
    reviewedAt: "",
    confirmDescriptionReady: false,
    confirmKeywordsReady: false,
    confirmScreenshotsReady: false,
    confirmAppPrivacyReady: false,
    confirmAgeRatingComplete: false,
    confirmExportComplianceComplete: false,
    confirmTestInformationReady: false,
    confirmExternalTestingGroupReady: false,
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
    } else if (arg === "--support-url") {
      args.supportURL = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--privacy-policy-url") {
      args.privacyPolicyURL = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--reviewed-at") {
      args.reviewedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-description-ready") {
      args.confirmDescriptionReady = true;
    } else if (arg === "--confirm-keywords-ready") {
      args.confirmKeywordsReady = true;
    } else if (arg === "--confirm-screenshots-ready") {
      args.confirmScreenshotsReady = true;
    } else if (arg === "--confirm-app-privacy-ready") {
      args.confirmAppPrivacyReady = true;
    } else if (arg === "--confirm-age-rating-complete") {
      args.confirmAgeRatingComplete = true;
    } else if (arg === "--confirm-export-compliance-complete") {
      args.confirmExportComplianceComplete = true;
    } else if (arg === "--confirm-test-information-ready") {
      args.confirmTestInformationReady = true;
    } else if (arg === "--confirm-external-testing-group-ready") {
      args.confirmExternalTestingGroupReady = true;
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
    throw new Error(`Refusing to overwrite existing App Store review observation: ${path}. Use --force to replace it.`);
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
  return !["TODO", "TBD", "FIXME", "CHANGE_ME", "PLACEHOLDER", "EXAMPLE", "SAMPLE", "DUMMY", "UNKNOWN", "N_A", "NA"].includes(
    normalized
  );
}

function validTimestamp(value) {
  return nonPlaceholderString(value) && /^\d{4}-\d{2}-\d{2}T/.test(value) && !Number.isNaN(Date.parse(value));
}

function cleanURL(value) {
  return String(value ?? "").trim();
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

function readProjectMetadata(appleDir) {
  const projectPath = join(appleDir, "project.yml");
  if (!existsSync(projectPath)) {
    return { error: `missing:${repoSafe(projectPath)}` };
  }
  const projectText = readFileSync(projectPath, "utf8");
  const marketing = /MARKETING_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  const build = /CURRENT_PROJECT_VERSION:\s*([^\n#]+)/.exec(projectText)?.[1];
  if (!marketing || !build) {
    return { error: `version_build:${repoSafe(projectPath)}` };
  }
  return {
    version: cleanBuildValue(marketing),
    build: cleanBuildValue(build),
    bundleIdentifier: bundleIdentifierForTarget(projectText, "MeetingAssistAppleApp"),
  };
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

function isPrivateIPv4(hostname) {
  const parts = hostname.split(".").map((part) => Number(part));
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) {
    return false;
  }
  const [first, second] = parts;
  return first === 10 || first === 127 || (first === 172 && second >= 16 && second <= 31) || (first === 192 && second === 168);
}

function validPublicHttpsURL(value) {
  const trimmed = cleanURL(value);
  if (!nonPlaceholderString(trimmed)) {
    return false;
  }
  try {
    const url = new URL(trimmed);
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
        /\b[A-Z0-9]{10}\b/.test(trimmed) ||
        /\b[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
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
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|issuer_?id|key_?id|keychain|profile|authorization|jwt|apple_?id|username|reviewer_?email|headers?|cookies?|raw(Log|Output)|uploadLog|log|stdout|stderr|command|args|env)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function artifactRef(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    throw new Error(`App Store review observation must stay under the repository: ${path}`);
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
    if (String(currentProject.version ?? "") !== String(draft.version ?? "")) {
      problems.push("currentProject.version");
    }
    if (String(currentProject.build ?? "") !== String(draft.build ?? "")) {
      problems.push("currentProject.build");
    }
    if (String(currentProject.bundleIdentifier ?? "") !== String(template?.app?.bundleIdentifier ?? "")) {
      problems.push("currentProject.bundleIdentifier");
    }
  }
  if (!proofpack.observationTemplates?.appStoreReview) {
    problems.push("proofpack.observationTemplates.appStoreReview");
  }
  if (!proofpack.evidenceArtifacts?.appStoreReview) {
    problems.push("proofpack.evidenceArtifacts.appStoreReview");
  }
  if (!draft.appStoreReview || typeof draft.appStoreReview !== "object" || Array.isArray(draft.appStoreReview)) {
    problems.push("draft.appStoreReview");
  } else if (proofpack.evidenceArtifacts?.appStoreReview !== draft.appStoreReview.artifactRef) {
    problems.push("draft.appStoreReview.artifactRef");
  }
  if (!template || typeof template !== "object" || Array.isArray(template)) {
    problems.push("template");
  } else {
    problems.push(
      ...collectUnexpectedKeys(template, ["schemaVersion", "artifactType", "status", "runId", "reviewedAt", "app", "metadata"], "template"),
      ...collectUnexpectedKeys(template.app, ["version", "build", "target", "clientPlatform", "bundleIdentifier"], "template.app"),
      ...collectUnexpectedKeys(
        template.metadata,
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
        "template.metadata"
      )
    );
    if (template.schemaVersion !== 1) {
      problems.push("template.schemaVersion");
    }
    if (template.artifactType !== "native_app_store_review_metadata_observation") {
      problems.push("template.artifactType");
    }
    if (template.status !== "template") {
      problems.push("template.status");
    }
    if (String(template.runId ?? "") !== String(draft.runId ?? "")) {
      problems.push("template.runId");
    }
    if (String(template.app?.version ?? "") !== String(draft.version ?? "")) {
      problems.push("template.app.version");
    }
    if (String(template.app?.build ?? "") !== String(draft.build ?? "")) {
      problems.push("template.app.build");
    }
    if (template.app?.target !== "MeetingAssistAppleApp") {
      problems.push("template.app.target");
    }
    if (!["ios", "ipados"].includes(String(template.app?.clientPlatform ?? ""))) {
      problems.push("template.app.clientPlatform");
    }
    if (!nonPlaceholderString(template.app?.bundleIdentifier)) {
      problems.push("template.app.bundleIdentifier");
    }
  }
  if (!args.confirmCurrentBuild) {
    problems.push("confirmCurrentBuild");
  }
  if (!args.confirmNoSecrets) {
    problems.push("confirmNoSecrets");
  }
  if (!validPublicHttpsURL(args.supportURL)) {
    problems.push("supportURL");
  }
  if (!validPublicHttpsURL(args.privacyPolicyURL)) {
    problems.push("privacyPolicyURL");
  }
  for (const [, flag] of requiredReadinessFlags) {
    if (args[flag] !== true) {
      problems.push(flag);
    }
  }
  if (args.reviewedAt && !validTimestamp(args.reviewedAt)) {
    problems.push("reviewedAt");
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
  const templateRef = proofpack.observationTemplates?.appStoreReview ?? "";
  const templatePath = templateRef ? resolve(rootDir, templateRef) : join(proofpackDir, "inbox", "app-store-review-observation.template.json");
  if (!existsSync(templatePath)) {
    throw new Error(`Missing App Store review observation template: ${templatePath}`);
  }
  const template = readJSON(templatePath);
  const appleDir = resolve(rootDir, proofpack.appleDir || "apple");
  const currentProject = readProjectMetadata(appleDir);
  const problems = validateProofpack({ proofpack, draft, template, currentProject, args });
  if (problems.length > 0) {
    throw new Error(`Cannot create App Store review observation. Invalid: ${problems.slice(0, 12).join(", ")}`);
  }

  const reviewedAt = args.reviewedAt || new Date().toISOString();
  if (!validTimestamp(reviewedAt)) {
    throw new Error("--reviewed-at must be an ISO-like timestamp.");
  }
  const observation = {
    schemaVersion: 1,
    artifactType: "native_app_store_review_metadata_observation",
    status: "observed",
    runId: draft.runId,
    reviewedAt,
    app: {
      version: draft.version,
      build: draft.build,
      target: "MeetingAssistAppleApp",
      clientPlatform: template.app.clientPlatform,
      bundleIdentifier: template.app.bundleIdentifier,
    },
    metadata: {
      supportURL: cleanURL(args.supportURL),
      privacyPolicyURL: cleanURL(args.privacyPolicyURL),
      descriptionReady: true,
      keywordsReady: true,
      screenshotsReady: true,
      appPrivacyReady: true,
      ageRatingComplete: true,
      exportComplianceComplete: true,
      testInformationReady: true,
      externalTestingGroupReady: true,
    },
  };
  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    throw new Error(`App Store review observation contains unsafe fields or values: ${unsafe.slice(0, 6).join(", ")}`);
  }

  const outputPath = join(proofpackDir, "inbox", "app-store-review-observation.json");
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
    reviewedAt,
    nextSteps: [
      `Promote ${repoSafe(outputPath)} with scripts/native-apple-promote-distribution-evidence.mjs --kind app-review.`,
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
