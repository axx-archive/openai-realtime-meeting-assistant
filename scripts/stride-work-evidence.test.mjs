import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

const source = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
function between(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from + start.length);
  assert.ok(from >= 0 && to > from);
  return source.slice(from, to);
}
function detailHarness(fetch) {
  const original = { id: 'private-work', title: 'Private report' };
  const context = vm.createContext({
    fetch, Date, Map, console,
    studioProjects: [original, { id: 'other-work' }],
    selectedStudioProjectId: original.id,
    studioProjectsLoadEpoch: 1,
    studioProjectDeepLinkUnavailable: '',
    studioProjectDetail: { querySelector: () => null },
    rendered: [],
    renderStudioProjects() { context.rendered.push('list'); },
    renderStudioProjectDetail(row) { context.rendered.push(row.id); },
  });
  vm.runInContext(between('var studioDetailRequests = new Map()', 'function studioWorkUsageNode('), context);
  return { context, original };
}

test('an explicit loss of access removes previously visible detail', async () => {
  const { context, original } = detailHarness(async () => ({ status: 403, ok: false, json: async () => ({ error: 'denied' }) }));
  await context.hydrateStudioWorkDetail(original);
  assert.equal(context.studioProjects.some(row => row.id === original.id), false);
  assert.equal(context.studioProjectDeepLinkUnavailable, original.id);
  assert.deepEqual(context.rendered, ['list']);
});

test('a response from the old account cannot repopulate work after session reset', async () => {
  let release;
  const { context, original } = detailHarness(() => new Promise(resolve => { release = resolve; }));
  const pending = context.hydrateStudioWorkDetail(original);
  context.studioProjectsLoadEpoch++;
  context.studioProjects = [{ id: 'new-account-work' }];
  release({ status: 200, ok: true, json: async () => ({ project: { ...original, feedback: { privateNote: 'old account' } } }) });
  await pending;
  assert.deepEqual(context.studioProjects, [{ id: 'new-account-work' }]);
  assert.equal(context.rendered.length, 0);
});

test('refresh hydrates only the exact requested result and preserves a review being typed', async () => {
  const { context, original } = detailHarness(async () => ({ status: 200, ok: true, json: async () => ({ project: { ...original, feedback: { reviewState: 'accepted' } } }) }));
  context.studioProjectDetail.querySelector = () => ({ dataset: { workReviewDirty: 'true' } });
  await context.hydrateStudioWorkDetail(original);
  assert.equal(context.studioProjects[0].feedback.reviewState, 'accepted');
  assert.equal(context.studioProjects[1].id, 'other-work');
  assert.equal(context.rendered.length, 0);
});

test('Home distinguishes active work, gated review, and an ordinary usable result', () => {
  const context = vm.createContext({});
  vm.runInContext(between('function homeOperatingGroups(', 'function homeOperatingWorkNode('), context);
  const groups = context.homeOperatingGroups([
    { id: 'question', status: 'needs_input' },
    { id: 'running', status: 'running', result: { canContinue: true } },
    { id: 'review', status: 'ready', result: { canContinue: true, qualityState: 'edited_after_admission' } },
    { id: 'ordinary', status: 'ready', result: { canContinue: true, reviewManaged: false } },
    { id: 'accepted-output', status: 'ready', result: { artifactId: 'document' } },
  ]);
  assert.equal(groups.judgment.map(row => row.id).join(','), 'question,review');
  assert.equal(groups.motion.map(row => row.id).join(','), 'running');
});

class ReviewNode {
  constructor(tag, className, text = '') { Object.assign(this, {tag, className, textContent:text, children:[], events:{}, dataset:{}, value:''}); }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.append(node); }
  setAttribute() {}
  addEventListener(event, handler) { this.events[event] = handler; }
  focus() {}
  remove() {}
}
function reviewHarness(fetch) {
  let nextID = 0;
  const context = vm.createContext({
    Map, Date, console, fetch,
    studioReviewDrafts:new Map(), studioProjectsLoadEpoch:1, selectedStudioProjectId:'A', studioProjects:[],
    crypto:{randomUUID:()=>`operation-${++nextID}`},
    bfEl:(...args)=>new ReviewNode(...args),
    studioProjectAction(text, disabled, action) { const node = new ReviewNode('button', '', text); node.disabled=disabled; node.events.click=action; return node; },
    refreshed:[], rendered:[],
    async hydrateStudioWorkDetail(project, force) { context.refreshed.push({project,force}); return true; },
    renderStudioProjectDetail(project) { context.rendered.push(project); },
    renderStudioProjects() {}, showToast() {},
  });
  vm.runInContext(between('function studioWorkFeedbackNode(', 'function renderStudioProjectDetail('), context);
  const project={id:'A',revision:'root-1',result:{artifactId:'result-A',version:1,digest:'digest-1'},feedback:{reviewState:'unreviewed',canReview:true}};
  context.studioProjects=[project];
  return {context,project};
}
const reviewForm = section => section.children.find(node=>node.tag==='form');
const submitReview = form => form.events.submit({preventDefault(){}});
function typeReview(form, note='Careful review draft') { form.children[0].value='accepted'; form.children[1].value=note; form.events.input(); }

