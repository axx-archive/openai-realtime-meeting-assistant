import { businessAPI, authorityPresets } from "./api.js";
const main = document.querySelector("#main"),
  nav = document.querySelector("#navigation"),
  switcher = document.querySelector("#business-switcher"),
  topActions = document.querySelector("#top-actions");
const state = {
  context: null,
  detail: null,
  loading: false,
  error: null,
  request: 0,
  controller: null,
  setupDraft: null,
  mutation: null,
};
const views = [
  { id: "overview", label: "Overview", icon: "◈" },
  { id: "team", label: "Team", icon: "◉" },
  { id: "work", label: "Work", icon: "▱" },
  { id: "activity", label: "Activity", icon: "⌁" },
  { id: "settings", label: "Settings", icon: "⚙" },
];
const presetLabels = {
  advise: "Advise",
  execute_assigned: "Execute assigned work",
  take_initiative: "Take initiative",
  full_autonomy: "Full autonomy",
  unknown: "Not specified",
};
const presetCopy = {
  advise: "Propose a next step. People decide what runs.",
  execute_assigned: "Carry out assigned work within the resources you grant.",
  take_initiative: "Find useful next steps and start work within your mandate.",
  full_autonomy:
    "Choose priorities, delegate and act within granted resources and spending limits. No routine human approval.",
};
function e(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === null || value === undefined || value === false) continue;
    if (key === "class") node.className = value;
    else if (key.startsWith("on"))
      node.addEventListener(key.slice(2).toLowerCase(), value);
    else if (key === "text") node.textContent = value;
    else if (key in node && !key.startsWith("aria-")) node[key] = value;
    else node.setAttribute(key, value === true ? "" : value);
  }
  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    node.append(
      child instanceof Node ? child : document.createTextNode(String(child)),
    );
  }
  return node;
}
const button = (label, action, className = "button") =>
  e("button", { type: "button", class: className, onclick: action }, label);
const label = (text) => e("span", { class: "eyebrow" }, text);
const announce = (text) => {
  document.querySelector("#announcements").textContent = text;
};
const initials = (text) =>
  String(text || "B")
    .trim()
    .slice(0, 1)
    .toUpperCase();
const statusText = (text) => String(text || "unknown").replaceAll("_", " ");
const validText = (text, fallback = "Not specified") =>
  typeof text === "string" && text.trim() ? text : fallback;
