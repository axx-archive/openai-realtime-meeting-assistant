export function passkeyErrorMessage(error: unknown): string | null {
  const message = error instanceof Error ? error.message : String(error || '');

  if (/cancel|user.*(denied|dismissed)|not.?handled/i.test(message)) {
    return null;
  }
  if (/biometric|face\s?id|touch\s?id/i.test(message)) {
    return 'Turn on Face ID or Touch ID for this device, then try your passkey again.';
  }
  if (/not (available|supported)|unsupported/i.test(message)) {
    return 'Passkeys are not available on this device yet. Use your name and password instead.';
  }
  if (/FunctionCallException|ExpoModulesCore|ReactNativePasskeysModule|\.swift:\d+/i.test(message)) {
    return 'Passkey sign-in could not start. Check this device’s security settings and try again.';
  }

  return message || 'Passkey sign-in failed. Try again or use your password.';
}
