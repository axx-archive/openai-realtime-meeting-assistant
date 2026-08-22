import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

const source = (relative: string) => readFileSync(path.resolve(import.meta.dirname, '..', relative), 'utf8');

test('blocked goal retry posts the exact root resume action and reloads without opening regeneration', async () => {
  const clientSource = source('api/client.ts');
  const screenSource = source('screens/ThreadScreen.tsx');
  const bubbleSource = source('messaging/MessageBubble.tsx');
  const retryGoalBody = screenSource.match(/const retryGoal = useCallback\([\s\S]*?const beginRegenerateWorkArtifact/u)?.[0] ?? '';

  assert.match(clientSource, /resumeGoal\([\s\S]*?request\('\/artifacts\/action',[\s\S]*?body: \{ id, action: 'resume' \}/u);
  assert.match(retryGoalBody, /goalID = String\(message\.thread\?\.artifactId/u);
  assert.match(retryGoalBody, /await api\.resumeGoal\(sessionToken, goalID\)/u);
  assert.match(retryGoalBody, /await load\(\)/u);
  assert.doesNotMatch(retryGoalBody, /followUpArtifact|setRegenerateWorkTarget|RegenerateWorkSheet/u);
  assert.match(bubbleSource, /failedGoal \? onRetryGoal\?\.\(message\) : onRegenerateWorkArtifact\?\.\(message\)/u);

  registerTestStubModules('goal-retry-api-stub:', {
    'goal-retry-api-stub:expo-file-system': 'export class File {}',
    'goal-retry-api-stub:../config': "export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';",
  });
  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; init?: RequestInit }> = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    requests.push({ url: String(input), init });
    return new Response('{"ok":true}', { status: 200, headers: { 'content-type': 'application/json' } });
  }) as typeof fetch;
  try {
    const { api } = await import('../api/client');
    await api.resumeGoal('native-session', 'goal-root-123');
  } finally {
    globalThis.fetch = originalFetch;
  }
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, 'https://example.test/artifacts/action');
  assert.equal(requests[0].init?.method, 'POST');
  assert.deepEqual(JSON.parse(String(requests[0].init?.body)), { id: 'goal-root-123', action: 'resume' });
  assert.equal((requests[0].init?.headers as Record<string, string>).Authorization, 'Bearer native-session');
});

test('goal drafts use resume while standalone research keeps regenerate', async () => {
  registerTestStubModules('goal-retry-bubble-stub:', {
    'goal-retry-bubble-stub:react-native': `
      export const ActivityIndicator='ActivityIndicator'; export const Animated={View:'AnimatedView'}; export const findNodeHandle=()=>1;
      export const Pressable='Pressable'; export const ScrollView='ScrollView'; export const Text='Text'; export const TextInput='TextInput'; export const View='View';
      export const StyleSheet={create:value=>value}; export const useWindowDimensions=()=>({width:390,height:844,fontScale:1});
    `,
    'goal-retry-bubble-stub:react-native-webview': `export const WebView='WebView';`,
    'goal-retry-bubble-stub:expo-image': `export const Image='Image';`,
    'goal-retry-bubble-stub:expo-symbols': `export const SymbolView='SymbolView';`,
    'goal-retry-bubble-stub:expo-linking': `export const openURL=async()=>{};`,
    'goal-retry-bubble-stub:expo-blur': `export const BlurView='BlurView';`,
    'goal-retry-bubble-stub:expo-glass-effect': `export const GlassView='GlassView'; export const isLiquidGlassAvailable=()=>false;`,
    'goal-retry-bubble-stub:@shopify/flash-list': `export const useMappingHelper=()=>({getMappingKey:value=>String(value)});`,
    'goal-retry-bubble-stub:../api/client': `export const api={};`,
    'goal-retry-bubble-stub:../files/fileActions': `export const authenticatedFileHeaders=()=>({}); export const authenticatedFileUrl=()=>'';`,
    'goal-retry-bubble-stub:../theme/glass': `export const Glass='Glass';`,
    'goal-retry-bubble-stub:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const shadow=proxy; export const space=proxy; export const type=proxy;`,
    'goal-retry-bubble-stub:./LinkPreviewCard': `export const LinkPreviewCard='LinkPreviewCard';`,
    'goal-retry-bubble-stub:./ScoutRichText': `export const ScoutRichText='ScoutRichText';`,
    'goal-retry-bubble-stub:./ChatAvatar': `export const ChatAvatar='ChatAvatar';`,
    'goal-retry-bubble-stub:./messageGestures': `export const messageLongPressDelayMs=350;`,
    'goal-retry-bubble-stub:./scoutReplyLifecycle': `export const scoutReplyLifecyclePresentation=()=>null;`,
    'goal-retry-bubble-stub:./messagePresentation': `export const extractHttpUrls=()=>[]; export const groupMessageReactions=()=>[]; export const parseMessageTextSegments=()=>[];`,
    'goal-retry-bubble-stub:./workPresentation': `export const workFamilyLabel=ref=>String(ref?.mode||'').toLowerCase()==='goal'?'Presentation':'Research'; export const workProgressPresentation=()=>({phase:null,phaseLabel:'Working',percent:50,needsInput:false,progressCopy:'Working'});`,
    'goal-retry-bubble-stub:./InlineArtifactPreview': `export const InlineArtifactPreview='InlineArtifactPreview';`,
  });
  (globalThis as typeof globalThis & { __DEV__?: boolean }).__DEV__ = false;
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { MessageBubble } = await import('../messaging/MessageBubble');
  const timestampReveal = { interpolate: () => 0 };
  let resumed = 0;
  let regenerated = 0;
  const callbacks = {
    onRetryGoal: () => { resumed += 1; },
    onRegenerateWorkArtifact: () => { regenerated += 1; },
  };
  const goal = {
    id: 'blocked-goal', kind: 'thread', role: 'scout', text: 'The current draft needs attention.', createdAt: '2026-08-21T15:00:00Z',
    thread: {
      id: 'goal-run', mode: 'goal', status: 'needs_attention', artifactId: 'goal-root',
      resultArtifactId: 'deck-draft', resultArtifactType: 'html_deck', resultTitle: 'Draft presentation',
    },
  };
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: goal, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal, ...callbacks,
    }));
  });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Retry draft from here' }).props.onPress(); });
  assert.deepEqual({ resumed, regenerated }, { resumed: 1, regenerated: 0 });

  const research = {
    id: 'failed-research', kind: 'thread', role: 'scout', text: 'Research needs attention.', createdAt: '2026-08-21T15:01:00Z',
    thread: { id: 'research-run', mode: 'research', status: 'failed', artifactId: 'research-artifact', query: 'Research the market' },
  };
  await act(async () => {
    renderer!.update(React.createElement(MessageBubble as React.ComponentType<any>, {
      message: research, own: false, showAuthor: true, sessionToken: 'session', viewerEmail: 'aj@example.test', timestampReveal, ...callbacks,
    }));
  });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Retry research' }).props.onPress(); });
  assert.deepEqual({ resumed, regenerated }, { resumed: 1, regenerated: 1 });
});
