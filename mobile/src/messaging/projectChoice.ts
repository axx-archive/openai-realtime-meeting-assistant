export type OpaqueProjectChoice = {
  token?: string | null;
  choiceKey?: string | null;
};

/**
 * Rebinds a local Project selection only through the server's opaque identity.
 * A missing or changed key is authority loss, not permission to silently choose
 * the next suggestion.
 */
export function rebindOpaqueProjectChoice<T extends OpaqueProjectChoice>(
  current: T | null | undefined,
  suggested: T | null | undefined,
  choices: readonly T[] | null | undefined,
  explicitNone: boolean,
): T | null {
  if (explicitNone) return null;
  if (current) {
    const key = String(current.choiceKey ?? "").trim();
    if (!key) return null;
    const refreshed = [suggested, ...(choices ?? [])].find(
      (choice): choice is T =>
        Boolean(choice?.token) && String(choice?.choiceKey ?? "").trim() === key,
    );
    return refreshed ?? null;
  }
  return suggested?.token && String(suggested.choiceKey ?? "").trim()
    ? suggested
    : null;
}
