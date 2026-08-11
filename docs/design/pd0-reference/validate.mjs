import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));
const read = name => fs.readFileSync(path.join(root, name), "utf8");
const fixture = JSON.parse(read("fixture.json"));
const css = read("styles.css");
const html = read("index.html");
const js = read("app.js");
const failures = [];
const assert = (condition, message) => { if (!condition) failures.push(message); };
const exactKeys = (value, keys) => Boolean(value && typeof value === "object" && !Array.isArray(value)) && JSON.stringify(Object.keys(value).sort()) === JSON.stringify([...keys].sort());
const sha256 = value => createHash("sha256").update(value).digest("hex");
const safeToken = /^[A-Za-z0-9][A-Za-z0-9_:]{0,63}$/;
const safeRevision = /^\d{2}$/;
const safeTime = /^\d{2}:\d{2}$/;
const unsafeBody = /<[^>]*>|https?:\/\/|www\.|[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}|(?:\+?\d[\d ()-]{7,}\d)/i;
const forbiddenBodyKeys = /^(body|text|html|markdown|email|phone|channel|query|prompt|response|note|metadata|privateData)$/i;
const safeProse = (value, maximum) => typeof value === "string" && value.length > 0 && value.length <= maximum && !unsafeBody.test(value);
const repoRoot = path.resolve(root, "../../..");
const manifestFiles = {
  parityAuditDigest: "docs/design/stride-pd0-current-product-parity-audit-20260809.md",
  semanticContractDigest: "docs/design/stride-pd0-semantic-system-contract-20260809.md",
  flowContractDigest: "docs/design/stride-pd0-reference-flow-contract-20260809.md",
  researchDigest: "docs/evidence/e10/stride-e10-pd0-primary-source-research-20260809.md",
  comparativeStudyDigest: "docs/evidence/e10/stride-e10-pd0-comparative-design-study-20260810.md",
  workstreamStudyDigest: "docs/evidence/e10/stride-e10-pd0-chatgpt-workstream-study-20260810.md",
  workstreamInteractionDigest: "docs/evidence/e10/stride-e10-pd0-chatgpt-workstream-interaction-manifest-20260810.json",
  checkpointDigest: "docs/evidence/e10/stride-e10-pd0-pi0-contracts-20260809.json"
};
const expectedManifest = {
  schema: "stride.pd0.reference-contract-manifest.v1",
  checkpoint: manifestFiles.checkpointDigest,
  checkpointDigest: "d966d745b801b7b537451cbc91e8f3ee49cd9b0a166b48e678a3e769ea83a680",
  parityAuditDigest: "da54564d784179c6e4552a774f0c9585d1a42eb92f0e512b45f7d34916b1091a",
  semanticContractDigest: "df5081f6f2f6d4a06275a2cd4b8fb4280f01c64d23b07e6212341bdbd41353aa",
  flowContractDigest: "475d220dddb96f32f170a8b8a24b68f98ff79a72bd53b805b30c2f98d7abca7c",
  researchDigest: "c9f3dc31960d6d12cd41ffc31efe7e9806eaa5e760b74fefcf5d0543ce913dd2",
  comparativeStudyDigest: "1e781de1ddbc10eeb051680e54e6a08a4c763d547135dece1c638a193e8cf786",
  workstreamStudyDigest: "7123407a41c422f9690b6c899e81dbc69b5f852b37913eb08af2140f43f800d4",
  workstreamInteractionDigest: "98027ae638ee1f0742725754cbdddfc0e7e8642cf990bd43958208bed82048fe"
};
const exactDestinations = ["Home", "Work", "Network", "Work Search", "You"];
const journeyDestinations = { J1: "Home", J2: "Home", J3: "Work", J4: "Work", J5: "You", J6: "Network", J7: "Network", J8: "Network", J9: "Network", J10: "Work Search", J11: "Work", J12: "You", J13: "Network" };
const journeyContractDigest = "f7ecd1c335f92b1a41938f3301e5b781f2e7c13c3422e0ec4397640dd15234b2";
const journeyFixtureDigest = "e79ba28c98a7b70a154a8d85bd30f8b1ac0bbc599a24318b29c02ac2809156fd";
const expectedJ4Postimages = [
  { step: 0, stage: "scope_review", currentActor: "Scout", nextAction: "inspect_sources_and_cost", interventionTitle: "Review the approved scope", agentTitle: "Scout is waiting for story approval", artifactTitle: "Presentation artifact not started", outcomeTitle: "Human presentation review not started", artifactState: "not_started", driveState: "not_saved", artifactReviewState: "pending_review", artifactPreviewState: "unavailable", decisionState: "human_review_required", outcomeNextAction: "wait_for_deck" },
  { step: 1, stage: "narrative_review", currentActor: "Alex", nextAction: "approve_story_direction", interventionTitle: "Approve the narrative boundary", agentTitle: "Scout assembled the story outline", artifactTitle: "Northstar story outline", outcomeTitle: "Human presentation review not started", artifactState: "outline_ready", driveState: "not_saved", artifactReviewState: "pending_review", artifactPreviewState: "unavailable", decisionState: "human_review_required", outcomeNextAction: "wait_for_deck" },
  { step: 2, stage: "design_review", currentActor: "Alex", nextAction: "review_rendered_deck", interventionTitle: "Narrative boundary approved", agentTitle: "Scout assembled the editable deck", artifactTitle: "Northstar six-slide deck", outcomeTitle: "Human presentation review pending", artifactState: "draft_ready", driveState: "not_saved", artifactReviewState: "pending_review", artifactPreviewState: "available", decisionState: "human_review_required", outcomeNextAction: "approve_deck" },
  { step: 3, stage: "human_review", currentActor: "Alex", nextAction: "save_approved_revision", interventionTitle: "Narrative boundary approved", agentTitle: "Scout assembled the editable deck", artifactTitle: "Northstar six-slide deck", outcomeTitle: "Human presentation review complete", artifactState: "reviewed_ready", driveState: "not_saved", artifactReviewState: "reviewed", artifactPreviewState: "available", decisionState: "human_review_complete", outcomeNextAction: "save_approved_revision" }
];