test('an uncertain save retains its idempotency key when a review form is restored', async () => {
  const bodies=[];
  const {context,project}=reviewHarness(async (_url, options)=>{ bodies.push(JSON.parse(options.body)); throw new Error('connection lost after send'); });
  const first=reviewForm(context.studioWorkFeedbackNode(project)); typeReview(first);
  await submitReview(first);
  const restored=reviewForm(context.studioWorkFeedbackNode(project));
  assert.equal(restored.children[1].value,'Careful review draft');
  await submitReview(restored);
  assert.equal(bodies[0].feedback.idempotencyKey,bodies[1].feedback.idempotencyKey);
  typeReview(restored,'A materially different judgment'); await submitReview(restored);
  assert.notEqual(bodies[2].feedback.idempotencyKey,bodies[1].feedback.idempotencyKey);
});

test('a conflict requires explicit refresh and retains the note without applying it to a replacement result', async () => {
  const {context,project}=reviewHarness(async()=>({status:409,ok:false,json:async()=>({error:'work changed'})}));
  const form=reviewForm(context.studioWorkFeedbackNode(project)); typeReview(form);
  await submitReview(form);
  assert.equal(context.refreshed.length,0);
  assert.equal(form.children[2].disabled,true);
  const refresh=form.children[3].children.find(node=>node.tag==='button');
  await refresh.events.click();
  assert.equal(context.refreshed[0].force,true);
  const replacement={...project,result:{artifactId:'replacement',version:1,digest:'new'}};
  const next=context.studioWorkFeedbackNode(replacement);
  const prior=next.children.find(node=>node.tag==='aside');
  assert.ok(prior, 'same numeric version with a different artifact must retain the prior note');
  assert.equal(prior.children[1].textContent,'Careful review draft');
  assert.equal(reviewForm(next).children[1].value,'');
});

test('loss of access clears retained private drafts while keeping unrelated drafts', async () => {
  const {context,original}=detailHarness(async()=>({status:403,ok:false,json:async()=>({})}));
  context.studioReviewDrafts.set('private',{workId:original.id,note:'private note'});
  context.studioReviewDrafts.set('other',{workId:'other-work',note:'other note'});
  await context.hydrateStudioWorkDetail(original);
  assert.equal(context.studioReviewDrafts.has('private'),false);
  assert.equal(context.studioReviewDrafts.has('other'),true);
});

test('selecting another Work replaces a dirty detail from the previous Work', () => {
  const rendered=[];
  const context=vm.createContext({
    URL,location:{href:'http://localhost/work?project=B'},selectedStudioProjectId:'A',
    studioProjects:[{id:'A',status:'ready'},{id:'B',status:'ready'}],studioProjectsMobileLibraryOpen:false,
    studioProjectDeepLinkUnavailable:'',appShell:{dataset:{tool:'research',pd1Destination:'Work'}},studioProjectList:{},
    studioProjectDetail:{dataset:{workId:'A'},querySelector:()=>({dataset:{workReviewDirty:'true'}})},
    studioProjectFilter:'all',studioProjectsTitle:null,studioProjectsMeta:null,studioProjectsStatus:null,studioProjectsWorkspace:null,authedUser:null,
    window:{matchMedia:()=>({matches:false}),clearTimeout(){}},document:{getElementById:()=>({dataset:{}}),visibilityState:'visible'},
    studioProjectsRefreshTimer:0,studioProjectFilterTitles:()=>[],renderStudioProjectKindChips(){},renderStudioProjectProjectFilter(){},
    syncPackagingStoryThreadsFromProjects(){},schedulePackagingCommissionPoll(){},filteredStudioProjects:()=>context.studioProjects,
    renderStudioProjectList(){},renderStudioProjectDetail:project=>rendered.push(project.id),syncToolTopbar(){},
  });
  vm.runInContext(between('function selectStudioProject(id, options = {}) {',"document.addEventListener('visibilitychange', () => {"),context);
  context.selectStudioProject('B',{updateURL:false});
  assert.equal(context.selectedStudioProjectId,'B');
  assert.deepEqual(rendered,['B']);
});
