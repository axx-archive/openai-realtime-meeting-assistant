export function loginCredentialError(name: string, password: string): string | null {
  if (!name.trim()) return 'Select your account.';
  if (!password) return 'Enter your password.';
  return null;
}
