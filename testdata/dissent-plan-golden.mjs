#!/usr/bin/env node
// dissent-plan-golden.mjs — regenerates testdata/dissent_plan_golden.json by
// running the REAL Dissent TypeScript (src/coordination.ts compileIntelligencePlan
// + src/assurance.ts classifyAssurance) over the fixture inputs. Committed so
// the cross-language evidence for dissent_plan.go can be re-derived and
// audited: when a work class is added, a consequence regex is tweaked or the
// PRIORITY list is reordered, the vectors must be re-derivable from the
// source rather than trusted because they are green.
//
//   DISSENT_SRC=/path/to/dissent node testdata/dissent-plan-golden.mjs
//   DISSENT_SRC=/path/to/dissent node testdata/dissent-plan-golden.mjs --check
//
// --check regenerates in memory and exits non-zero if the committed file
// differs, which is what a CI drift gate should run.
//
// CONTRACT: the vector INPUTS (contract / registry / runtime for plan vectors,
// input for classifier vectors) are hand-authored data and are preserved
// verbatim; everything else — plan, planCanonicalHex, planId, contractSha256,
// status, topology, workClasses, executor, assuranceSeats, expectError — is
// derived here. To add a vector, append `{ "name": ..., "contract": ...,
// "registry": ..., "runtime": ... }` and re-run: a vector whose TypeScript
// throws is recorded with the matching Go error code in `expectError`, derived
// from the schemas rather than copied forward — a committed code the source no
// longer implies (or a vector that stopped throwing) aborts the run instead of
// being silently preserved.
//
// The compiled dist is imported rather than the .ts, because coordination.ts
// uses ESM "./assurance.js" specifiers that node's type stripping does not
// rewrite. sourceSha256 records the digest of the TypeScript SOURCES, so a
// source edit that was never rebuilt into dist/ is still detectable.

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const SRC = process.env.DISSENT_SRC ?? "/Users/ajhart/Documents/business";
const SOURCES = ["src/coordination.ts", "src/assurance.ts", "src/schemas.ts", "src/crypto.ts"];
const { compileIntelligencePlan, workContractSchema, executorQualificationRegistrySchema, coordinationRuntimeAuthoritySchema } = await import(
  join(SRC, "dist/src/coordination.js")
);
const { classifyAssurance } = await import(join(SRC, "dist/src/assurance.js"));
const { canonicalJson } = await import(join(SRC, "src/crypto.ts"));

for (const source of SOURCES) {
  const built = join(SRC, "dist", source.replace(/^src\//, "src/").replace(/\.ts$/, ".js"));
  if (statSync(join(SRC, source)).mtimeMs > statSync(built).mtimeMs) {
    console.error(`STALE BUILD: ${source} is newer than ${built} — run the TypeScript build first`);
    process.exit(2);
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const goldenPath = join(here, "dissent_plan_golden.json");
const golden = JSON.parse(readFileSync(goldenPath, "utf8"));

// zod throw -> the code dissentPlanError carries for the same refusal.
//
// DERIVED, never read back from the committed vector. Each input is replayed
// against the schema that owns it, so the answer comes from the TypeScript
// itself: move the duplicate-provider refine off coordinationRuntimeAuthority-
// Schema and onto the registry schema and this returns registry_invalid, which
// then disagrees with the committed runtime_invalid and stops the run. The
// previous `if (vector.expectError) return vector.expectError` short-circuit
// made every throwing vector hand-authored data the generator only echoed —
// half the drift gate was inert, and "runtime_invalid" was not even reachable
// from the derivation below it.
const ERROR_GATES = [
  ["contract_invalid", workContractSchema, "contract"],
  ["registry_invalid", executorQualificationRegistrySchema, "registry"],
  ["runtime_invalid", coordinationRuntimeAuthoritySchema, "runtime"]
];

function errorCodeFor(vector, error) {
  for (const [code, schema, key] of ERROR_GATES) {
    if (!schema.safeParse(vector[key]).success) return code;
  }
  // Every schema accepted its input, so compile itself refused. Fall back to
  // the zod path, widened to the runtime-authority fields.
  const path = String(error?.issues?.[0]?.path?.[0] ?? "");
  if (path === "tenantPolicy" || path === "availableProviders" || path === "assuranceRegistry") return "runtime_invalid";
  if (path.startsWith("routes") || path === "productionEligible") return "registry_invalid";
  return "contract_invalid";
}

const vectors = golden.vectors.map((vector) => {
  const { name, contract, registry, runtime } = vector;
  let plan;
  try {
    plan = compileIntelligencePlan(contract, registry, runtime);
  } catch (error) {
    const derived = errorCodeFor(vector, error);
    if (vector.expectError && vector.expectError !== derived) {
      console.error(
        `vector ${name}: committed expectError "${vector.expectError}" but the TypeScript now implies "${derived}" — ` +
          "reconcile the port's error code before re-recording"
      );
      process.exit(3);
    }
    return { name, contract, registry, runtime, expectError: derived };
  }
  if (vector.expectError) {
    console.error(`vector ${name}: committed expectError "${vector.expectError}" but the TypeScript no longer throws`);
    process.exit(3);
  }
  const canonical = canonicalJson(plan);
  return {
    name,
    contract,
    registry,
    runtime,
    plan,
    planCanonicalHex: Buffer.from(canonical, "utf8").toString("hex"),
    planId: plan.id,
    contractSha256: plan.contractSha256,
    status: plan.status,
    topology: plan.topology,
    workClasses: plan.workClasses,
    executor: plan.executor ? { provider: plan.executor.provider, model: plan.executor.model } : null,
    assuranceSeats: plan.assurance.assignments.map((assignment) => ({
      workClass: assignment.workClass,
      reviewer: assignment.reviewer.provider,
      challenger: assignment.challenger.provider,
      judge: assignment.judge.provider
    }))
  };
});

const classifierVectors = golden.classifierVectors.map(({ name, input }) => ({
  name,
  input,
  classification: classifyAssurance(input)
}));

const next = {
  generatedBy: "testdata/dissent-plan-golden.mjs over src/coordination.ts + src/assurance.ts (test-only registries)",
  sourceSha256: Object.fromEntries(
    SOURCES.map((source) => [source, createHash("sha256").update(readFileSync(join(SRC, source))).digest("hex")])
  ),
  shaA: golden.shaA,
  shaB: golden.shaB,
  vectors,
  classifierVectors
};
const text = `${JSON.stringify(next, null, 2)}\n`;

if (process.argv.includes("--check")) {
  if (readFileSync(goldenPath, "utf8") !== text) {
    console.error("dissent_plan_golden.json is stale — re-run testdata/dissent-plan-golden.mjs");
    process.exit(1);
  }
  console.log(`ok: ${vectors.length} plan + ${classifierVectors.length} classifier vectors replay`);
} else {
  writeFileSync(goldenPath, text);
  console.log(`wrote ${vectors.length} plan + ${classifierVectors.length} classifier vectors`);
}
