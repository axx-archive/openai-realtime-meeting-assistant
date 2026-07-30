export const MOBILE_THEME_PREFERENCES = ['system', 'light', 'dark'] as const;

export type MobileThemePreference = (typeof MOBILE_THEME_PREFERENCES)[number];

export const DEFAULT_MOBILE_THEME: MobileThemePreference = 'dark';

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
 * A native install starts in Dark. A matching installation choice wins first,
 * including an explicit choice to follow the device's System appearance. An
 * account-level choice then restores the user's preference across devices.
 */
export function resolveInstalledThemePreference(
  record: MobileThemeRecord | null,
  email: string,
  accountPreference?: unknown,
): MobileThemePreference {
  if (record?.email === email.trim().toLowerCase()) return record.preference;
  return isMobileThemePreference(accountPreference)
    ? accountPreference
    : DEFAULT_MOBILE_THEME;
}

export function serializeMobileThemeRecord(
  email: string,
  preference: MobileThemePreference,
): string {
  return JSON.stringify({ email: email.trim().toLowerCase(), preference });
}