function route() {
  const query = new URLSearchParams(location.search);
  return {
    id: query.get("business") || "",
    view: views.some((v) => v.id === query.get("view"))
      ? query.get("view")
      : "overview",
    setup: query.get("setup") === "1",
  };
}
function url(id, view = "overview") {
  const query = new URLSearchParams();
  if (id) query.set("business", id);
  if (view !== "overview") query.set("view", view);
  return "/business" + (query.size ? "?" + query : "");
}
function navigate(href) {
  if (route().setup) captureSetupDraft();
  history.pushState({}, "", href);
  window.scrollTo(0, 0);
  main.focus({ preventScroll: true });
  loadRoute();
}
function safeHref(value) {
  return typeof value === "string" &&
    value.startsWith("/") &&
    !value.startsWith("//") &&
    !value.includes("\\") &&
    !/[\u0000-\u001f]/.test(value)
    ? value
    : null;
}
function money(value, currency = "USD") {
  if (value === null || value === undefined || currency !== "USD")
    return "Not available";
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value / 1e6);
}
function date(value) {
  if (!value) return "Time not recorded";
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf())
    ? "Time not recorded"
    : new Intl.DateTimeFormat(undefined, {
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
      }).format(parsed);
}
function empty(title, copy, icon = "↗", action) {
  return e(
    "div",
    { class: "empty-state" },
    e("span", { class: "empty-icon", "aria-hidden": "true" }, icon),
    e("h3", {}, title),
    e("p", {}, copy),
    action,
  );
}
function panel(title, subtitle, content, action) {
  return e(
    "section",
    { class: "panel" },
    e(
      "div",
      { class: "panel-heading" },
      e("div", {}, e("h2", {}, title), subtitle ? e("p", {}, subtitle) : null),
      action,
    ),
    content,
  );
}
function unavailable(kind) {
  return empty(
    `${kind} ${kind === "Decisions" ? "are" : "is"} not connected yet`,
    `${kind} will appear here when it is connected to this business. Your setup is saved.`,
    "◇",
  );
}
function stateBadge(value) {
  return e(
    "span",
    {
      class: `badge ${["active", "complete", "running"].includes(value) ? "positive" : ["paused", "blocked", "needs_attention"].includes(value) ? "warning" : ""}`,
    },
    e("i"),
    statusText(value),
  );
}
function header() {
  nav.replaceChildren(
    ...views.map((view) =>
      e(
        "a",
        {
          href: url(route().id, view.id),
          class: !route().setup && route().view === view.id ? "selected" : "",
          onclick: (event) => {
            event.preventDefault();
            navigate(url(route().id, view.id));
          },
          "aria-current":
            !route().setup && route().view === view.id ? "page" : null,
        },
        e("span", { "aria-hidden": "true" }, view.icon),
        e("span", {}, view.label),
      ),
    ),
  );
  if (!state.context) {
    switcher.replaceChildren(
      e("span", { class: "context-label" }, "Your businesses"),
    );
    topActions.replaceChildren();
    document.querySelector("#viewer").replaceChildren();
    return;
  }
  const options = [
    e("option", { value: "" }, "All businesses"),
    ...state.context.businesses.map((b) =>
      e("option", { value: b.id, selected: b.id === route().id }, b.name),
    ),
  ];
  if (
    route().id &&
    !state.context.businesses.some((b) => b.id === route().id) &&
    state.detail
  )
    options.push(
      e(
        "option",
        { value: route().id, selected: true },
        state.detail.business.name,
      ),
    );
  switcher.replaceChildren(
    e("span", { class: "switcher-symbol", "aria-hidden": "true" }, "◈"),
    e(
      "select",
      {
        "aria-label": "Current business",
        onchange: (event) => navigate(url(event.target.value)),
      },
      options,
    ),
  );
  topActions.replaceChildren(
    button(
      "New business",
      () => navigate("/business?setup=1"),
      "button compact new-business",
    ),
  );
  topActions.firstChild.disabled = !state.context.capabilities.createBusiness;
  topActions.firstChild.title = state.context.capabilities.createBusiness
    ? ""
    : "Business creation is not available for this account.";
  document
    .querySelector("#viewer")
    .replaceChildren(
      e(
        "span",
        { class: "viewer-avatar" },
        initials(state.context.viewer.name),
      ),
      e("span", {}, state.context.viewer.name),
    );
}
function loading() {
  main.replaceChildren(
    e(
      "div",
      { class: "page-loading", role: "status" },
      e("span", { class: "loading-mark" }),
      e("h1", {}, "Opening your business"),
      e("p", {}, "Loading the latest operating state."),
    ),
  );
}
function showError(error, retry) {
  announce("");
  document.title = "Business · STRIDE";
  const signedOut = error.status === 401;
  main.replaceChildren(
    e(
      "div",
      { class: "page-error", role: "alert" },
      label("BUSINESS WORKSPACE"),
      e(
        "h1",
        {},
        signedOut
          ? "Your session has ended."
          : error.status === 403
            ? "Access has changed."
            : "We couldn’t open this business.",
      ),
      e("p", {}, error.message),
      signedOut
        ? e(
            "a",
            { class: "button primary", href: "/index.html" },
            "Sign in to STRIDE",
          )
        : button("Try again", retry, "button primary"),
    ),
  );
}
async function bootstrap() {
  state.detail = null;
  state.context = null;
  state.setupDraft = null;
  header();
  loading();
  const ticket = ++state.request;
  try {
    const context = await businessAPI.context();
    if (ticket !== state.request) return;
    state.context = context;
    header();
    await loadRoute();
  } catch (error) {
    if (ticket !== state.request) return;
    showError(error, bootstrap);
  }
}
async function loadRoute() {
  const current = route();
  header();
  state.controller?.abort();
  state.detail = null;
  const ticket = ++state.request;
  if (!state.context) {
    return bootstrap();
  }
  if (current.setup) {
    renderSetup();
    return;
  }
  if (!current.id) {
    renderPortfolio();
    return;
  }
  state.controller = new AbortController();
  loading();
  try {
    const detail = await businessAPI.detail(
      current.id,
      state.controller.signal,
    );
    if (ticket !== state.request) return;
    state.detail = detail;
    header();
    renderDetail();
  } catch (error) {
    if (ticket !== state.request) return;
    state.detail = null;
    if ([401, 403, 404].includes(error.status)) {
      state.setupDraft = null;
      state.context = null;
      state.mutation = null;
      header();
    }
    showError(error, loadRoute);
  }
}
function renderPortfolio() {
  document.title = "Your businesses · STRIDE";
  const businesses = state.context.businesses;
  main.replaceChildren(
    e(
      "section",
      { class: "page-title" },
      label("YOUR OPERATING SPACE"),
      e("h1", {}, "Build something that keeps moving."),
      e(
        "p",
        {},
        "A mission, an accountable team, and a place to see what happens next.",
      ),
    ),
    businesses.length
      ? e(
          "div",
          { class: "business-grid" },
          businesses.map((b) =>
            e(
              "a",
              {
                class: "business-card",
                href: url(b.id),
                onclick: (event) => {
                  event.preventDefault();
                  navigate(url(b.id));
                },
              },
              e(
                "div",
                { class: "card-top" },
                e("span", { class: "business-monogram" }, initials(b.name)),
                stateBadge(b.status),
              ),
              e("h2", {}, b.name),
              e(
                "p",
                {},
                validText(
                  b.mission,
                  "Add a mission to give this business direction.",
                ),
              ),
              e(
                "div",
                { class: "card-bottom" },
                e("span", {}, presetLabels[b.authorityPreset]),
                e("span", { "aria-hidden": "true" }, "↗"),
              ),
            ),
          ),
        )
      : e(
          "section",
          { class: "welcome-panel" },
          e(
            "div",
            { class: "welcome-orbit", "aria-hidden": "true" },
            e("i"),
            e("i"),
            e("span", {}, "S"),
          ),
          empty(
            "Your next business starts here.",
            "Set a mission, choose who runs it, and define what the team is allowed to do.",
            "◈",
            state.context.capabilities.createBusiness
              ? button(
                  "Set up a business",
                  () => navigate("/business?setup=1"),
                  "button primary",
                )
              : e(
                  "p",
                  { class: "service-note" },
                  "Business creation is not available on this instance yet.",
                ),
          ),
        ),
  );
}
function collection(kind, title, emptyTitle, emptyCopy, renderer) {
  const d = state.detail;
  if (d.availability[kind] !== "available")
    return panel(title, null, unavailable(title));
  if (!d[kind].length) return panel(title, null, empty(emptyTitle, emptyCopy));
  const records = d[kind],
    rows = e("div", { class: `rows ${kind}-rows` });
  let shown = 0;
  const more = button("Show more", () => append(25), "button ghost show-more");
  const counter = e("p", { class: "row-note", "aria-live": "polite" });
  function append(count) {
    const next = Math.min(records.length, shown + count);
    for (const record of records.slice(shown, next))
      rows.append(renderer(record));
    shown = next;
    more.hidden = shown === records.length;
    counter.textContent =
      records.length > 6
        ? `Showing ${shown} of ${records.length} loaded records`
        : "";
  }
  append(6);
  return panel(title, null, e("div", {}, rows, counter, more));
}
function teamRow(person) {
  return e(
    "article",
    { class: "team-row" },
    e("span", { class: "seat-avatar" }, initials(person.name || person.role)),
    e(
      "div",
      { class: "row-content" },
      e("h3", {}, validText(person.name, "Unnamed team member")),
      e("p", {}, validText(person.role, "Role not specified")),
      person.mandate ? e("span", { class: "row-note" }, person.mandate) : null,
    ),
    stateBadge(person.status),
  );
}
function workRow(work) {
  const href = safeHref(work.href);
  return e(
    "article",
    { class: "work-row" },
    e("span", { class: "work-symbol", "aria-hidden": "true" }, "▱"),
    e(
      "div",
      { class: "row-content" },
      href
        ? e(
            "a",
            { class: "row-link", href },
            validText(work.title, validText(work.name, "Untitled work")),
          )
        : e(
            "h3",
            {},
            validText(work.title, validText(work.name, "Untitled work")),
          ),
      work.summary ? e("p", {}, work.summary) : null,
      work.ownerName ? e("span", { class: "row-note" }, work.ownerName) : null,
    ),
    stateBadge(work.status),
  );
}
function activityRow(event) {
  return e(
    "article",
    { class: "activity-row" },
    e("span", { class: "timeline-dot", "aria-hidden": "true" }),
    e(
      "div",
      { class: "row-content" },
      e(
        "h3",
        {},
        validText(event.title, validText(event.type, "Business activity")),
      ),
      event.summary || event.reason
        ? e("p", {}, event.summary || event.reason)
        : null,
      e(
        "span",
        { class: "row-note" },
        `${validText(event.actorName, "Business record")} · ${date(event.occurredAt || event.createdAt)}`,
      ),
    ),
  );
}
function decisionRow(decision) {
  return e(
    "article",
    { class: "decision-row" },
    e(
      "div",
      { class: "row-content" },
      e("h3", {}, validText(decision.title, "Decision")),
      decision.reason || decision.summary
        ? e("p", {}, decision.reason || decision.summary)
        : null,
      decision.actorName
        ? e("span", { class: "row-note" }, decision.actorName)
        : null,
    ),
    stateBadge(decision.status),
  );
}
function budgetPanel() {
  const b = state.detail.budget;
  const complete =
    b.currency === "USD" &&
    b.allowanceMicros !== null &&
    b.reservedMicros !== null &&
    b.spentMicros !== null;
  const used = complete
    ? Math.min(
        100,
        Math.max(
          0,
          ((b.spentMicros + b.reservedMicros) /
            Math.max(1, b.allowanceMicros)) *
            100,
        ),
      )
    : 0;
  return panel(
    "Model spending limit",
    "A spending limit; no payment has been made.",
    e(
      "div",
      { class: "budget-content" },
      e(
        "div",
        { class: "budget-total" },
        e("strong", {}, money(b.allowanceMicros, b.currency)),
        e("span", {}, "Authorized allowance"),
      ),
      complete
        ? e(
            "div",
            {
              class: "budget-track",
              role: "img",
              "aria-label": `${Math.round(used)}% spent or reserved`,
            },
            e("span", { style: `width:${used}%` }),
          )
        : e(
            "p",
            { class: "data-note" },
            "Budget coverage is not available yet.",
          ),
      e(
        "dl",
        { class: "budget-breakdown" },
        e(
          "div",
          {},
          e("dt", {}, "Recorded spend"),
          e("dd", {}, money(b.spentMicros, b.currency)),
        ),
        e(
          "div",
          {},
          e("dt", {}, "Reserved"),
          e("dd", {}, money(b.reservedMicros, b.currency)),
        ),
        e(
          "div",
          {},
          e("dt", {}, "Available"),
          e(
            "dd",
            {},
            complete
              ? money(
                  Math.max(
                    0,
                    b.allowanceMicros - b.spentMicros - b.reservedMicros,
                  ),
                )
              : "Not available",
          ),
        ),
      ),
      b.unpricedCalls
        ? e(
            "p",
            { class: "data-note warning-text" },
            `${b.unpricedCalls} unpriced ${b.unpricedCalls === 1 ? "call is" : "calls are"} excluded from recorded spend.`,
          )
        : null,
      b.unpricedCalls === null
        ? e("p", { class: "data-note" }, "Unpriced usage coverage is unknown.")
        : null,
    ),
  );
}
function operatingStrip() {
  const { business: b, execution: x } = state.detail;
  const active = x.status === "running" || x.status === "ready";
  return e(
    "div",
    { class: `operating-strip ${active ? "operating-ready" : ""}` },
    e("span", { class: "operating-indicator" }),
    e(
      "div",
      {},
      e(
        "strong",
        {},
        x.status === "running"
          ? "The business is operating"
          : x.status === "ready"
            ? "Ready for available work"
            : b.status === "paused"
              ? "The business is paused"
              : "Business saved · execution not active",
      ),
      e("p", {}, x.reason || "Your mission and operating policy are saved."),
    ),
    e(
      "span",
      { class: "operating-policy" },
      `Authority: ${presetLabels[b.authorityPreset]}`,
    ),
  );
}
function titleBlock() {
  const b = state.detail.business;
  const org = state.context.organizations.find(
    (o) => o.id === b.organizationId,
  );
  return e(
    "section",
    { class: "page-title business-title" },
    e(
      "div",
      {},
      label(org?.name || "BUSINESS"),
      e("h1", {}, b.name),
      e(
        "p",
        { class: "mission" },
        validText(b.mission, "A mission hasn’t been recorded yet."),
      ),
    ),
    e(
      "div",
      { class: "title-actions" },
      stateBadge(b.status),
      button("Refresh", loadRoute, "button ghost"),
    ),
  );
}
function renderDetail() {
  if (!state.detail) return;
  const d = state.detail,
    b = d.business,
    view = route().view;
  document.title = `${b.name} · STRIDE`;
  const nodes = [titleBlock()];
  if (view === "settings") {
    nodes.push(settingsPanel());
  } else if (view === "team") {
    nodes.push(
      e(
        "p",
        { class: "view-intro" },
        "Roles and responsibilities belong to this business. A seat stays with the business as its tools and models change.",
      ),
      teamPanel(),
    );
  } else if (view === "work") {
    nodes.push(
      e(
        "p",
        { class: "view-intro" },
        "Commitments, progress and results. Work appears here when a connected agent team creates it.",
      ),
      workPanel(),
    );
  } else if (view === "activity") {
    nodes.push(
      e("div", { class: "two-columns" }, decisionPanel(), initiativePanel()),
      activityPanel(),
    );
  } else {
    nodes.push(
      operatingStrip(),
      e(
        "section",
        { class: "mission-context" },
        e(
          "div",
          {},
          label("THE CUSTOMER"),
          e("p", {}, validText(b.customer, "No customer definition yet.")),
        ),
        e(
          "div",
          {},
          label("FIRST USEFUL OUTCOME"),
          e(
            "p",
            {},
            validText(b.firstOutcome, "No first outcome recorded yet."),
          ),
        ),
        e(
          "div",
          {},
          label("WHO RUNS IT"),
          e(
            "p",
            {},
            b.leadership === "agent_ceo"
              ? "Agent-led company"
              : b.leadership === "human_ceo"
                ? "Human-led company"
                : "Not specified",
          ),
          e(
            "small",
            {},
            b.leadership === "agent_ceo" && !d.team.length
              ? "An operator has not been hired yet."
              : presetLabels[b.authorityPreset],
          ),
        ),
      ),
      e(
        "div",
        { class: "operating-grid" },
        e(
          "div",
          { class: "main-column" },
          workPanel(),
          initiativePanel(),
          activityPanel(),
        ),
        e(
          "div",
          { class: "side-column" },
          budgetPanel(),
          teamPanel(),
          decisionPanel(),
        ),
      ),
    );
  }
  main.replaceChildren(...nodes);
  announce(`${b.name} ${view} loaded.`);
}
function teamPanel() {
  return collection(
    "team",
    "Your team",
    "Your team is still taking shape.",
    "No agents have been hired. Choosing an agent CEO saves your intended leadership; it doesn’t hire an operator.",
    teamRow,
  );
}
function workPanel() {
  return collection(
    "work",
    "Current work",
    "No work has started yet.",
    "Once execution is connected, the business’s active commitments and results will appear here.",
    workRow,
  );
}
function initiativePanel() {
  return collection(
    "initiatives",
    "Initiative",
    "Nothing has been picked up yet.",
    "Useful next steps will appear when your team can follow the conversations and sources you choose.",
    decisionRow,
  );
}
function decisionPanel() {
  return collection(
    "decisions",
    "Decisions",
    "No decisions are waiting here.",
    "Agent-led work can move within its mandate. Only decisions outside that mandate or assigned to you need your attention.",
    decisionRow,
  );
}
function activityPanel() {
  return collection(
    "activity",
    "Operating record",
    "The operating record starts here.",
    "Saved changes and decisions will appear with their actor and time.",
    activityRow,
  );
}
function field(title, name, value, options = {}) {
  const input = e(options.multiline ? "textarea" : "input", {
    name,
    id: `field-${name}`,
    value: value || "",
    required: options.required !== false,
    maxLength: options.maxLength || 1000,
    ...(options.multiline ? { rows: 3 } : { type: options.type || "text" }),
    placeholder: options.placeholder || "",
  });
  return e(
    "label",
    { class: "form-field", htmlFor: `field-${name}` },
    e("span", {}, title),
    input,
    options.hint ? e("small", {}, options.hint) : null,
  );
}
function choiceGroup(name, title, choices, selected) {
  return e(
    "fieldset",
    { class: "choice-fieldset" },
    e("legend", {}, title),
    e(
      "div",
      { class: `choice-grid ${choices.length > 2 ? "four-choices" : ""}` },
      choices.map((choice) =>
        e(
          "label",
          { class: "choice" },
          e("input", {
            type: "radio",
            name,
            value: choice.id,
            checked: selected === choice.id,
            required: true,
          }),
          e(
            "span",
            { class: "choice-content" },
            e("strong", {}, choice.title),
            e("small", {}, choice.description),
          ),
        ),
      ),
    ),
  );
}
function leadershipChoices() {
  return [
    {
      id: "human_ceo",
      title: "I run the company",
      description:
        "Keep the CEO role. Delegate work and decisions to your team.",
    },
    {
      id: "agent_ceo",
      title: "The team runs it",
      description: "An agent operator can lead within the mandate you set.",
    },
  ];
}
function policyChoices() {
  return authorityPresets.map((id) => ({
    id,
    title: presetLabels[id],
    description: presetCopy[id],
  }));
}
function formValues(form) {
  return Object.fromEntries(new FormData(form).entries());
}
function captureSetupDraft() {
  const form = document.querySelector("#setup-form");
  if (form && form.getAttribute("aria-busy") !== "true")
    state.setupDraft = formValues(form);
}
function operation(payload, scope = "setup") {
  const signature = JSON.stringify([scope, payload]);
  if (state.mutation?.signature !== signature)
    state.mutation = { signature, key: crypto.randomUUID() };
  return { ...payload, idempotencyKey: state.mutation.key };
}
function formError(form, message) {
  const box = form.querySelector(".form-error");
  box.hidden = false;
  box.textContent = message;
  box.focus();
}
function setBusy(form, busy) {
  for (const control of form.elements) control.disabled = busy;
  form.setAttribute("aria-busy", String(busy));
}
function renderSetup() {
  document.title = "Create a business · STRIDE";
  if (!state.context.capabilities.createBusiness) {
    main.replaceChildren(
      empty(
        "Business setup isn’t available yet.",
        "Your account cannot create a business on this instance.",
        "◇",
        button("Back to businesses", () => navigate("/business")),
      ),
    );
    return;
  }
  const draft = state.setupDraft || {
    leadership: "agent_ceo",
    authorityPreset: "execute_assigned",
    organizationId:
      state.context.organizations.find((o) => o.canCreateBusiness)?.id ||
      (state.context.capabilities.createOrganization ? "new" : ""),
  };
  const orgs = state.context.organizations.filter((o) => o.canCreateBusiness);
  const form = e(
    "form",
    { id: "setup-form", class: "setup-form", onsubmit: submitSetup },
    e("div", {
      class: "form-error",
      role: "alert",
      tabIndex: -1,
      hidden: true,
    }),
    e(
      "section",
      { class: "form-section" },
      e(
        "div",
        { class: "form-section-label" },
        label("01 / THE BUSINESS"),
        e("h2", {}, "Give it a direction."),
      ),
      e(
        "div",
        { class: "form-section-fields" },
        field("Business name", "name", draft.name, {
          maxLength: 120,
          placeholder: "What will you call it?",
        }),
        field("What are you building or operating?", "mission", draft.mission, {
          multiline: true,
          maxLength: 2000,
          placeholder: "Describe the job this business exists to do.",
        }),
        e(
          "div",
          { class: "form-pair" },
          field("Who is it for?", "customer", draft.customer, {
            multiline: true,
            placeholder: "The people or businesses you want to serve.",
          }),
          field(
            "What would a useful first result be?",
            "firstOutcome",
            draft.firstOutcome,
            {
              multiline: true,
              placeholder: "One concrete outcome the team can work toward.",
            },
          ),
        ),
      ),
    ),
    e(
      "section",
      { class: "form-section" },
      e(
        "div",
        { class: "form-section-label" },
        label("02 / LEADERSHIP"),
        e("h2", {}, "Choose your part."),
      ),
      e(
        "div",
        { class: "form-section-fields" },
        choiceGroup(
          "leadership",
          "Who runs the company?",
          leadershipChoices(),
          draft.leadership,
        ),
        e(
          "p",
          { class: "form-note" },
          "Choosing agent leadership saves your intent. You’ll need a real operator and connected resources before the business can run.",
        ),
        choiceGroup(
          "authorityPreset",
          "What can the team decide?",
          policyChoices(),
          draft.authorityPreset,
        ),
      ),
    ),
    e(
      "section",
      { class: "form-section" },
      e(
        "div",
        { class: "form-section-label" },
        label("03 / HOME & RESOURCES"),
        e("h2", {}, "Make room to work."),
      ),
      e(
        "div",
        { class: "form-section-fields" },
        e(
          "label",
          { class: "form-field" },
          e("span", {}, "Organization"),
          e(
            "select",
            {
              name: "organizationId",
              required: true,
              onchange: (event) => {
                const field = form.querySelector("#organization-name-field");
                field.hidden = event.target.value !== "new";
                field.querySelector("input").required =
                  event.target.value === "new";
              },
            },
            orgs.length
              ? null
              : e("option", { value: "" }, "Choose an organization"),
            orgs.map((org) =>
              e(
                "option",
                { value: org.id, selected: draft.organizationId === org.id },
                org.name,
              ),
            ),
            state.context.capabilities.createOrganization
              ? e(
                  "option",
                  { value: "new", selected: draft.organizationId === "new" },
                  "Create a new organization",
                )
              : null,
          ),
        ),
        e(
          "div",
          {
            id: "organization-name-field",
            hidden: draft.organizationId !== "new",
          },
          field(
            "Organization name",
            "organizationName",
            draft.organizationName,
            { required: draft.organizationId === "new", maxLength: 120 },
          ),
        ),
        field(
          "Model spending limit in USD",
          "modelAllowance",
          draft.modelAllowance,
          {
            type: "number",
            required: false,
            hint: "Optional. Authorize a model spending limit. No payment is made and no provider credits are purchased.",
            placeholder: "Leave blank to set a limit later",
          },
        ),
        e(
          "p",
          { class: "form-note" },
          "Resources and agent operation are configured separately. Creating this business does not start a provider call.",
        ),
      ),
    ),
    e(
      "footer",
      { class: "form-actions" },
      e("p", {}, "Your mission and operating policy will be saved together."),
      button("Cancel", () => navigate("/business"), "button ghost"),
      e(
        "button",
        { type: "submit", class: "button primary" },
        "Create business",
        e("span", { "aria-hidden": "true" }, "↗"),
      ),
    ),
  );
  const allowance = form.querySelector('[name="modelAllowance"]');
  allowance.min = "0";
  allowance.step = "0.01";
  allowance.max = "1000000";
  main.replaceChildren(
    e(
      "section",
      { class: "page-title setup-title" },
      label("A COMPANY STARTS WITH INTENT"),
      e("h1", {}, "What are we here to do?"),
      e(
        "p",
        {},
        "Set the mission, choose how it’s led, and give the team a clear operating boundary.",
      ),
    ),
    form,
  );
}
async function submitSetup(event) {
  event.preventDefault();
  const form = event.currentTarget;
  if (form.getAttribute("aria-busy") === "true") return;
  const values = formValues(form);
  state.setupDraft = values;
  const allowance = values.modelAllowance?.trim();
  let micros = null;
  if (allowance) {
    if (!/^\d+(\.\d{1,2})?$/.test(allowance)) {
      formError(
        form,
        "Enter a non-negative dollar amount with at most two decimal places.",
      );
      return;
    }
    micros = Math.round(Number(allowance) * 1e6);
    if (!Number.isSafeInteger(micros) || micros > 1e12) {
      formError(form, "Enter a model spending limit of $1,000,000 or less.");
      return;
    }
  }
  const payload = {
    organization:
      values.organizationId === "new"
        ? { name: values.organizationName.trim() }
        : { id: values.organizationId },
    name: values.name.trim(),
    mission: values.mission.trim(),
    customer: values.customer.trim(),
    firstOutcome: values.firstOutcome.trim(),
    leadership: values.leadership,
    authorityPreset: values.authorityPreset,
    modelAllowanceMicros: micros,
  };
  if (
    !payload.name ||
    !payload.mission ||
    !payload.customer ||
    !payload.firstOutcome ||
    (payload.organization.name !== undefined && !payload.organization.name)
  ) {
    formError(
      form,
      "Fill in the business, customer, outcome and organization details.",
    );
    return;
  }
  setBusy(form, true);
  form.querySelector(".form-error").hidden = true;
  const request = operation(payload),
    ticket = state.request,
    viewerId = state.context.viewer.id;
  try {
    const detail = await businessAPI.create(request);
    if (state.context?.viewer.id !== viewerId) return;
    if (ticket === state.request || !route().setup) state.setupDraft = null;
    if (state.mutation?.key === request.idempotencyKey) state.mutation = null;
    state.context.businesses = state.context.businesses
      .filter((b) => b.id !== detail.business.id)
      .concat(detail.business);
    if (ticket !== state.request) {
      header();
      if (!route().id && !route().setup) renderPortfolio();
      announce("Business created. Open it from your business list.");
      return;
    }
    state.detail = detail;
    history.pushState({}, "", url(detail.business.id));
    header();
    renderDetail();
    window.scrollTo(0, 0);
    main.focus({ preventScroll: true });
    announce("Business created. Your operating setup is saved.");
    refreshContextAfterCreate(viewerId);
  } catch (error) {
    setBusy(form, false);
    if ([401, 403].includes(error.status)) {
      state.setupDraft = null;
      state.mutation = null;
      state.detail = null;
      state.context = null;
      header();
      showError(error, bootstrap);
      return;
    }
    formError(form, error.message);
  }
}
// Creation has already succeeded. A directory refresh must never turn that
// receipt into a failed create or discard a newer selection/form.
async function refreshContextAfterCreate(viewerId) {
  try {
    const context = await businessAPI.context();
    if (state.context?.viewer.id !== viewerId) return;
    if (context.viewer.id !== viewerId) {
      state.detail = null;
      state.context = null;
      state.setupDraft = null;
      state.mutation = null;
      header();
      showError(
        {
          status: 401,
          message: "Your account changed. Sign in again to continue.",
        },
        bootstrap,
      );
      return;
    }
    state.context = context;
    header();
    if (state.detail && route().id === state.detail.business.id) {
      main.querySelector(".business-title")?.replaceWith(titleBlock());
    }
  } catch (error) {
    if (
      state.context?.viewer.id !== viewerId ||
      ![401, 403, 404].includes(error.status)
    )
      return;
    state.detail = null;
    state.context = null;
    state.setupDraft = null;
    state.mutation = null;
    header();
    showError(error, bootstrap);
  }
}
function settingsPanel() {
  const d = state.detail,
    b = d.business;
  const form = e(
    "form",
    { class: "settings-form", onsubmit: submitPolicy },
    e("div", {
      class: "form-error",
      role: "alert",
      hidden: true,
      tabIndex: -1,
    }),
    choiceGroup(
      "leadership",
      "Who runs the company?",
      leadershipChoices(),
      b.leadership,
    ),
    choiceGroup(
      "authorityPreset",
      "Team authority",
      policyChoices(),
      b.authorityPreset,
    ),
    e(
      "p",
      { class: "form-note" },
      "Full autonomy lets the team choose priorities and act within its granted resources and spending limits. It does not connect accounts or add funds.",
    ),
    e("div", { class: "policy-preview", hidden: true, "aria-live": "polite" }),
    e(
      "div",
      { class: "form-actions" },
      e("p", {}, `Current policy · revision ${b.revision}`),
      e(
        "button",
        {
          type: "submit",
          class: "button primary",
          disabled: !d.capabilities.updatePolicy,
        },
        "Save operating policy",
      ),
    ),
  );
  form.addEventListener("change", () => {
    const value = formValues(form),
      box = form.querySelector(".policy-preview");
    const changed =
      value.authorityPreset !== b.authorityPreset ||
      value.leadership !== b.leadership;
    box.hidden = !changed;
    box.replaceChildren(
      e("strong", {}, "This changes how the business is led."),
      e(
        "p",
        {},
        `${presetLabels[b.authorityPreset]} → ${presetLabels[value.authorityPreset]}. ${value.leadership === "agent_ceo" ? "The operator takes the CEO role when an operator is hired." : "You keep the CEO role."}`,
      ),
    );
  });
  if (!d.capabilities.updatePolicy) {
    for (const input of form.querySelectorAll("input")) input.disabled = true;
    form.prepend(
      e(
        "p",
        { class: "service-note" },
        "You can view this operating policy. Changes aren’t available to your account.",
      ),
    );
  }
  const pauseAction = b.status === "paused" ? "resume" : "pause",
    allowed =
      pauseAction === "pause" ? d.capabilities.pause : d.capabilities.resume;
  return e(
    "div",
    { class: "settings-layout" },
    panel(
      "Operating policy",
      "Your team can work up to this authority level. Role-specific mandates can narrow it.",
      form,
    ),
    panel(
      "Operating state",
      null,
      e(
        "div",
        { class: "operating-settings" },
        stateBadge(b.status),
        e(
          "p",
          {},
          b.status === "draft"
            ? "Your business setup is saved. Agent operation is not connected yet, so there is no running business to pause."
            : b.status === "closed"
              ? "This business is closed."
              : b.status === "paused"
                ? "Resuming permits future work only when the required operation, team and resources are available."
                : "Pausing stops new autonomous work. An external action already accepted may still complete.",
        ),
        ["active", "paused"].includes(b.status)
          ? e(
              "button",
              {
                type: "button",
                class: "button",
                disabled: !allowed,
                onclick: () => changeOperatingState(pauseAction),
              },
              pauseAction === "pause" ? "Pause business" : "Resume business",
            )
          : null,
        e("div", { class: "operation-error", role: "alert" }),
      ),
    ),
  );
}
async function submitPolicy(event) {
  event.preventDefault();
  const form = event.currentTarget;
  if (form.getAttribute("aria-busy") === "true") return;
  const b = state.detail.business,
    values = formValues(form);
  const request = operation(
    {
      action: "update_policy",
      expectedRevision: b.revision,
      leadership: values.leadership,
      authorityPreset: values.authorityPreset,
    },
    b.id,
  );
  const ticket = state.request;
  setBusy(form, true);
  try {
    const detail = await businessAPI.update(b.id, request);
    if (route().id !== b.id || ticket !== state.request) return;
    state.detail = detail;
    state.context.businesses = state.context.businesses.map((record) =>
      record.id === detail.business.id ? detail.business : record,
    );
    state.mutation = null;
    renderDetail();
    announce("Operating policy saved.");
  } catch (error) {
    setBusy(form, false);
    if ([401, 403, 404].includes(error.status)) {
      state.detail = null;
      state.context = null;
      state.mutation = null;
      header();
      showError(error, bootstrap);
      return;
    }
    if (ticket !== state.request) return;
    formError(form, error.message);
    if (error.status === 409) {
      form
        .querySelector(".form-error")
        .append(
          e("p", {}, "Your edits haven’t been applied."),
          button("Load current policy", loadRoute, "button ghost"),
        );
    }
  }
}
async function changeOperatingState(action) {
  const b = state.detail.business,
    ticket = state.request;
  const control = main.querySelector(".operating-settings button");
  control.disabled = true;
  try {
    const detail = await businessAPI.update(
      b.id,
      operation({ action, expectedRevision: b.revision }, b.id),
    );
    if (route().id !== b.id || ticket !== state.request) return;
    state.detail = detail;
    state.context.businesses = state.context.businesses.map((record) =>
      record.id === detail.business.id ? detail.business : record,
    );
    state.mutation = null;
    renderDetail();
    announce(`Business ${action === "pause" ? "paused" : "resumed"}.`);
  } catch (error) {
    if (route().id !== b.id || ticket !== state.request) return;
    if ([401, 403, 404].includes(error.status)) {
      state.detail = null;
      state.context = null;
      header();
      showError(error, bootstrap);
      return;
    }
    control.disabled = false;
    const target = main.querySelector(".operation-error");
    target.textContent = error.message;
    if (error.status === 409)
      target.append(button("Refresh business", loadRoute, "button ghost"));
  }
}
window.addEventListener("popstate", () => {
  captureSetupDraft();
  loadRoute();
});
bootstrap();
