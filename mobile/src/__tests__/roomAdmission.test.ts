import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  confirmNativeRoomAccessGranted,
  nativeRoomParticipantHello,
} from '../realtime/roomAdmission';

describe('native room admission', () => {
  it('retries a requested transfer until access is granted, then disarms it', () => {
    const context = { passcode: '  4321  ', transferExisting: true };

    assert.deepEqual(nativeRoomParticipantHello('ios-device', context), {
      endpointId: 'ios-device',
      passcode: '4321',
      transferExisting: true,
    });
    assert.equal(nativeRoomParticipantHello('ios-device', context).transferExisting, true);

    confirmNativeRoomAccessGranted(context);
    assert.deepEqual(nativeRoomParticipantHello('ios-device', context), {
      endpointId: 'ios-device',
      passcode: '4321',
      transferExisting: false,
    });
  });
});
