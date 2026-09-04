import test from "node:test";
import assert from "node:assert/strict";
import {
  normalizeContext,
  normalizeDetail,
  normalizeWorkDetail,
  businessAPI,
  BusinessAPIError,
} from "./api.js";
const business = {
  id: "b1",
  organizationId: "o1",
  name: "Test business",
  revision: 1,
  leadership: "agent_ceo",
  authorityPreset: "full_autonomy",
  status: "draft",
};
test("unknown or malformed coverage cannot become zero spend or an available team", () => {
  const detail = normalizeDetail({
    business,
    team: [],
    work: [],
    availability: { work: "available" },
    budget: {
      currency: "USD",
      allowanceMicros: 0,
      reservedMicros: -1,
      spentMicros: "0",
      unknownCostOperations: Number.MAX_SAFE_INTEGER + 1,
    },
    capabilities: { updatePolicy: "true", pause: true },
  });
  assert.equal(detail.availability.team, "unavailable");
  assert.equal(detail.availability.work, "available");
  assert.equal(detail.budget.allowanceMicros, 0);
  assert.equal(detail.budget.reservedMicros, null);
  assert.equal(detail.budget.spentMicros, null);
  assert.equal(detail.budget.unknownCostOperations, null);
  assert.equal(detail.capabilities.updatePolicy, false);
  assert.equal(detail.capabilities.pause, true);
  assert.equal(detail.execution.status, "unavailable");
});
test("wrong business, absent identity, and unsafe revision reject before display or mutation", () => {
  assert.throws(
    () => normalizeDetail({ business }, "b2"),
    (e) => e instanceof BusinessAPIError && e.status === 409,
  );
  assert.throws(
    () => normalizeDetail({ business: { ...business, revision: 1.5 } }),
    /incomplete/,
  );
  assert.throws(
    () => normalizeContext({ organizations: [], businesses: [] }),
    /could not be loaded/,
  );
});
test("explicit unavailable source suppresses rows even if stale records remain in the response", () => {
  const detail = normalizeDetail({
    business,
    team: [{ name: "Private former seat" }],
    availability: { team: "unavailable" },
  });
  assert.deepEqual(detail.team, []);
  assert.equal(detail.availability.team, "unavailable");
});
test("same-origin mutations preserve exact revision and operation key across acknowledgement-loss retry", async () => {
  const original = globalThis.fetch,
    calls = [];
  const payload = {
    idempotencyKey: "stable-key",
    expectedRevision: 4,
    action: "update_policy",
    authorityPreset: "full_autonomy",
    leadership: "agent_ceo",
  };
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    if (calls.length === 1) throw new TypeError("lost acknowledgement");
    return new Response(
      JSON.stringify({ business: { ...business, revision: 5 } }),
      { status: 200 },
    );
  };
  try {
    await assert.rejects(
      businessAPI.update("b1", payload),
      (e) => e.code === "network_error",
    );
    const result = await businessAPI.update("b1", payload);
    assert.equal(result.business.revision, 5);
    assert.equal(calls[0].options.body, calls[1].options.body);
    assert.equal(calls[1].options.credentials, "same-origin");
    assert.equal(calls[1].url, "/api/business/v1/businesses/b1");
    assert.deepEqual(JSON.parse(calls[1].options.body), payload);
  } finally {
    globalThis.fetch = original;
  }
});
test("authorization and conflict errors remain actionable status codes for privacy clearing and explicit refresh", async () => {
  const original = globalThis.fetch;
  try {
    for (const status of [401, 403, 404, 409, 503]) {
      globalThis.fetch = async () =>
        new Response(JSON.stringify({ error: { code: "test_error" } }), {
          status,
        });
      await assert.rejects(
        businessAPI.detail("b1"),
        (e) => e.status === status && e.code === "test_error",
      );
    }
  } finally {
    globalThis.fetch = original;
  }
});

