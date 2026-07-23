/**
 * Lightweight unit tests for the API client URL/header construction.
 * Run with: npx tsx --test src/__tests__/client.test.ts
 * (or after wiring jest). Pure logic tests that do not require a device.
 */

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

// Mirror path joining rules from client.ts without importing RN modules.
function buildUrl(base: string, path: string): string {
  const root = base.replace(/\/$/, '');
  return `${root}${path.startsWith('/') ? path : `/${path}`}`;
}

function authHeaders(sessionToken?: string | null): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'X-Bonfire-Client': 'expo',
  };
  if (sessionToken) {
    headers.Authorization = `Bearer ${sessionToken}`;
    headers['X-Bonfire-Session'] = sessionToken;
  }
  return headers;
}

describe('Bonfire mobile API helpers', () => {
  it('joins API paths against the production base', () => {
    assert.equal(buildUrl('https://thebonfire.xyz', '/rooms'), 'https://thebonfire.xyz/rooms');
    assert.equal(buildUrl('https://thebonfire.xyz/', 'auth/me'), 'https://thebonfire.xyz/auth/me');
  });

  it('always identifies as the expo native client', () => {
    const headers = authHeaders(null);
    assert.equal(headers['X-Bonfire-Client'], 'expo');
    assert.equal(headers.Authorization, undefined);
  });

  it('attaches bearer + session headers when a token is present', () => {
    const headers = authHeaders('abc123');
    assert.equal(headers.Authorization, 'Bearer abc123');
    assert.equal(headers['X-Bonfire-Session'], 'abc123');
  });
});
