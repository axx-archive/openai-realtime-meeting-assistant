import assert from 'node:assert/strict';
import test from 'node:test';
import {
  parseMobileThemeRecord,
  resolveInstalledThemePreference,
  serializeMobileThemeRecord,
} from '../theme/appearancePreference';

test('a fresh native install starts in System instead of inheriting legacy Light', () => {
  assert.equal(resolveInstalledThemePreference(null, 'aj@shareability.com'), 'system');
});

test('an explicit choice made on this installation is restored for the same account', () => {
  const stored = parseMobileThemeRecord(
    serializeMobileThemeRecord('AJ@Shareability.com', 'dark'),
  );
  assert.equal(resolveInstalledThemePreference(stored, 'aj@shareability.com'), 'dark');
});

test('another account does not inherit the previous account theme on the device', () => {
  const stored = parseMobileThemeRecord(
    serializeMobileThemeRecord('aj@shareability.com', 'light'),
  );
  assert.equal(resolveInstalledThemePreference(stored, 'tim@shareability.com'), 'system');
});

test('malformed local appearance data safely falls back to System', () => {
  assert.equal(parseMobileThemeRecord('{broken'), null);
  assert.equal(parseMobileThemeRecord('{"email":"aj@shareability.com","preference":"sepia"}'), null);
});
