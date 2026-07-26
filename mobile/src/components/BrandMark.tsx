import React from 'react';
import { StyleSheet, View, type ViewStyle } from 'react-native';
import Svg, { Defs, Mask, Path, Rect } from 'react-native-svg';
import { colors, radius, shadow } from '../theme/tokens';

const fullFlamePath = 'M553 92C531 173 464 225 425 296C394 352 394 410 370 458C376 413 350 374 307 339C320 400 285 447 263 506C226 604 273 670 391 710C431 724 472 731 513 735C559 731 603 719 642 700C720 662 765 604 755 528C746 468 710 427 680 380C633 306 529 253 553 92ZM538 435C528 485 492 527 463 566C436 603 434 637 454 665C468 687 489 699 513 706C540 687 558 660 561 628C565 591 548 558 526 529C544 505 550 470 538 435Z';
const fullRearLogPath = 'M233 693L814 818L828 839L816 898L794 912L213 787L199 766L211 707Z';
const fullFrontLogPath = 'M209 814L792 679L813 693L827 752L814 773L231 908L210 895L195 836Z';

const microFlamePath = 'M553 98C531 173 470 222 431 286C401 336 400 384 380 426C380 389 355 359 318 334C330 382 300 426 279 479C248 520 284 548 373 563C418 571 466 577 513 580C562 577 610 566 651 545C710 515 745 493 741 462C732 430 701 397 674 360C628 296 531 252 553 98ZM539 324C526 373 486 407 454 441C423 476 424 506 447 526C463 540 486 548 511 552C542 536 564 512 567 485C570 456 550 430 526 406C546 385 552 354 539 324Z';
const microRearLogPath = 'M233 692L810 859L821 879L811 913L791 924L214 757L203 737L213 703Z';
const microFrontLogPath = 'M214 859L791 692L811 703L821 737L810 757L233 924L213 913L203 879Z';

type Props = {
  size?: number;
  style?: ViewStyle;
};

/**
 * Canonical full Bonfire mark. The SVG is deliberately optically large inside
 * the tile; the separate micro form below is reserved for tab-size rendering.
 */
export function BrandMark({ size = 56, style }: Props) {
  const artwork = Math.round(size * 0.92);
  const r = size <= 44 ? radius.lg : 16;
  return (
    <View
      style={[
        styles.mark,
        shadow.mark,
        {
          width: size,
          height: size,
          borderRadius: r,
        },
        style,
      ]}
    >
      <Svg width={artwork} height={artwork} viewBox="0 0 1024 1024" fill="none">
        <Path
          fill={colors.onAccent}
          fillRule="evenodd"
          clipRule="evenodd"
          d={fullFlamePath}
        />
        <Path fill={colors.onAccent} d={fullRearLogPath} />
        <Path fill="none" stroke={colors.accent} strokeWidth={42} strokeLinejoin="bevel" d={fullFrontLogPath} />
        <Path fill={colors.onAccent} d={fullFrontLogPath} />
      </Svg>
    </View>
  );
}

type GlyphProps = {
  size?: number;
  color: string;
};

/** The critic-approved simplified Bonfire silhouette for 16–24px chrome. */
export function BonfireGlyph({ size = 22, color }: GlyphProps) {
  return (
    <Svg width={size} height={size} viewBox="0 0 1024 1024" fill="none">
      <Defs>
        <Mask id="bonfireMicroLogCutout" x={0} y={0} width={1024} height={1024} maskUnits="userSpaceOnUse">
          <Rect x={0} y={0} width={1024} height={1024} fill="#FFFFFF" />
          <Path fill="#FFFFFF" stroke="#000000" strokeWidth={160} strokeLinejoin="bevel" d={microFrontLogPath} />
        </Mask>
      </Defs>
      <Path fill={color} fillRule="evenodd" clipRule="evenodd" d={microFlamePath} />
      <Path fill={color} mask="url(#bonfireMicroLogCutout)" d={microRearLogPath} />
      <Path fill={color} d={microFrontLogPath} />
    </Svg>
  );
}

const styles = StyleSheet.create({
  mark: {
    backgroundColor: colors.accent,
    alignItems: 'center',
    justifyContent: 'center',
  },
});