function privateWorkFixture() {
  return {
    business: { ...business },
    visibility: "private",
    coverage: "complete",
    capabilities: { retry: false, publish: false },
    employment: {
      id: "e1",
      businessId: "b1",
      name: "Private document capability",
    },
    work: {
      id: "w1",
      businessId: "b1",
      employmentId: "e1",
      objective: "Draft a useful brief",
      outputContract: "private_document_v1",
      status: "completed",
      resultId: "r1",
      heldMicros: 0,
      settledMicros: 55,
    },
    attempts: [
      {
        id: "a1",
        ordinal: 1,
        state: "succeeded",
        mode: "execute",
        outcome: "succeeded",
        costState: "known",
        resultId: "r1",
        operation: { id: "op1", adapterId: "adapter1", maximumCostMicros: 100 },
      },
    ],
    result: {
      id: "r1",
      workId: "w1",
      attemptId: "a1",
      operationId: "op1",
      generation: 1,
      content:
        "# Private brief\n\nLiteral <script>unsafe()</script> and ![image](https://untrusted.invalid/tracker)",
      contentType: "text/markdown",
      digest: "sha256:" + "a".repeat(64),
      eligible: true,
      createdAt: "2026-09-04T20:00:00Z",
    },
  };
}
test("private work reader binds business, employment, Work, result, attempt and operation identities", () => {
  const normal = normalizeWorkDetail(privateWorkFixture(), "b1", "w1");
  assert.equal(normal.result.id, "r1");
  assert.equal(normal.work.settledMicros, 55);
  assert.equal(normal.capabilities.publish, false);
  for (const mutate of [
    (v) => (v.business.id = "b2"),
    (v) => (v.work.businessId = "b2"),
    (v) => (v.employment.businessId = "b2"),
    (v) => (v.employment.id = "e2"),
    (v) => (v.result.workId = "w2"),
    (v) => (v.result.id = "r2"),
    (v) => (v.result.attemptId = "a2"),
    (v) => (v.result.operationId = "op2"),
    (v) => (v.attempts[0].resultId = "r2"),
  ]) {
    const raw = privateWorkFixture();
    mutate(raw);
    assert.throws(
      () => normalizeWorkDetail(raw, "b1", "w1"),
      (e) => e.code === "invalid_work_response",
    );
  }
});
test("private result rejects changed visibility, truncated lineage, oversized or unsupported content", () => {
  for (const mutate of [
    (v) => (v.visibility = "public"),
    (v) => (v.coverage = "partial"),
    (v) => (v.attempts = []),
    (v) => v.attempts.push({ ...v.attempts[0] }),
    (v) => (v.result.contentType = "text/html"),
    (v) => (v.result.content = "x".repeat(256001)),
    (v) => (v.result.digest = "missing"),
    (v) => delete v.result.eligible,
    (v) => (v.result = null),
  ]) {
    const raw = privateWorkFixture();
    mutate(raw);
    assert.throws(
      () => normalizeWorkDetail(raw, "b1", "w1"),
      (e) => e.code === "invalid_work_response",
    );
  }
});
test("historical private result remains readable without inheriting current eligibility or known cost", () => {
  const raw = privateWorkFixture();
  raw.result.eligible = false;
  raw.result.ineligibleReason = "authority_changed";
  raw.work.status = "reconciling";
  raw.work.settledMicros = null;
  raw.attempts[0].costState = "unknown";
  const result = normalizeWorkDetail(raw, "b1", "w1");
  assert.equal(result.result.eligible, false);
  assert.equal(result.result.content, raw.result.content);
  assert.equal(result.work.settledMicros, null);
  assert.equal(result.attempts[0].costState, "unknown");
});
test("overview retains bounded coverage and uncertain operation costs without calling them provider calls", () => {
  const detail = normalizeDetail({
    business,
    team: [],
    work: [],
    availability: { team: "available", work: "available" },
    coverage: { teamMore: true, workMore: true, limit: 100 },
    budget: {
      currency: "USD",
      allowanceMicros: 1000,
      spentMicros: 55,
      reservedMicros: 500,
      unknownCostOperations: 100,
      unknownCostMore: true,
      state: "cost_unresolved",
    },
  });
  assert.deepEqual(detail.coverage, {
    teamMore: true,
    workMore: true,
    limit: 100,
  });
  assert.equal(detail.budget.unknownCostMore, true);
  assert.equal(detail.budget.unknownCostOperations, 100);
  assert.equal(detail.budget.spentMicros, 55);
});
test("exact Work transport uses the business-scoped route and same-origin session", async () => {
  const original = globalThis.fetch;
  let seen;
  globalThis.fetch = async (url, options) => {
    seen = { url, options };
    return new Response(JSON.stringify(privateWorkFixture()), { status: 200 });
  };
  try {
    await businessAPI.workDetail("b1", "w1");
    assert.equal(seen.url, "/api/business/v1/businesses/b1/work/w1");
    assert.equal(seen.options.credentials, "same-origin");
  } finally {
    globalThis.fetch = original;
  }
});
