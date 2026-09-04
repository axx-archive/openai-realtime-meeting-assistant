import test from "node:test";
import assert from "node:assert/strict";
import {
  normalizeContext,
  normalizeDetail,
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
      unpricedCalls: Number.MAX_SAFE_INTEGER + 1,
    },
    capabilities: { updatePolicy: "true", pause: true },
  });
  assert.equal(detail.availability.team, "unavailable");
  assert.equal(detail.availability.work, "available");
  assert.equal(detail.budget.allowanceMicros, 0);
  assert.equal(detail.budget.reservedMicros, null);
  assert.equal(detail.budget.spentMicros, null);
  assert.equal(detail.budget.unpricedCalls, null);
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
