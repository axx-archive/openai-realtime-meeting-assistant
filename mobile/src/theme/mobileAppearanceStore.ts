import { File, Paths } from 'expo-file-system';
import {
  parseMobileThemeRecord,
  serializeMobileThemeRecord,
  type MobileThemePreference,
  type MobileThemeRecord,
} from './appearancePreference';

const preferenceFile = new File(Paths.document, 'bonfire-mobile-appearance-v1.json');

export async function readInstalledThemePreference(): Promise<MobileThemeRecord | null> {
  try {
    if (!preferenceFile.exists) return null;
    return parseMobileThemeRecord(await preferenceFile.text());
  } catch {
    return null;
  }
}

export async function writeInstalledThemePreference(
  email: string,
  preference: MobileThemePreference,
): Promise<void> {
  try {
    if (!preferenceFile.exists) preferenceFile.create({ intermediates: true });
    preferenceFile.write(serializeMobileThemeRecord(email, preference));
  } catch {
    // Appearance remains usable in memory if local persistence is unavailable.
  }
}
