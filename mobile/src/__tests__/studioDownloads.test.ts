import assert from 'node:assert/strict';
import test from 'node:test';
import { registerTestStubModules } from './support/registerTestStubModules';

type StudioTestState = {
  downloads: Array<{ url: string; options: { headers?: Record<string, string> } }>;
  shares: Array<{ uri: string; options: { mimeType?: string; dialogTitle?: string } }>;
  writes: Uint8Array[];
};

test('native PDF and PowerPoint transfers preserve authentication and open the file share sheet', async () => {
  registerTestStubModules('studio-download-stub:', {
    'studio-download-stub:expo-file-system': `
      export const Paths={cache:'file:///cache'};
      export class File {
        static async downloadFileAsync(url,destination,options){
          globalThis.__studioDownloadState.downloads.push({url,options});
          destination.exists=true; destination.size=12; return destination;
        }
        constructor(...parts){this.uri='file:///cache/'+String(parts.at(-1));this.exists=false;this.size=0;}
        create(){this.exists=true;this.size=0;}
        write(bytes){this.exists=true;this.size=bytes.byteLength;globalThis.__studioDownloadState.writes.push(bytes);}
        delete(){this.exists=false;this.size=0;}
      }
    `,
    'studio-download-stub:expo-sharing': `
      export async function isAvailableAsync(){return true;}
      export async function shareAsync(uri,options){globalThis.__studioDownloadState.shares.push({uri,options});}
    `,
    'studio-download-stub:../config': `
      export const API_BASE_URL='https://thebonfire.xyz';
      export const NATIVE_CLIENT_HEADER='expo';
    `,
    'studio-download-stub:../api/requestHelpers': `
      export function buildAuthHeaders(client,token,extra={}){
        return {...extra,Authorization:'Bearer '+token,'X-Bonfire-Client':client};
      }
    `,
  });

  const state: StudioTestState = { downloads: [], shares: [], writes: [] };
  (globalThis as typeof globalThis & { __studioDownloadState: StudioTestState }).__studioDownloadState = state;
  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; init?: RequestInit }> = [];
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    requests.push({ url: String(input), init });
    return new Response(new Uint8Array([0x50, 0x4b, 0x03, 0x04, 1, 2, 3, 4]), {
      status: 200,
      headers: {
        'content-type': 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
        'content-length': '8',
      },
    });
  }) as typeof fetch;

  try {
    const { shareStudioDownload } = await import('../artifacts/studioDownloads');
    await shareStudioDownload('native-session', {
      kind: 'document', format: 'pdf', artifactId: 'doc-42', fileName: 'Opportunity report.pdf',
      downloadUrl: `https://thebonfire.xyz/artifacts/blob?ref=${'a'.repeat(64)}&name=Opportunity+report.pdf`,
    });
    await shareStudioDownload('native-session', {
      kind: 'deck', format: 'pptx', artifactId: 'deck-42', fileName: 'Board review.pptx',
      expectedVersion: 7, sceneRef: 'b'.repeat(64),
    });
    await assert.rejects(
      shareStudioDownload('native-session', {
        kind: 'document', format: 'pdf', artifactId: 'doc-42', fileName: 'Opportunity report.pdf',
        downloadUrl: `https://evil.example/artifacts/blob?ref=${'a'.repeat(64)}&name=Opportunity+report.pdf`,
      }),
      /unsafe download location/,
    );
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(state.downloads.length, 1);
  assert.equal(state.downloads[0].options.headers?.Authorization, 'Bearer native-session');
  assert.equal(state.downloads[0].options.headers?.['X-Bonfire-Client'], 'expo');
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, 'https://thebonfire.xyz/artifacts/export-pptx');
  assert.equal((requests[0].init?.headers as Record<string, string>).Authorization, 'Bearer native-session');
  assert.deepEqual(JSON.parse(String(requests[0].init?.body)), {
    artifactId: 'deck-42', expectedVersion: 7, sceneRef: 'b'.repeat(64),
  });
  assert.equal(state.writes.length, 1);
  assert.equal(state.writes[0].byteLength, 8);
  assert.deepEqual(state.shares.map((share) => share.options.mimeType), [
    'application/pdf',
    'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  ]);
  assert.match(state.shares[0].options.dialogTitle ?? '', /Opportunity report\.pdf/);
  assert.match(state.shares[1].options.dialogTitle ?? '', /Board review\.pptx/);
});
