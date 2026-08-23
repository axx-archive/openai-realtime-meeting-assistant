import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {
  DECK_PREVIEW_NAVIGATION_JS,
  deckPreviewFitFrame,
  deckPreviewNavigationCommand,
  deckPreviewNavigationTarget,
  initialDeckPreviewNavigationState,
  parseDeckPreviewNavigationMessage,
} from '../messaging/deckPreviewNavigation';
import { registerTestStubModules } from './support/registerTestStubModules';

test('deck preview navigation accepts only bounded authoritative state', () => {
  assert.deepEqual(initialDeckPreviewNavigationState(), {
    status: 'loading',
    currentIndex: 0,
    slideCount: 0,
  });
  assert.equal(parseDeckPreviewNavigationMessage('not-json'), null);
  assert.equal(parseDeckPreviewNavigationMessage(JSON.stringify({
    kind: 'other-message',
    status: 'ready',
    currentIndex: 0,
    slideCount: 3,
  })), null);
  assert.equal(parseDeckPreviewNavigationMessage(JSON.stringify({
    kind: 'bonfire:deck-preview-navigation',
    status: 'ready',
    currentIndex: 3,
    slideCount: 3,
  })), null);
  const secondOfThree = parseDeckPreviewNavigationMessage(JSON.stringify({
    kind: 'bonfire:deck-preview-navigation',
    status: 'ready',
    currentIndex: 1,
    slideCount: 3,
  }));
  assert.deepEqual(secondOfThree, { status: 'ready', currentIndex: 1, slideCount: 3 });
  assert.equal(deckPreviewNavigationTarget(secondOfThree!, 'previous'), 0);
  assert.equal(deckPreviewNavigationTarget(secondOfThree!, 'next'), 2);
  assert.equal(deckPreviewNavigationTarget({ status: 'ready', currentIndex: 0, slideCount: 3 }, 'previous'), null);
  assert.equal(deckPreviewNavigationTarget({ status: 'ready', currentIndex: 2, slideCount: 3 }, 'next'), null);
  assert.equal(deckPreviewNavigationTarget(initialDeckPreviewNavigationState(), 'next'), null);
  assert.match(deckPreviewNavigationCommand(2), /controller\.show\(2\)/u);
});

test('shared fit law contains the real packaging chassis on phone, landscape, iPad, and Split View', () => {
  for (const [name, width, height] of [
    ['phone preview', 358, 201],
    ['phone landscape presenter', 389, 219],
    ['iPad presenter', 960, 540],
    ['iPad Split View', 551, 310],
  ] as const) {
    const fit = deckPreviewFitFrame(width, height);
    assert.ok(fit.scale > 0, `${name} has a positive scale`);
    assert.ok(fit.left >= 0 && fit.top >= 0, `${name} is centered inside the viewport`);
    assert.ok(fit.left + fit.width <= width + 0.001, `${name} does not crop horizontally`);
    assert.ok(fit.top + fit.height <= height + 0.001, `${name} does not crop vertically`);
    assert.ok(Math.abs(fit.width / fit.height - (16 / 9)) < 0.001, `${name} preserves 16:9`);
  }
});

