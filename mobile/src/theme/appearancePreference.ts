export const MOBILE_THEME_PREFERENCES = ['system', 'light', 'dark'] as const;

export type MobileThemePreference = (typeof MOBILE_THEME_PREFERENCES)[number];

export type MobileThemeRecord = {
  email: string;
  preference: MobileThemePreference;
};

export function isMobileThemePreference(value: unknown): value is MobileThemePreference {
  return typeof value === 'string' && MOBILE_THEME_PREFERENCES.includes(value as MobileThemePreference);
}

export function parseMobileThemeRecord(value: string | null): MobileThemeRecord | null {
  if (!value) return null;
  try {
    const parsed = JSON.parse(value) as Partial<MobileThemeRecord>;
    if (typeof parsed.email !== 'string' || !isMobileThemePreference(parsed.preference)) {
      return null;
    }
    return {
      email: parsed.email.trim().toLowerCase(),
      preference: parsed.preference,
    };
  } catch {
    return null;
  }
}

/**
 * A native install starts in System, even when an account still carries the
 * web app's legacy Light default. Once this installation has an explicit
 * choice for the signed-in account, that local choice wins on later launches.
 */
export function resolveInstalledThemePreference(
  record: MobileThemeRecord | null,
  email: string,
): MobileThemePreference {
  return record?.email === email.trim().toLowerCase() ? record.preference : 'system';
}

export function serializeMobileThemeRecord(
  email: string,
  preference: MobileThemePreference,
): string {
  return JSON.stringify({ email: email.trim().toLowerCase(), preference });
}
