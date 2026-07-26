export type NativeRoomAdmissionContext = {
  passcode: string;
  transferExisting: boolean;
};

export function nativeRoomParticipantHello(
  endpointId: string,
  context: NativeRoomAdmissionContext,
): { endpointId: string; passcode?: string; transferExisting: boolean } {
  const passcode = context.passcode.trim();
  return {
    endpointId,
    ...(passcode ? { passcode } : {}),
    transferExisting: context.transferExisting,
  };
}

/** A failed pre-admission reconnect retries transfer; success disarms it. */
export function confirmNativeRoomAccessGranted(context: NativeRoomAdmissionContext): void {
  context.transferExisting = false;
}
