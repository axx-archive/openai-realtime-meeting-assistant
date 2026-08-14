import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';
import test from 'node:test';

test('Project-bound Research exposes exact Open Drive and regenerate actions on native cards', async () => {
  registerHooks({
    resolve(specifier, context, nextResolve) {
      const stubs = new Set([
        'react-native', 'expo-image', 'expo-symbols', 'expo-linking', '@shopify/flash-list',
        '../files/fileActions', '../theme/tokens', './LinkPreviewCard', './ScoutRichText',
        './ChatAvatar', './messageGestures', './scoutReplyLifecycle', './messagePresentation',
        './workPresentation',
      ]);
      if (stubs.has(specifier)) return { url: `project-work-card-stub:${specifier}`, shortCircuit: true };
      return nextResolve(specifier, context);
    },
    load(url, context, nextLoad) {
      const modules: Record<string, string> = {
        'project-work-card-stub:react-native': `
          export const ActivityIndicator='ActivityIndicator';
          export const Animated={View:'AnimatedView'};
          export const findNodeHandle=()=>1;
          export const Pressable='Pressable'; export const Text='Text'; export const TextInput='TextInput'; export const View='View';
          export const StyleSheet={create:value=>value};
        `,
        'project-work-card-stub:expo-image': `export const Image='Image';`,
        'project-work-card-stub:expo-symbols': `export const SymbolView='SymbolView';`,
        'project-work-card-stub:expo-linking': `export const openURL=async()=>{};`,
        'project-work-card-stub:@shopify/flash-list': `export const useMappingHelper=()=>({getMappingKey:value=>String(value)});`,
        'project-work-card-stub:../files/fileActions': `export const authenticatedFileHeaders=()=>({}); export const authenticatedFileUrl=()=>'';`,
        'project-work-card-stub:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const shadow=proxy; export const space=proxy; export const type=proxy;`,
        'project-work-card-stub:./LinkPreviewCard': `export const LinkPreviewCard='LinkPreviewCard';`,
        'project-work-card-stub:./ScoutRichText': `export const ScoutRichText='ScoutRichText';`,
        'project-work-card-stub:./ChatAvatar': `export const ChatAvatar='ChatAvatar';`,
        'project-work-card-stub:./messageGestures': `export const messageLongPressDelayMs=350;`,
        'project-work-card-stub:./scoutReplyLifecycle': `export const scoutReplyLifecyclePresentation=()=>null;`,
        'project-work-card-stub:./messagePresentation': `export const extractHttpUrls=()=>[]; export const groupMessageReactions=()=>[]; export const parseMessageTextSegments=()=>[];`,
        'project-work-card-stub:./workPresentation': `export const safeWorkProgressNote=(value,fallback)=>String(value||fallback); export const workFamilyLabel=()=> 'Research'; export const workPhaseLabel=()=> 'Delivered';`,
      };
      if (modules[url]) return { format: 'module', source: modules[url], shortCircuit: true };
      return nextLoad(url, context);
    },
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { MessageBubble } = await import('../messaging/MessageBubble');
  const message = {
    id: 'project-research-work', kind: 'thread', role: 'scout', text: 'Research delivered.', createdAt: '2026-08-13T18:00:00Z',
    thread: { id: 'run-project-research', mode: 'research', query: 'Research the durable creator-economy evidence', status: 'complete', artifactId: 'artifact-project-research', projectId: 'project-research', projectTitle: 'Research Project', progressPercent: 100 },
  };
  const timestampReveal = { interpolate: () => 0 };
  let opened = 0;
  let saved = 0;
  let openedDrive = 0;
  let regenerated = 0;
  const render = (workSaved: boolean) => React.createElement(MessageBubble as React.ComponentType<any>, {
    message, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal,
    workDriveSaveAvailability: 'available', workSaved,
    onOpenWorkArtifact: () => { opened += 1; }, onSaveWorkArtifact: () => { saved += 1; },
    onOpenSavedWorkArtifact: () => { openedDrive += 1; }, onRegenerateWorkArtifact: () => { regenerated += 1; },
  } as any);
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(render(false)); });
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
});
