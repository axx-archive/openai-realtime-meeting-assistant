import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

test('rendered global voice island starts, opens its exact receipt, retries, and stops', async () => {
  registerTestStubModules('personal-realtime-island-stub:', {
    'personal-realtime-island-stub:react-native': `
      export const Pressable='Pressable'; export const Text='Text'; export const View='View';
      export const StyleSheet={create:value=>value};
    `,
    'personal-realtime-island-stub:expo-symbols': `export const SymbolView='SymbolView';`,
    'personal-realtime-island-stub:../components/Waveform': `export const Waveform='Waveform';`,
    'personal-realtime-island-stub:../theme/glass': `export const Glass='Glass';`,
    'personal-realtime-island-stub:../theme/tokens': `
      const proxy=new Proxy({}, {get:()=>0});
      export const colors={danger:'danger',ember:'ember',text1:'text1',text2:'text2'};
      export const radius=proxy; export const space=proxy;
      export const type=proxy; export const hitMin=44;
    `,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { PersonalRealtimeFloatingControl } = await import('../realtime/PersonalRealtimeFloatingControl');
  const calls: string[] = [];
  const opened: string[] = [];
  const controller = (status: string, threadId: string | null = null) => ({
    enabled: true,
    status,
    active: !['idle', 'error'].includes(status),
    tearingDown: false,
    turn: null,
    error: status === 'error' ? 'Scout voice lost its secure connection.' : null,
    trace: Array.from({ length: 28 }, () => 0),
    threadId,
    start: async () => { calls.push('start'); },
    stop: async (reason: string) => { calls.push(`stop:${reason}`); },
  });
  const render = (status: string, threadId: string | null = null) => React.createElement(
    PersonalRealtimeFloatingControl as React.ComponentType<any>,
    {
      realtime: controller(status, threadId),
      onOpenThread: (exactThreadId: string) => { opened.push(exactThreadId); },
    },
  );

  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => { renderer = create(render('idle')); });
  const start = renderer!.root.findByProps({ accessibilityLabel: 'Talk to Scout' });
  assert.deepEqual(start.props.accessibilityState, { disabled: false });
  const waveformType = 'Waveform' as unknown as React.ElementType;
  assert.equal(renderer!.root.findByType(waveformType).props.color, 'text2');
  assert.equal(renderer!.root.findByType(waveformType).props.scale, 0.42);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'End Scout voice' }).length, 0);
  await act(async () => { start.props.onPress(); await Promise.resolve(); });
  assert.deepEqual(calls, ['start']);

  await act(async () => { renderer!.update(render('listening', 'private-thread-build-73')); });
  await act(async () => {
    renderer!.root.findByProps({ accessibilityLabel: 'Listening. Open private conversation' }).props.onPress();
  });
  assert.deepEqual(opened, ['private-thread-build-73']);
  await act(async () => {
    renderer!.root.findByProps({ accessibilityLabel: 'End Scout voice' }).props.onPress();
    await Promise.resolve();
  });
  assert.deepEqual(calls, ['start', 'stop:completed']);

  await act(async () => { renderer!.update(render('error')); });
  const errorCopy = renderer!.root.findByProps({ accessibilityRole: 'alert' });
  assert.equal(errorCopy.props.children, 'Scout voice lost its secure connection.');
  assert.equal(renderer!.root.findByProps({ accessibilityLiveRegion: 'assertive' }).type, 'Glass');
  await act(async () => {
    renderer!.root.findByProps({ accessibilityLabel: 'Try again' }).props.onPress();
    await Promise.resolve();
    await Promise.resolve();
  });
  assert.deepEqual(calls, ['start', 'stop:completed', 'stop:cancelled', 'start']);

  await act(async () => {
    renderer!.update(React.createElement(
      PersonalRealtimeFloatingControl as React.ComponentType<any>,
      {
        realtime: controller('error'),
        startAllowed: false,
        onOpenThread: (exactThreadId: string) => { opened.push(exactThreadId); },
      },
    ));
  });
  assert.deepEqual(
    renderer!.root.findByProps({ accessibilityLabel: 'Try again' }).props.accessibilityState,
    { disabled: true },
    'room ownership must leave the error visible without allowing a private voice restart',
  );
  assert.equal(
    renderer!.root.findAllByProps({ accessibilityLabel: 'Stop Scout voice' }).length,
    1,
  );
  await act(async () => {
    renderer!.root.findByProps({ accessibilityLabel: 'Stop Scout voice' }).props.onPress();
    await Promise.resolve();
  });
  assert.equal(calls.at(-1), 'stop:cancelled');
  await act(async () => {
    renderer!.update(React.createElement(
      PersonalRealtimeFloatingControl as React.ComponentType<any>,
      {
        realtime: controller('listening', 'private-thread-build-73'),
        startAllowed: false,
        onOpenThread: (exactThreadId: string) => { opened.push(exactThreadId); },
      },
    ));
  });
  assert.deepEqual(
    renderer!.root.findByProps({ accessibilityLabel: 'Listening' }).props.accessibilityState,
    { disabled: true },
    'room ownership must keep the exact thread bound without navigating away from room media',
  );
  assert.deepEqual(opened, ['private-thread-build-73']);

  await act(async () => {
    renderer!.update(React.createElement(
      PersonalRealtimeFloatingControl as React.ComponentType<any>,
      {
        realtime: {
          ...controller('listening', 'private-thread-build-73'),
          enabled: false,
          tearingDown: true,
        },
        startAllowed: false,
      },
    ));
  });
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Stopping' }).props.disabled, true);
  assert.equal(renderer!.root.findByType(waveformType).props.color, 'ember');
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'End Scout voice' }).length, 1);
  await act(async () => { renderer!.unmount(); });
});
