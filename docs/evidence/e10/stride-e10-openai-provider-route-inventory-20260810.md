# STRIDE E10 OpenAI-only provider route inventory

Date: 2026-08-10  
Status: read-only source inventory complete; implementation and activation pending  
Decision authority: AJ's controlling direction is OpenAI for every provider-backed
generative/inference lane. New Anthropic product traffic, retries, and fallback
are not authorized. Governed non-generative source systems such as Fiscal may
remain explicit data providers under their existing source/egress authority;
they are not a substitute model route and must not be silently removed.
Historical Anthropic receipts and usage records remain readable.

## Evidence boundary

This inventory read the current local source, tests, deployment examples, and
the existing AJA acceptance receipt. It made no code, configuration, provider,
account, production, user-data, Git, or deployment mutation and ran no tests.
Line references are the reviewed working-tree boundary and must be refreshed if
the implementation changes before its successor receipt is sealed.

## Current route matrix

| Capability | Current route | OpenAI-only disposition |
|---|---|---|
| Scout routing, chat and extraction | OpenAI Responses, closed models and route dials in `scout_openai_routes.go:12-59`; strict router schema starts at `:73` | preserve; Anthropic must remain unable to receive core Scout traffic |
| Fiscal financial grounding | non-generative Fiscal MCP source at `fiscal_client.go:3-25,41-62,88-118`; exposed to admitted agent tools at `kanban.go:2419-2420,2769-2790` | retain only as governed source egress for research/comps/presentation grounding; revalidate source authority, privacy, bounds and receipts independently from OpenAI model routing |
| Research and Deep Research | forced `openai_text` in `agent_runner_iface.go:243-260`; hosted web search in `agent_thread_runner.go:1400-1417`; default `gpt-5.6-sol/high` at `:1430-1467` | already exact OpenAI; preserve citation, current-source and terminal projection gates |
| Ordinary documents and briefs | `/assistant/goal` enters `launchGoalThread` at `main.go:2152-2248`; the goal engine is hard-wired to `anthropicMessagesResponder` and Anthropic key/model contracts in `goal_engine.go:396-430,4286-4327` | blocking conversion to provider-neutral strict goal calls backed by OpenAI Responses |
| Artifact follow-ups | non-research prefers Anthropic when configured; OpenAI fallback is `agent_thread_followup.go:497-531` | make OpenAI sole current route; first close attachment parity because OpenAI currently omits authorized file/image content |
| Deck outline | tool contract at `tool_registry.go:229-253`, then Anthropic-hard goal engine | convert with the goal engine; prove narrative beats, slide evidence and attachment parity |
| Packaging Studio presentations | process definition at `process_definitions.go:12-17,413-424`; authored pipeline at `packaging_studio.go:206-445`; model-backed stages transit the Anthropic goal engine | primary conversion blocker; preserve the authored process/checkpoint state machine and replace only strict model calls |
| Packaging imagery | art direction is a goal-engine stage; generation is OpenAI Images `gpt-image-2` in `openai_images.go:46-90,152-183` | preserve generation; convert art direction with the goal engine |
| Direct Scout images | OpenAI-only durable lifecycle in `scout_chat_images.go:1-16,77-102` | preserve |
| Slide jury | Anthropic-only multimodal wrapper in `slide_jury.go:243-335`; keyless jury is a disclosed skip in `packaging_studio.go:825-870` | implement Responses-native multimodal jury with exact page/artifact binding and strict scoreboard schema |
| Venture workbook | deterministic XLSX, `providerCalls=0` in `scout_venture_workbook.go:247-278` | preserve providerless behavior, ACL, Drive binary and publication denial |
| Mixed package compile/render/export | deterministic Go compile at `packaging_studio.go:410-418` plus sidecar HTML/PDF | preserve; upstream authored content and jury remain blocked until converted |
| Voice and Realtime | OpenAI Realtime in `kanban.go:1819-2004` | preserve |
| Dictation/file transcription | OpenAI transcription and key gate in `transcribe_dictation.go:73-83,123-165,259+` | preserve |
| Embeddings/semantic recall | OpenAI `text-embedding-3-small`, bounded breaker and lexical fallback in `embeddings.go:55-126,585-670,1309-1380` | preserve privacy allowlist, no-chat embedding and breaker evidence |
| W6 Work Search parser/rerank | deterministic closed parser; provider/reranker switches default off | preserve providerless qualification; do not turn it on merely to satisfy provider consolidation |
| Narrative maintainer | Anthropic when key exists, otherwise OpenAI at `narrative_maintainer.go:288-320` | remove key-presence routing and admit only OpenAI for new work; retain historical provider decoding |
| House style and taste analyst | Anthropic-only startup/responders in `house_style.go:135-155,195-208` and `taste_analyst.go:161-191,232-246` | convert or explicitly leave disabled with the presentation-quality gap visible |
| Insights and Opportunities | Claude seat names and Anthropic production calls in `insights_opportunities.go:120-122` and `insights_opportunities_production.go:334,409` | separate conversion; remain default off until exact OpenAI contracts and qualification exist |
| Ambient replay | historical provider selection can include Anthropic | retain historical replay compatibility; close new Anthropic selection without rewriting old receipts |
| Goal admission, restart and Grill | goal launch rejects without Anthropic key at `goal_engine.go:682-696`; restart recovery skips at `:3978-3996`; Grill objection loop skips at `grill.go:791-804` | convert admission/restart/Grill gates with the responder so OpenAI-only goals can launch and recover without any Anthropic key |
| Runner selection and persisted assignment | `BONFIRE_EXECUTION_RUNNER=anthropic_fable` and persisted assigned-runner metadata can select Anthropic in `agent_runner_iface.go:216-240,243-264,286-308` | deny new Anthropic admission, migrate current runner selection deliberately, and preserve old receipts as historical metadata only |

