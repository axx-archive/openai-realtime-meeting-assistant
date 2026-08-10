"use strict";

const platforms = [
  { id: "desktop", label: "Desktop", hint: "Workbench" },
  { id: "iphone", label: "iPhone", hint: "Continuation" },
  { id: "ipad", label: "iPad", hint: "Adaptive workroom" }
];

const recoveryStates = new Set(["offline", "retrying", "stale", "pending_approval", "purge_failed"]);
const projectionStates = new Set(["ready", "pending_approval", "quarantined", "corrected", "restored"]);
const semanticFamilyByType = Object.freeze({
  "SignalControl": "signal-control",
  "HumanMessage": "human-message",
  "AgentContribution": "agent-contribution",
  "SystemEvent": "system-event",
  "SuggestedWorkCard": "suggested-work",
  "RunTimeline": "run-timeline",
  "InterventionRequest": "intervention-request",
  "ArtifactRevisionView": "artifact-revision",
  "OutcomeRecordView": "outcome-record",
  "WorkRecordSection": "work-record-section",
  "EvidenceCard": "evidence-card",
  "PublicWorkspaceView": "public-workspace",
  "PublicWorkObjectView": "public-work-object",
  "WorkstreamRow": "workstream-row",
  "PersonProfileView": "person-profile",
  "OrganizationProfileView": "organization-profile",
  "WorkspaceProfileView": "workspace-profile",
  "AgentProfileView": "agent-profile",
  "WorkSearchInterpretation": "search-interpretation",
  "WorkSearchResult": "search-result",
  "ContactRequestView": "contact-request",
  "ModerationCaseView": "moderation-case",
  "ConsensusDisplay": "consensus-display"
});
const appState = { fixture: null, journeyIndex: 0, platform: "desktop", requestedState: "idle", concurrentState: "", preview: false, progress: {}, previewProgress: {}, previewStates: {} };
const el = {};

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, character => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);
}

document.addEventListener("DOMContentLoaded", async () => {
  Object.assign(el, {
    app: document.querySelector("#app"), workbench: document.querySelector("#workbench"),
    platform: document.querySelector("#platform-switcher"), states: document.querySelector("#state-switcher"), concurrentStates: document.querySelector("#concurrent-state-switcher"),
    journeys: document.querySelector("#journey-list"), count: document.querySelector("#journey-count"),
    heading: document.querySelector("#canvas-heading"), stepper: document.querySelector("#stepper"), flowStatus: document.querySelector("#flow-status"),
    statePanel: document.querySelector("#state-panel"), semantic: document.querySelector("#semantic-grid"),
    actions: document.querySelector("#action-dock"), inspector: document.querySelector("#inspector"),
    mobileInspector: document.querySelector("#mobile-inspector-content"), gates: document.querySelector("#gate-summary"),
    preview: document.querySelector("#preview-toggle"),
    destinations: document.querySelector("#destination-nav"),
    shortcutsButton: document.querySelector("#shortcuts-button"), shortcuts: document.querySelector("#shortcuts-dialog")
  });

  try {
    const response = await fetch("fixture.json", { cache: "no-store" });
    if (!response.ok) throw new Error(`fixture ${response.status}`);
    appState.fixture = await response.json();
    appState.fixture.journeys.forEach(journey => { appState.progress[journey.id] = 0; appState.previewProgress[journey.id] = 0; appState.previewStates[journey.id] = "idle"; });
    setupControls();
    render();
    el.app.setAttribute("aria-busy", "false");
  } catch (error) {
    el.app.setAttribute("aria-busy", "false");
    el.heading.innerHTML = `<div><p class="eyebrow">Local fixture unavailable</p><h1>Serve this directory over HTTP</h1><p class="outcome">The harness does not fall back to embedded or synthetic data. See README.md for the local command.</p></div>`;
    console.error("PD0 reference fixture could not be loaded", error);
  }
});

