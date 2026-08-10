import assert from 'node:assert/strict';
import test from 'node:test';

import { loginCredentialError } from '../screens/loginValidation';

test('login validation identifies the exact missing credential', () => {
  assert.equal(loginCredentialError('', ''), 'Select your account.');
  assert.equal(loginCredentialError('   ', 'secret'), 'Select your account.');
  assert.equal(loginCredentialError('AJ', ''), 'Enter your password.');
  assert.equal(loginCredentialError(' AJ ', 'secret'), null);
});
