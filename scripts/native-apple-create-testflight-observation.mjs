#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const allowedProcessingStatuses = new Set(["ready", "uploaded", "processing", "accepted"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-create-testflight-observation.mjs --proofpack-dir path",
    "    --app-store-connect-build-id id --processing-status ready|uploaded|processing|accepted",
    "    --confirm-app-store-connect-upload --confirm-current-build --confirm-no-secrets",
    "    [--uploaded-at iso] [--force]",
    "",
    "Creates a sanitized testflight-observation.json in a proof-pack inbox.",
    "It does not contact Apple, upload to TestFlight, mark release evidence ready,",
    "or prove external tester availability. Promote the created observation separately.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    appStoreConnectBuildId: "",
    processingStatus: "",
    uploadedAt: "",
    confirmAppleUpload: false,
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
    } else if (arg === "--app-store-connect-build-id") {
      args.appStoreConnectBuildId = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--processing-status") {
      args.processingStatus = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--uploaded-at") {
      args.uploadedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-app-store-connect-upload" || arg === "--confirm-apple-upload") {
      args.confirmAppleUpload = true;
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
    throw new Error(`Refusing to overwrite existing TestFlight observation: ${path}. Use --force to replace it.`);
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

function cleanBuildValue(value) {
  return String(value ?? "").trim().replace(/^["']|["']$/g, "").replace(/;$/, "").trim();
}

function cleanIdentifier(value) {
  return String(value ?? "").trim();
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
      /(secret|password|passwd|token|credential|api_?key|apikey|private_?key|provision|certificate|cert|p8|p12|team_?id|development_?team|issuer_?id|key_?id|keychain|profile|authorization|jwt|apple_?id|username|headers?|cookies?|raw(Log|Output)|uploadLog|log|stdout|stderr|command|args|env|notarytool(Output|Log)?|altool(Output|Log)?|codesign(Output|Log)?|spctl(Output|Log)?)/i.test(
        key
      )
    ) {
      problems.push(`${path}.${key}`);
    }
    problems.push(...collectUnsafeContent(item, `${path}.${key}`));
  }
  return problems;
}

function validBuildId(value) {
  const trimmed = cleanIdentifier(value);
  if (!nonPlaceholderString(trimmed)) {
    return false;
  }
  if (!/^[A-Za-z0-9._:-]{3,128}$/.test(trimmed)) {
    return false;
  }
  if (
    /\bsk-[A-Za-z0-9_-]{20,}\b/.test(trimmed) ||
    /\b(?=[A-Z0-9]*[A-Z])[A-Z0-9]{10}\b/.test(trimmed) ||
    /\.(p8|p12|mobileprovision|provisionprofile)$/i.test(trimmed) ||
    /^https?:\/\//i.test(trimmed)
  ) {
    return false;
  }
  return true;
}

function artifactRef(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    throw new Error(`TestFlight observation must stay under the repository: ${path}`);
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
  if (!proofpack.observationTemplates?.testFlight) {
    problems.push("proofpack.observationTemplates.testFlight");
  }
  if (!proofpack.evidenceArtifacts?.testFlight) {
    problems.push("proofpack.evidenceArtifacts.testFlight");
  }
  if (!draft.testFlight || typeof draft.testFlight !== "object" || Array.isArray(draft.testFlight)) {
    problems.push("draft.testFlight");
  } else if (proofpack.evidenceArtifacts?.testFlight !== draft.testFlight.artifactRef) {
    problems.push("draft.testFlight.artifactRef");
  }
  if (!template || typeof template !== "object" || Array.isArray(template)) {
    problems.push("template");
  } else {
    problems.push(
      ...collectUnexpectedKeys(template, ["schemaVersion", "artifactType", "status", "runId", "uploadedAt", "app", "appStoreConnect"], "template"),
      ...collectUnexpectedKeys(template.app, ["version", "build", "target", "clientPlatform", "bundleIdentifier"], "template.app"),
      ...collectUnexpectedKeys(template.appStoreConnect, ["buildId", "processingStatus"], "template.appStoreConnect")
    );
    if (template.schemaVersion !== 1) {
      problems.push("template.schemaVersion");
    }
    if (template.artifactType !== "native_testflight_upload_observation") {
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
  if (!args.confirmAppleUpload) {
    problems.push("confirmAppleUpload");
  }
  if (!args.confirmCurrentBuild) {
    problems.push("confirmCurrentBuild");
  }
  if (!args.confirmNoSecrets) {
    problems.push("confirmNoSecrets");
  }
  if (!validBuildId(args.appStoreConnectBuildId)) {
    problems.push("appStoreConnectBuildId");
  }
  const processingStatus = String(args.processingStatus ?? "").trim().toLowerCase();
  if (!allowedProcessingStatuses.has(processingStatus)) {
    problems.push("processingStatus");
  }
  if (args.uploadedAt && !validTimestamp(args.uploadedAt)) {
    problems.push("uploadedAt");
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
  const templateRef = proofpack.observationTemplates?.testFlight ?? "";
  const templatePath = templateRef ? resolve(rootDir, templateRef) : join(proofpackDir, "inbox", "testflight-observation.template.json");
  if (!existsSync(templatePath)) {
    throw new Error(`Missing TestFlight observation template: ${templatePath}`);
  }
  const template = readJSON(templatePath);
  const appleDir = resolve(rootDir, proofpack.appleDir || "apple");
  const currentProject = readProjectMetadata(appleDir);
  const problems = validateProofpack({ proofpack, draft, template, currentProject, args });
  if (problems.length > 0) {
    throw new Error(`Cannot create TestFlight observation. Invalid: ${problems.slice(0, 12).join(", ")}`);
  }

  const uploadedAt = args.uploadedAt || new Date().toISOString();
  if (!validTimestamp(uploadedAt)) {
    throw new Error("--uploaded-at must be an ISO-like timestamp.");
  }
  const observation = {
    schemaVersion: 1,
    artifactType: "native_testflight_upload_observation",
    status: "observed",
    runId: draft.runId,
    uploadedAt,
    app: {
      version: draft.version,
      build: draft.build,
      target: "MeetingAssistAppleApp",
      clientPlatform: template.app.clientPlatform,
      bundleIdentifier: template.app.bundleIdentifier,
    },
    appStoreConnect: {
      buildId: cleanIdentifier(args.appStoreConnectBuildId),
      processingStatus: String(args.processingStatus ?? "").trim().toLowerCase(),
    },
  };
  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    throw new Error(`TestFlight observation contains unsafe fields or values: ${unsafe.slice(0, 6).join(", ")}`);
  }

  const outputPath = join(proofpackDir, "inbox", "testflight-observation.json");
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
    uploadedAt,
    processingStatus: observation.appStoreConnect.processingStatus,
    nextSteps: [
      `Promote ${repoSafe(outputPath)} with scripts/native-apple-promote-distribution-evidence.mjs --kind testflight.`,
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