const expectedGovernedTokens = {
  "--space-0":"0px", "--space-2":"2px", "--space-4":"4px", "--space-8":"8px", "--space-12":"12px", "--space-16":"16px", "--space-20":"20px", "--space-24":"24px", "--space-32":"32px", "--space-40":"40px", "--space-48":"48px", "--space-64":"64px", "--space-80":"80px",
  "--radius-0":"0px", "--radius-4":"4px", "--radius-8":"8px", "--radius-12":"12px", "--radius-16":"16px", "--radius-22":"22px", "--radius-28":"28px", "--radius-999":"999px",
  "--elevation-0":"none", "--elevation-1":"0 1px 2px rgba(9,9,11,.10)", "--elevation-2":"0 8px 24px rgba(9,9,11,.14)", "--elevation-3":"0 24px 64px rgba(9,9,11,.18)",
  "--motion-fast-duration":"120ms", "--motion-medium-duration":"220ms", "--motion-slow-duration":"360ms", "--motion-spring-duration":"220ms", "--motion-breathe-duration":"2400ms", "--motion-standard-curve":"cubic-bezier(.32,.72,0,1)", "--motion-spring-curve":"cubic-bezier(.34,1.25,.5,1)", "--motion-breathe-curve":"ease-in-out", "--motion-reduced-duration":"0ms",
  "--material-glass-filter":"blur(20px) saturate(1.15)"
};

