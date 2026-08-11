import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(root, "../../..");
const evidencePath = path.join(repoRoot, "docs/evidence/e10/stride-e10-pd0-work-network-prototype-rendered-20260810.json");
const evidence = JSON.parse(fs.readFileSync(evidencePath, "utf8"));
const failures = [];
const sha256 = bytes => createHash("sha256").update(bytes).digest("hex");
const assert = (condition, message) => { if (!condition) failures.push(message); };

assert(evidence.schema === "stride.e10.pd0.work-network-prototype-rendered.v1", "unknown rendered-evidence schema");
assert(evidence.classification === "body_minimized_fictional_loopback_reference_only", "rendered evidence lost its bounded classification");
assert(evidence.renderedEvidence?.length === 4, "exact four rendered captures required");

for (const [relativePath, expectedDigest] of Object.entries(evidence.sourceBoundary?.files || {})) {
  const target = path.join(repoRoot, relativePath);
  assert(fs.existsSync(target), `${relativePath} is missing`);
  if (fs.existsSync(target)) assert(sha256(fs.readFileSync(target)) === expectedDigest, `${relativePath} differs from its rendered receipt`);
}

for (const capture of evidence.renderedEvidence || []) {
  assert(typeof capture.capture === "string" && path.isAbsolute(capture.capture), `${capture.surface} capture path must be absolute`);
  assert(fs.existsSync(capture.capture), `${capture.surface} capture is missing`);
  if (!fs.existsSync(capture.capture)) continue;
  const bytes = fs.readFileSync(capture.capture);
  assert(bytes.length >= 24 && bytes.subarray(1, 4).toString("ascii") === "PNG", `${capture.surface} is not a PNG`);
  if (bytes.length < 24) continue;
  const dimensions = `${bytes.readUInt32BE(16)}x${bytes.readUInt32BE(20)}`;
  assert(dimensions === capture.pixelSize, `${capture.surface} dimensions ${dimensions} differ from ${capture.pixelSize}`);
  assert(sha256(bytes) === capture.sha256, `${capture.surface} SHA-256 differs from the rendered receipt`);
}

assert(evidence.workPrototype?.assertions?.stepPostimages?.length === 4, "four exact presentation postimages required");
assert(evidence.workPrototype?.assertions?.backRestoresExactPriorPostimage === true, "Back postimage proof missing");
assert(evidence.workPrototype?.assertions?.recoveryPreservesExactCurrentPostimage === true, "recovery postimage proof missing");
assert(evidence.workPrototype?.assertions?.restartReturnsToIdleThenExactStepZero === true, "restart postimage proof missing");
assert(evidence.networkPrototype?.initialTypedChildCards === 0 && evidence.networkPrototype?.resolvedState === "feature_off", "Network parent-off proof differs");
assert(evidence.exclusions?.productImplementation === false && evidence.exclusions?.successfulOpenAIPresentationRun === false && evidence.exclusions?.activation === false, "rendered reference overclaims product/provider/activation acceptance");

if (failures.length) {
  console.error(failures.map(failure => `FAIL: ${failure}`).join("\n"));
  process.exit(1);
}

console.log(JSON.stringify({ status: "PASS", sourceFiles: Object.keys(evidence.sourceBoundary.files).length, captures: evidence.renderedEvidence.length, dimensionsVerified: evidence.renderedEvidence.length, screenshotSha256Verified: evidence.renderedEvidence.length }));