test('injected controller navigates only top-level pages and reports stable position', () => {
  class FakeStyle {
    readonly values = new Map<string, { value: string; priority: string }>();
    overflow = '';
    getPropertyValue(name: string) { return this.values.get(name)?.value ?? ''; }
    getPropertyPriority(name: string) { return this.values.get(name)?.priority ?? ''; }
    setProperty(name: string, value: string, priority = '') { this.values.set(name, { value, priority }); }
    removeProperty(name: string) { this.values.delete(name); }
  }
  class FakeClassList {
    readonly values: Set<string>;
    constructor(values: string[]) { this.values = new Set(values); }
    contains(value: string) { return this.values.has(value); }
    add(value: string) { this.values.add(value); }
    remove(value: string) { this.values.delete(value); }
  }
  function page(classes: string[]) {
    const attributes = new Map<string, string>();
    return {
      style: new FakeStyle(),
      classList: new FakeClassList(classes),
      matches: () => true,
      setAttribute: (name: string, value: string) => { attributes.set(name, value); },
      attributes,
    };
  }
  const pages = [page(['pg', 'on']), page(['pg']), page(['pg'])];
  const nestedSection = page(['slide']);
  const embeddedNavigation = { style: new FakeStyle() };
  const stageStyle = new FakeStyle();
  stageStyle.setProperty('width', '1920px');
  stageStyle.setProperty('height', '1080px');
  const stage = {
    children: pages,
    style: stageStyle,
    getAttribute: () => null,
  };
  const documentElement = {
    clientWidth: 358,
    clientHeight: 201,
    dataset: {} as Record<string, string>,
    style: new FakeStyle(),
  };
  const posted: Array<Record<string, unknown>> = [];
  let resizeHandler: () => void = () => assert.fail('native deck controller did not register resize fitting');
  const fakeWindow = {
    innerWidth: 358,
    innerHeight: 201,
    ReactNativeWebView: {
      postMessage: (value: string) => { posted.push(JSON.parse(value) as Record<string, unknown>); },
    },
    getComputedStyle: (candidate: ReturnType<typeof page> | typeof stage) => ({
      display: 'classList' in candidate
        ? candidate.style.getPropertyValue('display') || (candidate.classList.contains('on') ? 'block' : 'none')
        : 'block',
      width: candidate.style.getPropertyValue('width'),
      height: candidate.style.getPropertyValue('height'),
    }),
    addEventListener: (event: string, handler: () => void) => {
      if (event === 'resize') resizeHandler = handler;
    },
  };
  const context = vm.createContext({
    document: {
      body: { style: new FakeStyle() },
      documentElement,
      getElementById: (id: string) => id === 'stage' ? stage : null,
      querySelectorAll: () => [nestedSection],
      querySelector: () => embeddedNavigation,
    },
    window: fakeWindow,
  });
  vm.runInContext(DECK_PREVIEW_NAVIGATION_JS, context);
  assert.deepEqual(posted.at(-1), {
    kind: 'bonfire:deck-preview-navigation',
    status: 'ready',
    currentIndex: 0,
    slideCount: 3,
  });
  assert.equal(embeddedNavigation.style.getPropertyValue('display'), 'none');
  assert.equal(documentElement.dataset.bonfireNativeDeckReady, 'true');
  assert.equal(stageStyle.getPropertyValue('visibility'), 'visible');
  assert.equal(stageStyle.getPropertyPriority('transform'), 'important');
  assert.match(stageStyle.getPropertyValue('transform'), /^scale\(0\.18/u);
  assert.ok(Number.parseFloat(stageStyle.getPropertyValue('left')) >= 0);
  assert.ok(Number.parseFloat(stageStyle.getPropertyValue('top')) >= 0);
  assert.equal(pages[0].attributes.get('aria-hidden'), 'false');
  assert.equal(pages[1].attributes.get('aria-hidden'), 'true');
  assert.equal(nestedSection.style.getPropertyValue('display'), '');

  vm.runInContext(deckPreviewNavigationCommand(1), context);
  assert.deepEqual(posted.at(-1), {
    kind: 'bonfire:deck-preview-navigation',
    status: 'ready',
    currentIndex: 1,
    slideCount: 3,
  });
  assert.equal(pages[0].style.getPropertyValue('display'), 'none');
  assert.equal(pages[1].style.getPropertyValue('display'), '');
  assert.equal(pages[1].classList.contains('on'), true);
  fakeWindow.innerWidth = 551;
  fakeWindow.innerHeight = 310;
  resizeHandler();
  assert.match(stageStyle.getPropertyValue('transform'), /^scale\(0\.28/u);
  assert.doesNotMatch(DECK_PREVIEW_NAVIGATION_JS, /\[class\*=["']slide/u);
});

test('native preview keeps deck navigation top-right and actions separate from non-deck cards', async () => {
  registerTestStubModules('deck-preview-stub:', {
        'deck-preview-stub:react-native': `
          export const ActivityIndicator='ActivityIndicator'; export const Pressable='Pressable'; export const ScrollView='ScrollView'; export const Text='Text'; export const View='View';
          export const StyleSheet={create:value=>value};
        `,
        'deck-preview-stub:react-native-webview': `
          const React=globalThis.__deckPreviewReact;
          export const injectedScripts=[];
          export const WebView=React.forwardRef(function WebView(props, ref) {
            React.useImperativeHandle(ref, ()=>({injectJavaScript:script=>injectedScripts.push(script), reload:()=>{}}));
            return React.createElement('WebViewHost', props);
          });
        `,
        'deck-preview-stub:expo-image': `export const Image='Image';`,
        'deck-preview-stub:expo-symbols': `export const SymbolView='SymbolView';`,
        'deck-preview-stub:../api/client': `export const api={artifact:async()=>({artifacts:[{id:'legacy-deck',metadata:{type:'html_deck',artifactVersion:'3',contentDigest:'${'b'.repeat(64)}'}}]})};`,
        'deck-preview-stub:../config': `export const API_BASE_URL='https://example.test'; export const NATIVE_CLIENT_HEADER='expo';`,
        'deck-preview-stub:../api/requestHelpers': `export const buildApiUrl=(base,path)=>base+path; export const buildAuthHeaders=(client,token,extra={})=>({Accept:'application/json','X-Bonfire-Client':client,...extra,...(token?{Authorization:'Bearer '+token}:{})});`,
        'deck-preview-stub:../theme/glass': `export const Glass='Glass';`,
        'deck-preview-stub:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const space=proxy; export const type=proxy;`,
        'deck-preview-stub:../files/fileActions': `export const authenticatedFileUrl=()=>''; export const authenticatedFileHeaders=()=>({});`,
        'deck-preview-stub:./ScoutRichText': `export const ScoutRichText='ScoutRichText';`,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  (globalThis as typeof globalThis & { __deckPreviewReact?: typeof React }).__deckPreviewReact = React;
  const { act, create } = await import('react-test-renderer');
  const { InlineArtifactPreview } = await import('../messaging/InlineArtifactPreview');
  let edited = 0;
  let presented = 0;
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(InlineArtifactPreview, {
      kind: 'html_deck', title: 'Field network', text: '', htmlContent: '<html><body></body></html>',
      desktopEditingOnly: true, onEdit: () => { edited += 1; }, onPresent: () => { presented += 1; },
    }));
  });
  const previous = renderer!.root.findByProps({ accessibilityLabel: 'Previous slide' });
  const next = renderer!.root.findByProps({ accessibilityLabel: 'Next slide' });
  assert.equal(previous.props.disabled, true);
  assert.equal(next.props.disabled, true);
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Deck preview loading' }).children.join(''), '— / —');

  const webView = renderer!.root.findByType('WebViewHost' as any);
  assert.deepEqual(webView.props.source, { html: '<html><body></body></html>' });
  assert.equal(webView.props.injectedJavaScript, DECK_PREVIEW_NAVIGATION_JS);
  assert.equal(webView.props.style.some((style: { opacity?: number } | false) => style && style.opacity === 0), true);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Fitting presentation preview' }).length, 1);
  await act(async () => {
    webView.props.onMessage({ nativeEvent: { data: JSON.stringify({
      kind: 'bonfire:deck-preview-navigation', status: 'ready', currentIndex: 0, slideCount: 3,
    }) } });
  });
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Slide 1 of 3' }).children.join(''), '1 / 3');
  assert.equal(webView.props.style.some((style: { opacity?: number } | false) => style && style.opacity === 0), false);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Fitting presentation preview' }).length, 0);
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Previous slide' }).props.disabled, true);
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Next slide' }).props.disabled, false);
  await act(async () => {
    webView.props.onMessage({ nativeEvent: { data: JSON.stringify({
      kind: 'bonfire:deck-preview-navigation', status: 'ready', currentIndex: 1, slideCount: 3,
    }) } });
  });
  assert.equal(renderer!.root.findByProps({ accessibilityLabel: 'Slide 2 of 3' }).children.join(''), '2 / 3');
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Edit presentation' }).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Editing is available on desktop' }).length, 1);
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Present' }).props.onPress(); });
  assert.deepEqual({ edited, presented }, { edited: 0, presented: 1 });

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, {
      kind: 'html_deck', title: 'Archived field network', text: '', htmlContent: '<html><body></body></html>',
      previewOnlyLabel: 'Preview only',
    }));
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Present' }).length, 0);
  assert.equal(
    renderer!.root.findByProps({ accessibilityLabel: 'Preview only. Full-screen presentation is unavailable for this archived deck.' }).props.accessibilityRole,
    'summary',
  );

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, {
      kind: 'html_deck', title: 'Signed field network', text: '', artifactId: 'deck', sessionToken: 'session',
      artifactVersion: 7, artifactDigest: 'a'.repeat(64),
      desktopEditingOnly: true, onPresent: () => { presented += 1; },
    }));
    await Promise.resolve();
  });
  const signedWebView = renderer!.root.findByType('WebViewHost' as any);
  assert.deepEqual(signedWebView.props.source, {
    uri: `https://example.test/artifacts/preview?id=deck&version=7&digest=${'a'.repeat(64)}`,
    headers: {
      Accept: 'text/html',
      'X-Bonfire-Client': 'expo',
      Authorization: 'Bearer session',
    },
  });
  assert.equal(signedWebView.props.injectedJavaScript, DECK_PREVIEW_NAVIGATION_JS);
  assert.equal(signedWebView.props.style.some((style: { opacity?: number } | false) => style && style.opacity === 0), true);
  await act(async () => {
    signedWebView.props.onMessage({ nativeEvent: { data: JSON.stringify({
      kind: 'bonfire:deck-preview-navigation', status: 'ready', currentIndex: 0, slideCount: 3,
    }) } });
  });
  assert.equal(signedWebView.props.style.some((style: { opacity?: number } | false) => style && style.opacity === 0), false);

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, {
      kind: 'html_deck', title: 'Legacy field network', text: '', artifactId: 'legacy-deck', sessionToken: 'session',
      desktopEditingOnly: true,
    }));
    await Promise.resolve();
  });
  const legacyWebView = renderer!.root.findByType('WebViewHost' as any);
  assert.deepEqual(legacyWebView.props.source, {
    uri: `https://example.test/artifacts/preview?id=legacy-deck&version=3&digest=${'b'.repeat(64)}`,
    headers: {
      Accept: 'text/html',
      'X-Bonfire-Client': 'expo',
      Authorization: 'Bearer session',
    },
  });

  await act(async () => { legacyWebView.props.onError(); });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Previous slide' }).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Retry deck preview' }).length, 1);
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Retry deck preview' }).props.onPress(); });
  assert.equal(renderer!.root.findAllByType('WebViewHost' as any).length, 1);

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, {
      kind: 'research', title: 'Research brief', text: 'Evidence',
    }));
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Previous slide' }).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Next slide' }).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Presentation slide navigation' }).length, 0);
});
