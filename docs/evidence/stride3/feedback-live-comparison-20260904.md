# Prior feedback: controlled model comparison

2026-09-04. Two real OpenAI Responses calls, same server-selected `gpt-5.6-terra` / high reasoning, identical instructions and synthetic next-pilot request. Only the application-generated prior-feedback context changes. No tools, external actions, production data or customer messages. The requested maximum was 4,000 output tokens per call; observed usage totaled 1,769 tokens across both calls.

The control returned a generic pilot definition plan. The feedback variant focused on willingness to pay and explicitly cited the prior reported failure. This demonstrates a context-dependent change in one sample. It does not establish causal business improvement, model qualification, or a complete production outcome loop. Its cohort size, thresholds and pricing design are model proposals, not measured company facts.

Application tests separately proved exact source binding, current audiences, causal cutoff, restart replay, and refusal after feedback correction, account disablement or access loss. The live comparison used exported application context; it did not run the full production Work pipeline. Request/output hashes and actual model/usage are in the adjacent JSON receipt.

## Control: feedback disabled

### Next Pilot Plan

**Objective:** Establish a minimally scoped, evidence-ready pilot once the research question, target users, and success criteria are authorized.

**Assumptions**
- No prior findings, baseline metrics, participant definition, or intervention details are currently authorized.
- The pilot should therefore begin with a short design-validation phase rather than testing unverified hypotheses.

**Phase 1 — Pilot definition (1–2 working sessions)**
1. Define the decision the pilot must inform.
2. Specify target participant/user segment and recruitment constraints.
3. Select one intervention or workflow variant to test.
4. Set 1–3 measurable success metrics, plus safety/quality guardrails.
5. Document baseline measurement method and stop/continue thresholds.

**Phase 2 — Small controlled run**
1. Recruit a small, representative cohort using approved criteria.
2. Run the intervention for a fixed, predeclared period.
3. Capture:
   - Outcome metrics
   - Participation/completion rates
   - Operational effort and failures
   - Structured qualitative feedback
4. Log deviations, confounders, and any adverse effects.

**Phase 3 — Review and decision**
- Compare results with the predeclared thresholds.
- Decide: scale, revise and rerun, or stop.
- Produce a brief findings memo separating observations from assumptions.

**Immediate next action:** Obtain authorization for the research question, target population, intervention, timeline, and success metrics; without these, a specific pilot design would be speculative.

## Prior feedback included

**Next pilot: willingness-to-pay validation (2 weeks)**

**Objective:** Test whether the offering has paid demand, rather than optimizing engagement. This prioritization follows the unverified reported outcome that paid adoption was flat and recommends testing willingness to pay [work-feedback-work-review-abfdcda5b1b73a76640c26130605db77].

**Design**
1. Define one paid offer: clear outcome, scope, delivery timeline, and a single price.
2. Recruit 30 eligible prospects from the currently reachable audience; document eligibility before outreach.
3. Randomly assign prospects to one of two price points (e.g., standard price and +25%) with identical positioning.
4. Use a landing page or direct sales script that requests a real commitment: refundable deposit or signed paid intent—not a survey response.
5. Record: invitations sent, qualified conversations, offer views, deposits/commitments, price point, stated objection, and refunds/cancellations.

**Pre-registered success criteria**
- Primary: at least 20% of qualified prospects make a refundable paid commitment at either price.
- Secondary: the higher-price group retains at least 70% of the lower-price group’s commitment rate.
- Guardrail: fewer than 10% of commitments are refunded within seven days.

**Decision rule**
- If primary and guardrail pass, run a small paid-delivery cohort at the best-performing price.
- If interest is high but payment fails, conduct five structured objection interviews and revise value proposition or packaging.
- If qualified interest is low, pause pricing work and revisit target segment/problem.

**Assumptions:** access to 30 eligible prospects, permission to collect refundable deposits, and a deliverable paid offer.
