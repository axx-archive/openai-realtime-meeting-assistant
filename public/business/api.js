/** STRIDE Business v1 HTTP contract. Same-origin session cookies are the only identity input.
 * context: {viewer:{id,name},organizations:[{id,name,canCreateBusiness}],businesses:[Business],capabilities:{createBusiness,createOrganization}}
 * detail: {business:Business,team:[],work:[],initiatives:[],decisions:[],activity:[],budget,execution,capabilities,availability}
 * Business: {id,organizationId,name,mission,customer,firstOutcome,status,revision,leadership:"human_ceo"|"agent_ceo",authorityPreset}
 * budget: {currency:"USD",allowanceMicros:null|integer,reservedMicros:null|integer,spentMicros:null|integer,unknownCostOperations:null|integer,unknownCostMore:boolean,state}
 * availability: each collection is "available"|"unavailable"; omitted/malformed collections are unavailable, never an implied zero.
 * POST businesses: {idempotencyKey,organization:{id}|{name},name,mission,customer,firstOutcome,leadership,authorityPreset,modelAllowanceMicros:null|integer}
 * PATCH businesses/:id: {idempotencyKey,expectedRevision,action:"update_policy"|"pause"|"resume",leadership?,authorityPreset?}
 * Mutations return detail. Creation persists setup; execution.status/capabilities independently determine whether any work can run.
 */
const BASE = "/api/business/v1";
export const authorityPresets = [
  "advise",
  "execute_assigned",
  "take_initiative",
  "full_autonomy",
];
export class BusinessAPIError extends Error {
  constructor(message, status = 0, code = "request_failed") {
    super(message);
    this.status = status;
    this.code = code;
  }
}
const text = (value, fallback = "") =>
  typeof value === "string" ? value : fallback;
const record = (value) =>
  value && typeof value === "object" && !Array.isArray(value) ? value : {};
const integer = (value) =>
  Number.isSafeInteger(value) && value >= 0 ? value : null;
const id = (value) =>
  typeof value === "string" &&
  value.length > 0 &&
  value.length <= 256 &&
  !/[\u0000-\u001f]/.test(value)
    ? value
    : "";
const permissions = (raw) =>
  Object.fromEntries(
    Object.entries(record(raw)).map(([key, value]) => [key, value === true]),
  );
