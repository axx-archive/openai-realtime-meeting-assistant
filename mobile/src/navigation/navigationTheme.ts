import { strideLight, strideDark } from '../theme/generatedTokens';

/** Navigation expects resolved strings, while native view colors can remain DynamicColorIOS. */
export function navigationThemeColors(dark: boolean) {
  const palette = dark ? strideDark : strideLight;
  return {
    background: palette.canvas,
    card: palette.surface,
    text: palette.text,
    border: palette.border,
    primary: palette.action,
    notification: palette.action,
  };
}