const exactStates = ["idle", "loading", "empty", "ready", "feature_off", "unavailable", "blocked_dependency", "pending_approval", "offline", "retrying", "stale", "unauthorized", "revoked", "quarantined", "corrected", "purge_pending", "purge_failed", "restored", "terminal"];
const exactPrecedence = ["revoked", "unauthorized", "feature_off", "blocked_dependency", "unavailable", "offline", "stale", "purge_failed", "quarantined", "pending_approval", "retrying", "purge_pending", "loading", "empty", "corrected", "restored", "terminal", "idle", "ready"];
const closedTypes = ["SignalControl", "HumanMessage", "AgentContribution", "SystemEvent", "SuggestedWorkCard", "RunTimeline", "InterventionRequest", "ArtifactRevisionView", "OutcomeRecordView", "WorkRecordSection", "EvidenceCard", "PublicWorkspaceView", "PublicWorkObjectView", "WorkstreamRow", "PersonProfileView", "OrganizationProfileView", "WorkspaceProfileView", "AgentProfileView", "WorkSearchInterpretation", "WorkSearchResult", "ContactRequestView", "ModerationCaseView", "ConsensusDisplay"];
const expectedSemanticFamilies = {
  SignalControl:"signal-control", HumanMessage:"human-message", AgentContribution:"agent-contribution", SystemEvent:"system-event", SuggestedWorkCard:"suggested-work", RunTimeline:"run-timeline", InterventionRequest:"intervention-request", ArtifactRevisionView:"artifact-revision", OutcomeRecordView:"outcome-record", WorkRecordSection:"work-record-section", EvidenceCard:"evidence-card", PublicWorkspaceView:"public-workspace", PublicWorkObjectView:"public-work-object", WorkstreamRow:"workstream-row", PersonProfileView:"person-profile", OrganizationProfileView:"organization-profile", WorkspaceProfileView:"workspace-profile", AgentProfileView:"agent-profile", WorkSearchInterpretation:"search-interpretation", WorkSearchResult:"search-result", ContactRequestView:"contact-request", ModerationCaseView:"moderation-case", ConsensusDisplay:"consensus-display"
};
const expectedRendererMarkers = {
  SignalControl:"signal-console", HumanMessage:"message-bubble", AgentContribution:"agent-attribution", SystemEvent:"system-ledger", SuggestedWorkCard:"suggestion-contract", RunTimeline:"run-track", InterventionRequest:"intervention-boundary", ArtifactRevisionView:"artifact-sheet", OutcomeRecordView:"outcome-verdict", WorkRecordSection:"record-section", EvidenceCard:"evidence-chain", PublicWorkspaceView:"workspace-outline", PublicWorkObjectView:"work-object-document", WorkstreamRow:"chronological-row", PersonProfileView:"profile-person", OrganizationProfileView:"profile-organization", WorkspaceProfileView:"profile-workspace", AgentProfileView:"profile-agent", WorkSearchInterpretation:"search-proposal", WorkSearchResult:"search-disclosure", ContactRequestView:"contact-envelope", ModerationCaseView:"moderation-docket", ConsensusDisplay:"consensus-board"
};
const commonFields = ["dataState", "projectionRevision", "audience", "trust", "actions"];
const specificFields = {
  SignalControl: ["listeningState", "inputMode", "sourceAuthority", "interruptionState"],
  HumanMessage: ["humanPrincipal", "effectiveTime", "deliveryState"],
  AgentContribution: ["agentPrincipal", "accountableHuman", "packageRuntime", "delegation", "verificationLimit"],
  SystemEvent: ["eventType", "effectiveTime", "relatedObjectRef", "boundedState"],
  SuggestedWorkCard: ["suggestionRevision", "sourceClass", "sourceRef", "scope", "approvalState", "deliverable", "owner", "participantSet", "inputSet", "costState", "nextAction"],
  RunTimeline: ["runRevision", "interventionRevision", "currentActor", "accountableHuman", "providerState", "recoveryState", "workType", "deliverable", "owner", "participantSet", "sourceSet", "stage", "nextAction", "costState", "artifactState", "driveState", "providerRoute"],
  InterventionRequest: ["boundedSchema", "controller", "deadline", "revision", "consequence"],
  ArtifactRevisionView: ["artifactRevision", "sourceProvenance", "outputProvenance", "reviewState", "verificationState", "artifactType", "editability", "previewState", "driveState"],
  OutcomeRecordView: ["outcomeRevision", "evidenceRefs", "accountableHuman", "verificationState", "corrections", "decisionState", "nextAction", "distributionState"],
  WorkRecordSection: ["sectionType", "evidenceRefs", "fieldController", "releaseState"],
  EvidenceCard: ["claimRef", "attestationRef", "approvalRef", "revision", "freshness", "why", "unknown"],
  PublicWorkspaceView: ["workspaceProjection", "ownerRole", "moderatorRole", "participation", "retention", "moderationState", "publicationState", "workBridgeState", "eligibleInput", "releaseReview", "publicResult"],
  PublicWorkObjectView: ["objectType", "workspaceRevision", "objectRevision", "authorship", "provenance", "moderationState", "publicationState"],
  WorkstreamRow: ["chronologicalProjection", "mode", "why", "unknown", "observeState", "saveState"],
  PersonProfileView: ["personProjection", "evidence", "controls"],
  OrganizationProfileView: ["organizationProjection", "roles", "controls", "evidence"],
  WorkspaceProfileView: ["workspaceProjection", "participation", "purpose", "currentObjects", "controls"],
  AgentProfileView: ["agentProjection", "sponsor", "accountableHuman", "packageRuntime", "delegation", "visibilityState", "participationState"],
  WorkSearchInterpretation: ["proposalID", "proposalRevision", "interpretation", "policyBinding", "cohortBinding", "sessionBinding"],
  WorkSearchResult: ["recordedDisclosure", "publicationRef", "attestationRef", "why", "unknown", "contactCapability"],
  ContactRequestView: ["requestRevision", "purpose", "senderProjection", "recipientProjection", "terminalState"],
  ModerationCaseView: ["policyRevision", "caseRevision", "reporterSafeState", "quarantineState", "noticeDeadline", "decisionDeadline", "reviewerRole", "appealRole"],
  ConsensusDisplay: ["eligibilityManifestRevision", "proposalRevision", "population", "rule", "unknowns", "currentAggregate"]
};
const enumFields = {
  dataState: exactStates, audience: ["private", "organization", "named_parties", "public"], trust: ["fictional_body_minimized"], actions: ["none"],
  listeningState: ["idle"], inputMode: ["voice_or_typed"], interruptionState: ["none"], deliveryState: ["current"], approvalState: ["human_required"],
  providerState: ["not_called"], recoveryState: ["none"], reviewState: ["reviewed", "pending_review"], verificationState: ["partial", "human_reviewed", "unverified"],
  sectionType: ["contributions"], releaseState: ["off"], freshness: ["current"], ownerRole: ["owner"], moderatorRole: ["moderator"],
  participation: ["application"], retention: ["90_days"], moderationState: ["parent_off"], publicationState: ["off"],
  objectType: ["EvidenceNote"], authorship: ["human"], mode: ["following"], observeState: ["off"], saveState: ["not_saved"],
  visibilityState: ["parent_off"], participationState: ["none"], contactCapability: ["not_issued"], terminalState: ["draft"],
  reporterSafeState: ["withheld"], quarantineState: ["parent_off"], reviewerRole: ["moderator"], appealRole: ["separate_appeal_reviewer"],
  population: ["eligible_humans_only"], rule: ["one_current_person_decision"], currentAggregate: ["withheld"]
};
const exactFieldValues = {
  accountableHuman: ["Alex"],
  artifactState: ["draft_ready", "not_started", "outline_ready", "reviewed_ready"],
  artifactType: ["presentation", "source_brief"],
  agentPrincipal: ["Scout"],
  agentProjection: ["fictional_agent_02"],
  approvalRef: ["fictional_approval_03"],
  attestationRef: ["fictional_att_02", "fictional_attestation_02"],
  boundedSchema: ["one_closed_choice"],
  boundedState: ["MyMind_off", "active", "recorded"],
  chronologicalProjection: ["09:44_object_03"],
  claimRef: ["fictional_claim_04"],
  cohortBinding: ["fictional_cohort_01"],
  consequence: ["scope_only"],
  controller: ["Alex"],
  controls: ["view_as", "view_as_pause", "view_as_pause_export_delete"],
  corrections: ["none", "revision_03_superseded"],
  costState: ["approval_before_spend"],
  currentActor: ["Alex", "Scout"],
  currentObjects: ["one_fictional_object"],
  deadline: ["fictional_15_minutes"],
  decisionDeadline: ["5_days"],
  delegation: ["one_private_thread", "private_only", "private_run"],
  decisionState: ["human_review_complete", "human_review_required"],
  deliverable: ["editable_presentation"],
  distributionState: ["private_only"],
  driveState: ["not_saved"],
  editability: ["editable"],
  eligibleInput: ["reviewed_work_record"],
  eventType: ["privacy_inventory", "provider_route_declared", "session_admitted", "verification_partial"],
  evidence: ["released_only"],
  evidenceRefs: ["fictional_evidence_07"],
  fieldController: ["Alex"],
  humanPrincipal: ["Alex"],
  interpretation: ["accessible_realtime_collaborator"],
  inputSet: ["three_fictional_facts"],
  noticeDeadline: ["1_business_day"],
  organizationProjection: ["fictional_org_03"],
  nextAction: ["approve_deck", "approve_exact_scope", "approve_story_direction", "inspect_sources_and_cost", "keep_private", "review_rendered_deck", "save_approved_revision", "wait_for_deck"],
  outputProvenance: ["editable_presentation", "fictional_artifact"],
  owner: ["Alex"],
  packageRuntime: ["declared_reference_runtime"],
  personProjection: ["fictional_person_04"],
  participantSet: ["Alex_and_Scout"],
  policyBinding: ["fictional_policy_01"],
  proposalID: ["fictional_proposal_02"],
  providerRoute: ["openai_only_not_called"],
  previewState: ["available", "unavailable"],
  provenance: ["fictional_released_fields"],
  publicResult: ["no_projection"],
  publicationRef: ["fictional_pub_04"],
  purpose: ["review_accessibility_method", "shared_methods"],
  recipientProjection: ["fictional_recipient_projection"],
  recordedDisclosure: ["fictional_disclosure_04"],
  relatedObjectRef: ["fictional_account_ref", "fictional_run_07", "fictional_session_ref"],
  roles: ["not_disclosed"],
  releaseReview: ["human_and_policy_required"],
  scope: ["build_six_slide_deck"],
  senderProjection: ["fictional_sender_projection"],
  sessionBinding: ["fictional_session_ref"],
  sourceAuthority: ["fictional_private_source"],
  sourceClass: ["fictional_signal"],
  sourceProvenance: ["fictional_source_register", "human_reviewed"],
  sourceRef: ["body_free_ref_01"],
  sourceSet: ["three_fictional_facts"],
  sponsor: ["Alex"],
  stage: ["design_review", "human_review", "narrative_review", "scope_review"],
  unknown: ["availability", "device_acceptance"],
  unknowns: ["linked_person_review_pending"],
  verificationLimit: ["limited", "partial"],
  why: ["chosen_workspace", "matching_released_evidence", "named_party_review"],
  workBridgeState: ["private_work_only"],
  workType: ["presentation"],
  workspaceProjection: ["fictional_workspace_01"]
};
const exactJourneyRosters = {
  J1: ["SystemEvent"],
  J2: ["SignalControl", "HumanMessage", "SuggestedWorkCard"],
  J3: ["HumanMessage", "ArtifactRevisionView"],
  J4: ["RunTimeline", "InterventionRequest", "AgentContribution", "ArtifactRevisionView", "SystemEvent", "OutcomeRecordView"],
  J5: ["WorkRecordSection", "EvidenceCard", "OutcomeRecordView"],
  J6: ["PublicWorkspaceView"],
  J7: ["PublicWorkObjectView"],
  J8: ["WorkstreamRow", "PublicWorkspaceView"],
  J9: ["PersonProfileView", "OrganizationProfileView", "WorkspaceProfileView", "AgentProfileView"],
  J10: ["WorkSearchInterpretation", "WorkSearchResult", "ContactRequestView"],
  J11: ["AgentProfileView", "AgentContribution"],
  J12: ["SystemEvent", "PersonProfileView"],
  J13: ["ModerationCaseView", "ConsensusDisplay"]
};
const components = fixture.journeys.flatMap(journey => journey.components);
const observedTypes = new Set(components.map(component => component.type));