export function businessRecord(raw) {
  const value = record(raw);
  if (
    !id(value.id) ||
    !id(value.organizationId) ||
    integer(value.revision) === null ||
    !text(value.name)
  )
    throw new BusinessAPIError(
      "The business response is incomplete. Please try again.",
      502,
      "invalid_response",
    );
  return {
    ...value,
    id: id(value.id),
    organizationId: id(value.organizationId),
    name: text(value.name),
    mission: text(value.mission),
    customer: text(value.customer),
    firstOutcome: text(value.firstOutcome),
    status: text(value.status, "unknown"),
    revision: integer(value.revision),
    leadership: ["human_ceo", "agent_ceo"].includes(value.leadership)
      ? value.leadership
      : "unknown",
    authorityPreset: authorityPresets.includes(value.authorityPreset)
      ? value.authorityPreset
      : "unknown",
  };
}
export function normalizeContext(raw) {
  const value = record(raw);
  if (
    !id(value.viewer?.id) ||
    !Array.isArray(value.organizations) ||
    !Array.isArray(value.businesses)
  )
    throw new BusinessAPIError(
      "Your business workspace could not be loaded. Please try again.",
      502,
      "invalid_response",
    );
  return {
    viewer: {
      id: id(value.viewer.id),
      name: text(value.viewer.name, "Your account"),
    },
    organizations: value.organizations
      .filter((org) => id(org?.id))
      .map((org) => ({
        id: org.id,
        name: text(org.name, "Organization"),
        canCreateBusiness: org.canCreateBusiness === true,
      })),
    businesses: value.businesses.map(businessRecord),
    capabilities: permissions(value.capabilities),
  };
}
export function normalizeDetail(raw, expectedId) {
  const value = record(raw),
    business = businessRecord(value.business);
  if (expectedId && business.id !== expectedId)
    throw new BusinessAPIError(
      "The selected business changed. Refresh to continue.",
      409,
      "business_changed",
    );
  const detail = {
    business,
    capabilities: permissions(value.capabilities),
    availability: {},
    coverage: {
      teamMore: value.coverage?.teamMore === true,
      workMore: value.coverage?.workMore === true,
      limit: integer(value.coverage?.limit),
    },
    execution: {
      status: text(value.execution?.status, "unavailable"),
      reason: text(
        value.execution?.reason,
        "Execution is not connected for this business yet.",
      ),
    },
  };
  for (const key of ["team", "work", "initiatives", "decisions", "activity"]) {
    const available =
      Array.isArray(value[key]) && value.availability?.[key] === "available";
    detail.availability[key] = available ? "available" : "unavailable";
    detail[key] = available ? value[key].map(record) : [];
  }
  const budget = record(value.budget);
  detail.budget = {
    currency: budget.currency === "USD" ? "USD" : null,
    allowanceMicros: integer(budget.allowanceMicros),
    reservedMicros: integer(budget.reservedMicros),
    spentMicros: integer(budget.spentMicros),
    unknownCostOperations: integer(budget.unknownCostOperations),
    state: text(budget.state, "unavailable"),
    unknownCostMore: budget.unknownCostMore === true,
  };
  return detail;
}
// Validate the entire private result lineage before placing any content in UI.
export function normalizeWorkDetail(raw, expectedBusinessId, expectedWorkId) {
  const value = record(raw),
    business = businessRecord(value.business),
    work = record(value.work),
    employment = record(value.employment);
  const invalid = () => {
    throw new BusinessAPIError(
      "The work record is incomplete or changed. Refresh to continue.",
      502,
      "invalid_work_response",
    );
  };
  if (
    business.id !== expectedBusinessId ||
    work.id !== expectedWorkId ||
    work.businessId !== business.id ||
    !id(work.id) ||
    !text(work.objective) ||
    !id(work.employmentId)
  )
    invalid();
  if (
    employment.id !== work.employmentId ||
    employment.businessId !== business.id ||
    !id(employment.id)
  )
    invalid();
  if (
    value.visibility !== "private" ||
    value.coverage !== "complete" ||
    !Array.isArray(value.attempts) ||
    value.attempts.length > 10
  )
    invalid();
  const seen = new Set();
  const attempts = value.attempts
    .map((rawAttempt) => {
      const a = record(rawAttempt),
        operation = a.operation == null ? null : record(a.operation);
      if (
        !id(a.id) ||
        seen.has(a.id) ||
        !Number.isSafeInteger(a.ordinal) ||
        a.ordinal < 1 ||
        a.ordinal > 10 ||
        (operation && !id(operation.id))
      )
        invalid();
      seen.add(a.id);
      return {
        id: a.id,
        ordinal: a.ordinal,
        state: text(a.state, "unknown"),
        mode: text(a.mode, "unknown"),
        outcome: text(a.outcome),
        costState: text(a.costState, "unknown"),
        resultId: text(a.resultId),
        outcomeEvidenceRef: text(a.outcomeEvidenceRef),
        operation: operation
          ? {
              id: operation.id,
              requestDigest: text(operation.requestDigest),
              adapterId: text(operation.adapterId),
              routeRevision: text(operation.routeRevision),
              priceRevision: text(operation.priceRevision),
              maximumCostMicros: integer(operation.maximumCostMicros),
            }
          : null,
      };
    })
    .sort((a, b) => a.ordinal - b.ordinal);
  let result = null;
  if (value.result != null) {
    const r = record(value.result),
      attempt = attempts.find((a) => a.id === r.attemptId);
    if (
      !id(r.id) ||
      r.id !== work.resultId ||
      r.workId !== work.id ||
      !attempt ||
      attempt.resultId !== r.id ||
      attempt.operation?.id !== r.operationId ||
      !Number.isSafeInteger(r.generation) ||
      r.generation < 1 ||
      typeof r.content !== "string" ||
      r.content.length > 256000 ||
      r.contentType !== "text/markdown" ||
      !/^sha256:[0-9a-f]{64}$/.test(r.digest) ||
      typeof r.eligible !== "boolean"
    )
      invalid();
    result = {
      id: r.id,
      workId: r.workId,
      attemptId: r.attemptId,
      operationId: r.operationId,
      generation: r.generation,
      content: r.content,
      contentType: r.contentType,
      digest: r.digest,
      eligible: r.eligible,
      ineligibleReason: text(r.ineligibleReason),
      createdAt: text(r.createdAt),
    };
  } else if (work.resultId) invalid();
  return {
    business,
    work: {
      ...work,
      heldMicros: integer(work.heldMicros),
      settledMicros: integer(work.settledMicros),
      reservationMicros: integer(work.reservationMicros),
    },
    employment,
    attempts,
    result,
    visibility: "private",
    coverage: "complete",
    capabilities: permissions(value.capabilities),
  };
}
async function request(path, options = {}) {
  const controller = new AbortController(),
    timeout = setTimeout(() => controller.abort(), 20000);
  const external = options.signal;
  const abort = () => controller.abort();
  if (external?.aborted) controller.abort();
  else external?.addEventListener("abort", abort, { once: true });
  try {
    const response = await fetch(BASE + path, {
      ...options,
      signal: controller.signal,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...options.headers,
      },
    });
    const body = await response.json().catch(() => null);
    if (!response.ok) {
      const payload = record(body?.error);
      const message =
        {
          401: "Sign in again to open your businesses.",
          403: "You no longer have access to this business.",
          404: "This business is no longer available.",
          409: "This business changed. Refresh before making another change.",
          503: "Business services are not available on this instance yet.",
        }[response.status] ||
        text(
          payload.message,
          text(body?.message, "We couldn’t complete that request. Try again."),
        );
      throw new BusinessAPIError(
        message,
        response.status,
        text(payload.code, text(body?.code, "request_failed")),
      );
    }
    if (!body)
      throw new BusinessAPIError(
        "The server returned an unreadable response. Try again.",
        502,
        "invalid_response",
      );
    return body;
  } catch (error) {
    if (error instanceof BusinessAPIError) throw error;
    if (error.name === "AbortError")
      throw new BusinessAPIError(
        external?.aborted
          ? "Request cancelled."
          : "The request took too long. Retry to check the same operation.",
        0,
        "timeout",
      );
    throw new BusinessAPIError(
      "We couldn’t reach STRIDE. Check your connection and try again.",
      0,
      "network_error",
    );
  } finally {
    clearTimeout(timeout);
    external?.removeEventListener("abort", abort);
  }
}
export const businessAPI = {
  workDetail: async (businessId, workId, signal) =>
    normalizeWorkDetail(
      await request(
        `/businesses/${encodeURIComponent(businessId)}/work/${encodeURIComponent(workId)}`,
        { signal },
      ),
      businessId,
      workId,
    ),
  context: async (signal) =>
    normalizeContext(await request("/context", { signal })),
  detail: async (businessId, signal) =>
    normalizeDetail(
      await request(`/businesses/${encodeURIComponent(businessId)}`, {
        signal,
      }),
      businessId,
    ),
  create: async (payload) =>
    normalizeDetail(
      await request("/businesses", {
        method: "POST",
        body: JSON.stringify(payload),
      }),
    ),
  update: async (businessId, payload) =>
    normalizeDetail(
      await request(`/businesses/${encodeURIComponent(businessId)}`, {
        method: "PATCH",
        body: JSON.stringify(payload),
      }),
      businessId,
    ),
};