function setupControls() {
  el.platform.innerHTML = platforms.map((platform, index) => `<button class="segment" type="button" role="radio" aria-checked="${index === 0}" data-platform="${escapeHTML(platform.id)}" tabindex="${index === 0 ? 0 : -1}">${escapeHTML(platform.label)}</button>`).join("");
  el.platform.addEventListener("click", event => {
    const button = event.target.closest("[data-platform]");
    if (button) setPlatform(button.dataset.platform, true);
  });
  el.platform.addEventListener("keydown", event => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const current = platforms.findIndex(item => item.id === appState.platform);
    const next = event.key === "Home" ? 0 : event.key === "End" ? platforms.length - 1 : (current + (event.key === "ArrowRight" ? 1 : -1) + platforms.length) % platforms.length;
    setPlatform(platforms[next].id, true);
  });

  el.states.innerHTML = appState.fixture.states.map(state => `<option value="${escapeHTML(state.id)}">${escapeHTML(state.label)}</option>`).join("");
  el.concurrentStates.innerHTML = `<option value="">None</option>${appState.fixture.states.map(state => `<option value="${escapeHTML(state.id)}">${escapeHTML(state.label)}</option>`).join("")}`;
  el.states.addEventListener("change", () => {
    const journey = currentJourney();
    appState.requestedState = journey.gates.length ? "feature_off" : el.states.value;
    render();
    announceContext(`Selected state ${currentState().label} for ${journey.name}`);
  });
  el.concurrentStates.addEventListener("change", () => {
    appState.concurrentState = el.concurrentStates.value;
    render();
    announceContext(`Resolved ${currentJourney().name} to ${currentState().label}`);
  });
  el.journeys.addEventListener("click", event => {
    const button = event.target.closest("[data-journey-index]");
    if (button) selectJourney(Number(button.dataset.journeyIndex), true);
  });
  el.destinations.addEventListener("click", event => {
    const button = event.target.closest("[data-destination]");
    if (!button) return;
    const index = appState.fixture.journeys.findIndex(journey => journey.destination === button.dataset.destination);
    if (index >= 0) selectJourney(index, true);
  });
  el.actions.addEventListener("click", event => {
    const button = event.target.closest("[data-flow-action]");
    if (button) transitionFlow(button.dataset.flowAction);
  });
  el.preview.addEventListener("click", () => {
    if (!currentJourney().gates.length) return;
    appState.preview = !appState.preview;
    render();
    announceContext(`${appState.preview ? "Opened" : "Closed"} nonauthoritative layout preview for ${currentJourney().name}`);
  });
  el.shortcutsButton.addEventListener("click", () => el.shortcuts.showModal());
  document.addEventListener("keydown", handleGlobalKeys);
}

function handleGlobalKeys(event) {
  if (event.metaKey || event.ctrlKey || event.altKey || event.target.matches("select, input, textarea")) return;
  if (event.key === "?") { event.preventDefault(); el.shortcuts.showModal(); return; }
  if (event.key.toLowerCase() === "s") { event.preventDefault(); el.states.focus(); return; }
  if (["1", "2", "3"].includes(event.key)) { setPlatform(platforms[Number(event.key) - 1].id, true); return; }
  if (event.key.toLowerCase() === "j") selectJourney((appState.journeyIndex + 1) % appState.fixture.journeys.length, true);
  if (event.key.toLowerCase() === "k") selectJourney((appState.journeyIndex - 1 + appState.fixture.journeys.length) % appState.fixture.journeys.length, true);
}

function currentJourney() { return appState.fixture.journeys[appState.journeyIndex]; }
function currentContract() { return appState.fixture.journeyContracts[currentJourney().id]; }
function currentSteps() { return currentContract().steps; }
function currentState() {
  const journey = currentJourney();
  const stateID = journey.gates.length ? "feature_off" : resolveState([appState.requestedState, appState.concurrentState]);
  return appState.fixture.states.find(state => state.id === stateID);
}
function resolveState(activeStates) {
  const active = new Set(activeStates.filter(Boolean));
  return appState.fixture.statePrecedence.find(state => active.has(state)) || "idle";
}

function setPlatform(platform, focus) {
  appState.platform = platform;
  el.workbench.className = `workbench platform-${platform}`;
  el.platform.querySelectorAll("[data-platform]").forEach(button => {
    const selected = button.dataset.platform === platform;
    button.setAttribute("aria-checked", String(selected));
    button.tabIndex = selected ? 0 : -1;
    if (selected && focus) button.focus();
  });
  render();
  announceContext(`Selected ${platforms.find(item => item.id === platform).label} expression for ${currentJourney().name}`);
}

