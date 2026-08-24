import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

test('Project-bound Research uses governed actions while Project presentations keep the deck viewer', async () => {
  registerTestStubModules('project-work-card-stub:', {
        'project-work-card-stub:react-native': `
          export const ActivityIndicator='ActivityIndicator';
          export const Animated={View:'AnimatedView'};
          export const findNodeHandle=()=>1;
          export const Modal='Modal'; export const Pressable='Pressable'; export const ScrollView='ScrollView'; export const Text='Text'; export const TextInput='TextInput'; export const View='View';
          export const StyleSheet={create:value=>value};
          export const useWindowDimensions=()=>({width:390,height:844,fontScale:1});
        `,
        'project-work-card-stub:react-native-webview': `export const WebView='WebView';`,
        'project-work-card-stub:expo-image': `export const Image='Image';`,
        'project-work-card-stub:expo-symbols': `export const SymbolView='SymbolView';`,
        'project-work-card-stub:expo-linking': `export const openURL=async()=>{};`,
        'project-work-card-stub:expo-blur': `export const BlurView='BlurView';`,
        'project-work-card-stub:expo-glass-effect': `export const GlassView='GlassView'; export const isLiquidGlassAvailable=()=>false;`,
		'project-work-card-stub:react-native-safe-area-context': `export const SafeAreaView='SafeAreaView';`,
        'project-work-card-stub:@shopify/flash-list': `export const useMappingHelper=()=>({getMappingKey:value=>String(value)});`,
        'project-work-card-stub:../api/client': `export const api={};`,
        'project-work-card-stub:../files/fileActions': `export const authenticatedFileHeaders=()=>({}); export const authenticatedFileUrl=()=>'';`,
        'project-work-card-stub:../theme/glass': `export const Glass='Glass';`,
        'project-work-card-stub:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const shadow=proxy; export const space=proxy; export const type=proxy;`,
        'project-work-card-stub:./LinkPreviewCard': `export const LinkPreviewCard='LinkPreviewCard';`,
        'project-work-card-stub:./ScoutRichText': `export const ScoutRichText='ScoutRichText';`,
        'project-work-card-stub:./ChatAvatar': `export const ChatAvatar='ChatAvatar';`,
        'project-work-card-stub:./messageGestures': `export const messageLongPressDelayMs=350;`,
        'project-work-card-stub:./scoutReplyLifecycle': `export const scoutReplyLifecyclePresentation=()=>null;`,
        'project-work-card-stub:./messagePresentation': `export const extractHttpUrls=()=>[]; export const groupMessageReactions=()=>[]; export const parseMessageTextSegments=()=>[];`,
        'project-work-card-stub:./workPresentation': `export const workFamilyLabel=ref=>String(ref?.mode||'').toLowerCase()==='presentation'||String(ref?.processId||'').startsWith('packaging_studio')?'Presentation':'Research'; export const workProgressPresentation=ref=>({phase:null,phaseLabel:'Delivered',percent:Number(ref?.progressPercent||0),needsInput:Boolean(ref?.checkpoint),progressCopy:'Delivered'});`,
        'project-work-card-stub:./InlineArtifactPreview': `export const InlineArtifactPreview='InlineArtifactPreview';`,
  });
  (globalThis as typeof globalThis & { __DEV__?: boolean }).__DEV__ = false;
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { MessageBubble } = await import('../messaging/MessageBubble');
  const { WorkProjectDetail } = await import('../work/WorkProjectSheet');
  const message = {
    id: 'project-research-work', kind: 'thread', role: 'scout', text: 'Research delivered.', createdAt: '2026-08-13T18:00:00Z',
    thread: { id: 'run-project-research', mode: 'research', query: 'Research the durable creator-economy evidence', status: 'complete', artifactId: 'artifact-project-research', projectId: 'project-research', projectTitle: 'Research Project', progressPercent: 100 },
  };
  const timestampReveal = { interpolate: () => 0 };
  let opened = 0;
  let saved = 0;
  let openedDrive = 0;
  let regenerated = 0;
  let openedStudio = '';
  const render = (workSaved: boolean) => React.createElement(MessageBubble as React.ComponentType<any>, {
    message, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
    workDriveSaveAvailability: 'available', workSaved,
    onOpenWorkArtifact: () => { opened += 1; }, onSaveWorkArtifact: () => { saved += 1; },
    onOpenSavedWorkArtifact: () => { openedDrive += 1; }, onRegenerateWorkArtifact: () => { regenerated += 1; },
  } as any);
  let renderer: import('react-test-renderer').ReactTestRenderer;
  const studioMessage = {
    id: 'studio-project-receipt', kind: 'thread', role: 'scout', text: 'Presentation filed.', createdAt: '2026-08-13T17:58:00Z',
    studioProject: {
      id: 'studio-project-like-a-farmer', kind: 'presentation', title: 'Like A Farmer decision deck', status: 'running',
      href: '/work?project=studio-project-like-a-farmer', phase: 'build', progressPercent: 42,
    },
  };
  const renderStudio = (studioProject: Record<string, unknown>) => React.createElement(MessageBubble as React.ComponentType<any>, {
    message: { ...studioMessage, studioProject }, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
    onOpenStudioProject: (projectId: string) => { openedStudio = projectId; },
  } as any);
  const renderedText = () => renderer!.root.findAllByType('Text' as any)
    .flatMap((node) => node.children)
    .filter((value): value is string => typeof value === 'string');

  await act(async () => { renderer = create(renderStudio(studioMessage.studioProject)); });
  const studioReceipt = renderer!.root.findByProps({ accessibilityLabel: 'Presentation. Like A Farmer decision deck. Build · 42%. View in Work.' });
  assert.ok(renderedText().includes('Build · 42%'));
  assert.ok(renderedText().includes('View in Work'));
  await act(async () => { studioReceipt.props.onPress(); });
  assert.equal(openedStudio, 'studio-project-like-a-farmer');

  await act(async () => {
    renderer!.update(renderStudio({
      ...studioMessage.studioProject,
      status: 'needs_attention',
      progressPercent: 76,
      phase: 'polish',
      attention: {
        title: 'Final review needs another pass',
        body: 'Scout saved the draft, but its latest rendered review did not pass.',
      },
    }));
  });
  assert.ok(renderedText().includes('Final review needs another pass'));
  assert.ok(renderedText().includes('Scout saved the draft, but its latest rendered review did not pass.'));
  assert.ok(renderedText().includes('View in Work'));

  await act(async () => { renderer!.update(render(false)); });
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Project: Research Project' }).findByType('Text' as any).children.join(''), 'Project · Research Project');
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Open deliverable' }).props.onPress(); });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Save deliverable to Drive' }).props.onPress(); });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Edit prompt and regenerate deliverable' }).props.onPress(); });
  assert.deepEqual({ opened, saved, regenerated }, { opened: 1, saved: 1, regenerated: 1 });
  await act(async () => { renderer!.update(render(true)); });
  const drive = renderer!.root.findByProps({ accessibilityLabel: 'Open saved deliverable in Drive' });
  assert.equal(drive.findByType('Text' as any).children.join(''), 'Open in Drive');
  await act(async () => { drive.props.onPress(); });
  assert.equal(openedDrive, 1);

  const presentationMessage = {
    id: 'project-presentation-work', kind: 'thread', role: 'scout', text: 'Presentation delivered.', createdAt: '2026-08-13T18:05:00Z',
    thread: { id: 'run-project-presentation', mode: 'presentation', query: 'Build the launch deck', status: 'complete', artifactId: 'artifact-project-presentation', projectId: 'project-presentation', projectTitle: 'Launch Project', progressPercent: 100 },
  };
  let presented = 0;
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: presentationMessage, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
      onViewArtifactFullscreen: () => { presented += 1; },
    } as any));
  });
  const deck = renderer!.root.findByType('InlineArtifactPreview' as any);
  assert.equal(deck.props.kind, 'html_deck');
  assert.equal(deck.props.artifactId, 'artifact-project-presentation');
  await act(async () => { deck.props.onPresent(); });
  assert.equal(presented, 1);

  const runningGoal = {
    id: 'running-goal', kind: 'thread', role: 'scout', text: 'Building the presentation.', createdAt: '2026-08-13T18:06:00Z',
    thread: { id: 'run-goal', mode: 'goal', processId: 'packaging_studio', query: 'Build the deck', status: 'running', artifactId: 'goal-root-not-a-deck' },
  };
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: runningGoal, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
    } as any));
  });
  const runningPreview = renderer!.root.findByType('InlineArtifactPreview' as any);
  assert.equal(runningPreview.props.loading, true);
  assert.equal(runningPreview.props.artifactId, undefined);
  assert.equal(runningPreview.props.onEdit, undefined);
  assert.equal(runningPreview.props.onPresent, undefined);

  const blockedGoal = {
    ...runningGoal,
    id: 'blocked-goal',
    thread: { ...runningGoal.thread, status: 'needs_attention' },
  };
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: blockedGoal, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
    } as any));
  });
  assert.equal(renderer!.root.findAllByType('InlineArtifactPreview' as any).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'View presentation failure details' }).length, 1);

  const goalWithDeck = {
    id: 'goal-with-deck', kind: 'thread', role: 'scout', text: 'Ready for your decision.', createdAt: '2026-08-13T18:10:00Z',
    thread: {
      id: 'run-goal-deck', mode: 'goal', processId: 'packaging_studio', query: 'Build the Like A Farmer deck', status: 'approval_required', artifactId: 'goal-artifact',
      resultArtifactId: 'deck-artifact', resultTitle: 'Like A Farmer — Optimization Insights',
      checkpoint: { id: 'checkpoint-final', stageId: 'ship', question: 'Is this ready to share?', options: [{ id: 'approve-final', label: 'Approve and share', action: 'approve' }] },
    },
  };
  let checkpointChoice = '';
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: goalWithDeck, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
      onResolveWorkCheckpoint: (_message: unknown, option: { id: string }) => { checkpointChoice = option.id; },
    } as any));
  });
  const resultDeck = renderer!.root.findByType('InlineArtifactPreview' as any);
  assert.equal(resultDeck.props.artifactId, 'deck-artifact');
  assert.equal(resultDeck.props.title, 'Like A Farmer — Optimization Insights');
  const approve = renderer!.root.findByProps({ accessibilityLabel: 'Approve and share' });
  await act(async () => { approve.props.onPress(); });
  assert.equal(checkpointChoice, 'approve-final');

  const reportGoal = {
    id: 'goal-with-report', kind: 'thread', role: 'scout', text: 'Report delivered.', createdAt: '2026-08-13T18:12:00Z',
    thread: {
      id: 'run-goal-report', mode: 'goal', processId: 'document_report', query: 'Write the opportunity report', status: 'complete', artifactId: 'goal-report-root',
      resultArtifactId: 'report-artifact', resultTitle: 'Insights & Opportunities',
      resultQualityState: 'admitted', resultCanExport: true,
      resultPreview: '# Insights & Opportunities\n\nThe engagement army is an activation network, not a reach product.',
    },
  };
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: reportGoal, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
    } as any));
  });
  const report = renderer!.root.findByType('InlineArtifactPreview' as any);
  assert.equal(report.props.kind, 'document');
  assert.equal(report.props.artifactId, 'report-artifact');
  assert.match(report.props.text, /activation network, not a reach product/u);

  const historicalInlineDeck = {
    id: 'historical-inline-deck', kind: 'message', role: 'scout',
    text: '<!doctype html><html><head><title>Archived presentation</title></head><body></body></html>',
    createdAt: '2026-08-13T18:14:00Z',
  };
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: historicalInlineDeck, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
      onViewArtifactFullscreen: () => { presented += 1; },
    } as any));
  });
  const historicalPreview = renderer!.root.findByType('InlineArtifactPreview' as any);
  assert.equal(historicalPreview.props.previewOnlyLabel, 'Preview only');
  assert.equal(historicalPreview.props.onPresent, undefined);

  const attentionProject = {
    schemaVersion: 1, id: 'attention-project', kind: 'document', title: 'Market evidence report', revision: 1,
    status: 'needs_attention', progressPercent: 74, phase: 'polish',
    phases: [
      { id: 'brief', label: 'Brief', status: 'complete' },
      { id: 'build', label: 'Build', status: 'complete' },
      { id: 'polish', label: 'Polish', status: 'needs_attention' },
      { id: 'ready', label: 'Ready', status: 'upcoming' },
    ],
    createdAt: '2026-08-13T17:00:00Z', updatedAt: '2026-08-13T18:15:00Z',
    rootRunId: 'attention-run', rootArtifactId: 'attention-project', href: '/work?project=attention-project',
    source: { threadId: 'source-thread', kind: 'channel' },
    attention: { title: 'Source access changed', body: 'Scout can’t verify one of the sources needed to finish this work.', actionLabel: 'Review sources' },
    canRename: true,
  };
  let openedAttentionSource = '';
  await act(async () => {
    renderer!.update(React.createElement(WorkProjectDetail as React.ComponentType<any>, {
      project: attentionProject,
      onOpenResult: () => {},
      onOpenSource: (project: { id: string }) => { openedAttentionSource = project.id; },
      onContinueResult: () => {},
      onResolveCheckpoint: () => {},
      onRename: () => {},
    }));
  });
  assert.ok(renderedText().includes('Source access changed'));
  assert.ok(renderedText().includes('Scout can’t verify one of the sources needed to finish this work.'));
  assert.ok(renderedText().includes('Review sources'));
  assert.equal(renderedText().includes('The finished work will land here'), false);
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Review sources' }).props.onPress(); });
  assert.equal(openedAttentionSource, 'attention-project');
});