## Shared implementation seam

The existing `openai_responses.go` owns `/v1/responses`, structured output,
attachments, hosted search, `store:false`, and bounded transport/error handling.
The conversion must reuse that seam rather than adding another HTTP client. The
goal process remains the authored orchestrator. The current Fable tool loop is
bounded by allowlists and authority policy in
`agent_runner_anthropic.go:1152-1200`; it can expose Board, memory, files,
coworkers, Fiscal and goal-progress tools. The OpenAI migration must either add
a bounded Responses function-tool loop with the same per-call current-authority
checks, loop/tool budgets, source receipts and revocation behavior, or freeze an
explicit capability-retirement matrix accepted by the Product owner. A one-shot
`openai_text` call with hosted search alone is not parity and cannot silently
replace that loop.

## Blocking correctness gaps

1. The goal engine's responder, key accessor, request types, refusal handling,
   and tests are structurally Anthropic-specific, not merely model-configured.
2. OpenAI follow-ups currently omit authorized PDF/image attachment content;
   `attachments.go:944-1005,1112-1127` already has the necessary OpenAI wire
   support, but `agent_thread_followup.go:517-531` does not use it.
3. Slide jury is bound to Anthropic image blocks and provider-specific caps;
   it lacks a Responses-native strict production path.
4. House-style and taste inputs silently disappear without Anthropic, which
   would change Packaging Studio quality without a visible product state.
5. Health, economics, operator configuration and web provenance still contain
   active Anthropic/Fable language, including `capability_health.go:573-584`,
   `stride_routing_economics.go:655`, deployment docs/env examples, and
   `index.html:38563-38564`.
6. The historical AJA matrix proves the gap: research/image/workbook completed,
   while one-pager and Packaging Studio stopped on Anthropic `low_credit`.
7. Goal launch, restart recovery and Grill currently treat Anthropic key
   presence as capability admission. Replacing only the responder would still
   leave OpenAI-only goals unavailable or unrecoverable.
8. Runtime and persisted runner selection can still admit `anthropic_fable`;
   those paths must be closed before zero-Anthropic-traffic can be proven.
9. Fiscal is a separately governed non-generative data source. Removing it as
   if it were a model provider would silently weaken research, comps and deck
   grounding; retaining it requires explicit source/egress evidence.

## Dependency-ordered conversion

1. Freeze the exact retained-tool matrix. Implement an authority-gated bounded
   Responses function-tool loop for every retained Board/memory/file/coworker/
   Fiscal/progress capability, or obtain an explicit retirement decision before
   changing behavior.
2. Add a provider-neutral goal-call contract backed by OpenAI Responses strict
   output with explicit orchestration and review model/reasoning dials.
3. Convert goal-engine decompose/panel/synthesis/gate/review/verify plus launch,
   restart-recovery and Grill admission gates. This unblocks documents and
   non-jury Packaging Studio stages without requiring an Anthropic key.
4. Make `openai_text` the only admitted normal agent runner; reject new env or
   persisted `anthropic_fable` selection while preserving historical labels.
5. Close OpenAI follow-up attachment parity with authority-drift, PDF, image,
   restart and no-provider-after-revocation hard negatives.
6. Implement a Responses-native multimodal slide jury with strict scoreboard,
   exact rendered-page binding and provider-neutral byte/page caps.
7. Convert house style, taste analyst, narrative and Insights/Opportunities, or
   keep each explicitly unavailable until its OpenAI lane is accepted.
8. Preserve Fiscal only through its existing separately governed source lane;
   add explicit egress, unavailable, malformed and no-source-authority tests.
9. Remove active Anthropic config/copy/admission without deleting historical
   usage/pricing/provider metadata required to read prior receipts.
