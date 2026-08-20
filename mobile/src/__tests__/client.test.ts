/**
 * Lightweight unit tests for the API client URL/header construction.
 * Run with: npx tsx --test src/__tests__/client.test.ts
 * (or after wiring jest). Pure logic tests that do not require a device.
 */

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  ApiTransportError,
  apiErrorMessage,
  apiTransportError,
  buildApiUrl,
  buildAuthHeaders,
  buildIdempotencyHeaders,
  createRequestDeadline,
  parseApiResponseText,
} from '../api/requestHelpers';

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

  it('puts the opening-turn operation key in the canonical header', () => {
    assert.deepEqual(buildIdempotencyHeaders('  home-scout-123  '), {
      'Idempotency-Key': 'home-scout-123',
    });
    assert.deepEqual(buildIdempotencyHeaders('   '), {});
  });

  it('replaces native fetch internals with calm retry guidance', () => {
    const timeout = apiTransportError(true);
    assert.ok(timeout instanceof ApiTransportError);
    assert.equal(timeout.code, 'timeout');
    assert.equal(
      timeout.message,
      "Stride couldn't reach the office in time. Check your connection and try again.",
    );
    assert.doesNotMatch(timeout.message, /ExpoModulesCore|Promise\.swift|UnexpectedException/);

    const offline = apiTransportError(false);
    assert.equal(offline.code, 'network');
    assert.equal(
      offline.message,
      "Stride couldn't reach the office. Check your connection and try again.",
    );
  });

  it('never exposes an HTML or malformed response body as an auth error', () => {
    assert.throws(
      () => parseApiResponseText('<!doctype html><html><body>Not the office</body></html>'),
      (error: unknown) => {
        assert.ok(error instanceof ApiTransportError);
        assert.equal(error.code, 'unexpected_response');
        assert.equal(
          error.message,
          'Stride received an unexpected response. Check your connection and try again.',
        );
        assert.doesNotMatch(error.message, /doctype|html|Not the office/i);
        return true;
      },
    );
    assert.throws(
      () => parseApiResponseText('upstream sent something malformed'),
      ApiTransportError,
    );
  });

  it('keeps concise JSON API errors but rejects markup and oversized error fields', () => {
    assert.deepEqual(parseApiResponseText('{"error":"Invalid password."}'), {
      error: 'Invalid password.',
    });
    assert.equal(apiErrorMessage(401, { error: 'Invalid password.' }), 'Invalid password.');
    assert.equal(
      apiErrorMessage(502, { error: '<html><body>proxy failure</body></html>' }),
      "Stride couldn't complete that request (502). Try again.",
    );
    assert.equal(
      apiErrorMessage(500, { error: 'x'.repeat(301) }),
      "Stride couldn't complete that request (500). Try again.",
    );
  });

  it('aborts a stalled request at its app-owned deadline', async () => {
    const deadline = createRequestDeadline(undefined, 5);
    try {
      await new Promise<void>((resolve) => {
        deadline.signal.addEventListener('abort', () => resolve(), { once: true });
      });
      assert.equal(deadline.signal.aborted, true);
      assert.equal(deadline.didTimeout(), true);
    } finally {
      deadline.dispose();
    }
  });

  it('preserves caller cancellation as cancellation rather than a timeout', () => {
    const parent = new AbortController();
    const deadline = createRequestDeadline(parent.signal, 10_000);
    try {
      parent.abort();
      assert.equal(deadline.signal.aborted, true);
      assert.equal(deadline.didTimeout(), false);
    } finally {
      deadline.dispose();
    }
  });
});
