#!/usr/bin/env node
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const allowedDistributionKinds = new Set(["zip", "dmg", "pkg", "app"]);

function usage() {
  return [
    "Usage:",
    "  node scripts/native-apple-create-notarization-observation.mjs --proofpack-dir path",
    "    --distribution-artifact-kind zip|dmg|pkg|app",
    "    --distribution-artifact-filename name --distribution-artifact-sha256 hex",
    "    --notary-request-id id --gatekeeper-source label",
    "    --confirm-developer-id-archive --confirm-notary-accepted",
    "    --confirm-stapled-app --confirm-gatekeeper-accepted",
    "    --confirm-current-build --confirm-no-secrets [--checked-at iso] [--force]",
    "",
    "Creates a sanitized notarization-observation.json in a proof-pack inbox.",
    "It does not contact Apple, submit notarization, staple an app, run spctl,",
    "or mark release evidence ready. Promote the created observation separately.",
  ].join("\n");
}

function parseArgs(argv) {
  const args = {
    proofpackDir: "",
    distributionKind: "",
    distributionFilename: "",
    distributionSHA256: "",
    notaryRequestId: "",
    gatekeeperSource: "",
    checkedAt: "",
    confirmDeveloperIdArchive: false,
    confirmNotarizedApp: false,
    confirmStapledApp: false,
    confirmGatekeeperAccepted: false,
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
    } else if (arg === "--distribution-kind" || arg === "--distribution-artifact-kind") {
      args.distributionKind = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--distribution-filename" || arg === "--distribution-artifact-filename") {
      args.distributionFilename = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--distribution-sha256" || arg === "--distribution-artifact-sha256") {
      args.distributionSHA256 = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--notary-request-id") {
      args.notaryRequestId = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--gatekeeper-source") {
      args.gatekeeperSource = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--checked-at") {
      args.checkedAt = requiredValue(argv, index, arg);
      index += 1;
    } else if (arg === "--confirm-developer-id-archive") {
      args.confirmDeveloperIdArchive = true;
    } else if (arg === "--confirm-notary-accepted" || arg === "--confirm-notarized-app") {
      args.confirmNotarizedApp = true;
    } else if (arg === "--confirm-stapled-app") {
      args.confirmStapledApp = true;
    } else if (arg === "--confirm-gatekeeper-accepted") {
      args.confirmGatekeeperAccepted = true;
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
    throw new Error(`Refusing to overwrite existing macOS notarization observation: ${path}. Use --force to replace it.`);
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
  const block = targetBlockForTarget(projectText, targetName);
  return cleanBuildValue(/PRODUCT_BUNDLE_IDENTIFIER:\s*([^\n#]+)/.exec(block)?.[1] ?? "");
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

function readProjectMetadata(appleDir) {
  const projectPath = join(appleDir, "project.yml");
  if (!existsSync(projectPath)) {
    return { error: `missing:${repoSafe(projectPath)}` };
  }
  const projectText = readFileSync(projectPath, "utf8");
  const targetBlock = targetBlockForTarget(projectText, "MeetingAssistMacApp");
  const marketing = /MARKETING_VERSION:\s*([^\n#]+)/.exec(targetBlock)?.[1];
  const build = /CURRENT_PROJECT_VERSION:\s*([^\n#]+)/.exec(targetBlock)?.[1];
  if (!marketing || !build) {
    return { error: `version_build:${repoSafe(projectPath)}` };
  }
  return {
    version: cleanBuildValue(marketing),
    build: cleanBuildValue(build),
    bundleIdentifier: bundleIdentifierForTarget(projectText, "MeetingAssistMacApp"),
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

function validDistributionKind(value) {
  return allowedDistributionKinds.has(String(value ?? "").trim().toLowerCase());
}

function validDistributionFilename(value) {
  const trimmed = cleanIdentifier(value);
  return nonPlaceholderString(trimmed) && !trimmed.includes("/") && !trimmed.includes("\\") && trimmed.length <= 180;
}

function cleanSHA256(value) {
  return String(value ?? "").trim().toLowerCase();
}

function validRequestId(value) {
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

function validGatekeeperSource(value) {
  const trimmed = cleanIdentifier(value);
  return nonPlaceholderString(trimmed) && trimmed.length <= 120 && !/[{}\[\]\n\r]/.test(trimmed);
}

function artifactRef(path) {
  const relativePath = relative(rootDir, path);
  if (relativePath.startsWith("..")) {
    throw new Error(`macOS notarization observation must stay under the repository: ${path}`);
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
  if (!proofpack.observationTemplates?.notarization) {
    problems.push("proofpack.observationTemplates.notarization");
  }
  if (!proofpack.evidenceArtifacts?.notarization) {
    problems.push("proofpack.evidenceArtifacts.notarization");
  }
  if (!draft.macNotarization || typeof draft.macNotarization !== "object" || Array.isArray(draft.macNotarization)) {
    problems.push("draft.macNotarization");
  } else if (proofpack.evidenceArtifacts?.notarization !== draft.macNotarization.artifactRef) {
    problems.push("draft.macNotarization.artifactRef");
  }
  if (!template || typeof template !== "object" || Array.isArray(template)) {
    problems.push("template");
  } else {
    problems.push(
      ...collectUnexpectedKeys(
        template,
        ["schemaVersion", "artifactType", "status", "runId", "checkedAt", "distributionArtifact", "app", "signing", "notarization", "staple", "gatekeeper"],
        "template"
      ),
      ...collectUnexpectedKeys(template.distributionArtifact, ["kind", "filename", "sha256"], "template.distributionArtifact"),
      ...collectUnexpectedKeys(template.app, ["version", "build", "target", "clientPlatform", "bundleIdentifier"], "template.app"),
      ...collectUnexpectedKeys(template.signing, ["style", "signed", "hardenedRuntime", "timestamped"], "template.signing"),
      ...collectUnexpectedKeys(template.notarization, ["requestId", "status", "issueCount"], "template.notarization"),
      ...collectUnexpectedKeys(template.staple, ["stapled", "validated"], "template.staple"),
      ...collectUnexpectedKeys(template.gatekeeper, ["assessment", "source"], "template.gatekeeper")
    );
    if (template.schemaVersion !== 1) {
      problems.push("template.schemaVersion");
    }
    if (template.artifactType !== "native_macos_notarization_observation") {
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
    if (template.app?.target !== "MeetingAssistMacApp") {
      problems.push("template.app.target");
    }
    if (template.app?.clientPlatform !== "macos") {
      problems.push("template.app.clientPlatform");
    }
    if (!nonPlaceholderString(template.app?.bundleIdentifier)) {
      problems.push("template.app.bundleIdentifier");
    }
  }
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
  if (!args.confirmCurrentBuild) {
    problems.push("confirmCurrentBuild");
  }
  if (!args.confirmNoSecrets) {
    problems.push("confirmNoSecrets");
  }
  if (!validDistributionKind(args.distributionKind)) {
    problems.push("distributionKind");
  }
  if (!validDistributionFilename(args.distributionFilename)) {
    problems.push("distributionFilename");
  }
  if (!/^[a-f0-9]{64}$/.test(cleanSHA256(args.distributionSHA256))) {
    problems.push("distributionSHA256");
  }
  if (!validRequestId(args.notaryRequestId)) {
    problems.push("notaryRequestId");
  }
  if (!validGatekeeperSource(args.gatekeeperSource)) {
    problems.push("gatekeeperSource");
  }
  if (args.checkedAt && !validTimestamp(args.checkedAt)) {
    problems.push("checkedAt");
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
  const templateRef = proofpack.observationTemplates?.notarization ?? "";
  const templatePath = templateRef ? resolve(rootDir, templateRef) : join(proofpackDir, "inbox", "notarization-observation.template.json");
  if (!existsSync(templatePath)) {
    throw new Error(`Missing macOS notarization observation template: ${templatePath}`);
  }
  const template = readJSON(templatePath);
  const appleDir = resolve(rootDir, proofpack.appleDir || "apple");
  const currentProject = readProjectMetadata(appleDir);
  const problems = validateProofpack({ proofpack, draft, template, currentProject, args });
  if (problems.length > 0) {
    throw new Error(`Cannot create macOS notarization observation. Invalid: ${problems.slice(0, 12).join(", ")}`);
  }

  const checkedAt = args.checkedAt || new Date().toISOString();
  if (!validTimestamp(checkedAt)) {
    throw new Error("--checked-at must be an ISO-like timestamp.");
  }
  const observation = {
    schemaVersion: 1,
    artifactType: "native_macos_notarization_observation",
    status: "accepted",
    runId: draft.runId,
    checkedAt,
    distributionArtifact: {
      kind: String(args.distributionKind ?? "").trim().toLowerCase(),
      filename: basename(cleanIdentifier(args.distributionFilename)),
      sha256: cleanSHA256(args.distributionSHA256),
    },
    app: {
      version: draft.version,
      build: draft.build,
      target: "MeetingAssistMacApp",
      clientPlatform: "macos",
      bundleIdentifier: template.app.bundleIdentifier,
    },
    signing: {
      style: "developer_id",
      signed: true,
      hardenedRuntime: true,
      timestamped: true,
    },
    notarization: {
      requestId: cleanIdentifier(args.notaryRequestId),
      status: "accepted",
      issueCount: 0,
    },
    staple: {
      stapled: true,
      validated: true,
    },
    gatekeeper: {
      assessment: "accepted",
      source: cleanIdentifier(args.gatekeeperSource),
    },
  };
  const unsafe = collectUnsafeContent(observation);
  if (unsafe.length > 0) {
    throw new Error(`macOS notarization observation contains unsafe fields or values: ${unsafe.slice(0, 6).join(", ")}`);
  }

  const outputPath = join(proofpackDir, "inbox", "notarization-observation.json");
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
    checkedAt,
    requestId: observation.notarization.requestId,
    nextSteps: [
      `Promote ${repoSafe(outputPath)} with scripts/native-apple-promote-distribution-evidence.mjs --kind notarization.`,
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
