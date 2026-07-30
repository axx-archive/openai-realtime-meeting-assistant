import assert from 'node:assert/strict';
import test from 'node:test';
import {
  DEFAULT_MOBILE_THEME,
  parseMobileThemeRecord,
  resolveInstalledThemePreference,
  serializeMobileThemeRecord,
} from '../theme/appearancePreference';

test('a fresh native install starts in Dark', () => {
  assert.equal(DEFAULT_MOBILE_THEME, 'dark');
  assert.equal(resolveInstalledThemePreference(null, 'aj@shareability.com'), 'dark');
});

test('every explicit choice made on this installation is restored for the same account', () => {
  for (const preference of ['light', 'dark', 'system'] as const) {
    const stored = parseMobileThemeRecord(
      serializeMobileThemeRecord('AJ@Shareability.com', preference),
    );
    assert.equal(resolveInstalledThemePreference(stored, 'aj@shareability.com'), preference);
  }
});

test('another account restores its account-level choice instead of inheriting this device record', () => {
  const stored = parseMobileThemeRecord(
    serializeMobileThemeRecord('aj@shareability.com', 'light'),
  );
  assert.equal(resolveInstalledThemePreference(stored, 'tim@shareability.com', 'system'), 'system');
  assert.equal(resolveInstalledThemePreference(stored, 'tim@shareability.com', 'light'), 'light');
});

test('the matching installation choice wins over stale account state', () => {
  const stored = parseMobileThemeRecord(
    serializeMobileThemeRecord('aj@shareability.com', 'system'),
  );
  assert.equal(resolveInstalledThemePreference(stored, 'aj@shareability.com', 'light'), 'system');
});

test('an account with no valid explicit choice receives the Dark default', () => {
  assert.equal(resolveInstalledThemePreference(null, 'aj@shareability.com', ''), 'dark');
  assert.equal(resolveInstalledThemePreference(null, 'aj@shareability.com', 'sepia'), 'dark');
});

test('malformed local appearance data is rejected so callers can fall back to Dark', () => {
  assert.equal(parseMobileThemeRecord('{broken'), null);
  assert.equal(parseMobileThemeRecord('{"email":"aj@shareability.com","preference":"sepia"}'), null);
  assert.equal(
    resolveInstalledThemePreference(parseMobileThemeRecord('{broken'), 'aj@shareability.com'),
    'dark',
  );
});
