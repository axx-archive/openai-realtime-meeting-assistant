import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import {
  artifactStudioIntent,
  artifactStudioKind,
  artifactStudioPath,
} from '../artifacts/studioRoutes';
import { registerTestStubModules } from './support/registerTestStubModules';

const source = (relative: string) => readFileSync(
  path.resolve(import.meta.dirname, '..', relative),
  'utf8',
);

test('shared web-route helper encodes authenticated Studio destinations', () => {
  const artifactId = 'artifact /?#+ field deck';
  for (const device of [
    { name: 'phone', width: 390, height: 844 },
    { name: 'iPad', width: 1024, height: 1366 },
  ]) {
    assert.ok(device.width < device.height, `${device.name} fixture must be portrait`);
    assert.equal(
      artifactStudioPath(artifactId, 'deck', 'edit'),
      '/studio/deck/artifact%20%2F%3F%23%2B%20field%20deck?mode=edit',
    );
    assert.equal(
      artifactStudioPath(artifactId, 'deck', 'present'),
      '/studio/deck/artifact%20%2F%3F%23%2B%20field%20deck?mode=present',
    );
    assert.equal(
      artifactStudioPath(artifactId, 'document', 'edit'),
      '/studio/document/artifact%20%2F%3F%23%2B%20field%20deck?mode=edit',
    );
    assert.equal(
      artifactStudioPath(artifactId, 'document', 'present'),
      '/studio/document/artifact%20%2F%3F%23%2B%20field%20deck?mode=view',
    );
  }
});

test('web-route helper preserves exact artifact capability independent of screen size', () => {
  for (const device of [
    { name: 'phone', width: 390, height: 844 },
    { name: 'iPad', width: 1024, height: 1366 },
  ]) {
    assert.ok(device.width < device.height, `${device.name} fixture must be portrait`);
    assert.equal(
      artifactStudioPath('owner-deck', 'deck', artifactStudioIntent(true)),
      '/studio/deck/owner-deck?mode=edit',
      `${device.name} web-route helper should retain deck write authority`,
    );
    assert.equal(
      artifactStudioPath('shared-deck', 'deck', artifactStudioIntent(false)),
      '/studio/deck/shared-deck?mode=present',
      `${device.name} web-route helper should retain deck read authority`,
    );
    assert.equal(
      artifactStudioPath('owner-document', 'document', artifactStudioIntent(true)),
      '/studio/document/owner-document?mode=edit',
      `${device.name} writer should edit a document`,
    );
    assert.equal(
      artifactStudioPath('shared-document', 'document', artifactStudioIntent(false)),
      '/studio/document/shared-document?mode=view',
      `${device.name} reader should view a document`,
    );
  }

  assert.equal(artifactStudioIntent(undefined), 'present');
  assert.equal(artifactStudioIntent('true'), 'present');
});

test('only editable artifact families receive Studio routes', () => {
  for (const kind of ['html_deck', 'presentation', 'slides', 'deck']) {
    assert.equal(artifactStudioKind(kind), 'deck');
  }
  for (const kind of ['markdown', 'document', 'doc', 'memo', 'brief']) {
    assert.equal(artifactStudioKind(kind), 'document');
  }
  for (const kind of ['research', 'report', 'table', 'ideation', 'image', '']) {
    assert.equal(artifactStudioKind(kind), null);
  }
  assert.equal(artifactStudioPath('   ', 'deck', 'edit'), '');
});

