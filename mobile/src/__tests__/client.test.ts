/**
 * Lightweight unit tests for the API client URL/header construction.
 * Run with: npx tsx --test src/__tests__/client.test.ts
 * (or after wiring jest). Pure logic tests that do not require a device.
 */

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { buildApiUrl, buildAuthHeaders } from '../api/requestHelpers';

describe('Bonfire mobile API helpers', () => {
  it('joins API paths against the production base', () => {
    assert.equal(buildApiUrl('https://thebonfire.xyz', '/rooms'), 'https://thebonfire.xyz/rooms');
    assert.equal(buildApiUrl('https://thebonfire.xyz/', 'auth/me'), 'https://thebonfire.xyz/auth/me');
  });

  it('always identifies as the expo native client', () => {
    const headers = buildAuthHeaders('expo', null);
    assert.equal(headers['X-Bonfire-Client'], 'expo');
    assert.equal(headers.Authorization, undefined);
  });

  it('attaches only the canonical bearer when a token is present', () => {
    const headers = buildAuthHeaders('expo', 'abc123');
    assert.equal(headers.Authorization, 'Bearer abc123');
    assert.equal(headers['X-Bonfire-Session'], undefined);
  });
});