function componentFailures(component) {
  const errors = [];
  if (!component || !closedTypes.includes(component.type)) return ["unknown component type"];
  if (!safeProse(component.title, 80)) errors.push("unsafe or unbounded title");
  if (!component.fields || typeof component.fields !== "object" || Array.isArray(component.fields)) return [...errors, "fields must be an object"];
  const exact = [...commonFields, ...specificFields[component.type]].sort();
  const actual = Object.keys(component.fields).sort();
  if (JSON.stringify(actual) !== JSON.stringify(exact)) errors.push("missing or unknown fields");
  for (const [field, value] of Object.entries(component.fields)) {
    if (forbiddenBodyKeys.test(field)) errors.push(`${field} is a forbidden body key`);
    if (typeof value !== "string" || unsafeBody.test(String(value))) {
      errors.push(`${field} contains an unsafe body or non-string value`);
      continue;
    }
    const allowed = enumFields[field];
    if (allowed) {
      if (!allowed.includes(value)) errors.push(`${field} has unknown enum value`);
    } else if (field === "effectiveTime") {
      if (!safeTime.test(value)) errors.push(`${field} must be a closed time`);
    } else if (field === "projectionRevision" || /Revision$/.test(field) || field === "revision") {
      if (!safeRevision.test(value)) errors.push(`${field} must be a bounded revision`);
    } else if (!Object.hasOwn(exactFieldValues, field) || !exactFieldValues[field].includes(value)) {
      errors.push(`${field} is outside its exact fictional allowlist`);
    }
  }
  return errors;
}

function matchesJourneyRoster(journey) {
  return Boolean(journey && exactJourneyRosters[journey.id]) && JSON.stringify(journey.components.map(component => component.type)) === JSON.stringify(exactJourneyRosters[journey.id]);
}