function selectJourney(index, focusCanvas) {
  appState.journeyIndex = index;
  appState.preview = false;
  appState.concurrentState = "";
  appState.requestedState = currentJourney().gates.length ? "feature_off" : "idle";
  render();
  announceContext(`Selected ${currentJourney().id} ${currentJourney().name}; state ${currentState().label}`);
  if (focusCanvas) document.querySelector("#reference-canvas").focus({ preventScroll: false });
}

function transitionFlow(action) {
  const journey = currentJourney();
  const steps = currentSteps();
  if (journey.gates.length) { transitionPreview(action); return; }
  const step = appState.progress[journey.id];
  let announcement = "Journey state unchanged";
  if (action === "start") { appState.requestedState = "ready"; announcement = `Opened ${steps[0].screen}; step 1 of ${steps.length}`; }
  if (action === "advance") {
    if (appState.requestedState !== "ready" && appState.requestedState !== "restored" && appState.requestedState !== "corrected") return;
    if (step < steps.length - 1) {
      appState.progress[journey.id] = step + 1;
      appState.requestedState = "ready";
      announcement = `${steps[step].action}; opened ${steps[step + 1].screen}, step ${step + 2} of ${steps.length}`;
    } else {
      appState.requestedState = "terminal";
      announcement = currentContract().terminalOutcome;
    }
  }
  if (action === "back" && step > 0) {
    appState.progress[journey.id] = step - 1;
    appState.requestedState = "ready";
    announcement = `Back to ${steps[step - 1].screen}, step ${step} of ${steps.length}`;
  }
  if (action === "resume" && recoveryStates.has(appState.requestedState)) { appState.requestedState = "restored"; announcement = `Restored ${steps[step].screen} without replay`; }
  if (action === "restart") {
    appState.progress[journey.id] = 0;
    appState.requestedState = "idle";
    announcement = `Restarted ${journey.name} at idle step 1`;
  }
  appState.concurrentState = "";
  render();
  focusFlowStatus(announcement);
}

function transitionPreview(action) {
  const journey = currentJourney();
  const steps = currentSteps();
  if (!appState.preview) return;
  let step = appState.previewProgress[journey.id];
  let state = appState.previewStates[journey.id];
  let announcement = "Local preview state unchanged";
  if (action === "preview-start") { state = "ready"; announcement = `Opened nonauthoritative ${steps[0].screen}; production remains Feature off`; }
  if (action === "preview-advance" && ["ready", "restored"].includes(state)) {
    if (step < steps.length - 1) { const previous = step; step += 1; announcement = `${steps[previous].action}; opened nonauthoritative ${steps[step].screen}`; }
    else { state = "terminal"; announcement = currentContract().terminalOutcome; }
  }
  if (action === "preview-back" && step > 0) { step -= 1; state = "ready"; announcement = `Back in nonauthoritative preview to ${steps[step].screen}, step ${step + 1} of ${steps.length}`; }
  if (action === "preview-interrupt" && state === "ready") { state = "offline"; announcement = `Nonauthoritative preview interrupted at step ${step + 1}`; }
  if (action === "preview-resume" && state === "offline") { state = "restored"; announcement = `Nonauthoritative preview restored at step ${step + 1} without replay`; }
  if (action === "preview-restart") { step = 0; state = "idle"; announcement = `Restarted nonauthoritative preview at idle step 1`; }
  appState.previewProgress[journey.id] = step;
  appState.previewStates[journey.id] = state;
  render();
  focusFlowStatus(announcement);
}

function focusFlowStatus(message) {
  el.flowStatus.textContent = message;
  el.flowStatus.focus({ preventScroll: true });
}

function announceContext(message) {
  el.flowStatus.textContent = message;
}

