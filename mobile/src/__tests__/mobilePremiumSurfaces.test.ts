import assert from 'node:assert/strict';
import test from 'node:test';

import { registerTestStubModules } from './support/registerTestStubModules';

test('Home Ember shortcut renders as one accessible direct action', async () => {
  registerTestStubModules('premium-home-stub:', {
    'premium-home-stub:react-native': `
      export const ActivityIndicator='ActivityIndicator'; export const Pressable='Pressable'; export const View='View';
      export const StyleSheet={create:value=>value};
    `,
    'premium-home-stub:react-native-svg': `export const Circle='Circle'; export const Path='Path'; export default 'Svg';`,
    'premium-home-stub:../theme/glass': `export const Glass='Glass';`,
    'premium-home-stub:../theme/tokens': `
      const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const shadow=proxy;
    `,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { BonfireChatShortcut } = await import('../components/BonfireChatShortcut');
  let presses = 0;
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(BonfireChatShortcut, { onPress: () => { presses += 1; } }));
  });
  const shortcut = renderer!.root.findByProps({ accessibilityLabel: 'Open Bonfire Chat' });
  assert.equal(shortcut.props.accessibilityRole, 'button');
  assert.equal(shortcut.props.accessibilityState.busy, false);
  assert.equal(renderer!.root.findAllByType('Svg' as any).length, 1);
  await act(async () => { shortcut.props.onPress(); });
  assert.equal(presses, 1);
  await act(async () => { renderer!.unmount(); });
});

test('native Activity renders the quiet five-stage sheet and opens the final result', async () => {
  registerTestStubModules('premium-activity-stub:', {
    'premium-activity-stub:react-native': `
      export const AccessibilityInfo={setAccessibilityFocus:()=>{}}; export const findNodeHandle=()=>1;
      export const Modal='Modal'; export const Pressable='Pressable'; export const ScrollView='ScrollView'; export const Text='Text'; export const View='View';
      export const StyleSheet={create:value=>value,hairlineWidth:1};
    `,
    'premium-activity-stub:react-native-safe-area-context': `export const SafeAreaView='SafeAreaView';`,
    'premium-activity-stub:expo-symbols': `export const SymbolView='SymbolView';`,
    'premium-activity-stub:../theme/tokens': `
      const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const space=proxy; export const type=proxy; export const hitMin=44;
    `,
    'premium-activity-stub:../theme/motion': `export const useReduceMotion=()=>true;`,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { WorkActivitySheet } = await import('../messaging/WorkActivitySheet');
  const message = {
    id: 'finished-deck',
    kind: 'thread',
    role: 'scout',
    createdAt: '2026-08-22T12:00:00Z',
    thread: {
      id: 'goal-1',
      mode: 'goal',
      processId: 'packaging_studio_v3',
      query: 'Make the deck',
      status: 'complete',
      currentStage: 'ship_approval',
      progressPercent: 100,
      resultArtifactId: 'deck-1',
      resultArtifactType: 'html_deck',
      resultQualityState: 'admitted',
      resultCanPresent: true,
      resultCanEdit: true,
    },
  } as const;
  let closed = 0;
  let opened = 0;
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(WorkActivitySheet as React.ComponentType<any>, {
      visible: true,
      message,
      onClose: () => { closed += 1; },
      onOpenResult: () => { opened += 1; },
    }));
  });
  const modal = renderer!.root.findByType('Modal' as any);
  assert.equal(modal.props.presentationStyle, 'formSheet');
  assert.equal(modal.props.animationType, 'none');
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Presentation stages' }).children.length, 5);
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: '100% complete' }).props.accessibilityRole, 'progressbar');
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Open presentation' }).props.onPress(); });
  assert.equal(opened, 1);

  await act(async () => {
    renderer!.update(React.createElement(WorkActivitySheet as React.ComponentType<any>, {
      visible: true,
      message: {
        ...message,
        id: 'edited-deck',
        thread: { ...message.thread, resultQualityState: 'edited_after_admission' },
      },
      onClose: () => { closed += 1; },
      onOpenResult: () => { opened += 1; },
    }));
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Open presentation' }).length, 0);
  assert.equal(renderer!.root.findByProps({ children: 'Presentation needs review' }).children.join(''), 'Presentation needs review');
  assert.equal(
    renderer!.root.findByProps({ children: 'Continue editing on desktop, then run a fresh review before sharing this version.' }).children.join(''),
    'Continue editing on desktop, then run a fresh review before sharing this version.',
  );
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Close activity' }).props.onPress(); });
  assert.deepEqual({ opened, closed }, { opened: 1, closed: 1 });
  await act(async () => { renderer!.unmount(); });
});