function cssGovernanceFailures(source) {
  const errors = [];
  const governedPosition = value => {
    if (value === "auto" || /^var\(--space-\d+\)$/.test(value)) return true;
    if (!value.startsWith("calc(") || !value.endsWith(")")) return false;
    const expression = value.replace(/var\(--space-\d+\)/g, "");
    return !/var\(|(?:px|rem|em|%)/i.test(expression) && /^calc\([0-9+*/().\s-]*\)$/.test(expression);
  };
  const rootTokens = {};
  for (const block of source.matchAll(/:root\s*\{([\s\S]*?)\}/g)) {
    for (const declaration of block[1].matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
      if (Object.hasOwn(rootTokens, declaration[1]) && /^(--space-|--radius-|--elevation-|--motion-|--material-glass-filter$)/.test(declaration[1])) errors.push(`duplicate governed root token ${declaration[1]}`);
      rootTokens[declaration[1]] = declaration[2].trim();
    }
  }
  const governed = Object.fromEntries(Object.entries(rootTokens).filter(([name]) => /^(--space-|--radius-|--elevation-|--motion-|--material-glass-filter$)/.test(name)));
  if (JSON.stringify(governed) !== JSON.stringify(expectedGovernedTokens)) errors.push("root governed token map differs from exact contract");
  const withoutRoot = source.replace(/:root\s*\{[\s\S]*?\}/g, "");
  const declarationValues = property => [...withoutRoot.matchAll(new RegExp(`${property}\\s*:\\s*([^;}]+)`, "gi"))].map(match => match[1].trim());
  if (/(?:#[0-9a-f]{3,8}|rgba?\(|hsla?\()/i.test(withoutRoot)) errors.push("direct color literal outside governed root");
  if (declarationValues("border-radius").some(value => !value.startsWith("var(") && value !== "inherit")) errors.push("direct radius literal outside governed root");
  if (declarationValues("(?:^|[;{])\\s*gap").some(value => !value.startsWith("var("))) errors.push("direct gap literal outside governed root");
  if (declarationValues("(?:padding|padding-top|padding-right|padding-bottom|padding-left|padding-inline|margin|margin-top|margin-right|margin-bottom|margin-left|margin-inline)").some(value => /\d+(?:px|rem|em)/i.test(value))) errors.push("direct spacing literal outside governed root");
  if (declarationValues("(?:^|[;{])\\s*(?:top|right|bottom|left|inset|inset-block|inset-inline)").some(value => !governedPosition(value))) errors.push("direct positional or inset literal outside governed spacing scale");
  if (declarationValues("(?:background|background-image)").filter(value => /(?:repeating-)?(?:linear|radial)-gradient\(/i.test(value)).some(value => /-?(?:\d*\.)?\d+(?:px|rem|em|%)(?![a-z])/i.test(value))) errors.push("direct gradient stop outside governed spacing scale");
  if (declarationValues("box-shadow").some(value => !/^var\(--elevation-[0-3]\)$/.test(value))) errors.push("unknown or nested elevation outside governed root");
  if (declarationValues("(?:transition-duration|animation-duration|transition-timing-function)").some(value => !value.startsWith("var("))) errors.push("direct timing literal outside governed root");
  if (declarationValues("backdrop-filter").some(value => value !== "var(--material-glass-filter)")) errors.push("ungoverned material filter");
  return errors;
}

function componentRendererFailures(source) {
  const errors = [];
  const mapping = source.match(/const semanticFamilyByType = Object\.freeze\((\{[\s\S]*?\})\);/);
  let parsed = null;
  try { parsed = mapping ? JSON.parse(mapping[1]) : null; } catch { parsed = null; }
  if (JSON.stringify(parsed) !== JSON.stringify(expectedSemanticFamilies)) errors.push("semantic family registry differs from exact 23-type contract");
  if (parsed && new Set(Object.values(parsed)).size !== closedTypes.length) errors.push("semantic families collapse distinct component types");
  for (const [type, marker] of Object.entries(expectedRendererMarkers)) {
    const caseStart = `case "${type}": return`;
    const caseIndex = source.indexOf(caseStart);
    const caseLine = caseIndex < 0 ? "" : source.slice(caseIndex, source.indexOf("\n", caseIndex));
    if (!caseLine.includes(marker) || !source.includes(`data-semantic-family=`)) errors.push(`${type} lacks its structural renderer ${marker}`);
    if (!css.includes(`.${marker}`)) errors.push(`${type} structural renderer lacks governed presentation`);
  }
  return errors;
}

assert(fixture.schema === "stride.pd0.reference-fixture.v1", "unknown fixture schema");
assert(fixture.fictional === true, "fixture must be explicitly fictional");
assert(exactKeys(fixture, ["schema", "generatedFor", "fictional", "contractManifest", "destinations", "journeyContracts", "journeyPostimages", "principal", "featureGates", "statePrecedence", "states", "journeys"]), "fixture root has missing or unknown keys");
assert(fixture.generatedFor === "PD0 reference evidence only", "fixture classification must remain browser-reference-only evidence");
assert(JSON.stringify(fixture.contractManifest) === JSON.stringify(expectedManifest), "fixture contract manifest differs from the frozen binding");
for (const [digestField, relativePath] of Object.entries(manifestFiles)) {
  const target = path.join(repoRoot, relativePath);
  assert(fs.existsSync(target) && sha256(fs.readFileSync(target)) === expectedManifest[digestField], `${relativePath} is missing or differs from the bound digest`);
}
const checkpoint = JSON.parse(fs.readFileSync(path.join(repoRoot, expectedManifest.checkpoint), "utf8"));
const boundArtifactDigests = Object.fromEntries(checkpoint.artifacts.map(artifact => [artifact.path, artifact.sha256]));
assert(boundArtifactDigests[manifestFiles.parityAuditDigest] === expectedManifest.parityAuditDigest && boundArtifactDigests[manifestFiles.semanticContractDigest] === expectedManifest.semanticContractDigest && boundArtifactDigests[manifestFiles.flowContractDigest] === expectedManifest.flowContractDigest && boundArtifactDigests[manifestFiles.researchDigest] === expectedManifest.researchDigest, "checkpoint does not bind the exact PD0 audit, semantic, flow, and research artifacts");
assert(checkpoint.critic?.pd0ContractSet?.verdict === "PASS" && checkpoint.critic.pd0ContractSet.remainingBlockers === 0 && checkpoint.critic.pd0ContractSet.remainingMajors === 0, "checkpoint does not record a clean independent PD0 contract gate");
assert(JSON.stringify(fixture.destinations) === JSON.stringify(exactDestinations), "IA destinations or order differ from the closed contract");
assert(exactKeys(fixture.principal, ["displayName", "organization", "session"]) && JSON.stringify(fixture.principal) === JSON.stringify({ displayName: "Alex", organization: "Northstar", session: "current reference session" }), "fictional principal shape differs from the closed fixture");
assert(exactKeys(fixture.featureGates, ["PN1", "PN2", "PN3", "PN4", "PN5", "W5", "W6", "pn_moderation", "semantic_provider", "reranker"]), "feature-gate root has missing or unknown gates");
assert(exactKeys(fixture.journeyContracts, Object.keys(journeyDestinations)), "journey-contract root must contain exact J1-J13");
assert(exactKeys(fixture.journeyPostimages, ["J4"]) && JSON.stringify(fixture.journeyPostimages.J4) === JSON.stringify(expectedJ4Postimages), "J4 lifecycle postimages must equal the exact four-step contract");
assert(sha256(JSON.stringify(fixture.journeyContracts)) === journeyContractDigest, "journey screen/action/transition/outcome contracts differ from the frozen reference");
assert(sha256(JSON.stringify(fixture.journeys)) === journeyFixtureDigest, "journey fixture differs from the frozen reference");
assert(fixture.journeys.length === 13 && fixture.journeys.every((journey, index) => journey.id === `J${index + 1}`), "fixture must contain ordered J1-J13");
assert(fixture.states.every(state => exactKeys(state, ["id", "label", "copy"])), "state objects must have exact id/label/copy keys");
assert(fixture.journeys.every(journey => exactKeys(journey, ["id", "name", "destination", "kicker", "outcome", "kind", "hardState", "gates", "steps", "components", "compositions"])), "journey objects have missing or unknown keys");
assert(fixture.journeys.every(journey => exactKeys(journey.compositions, ["desktop", "iphone", "ipad"]) && journey.steps.every(step => exactKeys(step, ["label", "action"]))), "journey compositions or steps are not strictly closed");
assert(fixture.journeys.every(journey => journey.destination === journeyDestinations[journey.id]), "journey destination differs from the exact IA mapping");
assert(fixture.journeys.every(journey => {
  const contract = fixture.journeyContracts[journey.id];
  return exactKeys(contract, ["destination", "terminalOutcome", "steps"]) && contract.destination === journey.destination && safeProse(contract.terminalOutcome, 240) && contract.steps.length === journey.steps.length && contract.steps.every((step, index) => exactKeys(step, ["screen", "action", "transition"]) && step.action === journey.steps[index].action && safeProse(step.screen, 80) && safeToken.test(step.transition) && (index === contract.steps.length - 1 ? step.transition === "terminal" : step.transition !== "terminal"));
}), "journey IA screen/action/transition contracts are incomplete or inconsistent");
assert(fixture.journeys.every(journey => ["desktop", "iphone", "ipad"].every(platform => typeof journey.compositions[platform] === "string" && journey.compositions[platform].length > 20)), "every journey needs three explicit platform compositions");
assert(fixture.journeys.every(journey => Array.isArray(journey.steps) && journey.steps.length >= 3 && journey.steps.every(step => step.label && step.action)), "every journey needs at least three actionable steps");
assert(fixture.states.every(state => safeToken.test(state.id) && safeProse(state.label, 40) && safeProse(state.copy, 240)), "states must use bounded safe text");
assert(fixture.journeys.every(journey => safeToken.test(journey.id) && [journey.name, journey.destination, journey.kicker, journey.outcome, journey.hardState, ...Object.values(journey.compositions), ...journey.steps.flatMap(step => [step.label, step.action])].every(value => safeProse(value, 320))), "journey text must be bounded and markup/contact free");
assert(["PN1", "PN2", "PN3", "PN4", "PN5", "W5", "W6", "pn_moderation", "semantic_provider", "reranker"].every(gate => fixture.featureGates[gate] === false), "all PN/W5/W6/provider gates must be false");
assert(JSON.stringify(fixture.states.map(state => state.id)) === JSON.stringify(exactStates), "state vocabulary or order differs from the reference contract");
assert(JSON.stringify(fixture.statePrecedence) === JSON.stringify(exactPrecedence), "state precedence must equal the complete frozen sequence");
assert(fixture.journeys.filter(journey => Number(journey.id.slice(1)) >= 6).every(journey => journey.gates.length > 0), "J6-J13 must be visibly gated");
assert(fixture.journeys.find(journey => journey.id === "J13").gates.includes("pn_moderation"), "J13 requires moderation parent");
assert(components.every(component => closedTypes.includes(component.type)), "unknown domain component type found");
assert(closedTypes.every(type => observedTypes.has(type)), "closed domain catalog is not fully represented");
assert(fixture.journeys.every(matchesJourneyRoster), "journey component roster or cardinality differs from the frozen contract");

for (const component of components) {
  const errors = componentFailures(component);
  assert(errors.length === 0, `${component.type} invalid: ${errors.join(", ")}`);
}

const prohibitedCollapsePairs = [["HumanMessage","AgentContribution"],["HumanMessage","SystemEvent"],["AgentContribution","SystemEvent"],["ArtifactRevisionView","OutcomeRecordView"],["WorkRecordSection","PersonProfileView"],["PersonProfileView","AgentProfileView"],["OrganizationProfileView","WorkspaceProfileView"],["PublicWorkspaceView","OrganizationProfileView"],["PublicWorkObjectView","ArtifactRevisionView"],["WorkstreamRow","WorkSearchResult"],["ModerationCaseView","ConsensusDisplay"]];
for (const [left, right] of prohibitedCollapsePairs) {
  const sourceJourney = fixture.journeys.find(journey => journey.components.some(component => component.type === left));
  const target = structuredClone(components.find(component => component.type === right));
  const collapsedJourney = structuredClone(sourceJourney);
  collapsedJourney.components = collapsedJourney.components.map(component => component.type === left ? target : component);
  assert(componentFailures(target).length === 0, `collapse target is not independently schema-valid: ${right}`);
  assert(!matchesJourneyRoster(collapsedJourney), `schema-valid collapse mutation was admitted: ${left} as ${right}`);
}
const markupMutation = structuredClone(components.find(component => component.type === "HumanMessage"));
markupMutation.fields.humanPrincipal = `<img src=x onerror=alert(1)>`;
assert(componentFailures(markupMutation).length > 0, "markup mutation was admitted");
const privateBodyMutation = structuredClone(components.find(component => component.type === "HumanMessage"));
privateBodyMutation.fields.humanPrincipal = "person@example.com";
assert(componentFailures(privateBodyMutation).length > 0, "private-body mutation was admitted");
const unknownFieldMutation = structuredClone(components[0]);
unknownFieldMutation.fields.body = "private_body";
assert(componentFailures(unknownFieldMutation).length > 0, "forbidden body-key mutation was admitted");
for (const [type, field, bodyLikeToken] of [["HumanMessage", "humanPrincipal", "raw_private_transcript"], ["SuggestedWorkCard", "sourceRef", "secret_access_token"], ["ArtifactRevisionView", "sourceProvenance", "private_message_body"]]) {
  const mutation = structuredClone(components.find(component => component.type === type));
  assert(safeToken.test(bodyLikeToken) && !unsafeBody.test(bodyLikeToken), `${field} negative must exercise the exact allowlist rather than generic lexical rejection`);
  mutation.fields[field] = bodyLikeToken;
  assert(componentFailures(mutation).length > 0, `${field} admitted a body/secret-like safe token`);
}
assert(new Set(fixture.journeys.find(journey => journey.id === "J9").components.map(component => component.type)).size === 4, "J9 must keep four separate profile types");
assert(JSON.stringify(fixture.journeys.find(journey => journey.id === "J13").components.map(component => component.type)) === JSON.stringify(["ModerationCaseView", "ConsensusDisplay"]), "J13 moderation and consensus must remain separate");
const presentationJourney = fixture.journeys.find(journey => journey.id === "J4");
const presentationRun = presentationJourney.components.find(component => component.type === "RunTimeline");
const presentationArtifact = presentationJourney.components.find(component => component.type === "ArtifactRevisionView");
assert(presentationJourney.name === "Build and review a presentation" && presentationRun.fields.workType === "presentation" && presentationRun.fields.providerRoute === "openai_only_not_called", "J4 must remain the exact fictional OpenAI-only presentation work prototype");
assert(presentationArtifact.fields.artifactType === "presentation" && presentationArtifact.fields.editability === "editable" && presentationArtifact.fields.driveState === "not_saved", "J4 must preserve editable presentation and honest Drive state");
for (const postimage of expectedJ4Postimages) {
  const projected = structuredClone(presentationJourney.components);
  const run = projected.find(component => component.type === "RunTimeline");
  const artifact = projected.find(component => component.type === "ArtifactRevisionView");
  const outcome = projected.find(component => component.type === "OutcomeRecordView");
  Object.assign(run.fields, { stage: postimage.stage, currentActor: postimage.currentActor, nextAction: postimage.nextAction, artifactState: postimage.artifactState, driveState: postimage.driveState });
  Object.assign(artifact.fields, { reviewState: postimage.artifactReviewState, previewState: postimage.artifactPreviewState, driveState: postimage.driveState });
  Object.assign(outcome.fields, { decisionState: postimage.decisionState, nextAction: postimage.outcomeNextAction });
  projected.find(component => component.type === "InterventionRequest").title = postimage.interventionTitle;
  projected.find(component => component.type === "AgentContribution").title = postimage.agentTitle;
  artifact.title = postimage.artifactTitle;
  outcome.title = postimage.outcomeTitle;
  assert(projected.every(component => componentFailures(component).length === 0), `J4 step ${postimage.step} projects an invalid typed postimage`);
}
assert(!/anthropic|claude|fable/i.test(JSON.stringify([fixture.journeyContracts.J2, fixture.journeyContracts.J3, fixture.journeyContracts.J4, presentationJourney])), "representative presentation prototype must not route or fall back to Anthropic");
const networkWorkspace = fixture.journeys.find(journey => journey.id === "J6").components.find(component => component.type === "PublicWorkspaceView");
assert(networkWorkspace.fields.workBridgeState === "private_work_only" && networkWorkspace.fields.eligibleInput === "reviewed_work_record" && networkWorkspace.fields.publicResult === "no_projection", "Network prototype must preserve the governed private Work to reviewed Work Record boundary");
assert(!/IdentityEntry|ArtifactRevision(?!View)|OutcomeReview|WorkRecordEvidence|PublicWorkspaceProjection|PublicWorkObject(?!View)|WorkstreamItem|SettingsPolicyView|PublicPresenceInventory/.test(JSON.stringify(fixture)), "invented legacy component remains");

assert(!/transition\s*:\s*all\b/i.test(css), "CSS must not use transition: all");
assert(cssGovernanceFailures(css).length === 0, `CSS semantic-token governance failed: ${cssGovernanceFailures(css).join(", ")}`);
for (const [label, mutation] of [
  ["color", ".mutation { color: #fff; }"],
  ["radius", ".mutation { border-radius: 13px; }"],
  ["gap", ".mutation { display: flex; gap: 13px; }"],
  ["spacing", ".mutation { padding: 13px; }"],
  ["elevation", ".mutation { box-shadow: 0 2px 8px rgba(0,0,0,.2); }"],
  ["material", ".mutation { backdrop-filter: blur(16px); }"],
  ["top", ".mutation { position: absolute; top: 13px; }"],
  ["left", ".mutation { position: absolute; left: 13px; }"],
  ["inset", ".mutation { position: absolute; inset: 13px; }"],
  ["gradient-stop", ".mutation { background: linear-gradient(var(--surface) 12px, var(--canvas) 24px); }"],
  ["timing", ".mutation { transition-duration: 220ms; }"],
  ["easing", ".mutation { transition-timing-function: ease-in; }"]
]) assert(cssGovernanceFailures(`${css}\n${mutation}`).length > 0, `CSS ${label} literal mutation was admitted`);
assert(cssGovernanceFailures(css.replace("--space-12: 12px", "--space-12: 13px")).length > 0, "governed spacing token value drift was admitted");
assert(cssGovernanceFailures(css.replace("--radius-22: 22px", "--radius-22: 24px")).length > 0, "governed radius token value drift was admitted");
assert(cssGovernanceFailures(css.replace("--motion-medium-duration: 220ms", "--motion-medium-duration: 160ms")).length > 0, "governed motion token value drift was admitted");
assert(cssGovernanceFailures(css.replace(":root {", ":root { --space-6: 6px;")).length > 0, "unknown governed token was admitted");
assert(componentRendererFailures(js).length === 0, `semantic component renderer failed: ${componentRendererFailures(js).join(", ")}`);
for (const [left, right] of [
  ["suggestion-contract", "work-object-document"],
  ["artifact-sheet", "outcome-verdict"],
  ["outcome-verdict", "evidence-chain"],
  ["profile-person", "profile-organization"],
  ["profile-organization", "profile-workspace"],
  ["profile-workspace", "profile-agent"],
  ["moderation-docket", "consensus-board"]
]) assert(componentRendererFailures(js.replace(left, right)).length > 0, `${left} renderer collapsed into ${right}`);
assert(/min-width:\s*40px/.test(css) && /min-height:\s*40px/.test(css), "web controls require 40x40 minimum hit areas");
assert(/prefers-reduced-motion/.test(css) && /-webkit-font-smoothing:\s*antialiased/.test(css) && /font-variant-numeric:\s*tabular-nums/.test(css), "polish/accessibility primitives missing");
assert(["styles.css", "app.js", "fixture.json"].every(name => html.includes(name) || js.includes(name)), "asset linkage incomplete");
assert(/statePrecedence\.find/.test(js) && /new Set\(activeStates\.filter/.test(js), "app must consume the frozen multi-active-state precedence");
assert(/aria-current="step"/.test(js), "current step must use aria-current=step");
assert(/flowStatus\.focus/.test(js) && /role="status"/.test(html), "flow transitions must announce and move focus to a stable status");
assert(/announceContext\(`Selected/.test(js) && /announceContext\(`Resolved/.test(js), "journey, platform, and state changes must replace the live status");
assert(/function escapeHTML/.test(js) && /escapeHTML\(component\.title\)/.test(js) && /escapeHTML\(value\)/.test(js) && /escapeHTML\(journey\.name\)/.test(js) && /escapeHTML\(state\.copy\)/.test(js) && /escapeHTML\(journey\.compositions/.test(js) && /escapeHTML\(gate\)/.test(js), "fixture-derived rendered content must pass through HTML escaping");
assert(/function fixtureValue/.test(js) && /if \(preview\) return "field shape only · no current value"/.test(js) && /return escapeHTML\(labels\[component\.fields\[key\]\] \|\| humanize\(component\.fields\[key\]\)\)/.test(js), "prototype field values must be escaped and withheld in gated preview");
assert(/function contractRows/.test(js) && /technical-contract/.test(js) && /Technical contract/.test(js), "dense implementation fields must remain available through progressive disclosure");
assert(/delivery-stages/.test(js) && /Scope", "Research", "Story", "Design", "Review", "Drive/.test(js), "presentation work card must expose the exact delivery lifecycle");
assert(/work-contract-grid/.test(js) && /Accountable owner/.test(js) && /Provider route/.test(js) && /Next action/.test(js), "work card must expose owner, participants, inputs, route, cost, and next action");
assert(/work-network-bridge/.test(js) && /Private Work/.test(js) && /Reviewed evidence/.test(js) && /Work Record/.test(js) && /Network projection/.test(js), "Work and Network must share one governed lifecycle grammar");
assert(/function projectedJourneyComponents/.test(js) && /journeyPostimages\?\.\[journey\.id\]\?\.\[stepIndex\]/.test(js) && /Object\.assign\(run\.fields/.test(js) && /Object\.assign\(artifact\.fields/.test(js) && /Object\.assign\(outcome\.fields/.test(js) && /interventionTitle/.test(js) && /agentTitle/.test(js) && /artifact\.title = postimage\.artifactTitle/.test(js) && /outcome\.title = postimage\.outcomeTitle/.test(js), "J4 visible cards and titles must consume one exact step-specific postimage");
assert(/platform-iphone \.work-contract-grid/.test(css) && /platform-ipad \.semantic-grid/.test(css) && /family-run-timeline/.test(css), "representative Work prototype needs explicit iPhone and iPad composition rules");
assert(/preview-start|preview-advance|preview-back|preview-resume|preview-restart/.test(js), "gated local preview state machine is incomplete");
assert(/preview: false/.test(js) && /journey\.gates\.length && !appState\.preview\) return opaqueStateCard/.test(js), "gated child projection must be opaque unless local preview is explicit");
assert(/currentContract\(\)/.test(js) && /currentSteps\(\)/.test(js) && /destination-nav/.test(html) && /data-destination/.test(js), "app must execute the exact IA destination and per-journey contract");
assert(/Open \$\{escapeHTML\(steps\[0\]\.screen\)\}/.test(js) && /escapeHTML\(steps\[stepIndex\]\.action\)/.test(js) && /currentContract\(\)\.terminalOutcome/.test(js), "actions and terminal outcomes must be journey-specific");
assert(/field shape only · no current value/.test(js), "local preview must omit current values");
assert(/platform-iphone \.mobile-inspector \{ display: block; \}/.test(css), "iPhone must expose the full journey inspector");
assert(!/(fetch|XMLHttpRequest)\s*\(\s*["']https?:/i.test(js), "external network call found");

if (failures.length) {
  console.error(failures.map(item => `FAIL: ${item}`).join("\n"));
  process.exit(1);
}
console.log(JSON.stringify({ status: "PASS", journeys: fixture.journeys.length, destinations: fixture.destinations.length, states: fixture.states.length, componentTypes: observedTypes.size, gatesDefaultFalse: Object.keys(fixture.featureGates).length, mutationNegatives: prohibitedCollapsePairs.length + 29, boundArtifacts: Object.keys(manifestFiles).length, externalCalls: 0 }));