function render() {
  const fixture = appState.fixture;
  const journey = currentJourney();
  const state = currentState();
  const gated = journey.gates.length > 0;
  const stepIndex = gated && appState.preview ? appState.previewProgress[journey.id] : appState.progress[journey.id];
  const contract = currentContract();
  const steps = contract.steps;
  el.states.value = gated ? state.id : appState.requestedState;
  el.concurrentStates.value = appState.concurrentState;
  el.count.textContent = fixture.journeys.length;
  el.destinations.innerHTML = fixture.destinations.map(destination => `<button type="button" class="destination-tab" data-destination="${escapeHTML(destination)}" aria-current="${destination === journey.destination ? "page" : "false"}">${escapeHTML(destination)}</button>`).join("");
  el.journeys.innerHTML = fixture.journeys.map((item, index) => `<button class="journey-button" type="button" data-journey-index="${index}" aria-current="${index === appState.journeyIndex ? "page" : "false"}"><span class="journey-id">${escapeHTML(item.id)}</span><span class="journey-name">${escapeHTML(item.name)}</span></button>`).join("");
  el.heading.innerHTML = `<div><p class="eyebrow">${escapeHTML(journey.id)} · ${escapeHTML(journey.kicker)}</p><h1>${escapeHTML(journey.name)}</h1><p class="outcome">${escapeHTML(journey.outcome)}</p></div><span class="destination-chip">${escapeHTML(journey.destination)}</span>`;
  el.stepper.innerHTML = steps.map((step, index) => `<span class="step ${index === stepIndex ? "current" : ""} ${index < stepIndex ? "complete" : ""}" ${index === stepIndex ? `aria-current="step"` : ""}><i aria-hidden="true">${index + 1}</i>${escapeHTML(step.screen)}</span>`).join("");

  const showState = state.id !== "ready";
  el.statePanel.hidden = !showState;
  el.statePanel.dataset.state = state.id;
  el.statePanel.innerHTML = showState ? `<strong>${escapeHTML(state.label)}</strong><p>${escapeHTML(state.copy)}${gated ? " Every parent is false; no child projection or action is admitted for any requested state." : ""}</p>` : "";
  el.semantic.innerHTML = renderProjection(journey, state, stepIndex);
  el.actions.innerHTML = renderActions(journey, state, stepIndex);
  const context = inspector(journey, state, stepIndex);
  el.inspector.innerHTML = context;
  el.mobileInspector.innerHTML = context;
  el.gates.innerHTML = gated ? `<span>Required parents</span>${journey.gates.map(gate => `<span class="gate-pill">${escapeHTML(gate)} OFF</span>`).join("")}` : `<span class="gate-pill private-pill">Private fictional flow · no live calls</span>`;
  el.preview.hidden = !gated;
  el.preview.setAttribute("aria-pressed", String(appState.preview));
  el.preview.textContent = appState.preview ? "Hide layout preview" : "Show layout preview";
}

function renderProjection(journey, state, stepIndex) {
  if (journey.gates.length && !appState.preview) return opaqueStateCard(state, "Governed child projection not admitted");
  if (journey.gates.length) {
    const localState = appState.previewStates[journey.id];
    const visibleCount = localState === "idle" ? 0 : Math.max(1, Math.ceil((stepIndex + 1) / currentSteps().length * journey.components.length));
    const localCard = localState === "offline" ? `<article class="semantic-card wide opaque-card"><div class="card-meta"><span class="principal system">SystemEvent</span><span>local preview offline</span></div><h2>Preview progression held</h2><p>No field-shape step was advanced or replayed. Resume affects local reference progress only.</p></article>` : journey.components.slice(0, visibleCount).map(component => componentCard(component, true)).join("");
    return `<div class="preview-banner" role="note"><strong>Nonauthoritative layout preview · ${escapeHTML(humanize(localState))}</strong><p>Parents remain false. These are component names and field shapes only—not current state, admitted values, identity, authority, or an available production action.</p></div>${localCard}`;
  }
  if (state.id === "terminal") return `<article class="semantic-card wide completion-card"><div class="card-meta"><span class="principal system">SystemEvent</span><span>fictional completion</span></div><h2>${escapeHTML(journey.name)} complete</h2><p>${escapeHTML(currentContract().terminalOutcome)} It created no authority, provider work, persistence, or external effect.</p></article>`;
  if (!projectionStates.has(state.id)) return opaqueStateCard(state, state.id === "idle" ? "Start the fictional journey" : "Reference projection held");
  const visibleCount = Math.max(1, Math.ceil((stepIndex + 1) / currentSteps().length * journey.components.length));
  return journey.components.slice(0, visibleCount).map(component => componentCard(component, false)).join("");
}

