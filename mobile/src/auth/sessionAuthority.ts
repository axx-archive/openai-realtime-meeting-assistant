export function identityUpdateIsAuthorized(
  currentSessionToken: string | null,
  expectedSessionToken: string,
  currentEmail: string | null | undefined,
  nextEmail: string | null | undefined,
): boolean {
  const currentIdentity = currentEmail?.trim().toLowerCase() ?? '';
  const nextIdentity = nextEmail?.trim().toLowerCase() ?? '';
  return Boolean(
    expectedSessionToken
    && currentSessionToken === expectedSessionToken
    && currentIdentity
    && currentIdentity === nextIdentity,
  );
}
