import assert from 'node:assert/strict';
import test from 'node:test';
import {
  AVATAR_ENCODING_PASSES,
  MAX_AVATAR_DATA_URL_LENGTH,
  avatarDataURLFits,
  jpegAvatarDataURL,
} from '../profile/avatarEncoding';

test('avatar encoding always gets smaller and more compressed across fallbacks', () => {
  for (let index = 1; index < AVATAR_ENCODING_PASSES.length; index += 1) {
    assert.ok(AVATAR_ENCODING_PASSES[index].dimension < AVATAR_ENCODING_PASSES[index - 1].dimension);
    assert.ok(AVATAR_ENCODING_PASSES[index].compression < AVATAR_ENCODING_PASSES[index - 1].compression);
  }
});

test('avatar data URLs are normalized to an accepted JPEG media type', () => {
  assert.equal(jpegAvatarDataURL('aGVs\n bG8='), 'data:image/jpeg;base64,aGVsbG8=');
});

test('avatar payload guard stays below the server 192 KiB ceiling', () => {
  assert.ok(MAX_AVATAR_DATA_URL_LENGTH < 192 * 1024);
  assert.equal(avatarDataURLFits('x'.repeat(MAX_AVATAR_DATA_URL_LENGTH)), true);
  assert.equal(avatarDataURLFits('x'.repeat(MAX_AVATAR_DATA_URL_LENGTH + 1)), false);
});