function opaqueStateCard(state, title) {
  return `<article class="semantic-card wide opaque-card"><div class="card-meta"><span class="principal system">SystemEvent</span><span>${escapeHTML(state.id)}</span></div><h2>${escapeHTML(title)}</h2><p>${escapeHTML(state.copy)} No private body, inferred identity, cached authority, synthetic result, or child projection is shown.</p><div class="trust-strip"><span>body-minimized</span><span>no authority inferred</span><span>no child fields</span></div></article>`;
}

function componentCard(component, preview) {
  const family = semanticFamilyByType[component.type];
  if (!family) return opaqueStateCard({ id: "unavailable", copy: "The semantic component contract is unavailable." }, "Unknown semantic component");
  const actorClass = component.type === "HumanMessage" ? "human" : component.type === "AgentContribution" || component.type === "AgentProfileView" ? "agent" : component.type === "SystemEvent" ? "system" : "governed";
  const rows = semanticFieldRows(component, preview);
  const body = semanticComponentBody(component, family, rows, preview);
  return `<article class="semantic-card typed-card family-${escapeHTML(family)} ${preview ? "preview-card" : ""}" data-component-type="${escapeHTML(component.type)}" data-semantic-family="${escapeHTML(family)}"><div class="card-meta"><span class="principal ${actorClass}">${escapeHTML(component.type)}</span><span>${preview ? "nonauthoritative preview" : "fictional typed fixture"}</span></div><h2>${preview ? `Layout: ${escapeHTML(component.type)}` : escapeHTML(component.title)}</h2>${body}${preview ? "" : `<div class="trust-strip"><span>Why can I see this?</span><span>What is unknown?</span><span>View provenance</span></div>`}</article>`;
}

function semanticFieldRows(component, preview) {
  return Object.entries(component.fields).map(([key, value]) => `<div data-field="${escapeHTML(key)}"><dt>${escapeHTML(humanize(key))}</dt><dd>${preview ? "field shape only · no current value" : escapeHTML(value)}</dd></div>`).join("");
}

