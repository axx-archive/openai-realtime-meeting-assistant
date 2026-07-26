import assert from 'node:assert/strict';
import test from 'node:test';
import { passkeyErrorMessage } from '../auth/passkeyError';

test('passkey errors stay calm and actionable', () => {
  assert.equal(passkeyErrorMessage(new Error('Passkey sign-in was cancelled.')), null);
  assert.equal(
    passkeyErrorMessage(new Error('FunctionCallException: Biometrics must be enabled at ReactNativePasskeysModule.swift:125')),
    'Turn on Face ID or Touch ID for this device, then try your passkey again.',
  );
  assert.equal(
    passkeyErrorMessage(new Error('Passkeys are not available on this device.')),
    'Passkeys are not available on this device yet. Use your name and password instead.',
  );
});
