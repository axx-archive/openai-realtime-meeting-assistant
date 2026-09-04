# Business operating console

Standalone, same-origin `/business` surface. `business.html` loads only these modules and existing local STRIDE brand/font assets. `api.js` defines the HTTP projection consumed by the console; PostgreSQL and authorization remain server responsibilities.

## Integration

- Context: `GET /api/business/v1/context`.
- Detail: `GET /api/business/v1/businesses/:id`.
- Setup: `POST /api/business/v1/businesses`.
- Policy/state: `PATCH /api/business/v1/businesses/:id`.
- All mutations carry a stable idempotency key. Updates carry `expectedRevision`.
- The server supplies each collection's explicit `availability: "available"`. Missing, unknown, or unavailable collections suppress rows; unknown money remains unknown.
- Model allowance uses integer USD micros and represents an owner-authorized internal model spending limit. It is separate from operating cash; setting it does not make a payment or purchase provider credits. The execution banner uses server execution state independently from a saved authority ceiling.
- Server responses must be private/no-store, reauthorize tenant membership and exact business on every request, bound collections, and return the same detail projection after mutation. No client-provided actor or organization selection is authority.
- Setup creates a draft. Choosing agent CEO does not create a team or start a provider call.

## Checks

`node --test public/business/api.test.mjs`

Five contract tests cover absent/invalid coverage, exact business and revision identity, stale unavailable rows, same-origin acknowledgement-loss retries, and typed denial/conflict errors.

Rendered in Chrome against an isolated HTTP fixture adapter outside the repository at `/tmp/stride-business-console-qa.py`. This is synthetic interface QA, not proof of database persistence or agent operation.

Verified:

- 1440px desktop overview, 834px tablet policy, 390px phone setup and overview, 320px phone overview with no horizontal overflow.
- Phone setup with agent CEO/full autonomy returns a saved draft with no invented team; unspecified allowance is shown unavailable.
- Policy save increments the returned revision; HTTP409 retains choices and offers an explicit refresh. Repeated unchanged retry sends the same operation key.
- HTTP403 clears the business, selector names, title, draft, and live announcement. The interface shows changed access and an actionable retry.
- Source guards ignore superseded detail responses and scope mutation completion to the business/view request. Setup acknowledgement after navigation does not take over the newly selected screen. Adversarial asynchronous browser scheduling remains an integration check.
- Collection rendering starts with six records; subsequent user actions reveal 25 at a time. Counts describe loaded records, not an asserted total in the database.
- Reduced motion removes animations/transitions. Forms have native labels, radio groups, focus treatment and phone-size input text. All result text is DOM text, and result links are restricted to same-origin paths.

Initial source payload is approximately 20 KB gzip excluding reused fonts. A warm local fixture navigation measured 15 ms to DOMContentLoaded with 1.6 ms script and 1.4 ms layout. These are local observations, not production/network performance guarantees.

## Real HTTP and PostgreSQL integration

The root-owned disposable preview on port 4320 uses `authenticateBusinessPerson`, a canonical session/person fixture, and the restricted SQL runtime connection. Rendered Chrome checks passed against this actual path:

- Phone at 390px created a new organization and `Fieldwork Studio · interface QA`, with agent CEO, full autonomy and a $25 model allowance. The result was a draft, with no connected agents or work. Directory refresh resolved the new organization name after the create receipt.
- Reload preserved business `biz_8d8794d0-7f52-419b-ad6d-fac526c82661` and its mission/allowance.
- Tablet at 834px changed leadership to human CEO and authority to take initiative. The server returned revision 2, and a further reload preserved both choices and that revision.
- Real team/work/activity availability remained unavailable. The console showed no synthetic seats or work, and null spend/unpriced coverage remained unknown.
- Both rendered viewports had document scrollWidth equal to clientWidth. Draft settings now explain the saved, unconnected state instead of showing a disabled pause control.

Independent root review accepted the interface as a clear, coherent operating foundation, with one correction: the authority value now has an explicit text label and no button-like chip. This does not establish whole-product visual acceptance or autonomous-business execution. No production data, commits or deployment are included in this console work.