function semanticComponentBody(component, family, rows, preview) {
  const marker = preview ? "Field-shape contract" : semanticFamilyLabel(family);
  switch (component.type) {
    case "SignalControl": return `<section class="semantic-structure signal-console"><p class="semantic-mark">${marker}</p><div class="signal-orbit" aria-hidden="true"><i></i><i></i><i></i></div><dl class="field-list">${rows}</dl></section>`;
    case "HumanMessage": return `<section class="semantic-structure message-bubble human-bubble"><p class="semantic-mark">Human-authored message</p><dl class="field-list">${rows}</dl></section>`;
    case "AgentContribution": return `<section class="semantic-structure agent-attribution"><header><span>Agent output</span><span>Accountable human required</span></header><dl class="field-list">${rows}</dl></section>`;
    case "SystemEvent": return `<section class="semantic-structure system-ledger"><span class="ledger-node" aria-hidden="true"></span><div><p class="semantic-mark">Non-social system fact</p><dl class="field-list">${rows}</dl></div></section>`;
    case "SuggestedWorkCard": return `<section class="semantic-structure suggestion-contract"><header><span>Proposed scope</span><span>Human decision</span></header><div class="decision-boundary"><strong>No run yet</strong><span>Approve or dismiss exact revision</span></div><dl class="field-list">${rows}</dl></section>`;
    case "RunTimeline": return `<section class="semantic-structure run-track"><ol aria-label="Run phases"><li>Queued</li><li>Current actor</li><li>Verification</li></ol><dl class="field-list">${rows}</dl></section>`;
    case "InterventionRequest": return `<section class="semantic-structure intervention-boundary"><p class="semantic-mark">Bounded human intervention</p><div class="choice-slot">One closed choice · exact consequence</div><dl class="field-list">${rows}</dl></section>`;
    case "ArtifactRevisionView": return `<section class="semantic-structure artifact-sheet"><header><span>Source provenance</span><span>Revision</span><span>Review</span></header><div class="artifact-page" aria-label="Artifact revision structure"></div><dl class="field-list">${rows}</dl></section>`;
    case "OutcomeRecordView": return `<section class="semantic-structure outcome-verdict"><div class="outcome-axis"><span>Evidence</span><span>Human review</span><span>Verification</span></div><dl class="field-list">${rows}</dl></section>`;
    case "WorkRecordSection": return `<section class="semantic-structure record-section"><aside>Private Work Record</aside><div><p class="semantic-mark">Section controller and release state</p><dl class="field-list">${rows}</dl></div></section>`;
    case "EvidenceCard": return `<section class="semantic-structure evidence-chain"><ol aria-label="Evidence lineage"><li>Claim</li><li>Attestation</li><li>Approval</li></ol><dl class="field-list">${rows}</dl></section>`;
    case "PublicWorkspaceView": return `<section class="semantic-structure workspace-outline"><nav aria-label="Workspace structure">Purpose · Participation · Moderation</nav><dl class="field-list">${rows}</dl></section>`;
    case "PublicWorkObjectView": return `<section class="semantic-structure work-object-document"><header>Typed public object</header><div class="object-provenance">Authorship → provenance → release</div><dl class="field-list">${rows}</dl></section>`;
    case "WorkstreamRow": return `<section class="semantic-structure chronological-row"><time>09:44</time><div><p class="semantic-mark">Chronological, not ranked</p><dl class="field-list">${rows}</dl></div></section>`;
    case "PersonProfileView": return `<section class="semantic-structure profile-person"><div class="profile-avatar" aria-hidden="true">P</div><div><p class="semantic-mark">Person · opted-in fields</p><dl class="field-list">${rows}</dl></div></section>`;
    case "OrganizationProfileView": return `<section class="semantic-structure profile-organization"><header>Organization roles and evidence</header><dl class="field-list">${rows}</dl></section>`;
    case "WorkspaceProfileView": return `<section class="semantic-structure profile-workspace"><aside>Workspace purpose</aside><div><p class="semantic-mark">Participation and current objects</p><dl class="field-list">${rows}</dl></div></section>`;
    case "AgentProfileView": return `<section class="semantic-structure profile-agent"><header><span>Agent</span><span>Human sponsor</span></header><p class="semantic-mark">Machine identity · no human rights</p><dl class="field-list">${rows}</dl></section>`;
    case "WorkSearchInterpretation": return `<section class="semantic-structure search-proposal"><div class="search-query-shape">Interpretation before retrieval</div><dl class="field-list">${rows}</dl></section>`;
    case "WorkSearchResult": return `<section class="semantic-structure search-disclosure"><header>Recorded disclosure</header><div class="why-unknown"><span>Why</span><span>Unknown</span></div><dl class="field-list">${rows}</dl></section>`;
    case "ContactRequestView": return `<section class="semantic-structure contact-envelope"><header>Purpose-bound request</header><div class="sealed-channel">Channel remains sealed</div><dl class="field-list">${rows}</dl></section>`;
    case "ModerationCaseView": return `<section class="semantic-structure moderation-docket"><header>Private case docket</header><div class="role-separation"><span>Reviewer</span><span>Separate appeal</span></div><dl class="field-list">${rows}</dl></section>`;
    case "ConsensusDisplay": return `<section class="semantic-structure consensus-board"><header>Eligible humans only</header><div class="consensus-aggregate">Aggregate withheld until admitted</div><dl class="field-list">${rows}</dl></section>`;
    default: return `<section class="semantic-structure unavailable-structure"><p>Closed component renderer unavailable.</p></section>`;
  }
}

function semanticFamilyLabel(family) {
  return humanize(family).replaceAll("-", " ");
}

