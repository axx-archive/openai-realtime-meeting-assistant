import { strideTokens, strideDark } from './generatedTokens';

// Calls remain on one fixed dark stage in either app appearance. Explicit dark
// status/action roles avoid adaptive light-theme foregrounds on this surface.
const stage = strideTokens.color.constant;
export const callColors = {
  canvas: stage.stage,
  surface: stage.stageChrome,
  control: strideDark.surface,
  inset: strideDark.surfaceInset,
  text: stage.stageText,
  textSecondary: stage.stageTextSecondary,
  border: strideDark.border,
  borderControl: stage.stageControlBorder,
  action: strideDark.action,
  actionSurface: strideDark.selection,
  selected: strideDark.action,
  onSelected: strideDark.onAction,
  onSelectedSecondary: strideDark.onAction,
  speaking: stage.speaking,
  success: strideDark.success,
  successSurface: strideDark.successSurface,
  warning: strideDark.warning,
  warningSurface: strideDark.warningSurface,
  danger: strideDark.danger,
  dangerSurface: strideDark.dangerSurface,
  leave: stage.leaveFill,
  onLeave: stage.onLeave,
  letterbox: stage.videoLetterbox,
} as const;
