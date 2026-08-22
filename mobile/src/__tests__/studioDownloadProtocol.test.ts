import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyOSWebNavigation,
  isStudioDownloadBridgeMessage,
  parseStudioDownloadMessage,
  parseStudioFileDownloadUrl,
} from '../artifacts/studioDownloadProtocol';

const origin = 'https://thebonfire.xyz';
const ref = 'a'.repeat(64);
const sceneRef = 'b'.repeat(64);

function nativeMessage(overrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    type: 'stride.studio.download',
    version: 1,
    kind: 'deck',
    format: 'pptx',
    artifactId: 'deck-42',
    fileName: 'Board review.pptx',
    expectedVersion: 7,
    sceneRef,
    ...overrides,
  });
}

test('iPhone and iPad accept the same exact revision-bound PowerPoint bridge', () => {
  for (const device of [
    { name: 'iPhone', width: 390, height: 844 },
    { name: 'iPad', width: 1024, height: 1366 },
  ]) {
    assert.ok(device.width < device.height);
    assert.deepEqual(
      parseStudioDownloadMessage(nativeMessage(), '/studio/deck/deck-42?mode=edit', origin),
      {
        kind: 'deck',
        format: 'pptx',
        artifactId: 'deck-42',
        fileName: 'Board review.pptx',
        expectedVersion: 7,
        sceneRef,
      },
      `${device.name} should preserve the exact deck revision contract`,
    );
  }
});

test('iPhone and iPad accept only same-origin artifact PDF blobs for either Studio', () => {
  for (const fixture of [
    { name: 'iPhone deck', path: '/studio/deck/deck-42?mode=present', kind: 'deck' },
    { name: 'iPad document', path: '/studio/document/doc-42?mode=view', kind: 'document' },
  ] as const) {
    const artifactId = fixture.kind === 'deck' ? 'deck-42' : 'doc-42';
    const fileName = `${fixture.kind} review.pdf`;
    const url = `/artifacts/blob?ref=${ref}&name=${encodeURIComponent(fileName)}`;
    const raw = JSON.stringify({
      type: 'stride.studio.download', version: 1, kind: fixture.kind, format: 'pdf',
      artifactId, fileName, url,
    });
    assert.deepEqual(parseStudioDownloadMessage(raw, fixture.path, origin), {
      kind: fixture.kind,
      format: 'pdf',
      artifactId,
      fileName,
      downloadUrl: `${origin}${url}`,
    }, fixture.name);
    assert.deepEqual(parseStudioFileDownloadUrl(`${origin}${url}`, fixture.path, origin), {
      kind: fixture.kind,
      format: 'pdf',
      artifactId,
      fileName,
      downloadUrl: `${origin}${url}`,
    });
  }
});

test('bridge rejects cross-artifact, cross-kind, extra-field, and unsafe URL messages', () => {
  const path = '/studio/deck/deck-42?mode=edit';
  assert.equal(parseStudioDownloadMessage(nativeMessage({ artifactId: 'deck-43' }), path, origin), null);
  assert.equal(isStudioDownloadBridgeMessage(nativeMessage({ artifactId: 'deck-43' })), true);
  assert.equal(isStudioDownloadBridgeMessage('{"type":"stride.studio.close"}'), false);
  assert.equal(parseStudioDownloadMessage(nativeMessage({ kind: 'document' }), path, origin), null);
  assert.equal(parseStudioDownloadMessage(nativeMessage({ debug: true }), path, origin), null);
  assert.equal(parseStudioDownloadMessage(nativeMessage({ sceneRef: 'A'.repeat(64) }), path, origin), null);
  assert.equal(parseStudioDownloadMessage(nativeMessage({ expectedVersion: 0 }), path, origin), null);
  assert.equal(
    parseStudioDownloadMessage(nativeMessage({ kind: 'document' }), '/studio/document/deck-42?mode=edit', origin),
    null,
  );

  const pdfBase = {
    type: 'stride.studio.download', version: 1, kind: 'deck', format: 'pdf',
    artifactId: 'deck-42', fileName: 'Board review.pdf',
  };
  for (const url of [
    `https://evil.example/artifacts/blob?ref=${ref}&name=Board+review.pdf`,
    `http://thebonfire.xyz/artifacts/blob?ref=${ref}&name=Board+review.pdf`,
    `blob:${origin}/opaque`,
    `data:application/pdf;base64,AA==`,
    `/artifacts/blob?ref=${ref}&ref=${ref}&name=Board+review.pdf`,
    `/artifacts/deck?ref=${ref}&name=Board+review.pdf`,
  ]) {
    assert.equal(parseStudioDownloadMessage(JSON.stringify({ ...pdfBase, url }), path, origin), null, url);
    assert.equal(parseStudioFileDownloadUrl(url, path, origin), null, url);
  }
  assert.equal(
    parseStudioDownloadMessage(JSON.stringify({
      ...pdfBase,
      url: `/artifacts/blob?ref=${ref}&name=different.pdf`,
    }), path, origin),
    null,
  );
});

test('OSWeb navigation keeps exact-origin pages internal and blocks unsafe schemes or downgrades', () => {
  assert.deepEqual(classifyOSWebNavigation('/studio/deck/deck-42?mode=edit', origin), { action: 'allow' });
  assert.deepEqual(classifyOSWebNavigation('https://thebonfire.xyz/files', origin), { action: 'allow' });
  assert.deepEqual(classifyOSWebNavigation('https://research.example/report', origin), {
    action: 'external', url: 'https://research.example/report',
  });
  assert.deepEqual(classifyOSWebNavigation('mailto:hello@example.com', origin), {
    action: 'external', url: 'mailto:hello@example.com',
  });
  for (const unsafe of [
    'http://thebonfire.xyz/studio/deck/deck-42',
    'http://research.example/report',
    'javascript:alert(1)',
    'blob:https://thebonfire.xyz/opaque',
    'file:///private/secret',
  ]) {
    assert.deepEqual(classifyOSWebNavigation(unsafe, origin), { action: 'block' }, unsafe);
  }
});
