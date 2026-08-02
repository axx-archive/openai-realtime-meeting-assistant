#!/usr/bin/env node
/**
 * Repeatable runner for the real app/runtime E9 integration drill.
 *
 * The receipt is intentionally classified as local_deterministic_integration.
 * It must never be interpreted as paid-provider, WebRTC, physical-device,
 * production, restore-host, HA, live-soak, release, or deployment evidence.
 */

import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const tests = [
  "TestE9LocalDeterministicVerticalIntegrationDrill",
  "TestSTRIDEProductMeetingSuggestionHTTPUsesCurrentConsentedMemberSet",
  "TestE9DeterministicFounderGraphThroughProductEndpoints",
];
const testPattern = `^(${tests.join("|")})$`;
const args = new Set(process.argv.slice(2));

if (args.has("--help") || args.has("-h")) {
  process.stdout.write([
    "Usage: node scripts/e9-vertical-integration.mjs [--race]",
    "",
    "Runs the genuine token-free app/runtime integration drill.",
    "--race runs both the normal and Go race-detector gates.",
  ].join("\n") + "\n");
  process.exit(0);
}
for (const arg of args) {
  if (arg !== "--race") throw new Error(`Unknown argument: ${arg}`);
}

const providerFreeEnv = {
  ...process.env,
  OPENAI_API_KEY: "",
  ANTHROPIC_API_KEY: "",
  FISCAL_API_KEY: "",
  FISCAL_AI_API_KEY: "",
  OPENAI_REALTIME_API_KEY: "",
  OPENAI_TRANSCRIPTION_API_KEY: "",
};
const gates = [
  { id: "go_normal", argv: ["test", ".", "-run", testPattern, "-count=1", "-v"] },
];
if (args.has("--race")) {
  gates.push({ id: "go_race", argv: ["test", ".", "-run", testPattern, "-count=1", "-race", "-v"] });
}

const results = gates.map((gate) => {
  const result = spawnSync("go", gate.argv, {
    cwd: rootDir,
    env: providerFreeEnv,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  return {
    id: gate.id,
    command: ["go", ...gate.argv].join(" "),
    passed: result.status === 0,
    exitCode: result.status,
    outputTail: `${result.stdout ?? ""}${result.stderr ?? ""}`.slice(-6000),
  };
});
const passed = results.every((result) => result.passed);
const receipt = {
  schema: "stride.e9.local-founder-integration/v1",
  evidenceClass: "local_deterministic_integration",
  state: passed ? "passed" : "failed",
  tests,
  modelProviderCredentialsSupplied: false,
  productionMutation: false,
  gates: results,
  provedWhenPassed: [
    "authenticated_runtime_http",
    "durable_team_projection",
    "private_exclusion",
    "tenant_boundary",
    "suggested_work_revision_bound_approval",
    "meeting_origin_consent_current_participant_guest_privacy_tenant",
    "insights_opportunities_v1_run_artifact_brain_link",
    "marketplace_workforce_lifecycle",
    "private_agent_direct_message",
    "scout_introduction_and_assisted_specialist_selection",
    "temporal_last_five_minutes_product",
    "specialist_fake_success_and_failure_isolation",
    "composer_source_contracts",
    "default_off_work_marketplace_provider_routes",
    "signed_restart_restore_without_transient_resurrection",
  ],
  claimsExcluded: [
    "paid_provider_quality",
    "provider_model_compatibility",
    "realtime_audio_or_video",
    "live_stt_or_dictation_quality",
    "physical_devices",
    "production_data",
    "production_restore",
    "ha_failover",
    "live_soak",
    "release_or_deployment",
  ],
};

process.stdout.write(`${JSON.stringify(receipt, null, 2)}\n`);
process.exit(passed ? 0 : 1);