10. Add integration proof that an installed Anthropic key receives zero current
   product traffic across research, documents, follow-ups, images, decks and
   mixed packages.
11. Run focused normal/race gates, full `go test ./...`, broad Scout/goal/deck
   races, `go vet ./...`, diff checks, rendered deck/PDF QA, and provider-host
   network evidence.
12. Seal an OpenAI-only successor receipt and independently critic it before
    any configuration, release, production run, provider spend, or activation.

## Exact stale-test and configuration migration matrix

| Contract to reverse or preserve | Exact current evidence | Successor requirement |
|---|---|---|
| Anthropic default and explicit Fable selection | `agent_runner_iface_test.go`; runtime dials in `agent_runner_iface.go:216-240,258-264,286-308` | new Anthropic selection rejected; historical assignment remains readable; installed key receives zero current traffic |
| Anthropic follow-up preference | `agent_thread_followup_test.go:444+` | OpenAI sole route plus authorized PDF/image attachment parity and authority-drift negatives |
| Anthropic multimodal jury | `slide_jury_test.go:272+`, `packaging_studio_test.go:979+` | strict Responses-native multimodal request/result, exact pages, caps, refusal and malformed-score negatives |
| Attachment wire/exclusion | `attachments_test.go:421+,819+` | preserve exact OpenAI input-file/image shape and prove Anthropic exclusion across follow-up/restart |
| Fiscal data source | `fiscal_client_test.go`, `fiscal_tools_test.go`, `fiscal_prompts_test.go` | retain governed non-generative source behavior or bind an explicit retirement decision; never reclassify it as OpenAI model traffic |
| Provider host inventory | `provider_clients_test.go:60`, `usage_ledger_test.go:98+` | OpenAI generative hosts admitted; Fiscal separately labeled source egress; Anthropic denied for new product calls; historical usage parses |
| Goal launch/recovery/panel state | `goal_engine_test.go` and `goal_engine.go:682-696,3978-3996` | OpenAI key/model/route admission, crash recovery and exact state machine without Anthropic key |
| Grill objection loop | `grill_test.go`, `grill.go:791-804` | OpenAI-backed or explicit disabled state; no silent key-presence skip |
| Health and economics | `capability_health_test.go`, `stride_routing_economics_test.go` | OpenAI-only current capability truth, costs and unavailable reasons; historical Anthropic evidence retained |
| Core Anthropic wire/history | `agent_runner_anthropic_test.go`, `anthropic_text_test.go`, `usage_ledger_test.go`, `models_pricing_test.go` | remove new product admission without deleting historical decode/price/receipt compatibility |
| Packaging and render | `packaging_studio_test.go`, `artifact_render_test.go`, `render_runner_test.go`, `report_print_test.go` | all authored stages, images, compile, jury, PDF, restart, review and no-publish/no-send remain exact |
| Operator configuration and isolation | `deploy/digitalocean/.env.example`, `deploy/digitalocean/docker-compose.yml`, `deploy/digitalocean/README.md`, `deploy/e9/worker-isolation-policy.json` | no active Anthropic config/admission copy; exact OpenAI routes and separately governed Fiscal egress; historical docs clearly time-bounded |

## Presentation acceptance after conversion

A presentation is not accepted because an outline or deterministic HTML shell
exists. The real AJA run must produce an editable deck, current OpenAI-generated
imagery where requested, self-contained preview, complete all-page PDF/export,
strict rendered-slide jury result, source/evidence bindings, founder
send-back/rebuild behavior, restart/idempotency proof, accurate card stages and
interventions, rich web/iPhone/iPad preview, Drive receipt, provider/project/
model/cost receipt, and private no-publish/no-send proof. Any missing element is
an honest partial or failure, never a completed presentation.

## Exclusions

- No provider call, spend, config change, deployment, release or activation.
- No Anthropic retry or fallback.
- OpenAI-only governs generative/inference traffic; it does not silently remove
  an independently governed non-generative source such as Fiscal.
- No rewrite of historical receipts or provider usage.
- No change to deterministic workbook/package/render lanes.
- No claim that documents, slides, mixed packages or native rich previews are
  accepted today.

## Independent critic

`/root/w0_code_inventory` returned **PASS** against reviewed artifact SHA-256
`39a629362c45fef94e63a88bafcaaacf21b8bfdfa234fdcabf1e16a55f388c0b`.
The read-only gate verified exact source claims, Fiscal's separately governed
non-generative role, bounded tool parity/retirement ordering, launch/restart/
Grill and persisted-runner admission, stale-test/config coverage, historical
compatibility, fail-closed boundaries, and the absence of activation or current
product-completion overclaim. This one-way record does not self-attest its new
containing artifact hash.