test('iOS decks use the native read-only viewer while documents retain the authenticated Studio route', () => {
  const thread = source('screens/ThreadScreen.tsx');
  const files = source('screens/FilesScreen.tsx');
  const web = source('screens/OSWebScreen.tsx');
  const navigator = source('navigation/RootNavigator.tsx');

  assert.match(thread, /resultStudioKind === 'deck'[\s\S]*navigation\.navigate\('DeckViewer'/u);
  assert.match(thread, /studioKind === 'deck'[\s\S]*navigation\.navigate\('DeckViewer'/u);
  assert.equal((thread.match(/desktopEditable: message\.thread\?\.resultCanEdit === true/g) ?? []).length, 3);
  assert.match(thread, /navigation\.navigate\('OSWeb', \{[\s\S]*artifactStudioPath\(resultArtifactId, resultStudioKind/u);
  assert.doesNotMatch(thread, /\/artifacts\/deck\?id=/u);
  assert.doesNotMatch(thread, /`\/artifact\/\$\{artifactId\}/u);
  assert.match(files, /studioKind === 'deck'[\s\S]*navigation\.navigate\('DeckViewer'/u);
  assert.match(files, /artifactStudioAccess\([\s\S]*?studioKind[\s\S]*?studioKind === 'deck'[\s\S]*?desktopEditable: access\.canWrite === true/u);
  assert.match(files, /await api\.artifactStudioAccess\([\s\S]*?sessionToken,[\s\S]*?file\.artifactId,[\s\S]*?studioKind,[\s\S]*?\)/u);
  assert.match(files, /artifactStudioPath\([\s\S]*?file\.artifactId,[\s\S]*?studioKind,[\s\S]*?artifactStudioIntent\(access\.canWrite\),[\s\S]*?\)/u);
  assert.match(files, /artifact\?\.metadata\?\.type \?\? artifact\?\.metadata\?\.artifactType/u);

  assert.match(navigator, /name="OSWeb"[\s\S]*?presentation: 'modal'/u);
  assert.match(navigator, /name="DeckViewer"[\s\S]*?presentation: 'fullScreenModal'/u);
  assert.match(web, /<SafeAreaView[\s\S]*?edges=\{\['top', 'left', 'right'\]\}/u);
  assert.match(web, /auth\/native-web-session\?path=\$\{encodeURIComponent\(safePath\)\}/u);
  assert.match(web, /candidate\.type !== 'stride\.studio\.close' \|\| candidate\.version !== 1/u);
  assert.match(web, /keys !== 'artifactId,kind,mode,type,version'/u);
  assert.match(web, /routeArtifactId === message\.artifactId[\s\S]*?destination\.searchParams\.get\('mode'\) === message\.mode/u);
  assert.match(web, /onMessage=\{\(\{ nativeEvent \}\) => \{[\s\S]*?studioCloseMessageMatchesPath\(nativeEvent\.data, path\)[\s\S]*?navigation\.goBack\(\)/u);
});

test('documents expose Edit and full-screen actions while unsupported artifacts stay read-only', async () => {
  registerTestStubModules('studio-preview-stub:', {
    'studio-preview-stub:react-native': `
      export const ActivityIndicator='ActivityIndicator'; export const Pressable='Pressable';
      export const ScrollView='ScrollView'; export const Text='Text'; export const View='View';
      export const StyleSheet={create:value=>value};
    `,
    'studio-preview-stub:react-native-webview': `export const WebView='WebView';`,
    'studio-preview-stub:expo-symbols': `export const SymbolView='SymbolView';`,
    'studio-preview-stub:../api/client': `export const api={};`,
    'studio-preview-stub:../config': `export const API_BASE_URL='https://example.test';`,
    'studio-preview-stub:../api/requestHelpers': `export const buildApiUrl=(_base,path)=>path;`,
    'studio-preview-stub:../artifacts/nativeDeckViewer': `export const nativeTextArtifactIsRenderable=value=>{const text=String(value??'').trim();if(!text||text.startsWith('<'))return false;try{JSON.parse(text);return false}catch{return true}};`,
    'studio-preview-stub:../theme/glass': `export const Glass='Glass';`,
    'studio-preview-stub:../theme/tokens': `const proxy=new Proxy({}, {get:()=>0}); export const colors=proxy; export const radius=proxy; export const space=proxy; export const type=proxy;`,
    'studio-preview-stub:./ScoutRichText': `export const ScoutRichText='ScoutRichText';`,
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const React = (await import('react')).default;
  const { act, create } = await import('react-test-renderer');
  const { InlineArtifactPreview } = await import('../messaging/InlineArtifactPreview');
  let edits = 0;
  let expands = 0;
  const props = {
    title: 'Opportunity report', text: 'A concise native report.', artifactId: 'document-1',
    onEdit: () => { edits += 1; }, onExpand: () => { expands += 1; },
  };
  let renderer: import('react-test-renderer').ReactTestRenderer;
  await act(async () => {
    renderer = create(React.createElement(InlineArtifactPreview, { ...props, kind: 'document' }));
  });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Edit document' }).props.onPress(); });
  await act(async () => { renderer!.root.findByProps({ accessibilityLabel: 'Open in full screen' }).props.onPress(); });
  assert.deepEqual({ edits, expands }, { edits: 1, expands: 1 });

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, { title: props.title, text: props.text, artifactId: props.artifactId, kind: 'document', onExpand: props.onExpand }));
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Edit document' }).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Open in full screen' }).length, 1);

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, { ...props, kind: 'research' }));
  });
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Edit document' }).length, 0);
  assert.equal(renderer!.root.findAllByProps({ accessibilityLabel: 'Open in full screen' }).length, 1);

  await act(async () => {
    renderer!.update(React.createElement(InlineArtifactPreview, {
      title: props.title,
      text: '{"artifact":{"slides":[]}}',
      artifactId: props.artifactId,
      kind: 'document',
      onExpand: props.onExpand,
    }));
  });
  const serializedPreview = renderer!.root.findByProps({ text: 'Document is ready to open.' }).props.text as string;
  assert.equal(serializedPreview, 'Document is ready to open.');
  assert.doesNotMatch(serializedPreview, /artifact|slides|\{|\}/u);
});