function renderActions(journey, state, stepIndex) {
  const gated = journey.gates.length > 0;
  const steps = currentSteps();
  if (gated && !appState.preview) return `<p class="action-note">All required parents are false. The separate local preview is off.</p><button class="primary-button" type="button" disabled>Feature off</button>`;
  if (gated) {
    const localState = appState.previewStates[journey.id];
    if (localState === "idle") return `<p class="action-note">Starts local field-shape progression only. Production state remains Feature off.</p><button class="primary-button" type="button" data-flow-action="preview-start">Preview ${escapeHTML(steps[0].screen)}</button>`;
    if (localState === "offline") return `<p class="action-note">Local preview progress is held without replay.</p><button class="secondary-button" type="button" data-flow-action="preview-back" ${stepIndex === 0 ? "disabled" : ""}>Back preview</button><button class="primary-button" type="button" data-flow-action="preview-resume">Resume preview</button>`;
    if (localState === "terminal") return `<p class="action-note">Terminal local preview. Production state is still Feature off.</p><button class="secondary-button" type="button" data-flow-action="preview-restart">Restart preview</button>`;
    return `<p class="action-note">${escapeHTML(steps[stepIndex].screen)} · local preview only; no current values or production actions.</p><button class="secondary-button" type="button" data-flow-action="preview-back" ${stepIndex === 0 ? "disabled" : ""}>Back preview</button><button class="secondary-button" type="button" data-flow-action="preview-interrupt">Simulate interruption</button><button class="primary-button" type="button" data-flow-action="preview-advance">${escapeHTML(steps[stepIndex].action)}</button>`;
  }
  if (state.id === "terminal") return `<p class="action-note">Terminal fictional state. Restart resets only local in-memory progress.</p><button class="secondary-button" type="button" data-flow-action="restart">Restart journey</button>`;
  if (state.id === "idle") return `<p class="action-note">Fictional interaction only. Opening this destination creates no external effect.</p><button class="primary-button" type="button" data-flow-action="start">Open ${escapeHTML(steps[0].screen)}</button>`;
  if (recoveryStates.has(state.id)) return `<p class="action-note">Resume reconciles the exact fictional step without replaying an effect.</p><button class="secondary-button" type="button" data-flow-action="back" ${stepIndex === 0 ? "disabled" : ""}>Back</button><button class="primary-button" type="button" data-flow-action="resume">Resume safely</button>`;
  const canAdvance = ["ready", "restored", "corrected"].includes(state.id);
  return `<p class="action-note">${escapeHTML(steps[stepIndex].screen)} · step ${stepIndex + 1} of ${steps.length}. No data leaves this directory.</p><button class="secondary-button" type="button" data-flow-action="back" ${stepIndex === 0 ? "disabled" : ""}>Back</button><button class="primary-button" type="button" data-flow-action="advance" ${canAdvance ? "" : "disabled"}>${escapeHTML(steps[stepIndex].action)}</button>`;
}

function inspector(journey, state, stepIndex) {
  const platform = platforms.find(item => item.id === appState.platform);
  const step = currentSteps()[stepIndex];
  return `<p class="eyebrow">${escapeHTML(platform.label)} expression</p><h2>${escapeHTML(platform.hint)}</h2><p class="inspector-copy">${escapeHTML(journey.compositions[appState.platform])}</p>
  <section class="inspector-section"><h3>${journey.gates.length && appState.preview ? "Local preview task" : "Current task"}</h3><p>${stepIndex + 1} of ${currentSteps().length}: ${escapeHTML(step.screen)}. Action: ${escapeHTML(step.action)}. Next: ${escapeHTML(step.transition)}. ${journey.gates.length && appState.preview ? `Preview state: ${escapeHTML(humanize(appState.previewStates[journey.id]))}; production state: Feature off.` : `State: ${escapeHTML(state.label)}.`}</p></section>
  <section class="inspector-section"><h3>Hard-state contract</h3><p>${escapeHTML(journey.hardState)}</p></section>
  <section class="inspector-section"><h3>Required gates</h3>${journey.gates.length ? `<div class="gate-list">${journey.gates.map(gate => `<span>${escapeHTML(gate)} OFF</span>`).join("")}</div>` : `<p>No public activation gate. Interaction remains a fictional local reference.</p>`}</section>
  <section class="inspector-section"><h3>Fixture boundary</h3><p>No production people, bodies, source digests, contact channels, sessions, scores, provider output, or live calls.</p></section>`;
}

function humanize(value) { return value.replace(/([a-z])([A-Z])/g, "$1 $2").replaceAll("_", " "); }
